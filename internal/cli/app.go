package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/audit"
	"github.com/linlay/cli-dbx/internal/config"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/db"
	"github.com/linlay/cli-dbx/internal/output"
	"github.com/linlay/cli-dbx/internal/sqlanalyzer"
	"github.com/linlay/cli-dbx/internal/sqlclass"
)

type App struct{}

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit %d", e.Code)
}

func New() *App { return &App{} }

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return printHelp("")
	}
	switch args[0] {
	case "conn":
		return a.runConn(ctx, args[1:])
	case "query":
		return a.runSQLCommand(ctx, "query", action.Query, args[1:])
	case "update":
		return a.runSQLCommand(ctx, "update", action.Update, args[1:])
	case "schema":
		return a.runSQLCommand(ctx, "schema", action.Schema, args[1:])
	case "admin":
		return a.runSQLCommand(ctx, "admin", action.Admin, args[1:])
	case "inspect":
		return a.runInspect(ctx, args[1:])
	case "export":
		return a.runExport(ctx, args[1:])
	case "import":
		return a.runImport(ctx, args[1:])
	case "tx":
		return a.runTx(ctx, args[1:])
	case "version", "--version":
		return printVersion()
	case "help", "--help", "-h":
		topic := ""
		if len(args) > 1 {
			topic = args[1]
		}
		return printHelp(topic)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

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

func (a *App) bindCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.configPath, "config", "", "config path")
	fs.StringVar(&c.format, "format", "json", "output format: json|table")
	fs.BoolVar(&c.dryRun, "dry-run", false, "validate without executing")
	fs.IntVar(&c.pageSize, "page-size", 100, "maximum rows to materialize for read results")
	fs.IntVar(&c.maxRows, "max-rows-affected", 1000, "maximum rows affected by write operations")
	fs.StringVar(&c.cursor, "cursor", "", "continuation cursor for paged reads")
	fs.BoolVar(&c.verbose, "verbose", false, "include extra metadata in output")
	return c
}

