package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigWithSources(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBX_SECRET", "env-secret")
	configPath := filepath.Join(dir, "config.toml")
	raw := `
default_connection = "mysql1"

[connections.mysql1]
engine = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
database = "zenmind"
mode = "Lantern"
password.env = "DBX_SECRET"

[connections.sqlite1]
engine = "sqlite"
path = "./test.db"
password.file = "` + secretFile + `"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	secret, source, err := cfg.Connections["mysql1"].Password.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if source != "env" || secret != "env-secret" {
		t.Fatalf("unexpected source resolution: %q / %q", source, secret)
	}
}
