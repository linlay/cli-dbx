//go:build dbmod

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/linlay/cli-dbx/internal/dbmodule"
	"github.com/linlay/cli-dbx/internal/driverprocess"
)

func runModuleWorker(ctx context.Context, args []string) (bool, int) {
	if len(args) == 0 || args[0] != "--dbx-module-worker" {
		return false, 0
	}
	if len(args) != 3 {
		return true, 2
	}
	// Some vendor libraries parse the global Go flag set when constructing
	// their connector. Worker transport arguments are DBX's private arguments.
	os.Args = os.Args[:1]
	host, err := os.Executable()
	if err != nil {
		return true, 1
	}
	m, content, err := dbmodule.Verify(args[1], args[2], host)
	if err != nil {
		fmt.Fprintln(os.Stderr, "module validation failed")
		return true, 1
	}
	loaded, err := dbmodule.Load(bytes.NewReader(content), dbmodule.Entry(m.Engine), m.DecodedBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "module initialization failed:", err)
		return true, 1
	}
	err = serveUntilParentCloses(ctx, os.Stdin, func(in io.Reader) error {
		return driverprocess.Serve(ctx, in, os.Stdout, m.Engine, loaded.Prepare)
	})
	runtime.KeepAlive(loaded)
	if err != nil {
		return true, 1
	}
	return true, 0
}

// A vendor may ignore context cancellation while inside a network read. Keep
// the parent pipe lifetime independent of that operation: main exits this
// dedicated worker after a short grace period, closing all its connections.
type parentPipe struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (p *parentPipe) Read(b []byte) (int, error) {
	n, err := p.Reader.Read(b)
	if err != nil {
		p.once.Do(func() { close(p.closed) })
	}
	return n, err
}

func serveUntilParentCloses(ctx context.Context, in io.Reader, serve func(io.Reader) error) error {
	pipe := &parentPipe{Reader: in, closed: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- serve(pipe) }()
	select {
	case err := <-result:
		return err
	case <-pipe.closed:
	case <-ctx.Done():
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		return fmt.Errorf("worker parent pipe or context closed")
	}
}
