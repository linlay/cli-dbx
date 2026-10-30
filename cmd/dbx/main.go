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
	args := os.Args[1:]
	// Vendor connectors may parse the global Go flag set. CLI parsing uses
	// the saved arguments explicitly; vendor code must not consume them.
	os.Args = os.Args[:1]
	os.Exit(app.ExecuteContext(ctx, args, os.Stdin, os.Stdout, os.Stderr))
}
