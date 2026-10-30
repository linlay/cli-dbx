//go:build dbmod

package driverprocess

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/dbmodule"
)

type moduleEntry struct {
	path   string
	once   sync.Once
	loaded *dbmodule.Loaded
	err    error
}

// Drivers register globals and can retain background goroutines. Keep one
// module per engine alive until this single-shot CLI exits; never hot-unload.
var modules = struct {
	sync.Mutex
	byEngine map[string]*moduleEntry
}{byEngine: make(map[string]*moduleEntry)}

func loadModule(spec conn.Spec) (*dbmodule.Loaded, error) {
	path, err := filepath.Abs(spec.DriverPath)
	if err != nil {
		return nil, err
	}
	modules.Lock()
	entry := modules.byEngine[spec.Engine]
	if entry == nil {
		entry = &moduleEntry{path: path}
		modules.byEngine[spec.Engine] = entry
	}
	modules.Unlock()
	if entry.path != path {
		return nil, &Error{Code: "driver_invalid", Message: "cannot switch module versions for an engine within a live CLI process"}
	}
	entry.once.Do(func() {
		entry.err = &Error{Code: "driver_invalid", Message: "module initialization failed"}
		host, err := os.Executable()
		if err != nil {
			return
		}
		manifest, content, err := dbmodule.Verify(path, spec.Engine, host)
		if err != nil {
			code := "driver_invalid"
			if os.IsNotExist(err) || strings.Contains(err.Error(), "is missing") {
				code = "driver_missing"
			}
			entry.err = &Error{Code: code, Message: err.Error()}
			return
		}
		loaded, err := dbmodule.Load(bytes.NewReader(content), dbmodule.Entry(manifest.Engine), manifest.DecodedBytes)
		if err == nil {
			entry.loaded, entry.err = loaded, nil
		}
	})
	return entry.loaded, entry.err
}

func callModule(ctx context.Context, spec conn.Spec, req Request, dispatched *atomic.Bool) (Response, error) {
	type outcome struct {
		response Response
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		var result outcome
		defer func() {
			if recover() != nil {
				// Panic values and stacks may contain connection credentials.
				result = outcome{err: &Error{Code: "database_error", Message: "database module failed"}}
			}
			done <- result
		}()
		loaded, err := loadModule(spec)
		if err != nil {
			result.err = err
			return
		}
		defer runtime.KeepAlive(loaded)
		if result.err = ctx.Err(); result.err != nil {
			return
		}
		if loaded.Prepare != nil {
			if err := loaded.Prepare(&req.Spec); err != nil {
				result.err = &Error{Code: "database_error", Message: redactMessage(err.Error(), spec)}
				return
			}
		}
		if result.err = ctx.Err(); result.err != nil {
			return
		}
		dispatched.Store(true)
		result.response, result.err = dispatch(ctx, req)
		if result.err != nil {
			result.err = &Error{Code: "database_error", Message: redactMessage(result.err.Error(), spec)}
		}
	}()
	select {
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		return result.response, result.err
	case <-ctx.Done():
		// Allow normal rollback/Close, but don't wait forever for a vendor that
		// ignores Context. The CLI renders the error and exits the whole process.
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
		return Response{}, ctx.Err()
	}
}
