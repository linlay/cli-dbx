//go:build dbmod

// Package runtimeformat is an experimental serialization format for completed
// goloader graphs. It does not fix relocation/runtime compatibility problems.
package runtimeformat

import (
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"runtime"
	"sort"

	"github.com/pkujhd/goloader/link"
	"github.com/pkujhd/goloader/obj"
)

// RuntimeOnly removes construction indexes after ReadDependPackages is complete.
// Packages retain exactly the package path and import edges used by doInitialize.
// No later dependency augmentation is supported for this closed runtime graph.
func legacyRuntimeOnly(l *link.Linker) *link.Linker {
	c := *l
	c.ObjSymbolMap = nil
	c.NameMap = nil
	c.CgoFuncs = nil
	c.CUOffset = 0
	c.Packages = make(map[string]*obj.Pkg, len(l.Packages))
	for k, p := range l.Packages {
		c.Packages[k] = &obj.Pkg{PkgPath: p.PkgPath, ImportPkgs: append([]string(nil), p.ImportPkgs...)}
	}
	return &c
}

type legacyWireReloc struct {
	Offset                       int
	Name                         uint32
	Size, Type, Add              int
	Op                           uint
	Start, End                   int
	Text                         []byte
	Args                         []uint32
	EpilogueOffset, EpilogueSize int
}
type legacyWireSym struct {
	Key, Name, Type uint32
	Kind, Offset    int
	Func            *obj.Func
	Reloc           []legacyWireReloc
}
type legacyWire struct {
	Version                   uint32
	GoVersion, GOOS, GOARCH   string
	Base                      *link.Linker
	Names                     []string
	Symbols                   []legacyWireSym
	BssLength, NoPtrBssLength int
}

func legacyOnlyZeros(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Encode keeps machine code, data, GC/PC metadata, relocations and init edges.
// Repeated symbol names are replaced by IDs; zero BSS becomes a length record.
func legacyEncode(l *link.Linker, out io.Writer) error {
	gob.Register(binary.LittleEndian)
	gob.Register(binary.BigEndian)
	base := legacyRuntimeOnly(l)
	w := legacyWire{Version: 1, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Base: base}
	w.BssLength = len(base.Bss)
	w.NoPtrBssLength = len(base.NoPtrBss)
	if legacyOnlyZeros(base.Bss) {
		base.Bss = nil
	}
	if legacyOnlyZeros(base.NoPtrBss) {
		base.NoPtrBss = nil
	}
	ids := map[string]uint32{}
	intern := func(s string) uint32 {
		if id, ok := ids[s]; ok {
			return id
		}
		id := uint32(len(w.Names))
		ids[s] = id
		w.Names = append(w.Names, s)
		return id
	}
	keys := make([]string, 0, len(base.SymMap))
	for k := range base.SymMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := base.SymMap[k]
		ws := legacyWireSym{Key: intern(k), Name: intern(s.Name), Type: intern(s.Type), Kind: s.Kind, Offset: s.Offset, Func: s.Func}
		if s.Reloc != nil {
			ws.Reloc = make([]legacyWireReloc, len(s.Reloc))
		}
		for i, r := range s.Reloc {
			wr := legacyWireReloc{Offset: r.Offset, Name: intern(r.SymName), Size: r.Size, Type: r.Type, Add: r.Add, Op: r.Op, Start: r.Start, End: r.End, Text: r.Text, EpilogueOffset: r.Epilogue.Offset, EpilogueSize: r.Epilogue.Size}
			if r.Args != nil {
				wr.Args = make([]uint32, len(r.Args))
				for j, a := range r.Args {
					wr.Args[j] = intern(a)
				}
			}
			ws.Reloc[i] = wr
		}
		w.Symbols = append(w.Symbols, ws)
	}
	base.SymMap = nil
	if _, err := io.WriteString(out, "DBXMOD1\n"); err != nil {
		return err
	}
	return gob.NewEncoder(out).Encode(&w)
}
func legacyDecode(in io.Reader) (*link.Linker, error) {
	gob.Register(binary.LittleEndian)
	gob.Register(binary.BigEndian)
	header := make([]byte, 8)
	if _, err := io.ReadFull(in, header); err != nil {
		return nil, err
	}
	if string(header) != "DBXMOD1\n" {
		return nil, fmt.Errorf("unsupported module magic")
	}
	var w legacyWire
	if err := gob.NewDecoder(in).Decode(&w); err != nil {
		return nil, err
	}
	if w.Version != 1 || w.Base == nil {
		return nil, fmt.Errorf("unsupported module schema")
	}
	if w.GoVersion != runtime.Version() || w.GOOS != runtime.GOOS || w.GOARCH != runtime.GOARCH {
		return nil, fmt.Errorf("module toolchain/platform mismatch")
	}
	l := w.Base
	if w.BssLength < 0 || w.NoPtrBssLength < 0 {
		return nil, fmt.Errorf("invalid BSS length")
	}
	if l.Bss == nil && w.BssLength > 0 {
		l.Bss = make([]byte, w.BssLength)
	} else if len(l.Bss) != w.BssLength {
		return nil, fmt.Errorf("invalid BSS data")
	}
	if l.NoPtrBss == nil && w.NoPtrBssLength > 0 {
		l.NoPtrBss = make([]byte, w.NoPtrBssLength)
	} else if len(l.NoPtrBss) != w.NoPtrBssLength {
		return nil, fmt.Errorf("invalid noptr BSS data")
	}
	l.SymMap = make(map[string]*obj.Sym, len(w.Symbols))
	lookup := func(id uint32) (string, error) {
		if uint64(id) >= uint64(len(w.Names)) {
			return "", fmt.Errorf("invalid symbol name id")
		}
		return w.Names[id], nil
	}
	for _, ws := range w.Symbols {
		key, err := lookup(ws.Key)
		if err != nil {
			return nil, err
		}
		name, err := lookup(ws.Name)
		if err != nil {
			return nil, err
		}
		typ, err := lookup(ws.Type)
		if err != nil {
			return nil, err
		}
		s := &obj.Sym{Name: name, Type: typ, Kind: ws.Kind, Offset: ws.Offset, Func: ws.Func}
		if ws.Reloc != nil {
			s.Reloc = make([]obj.Reloc, len(ws.Reloc))
		}
		for i, wr := range ws.Reloc {
			target, err := lookup(wr.Name)
			if err != nil {
				return nil, err
			}
			r := obj.Reloc{Offset: wr.Offset, SymName: target, Size: wr.Size, Type: wr.Type, Add: wr.Add, Instruction: obj.Instruction{Op: wr.Op, Start: wr.Start, End: wr.End, Text: wr.Text}, Epilogue: obj.Epilogue{Offset: wr.EpilogueOffset, Size: wr.EpilogueSize}}
			if wr.Args != nil {
				r.Args = make([]string, len(wr.Args))
				for j, id := range wr.Args {
					a, err := lookup(id)
					if err != nil {
						return nil, err
					}
					r.Args[j] = a
				}
			}
			s.Reloc[i] = r
		}
		l.SymMap[key] = s
	}
	return l, nil
}
