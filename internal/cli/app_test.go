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
	if !strings.Contains(root, "dbx exec --conn local-pg --sql") {
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
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "--sql", "select id, name from users order by id"})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if got := payload["kind"]; got != "query_result" {
		t.Fatalf("kind = %v, want query_result", got)
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
}

func TestExecVerboseIncludesMetaAndErrorsUseEnvelope(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)

	verbose := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"exec", "--config", configPath, "--verbose", "--sql", "select id from users order by id"})
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
		err := New().Run(context.Background(), []string{"exec", "--config", configPath, "--mode", "Tweezers", "--sql", "delete from users", "--verbose-errors"})
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

func TestSQLCommandIsUnknown(t *testing.T) {
	configPath := makeSQLiteFixture(t, 2)
	err := New().Run(context.Background(), []string{"sql", "--config", configPath, "--sql", "select id from users order by id"})
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
