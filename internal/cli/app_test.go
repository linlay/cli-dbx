package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/buildinfo"
	_ "modernc.org/sqlite"
)

func TestHelpOutputsUseCompactTaskCards(t *testing.T) {
	root := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"help"})
	})
	if !strings.Contains(root, "dbx query local-pg 'select * from users order by id' --page-size 100") {
		t.Fatalf("root help should point to query usage, got:\n%s", root)
	}
	if !strings.Contains(root, "query     run read-only SQL") || !strings.Contains(root, "tx        run a structured transaction plan") {
		t.Fatalf("root help should include action and tx commands, got:\n%s", root)
	}
	if !strings.Contains(root, "version   show build version") || !strings.Contains(root, "dbx version") {
		t.Fatalf("root help should include version command, got:\n%s", root)
	}
	if strings.Contains(root, "exec      compatibility command") {
		t.Fatalf("root help should not mention removed exec command, got:\n%s", root)
	}
	if strings.Contains(root, "self-describing") || strings.Contains(root, "external docs") {
		t.Fatalf("root help should omit design commentary, got:\n%s", root)
	}

	examples := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"help", "examples"})
	})
	if !strings.Contains(examples, "PostgreSQL: inspect users") || !strings.Contains(examples, "MySQL: import customers.csv") || !strings.Contains(examples, "--cursor 100") || !strings.Contains(examples, "dbx tx local-pg --plan ./plan.json") {
		t.Fatalf("help examples should include postgres and mysql examples, got:\n%s", examples)
	}

	queryHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--help"})
	})
	if !strings.Contains(queryHelp, "Run read-only SQL.") || !strings.Contains(queryHelp, "allow_actions") {
		t.Fatalf("query help should include action guidance, got:\n%s", queryHelp)
	}
	if !strings.Contains(queryHelp, "--page-size <n>               read page size; default 100") || !strings.Contains(queryHelp, "--format <json|table>         result format; default json") {
		t.Fatalf("query help should list retained params and defaults, got:\n%s", queryHelp)
	}
	if !strings.Contains(queryHelp, "Keep the same order by when you continue with --cursor.") || !strings.Contains(queryHelp, "dbx query local-pg 'select * from users order by id' --cursor 100") {
		t.Fatalf("query help should include pagination guidance, got:\n%s", queryHelp)
	}
	if strings.Contains(queryHelp, "--require-ack") || strings.Contains(queryHelp, "--verbose-errors") || strings.Contains(queryHelp, "sampled by default") {
		t.Fatalf("query help should not mention removed flags or sampling, got:\n%s", queryHelp)
	}
	if strings.Contains(queryHelp, "--mode") {
		t.Fatalf("query help should not mention removed mode flag, got:\n%s", queryHelp)
	}
	if strings.Contains(queryHelp, "Common next actions") || strings.Contains(queryHelp, "agent-first") {
		t.Fatalf("query help should omit design sections, got:\n%s", queryHelp)
	}

	txHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"tx", "--help"})
	})
	if !strings.Contains(txHelp, "<conn> --plan <path.json>") || !strings.Contains(txHelp, "Any failure rolls the whole transaction back.") {
		t.Fatalf("tx help should describe transaction plans, got:\n%s", txHelp)
	}
	if strings.Contains(txHelp, "run <conn> --plan <path.json>") {
		t.Fatalf("tx help should not mention removed run subcommand, got:\n%s", txHelp)
	}
	if !strings.Contains(txHelp, "Use tx when a sequence of reads and writes must commit together.") || !strings.Contains(txHelp, `"max_rows_affected":1`) {
		t.Fatalf("tx help should include multi-step transaction guidance, got:\n%s", txHelp)
	}

	connHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"conn", "--help"})
	})
	if !strings.Contains(connHelp, "test <name>") {
		t.Fatalf("conn help should describe test usage, got:\n%s", connHelp)
	}

	inspectHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "--help"})
	})
	if !strings.Contains(inspectHelp, "inspect table") {
		t.Fatalf("inspect help should describe table inspection, got:\n%s", inspectHelp)
	}

	versionHelp := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"help", "version"})
	})
	if !strings.Contains(versionHelp, "dbx --version") {
		t.Fatalf("version help should describe version flag, got:\n%s", versionHelp)
	}
}

