//go:build dbmod

package runtimeformat

import (
	"bytes"
	"fmt"
	"github.com/pkujhd/goloader/link"
	"github.com/pkujhd/goloader/obj"
	"reflect"
	"testing"
)

func functionOffsets(l *link.Linker, offsets ...int) {
	dst := reflect.ValueOf(&l.Funcs).Elem()
	dst.Set(reflect.MakeSlice(dst.Type(), len(offsets), len(offsets)))
	for i, off := range offsets {
		p := reflect.New(dst.Type().Elem().Elem())
		p.Elem().FieldByName("Nameoff").SetInt(int64(off))
		dst.Index(i).Set(p)
	}
}

func TestPclntableNameReferences(t *testing.T) {
	l := &link.Linker{SymMap: map[string]*obj.Sym{}}
	l.Pclntable = []byte{0, 1, 2, 0}
	offsets := []int{}
	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("example.com/database/driver/internal/protocol.(*Connection).veryLongMethod%04d", i)
		l.SymMap[name] = &obj.Sym{Name: name}
		offsets = append(offsets, len(l.Pclntable))
		l.Pclntable = append(l.Pclntable, []byte(name)...)
		l.Pclntable = append(l.Pclntable, 0, 5, 1, 0, 2, 6)
	}
	functionOffsets(l, offsets...)
	data := encoded(t, l)
	if len(data) > len(l.Pclntable)/2 {
		t.Fatalf("repeated function names remain: file=%d PC table=%d", len(data), len(l.Pclntable))
	}
	got, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l, got) {
		t.Fatal("function name references changed runtime graph")
	}
}

func TestPclntableRawFallback(t *testing.T) {
	for _, test := range []struct {
		name    string
		data    []byte
		offsets []int
		names   []string
	}{
		{"nil", nil, nil, nil}, {"empty", []byte{}, nil, nil}, {"no functions", []byte("hello\x00"), nil, []string{"hello"}},
		{"bad offset", []byte("hello\x00"), []int{99}, []string{"hello"}},
		{"missing dictionary", []byte("hello\x00"), []int{0}, nil},
		{"no terminator", []byte("hello"), []int{0}, []string{"hello"}},
		{"overlapping names", []byte("abcdefgh\x00"), []int{0, 3}, []string{"abcdefgh", "defgh"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := &link.Linker{LinkerData: link.LinkerData{Pclntable: test.data}}
			functionOffsets(l, test.offsets...)
			ids := map[string]uint64{}
			for i, n := range test.names {
				ids[n] = uint64(i)
			}
			w := &encoder{}
			w.pclntable(l, ids)
			if w.Bytes()[0] != 0 {
				t.Fatal("expected raw fallback")
			}
			d := &decoder{data: w.Bytes(), names: test.names, budget: 1024}
			got := d.pclntable()
			if d.err != nil {
				t.Fatal(d.err)
			}
			if !reflect.DeepEqual(got, test.data) {
				t.Fatal("raw PC data changed")
			}
		})
	}
}

func TestPclntableRejectsMalformedReferences(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields []uint64
		names  []string
	}{
		{"unknown mode", []uint64{2}, nil},
		{"empty length", []uint64{1, 0, 1, 0, 0}, []string{"abc"}},
		{"empty patch list", []uint64{1, 8, 0}, nil},
		{"length bound", []uint64{1, maxModuleBytes + 1, 1, 0, 0}, []string{"abc"}},
		{"count bound", []uint64{1, 8, maxCount + 1}, nil},
		{"invalid dictionary ID", []uint64{1, 8, 1, 0, 2}, []string{"abc"}},
		{"name past end", []uint64{1, 3, 1, 0, 0}, []string{"abc"}},
		{"second name past end", []uint64{1, 4, 2, 0, 0, 0, 0}, []string{"abc"}},
		{"overlarge span", []uint64{1, 8, 1, 9, 0}, []string{"abc"}},
		{"truncated span", []uint64{1, 8, 1, 4, 0}, []string{"abc"}},
		{"embedded NUL", []uint64{1, 8, 1, 0, 0}, []string{"a\x00b"}},
		{"unterminated tail", []uint64{1, 8, 1, 0, 0}, []string{"abc"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := &encoder{}
			for _, v := range test.fields {
				w.u(v)
			}
			d := &decoder{data: w.Bytes(), names: test.names, budget: 1024}
			d.pclntable()
			if d.err == nil {
				t.Fatal("accepted malformed PC reference")
			}
		})
	}
}

func TestPclntableUnsortedDuplicateOffsets(t *testing.T) {
	a := "first.package.with.a.long.name.functionA"
	b := "second.package.with.a.long.name.functionB"
	l := &link.Linker{LinkerData: link.LinkerData{Pclntable: []byte(a + "\x00" + b + "\x00")}}
	functionOffsets(l, len(a)+1, 0, len(a)+1)
	ids := map[string]uint64{a: 0, b: 1}
	w := &encoder{}
	w.pclntable(l, ids)
	if w.Bytes()[0] != 1 {
		t.Fatal("expected sparse references")
	}
	d := &decoder{data: w.Bytes(), names: []string{a, b}, budget: 1024}
	got := d.pclntable()
	if d.err != nil {
		t.Fatal(d.err)
	}
	if !bytes.Equal(got, l.Pclntable) {
		t.Fatal("unsorted duplicate offsets changed bytes")
	}
}

func FuzzPclntableReferences(f *testing.F) {
	f.Add([]byte{1, 4, 1, 0, 0})
	f.Add([]byte{0, 5, 'a', 'b', 'c', 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		d := &decoder{data: data, names: []string{"abc", "long.package.(*Type).Function", "a\x00b"}, budget: 1 << 20}
		got := d.pclntable()
		if d.err == nil && len(got) > 1<<20 {
			t.Fatal("PC table exceeds allocation budget")
		}
	})
}
