package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/conn"
)

type odbcUnknownCountRunner struct{ err error }

func (r odbcUnknownCountRunner) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return odbcUnknownCountResult{r.err}, nil
}

// A local SQLite ODBC library exercises the transport, not vendor SQL dialects.
// No dependency is installed by this test and the database lives in t.TempDir().
func TestODBCNativeTransactions(t *testing.T) {
	library := os.Getenv("DBX_TEST_SQLITE_ODBC_LIBRARY")
	if library == "" {
		t.Skip("set DBX_TEST_SQLITE_ODBC_LIBRARY and build with ODBC enabled")
	}
	spec := conn.Spec{
		Engine: "sqlserver", Driver: "odbc", Timeout: 10 * time.Second,
		DSN: fmt.Sprintf("DRIVER={%s};Database=%s;", library, filepath.Join(t.TempDir(), "odbc.db")),
	}
	ctx := context.Background()
	if _, err := Execute(ctx, spec, "CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance INTEGER, label VARCHAR(100))"); err != nil {
		t.Fatal(err)
	}
	n, err := ImportRows(ctx, spec, "accounts", []string{"id", "balance", "label"}, []map[string]any{
		{"id": int64(1), "balance": int64(10), "label": "中文账户"},
		{"id": int64(2), "balance": int64(20), "label": "second"},
	})
	if err != nil || n != 2 {
		t.Fatalf("import: count=%d err=%v", n, err)
	}
	if _, err := ExecuteGuarded(ctx, spec, "UPDATE accounts SET balance=99 WHERE id>0", 1); err == nil {
		t.Fatal("expected affected-row guard failure")
	}
	assertRows := func(query string, count int) {
		t.Helper()
		result, err := Query(ctx, spec, query, 0, 10)
		if err != nil || result.RowCount != count {
			t.Fatalf("query %q: count=%d err=%v", query, result.RowCount, err)
		}
	}
	assertRows("SELECT id FROM accounts WHERE balance=99", 0)
	assertRows("SELECT id FROM accounts WHERE label='中文账户'", 1)
	plan := TxPlan{Steps: []TxStep{
		{Action: action.Update, SQL: "UPDATE accounts SET balance=30 WHERE id=1", MaxRowsAffected: 1},
		{Action: action.Update, SQL: "UPDATE accounts SET balance=50 WHERE id>0", MaxRowsAffected: 1},
	}}
	if _, err := RunTxPlan(ctx, spec, plan, 10, 1); err == nil {
		t.Fatal("expected transaction guard failure")
	}
	assertRows("SELECT id FROM accounts WHERE balance=10", 1)
	plan.Steps = plan.Steps[:1]
	if _, err := RunTxPlan(ctx, spec, plan, 10, 1); err != nil {
		t.Fatal(err)
	}
	assertRows("SELECT id FROM accounts WHERE balance=30", 1)
}

type odbcUnknownCountResult struct{ err error }

func (r odbcUnknownCountResult) LastInsertId() (int64, error) { return 0, nil }
func (r odbcUnknownCountResult) RowsAffected() (int64, error) { return -1, r.err }
func TestODBCGuardRejectsUnknownAffectedRows(t *testing.T) {
	for _, cause := range []error{nil, errors.New("row count unavailable")} {
		if _, err := executeWithRunner(context.Background(), odbcUnknownCountRunner{cause}, "update accounts set x=1 where id=1", 1, true); err == nil {
			t.Fatal("unknown affected rows must not pass write protection")
		}
	}
}

func TestBuiltinDatabaseHandles(t *testing.T) {
	// This exercises driver registration and DSN parsing, without connecting.
	for _, spec := range []conn.Spec{
		{Engine: "mysql", Driver: "mysql", DSN: "app:@tcp(localhost:3306)/finance?parseTime=true"},
		{Engine: "postgres", Driver: "pgx", DSN: "postgres://app@localhost:5432/finance?sslmode=require"},
		{Engine: "sqlite", Driver: "sqlite", DSN: "file::memory:"},
		{Engine: "oracle", Driver: "oracledb", DSN: "oracle://app:p%40ss%2Fword@localhost:1521/ORCL"},
		{Engine: "sqlserver", Driver: "sqlserver", DSN: "sqlserver://app:p%40ss@localhost:1433?database=finance"},
		{Engine: "dm", Driver: "dm", DSN: "dm://app:p@ss?/ #%&+@localhost:5236?connectTimeout=15000"},
	} {
		t.Run(spec.Engine, func(t *testing.T) {
			handle, err := Open(spec)
			if err != nil {
				t.Fatal(err)
			}
			handle.Close()
		})
	}
}