func bindConnFlags(fs *flag.FlagSet) *connFlags {
	c := &connFlags{}
	fs.StringVar(&c.configPath, "config", "", "config path")
	fs.StringVar(&c.format, "format", "json", "output format: json|table")
	fs.BoolVar(&c.verbose, "verbose", false, "include extra metadata in output")
	return c
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func normalizeFlagArgs(args []string, valueFlags map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		if strings.Contains(arg, "=") {
			flags = append(flags, arg)
			continue
		}
		flags = append(flags, arg)
		if valueFlags[arg] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}

func commonValueFlags() map[string]bool {
	return map[string]bool{
		"--config":            true,
		"--format":            true,
		"--page-size":         true,
		"--max-rows-affected": true,
		"--cursor":            true,
		"--limit":             true,
	}
}

func (a *App) resolveSpec(ctx context.Context, configPath, name, dsn, engine string, act action.Action) (conn.Spec, error) {
	spec, err := conn.Resolve(ctx, conn.ResolveInput{
		ConfigPath: configPath,
		Name:       name,
		DSN:        dsn,
		Engine:     engine,
		Action:     act,
	})
	if err != nil {
		return conn.Spec{}, err
	}
	return spec, nil
}

type sqlInput struct {
	name   string
	dsn    string
	engine string
	sql    string
	file   string
}

type txRunInput struct {
	name     string
	planPath string
}

func parseSQLInput(command string, args []string) (sqlInput, error) {
	if len(args) == 0 {
		return sqlInput{}, fmt.Errorf("%s requires: <conn> <sql>, %s file <conn> <path>, or %s dsn <engine> <dsn> <sql>", command, command, command)
	}
	if args[0] == "dsn" {
		if len(args) < 4 {
			return sqlInput{}, fmt.Errorf("%s dsn requires: <engine> <dsn> <sql>", command)
		}
		return sqlInput{engine: args[1], dsn: args[2], sql: args[3]}, nil
	}
	if args[0] == "file" {
		if len(args) < 3 {
			return sqlInput{}, fmt.Errorf("%s file requires: <conn> <path>", command)
		}
		return sqlInput{name: args[1], file: args[2]}, nil
	}
	if len(args) < 2 {
		return sqlInput{}, fmt.Errorf("%s requires: <conn> <sql>, %s file <conn> <path>, or %s dsn <engine> <dsn> <sql>", command, command, command)
	}
	return sqlInput{name: args[0], sql: args[1]}, nil
}

func parseTxInput(args []string, planPath string) (txRunInput, error) {
	if strings.TrimSpace(planPath) == "" {
		return txRunInput{}, errors.New("tx requires --plan <path>")
	}
	if len(args) < 1 {
		return txRunInput{}, errors.New("tx requires: <conn> --plan <path>")
	}
	return txRunInput{name: args[0], planPath: planPath}, nil
}

func parseInspectInput(kind string, args []string) (string, string, string, error) {
	switch kind {
	case "schema":
		if len(args) == 0 {
			return "", "", "", errors.New("inspect schema requires: <conn> [schema]")
		}
		schema := ""
		if len(args) > 1 {
			schema = args[1]
		}
		return args[0], schema, "", nil
	case "table":
		if len(args) < 2 {
			return "", "", "", errors.New("inspect table requires: <conn> <table> [schema]")
		}
		schema := ""
		if len(args) > 2 {
			schema = args[2]
		}
		return args[0], schema, args[1], nil
	case "connection":
		if len(args) == 0 {
			return "", "", "", errors.New("inspect connection requires: <conn>")
		}
		return args[0], "", "", nil
	default:
		return "", "", "", fmt.Errorf("unknown inspect subcommand %q", kind)
	}
}

func parseImportInput(args []string) (path, connName, table string, err error) {
	if len(args) < 4 || args[0] != "file" {
		return "", "", "", errors.New("import file requires: <path> <conn> <table>")
	}
	return args[1], args[2], args[3], nil
}

func parseExportInput(args []string) (table, connName, out string, err error) {
	if len(args) < 4 || args[0] != "table" {
		return "", "", "", errors.New("export table requires: <table> <conn> <out>")
	}
	return args[1], args[2], args[3], nil
}

func (a *App) runConn(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return printHelp("conn")
	}
	if isHelpArg(args[0]) {
		return printHelp("conn")
	}
	fs := newFlagSet("conn")
	flags := bindConnFlags(fs)
	flagArgs := normalizeFlagArgs(args[1:], map[string]bool{
		"--config": true,
		"--format": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("conn")
		}
		return err
	}
	profiles, _, err := config.List(flags.configPath)
	if err != nil {
		return a.renderError(flags.format, flags.verbose, conn.Spec{}, "", "", err)
	}
	switch args[0] {
	case "list":
		names := make([]map[string]any, 0, len(profiles))
		for _, item := range profiles {
			names = append(names, map[string]any{
				"name":          item.Name,
				"engine":        item.Connection.Engine,
				"allow_actions": item.Connection.AllowActions,
				"tags":          item.Connection.Tags,
			})
		}
		return output.PrintEnvelope(flags.format, output.Envelope{
			OK:      true,
			Kind:    "conn_list",
			Summary: fmt.Sprintf("%d connection targets available; test one before query", len(names)),
			Data:    map[string]any{"connections": names},
			AuditID: audit.ID("conn-list"),
			Verbose: flags.verbose,
		})
	case "show", "resolve", "test":
		rest := fs.Args()
		name := ""
		if len(rest) > 0 {
			name = rest[0]
		}
		spec, err := conn.Resolve(ctx, conn.ResolveInput{ConfigPath: flags.configPath, Name: name})
		if err != nil {
			return a.renderError(flags.format, flags.verbose, conn.Spec{Name: name}, "", "", err)
		}
		if args[0] == "test" {
			err = db.Ping(ctx, spec)
			if err != nil {
				return a.renderError(flags.format, flags.verbose, spec, "", "", err)
			}
			return printEnvelopeWithWarnings(flags.format, spec, output.Envelope{
				OK:         true,
				Kind:       "conn_test",
				Connection: spec.Name,
				Engine:     spec.Engine,
				Summary:    "connection test passed; you can inspect tables or run query next",
				Data:       output.ConnectionMeta(spec),
				AuditID:    audit.ID("conn-test:" + spec.Name),
				Meta:       output.ConnectionMeta(spec),
				Verbose:    flags.verbose,
			})
		}
		return printEnvelopeWithWarnings(flags.format, spec, output.Envelope{
			OK:         true,
			Kind:       "conn_show",
			Connection: spec.Name,
			Engine:     spec.Engine,
			Summary:    "connection resolved; inspect connection or run query next",
			Data: map[string]any{
				"name":          spec.Name,
				"allow_actions": action.Strings(spec.AllowActions),
				"dsn":           redactDSN(spec),
				"meta":          output.ConnectionMeta(spec),
			},
			AuditID: audit.ID("conn-resolve:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: flags.verbose,
		})
	default:
		return fmt.Errorf("unknown conn subcommand %q", args[0])
	}
}

func (a *App) runSQLCommand(ctx context.Context, command string, expectedAction action.Action, args []string) error {
	fs := newFlagSet(command)
	fs.Usage = func() {}
	common := a.bindCommon(fs)
	if err := fs.Parse(normalizeFlagArgs(args, commonValueFlags())); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp(command)
		}
		return err
	}
	input, err := parseSQLInput(command, fs.Args())
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, input.name, input.dsn, input.engine, expectedAction)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: input.name, Engine: input.engine}, "", "", err)
	}
	text, err := readSQL(command, input.sql, input.file)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, "", "", err)
	}
	analysis := sqlanalyzer.Analyze(text)
	actualAction := action.FromClass(analysis.StatementClass)
	reportedAction := actualAction
	if expectedAction != "" {
		reportedAction = expectedAction
	}
	cursorOffset, err := parseCursor(common.cursor)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, reportedAction, analysis.StatementClass, err)
	}
	if err := enforcePolicy(spec, analysis, expectedAction); err != nil {
		return a.renderError(common.format, common.verbose, spec, reportedAction, analysis.StatementClass, err)
	}
	auditID := audit.ID(string(actualAction) + ":" + spec.Name)
	if common.dryRun {
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:             true,
			Kind:           "sql_plan",
			Engine:         spec.Engine,
			Connection:     spec.Name,
			Action:         actualAction,
			StatementClass: analysis.StatementClass,
			RiskLevel:      sqlclass.RiskLevel(analysis.StatementClass),
			Summary:        fmt.Sprintf("dry run passed policy checks; run %s without --dry-run when ready", command),
			Data: map[string]any{
				"objects":      analysis.Objects,
				"statements":   len(analysis.Statements),
				"needs_ack":    analysis.NeedsAck,
				"multi":        analysis.MultiStatement,
				"unsafe_write": analysis.HasUnsafeWrite,
			},
			Next:        nextForClass(analysis.StatementClass, false),
			AuditID:     auditID,
			Fingerprint: audit.Fingerprint(text),
			Meta:        output.ConnectionMeta(spec),
			Verbose:     common.verbose,
		})
	}
	if actualAction == action.Query {
		result, err := db.Query(ctx, spec, text, cursorOffset, common.pageSize)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, reportedAction, analysis.StatementClass, err)
		}
		more := result.Truncated
		next := nextForClass(analysis.StatementClass, more)
		nextCursorValue := nextCursor(cursorOffset, common.pageSize, result)
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:             true,
			Kind:           "query_result",
			Engine:         spec.Engine,
			Connection:     spec.Name,
			Action:         actualAction,
			StatementClass: analysis.StatementClass,
			RiskLevel:      sqlclass.RiskLevel(analysis.StatementClass),
			RowCount:       result.RowCount,
			Truncated:      result.Truncated,
			More:           more,
			Summary:        output.QuerySummary(result, nextCursorValue),
			Data:           output.QueryData(result, common.cursor, nextCursorValue),
			Next:           next,
			AuditID:        auditID,
			Fingerprint:    audit.Fingerprint(text),
			Meta:           output.ConnectionMeta(spec),
			Verbose:        common.verbose,
		})
	}
	affected, err := db.ExecuteGuarded(ctx, spec, text, common.maxRows)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, reportedAction, analysis.StatementClass, err)
	}
	return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
		OK:             true,
		Kind:           "write_result",
		Engine:         spec.Engine,
		Connection:     spec.Name,
		Action:         actualAction,
		StatementClass: analysis.StatementClass,
		RiskLevel:      sqlclass.RiskLevel(analysis.StatementClass),
		RowCount:       int(affected),
		Summary:        fmt.Sprintf("%d row(s) affected; inspect or run query to verify the change", affected),
		Data:           map[string]any{"rows_affected": affected},
		Next:           "inspect_table",
		AuditID:        auditID,
		Fingerprint:    audit.Fingerprint(text),
		Meta:           output.ConnectionMeta(spec),
		Verbose:        common.verbose,
	})
}

