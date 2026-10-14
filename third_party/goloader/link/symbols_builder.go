//go:build !dbmod_runtime

package link

import "cmd/objfile/objfile"

type executableSymbol = objfile.Sym

func readExecutableSymbols(path string) ([]executableSymbol, error) {
	f, err := objfile.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Symbols()
}
