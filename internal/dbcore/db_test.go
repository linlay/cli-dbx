package dbcore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/conn"
)

func TestVendorBindsAndQualifiedTableQuotes(t *testing.T) {
	for _, tc := range []struct{ engine, bind, table string }{
		{"postgres", "$2", `"a"."b"`},
		{"mysql", "?", "`a`.`b`"},
		{"sqlite", "?", `"a"."b"`},
		{"oracle", ":2", `"a"."b"`},
		{"dm", "?", `"a"."b"`},
		{"sqlserver", "@p2", `[a].[b]`},
	} {
		if got := bind(tc.engine, 2); got != tc.bind {
			t.Errorf("%s bind = %q, want %q", tc.engine, got, tc.bind)
		}
		got, err := QuoteTable(tc.engine, "a.b")
		if err != nil || got != tc.table {
			t.Errorf("%s table = %q, %v, want %q", tc.engine, got, err, tc.table)
		}
	}
	if _, err := QuoteTable("sqlserver", "a..b"); err == nil {
		t.Fatal("empty identifier component accepted")
	}
	if got, _ := QuoteTable("sqlserver", "a.b]c"); got != "[a].[b]]c]" {
		t.Fatalf("escape = %q", got)
	}
}

func TestODBCBindsUseQuestionMarkWithVendorDialect(t *testing.T) {
	for _, engine := range []string{"oracle", "dm", "sqlserver"} {
		if got := bindSpec(conn.Spec{Engine: engine, Driver: "odbc"}, 2); got != "?" {
			t.Errorf("%s bind = %q", engine, got)
		}
	}
}

func TestQueryPreservesBinaryValues(t *testing.T) {
	spec := conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: ":memory:", Timeout: time.Second}
	db, err := Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("create table samples (payload blob, label text); insert into samples values (x'00ff', 'plain'), (x'', 'empty'), (null, 'null')"); err != nil {
		t.Fatal(err)
	}
	result, err := queryWithRunner(context.Background(), db, "select payload, label from samples order by rowid", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := result.Rows[0]["payload"].([]byte)
	if !ok || len(got) != 2 || got[0] != 0 || got[1] != 255 {
		t.Fatalf("binary payload = %#v, columns = %#v", result.Rows[0]["payload"], result.Columns)
	}
	if result.Rows[0]["label"] != "plain" {
		t.Fatalf("text = %#v", result.Rows[0]["label"])
	}
	if empty, ok := result.Rows[1]["payload"].([]byte); !ok || empty == nil || len(empty) != 0 {
		t.Fatalf("empty blob = %#v", result.Rows[1]["payload"])
	}
	if result.Rows[2]["payload"] != nil {
		t.Fatalf("SQL NULL = %#v", result.Rows[2]["payload"])
	}
}

