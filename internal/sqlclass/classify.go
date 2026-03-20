package sqlclass

import (
	"strings"

	"github.com/linlay/dbx/internal/mode"
)

func Classify(sql string) mode.StatementClass {
	s := normalize(sql)
	if s == "" {
		return mode.ClassUnknown
	}
	token := firstToken(s)
	switch token {
	case "select", "show", "describe", "desc", "pragma", "with", "explain":
		return mode.ClassRead
	case "insert", "update", "delete", "replace", "merge":
		return mode.ClassWriteData
	case "create", "alter", "drop", "truncate", "rename":
		return mode.ClassDDL
	case "grant", "revoke", "analyze", "vacuum", "set", "reset":
		return mode.ClassAdmin
	default:
		return mode.ClassUnknown
	}
}

func HasUnsafeWrite(sql string) bool {
	s := normalize(sql)
	token := firstToken(s)
	if token != "update" && token != "delete" {
		return false
	}
	return !strings.Contains(s, " where ")
}

func normalize(sql string) string {
	lines := strings.Split(sql, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		clean = append(clean, line)
	}
	return strings.ToLower(" " + strings.Join(clean, " ") + " ")
}

func firstToken(s string) string {
	for _, token := range strings.Fields(s) {
		if strings.HasPrefix(token, "/*") {
			continue
		}
		return token
	}
	return ""
}
