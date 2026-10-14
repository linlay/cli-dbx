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
	"reflect"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/driverprocess"
)

func fixFixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec("create table users (id integer primary key, name text); insert into users values (1, 'original')"); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("[connection]\nengine = \"sqlite\"\npath = %q\nallow_actions = [\"query\", \"update\", \"schema\", \"admin\"]\n", dbPath)
	if err := os.WriteFile(filepath.Join(dir, "local.toml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, database
}

func fixRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() error { runErr = New().Run(context.Background(), args); return nil })
	return out, runErr
}

func TestFixRegressionSQLServerChangePasswordRedacted(t *testing.T) {
	password := "dummy-New p@ss/?#%+"
	login := "dummy-Old p@ss/?#%+"
	for _, key := range []string{"change password", "ChAnGe PaSsWoRd", "password", "PWD"} {
		t.Run(key, func(t *testing.T) {
			query := url.Values{key: {password}, "database": {"sample"}}
			dsn := (&url.URL{Scheme: "sqlserver", Host: "localhost", User: url.UserPassword("app", login), RawQuery: query.Encode()}).String()
			spec := conn.Spec{Engine: "sqlserver", DSN: dsn}
			encodedUser := strings.TrimPrefix(url.UserPassword("user", password).String(), "user:")
			message := dsn + " " + password + " " + url.QueryEscape(password) + " " + url.PathEscape(password) + " " + encodedUser + " " + login
			for name, result := range map[string]string{"dsn": redactDSN(spec), "error": sanitizeError(errors.New(message), spec)} {
				for _, leak := range []string{password, login, url.QueryEscape(password), url.PathEscape(password), encodedUser} {
					if strings.Contains(result, leak) {
						t.Errorf("%s leaked %q: %q", name, leak, result)
					}
				}
			}
			out := captureStdout(t, func() error {
				err := New().renderError("json", false, spec, "", "", errors.New(message))
				var exit *ExitError
				if !errors.As(err, &exit) || exit.Code != 1 {
					t.Errorf("error envelope exit = %v", err)
				}
				return nil
			})
			if strings.Contains(out, password) || strings.Contains(out, url.QueryEscape(password)) {
				t.Errorf("error envelope leaked dummy password: %s", out)
			}
		})
	}
	// Connection display uses the same redactor, with no database or keychain access.
	dir := t.TempDir()
	dsn := "sqlserver://app:dummyLogin@localhost?change%20password=dummyReplacement&database=sample"
	if err := os.WriteFile(filepath.Join(dir, "remote.toml"), []byte(fmt.Sprintf("[connection]\nengine = \"sqlserver\"\ndsn = %q\nallow_actions = [\"query\"]\n", dsn)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := fixRun(t, "conn", "show", "--config", dir, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "dummyLogin") || strings.Contains(out, "dummyReplacement") {
		t.Errorf("conn show leaked: %s", out)
	}
}

func TestFixRegressionJSONImportPreservesLargeInteger(t *testing.T) {
	dir, database := fixFixture(t)
	path := filepath.Join(dir, "rows.json")
	if err := os.WriteFile(path, []byte(`[{"id":9007199254740993,"name":"exact"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := fixRun(t, "import", "--config", dir, "file", path, "local", "users")
	if err != nil {
		t.Fatalf("import failed: %v %s", err, out)
	}
	var id int64
	if err := database.QueryRow("select id from users where name = 'exact'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 9007199254740993 {
		t.Errorf("import changed integer to %d", id)
	}
}

func TestFixRegressionOverlappingSQLServerPasswords(t *testing.T) {
	spec := conn.Spec{Engine: "sqlserver", DSN: "sqlserver://app:dummy-prefix@localhost?change+password=dummy-prefix-new"}
	if got := sanitizeError(errors.New("dummy-prefix-new dummy-prefix"), spec); got != "*** ***" {
		t.Errorf("password suffix retained after redaction: %q", got)
	}
}

func TestFixRegressionJSONNumericScalarsAndWholeDocument(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want any
	}{
		{"9007199254740993", int64(9007199254740993)},
		{"9223372036854775807", int64(9223372036854775807)},
		{"-9223372036854775808", int64(-9223372036854775808)},
		{"9223372036854775808", "9223372036854775808"},
		{"-9223372036854775809", "-9223372036854775809"},
		{"1", int64(1)}, {"1.25", float64(1.25)}, {"0.1", float64(0.1)},
		{"1.234567890123456789", "1.234567890123456789"},
		{"9007199254740993.0", "9007199254740993.0"},
		{"1e1000", "1e1000"}, {"null", nil},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rows.json")
			if err := os.WriteFile(path, []byte(`[{"value":`+tc.raw+`}]`), 0600); err != nil {
				t.Fatal(err)
			}
			_, rows, err := readImportFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rows[0]["value"], tc.want) {
				t.Errorf("got %#v (%T), want %#v (%T)", rows[0]["value"], rows[0]["value"], tc.want, tc.want)
			}
			// All resulting values must traverse the existing private process protocol.
			if _, err := driverprocess.EncodeRows(rows); err != nil {
				t.Errorf("protocol cannot encode: %v", err)
			}
		})
	}
	for _, raw := range []string{`[{"value":1}] []`, `[{"value":1}] trailing`, `[{"value":01}]`, `[{"value":NaN}]`, `[{"value":1e}]`} {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readImportFile(path); err == nil {
			t.Errorf("accepted invalid document %q", raw)
		}
	}
}

func TestFixRegressionJSONImportBoundariesDecimalsAndNull(t *testing.T) {
	dir, database := fixFixture(t)
	if _, err := database.Exec("create table values_test (id integer primary key, decimal_value real, exact_text text, nullable text)"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "values.json")
	raw := `[{"id":9223372036854775807,"decimal_value":1.25,"exact_text":9223372036854775808,"nullable":null},{"id":-9223372036854775808,"decimal_value":0.1,"exact_text":1.234567890123456789,"nullable":"small"},{"id":2,"decimal_value":3.5,"exact_text":-9223372036854775809,"nullable":null}]`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := fixRun(t, "import", "--config", dir, "file", path, "local", "values_test")
	if err != nil {
		t.Fatalf("import failed: %v %s", err, out)
	}
	for _, tc := range []struct {
		id       int64
		decimal  float64
		text     string
		nullable sql.NullString
	}{
		{9223372036854775807, 1.25, "9223372036854775808", sql.NullString{}},
		{-9223372036854775808, 0.1, "1.234567890123456789", sql.NullString{String: "small", Valid: true}},
		{2, 3.5, "-9223372036854775809", sql.NullString{}},
	} {
		var decimal float64
		var text string
		var nullable sql.NullString
		if err := database.QueryRow("select decimal_value, exact_text, nullable from values_test where id = ?", tc.id).Scan(&decimal, &text, &nullable); err != nil {
			t.Fatal(err)
		}
		if decimal != tc.decimal || text != tc.text || nullable != tc.nullable {
			t.Errorf("id %d values = %v, %q, %#v; want %#v", tc.id, decimal, text, nullable, tc)
		}
	}
}

func TestFixRegressionAdminVacuum(t *testing.T) {
	dir, database := fixFixture(t)
	out, err := fixRun(t, "admin", "--config", dir, "local", "VACUUM")
	if err != nil {
		t.Fatalf("VACUUM failed: %v %s", err, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != true || payload["action"] != "admin" {
		t.Errorf("admin envelope = %#v", payload)
	}
	var count int
	if err := database.QueryRow("select count(*) from users").Scan(&count); err != nil || count != 1 {
		t.Errorf("post-VACUUM rows = %d, %v", count, err)
	}
}

func TestFixRegressionUnsupportedFormatHasNoSideEffects(t *testing.T) {
	for _, command := range []string{"update", "schema", "admin", "tx", "import", "export"} {
		t.Run(command, func(t *testing.T) {
			dir, database := fixFixture(t)
			path := filepath.Join(dir, "input.json")
			plan := `{"steps":[{"action":"update","sql":"update users set name = 'changed' where id = 1"}]}`
			args := []string{command, "--config", dir, "--format", "unsupported"}
			switch command {
			case "update":
				args = append(args, "local", "update users set name = 'changed' where id = 1")
			case "schema":
				args = append(args, "local", "create table changed (id integer)")
			case "admin":
				args = append(args, "local", "VACUUM INTO '"+filepath.Join(dir, "side-effect.db")+"'")
			case "tx":
				if err := os.WriteFile(path, []byte(plan), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "local", "--plan", path)
			case "import":
				if err := os.WriteFile(path, []byte(`[{"id":2,"name":"changed"}]`), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "file", path, "local", "users")
			case "export":
				path = filepath.Join(dir, "output.csv")
				if err := os.WriteFile(path, []byte("preserve me"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "table", "users", "local", path)
			}
			out, err := fixRun(t, args...)
			if err == nil {
				t.Fatalf("unsupported format succeeded: %s", out)
			}
			var name string
			if err := database.QueryRow("select name from users where id = 1").Scan(&name); err != nil || name != "original" {
				t.Errorf("changed row despite invalid format: %q %v", name, err)
			}
			var count int
			if err := database.QueryRow("select count(*) from users").Scan(&count); err != nil || count != 1 {
				t.Errorf("changed row count: %d %v", count, err)
			}
			if err := database.QueryRow("select count(*) from sqlite_master where name = 'changed'").Scan(&count); err != nil || count != 0 {
				t.Errorf("schema side effect: %d %v", count, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "side-effect.db")); !os.IsNotExist(err) {
				t.Errorf("admin file side effect: %v", err)
			}
			if command == "export" {
				content, err := os.ReadFile(path)
				if err != nil || string(content) != "preserve me" {
					t.Errorf("export truncated file: %q %v", content, err)
				}
			}
		})
	}
}

func TestFixRegressionFormatValidationPrecedesConnectionResolution(t *testing.T) {
	// Missing config paths and drivers expose any attempt to resolve/start a driver first.
	for _, args := range [][]string{
		{"query", "dsn", "postgres", "postgres://app:dummy@localhost/test", "select 1"},
		{"conn", "test", "remote"}, {"inspect", "schema", "remote"},
		{"export", "table", "users", "remote", filepath.Join(t.TempDir(), "out.csv")},
	} {
		args = append(args, "--config", filepath.Join(t.TempDir(), "missing"), "--format", "unsupported")
		out, err := fixRun(t, args...)
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("expected early format error, got %v and %s", err, out)
		}
	}
}
