package link

import (
	"bytes"
	"cmd/objfile/sys"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/pkujhd/goloader/constants"
	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
	"github.com/pkujhd/goloader/objabi/symkind"
)

func TestPrelinkRelativeInstructions(t *testing.T) {
	tests := []struct {
		name                     string
		arch                     *sys.Arch
		typ, size, from, to, add int
		instruction              []byte
	}{
		{"arm64 call forward", sys.ArchARM64, reloctype.R_CALLARM64, 4, 0x1004, 0x2010, 4, []byte{0, 0, 0, 0x94}},
		{"arm64 call backward", sys.ArchARM64, reloctype.R_CALLARM64, 4, 0x2010, 0x1004, 0, []byte{0, 0, 0, 0x94}},
		{"arm64 weak call", sys.ArchARM64, reloctype.R_CALLARM64 | reloctype.R_WEAK, 4, 0x1004, 0x2010, 0, []byte{0, 0, 0, 0x94}},
		{"arm64 address forward", sys.ArchARM64, reloctype.R_ADDRARM64, 8, 0x1004, 0x2010, 4, []byte{0, 0, 0, 0x90, 0, 0, 0, 0x91}},
		{"arm64 address backward", sys.ArchARM64, reloctype.R_ADDRARM64, 8, 0x2010, 0x1004, 0, []byte{0, 0, 0, 0x90, 0, 0, 0, 0x91}},
		{"amd64 call", sys.ArchAMD64, reloctype.R_CALL, 4, 0x1004, 0x2010, 0, []byte{0, 0, 0, 0}},
		{"amd64 weak call", sys.ArchAMD64, reloctype.R_CALL | reloctype.R_WEAK, 4, 0x1004, 0x2010, 0, []byte{0, 0, 0, 0}},
		{"amd64 lea", sys.ArchAMD64, reloctype.R_PCREL, 4, 0x2010, 0x1004, 3, []byte{0, 0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Linker{Arch: tt.arch, LinkerData: LinkerData{Code: make([]byte, 0x3000), Pclntable: []byte{1, 2, 3}}, SymMap: make(map[string]*obj.Sym)}
			copy(l.Code[tt.from:], tt.instruction)
			if tt.arch == sys.ArchAMD64 {
				l.Code[tt.from-1] = 0xe8 // CALL rel32
				if tt.typ == reloctype.R_PCREL {
					copy(l.Code[tt.from-3:], []byte{0x48, 0x8d, 0x05}) // LEA RIP+rel32, RAX
				}
			}
			reloc := obj.Reloc{Offset: tt.from, Size: tt.size, Type: tt.typ, SymName: "target", Add: tt.add, Epilogue: obj.Epilogue{Offset: 0x2800, Size: 16}}
			l.SymMap["source"] = &obj.Sym{Name: "source", Offset: tt.from, Kind: symkind.STEXT, Reloc: []obj.Reloc{reloc}}
			l.SymMap["target"] = &obj.Sym{Name: "target", Offset: tt.to, Kind: symkind.STEXT}
			original := bytes.Clone(l.Code)
			stats, err := Prelink(l)
			if err != nil {
				t.Fatal(err)
			}
			if stats.ResolvedRelocations != 1 || stats.RelocationsBefore != 1 || stats.RelocationsAfter != 0 || len(l.SymMap["source"].Reloc) != 0 {
				t.Fatalf("not resolved: %+v", stats)
			}
			if tt.typ&reloctype.R_WEAK != 0 && stats.ResolvedByType[reloctype.RelocTypeString(tt.typ&^reloctype.R_WEAK)+"|R_WEAK"] != 1 {
				t.Fatalf("unreadable weak relocation statistics: %+v", stats.ResolvedByType)
			}
			if stats.RetainedResolvedReserveBytes != 16 || stats.ReclaimedCodeBytes != 0 || len(l.Code) != len(original) {
				t.Fatalf("changed reserved code: %+v", stats)
			}
			if !bytes.Equal(l.Pclntable, []byte{1, 2, 3}) {
				t.Fatal("changed PC metadata")
			}
			for _, base := range []int{0x10000000, 0x700000000} {
				cm := &CodeModule{segment: segment{codeSeg: codeSeg{codeByte: bytes.Clone(original), codeBase: base}, dataSeg: dataSeg{dataByte: make([]byte, 32)}}}
				plain := &Linker{Arch: tt.arch, SymMap: map[string]*obj.Sym{"source": {Name: "source", Kind: symkind.STEXT, Reloc: []obj.Reloc{reloc}}}}
				if err := plain.relocate(cm, map[string]uintptr{"target": uintptr(base + tt.to)}, nil); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(l.Code, cm.codeByte) {
					t.Fatalf("differs from runtime relocation at base %#x: got %x want %x", base, l.Code[tt.from:tt.from+tt.size], cm.codeByte[tt.from:tt.from+tt.size])
				}
			}
			second, err := Prelink(l)
			if err != nil || second.ResolvedRelocations != 0 {
				t.Fatalf("not idempotent: %+v, %v", second, err)
			}
		})
	}
}

