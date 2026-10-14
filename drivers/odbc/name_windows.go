//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// The selected registry view matches this process architecture. ODBC on Windows
// requires the registered driver description, even when the DLL path is known.
func connectionName(path string) (string, error) {
	for _, hive := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		root, err := registry.OpenKey(hive, `SOFTWARE\ODBC\ODBCINST.INI`, registry.READ)
		if err != nil {
			continue
		}
		names, err := root.ReadSubKeyNames(-1)
		root.Close()
		if err != nil {
			continue
		}
		for _, name := range names {
			key, err := registry.OpenKey(hive, `SOFTWARE\ODBC\ODBCINST.INI\`+name, registry.READ)
			if err != nil {
				continue
			}
			value, valueType, err := key.GetStringValue("Driver")
			key.Close()
			if err != nil {
				continue
			}
			if valueType == registry.EXPAND_SZ {
				value, err = registry.ExpandString(value)
				if err != nil {
					continue
				}
			}
			if strings.EqualFold(filepath.Clean(value), filepath.Clean(path)) {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("ODBC library is not registered for this process architecture; use the vendor installer")
}
