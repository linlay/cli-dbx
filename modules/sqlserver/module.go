//go:build dbmod

package sqlserver

import _ "github.com/microsoft/go-mssqldb"

func Initialize() error { return nil }