func (a *App) runInspect(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return printHelp("inspect")
	}
	if isHelpArg(args[0]) {
		return printHelp("inspect")
	}
	fs := newFlagSet("inspect")
	common := a.bindCommon(fs)
	if err := fs.Parse(normalizeFlagArgs(args[1:], commonValueFlags())); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("inspect")
		}
		return err
	}
	connName, schema, table, err := parseInspectInput(args[0], fs.Args())
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, connName, "", "", action.Query)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: connName}, "", "", err)
	}
	switch args[0] {
	case "schema":
		schemas, err := db.ListSchemas(ctx, spec)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, "", "", err)
		}
		tables, err := db.ListTables(ctx, spec, schema)
		if err != nil && schema != "" {
			return a.renderError(common.format, common.verbose, spec, "", "", err)
		}
		relations, err := db.ListRelations(ctx, spec, schema)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, "", "", err)
		}
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:         true,
			Kind:       "inspect_schema",
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("%d schema(s), %d table(s), %d relation(s) found; inspect a table before query", len(schemas), len(tables), len(relations)),
			Data: map[string]any{
				"schemas":   schemas,
				"tables":    tables,
				"relations": relations,
			},
			Next:    "inspect_table",
			AuditID: audit.ID("inspect-schema:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	case "table":
		info, err := db.DescribeTable(ctx, spec, schema, table)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, "", "", err)
		}
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:         true,
			Kind:       "inspect_table",
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("table %s.%s has %d columns; run query next if needed", info.Schema, info.Name, len(info.Columns)),
			Data: map[string]any{
				"schema":       info.Schema,
				"name":         info.Name,
				"cols":         info.Columns,
				"primary_key":  info.PrimaryKey,
				"unique_keys":  info.UniqueKeys,
				"foreign_keys": info.ForeignKeys,
			},
			Next:    "query",
			AuditID: audit.ID("inspect-table:" + spec.Name + ":" + table),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	case "connection":
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:         true,
			Kind:       "inspect_connection",
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    "connection metadata ready; inspect tables or run query next",
			Data:       output.ConnectionMeta(spec),
			Next:       "inspect_table",
			AuditID:    audit.ID("inspect-connection:" + spec.Name),
			Meta:       output.ConnectionMeta(spec),
			Verbose:    common.verbose,
		})
	default:
		return fmt.Errorf("unknown inspect subcommand %q", args[0])
	}
}

