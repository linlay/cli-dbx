package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/driverprocess"
)

func driverConfig(t *testing.T, actions string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing-drivers")
	raw := fmt.Sprintf("[connection]\nengine = \"postgres\"\ndsn = \"postgres://app:secret@localhost/app\"\ndriver_dir = %q\nallow_actions = [%s]\nallow_tables = [\"users\"]\n", missing, actions)
	if err := os.WriteFile(filepath.Join(dir, "remote.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, missing
}

func runDriverCLI(t *testing.T, args ...string) map[string]any {
	t.Helper()
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), args)
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return payload
}

func TestMissingExternalDriverUsesStableEnvelope(t *testing.T) {
	dir, missing := driverConfig(t, `"query"`)
	payload := runDriverCLI(t, "query", "--config", dir, "remote", "select * from users")
	if payload["code"] != "driver_missing" {
		t.Fatalf("unexpected envelope: %#v", payload)
	}
	if payload["next"] != "install_driver" || !strings.Contains(payload["hint"].(string), "driver") {
		t.Fatalf("missing install guidance: %#v", payload)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["driver_dir"] != missing || data["engine"] != "postgres" {
		t.Fatalf("missing driver location: %#v", payload)
	}
	if strings.Contains(fmt.Sprint(payload), "secret") {
		t.Fatalf("envelope leaked password: %#v", payload)
	}
}

func TestDriverErrorsKeepStructuredCodes(t *testing.T) {
	for _, code := range []string{"driver_missing", "driver_invalid", "driver_integrity_failed", "driver_protocol_mismatch", "driver_start_failed", "driver_protocol_error", "driver_process_failed"} {
		err := fmt.Errorf("wrapped: %w", &driverprocess.Error{Code: code, Message: "driver failed"})
		if got := classifyErrorCode(err); got != code {
			t.Fatalf("%s classified as %s", code, got)
		}
		if summaryForError(code) == "database command failed" || hintForCode(code) == "Check the SQL text, target connection, or input file." {
			t.Fatalf("%s lacks driver guidance", code)
		}
	}
	if got := classifyErrorCode(&driverprocess.Error{Code: "database_error", Message: "syntax error"}); got != "sql_error" {
		t.Fatalf("remote database error classified as %s", got)
	}
}

func TestExternalDriverPolicyChecksPrecedeLaunch(t *testing.T) {
	dir, _ := driverConfig(t, `"query", "update"`)
	for _, tc := range []struct {
		name string
		args []string
		code string
		ok   bool
	}{
		{"unsafe write", []string{"update", "--config", dir, "remote", "delete from users"}, "missing_where", false},
		{"action policy", []string{"schema", "--config", dir, "remote", "create table users (id int)"}, "action_blocked", false},
		{"multiple statements", []string{"query", "--config", dir, "remote", "select * from users; select * from users"}, "multiple_statements_blocked", false},
		{"table policy", []string{"query", "--config", dir, "remote", "select * from secrets"}, "table_blocked", false},
		{"select into query", []string{"query", "--config", dir, "remote", "select * into backup from users"}, "action_mismatch", false},
		{"select into update", []string{"update", "--config", dir, "remote", "select * into backup from users"}, "action_mismatch", false},
		{"dry run", []string{"query", "--config", dir, "--dry-run", "remote", "select * from users"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := runDriverCLI(t, tc.args...)
			if payload["ok"] != tc.ok || (tc.code != "" && payload["code"] != tc.code) {
				t.Fatalf("unexpected envelope: %#v", payload)
			}
		})
	}
}

func TestSanitizeExternalDSNPasswords(t *testing.T) {
	password := "p@ss /?#%+"
	for _, tc := range []struct{ engine, dsn string }{
		{"postgres", (&url.URL{Scheme: "postgres", User: url.UserPassword("app", password), Host: "localhost", Path: "/db"}).String()},
		{"oracle", (&url.URL{Scheme: "oracle", User: url.UserPassword("app", password), Host: "localhost", Path: "/db"}).String()},
		{"sqlserver", (&url.URL{Scheme: "sqlserver", User: url.UserPassword("app", password), Host: "localhost", RawQuery: "database=db"}).String()},
		{"dm", "dm://app:" + password + "@localhost:5236?connectTimeout=15000"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			spec := conn.Spec{Engine: tc.engine, DSN: tc.dsn}
			message := tc.dsn + " " + password + " " + url.QueryEscape(password) + " " + url.PathEscape(password) + " " + url.UserPassword("app", password).String()
			clean := sanitizeError(errors.New(message), spec)
			for _, leak := range []string{password, url.QueryEscape(password), url.PathEscape(password), url.UserPassword("app", password).String()} {
				if strings.Contains(clean, leak) {
					t.Fatalf("sanitized error leaked %q: %q", leak, clean)
				}
			}
			if strings.Contains(redactDSN(spec), password) || strings.Contains(redactDSN(spec), url.UserPassword("app", password).String()) {
				t.Fatalf("redacted DSN leaked password: %q", redactDSN(spec))
			}
		})
	}
}