func TestExecCommandAndHelpTopicAreRemoved(t *testing.T) {
	if err := New().Run(context.Background(), []string{"help", "exec"}); err == nil || !strings.Contains(err.Error(), `unknown help topic "exec"`) {
		t.Fatalf("help exec should be removed, got: %v", err)
	}
	if err := New().Run(context.Background(), []string{"exec", "--help"}); err == nil || !strings.Contains(err.Error(), `unknown command "exec"`) {
		t.Fatalf("exec command should be removed, got: %v", err)
	}
}

func TestTxRunSubcommandIsRemoved(t *testing.T) {
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"tx", "run", "local-pg", "--plan", "./plan.json"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal tx run removed output: %v\n%s", err, out)
	}
	if payload["code"] != "conn_not_found" || payload["conn"] != "run" {
		t.Fatalf("tx run form should be treated as invalid old syntax, got %#v", payload)
	}
}

func TestVersionOutputsEmbeddedBuildInfo(t *testing.T) {
	oldVersion := buildinfo.Version
	oldCommit := buildinfo.Commit
	oldBuildTime := buildinfo.BuildTime
	buildinfo.Version = "v0.1.0"
	buildinfo.Commit = "abc1234"
	buildinfo.BuildTime = "2026-03-25T12:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.Commit = oldCommit
		buildinfo.BuildTime = oldBuildTime
	})

	versionOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"version"})
	})
	if strings.TrimSpace(versionOut) != "dbx v0.1.0 (commit abc1234, built 2026-03-25T12:00:00Z)" {
		t.Fatalf("unexpected version output: %q", versionOut)
	}

	flagOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"--version"})
	})
	if strings.TrimSpace(flagOut) != "dbx v0.1.0 (commit abc1234, built 2026-03-25T12:00:00Z)" {
		t.Fatalf("unexpected --version output: %q", flagOut)
	}
}

func TestQueryDefaultJSONOutputUsesPageSizeAndCursor(t *testing.T) {
	configPath := makeSQLiteFixture(t, 125)
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--config", configPath, "local-sqlite", "select id, name from users order by id"})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if got := payload["kind"]; got != "query_result" {
		t.Fatalf("kind = %v, want query_result", got)
	}
	if got := payload["action"]; got != "query" {
		t.Fatalf("action = %v, want query", got)
	}
	if got := payload["more"]; got != true {
		t.Fatalf("more = %v, want true", got)
	}
	if _, ok := payload["engine"]; ok {
		t.Fatalf("default json output should omit engine: %v", payload)
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("data should be an object: %#v", payload["data"])
	}
	rows, ok := data["rows"].([]any)
	if !ok || len(rows) != 100 {
		t.Fatalf("rows should contain the full page, got %#v", data["rows"])
	}
	if got := int(data["seen"].(float64)); got != 125 {
		t.Fatalf("seen = %d, want 125", got)
	}
	if nextCursor := data["next_cursor"]; nextCursor != "100" {
		t.Fatalf("next_cursor = %v, want 100", nextCursor)
	}
}

func TestQueryRequiresExplicitConnectionName(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"query", "--config", configPath, "select id from users order by id"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["code"] != "missing_sql" {
		t.Fatalf("expected explicit-conn error, got %#v", payload)
	}
}

func TestInspectRequiresExplicitConnectionName(t *testing.T) {
	configPath := makeSQLiteRelationFixture(t)
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"inspect", "table", "--config", configPath, "orders"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["kind"] != "error" || !strings.Contains(payload["warnings"].([]any)[0].(string), "inspect table requires") {
		t.Fatalf("expected explicit-conn error, got %#v", payload)
	}
}

