package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/secret"
)

func TestLoadNamedUsesDefaultConfigDirAndNormalizesRelativePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, "")

	configDir := filepath.Join(home, ".config", "dbx")
	if err := os.MkdirAll(filepath.Join(configDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `
[connection]
engine = "sqlite"
path = "./data/test.db"
allow_actions = ["query"]
allow_tables = ["users", "orders", "public.audit_*"]
password = "plain-secret"
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
	value, source, warnings, err := profile.Connection.Password.ResolveWithWarnings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if source != "plaintext" || value != "plain-secret" || len(warnings) != 1 {
		t.Fatalf("unexpected secret resolution: value=%q source=%q warnings=%v", value, source, warnings)
	}
}

func TestDefaultConfigUsesAgentConnectionThenSystemFallback(t *testing.T) {
	home := t.TempDir()
	agentHome := t.TempDir()
	systemHome := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentHome)

	writeProfile := func(dir, name, database string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "dbx"), 0o700); err != nil {
			t.Fatal(err)
		}
		raw := "[connection]\nengine = \"sqlite\"\npath = \"./" + database + ".db\"\nallow_actions = [\"query\"]\n"
		if err := os.WriteFile(filepath.Join(dir, "dbx", name+".toml"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile(systemHome, "system-only", "system")
	writeProfile(systemHome, "shared", "system-shared")
	writeProfile(agentHome, "shared", "agent-shared")

	profile, err := LoadNamed("", "system-only")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := profile.Path, filepath.Join(systemHome, "dbx", "system-only.toml"); got != want {
		t.Fatalf("system fallback path = %q, want %q", got, want)
	}
	profile, err = LoadNamed("", "shared")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := profile.Path, filepath.Join(agentHome, "dbx", "shared.toml"); got != want {
		t.Fatalf("agent override path = %q, want %q", got, want)
	}

	profiles, _, err := List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[0].Name != "shared" || profiles[1].Name != "system-only" {
		t.Fatalf("unexpected overlay list: %#v", profiles)
	}
}

func TestDefaultConfigUsesDBXAgentConfigHomeAndIgnoresLegacyEnvironment(t *testing.T) {
	home := t.TempDir()
	agentHome := t.TempDir()
	legacyAgentHome := t.TempDir()
	legacySystemHome := t.TempDir()
	xdgConfigHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentHome)
	t.Setenv("AP_AGENT_CONFIG_HOME", legacyAgentHome)
	t.Setenv("AP_SYSTEM_XDG_CONFIG_HOME", legacySystemHome)
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	paths, err := defaultConfigPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(agentHome, "dbx"), filepath.Join(home, ".config", "dbx")}
	if !samePaths(paths, want) {
		t.Fatalf("config paths = %q, want %q", paths, want)
	}
}

func samePaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDefaultConfigDoesNotFallbackWhenAgentConnectionIsInvalid(t *testing.T) {
	home := t.TempDir()
	agentHome := t.TempDir()
	systemHome := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentHome)
	for _, dir := range []string{agentHome, systemHome} {
		if err := os.MkdirAll(filepath.Join(dir, "dbx"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentHome, "dbx", "shared.toml"), []byte("[connection\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemHome, "dbx", "shared.toml"), []byte("[connection]\nengine = \"sqlite\"\npath = \":memory:\"\nallow_actions = [\"query\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNamed("", "shared"); err == nil || !strings.Contains(err.Error(), "shared.toml") {
		t.Fatalf("expected agent config parse error, got %v", err)
	}
}

func TestExplicitConfigDoesNotUseAgentFallback(t *testing.T) {
	home := t.TempDir()
	agentHome := t.TempDir()
	explicitDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentHome)
	if err := os.MkdirAll(filepath.Join(agentHome, "dbx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentHome, "dbx", "reader.toml"), []byte("[connection]\nengine = \"sqlite\"\npath = \":memory:\"\nallow_actions = [\"query\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNamed(explicitDir, "reader"); err == nil || !strings.Contains(err.Error(), explicitDir) {
		t.Fatalf("expected explicit config not-found error, got %v", err)
	}
}

func TestExplicitConfigErrorsDoNotFallback(t *testing.T) {
	home := t.TempDir()
	agentHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentHome)

	writeProfile := func(dir, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "dbx"), 0o700); err != nil {
			t.Fatal(err)
		}
		raw := "[connection]\nengine = \"sqlite\"\npath = \":memory:\"\nallow_actions = [\"query\"]\n"
		if err := os.WriteFile(filepath.Join(dir, "dbx", name+".toml"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile(agentHome, "reader")
	writeProfile(filepath.Join(home, ".config"), "reader")

	t.Run("directory missing connection", func(t *testing.T) {
		explicitDir := t.TempDir()
		if _, err := LoadNamed(explicitDir, "reader"); err == nil || !strings.Contains(err.Error(), explicitDir) {
			t.Fatalf("expected explicit directory error, got %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		explicitPath := filepath.Join(t.TempDir(), "reader.toml")
		if _, err := LoadNamed(explicitPath, "reader"); err == nil || !strings.Contains(err.Error(), explicitPath) {
			t.Fatalf("expected missing explicit file error, got %v", err)
		}
	})

	t.Run("invalid file", func(t *testing.T) {
		explicitPath := filepath.Join(t.TempDir(), "reader.toml")
		if err := os.WriteFile(explicitPath, []byte("[connection\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadNamed(explicitPath, "reader"); err == nil || !strings.Contains(err.Error(), explicitPath) {
			t.Fatalf("expected invalid explicit file error, got %v", err)
		}
	})

	t.Run("name mismatch", func(t *testing.T) {
		explicitPath := filepath.Join(t.TempDir(), "other.toml")
		raw := "[connection]\nengine = \"sqlite\"\npath = \":memory:\"\nallow_actions = [\"query\"]\n"
		if err := os.WriteFile(explicitPath, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadNamed(explicitPath, "reader"); err == nil || !strings.Contains(err.Error(), "does not match config file") {
			t.Fatalf("expected explicit file name-mismatch error, got %v", err)
		}
	})
}

func TestValueSourceResolvesEncryptedPassword(t *testing.T) {
	encrypted, err := secret.EncryptString("master-passphrase", "db-secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := secret.WithPassphraseProvider(context.Background(), func(context.Context) (string, error) {
		return "master-passphrase", nil
	})

	value, source, warnings, err := (ValueSource{Value: encrypted}).ResolveWithWarnings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value != "db-secret" || source != "encrypted" || len(warnings) != 0 {
		t.Fatalf("unexpected encrypted resolution: value=%q source=%q warnings=%v", value, source, warnings)
	}
}

func TestValueSourceDisablesAgentReadableSources(t *testing.T) {
	testCases := []struct {
		name string
		src  ValueSource
		want string
	}{
		{name: "env", src: ValueSource{Env: "DB_PASSWORD"}, want: "password.env is disabled"},
		{name: "cmd", src: ValueSource{Cmd: []string{"pass", "show", "db"}}, want: "password.cmd is disabled"},
		{name: "file", src: ValueSource{File: "./secret.txt"}, want: "password.file is disabled"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := tc.src.ResolveWithWarnings(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
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
