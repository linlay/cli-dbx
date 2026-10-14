package sqlclass

import (
	"strings"

	"github.com/linlay/cli-dbx/internal/sqllex"
)

type StatementClass string

const (
	ClassRead      StatementClass = "read"
	ClassWriteData StatementClass = "write-data"
	ClassDDL       StatementClass = "ddl"
	ClassAdmin     StatementClass = "admin"
	ClassUnknown   StatementClass = "unknown"
)

func Classify(sql string) StatementClass {
	tokens, err := sqllex.Scan(sql)
	if err != nil {
		return ClassUnknown
	}
	return classify(sqllex.First(tokens))
}
func classify(tokens []sqllex.Token) StatementClass {
	if len(tokens) == 0 {
		return ClassUnknown
	}
	outer, ctes, _, ok := sqllex.With(tokens)
	if !ok {
		return ClassUnknown
	}
	for _, cte := range ctes {
		if classify(cte.Body) != ClassRead {
			return ClassUnknown
		}
	}
	if len(outer) == 0 || outer[0].Kind != sqllex.Word {
		return ClassUnknown
	}
	switch strings.ToLower(outer[0].Text) {
	case "select":
		if sqllex.HasTop(outer, "into") {
			return ClassDDL
		}
		return ClassRead
	case "with":
		return ClassUnknown
	case "explain":
		// EXPLAIN options vary by dialect. Only a verified read-only body is admitted,
		// including ANALYZE, which can execute the body rather than just plan it.
		for i := 1; i < len(outer); i++ {
			t := outer[i]
			if t.Depth != outer[0].Depth {
				continue
			}
			if t.Is("select") || t.Is("with") || t.Is("values") {
				if classify(outer[i:]) == ClassRead {
					return ClassRead
				}
				return ClassUnknown
			}
			if classifyToken(strings.ToLower(t.Text)) != ClassUnknown && !t.Is("analyze") {
				return ClassUnknown
			}
		}
		return ClassUnknown
	case "pragma":
		return classifyPragma(outer)
	default:
		return classifyToken(strings.ToLower(outer[0].Text))
	}
}
func classifyPragma(tokens []sqllex.Token) StatementClass {
	if len(tokens) < 2 || !tokens[1].Ident() {
		return ClassUnknown
	}
	i := 1
	if len(tokens) > 3 && tokens[2].Text == "." {
		i = 3
	}
	name := strings.ToLower(tokens[i].Text)
	// Argument-bearing metadata pragmas are read-only; other argument/assignment
	// forms may change database or connection state and require admin.
	switch name {
	case "table_info", "table_xinfo", "index_info", "index_xinfo", "index_list", "foreign_key_list", "table_list", "foreign_key_check", "integrity_check", "quick_check":
		return ClassRead
	}
	if i+1 < len(tokens) {
		return ClassAdmin
	}
	switch name {
	case "user_version", "application_id", "schema_version", "data_version", "database_list", "compile_options", "collation_list", "function_list", "module_list", "pragma_list", "foreign_keys", "journal_mode", "synchronous", "cache_size", "page_size", "page_count", "freelist_count", "encoding", "auto_vacuum", "busy_timeout", "query_only", "read_uncommitted", "recursive_triggers", "secure_delete", "temp_store", "locking_mode", "mmap_size", "wal_autocheckpoint", "defer_foreign_keys", "ignore_check_constraints", "legacy_alter_table", "analysis_limit":
		return ClassRead
	default:
		return ClassAdmin
	}
}
func HasUnsafeWrite(sql string) bool {
	tokens, err := sqllex.Scan(sql)
	if err != nil {
		return true
	}
	outer, _, _, ok := sqllex.With(sqllex.First(tokens))
	if !ok {
		return true
	}
	return len(outer) > 0 && (outer[0].Is("update") || outer[0].Is("delete")) && !sqllex.HasTop(outer, "where")
}
func classifyToken(token string) StatementClass {
	switch token {
	case "select", "values", "show", "describe", "desc":
		return ClassRead
	case "insert", "update", "delete", "replace", "merge":
		return ClassWriteData
	case "create", "alter", "drop", "truncate", "rename":
		return ClassDDL
	case "grant", "revoke", "analyze", "vacuum", "set", "reset":
		return ClassAdmin
	default:
		return ClassUnknown
	}
}
func RiskLevel(class StatementClass) string {
	switch class {
	case ClassAdmin, ClassDDL:
		return "high"
	case ClassWriteData:
		return "medium"
	default:
		return "low"
	}
}
