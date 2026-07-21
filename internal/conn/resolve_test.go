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

func TestResolveDecryptsKeyringEncryptedPassword(t *testing.T) {
	store := newConnMemoryKeyStore()
	ctx := secret.WithKeyStore(context.Background(), store)
	encrypted, err := secret.EncryptStringV2(ctx, "db-secret")
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

type connMemoryKeyStore struct {
	keys map[string][]byte
}

func newConnMemoryKeyStore() *connMemoryKeyStore {
	return &connMemoryKeyStore{keys: map[string][]byte{}}
}

func (s *connMemoryKeyStore) Get(_ context.Context, keyID string) ([]byte, error) {
	key, ok := s.keys[keyID]
	if !ok {
		return nil, secret.ErrSecretKeyNotFound
	}
	return append([]byte(nil), key...), nil
}

func (s *connMemoryKeyStore) Set(_ context.Context, keyID string, key []byte) error {
	s.keys[keyID] = append([]byte(nil), key...)
	return nil
}

func TestParseAllowTablesTrimsAndDeduplicates(t *testing.T) {
	got := parseAllowTables([]string{" users", "orders", "public.audit_*", "users", "", "`quoted`"})
	want := []string{"users", "orders", "public.audit_*", "quoted"}
	if len(got) != len(want) {
		t.Fatalf("allow tables = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allow tables = %#v, want %#v", got, want)
		}
	}
}

func TestAllowsTableMatchesSchemaAwareWildcards(t *testing.T) {
	spec := Spec{AllowTables: parseAllowTables([]string{"users", "public.audit_*", "*.orders"})}
	for _, object := range []string{"users", "main.users", "public.audit_log", "sales.orders"} {
		if !spec.AllowsTable(object) {
			t.Fatalf("expected %s to be allowed", object)
		}
	}
	for _, object := range []string{"orders", "private.audit_log", "sales.users_archive"} {
		if spec.AllowsTable(object) {
			t.Fatalf("expected %s to be blocked", object)
		}
	}
}

func TestAllowsTableDefaultsToUnrestricted(t *testing.T) {
	spec := Spec{}
	if !spec.AllowsTable("anything") {
		t.Fatal("empty allow_tables should not restrict tables")
	}
}
