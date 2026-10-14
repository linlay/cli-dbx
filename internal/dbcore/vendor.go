package dbcore

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/linlay/cli-dbx/internal/conn"
	"sort"
)

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
	query := "select column_name, data_type, nullable, data_default from all_tab_columns where owner = " + bindSpec(spec, 1) + " and table_name = " + bindSpec(spec, 2) + " order by column_id"
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
	query := "select column_name, data_type, nullable, data_default from all_tab_columns where owner = " + bindSpec(spec, 1) + " and table_name = " + bindSpec(spec, 2) + " order by column_id"
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
	query = fmt.Sprintf(query, bindSpec(spec, 1), bindSpec(spec, 2))
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
		" where c.owner = " + bindSpec(config, 1) + " and c.table_name = " + bindSpec(config, 2) + " and c.constraint_type in ('P','U','R') order by c.constraint_name, cc.position"
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
		query = fmt.Sprintf(query, bindSpec(config, 1), bindSpec(config, 2), bindSpec(config, 3), bindSpec(config, 4))
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
		" where c.owner = " + bindSpec(spec, 1) + " and c.constraint_type = 'R' order by c.table_name, c.constraint_name, cc.position"
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
		" where c.owner = " + bindSpec(spec, 1) + " and c.constraint_type = 'R' order by c.table_name, c.constraint_name, cc.position"
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
where s.name = %s order by t.name, fk.name, fkc.constraint_column_id`, bindSpec(spec, 1))
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
