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
	"strings"

	"github.com/linlay/dbx/internal/audit"
	"github.com/linlay/dbx/internal/config"
	"github.com/linlay/dbx/internal/conn"
	"github.com/linlay/dbx/internal/db"
	"github.com/linlay/dbx/internal/mode"
	"github.com/linlay/dbx/internal/output"
	"github.com/linlay/dbx/internal/sqlclass"
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
	configPath    string
	connName      string
	dsn           string
	engine        string
	mode          string
	format        string
	dryRun        bool
	requireAck    bool
	tx            bool
	pageSize      int
	maxRows       int
	verbose       bool
	verboseErrors bool
}

type connFlags struct {
	configPath    string
	format        string
	verbose       bool
	verboseErrors bool
}

func (a *App) bindCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.configPath, "config", "", "config path")
	fs.StringVar(&c.connName, "conn", "", "connection profile name")
	fs.StringVar(&c.dsn, "dsn", "", "adhoc DSN")
	fs.StringVar(&c.engine, "engine", "", "database engine")
	fs.StringVar(&c.mode, "mode", "", "execution mode")
	fs.StringVar(&c.format, "format", "agent", "output format: agent|table|json|jsonl|llm")
	fs.BoolVar(&c.dryRun, "dry-run", false, "validate without executing")
	fs.BoolVar(&c.requireAck, "require-ack", false, "explicitly confirm risky writes")
	fs.BoolVar(&c.tx, "tx", false, "transaction hint for future compatibility")
	fs.IntVar(&c.pageSize, "page-size", 20, "maximum rows to materialize for read results")
	fs.IntVar(&c.maxRows, "max-rows-affected", 1000, "maximum rows affected by write operations")
	fs.BoolVar(&c.verbose, "verbose", false, "include extra metadata in output")
	fs.BoolVar(&c.verboseErrors, "verbose-errors", false, "include raw driver errors in business error output")
	return c
}

