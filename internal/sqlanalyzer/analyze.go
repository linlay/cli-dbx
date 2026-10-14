package sqlanalyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/linlay/cli-dbx/internal/sqlclass"
	"github.com/linlay/cli-dbx/internal/sqllex"
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

func Analyze(sql string) Analysis { return analyze(sql, "") }

func analyze(sql, engine string) Analysis {
	result := Analysis{Raw: sql, StatementClass: sqlclass.ClassUnknown, Statements: []StatementAnalysis{}}
	tokens, err := sqllex.Scan(sql)
	if err != nil {
		result.BlockedByDefault = true
		return result
	}
	objects := map[string]struct{}{}
	appendStatement := func(part []sqllex.Token) {
		if len(part) == 0 {
			return
		}
		item := analyzeStatement(sql[part[0].Start:part[len(part)-1].End], part, engine)
		result.Statements = append(result.Statements, item)
		if len(result.Statements) == 1 {
			result.StatementClass = item.Class
		}
		for _, object := range item.Objects {
			objects[object] = struct{}{}
		}
		result.HasUnsafeWrite = result.HasUnsafeWrite || item.UnsafeWrite
		if item.Class == sqlclass.ClassUnknown {
			result.BlockedByDefault = true
		}
		if item.Class == sqlclass.ClassWriteData || item.Class == sqlclass.ClassDDL || item.Class == sqlclass.ClassAdmin {
			result.NeedsAck = true
		}
	}
	start := 0
	for i, t := range tokens {
		if t.Kind == sqllex.Symbol && t.Text == ";" {
			appendStatement(tokens[start:i])
			start = i + 1
		}
	}
	appendStatement(tokens[start:])
	result.MultiStatement = len(result.Statements) > 1
	result.BlockedByDefault = result.BlockedByDefault || result.MultiStatement
	for object := range objects {
		result.Objects = append(result.Objects, object)
	}
	sort.Strings(result.Objects)
	return result
}

// AnalyzeForEngine adds dialect checks to the ordinary statement analysis.
// SQL Server permits adjacent commands without semicolons, so a second
// top-level command is blocked even when the generic splitter sees one part.
func AnalyzeForEngine(engine, sql string) Analysis {
	if _, err := sqllex.ScanForEngine(engine, sql); err != nil {
		return Analysis{Raw: sql, StatementClass: sqlclass.ClassUnknown, Statements: []StatementAnalysis{}, BlockedByDefault: true}
	}
	result := analyze(sql, engine)
	if engine == "sqlserver" && hasAdjacentSQLServerCommand(sql) {
		result.MultiStatement = true
		result.BlockedByDefault = true
	}
	return result
}

func hasAdjacentSQLServerCommand(sql string) bool {
	words := sqlServerTopLevelWords(sql)
	command := func(word string) bool {
		switch word {
		case "select", "insert", "update", "delete", "merge", "create", "alter", "drop", "truncate", "grant", "deny", "revoke", "exec", "execute", "use", "declare", "backup", "restore", "dbcc", "waitfor", "set", "if", "while", "begin", "commit", "rollback", "save", "print", "raiserror", "throw", "bulk", "enable", "disable", "kill", "shutdown", "reconfigure", "checkpoint", "revert", "break", "continue", "goto", "return", "readtext", "writetext", "updatetext", "send", "receive", "open", "close", "deallocate":
			return true
		}
		return false
	}
	first := -1
	for i, word := range words {
		if command(word) {
			first = i
			break
		}
	}
	if first < 0 {
		return false
	}
	initial := words[first]
	allowedSelect := false
	allowedSet := 0
	if initial == "update" {
		allowedSet = 1
	}
	for i := first + 1; i < len(words); i++ {
		word := words[i]
		if !command(word) {
			continue
		}
		prev := words[i-1]
		if word == "set" && allowedSet > 0 {
			allowedSet--
			continue
		}
		if word == "select" {
			if prev == "union" || prev == "except" || prev == "intersect" || prev == "all" && i > 1 && words[i-2] == "union" {
				continue
			}
			if initial == "insert" && !allowedSelect && !containsWord(words[first:i], "values") {
				allowedSelect = true
				continue
			}
			if (initial == "create" || initial == "alter") && !allowedSelect && prev == "as" && containsWord(words[first:i], "view") {
				allowedSelect = true
				continue
			}
		}
		if initial == "merge" && prev == "then" && (word == "update" || word == "insert" || word == "delete") {
			if word == "update" {
				allowedSet++
			}
			continue
		}
		return true
	}
	return false
}

func containsWord(words []string, target string) bool {
	for _, word := range words {
		if word == target {
			return true
		}
	}
	return false
}

func sqlServerTopLevelWords(sql string) []string {
	tokens, err := sqllex.Scan(sql)
	if err != nil {
		return nil
	}
	var words []string
	for _, t := range tokens {
		if t.Kind == sqllex.Word && t.Depth == 0 {
			words = append(words, strings.ToLower(t.Text))
		}
	}
	return words
}

