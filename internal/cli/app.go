package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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

func New() *App { return &App{} }

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "conn":
		return a.runConn(ctx, args[1:])
	case "query":
		return a.runQuery(ctx, args[1:])
	case "inspect":
		return a.runInspect(ctx, args[1:])
	case "export":
		return a.runExport(ctx, args[1:])
	case "import":
		return a.runImport(ctx, args[1:])
	case "help", "--help", "-h":
		return usage()
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() error {
	fmt.Print(`dbx - agent-first database CLI

Commands:
  conn list|show|resolve|test
  query --sql "select 1"
  inspect schema|table|connection
  export table <name> --format csv
  import file <path> --into <table>
`)
	return nil
}

type commonFlags struct {
	configPath string
	connName   string
	dsn        string
	engine     string
	mode       string
	format     string
	dryRun     bool
	requireAck bool
	tx         bool
	pageSize   int
	maxRows    int
}

func (a *App) bindCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.configPath, "config", "", "config path")
	fs.StringVar(&c.connName, "conn", "", "connection profile name")
	fs.StringVar(&c.dsn, "dsn", "", "adhoc DSN")
	fs.StringVar(&c.engine, "engine", "", "database engine")
	fs.StringVar(&c.mode, "mode", "", "execution mode")
	fs.StringVar(&c.format, "format", "json", "output format: table|json|jsonl|llm")
	fs.BoolVar(&c.dryRun, "dry-run", false, "validate without executing")
	fs.BoolVar(&c.requireAck, "require-ack", false, "require explicit acknowledgement for risky actions")
	fs.BoolVar(&c.tx, "tx", false, "transaction hint for future compatibility")
	fs.IntVar(&c.pageSize, "page-size", 100, "maximum rows to materialize for read results")
	fs.IntVar(&c.maxRows, "max-rows-affected", 1000, "maximum rows affected by write operations")
	return c
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
		return errors.New("conn subcommand required")
	}
	fs := flag.NewFlagSet("conn", flag.ContinueOnError)
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, path, err := config.Load(*configPath)
	if err != nil {
		return err
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
		return output.PrintEnvelope("json", output.Envelope{
			OK:         true,
			Connection: path,
			Summary:    fmt.Sprintf("%d configured connection(s)", len(names)),
			Data:       names,
			AuditID:    audit.ID("conn-list"),
		})
	case "show", "resolve", "test":
		rest := fs.Args()
		if len(rest) == 0 {
			return fmt.Errorf("%s requires a connection name", args[0])
		}
		spec, err := conn.Resolve(ctx, conn.ResolveInput{Config: cfg, Name: rest[0]})
		if err != nil {
			return err
		}
		if args[0] == "test" {
			err = db.Ping(ctx, spec)
			return output.PrintEnvelope("json", output.Envelope{
				OK:         err == nil,
				Connection: spec.Name,
				Engine:     spec.Engine,
				Summary:    pingSummary(err),
				Data:       output.ConnectionMeta(spec),
				AuditID:    audit.ID("conn-test:" + spec.Name),
				Warnings:   errStrings(err),
			})
		}
		return output.PrintEnvelope("json", output.Envelope{
			OK:         true,
			Connection: spec.Name,
			Engine:     spec.Engine,
			Summary:    "resolved connection profile",
			Data: map[string]any{
				"name": spec.Name,
				"mode": spec.Mode,
				"dsn":  redactDSN(spec),
				"meta": output.ConnectionMeta(spec),
			},
			AuditID: audit.ID("conn-resolve:" + spec.Name),
		})
	default:
		return fmt.Errorf("unknown conn subcommand %q", args[0])
	}
}

func (a *App) runQuery(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	common := a.bindCommon(fs)
	sqlText := fs.String("sql", "", "SQL statement")
	filePath := fs.String("file", "", "SQL file path")
	sampleSize := fs.Int("sample", 10, "sample size for llm output")
	truncateTokens := fs.Int("truncate-tokens", 256, "soft token budget for summaries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return err
	}
	text, err := readSQL(*sqlText, *filePath)
	if err != nil {
		return err
	}
	class := sqlclass.Classify(text)
	if err := enforcePolicy(spec, class, text, common); err != nil {
		return err
	}
	auditID := audit.ID("query:" + spec.Name)
	if common.dryRun {
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:             true,
			Mode:           string(spec.Mode),
			Engine:         spec.Engine,
			Connection:     spec.Name,
			StatementClass: class,
			RiskLevel:      spec.Mode.RiskLevel(class),
			Summary:        "dry run passed policy checks",
			AuditID:        auditID,
			Fingerprint:    audit.Fingerprint(text),
			Meta:           output.ConnectionMeta(spec),
		})
	}
	if class == mode.ClassRead {
		result, err := db.Query(ctx, spec, text, common.pageSize)
		if err != nil {
			return output.PrintEnvelope(common.format, output.Envelope{
				OK:             false,
				Mode:           string(spec.Mode),
				Engine:         spec.Engine,
				Connection:     spec.Name,
				StatementClass: class,
				RiskLevel:      spec.Mode.RiskLevel(class),
				Summary:        "query failed",
				Warnings:       []string{err.Error()},
				AuditID:        auditID,
				Fingerprint:    audit.Fingerprint(text),
				Meta:           output.ConnectionMeta(spec),
			})
		}
		data := any(result.Rows)
		if strings.EqualFold(common.format, "llm") {
			data = output.LLMData(result, *sampleSize)
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:             true,
			Mode:           string(spec.Mode),
			Engine:         spec.Engine,
			Connection:     spec.Name,
			StatementClass: class,
			RiskLevel:      spec.Mode.RiskLevel(class),
			RowCount:       result.RowCount,
			Truncated:      result.Truncated,
			Summary:        output.SummarizeResult(result, *truncateTokens),
			Data:           data,
			AuditID:        auditID,
			Fingerprint:    audit.Fingerprint(text),
			Meta:           output.ConnectionMeta(spec),
		})
	}
	affected, err := db.ExecuteGuarded(ctx, spec, text, common.maxRows)
	if err != nil {
		return err
	}
	return output.PrintEnvelope(common.format, output.Envelope{
		OK:             true,
		Mode:           string(spec.Mode),
		Engine:         spec.Engine,
		Connection:     spec.Name,
		StatementClass: class,
		RiskLevel:      spec.Mode.RiskLevel(class),
		RowCount:       int(affected),
		Summary:        fmt.Sprintf("%d row(s) affected", affected),
		Data:           map[string]any{"rows_affected": affected},
		AuditID:        auditID,
		Fingerprint:    audit.Fingerprint(text),
		Meta:           output.ConnectionMeta(spec),
	})
}

