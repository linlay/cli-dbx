//go:build !windows

package odbcdriver

// unixODBC accepts an absolute shared-library path in DRIVER.
func ConnectionName(path string) (string, error) { return path, nil }
