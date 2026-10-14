//go:build dbmod

package runtimeformat

import (
	"bytes"
	"cmd/objfile/sys"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/pkujhd/goloader/link"
	"github.com/pkujhd/goloader/obj"
)

func hostArch() *sys.Arch {
	for _, a := range sys.Archs {
		if a.Name == runtime.GOARCH {
			return a
		}
	}
	return nil
}

func sampleGraph() *link.Linker {
	literal := "literal\x00value"
	l := &link.Linker{LinkerData: link.LinkerData{Code: []byte{1, 2, 3, 4}, Data: []byte{8, 7, 6, 5}, NoPtrTypeData: []byte{4, 3}, NoPtrItabData: []byte{2, 1}, NoPtrData: []byte{0, 1}, Pclntable: []byte{0, 'f', 0}, Bss: make([]byte, 13), NoPtrBss: []byte{0, 3}}, Arch: hostArch(), ExtraData: 16, Filetab: []uint32{0, 2}, StringMap: map[string]*string{"literal": &literal}, CgoImportMap: map[string]*obj.CgoImport{"cgo": {GoSymName: "go", CSymName: "c", SoName: "lib"}}, UnImplementedTypes: map[string]map[string]int{"type": {"method": 1}}, Packages: map[string]*obj.Pkg{"package": {PkgPath: "package", ImportPkgs: []string{"dependency"}, File: "build-only"}}, SymMap: map[string]*obj.Sym{}}
	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("example.com/database/driver.(*Conn).method%04d", i)
		l.SymMap[name] = &obj.Sym{Name: name, Type: "type:*database.Connection", Kind: 1, Offset: i * 16, Func: &obj.Func{PCData: []uint32{1, 2, 0}, FuncData: []uintptr{3, 0, 7}}, Reloc: []obj.Reloc{{Offset: i*16 + 4, SymName: "example.com/database/driver.target", Size: 4, Type: 10, Add: -3, Instruction: obj.Instruction{Op: 11, OpName: "CMP", Start: 1, End: 5, Text: []byte{1, 2, 3, 4}, Args: []string{"x0", "x1"}}, Epilogue: obj.Epilogue{Offset: i*16 + 8, Size: 8}}, {Offset: -1, SymName: "external", Type: 2}}}
	}
	return l
}