func TestQueryMakesDuplicateColumnNamesUniqueWithoutLosingValues(t *testing.T) {
	spec := conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: ":memory:", Timeout: time.Second}
	tests := []struct {
		name    string
		sql     string
		columns []string
		values  []any
	}{
		{
			name:    "two duplicate columns",
			sql:     "select 1 as id, 2 as id",
			columns: []string{"id", "id_2"},
			values:  []any{int64(1), int64(2)},
		},
		{
			name:    "three duplicates",
			sql:     "select 1 as id, 2 as id, 3 as id",
			columns: []string{"id", "id_2", "id_3"},
			values:  []any{int64(1), int64(2), int64(3)},
		},
		{
			name:    "generated suffix avoids original column",
			sql:     "select 1 as id, 2 as id, 3 as id_2, 4 as id",
			columns: []string{"id", "id_3", "id_2", "id_4"},
			values:  []any{int64(1), int64(2), int64(3), int64(4)},
		},
		{
			name:    "unique names unchanged",
			sql:     "select 1 as first, 2 as second",
			columns: []string{"first", "second"},
			values:  []any{int64(1), int64(2)},
		},
		{
			name:    "duplicate empty names",
			sql:     `select 1 as "", 2 as ""`,
			columns: []string{"", "_2"},
			values:  []any{int64(1), int64(2)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Query(context.Background(), spec, tc.sql, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Columns) != len(tc.columns) || len(result.Rows) != 1 {
				t.Fatalf("columns/rows = %#v / %#v", result.Columns, result.Rows)
			}
			for i, name := range tc.columns {
				if result.Columns[i].Name != name {
					t.Errorf("column %d name = %q, want %q", i, result.Columns[i].Name, name)
				}
				if got := result.Rows[0][name]; got != tc.values[i] {
					t.Errorf("row[%q] = %#v, want %#v", name, got, tc.values[i])
				}
				stats := result.Stats[name]
				if stats.DistinctSeen != 1 || stats.NullCount != 0 || len(stats.Samples) != 1 || stats.Samples[0] != fmt.Sprint(tc.values[i]) {
					t.Errorf("stats[%q] = %#v", name, stats)
				}
			}
			if result.RowCount != 1 || result.SeenCount != 1 {
				t.Errorf("row/seen count = %d/%d, want 1/1", result.RowCount, result.SeenCount)
			}
		})
	}
}

