package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestHelpOutputsUseCompactTaskCards(t *testing.T) {
	root := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"help"})
	})
	if !strings.Contains(root, "dbx exec local-pg 'select * from users limit 5'") {
		t.Fatalf("root help should point to exec usage, got:\n%s", root)
	}
	if strings.Contains(root, "self-describing") || strings.Contains(root, "external docs") {
		t.Fatalf("root help should omit design commentary, got:\n%s", root)
	}

	examples := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"help", "examples"})
	})
	if !strings.Contains(examples, "PostgreSQL: inspect users") || !strings.Contains(examples, "MySQL: import customers.csv") {
		t.Fatalf("help examples should include postgres and mysql examples, got:\n%s", examples)
	}

	execHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--help"})
	})
	if !strings.Contains(execHelp, "Run any SQL") || !strings.Contains(execHelp, "Lantern   read only; default for selects") {
		t.Fatalf("exec help should include compact mode guidance, got:\n%s", execHelp)
	}
	if !strings.Contains(execHelp, "Multiple statements are blocked by default.") || !strings.Contains(execHelp, "Use --cursor <n> to continue a paged read.") {
		t.Fatalf("exec help should include default safety and cursor guidance, got:\n%s", execHelp)
	}
	if strings.Contains(execHelp, "--require-ack") || strings.Contains(execHelp, "--verbose-errors") {
		t.Fatalf("exec help should not mention removed flags, got:\n%s", execHelp)
	}
	if strings.Contains(execHelp, "Common next actions") || strings.Contains(execHelp, "agent-first") {
		t.Fatalf("exec help should omit design sections, got:\n%s", execHelp)
	}

	connHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"conn", "--help"})
	})
	if !strings.Contains(connHelp, "test <name>") {
		t.Fatalf("conn help should describe test usage, got:\n%s", connHelp)
	}

	inspectHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "--help"})
	})
	if !strings.Contains(inspectHelp, "inspect table") {
		t.Fatalf("inspect help should describe table inspection, got:\n%s", inspectHelp)
	}
}

func TestExecDefaultAgentOutput(t *testing.T) {
	configPath := makeSQLiteFixture(t, 25)
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "local-sqlite", "select id, name from users order by id"})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if got := payload["kind"]; got != "exec_result" {
		t.Fatalf("kind = %v, want exec_result", got)
	}
	if got := payload["more"]; got != true {
		t.Fatalf("more = %v, want true", got)
	}
	if _, ok := payload["engine"]; ok {
		t.Fatalf("default agent output should omit engine: %v", payload)
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("data should be an object: %#v", payload["data"])
	}
	rows, ok := data["rows"].([]any)
	if !ok || len(rows) != 5 {
		t.Fatalf("rows should contain 5 samples, got %#v", data["rows"])
	}
	if got := int(data["seen"].(float64)); got != 25 {
		t.Fatalf("seen = %d, want 25", got)
	}
	if nextCursor := data["next_cursor"]; nextCursor != "20" {
		t.Fatalf("next_cursor = %v, want 20", nextCursor)
	}
}

func TestExecUsesDefaultConnectionWhenConnIsOmitted(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "select id from users order by id"})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["conn"] != "local-sqlite" {
		t.Fatalf("expected default connection local-sqlite, got %#v", payload)
	}
}

func TestInspectUsesDefaultConnectionWhenConnIsOmitted(t *testing.T) {
	configPath := makeSQLiteRelationFixture(t)
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "table", "--config", configPath, "orders"})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["conn"] != "local-sqlite" {
		t.Fatalf("expected default connection local-sqlite, got %#v", payload)
	}
}

func TestExecVerboseIncludesMetaAndErrorsUseEnvelope(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)

	verbose := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "--verbose", "local-sqlite", "select id from users order by id"})
	})
	var verbosePayload map[string]any
	if err := json.Unmarshal([]byte(verbose), &verbosePayload); err != nil {
		t.Fatalf("unmarshal verbose output: %v", err)
	}
	if verbosePayload["engine"] != "sqlite" {
		t.Fatalf("verbose engine = %v, want sqlite", verbosePayload["engine"])
	}
	if _, ok := verbosePayload["meta"]; !ok {
		t.Fatalf("verbose output should include meta: %#v", verbosePayload)
	}

	errOut := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"exec", "--config", configPath, "--mode", "Tweezers", "local-sqlite", "delete from users"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var errPayload map[string]any
	if err := json.Unmarshal([]byte(errOut), &errPayload); err != nil {
		t.Fatalf("unmarshal error output: %v\n%s", err, errOut)
	}
	if errPayload["kind"] != "error" || errPayload["code"] != "missing_where" {
		t.Fatalf("unexpected business error envelope: %#v", errPayload)
	}
	if _, ok := errPayload["warnings"]; !ok {
		t.Fatalf("verbose error output should include warnings: %#v", errPayload)
	}
}

func TestExecParsesFlagsAfterPositionals(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "local-sqlite", "update users set name = 'aaa' where id = 1", "--mode", "Tweezers"})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["kind"] != "write_result" {
		t.Fatalf("expected trailing --mode to be parsed, got %#v", payload)
	}
}