func analyzeStatement(sql string, tokens []sqllex.Token, engine string) StatementAnalysis {
	item := StatementAnalysis{SQL: sql, Class: sqlclass.Classify(sql)}
	outer, _, _, ok := sqllex.With(tokens)
	if !ok || len(outer) == 0 {
		return item
	}
	if outer[0].Kind == sqllex.Word {
		item.Action = strings.ToLower(outer[0].Text)
	}
	item.HasWhere = sqllex.HasTop(outer, "where")
	item.HasLimit = sqllex.HasTop(outer, "limit")
	item.UnsafeWrite = (item.Action == "update" || item.Action == "delete") && !item.HasWhere
	item.Objects = extractObjects(tokens, engine)
	if len(item.Objects) > 0 {
		item.Object = item.Objects[0]
	}
	return item
}

func extractObjects(tokens []sqllex.Token, engine string) []string {
	objects := map[string]struct{}{}
	var walk func([]sqllex.Token, map[string]bool, bool)
	walk = func(tokens []sqllex.Token, visible map[string]bool, relation bool) {
		if len(tokens) == 0 {
			return
		}
		if tokens[0].Is("explain") {
			for i, t := range tokens[1:] {
				if t.Depth == tokens[0].Depth && (t.Is("select") || t.Is("with") || t.Is("values")) {
					walk(tokens[i+1:], visible, false)
					return
				}
			}
		}
		outer, ctes, recursive, ok := sqllex.With(tokens)
		if !ok {
			return
		}
		names := make(map[string]bool, len(visible)+len(ctes))
		for name := range visible {
			names[name] = true
		}
		if recursive {
			for _, cte := range ctes {
				names[strings.ToLower(cte.Name)] = true
			}
		}
		for _, cte := range ctes {
			body := cte.Body
			// SQLite and SQL Server allow recursive CTEs without RECURSIVE.
			// Only the UNION tail may refer to this CTE: the anchor still has
			// ordinary table lookup, so a physical namesake cannot be hidden.
			if !recursive && (engine == "sqlite" || engine == "sqlserver") {
				for i, t := range body {
					if t.Depth == body[0].Depth && t.Is("union") {
						walk(body[:i], names, false)
						self := make(map[string]bool, len(names)+1)
						for name := range names {
							self[name] = true
						}
						self[strings.ToLower(cte.Name)] = true
						walk(body[i+1:], self, false)
						body = nil
						break
					}
				}
			}
			walk(body, names, false)
			names[strings.ToLower(cte.Name)] = true
		}
		tokens = outer
		if len(tokens) == 0 {
			return
		}
		action := strings.ToLower(tokens[0].Text)
		// Parentheses in a FROM/JOIN/USING position may group relations,
		// not just SELECT subqueries. Preserve that context through nested groups.
		grouped := relation && !tokens[0].Is("select") && !tokens[0].Is("values") && len(ctes) == 0
		expect, inList := grouped, grouped
		for i := 0; i < len(tokens); i++ {
			t := tokens[i]
			// SQL Server places TOP (expression) [PERCENT] before MERGE's
			// optional INTO and target. Its argument is an expression, not a table.
			if action == "merge" && i == 1 && t.Is("top") {
				if end := sqllex.Matching(tokens, i+1); end >= 0 {
					walk(tokens[i+2:end], names, false)
					i = end
					if i+1 < len(tokens) && tokens[i+1].Is("percent") {
						i++
					}
					expect = true
					continue
				}
			}
			if t.Kind == sqllex.Symbol && t.Text == "(" {
				end := sqllex.Matching(tokens, i)
				if end < 0 {
					return
				}
				walk(tokens[i+1:end], names, expect)
				i = end
				expect = false
				continue
			}
			if t.Kind == sqllex.Symbol && t.Text == "," {
				if inList {
					expect = true
				}
				continue
			}
			if t.Kind == sqllex.Word {
				switch strings.ToLower(t.Text) {
				case "from", "join", "into":
					expect = true
					inList = true
					continue
				case "using":
					if action == "delete" || action == "merge" {
						expect = true
						inList = true
					} else {
						expect = false
						inList = false
					}
					continue
				case "update", "merge":
					if i == 0 {
						expect = true
						inList = false
					}
					continue
				case "truncate":
					expect = true
					inList = false
					continue
				case "table", "view":
					if action == "create" || action == "alter" || action == "drop" || action == "rename" || action == "truncate" {
						expect = true
						inList = true
					}
					continue
				case "on":
					expect = action == "create" && sqllex.HasTop(tokens, "index")
					inList = false
					continue
				case "where", "group", "order", "limit", "having", "set", "values", "returning", "union", "except", "intersect", "when":
					expect = false
					inList = false
					continue
				case "if", "not", "exists", "only", "lateral", "temp", "temporary":
					if expect {
						continue
					}
				}
			}
			// SQLite also accepts single-quoted names in relation positions.
			// Literals elsewhere remain values and never become table references.
			if expect && (t.Ident() || t.Kind == sqllex.Literal) {
				name := t.Text
				for i+2 < len(tokens) && tokens[i+1].Kind == sqllex.Symbol && tokens[i+1].Text == "." && (tokens[i+2].Ident() || tokens[i+2].Kind == sqllex.Literal) {
					name += "." + tokens[i+2].Text
					i += 2
				}
				if !names[strings.ToLower(name)] {
					objects[name] = struct{}{}
				}
				expect = false
			}
		}
	}
	walk(tokens, nil, false)
	var out []string
	for object := range objects {
		out = append(out, object)
	}
	sort.Strings(out)
	return out
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
