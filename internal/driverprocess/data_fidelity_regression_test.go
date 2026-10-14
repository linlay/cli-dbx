package driverprocess

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/dbcore"
)

func TestProcessDataFidelityRegression(t *testing.T) {
	spec := installTestDriver(t)
	call := func(req Request) Response {
		t.Helper()
		response, err := Call(context.Background(), spec, req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	call(Request{Operation: "execute", SQL: "create table fidelity(id integer primary key, value integer)"})
	encoded, err := EncodeRows([]map[string]any{
		{"id": int64(1), "value": json.Number("9007199254740993")},
		{"id": int64(2), "value": int64(9223372036854775807)},
	})
	if err != nil {
		t.Fatal(err)
	}
	call(Request{Operation: "import", Table: "fidelity", Columns: []string{"id", "value"}, Rows: encoded})

	check := func(result *dbcore.QueryResult, expected map[string]any) {
		t.Helper()
		if result == nil || len(result.Rows) != 1 || !reflect.DeepEqual(result.Rows[0], expected) {
			t.Fatalf("values changed across driver process: %+v; want %+v", result, expected)
		}
		seen := make(map[string]bool)
		for _, column := range result.Columns {
			if seen[column.Name] {
				t.Fatalf("duplicate metadata name %q: %+v", column.Name, result.Columns)
			}
			seen[column.Name] = true
			if _, ok := result.Stats[column.Name]; !ok {
				t.Fatalf("missing statistics for %q: %+v", column.Name, result.Stats)
			}
		}
		if len(seen) != len(expected) || len(result.Stats) != len(expected) {
			t.Fatalf("inconsistent metadata and statistics: %+v", result)
		}
	}
	const sql = "select value as id, 2 as id, 3 as id_2 from fidelity order by fidelity.id"
	first := call(Request{Operation: "query", SQL: sql, Limit: 1})
	check(first.Query, map[string]any{"id": int64(9007199254740993), "id_3": int64(2), "id_2": int64(3)})
	if !first.Query.Truncated {
		t.Fatal("first page should report more rows")
	}
	second := call(Request{Operation: "query", SQL: sql, Offset: 1, Limit: 1})
	check(second.Query, map[string]any{"id": int64(9223372036854775807), "id_3": int64(2), "id_2": int64(3)})
	if second.Query.Truncated {
		t.Fatal("last page should not report more rows")
	}
	tx := call(Request{Operation: "tx", Plan: dbcore.TxPlan{Steps: []dbcore.TxStep{{Action: action.Query, SQL: sql + " limit 1"}}}, Limit: 1})
	if tx.Tx == nil || len(tx.Tx.Steps) != 1 {
		t.Fatalf("missing transaction query: %+v", tx.Tx)
	}
	check(tx.Tx.Steps[0].Query, map[string]any{"id": int64(9007199254740993), "id_3": int64(2), "id_2": int64(3)})
}
