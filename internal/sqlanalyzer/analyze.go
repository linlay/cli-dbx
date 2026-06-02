package sqlanalyzer

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/linlay/cli-dbx/internal/sqlclass"
)

type Analysis struct {
	Raw              string                  `json:"raw"`
	Statements       []StatementAnalysis     `json:"statements"`
	Objects          []string                `json:"objects,omitempty"`
	StatementClass   sqlclass.StatementClass `json:"statement_class"`
	MultiStatement   bool                    `json:"multi_statement"`
	HasUnsafeWrite   bool                    `json:"has_unsafe_write"`
	NeedsAck         bool                    `json:"needs_ack"`
	BlockedByDefault bool                    `json:"blocked_by_default"`
}

type StatementAnalysis struct {
	SQL         string                  `json:"sql"`
	Class       sqlclass.StatementClass `json:"class"`
	Action      string                  `json:"action"`
	Object      string                  `json:"object,omitempty"`
	Objects     []string                `json:"objects,omitempty"`
	HasWhere    bool                    `json:"has_where"`
	HasLimit    bool                    `json:"has_limit"`
	UnsafeWrite bool                    `json:"unsafe_write"`
}

func Analyze(sql string) Analysis {
	stmts := splitStatements(sql)
	result := Analysis{
		Raw:        sql,
		Statements: make([]StatementAnalysis, 0, len(stmts)),
	}
	objectSet := map[string]struct{}{}
	for _, stmt := range stmts {
		item := analyzeStatement(stmt)
		result.Statements = append(result.Statements, item)
		for _, object := range item.Objects {
			objectSet[object] = struct{}{}
		}
		if result.StatementClass == "" || result.StatementClass == sqlclass.ClassUnknown {
			result.StatementClass = item.Class
		}
		if item.UnsafeWrite {
			result.HasUnsafeWrite = true
		}
		if item.Class == sqlclass.ClassWriteData || item.Class == sqlclass.ClassDDL || item.Class == sqlclass.ClassAdmin {
			result.NeedsAck = true
		}
	}
	if len(result.Statements) > 1 {
		result.MultiStatement = true
		result.BlockedByDefault = true
	}
	if result.StatementClass == "" {
		result.StatementClass = sqlclass.ClassUnknown
	}
	for object := range objectSet {
		result.Objects = append(result.Objects, object)
	}
	sort.Strings(result.Objects)
	return result
}

func analyzeStatement(sql string) StatementAnalysis {
	stmt := strings.TrimSpace(stripComments(sql))
	item := StatementAnalysis{
		SQL:      stmt,
		Class:    sqlclass.ClassUnknown,
		HasWhere: containsKeyword(stmt, "where"),
		HasLimit: containsKeyword(stmt, "limit"),
	}
	if stmt == "" {
		return item
	}
	tokens := tokenize(stmt)
	if len(tokens) == 0 {
		return item
	}
	actionIndex := 0
	action := strings.ToLower(tokens[0])
	if action == "with" {
		actionIndex = findCTEAction(tokens)
		if actionIndex >= len(tokens) {
			return item
		}
		action = strings.ToLower(tokens[actionIndex])
	}
	item.Action = action
	item.Class = classifyToken(action)
	item.Objects = extractObjects(tokens, action)
	if len(item.Objects) > 0 {
		item.Object = item.Objects[0]
	}
	if (action == "update" || action == "delete") && !item.HasWhere {
		item.UnsafeWrite = true
	}
	return item
}

func classifyToken(token string) sqlclass.StatementClass {
	switch token {
	case "select", "show", "describe", "desc", "pragma", "explain":
		return sqlclass.ClassRead
	case "insert", "update", "delete", "replace", "merge":
		return sqlclass.ClassWriteData
	case "create", "alter", "drop", "truncate", "rename":
		return sqlclass.ClassDDL
	case "grant", "revoke", "analyze", "vacuum", "set", "reset":
		return sqlclass.ClassAdmin
	default:
		return sqlclass.ClassUnknown
	}
}

