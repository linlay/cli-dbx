//go:build dbmod && dbmod_testfixture

package postgres

import (
	"fmt"
	"github.com/linlay/cli-dbx/internal/conn"
	"os"
)

// A build-time test fixture. Release builds never select this file. The host's
// built-in SQLite exercises real SQL through the complete module loading path.
func Initialize() error {
	trace("initialize")
	if os.Getenv("DBX_TEST_MODULE_BEHAVIOR") == "block_initialize" {
		select {}
	}
	return nil
}

func Prepare(spec *conn.Spec) error {
	trace("prepare")
	switch os.Getenv("DBX_TEST_MODULE_BEHAVIOR") {
	case "block_prepare", "interrupt_prepare":
		select {}
	case "panic_prepare":
		panic("fixture private credential must not escape")
	}
	spec.Engine, spec.Driver, spec.DSN = "sqlite", "sqlite", spec.Database
	return nil
}

func trace(event string) {
	if path := os.Getenv("DBX_TEST_MODULE_TRACE"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			defer file.Close()
			fmt.Fprintf(file, "%s %d\n", event, os.Getpid())
		}
	}
}