func encoded(t *testing.T, l *link.Linker) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := Encode(l, &b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRoundTripAndDoesNotMutate(t *testing.T) {
	l := sampleGraph()
	want := RuntimeOnly(l)
	got, err := Decode(bytes.NewReader(encoded(t, l)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("roundtrip changed linker data")
	}
	if l.Packages["package"].File != "build-only" || l.SymMap == nil || l.Bss == nil {
		t.Fatal("encoding mutated input")
	}
}

func TestDeterministic(t *testing.T) {
	l := sampleGraph()
	for i := 0; i < 20; i++ {
		key := fmt.Sprint(i)
		value := key
		l.StringMap[key] = &value
		l.CgoImportMap[key] = &obj.CgoImport{GoSymName: key}
		l.Packages[key] = &obj.Pkg{PkgPath: key}
		l.UnImplementedTypes[key] = map[string]int{"a": 1, "b": 2, "c": 3}
	}

	want := encoded(t, l)
	for i := 0; i < 10; i++ {
		if !bytes.Equal(want, encoded(t, l)) {
			t.Fatal("same graph encoded differently")
		}
	}
}

func TestRawSizeRegression(t *testing.T) {
	l := sampleGraph()
	var old bytes.Buffer
	if err := legacyEncode(l, &old); err != nil {
		t.Fatal(err)
	}
	got := encoded(t, l)
	if len(got)*100 > old.Len()*55 {
		t.Fatalf("raw format is %d bytes, want <=55%% of legacy %d bytes", len(got), old.Len())
	}
	t.Logf("raw bytes: legacy=%d compact=%d", old.Len(), len(got))
}

func TestTruncationAndTrailingData(t *testing.T) {
	data := encoded(t, sampleGraph())
	for _, n := range []int{0, 1, 7, 8, 9, 10, len(data) / 2, len(data) - 1} {
		if _, err := Decode(bytes.NewReader(data[:n])); err == nil {
			t.Errorf("accepted truncation at %d", n)
		}
	}
	if _, err := Decode(bytes.NewReader(append(data, 0))); err == nil {
		t.Fatal("accepted trailing data")
	}
}

func TestInvalidGraph(t *testing.T) {
	if err := Encode(nil, io.Discard); err == nil {
		t.Fatal("accepted nil graph")
	}
	l := sampleGraph()
	l.SymMap["nil"] = nil
	if err := Encode(l, io.Discard); err == nil {
		t.Fatal("accepted nil symbol")
	}
}

// DBX_LEGACY_MODULES enables whole-driver roundtrips and raw size measurements
// against archived real compiler output; ordinary tests use a checked-in slice.
func TestRealModuleRoundTrip(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" || runtime.Version() != "go1.26.8" {
		t.Skip("archived compiler output targets darwin/arm64 Go 1.26.8")
	}

	root := os.Getenv("DBX_LEGACY_MODULES")
	paths := []string{filepath.Join("testdata", "postgres-v1.dbmod.gz")}
	if root != "" {
		paths = nil
		for _, engine := range []string{"postgres", "oracle", "dm", "sqlserver"} {
			paths = append(paths, filepath.Join(root, engine, "module.dbmod.gz"))
		}
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			z, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			raw, err := io.ReadAll(z)
			if err != nil {
				t.Fatal(err)
			}
			original, err := legacyDecode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			data := encoded(t, original)
			got, err := Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, got) {
				t.Fatal("real module graph changed")
			}
			if len(data)*100 > len(raw)*85 {
				t.Fatalf("raw size regression: compact=%d old=%d", len(data), len(raw))
			}
			if !bytes.Equal(data, encoded(t, got)) {
				t.Fatal("reencoded module differs")
			}
			t.Logf("%s raw bytes: legacy=%d compact=%d reduction=%.2f%%", path, len(raw), len(data), 100*(1-float64(len(data))/float64(len(raw))))
		})
	}
}

