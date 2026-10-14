//go:build dbmod

package runtimeformat

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
)

type encoder struct{ bytes.Buffer }

func (w *encoder) u(v uint64)    { var b [10]byte; n := binary.PutUvarint(b[:], v); w.Write(b[:n]) }
func (w *encoder) i(v int)       { w.u(uint64(int64(v)<<1) ^ uint64(int64(v)>>63)) }
func (w *encoder) str(s string)  { w.u(uint64(len(s))); w.WriteString(s) }
func (w *encoder) blob(b []byte) { w.count(len(b), b == nil); w.Write(b) }
func (w *encoder) count(n int, nilValue bool) {
	if nilValue {
		w.u(0)
	} else {
		w.u(uint64(n) + 1)
	}
}
func (w *encoder) boolean(b bool) {
	if b {
		w.u(1)
	} else {
		w.u(0)
	}
}
func (w *encoder) bss(b []byte) {
	w.count(len(b), b == nil)
	zeros := true
	for _, x := range b {
		if x != 0 {
			zeros = false
			break
		}
	}
	w.boolean(zeros)
	if !zeros {
		w.Write(b)
	}
}
func (w *encoder) funcs(v reflect.Value) {
	w.count(v.Len(), v.IsNil())
	for i := 0; i < v.Len(); i++ {
		v := v.Index(i).Elem()
		for j := 0; j < v.NumField(); j++ {
			f := v.Field(j)
			switch f.Kind() {
			case reflect.Int32:
				w.i(int(f.Int()))
			case reflect.Uint8, reflect.Uint32:
				w.u(f.Uint())
			}
		}
	}
}

type decoder struct {
	data   []byte
	names  []string
	budget int
	err    error
}

func (d *decoder) fail(s string) {
	if d.err == nil {
		d.err = fmt.Errorf("invalid module: %s", s)
	}
}
func (d *decoder) charge(n int) bool {
	if d.err != nil {
		return false
	}
	if n < 0 || n > d.budget {
		d.fail("allocation limit exceeded")
		return false
	}
	d.budget -= n
	return true
}
func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.data) {
		d.fail("truncated data")
		return nil
	}
	b := d.data[:n]
	d.data = d.data[n:]
	return b
}
func (d *decoder) u() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.data)
	if n <= 0 {
		d.fail("truncated or overflowing varint")
		return 0
	}
	d.data = d.data[n:]
	return v
}
func (d *decoder) bounded(max uint64) uint64 {
	v := d.u()
	if v > max {
		d.fail("integer out of bounds")
		return 0
	}
	return v
}
func (d *decoder) i() int {
	u := d.u()
	v := int64(u>>1) ^ -int64(u&1)
	if int64(int(v)) != v {
		d.fail("integer overflows int")
		return 0
	}
	return int(v)
}
func (d *decoder) nonnegative(max int) int {
	v := d.i()
	if v < 0 || v > max {
		d.fail("integer out of bounds")
		return 0
	}
	return v
}
func (d *decoder) offset() int {
	v := d.i()
	if v < -maxModuleBytes || v > maxModuleBytes {
		d.fail("offset out of bounds")
		return 0
	}
	return v
}
func (d *decoder) length(max, unit int) int {
	n := int(d.bounded(uint64(max)))
	if n > len(d.data) {
		d.fail("length exceeds input")
		return 0
	}
	if !d.charge(n * unit) {
		return 0
	}
	return n
}
func (d *decoder) count(max, unit int) (int, bool) {
	v := d.bounded(uint64(max) + 1)
	if v == 0 || d.err != nil {
		return 0, true
	}
	n := int(v) - 1
	if n > len(d.data) {
		d.fail("count exceeds input")
		return 0, true
	}
	if !d.charge(n * unit) {
		return 0, true
	}
	return n, false
}
func (d *decoder) str(max int) string { n := d.length(max, 1); return string(d.take(n)) }
func (d *decoder) blob() []byte {
	n, nul := d.count(maxModuleBytes, 1)
	if nul {
		return nil
	}
	b := make([]byte, n)
	copy(b, d.take(n))
	return b
}
func (d *decoder) bss() []byte {
	v := d.bounded(maxModuleBytes + 1)
	nul := v == 0
	n := 0
	if !nul {
		n = int(v) - 1
	}
	zero := d.boolean()
	if !d.charge(n) {
		return nil
	}
	if nul {
		return nil
	}
	b := make([]byte, n)
	if !zero {
		copy(b, d.take(n))
	}
	return b
}
func (d *decoder) boolean() bool { return d.bounded(1) == 1 }
func (d *decoder) name() string {
	id := d.u()
	if id >= uint64(len(d.names)) {
		d.fail("symbol name index out of bounds")
		return ""
	}
	return d.names[id]
}
func (d *decoder) funcs(dst reflect.Value) {
	n, nul := d.count(maxCount, 56)
	if nul {
		return
	}
	dst.Set(reflect.MakeSlice(dst.Type(), n, n))
	for i := 0; i < n; i++ {
		p := reflect.New(dst.Type().Elem().Elem())
		dst.Index(i).Set(p)
		v := p.Elem()
		for j := 0; j < v.NumField(); j++ {
			f := v.Field(j)
			switch f.Kind() {
			case reflect.Int32:
				x := int64(d.i())
				if f.OverflowInt(x) {
					d.fail("function integer overflow")
				} else {
					f.SetInt(x)
				}
			case reflect.Uint8, reflect.Uint32:
				x := d.u()
				if f.OverflowUint(x) {
					d.fail("function integer overflow")
				} else {
					f.SetUint(x)
				}
			}
		}
	}
}
