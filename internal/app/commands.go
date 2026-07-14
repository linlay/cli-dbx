package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type commonFlags struct {
	configPath string
	format     string
	dryRun     bool
	pageSize   int
	maxRows    int
	cursor     string
	verbose    bool
}

type connFlags struct {
	configPath string
	format     string
	verbose    bool
}

type exportFlags struct {
	configPath string
	format     string
	verbose    bool
	limit      int
}

func newConnCommand() *cobra.Command {
	flags := &connFlags{}

	cmd := &cobra.Command{
		Use:   "conn",
		Short: "Test or show a connection",
		Long: strings.TrimSpace(`
Use conn to list configured targets, inspect one target, or verify that a
connection can be reached before inspect or query.
`),
		UsageLines: []string{
			"dbx conn [command]",
		},
		Example: strings.TrimSpace(`
dbx conn list
dbx conn test local-pg
dbx conn show local-mysql
`),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	addConnFlags(cmd, flags)
	cmd.AddCommand(
		newConnLeafCommand(flags, "list", "List configured connections", cobra.NoArgs, false),
		newConnLeafCommand(flags, "test", "Test a named connection", cobra.ExactArgs(1), false),
		newConnLeafCommand(flags, "show", "Show connection details", cobra.ExactArgs(1), false),
		newConnLeafCommand(flags, "resolve", "Resolve a named connection", cobra.ExactArgs(1), true),
	)

	return cmd
}

func newConnLeafCommand(flags *connFlags, name, short string, argsValidator cobra.PositionalArgs, hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:           useWithPlaceholder(name),
		Short:         short,
		Args:          argsValidator,
		Hidden:        hidden,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"conn", name}
			argv = appendConnFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}

	cmd.UsageLines = []string{fmt.Sprintf("dbx conn %s", useWithPlaceholder(name))}
	if name == "test" || name == "show" || name == "resolve" {
		cmd.ArgFields = []cobra.HelpField{
			helpField("name", "string", true, "Connection name defined in config."),
		}
	}
	return cmd
}

func newInspectCommand() *cobra.Command {
	flags := &connFlags{}

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect schema, table, or connection metadata",
		Long: strings.TrimSpace(`
Use inspect to understand schema shape, table columns, keys, and relation
metadata before writing SQL.
`),
		UsageLines: []string{
			"dbx inspect [command]",
		},
		Example: strings.TrimSpace(`
dbx inspect schema local-pg
dbx inspect table local-pg users
dbx inspect connection local-pg
`),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	addConnFlags(cmd, flags)
	cmd.AddCommand(
		newInspectSchemaCommand(flags),
		newInspectTableCommand(flags),
		newInspectConnectionCommand(flags),
	)

	return cmd
}

func newInspectSchemaCommand(flags *connFlags) *cobra.Command {
	return &cobra.Command{
		Use:        "schema <conn> [schema]",
		Short:      "List schemas, tables, and relations",
		Long:       "List schemas, tables, and relations for one connection.",
		UsageLines: []string{"dbx inspect schema <conn> [schema]"},
		ArgFields: []cobra.HelpField{
			helpField("conn", "string", true, "Connection name defined in config."),
			helpField("schema", "string", false, "Optional schema name to narrow the inspection scope."),
		},
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"inspect", "schema"}
			argv = appendConnFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}
}

func newInspectTableCommand(flags *connFlags) *cobra.Command {
	return &cobra.Command{
		Use:        "table <conn> <table> [schema]",
		Short:      "Describe one table",
		Long:       "Describe one table, including columns, keys, and relations.",
		UsageLines: []string{"dbx inspect table <conn> <table> [schema]"},
		ArgFields: []cobra.HelpField{
			helpField("conn", "string", true, "Connection name defined in config."),
			helpField("table", "string", true, "Table name to inspect."),
			helpField("schema", "string", false, "Optional schema name when the table is not in the default schema."),
		},
		Args:          cobra.RangeArgs(2, 3),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"inspect", "table"}
			argv = appendConnFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}
}

func newInspectConnectionCommand(flags *connFlags) *cobra.Command {
	return &cobra.Command{
		Use:        "connection <conn>",
		Short:      "Show connection metadata",
		Long:       "Show resolved connection metadata and policy details.",
		UsageLines: []string{"dbx inspect connection <conn>"},
		ArgFields: []cobra.HelpField{
			helpField("conn", "string", true, "Connection name defined in config."),
		},
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"inspect", "connection"}
			argv = appendConnFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}
}

