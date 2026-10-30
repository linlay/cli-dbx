//go:build dbmod

package dbmodule

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"runtime"
	"strings"
	"unsafe"

	"github.com/linlay/cli-dbx/internal/conn"
	format "github.com/linlay/cli-dbx/internal/dbmodformat"
	"github.com/pkujhd/goloader"
)

// Loaded owns mapped code until the CLI exits. Unloading live driver code is
// deliberately unsupported: database drivers can retain goroutines and globals.
type Loaded struct {
	module     *goloader.CodeModule
	Prepare    func(*conn.Spec) error
	entryCells []*uintptr
}

func Load(in io.Reader, entry string, expectedSize int64) (*Loaded, error) {
	symbols := make(map[string]uintptr)
	if err := goloader.RegSymbol(symbols); err != nil {
		return nil, fmt.Errorf("read host symbols: %w", err)
	}
	LocalizeConstants(symbols)
	z, err := gzip.NewReader(in)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	decoded, err := io.ReadAll(io.LimitReader(z, MaxDecodedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > MaxDecodedBytes || (expectedSize > 0 && int64(len(decoded)) != expectedSize) {
		return nil, fmt.Errorf("invalid decoded module size")
	}
	linker, err := format.Decode(bytes.NewReader(decoded))
	if err != nil {
		return nil, err
	}
	module, err := goloader.Load((*goloader.Linker)(linker), symbols)
	if err != nil {
		return nil, err
	}
	pointer := new(uintptr)
	*pointer = module.Syms[entry+".Initialize"]
	if *pointer == 0 {
		return nil, fmt.Errorf("module entry is missing")
	}
	container := unsafe.Pointer(pointer)
	initialize := *(*func() error)(unsafe.Pointer(&container))
	if err := initialize(); err != nil {
		return nil, err
	}
	loaded := &Loaded{module: module, entryCells: []*uintptr{pointer}}
	if address := module.Syms[entry+".Prepare"]; address != 0 {
		pointer := new(uintptr)
		*pointer = address
		loaded.entryCells = append(loaded.entryCells, pointer)
		container := unsafe.Pointer(pointer)
		loaded.Prepare = *(*func(*conn.Spec) error)(unsafe.Pointer(&container))
	}
	runtime.KeepAlive(module)
	return loaded, nil
}

// SIMD loads of shared floating-point constants cannot reach an arbitrary
// Windows mapping with a 32-bit displacement. Keep immutable literal pools in
// the module's nearby data mapping instead of binding them to the host image.
func LocalizeConstants(symbols map[string]uintptr) {
	for name := range symbols {
		if strings.HasPrefix(name, "$f32.") || strings.HasPrefix(name, "$f64.") || strings.HasPrefix(name, "$i64.") {
			delete(symbols, name)
		}
	}
}
