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
}

func PrintEnvelope(format string, env Envelope) error {
	switch strings.ToLower(format) {
	case "", "json", "llm":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(env)
	case "jsonl":
		return json.NewEncoder(os.Stdout).Encode(env)
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

func SummarizeResult(result db.QueryResult, truncateTokens int) string {
	parts := []string{fmt.Sprintf("%d rows", result.RowCount)}
	if result.Truncated {
		parts = append(parts, "truncated")
	}
	if len(result.Columns) > 0 {
		colNames := make([]string, 0, len(result.Columns))
		for _, col := range result.Columns {
			colNames = append(colNames, fmt.Sprintf("%s:%s", col.Name, strings.ToLower(col.Type)))
		}
		parts = append(parts, "columns="+strings.Join(colNames, ", "))
	}
	summary := strings.Join(parts, "; ")
	if truncateTokens > 0 && len(summary) > truncateTokens*4 {
		summary = summary[:truncateTokens*4] + "..."
	}
	return summary
}

func LLMData(result db.QueryResult, sampleSize int) map[string]any {
	data := map[string]any{
		"columns": result.Columns,
	}
	rows := result.Rows
	if sampleSize > 0 && len(rows) > sampleSize {
		rows = rows[:sampleSize]
	}
	data["rows"] = rows
	if len(result.Stats) > 0 {
		stats := make([]map[string]any, 0, len(result.Stats))
		names := make([]string, 0, len(result.Stats))
		for name := range result.Stats {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			stat := result.Stats[name]
			stats = append(stats, map[string]any{
				"name":          name,
				"null_count":    stat.NullCount,
				"distinct_seen": stat.DistinctSeen,
				"samples":       stat.Samples,
			})
		}
		data["column_stats"] = stats
	}
	return data
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
