package action

import (
	"fmt"
	"slices"
	"strings"

	"github.com/linlay/cli-dbx/internal/sqlclass"
)

type Action string

const (
	Query  Action = "query"
	Update Action = "update"
	Schema Action = "schema"
	Admin  Action = "admin"
)

func Parse(raw string) (Action, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "query":
		return Query, nil
	case "update":
		return Update, nil
	case "schema":
		return Schema, nil
	case "admin":
		return Admin, nil
	default:
		return "", fmt.Errorf("unknown action %q", raw)
	}
}

func ParseList(raw []string) ([]Action, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]Action, 0, len(raw))
	for _, item := range raw {
		act, err := Parse(item)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, act) {
			out = append(out, act)
		}
	}
	return out, nil
}

func Strings(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, act := range actions {
		out = append(out, string(act))
	}
	return out
}

func FromClass(class sqlclass.StatementClass) Action {
	switch class {
	case sqlclass.ClassRead:
		return Query
	case sqlclass.ClassWriteData:
		return Update
	case sqlclass.ClassDDL:
		return Schema
	case sqlclass.ClassAdmin:
		return Admin
	default:
		return ""
	}
}

func Contains(actions []Action, target Action) bool {
	return slices.Contains(actions, target)
}
