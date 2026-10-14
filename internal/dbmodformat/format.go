//go:build dbmod

// Package runtimeformat serializes completed goloader graphs. Modules contain
// trusted executable code and are bound to one Go toolchain and target platform.
package runtimeformat

import (
	"cmd/objfile/sys"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"sort"

	"github.com/pkujhd/goloader/link"
	"github.com/pkujhd/goloader/obj"
)

const (
	magic           = "DBXMOD2\n"
	maxModuleBytes  = 128 << 20
	maxDecodedBytes = 256 << 20
	maxCount        = 1 << 20
	maxNameBytes    = 1 << 20
)

// RuntimeOnly removes construction indexes after ReadDependPackages completes.
// The returned graph shares immutable runtime data with l.
func RuntimeOnly(l *link.Linker) *link.Linker {
	if l == nil {
		return nil
	}
	c := *l
	c.ObjSymbolMap = nil
	c.NameMap = nil
	c.CgoFuncs = nil
	c.CUOffset = 0
	if l.Packages != nil {
		c.Packages = make(map[string]*obj.Pkg, len(l.Packages))
		for k, p := range l.Packages {
			if p != nil {
				c.Packages[k] = &obj.Pkg{PkgPath: p.PkgPath, ImportPkgs: append([]string(nil), p.ImportPkgs...)}
			}
		}
	}
	return &c
}

// Encode writes the uncompressed format for the running host platform.
func Encode(l *link.Linker, out io.Writer) error {
	return EncodeTarget(l, out, runtime.GOOS, runtime.GOARCH)
}

