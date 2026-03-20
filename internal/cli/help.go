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
  2. dbx inspect table <name> <table>
  3. dbx exec <name> 'select ...'
  Omit <name> to use the default connection.

Example:
  dbx exec local-pg 'select * from users limit 5'
`
}

func helpExamples() string {
	return `Examples

PostgreSQL: inspect users
  dbx conn test local-pg
  dbx inspect table local-pg users
  dbx exec local-pg 'select id, email from users limit 10'

MySQL: import customers.csv
  dbx conn test local-mysql
  dbx inspect table local-mysql customers
  dbx import file ./customers.csv local-mysql customers --mode Tweezers

SQLite: export users to csv
  dbx inspect table local-sqlite users
  dbx exec local-sqlite 'select * from users order by id desc'
  dbx export table users local-sqlite ./users.csv --format csv
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
  dbx conn test

Next:
  Use inspect or exec.
`
}

func inspectHelp() string {
	return `dbx inspect

When to use:
  Look at schema, keys, and relations before writing SQL.

Commands:
  schema
  table <name>
  connection

Examples:
  dbx inspect table users
  dbx inspect schema local-pg
  dbx inspect table local-pg users

Next:
  Use exec after you know the table shape.
`
}

func execHelp() string {
	return `dbx exec

When to use:
  Run any SQL: read, write, ddl, or admin.

Minimum:
  '<statement>'
  <conn> '<statement>'
  dsn <engine> <dsn> '<statement>'
  file <conn> <path.sql>

Facts:
  Read results are sampled by default.
  Multiple statements are blocked by default.
  Use --verbose only when you need more context.
  Use --cursor <n> to continue a paged read.

Modes:
  Lantern   read only; default for selects
  Tweezers  read + row writes; use for insert/update/delete
  Chisel    read + ddl; use for create/alter/drop
  Forge     read + writes + ddl
  Crown     admin access
  Wildfire  unrestricted

Mode examples:
  --mode Tweezers   load or edit rows
  --mode Chisel     create or alter tables
  --mode Forge      mixed data + schema work

Examples:
  dbx exec 'select * from users limit 5'
  dbx exec local-pg 'select * from users limit 5'
  dbx exec dsn postgres 'postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable' 'select now()'
  dbx exec local-sqlite 'create table users (id integer primary key, name text)' --mode Chisel

Next:
  Use inspect first if the table shape is unknown.
`
}

func importHelp() string {
	return `dbx import

When to use:
  Load csv or json rows into a table.

Example:
  dbx import file ./customers.csv customers --mode Tweezers
  dbx import file ./customers.csv local-mysql customers --mode Tweezers

Next:
  Use exec to verify imported rows.
`
}

func exportHelp() string {
	return `dbx export

When to use:
  Write a table to csv, json, or jsonl.

Example:
  dbx export table users ./users.csv --format csv
  dbx export table users local-sqlite ./users.csv --format csv

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