func bindConnFlags(fs *flag.FlagSet) *connFlags {
	c := &connFlags{}
	fs.StringVar(&c.configPath, "config", "", "config path")
	fs.StringVar(&c.format, "format", "agent", "output format: agent|json|jsonl|table")
	fs.BoolVar(&c.verbose, "verbose", false, "include extra metadata in output")
	fs.BoolVar(&c.verboseErrors, "verbose-errors", false, "include raw driver errors in business error output")
	return c
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func (a *App) resolveSpec(ctx context.Context, common *commonFlags) (*config.Config, conn.Spec, error) {
	cfg, _, err := config.Load(common.configPath)
	if err != nil {
		return nil, conn.Spec{}, err
	}
	spec, err := conn.Resolve(ctx, conn.ResolveInput{
		ConfigPath: common.configPath,
		Config:     cfg,
		Name:       common.connName,
		DSN:        common.dsn,
		Engine:     common.engine,
		Mode:       common.mode,
	})
	if err != nil {
		return nil, conn.Spec{}, err
	}
	return cfg, spec, nil
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
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("conn")
		}
		return err
	}
	cfg, path, err := config.Load(flags.configPath)
	if err != nil {
		return a.renderError(flags.format, flags.verbose, flags.verboseErrors, conn.Spec{}, "", err)
	}
	switch args[0] {
	case "list":
		names := make([]map[string]any, 0, len(cfg.Connections))
		for name, item := range cfg.Connections {
			names = append(names, map[string]any{
				"name":   name,
				"engine": item.Engine,
				"mode":   item.Mode,
				"tags":   item.Tags,
			})
		}
		return output.PrintEnvelope(flags.format, output.Envelope{
			OK:         true,
			Kind:       "conn_list",
			Connection: path,
			Summary:    fmt.Sprintf("%d connection targets available; test one before querying", len(names)),
			Data:       map[string]any{"connections": names},
			AuditID:    audit.ID("conn-list"),
			Verbose:    flags.verbose,
		})
	case "show", "resolve", "test":
		rest := fs.Args()
		if len(rest) == 0 {
			return a.renderError(flags.format, flags.verbose, flags.verboseErrors, conn.Spec{}, "", fmt.Errorf("%s requires a connection name", args[0]))
		}
		spec, err := conn.Resolve(ctx, conn.ResolveInput{Config: cfg, Name: rest[0]})
		if err != nil {
			return a.renderError(flags.format, flags.verbose, flags.verboseErrors, conn.Spec{Name: rest[0]}, "", err)
		}
		if args[0] == "test" {
			err = db.Ping(ctx, spec)
			if err != nil {
				return a.renderError(flags.format, flags.verbose, flags.verboseErrors, spec, "", err)
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
	sqlText := fs.String("sql", "", "SQL statement")
	filePath := fs.String("file", "", "SQL file path")
	sampleSize := fs.Int("sample", 5, "sample size for agent output")
	truncateTokens := fs.Int("truncate-tokens", 256, "soft token budget for summaries")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("exec")
		}
		return err
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, conn.Spec{Name: common.connName, Engine: common.engine}, "", err)
	}
	text, err := readSQL(*sqlText, *filePath)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", err)
	}
	class := sqlclass.Classify(text)
	if err := enforcePolicy(spec, class, text, common); err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, class, err)
	}
	auditID := audit.ID("query:" + spec.Name)
	if common.dryRun {
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:             true,
			Kind:           "query_plan",
			Mode:           string(spec.Mode),
			Engine:         spec.Engine,
			Connection:     spec.Name,
			StatementClass: class,
			RiskLevel:      spec.Mode.RiskLevel(class),
			Summary:        "dry run passed policy checks; execute the same command without --dry-run when ready",
			Next:           nextForClass(class, false),
			AuditID:        auditID,
			Fingerprint:    audit.Fingerprint(text),
			Meta:           output.ConnectionMeta(spec),
			Verbose:        common.verbose,
		})
	}
	if class == mode.ClassRead {
		result, err := db.Query(ctx, spec, text, common.pageSize)
		if err != nil {
			return a.renderError(common.format, common.verbose, common.verboseErrors, spec, class, err)
		}
		data := any(result.Rows)
		summary := output.SummarizeResult(result, *truncateTokens)
		more := result.Truncated
		next := nextForClass(class, more)
		if strings.EqualFold(common.format, "llm") {
			data = output.LLMData(result, *sampleSize)
		} else if strings.EqualFold(common.format, "agent") {
			data = output.AgentQueryData(result, *sampleSize)
			summary = output.AgentQuerySummary(result, *sampleSize)
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:             true,
			Kind:           "query_result",
			Mode:           string(spec.Mode),
			Engine:         spec.Engine,
			Connection:     spec.Name,
			StatementClass: class,
			RiskLevel:      spec.Mode.RiskLevel(class),
			RowCount:       result.RowCount,
			Truncated:      result.Truncated,
			More:           more,
			Summary:        summary,
			Data:           data,
			Next:           next,
			AuditID:        auditID,
			Fingerprint:    audit.Fingerprint(text),
			Meta:           output.ConnectionMeta(spec),
			Verbose:        common.verbose,
		})
	}
	affected, err := db.ExecuteGuarded(ctx, spec, text, common.maxRows)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, class, err)
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
	schema := fs.String("schema", "", "schema name")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("inspect")
		}
		return err
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, conn.Spec{Name: common.connName, Engine: common.engine}, "", err)
	}
	switch args[0] {
	case "schema":
		schemas, err := db.ListSchemas(ctx, spec)
		if err != nil {
			return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", err)
		}
		tables, err := db.ListTables(ctx, spec, *schema)
		if err != nil && *schema != "" {
			return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", err)
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "inspect_schema",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("%d schema(s) and %d table(s) found; inspect a table before querying", len(schemas), len(tables)),
			Data: map[string]any{
				"schemas": schemas,
				"tables":  tables,
			},
			Next:    "inspect_table",
			AuditID: audit.ID("inspect-schema:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	case "table":
		rest := fs.Args()
		if len(rest) == 0 {
			return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", errors.New("inspect table requires a table name"))
		}
		info, err := db.DescribeTable(ctx, spec, *schema, rest[0])
		if err != nil {
			return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", err)
		}
		data := any(info)
		if strings.EqualFold(common.format, "agent") {
			data = map[string]any{
				"schema": info.Schema,
				"name":   info.Name,
				"cols":   output.CompactColumns(info.Columns),
			}
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "inspect_table",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("table %s.%s has %d columns; run exec next if needed", info.Schema, info.Name, len(info.Columns)),
			Data:       data,
			Next:       "query",
			AuditID:    audit.ID("inspect-table:" + spec.Name + ":" + rest[0]),
			Meta:       output.ConnectionMeta(spec),
			Verbose:    common.verbose,
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
	if len(args) < 2 || args[0] != "table" {
		return errors.New("export currently supports: export table <name>")
	}
	fs := newFlagSet("export")
	common := a.bindCommon(fs)
	outPath := fs.String("out", "", "output file path")
	limit := fs.Int("limit", 0, "optional row limit for export")
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("export")
		}
		return err
	}
	if *outPath == "" {
		return a.renderError(common.format, common.verbose, common.verboseErrors, conn.Spec{Name: common.connName, Engine: common.engine}, "", errors.New("export requires --out to avoid mixing export content with audit output"))
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, conn.Spec{Name: common.connName, Engine: common.engine}, "", err)
	}
	table := args[1]
	query := fmt.Sprintf("select * from %s", quoteExportTable(spec.Engine, table))
	result, err := db.Query(ctx, spec, query, *limit)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, mode.ClassRead, err)
	}
	file, err := os.Create(*outPath)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", err)
	}
	defer file.Close()
	if err := db.ExportRows(result.Rows, result.Columns, common.format, file); err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, "", err)
	}
	return output.PrintEnvelope("agent", output.Envelope{
		OK:         true,
		Kind:       "export_result",
		Mode:       string(spec.Mode),
		Engine:     spec.Engine,
		Connection: spec.Name,
		RowCount:   result.RowCount,
		Summary:    fmt.Sprintf("exported %d row(s) from %s; inspect the output file if needed", result.RowCount, table),
		Data: map[string]any{
			"out":    *outPath,
			"format": common.format,
		},
		Next:    "query",
		AuditID: audit.ID("export:" + spec.Name + ":" + table),
		Meta:    output.ConnectionMeta(spec),
		Verbose: common.verbose,
	})
}

