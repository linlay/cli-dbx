//go:build dbmod && !dbmod_testfixture

package postgres

import _ "github.com/jackc/pgx/v5/stdlib"

func Initialize() error { return nil }
