package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/driverprocess"
	"github.com/oracle/go-oracledb/v26/oracle"
)

func prepare(spec *conn.Spec) error {
	u, err := url.Parse(spec.DSN)
	if err != nil || u.Scheme != "oracle" || u.User == nil || u.Host == "" || u.Path == "" {
		return fmt.Errorf("invalid Oracle connection; use structured connection fields")
	}
	cfg := oracle.NewOracleDriverConfig()
	cfg.ConnectDescriptor = "tcp://" + u.Host + u.EscapedPath()
	cfg.Credentials.User = u.User.Username()
	cfg.Credentials.Password, _ = u.User.Password()
	connector, err := oracle.NewOracleConnector(cfg)
	if err != nil {
		return fmt.Errorf("invalid Oracle connector configuration")
	}
	spec.Connector = connector
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := driverprocess.Serve(ctx, os.Stdin, os.Stdout, "oracle", prepare); err != nil {
		fmt.Fprintln(os.Stderr, "driver process failed")
		os.Exit(1)
	}
}