func TestNativeImportQualifiedTable(t *testing.T) {
	// SQLite accepts these identifier and positional bind syntaxes. Oracle
	// and SQL Server named parameters are covered by the bind tests below.
	// This tests DBX SQL generation, not server compatibility.
	for _, engine := range []string{"mysql", "postgres", "sqlite", "dm"} {
		t.Run(engine, func(t *testing.T) {
			spec := conn.Spec{Engine: engine, Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "import.db"), Timeout: time.Second}
			ctx := context.Background()
			if _, err := Execute(ctx, spec, "create table items (id integer, label text)"); err != nil {
				t.Fatal(err)
			}
			count, err := ImportRows(ctx, spec, "main.items", []string{"id", "label"}, []map[string]any{{"id": 1, "label": "中文"}})
			if err != nil || count != 1 {
				t.Fatalf("import count=%d error=%v", count, err)
			}
		})
	}
}

func TestCatalogSelectionDoesNotDependOnODBC(t *testing.T) {
	for _, engine := range []string{"oracle", "dm"} {
		t.Run(engine, func(t *testing.T) {
			spec := conn.Spec{Engine: engine, Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "catalog.db"), Timeout: time.Second}
			ctx := context.Background()
			for _, query := range []string{
				"create table all_users (username text)", "insert into all_users values ('APP')",
				"create table sysobjects (name text, type$ text)", "insert into sysobjects values ('APP', 'SCH')",
			} {
				if _, err := Execute(ctx, spec, query); err != nil {
					t.Fatal(err)
				}
			}
			schemas, err := ListSchemas(ctx, spec)
			if err != nil || len(schemas) != 1 || schemas[0] != "APP" {
				t.Fatalf("native catalog dispatch: %v, %v", schemas, err)
			}
		})
	}
}