func (a *App) runExport(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		return printHelp("export")
	}
	fs := newFlagSet("export")
	configPath := fs.String("config", "", "config path")
	fileFormat := fs.String("format", "csv", "export file format: csv|json")
	verbose := fs.Bool("verbose", false, "include extra metadata in output")
	limit := fs.Int("limit", 0, "optional row limit for export")
	if err := fs.Parse(normalizeFlagArgs(args, map[string]bool{
		"--config": true,
		"--format": true,
		"--limit":  true,
	})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("export")
		}
		return err
	}
	table, connName, outPath, err := parseExportInput(fs.Args())
	if err != nil {
		return a.renderError("json", *verbose, conn.Spec{}, "", "", err)
	}
	spec, err := a.resolveSpec(ctx, *configPath, connName, "", "", action.Query)
	if err != nil {
		return a.renderError("json", *verbose, conn.Spec{Name: connName}, "", "", err)
	}
	if !spec.AllowsAction(action.Query) {
		return a.renderError("json", *verbose, spec, action.Query, sqlclass.ClassRead, fmt.Errorf("connection %s does not allow action %s", spec.Name, action.Query))
	}
	query := fmt.Sprintf("select * from %s", quoteExportTable(spec.Engine, table))
	result, err := db.Query(ctx, spec, query, 0, *limit)
	if err != nil {
		return a.renderError("json", *verbose, spec, action.Query, sqlclass.ClassRead, err)
	}
	file, err := os.Create(outPath)
	if err != nil {
		return a.renderError("json", *verbose, spec, action.Query, sqlclass.ClassRead, err)
	}
	defer file.Close()
	if err := db.ExportRows(result.Rows, result.Columns, *fileFormat, file); err != nil {
		return a.renderError("json", *verbose, spec, action.Query, sqlclass.ClassRead, err)
	}
	return printEnvelopeWithWarnings("json", spec, output.Envelope{
		OK:         true,
		Kind:       "export_result",
		Engine:     spec.Engine,
		Connection: spec.Name,
		Action:     action.Query,
		RowCount:   result.RowCount,
		Summary:    fmt.Sprintf("exported %d row(s) from %s; inspect the output file if needed", result.RowCount, table),
		Data: map[string]any{
			"out":    outPath,
			"format": *fileFormat,
		},
		Next:    "query",
		AuditID: audit.ID("export:" + spec.Name + ":" + table),
		Meta:    output.ConnectionMeta(spec),
		Verbose: *verbose,
	})
}