func TestExportQuotesQualifiedSQLServerTable(t *testing.T) {
	got, err := quoteExportTable("sqlserver", "sales.orders")
	if err != nil || got != "[sales].[orders]" {
		t.Fatalf("quoted table = %q, %v", got, err)
	}
}

func TestSQLServerBatchesBlockedInQueryAndTransaction(t *testing.T) {
	dir := t.TempDir()
	raw := "[connection]\nengine = \"sqlserver\"\nhost = \"127.0.0.1\"\nuser = \"app\"\nallow_actions = [\"query\",\"update\"]\ndriver_dir = " + fmt.Sprintf("%q", filepath.Join(dir, "missing-drivers")) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "remote.toml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"SELECT 1 AS n DELETE FROM users",
		"WITH c AS (SELECT 1 AS n) SELECT n FROM c DELETE FROM users",
		"SELECT 1 DISABLE TRIGGER ALL ON users",
		"SELECT 1 /* outer /* inner */ ( */ DELETE FROM users",
		"SELECT 1 -- hidden\rDELETE FROM users",
	} {
		payload := runDriverCLI(t, "query", "--config", dir, "--dry-run", "remote", text)
		if payload["code"] != "multiple_statements_blocked" {
			t.Fatalf("query batch accepted: %+v", payload)
		}
		plan := filepath.Join(dir, "plan.json")
		b, _ := json.Marshal(map[string]any{"steps": []map[string]string{{"action": "query", "sql": text}}})
		if err := os.WriteFile(plan, b, 0600); err != nil {
			t.Fatal(err)
		}
		payload = runDriverCLI(t, "tx", "--config", dir, "--dry-run", "remote", "--plan", plan)
		if payload["code"] != "multiple_statements_blocked" {
			t.Fatalf("tx query batch accepted: %+v", payload)
		}
	}
}

func TestCLIImportLimitRollsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err = database.Exec("create table imported(id integer)"); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("[connection]\nengine=\"sqlite\"\npath=%q\nallow_actions=[\"query\",\"update\"]\n", path)
	if err := os.WriteFile(filepath.Join(dir, "local.toml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(dir, "rows.csv")
	if err := os.WriteFile(csv, []byte("id\n1\n2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := runDriverCLI(t, "import", "--config", dir, "--max-rows-affected", "1", "file", csv, "local", "imported")
	if payload["ok"] != false {
		t.Fatalf("import limit ignored: %+v", payload)
	}
	var count int
	if err = database.QueryRow("select count(*) from imported").Scan(&count); err != nil || count != 0 {
		t.Fatalf("import rollback count %d, err=%v", count, err)
	}
}

func TestPostgresKeywordDSNDoesNotLeakInShowOrErrors(t *testing.T) {
	dir := t.TempDir()
	dsn := "host=localhost user=app password='s3cret \\'quoted\\'' dbname=app"
	raw := "[connection]\nengine = \"postgres\"\ndsn = " + strconv.Quote(dsn) + "\nallow_actions = [\"query\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "remote.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := runDriverCLI(t, "conn", "show", "--config", dir, "remote")
	if payload["ok"] != true || strings.Contains(fmt.Sprint(payload), "s3cret") {
		t.Fatalf("connection show leaked keyword DSN: %#v", payload)
	}
	spec := conn.Spec{Engine: "postgres", DSN: dsn}
	if got := redactDSN(spec); got != "[redacted connection]" {
		t.Fatalf("redacted DSN = %q", got)
	}
	for _, message := range []string{dsn, "driver rejected password s3cret", "password='s3cret \\'quoted\\''"} {
		if got := sanitizeError(errors.New(message), spec); strings.Contains(got, "s3cret") {
			t.Fatalf("error leaked keyword password: %q", got)
		}
	}
}

func TestPostgresURLQueryPasswordDoesNotLeak(t *testing.T) {
	spec := conn.Spec{Engine: "postgres", DSN: "postgres://localhost/app?user=app&password=s3cret"}
	if got := redactDSN(spec); strings.Contains(got, "s3cret") {
		t.Fatalf("redacted DSN leaked URL query password: %q", got)
	}
	if got := sanitizeError(errors.New("driver reported s3cret for "+spec.DSN), spec); strings.Contains(got, "s3cret") {
		t.Fatalf("error leaked URL query password: %q", got)
	}
}

func TestPostgresRedactionPreservesWriteUncertainty(t *testing.T) {
	for _, dsn := range []string{"host=localhost password=s3cret", "postgres://localhost/app?password=s3cret"} {
		err := errors.New("driver lost connection with s3cret; verify database state before retrying a write")
		got := sanitizeError(err, conn.Spec{Engine: "postgres", DSN: dsn})
		if strings.Contains(got, "s3cret") || !strings.Contains(got, "verify database state before retrying a write") {
			t.Fatalf("unsafe or incomplete guidance: %s", got)
		}
	}
}

func TestODBCRedactionHidesVendorDetails(t *testing.T) {
	spec := conn.Spec{Engine: "oracle", Driver: "odbc", ODBC: &conn.ODBCConfig{Password: "s3cret", Options: map[string]string{"Token": "option-secret"}}}
	if got := redactDSN(spec); got != "[redacted connection]" {
		t.Fatalf("dsn = %q", got)
	}
	got := sanitizeError(errors.New("ODBC failure PWD=s3cret Token=option-secret; verify database state before retrying a write"), spec)
	if strings.Contains(got, "s3cret") || strings.Contains(got, "option-secret") || !strings.Contains(got, "verify database state before retrying a write") {
		t.Fatalf("redaction = %q", got)
	}
}

func TestODBCRedactionPreservesDriverInstallError(t *testing.T) {
	spec := conn.Spec{Engine: "oracle", Driver: "odbc", ODBC: &conn.ODBCConfig{Password: "s3cret"}}
	err := &driverprocess.Error{Code: "driver_missing", Message: "oracle driver executable is missing; install its driver package"}
	got := sanitizeError(err, spec)
	if !strings.Contains(got, "install its driver package") {
		t.Fatalf("lost installation guidance: %q", got)
	}
}

func TestDriverPathMissingReportsSelectedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dbx-driver-postgres-2.0.0")
	raw := "[connection]\nengine='postgres'\nhost='localhost'\nuser='app'\ndriver_path='dbx-driver-postgres-2.0.0'\nallow_actions=['query']\n"
	if err := os.WriteFile(filepath.Join(dir, "remote.toml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	payload := runDriverCLI(t, "conn", "test", "--config", dir, "remote")
	data, ok := payload["data"].(map[string]any)
	if payload["code"] != "driver_missing" || !ok || data["driver_path"] != path {
		t.Fatalf("missing selected driver information: %#v", payload)
	}
}
