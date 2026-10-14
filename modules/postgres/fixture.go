//go:build dbmod && dbmod_testfixture

package postgres

import "github.com/linlay/cli-dbx/internal/conn"

// A build-time test fixture. Release builds never select this file. The host's
// built-in SQLite exercises real SQL through the complete module worker path.
func Initialize() error { return nil }

func Prepare(spec *conn.Spec) error {
	spec.Engine, spec.Driver, spec.DSN = "sqlite", "sqlite", spec.Database
	return nil
}
