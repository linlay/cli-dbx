package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUTF8BOMConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 空格.toml")
	data := "\xef\xbb\xbf[connection]\r\nengine = 'sqlite'\r\npath = './中文 空格.db'\r\nallow_actions = ['query']\r\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := LoadNamed(path, "中文 空格")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Connection.Path != filepath.Join(filepath.Dir(path), "中文 空格.db") {
		t.Fatalf("path=%q", profile.Connection.Path)
	}
}