func (a *App) runImport(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		return printHelp("import")
	}
	if len(args) < 2 || args[0] != "file" {
		return errors.New("import currently supports: import file <path> --into <table>")
	}
	fs := newFlagSet("import")
	common := a.bindCommon(fs)
	into := fs.String("into", "", "target table")
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp("import")
		}
		return err
	}
	if *into == "" {
		return a.renderError(common.format, common.verbose, common.verboseErrors, conn.Spec{Name: common.connName, Engine: common.engine}, "", errors.New("--into is required"))
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, conn.Spec{Name: common.connName, Engine: common.engine}, "", err)
	}
	if err := enforcePolicy(spec, mode.ClassWriteData, "insert into", common); err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, mode.ClassWriteData, err)
	}
	columns, rows, err := readImportFile(args[1])
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, mode.ClassWriteData, err)
	}
	if common.dryRun {
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Kind:       "import_plan",
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("validated %d row(s) for import into %s; run again without --dry-run when ready", len(rows), *into),
			Data: map[string]any{
				"columns": columns,
				"rows":    len(rows),
			},
			Next:    "ack_and_retry",
			AuditID: audit.ID("import-dry:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
			Verbose: common.verbose,
		})
	}
	count, err := db.ImportRows(ctx, spec, *into, columns, rows)
	if err != nil {
		return a.renderError(common.format, common.verbose, common.verboseErrors, spec, mode.ClassWriteData, err)
	}
	return output.PrintEnvelope(common.format, output.Envelope{
		OK:         true,
		Kind:       "import_result",
		Mode:       string(spec.Mode),
		Engine:     spec.Engine,
		Connection: spec.Name,
		RowCount:   int(count),
		Summary:    fmt.Sprintf("imported %d row(s) into %s; run exec to verify the load", count, *into),
		Data: map[string]any{
			"table": *into,
			"rows":  count,
		},
		Next:    "query",
		AuditID: audit.ID("import:" + spec.Name + ":" + *into),
		Meta:    output.ConnectionMeta(spec),
		Verbose: common.verbose,
	})
}

