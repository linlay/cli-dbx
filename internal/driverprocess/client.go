package driverprocess

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/linlay/cli-dbx/internal/conn"
)

func Call(ctx context.Context, spec conn.Spec, req Request) (response Response, callErr error) {
	var dispatched atomic.Bool
	defer func() {
		if callErr != nil && dispatched.Load() && (req.Operation == "execute" || req.Operation == "guarded" || req.Operation == "tx" || req.Operation == "import") {
			callErr = fmt.Errorf("%w; verify database state before retrying a write", callErr)
		}
	}()
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	// The same typed request can be dispatched directly without transport.
	req.Version, req.Engine = ProtocolVersion, spec.Engine
	req.Spec = conn.Spec{Engine: spec.Engine, Driver: spec.Driver, ODBC: spec.ODBC, DSN: spec.DSN, Host: spec.Host, Port: spec.Port, User: spec.User, Database: spec.Database, Schema: spec.Schema, Timeout: timeout}
	if isModule(spec) {
		if spec.Driver == "odbc" {
			return Response{}, &Error{Code: "driver_invalid", Message: "ODBC does not support Go object modules"}
		}
		return callModule(ctx, spec, req, &dispatched)
	}
	path, err := executable(spec)
	if err != nil {
		return Response{}, err
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	// Only connection fields cross the private pipe; policy and key-store
	// details stay in DBX.
	if err := ensureWorkerLifetime(); err != nil {
		return Response{}, &Error{Code: "driver_start_failed", Message: "cannot establish driver process lifetime protection"}
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Response{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return Response{}, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return Response{}, &Error{Code: "driver_start_failed", Message: "cannot start installed driver executable"}
	}
	// Exactly one Wait on every started child, including write/protocol errors.
	if err = writeFrame(stdin, req); err != nil {
		_ = cmd.Process.Kill()
		stdin.Close()
		_ = cmd.Wait()
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, &Error{Code: "driver_protocol_error", Message: "cannot send request to driver"}
	}
	dispatched.Store(true)
	result, readErr := readResponse(stdout, spec.Engine, req)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	stdin.Close()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if readErr != nil {
		return Response{}, readErr
	}
	if waitErr != nil {
		return Response{}, &Error{Code: "driver_process_failed", Message: "driver process exited unsuccessfully; verify database state before retrying a write"}
	}
	return result, nil
}

func isModule(spec conn.Spec) bool {
	return strings.EqualFold(filepath.Ext(spec.DriverPath), ".dbmod")
}

// ImportRows avoids protocol cell copies for in-process object modules.
func ImportRows(ctx context.Context, spec conn.Spec, table string, columns []string, rows []map[string]any, maxRows int) (int64, error) {
	req := Request{Operation: "import", Table: table, Columns: columns, MaxRows: maxRows}
	if isModule(spec) {
		req.importRows = rows
	} else {
		var err error
		req.Rows, err = EncodeRows(rows)
		if err != nil {
			return 0, err
		}
	}
	result, err := Call(ctx, spec, req)
	return result.Count, err
}

func readResponse(r io.Reader, engine string, req Request) (Response, error) {
	rows := map[int][]map[string]any{}
	for {
		var f frame
		if err := readFrame(r, &f); err != nil {
			return Response{}, &Error{Code: "driver_protocol_error", Message: "driver closed or returned an invalid response; verify database state before retrying a write"}
		}
		if f.Version != ProtocolVersion {
			return Response{}, &Error{Code: "driver_protocol_mismatch", Message: "driver response protocol does not match DBX"}
		}
		if f.Engine != engine {
			return Response{}, &Error{Code: "driver_protocol_error", Message: "driver response engine does not match DBX"}
		}
		switch f.Kind {
		case "error":
			if f.Error == nil {
				return Response{}, invalidResponse()
			}
			return Response{}, f.Error
		case "row":
			if req.Operation == "query" {
				if f.Step != -1 {
					return Response{}, invalidResponse()
				}
			} else if req.Operation == "tx" {
				if f.Step < 0 || f.Step >= len(req.Plan.Steps) || req.Plan.Steps[f.Step].Action != "query" {
					return Response{}, invalidResponse()
				}
			} else {
				return Response{}, invalidResponse()
			}
			if req.Limit > 0 && len(rows[f.Step]) >= req.Limit {
				return Response{}, invalidResponse()
			}
			row, err := decodeRow(f.Row)
			if err != nil {
				return Response{}, invalidResponse()
			}
			rows[f.Step] = append(rows[f.Step], row)
		case "result":
			if f.Result == nil {
				return Response{}, invalidResponse()
			}
			result := *f.Result
			if req.Operation == "query" {
				if result.Query == nil {
					return Response{}, invalidResponse()
				}
				result.Query.Rows = rows[-1]
				if result.Query.Rows == nil {
					result.Query.Rows = []map[string]any{}
				}
				if result.Query.RowCount != len(result.Query.Rows) {
					return Response{}, invalidResponse()
				}
			}
			if req.Operation == "tx" {
				if result.Tx == nil || len(result.Tx.Steps) != len(req.Plan.Steps) {
					return Response{}, invalidResponse()
				}
				for i := range result.Tx.Steps {
					step := &result.Tx.Steps[i]
					if step.Action != req.Plan.Steps[i].Action || step.Index != i {
						return Response{}, invalidResponse()
					}
					if step.Action == "query" {
						if step.Query == nil {
							return Response{}, invalidResponse()
						}
						step.Query.Rows = rows[i]
						if step.Query.Rows == nil {
							step.Query.Rows = []map[string]any{}
						}
						if step.Query.RowCount != len(step.Query.Rows) {
							return Response{}, invalidResponse()
						}
					}
				}
			}
			if req.Operation == "describe" && result.Table == nil {
				return Response{}, invalidResponse()
			}
			return result, nil
		default:
			return Response{}, invalidResponse()
		}
	}
}

func invalidResponse() error {
	return &Error{Code: "driver_protocol_error", Message: "invalid driver response shape"}
}