func TestQueryVerboseIncludesMetaAndErrorsUseEnvelope(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)

	verbose := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--config", configPath, "--verbose", "local-sqlite", "select id from users order by id"})
	})
	var verbosePayload map[string]any
	if err := json.Unmarshal([]byte(verbose), &verbosePayload); err != nil {
		t.Fatalf("unmarshal verbose output: %v", err)
	}
	if verbosePayload["engine"] != "sqlite" {
		t.Fatalf("verbose engine = %v, want sqlite", verbosePayload["engine"])
	}
	if _, ok := verbosePayload["meta"]; !ok {
		t.Fatalf("verbose output should include meta: %#v", verbosePayload)
	}
	meta := verbosePayload["meta"].(map[string]any)
	if _, ok := meta["allow_actions"]; !ok {
		t.Fatalf("verbose meta should include allow_actions: %#v", verbosePayload)
	}
	if _, ok := verbosePayload["mode"]; ok {
		t.Fatalf("verbose output should not include mode: %#v", verbosePayload)
	}

	errOut := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"update", "--config", configPath, "local-sqlite", "delete from users"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var errPayload map[string]any
	if err := json.Unmarshal([]byte(errOut), &errPayload); err != nil {
		t.Fatalf("unmarshal error output: %v\n%s", err, errOut)
	}
	if errPayload["kind"] != "error" || errPayload["code"] != "missing_where" {
		t.Fatalf("unexpected business error envelope: %#v", errPayload)
	}
	if _, ok := errPayload["warnings"]; !ok {
		t.Fatalf("verbose error output should include warnings: %#v", errPayload)
	}
}

func TestRemovedModeFlagIsRejected(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)
	for _, args := range [][]string{
		{"update", "--config", configPath, "--mode", "Tweezers", "local-sqlite", "update users set name = 'aaa' where id = 1"},
		{"update", "--config", configPath, "local-sqlite", "update users set name = 'aaa' where id = 1", "--mode", "Tweezers"},
	} {
		err := New().Run(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -mode") {
			t.Fatalf("expected removed --mode flag error, got %v", err)
		}
	}
}

func TestQueryBlocksMultiStatementButAllowsScopedWriteWithoutAck(t *testing.T) {
	configPath := makeSQLiteFixture(t, 3)

	multiOut := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"query", "--config", configPath, "local-sqlite", "select 1; select 2"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var multiPayload map[string]any
	if err := json.Unmarshal([]byte(multiOut), &multiPayload); err != nil {
		t.Fatalf("unmarshal multi output: %v", err)
	}
	if multiPayload["code"] != "multiple_statements_blocked" {
		t.Fatalf("expected multi statement block, got %#v", multiPayload)
	}

	writeOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"update", "--config", configPath, "local-sqlite", "update users set name = 'x' where id = 1"})
	})
	var writePayload map[string]any
	if err := json.Unmarshal([]byte(writeOut), &writePayload); err != nil {
		t.Fatalf("unmarshal write output: %v", err)
	}
	if writePayload["kind"] != "write_result" {
		t.Fatalf("expected successful scoped write, got %#v", writePayload)
	}
}

