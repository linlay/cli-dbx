package driverprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
)

func writeFlatDriver(t *testing.T, dir, engine, version string) string {
	t.Helper()
	name := "dbx-driver-" + engine
	if version != "" {
		name += "-" + version
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	body := []byte("driver " + engine + " " + version)
	if err := os.WriteFile(path, body, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	m := Manifest{ProtocolVersion: 1, Engine: engine, Version: "v" + version, OS: runtime.GOOS, Arch: runtime.GOARCH, Executable: name, SHA256: hex.EncodeToString(sum[:])}
	saveManifest(t, path+".json", m)
	return path
}

func saveManifest(t *testing.T, path string, m Manifest) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func expectDriverError(t *testing.T, spec conn.Spec, code string) {
	t.Helper()
	_, err := executable(spec)
	var de *Error
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestFlatDriverExplicitVersionsAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	first := writeFlatDriver(t, dir, "oracle", "1.0.0")
	second := writeFlatDriver(t, dir, "oracle", "2.0.0")
	writeFlatDriver(t, dir, "postgres", "1.0.0")
	t.Setenv("DBX_DRIVER_DIR", t.TempDir())
	for _, path := range []string{first, second} {
		spec := conn.Spec{Engine: "oracle", DriverPath: path, DriverDir: t.TempDir()}
		got, err := executable(spec)
		if err != nil || got != path {
			t.Fatalf("selected %q: got %q, %v", path, got, err)
		}
	}
	// A missing explicit file never falls back to an installed default or other version.
	writeFlatDriver(t, dir, "oracle", "")
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	expectDriverError(t, conn.Spec{Engine: "oracle", DriverPath: second, DriverDir: dir}, "driver_missing")
}

func TestFlatDriverDefaultDoesNotGuessVersion(t *testing.T) {
	dir := t.TempDir()
	writeFlatDriver(t, dir, "oracle", "1.0.0")
	spec := conn.Spec{Engine: "oracle", DriverDir: dir}
	expectDriverError(t, spec, "driver_missing")
	want := writeFlatDriver(t, dir, "oracle", "")
	got, err := executable(spec)
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

func TestFlatDriverRejectsManifestRedirectionAndWrongMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest)
		code   string
	}{
		{"other basename", func(m *Manifest) { m.Executable = "other-driver" }, "driver_invalid"},
		{"escape", func(m *Manifest) { m.Executable = "../other-driver" }, "driver_invalid"},
		{"other engine", func(m *Manifest) { m.Engine = "postgres" }, "driver_invalid"},
		{"other architecture", func(m *Manifest) { m.Arch = "unknown" }, "driver_invalid"},
		{"other platform", func(m *Manifest) { m.OS = "unknown" }, "driver_invalid"},
		{"no version", func(m *Manifest) { m.Version = "" }, "driver_invalid"},
		{"protocol", func(m *Manifest) { m.ProtocolVersion = 2 }, "driver_protocol_mismatch"},
		{"checksum", func(m *Manifest) { m.SHA256 = strings.Repeat("0", 64) }, "driver_integrity_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFlatDriver(t, t.TempDir(), "oracle", "1.0.0")
			b, err := os.ReadFile(path + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var m Manifest
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&m)
			saveManifest(t, path+".json", m)
			expectDriverError(t, conn.Spec{Engine: "oracle", DriverPath: path}, tc.code)
		})
	}
}

func TestFlatDriverRejectsMissingNonregularAndSymlinkFiles(t *testing.T) {
	for _, suffix := range []string{"", ".json"} {
		for _, mode := range []string{"missing", "directory", "symlink"} {
			t.Run(suffix+mode, func(t *testing.T) {
				path := writeFlatDriver(t, t.TempDir(), "oracle", "1.0.0")
				target := path + suffix
				if err := os.Rename(target, target+".saved"); err != nil {
					t.Fatal(err)
				}
				code := "driver_invalid"
				switch mode {
				case "missing":
					code = "driver_missing"
				case "directory":
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(target+".saved", target); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
				expectDriverError(t, conn.Spec{Engine: "oracle", DriverPath: path}, code)
			})
		}
	}
}

func TestFlatDriverRejectsOversizedManifest(t *testing.T) {
	path := writeFlatDriver(t, t.TempDir(), "oracle", "1.0.0")
	if err := os.WriteFile(path+".json", []byte(strings.Repeat(" ", 65537)), 0600); err != nil {
		t.Fatal(err)
	}
	expectDriverError(t, conn.Spec{Engine: "oracle", DriverPath: path}, "driver_invalid")
}

func TestFlatDriverDefaultHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBX_DRIVER_DIR", "")
	dir := filepath.Join(home, ".config", "dbx", "drivers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	want := writeFlatDriver(t, dir, "oracle", "")
	got, err := executable(conn.Spec{Engine: "oracle"})
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

func TestDriverAdapterAndBundledResources(t *testing.T) {
	for _, tc := range []struct{ name, adapter, driver, resource, content, code string }{
		{"odbc selected", "odbc", "odbc", "runtime.dylib", "runtime", ""},
		{"native cannot use odbc", "odbc", "oracle", "runtime.dylib", "runtime", "driver_invalid"},
		{"odbc cannot use native", "", "odbc", "runtime.dylib", "runtime", "driver_invalid"},
		{"unknown adapter", "future", "oracle", "runtime.dylib", "runtime", "driver_invalid"},
		{"resource changed", "odbc", "odbc", "runtime.dylib", "changed", "driver_integrity_failed"},
		{"resource missing", "odbc", "odbc", "runtime.dylib", "", "driver_missing"},
		{"resource escape", "odbc", "odbc", "../runtime.dylib", "", "driver_invalid"},
		{"resource directory", "odbc", "odbc", "runtime.dylib", "directory", "driver_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFlatDriver(t, dir, "oracle", "1.0.0")
			b, err := os.ReadFile(path + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err = json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte("runtime"))
			m["adapter"] = tc.adapter
			m["files"] = map[string]string{tc.resource: hex.EncodeToString(sum[:])}
			b, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path+".json", b, 0600); err != nil {
				t.Fatal(err)
			}
			if tc.content == "directory" {
				if err = os.Mkdir(filepath.Join(dir, tc.resource), 0700); err != nil {
					t.Fatal(err)
				}
			} else if tc.content != "" {
				if err = os.WriteFile(filepath.Join(dir, tc.resource), []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			spec := conn.Spec{Engine: "oracle", Driver: tc.driver, DriverPath: path}
			if tc.code != "" {
				expectDriverError(t, spec, tc.code)
			} else {
				if _, err := executable(spec); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
