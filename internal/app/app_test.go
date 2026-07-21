package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/buildinfo"
	"github.com/linlay/cli-dbx/internal/secret"
	_ "modernc.org/sqlite"
)

type commandResult struct {
	code   int
	stdout string
	stderr string
}

func TestExecuteRootHelpUsesStructuredLayout(t *testing.T) {
	result := runCommand(t, nil, "help")

	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, result.code)
	}
	for _, token := range []string{"Usage:", "Description:", "Subcommands:", "Flags:", "Examples:"} {
		if !strings.Contains(result.stdout, token) {
			t.Fatalf("expected root help to contain %q, got:\n%s", token, result.stdout)
		}
	}
	if strings.Contains(result.stdout, "Available Commands:") {
		t.Fatalf("expected custom help layout, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "dbx [command]") {
		t.Fatalf("expected root usage override, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "conn") || !strings.Contains(result.stdout, "query") || !strings.Contains(result.stdout, "version") {
		t.Fatalf("expected root commands in help, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "dbx query local-pg 'select id, email from users order by id'") {
		t.Fatalf("expected DBX examples in help, got:\n%s", result.stdout)
	}
	if strings.Contains(result.stdout, "resolve") {
		t.Fatalf("hidden compatibility command should not appear in help, got:\n%s", result.stdout)
	}
	if result.stderr != "" {
		t.Fatalf("expected empty stderr, got %q", result.stderr)
	}
}

func TestExecuteCommandHelpUsesStructuredLayout(t *testing.T) {
	result := runCommand(t, nil, "inspect", "--help")

	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, result.code)
	}
	for _, token := range []string{"Usage:", "Description:", "Subcommands:", "Flags:", "Examples:"} {
		if !strings.Contains(result.stdout, token) {
			t.Fatalf("expected inspect help to contain %q, got:\n%s", token, result.stdout)
		}
	}
	if strings.Contains(result.stdout, "Args fields:") {
		t.Fatalf("inspect group help should not show args table, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "schema") || !strings.Contains(result.stdout, "table") || !strings.Contains(result.stdout, "connection") {
		t.Fatalf("expected inspect subcommands in help, got:\n%s", result.stdout)
	}
}

func TestExecuteLeafHelpShowsArgsFieldsAndMatchesHelpCommand(t *testing.T) {
	direct := runCommand(t, nil, "inspect", "table", "--help")
	viaHelp := runCommand(t, nil, "help", "inspect", "table")

	if direct.code != ExitSuccess || viaHelp.code != ExitSuccess {
		t.Fatalf("expected exit %d, got direct=%d viaHelp=%d", ExitSuccess, direct.code, viaHelp.code)
	}
	if direct.stdout != viaHelp.stdout {
		t.Fatalf("expected help inspect table and inspect table --help to match\ndirect:\n%s\nvia help:\n%s", direct.stdout, viaHelp.stdout)
	}
	for _, token := range []string{"Usage:", "Description:", "Flags:", "Args fields:"} {
		if !strings.Contains(direct.stdout, token) {
			t.Fatalf("expected inspect table help to contain %q, got:\n%s", token, direct.stdout)
		}
	}
	if !strings.Contains(direct.stdout, "dbx inspect table <conn> <table> [schema]") {
		t.Fatalf("expected inspect table usage, got:\n%s", direct.stdout)
	}
	for _, token := range []string{"conn", "table", "schema"} {
		if !strings.Contains(direct.stdout, token) {
			t.Fatalf("expected inspect table args field %q, got:\n%s", token, direct.stdout)
		}
	}
}

func TestExecuteMultiFormHelpShowsAllUsageForms(t *testing.T) {
	result := runCommand(t, nil, "query", "--help")

	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, result.code)
	}
	for _, token := range []string{
		"dbx query <conn> <sql>",
		"dbx query file <conn> <path.sql>",
		"dbx query dsn <engine> <dsn> <sql>",
		"Args fields:",
		"path.sql",
		"engine",
		"dsn",
	} {
		if !strings.Contains(result.stdout, token) {
			t.Fatalf("expected query help to contain %q, got:\n%s", token, result.stdout)
		}
	}
	if strings.Contains(result.stdout, "Available Commands:") {
		t.Fatalf("expected custom help layout, got:\n%s", result.stdout)
	}
}

func TestExecuteRequiredFlagHelpIncludesFlagSection(t *testing.T) {
	result := runCommand(t, nil, "tx", "--help")

	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, result.code)
	}
	for _, token := range []string{"dbx tx <conn> --plan <path.json>", "--plan string", "Args fields:", "conn"} {
		if !strings.Contains(result.stdout, token) {
			t.Fatalf("expected tx help to contain %q, got:\n%s", token, result.stdout)
		}
	}
}