func TestSQLErrorIncludesDriverReasonByDefault(t *testing.T) {
	configPath := makeSQLiteFixture(t, 1)
	csvPath := filepath.Join(t.TempDir(), "dupe.csv")
	if err := os.WriteFile(csvPath, []byte("id,name\n1,Ada\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"import", "--config", configPath, "file", csvPath, "local-sqlite", "users"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if payload["code"] != "sql_error" {
		t.Fatalf("expected sql_error, got %#v", payload)
	}
	warnings, ok := payload["warnings"].([]any)
	if !ok || len(warnings) == 0 || !strings.Contains(warnings[0].(string), "UNIQUE constraint failed") {
		t.Fatalf("expected raw driver reason by default, got %#v", payload)
	}
}

func TestImportRequiresExplicitConnectionName(t *testing.T) {
	configPath := makeSQLiteFixture(t, 0)
	csvPath := filepath.Join(t.TempDir(), "users.csv")
	if err := os.WriteFile(csvPath, []byte("id,name\n1,Ada\n2,Linus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"import", "--config", configPath, "file", csvPath, "users"})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["kind"] != "error" || !strings.Contains(payload["warnings"].([]any)[0].(string), "import file requires") {
		t.Fatalf("expected explicit-conn import error, got %#v", payload)
	}
}

func TestExecCursorAndInspectRelations(t *testing.T) {
	configPath := makeSQLiteRelationFixture(t)

	pageOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--config", configPath, "--cursor", "20", "--page-size", "5", "local-sqlite", "select id, name from users order by id"})
	})
	var pagePayload map[string]any
	if err := json.Unmarshal([]byte(pageOut), &pagePayload); err != nil {
		t.Fatalf("unmarshal page output: %v", err)
	}
	data := pagePayload["data"].(map[string]any)
	rows := data["rows"].([]any)
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows from cursor page, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["id"].(float64) != 21 {
		t.Fatalf("expected cursor page to continue at id 21, got %#v", first)
	}

	tableOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "table", "--config", configPath, "local-sqlite", "orders"})
	})
	var tablePayload map[string]any
	if err := json.Unmarshal([]byte(tableOut), &tablePayload); err != nil {
		t.Fatalf("unmarshal inspect table output: %v", err)
	}
	tableData := tablePayload["data"].(map[string]any)
	if _, ok := tableData["primary_key"]; !ok {
		t.Fatalf("expected primary key in inspect table output: %#v", tableData)
	}
	if _, ok := tableData["foreign_keys"]; !ok {
		t.Fatalf("expected foreign keys in inspect table output: %#v", tableData)
	}

	schemaOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"inspect", "schema", "--config", configPath, "local-sqlite"})
	})
	var schemaPayload map[string]any
	if err := json.Unmarshal([]byte(schemaOut), &schemaPayload); err != nil {
		t.Fatalf("unmarshal inspect schema output: %v", err)
	}
	schemaData := schemaPayload["data"].(map[string]any)
	relations := schemaData["relations"].([]any)
	if len(relations) == 0 {
		t.Fatalf("expected relations in inspect schema output: %#v", schemaData)
	}
}

func TestExportRequiresExplicitConnectionName(t *testing.T) {
	configPath := makeSQLiteFixture(t, 2)
	outFile := filepath.Join(t.TempDir(), "users.csv")
	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"export", "--config", configPath, "--format", "csv", "table", "users", outFile})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["kind"] != "error" || !strings.Contains(payload["warnings"].([]any)[0].(string), "export table requires") {
		t.Fatalf("expected explicit-conn export error, got %#v", payload)
	}
}

func TestQuerySupportsConfigFilePath(t *testing.T) {
	configPath := makeSQLiteConfigFileFixture(t, 3)
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--config", configPath, "local-sqlite", "select id from users order by id"})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if payload["kind"] != "query_result" || payload["conn"] != "local-sqlite" {
		t.Fatalf("expected query_result via config file, got %#v", payload)
	}
	if payload["action"] != "query" {
		t.Fatalf("expected action=query via config file, got %#v", payload)
	}
}

func TestConnListScansDirectoryInStableOrder(t *testing.T) {
	configDir := t.TempDir()
	for name, raw := range map[string]string{
		"zeta.toml": `
[connection]
engine = "mysql"
allow_actions = ["query"]
`,
		"alpha.toml": `
[connection]
engine = "sqlite"
path = "./alpha.db"
allow_actions = ["query"]
`,
		"notes.txt": "ignore me",
	} {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"conn", "list", "--config", configDir})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	data := payload["data"].(map[string]any)
	connections := data["connections"].([]any)
	if len(connections) != 2 {
		t.Fatalf("expected two connections, got %#v", payload)
	}
	first := connections[0].(map[string]any)
	second := connections[1].(map[string]any)
	if first["name"] != "alpha" || second["name"] != "zeta" {
		t.Fatalf("expected sorted names, got %#v", connections)
	}
}

