package link

import (
	"bytes"
	"cmd/objfile/sys"
	"fmt"
	"reflect"
	"testing"

	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/symkind"
)

func TestLinkerConstructionStableOrder(t *testing.T) {
	var first *Linker
	for run := 0; run < 24; run++ {
		l := initLinker()
		l.Arch = sys.ArchARM64
		l.initPcHeader()
		for i := 0; i < 20; i++ {
			n := (i + run) % 20
			name := fmt.Sprintf("sample.Func%02d", n)
			l.ObjSymbolMap[name] = &obj.ObjSymbol{Name: name, Kind: symkind.STEXT, Data: []byte{byte(n), 0, 0, 0}, Func: &obj.FuncInfo{}}
		}
		if err := l.addSymbols(); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = l
		} else if !bytes.Equal(first.Code, l.Code) || !bytes.Equal(first.Pclntable, l.Pclntable) || !reflect.DeepEqual(first.NameMap, l.NameMap) || !reflect.DeepEqual(first.SymMap, l.SymMap) {
			t.Fatalf("construction layout changed on run %d", run)
		}
	}
}