func TestHelpExamplesTopicRemoved(t *testing.T) {
	result := runCommand(t, nil, "help", "examples")

	if result.code != ExitUsage {
		t.Fatalf("expected exit %d, got %d", ExitUsage, result.code)
	}
	if !strings.Contains(result.stderr, `unknown command "examples" for "dbx help"`) {
		t.Fatalf("expected unknown help topic error, got stderr=%q", result.stderr)
	}
}

func TestVersionCommandAndFlag(t *testing.T) {
	oldVersion := buildinfo.Version
	oldCommit := buildinfo.Commit
	oldBuildTime := buildinfo.BuildTime
	buildinfo.Version = "v0.1.0"
	buildinfo.Commit = "abc1234"
	buildinfo.BuildTime = "2026-03-25T12:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.Commit = oldCommit
		buildinfo.BuildTime = oldBuildTime
	})

	versionResult := runCommand(t, nil, "version")
	if versionResult.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, versionResult.code)
	}
	if strings.TrimSpace(versionResult.stdout) != "dbx v0.1.0 (commit abc1234, built 2026-03-25T12:00:00Z)" {
		t.Fatalf("unexpected version stdout: %q", versionResult.stdout)
	}

	flagResult := runCommand(t, nil, "--version")
	if flagResult.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, flagResult.code)
	}
	if strings.TrimSpace(flagResult.stdout) != "dbx v0.1.0 (commit abc1234, built 2026-03-25T12:00:00Z)" {
		t.Fatalf("unexpected --version stdout: %q", flagResult.stdout)
	}
}

func TestSecretEncryptOutputsTomlPasswordLine(t *testing.T) {
	store := newAppMemoryKeyStore()
	ctx := secret.WithKeyStore(context.Background(), store)
	stdin := bytes.NewBufferString("db-secret\n")
	result := runCommandContext(t, ctx, stdin, "secret", "encrypt")
	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d stdout=%q stderr=%q", ExitSuccess, result.code, result.stdout, result.stderr)
	}
	if strings.Contains(result.stderr, "master passphrase:") || !strings.Contains(result.stderr, "database password:") {
		t.Fatalf("expected secret prompts on stderr, got %q", result.stderr)
	}
	raw := strings.TrimSpace(result.stdout)
	if !strings.HasPrefix(raw, `password = "dbx-aes-gcm:v2:`) || !strings.HasSuffix(raw, `"`) {
		t.Fatalf("unexpected secret encrypt output: %q", result.stdout)
	}
	value := strings.TrimSuffix(strings.TrimPrefix(raw, `password = "`), `"`)
	plain, err := secret.DecryptString(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "db-secret" {
		t.Fatalf("decrypted password = %q", plain)
	}
}

func TestSecretHelpDoesNotExposePlaintextCommands(t *testing.T) {
	result := runCommand(t, nil, "secret", "--help")
	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, result.code)
	}
	for _, forbidden := range []string{"decrypt", "export", "show-key"} {
		if strings.Contains(strings.ToLower(result.stdout), forbidden) {
			t.Fatalf("secret help must not contain %q:\n%s", forbidden, result.stdout)
		}
	}
	if !strings.Contains(result.stdout, "encrypt") {
		t.Fatalf("secret help must contain encrypt:\n%s", result.stdout)
	}
}

func TestConnShowDecryptsV2WithoutPromptOrSecretOutput(t *testing.T) {
	store := newAppMemoryKeyStore()
	ctx := secret.WithKeyStore(context.Background(), store)
	encrypted, err := secret.EncryptStringV2(ctx, "db-secret")
	if err != nil {
		t.Fatal(err)
	}
	configDir := writeAppConnectionConfig(t, "local-mysql", `
[connection]
engine = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
database = "appdb"
password = "`+encrypted+`"
allow_actions = ["query"]
`)

	result := runCommandContext(t, ctx, nil, "conn", "show", "--config", configDir, "local-mysql")
	if result.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d stdout=%q stderr=%q", ExitSuccess, result.code, result.stdout, result.stderr)
	}
	if result.stderr != "" || strings.Contains(result.stdout, "master passphrase") {
		t.Fatalf("v2 resolution must not prompt, stdout=%q stderr=%q", result.stdout, result.stderr)
	}
	for _, forbidden := range []string{"db-secret", "app:db-secret@", encrypted} {
		if strings.Contains(result.stdout, forbidden) {
			t.Fatalf("conn show leaked %q in %q", forbidden, result.stdout)
		}
	}
	if !strings.Contains(result.stdout, "***") {
		t.Fatalf("conn show should contain a redacted DSN: %q", result.stdout)
	}
}