// Regenerate the small, real compiler-output fixture from an existing v1 module.
func TestExtractFixture(t *testing.T) {
	root := os.Getenv("DBX_EXTRACT_FIXTURE")
	if root == "" {
		t.Skip("fixture extraction disabled")
	}
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	l, err := legacyDecode(z)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(l.SymMap))
	for k, s := range l.SymMap {
		if s.Func != nil && len(s.Reloc) > 5 {
			keys = append(keys, k)
		}
	}
	// Sorting makes extraction reproducible across Go map iteration orders.
	sort.Strings(keys)
	keep := map[string]*obj.Sym{}
	for _, k := range keys[:8] {
		keep[k] = l.SymMap[k]
	}
	l.SymMap = keep
	l.Code = l.Code[:64]
	l.Data = l.Data[:32]
	l.NoPtrTypeData = l.NoPtrTypeData[:64]
	l.NoPtrItabData = l.NoPtrItabData[:32]
	l.NoPtrData = l.NoPtrData[:32]
	l.Pclntable = l.Pclntable[:128]
	l.Funcs = l.Funcs[:4]
	l.Filetab = l.Filetab[:8]
	l.StringMap = nil
	l.UnImplementedTypes = nil
	l.Packages = nil
	l.Bss = l.Bss[:8]
	l.NoPtrBss = l.NoPtrBss[:8]
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create("testdata/postgres-v1.dbmod.gz")
	if err != nil {
		t.Fatal(err)
	}
	g := gzip.NewWriter(out)
	if err := legacyEncode(l, g); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLongCommonPrefix(t *testing.T) {
	prefix := strings.Repeat("package/", 2048)
	l := &link.Linker{SymMap: map[string]*obj.Sym{prefix + "A": {Name: prefix + "A"}, prefix + "B": {Name: prefix + "B"}}}
	got, err := Decode(bytes.NewReader(encoded(t, l)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, l) {
		t.Fatal("long prefix changed graph")
	}
}

func TestTargetMismatch(t *testing.T) {
	var b bytes.Buffer
	if err := EncodeTarget(&link.Linker{}, &b, "different-os", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(&b); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("want platform mismatch, got %v", err)
	}
	b.Reset()
	if err := EncodeTarget(&link.Linker{Arch: sys.ArchARM64}, &b, runtime.GOOS, "amd64"); err == nil {
		t.Fatal("accepted incompatible architecture")
	}
}

// This minimal schema fixture lets rejection tests change exact fields without
// relying on offsets in a compiler-generated module.
func minimalFrame(change func(*encoder)) []byte {
	w := &encoder{}
	w.WriteString(magic)
	w.str(runtime.Version())
	w.str(runtime.GOOS)
	w.str(runtime.GOARCH)
	if change != nil {
		change(w)
		return w.Bytes()
	}
	w.u(1)
	w.u(0)
	w.str("") // dictionary containing only empty string
	w.u(0)    // plain code representation
	for i := 0; i < 5; i++ {
		w.blob(nil)
	}
	w.u(0) // raw PC table representation
	w.blob(nil)
	w.bss(nil)
	w.bss(nil)
	w.str("")
	w.i(0)
	w.boolean(false)
	for i := 0; i < 6; i++ {
		w.u(0)
	} // Filetab, Funcs, StringMap, CgoImportMap, interfaces, Packages
	w.u(0) // SymMap
	return w.Bytes()
}

func TestMalformedRecords(t *testing.T) {
	base := minimalFrame(nil)
	if _, err := Decode(bytes.NewReader(base)); err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"dictionary count":   minimalFrame(func(w *encoder) { w.u(maxCount + 1) }),
		"dictionary prefix":  minimalFrame(func(w *encoder) { w.u(1); w.u(1); w.str("") }),
		"dictionary order":   minimalFrame(func(w *encoder) { w.u(2); w.u(0); w.str("z"); w.u(0); w.str("a") }),
		"dictionary length":  minimalFrame(func(w *encoder) { w.u(1); w.u(0); w.u(maxNameBytes + 1) }),
		"overflowing varint": append(minimalFrame(func(w *encoder) {}), bytes.Repeat([]byte{255}, 11)...),
	}
	// Replace the final nil SymMap marker with a one-entry map.
	for name, key := range map[string]uint64{"invalid name index": 1, "huge name index": ^uint64(0)} {
		w := &encoder{}
		w.Write(base[:len(base)-1])
		w.u(2)
		w.u(key)
		w.u(0)
		w.i(1)
		w.i(0)
		w.u(0)
		tests[name] = w.Bytes()
	}
	for name, field := range map[string]uint64{"invalid flags": 8, "overflowing flags": ^uint64(0)} {
		w := &encoder{}
		w.Write(base[:len(base)-1])
		w.u(2)
		w.u(0)
		w.u(field)
		w.i(1)
		w.i(0)
		w.u(0)
		tests[name] = w.Bytes()
	}
	w := &encoder{}
	w.Write(base[:len(base)-1])
	w.u(2)
	w.u(0)
	w.u(0)
	w.i(1)
	w.i(maxModuleBytes + 1)
	w.u(0)
	tests["symbol offset bounds"] = w.Bytes()
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(bytes.NewReader(data)); err == nil {
				t.Fatal("accepted malformed module")
			}
		})
	}
}