func newSQLCommand(name, short, longHelp, example string) *cobra.Command {
	flags := &commonFlags{}

	cmd := &cobra.Command{
		Use:     name + " <conn> <sql>",
		Short:   short,
		Long:    longHelp,
		Example: example,
		UsageLines: []string{
			fmt.Sprintf("dbx %s <conn> <sql>", name),
			fmt.Sprintf("dbx %s file <conn> <path.sql>", name),
			fmt.Sprintf("dbx %s dsn <engine> <dsn> <sql>", name),
		},
		ArgFields: []cobra.HelpField{
			helpField("conn", "string", false, "Connection name used by the direct or file form."),
			helpField("sql", "string", false, "SQL statement used by the direct or dsn form."),
			helpField("path.sql", "string", false, "SQL file path used by the file form."),
			helpField("engine", "string", false, "Database engine name used by the dsn form."),
			helpField("dsn", "string", false, "Connection string used by the dsn form."),
		},
		Args:          validateSQLArgs(name),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{name}
			argv = appendCommonFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}

	addCommonFlags(cmd, flags)
	return cmd
}

func newTxCommand() *cobra.Command {
	flags := &commonFlags{}
	var planPath string

	cmd := &cobra.Command{
		Use:   "tx <conn>",
		Short: "Run a structured multi-step transaction plan",
		Long: strings.TrimSpace(`
Use tx when multiple reads and writes must commit together.

Only query and update steps are allowed in a transaction plan. Any failure rolls
the entire transaction back.
`),
		UsageLines: []string{
			"dbx tx <conn> --plan <path.json>",
		},
		Example: strings.TrimSpace(`
dbx tx local-pg --plan ./plan.json
dbx query local-pg 'select id, active from users where id = 1'
`),
		ArgFields: []cobra.HelpField{
			helpField("conn", "string", true, "Connection name defined in config."),
		},
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"tx"}
			argv = appendCommonFlagArgs(argv, *flags)
			argv = append(argv, args...)
			argv = append(argv, "--plan", planPath)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}

	addCommonFlags(cmd, flags)
	cmd.Flags().StringVar(&planPath, "plan", "", "Transaction plan path")
	_ = cmd.MarkFlagRequired("plan")
	return cmd
}

func newImportCommand() *cobra.Command {
	flags := &commonFlags{}

	cmd := &cobra.Command{
		Use:   "import file <path> <conn> <table>",
		Short: "Load CSV or JSON rows into a table",
		Long:  "Use import to load CSV or JSON files into one table.",
		UsageLines: []string{
			"dbx import file <path> <conn> <table>",
		},
		Example: strings.TrimSpace(`
dbx import file ./customers.csv local-mysql customers
dbx query local-mysql 'select * from customers order by id'
`),
		ArgFields: []cobra.HelpField{
			helpField("path", "string", true, "Input CSV or JSON file path."),
			helpField("conn", "string", true, "Connection name defined in config."),
			helpField("table", "string", true, "Target table name."),
		},
		Args:          validateImportArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"import"}
			argv = appendCommonFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}

	addCommonFlags(cmd, flags)
	return cmd
}

func newExportCommand() *cobra.Command {
	flags := &exportFlags{}

	cmd := &cobra.Command{
		Use:   "export table <table> <conn> <out>",
		Short: "Write a table to CSV or JSON",
		Long:  "Use export to dump one table into a CSV or JSON file.",
		UsageLines: []string{
			"dbx export table <table> <conn> <out>",
		},
		Example: strings.TrimSpace(`
dbx export table users local-sqlite ./users.csv --format csv
dbx export table users local-pg ./users.json --format json
`),
		ArgFields: []cobra.HelpField{
			helpField("table", "string", true, "Source table name."),
			helpField("conn", "string", true, "Connection name defined in config."),
			helpField("out", "string", true, "Output file path."),
		},
		Args:          validateExportArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"export"}
			argv = appendExportFlagArgs(argv, *flags)
			argv = append(argv, args...)
			return runLegacyCommand(cmd.Context(), cmd.OutOrStdout(), argv)
		},
	}

	addExportFlags(cmd, flags)
	return cmd
}

