package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type archivedFile struct {
	data []byte
	mode os.FileMode
}

func TestDriverPackagesPreserveManifestBinaryAndModes(t *testing.T) {
	for _, target := range []string{"linux", "darwin", "windows"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "compiled")
			payload := []byte("independent-driver\x00bytes")
			if err := os.WriteFile(binary, payload, 0600); err != nil {
				t.Fatal(err)
			}
			archive, err := packageDriver(binary, root, "postgres", "v0.2.0-driver-process.1", target, "arm64")
			if err != nil {
				t.Fatal(err)
			}
			ext := ".tar.gz"
			if target == "windows" {
				ext = ".zip"
			}
			if got, want := filepath.Base(archive), "dbx-driver-postgres_v0.2.0-driver-process.1_"+target+"_arm64"+ext; got != want {
				t.Fatalf("archive name = %q, want %q", got, want)
			}
			files := readArchive(t, archive)
			exe := "dbx-driver-postgres-0.2.0-driver-process.1"
			if target == "windows" {
				exe += ".exe"
			}
			if len(files) != 2 {
				t.Fatalf("package contains unexpected files: %v", files)
			}
			if !bytes.Equal(files[exe].data, payload) {
				t.Fatal("binary content or layout changed")
			}
			if files[exe].mode.Perm() != 0755 {
				t.Fatal("driver must be executable")
			}
			if files[exe+".json"].mode.Perm() != 0644 {
				t.Fatal("manifest permissions")
			}
			var manifest map[string]any
			if err := json.Unmarshal(files[exe+".json"].data, &manifest); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(payload)
			for key, want := range map[string]any{"protocol_version": float64(1), "engine": "postgres", "version": "v0.2.0-driver-process.1", "os": target, "arch": "arm64", "executable": exe, "sha256": hex.EncodeToString(sum[:])} {
				if manifest[key] != want {
					t.Fatalf("manifest %s = %v, want %v", key, manifest[key], want)
				}
			}
			if len(manifest) != 7 {
				t.Fatalf("unexpected manifest fields: %v", manifest)
			}
			first, _ := os.ReadFile(archive)
			if _, err := packageDriver(binary, root, "postgres", "v0.2.0-driver-process.1", target, "arm64"); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(archive)
			if !bytes.Equal(first, second) {
				t.Fatal("package is not deterministic")
			}
			checksum, err := os.ReadFile(archive + ".sha256")
			if err != nil {
				t.Fatal(err)
			}
			archiveSum := sha256.Sum256(first)
			if string(checksum) != hex.EncodeToString(archiveSum[:])+"  "+filepath.Base(archive)+"\n" {
				t.Fatal("archive checksum does not match")
			}
		})
	}
}

func TestDriverVersionsExtractTogether(t *testing.T) {
	for _, target := range []string{"linux", "windows"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			installDir := filepath.Join(root, "drivers")
			if err := os.Mkdir(installDir, 0755); err != nil {
				t.Fatal(err)
			}
			versions := []string{"v1.0.0", "v2.0.0"}
			for _, version := range versions {
				binary := filepath.Join(root, "compiled-"+version)
				if err := os.WriteFile(binary, []byte("binary for "+version), 0600); err != nil {
					t.Fatal(err)
				}
				archive, err := packageDriver(binary, root, "postgres", version, target, "amd64")
				if err != nil {
					t.Fatal(err)
				}
				for name, file := range readArchive(t, archive) {
					if name != filepath.Base(name) {
						t.Fatalf("archive entry is not at root: %q", name)
					}
					if err := os.WriteFile(filepath.Join(installDir, name), file.data, file.mode.Perm()); err != nil {
						t.Fatal(err)
					}
				}
			}
			installed, err := os.ReadDir(installDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(installed) != 4 {
				t.Fatalf("installed %d files, want four", len(installed))
			}
			for _, version := range versions {
				exe := "dbx-driver-postgres-" + strings.TrimPrefix(version, "v")
				if target == "windows" {
					exe += ".exe"
				}
				binary, err := os.ReadFile(filepath.Join(installDir, exe))
				if err != nil {
					t.Fatal(err)
				}
				if string(binary) != "binary for "+version {
					t.Fatalf("%s has wrong bytes", exe)
				}
				var m manifest
				manifestBytes, err := os.ReadFile(filepath.Join(installDir, exe+".json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(manifestBytes, &m); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(binary)
				if m.Executable != exe || m.Version != version || m.SHA256 != hex.EncodeToString(sum[:]) {
					t.Fatalf("%s has mismatched manifest: %+v", exe, m)
				}
				for name, want := range map[string]os.FileMode{exe: 0755, exe + ".json": 0644} {
					info, err := os.Stat(filepath.Join(installDir, name))
					if err != nil {
						t.Fatal(err)
					}
					// Windows does not expose Unix permission bits. Archive modes
					// are checked directly on every host in the test above.
					if runtime.GOOS != "windows" && info.Mode().Perm() != want {
						t.Fatalf("%s mode = %o, want %o", name, info.Mode().Perm(), want)
					}
				}
			}
		})
	}
}

func TestDriverPackagerRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "binary")
	if err := os.WriteFile(binary, []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ engine, version, target, arch string }{
		{"../postgres", "v1.0.0", "linux", "amd64"},
		{"sqlite", "v1.0.0", "linux", "amd64"},
		{"postgres", "../../escaped", "linux", "amd64"},
		{"postgres", "v1.0.0", "freebsd", "amd64"},
		{"postgres", "v1.0.0", "linux", "../amd64"},
	} {
		if _, err := packageDriver(binary, root, tc.engine, tc.version, tc.target, tc.arch); err == nil {
			t.Fatalf("accepted invalid inputs: %+v", tc)
		}
	}
	if _, err := packageDriver(root, root, "dm", "v1.0.0", "linux", "amd64"); err == nil {
		t.Fatal("accepted directory as binary")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(binary, link); err == nil {
		if _, err := packageDriver(link, root, "dm", "v1.0.0", "linux", "amd64"); err == nil {
			t.Fatal("accepted symlink as binary")
		}
	}
	if err := os.WriteFile(binary, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := packageDriver(binary, root, "dm", "v1.0.0", "linux", "amd64"); err == nil {
		t.Fatal("accepted empty binary")
	}
}

func TestEveryExternalEnginePackages(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "binary")
	if err := os.WriteFile(binary, []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"postgres", "oracle", "dm", "sqlserver"} {
		if _, err := packageDriver(binary, root, engine, "v1.0.0", "linux", "amd64"); err != nil {
			t.Fatal(err)
		}
	}
}

