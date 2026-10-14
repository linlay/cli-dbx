package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/linlay/cli-dbx/internal/driverprocess"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := driverprocess.Serve(ctx, os.Stdin, os.Stdout, "postgres", nil); err != nil {
		fmt.Fprintln(os.Stderr, "driver process failed")
		os.Exit(1)
	}
}
