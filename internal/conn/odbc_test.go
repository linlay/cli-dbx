package conn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/action"
)

func TestResolveODBCUsesPrivateSpecAndResolvedLibrary(t *testing.T) {
	dir := writeConfig(t, "remote", `[connection]
engine = "oracle"
driver = "odbc"
driver_path = "adapter"
odbc_driver = "vendor/libvendor.dylib"
host = "db.example"
user = "app"
database = "service"
password = "secret"
allow_actions = ["query"]
[connection.odbc_options]
Encrypt = "yes"
`)
	spec, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "remote", Action: action.Query})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Driver != "odbc" || spec.DSN != "" || spec.ODBC == nil || spec.ODBC.Password != "secret" || spec.ODBC.DriverPath != filepath.Join(dir, "vendor/libvendor.dylib") || spec.DriverPath != filepath.Join(dir, "adapter") {
		t.Fatalf("bad ODBC spec: %+v", spec)
	}
	dsn, err := BuildODBCDSN(spec, spec.ODBC.DriverPath)
	if err != nil || !strings.Contains(dsn, "PWD={secret};") || !strings.Contains(dsn, "DRIVER={"+spec.ODBC.DriverPath+"};") {
		t.Fatalf("dsn %q: %v", dsn, err)
	}
}

func TestResolveODBCRejectsUnsafeConfiguration(t *testing.T) {
	cases := []struct{ name, fields string }{
		{"raw dsn", `dsn = "oracle://app:secret@host/service"`},
		{"dsn env", `dsn_env = "SECRET_DSN"`},
		{"missing library", `odbc_driver = ""`},
		{"credential override", `odbc_driver = "vendor.so"
[connection.odbc_options]
PWD = "leak"`},
		{"duplicate normalized", `odbc_driver = "vendor.so"
[connection.odbc_options]
foo = "one"
FOO = "two"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeConfig(t, "remote", `[connection]
engine = "oracle"
driver = "odbc"
driver_path = "adapter"
host = "db.example"
database = "service"
allow_actions = ["query"]
`+tc.fields)
			_, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "remote", Action: action.Query})
			if err == nil || strings.Contains(err.Error(), "leak") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestResolveNativeRejectsODBCFields(t *testing.T) {
	dir := writeConfig(t, "remote", `[connection]
engine = "oracle"
odbc_driver = "vendor.so"
host = "db.example"
database = "service"
user = "app"
allow_actions = ["query"]`)
	if _, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "remote", Action: action.Query}); err == nil {
		t.Fatal("native accepted ODBC library")
	}
}

func TestBuildODBCDSNEscapesValuesAndOrdersOptions(t *testing.T) {
	spec := Spec{Engine: "sqlserver", Driver: "odbc", Host: "db.example", Database: "data}base", User: "app", ODBC: &ODBCConfig{Password: "p;ass}word", Options: map[string]string{"Zeta": "a;b", "Alpha": "x}y"}}}
	got, err := BuildODBCDSN(spec, "/vendor/lib.so")
	if err != nil {
		t.Fatal(err)
	}
	want := "ALPHA={x}}y};DATABASE={data}}base};DRIVER={/vendor/lib.so};PWD={p;ass}}word};SERVER={tcp:db.example,1433};UID={app};ZETA={a;b};"
	if got != want {
		t.Fatalf("dsn = %q, want %q", got, want)
	}
}

func TestBuildODBCDSNRejectsUnsafeOptionsWithoutEchoingValues(t *testing.T) {
	for _, options := range []map[string]string{
		{"PWD": "secret-value"},
		{"SERVER": "secret-value"},
		{"foo": "one", " FOO ": "secret-value"},
		{"bad;key": "secret-value"},
		{"okay": "nul\x00secret-value"},
	} {
		spec := Spec{Engine: "dm", Driver: "odbc", Host: "db", ODBC: &ODBCConfig{DriverPath: "/vendor/lib.so", Options: options}}
		_, err := BuildODBCDSN(spec, "/vendor/lib.so")
		if err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("unsafe options accepted or exposed: %v", err)
		}
	}
}

func TestBuildODBCDSNRejectsTargetAliasesAndStatementRetries(t *testing.T) {
	for _, key := range []string{"Address", "Addr", "Network Address", "RetryExec", " retryexec "} {
		t.Run(key, func(t *testing.T) {
			spec := Spec{Engine: "sqlserver", Driver: "odbc", Host: "approved-host", ODBC: &ODBCConfig{Options: map[string]string{key: "private-option-value"}}}
			_, err := BuildODBCDSN(spec, "/vendor/lib.so")
			if err == nil || strings.Contains(err.Error(), "private-option-value") {
				t.Fatalf("unsafe option accepted or leaked: %v", err)
			}
		})
	}
}
