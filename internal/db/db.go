package db

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/linlay/dbx/internal/conn"
)

type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type QueryResult struct {
	Columns       []Column               `json:"columns"`
	Rows          []map[string]any       `json:"rows"`
	RowCount      int                    `json:"row_count"`
	Complete      bool                   `json:"complete"`
	Sampled       bool                   `json:"sampled"`
	Truncated     bool                   `json:"truncated"`
	TruncatedFrom int                    `json:"truncated_from,omitempty"`
	Stats         map[string]ColumnStats `json:"stats,omitempty"`
}

type ColumnStats struct {
	NullCount    int      `json:"null_count"`
	DistinctSeen int      `json:"distinct_seen"`
	Samples      []string `json:"samples,omitempty"`
}

type TableInfo struct {
	Schema  string   `json:"schema"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Columns []Column `json:"columns,omitempty"`
}

func Open(spec conn.Spec) (*sql.DB, error) {
	db, err := sql.Open(spec.Driver, spec.DSN)
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(0)
	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(4)
	return db, nil
}

func Ping(ctx context.Context, spec conn.Spec) error {
	db, err := Open(spec)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	return db.PingContext(ctx)
}

func Query(ctx context.Context, spec conn.Spec, sqlText string, limit int) (QueryResult, error) {
	db, err := Open(spec)
	if err != nil {
		return QueryResult{}, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return QueryResult{}, err
	}
	columns := make([]Column, 0, len(columnTypes))
	for _, ct := range columnTypes {
		nullable, ok := ct.Nullable()
		columns = append(columns, Column{
			Name:     ct.Name(),
			Type:     ct.DatabaseTypeName(),
			Nullable: ok && nullable,
		})
	}

	result := QueryResult{
		Columns:  columns,
		Rows:     make([]map[string]any, 0),
		Complete: true,
		Stats:    map[string]ColumnStats{},
	}

	for rows.Next() {
		dest := make([]any, len(columns))
		scan := make([]any, len(columns))
		for i := range dest {
			scan[i] = &dest[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return QueryResult{}, err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			value := normalizeValue(dest[i])
			row[col.Name] = value
			stats := result.Stats[col.Name]
			if value == nil {
				stats.NullCount++
			} else if len(stats.Samples) < 3 {
				stats.Samples = append(stats.Samples, fmt.Sprint(value))
			}
			stats.DistinctSeen++
			result.Stats[col.Name] = stats
		}
		if limit > 0 && len(result.Rows) >= limit {
			result.Truncated = true
			result.Complete = false
			result.TruncatedFrom++
			continue
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
	}
	result.RowCount = len(result.Rows)
	return result, nil
}

func Execute(ctx context.Context, spec conn.Spec, sqlText string) (int64, error) {
	db, err := Open(spec)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	res, err := db.ExecContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	return count, nil
}

func ExecuteGuarded(ctx context.Context, spec conn.Spec, sqlText string, maxRows int) (int64, error) {
	db, err := Open(spec)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	if maxRows > 0 && count > int64(maxRows) {
		return count, fmt.Errorf("rows affected %d exceeds max-rows-affected %d", count, maxRows)
	}
	if err := tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}

func ListSchemas(ctx context.Context, spec conn.Spec) ([]string, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	var query string
	switch spec.Engine {
	case "postgres", "mysql":
		query = "select schema_name from information_schema.schemata order by schema_name"
	case "sqlite":
		return []string{"main"}, nil
	default:
		return nil, fmt.Errorf("unsupported engine %s", spec.Engine)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schemas []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		schemas = append(schemas, name)
	}
	return schemas, rows.Err()
}

func ListTables(ctx context.Context, spec conn.Spec, schema string) ([]TableInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	var query string
	var args []any
	switch spec.Engine {
	case "postgres":
		if schema == "" {
			schema = "public"
		}
		query = "select table_schema, table_name, table_type from information_schema.tables where table_schema = $1 order by table_name"
		args = append(args, schema)
	case "mysql":
		if schema == "" {
			schema = spec.Database
		}
		query = "select table_schema, table_name, table_type from information_schema.tables where table_schema = ? order by table_name"
		args = append(args, schema)
	case "sqlite":
		query = "select 'main' as table_schema, name as table_name, type as table_type from sqlite_master where type in ('table','view') and name not like 'sqlite_%' order by name"
	default:
		return nil, fmt.Errorf("unsupported engine %s", spec.Engine)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []TableInfo
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Schema, &t.Name, &t.Type); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func DescribeTable(ctx context.Context, spec conn.Spec, schema, table string) (TableInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return TableInfo{}, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	info := TableInfo{Schema: schema, Name: table}
	var query string
	var args []any
	switch spec.Engine {
	case "postgres":
		if schema == "" {
			schema = "public"
		}
		info.Schema = schema
		query = `select column_name, data_type, is_nullable = 'YES'
			from information_schema.columns
			where table_schema = $1 and table_name = $2
			order by ordinal_position`
		args = []any{schema, table}
	case "mysql":
		if schema == "" {
			schema = spec.Database
		}
		info.Schema = schema
		query = `select column_name, data_type, is_nullable = 'YES'
			from information_schema.columns
			where table_schema = ? and table_name = ?
			order by ordinal_position`
		args = []any{schema, table}
	case "sqlite":
		info.Schema = "main"
		query = fmt.Sprintf("pragma table_info(%s)", quoteIdent(spec.Engine, table))
	default:
		return TableInfo{}, fmt.Errorf("unsupported engine %s", spec.Engine)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return TableInfo{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Column
		switch spec.Engine {
		case "sqlite":
			var cid int
			var notNull int
			var defaultValue any
			var pk int
			if err := rows.Scan(&cid, &c.Name, &c.Type, &notNull, &defaultValue, &pk); err != nil {
				return TableInfo{}, err
			}
			c.Nullable = notNull == 0
		default:
			if err := rows.Scan(&c.Name, &c.Type, &c.Nullable); err != nil {
				return TableInfo{}, err
			}
		}
		info.Columns = append(info.Columns, c)
	}
	return info, rows.Err()
}

func ExportRows(rows []map[string]any, columns []Column, format string, out *os.File) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "jsonl":
		enc := json.NewEncoder(out)
		for _, row := range rows {
			if err := enc.Encode(row); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		w := csv.NewWriter(out)
		header := make([]string, 0, len(columns))
		for _, col := range columns {
			header = append(header, col.Name)
		}
		if err := w.Write(header); err != nil {
			return err
		}
		for _, row := range rows {
			record := make([]string, 0, len(columns))
			for _, col := range columns {
				record = append(record, fmt.Sprint(row[col.Name]))
			}
			if err := w.Write(record); err != nil {
				return err
			}
		}
		w.Flush()
		return w.Error()
	default:
		return fmt.Errorf("unsupported export format %q", format)
	}
}

func ImportRows(ctx context.Context, spec conn.Spec, table string, columns []string, rows []map[string]any) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	db, err := Open(spec)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	placeholders := make([]string, 0, len(columns))
	for i := range columns {
		placeholders = append(placeholders, bind(spec.Engine, i+1))
	}
	stmt := fmt.Sprintf("insert into %s (%s) values (%s)",
		quoteIdent(spec.Engine, table),
		strings.Join(quoteAll(spec.Engine, columns), ", "),
		strings.Join(placeholders, ", "),
	)
	prepared, err := tx.PrepareContext(ctx, stmt)
	if err != nil {
		return 0, err
	}
	defer prepared.Close()

	var count int64
	for _, row := range rows {
		args := make([]any, 0, len(columns))
		for _, col := range columns {
			args = append(args, row[col])
		}
		if _, err := prepared.ExecContext(ctx, args...); err != nil {
			return count, err
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}

func normalizeValue(v any) any {
	switch raw := v.(type) {
	case []byte:
		return string(raw)
	default:
		return raw
	}
}

func bind(engine string, n int) string {
	if engine == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func quoteAll(engine string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, quoteIdent(engine, name))
	}
	return out
}

func quoteIdent(engine, ident string) string {
	escaped := strings.ReplaceAll(ident, `"`, `""`)
	switch engine {
	case "mysql":
		return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
	default:
		return `"` + escaped + `"`
	}
}