func TestPrelinkRetainsOutOfRangeBranches(t *testing.T) {
	for _, offset := range []int{-1<<27 - 4, 1 << 27} {
		l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: []byte{0, 0, 0, 0x94}}, SymMap: map[string]*obj.Sym{
			"source": {Name: "source", Kind: symkind.STEXT, Reloc: []obj.Reloc{{Size: 4, Type: reloctype.R_CALLARM64, SymName: "target", Add: offset}}},
			"target": {Name: "target", Kind: symkind.STEXT},
		}}
		stats, err := Prelink(l)
		if err != nil || stats.ResolvedRelocations != 0 || len(l.SymMap["source"].Reloc) != 1 {
			t.Fatalf("resolved far branch %d: %+v, %v", offset, stats, err)
		}
	}
}

func TestPrelinkRetainsHugeAddends(t *testing.T) {
	l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: make([]byte, 32)}, SymMap: map[string]*obj.Sym{
		"source": {Name: "source", Kind: symkind.STEXT, Reloc: []obj.Reloc{{Size: 4, Type: reloctype.R_ADDROFF, SymName: "target", Add: int(^uint(0) >> 1)}}},
		"target": {Name: "target", Kind: symkind.STEXT, Offset: int(^uint(0) >> 1)},
	}}
	stats, err := Prelink(l)
	if err != nil || stats.ResolvedRelocations != 0 {
		t.Fatalf("resolved overflowing addend: %+v, %v", stats, err)
	}
}

func TestPrelinkErrorDoesNotApplyEarlierPatch(t *testing.T) {
	l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: make([]byte, 16)}, SymMap: map[string]*obj.Sym{
		"source": {Name: "source", Kind: symkind.STEXT, Reloc: []obj.Reloc{
			{Offset: 0, Size: 4, Type: reloctype.R_CALLARM64, SymName: "target"},
			{Offset: 4, Size: 4, Type: reloctype.R_CALLARM64, SymName: "target"},
		}},
		"target": {Name: "target", Kind: symkind.STEXT, Offset: 12},
	}}
	binary.LittleEndian.PutUint32(l.Code, 0x94000000)
	original := bytes.Clone(l.Code)
	if _, err := Prelink(l); err == nil {
		t.Fatal("accepted invalid second instruction")
	}
	if !bytes.Equal(original, l.Code) || len(l.SymMap["source"].Reloc) != 2 {
		t.Fatal("partially applied failed operation")
	}
}

