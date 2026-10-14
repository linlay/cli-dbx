//go:build go1.8 && !go1.27
// +build go1.8,!go1.27

package link

import (
	"fmt"
	"github.com/pkujhd/goloader/constants"
	"github.com/pkujhd/goloader/obj"
	"sort"
	"strings"
	"unsafe"
)

// !IMPORTANT: only init firstmodule type, avoid load multiple objs but unload non-sequence errors
func typelinksRegister(symPtr map[string]uintptr) {
	md := firstmoduledata
	for _, tl := range md.typelinks {
		t := (*_type)(adduintptr(md.types, int(tl)))
		registerType(t, symPtr)
	}
}

func (linker *Linker) AddTypeLink(codeModule *CodeModule) error {
	module := codeModule.module
	names := make(map[int32]string)
	for name, symbol := range linker.SymMap {
		if isTypeName(name) && symbol.Offset != constants.InvalidOffset {
			typeOff := int32(codeModule.dataBase + symbol.Offset - int(module.types))
			address := uintptr(codeModule.dataBase + symbol.Offset)
			if address < module.types || address+uintptr(_typeSize) > module.etypes {
				return fmt.Errorf("invalid local type bounds: %s", name)
			}
			typ := (*_type)(unsafe.Pointer(address))
			if typ.str <= 0 || uintptr(typ.str) >= module.etypes-module.types {
				return fmt.Errorf("invalid local type name offset: %s", name)
			}
			encoded := obj.Name{Bytes: (*byte)(unsafe.Pointer(module.types + uintptr(typ.str)))}
			display := encoded.Name()
			if typ.tflag&tflagExtraStar != 0 {
				display = strings.TrimPrefix(display, "*")
			}
			names[typeOff] = display
			module.typelinks = append(module.typelinks, typeOff)
		}
	}
	sort.Slice(module.typelinks, func(i, j int) bool { return names[module.typelinks[i]] < names[module.typelinks[j]] })
	return nil
}

func removeModuleToTypelinks(md *moduledata) {}
