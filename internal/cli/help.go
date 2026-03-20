package cli

import "fmt"

func rootHelp() string {
	return `dbx

Use:
  conn      test or show a connection
  inspect   inspect schema, table, or connection
  exec      run any SQL
  import    load csv/json into a table
  export    write a table to a file

Flow:
  1. dbx conn test <name>
  2. dbx inspect table --conn <name> <table>
  3. dbx exec --conn <name> --sql 'select ...'

Example:
  dbx exec --conn local-pg --sql 'select * from users limit 5'
`
}

func helpExamples() string {
	return `Examples

PostgreSQL: inspect users
  dbx conn test local-pg
  dbx inspect table --conn local-pg users
  dbx exec --conn local-pg --sql 'select id, email from users limit 10'

MySQL: import customers.csv
  dbx conn test local-mysql
  dbx inspect table --conn local-mysql customers
  dbx import file ./customers.csv --conn local-mysql --into customers --mode Tweezers

SQLite: export users to csv
  dbx inspect table --conn local-sqlite users
  dbx exec --conn local-sqlite --sql 'select * from users order by id desc'
  dbx export table users --conn local-sqlite --format csv --out ./users.csv
`
}

func connHelp() string {
	return `dbx conn

When to use:
  Check a target before inspect or exec.

Commands:
  list
  test <name>
  show <name>

Examples:
  dbx conn test local-pg
  dbx conn show local-mysql

Next:
  Use inspect or exec.
`
}

func inspectHelp() string {
	return `dbx inspect

When to use:
  Look at schema or table shape before writing SQL.

Commands:
  schema
  table <name>
  connection

Examples:
  dbx inspect schema --conn local-pg
  dbx inspect table --conn local-pg users

Next:
  Use exec after you know the table shape.
`
}

func execHelp() string {
	return `dbx exec

When to use:
  Run any SQL: read, write, ddl, or admin.

Minimum:
  --conn <name> --sql '<statement>'

Facts:
  Read results are sampled by default.
  Risky writes may require --require-ack.
  Use --verbose only when you need more context.

Modes:
  Lantern   read only; default for selects
  Tweezers  read + row writes; use for insert/update/delete
  Chisel    read + ddl; use for create/alter/drop
  Forge     read + writes + ddl; requires --require-ack
  Crown     admin access; requires --require-ack
  Wildfire  unrestricted; requires --require-ack

Mode examples:
  --mode Tweezers   load or edit rows
  --mode Chisel     create or alter tables
  --mode Forge      mixed data + schema work

Examples:
  dbx exec --conn local-pg --sql 'select * from users limit 5'
  dbx exec --engine postgres --dsn 'postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable' --sql 'select now()'
  dbx exec --conn local-sqlite --mode Chisel --require-ack --sql 'create table users (id integer primary key, name text)'

Next:
  Use inspect first if the table shape is unknown.
`
}

func importHelp() string {
	return `dbx import

When to use:
  Load csv or json rows into a table.

Example:
  dbx import file ./customers.csv --conn local-mysql --into customers --mode Tweezers

Next:
  Use exec to verify imported rows.
`
}

func exportHelp() string {
	return `dbx export

When to use:
  Write a table to csv, json, or jsonl.

Example:
  dbx export table users --conn local-sqlite --format csv --out ./users.csv

Next:
  Use inspect if you need to confirm columns first.
`
}

func printHelp(topic string) error {
	switch topic {
	case "", "root":
		fmt.Print(rootHelp())
	case "examples":
		fmt.Print(helpExamples())
	case "conn":
		fmt.Print(connHelp())
	case "inspect":
		fmt.Print(inspectHelp())
	case "exec":
		fmt.Print(execHelp())
	case "import":
		fmt.Print(importHelp())
	case "export":
		fmt.Print(exportHelp())
	default:
		return fmt.Errorf("unknown help topic %q", topic)
	}
	return nil
}
