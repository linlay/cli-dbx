//go:build go1.26 && !go1.27

package link

import (
	"encoding/binary"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

var methodPointerFixture []byte

// A broken pointer would fault in unsafe code. Keep the reproducer in a child
// process so it fails this test without aborting the other loader regressions.
func TestInterfaceMethodPointerUsesMask(t *testing.T) {
	if os.Getenv("DBX_TEST_INTERFACE_POINTER_CHILD") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(executable, "-test.run=^TestInterfaceMethodPointerUsesMask$")
		cmd.Env = append(os.Environ(), "DBX_TEST_INTERFACE_POINTER_CHILD=1")
		out, err := cmd.CombinedOutput()
		if len(out) > 2000 {
			out = out[:2000]
		}
		if err != nil {
			t.Fatalf("interface pointer was not masked before rebasing: %v\n%s", err, out)
		}
		return
	}
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit Mach-O pointer encoding")
	}
	// This fixture only exercises pointer adaptation, not runtime symbol lookup.
	_Kind = func(typ *_type) reflect.Kind { return reflect.Kind(typ.kind) }
	methodPointerFixture = make([]byte, 256)
	data := methodPointerFixture
	base := uintptr(unsafe.Pointer(&data[0]))
	inter := (*interfacetype)(unsafe.Pointer(base))
	inter.typ.kind = uint8(reflect.Interface)
	const imageBase, methodsOffset = uintptr(0x1000), uintptr(128)
	tag := uintptr(1) << 56
	header := (*sliceHeader)(unsafe.Pointer(&inter.mhdr))
	header.Data, header.Len, header.Cap = tag|imageBase|methodsOffset, 1, 1
	state := typeData{data: data, addrBase: imageBase, newAddrBase: base,
		byteOrder: binary.LittleEndian, adapted: make(map[int32]int32),
		ptrMask: func(pointer uintptr) uintptr { return pointer &^ tag }}
	state.adaptType(0)
	if header.Data != base+methodsOffset || len(inter.mhdr) != 1 {
		t.Fatalf("method slice points to %#x, want %#x", header.Data, base+methodsOffset)
	}
	runtime.KeepAlive(data)
}
