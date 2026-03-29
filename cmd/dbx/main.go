package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/linlay/cli-dbx/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(app.ExecuteContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