func TestRemovedFormatsAreRejected(t *testing.T) {
	configPath := makeSQLiteFixture(t, 2)
	err := New().Run(context.Background(), []string{"query", "--config", configPath, "--format", "agent", "local-sqlite", "select id from users order by id"})
	if err == nil || !strings.Contains(err.Error(), `unsupported output format "agent"`) {
		t.Fatalf("agent format should be removed, got: %v", err)
	}

	err = New().Run(context.Background(), []string{"query", "--config", configPath, "--format", "llm", "local-sqlite", "select id from users order by id"})
	if err == nil || !strings.Contains(err.Error(), `unsupported output format "llm"`) {
		t.Fatalf("llm format should be removed, got: %v", err)
	}

	err = New().Run(context.Background(), []string{"query", "--config", configPath, "--format", "jsonl", "local-sqlite", "select id from users order by id"})
	if err == nil || !strings.Contains(err.Error(), `unsupported output format "jsonl"`) {
		t.Fatalf("jsonl format should be removed, got: %v", err)
	}
}

func TestSQLCommandIsUnknown(t *testing.T) {
	configPath := makeSQLiteFixture(t, 2)
	err := New().Run(context.Background(), []string{"sql", "--config", configPath, "local-sqlite", "select id from users order by id"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "sql"`) {
		t.Fatalf("sql should be removed, got: %v", err)
	}
}

func TestAllowActionsBlocksWriteCommandsButKeepsQuery(t *testing.T) {
	configPath := makeSQLiteFixtureWithExtras(t, 3, "allow_actions = [\"query\"]\n")

	queryOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--config", configPath, "local-sqlite", "select id from users order by id"})
	})
	var queryPayload map[string]any
	if err := json.Unmarshal([]byte(queryOut), &queryPayload); err != nil {
		t.Fatalf("unmarshal query output: %v", err)
	}
	if queryPayload["action"] != "query" {
		t.Fatalf("expected query action, got %#v", queryPayload)
	}

	for _, args := range [][]string{
		{"update", "--config", configPath, "local-sqlite", "update users set name = 'x' where id = 1"},
		{"schema", "--config", configPath, "local-sqlite", "alter table users add column email text"},
	} {
		out := captureStdout(t, func() error {
			err := New().Run(context.Background(), args)
			var exitErr *ExitError
			if errors.As(err, &exitErr) {
				return nil
			}
			return err
		})
		var payload map[string]any
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("unmarshal blocked output: %v", err)
		}
		if payload["code"] != "action_blocked" {
			t.Fatalf("expected action_blocked, got %#v", payload)
		}
	}
}

func TestActionCommandsRejectMismatchedSQL(t *testing.T) {
	configPath := makeSQLiteFixtureWithExtras(t, 3, "allow_actions = [\"query\", \"update\", \"schema\"]\n")

	for _, tc := range []struct {
		args   []string
		action string
	}{
		{args: []string{"update", "--config", configPath, "local-sqlite", "select id from users"}, action: "update"},
		{args: []string{"schema", "--config", configPath, "local-sqlite", "update users set name = 'x' where id = 1"}, action: "schema"},
	} {
		out := captureStdout(t, func() error {
			err := New().Run(context.Background(), tc.args)
			var exitErr *ExitError
			if errors.As(err, &exitErr) {
				return nil
			}
			return err
		})
		var payload map[string]any
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("unmarshal mismatch output: %v", err)
		}
		if payload["code"] != "action_mismatch" || payload["action"] != tc.action {
			t.Fatalf("expected action_mismatch for %s, got %#v", tc.action, payload)
		}
	}
}

func TestTxRollsBackOnFailureAndRejectsSchemaSteps(t *testing.T) {
	configPath := makeSQLiteFixtureWithExtras(t, 2, "allow_actions = [\"query\", \"update\"]\n")
	dir := t.TempDir()
	failingPlanPath := filepath.Join(dir, "failing-plan.json")
	failingPlan := `{"steps":[{"action":"update","sql":"update users set name = 'changed' where id = 1"},{"action":"update","sql":"update users set name = 'bad'"}]}`
	if err := os.WriteFile(failingPlanPath, []byte(failingPlan), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"tx", "--config", configPath, "local-sqlite", "--plan", failingPlanPath})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal tx error output: %v", err)
	}
	if payload["code"] != "missing_where" {
		t.Fatalf("expected missing_where for failing tx, got %#v", payload)
	}

	verifyOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"query", "--config", configPath, "local-sqlite", "select name from users where id = 1"})
	})
	var verifyPayload map[string]any
	if err := json.Unmarshal([]byte(verifyOut), &verifyPayload); err != nil {
		t.Fatalf("unmarshal verify output: %v", err)
	}
	verifyRows := verifyPayload["data"].(map[string]any)["rows"].([]any)
	if verifyRows[0].(map[string]any)["name"] != "user" {
		t.Fatalf("transaction should have rolled back, got %#v", verifyPayload)
	}

	successPlanPath := filepath.Join(dir, "success-plan.json")
	successPlan := `{"steps":[{"action":"query","sql":"select id from users where id = 1"},{"action":"update","sql":"update users set name = 'ok' where id = 1","max_rows_affected":1}]}`
	if err := os.WriteFile(successPlanPath, []byte(successPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	successOut := captureStdout(t, func() error {
		return New().Run(context.Background(), []string{"tx", "--config", configPath, "local-sqlite", "--plan", successPlanPath})
	})
	var successPayload map[string]any
	if err := json.Unmarshal([]byte(successOut), &successPayload); err != nil {
		t.Fatalf("unmarshal tx success output: %v", err)
	}
	if successPayload["kind"] != "tx_result" {
		t.Fatalf("expected tx_result, got %#v", successPayload)
	}
	steps := successPayload["data"].(map[string]any)["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("expected 2 tx steps, got %#v", successPayload)
	}

	schemaPlanPath := filepath.Join(dir, "schema-plan.json")
	schemaPlan := `{"steps":[{"action":"schema","sql":"alter table users add column email text"}]}`
	if err := os.WriteFile(schemaPlanPath, []byte(schemaPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	schemaOut := captureStdout(t, func() error {
		err := New().Run(context.Background(), []string{"tx", "--config", configPath, "local-sqlite", "--plan", schemaPlanPath})
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	})
	var schemaPayload map[string]any
	if err := json.Unmarshal([]byte(schemaOut), &schemaPayload); err != nil {
		t.Fatalf("unmarshal schema tx output: %v", err)
	}
	if schemaPayload["code"] != "tx_unsupported_action" {
		t.Fatalf("expected tx_unsupported_action, got %#v", schemaPayload)
	}
}

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	buf, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return string(buf)
}

func makeSQLiteFixture(t *testing.T, rows int) string {
	return makeSQLiteFixtureWithExtras(t, rows, "")
}

func makeSQLiteFixtureWithExtras(t *testing.T, rows int, extras string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`create table users (id integer primary key, name text)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= rows; i++ {
		if _, err := sqlDB.Exec(`insert into users (id, name) values (?, ?)`, i, "user"); err != nil {
			t.Fatal(err)
		}
	}
	configDir := filepath.Join(dir, "dbx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`
[connection]
engine = "sqlite"
path = "%s"
allow_actions = ["query", "update", "schema"]
tags = ["local"]
%s
`, dbPath, extras)
	if err := os.WriteFile(filepath.Join(configDir, "local-sqlite.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return configDir
}

func makeSQLiteRelationFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relations.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`pragma foreign_keys = on`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`create table users (id integer primary key, name text not null)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`create table orders (id integer primary key, user_id integer not null, total integer default 0, foreign key(user_id) references users(id))`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		if _, err := sqlDB.Exec(`insert into users (id, name) values (?, ?)`, i, "user"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		if _, err := sqlDB.Exec(`insert into orders (id, user_id, total) values (?, ?, ?)`, i, i, 10); err != nil {
			t.Fatal(err)
		}
	}
	configDir := filepath.Join(dir, "dbx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `
[connection]
engine = "sqlite"
path = "` + dbPath + `"
allow_actions = ["query"]
tags = ["local"]
`
	if err := os.WriteFile(filepath.Join(configDir, "local-sqlite.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return configDir
}

func makeSQLiteConfigFileFixture(t *testing.T, rows int) string {
	t.Helper()
	configDir := makeSQLiteFixture(t, rows)
	configPath := filepath.Join(configDir, "local-sqlite.toml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	}
	return configPath
}
