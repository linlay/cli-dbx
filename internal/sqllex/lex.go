// Package sqllex supplies the shared, conservative lexical boundary used by SQL policy checks.
package sqllex

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Kind uint8

const (
	Word Kind = iota
	Identifier
	Literal
	Symbol
)

type Token struct {
	Text              string
	Kind              Kind
	Start, End, Depth int
}

func (t Token) Is(word string) bool { return t.Kind == Word && strings.EqualFold(t.Text, word) }
func (t Token) Ident() bool         { return t.Kind == Word || t.Kind == Identifier }

// Scan rejects incomplete or dialect-dependent constructs instead of hiding executable text.
// Ordinary SQL doubled delimiters, SQL Server brackets and PostgreSQL dollar/E strings are supported.
func Scan(sql string) ([]Token, error) { return ScanForEngine("", sql) }

// ScanForEngine rejects quoting/comment syntax whose meaning differs in the target engine.
// The empty engine preserves the generic scanner's union of supported dialect syntax.
func ScanForEngine(engine, sql string) ([]Token, error) {
	var out []Token
	depth := 0
	for i := 0; i < len(sql); {
		r, n := utf8.DecodeRuneInString(sql[i:])
		if unicode.IsSpace(r) {
			i += n
			continue
		}
		start := i
		if engine == "mysql" && sql[i] == '#' {
			return nil, fmt.Errorf("hash comment is unsupported")
		}
		if engine == "oracle" || engine == "dm" {
			if i+1 < len(sql) && strings.EqualFold(sql[i:i+2], "q'") || i+2 < len(sql) && strings.EqualFold(sql[i:i+3], "nq'") {
				return nil, fmt.Errorf("alternative quote is unsupported")
			}
		}
		if strings.HasPrefix(sql[i:], "--") {
			if engine == "mysql" && i+2 < len(sql) && sql[i+2] > ' ' {
				return nil, fmt.Errorf("ambiguous line comment")
			}
			for i < len(sql) && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
			continue
		}
		if strings.HasPrefix(sql[i:], "/*") {
			i += 2
			levels := 1
			for i < len(sql) && levels > 0 {
				if strings.HasPrefix(sql[i:], "/*") {
					if engine != "" && engine != "postgres" && engine != "sqlserver" {
						return nil, fmt.Errorf("nested comment is unsupported for engine")
					}
					if strings.HasPrefix(sql[i:], "/*!") || strings.HasPrefix(sql[i:], "/*M!") {
						return nil, fmt.Errorf("executable comment is unsupported")
					}
					levels++
					i += 2
					continue
				}
				if strings.HasPrefix(sql[i:], "*/") {
					levels--
					i += 2
					continue
				}
				// MySQL and MariaDB executable comments cannot be treated as whitespace.
				if i == start+2 && (sql[i] == '!' || strings.HasPrefix(sql[i:], "M!")) {
					return nil, fmt.Errorf("executable comment is unsupported")
				}
				i++
			}
			if levels != 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			continue
		}
		escape := false
		if (sql[i] == 'e' || sql[i] == 'E') && i+1 < len(sql) && sql[i+1] == '\'' {
			if engine != "" && engine != "postgres" {
				return nil, fmt.Errorf("escape string is unsupported for engine")
			}
			escape = true
			i++
		}
		if sql[i] == '\'' || sql[i] == '"' || sql[i] == '`' || sql[i] == '[' {
			delim := sql[i]
			if delim == '[' && engine != "" && engine != "sqlserver" && engine != "sqlite" {
				return nil, fmt.Errorf("bracket expression is unsupported for engine")
			}
			end := delim
			kind := Identifier
			if delim == '\'' {
				kind = Literal
			}
			if delim == '[' {
				end = ']'
			}
			i++
			var value strings.Builder
			closed := false
			for i < len(sql) {
				ch := sql[i]
				if ch == '\\' && delim != '[' {
					if !escape {
						return nil, fmt.Errorf("ambiguous backslash quoting")
					}
					if i+1 >= len(sql) {
						return nil, fmt.Errorf("unterminated escape")
					}
					value.WriteByte(sql[i+1])
					i += 2
					continue
				}
				if ch == end {
					if i+1 < len(sql) && sql[i+1] == end {
						value.WriteByte(end)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				value.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quote")
			}
			out = append(out, Token{value.String(), kind, start, i, depth})
			continue
		}
		if sql[i] == '$' {
			j := i + 1
			for j < len(sql) && ((sql[j] >= 'a' && sql[j] <= 'z') || (sql[j] >= 'A' && sql[j] <= 'Z') || sql[j] == '_' || (j > i+1 && sql[j] >= '0' && sql[j] <= '9')) {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				if engine != "" && engine != "postgres" {
					return nil, fmt.Errorf("dollar quote is unsupported for engine")
				}
				tag := sql[i : j+1]
				k := strings.Index(sql[j+1:], tag)
				if k < 0 {
					return nil, fmt.Errorf("unterminated dollar quote")
				}
				i = j + 1 + k + len(tag)
				out = append(out, Token{sql[j+1 : j+1+k], Literal, start, i, depth})
				continue
			}
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '#' || r == '@' {
			i += n
			for i < len(sql) {
				r, n = utf8.DecodeRuneInString(sql[i:])
				if engine == "mysql" && r == '#' {
					break
				}
				if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '#' || r == '@') {
					break
				}
				i += n
			}
			out = append(out, Token{sql[start:i], Word, start, i, depth})
			continue
		}
		if sql[i] == ')' {
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced parentheses")
			}
		}
		out = append(out, Token{sql[i : i+1], Symbol, start, i + 1, depth})
		if sql[i] == '(' {
			depth++
		}
		i++
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parentheses")
	}
	return out, nil
}

func Matching(tokens []Token, start int) int {
	if start >= len(tokens) || tokens[start].Kind != Symbol || tokens[start].Text != "(" {
		return -1
	}
	for i := start + 1; i < len(tokens); i++ {
		if tokens[i].Kind == Symbol && tokens[i].Text == ")" && tokens[i].Depth == tokens[start].Depth {
			return i
		}
	}
	return -1
}
func First(tokens []Token) []Token {
	for i, t := range tokens {
		if t.Kind == Symbol && t.Text == ";" {
			return tokens[:i]
		}
	}
	return tokens
}
func HasTop(tokens []Token, word string) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if t.Depth == tokens[0].Depth && t.Is(word) {
			return true
		}
	}
	return false
}

