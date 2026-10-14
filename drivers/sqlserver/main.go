package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/linlay/cli-dbx/internal/driverprocess"
	_ "github.com/microsoft/go-mssqldb"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := driverprocess.Serve(ctx, os.Stdin, os.Stdout, "sqlserver", nil); err != nil {
		fmt.Fprintln(os.Stderr, "driver process failed")
		os.Exit(1)
	}
}
