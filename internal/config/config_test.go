package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNamedUsesDefaultConfigDirAndNormalizesRelativePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "dbx")
	if err := os.MkdirAll(filepath.Join(configDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(configDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := `
[connection]
engine = "sqlite"
path = "./data/test.db"
allow_actions = ["query"]
allow_tables = ["users", "orders", "public.audit_*"]
password.file = "./secret.txt"
`
	if err := os.WriteFile(filepath.Join(configDir, "local-sqlite.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	profile, err := LoadNamed("", "local-sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Path != filepath.Join(configDir, "local-sqlite.toml") {
		t.Fatalf("profile path = %q", profile.Path)
	}
	if profile.Connection.Path != filepath.Join(configDir, "data", "test.db") {
		t.Fatalf("sqlite path = %q", profile.Connection.Path)
	}
	if len(profile.Connection.AllowTables) != 3 || profile.Connection.AllowTables[0] != "users" || profile.Connection.AllowTables[2] != "public.audit_*" {
		t.Fatalf("allow_tables = %#v", profile.Connection.AllowTables)
	}
	secret, source, err := profile.Connection.Password.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if source != "file" || secret != "file-secret" {
		t.Fatalf("unexpected secret resolution: %q / %q", source, secret)
	}
}

func TestLoadNamedSupportsExplicitFileAndNameMatch(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "prod-db.toml")
	raw := `
[connection]
engine = "postgres"
dsn_env = "PROD_DSN"
allow_actions = ["query"]
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	profile, err := LoadNamed(configPath, "prod-db")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "prod-db" {
		t.Fatalf("profile name = %q", profile.Name)
	}

	_, err = LoadNamed(configPath, "other-db")
	if err == nil || !strings.Contains(err.Error(), `does not match config file`) {
		t.Fatalf("expected name mismatch error, got %v", err)
	}
}

func TestLoadNamedRejectsLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
default_connection = "local-sqlite"

[connections.local-sqlite]
engine = "sqlite"
path = "./demo.db"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadNamed(configPath, "config")
	if err == nil || !strings.Contains(err.Error(), "legacy multi-connection format") {
		t.Fatalf("expected legacy format error, got %v", err)
	}
}

func TestListSortsTomlFilesAndIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"zeta.toml": `
[connection]
engine = "mysql"
allow_actions = ["query"]
`,
		"alpha.toml": `
[connection]
engine = "sqlite"
path = "./alpha.db"
allow_actions = ["query"]
`,
		"notes.txt": "ignore me",
	}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	profiles, path, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != dir {
		t.Fatalf("list path = %q", path)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	if profiles[0].Name != "alpha" || profiles[1].Name != "zeta" {
		t.Fatalf("unexpected order: %#v", profiles)
	}

	one, path, err := List(filepath.Join(dir, "alpha.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "alpha.toml") || len(one) != 1 || one[0].Name != "alpha" {
		t.Fatalf("unexpected single-file list: path=%q profiles=%#v", path, one)
	}
}

func TestListReturnsPathInInvalidTomlError(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "broken.toml")
	if err := os.WriteFile(badPath, []byte("[connection\nengine = \"sqlite\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := List(dir)
	if err == nil || !strings.Contains(err.Error(), badPath) {
		t.Fatalf("expected invalid toml error with path, got %v", err)
	}
}

func TestLoadNamedRejectsRemovedMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "legacy.toml")
	raw := `
[connection]
engine = "sqlite"
path = "./demo.db"
mode = "Lantern"
allow_actions = ["query"]
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadNamed(configPath, "legacy")
	if err == nil || !strings.Contains(err.Error(), "mode has been removed; use allow_actions = [...]") {
		t.Fatalf("expected removed mode error, got %v", err)
	}
}

func TestLoadNamedRequiresAllowActions(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "missing-actions.toml")
	raw := `
[connection]
engine = "sqlite"
path = "./demo.db"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadNamed(configPath, "missing-actions")
	if err == nil || !strings.Contains(err.Error(), "connection.allow_actions is required") {
		t.Fatalf("expected missing allow_actions error, got %v", err)
	}
}