func readArchive(t *testing.T, path string) map[string]archivedFile {
	t.Helper()
	out := map[string]archivedFile{}
	if strings.HasSuffix(path, ".zip") {
		r, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for _, f := range r.File {
			rd, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rd)
			rd.Close()
			if err != nil {
				t.Fatal(err)
			}
			out[f.Name] = archivedFile{b, f.Mode()}
		}
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = archivedFile{b, h.FileInfo().Mode()}
	}
	return out
}

func TestODBCPackagesKeepNativeAndRuntimeFilesSeparate(t *testing.T) {
	for _, target := range []string{"darwin", "windows", "linux"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "binary")
			resources := filepath.Join(root, "resources")
			if err := os.WriteFile(binary, []byte("odbc program"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(resources, 0700); err != nil {
				t.Fatal(err)
			}
			resource := "dbx-odbc-runtime-1.0.0-libodbc.2.dylib"
			if err := os.WriteFile(filepath.Join(resources, resource), []byte("runtime"), 0755); err != nil {
				t.Fatal(err)
			}
			archive, err := packageDriverVariant(binary, root, "oracle", "v1.0.0", target, "amd64", "odbc", resources)
			if err != nil {
				t.Fatal(err)
			}
			files := readArchive(t, archive)
			exe := "dbx-driver-oracle-odbc-1.0.0"
			if target == "windows" {
				exe += ".exe"
			}
			if len(files) != 3 || !bytes.Equal(files[exe].data, []byte("odbc program")) {
				t.Fatalf("files=%v", files)
			}
			var m struct {
				Adapter    string            `json:"adapter"`
				Executable string            `json:"executable"`
				Files      map[string]string `json:"files"`
			}
			if err = json.Unmarshal(files[exe+".json"].data, &m); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte("runtime"))
			if m.Adapter != "odbc" || m.Executable != exe || m.Files[resource] != hex.EncodeToString(sum[:]) {
				t.Fatalf("manifest=%+v", m)
			}
			if !strings.HasPrefix(filepath.Base(archive), "dbx-driver-oracle-odbc_v1.0.0_") {
				t.Fatal(archive)
			}
			native, err := packageDriver(binary, root, "oracle", "v1.0.0", target, "amd64")
			if err != nil {
				t.Fatal(err)
			}
			if native == archive {
				t.Fatal("ODBC archive overwrote native archive")
			}
		})
	}
}

func TestODBCPackagerRejectsInvalidResourcesAndAdapters(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "binary")
	resources := filepath.Join(root, "resources")
	if err := os.WriteFile(binary, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(resources, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ engine, adapter, target, arch string }{
		{"oracle", "unknown", "linux", "amd64"}, {"postgres", "odbc", "linux", "amd64"}, {"oracle", "odbc", "windows", "arm64"},
	} {
		if _, err := packageDriverVariant(binary, root, tc.engine, "v1.0.0", tc.target, tc.arch, tc.adapter, ""); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if err := os.Mkdir(filepath.Join(resources, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := packageDriverVariant(binary, root, "oracle", "v1.0.0", "darwin", "arm64", "odbc", resources); err == nil {
		t.Fatal("accepted nested resources")
	}
}