func TestPrelinkSectionOffsets(t *testing.T) {
	for _, typ := range []int{reloctype.R_ADDROFF, reloctype.R_WEAKADDROFF, reloctype.R_METHODOFF} {
		for _, target := range []struct {
			name               string
			kind, offset, want int
		}{
			{"function", symkind.STEXT, 32, 35},
			{"type:sample", symkind.SRODATA, 8, 11},
			{"go:itab.T,I", symkind.SRODATA, 8, 43},
			{"data", symkind.SDATA, 8, 11},
			{"constant", symkind.SRODATA, 8, 67},
		} {
			t.Run(reloctype.RelocTypeString(typ)+"/"+target.name, func(t *testing.T) {
				l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: make([]byte, 64), Data: make([]byte, 16), NoPtrTypeData: make([]byte, 32), NoPtrItabData: make([]byte, 24), NoPtrData: make([]byte, 24)}, SymMap: map[string]*obj.Sym{
					"owner":     {Name: "owner", Kind: symkind.SRODATA, Offset: 0, Reloc: []obj.Reloc{{Offset: 0, Size: 4, Type: typ, SymName: target.name, Add: 3}}},
					target.name: {Name: target.name, Kind: target.kind, Offset: target.offset},
				}}
				stats, err := Prelink(l)
				if err != nil {
					t.Fatal(err)
				}
				if stats.ResolvedRelocations != 1 || binary.LittleEndian.Uint32(l.NoPtrData) != uint32(target.want) {
					t.Fatalf("stats %+v, offset %d want %d", stats, binary.LittleEndian.Uint32(l.NoPtrData), target.want)
				}
				if l.SymMap[target.name].Offset != target.offset || l.AdaptedOffset {
					t.Fatal("changed symbol layout")
				}
			})
		}
	}
}

func TestPrelinkOffsetsMatchRuntimeHostReuse(t *testing.T) {
	l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: make([]byte, 64), Data: make([]byte, 32), NoPtrTypeData: make([]byte, 40), NoPtrItabData: make([]byte, 48), NoPtrData: make([]byte, 56)}, SymMap: make(map[string]*obj.Sym)}
	targets := []struct {
		name string
		kind int
	}{
		{"function", symkind.STEXT}, {"type:T", symkind.SRODATA}, {"go:itab.T,I", symkind.SRODATA}, {"data", symkind.SDATA}, {"constant", symkind.SRODATA},
	}
	symbolMap := make(map[string]uintptr)
	for _, target := range targets {
		l.SymMap[target.name] = &obj.Sym{Name: target.name, Kind: target.kind, Offset: 0}
		// Off-type relocations must still use the local section offset even
		// when addSymbolMap has chosen a canonical host type/data address.
		symbolMap[target.name] = 0x700000000
	}
	for _, owner := range targets {
		for i, target := range targets {
			l.SymMap[owner.name].Reloc = append(l.SymMap[owner.name].Reloc, obj.Reloc{Offset: i * 4, Size: 4, SymName: target.name, Type: reloctype.R_ADDROFF, Add: 4})
		}
	}
	plain := *l
	plain.SymMap = make(map[string]*obj.Sym)
	for name, s := range l.SymMap {
		copySym := *s
		copySym.Reloc = append([]obj.Reloc(nil), s.Reloc...)
		plain.SymMap[name] = &copySym
	}
	joinData := func() []byte {
		var data []byte
		for _, part := range [][]byte{l.Data, l.NoPtrTypeData, l.NoPtrItabData, l.NoPtrData} {
			data = append(data, part...)
		}
		return data
	}
	cm := &CodeModule{segment: segment{
		codeSeg: codeSeg{codeBase: 0x10000000, codeByte: bytes.Clone(l.Code)},
		dataSeg: dataSeg{dataBase: 0x20000000, dataByte: joinData(), dataLen: len(l.Data), noPtrTypeDataLen: len(l.NoPtrTypeData), noPtrItabDataLen: len(l.NoPtrItabData), noPtrDataLen: len(l.NoPtrData)},
	}}
	stats, err := Prelink(l)
	if err != nil || stats.ResolvedRelocations != 25 {
		t.Fatalf("prelink: %+v, %v", stats, err)
	}
	plain.adaptSymbolOffset(cm)
	if err := plain.relocate(cm, symbolMap, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l.Code, cm.codeByte) || !bytes.Equal(joinData(), cm.dataByte) {
		t.Fatal("offline offsets differ from adapted runtime relocation with host reuse")
	}
}

