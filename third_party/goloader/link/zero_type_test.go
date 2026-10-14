package link

import (
	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
	"github.com/pkujhd/goloader/objabi/symkind"
	"testing"
)

func TestZeroSizedVariableDoesNotOverwriteTypeDescriptor(t *testing.T) {
	linker := initLinker()
	linker.Arch = getArch("amd64")
	linker.NoPtrTypeData = make([]byte, 8)
	linker.ObjSymbolMap = map[string]*obj.ObjSymbol{
		"root":           {Name: "root", Kind: symkind.SNOPTRDATA, Data: make([]byte, 8), Reloc: []obj.Reloc{{SymName: "zero", Size: 8, Type: reloctype.R_ADDR}}},
		"zero":           {Name: "zero", Kind: symkind.SNOPTRBSS, Type: "type:struct {}"},
		"type:struct {}": {Name: "type:struct {}", Kind: symkind.SRODATA, Data: make([]byte, 48)},
	}
	if _, err := linker.addSymbol("root", nil); err != nil {
		t.Fatal(err)
	}
	if linker.SymMap["zero"].Offset != 0 {
		t.Fatal("zero-sized variable lost its zero slot")
	}
	if got := linker.SymMap["type:struct {}"].Offset; got != 8 {
		t.Fatalf("type descriptor offset overwritten: %d, want 8", got)
	}
	if symbol, err := linker.addSymbol("zero", nil); err != nil || symbol.Name != "zero" {
		t.Fatalf("returned a variable's type instead of the variable: %v %v", symbol, err)
	}
}