func (a *App) runImport(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		return printHelp("import")
	}
	fs := newFlagSet("import")
	common := a.bindCommon(fs)
	if err := fs.Parse(normalizeFlagArgs(args, commonValueFlags())); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("import")
		}
		return err
	}
	path, connName, into, err := parseImportInput(fs.Args())
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, connName, "", "", action.Update)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: connName}, "", "", err)
	}
	analysis := sqlanalyzer.Analyze("insert into " + into + " values (?)")
	if err := enforcePolicy(spec, analysis, action.Update); err != nil {
		return a.renderError(common.format, common.verbose, spec, action.Update, sqlclass.ClassWriteData, err)
	}
	columns, rows, err := readImportFile(path)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, action.Update, sqlclass.ClassWriteData, err)
	}
	if common.dryRun {
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:         true,
			Kind:       "import_plan",
			Engine:     spec.Engine,
			Connection: spec.Name,
			Action:     action.Update,
			Summary:    fmt.Sprintf("validated %d row(s) for import into %s; run again without --dry-run when ready", len(rows), into),
			Data: map[string]any{
				"columns": columns,
				"rows":    len(rows),
			},
			Next:    "update",
			AuditID: audit.ID("import-dry:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	}
	count, err := db.ImportRows(ctx, spec, into, columns, rows)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, action.Update, sqlclass.ClassWriteData, err)
	}
	return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
		OK:         true,
		Kind:       "import_result",
		Engine:     spec.Engine,
		Connection: spec.Name,
		Action:     action.Update,
		RowCount:   int(count),
		Summary:    fmt.Sprintf("imported %d row(s) into %s; run query to verify the load", count, into),
		Data: map[string]any{
			"table": into,
			"rows":  count,
		},
		Next:    "query",
		AuditID: audit.ID("import:" + spec.Name + ":" + into),
		Meta:    output.ConnectionMeta(spec),
		Verbose: common.verbose,
	})
}

