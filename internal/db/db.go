package db

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	_ "gitee.com/chunanyong/dm"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	"github.com/oracle/go-oracledb/v26/oracle"
	_ "modernc.org/sqlite"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/odbcdriver"
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

type TxPlan struct {
	Steps []TxStep `json:"steps"`
}

type TxStep struct {
	Action          action.Action `json:"action"`
	SQL             string        `json:"sql"`
	MaxRowsAffected int           `json:"max_rows_affected,omitempty"`
}

type TxStepResult struct {
	Index        int           `json:"index"`
	Action       action.Action `json:"action"`
	RowsAffected int64         `json:"rows_affected,omitempty"`
	Query        *QueryResult  `json:"query,omitempty"`
}

type TxRunResult struct {
	Steps []TxStepResult `json:"steps"`
}

type queryRunner interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type execRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func Open(spec conn.Spec) (*sql.DB, error) {
	var db *sql.DB
	var err error
	if spec.Driver == "odbc" {
		if err := odbcdriver.Check(); err != nil {
			return nil, err
		}
		db, err = sql.Open(spec.Driver, spec.DSN)
	} else {
		db, err = openNative(spec)
	}
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(0)
	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(4)
	return db, nil
}

func openNative(spec conn.Spec) (*sql.DB, error) {
	if spec.Driver != "oracledb" {
		return sql.Open(spec.Driver, spec.DSN)
	}
	// The official Oracle driver takes structured connector configuration.
	u, err := url.Parse(spec.DSN)
	if err != nil || u.Scheme != "oracle" || u.User == nil || u.Host == "" || u.Path == "" {
		return nil, fmt.Errorf("invalid Oracle connection; use structured connection fields")
	}
	cfg := oracle.NewOracleDriverConfig()
	cfg.ConnectDescriptor = "tcp://" + u.Host + u.EscapedPath()
	cfg.Credentials.User = u.User.Username()
	cfg.Credentials.Password, _ = u.User.Password()
	connector, err := oracle.NewOracleConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

// These catalogs are shared by native Go and ODBC connections.
func usesVendorCatalog(engine string) bool {
	switch engine {
	case "oracle", "dm", "sqlserver":
		return true
	}
	return false
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
	return queryWithRunner(ctx, db, sqlText, offset, limit)
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
	count, err := executeWithRunner(ctx, tx, sqlText, maxRows, spec.Driver == "odbc" || usesVendorCatalog(spec.Engine))
	if err != nil {
		return count, err
	}
	if err := tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}

func RunTxPlan(ctx context.Context, spec conn.Spec, plan TxPlan, queryLimit, defaultMaxRows int) (TxRunResult, error) {
	db, err := Open(spec)
	if err != nil {
		return TxRunResult{}, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return TxRunResult{}, err
	}
	defer tx.Rollback()

	result := TxRunResult{Steps: make([]TxStepResult, 0, len(plan.Steps))}
	for i, step := range plan.Steps {
		item := TxStepResult{
			Index:  i,
			Action: step.Action,
		}
		switch step.Action {
		case action.Query:
			queryResult, err := queryWithRunner(ctx, tx, step.SQL, 0, queryLimit)
			if err != nil {
				return TxRunResult{}, err
			}
			item.Query = &queryResult
		case action.Update:
			maxRows := step.MaxRowsAffected
			if maxRows <= 0 {
				maxRows = defaultMaxRows
			}
			count, err := executeWithRunner(ctx, tx, step.SQL, maxRows, spec.Driver == "odbc" || usesVendorCatalog(spec.Engine))
			if err != nil {
				return TxRunResult{}, err
			}
			item.RowsAffected = count
		default:
			return TxRunResult{}, fmt.Errorf("transaction action %s is not supported", step.Action)
		}
		result.Steps = append(result.Steps, item)
	}
	if err := tx.Commit(); err != nil {
		return TxRunResult{}, err
	}
	return result, nil
}

func queryWithRunner(ctx context.Context, runner queryRunner, sqlText string, offset, limit int) (QueryResult, error) {
	rows, err := runner.QueryContext(ctx, sqlText)
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

func executeWithRunner(ctx context.Context, runner execRunner, sqlText string, maxRows int, requireCount bool) (int64, error) {
	res, err := runner.ExecContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}
	count, countErr := res.RowsAffected()
	if requireCount && (countErr != nil || count < 0) {
		return 0, fmt.Errorf("driver did not provide a reliable affected-row count; write protection cannot be verified")
	}
	if maxRows > 0 && count > int64(maxRows) {
		return count, fmt.Errorf("rows affected %d exceeds max-rows-affected %d", count, maxRows)
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
	case "oracle":
		query = "select username from all_users order by username"
	case "dm":
		query = "select name from sysobjects where type$ = 'SCH' order by name"
	case "sqlserver":
		query = "select name from sys.schemas order by name"
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
		query = "select table_schema, table_name, table_type from information_schema.tables where table_schema = " + bind(spec, 1) + " order by table_name"
		args = append(args, schema)
	case "mysql":
		if schema == "" {
			schema = spec.Database
		}
		query = "select table_schema, table_name, table_type from information_schema.tables where table_schema = ? order by table_name"
		args = append(args, schema)
	case "oracle", "dm", "sqlserver":
		schema, err = vendorCurrentSchema(ctx, db, spec.Engine, spec, schema)
		if err != nil {
			return nil, err
		}
		query = "select owner, object_name, object_type from all_objects where owner = " + bind(spec, 1) + " and object_type in ('TABLE', 'VIEW') order by object_name"
		if spec.Engine == "sqlserver" {
			query = "select table_schema, table_name, table_type from information_schema.tables where table_schema = " + bind(spec, 1) + " order by table_name"
		}
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
		query = fmt.Sprintf(`select column_name, data_type, is_nullable = 'YES', coalesce(column_default, '')
			from information_schema.columns
			where table_schema = %s and table_name = %s
			order by ordinal_position`, bind(spec, 1), bind(spec, 2))
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
	case "oracle":
		return describeTableOracle(ctx, db, spec, schema, table)
	case "dm":
		return describeTableDM(ctx, db, spec, schema, table)
	case "sqlserver":
		return describeTableSQLServer(ctx, db, spec, schema, table)
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
	case "oracle":
		return listRelationsOracle(ctx, spec, schema)
	case "dm":
		return listRelationsDM(ctx, spec, schema)
	case "sqlserver":
		return listRelationsSQLServer(ctx, spec, schema)
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

	query := fmt.Sprintf(`select tc.constraint_name, tc.constraint_type, kcu.column_name,
		coalesce(ccu.table_schema, ''), coalesce(ccu.table_name, ''), coalesce(ccu.column_name, '')
		from information_schema.table_constraints tc
		left join information_schema.key_column_usage kcu
		  on tc.constraint_name = kcu.constraint_name
		 and tc.table_schema = kcu.table_schema
		 and tc.table_name = kcu.table_name
		left join information_schema.constraint_column_usage ccu
		  on tc.constraint_name = ccu.constraint_name
		 and tc.table_schema = ccu.table_schema
		where tc.table_schema = %s and tc.table_name = %s
		  and tc.constraint_type in ('PRIMARY KEY', 'UNIQUE', 'FOREIGN KEY')
		order by tc.constraint_name, kcu.ordinal_position`, bind(spec, 1), bind(spec, 2))
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
	query := fmt.Sprintf(`select tc.constraint_name, tc.table_schema, tc.table_name, kcu.column_name,
		ccu.table_schema, ccu.table_name, ccu.column_name
		from information_schema.table_constraints tc
		join information_schema.key_column_usage kcu
		  on tc.constraint_name = kcu.constraint_name and tc.table_schema = kcu.table_schema
		join information_schema.constraint_column_usage ccu
		  on tc.constraint_name = ccu.constraint_name and tc.table_schema = ccu.table_schema
		where tc.constraint_type = 'FOREIGN KEY' and tc.table_schema = %s
		order by tc.constraint_name, kcu.ordinal_position`, bind(spec, 1))
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
		placeholders = append(placeholders, bind(spec, i+1))
	}
	quotedTable, err := QuoteTable(spec.Engine, table)
	if err != nil {
		return 0, err
	}
	stmt := fmt.Sprintf("insert into %s (%s) values (%s)",
		quotedTable,
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

func bind(spec conn.Spec, n int) string {
	if spec.Driver == "odbc" {
		return "?"
	}
	switch spec.Engine {
	case "postgres":
		return fmt.Sprintf("$%d", n)
	case "oracle":
		return fmt.Sprintf(":%d", n)
	case "sqlserver":
		return fmt.Sprintf("@p%d", n)
	}
	return "?"
}

// QuoteTable quotes a plain table name or dot-separated qualified name.
// Each component is treated as a literal identifier, not a SQL fragment.
func QuoteTable(engine, table string) (string, error) {
	parts := strings.Split(table, ".")
	for i, part := range parts {
		if part == "" || strings.ContainsRune(part, '\x00') {
			return "", fmt.Errorf("table identifier components must be non-empty and contain no NUL")
		}
		parts[i] = quoteIdent(engine, part)
	}
	return strings.Join(parts, "."), nil
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
	case "sqlserver":
		return "[" + strings.ReplaceAll(ident, "]", "]]") + "]"
	default:
		return `"` + escaped + `"`
	}
}

func vendorCurrentSchema(ctx context.Context, db *sql.DB, engine string, config conn.Spec, schema string) (string, error) {
	if schema != "" {
		return schema, nil
	}
	if config.Schema != "" {
		return config.Schema, nil
	}
	query := "select SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') from dual"
	if engine == "sqlserver" {
		query = "select SCHEMA_NAME()"
	}
	var current sql.NullString
	if err := db.QueryRowContext(ctx, query).Scan(&current); err != nil {
		return "", err
	}
	if !current.Valid || current.String == "" {
		return "", fmt.Errorf("cannot determine current schema for %s; configure schema explicitly", engine)
	}
	return current.String, nil
}

func describeTableOracle(ctx context.Context, db *sql.DB, spec conn.Spec, schema, table string) (TableInfo, error) {
	schema, err := vendorCurrentSchema(ctx, db, "oracle", spec, schema)
	if err != nil {
		return TableInfo{}, err
	}
	query := "select column_name, data_type, nullable, data_default from all_tab_columns where owner = " + bind(spec, 1) + " and table_name = " + bind(spec, 2) + " order by column_id"
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return TableInfo{}, err
	}
	info := TableInfo{Schema: schema, Name: table}
	for rows.Next() {
		var column Column
		var nullable string
		var defaultValue sql.NullString
		if err := rows.Scan(&column.Name, &column.Type, &nullable, &defaultValue); err != nil {
			rows.Close()
			return TableInfo{}, err
		}
		column.Nullable = nullable == "Y" || nullable == "YES"
		column.DefaultValue = defaultValue.String
		info.Columns = append(info.Columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TableInfo{}, err
	}
	if err := rows.Close(); err != nil {
		return TableInfo{}, err
	}
	if len(info.Columns) == 0 {
		return TableInfo{}, fmt.Errorf("table %s.%s does not exist or its metadata is not accessible", schema, table)
	}
	if err := vendorLoadConstraints(ctx, db, "oracle", spec, &info); err != nil {
		return TableInfo{}, err
	}
	for index := range info.Columns {
		for _, key := range info.PrimaryKey {
			if info.Columns[index].Name == key {
				info.Columns[index].PrimaryKey = true
			}
		}
	}
	return info, nil
}

func describeTableDM(ctx context.Context, db *sql.DB, spec conn.Spec, schema, table string) (TableInfo, error) {
	schema, err := vendorCurrentSchema(ctx, db, "dm", spec, schema)
	if err != nil {
		return TableInfo{}, err
	}
	query := "select column_name, data_type, nullable, data_default from all_tab_columns where owner = " + bind(spec, 1) + " and table_name = " + bind(spec, 2) + " order by column_id"
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return TableInfo{}, err
	}
	info := TableInfo{Schema: schema, Name: table}
	for rows.Next() {
		var column Column
		var nullable string
		var defaultValue sql.NullString
		if err := rows.Scan(&column.Name, &column.Type, &nullable, &defaultValue); err != nil {
			rows.Close()
			return TableInfo{}, err
		}
		column.Nullable = nullable == "Y" || nullable == "YES"
		column.DefaultValue = defaultValue.String
		info.Columns = append(info.Columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TableInfo{}, err
	}
	if err := rows.Close(); err != nil {
		return TableInfo{}, err
	}
	if len(info.Columns) == 0 {
		return TableInfo{}, fmt.Errorf("table %s.%s does not exist or its metadata is not accessible", schema, table)
	}
	if err := vendorLoadConstraints(ctx, db, "dm", spec, &info); err != nil {
		return TableInfo{}, err
	}
	for index := range info.Columns {
		for _, key := range info.PrimaryKey {
			if info.Columns[index].Name == key {
				info.Columns[index].PrimaryKey = true
			}
		}
	}
	return info, nil
}

func describeTableSQLServer(ctx context.Context, db *sql.DB, spec conn.Spec, schema, table string) (TableInfo, error) {
	schema, err := vendorCurrentSchema(ctx, db, "sqlserver", spec, schema)
	if err != nil {
		return TableInfo{}, err
	}
	query := `select c.name, ty.name, case when c.is_nullable = 1 then 'Y' else 'N' end, d.definition
from sys.columns c join sys.objects o on o.object_id = c.object_id
join sys.schemas s on s.schema_id = o.schema_id
join sys.types ty on ty.user_type_id = c.user_type_id
left join sys.default_constraints d on d.object_id = c.default_object_id
where s.name = %s and o.name = %s and o.type in ('U', 'V') order by c.column_id`
	query = fmt.Sprintf(query, bind(spec, 1), bind(spec, 2))
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return TableInfo{}, err
	}
	info := TableInfo{Schema: schema, Name: table}
	for rows.Next() {
		var column Column
		var nullable string
		var defaultValue sql.NullString
		if err := rows.Scan(&column.Name, &column.Type, &nullable, &defaultValue); err != nil {
			rows.Close()
			return TableInfo{}, err
		}
		column.Nullable = nullable == "Y" || nullable == "YES"
		column.DefaultValue = defaultValue.String
		info.Columns = append(info.Columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TableInfo{}, err
	}
	if err := rows.Close(); err != nil {
		return TableInfo{}, err
	}
	if len(info.Columns) == 0 {
		return TableInfo{}, fmt.Errorf("table %s.%s does not exist or its metadata is not accessible", schema, table)
	}
	if err := vendorLoadConstraints(ctx, db, "sqlserver", spec, &info); err != nil {
		return TableInfo{}, err
	}
	for index := range info.Columns {
		for _, key := range info.PrimaryKey {
			if info.Columns[index].Name == key {
				info.Columns[index].PrimaryKey = true
			}
		}
	}
	return info, nil
}

const oracleConstraintJoins = ` from all_constraints c
join all_cons_columns cc on cc.owner = c.owner and cc.constraint_name = c.constraint_name and cc.table_name = c.table_name
left join all_constraints rc on rc.owner = c.r_owner and rc.constraint_name = c.r_constraint_name
left join all_cons_columns rcc on rcc.owner = rc.owner and rcc.constraint_name = rc.constraint_name and rcc.table_name = rc.table_name and rcc.position = cc.position`

func vendorLoadConstraints(ctx context.Context, db *sql.DB, engine string, config conn.Spec, info *TableInfo) error {
	query := `select c.constraint_name, c.constraint_type, cc.column_name, rc.owner, rc.table_name, rcc.column_name` + oracleConstraintJoins +
		" where c.owner = " + bind(config, 1) + " and c.table_name = " + bind(config, 2) + " and c.constraint_type in ('P','U','R') order by c.constraint_name, cc.position"
	if engine == "sqlserver" {
		query = `select constraint_name, constraint_type, column_name, ref_schema, ref_table, ref_column from (
select k.name constraint_name, case when k.type = 'PK' then 'P' else 'U' end constraint_type,
col.name column_name, cast(null as nvarchar(128)) ref_schema, cast(null as nvarchar(128)) ref_table,
cast(null as nvarchar(128)) ref_column, ic.key_ordinal ordinal
from sys.key_constraints k join sys.tables t on t.object_id = k.parent_object_id
join sys.schemas s on s.schema_id = t.schema_id
join sys.index_columns ic on ic.object_id = t.object_id and ic.index_id = k.unique_index_id
join sys.columns col on col.object_id = t.object_id and col.column_id = ic.column_id
where s.name = %s and t.name = %s and ic.key_ordinal > 0
union all
select fk.name, 'R', col.name, rs.name, rt.name, rcol.name, fkc.constraint_column_id
from sys.foreign_keys fk join sys.tables t on t.object_id = fk.parent_object_id
join sys.schemas s on s.schema_id = t.schema_id
join sys.foreign_key_columns fkc on fkc.constraint_object_id = fk.object_id
join sys.columns col on col.object_id = t.object_id and col.column_id = fkc.parent_column_id
left join sys.tables rt on rt.object_id = fkc.referenced_object_id
left join sys.schemas rs on rs.schema_id = rt.schema_id
left join sys.columns rcol on rcol.object_id = fkc.referenced_object_id and rcol.column_id = fkc.referenced_column_id
where s.name = %s and t.name = %s) keys_metadata order by constraint_name, ordinal`
		query = fmt.Sprintf(query, bind(config, 1), bind(config, 2), bind(config, 3), bind(config, 4))
	}
	args := []any{info.Schema, info.Name}
	if engine == "sqlserver" {
		args = append(args, info.Schema, info.Name)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	unique := map[string][]string{}
	foreign := map[string]*ForeignKey{}
	for rows.Next() {
		var name, kind, column string
		var refSchema, refTable, refColumn sql.NullString
		if err := rows.Scan(&name, &kind, &column, &refSchema, &refTable, &refColumn); err != nil {
			return err
		}
		switch kind {
		case "P":
			info.PrimaryKey = append(info.PrimaryKey, column)
		case "U":
			unique[name] = append(unique[name], column)
		case "R":
			if !refSchema.Valid || !refTable.Valid || !refColumn.Valid {
				return fmt.Errorf("referenced metadata for foreign key %s is not accessible", name)
			}
			key := foreign[name]
			if key == nil {
				key = &ForeignKey{Name: name, RefSchema: refSchema.String, RefTable: refTable.String}
				foreign[name] = key
			}
			key.Columns = append(key.Columns, column)
			key.RefColumns = append(key.RefColumns, refColumn.String)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	info.UniqueKeys = constraintsFromMap(unique)
	sort.Slice(info.UniqueKeys, func(i, j int) bool { return info.UniqueKeys[i].Name < info.UniqueKeys[j].Name })
	info.ForeignKeys = foreignKeysFromMap(foreign)
	sort.Slice(info.ForeignKeys, func(i, j int) bool { return info.ForeignKeys[i].Name < info.ForeignKeys[j].Name })
	return nil
}

func listRelationsOracle(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	if schema == "" {
		schema = spec.Schema
	}
	if schema == "" {
		var current sql.NullString
		if err := db.QueryRowContext(ctx, "select SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') from dual").Scan(&current); err != nil {
			return nil, err
		}
		if !current.Valid || current.String == "" {
			return nil, fmt.Errorf("cannot determine current schema for %s; configure schema explicitly", spec.Engine)
		}
		schema = current.String
	}
	query := `select c.constraint_name, c.owner, c.table_name, cc.column_name, rc.owner, rc.table_name, rcc.column_name from all_constraints c
join all_cons_columns cc on cc.owner = c.owner and cc.constraint_name = c.constraint_name and cc.table_name = c.table_name
left join all_constraints rc on rc.owner = c.r_owner and rc.constraint_name = c.r_constraint_name
left join all_cons_columns rcc on rcc.owner = rc.owner and rcc.constraint_name = rc.constraint_name and rcc.table_name = rc.table_name and rcc.position = cc.position` +
		" where c.owner = " + bind(spec, 1) + " and c.constraint_type = 'R' order by c.table_name, c.constraint_name, cc.position"
	rows, err := db.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []RelationInfo
	indices := map[[3]string]int{}
	for rows.Next() {
		var name, fromSchema, fromTable, fromColumn string
		var toSchema, toTable, toColumn sql.NullString
		if err := rows.Scan(&name, &fromSchema, &fromTable, &fromColumn, &toSchema, &toTable, &toColumn); err != nil {
			return nil, err
		}
		if !toSchema.Valid || !toTable.Valid || !toColumn.Valid {
			return nil, fmt.Errorf("referenced metadata for foreign key %s.%s.%s is not accessible", fromSchema, fromTable, name)
		}
		key := [3]string{fromSchema, fromTable, name}
		index, ok := indices[key]
		if !ok {
			index = len(relations)
			indices[key] = index
			relations = append(relations, RelationInfo{Name: name, FromSchema: fromSchema, FromTable: fromTable, ToSchema: toSchema.String, ToTable: toTable.String})
		}
		relations[index].FromCols = append(relations[index].FromCols, fromColumn)
		relations[index].ToCols = append(relations[index].ToCols, toColumn.String)
	}
	return relations, rows.Err()
}

func listRelationsDM(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	if schema == "" {
		schema = spec.Schema
	}
	if schema == "" {
		var current sql.NullString
		if err := db.QueryRowContext(ctx, "select SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') from dual").Scan(&current); err != nil {
			return nil, err
		}
		if !current.Valid || current.String == "" {
			return nil, fmt.Errorf("cannot determine current schema for %s; configure schema explicitly", spec.Engine)
		}
		schema = current.String
	}
	query := `select c.constraint_name, c.owner, c.table_name, cc.column_name, rc.owner, rc.table_name, rcc.column_name from all_constraints c
join all_cons_columns cc on cc.owner = c.owner and cc.constraint_name = c.constraint_name and cc.table_name = c.table_name
left join all_constraints rc on rc.owner = c.r_owner and rc.constraint_name = c.r_constraint_name
left join all_cons_columns rcc on rcc.owner = rc.owner and rcc.constraint_name = rc.constraint_name and rcc.table_name = rc.table_name and rcc.position = cc.position` +
		" where c.owner = " + bind(spec, 1) + " and c.constraint_type = 'R' order by c.table_name, c.constraint_name, cc.position"
	rows, err := db.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []RelationInfo
	indices := map[[3]string]int{}
	for rows.Next() {
		var name, fromSchema, fromTable, fromColumn string
		var toSchema, toTable, toColumn sql.NullString
		if err := rows.Scan(&name, &fromSchema, &fromTable, &fromColumn, &toSchema, &toTable, &toColumn); err != nil {
			return nil, err
		}
		if !toSchema.Valid || !toTable.Valid || !toColumn.Valid {
			return nil, fmt.Errorf("referenced metadata for foreign key %s.%s.%s is not accessible", fromSchema, fromTable, name)
		}
		key := [3]string{fromSchema, fromTable, name}
		index, ok := indices[key]
		if !ok {
			index = len(relations)
			indices[key] = index
			relations = append(relations, RelationInfo{Name: name, FromSchema: fromSchema, FromTable: fromTable, ToSchema: toSchema.String, ToTable: toTable.String})
		}
		relations[index].FromCols = append(relations[index].FromCols, fromColumn)
		relations[index].ToCols = append(relations[index].ToCols, toColumn.String)
	}
	return relations, rows.Err()
}

func listRelationsSQLServer(ctx context.Context, spec conn.Spec, schema string) ([]RelationInfo, error) {
	db, err := Open(spec)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	if schema == "" {
		schema = spec.Schema
	}
	if schema == "" {
		var current sql.NullString
		if err := db.QueryRowContext(ctx, "select SCHEMA_NAME()").Scan(&current); err != nil {
			return nil, err
		}
		if !current.Valid || current.String == "" {
			return nil, fmt.Errorf("cannot determine current schema for %s; configure schema explicitly", spec.Engine)
		}
		schema = current.String
	}
	query := fmt.Sprintf(`select fk.name, s.name, t.name, col.name, rs.name, rt.name, rcol.name
from sys.foreign_keys fk join sys.tables t on t.object_id = fk.parent_object_id
join sys.schemas s on s.schema_id = t.schema_id
join sys.foreign_key_columns fkc on fkc.constraint_object_id = fk.object_id
join sys.columns col on col.object_id = t.object_id and col.column_id = fkc.parent_column_id
left join sys.tables rt on rt.object_id = fkc.referenced_object_id
left join sys.schemas rs on rs.schema_id = rt.schema_id
left join sys.columns rcol on rcol.object_id = fkc.referenced_object_id and rcol.column_id = fkc.referenced_column_id
where s.name = %s order by t.name, fk.name, fkc.constraint_column_id`, bind(spec, 1))
	rows, err := db.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []RelationInfo
	indices := map[[3]string]int{}
	for rows.Next() {
		var name, fromSchema, fromTable, fromColumn string
		var toSchema, toTable, toColumn sql.NullString
		if err := rows.Scan(&name, &fromSchema, &fromTable, &fromColumn, &toSchema, &toTable, &toColumn); err != nil {
			return nil, err
		}
		if !toSchema.Valid || !toTable.Valid || !toColumn.Valid {
			return nil, fmt.Errorf("referenced metadata for foreign key %s.%s.%s is not accessible", fromSchema, fromTable, name)
		}
		key := [3]string{fromSchema, fromTable, name}
		index, ok := indices[key]
		if !ok {
			index = len(relations)
			indices[key] = index
			relations = append(relations, RelationInfo{Name: name, FromSchema: fromSchema, FromTable: fromTable, ToSchema: toSchema.String, ToTable: toTable.String})
		}
		relations[index].FromCols = append(relations[index].FromCols, fromColumn)
		relations[index].ToCols = append(relations[index].ToCols, toColumn.String)
	}
	return relations, rows.Err()
}
