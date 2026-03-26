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

	"github.com/linlay/cli-dbx/internal/conn"
)

type Column struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Nullable     bool   `json:"nullable"`
	DefaultValue string `json:"default,omitempty"`
	PrimaryKey   bool   `json:"primary_key,omitempty"`
}

type QueryResult struct {
	Columns       []Column               `json:"columns"`
	Rows          []map[string]any       `json:"rows"`
	RowCount      int                    `json:"row_count"`
	SeenCount     int                    `json:"seen_count"`
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
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Columns     []Column     `json:"columns,omitempty"`
	PrimaryKey  []string     `json:"primary_key,omitempty"`
	UniqueKeys  []Constraint `json:"unique_keys,omitempty"`
	ForeignKeys []ForeignKey `json:"foreign_keys,omitempty"`
}

type Constraint struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

type ForeignKey struct {
	Name       string   `json:"name,omitempty"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema,omitempty"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns,omitempty"`
}

type RelationInfo struct {
	FromSchema string   `json:"from_schema,omitempty"`
	FromTable  string   `json:"from_table"`
	FromCols   []string `json:"from_columns"`
	ToSchema   string   `json:"to_schema,omitempty"`
	ToTable    string   `json:"to_table"`
	ToCols     []string `json:"to_columns,omitempty"`
	Name       string   `json:"name,omitempty"`
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

func Query(ctx context.Context, spec conn.Spec, sqlText string, offset, limit int) (QueryResult, error) {
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
		result.SeenCount++
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
		if result.SeenCount <= offset {
			continue
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
		query = `select column_name, data_type, is_nullable = 'YES', coalesce(column_default, '')
			from information_schema.columns
			where table_schema = $1 and table_name = $2
			order by ordinal_position`
		args = []any{schema, table}
	case "mysql":
		if schema == "" {
			schema = spec.Database
		}
		info.Schema = schema
		query = `select column_name, data_type, is_nullable = 'YES', coalesce(column_default, '')
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
			c.PrimaryKey = pk > 0
			if defaultValue != nil {
				c.DefaultValue = fmt.Sprint(defaultValue)
			}
		default:
			if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.DefaultValue); err != nil {
				return TableInfo{}, err
			}
		}
		info.Columns = append(info.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return TableInfo{}, err
	}
	pk, unique, fks, err := loadConstraints(ctx, spec, info.Schema, table)
	if err != nil {
		return TableInfo{}, err
	}
	info.PrimaryKey = pk
	info.UniqueKeys = unique
	info.ForeignKeys = fks
	for i := range info.Columns {
		for _, pkCol := range pk {
			if info.Columns[i].Name == pkCol {
				info.Columns[i].PrimaryKey = true
			}
		}
	}
	return info, nil
}

func ListRelations(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	switch spec.Engine {
	case "postgres":
		return listRelationsPostgres(ctx, spec, schema)
	case "mysql":
		return listRelationsMySQL(ctx, spec, schema)
	case "sqlite":
		return listRelationsSQLite(ctx, spec)
	default:
		return nil, fmt.Errorf("unsupported engine %s", spec.Engine)
	}
}

func loadConstraints(ctx context.Context, spec conn.Spec, schema, table string) ([]string, []Constraint, []ForeignKey, error) {
	switch spec.Engine {
	case "postgres":
		return loadConstraintsPostgres(ctx, spec, schema, table)
	case "mysql":
		return loadConstraintsMySQL(ctx, spec, schema, table)
	case "sqlite":
		return loadConstraintsSQLite(ctx, spec, table)
	default:
		return nil, nil, nil, fmt.Errorf("unsupported engine %s", spec.Engine)
	}
}

func loadConstraintsPostgres(ctx context.Context, spec conn.Spec, schema, table string) ([]string, []Constraint, []ForeignKey, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, nil, nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	query := `select tc.constraint_name, tc.constraint_type, kcu.column_name,
		coalesce(ccu.table_schema, ''), coalesce(ccu.table_name, ''), coalesce(ccu.column_name, '')
		from information_schema.table_constraints tc
		left join information_schema.key_column_usage kcu
		  on tc.constraint_name = kcu.constraint_name
		 and tc.table_schema = kcu.table_schema
		 and tc.table_name = kcu.table_name
		left join information_schema.constraint_column_usage ccu
		  on tc.constraint_name = ccu.constraint_name
		 and tc.table_schema = ccu.table_schema
		where tc.table_schema = $1 and tc.table_name = $2
		  and tc.constraint_type in ('PRIMARY KEY', 'UNIQUE', 'FOREIGN KEY')
		order by tc.constraint_name, kcu.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var pk []string
	uniqueMap := map[string][]string{}
	fkMap := map[string]*ForeignKey{}
	for rows.Next() {
		var name, ctype, col, refSchema, refTable, refCol string
		if err := rows.Scan(&name, &ctype, &col, &refSchema, &refTable, &refCol); err != nil {
			return nil, nil, nil, err
		}
		switch ctype {
		case "PRIMARY KEY":
			pk = append(pk, col)
		case "UNIQUE":
			uniqueMap[name] = append(uniqueMap[name], col)
		case "FOREIGN KEY":
			item := fkMap[name]
			if item == nil {
				item = &ForeignKey{Name: name, RefSchema: refSchema, RefTable: refTable}
				fkMap[name] = item
			}
			item.Columns = append(item.Columns, col)
			item.RefColumns = append(item.RefColumns, refCol)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	unique := constraintsFromMap(uniqueMap)
	fks := foreignKeysFromMap(fkMap)
	return pk, unique, fks, nil
}

func loadConstraintsMySQL(ctx context.Context, spec conn.Spec, schema, table string) ([]string, []Constraint, []ForeignKey, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, nil, nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	query := `select tc.constraint_name, tc.constraint_type, kcu.column_name,
		coalesce(kcu.referenced_table_schema, ''), coalesce(kcu.referenced_table_name, ''), coalesce(kcu.referenced_column_name, '')
		from information_schema.table_constraints tc
		left join information_schema.key_column_usage kcu
		  on tc.constraint_name = kcu.constraint_name
		 and tc.table_schema = kcu.table_schema
		 and tc.table_name = kcu.table_name
		where tc.table_schema = ? and tc.table_name = ?
		  and tc.constraint_type in ('PRIMARY KEY', 'UNIQUE', 'FOREIGN KEY')
		order by tc.constraint_name, kcu.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var pk []string
	uniqueMap := map[string][]string{}
	fkMap := map[string]*ForeignKey{}
	for rows.Next() {
		var name, ctype, col, refSchema, refTable, refCol string
		if err := rows.Scan(&name, &ctype, &col, &refSchema, &refTable, &refCol); err != nil {
			return nil, nil, nil, err
		}
		switch ctype {
		case "PRIMARY KEY":
			pk = append(pk, col)
		case "UNIQUE":
			uniqueMap[name] = append(uniqueMap[name], col)
		case "FOREIGN KEY":
			item := fkMap[name]
			if item == nil {
				item = &ForeignKey{Name: name, RefSchema: refSchema, RefTable: refTable}
				fkMap[name] = item
			}
			item.Columns = append(item.Columns, col)
			item.RefColumns = append(item.RefColumns, refCol)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return pk, constraintsFromMap(uniqueMap), foreignKeysFromMap(fkMap), nil
}

func loadConstraintsSQLite(ctx context.Context, spec conn.Spec, table string) ([]string, []Constraint, []ForeignKey, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, nil, nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	tableQuery := fmt.Sprintf("pragma table_info(%s)", quoteIdent(spec.Engine, table))
	rows, err := db.QueryContext(ctx, tableQuery)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var pk []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var defaultValue any
		var pkPos int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &defaultValue, &pkPos); err != nil {
			return nil, nil, nil, err
		}
		if pkPos > 0 {
			pk = append(pk, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	indexListQuery := fmt.Sprintf("pragma index_list(%s)", quoteIdent(spec.Engine, table))
	indexRows, err := db.QueryContext(ctx, indexListQuery)
	if err != nil {
		return nil, nil, nil, err
	}
	defer indexRows.Close()
	var unique []Constraint
	for indexRows.Next() {
		var seq int
		var name string
		var uniqueFlag int
		var origin string
		var partial int
		if err := indexRows.Scan(&seq, &name, &uniqueFlag, &origin, &partial); err != nil {
			return nil, nil, nil, err
		}
		if uniqueFlag == 0 || origin == "pk" {
			continue
		}
		infoRows, err := db.QueryContext(ctx, fmt.Sprintf("pragma index_info(%s)", quoteIdent(spec.Engine, name)))
		if err != nil {
			return nil, nil, nil, err
		}
		var cols []string
		for infoRows.Next() {
			var seqno, cid int
			var col string
			if err := infoRows.Scan(&seqno, &cid, &col); err != nil {
				infoRows.Close()
				return nil, nil, nil, err
			}
			cols = append(cols, col)
		}
		infoRows.Close()
		unique = append(unique, Constraint{Name: name, Columns: cols})
	}
	if err := indexRows.Err(); err != nil {
		return nil, nil, nil, err
	}

	fkRows, err := db.QueryContext(ctx, fmt.Sprintf("pragma foreign_key_list(%s)", quoteIdent(spec.Engine, table)))
	if err != nil {
		return nil, nil, nil, err
	}
	defer fkRows.Close()
	fkMap := map[string]*ForeignKey{}
	for fkRows.Next() {
		var id, seq int
		var refTable, fromCol, toCol, onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &refTable, &fromCol, &toCol, &onUpdate, &onDelete, &match); err != nil {
			return nil, nil, nil, err
		}
		name := fmt.Sprintf("fk_%d", id)
		item := fkMap[name]
		if item == nil {
			item = &ForeignKey{Name: name, RefTable: refTable}
			fkMap[name] = item
		}
		item.Columns = append(item.Columns, fromCol)
		item.RefColumns = append(item.RefColumns, toCol)
	}
	return pk, unique, foreignKeysFromMap(fkMap), nil
}

func listRelationsPostgres(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	if schema == "" {
		schema = "public"
	}
	query := `select tc.constraint_name, tc.table_schema, tc.table_name, kcu.column_name,
		ccu.table_schema, ccu.table_name, ccu.column_name
		from information_schema.table_constraints tc
		join information_schema.key_column_usage kcu
		  on tc.constraint_name = kcu.constraint_name and tc.table_schema = kcu.table_schema
		join information_schema.constraint_column_usage ccu
		  on tc.constraint_name = ccu.constraint_name and tc.table_schema = ccu.table_schema
		where tc.constraint_type = 'FOREIGN KEY' and tc.table_schema = $1
		order by tc.constraint_name, kcu.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationRows(rows)
}

func listRelationsMySQL(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	if schema == "" {
		schema = spec.Database
	}
	query := `select kcu.constraint_name, kcu.table_schema, kcu.table_name, kcu.column_name,
		coalesce(kcu.referenced_table_schema, ''), coalesce(kcu.referenced_table_name, ''), coalesce(kcu.referenced_column_name, '')
		from information_schema.key_column_usage kcu
		where kcu.table_schema = ? and kcu.referenced_table_name is not null
		order by kcu.constraint_name, kcu.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationRows(rows)
}

func listRelationsSQLite(ctx context.Context, spec conn.Spec) ([]RelationInfo, error) {
	tables, err := ListTables(ctx, spec, "")
	if err != nil {
		return nil, err
	}
	dbConn, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer dbConn.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	var relations []RelationInfo
	for _, table := range tables {
		rows, err := dbConn.QueryContext(ctx, fmt.Sprintf("pragma foreign_key_list(%s)", quoteIdent(spec.Engine, table.Name)))
		if err != nil {
			return nil, err
		}
		fkMap := map[string]*RelationInfo{}
		for rows.Next() {
			var id, seq int
			var refTable, fromCol, toCol, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &seq, &refTable, &fromCol, &toCol, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				return nil, err
			}
			name := fmt.Sprintf("fk_%s_%d", table.Name, id)
			item := fkMap[name]
			if item == nil {
				item = &RelationInfo{Name: name, FromSchema: "main", FromTable: table.Name, ToSchema: "main", ToTable: refTable}
				fkMap[name] = item
			}
			item.FromCols = append(item.FromCols, fromCol)
			item.ToCols = append(item.ToCols, toCol)
		}
		rows.Close()
		for _, item := range fkMap {
			relations = append(relations, *item)
		}
	}
	return relations, nil
}

func scanRelationRows(rows *sql.Rows) ([]RelationInfo, error) {
	relationsMap := map[string]*RelationInfo{}
	for rows.Next() {
		var name, fromSchema, fromTable, fromCol, toSchema, toTable, toCol string
		if err := rows.Scan(&name, &fromSchema, &fromTable, &fromCol, &toSchema, &toTable, &toCol); err != nil {
			return nil, err
		}
		item := relationsMap[name]
		if item == nil {
			item = &RelationInfo{Name: name, FromSchema: fromSchema, FromTable: fromTable, ToSchema: toSchema, ToTable: toTable}
			relationsMap[name] = item
		}
		item.FromCols = append(item.FromCols, fromCol)
		item.ToCols = append(item.ToCols, toCol)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var relations []RelationInfo
	for _, item := range relationsMap {
		relations = append(relations, *item)
	}
	return relations, nil
}

func constraintsFromMap(items map[string][]string) []Constraint {
	out := make([]Constraint, 0, len(items))
	for name, cols := range items {
		out = append(out, Constraint{Name: name, Columns: cols})
	}
	return out
}

func foreignKeysFromMap(items map[string]*ForeignKey) []ForeignKey {
	out := make([]ForeignKey, 0, len(items))
	for _, item := range items {
		out = append(out, *item)
	}
	return out
}

func ExportRows(rows []map[string]any, columns []Column, format string, out *os.File) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
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
