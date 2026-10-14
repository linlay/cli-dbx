package link

import (
	"fmt"
	"strings"

	"github.com/pkujhd/goloader/constants"
	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
	"github.com/pkujhd/goloader/objabi/symkind"
)

// PrelinkStats counts records, not their format-dependent serialized sizes.
// Code reservations remain in place because PC tables already describe them.
type PrelinkStats struct {
	RelocationsBefore            int
	RelocationsAfter             int
	ResolvedRelocations          int
	ResolvedByType               map[string]int
	RetainedCodeReserveBytes     int
	RetainedResolvedReserveBytes int
	ReclaimedCodeBytes           int
}

type prelinkPatch struct {
	bytes []byte
	value uint64
}

type prelinkSymbol struct {
	symbol  *obj.Sym
	relocs  []obj.Reloc
	patches []prelinkPatch
}

// Prelink resolves load-base-independent relocations in a fully built Linker.
// Call it after dependency/interface resolution and before serialization or Load.
// Do not add dependencies or change the segment layout afterwards. Symbol/PC/GC
// metadata and all code bytes outside the relocation operands retain their layout.
// On an error the linker is unchanged. Calling Prelink again is harmless.
func Prelink(l *Linker) (PrelinkStats, error) {
	stats := PrelinkStats{ResolvedByType: make(map[string]int)}
	if l == nil || l.Arch == nil || l.Arch.ByteOrder == nil {
		return stats, fmt.Errorf("prelink requires a linker with an architecture")
	}
	if l.AdaptedOffset {
		return stats, fmt.Errorf("prelink requires unadapted segment offsets")
	}
	plans := make([]prelinkSymbol, 0)
	for _, symbol := range l.SymMap {
		if symbol == nil {
			return PrelinkStats{}, fmt.Errorf("prelink encountered a nil symbol")
		}
		plan := prelinkSymbol{symbol: symbol}
		for index, reloc := range symbol.Reloc {
			stats.RelocationsBefore++
			stats.RetainedCodeReserveBytes += reloc.Epilogue.Size
			patch, ok, err := l.prelinkRelocation(symbol, reloc)
			if err != nil {
				return PrelinkStats{}, fmt.Errorf("prelink %s relocation %d: %w", symbol.Name, index, err)
			}
			if ok {
				if plan.patches == nil {
					plan.relocs = append(make([]obj.Reloc, 0, len(symbol.Reloc)-1), symbol.Reloc[:index]...)
				}
				plan.patches = append(plan.patches, patch)
				stats.ResolvedRelocations++
				name := reloctype.RelocTypeString(reloc.Type &^ reloctype.R_WEAK)
				if reloc.Type&reloctype.R_WEAK != 0 {
					name += "|R_WEAK"
				}
				stats.ResolvedByType[name]++
				stats.RetainedResolvedReserveBytes += reloc.Epilogue.Size
			} else if plan.patches != nil {
				plan.relocs = append(plan.relocs, reloc)
			}
		}
		if len(plan.patches) != 0 {
			plans = append(plans, plan)
		}
	}
	for _, plan := range plans {
		for _, patch := range plan.patches {
			if len(patch.bytes) == 8 {
				l.Arch.ByteOrder.PutUint64(patch.bytes, patch.value)
			} else {
				l.Arch.ByteOrder.PutUint32(patch.bytes, uint32(patch.value))
			}
		}
		plan.symbol.Reloc = plan.relocs
	}
	stats.RelocationsAfter = stats.RelocationsBefore - stats.ResolvedRelocations
	return stats, nil
}

