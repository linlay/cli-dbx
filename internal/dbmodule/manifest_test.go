package dbmodule

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestModuleManifestFailsClosedBeforeLoadingCode(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "host")
	path := filepath.Join(dir, "postgres.dbmod")
	content := []byte("not executable; validation must finish before a loader sees it")
	if err := os.WriteFile(host, []byte("exact host"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	hostDigest, _ := Digest(host)
	digest := sha256.Sum256(content)
	base := Manifest{FormatVersion: FormatVersion, APIVersion: 1, ProtocolVersion: 1, Engine: "postgres", Version: "test",
		OS: runtime.GOOS, Arch: runtime.GOARCH, Toolchain: runtime.Version(), HostSHA256: hostDigest,
		Module: filepath.Base(path), SHA256: hex.EncodeToString(digest[:]), DecodedBytes: 100}
	write := func(m Manifest) {
		t.Helper()
		b, _ := json.Marshal(m)
		if err := os.WriteFile(path+".json", b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(base)
	if _, got, err := Verify(path, "postgres", host); err != nil || string(got) != string(content) {
		t.Fatalf("valid package: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"host", func(m *Manifest) { m.HostSHA256 = strings.Repeat("0", 64) }},
		{"digest", func(m *Manifest) { m.SHA256 = strings.Repeat("0", 64) }},
		{"format", func(m *Manifest) { m.FormatVersion++ }},
		{"api", func(m *Manifest) { m.APIVersion++ }},
		{"protocol", func(m *Manifest) { m.ProtocolVersion++ }},
		{"toolchain", func(m *Manifest) { m.Toolchain = "go1.22" }},
		{"os", func(m *Manifest) { m.OS = "other" }},
		{"arch", func(m *Manifest) { m.Arch = "other" }},
		{"engine", func(m *Manifest) { m.Engine = "oracle" }},
		{"escape", func(m *Manifest) { m.Module = "../postgres.dbmod" }},
		{"empty version", func(m *Manifest) { m.Version = "" }},
		{"decode bound", func(m *Manifest) { m.DecodedBytes = MaxDecodedBytes + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.mutate(&m)
			write(m)
			if _, _, err := Verify(path, "postgres", host); err == nil {
				t.Fatal("accepted incompatible module")
			}
		})
	}
	write(base)
	b, _ := os.ReadFile(path + ".json")
	for _, suffix := range []string{` {}`, ` garbage`} {
		if err := os.WriteFile(path+".json", append(append([]byte{}, b...), suffix...), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Verify(path, "postgres", host); err == nil {
			t.Fatal("accepted trailing manifest data")
		}
	}
	if err := os.WriteFile(path+".json", []byte(`{"unknown":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Verify(path, "postgres", host); err == nil {
		t.Fatal("accepted unknown manifest fields")
	}
}
