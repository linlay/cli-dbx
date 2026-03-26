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

	"github.com/linlay/cli-dbx/internal/audit"
	"github.com/linlay/cli-dbx/internal/config"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/db"
	"github.com/linlay/cli-dbx/internal/mode"
	"github.com/linlay/cli-dbx/internal/output"
	"github.com/linlay/cli-dbx/internal/sqlanalyzer"
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
	case "exec":
		return a.runExec(ctx, args[1:])
	case "inspect":
		return a.runInspect(ctx, args[1:])
	case "export":
		return a.runExport(ctx, args[1:])
	case "import":
		return a.runImport(ctx, args[1:])
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
	mode       string
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
	fs.StringVar(&c.mode, "mode", "", "execution mode")
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
		"--mode":              true,
		"--format":            true,
		"--page-size":         true,
		"--max-rows-affected": true,
		"--cursor":            true,
		"--limit":             true,
	}
}

func (a *App) resolveSpec(ctx context.Context, configPath, name, dsn, engine, selectedMode string) (conn.Spec, error) {
	spec, err := conn.Resolve(ctx, conn.ResolveInput{
		ConfigPath: configPath,
		Name:       name,
		DSN:        dsn,
		Engine:     engine,
		Mode:       selectedMode,
	})
	if err != nil {
		return conn.Spec{}, err
	}
	return spec, nil
}

type execInput struct {
	name   string
	dsn    string
	engine string
	sql    string
	file   string
}

