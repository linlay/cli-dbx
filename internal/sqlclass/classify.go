package sqlclass

import (
	"strings"
	"unicode"
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
	s := firstStatement(stripComments(sql))
	if s == "" {
		return ClassUnknown
	}
	token := leadingToken(s)
	if token == "with" {
		token = classifyWithToken(s)
	}
	return classifyToken(token)
}

func HasUnsafeWrite(sql string) bool {
	s := firstStatement(stripComments(sql))
	token := leadingToken(s)
	if token == "with" {
		token = classifyWithToken(s)
	}
	if token != "update" && token != "delete" {
		return false
	}
	return !strings.Contains(" "+strings.ToLower(s)+" ", " where ")
}

func classifyToken(token string) StatementClass {
	switch token {
	case "select", "show", "describe", "desc", "pragma", "explain":
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

func stripComments(sql string) string {
	var b strings.Builder
	inLine := false
	inBlock := false
	for i := 0; i < len(sql); i++ {
		if inLine {
			if sql[i] == '\n' {
				inLine = false
				b.WriteByte(sql[i])
			}
			continue
		}
		if inBlock {
			if i+1 < len(sql) && sql[i] == '*' && sql[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		if i+1 < len(sql) && sql[i] == '-' && sql[i+1] == '-' {
			inLine = true
			i++
			continue
		}
		if i+1 < len(sql) && sql[i] == '/' && sql[i+1] == '*' {
			inBlock = true
			i++
			continue
		}
		b.WriteByte(sql[i])
	}
	return b.String()
}

func firstStatement(sql string) string {
	parts := strings.Split(sql, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func leadingToken(s string) string {
	for _, token := range strings.Fields(strings.ToLower(s)) {
		return token
	}
	return ""
}

func classifyWithToken(sql string) string {
	s := strings.TrimSpace(strings.ToLower(sql))
	if !strings.HasPrefix(s, "with") {
		return leadingToken(s)
	}
	i := len("with")
	i = skipSpace(s, i)
	if strings.HasPrefix(s[i:], "recursive") {
		i += len("recursive")
	}
	for {
		i = skipSpace(s, i)
		for i < len(s) && (unicode.IsLetter(rune(s[i])) || unicode.IsDigit(rune(s[i])) || s[i] == '_' || s[i] == '.') {
			i++
		}
		i = skipSpace(s, i)
		if i < len(s) && s[i] == '(' {
			i = skipBalanced(s, i)
		}
		i = skipSpace(s, i)
		if !strings.HasPrefix(s[i:], "as") {
			return "with"
		}
		i += len("as")
		i = skipSpace(s, i)
		if i >= len(s) || s[i] != '(' {
			return "with"
		}
		i = skipBalanced(s, i)
		i = skipSpace(s, i)
		if i >= len(s) || s[i] != ',' {
			break
		}
		i++
	}
	i = skipSpace(s, i)
	return leadingToken(s[i:])
}

func skipBalanced(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

func skipSpace(s string, i int) int {
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	return i
}