func (l *Linker) prelinkRelocation(owner *obj.Sym, r obj.Reloc) (prelinkPatch, bool, error) {
	// StackObject consumes its original relocation sequence at runtime. Defer
	// return calls are likewise inspected while constructing function metadata.
	if owner.Offset == constants.InvalidOffset || strings.HasSuffix(owner.Name, constants.StkobjSuffix) || r.SymName == constants.RuntimeDeferReturn {
		return prelinkPatch{}, false, nil
	}
	target := l.SymMap[r.SymName]
	if target == nil || target.Offset == constants.InvalidOffset || isStringTypeName(target.Name) {
		return prelinkPatch{}, false, nil
	}
	// Keep oversized operands for the runtime linker. These bounds also make
	// the signed arithmetic below independent of host integer overflow.
	if target.Offset < 0 || target.Offset > 1<<31-1 || r.Offset > 1<<31-1 || r.Add < -1<<31 || r.Add > 1<<31-1 {
		return prelinkPatch{}, false, nil
	}
	var data []byte
	var value int64
	size := 4
	switch r.Type {
	case reloctype.R_ADDROFF, reloctype.R_WEAKADDROFF, reloctype.R_METHODOFF:
		var ok bool
		data, _, ok = l.prelinkSection(owner)
		if !ok {
			return prelinkPatch{}, false, nil
		}
		_, prefix, ok := l.prelinkSection(target)
		if !ok {
			return prelinkPatch{}, false, nil
		}
		// relocate uses SymMap offsets, even when addSymbolMap reuses a host
		// address. Types/noptrdata offsets are relative to module.types;
		// text and pointerful data offsets are relative to their own segment.
		value = int64(prefix) + int64(target.Offset) + int64(r.Add)
		if value < -1<<31 || value > 1<<31-1 {
			return prelinkPatch{}, false, nil
		}
	case reloctype.R_CALL, reloctype.R_CALL | reloctype.R_WEAK, reloctype.R_PCREL:
		if l.Arch.Name != "amd64" || !symkind.IsText(owner.Kind) || !symkind.IsText(target.Kind) {
			return prelinkPatch{}, false, nil
		}
		data = l.Code
		value = int64(target.Offset) + int64(r.Add) - int64(r.Offset) - 4
		if value < -1<<31 || value > 1<<31-1 {
			return prelinkPatch{}, false, nil
		}
	case reloctype.R_CALLARM64, reloctype.R_CALLARM64 | reloctype.R_WEAK:
		if l.Arch.Name != "arm64" || !symkind.IsText(owner.Kind) || !symkind.IsText(target.Kind) {
			return prelinkPatch{}, false, nil
		}
		data = l.Code
		value = int64(target.Offset) + int64(r.Add) - int64(r.Offset)
		if value%4 != 0 || r.Offset%4 != 0 {
			return prelinkPatch{}, false, fmt.Errorf("unaligned ARM64 branch")
		}
		if value < -1<<27 || value >= 1<<27 {
			return prelinkPatch{}, false, nil
		}
	case reloctype.R_ADDRARM64:
		if l.Arch.Name != "arm64" || !symkind.IsText(owner.Kind) || !symkind.IsText(target.Kind) {
			return prelinkPatch{}, false, nil
		}
		// All loader code mappings are page aligned. Only code-to-code ADRP
		// is invariant; code and data are mapped independently.
		data = l.Code
		size = 8
		value = int64(target.Offset) + int64(r.Add) - (int64(r.Offset) &^ 0xfff)
		if r.Offset%4 != 0 {
			return prelinkPatch{}, false, fmt.Errorf("unaligned ARM64 address")
		}
		if value < -1<<32 || value >= 1<<32 {
			return prelinkPatch{}, false, nil
		}
	default:
		return prelinkPatch{}, false, nil
	}
	if r.Size != size || r.Offset < 0 || r.Offset > len(data)-size {
		return prelinkPatch{}, false, fmt.Errorf("invalid relocation operand offset %d size %d", r.Offset, r.Size)
	}
	patch := prelinkPatch{bytes: data[r.Offset : r.Offset+size], value: uint64(uint32(value))}
	switch r.Type {
	case reloctype.R_CALLARM64, reloctype.R_CALLARM64 | reloctype.R_WEAK:
		instruction := l.Arch.ByteOrder.Uint32(patch.bytes)
		if instruction&0x7fffffff != 0x14000000 {
			return prelinkPatch{}, false, fmt.Errorf("expected unrelocated ARM64 B/BL instruction")
		}
		patch.value = uint64(instruction | uint32(value/4)&0x03ffffff)
	case reloctype.R_ADDRARM64:
		adrp := l.Arch.ByteOrder.Uint32(patch.bytes)
		add := l.Arch.ByteOrder.Uint32(patch.bytes[4:])
		if adrp&^uint32(0x1f) != 0x90000000 || add&^uint32(0x3ff) != 0x91000000 {
			return prelinkPatch{}, false, fmt.Errorf("expected unrelocated ARM64 ADRP/ADD instructions")
		}
		adrp |= uint32((value>>12)&3)<<29 | uint32((value>>14)&0x7ffff)<<5
		add |= uint32(value&0xfff) << 10
		patch.value = uint64(adrp) | uint64(add)<<32
	}
	return patch, true, nil
}

// prelinkSection returns the original byte slice and its offset relative to the
// base used by R_*OFF. BSS follows the runtime-expanded pclntable and is left to
// Load. Heap strings are also excluded. This does not adapt symbol offsets.
func (l *Linker) prelinkSection(symbol *obj.Sym) ([]byte, int, bool) {
	switch symbol.Kind {
	case symkind.STEXT, symkind.STEXTFIPS:
		return l.Code, 0, true
	case symkind.SDATA, symkind.SDATAFIPS:
		return l.Data, 0, true
	case symkind.SRODATA, symkind.SNOPTRDATA, symkind.SRODATAFIPS, symkind.SNOPTRDATAFIPS:
		if isTypeName(symbol.Name) {
			return l.NoPtrTypeData, 0, true
		}
		if isItabName(symbol.Name) {
			return l.NoPtrItabData, len(l.NoPtrTypeData), true
		}
		if !isStringTypeName(symbol.Name) {
			return l.NoPtrData, len(l.NoPtrTypeData) + len(l.NoPtrItabData), true
		}
	}
	return nil, 0, false
}