func enforcePolicy(spec conn.Spec, class mode.StatementClass, sqlText string, common *commonFlags) error {
	if !spec.Mode.Allows(class) {
		return fmt.Errorf("mode %s does not allow statement class %s", spec.Mode, class)
	}
	if sqlclass.HasUnsafeWrite(sqlText) {
		return fmt.Errorf("unsafe write blocked: missing WHERE clause")
	}
	if (spec.Mode.RequiresAck() || class == mode.ClassDDL || class == mode.ClassAdmin) && !common.requireAck && !common.dryRun {
		return fmt.Errorf("mode %s or statement class %s requires --require-ack", spec.Mode, class)
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
		return "", errors.New("one of --sql or --file is required")
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

func nextForClass(class mode.StatementClass, more bool) string {
	if more {
		return "fetch_more"
	}
	switch class {
	case mode.ClassRead:
		return "refine_query"
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
	case strings.Contains(msg, "does not allow statement class"):
		return "mode_blocked"
	case strings.Contains(msg, "requires --require-ack"):
		return "ack_required"
	case strings.Contains(msg, "missing where"):
		return "missing_where"
	case strings.Contains(msg, "one of --sql or --file is required"):
		return "missing_sql"
	default:
		return "sql_error"
	}
}

func hintForCode(code string) string {
	switch code {
	case "conn_not_found":
		return "Run dbx conn list or use a valid --conn name."
	case "no_connection":
		return "Provide --conn or use --dsn with --engine."
	case "mode_blocked":
		return "Use a mode that allows this statement class."
	case "ack_required":
		return "Add --require-ack and rerun the same command."
	case "missing_where":
		return "Add a WHERE clause or narrow the write before retrying."
	case "missing_sql":
		return "Provide --sql or --file."
	default:
		return "Check the SQL, target connection, or rerun with --verbose-errors."
	}
}

func nextForCode(code string) string {
	switch code {
	case "conn_not_found", "no_connection":
		return "inspect_connection"
	case "mode_blocked":
		return "refine_query"
	case "ack_required":
		return "ack_and_retry"
	case "missing_where":
		return "refine_query"
	default:
		return "refine_query"
	}
}

func summaryForError(code string) string {
	switch code {
	case "conn_not_found":
		return "connection target not found"
	case "no_connection":
		return "no connection target selected"
	case "mode_blocked":
		return "statement blocked by the current mode"
	case "ack_required":
		return "explicit confirmation is required for this operation"
	case "missing_where":
		return "unsafe write blocked because WHERE is missing"
	case "missing_sql":
		return "no SQL input was provided"
	default:
		return "database command failed"
	}
}

func (a *App) renderError(format string, verbose bool, verboseErrors bool, spec conn.Spec, class mode.StatementClass, err error) error {
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
	if verboseErrors {
		env.Warnings = []string{err.Error()}
	}
	if printErr := output.PrintEnvelope(format, env); printErr != nil {
		return printErr
	}
	return &ExitError{Code: 1}
}
