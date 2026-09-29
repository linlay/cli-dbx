// Package odbcdriver locates vendor ODBC libraries and registers ODBC support.
package odbcdriver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Driver struct {
	Path   string `json:"path"`
	Engine string `json:"engine"`
}

type MissingError struct{ Engine, DriverDir, MarketPackage string }

func (e *MissingError) Error() string {
	return fmt.Sprintf("vendor ODBC driver for %s was not found in %s", e.Engine, e.DriverDir)
}
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dbx", "drivers"), nil
}
func ResolveDir(dir string) (string, error) {
	if dir == "" {
		dir = os.Getenv("DBX_DRIVER_DIR")
	}
	if dir == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(dir)
}
func Find(ctx context.Context, dir, engine string) (Driver, error) {
	return FindFile(ctx, dir, engine, "")
}
func FindFile(ctx context.Context, dir, engine, filename string) (Driver, error) {
	if err := ctx.Err(); err != nil {
		return Driver{}, err
	}
	dir, err := ResolveDir(dir)
	if err != nil {
		return Driver{}, err
	}
	prefix, product := "", ""
	switch engine {
	case "oracle":
		prefix, product = "sqora", "oracle-odbc"
	case "dm":
		prefix, product = "dodbc", "dameng-odbc"
	case "sqlserver":
		prefix, product = "msodbcsql", "sqlserver-odbc"
	default:
		product = engine + "-odbc"
	}
	if prefix == "" && filename == "" {
		return Driver{}, fmt.Errorf("%s requires an explicit odbc_driver filename", engine)
	}
	missing := &MissingError{engine, dir, product}
	if filename != "" {
		if !filepath.IsLocal(filename) {
			return Driver{}, errors.New("odbc_driver must be a relative file path inside driver_dir")
		}
		path := filepath.Join(dir, filename)
		if !nativeLibrary(path) {
			return Driver{}, errors.New("odbc_driver must name a native ODBC library for this operating system")
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return Driver{}, missing
		}
		if err != nil {
			return Driver{}, err
		}
		if !info.Mode().IsRegular() {
			return Driver{}, errors.New("odbc_driver is not a regular file")
		}
		return Driver{Path: path, Engine: engine}, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Driver{}, missing
	}
	if err != nil {
		return Driver{}, err
	}
	var matches []string
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if entry.IsDir() || !nativeLibrary(name) {
			continue
		}
		if strings.HasPrefix(name, prefix) || strings.HasPrefix(name, "lib"+prefix) {
			path := filepath.Join(dir, entry.Name())
			info, err := os.Stat(path)
			if err != nil {
				return Driver{}, err
			}
			if info.Mode().IsRegular() {
				matches = append(matches, path)
			}
		}
	}
	if len(matches) == 0 {
		return Driver{}, missing
	}
	// A versioned library and its symlink refer to the same driver.
	unique := map[string]string{}
	for _, path := range matches {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return Driver{}, err
		}
		if _, ok := unique[target]; !ok {
			unique[target] = path
		}
	}
	if len(unique) > 1 {
		return Driver{}, fmt.Errorf("multiple %s ODBC libraries in %s; set odbc_driver to the intended filename", engine, dir)
	}
	for _, path := range unique {
		return Driver{Path: path, Engine: engine}, nil
	}
	return Driver{}, missing
}
func nativeLibrary(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	switch runtime.GOOS {
	case "windows":
		return strings.HasSuffix(name, ".dll")
	case "darwin":
		return strings.HasSuffix(name, ".dylib")
	default:
		return strings.HasSuffix(name, ".so") || strings.Contains(name, ".so.")
	}
}
func List(ctx context.Context, dir string) ([]Driver, error) {
	drivers := []Driver{}
	for _, engine := range []string{"oracle", "dm", "sqlserver"} {
		found, err := Find(ctx, dir, engine)
		var missing *MissingError
		if errors.As(err, &missing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		drivers = append(drivers, found)
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Engine < drivers[j].Engine })
	return drivers, nil
}

var ErrUnavailable = errors.New("ODBC support is not enabled in this build")

func Check() error {
	for _, name := range sql.Drivers() {
		if name == "odbc" {
			return nil
		}
	}
	return ErrUnavailable
}