func addCommonFlags(cmd *cobra.Command, flags *commonFlags) {
	cmd.Flags().StringVar(&flags.configPath, "config", "", "Config file or directory; disables default lookup")
	cmd.Flags().StringVar(&flags.format, "format", "json", "Output format: json|table")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "Validate without executing")
	cmd.Flags().IntVar(&flags.pageSize, "page-size", 100, "Maximum rows to materialize for read results")
	cmd.Flags().IntVar(&flags.maxRows, "max-rows-affected", 1000, "Maximum rows affected by write operations")
	cmd.Flags().StringVar(&flags.cursor, "cursor", "", "Continuation cursor for paged reads")
	cmd.Flags().BoolVar(&flags.verbose, "verbose", false, "Include extra metadata in output")
}

func addConnFlags(cmd *cobra.Command, flags *connFlags) {
	cmd.PersistentFlags().StringVar(&flags.configPath, "config", "", "Config file or directory; disables default lookup")
	cmd.PersistentFlags().StringVar(&flags.format, "format", "json", "Output format: json|table")
	cmd.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "Include extra metadata in output")
}

func addExportFlags(cmd *cobra.Command, flags *exportFlags) {
	cmd.Flags().StringVar(&flags.configPath, "config", "", "Config file or directory; disables default lookup")
	cmd.Flags().StringVar(&flags.format, "format", "csv", "Export file format: csv|json")
	cmd.Flags().BoolVar(&flags.verbose, "verbose", false, "Include extra metadata in output")
	cmd.Flags().IntVar(&flags.limit, "limit", 0, "Optional row limit for export")
}

func appendCommonFlagArgs(argv []string, flags commonFlags) []string {
	if flags.configPath != "" {
		argv = append(argv, "--config", flags.configPath)
	}
	if flags.format != "" && flags.format != "json" {
		argv = append(argv, "--format", flags.format)
	}
	if flags.dryRun {
		argv = append(argv, "--dry-run")
	}
	if flags.pageSize != 100 {
		argv = append(argv, "--page-size", fmt.Sprintf("%d", flags.pageSize))
	}
	if flags.maxRows != 1000 {
		argv = append(argv, "--max-rows-affected", fmt.Sprintf("%d", flags.maxRows))
	}
	if flags.cursor != "" {
		argv = append(argv, "--cursor", flags.cursor)
	}
	if flags.verbose {
		argv = append(argv, "--verbose")
	}
	return argv
}

func appendConnFlagArgs(argv []string, flags connFlags) []string {
	if flags.configPath != "" {
		argv = append(argv, "--config", flags.configPath)
	}
	if flags.format != "" && flags.format != "json" {
		argv = append(argv, "--format", flags.format)
	}
	if flags.verbose {
		argv = append(argv, "--verbose")
	}
	return argv
}

func appendExportFlagArgs(argv []string, flags exportFlags) []string {
	if flags.configPath != "" {
		argv = append(argv, "--config", flags.configPath)
	}
	if flags.format != "" && flags.format != "csv" {
		argv = append(argv, "--format", flags.format)
	}
	if flags.limit != 0 {
		argv = append(argv, "--limit", fmt.Sprintf("%d", flags.limit))
	}
	if flags.verbose {
		argv = append(argv, "--verbose")
	}
	return argv
}

func validateSQLArgs(command string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == 2 && args[0] != "file" && args[0] != "dsn":
			return nil
		case len(args) == 3 && args[0] == "file":
			return nil
		case len(args) == 4 && args[0] == "dsn":
			return nil
		default:
			return usagef("%s requires: <conn> <sql>, %s file <conn> <path>, or %s dsn <engine> <dsn> <sql>", command, command, command)
		}
	}
}

func validateImportArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 4 && args[0] == "file" {
		return nil
	}
	return usagef("import file requires: <path> <conn> <table>")
}

func validateExportArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 4 && args[0] == "table" {
		return nil
	}
	return usagef("export table requires: <table> <conn> <out>")
}

func useWithPlaceholder(name string) string {
	switch name {
	case "test", "show", "resolve":
		return name + " <name>"
	default:
		return name
	}
}
