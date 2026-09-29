package odbcdriver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeDriverDiscoveryDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DBX_DRIVER_DIR", dir)
	name := "libsqora.so.23.1"
	if runtime.GOOS == "darwin" {
		name = "libsqora.dylib"
	}
	if runtime.GOOS == "windows" {
		name = "sqora32.dll"
	}
	path := filepath.Join(dir, name)
	// A non-executable fixture is enough: discovery must only inspect files.
	if err := os.WriteFile(path, []byte("native driver fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Find(context.Background(), "", "oracle")
	if err != nil || got.Path != path {
		t.Fatalf("Find = %+v, %v", got, err)
	}
}

func TestMissingDriverDoesNotCreateDirectoryOrSearchPATH(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("DBX_DRIVER_DIR", dir)
	_, err := Find(context.Background(), "", "oracle")
	var missing *MissingError
	if !errors.As(err, &missing) || missing.DriverDir != dir || missing.Engine != "oracle" {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory created: %v", err)
	}
}

func TestExplicitNativeFileAndAmbiguousVersions(t *testing.T) {
	dir := t.TempDir()
	suffix := ".so"
	if runtime.GOOS == "darwin" {
		suffix = ".dylib"
	}
	if runtime.GOOS == "windows" {
		suffix = ".dll"
	}
	for _, name := range []string{"libsqora1" + suffix, "libsqora2" + suffix} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("library"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Find(context.Background(), dir, "oracle"); err == nil {
		t.Fatal("ambiguous versions must require an explicit filename")
	}
	got, err := FindFile(context.Background(), dir, "oracle", "libsqora2"+suffix)
	if err != nil || got.Path != filepath.Join(dir, "libsqora2"+suffix) {
		t.Fatalf("explicit selection failed: %+v %v", got, err)
	}
	for _, name := range []string{"../outside" + suffix, "dbx-driver-oracle.exe"} {
		if _, err := FindFile(context.Background(), dir, "oracle", name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
