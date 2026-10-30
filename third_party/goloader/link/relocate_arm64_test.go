package link

import (
	"bytes"
	"cmd/objfile/sys"
	"encoding/binary"
	"testing"

	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
)

func TestWeakARM64CallReservesTrampoline(t *testing.T) {
	l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: make([]byte, 4)}}
	object := &obj.ObjSymbol{Reloc: []obj.Reloc{{Type: reloctype.R_CALLARM64 | reloctype.R_WEAK}}}
	expandFunc(l, object, &obj.Sym{Offset: 0})
	if object.Reloc[0].Epilogue.Size != maxExtraCodeSize_CALLARM64 || len(l.Code) != 4+maxExtraCodeSize_CALLARM64 {
		t.Fatalf("weak call lacks reserved trampoline: %+v, code bytes=%d", object.Reloc[0].Epilogue, len(l.Code))
	}
}

func TestFarWeakARM64CallUsesARM64Instructions(t *testing.T) {
	l := &Linker{Arch: sys.ArchARM64}
	const target = 0x200000000
	code := make([]byte, 32)
	binary.LittleEndian.PutUint32(code, 0x94000000) // BL
	copy(code[24:], bytes.Repeat([]byte{0xa5}, 8))
	segment := &segment{codeSeg: codeSeg{codeByte: code, codeBase: 0x100000000}}
	reloc := obj.Reloc{Type: reloctype.R_CALLARM64 | reloctype.R_WEAK, Offset: 0, Size: 4, Epilogue: obj.Epilogue{Offset: 8, Size: 16}}
	l.relocateCALLARM(target, reloc, segment)
	if !bytes.Equal(code[8:16], arm64ReplaceCALLCode) {
		t.Fatalf("far weak ARM64 call emitted non-ARM64 instructions: %x", code[8:16])
	}
	if binary.LittleEndian.Uint64(code[16:24]) != target || !bytes.Equal(code[24:], bytes.Repeat([]byte{0xa5}, 8)) {
		t.Fatalf("trampoline target or adjacent code corrupted: %x", code)
	}
}