// EncodeTarget supports an offline builder targeting another OS/architecture.
// The compiler and host must still use the same exact Go version.
func EncodeTarget(l *link.Linker, out io.Writer, goos, goarch string) error {
	if l == nil {
		return fmt.Errorf("nil module graph")
	}
	if goos == "" || goarch == "" {
		return fmt.Errorf("missing target platform")
	}
	if l.Arch != nil && l.Arch.Name != goarch {
		return fmt.Errorf("module architecture mismatch")
	}
	if err := validateGraph(l); err != nil {
		return err
	}
	l = RuntimeOnly(l)
	names := collectNames(l)
	if len(names) > maxCount {
		return fmt.Errorf("symbol dictionary exceeds limit")
	}
	for _, name := range names {
		if len(name) > maxNameBytes {
			return fmt.Errorf("symbol name exceeds limit")
		}
	}
	ids := make(map[string]uint64, len(names))
	for i, n := range names {
		ids[n] = uint64(i)
	}
	w := &encoder{}
	w.WriteString(magic)
	w.str(runtime.Version())
	w.str(goos)
	w.str(goarch)
	w.u(uint64(len(names)))
	prev := ""
	for _, n := range names {
		prefix := 0
		for prefix < len(prev) && prefix < len(n) && prev[prefix] == n[prefix] {
			prefix++
		}
		w.u(uint64(prefix))
		w.str(n[prefix:])
		prev = n
	}
	w.code(l.Code, goarch)
	for _, b := range [][]byte{l.Data, l.NoPtrTypeData, l.NoPtrItabData, l.NoPtrData} {
		w.blob(b)
	}
	w.pclntable(l, ids)
	w.bss(l.Bss)
	w.bss(l.NoPtrBss)
	archName := ""
	if l.Arch != nil {
		archName = l.Arch.Name
	}
	w.str(archName)
	w.i(l.ExtraData)
	w.boolean(l.AdaptedOffset)
	w.count(len(l.Filetab), l.Filetab == nil)
	for _, v := range l.Filetab {
		w.u(uint64(v))
	}
	// _func is a loader-private type. Its exported integer fields are encoded in
	// declaration order; the exact Go version in the header pins this schema.
	w.funcs(reflect.ValueOf(l.Funcs))
	w.count(len(l.StringMap), l.StringMap == nil)
	for _, k := range sortedKeys(l.StringMap) {
		w.u(ids[k])
		v := l.StringMap[k]
		w.boolean(v != nil)
		if v != nil {
			w.str(*v)
		}
	}
	w.count(len(l.CgoImportMap), l.CgoImportMap == nil)
	for _, k := range sortedKeys(l.CgoImportMap) {
		w.u(ids[k])
		v := l.CgoImportMap[k]
		w.u(ids[v.GoSymName])
		w.u(ids[v.CSymName])
		w.u(ids[v.SoName])
	}
	w.count(len(l.UnImplementedTypes), l.UnImplementedTypes == nil)
	for _, k := range sortedKeys(l.UnImplementedTypes) {
		w.u(ids[k])
		m := l.UnImplementedTypes[k]
		w.count(len(m), m == nil)
		for _, n := range sortedKeys(m) {
			w.u(ids[n])
			w.i(m[n])
		}
	}
	w.count(len(l.Packages), l.Packages == nil)
	for _, k := range sortedKeys(l.Packages) {
		w.u(ids[k])
		p := l.Packages[k]
		w.u(ids[p.PkgPath])
		w.count(len(p.ImportPkgs), p.ImportPkgs == nil)
		for _, n := range p.ImportPkgs {
			w.u(ids[n])
		}
	}
	w.count(len(l.SymMap), l.SymMap == nil)
	for _, k := range sortedKeys(l.SymMap) {
		s := l.SymMap[k]
		w.u(ids[k])
		var flags uint64
		if s.Name != k {
			flags |= 1
		}
		if s.Type != "" {
			flags |= 2
		}
		if s.Func != nil {
			flags |= 4
		}
		w.u(flags)
		if flags&1 != 0 {
			w.u(ids[s.Name])
		}
		if flags&2 != 0 {
			w.u(ids[s.Type])
		}
		w.i(s.Kind)
		w.i(s.Offset)
		if s.Func != nil {
			w.count(len(s.Func.PCData), s.Func.PCData == nil)
			for _, v := range s.Func.PCData {
				w.u(uint64(v))
			}
			w.count(len(s.Func.FuncData), s.Func.FuncData == nil)
			for _, v := range s.Func.FuncData {
				w.u(uint64(v))
			}
		}
		w.count(len(s.Reloc), s.Reloc == nil)
		previous := s.Offset
		for _, r := range s.Reloc {
			w.i(r.Offset - previous)
			previous = r.Offset
			w.u(ids[r.SymName])
			w.i(r.Size)
			w.i(r.Type)
			var flags uint64
			if r.Add != 0 {
				flags |= 1
			}
			if r.Op != 0 {
				flags |= 2
			}
			if r.Start != 0 || r.End != 0 || r.Text != nil || r.Args != nil {
				flags |= 4
			}
			if r.Epilogue.Offset != 0 || r.Epilogue.Size != 0 {
				flags |= 8
			}
			// OpName is retained for the small runtime loader, which excludes
			// the build-time x86 instruction decoder.
			if r.OpName != "" {
				flags |= 16
			}
			w.u(flags)
			if flags&1 != 0 {
				w.i(r.Add)
			}
			if flags&2 != 0 {
				w.u(uint64(r.Op))
			}
			if flags&16 != 0 {
				w.u(ids[r.OpName])
			}
			if flags&4 != 0 {
				w.i(r.Start)
				w.i(r.End)
				w.blob(r.Text)
				w.count(len(r.Args), r.Args == nil)
				for _, a := range r.Args {
					w.u(ids[a])
				}
			}
			if flags&8 != 0 {
				w.i(r.Epilogue.Offset)
				w.i(r.Epilogue.Size)
			}
		}
	}
	if w.Len() > maxModuleBytes {
		return fmt.Errorf("module exceeds format size limit")
	}
	n, err := out.Write(w.Bytes())
	if err == nil && n != w.Len() {
		err = io.ErrShortWrite
	}
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func collectNames(l *link.Linker) []string {
	set := map[string]bool{"": true}
	add := func(s string) { set[s] = true }
	for k, s := range l.SymMap {
		add(k)
		add(s.Name)
		add(s.Type)
		for _, r := range s.Reloc {
			add(r.SymName)
			add(r.OpName)
			for _, a := range r.Args {
				add(a)
			}
		}
	}
	for k := range l.StringMap {
		add(k)
	}
	for k, v := range l.CgoImportMap {
		add(k)
		add(v.GoSymName)
		add(v.CSymName)
		add(v.SoName)
	}
	for k, m := range l.UnImplementedTypes {
		add(k)
		for n := range m {
			add(n)
		}
	}
	for k, p := range l.Packages {
		add(k)
		add(p.PkgPath)
		for _, n := range p.ImportPkgs {
			add(n)
		}
	}
	return sortedKeys(set)
}

// Decode validates framing, integer bounds, dictionary references and aggregate
// allocation limits before reconstructing the graph. It never executes code.
func Decode(in io.Reader) (*link.Linker, error) {
	data, err := io.ReadAll(io.LimitReader(in, maxModuleBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxModuleBytes {
		return nil, fmt.Errorf("module exceeds format size limit")
	}
	d := &decoder{data: data, budget: maxDecodedBytes}
	if string(d.take(len(magic))) != magic {
		return nil, fmt.Errorf("unsupported module magic")
	}
	version, goos, goarch := d.str(maxNameBytes), d.str(64), d.str(64)
	if d.err != nil {
		return nil, d.err
	}
	if version != runtime.Version() || goos != runtime.GOOS || goarch != runtime.GOARCH {
		return nil, fmt.Errorf("module toolchain/platform mismatch")
	}
	n := d.length(maxCount, 16)
	d.names = make([]string, n)
	prev := ""
	for i := range d.names {
		prefix := int(d.bounded(uint64(len(prev))))
		suffix := d.str(maxNameBytes - prefix)
		if !d.charge(prefix + len(suffix)) {
			return nil, d.err
		}
		name := prev[:prefix] + suffix
		if i > 0 && name <= prev {
			d.fail("unsorted symbol dictionary")
		}
		d.names[i] = name
		prev = name
	}
	l := &link.Linker{}
	l.Code = d.code(goarch)
	l.Data = d.blob()
	l.NoPtrTypeData = d.blob()
	l.NoPtrItabData = d.blob()
	l.NoPtrData = d.blob()
	l.Pclntable = d.pclntable()
	l.Bss = d.bss()
	l.NoPtrBss = d.bss()
	archName := d.str(64)
	if archName != "" {
		for _, a := range sys.Archs {
			if a.Name == archName {
				c := *a
				l.Arch = &c
				break
			}
		}
		if l.Arch == nil || archName != goarch {
			d.fail("invalid module architecture")
		}
	}
	l.ExtraData = d.nonnegative(maxModuleBytes)
	l.AdaptedOffset = d.boolean()
	n, isNil := d.count(maxCount, 4)
	if !isNil {
		l.Filetab = make([]uint32, n)
	}
	for i := range l.Filetab {
		l.Filetab[i] = uint32(d.bounded(1<<32 - 1))
	}
	d.funcs(reflect.ValueOf(&l.Funcs).Elem())
	n, isNil = d.count(maxCount, 48)
	if !isNil {
		l.StringMap = make(map[string]*string, n)
	}
	for i := 0; i < n; i++ {
		k := d.name()
		if _, ok := l.StringMap[k]; ok {
			d.fail("duplicate string key")
		}
		var v *string
		if d.boolean() {
			s := d.str(maxModuleBytes)
			v = &s
		}
		l.StringMap[k] = v
	}
	n, isNil = d.count(maxCount, 96)
	if !isNil {
		l.CgoImportMap = make(map[string]*obj.CgoImport, n)
	}
	for i := 0; i < n; i++ {
		k := d.name()
		if _, ok := l.CgoImportMap[k]; ok {
			d.fail("duplicate cgo key")
		}
		l.CgoImportMap[k] = &obj.CgoImport{GoSymName: d.name(), CSymName: d.name(), SoName: d.name()}
	}
	n, isNil = d.count(maxCount, 48)
	if !isNil {
		l.UnImplementedTypes = make(map[string]map[string]int, n)
	}
	for i := 0; i < n; i++ {
		k := d.name()
		if _, ok := l.UnImplementedTypes[k]; ok {
			d.fail("duplicate type key")
		}
		nn, nul := d.count(maxCount, 48)
		var m map[string]int
		if !nul {
			m = make(map[string]int, nn)
		}
		for j := 0; j < nn; j++ {
			k := d.name()
			if _, ok := m[k]; ok {
				d.fail("duplicate interface key")
			}
			m[k] = d.i()
		}
		l.UnImplementedTypes[k] = m
	}
	n, isNil = d.count(maxCount, 256)
	if !isNil {
		l.Packages = make(map[string]*obj.Pkg, n)
	}
	for i := 0; i < n; i++ {
		k := d.name()
		if _, ok := l.Packages[k]; ok {
			d.fail("duplicate package key")
		}
		p := &obj.Pkg{PkgPath: d.name()}
		nn, nul := d.count(maxCount, 16)
		if !nul {
			p.ImportPkgs = make([]string, nn)
		}
		for j := range p.ImportPkgs {
			p.ImportPkgs[j] = d.name()
		}
		l.Packages[k] = p
	}
	n, isNil = d.count(maxCount, 224)
	if !isNil {
		l.SymMap = make(map[string]*obj.Sym, n)
	}
	for i := 0; i < n; i++ {
		k := d.name()
		if _, ok := l.SymMap[k]; ok {
			d.fail("duplicate symbol key")
		}
		flags := d.bounded(7)
		s := &obj.Sym{Name: k}
		if flags&1 != 0 {
			s.Name = d.name()
		}
		if flags&2 != 0 {
			s.Type = d.name()
		}
		s.Kind = d.i()
		s.Offset = d.offset()
		if flags&4 != 0 {
			s.Func = &obj.Func{}
			nn, nul := d.count(maxCount, 4)
			if !nul {
				s.Func.PCData = make([]uint32, nn)
			}
			for j := range s.Func.PCData {
				s.Func.PCData[j] = uint32(d.bounded(1<<32 - 1))
			}
			nn, nul = d.count(maxCount, 8)
			if !nul {
				s.Func.FuncData = make([]uintptr, nn)
			}
			for j := range s.Func.FuncData {
				s.Func.FuncData[j] = uintptr(d.bounded(uint64(^uintptr(0))))
			}
		}
		nn, nul := d.count(maxCount, 160)
		if !nul {
			s.Reloc = make([]obj.Reloc, nn)
		}
		previous := s.Offset
		for j := range s.Reloc {
			r := &s.Reloc[j]
			delta := d.i()
			if delta < -2*maxModuleBytes || delta > 2*maxModuleBytes {
				d.fail("relocation delta out of bounds")
				delta = 0
			}
			r.Offset = previous + delta
			if r.Offset < -maxModuleBytes || r.Offset > maxModuleBytes {
				d.fail("relocation offset out of bounds")
			}
			previous = r.Offset
			r.SymName = d.name()
			r.Size = d.nonnegative(64)
			r.Type = d.i()
			flags := d.bounded(31)
			if flags&1 != 0 {
				r.Add = d.i()
			}
			if flags&2 != 0 {
				r.Op = uint(d.bounded(uint64(^uint(0))))
			}
			if flags&16 != 0 {
				r.OpName = d.name()
			}
			if flags&4 != 0 {
				r.Start = d.offset()
				r.End = d.offset()
				r.Text = d.blob()
				nn, nul := d.count(maxCount, 16)
				if !nul {
					r.Args = make([]string, nn)
				}
				for j := range r.Args {
					r.Args[j] = d.name()
				}
			}
			if flags&8 != 0 {
				r.Epilogue.Offset = d.offset()
				r.Epilogue.Size = d.nonnegative(maxModuleBytes)
			}
		}
		l.SymMap[k] = s
	}
	if d.err != nil {
		return nil, d.err
	}
	if len(d.data) != 0 {
		return nil, fmt.Errorf("trailing module data")
	}
	return l, nil
}

func validateGraph(l *link.Linker) error {
	for _, b := range [][]byte{l.Code, l.Data, l.NoPtrTypeData, l.NoPtrItabData, l.NoPtrData, l.Pclntable, l.Bss, l.NoPtrBss} {
		if len(b) > maxModuleBytes {
			return fmt.Errorf("module section exceeds limit")
		}
	}
	for _, n := range []int{len(l.SymMap), len(l.StringMap), len(l.CgoImportMap), len(l.UnImplementedTypes), len(l.Filetab), len(l.Funcs), len(l.Packages)} {
		if n > maxCount {
			return fmt.Errorf("module collection exceeds limit")
		}
	}
	for _, v := range l.StringMap {
		if v != nil && len(*v) > maxModuleBytes {
			return fmt.Errorf("string value exceeds limit")
		}
	}
	for _, v := range l.UnImplementedTypes {
		if len(v) > maxCount {
			return fmt.Errorf("interface collection exceeds limit")
		}
	}

	for k, s := range l.SymMap {
		if s == nil {
			return fmt.Errorf("nil symbol %q", k)
		}
		if s.Offset < -maxModuleBytes || s.Offset > maxModuleBytes {
			return fmt.Errorf("symbol offset out of bounds")
		}
		if len(s.Reloc) > maxCount {
			return fmt.Errorf("relocation count exceeds limit")
		}
		if s.Func != nil && (len(s.Func.PCData) > maxCount || len(s.Func.FuncData) > maxCount) {
			return fmt.Errorf("function data count exceeds limit")
		}
		for _, r := range s.Reloc {
			if r.Start < -maxModuleBytes || r.Start > maxModuleBytes || r.End < -maxModuleBytes || r.End > maxModuleBytes || r.Epilogue.Offset < -maxModuleBytes || r.Epilogue.Offset > maxModuleBytes || r.Epilogue.Size < 0 || r.Epilogue.Size > maxModuleBytes || len(r.Text) > maxModuleBytes || len(r.Args) > maxCount {
				return fmt.Errorf("relocation metadata bounds invalid")
			}
			if r.Offset < -maxModuleBytes || r.Offset > maxModuleBytes || r.Size < 0 || r.Size > 64 {
				return fmt.Errorf("relocation bounds invalid")
			}
		}
	}
	for _, f := range l.Funcs {
		if f == nil {
			return fmt.Errorf("nil function metadata")
		}
	}
	for _, p := range l.Packages {
		if p == nil {
			return fmt.Errorf("nil package")
		}
		if len(p.ImportPkgs) > maxCount {
			return fmt.Errorf("package imports exceed limit")
		}
	}
	for _, c := range l.CgoImportMap {
		if c == nil {
			return fmt.Errorf("nil cgo import")
		}
	}
	if l.ExtraData < 0 || l.ExtraData > maxModuleBytes {
		return fmt.Errorf("extra data out of bounds")
	}
	if l.Arch != nil {
		found := false
		for _, a := range sys.Archs {
			if reflect.DeepEqual(a, l.Arch) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported architecture metadata")
		}
	}
	return nil
}
