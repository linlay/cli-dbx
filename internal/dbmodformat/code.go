//go:build dbmod

package runtimeformat

// ARM64 linkers reserve NOP-filled space for relocation epilogues. Store aligned
// runs as counts and reconstruct them at decode time. This changes disk bytes
// only: executable bytes, offsets and mapped code length stay exactly the same.
// Scanning bytes also retains savings when prelink removes relocation records.
var arm64NOP = [4]byte{0x1f, 0x20, 0x03, 0xd5}

type nopRun struct{ start, end int }

func (w *encoder) code(code []byte, arch string) {
	var runs []nopRun
	if arch == "arm64" {
		for i := 0; i+4 <= len(code); {
			start := i
			for i+4 <= len(code) && code[i] == arm64NOP[0] && code[i+1] == arm64NOP[1] && code[i+2] == arm64NOP[2] && code[i+3] == arm64NOP[3] {
				i += 4
			}
			if i-start >= 16 {
				runs = append(runs, nopRun{start, i})
			}
			if i == start {
				i += 4
			}
		}
	}
	if len(runs) == 0 {
		w.u(0)
		w.blob(code)
		return
	}
	w.u(1)
	w.u(uint64(len(code)))
	previous := 0
	for _, r := range runs {
		w.u(uint64(r.start - previous))
		w.Write(code[previous:r.start])
		w.u(uint64((r.end - r.start) / 4))
		previous = r.end
	}
	if previous < len(code) {
		w.u(uint64(len(code) - previous))
		w.Write(code[previous:])
		w.u(0)
	}
}

func (d *decoder) code(arch string) []byte {
	sparse := d.bounded(1) == 1
	if !sparse {
		return d.blob()
	}
	if arch != "arm64" {
		d.fail("unsupported sparse code architecture")
		return nil
	}
	n := int(d.bounded(maxModuleBytes))
	if n == 0 {
		d.fail("empty sparse code")
		return nil
	}
	if !d.charge(n) {
		return nil
	}
	code := make([]byte, n)
	for pos := 0; pos < n && d.err == nil; {
		raw := d.length(n-pos, 0)
		copy(code[pos:], d.take(raw))
		pos += raw
		words := int(d.bounded(uint64((n - pos) / 4)))
		if words > 0 && pos%4 != 0 {
			d.fail("unaligned sparse NOP run")
			return nil
		}
		if words == 0 && pos < n {
			d.fail("empty sparse code run")
			return nil
		}
		for end := pos + words*4; pos < end; pos += 4 {
			copy(code[pos:pos+4], arm64NOP[:])
		}
	}
	return code
}
