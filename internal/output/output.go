package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/linlay/dbx/internal/conn"
	"github.com/linlay/dbx/internal/db"
	"github.com/linlay/dbx/internal/mode"
)

type Envelope struct {
	OK             bool                `json:"ok"`
	Kind           string              `json:"kind,omitempty"`
	Code           string              `json:"code,omitempty"`
	Hint           string              `json:"hint,omitempty"`
	Next           string              `json:"next,omitempty"`
	More           bool                `json:"more,omitempty"`
	Mode           string              `json:"mode"`
	Engine         string              `json:"engine"`
	Connection     string              `json:"connection"`
	StatementClass mode.StatementClass `json:"statement_class,omitempty"`
	RiskLevel      string              `json:"risk_level,omitempty"`
	RowCount       int                 `json:"row_count,omitempty"`
	Truncated      bool                `json:"truncated,omitempty"`
	Summary        string              `json:"summary,omitempty"`
	Data           any                 `json:"data,omitempty"`
	Warnings       []string            `json:"warnings,omitempty"`
	AuditID        string              `json:"audit_id"`
	Fingerprint    string              `json:"fingerprint,omitempty"`
	Meta           map[string]any      `json:"meta,omitempty"`
	Verbose        bool                `json:"-"`
}

func PrintEnvelope(format string, env Envelope) error {
	switch strings.ToLower(format) {
	case "", "json":
		return json.NewEncoder(os.Stdout).Encode(compactPayload(env))
	case "table":
		fmt.Printf("ok: %t\n", env.OK)
		fmt.Printf("connection: %s (%s)\n", env.Connection, env.Engine)
		if env.StatementClass != "" {
			fmt.Printf("statement_class: %s\n", env.StatementClass)
			fmt.Printf("risk_level: %s\n", env.RiskLevel)
		}
		if env.Summary != "" {
			fmt.Printf("summary: %s\n", env.Summary)
		}
		if env.Data != nil {
			if rows, ok := env.Data.([]map[string]any); ok {
				printRows(rows)
			} else {
				buf, _ := json.MarshalIndent(env.Data, "", "  ")
				fmt.Println(string(buf))
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}

func compactPayload(env Envelope) map[string]any {
	payload := map[string]any{
		"ok":      env.OK,
		"kind":    env.Kind,
		"conn":    env.Connection,
		"summary": env.Summary,
		"more":    env.More,
	}
	if env.StatementClass != "" {
		payload["class"] = env.StatementClass
	}
	if env.Data != nil {
		payload["data"] = env.Data
	}
	if env.Next != "" {
		payload["next"] = env.Next
	}
	if env.Code != "" {
		payload["code"] = env.Code
	}
	if env.Hint != "" {
		payload["hint"] = env.Hint
	}
	if len(env.Warnings) > 0 {
		payload["warnings"] = env.Warnings
	}
	if env.Verbose {
		if env.Engine != "" {
			payload["engine"] = env.Engine
		}
		if env.Mode != "" {
			payload["mode"] = env.Mode
		}
		if env.RiskLevel != "" {
			payload["risk_level"] = env.RiskLevel
		}
		if env.Meta != nil {
			payload["meta"] = env.Meta
		}
		if env.AuditID != "" {
			payload["audit_id"] = env.AuditID
		}
		if env.Fingerprint != "" {
			payload["fingerprint"] = env.Fingerprint
		}
		if env.RowCount > 0 {
			payload["row_count"] = env.RowCount
		}
		if env.Truncated {
			payload["truncated"] = env.Truncated
		}
	}
	return payload
}

func CompactColumns(columns []db.Column) []string {
	out := make([]string, 0, len(columns))
	for _, col := range columns {
		suffix := "?"
		if !col.Nullable {
			suffix = "!"
		}
		colType := strings.ToLower(col.Type)
		if colType == "" {
			colType = "unknown"
		}
		out = append(out, fmt.Sprintf("%s:%s%s", col.Name, colType, suffix))
	}
	return out
}

func QueryData(result db.QueryResult, cursor, nextCursor string) map[string]any {
	data := map[string]any{
		"cols":      CompactColumns(result.Columns),
		"rows":      result.Rows,
		"returned":  len(result.Rows),
		"seen":      result.SeenCount,
		"truncated": result.Truncated,
	}
	if cursor != "" {
		data["cursor"] = cursor
	}
	if nextCursor != "" {
		data["next_cursor"] = nextCursor
	}
	return data
}

func QuerySummary(result db.QueryResult, nextCursor string) string {
	returned := len(result.Rows)
	switch {
	case result.SeenCount == 0:
		return "no rows found; refine the exec only if you expected data"
	case result.Truncated:
		if nextCursor != "" {
			return fmt.Sprintf("%d rows found; returned %d rows; continue with --cursor %s", result.SeenCount, returned, nextCursor)
		}
		return fmt.Sprintf("%d rows found; returned %d rows; refine with where/order by if needed", result.SeenCount, returned)
	default:
		return fmt.Sprintf("%d rows found; returned %d rows", result.SeenCount, returned)
	}
}

func ConnectionMeta(spec conn.Spec) map[string]any {
	return map[string]any{
		"target":       spec.DisplayTarget,
		"environment":  spec.Environment,
		"role":         spec.Role,
		"read_only":    spec.ReadOnly,
		"tags":         spec.Tags,
		"secretSource": spec.SecretSources,
	}
}

func printRows(rows []map[string]any) {
	if len(rows) == 0 {
		fmt.Println("(no rows)")
		return
	}
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println(strings.Join(keys, "\t"))
	for _, row := range rows {
		values := make([]string, 0, len(keys))
		for _, key := range keys {
			values = append(values, fmt.Sprint(row[key]))
		}
		fmt.Println(strings.Join(values, "\t"))
	}
}
