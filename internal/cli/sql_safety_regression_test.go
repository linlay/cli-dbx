package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafetyRegressionPolicyBeforeDriver(t *testing.T) {
	dir, _ := driverConfig(t, `"query", "update", "admin"`)
	for _, tc := range []struct{ action, sql, code string }{
		{"query", "with d as (delete from users returning *) select * from d", "unknown_statement"},
		{"update", "with d as (delete from users returning *) select * from d", "unknown_statement"},
		{"query", "explain analyze delete from users", "unknown_statement"},
		{"query", "pragma user_version=42", "action_mismatch"},
		{"update", "with c as (select id from users where id=1) delete from users", "missing_where"},
		{"update", "update users set active=(select 1 where 1=1)", "missing_where"},
		{"update", "update users set name=' where '", "missing_where"},
		{"query", "select '/*' from secrets", "table_blocked"},
		{"query", "select * from users where name='/*' and exists (select 1 from secrets)", "table_blocked"},
		{"update", "delete from users using secrets where users.id=secrets.id", "table_blocked"},
		{"update", "merge into users using secrets on users.id=secrets.id when matched then delete", "table_blocked"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			p := runDriverCLI(t, tc.action, "--config", dir, "remote", tc.sql)
			if p["code"] != tc.code {
				t.Errorf("want %s, got %#v", tc.code, p)
			}
		})
	}
	for _, sql := range []string{"with d as (delete from users returning *) select * from d", "explain analyze delete from users"} {
		plan := map[string]any{"steps": []any{map[string]any{"action": "query", "sql": sql}}}
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		p := runDriverCLI(t, "tx", "--config", dir, "remote", "--plan", path)
		if p["code"] != "unknown_statement" {
			t.Errorf("tx hidden write reached driver: %#v", p)
		}
	}
}

func TestSafetyRegressionSQLiteStateAndReadCompatibility(t *testing.T) {
	dir := makeSQLiteFixture(t, 3)
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(dir), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tc := range []struct{ action, sql, code string }{
		{"query", "pragma user_version=42", "action_mismatch"},
		{"update", "with c as (select id from users where id=1) delete from users", "missing_where"},
		{"update", "update users set name=(select 'changed' where 1=1)", "missing_where"},
		{"update", "update users set name=' where '", "missing_where"},
	} {
		p := runDriverCLI(t, tc.action, "--config", dir, "local-sqlite", tc.sql)
		if p["code"] != tc.code {
			t.Fatalf("%q: %#v", tc.sql, p)
		}
	}
	var version, count int
	if err := db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("select count(*) from users where name='user'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if version != 0 || count != 3 {
		t.Fatalf("blocked SQL changed database: version=%d intact rows=%d", version, count)
	}
	for _, query := range []string{
		"with c as (select id from users where id=1) select * from c",
		"with recursive c(x) as (values (1) union all select x+1 from c where x<3) select * from c",
		"explain query plan select * from users",
		"pragma table_info('users')",
		"pragma table_info = 'users'",
		"pragma integrity_check",
		"select '/*', 'it''s where;' from users",
	} {
		p := runDriverCLI(t, "query", "--config", dir, "local-sqlite", query)
		if p["ok"] != true {
			t.Errorf("read-only SQL rejected %q: %#v", query, p)
		}
	}
}

func TestSafetyRegressionGroupedTablesPolicy(t *testing.T) {
	dir := makeSQLiteFixtureWithExtras(t, 3, "allow_tables = [\"users\"]\n")
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(dir), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("create table secrets(id integer); insert into secrets values(42)"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"select * from users cross join 'secrets'",
		"select * from users cross join ('secrets')",
		"select '/*' from users, 'secrets'",
		"select * from (secrets cross join users)",
		"select * from ((secrets)) cross join users",
		"select * from (secrets, users)",
		"select * from users join (secrets) s on users.id=s.id",
		"select * from (select id from secrets) s cross join users",
	} {
		p := runDriverCLI(t, "query", "--config", dir, "local-sqlite", query)
		if p["code"] != "table_blocked" {
			t.Errorf("grouped relation reached database %q: %#v", query, p)
		}
	}
}

func TestSafetyRegressionImplicitRecursiveCTEPolicy(t *testing.T) {
	dir := makeSQLiteFixtureWithExtras(t, 1, "allow_tables = [\"users\"]\n")
	query := "with c(x) as (select id from users union all select x+1 from c where x<3) select * from c"
	p := runDriverCLI(t, "query", "--config", dir, "local-sqlite", query)
	if p["ok"] != true {
		t.Fatalf("read-only recursive CTE rejected: %#v", p)
	}
	data := p["data"].(map[string]any)
	if data["returned"] != float64(3) {
		t.Fatalf("recursive query rows: %#v", data)
	}
}

func TestSafetyRegressionMergeOptionalIntoPolicy(t *testing.T) {
	dir, _ := driverConfig(t, `"query", "update"`)
	path := filepath.Join(dir, "remote.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "postgres", "sqlserver"), `allow_tables = ["users"]`, `allow_tables = ["users", "top"]`)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"merge secrets using users on secrets.id=users.id when matched then delete;",
		"merge top(1) secrets using users on secrets.id=users.id when matched then delete;",
		"merge top(10) percent secrets using users on secrets.id=users.id when matched then delete;",
	} {
		p := runDriverCLI(t, "update", "--config", dir, "remote", query)
		if p["code"] != "table_blocked" {
			t.Errorf("merge target passed policy: %#v", p)
		}
	}
}
