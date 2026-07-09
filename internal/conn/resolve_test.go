package conn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/secret"
)

func TestResolveDecryptsEncryptedPassword(t *testing.T) {
	encrypted, err := secret.EncryptString("master-passphrase", "db-secret")
	if err != nil {
		t.Fatal(err)
	}
	configDir := writeConfig(t, "local-mysql", `
[connection]
engine = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
database = "appdb"
password = "`+encrypted+`"
allow_actions = ["query"]
`)
	ctx := secret.WithPassphraseProvider(context.Background(), func(context.Context) (string, error) {
		return "master-passphrase", nil
	})

	spec, err := Resolve(ctx, ResolveInput{ConfigPath: configDir, Name: "local-mysql", Action: action.Query})
	if err != nil {
		t.Fatal(err)
	}
	if spec.DSN != "app:db-secret@tcp(127.0.0.1:3306)/appdb?parseTime=true" {
		t.Fatalf("dsn = %q", spec.DSN)
	}
	if spec.SecretSources["password"] != "encrypted" {
		t.Fatalf("secret source = %#v", spec.SecretSources)
	}
	if len(spec.Warnings) != 0 {
		t.Fatalf("warnings = %v", spec.Warnings)
	}
}

func TestResolvePlaintextPasswordWarns(t *testing.T) {
	configDir := writeConfig(t, "local-mysql", `
[connection]
engine = "mysql"
host = "127.0.0.1"
user = "app"
database = "appdb"
password = "plain-secret"
allow_actions = ["query"]
`)

	spec, err := Resolve(context.Background(), ResolveInput{ConfigPath: configDir, Name: "local-mysql", Action: action.Query})
	if err != nil {
		t.Fatal(err)
	}
	if spec.SecretSources["password"] != "plaintext" {
		t.Fatalf("secret source = %#v", spec.SecretSources)
	}
	if len(spec.Warnings) != 1 || !strings.Contains(spec.Warnings[0], "plaintext password is not recommended") {
		t.Fatalf("warnings = %v", spec.Warnings)
	}
}

func TestResolveDSNEnvWithPasswordWarns(t *testing.T) {
	t.Setenv("LOCAL_PG_DSN", "postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable")
	configDir := writeConfig(t, "local-pg", `
[connection]
engine = "postgres"
dsn_env = "LOCAL_PG_DSN"
allow_actions = ["query"]
`)

	spec, err := Resolve(context.Background(), ResolveInput{ConfigPath: configDir, Name: "local-pg", Action: action.Query})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Warnings) != 1 || !strings.Contains(spec.Warnings[0], "dsn_env resolved to a DSN with an inline password") {
		t.Fatalf("warnings = %v", spec.Warnings)
	}
}

func writeConfig(t *testing.T, name, raw string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
