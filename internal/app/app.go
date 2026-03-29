package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/linlay/cli-dbx/internal/buildinfo"
	legacycli "github.com/linlay/cli-dbx/internal/cli"
	"github.com/spf13/cobra"
)

const (
	ExitSuccess = 0
	ExitFailure = 1
	ExitUsage   = 2
)

type exitError struct {
	Code int
	Err  error
}

func (e *exitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *exitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return ExecuteContext(context.Background(), args, stdin, stdout, stderr)
}

func ExecuteContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCommand(stdin, stdout, stderr)
	root.SetContext(ctx)
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			if exitErr.Err != nil {
				fmt.Fprintln(stderr, exitErr.Err.Error())
			}
			return exitErr.Code
		}

		fmt.Fprintln(stderr, err.Error())
		return ExitUsage
	}

	return ExitSuccess
}

func newRootCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	var showVersion bool

	cmd := &cobra.Command{
		Use:   "dbx",
		Short: "Database CLI for connection management, SQL execution, and data movement",
		Long: strings.TrimSpace(`
DBX is a database CLI for MySQL, PostgreSQL, and SQLite.

It keeps connection management explicit, separates query/update/schema/admin actions,
and returns results in formats that work well in terminals and scripts.
`),
		UsageLines: []string{
			"dbx [command]",
		},
		Example: strings.TrimSpace(`
dbx conn test local-pg
dbx inspect table local-pg users
dbx query local-pg 'select id, email from users order by id'
dbx tx local-pg --plan ./plan.json
`),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Summary())
				return err
			}
			return cmd.Help()
		},
	}

	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.Flags().BoolVar(&showVersion, "version", false, "Show build version")

	cmd.AddCommand(
		newConnCommand(),
		newInspectCommand(),
		newSQLCommand("query", "Run read-only SQL.", strings.TrimSpace(`
Use query when you need read-only SQL.

Read results return up to 100 rows by default. Keep the same ORDER BY when
continuing with --cursor.
`), strings.TrimSpace(`
dbx query local-pg 'select * from users order by id'
dbx query local-pg 'select * from users order by id' --cursor 100
dbx query dsn postgres 'postgres://app:secret@127.0.0.1:5432/appdb?sslmode=disable' 'select now()'
`)),
		newSQLCommand("update", "Run insert, update, delete, or merge SQL.", strings.TrimSpace(`
Use update for row-changing SQL.

DBX validates the detected statement action against the requested command and
enforces the connection allow_actions policy.
`), strings.TrimSpace(`
dbx update local-pg 'update users set active = 1 where id = 1'
dbx update local-pg 'delete from users where archived = 1'
dbx update file local-pg ./change.sql
`)),
		newSQLCommand("schema", "Run DDL SQL such as create, alter, drop, or truncate.", strings.TrimSpace(`
Use schema for DDL statements.
`), strings.TrimSpace(`
dbx schema local-pg 'create table audit_log (id bigint primary key)'
dbx schema local-pg 'alter table users add column timezone text'
dbx schema file local-pg ./schema.sql
`)),
		newSQLCommand("admin", "Run supported admin SQL such as grant, revoke, set, or vacuum.", strings.TrimSpace(`
Use admin for operational SQL that is neither query, update, nor schema.
`), strings.TrimSpace(`
dbx admin local-pg 'analyze users'
dbx admin local-sqlite 'vacuum'
dbx admin file local-pg ./admin.sql
`)),
		newTxCommand(),
		newImportCommand(),
		newExportCommand(),
		newVersionCommand(),
	)

	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the embedded version, commit, and build time",
		Long:  "Print the embedded version, commit, and build time.",
		UsageLines: []string{
			"dbx version",
		},
		Example: strings.TrimSpace(`
dbx version
dbx --version
`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Summary())
			return err
		},
	}
}

func runLegacyCommand(ctx context.Context, stdout io.Writer, args []string) error {
	if file, ok := stdout.(*os.File); ok && file == os.Stdout {
		return wrapLegacyError(legacycli.New().Run(ctx, args))
	}

	r, w, err := os.Pipe()
	if err != nil {
		return failuref("capture stdout: %v", err)
	}

	oldStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stdout, r)
		copyDone <- copyErr
	}()

	runErr := legacycli.New().Run(ctx, args)
	_ = w.Close()
	copyErr := <-copyDone
	_ = r.Close()

	if copyErr != nil {
		return failuref("copy stdout: %v", copyErr)
	}
	return wrapLegacyError(runErr)
}

func wrapLegacyError(err error) error {
	if err == nil {
		return nil
	}

	var legacyExit *legacycli.ExitError
	if errors.As(err, &legacyExit) {
		return &exitError{Code: legacyExit.Code}
	}

	return failuref("%v", err)
}

func usagef(format string, args ...any) error {
	return &exitError{
		Code: ExitUsage,
		Err:  fmt.Errorf(format, args...),
	}
}

func failuref(format string, args ...any) error {
	return &exitError{
		Code: ExitFailure,
		Err:  fmt.Errorf(format, args...),
	}
}
