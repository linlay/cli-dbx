// Package probe exercises dynamic code, heap objects and GC without a database.
package probe

import "runtime"

func Initialize() error {
	values := make([]string, 4096)
	for i := range values {
		values[i] = "中文模块"
	}
	runtime.GC()
	runtime.KeepAlive(values)
	return nil
}