func (a *App) runTx(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		return printHelp("tx")
	}
	fs := newFlagSet("tx")
	common := a.bindCommon(fs)
	planPath := fs.String("plan", "", "transaction plan path")
	if err := fs.Parse(normalizeFlagArgs(args, map[string]bool{
		"--config":            true,
		"--format":            true,
		"--page-size":         true,
		"--max-rows-affected": true,
		"--plan":              true,
	})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("tx")
		}
		return err
	}
	input, err := parseTxInput(fs.Args(), *planPath)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, input.name, "", "", action.Update)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: input.name}, "", "", err)
	}
	plan, err := readTxPlan(input.planPath)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, "", "", err)
	}
	for _, step := range plan.Steps {
		if step.Action != action.Query && step.Action != action.Update {
			return a.renderError(common.format, common.verbose, spec, step.Action, "", fmt.Errorf("transaction action %s is not supported", step.Action))
		}
		analysis := sqlanalyzer.Analyze(step.SQL)
		if err := enforcePolicy(spec, analysis, step.Action); err != nil {
			return a.renderError(common.format, common.verbose, spec, step.Action, analysis.StatementClass, err)
		}
	}
	if common.dryRun {
		return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
			OK:         true,
			Kind:       "tx_plan",
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("validated %d transaction step(s); run tx without --dry-run when ready", len(plan.Steps)),
			Data: map[string]any{
				"steps": plan.Steps,
			},
			Next:    "tx",
			AuditID: audit.ID("tx-plan:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	}
	result, err := db.RunTxPlan(ctx, spec, plan, common.pageSize, common.maxRows)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, "", "", err)
	}
	return printEnvelopeWithWarnings(common.format, spec, output.Envelope{
		OK:         true,
		Kind:       "tx_result",
		Engine:     spec.Engine,
		Connection: spec.Name,
		Summary:    fmt.Sprintf("transaction committed with %d step(s)", len(result.Steps)),
		Data:       txResultData(result),
		Next:       "query",
		AuditID:    audit.ID("tx-run:" + spec.Name),
		Meta:       output.ConnectionMeta(spec),
		Verbose:    common.verbose,
	})
}

func enforcePolicy(spec conn.Spec, analysis sqlanalyzer.Analysis, expectedAction action.Action) error {
	class := analysis.StatementClass
	if err := analysis.ValidateSingleStatement(); err != nil {
		return err
	}
	actualAction := action.FromClass(class)
	if expectedAction != "" && actualAction != expectedAction {
		return fmt.Errorf("action %s requires matching SQL; got %s", expectedAction, actualAction)
	}
	if actualAction == "" {
		return fmt.Errorf("statement type is unknown and blocked by default")
	}
	if !spec.AllowsAction(actualAction) {
		return fmt.Errorf("connection %s does not allow action %s", spec.Name, actualAction)
	}
	if analysis.HasUnsafeWrite {
		return fmt.Errorf("unsafe write blocked: missing WHERE clause")
	}
	return nil
}

func readSQL(command, inline, path string) (string, error) {
	switch {
	case inline != "":
		return inline, nil
	case path != "":
		buf, err := os.ReadFile(path)
		return string(buf), err
	default:
		return "", fmt.Errorf("%s requires SQL text or: %s file <conn> <path>", command, command)
	}
}

func readTxPlan(path string) (db.TxPlan, error) {
	file, err := os.Open(path)
	if err != nil {
		return db.TxPlan{}, err
	}
	defer file.Close()
	var plan db.TxPlan
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return db.TxPlan{}, err
	}
	if len(plan.Steps) == 0 {
		return db.TxPlan{}, errors.New("transaction plan requires at least one step")
	}
	for i := range plan.Steps {
		if strings.TrimSpace(string(plan.Steps[i].Action)) == "" {
			return db.TxPlan{}, fmt.Errorf("transaction step %d is missing action", i)
		}
		act, err := action.Parse(string(plan.Steps[i].Action))
		if err != nil {
			return db.TxPlan{}, fmt.Errorf("transaction step %d: %w", i, err)
		}
		plan.Steps[i].Action = act
		plan.Steps[i].SQL = strings.TrimSpace(plan.Steps[i].SQL)
		if plan.Steps[i].SQL == "" {
			return db.TxPlan{}, fmt.Errorf("transaction step %d is missing sql", i)
		}
	}
	return plan, nil
}

