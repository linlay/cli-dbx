//go:build (386 || amd64) && go1.24 && !go1.28 && !dbmod_runtime
// +build 386 amd64
// +build go1.24
// +build !go1.28
// +build !dbmod_runtime

package obj

import (
	"cmd/objfile/disasm"
)

func _Dummy(dummy bool) {
	if dummy {
		_, _ = disasm.DisasmForFile(nil)
	}
}
