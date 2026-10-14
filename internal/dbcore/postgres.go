package dbcore

// Keep information_schema's visibility rules, but identify the actual
// constraint by its owning relation and map each referenced column by position.
// constraint_column_usage has one row per column, so joining it only by name
// multiplies composite keys and loses the foreign-key column pairing.
const postgresConstraintJoins = ` from information_schema.table_constraints tc
join information_schema.key_column_usage kcu
  on tc.constraint_name = kcu.constraint_name
 and tc.table_schema = kcu.table_schema and tc.table_name = kcu.table_name
join pg_catalog.pg_namespace ns on ns.nspname = tc.table_schema
join pg_catalog.pg_class rel on rel.relnamespace = ns.oid and rel.relname = tc.table_name
join pg_catalog.pg_constraint con on con.conrelid = rel.oid and con.conname = tc.constraint_name
left join pg_catalog.pg_class refrel on refrel.oid = con.confrelid
left join pg_catalog.pg_namespace refns on refns.oid = refrel.relnamespace
left join pg_catalog.pg_attribute refcol
  on refcol.attrelid = con.confrelid and refcol.attnum = con.confkey[kcu.ordinal_position]`