type CTE struct {
	Name string
	Body []Token
}

// With parses only the CTE envelope. The caller must validate each body's action.
func With(tokens []Token) (outer []Token, ctes []CTE, recursive bool, ok bool) {
	if len(tokens) == 0 || !tokens[0].Is("with") {
		return tokens, nil, false, true
	}
	i := 1
	if i < len(tokens) && tokens[i].Is("recursive") {
		recursive = true
		i++
	}
	for i < len(tokens) {
		if !tokens[i].Ident() {
			return nil, nil, false, false
		}
		name := tokens[i].Text
		i++
		if i < len(tokens) && tokens[i].Kind == Symbol && tokens[i].Text == "(" {
			end := Matching(tokens, i)
			if end < 0 {
				return nil, nil, false, false
			}
			i = end + 1
		}
		if i >= len(tokens) || !tokens[i].Is("as") {
			return nil, nil, false, false
		}
		i++
		if i < len(tokens) && tokens[i].Is("not") {
			i++
			if i >= len(tokens) || !tokens[i].Is("materialized") {
				return nil, nil, false, false
			}
		}
		if i < len(tokens) && tokens[i].Is("materialized") {
			i++
		}
		end := Matching(tokens, i)
		if end < 0 || end == i+1 {
			return nil, nil, false, false
		}
		ctes = append(ctes, CTE{name, tokens[i+1 : end]})
		i = end + 1
		if i < len(tokens) && tokens[i].Kind == Symbol && tokens[i].Text == "," {
			i++
			continue
		}
		if i >= len(tokens) {
			return nil, nil, false, false
		}
		return tokens[i:], ctes, recursive, true
	}
	return nil, nil, false, false
}
