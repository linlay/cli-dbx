package link

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/pkujhd/goloader/obj"
)

// These are assembler encodings of RIP-relative byte, word, dword and qword
// comparisons. A bool next to nonzero data must remain a byte access.
func TestFarComparePreservesOperandWidths(t *testing.T) {
	cases := []struct {
		name         string
		text         []byte
		displacement int
		args         []string
		rewritten    []byte
	}{
		{"bool-byte", []byte{0x80, 0x3d, 0, 0, 0, 0, 0}, 2, nil, []byte{0x41, 0x80, 0x3b, 0}},
		{"word", []byte{0x66, 0x83, 0x3d, 0, 0, 0, 0, 1}, 3, nil, []byte{0x66, 0x41, 0x83, 0x3b, 1}},
		{"dword", []byte{0x83, 0x3d, 0, 0, 0, 0, 1}, 2, nil, []byte{0x41, 0x83, 0x3b, 1}},
		{"qword", []byte{0x48, 0x83, 0x3d, 0, 0, 0, 0, 1}, 3, nil, []byte{0x49, 0x83, 0x3b, 1}},
		{"register-first", []byte{0x48, 0x3b, 0x05, 0, 0, 0, 0}, 3, []string{"RAX"}, []byte{0x49, 0x3b, 0x03}},
		{"memory-first", []byte{0x48, 0x39, 0x05, 0, 0, 0, 0}, 3, []string{"RAX"}, []byte{0x49, 0x39, 0x03}},
		{"r11-preserved", []byte{0x4c, 0x3b, 0x1d, 0, 0, 0, 0}, 3, []string{"R11"}, []byte{0x4d, 0x3b, 0x1a}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := obj.Reloc{Offset: 1 + tc.displacement, Size: 4, Instruction: obj.Instruction{Start: 1, Text: tc.text, Args: tc.args}}
			const address = 0x123456789abcdef0
			code, err := farCompareCode(loc, address, binary.LittleEndian)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(code[12:len(code)-7], tc.rewritten) {
				t.Fatalf("comparison changed: %x", code)
			}
			if binary.LittleEndian.Uint64(code[4:12]) != address {
				t.Fatal("target address truncated")
			}
			if len(code) > maxExtraCodeSize_PCRELxCMPL {
				t.Fatal("trampoline exceeds reserved epilogue")
			}
			scratch := byte(3)
			if tc.name == "r11-preserved" {
				scratch = 2
			}
			if !bytes.Equal(code[:4], []byte{0x41, 0x50 | scratch, 0x49, 0xb8 | scratch}) || !bytes.Equal(code[len(code)-7:len(code)-4], []byte{0x41, 0x58 | scratch, 0xe9}) {
				t.Fatal("scratch register not preserved")
			}
		})
	}
}

func TestFarCompareRejectsUnsafeOperands(t *testing.T) {
	for _, register := range []string{"RSP", "ESP", "SP", "AH", "BH", "CH", "DH"} {
		loc := obj.Reloc{Offset: 3, Size: 4, Instruction: obj.Instruction{Start: 1, Text: []byte{0x80, 0x3d, 0, 0, 0, 0, 0}, Args: []string{register}}}
		if _, err := farCompareCode(loc, 1, binary.LittleEndian); err == nil {
			t.Fatalf("accepted %s", register)
		}
	}
	loc := obj.Reloc{Offset: 3, Size: 4, Instruction: obj.Instruction{Start: 1, Text: []byte{0x80, 0x38, 0, 0, 0, 0, 0}}}
	if _, err := farCompareCode(loc, 1, binary.LittleEndian); err == nil {
		t.Fatal("accepted non-RIP addressing")
	}
}