func (a *App) runInspect(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("inspect subcommand required")
	}
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	common := a.bindCommon(fs)
	schema := fs.String("schema", "", "schema name")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return err
	}
	switch args[0] {
	case "schema":
		schemas, err := db.ListSchemas(ctx, spec)
		if err != nil {
			return err
		}
		tables, err := db.ListTables(ctx, spec, *schema)
		if err != nil && *schema != "" {
			return err
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("%d schema(s), %d table(s)", len(schemas), len(tables)),
			Data: map[string]any{
				"schemas": schemas,
				"tables":  tables,
			},
			AuditID: audit.ID("inspect-schema:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
		})
	case "table":
		rest := fs.Args()
		if len(rest) == 0 {
			return errors.New("inspect table requires a table name")
		}
		info, err := db.DescribeTable(ctx, spec, *schema, rest[0])
		if err != nil {
			return err
		}
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("table %s.%s has %d columns", info.Schema, info.Name, len(info.Columns)),
			Data:       info,
			AuditID:    audit.ID("inspect-table:" + spec.Name + ":" + rest[0]),
			Meta:       output.ConnectionMeta(spec),
		})
	case "connection":
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    "connection metadata",
			Data:       output.ConnectionMeta(spec),
			AuditID:    audit.ID("inspect-connection:" + spec.Name),
		})
	default:
		return fmt.Errorf("unknown inspect subcommand %q", args[0])
	}
}

func (a *App) runExport(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "table" {
		return errors.New("export currently supports: export table <name>")
	}
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	common := a.bindCommon(fs)
	outPath := fs.String("out", "", "output file path")
	limit := fs.Int("limit", 0, "optional row limit for export")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *outPath == "" {
		return errors.New("export requires --out to avoid mixing export content with audit output")
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return err
	}
	table := args[1]
	query := fmt.Sprintf("select * from %s", quoteExportTable(spec.Engine, table))
	result, err := db.Query(ctx, spec, query, *limit)
	if err != nil {
		return err
	}
	out := os.Stdout
	if *outPath != "" {
		file, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	if err := db.ExportRows(result.Rows, result.Columns, common.format, out); err != nil {
		return err
	}
	return output.PrintEnvelope("json", output.Envelope{
		OK:         true,
		Mode:       string(spec.Mode),
		Engine:     spec.Engine,
		Connection: spec.Name,
		RowCount:   result.RowCount,
		Summary:    fmt.Sprintf("exported %d row(s) from %s", result.RowCount, table),
		AuditID:    audit.ID("export:" + spec.Name + ":" + table),
		Meta:       output.ConnectionMeta(spec),
	})
}

func (a *App) runImport(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "file" {
		return errors.New("import currently supports: import file <path> --into <table>")
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	common := a.bindCommon(fs)
	into := fs.String("into", "", "target table")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *into == "" {
		return errors.New("--into is required")
	}
	_, spec, err := a.resolveSpec(ctx, common)
	if err != nil {
		return err
	}
	if err := enforcePolicy(spec, mode.ClassWriteData, "insert into", common); err != nil {
		return err
	}
	columns, rows, err := readImportFile(args[1])
	if err != nil {
		return err
	}
	if common.dryRun {
		return output.PrintEnvelope(common.format, output.Envelope{
			OK:         true,
			Mode:       string(spec.Mode),
			Engine:     spec.Engine,
			Connection: spec.Name,
			Summary:    fmt.Sprintf("dry run validated %d row(s) for import into %s", len(rows), *into),
			Data: map[string]any{
				"columns": columns,
				"rows":    len(rows),
			},
			AuditID: audit.ID("import-dry:" + spec.Name),
			Meta:    output.ConnectionMeta(spec),
		})
	}
	count, err := db.ImportRows(ctx, spec, *into, columns, rows)
	if err != nil {
		return err
	}
	return output.PrintEnvelope(common.format, output.Envelope{
		OK:         true,
		Mode:       string(spec.Mode),
		Engine:     spec.Engine,
		Connection: spec.Name,
		RowCount:   int(count),
		Summary:    fmt.Sprintf("imported %d row(s) into %s", count, *into),
		AuditID:    audit.ID("import:" + spec.Name + ":" + *into),
		Meta:       output.ConnectionMeta(spec),
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

func pingSummary(err error) string {
	if err == nil {
		return "connection test passed"
	}
	return "connection test failed"
}

func errStrings(err error) []string {
	if err == nil {
		return nil
	}
	return []string{err.Error()}
}