func txResultData(result db.TxRunResult) map[string]any {
	steps := make([]map[string]any, 0, len(result.Steps))
	for _, step := range result.Steps {
		item := map[string]any{
			"index":  step.Index,
			"action": step.Action,
		}
		if step.Query != nil {
			item["query"] = output.QueryData(*step.Query, "", "")
		}
		if step.RowsAffected > 0 {
			item["rows_affected"] = step.RowsAffected
		}
		steps = append(steps, item)
	}
	return map[string]any{"steps": steps}
}

func readImportFile(path string) ([]string, []map[string]any, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		buf, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		var rows []map[string]any
		if err := json.Unmarshal(buf, &rows); err != nil {
			return nil, nil, err
		}
		columns := orderedKeys(rows)
		return columns, rows, nil
	case ".csv":
		file, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		defer file.Close()
		reader := csv.NewReader(file)
		records, err := reader.ReadAll()
		if err != nil {
			return nil, nil, err
		}
		if len(records) == 0 {
			return nil, nil, nil
		}
		columns := records[0]
		rows := make([]map[string]any, 0, len(records)-1)
		for _, record := range records[1:] {
			row := make(map[string]any, len(columns))
			for i, col := range columns {
				if i < len(record) {
					row[col] = record[i]
				} else {
					row[col] = nil
				}
			}
			rows = append(rows, row)
		}
		return columns, rows, nil
	default:
		return nil, nil, fmt.Errorf("unsupported import file type %q", path)
	}
}

func orderedKeys(rows []map[string]any) []string {
	if len(rows) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rows[0]))
	for key := range rows[0] {
		keys = append(keys, key)
	}
	return keys
}

