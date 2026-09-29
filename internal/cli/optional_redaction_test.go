package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
)

func TestOptionalDSNsAreRedacted(t *testing.T) {
	for _, engine := range []string{"oracle", "dm", "sqlserver"} {
		for _, dsn := range []string{"sqlserver://host?user+id=app&password=secret", "dm://app:p@ss:/?#@host:5236?schema=APP", "app@tenant:secret@tcp(host:2881)/"} {
			if got := redactDSN(conn.Spec{Engine: engine, DSN: dsn}); got != "[redacted]" {
				t.Errorf("%s DSN must be fully redacted", engine)
			}
		}
	}
}

func TestOptionalErrorsRedactPasswords(t *testing.T) {
	for _, tc := range []struct{ engine, dsn, password string }{
		{"oracle", "oracle://app:p%40ss@host:1521/pdb", "p@ss"},
		{"dm", "dm://app:p@ss?/ #%&+@host:5236?connectTimeout=15000", "p@ss?/ #%&+"},
		{"dm", "dm://app:unused@host:5236?password=secret&schema=APP", "secret"},
		{"sqlserver", "sqlserver://host?user+id=app&password=p%40ss", "p@ss"},
		{"sqlserver", "server=host;user id=app;password=secret", "secret"},
		{"mysql", "app@tenant:p@ss@tcp(host:2881)/db", "p@ss"},
		{"mysql", "app@tenant:p@ss@tcp(host:2881)/", "p@ss"},
	} {
		spec := conn.Spec{Engine: tc.engine, DSN: tc.dsn}
		got := sanitizeError(errors.New("rejected credential "+tc.password), spec)
		if strings.Contains(got, tc.password) {
			t.Errorf("%s error leaked credential", tc.engine)
		}
	}
}

func TestODBCErrorRedaction(t *testing.T) {
	spec := conn.Spec{Engine: "sqlserver", Driver: "odbc", DSN: "DRIVER={vendor};PWD={private};"}
	got := sanitizeError(errors.New("native call failed, password private"), spec)
	if strings.Contains(got, "private") {
		t.Fatal("ODBC error leaked credentials")
	}
}