func TestConnShowReportsMissingV2KeyWithStableCode(t *testing.T) {
	encryptStore := newAppMemoryKeyStore()
	encryptCtx := secret.WithKeyStore(context.Background(), encryptStore)
	encrypted, err := secret.EncryptStringV2(encryptCtx, "db-secret")
	if err != nil {
		t.Fatal(err)
	}
	configDir := writeAppConnectionConfig(t, "local-mysql", `
[connection]
engine = "mysql"
host = "127.0.0.1"
user = "app"
database = "appdb"
password = "`+encrypted+`"
allow_actions = ["query"]
`)
	missingCtx := secret.WithKeyStore(context.Background(), newAppMemoryKeyStore())

	result := runCommandContext(t, missingCtx, nil, "conn", "show", "--config", configDir, "local-mysql")
	if result.code != ExitFailure {
		t.Fatalf("expected exit %d, got %d stdout=%q stderr=%q", ExitFailure, result.code, result.stdout, result.stderr)
	}
	if result.stderr != "" {
		t.Fatalf("expected structured error on stdout, got stderr=%q", result.stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("unmarshal error output: %v\n%s", err, result.stdout)
	}
	if payload["code"] != "secret_key_not_found" {
		t.Fatalf("expected secret_key_not_found, got %#v", payload)
	}
	if strings.Contains(result.stdout, "db-secret") {
		t.Fatalf("error output leaked password: %q", result.stdout)
	}
}

func TestUsageErrorsReturnExit2(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"nope"}, want: `unknown command "nope" for "dbx"`},
		{name: "missing query input", args: []string{"query"}, want: "query requires:"},
		{name: "unknown flag", args: []string{"query", "--bogus"}, want: "unknown flag: --bogus"},
		{name: "missing tx plan", args: []string{"tx", "local-sqlite"}, want: `required flag(s) "plan" not set`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := runCommand(t, nil, tc.args...)
			if result.code != ExitUsage {
				t.Fatalf("expected exit %d, got %d", ExitUsage, result.code)
			}
			if !strings.Contains(result.stderr, tc.want) {
				t.Fatalf("expected stderr to contain %q, got %q", tc.want, result.stderr)
			}
		})
	}
}

func TestRuntimeErrorsReturnExit1WithEnvelope(t *testing.T) {
	configDir := makeSQLiteFixture(t, []string{"query", "update", "schema"}, 3)

	result := runCommand(t, nil, "query", "--config", configDir, "missing-conn", "select 1")
	if result.code != ExitFailure {
		t.Fatalf("expected exit %d, got %d", ExitFailure, result.code)
	}
	if result.stderr != "" {
		t.Fatalf("expected empty stderr, got %q", result.stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("unmarshal runtime error output: %v\n%s", err, result.stdout)
	}
	if payload["kind"] != "error" || payload["code"] != "conn_not_found" {
		t.Fatalf("unexpected runtime error payload: %#v", payload)
	}
}

func TestConnResolveHiddenButExecutable(t *testing.T) {
	configDir := makeSQLiteFixture(t, []string{"query", "update", "schema"}, 3)

	helpResult := runCommand(t, nil, "help", "conn")
	if helpResult.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, helpResult.code)
	}
	if strings.Contains(helpResult.stdout, "resolve") {
		t.Fatalf("resolve should be hidden from conn help, got:\n%s", helpResult.stdout)
	}

	resolveResult := runCommand(t, nil, "conn", "resolve", "--config", configDir, "local-sqlite")
	if resolveResult.code != ExitSuccess {
		t.Fatalf("expected exit %d, got %d", ExitSuccess, resolveResult.code)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(resolveResult.stdout), &payload); err != nil {
		t.Fatalf("unmarshal resolve output: %v\n%s", err, resolveResult.stdout)
	}
	if payload["kind"] != "conn_show" || payload["conn"] != "local-sqlite" {
		t.Fatalf("unexpected resolve payload: %#v", payload)
	}
}

