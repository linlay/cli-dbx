package driverprocess

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/dbcore"
)

// Serve handles exactly one operation. A transaction plan or import is one
// operation and therefore cannot escape into a second process/connection.
func Serve(ctx context.Context, in io.Reader, out io.Writer, engine string, prepare func(*conn.Spec) error) error {
	respondError := func(code, message string) error {
		return writeFrame(out, frame{Version: ProtocolVersion, Engine: engine, Kind: "error", Error: &Error{Code: code, Message: message}})
	}
	var req Request
	if err := readFrame(in, &req); err != nil {
		return respondError("driver_protocol_error", "invalid driver request")
	}
	if req.Version != ProtocolVersion {
		return respondError("driver_protocol_mismatch", "driver protocol version does not match DBX")
	}
	if req.Engine != engine || req.Spec.Engine != engine {
		return respondError("driver_invalid", "driver engine does not match request")
	}
	if req.Spec.Timeout <= 0 {
		req.Spec.Timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, req.Spec.Timeout)
	defer cancel()
	// Parent keeps stdin open while awaiting a result. EOF means it vanished.
	// The caller process exits after Serve, so this reader cannot outlive it.
	go func() { var b [1]byte; _, _ = in.Read(b[:]); cancel() }()
	if prepare != nil {
		if err := prepare(&req.Spec); err != nil {
			return respondError("database_error", redactMessage(err.Error(), req.Spec))
		}
	}
	result, err := dispatch(ctx, req)
	if err != nil {
		return respondError("database_error", redactMessage(err.Error(), req.Spec))
	}
	sendRows := func(step int, q *dbcore.QueryResult) error {
		if q == nil {
			return nil
		}
		for _, row := range q.Rows {
			encoded, err := encodeRow(row)
			if err != nil {
				return err
			}
			if err = writeFrame(out, frame{Version: ProtocolVersion, Engine: engine, Kind: "row", Step: step, Row: encoded}); err != nil {
				return err
			}
		}
		q.Rows = nil
		return nil
	}
	if err := sendRows(-1, result.Query); err != nil {
		return respondError("driver_protocol_error", err.Error())
	}
	if result.Tx != nil {
		for i := range result.Tx.Steps {
			if err := sendRows(i, result.Tx.Steps[i].Query); err != nil {
				return respondError("driver_protocol_error", err.Error())
			}
		}
	}
	return writeFrame(out, frame{Version: ProtocolVersion, Engine: engine, Kind: "result", Result: &result})
}

func redactMessage(message string, spec conn.Spec) string {
	if spec.Driver == "odbc" {
		redacted := "ODBC driver error details redacted; check the connection and driver installation"
		if strings.Contains(message, "verify database state before retrying a write") {
			redacted += "; verify database state before retrying a write"
		}
		return redacted
	}
	if spec.DSN == "" {
		return message
	}
	if spec.Engine == "postgres" && !strings.HasPrefix(strings.ToLower(spec.DSN), "postgres://") && !strings.HasPrefix(strings.ToLower(spec.DSN), "postgresql://") {
		return "PostgreSQL driver error details redacted"
	}
	if spec.Engine == "postgres" {
		if parsed, err := url.Parse(spec.DSN); err == nil {
			for key := range parsed.Query() {
				if strings.EqualFold(key, "password") {
					return "PostgreSQL driver error details redacted"
				}
			}
		}
	}
	message = strings.ReplaceAll(message, spec.DSN, "[redacted connection]")
	password := ""
	if spec.Engine == "dm" {
		raw := strings.TrimPrefix(spec.DSN, "dm://")
		if end := strings.LastIndex(raw, "?"); end >= 0 {
			raw = raw[:end]
		}
		if end := strings.LastIndex(raw, "@"); end >= 0 {
			raw = raw[:end]
			if start := strings.Index(raw, ":"); start >= 0 {
				password = raw[start+1:]
			}
		}
	} else if u, err := url.Parse(spec.DSN); err == nil && u.User != nil {
		password, _ = u.User.Password()
	}
	if password != "" {
		userinfoPassword := strings.TrimPrefix(url.UserPassword("user", password).String(), "user:")
		for _, s := range []string{userinfoPassword, url.QueryEscape(password), url.PathEscape(password), password} {
			message = strings.ReplaceAll(message, s, "***")
		}
	}
	return message
}

func dispatch(ctx context.Context, req Request) (Response, error) {
	var r Response
	var err error
	switch req.Operation {
	case "ping":
		err = dbcore.Ping(ctx, req.Spec)
	case "query":
		var q dbcore.QueryResult
		q, err = dbcore.Query(ctx, req.Spec, req.SQL, req.Offset, req.Limit)
		r.Query = &q
	case "execute":
		r.Count, err = dbcore.Execute(ctx, req.Spec, req.SQL)
	case "guarded":
		r.Count, err = dbcore.ExecuteGuarded(ctx, req.Spec, req.SQL, req.MaxRows)
	case "tx":
		var tx dbcore.TxRunResult
		tx, err = dbcore.RunTxPlan(ctx, req.Spec, req.Plan, req.Limit, req.MaxRows)
		r.Tx = &tx
	case "schemas":
		r.Schemas, err = dbcore.ListSchemas(ctx, req.Spec)
	case "tables":
		r.Tables, err = dbcore.ListTables(ctx, req.Spec, req.Schema)
	case "describe":
		var table dbcore.TableInfo
		table, err = dbcore.DescribeTable(ctx, req.Spec, req.Schema, req.Table)
		r.Table = &table
	case "relations":
		r.Relations, err = dbcore.ListRelations(ctx, req.Spec, req.Schema)
	case "import":
		rows := make([]map[string]any, 0, len(req.Rows))
		for _, row := range req.Rows {
			decoded, e := decodeRow(row)
			if e != nil {
				return r, e
			}
			rows = append(rows, decoded)
		}
		r.Count, err = dbcore.ImportRows(ctx, req.Spec, req.Table, req.Columns, rows, req.MaxRows)
	default:
		err = fmt.Errorf("unsupported driver operation")
	}
	return r, err
}