func TestTruncationAtEveryByte(t *testing.T) {
	data := encoded(t, &link.Linker{SymMap: map[string]*obj.Sym{"f": {Name: "f", Func: &obj.Func{PCData: []uint32{4, 8}, FuncData: []uintptr{16}}, Reloc: []obj.Reloc{{Offset: 4, SymName: "target", Size: 4, Type: 1, Epilogue: obj.Epilogue{Offset: 8, Size: 16}}}}}})
	for n := range data {
		if _, err := Decode(bytes.NewReader(data[:n])); err == nil {
			t.Fatalf("accepted truncated byte %d/%d", n, len(data))
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(minimalFrame(nil))
	f.Add([]byte(magic))
	f.Fuzz(func(t *testing.T, data []byte) {
		l, err := Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		var b bytes.Buffer
		if err := Encode(l, &b); err != nil {
			t.Fatalf("decoded graph cannot encode: %v", err)
		}
		if _, err := Decode(&b); err != nil {
			t.Fatalf("reencoded graph cannot decode: %v", err)
		}
	})
}

func TestSparseNOPReservations(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("ARM64 code representation")
	}

	// ARM64 epilogues reserve repeated architectural NOP instructions. Saving a
	// run length must reconstruct every byte and leave relocation offsets intact.
	nop := []byte{0x1f, 0x20, 0x03, 0xd5}
	code := append([]byte{0, 1, 2, 3}, bytes.Repeat(nop, 512)...)
	code = append(code, 4, 5, 6, 7)
	l := &link.Linker{Arch: sys.ArchARM64, LinkerData: link.LinkerData{Code: code}, SymMap: map[string]*obj.Sym{"f": {Name: "f", Kind: 1, Reloc: []obj.Reloc{{Offset: 0, Size: 4, Type: 1, Epilogue: obj.Epilogue{Offset: 4, Size: 2048}}}}}}
	data := encoded(t, l)
	if len(data) > len(code)/4 {
		t.Fatalf("NOP reservations remain in raw format: %d bytes", len(data))
	}
	got, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l, got) {
		t.Fatal("sparse NOP encoding changed graph")
	}
	// A near-match cannot be reconstructed as an architectural NOP.
	l.Code[104] = 0xaa
	got, err = Decode(bytes.NewReader(encoded(t, l)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l, got) {
		t.Fatal("near-NOP bytes were changed")
	}
}

func TestInvalidEncoderBounds(t *testing.T) {
	cases := map[string]*link.Linker{
		"long name":              {SymMap: map[string]*obj.Sym{strings.Repeat("x", maxNameBytes+1): {}}},
		"instruction offset":     {SymMap: map[string]*obj.Sym{"x": {Reloc: []obj.Reloc{{Instruction: obj.Instruction{Start: maxModuleBytes + 1}}}}}},
		"negative epilogue size": {SymMap: map[string]*obj.Sym{"x": {Reloc: []obj.Reloc{{Epilogue: obj.Epilogue{Size: -1}}}}}},
	}
	for name, l := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Encode(l, io.Discard); err == nil {
				t.Fatal("accepted graph that cannot decode")
			}
		})
	}
}

func TestOffsetDeltaBounds(t *testing.T) {
	l := &link.Linker{SymMap: map[string]*obj.Sym{"x": {Name: "x", Offset: -1, Reloc: []obj.Reloc{{Offset: maxModuleBytes}}}}}
	got, err := Decode(bytes.NewReader(encoded(t, l)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l, got) {
		t.Fatal("offset delta corrupted graph")
	}
}

func TestMalformedSparseCode(t *testing.T) {
	cases := map[string][]uint64{
		"empty": {1, 0}, "huge": {1, maxModuleBytes + 1}, "overlong run": {1, 8, 0, 3}, "zero run": {1, 4, 0, 0}, "raw too long": {1, 4, 5}, "truncated": {1, 8},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			w := &encoder{}
			for _, v := range fields {
				w.u(v)
			}
			d := &decoder{data: w.Bytes(), budget: 1024}
			d.code("arm64")
			if d.err == nil {
				t.Fatal("accepted malformed sparse code")
			}
		})
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
func TestShortWrite(t *testing.T) {
	if err := Encode(&link.Linker{}, shortWriter{}); err != io.ErrShortWrite {
		t.Fatalf("got %v, want short write", err)
	}
}
