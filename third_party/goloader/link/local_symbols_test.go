package link

import (
	"github.com/pkujhd/goloader/constants"
	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
	"testing"
)

func TestHostRelativeTargetRequiresLocalCopy(t *testing.T) {
	for _, typ := range []int{reloctype.R_ADDROFF, reloctype.R_METHODOFF} {
		l := &Linker{SymMap: map[string]*obj.Sym{
			"wrapper": {Name: "wrapper", Reloc: []obj.Reloc{{Type: typ, SymName: "target"}}},
			"target":  {Name: "target", Offset: constants.InvalidOffset},
		}}
		host := map[string]uintptr{"target": 0x1000}
		if got := UnresolvedSymbols(l, host); len(got) != 1 || got[0] != "target" {
			t.Fatalf("offset target bound to absolute host address: %v", got)
		}
		l.SymMap["target"].Offset = 32
		if got := UnresolvedSymbols(l, host); len(got) != 0 {
			t.Fatalf("local target unresolved: %v", got)
		}
		l.SymMap["target"].Offset = constants.InvalidOffset
		l.SymMap["wrapper"].Reloc[0].Type = reloctype.R_CALL
		if got := UnresolvedSymbols(l, host); len(got) != 0 {
			t.Fatalf("ordinary call should reuse host: %v", got)
		}
	}
}