func TestListTablesCatalogSchema(t *testing.T) {
	// A SQLite fixture exercises the shared catalog query and schema selection,
	// without claiming compatibility with a live Dameng or Oracle server.
	spec := conn.Spec{Engine: "dm", Driver: "sqlite", Schema: "APP", DSN: filepath.Join(t.TempDir(), "catalog.db"), Timeout: time.Second}
	ctx := context.Background()
	for _, query := range []string{
		"create table all_objects (owner text, object_name text, object_type text)",
		"insert into all_objects values ('APP', 'ITEMS', 'TABLE'), ('APP', 'IDX', 'INDEX'), ('OTHER', 'REPORT', 'VIEW')",
	} {
		if _, err := Execute(ctx, spec, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		schema string
		want   TableInfo
	}{
		{"", TableInfo{Schema: "APP", Name: "ITEMS", Type: "TABLE"}},
		{"OTHER", TableInfo{Schema: "OTHER", Name: "REPORT", Type: "VIEW"}},
	} {
		tables, err := ListTables(ctx, spec, tc.schema)
		if err != nil || len(tables) != 1 {
			t.Fatalf("schema %q: tables=%v, err=%v", tc.schema, tables, err)
		}
		if got := tables[0]; got.Schema != tc.want.Schema || got.Name != tc.want.Name || got.Type != tc.want.Type {
			t.Fatalf("schema %q: got %+v, want %+v", tc.schema, got, tc.want)
		}
	}
}

func TestBindUsesEngineWithODBCOverride(t *testing.T) {
	for _, tc := range []struct{ engine, driver, want string }{
		{"oracle", "oracledb", ":2"}, {"sqlserver", "sqlserver", "@p2"},
		{"postgres", "pgx", "$2"}, {"dm", "dm", "?"},
		{"postgres", "", "$2"},
		{"oracle", "odbc", "?"}, {"sqlserver", "odbc", "?"}, {"postgres", "odbc", "?"},
	} {
		if got := bind(conn.Spec{Engine: tc.engine, Driver: tc.driver}, 2); got != tc.want {
			t.Errorf("%s/%s: got %s want %s", tc.engine, tc.driver, got, tc.want)
		}
	}
}

func TestQuoteTable(t *testing.T) {
	for _, tc := range []struct{ engine, table, want string }{
		{"mysql", "sales.orders", "`sales`.`orders`"},
		{"postgres", "sales.orders", `"sales"."orders"`},
		{"sqlite", "main.orders", `"main"."orders"`},
		{"oracle", "APP.ORDERS", `"APP"."ORDERS"`},
		{"dm", "APP.ORDERS", `"APP"."ORDERS"`},
		{"sqlserver", "finance.dbo.orders", "[finance].[dbo].[orders]"},
		{"mysql", "odd`name", "`odd``name`"},
		{"postgres", `odd"name`, `"odd""name"`},
		{"sqlite", `odd"name`, `"odd""name"`},
		{"oracle", `APP.odd"name`, `"APP"."odd""name"`},
		{"sqlserver", "dbo.odd]name", "[dbo].[odd]]name]"},
		{"sqlite", `x"; DROP TABLE users; --`, `"x""; DROP TABLE users; --"`},
	} {
		t.Run(tc.engine+"/"+tc.table, func(t *testing.T) {
			got, err := QuoteTable(tc.engine, tc.table)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestQuoteTableRejectsInvalidComponents(t *testing.T) {
	for _, table := range []string{"", ".orders", "sales.", "sales..orders", "sales.ord\x00ers"} {
		if _, err := QuoteTable("sqlite", table); err == nil {
			t.Errorf("accepted invalid table %q", table)
		}
	}
}

func TestDescribeTableOracleAndDM(t *testing.T) {
	// SQLite catalog fixtures verify DBX metadata handling, not server compatibility.
	for _, engine := range []string{"oracle", "dm"} {
		t.Run(engine, func(t *testing.T) {
			spec := conn.Spec{Engine: engine, Driver: "sqlite", Schema: "APP", DSN: filepath.Join(t.TempDir(), "catalog.db"), Timeout: time.Second}
			ctx := context.Background()
			for _, query := range []string{
				"create table all_tab_columns (owner text, table_name text, column_name text, data_type text, nullable text, data_default text, column_id integer)",
				"insert into all_tab_columns values ('APP','ITEMS','ID','NUMBER','N',null,1), ('APP','ITEMS','LABEL','VARCHAR','Y','default',2), ('OTHER','ITEMS','OTHER_ID','NUMBER','N',null,1)",
				"create table all_constraints (owner text, table_name text, constraint_name text, constraint_type text, r_owner text, r_constraint_name text)",
				"insert into all_constraints values ('APP','ITEMS','PK_ITEMS','P',null,null)",
				"create table all_cons_columns (owner text, table_name text, constraint_name text, column_name text, position integer)",
				"insert into all_cons_columns values ('APP','ITEMS','PK_ITEMS','ID',1)",
			} {
				if _, err := Execute(ctx, spec, query); err != nil {
					t.Fatal(err)
				}
			}
			describe := func(schema, table string) (TableInfo, error) {
				if engine == "dm" {
					return DescribeTable(ctx, spec, schema, table)
				}
				handle, err := Open(spec)
				if err != nil {
					return TableInfo{}, err
				}
				defer handle.Close()
				// SQLite accepts ODBC's ? parameters; native binds are tested separately.
				odbcSpec := spec
				odbcSpec.Driver = "odbc"
				return describeTableOracle(ctx, handle, odbcSpec, schema, table)
			}
			info, err := describe("", "ITEMS")
			if err != nil {
				t.Fatal(err)
			}
			if info.Schema != "APP" || len(info.Columns) != 2 || len(info.PrimaryKey) != 1 || info.PrimaryKey[0] != "ID" || !info.Columns[0].PrimaryKey || info.Columns[0].Nullable || !info.Columns[1].Nullable || info.Columns[1].DefaultValue != "default" {
				t.Fatalf("unexpected table description: %+v", info)
			}
			info, err = describe("OTHER", "ITEMS")
			if err != nil || len(info.Columns) != 1 || info.Columns[0].Name != "OTHER_ID" {
				t.Fatalf("explicit schema not honored: %+v, %v", info, err)
			}
			if _, err := describe("", "MISSING"); err == nil {
				t.Fatal("missing table must fail")
			}
		})
	}
}

func TestDescribeTableSQLServer(t *testing.T) {
	// An attached SQLite database supplies sys catalog fixtures on this connection.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		"attach database ':memory:' as sys",
		"create table sys.schemas (schema_id integer, name text)",
		"insert into sys.schemas values (1,'APP')",
		"create table sys.objects (object_id integer, schema_id integer, name text, type text)",
		"insert into sys.objects values (1,1,'ITEMS','U')",
		"create table sys.tables (object_id integer, schema_id integer, name text)",
		"create table sys.columns (object_id integer, column_id integer, name text, user_type_id integer, default_object_id integer, is_nullable integer)",
		"insert into sys.columns values (1,1,'ID',1,null,0), (1,2,'LABEL',2,1,1)",
		"create table sys.types (user_type_id integer, name text)",
		"insert into sys.types values (1,'int'), (2,'varchar')",
		"create table sys.default_constraints (object_id integer, definition text)",
		"insert into sys.default_constraints values (1,'default')",
		"create table sys.key_constraints (name text, type text, parent_object_id integer, unique_index_id integer)",
		"create table sys.index_columns (object_id integer, index_id integer, column_id integer, key_ordinal integer)",
		"create table sys.foreign_keys (object_id integer, name text, parent_object_id integer)",
		"create table sys.foreign_key_columns (constraint_object_id integer, parent_column_id integer, referenced_object_id integer, referenced_column_id integer, constraint_column_id integer)",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	spec := conn.Spec{Engine: "sqlserver", Driver: "odbc", Schema: "APP"}
	info, err := describeTableSQLServer(ctx, db, spec, "", "ITEMS")
	if err != nil {
		t.Fatal(err)
	}
	if info.Schema != "APP" || len(info.Columns) != 2 || info.Columns[0].Name != "ID" || info.Columns[0].Nullable || !info.Columns[1].Nullable || info.Columns[1].DefaultValue != "default" {
		t.Fatalf("unexpected table description: %+v", info)
	}
}
