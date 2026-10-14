//go:build dbmod

package runtimeformat

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strings"

	"github.com/pkujhd/goloader/link"
)

type namePatch struct {
	offset, end int
	id          uint64
}

// pclntable stores verified function names as dictionary references. Restoration
// preserves every byte and offset, including interleaved PC and inline tables.
func (w *encoder) pclntable(l *link.Linker, ids map[string]uint64) {
	data := l.Pclntable
	patches := make([]namePatch, 0, len(l.Funcs))
	seen := make(map[int]bool)
	for _, f := range l.Funcs {
		off := int(f.Nameoff)
		if off < 0 || off >= len(data) || seen[off] {
			continue
		}
		seen[off] = true
		end := bytes.IndexByte(data[off:], 0)
		if end <= 0 {
			continue
		}
		id, ok := ids[string(data[off:off+end])]
		if ok {
			patches = append(patches, namePatch{off, off + end + 1, id})
		}
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].offset < patches[j].offset })
	sparse := &encoder{}
	sparse.u(1)
	sparse.u(uint64(len(data)))
	sparse.u(uint64(len(patches)))
	previous := 0
	for _, p := range patches {
		if p.offset < previous {
			w.u(0)
			w.blob(data)
			return
		} // Overlap: retain the raw bytes.
		sparse.u(uint64(p.offset - previous))
		sparse.Write(data[previous:p.offset])
		sparse.u(p.id)
		previous = p.end
	}
	sparse.Write(data[previous:])
	var scratch [10]byte
	rawSize := 1 + binary.PutUvarint(scratch[:], uint64(len(data))+1) + len(data)
	if len(patches) == 0 || sparse.Len() >= rawSize {
		w.u(0)
		w.blob(data)
		return
	}
	w.Write(sparse.Bytes())
}

func (d *decoder) pclntable() []byte {
	if d.bounded(1) == 0 {
		return d.blob()
	}
	n := int(d.bounded(maxModuleBytes))
	count := d.length(maxCount, 0)
	if n == 0 || count == 0 {
		d.fail("empty PC name references")
		return nil
	}
	if !d.charge(n) {
		return nil
	}
	data := make([]byte, n)
	pos := 0
	for i := 0; i < count && d.err == nil; i++ {
		span := d.length(n-pos, 0)
		copy(data[pos:], d.take(span))
		pos += span
		name := d.name()
		if len(name) == 0 || strings.IndexByte(name, 0) >= 0 || len(name) >= n-pos {
			d.fail("PC name reference exceeds table bounds")
			return nil
		}
		copy(data[pos:], name)
		pos += len(name)
		data[pos] = 0
		pos++
	}
	copy(data[pos:], d.take(n-pos))
	return data
}