func parseExecInput(args []string) (execInput, error) {
	if len(args) == 0 {
		return execInput{}, errors.New("exec requires: <conn> <sql>, exec file <conn> <path>, or exec dsn <engine> <dsn> <sql>")
	}
	if args[0] == "dsn" {
		if len(args) < 4 {
			return execInput{}, errors.New("exec dsn requires: <engine> <dsn> <sql>")
		}
		return execInput{engine: args[1], dsn: args[2], sql: args[3]}, nil
	}
	if args[0] == "file" {
		if len(args) < 3 {
			return execInput{}, errors.New("exec file requires: <conn> <path>")
		}
		return execInput{name: args[1], file: args[2]}, nil
	}
	if len(args) < 2 {
		return execInput{}, errors.New("exec requires: <conn> <sql>, exec file <conn> <path>, or exec dsn <engine> <dsn> <sql>")
	}
	return execInput{name: args[0], sql: args[1]}, nil
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
	profiles, path, err := config.List(flags.configPath)
	if err != nil {
		return a.renderError(flags.format, flags.verbose, conn.Spec{}, "", err)
	}
	switch args[0] {
	case "list":
		names := make([]map[string]any, 0, len(profiles))
		for _, item := range profiles {
			names = append(names, map[string]any{
				"name":   item.Name,
				"engine": item.Connection.Engine,
				"mode":   item.Connection.Mode,
				"tags":   item.Connection.Tags,
			})
		}
		return output.PrintEnvelope(flags.format, output.Envelope{
			OK:         true,
			Kind:       "conn_list",
			Connection: path,
			Summary:    fmt.Sprintf("%d connection targets available; test one before exec", len(names)),
			Data:       map[string]any{"connections": names},
			AuditID:    audit.ID("conn-list"),
			Verbose:    flags.verbose,
		})
	case "show", "resolve", "test":
		rest := fs.Args()
		name := ""
		if len(rest) > 0 {
			name = rest[0]
		}
		spec, err := conn.Resolve(ctx, conn.ResolveInput{ConfigPath: flags.configPath, Name: name})
		if err != nil {
			return a.renderError(flags.format, flags.verbose, conn.Spec{Name: name}, "", err)
		}
		if args[0] == "test" {
			err = db.Ping(ctx, spec)
			if err != nil {
				return a.renderError(flags.format, flags.verbose, spec, "", err)
			}
			return output.PrintEnvelope(flags.format, output.Envelope{
				OK:         true,
				Kind:       "conn_test",
				Connection: spec.Name,
				Engine:     spec.Engine,
				Summary:    "connection test passed; you can inspect tables or run exec next",
				Data:       output.ConnectionMeta(spec),
				AuditID:    audit.ID("conn-test:" + spec.Name),
				Meta:       output.ConnectionMeta(spec),
				Verbose:    flags.verbose,
			})
		}
		return output.PrintEnvelope(flags.format, output.Envelope{
			OK:         true,
			Kind:       "conn_show",
			Connection: spec.Name,
			Engine:     spec.Engine,
			Summary:    "connection resolved; inspect connection or run exec next",
			Data: map[string]any{
				"name": spec.Name,
				"mode": spec.Mode,
				"dsn":  redactDSN(spec),
				"meta": output.ConnectionMeta(spec),
			},
			AuditID: audit.ID("conn-resolve:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: flags.verbose,
		})
	default:
		return fmt.Errorf("unknown conn subcommand %q", args[0])
	}
}

func (a *App) runExec(ctx context.Context, args []string) error {
	fs := newFlagSet("exec")
	fs.Usage = func() {}
	common := a.bindCommon(fs)
	if err := fs.Parse(normalizeFlagArgs(args, commonValueFlags())); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("exec")
		}
		return err
	}
	input, err := parseExecInput(fs.Args())
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, input.name, input.dsn, input.engine, common.mode)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: input.name, Engine: input.engine}, "", err)
	}
	text, err := readSQL(input.sql, input.file)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, "", err)
	}
	analysis := sqlanalyzer.Analyze(text)
	class := analysis.StatementClass
	cursorOffset, err := parseCursor(common.cursor)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, class, err)
	}
	if err := enforcePolicy(spec, analysis, common); err != nil {
		return a.renderError(common.format, common.verbose, spec, class, err)
	}
	auditID := audit.ID("query:" + spec.Name)
	if common.dryRun {
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:             true,
			Kind:           "exec_plan",
			Mode:           string(spec.Mode),
			Engine:         spec.Engine,
			Connection:     spec.Name,
			StatementClass: class,
			RiskLevel:      spec.Mode.RiskLevel(class),
			Summary:        "dry run passed policy checks; run exec without --dry-run when ready",
			Data: map[string]any{
				"objects":      analysis.Objects,
				"statements":   len(analysis.Statements),
				"needs_ack":    analysis.NeedsAck,
				"multi":        analysis.MultiStatement,
				"unsafe_write": analysis.HasUnsafeWrite,
			},
			Next:        nextForClass(class, false),
			AuditID:     auditID,
			Fingerprint: audit.Fingerprint(text),
			Meta:        output.ConnectionMeta(spec),
			Verbose:     common.verbose,
		})
	}
	if class == mode.ClassRead {
		result, err := db.Query(ctx, spec, text, cursorOffset, common.pageSize)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, class, err)
		}
		more := result.Truncated
		next := nextForClass(class, more)
		nextCursorValue := nextCursor(cursorOffset, common.pageSize, result)
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:             true,
			Kind:           "exec_result",
			Mode:           string(spec.Mode),
			Engine:         spec.Engine,
			Connection:     spec.Name,
			StatementClass: class,
			RiskLevel:      spec.Mode.RiskLevel(class),
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
		return a.renderError(common.format, common.verbose, spec, class, err)
	}
	return output.PrintEnvelope(common.format, output.Envelope{
		OK:             true,
		Kind:           "write_result",
		Mode:           string(spec.Mode),
		Engine:         spec.Engine,
		Connection:     spec.Name,
		StatementClass: class,
		RiskLevel:      spec.Mode.RiskLevel(class),
		RowCount:       int(affected),
		Summary:        fmt.Sprintf("%d row(s) affected; inspect or run exec to verify the change", affected),
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
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, connName, "", "", common.mode)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: connName}, "", err)
	}
	switch args[0] {
	case "schema":
		schemas, err := db.ListSchemas(ctx, spec)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, "", err)
		}
		tables, err := db.ListTables(ctx, spec, schema)
		if err != nil && schema != "" {
			return a.renderError(common.format, common.verbose, spec, "", err)
		}
		relations, err := db.ListRelations(ctx, spec, schema)
		if err != nil {
			return a.renderError(common.format, common.verbose, spec, "", err)
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "inspect_schema",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("%d schema(s), %d table(s), %d relation(s) found; inspect a table before exec", len(schemas), len(tables), len(relations)),
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
			return a.renderError(common.format, common.verbose, spec, "", err)
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "inspect_table",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("table %s.%s has %d columns; run exec next if needed", info.Schema, info.Name, len(info.Columns)),
			Data: map[string]any{
				"schema":       info.Schema,
				"name":         info.Name,
				"cols":         info.Columns,
				"primary_key":  info.PrimaryKey,
				"unique_keys":  info.UniqueKeys,
				"foreign_keys": info.ForeignKeys,
			},
			Next:    "exec",
			AuditID: audit.ID("inspect-table:" + spec.Name + ":" + table),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	case "connection":
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "inspect_connection",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    "connection metadata ready; inspect tables or run exec next",
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
	selectedMode := fs.String("mode", "", "execution mode")
	fileFormat := fs.String("format", "csv", "export file format: csv|json")
	verbose := fs.Bool("verbose", false, "include extra metadata in output")
	limit := fs.Int("limit", 0, "optional row limit for export")
	if err := fs.Parse(normalizeFlagArgs(args, map[string]bool{
		"--config": true,
		"--mode":   true,
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
		return a.renderError("json", *verbose, conn.Spec{}, "", err)
	}
	spec, err := a.resolveSpec(ctx, *configPath, connName, "", "", *selectedMode)
	if err != nil {
		return a.renderError("json", *verbose, conn.Spec{Name: connName}, "", err)
	}
	query := fmt.Sprintf("select * from %s", quoteExportTable(spec.Engine, table))
	result, err := db.Query(ctx, spec, query, 0, *limit)
	if err != nil {
		return a.renderError("json", *verbose, spec, mode.ClassRead, err)
	}
	file, err := os.Create(outPath)
	if err != nil {
		return a.renderError("json", *verbose, spec, "", err)
	}
	defer file.Close()
	if err := db.ExportRows(result.Rows, result.Columns, *fileFormat, file); err != nil {
		return a.renderError("json", *verbose, spec, "", err)
	}
	return output.PrintEnvelope("json", output.Envelope{
		OK:         true,
		Kind:       "export_result",
		Mode:       string(spec.Mode),
		Engine:     spec.Engine,
		Connection: spec.Name,
		RowCount:   result.RowCount,
		Summary:    fmt.Sprintf("exported %d row(s) from %s; inspect the output file if needed", result.RowCount, table),
		Data: map[string]any{
			"out":    outPath,
			"format": *fileFormat,
		},
		Next:    "exec",
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
		return a.renderError(common.format, common.verbose, conn.Spec{}, "", err)
	}
	spec, err := a.resolveSpec(ctx, common.configPath, connName, "", "", common.mode)
	if err != nil {
		return a.renderError(common.format, common.verbose, conn.Spec{Name: connName}, "", err)
	}
	if err := enforcePolicy(spec, sqlanalyzer.Analyze("insert into "+into+" values (?)"), common); err != nil {
		return a.renderError(common.format, common.verbose, spec, mode.ClassWriteData, err)
	}
	columns, rows, err := readImportFile(path)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, mode.ClassWriteData, err)
	}
	if common.dryRun {
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "import_plan",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("validated %d row(s) for import into %s; run again without --dry-run when ready", len(rows), into),
			Data: map[string]any{
				"columns": columns,
				"rows":    len(rows),
			},
			Next:    "exec",
			AuditID: audit.ID("import-dry:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	}
	count, err := db.ImportRows(ctx, spec, into, columns, rows)
	if err != nil {
		return a.renderError(common.format, common.verbose, spec, mode.ClassWriteData, err)
	}
	return output.PrintEnvelope(common.format, output.Envelope{
		OK:         true,
		Kind:       "import_result",
		Mode:       string(spec.Mode),
		Engine:     spec.Engine,
		Connection: spec.Name,
		RowCount:   int(count),
		Summary:    fmt.Sprintf("imported %d row(s) into %s; run exec to verify the load", count, into),
		Data: map[string]any{
			"table": into,
			"rows":  count,
		},
		Next:    "exec",
		AuditID: audit.ID("import:" + spec.Name + ":" + into),
		Meta:    output.ConnectionMeta(spec),
		Verbose: common.verbose,
	})
}

func enforcePolicy(spec conn.Spec, analysis sqlanalyzer.Analysis, common *commonFlags) error {
	class := analysis.StatementClass
	if err := analysis.ValidateSingleStatement(); err != nil {
		return err
	}
	if !spec.Mode.Allows(class) {
		return fmt.Errorf("mode %s does not allow statement class %s", spec.Mode, class)
	}
	if analysis.HasUnsafeWrite {
		return fmt.Errorf("unsafe write blocked: missing WHERE clause")
	}
	return nil
}

func readSQL(inline, path string) (string, error) {
	switch {
	case inline != "":
		return inline, nil
	case path != "":
		buf, err := os.ReadFile(path)
		return string(buf), err
	default:
		return "", errors.New("exec requires SQL text or: exec file <conn> <path>")
	}
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
		start := strings.Index(spec.DSN, "://")
		if start >= 0 {
			return spec.DSN[:start+3] + "***:***" + spec.DSN[idx:]
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

func nextForClass(class mode.StatementClass, more bool) string {
	if more {
		return "fetch_more"
	}
	switch class {
	case mode.ClassRead:
		return "refine_exec"
	case mode.ClassWriteData, mode.ClassDDL, mode.ClassAdmin:
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
	case strings.Contains(msg, "does not allow statement class"):
		return "mode_blocked"
	case strings.Contains(msg, "missing where"):
		return "missing_where"
	case strings.Contains(msg, "cursor must be a non-negative integer"):
		return "invalid_cursor"
	case strings.Contains(msg, "exec requires:"):
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
		return "Provide a connection name like dbx exec <conn> '<sql>' or use exec dsn <engine> <dsn> <sql>."
	case "multiple_statements_blocked":
		return "Split the SQL into one statement per exec call."
	case "unknown_statement":
		return "Use one supported statement type per exec call."
	case "mode_blocked":
		return "Use a mode that allows this statement class."
	case "missing_where":
		return "Add a WHERE clause or narrow the write before retrying."
	case "invalid_cursor":
		return "Use --cursor <non-negative integer> from a previous result."
	case "missing_sql":
		return "Use exec <conn> '<sql>' or exec file <conn> <path>."
	default:
		return "Check the SQL text, target connection, or input file."
	}
}

func nextForCode(code string) string {
	switch code {
	case "conn_not_found", "no_connection":
		return "inspect_connection"
	case "multiple_statements_blocked", "unknown_statement":
		return "refine_exec"
	case "mode_blocked":
		return "refine_exec"
	case "missing_where":
		return "refine_exec"
	case "invalid_cursor":
		return "fetch_more"
	default:
		return "refine_exec"
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
	case "mode_blocked":
		return "statement blocked by the current mode"
	case "missing_where":
		return "unsafe write blocked because WHERE is missing"
	case "invalid_cursor":
		return "continuation cursor is invalid"
	case "missing_sql":
		return "no SQL input was provided"
	default:
		return "database command failed"
	}
}

func (a *App) renderError(format string, verbose bool, spec conn.Spec, class mode.StatementClass, err error) error {
	code := classifyErrorCode(err)
	env := output.Envelope{
		OK:             false,
		Kind:           "error",
		Code:           code,
		Hint:           hintForCode(code),
		Next:           nextForCode(code),
		Connection:     spec.Name,
		Engine:         spec.Engine,
		StatementClass: class,
		Summary:        summaryForError(code),
		Verbose:        verbose,
	}
	if err != nil {
		env.Warnings = []string{err.Error()}
	}
	if printErr := output.PrintEnvelope(format, env); printErr != nil {
		return printErr
	}
	return &ExitError{Code: 1}
}