func quoteExportTable(engine, table string) string {
	switch engine {
	case "mysql":
		return "`" + strings.ReplaceAll(table, "`", "``") + "`"
	default:
		return `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
	}
}

func redactDSN(spec conn.Spec) string {
	if spec.Engine == "sqlite" {
		return spec.DSN
	}
	if idx := strings.Index(spec.DSN, "@"); idx >= 0 {
		// PostgreSQL URL format: scheme://user:password@host/...
		if start := strings.Index(spec.DSN, "://"); start >= 0 {
			return spec.DSN[:start+3] + "***:***" + spec.DSN[idx:]
		}
		// MySQL DSN format: user:password@protocol(host)/...
		prefix := spec.DSN[:idx]
		if colon := strings.LastIndex(prefix, ":"); colon >= 0 {
			return spec.DSN[:colon+1] + "***" + spec.DSN[idx:]
		}
	}
	return spec.DSN
}

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func parseCursor(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("cursor must be a non-negative integer")
	}
	return value, nil
}

func nextCursor(offset, pageSize int, result db.QueryResult) string {
	if !result.Truncated || pageSize <= 0 {
		return ""
	}
	return strconv.Itoa(offset + pageSize)
}

func nextForClass(class sqlclass.StatementClass, more bool) string {
	if more {
		return "fetch_more"
	}
	switch class {
	case sqlclass.ClassRead:
		return "refine_sql"
	case sqlclass.ClassWriteData, sqlclass.ClassDDL, sqlclass.ClassAdmin:
		return "inspect_table"
	default:
		return ""
	}
}

func classifyErrorCode(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return "conn_not_found"
	case strings.Contains(msg, "no connection selected"):
		return "no_connection"
	case strings.Contains(msg, "multiple statements are blocked"):
		return "multiple_statements_blocked"
	case strings.Contains(msg, "statement type is unknown"):
		return "unknown_statement"
	case strings.Contains(msg, "does not allow action"):
		return "action_blocked"
	case strings.Contains(msg, "requires matching sql"):
		return "action_mismatch"
	case strings.Contains(msg, "missing where"):
		return "missing_where"
	case strings.Contains(msg, "cursor must be a non-negative integer"):
		return "invalid_cursor"
	case strings.Contains(msg, "transaction action") && strings.Contains(msg, "not supported"):
		return "tx_unsupported_action"
	case strings.Contains(msg, "requires --plan"):
		return "missing_plan"
	case strings.Contains(msg, "requires:"):
		return "missing_sql"
	default:
		return "sql_error"
	}
}

func hintForCode(code string) string {
	switch code {
	case "conn_not_found":
		return "Run dbx conn list or use a valid connection name."
	case "no_connection":
		return "Provide a connection name like dbx query <conn> '<sql>' or use an explicit SQL command form."
	case "multiple_statements_blocked":
		return "Split the SQL into one statement per command."
	case "unknown_statement":
		return "Use one supported statement type per command."
	case "action_blocked":
		return "Update the connection allow_actions list or use a connection with the required action."
	case "action_mismatch":
		return "Use the SQL command that matches the statement type, such as query, update, or schema."
	case "missing_where":
		return "Add a WHERE clause or narrow the write before retrying."
	case "invalid_cursor":
		return "Use --cursor <non-negative integer> from a previous result."
	case "missing_plan":
		return "Use tx <conn> --plan <path>."
	case "tx_unsupported_action":
		return "Use only query and update steps in tx."
	case "missing_sql":
		return "Provide the required command arguments, such as <conn> <sql> or --plan <path>."
	default:
		return "Check the SQL text, target connection, or input file."
	}
}

func nextForCode(code string) string {
	switch code {
	case "conn_not_found", "no_connection":
		return "inspect_connection"
	case "multiple_statements_blocked", "unknown_statement":
		return "refine_sql"
	case "action_blocked", "action_mismatch":
		return "refine_sql"
	case "missing_where":
		return "refine_sql"
	case "invalid_cursor":
		return "fetch_more"
	case "missing_plan", "tx_unsupported_action":
		return "tx"
	default:
		return "refine_sql"
	}
}

func summaryForError(code string) string {
	switch code {
	case "conn_not_found":
		return "connection target not found"
	case "no_connection":
		return "no connection target selected"
	case "multiple_statements_blocked":
		return "multiple statements are blocked by default"
	case "unknown_statement":
		return "statement type is blocked by default"
	case "action_blocked":
		return "statement blocked by the connection allow_actions policy"
	case "action_mismatch":
		return "statement does not match the requested action"
	case "missing_where":
		return "unsafe write blocked because WHERE is missing"
	case "invalid_cursor":
		return "continuation cursor is invalid"
	case "missing_plan":
		return "transaction plan path is missing"
	case "tx_unsupported_action":
		return "transaction step uses an unsupported action"
	case "missing_sql":
		return "required command input is missing"
	default:
		return "database command failed"
	}
}

func (a *App) renderError(format string, verbose bool, spec conn.Spec, act action.Action, class sqlclass.StatementClass, err error) error {
	code := classifyErrorCode(err)
	env := output.Envelope{
		OK:             false,
		Kind:           "error",
		Code:           code,
		Hint:           hintForCode(code),
		Next:           nextForCode(code),
		Connection:     spec.Name,
		Engine:         spec.Engine,
		Action:         act,
		StatementClass: class,
		Summary:        summaryForError(code),
		Verbose:        verbose,
	}
	if err != nil {
		env.Warnings = []string{err.Error()}
	}
	env.Warnings = append(env.Warnings, spec.Warnings...)
	if printErr := output.PrintEnvelope(format, env); printErr != nil {
		return printErr
	}
	return &ExitError{Code: 1}
}

func printEnvelopeWithWarnings(format string, spec conn.Spec, env output.Envelope) error {
	if len(spec.Warnings) > 0 {
		env.Warnings = append(env.Warnings, spec.Warnings...)
	}
	return output.PrintEnvelope(format, env)
}