func TestPrelinkKeepsRuntimeConsumersAndDynamicTargets(t *testing.T) {
	l := &Linker{Arch: sys.ArchARM64, LinkerData: LinkerData{Code: make([]byte, 96), Data: make([]byte, 32), NoPtrData: make([]byte, 32)}, SymMap: map[string]*obj.Sym{
		"source": {Name: "source", Kind: symkind.STEXT, Reloc: []obj.Reloc{
			{Offset: 0, Size: 4, Type: reloctype.R_CALLARM64, SymName: "host"},
			{Offset: 4, Size: 8, Type: reloctype.R_ADDRARM64, SymName: "data"},
			{Offset: 12, Size: 8, Type: reloctype.R_ADDR, SymName: "target"},
			{Type: reloctype.R_USEIFACE, SymName: "type:sample"},
			{Type: reloctype.R_USEIFACEMETHOD, SymName: "type:interface"},
			{Type: reloctype.R_CALLIND},
			{Offset: 20, Size: 4, Type: reloctype.R_CALLARM64, SymName: constants.RuntimeDeferReturn},
		}},
		"source.stkobj":              {Name: "source.stkobj", Kind: symkind.SRODATA, Reloc: []obj.Reloc{{Offset: 8, Size: 4, Type: reloctype.R_ADDROFF, SymName: "target"}}},
		"target":                     {Name: "target", Kind: symkind.STEXT, Offset: 40},
		constants.RuntimeDeferReturn: {Name: constants.RuntimeDeferReturn, Kind: symkind.STEXT, Offset: 48},
		"data":                       {Name: "data", Kind: symkind.SDATA, Offset: 8},
		"host":                       {Name: "host", Kind: symkind.STEXT, Offset: constants.InvalidOffset},
	}}
	originalCode := bytes.Clone(l.Code)
	originalRelocs := append([]obj.Reloc(nil), l.SymMap["source"].Reloc...)
	stats, err := Prelink(l)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ResolvedRelocations != 0 || !bytes.Equal(originalCode, l.Code) || !reflect.DeepEqual(originalRelocs, l.SymMap["source"].Reloc) || len(l.SymMap["source.stkobj"].Reloc) != 1 {
		t.Fatalf("removed runtime data: %+v", stats)
	}
}

func TestPrelinkRejectsInvalidInputWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		offset, size int
		instruction  uint32
		adapted      bool
	}{
		{"adapted", 0, 4, 0x94000000, true},
		{"offset", -1, 4, 0x94000000, false},
		{"end", 30, 4, 0x94000000, false},
		{"size", 0, 8, 0x94000000, false},
		{"opcode", 0, 4, 0xd503201f, false},
		{"encoded immediate", 0, 4, 0x94000001, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &Linker{Arch: sys.ArchARM64, AdaptedOffset: tc.adapted, LinkerData: LinkerData{Code: make([]byte, 32)}, SymMap: map[string]*obj.Sym{
				"source": {Name: "source", Kind: symkind.STEXT, Reloc: []obj.Reloc{{Offset: tc.offset, Size: tc.size, Type: reloctype.R_CALLARM64, SymName: "target"}}},
				"target": {Name: "target", Kind: symkind.STEXT, Offset: 24},
			}}
			binary.LittleEndian.PutUint32(l.Code, tc.instruction)
			before := bytes.Clone(l.Code)
			if _, err := Prelink(l); err == nil {
				t.Fatal("accepted invalid input")
			}
			if !bytes.Equal(l.Code, before) || len(l.SymMap["source"].Reloc) != 1 {
				t.Fatal("mutated failed input")
			}
		})
	}
}