func TestExecBlocksMultiStatementButAllowsScopedWriteWithoutAck(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)

	multiOut := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"exec", "--config", configPath, "local-sqlite", "select 1; select 2"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var multiPayload map[string]any
	if err := json.Unmarshal([]byte(multiOut), &multiPayload); err != nil {
		t.Fatalf("unmarshal multi output: %v", err)
	}
	if multiPayload["code"] != "multiple_statements_blocked" {
		t.Fatalf("expected multi statement block, got %#v", multiPayload)
	}

	writeOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "--mode", "Tweezers", "local-sqlite", "update users set name = 'x' where id = 1"})
	})
	var writePayload map[string]any
	if err := json.Unmarshal([]byte(writeOut), &writePayload); err != nil {
		t.Fatalf("unmarshal write output: %v", err)
	}
	if writePayload["kind"] != "write_result" {
		t.Fatalf("expected successful scoped write, got %#v", writePayload)
	}
}

func TestSQLErrorIncludesDriverReasonByDefault(t *testing.T) {
	configPath := makeSQLiteFixture(t, 1)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(filepath.Dir(filepath.Dir(cwd)), "users.csv")
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"import", "--config", configPath, "--mode", "Tweezers", "file", csvPath, "local-sqlite", "users"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if payload["code"] != "sql_error" {
		t.Fatalf("expected sql_error, got %#v", payload)
	}
	if _, ok := payload["warnings"]; !ok {
		t.Fatalf("expected raw driver reason by default, got %#v", payload)
	}
}

func TestImportUsesDefaultConnectionWhenConnIsOmitted(t *testing.T) {
	configPath := makeSQLiteFixture(t, 0)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(filepath.Dir(filepath.Dir(cwd)), "users.csv")
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"import", "--config", configPath, "--mode", "Tweezers", "file", csvPath, "users"})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["conn"] != "local-sqlite" || payload["kind"] != "import_result" {
		t.Fatalf("expected default-conn import result, got %#v", payload)
	}
}

func TestExecCursorAndInspectRelations(t *testing.T) {
	configPath := makeSQLiteRelationFixture(t)

	pageOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "--cursor", "20", "local-sqlite", "select id, name from users order by id"})
	})
	var pagePayload map[string]any
	if err := json.Unmarshal([]byte(pageOut), &pagePayload); err != nil {
		t.Fatalf("unmarshal page output: %v", err)
	}
	data := pagePayload["data"].(map[string]any)
	rows := data["rows"].([]any)
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows from cursor page, got %d", len(rows))
	}

	tableOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "table", "--config", configPath, "local-sqlite", "orders"})
	})
	var tablePayload map[string]any
	if err := json.Unmarshal([]byte(tableOut), &tablePayload); err != nil {
		t.Fatalf("unmarshal inspect table output: %v", err)
	}
	tableData := tablePayload["data"].(map[string]any)
	if _, ok := tableData["primary_key"]; !ok {
		t.Fatalf("expected primary key in inspect table output: %#v", tableData)
	}
	if _, ok := tableData["foreign_keys"]; !ok {
		t.Fatalf("expected foreign keys in inspect table output: %#v", tableData)
	}

	schemaOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "schema", "--config", configPath, "local-sqlite"})
	})
	var schemaPayload map[string]any
	if err := json.Unmarshal([]byte(schemaOut), &schemaPayload); err != nil {
		t.Fatalf("unmarshal inspect schema output: %v", err)
	}
	schemaData := schemaPayload["data"].(map[string]any)
	relations := schemaData["relations"].([]any)
	if len(relations) == 0 {
		t.Fatalf("expected relations in inspect schema output: %#v", schemaData)
	}
}

func TestExportUsesDefaultConnectionWhenConnIsOmitted(t *testing.T) {
	configPath := makeSQLiteFixture(t, 2)
	outFile := filepath.Join(t.TempDir(), "users.csv")
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"export", "--config", configPath, "--format", "csv", "table", "users", outFile})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["conn"] != "local-sqlite" || payload["kind"] != "export_result" {
		t.Fatalf("expected default-conn export result, got %#v", payload)
	}
}

func TestSQLCommandIsUnknown(t *testing.T) {
	configPath := makeSQLiteFixture(t, 2)
	err := New().Run(context.Background(), []string{"sql", "--config", configPath, "local-sqlite", "select id from users order by id"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "sql"`) {
		t.Fatalf("sql should be removed, got: %v", err)
	}
}

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	buf, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return string(buf)
}

func makeSQLiteFixture(t *testing.T, rows int) string {
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
	configPath := filepath.Join(dir, "config.toml")
	raw := `
default_connection = "local-sqlite"

[connections.local-sqlite]
engine = "sqlite"
path = "` + dbPath + `"
mode = "Lantern"
tags = ["local"]
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func makeSQLiteRelationFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relations.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`pragma foreign_keys = on`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`create table users (id integer primary key, name text not null)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`create table orders (id integer primary key, user_id integer not null, total integer default 0, foreign key(user_id) references users(id))`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		if _, err := sqlDB.Exec(`insert into users (id, name) values (?, ?)`, i, "user"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		if _, err := sqlDB.Exec(`insert into orders (id, user_id, total) values (?, ?, ?)`, i, i, 10); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(dir, "config.toml")
	raw := `
default_connection = "local-sqlite"

[connections.local-sqlite]
engine = "sqlite"
path = "` + dbPath + `"
mode = "Lantern"
tags = ["local"]
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}