func TestDuplicateColumnNamesArePreservedInPagedAndTransactionalQueries(t *testing.T) {
	spec := conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: ":memory:", Timeout: time.Second}
	query := "select 1 as id, 2 as id union all select 3, 4"

	paged, err := Query(context.Background(), spec, query, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{paged.Columns[0].Name, paged.Columns[1].Name}; !equalStrings(got, []string{"id", "id_2"}) {
		t.Fatalf("paged columns = %#v", got)
	}
	if paged.Rows[0]["id"] != int64(3) || paged.Rows[0]["id_2"] != int64(4) || paged.RowCount != 1 || paged.SeenCount != 2 {
		t.Fatalf("paged result = %#v", paged)
	}
	if paged.Stats["id"].DistinctSeen != 2 || !equalStrings(paged.Stats["id"].Samples, []string{"1", "3"}) ||
		paged.Stats["id_2"].DistinctSeen != 2 || !equalStrings(paged.Stats["id_2"].Samples, []string{"2", "4"}) {
		t.Fatalf("paged stats = %#v", paged.Stats)
	}

	txResult, err := RunTxPlan(context.Background(), spec, TxPlan{Steps: []TxStep{{Action: action.Query, SQL: "select 5 as id, 6 as id"}}}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	txQuery := txResult.Steps[0].Query
	if txQuery == nil || !equalStrings([]string{txQuery.Columns[0].Name, txQuery.Columns[1].Name}, []string{"id", "id_2"}) ||
		txQuery.Rows[0]["id"] != int64(5) || txQuery.Rows[0]["id_2"] != int64(6) ||
		txQuery.Stats["id"].DistinctSeen != 1 || !equalStrings(txQuery.Stats["id"].Samples, []string{"5"}) ||
		txQuery.Stats["id_2"].DistinctSeen != 1 || !equalStrings(txQuery.Stats["id_2"].Samples, []string{"6"}) {
		t.Fatalf("transaction query = %#v", txQuery)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestImportRowsLimitRollsBack(t *testing.T) {
	path := t.TempDir() + "/import.db"
	spec := conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: path, Timeout: time.Second}
	db, err := Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("create table items (id integer primary key)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	rows := []map[string]any{{"id": int64(1)}, {"id": int64(2)}}
	if _, err := ImportRows(context.Background(), spec, "items", []string{"id"}, rows, 1); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected row-limit error, got %v", err)
	}
	result, err := Query(context.Background(), spec, "select id from items", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 0 {
		t.Fatalf("rows committed despite limit: %+v", result.Rows)
	}
}

func TestDMSchemasUseAccessibleMetadataAndIncludeEmptySchemas(t *testing.T) {
	path := t.TempDir() + "/schemas.db"
	spec := conn.Spec{Engine: "dm", Driver: "sqlite", DSN: path, Timeout: time.Second}
	db, err := Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Only the public metadata view is available to this connection. A schema
	// can exist without owning any tables, so listing distinct owners is wrong.
	if _, err := db.Exec(`create table all_objects(object_name text, object_type text);
		insert into all_objects values ('EMPTY_SCHEMA', 'SCH'), ('LOCALDEV', 'SCH'), ('ITEMS', 'TABLE')`); err != nil {
		t.Fatal(err)
	}
	got, err := ListSchemas(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(got, []string{"EMPTY_SCHEMA", "LOCALDEV"}) {
		t.Fatalf("schemas = %#v", got)
	}
}

func TestSQLiteCompositePrimaryKeyUsesConstraintOrder(t *testing.T) {
	spec := conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: t.TempDir() + "/keys.db", Timeout: time.Second}
	db, err := Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("create table composite(a integer,b integer,primary key(b,a))"); err != nil {
		t.Fatal(err)
	}
	info, err := DescribeTable(context.Background(), spec, "", "composite")
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(info.PrimaryKey, []string{"b", "a"}) {
		t.Fatalf("primary key = %#v", info.PrimaryKey)
	}
}

func TestRelationsKeepSameNamedConstraintsOnDifferentTablesSeparate(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`select 'same_fk','public','child1','b','public','parent','id2'
		union all select 'same_fk','public','child2','x','public','parent','id1'
		union all select 'same_fk','public','child1','a','public','parent','id1'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	relations, err := scanRelationRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 2 {
		t.Fatalf("merged distinct constraints: %#v", relations)
	}
	if relations[0].FromTable != "child1" || !equalStrings(relations[0].FromCols, []string{"b", "a"}) || !equalStrings(relations[0].ToCols, []string{"id2", "id1"}) {
		t.Fatalf("composite pairing = %#v", relations[0])
	}
	if relations[1].FromTable != "child2" || !equalStrings(relations[1].FromCols, []string{"x"}) {
		t.Fatalf("second constraint = %#v", relations[1])
	}
}

type unknownCountDriver struct{ committed, rolledBack *atomic.Bool }
type unknownCountConn struct{ committed, rolledBack *atomic.Bool }
type unknownCountTx struct{ committed, rolledBack *atomic.Bool }

func (d unknownCountDriver) Open(string) (driver.Conn, error) {
	return unknownCountConn{d.committed, d.rolledBack}, nil
}
func (c unknownCountConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c unknownCountConn) Close() error { return nil }
func (c unknownCountConn) Begin() (driver.Tx, error) {
	return unknownCountTx{c.committed, c.rolledBack}, nil
}
func (c unknownCountConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c unknownCountConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return unknownResult{}, nil
}
func (tx unknownCountTx) Commit() error   { tx.committed.Store(true); return nil }
func (tx unknownCountTx) Rollback() error { tx.rolledBack.Store(true); return nil }

type unknownResult struct{}

func (unknownResult) LastInsertId() (int64, error) { return 0, errors.New("unknown") }
func (unknownResult) RowsAffected() (int64, error) { return 0, errors.New("unknown") }

func TestGuardedUnknownAffectedRowsRollsBack(t *testing.T) {
	var committed, rolledBack atomic.Bool
	const name = "dbcore_unknown_count_test"
	sql.Register(name, unknownCountDriver{&committed, &rolledBack})
	spec := conn.Spec{Engine: "sqlite", Driver: name, Timeout: time.Second}
	if _, err := ExecuteGuarded(context.Background(), spec, "update things set value = 1", 1); err == nil || !strings.Contains(err.Error(), "affected-row count") {
		t.Fatalf("expected unavailable count failure, got %v", err)
	}
	if committed.Load() || !rolledBack.Load() {
		t.Fatalf("commit=%t rollback=%t", committed.Load(), rolledBack.Load())
	}
}
