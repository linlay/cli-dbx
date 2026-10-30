// Package db routes built-in engines locally and optional engines to verified
// object modules or verified driver processes. Security policy is checked by
// the CLI before these calls.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/dbcore"
	"github.com/linlay/cli-dbx/internal/driverprocess"
	_ "modernc.org/sqlite"
)

type Column = dbcore.Column
type QueryResult = dbcore.QueryResult
type ColumnStats = dbcore.ColumnStats
type TableInfo = dbcore.TableInfo
type Constraint = dbcore.Constraint
type ForeignKey = dbcore.ForeignKey
type RelationInfo = dbcore.RelationInfo
type TxPlan = dbcore.TxPlan
type TxStep = dbcore.TxStep
type TxStepResult = dbcore.TxStepResult
type TxRunResult = dbcore.TxRunResult

func Open(spec conn.Spec) (*sql.DB, error) {
	if driverprocess.IsExternal(spec.Engine) {
		return nil, fmt.Errorf("%s uses an independent driver process", spec.Engine)
	}
	return dbcore.Open(spec)
}
func Ping(ctx context.Context, spec conn.Spec) error {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.Ping(ctx, spec)
	}
	_, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "ping"})
	return err
}
func Query(ctx context.Context, spec conn.Spec, text string, offset, limit int) (QueryResult, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.Query(ctx, spec, text, offset, limit)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "query", SQL: text, Offset: offset, Limit: limit})
	if err != nil {
		return QueryResult{}, err
	}
	return *r.Query, nil
}
func Execute(ctx context.Context, spec conn.Spec, text string) (int64, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.Execute(ctx, spec, text)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "execute", SQL: text})
	return r.Count, err
}
func ExecuteGuarded(ctx context.Context, spec conn.Spec, text string, maxRows int) (int64, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.ExecuteGuarded(ctx, spec, text, maxRows)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "guarded", SQL: text, MaxRows: maxRows})
	return r.Count, err
}
func RunTxPlan(ctx context.Context, spec conn.Spec, plan TxPlan, queryLimit, defaultMaxRows int) (TxRunResult, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.RunTxPlan(ctx, spec, plan, queryLimit, defaultMaxRows)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "tx", Plan: plan, Limit: queryLimit, MaxRows: defaultMaxRows})
	if err != nil {
		return TxRunResult{}, err
	}
	return *r.Tx, nil
}
func ListSchemas(ctx context.Context, spec conn.Spec) ([]string, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.ListSchemas(ctx, spec)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "schemas"})
	return r.Schemas, err
}
func ListTables(ctx context.Context, spec conn.Spec, schema string) ([]TableInfo, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.ListTables(ctx, spec, schema)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "tables", Schema: schema})
	return r.Tables, err
}
func DescribeTable(ctx context.Context, spec conn.Spec, schema, table string) (TableInfo, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.DescribeTable(ctx, spec, schema, table)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "describe", Schema: schema, Table: table})
	if err != nil {
		return TableInfo{}, err
	}
	return *r.Table, nil
}
func ListRelations(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.ListRelations(ctx, spec, schema)
	}
	r, err := driverprocess.Call(ctx, spec, driverprocess.Request{Operation: "relations", Schema: schema})
	return r.Relations, err
}
func ExportRows(rows []map[string]any, columns []Column, format string, out *os.File) error {
	return dbcore.ExportRows(rows, columns, format, out)
}
func ImportRows(ctx context.Context, spec conn.Spec, table string, columns []string, rows []map[string]any, limits ...int) (int64, error) {
	if !driverprocess.IsExternal(spec.Engine) {
		return dbcore.ImportRows(ctx, spec, table, columns, rows, limits...)
	}
	maxRows := 0
	if len(limits) > 0 {
		maxRows = limits[0]
	}
	return driverprocess.ImportRows(ctx, spec, table, columns, rows, maxRows)
}
func QuoteTable(engine, table string) (string, error) { return dbcore.QuoteTable(engine, table) }
