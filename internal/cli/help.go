package cli

import (
	"fmt"

	"github.com/linlay/cli-dbx/internal/buildinfo"
)

func rootHelp() string {
	return `dbx

Use:
  conn      test or show a connection
  inspect   inspect schema, table, or connection
  query     run read-only SQL
  update    run row-changing SQL
  schema    run DDL SQL
  admin     run admin SQL
  tx        run a structured transaction plan
  import    load csv/json into a table
  export    write a table to a file
  secret    encrypt passwords for config files
  version   show build version

Flow:
  1. dbx conn test <name>
  2. dbx inspect table <name> <table>
  3. dbx query <name> 'select ...'
  Config files live in ~/.config/dbx/<name>.toml by default.
  AP_AGENT_CONFIG_HOME/dbx/<name>.toml takes priority when no --config is given.

Example:
  dbx query local-pg 'select * from users order by id' --page-size 100
  dbx update local-pg 'update users set active = 1 where id = 1'
  dbx secret encrypt
  dbx version
`
}

func helpExamples() string {
	return `Examples

PostgreSQL: inspect users
  dbx conn test local-pg
  dbx inspect table local-pg users
  dbx query local-pg 'select id, email from users limit 10'

MySQL: import customers.csv
  dbx conn test local-mysql
  dbx inspect table local-mysql customers
  dbx import file ./customers.csv local-mysql customers

SQLite: continue a paged read
  dbx query local-sqlite 'select * from users order by id' --page-size 100
  dbx query local-sqlite 'select * from users order by id' --cursor 100

PostgreSQL: run a transaction plan
  {"steps":[{"action":"query","sql":"select id from users where id = 1"},{"action":"update","sql":"update users set active = 1 where id = 1","max_rows_affected":1}]}
  dbx tx local-pg --plan ./plan.json
`
}

func connHelp() string {
	return `dbx conn

When to use:
  Check a target before inspect or query.

Commands:
  list
  test <name>
  show <name>

Examples:
  dbx conn test local-pg
  dbx conn show local-mysql
  dbx conn list

Next:
  Use inspect or query.
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
  dbx inspect table local-pg users
  dbx inspect schema local-pg
  dbx inspect connection local-pg

Next:
  Use query after you know the table shape.
`
}

func sqlCommandHelp(command string, summary string, examples []string) string {
	return fmt.Sprintf(`dbx %s

When to use:
  %s

Minimum:
  <conn> '<statement>'
  dsn <engine> <dsn> '<statement>'
  file <conn> <path.sql>

Facts:
  DBX maps each statement to an action and checks allow_actions and allow_tables.
  Read results return up to 100 rows by default.
  Multiple statements are blocked by default.
  Keep the same order by when you continue with --cursor.

Options:
  --page-size <n>               read page size; default 100
  --cursor <n>                  continue from data.next_cursor
  --format <json|table>         result format; default json
  --verbose                     include engine, risk, and meta
  --dry-run                     validate policy without executing
  --config <path>               read only a specific config file or directory
  --max-rows-affected <n>       write safety limit; default 1000

Examples:
  %s

Next:
  Use inspect first if the table shape is unknown.
`, command, summary, joinHelpExamples(examples))
}

func joinHelpExamples(examples []string) string {
	if len(examples) == 0 {
		return ""
	}
	out := examples[0]
	for i := 1; i < len(examples); i++ {
		out += "\n  " + examples[i]
	}
	return out
}

func queryHelp() string {
	return sqlCommandHelp("query", "Run read-only SQL.", []string{
		"dbx query local-pg 'select * from users order by id'",
		"dbx query local-pg 'select * from users order by id' --cursor 100",
		"dbx query dsn postgres 'postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable' 'select now()'",
	})
}

func updateHelp() string {
	return sqlCommandHelp("update", "Run insert, update, delete, or merge SQL.", []string{
		"dbx update local-pg 'update users set active = 1 where id = 1'",
		"dbx update local-pg 'delete from users where archived = 1'",
		"dbx update file local-pg ./change.sql",
	})
}

func schemaHelp() string {
	return sqlCommandHelp("schema", "Run create, alter, drop, rename, or truncate SQL.", []string{
		"dbx schema local-pg 'create table audit_log (id bigint primary key)'",
		"dbx schema local-pg 'alter table users add column timezone text'",
		"dbx schema file local-pg ./schema.sql",
	})
}

func adminHelp() string {
	return sqlCommandHelp("admin", "Run supported admin SQL such as grant, revoke, set, or vacuum.", []string{
		"dbx admin local-pg 'analyze users'",
		"dbx admin local-sqlite 'vacuum'",
		"dbx admin file local-pg ./admin.sql",
	})
}

func txHelp() string {
	return `dbx tx

When to use:
  Run a structured multi-step transaction in one DBX call.

Minimum:
  <conn> --plan <path.json>

Facts:
  tx only accepts query and update steps.
  Every step runs on one connection inside one transaction.
  Any failure rolls the whole transaction back.
  Use tx when a sequence of reads and writes must commit together.

Example plan:
  {"steps":[{"action":"query","sql":"select id from users where id = 1"},{"action":"update","sql":"update users set active = 1 where id = 1","max_rows_affected":1}]}

Example:
  dbx tx local-pg --plan ./plan.json
  dbx query local-pg 'select id, active from users where id = 1'

Next:
  Use query to verify the committed result.
`
}

func importHelp() string {
	return `dbx import

When to use:
  Load csv or json rows into a table.

Example:
  dbx import file ./customers.csv local-mysql customers

Next:
  Use query to verify imported rows.
`
}

func exportHelp() string {
	return `dbx export

When to use:
  Write a table to csv or json.

Options:
  --format <csv|json>           export file format; default csv
  --limit <n>                   limit rows written to the file
  --config <path>               read only a specific config file or directory
  --verbose                     include engine and meta

Example:
  dbx export table users local-sqlite ./users.csv --format csv

Next:
  Use inspect if you need to confirm columns first.
`
}

func versionHelp() string {
	return `dbx version

When to use:
  Show the embedded version, commit, and build time.

Examples:
  dbx version
  dbx --version
`
}

func secretHelp() string {
	return `dbx secret

When to use:
  Encrypt a database password before putting it in ~/.config/dbx/<name>.toml.

Behavior:
  Stores a per-value encryption key in the operating system credential store.
  The generated v2 value is bound to the current machine and OS user.
  With no password argument, prompts without terminal echo.
  With one password argument, encrypts immediately without reading stdin.

Warning:
  A password argument is visible to the calling agent, shell history, command
  audit, and process inspection. Use it only when one-time exposure is acceptable.

Commands:
  encrypt

Examples:
  dbx secret encrypt
  dbx secret encrypt '<password>'
  dbx secret encrypt -- '-password'

Next:
  Store the output as password = "dbx-aes-gcm:v2:..." in the connection file.
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
	case "query":
		fmt.Print(queryHelp())
	case "update":
		fmt.Print(updateHelp())
	case "schema":
		fmt.Print(schemaHelp())
	case "admin":
		fmt.Print(adminHelp())
	case "tx":
		fmt.Print(txHelp())
	case "import":
		fmt.Print(importHelp())
	case "export":
		fmt.Print(exportHelp())
	case "secret":
		fmt.Print(secretHelp())
	case "version":
		fmt.Print(versionHelp())
	default:
		return fmt.Errorf("unknown help topic %q", topic)
	}
	return nil
}

func printVersion() error {
	fmt.Println(buildinfo.Summary())
	return nil
}
