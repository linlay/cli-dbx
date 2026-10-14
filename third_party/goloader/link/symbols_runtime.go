//go:build dbmod_runtime

package link

import (
	"debug/macho"
	"debug/pe"
	"fmt"
	"runtime"
)

type executableSymbol struct {
	Name string
	Addr uint64
	Code rune
}

// Runtime only needs named host addresses. Compiler archives and disassembly
// remain build-time dependencies instead of being linked into the CLI.
func readExecutableSymbols(path string) ([]executableSymbol, error) {
	var symbols []executableSymbol
	switch runtime.GOOS {
	case "windows":
		f, err := pe.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		header, ok := f.OptionalHeader.(*pe.OptionalHeader64)
		if !ok {
			return nil, fmt.Errorf("shared host requires Windows x64")
		}
		for _, s := range f.Symbols {
			if s.SectionNumber > 0 && int(s.SectionNumber) <= len(f.Sections) {
				address := header.ImageBase + uint64(f.Sections[s.SectionNumber-1].VirtualAddress) + uint64(s.Value)
				symbols = append(symbols, executableSymbol{s.Name, address, 'R'})
			}
		}
	case "darwin":
		f, err := macho.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if f.Symtab == nil {
			return nil, fmt.Errorf("host symbols are missing")
		}
		for _, s := range f.Symtab.Syms {
			if s.Sect > 0 {
				symbols = append(symbols, executableSymbol{s.Name, s.Value, 'R'})
			}
		}
	default:
		return nil, fmt.Errorf("shared host currently supports windows/amd64 and darwin/arm64")
	}
	if len(symbols) == 0 {
		return nil, fmt.Errorf("host symbols are missing")
	}
	return symbols, nil
}