func extractObjects(tokens []string, action string) []string {
	cteNames := collectCTENames(tokens)
	objects := map[string]struct{}{}
	add := func(token string) {
		ident := cleanIdent(token)
		if ident == "" {
			return
		}
		if _, ok := cteNames[strings.ToLower(ident)]; ok {
			return
		}
		objects[ident] = struct{}{}
	}
	expectTable := false
	inTableList := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		lower := strings.ToLower(token)
		switch lower {
		case "from", "join", "into":
			expectTable = true
			inTableList = true
			continue
		case "update", "truncate":
			expectTable = true
			inTableList = false
			continue
		case "table", "view":
			if action == "create" || action == "alter" || action == "drop" || action == "rename" {
				expectTable = true
				inTableList = true
			}
			continue
		case "on":
			if action == "create" && hasToken(tokens, "index") {
				expectTable = true
			} else {
				expectTable = false
				inTableList = false
			}
			continue
		case "where", "group", "order", "limit", "having", "set", "values", "returning", "using", "with":
			expectTable = false
			inTableList = false
			continue
		case ",":
			if inTableList {
				expectTable = true
			}
			continue
		case "if", "not", "exists", "only":
			continue
		}
		if expectTable {
			if token == "(" || token == ")" {
				continue
			}
			add(token)
			expectTable = false
		}
	}
	out := make([]string, 0, len(objects))
	for object := range objects {
		out = append(out, object)
	}
	sort.Strings(out)
	return out
}

func splitStatements(sql string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	for i := 0; i < len(sql); i++ {
		ch := rune(sql[i])
		if quote != 0 {
			current.WriteByte(sql[i])
			if ch == quote {
				quote = 0
			} else if ch == '\\' && i+1 < len(sql) {
				i++
				current.WriteByte(sql[i])
			}
			continue
		}
		if i+1 < len(sql) && sql[i] == '-' && sql[i+1] == '-' {
			for i < len(sql) && sql[i] != '\n' {
				current.WriteByte(sql[i])
				i++
			}
			if i < len(sql) {
				current.WriteByte(sql[i])
			}
			continue
		}
		if i+1 < len(sql) && sql[i] == '/' && sql[i+1] == '*' {
			current.WriteByte(sql[i])
			i++
			current.WriteByte(sql[i])
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
				current.WriteByte(sql[i])
			}
			if i+1 < len(sql) {
				i++
				current.WriteByte(sql[i])
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
			current.WriteByte(sql[i])
		case ';':
			part := strings.TrimSpace(current.String())
			if part != "" {
				parts = append(parts, part)
			}
			current.Reset()
		default:
			current.WriteByte(sql[i])
		}
	}
	part := strings.TrimSpace(current.String())
	if part != "" {
		parts = append(parts, part)
	}
	return parts
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

func tokenize(sql string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range sql {
		switch {
		case unicode.IsSpace(r):
			flush()
		case strings.ContainsRune("(),", r):
			flush()
			tokens = append(tokens, string(r))
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func findCTEAction(tokens []string) int {
	depth := 0
	for i := 1; i < len(tokens); i++ {
		switch tokens[i] {
		case "(":
			depth++
		case ")":
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				token := strings.ToLower(tokens[i])
				if classifyToken(token) != sqlclass.ClassUnknown || token == "with" {
					return i
				}
			}
		}
	}
	return len(tokens)
}

func collectCTENames(tokens []string) map[string]struct{} {
	out := map[string]struct{}{}
	if len(tokens) == 0 || !strings.EqualFold(tokens[0], "with") {
		return out
	}
	i := 1
	if i < len(tokens) && strings.EqualFold(tokens[i], "recursive") {
		i++
	}
	for i < len(tokens) {
		name := cleanIdent(tokens[i])
		if name == "" || classifyToken(strings.ToLower(name)) != sqlclass.ClassUnknown {
			return out
		}
		out[strings.ToLower(name)] = struct{}{}
		i++
		if i < len(tokens) && tokens[i] == "(" {
			i = skipTokenBalanced(tokens, i)
		}
		if i >= len(tokens) || !strings.EqualFold(tokens[i], "as") {
			return out
		}
		i++
		if i >= len(tokens) || tokens[i] != "(" {
			return out
		}
		i = skipTokenBalanced(tokens, i)
		if i >= len(tokens) || tokens[i] != "," {
			return out
		}
		i++
	}
	return out
}

func skipTokenBalanced(tokens []string, start int) int {
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i] {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(tokens)
}

func hasToken(tokens []string, target string) bool {
	for _, token := range tokens {
		if strings.EqualFold(token, target) {
			return true
		}
	}
	return false
}

func containsKeyword(sql, keyword string) bool {
	target := strings.ToLower(keyword)
	for _, token := range tokenize(strings.ToLower(stripComments(sql))) {
		if token == target {
			return true
		}
	}
	return false
}

func cleanIdent(ident string) string {
	ident = strings.Trim(ident, "`\"'")
	parts := strings.Split(ident, ".")
	for i, part := range parts {
		parts[i] = strings.Trim(part, "`\"'")
	}
	return strings.Join(parts, ".")
}

func (a Analysis) ValidateSingleStatement() error {
	if a.MultiStatement {
		return fmt.Errorf("multiple statements are blocked by default")
	}
	if a.StatementClass == sqlclass.ClassUnknown {
		return fmt.Errorf("statement type is unknown and blocked by default")
	}
	return nil
}
