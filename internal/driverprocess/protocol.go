// Package driverprocess implements DBX's private, versioned driver transport.
package driverprocess

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/dbcore"
)

const ProtocolVersion = 1
const maxFrameBytes = 64 << 20

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Request struct {
	Version    int                `json:"version"`
	Engine     string             `json:"engine"`
	Operation  string             `json:"operation"`
	Spec       conn.Spec          `json:"connection"`
	SQL        string             `json:"sql,omitempty"`
	Offset     int                `json:"offset,omitempty"`
	Limit      int                `json:"limit,omitempty"`
	MaxRows    int                `json:"max_rows,omitempty"`
	Plan       dbcore.TxPlan      `json:"plan,omitempty"`
	Schema     string             `json:"schema,omitempty"`
	Table      string             `json:"table,omitempty"`
	Columns    []string           `json:"columns,omitempty"`
	Rows       []map[string]Value `json:"rows,omitempty"`
	importRows []map[string]any   // In-process only; never serialized to a driver.
}

type Response struct {
	Query     *dbcore.QueryResult   `json:"query,omitempty"`
	Tx        *dbcore.TxRunResult   `json:"tx,omitempty"`
	Count     int64                 `json:"count,omitempty"`
	Schemas   []string              `json:"schemas,omitempty"`
	Tables    []dbcore.TableInfo    `json:"tables,omitempty"`
	Table     *dbcore.TableInfo     `json:"table,omitempty"`
	Relations []dbcore.RelationInfo `json:"relations,omitempty"`
}

type frame struct {
	Version int              `json:"version"`
	Engine  string           `json:"engine"`
	Kind    string           `json:"kind"`
	Step    int              `json:"step"`
	Row     map[string]Value `json:"row,omitempty"`
	Result  *Response        `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
}

// Value makes the protocol independent of JSON's float64 default, preserving
// large integers, exact decimal strings, binary cells and timestamps.
type Value struct {
	Type string `json:"type"`
	Text string `json:"value,omitempty"`
}

func encodeValue(value any) (Value, error) {
	switch v := value.(type) {
	case nil:
		return Value{Type: "null"}, nil
	case string:
		return Value{Type: "string", Text: v}, nil
	case []byte:
		return Value{Type: "bytes", Text: base64.StdEncoding.EncodeToString(v)}, nil
	case bool:
		return Value{Type: "bool", Text: strconv.FormatBool(v)}, nil
	case int:
		return Value{Type: "int", Text: strconv.FormatInt(int64(v), 10)}, nil
	case int32:
		return Value{Type: "int", Text: strconv.FormatInt(int64(v), 10)}, nil
	case int64:
		return Value{Type: "int", Text: strconv.FormatInt(v, 10)}, nil
	case float64:
		return Value{Type: "float", Text: strconv.FormatFloat(v, 'g', -1, 64)}, nil
	case float32:
		return Value{Type: "float", Text: strconv.FormatFloat(float64(v), 'g', -1, 32)}, nil
	case time.Time:
		return Value{Type: "time", Text: v.Format(time.RFC3339Nano)}, nil
	case json.Number:
		return Value{Type: "number", Text: string(v)}, nil
	default:
		return Value{}, fmt.Errorf("unsupported driver value type %T", value)
	}
}

func (v Value) decode() (any, error) {
	switch v.Type {
	case "null":
		return nil, nil
	case "string":
		return v.Text, nil
	case "bytes":
		return base64.StdEncoding.DecodeString(v.Text)
	case "bool":
		return strconv.ParseBool(v.Text)
	case "int":
		return strconv.ParseInt(v.Text, 10, 64)
	case "float":
		return strconv.ParseFloat(v.Text, 64)
	case "time":
		return time.Parse(time.RFC3339Nano, v.Text)
	case "number":
		// Import JSON numbers become database/sql-compatible scalars, never
		// an unvalidated json.Number passed through a vendor driver.
		if n, err := strconv.ParseInt(v.Text, 10, 64); err == nil {
			return n, nil
		}
		if n, err := strconv.ParseFloat(v.Text, 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
			return v.Text, nil
		}
		return nil, fmt.Errorf("invalid numeric driver value")
	default:
		return nil, fmt.Errorf("unknown driver value type")
	}
}

func EncodeRows(rows []map[string]any) ([]map[string]Value, error) {
	out := make([]map[string]Value, 0, len(rows))
	for _, row := range rows {
		r, err := encodeRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func encodeRow(row map[string]any) (map[string]Value, error) {
	out := make(map[string]Value, len(row))
	for k, v := range row {
		cell, err := encodeValue(v)
		if err != nil {
			return nil, err
		}
		out[k] = cell
	}
	return out, nil
}

func decodeRow(row map[string]Value) (map[string]any, error) {
	out := make(map[string]any, len(row))
	for k, v := range row {
		cell, err := v.decode()
		if err != nil {
			return nil, err
		}
		out[k] = cell
	}
	return out, nil
}

func writeFrame(w io.Writer, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > maxFrameBytes {
		return fmt.Errorf("driver frame exceeds %d bytes", maxFrameBytes)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if err = writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, b)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func readFrame(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxFrameBytes {
		return fmt.Errorf("invalid driver frame length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	if err := json.Unmarshal(b, value); err != nil {
		return fmt.Errorf("invalid driver frame JSON")
	}
	return nil
}
