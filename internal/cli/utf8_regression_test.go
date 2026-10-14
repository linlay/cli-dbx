package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const testUTF8BOM = "\xef\xbb\xbf"

func TestReadSQLRemovesOnlyLeadingUTF8BOM(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"plain", "select 'a\ufeffb'", "select 'a\ufeffb'"},
		{"BOM", testUTF8BOM + "select 'a\ufeffb'", "select 'a\ufeffb'"},
		{"double BOM", testUTF8BOM + testUTF8BOM + "select 1", testUTF8BOM + "select 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "query.sql")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readSQL("query", "", path)
			if err != nil || got != tc.want {
				t.Fatalf("readSQL = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	inline := testUTF8BOM + "select 1"
	if got, err := readSQL("query", inline, ""); err != nil || got != inline {
		t.Fatalf("inline SQL changed: %q, %v", got, err)
	}
}

func TestCLIBOMSQLPreservesEmbeddedFEFF(t *testing.T) {
	dir, _ := fixFixture(t)
	queryPath := filepath.Join(dir, "query.sql")
	if err := os.WriteFile(queryPath, []byte(testUTF8BOM+"select 'a\ufeffb' as value from users where id = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := fixRun(t, "query", "--config", dir, "file", "local", queryPath)
	if err != nil || !strings.Contains(out, "a\ufeffb") {
		t.Fatalf("BOM SQL failed or changed data: %v %s", err, out)
	}
}

func TestCLIBOMTransactionPreservesEmbeddedFEFF(t *testing.T) {
	dir, database := fixFixture(t)
	planPath := filepath.Join(dir, "plan.json")
	plan := testUTF8BOM + `{"steps":[{"action":"update","sql":"update users set name = 'a` + "\ufeff" + `b' where id = 1","max_rows_affected":1}]}` + "\r\n\t "
	if err := os.WriteFile(planPath, []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := fixRun(t, "tx", "--config", dir, "local", "--plan", planPath)
	if err != nil {
		t.Fatalf("BOM transaction failed: %v %s", err, out)
	}
	var name string
	if err := database.QueryRow("select name from users where id = 1").Scan(&name); err != nil || name != "a\ufeffb" {
		t.Fatalf("transaction stored name = %q, %v", name, err)
	}
}

func TestReadImportFileBOMPreservesHeadersAndNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		columns   []string
		rows      []map[string]any
	}{
		{"rows.csv", testUTF8BOM + "id,name\r\n2,a\ufeffb\r\n", []string{"id", "name"}, []map[string]any{{"id": "2", "name": "a\ufeffb"}}},
		{"rows.json", testUTF8BOM + `[{"id":9007199254740993,"name":"a` + "\ufeff" + `b"}]`, []string{"id", "name"}, []map[string]any{{"id": int64(9007199254740993), "name": "a\ufeffb"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			columns, rows, err := readImportFile(path)
			if filepath.Ext(path) == ".json" {
				sort.Strings(columns)
			}
			if err != nil || !reflect.DeepEqual(columns, tc.columns) || !reflect.DeepEqual(rows, tc.rows) {
				t.Fatalf("import = %#v, %#v, %v; want %#v, %#v", columns, rows, err, tc.columns, tc.rows)
			}
		})
	}
}

func TestCLIBOMImportsPreserveLargeIntegerAndEmbeddedFEFF(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		id        int64
	}{
		{"rows.json", testUTF8BOM + `[{"id":9007199254740993,"name":"a` + "\ufeff" + `b"}]`, 9007199254740993},
		{"rows.csv", testUTF8BOM + "id,name\r\n2,a\ufeffb\r\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, database := fixFixture(t)
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := fixRun(t, "import", "--config", dir, "file", path, "local", "users")
			if err != nil {
				t.Fatalf("BOM import failed: %v %s", err, out)
			}
			var id int64
			if err := database.QueryRow("select id from users where name = ?", "a\ufeffb").Scan(&id); err != nil || id != tc.id {
				t.Fatalf("import stored id = %d, %v; want %d", id, err, tc.id)
			}
		})
	}
}

func TestCLITransactionRejectsInvalidJSONBeforeWrite(t *testing.T) {
	valid := `{"steps":[{"action":"update","sql":"update users set name = 'changed' where id = 1","max_rows_affected":1}]}`
	for _, tc := range []struct{ name, raw, errorText string }{
		{"trailing object", valid + ` {"steps":[]}`, "single JSON document"},
		{"trailing junk", valid + " junk", "invalid character"},
		{"BOM trailing object", testUTF8BOM + valid + ` {}`, "single JSON document"},
		{"BOM trailing junk", testUTF8BOM + valid + " junk", "invalid character 'j'"},
		{"unknown root field", strings.Replace(valid, `{"steps":`, `{"unexpected":true,"steps":`, 1), "unknown field"},
		{"unknown step field", strings.Replace(valid, `"action":"update"`, `"unexpected":true,"action":"update"`, 1), "unknown field"},
		{"BOM unknown root field", testUTF8BOM + strings.Replace(valid, `{"steps":`, `{"unexpected":true,"steps":`, 1), "unknown field"},
		{"BOM unknown step field", testUTF8BOM + strings.Replace(valid, `"action":"update"`, `"unexpected":true,"action":"update"`, 1), "unknown field"},
		{"double BOM", testUTF8BOM + testUTF8BOM + valid, "invalid character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, database := fixFixture(t)
			path := filepath.Join(dir, "plan.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := fixRun(t, "tx", "--config", dir, "local", "--plan", path)
			if err == nil || !strings.Contains(out, tc.errorText) {
				t.Errorf("invalid transaction returned %v %s; want %q", err, out, tc.errorText)
			}
			var name string
			if err := database.QueryRow("select name from users where id = 1").Scan(&name); err != nil || name != "original" {
				t.Errorf("rejected transaction changed name to %q, %v", name, err)
			}
		})
	}
}

func TestCLIImportRejectsBOMJSONTrailingContentBeforeWrite(t *testing.T) {
	valid := testUTF8BOM + `[{"id":2,"name":"changed"}]`
	for _, tc := range []struct{ name, raw, errorText string }{
		{"trailing array", valid + ` []`, "single JSON document"},
		{"trailing junk", valid + " junk", "invalid character 'j'"},
		{"double BOM", testUTF8BOM + valid, "invalid character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, database := fixFixture(t)
			path := filepath.Join(dir, "rows.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := fixRun(t, "import", "--config", dir, "file", path, "local", "users")
			if err == nil || !strings.Contains(out, tc.errorText) {
				t.Errorf("invalid import returned %v %s; want %q", err, out, tc.errorText)
			}
			var count int
			if err := database.QueryRow("select count(*) from users").Scan(&count); err != nil || count != 1 {
				t.Errorf("rejected import changed row count to %d, %v", count, err)
			}
		})
	}
}