func TestCommandFlagsWorkBeforeAndAfterPositionals(t *testing.T) {
	configDir := makeSQLiteFixture(t, []string{"query", "update", "schema", "admin"}, 5)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"steps":[{"action":"query","sql":"select id from users where id = 1"},{"action":"update","sql":"update users set name = 'ok' where id = 1","max_rows_affected":1}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(t.TempDir(), "users.csv")
	if err := os.WriteFile(csvPath, []byte("id,name\n10,Ada\n11,Bob\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exportPathBefore := filepath.Join(t.TempDir(), "users-before.csv")
	exportPathAfter := filepath.Join(t.TempDir(), "users-after.csv")

	testCases := []struct {
		name      string
		args      []string
		wantKind  string
		checkFile string
	}{
		{name: "query before", args: []string{"query", "--config", configDir, "local-sqlite", "select id from users order by id"}, wantKind: "query_result"},
		{name: "query after", args: []string{"query", "local-sqlite", "select id from users order by id", "--config", configDir}, wantKind: "query_result"},
		{name: "update before", args: []string{"update", "--config", configDir, "--dry-run", "local-sqlite", "update users set name = 'x' where id = 1"}, wantKind: "sql_plan"},
		{name: "update after", args: []string{"update", "local-sqlite", "update users set name = 'x' where id = 1", "--config", configDir, "--dry-run"}, wantKind: "sql_plan"},
		{name: "schema before", args: []string{"schema", "--config", configDir, "--dry-run", "local-sqlite", "alter table users add column email text"}, wantKind: "sql_plan"},
		{name: "schema after", args: []string{"schema", "local-sqlite", "alter table users add column email text", "--config", configDir, "--dry-run"}, wantKind: "sql_plan"},
		{name: "admin before", args: []string{"admin", "--config", configDir, "--dry-run", "local-sqlite", "vacuum"}, wantKind: "sql_plan"},
		{name: "admin after", args: []string{"admin", "local-sqlite", "vacuum", "--config", configDir, "--dry-run"}, wantKind: "sql_plan"},
		{name: "tx before", args: []string{"tx", "--config", configDir, "--dry-run", "local-sqlite", "--plan", planPath}, wantKind: "tx_plan"},
		{name: "tx after", args: []string{"tx", "local-sqlite", "--config", configDir, "--plan", planPath, "--dry-run"}, wantKind: "tx_plan"},
		{name: "import before", args: []string{"import", "--config", configDir, "--dry-run", "file", csvPath, "local-sqlite", "users"}, wantKind: "import_plan"},
		{name: "import after", args: []string{"import", "file", csvPath, "local-sqlite", "users", "--config", configDir, "--dry-run"}, wantKind: "import_plan"},
		{name: "export before", args: []string{"export", "--config", configDir, "table", "users", "local-sqlite", exportPathBefore}, wantKind: "export_result", checkFile: exportPathBefore},
		{name: "export after", args: []string{"export", "table", "users", "local-sqlite", exportPathAfter, "--config", configDir}, wantKind: "export_result", checkFile: exportPathAfter},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := runCommand(t, nil, tc.args...)
			if result.code != ExitSuccess {
				t.Fatalf("expected exit %d, got %d, stdout=%q stderr=%q", ExitSuccess, result.code, result.stdout, result.stderr)
			}
			if result.stderr != "" {
				t.Fatalf("expected empty stderr, got %q", result.stderr)
			}

			var payload map[string]any
			if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
				t.Fatalf("unmarshal output: %v\n%s", err, result.stdout)
			}
			if payload["kind"] != tc.wantKind {
				t.Fatalf("kind = %v, want %s", payload["kind"], tc.wantKind)
			}
			if tc.checkFile != "" {
				if _, err := os.Stat(tc.checkFile); err != nil {
					t.Fatalf("expected exported file %s: %v", tc.checkFile, err)
				}
			}
		})
	}
}

func runCommand(t *testing.T, stdin *bytes.Buffer, args ...string) commandResult {
	t.Helper()
	return runCommandContext(t, context.Background(), stdin, args...)
}

func runCommandContext(t *testing.T, ctx context.Context, stdin *bytes.Buffer, args ...string) commandResult {
	t.Helper()

	var in *bytes.Buffer
	if stdin != nil {
		in = stdin
	} else {
		in = bytes.NewBuffer(nil)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ExecuteContext(ctx, args, in, &stdout, &stderr)

	return commandResult{
		code:   code,
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

type appMemoryKeyStore struct {
	keys map[string][]byte
}

func newAppMemoryKeyStore() *appMemoryKeyStore {
	return &appMemoryKeyStore{keys: map[string][]byte{}}
}

func (s *appMemoryKeyStore) Get(_ context.Context, keyID string) ([]byte, error) {
	key, ok := s.keys[keyID]
	if !ok {
		return nil, secret.ErrSecretKeyNotFound
	}
	return append([]byte(nil), key...), nil
}

func (s *appMemoryKeyStore) Set(_ context.Context, keyID string, key []byte) error {
	s.keys[keyID] = append([]byte(nil), key...)
	return nil
}

func makeSQLiteFixture(t *testing.T, allowActions []string, rows int) string {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	if _, err := sqlDB.Exec(`create table users (id integer primary key, name text)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= rows; i++ {
		if _, err := sqlDB.Exec(`insert into users (id, name) values (?, ?)`, i, "user"); err != nil {
			t.Fatal(err)
		}
	}

	configDir := filepath.Join(dir, "dbx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`
[connection]
engine = "sqlite"
path = %q
allow_actions = [%s]
tags = ["local"]
`, dbPath, quoteStrings(allowActions))
	if err := os.WriteFile(filepath.Join(configDir, "local-sqlite.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	return configDir
}

func writeAppConnectionConfig(t *testing.T, name, raw string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func quoteStrings(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, ", ")
}
