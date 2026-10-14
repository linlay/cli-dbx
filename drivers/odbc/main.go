package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	_ "github.com/alexbrainman/odbc"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/driverprocess"
)

var engine = "oracle"

func prepare(spec *conn.Spec) error {
	if spec.Driver != "odbc" || spec.ODBC == nil {
		return fmt.Errorf("ODBC connection configuration is required")
	}
	path := spec.ODBC.DriverPath
	if !filepath.IsAbs(path) {
		return fmt.Errorf("odbc_driver must be an absolute vendor library path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("odbc_driver must resolve to a regular vendor library file")
	}
	name, err := connectionName(path)
	if err != nil {
		return err
	}
	dsn, err := conn.BuildODBCDSN(*spec, name)
	if err != nil {
		return err
	}
	spec.DSN = dsn
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := driverprocess.Serve(ctx, os.Stdin, os.Stdout, engine, prepare); err != nil {
		fmt.Fprintln(os.Stderr, "driver process failed")
		os.Exit(1)
	}
}
