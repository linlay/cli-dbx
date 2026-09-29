package conn

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/config"
	"github.com/linlay/cli-dbx/internal/odbcdriver"
)

func TestMySQLUsesConfiguredEndpoint(t *testing.T) {
	t.Setenv("DBX_DRIVER_DIR", filepath.Join(t.TempDir(), "missing"))
	for _, tc := range []struct{ engine, fields, address string }{
		{"mysql", "port=2881", "localhost:2881"},
		{"mysql", "", "localhost:3306"},
		{"mysql", "port=2883", "localhost:2883"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			dir := writeConfig(t, "test", "[connection]\nengine=\""+tc.engine+"\"\nhost=\"localhost\"\nuser=\"app@tenant#cluster\"\ndatabase=\"finance\"\nallow_actions=[\"query\"]\n"+tc.fields+"\n")
			spec, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "test", Action: action.Query})
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := mysql.ParseDSN(spec.DSN)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Driver != "mysql" || parsed.Addr != tc.address || parsed.User != "app@tenant#cluster" || parsed.DBName != "finance" || !parsed.ParseTime {
				t.Fatalf("unexpected connection: driver=%s config=%+v", spec.Driver, parsed)
			}
			if !spec.AllowsAction(action.Query) || spec.AllowsAction(action.Update) {
				t.Fatal("action policy changed")
			}
		})
	}
}

func TestMySQLAcceptsTenantDSN(t *testing.T) {
	dsn := "app@tenant:p@ss@tcp(localhost:2883)/finance?parseTime=true&tls=skip-verify"
	spec, err := Resolve(context.Background(), ResolveInput{Engine: "mysql", DSN: dsn, Action: action.Query})
	if err != nil || spec.Driver != "mysql" || spec.Engine != "mysql" || spec.DSN != dsn {
		t.Fatalf("unexpected MySQL DSN resolution: driver=%s error=%v", spec.Driver, err)
	}
}

func TestResolveOptionalEngineReturnsMissingDriver(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "native drivers")
	t.Setenv("DBX_DRIVER_DIR", dir)
	configs := writeConfig(t, "finance", `[connection]
engine="oracle"
driver="odbc"
host="localhost"
user="app"
database="ORCLPDB1"
allow_actions=["query"]
`)
	_, err := Resolve(context.Background(), ResolveInput{ConfigPath: configs, Name: "finance", Action: action.Query})
	var missing *odbcdriver.MissingError
	if !errors.As(err, &missing) || missing.DriverDir != dir || missing.Engine != "oracle" || missing.MarketPackage != "oracle-odbc" {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestODBCConnectionStringDialectsAndEscaping(t *testing.T) {
	for _, tc := range []struct{ engine, want string }{
		{"oracle", "DBQ={//localhost:1521/finance};"},
		{"dm", "TCP_PORT={5236};"},
		{"sqlserver", "SERVER={tcp:localhost,1433};"},
		{"mysql", "PORT={3306};"},
		{"postgres", "PORT={5432};"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			c := config.ConnectionConfig{Host: "localhost", User: "app", Database: "finance", Schema: "APP"}
			dsn, err := odbcConnectionString(c, tc.engine, "p};UID=other;{", "vendor driver")
			if err != nil || !strings.Contains(dsn, tc.want) || !strings.Contains(dsn, "PWD={p}};UID=other;{};") || !strings.Contains(dsn, "DRIVER={vendor driver};") {
				t.Fatalf("unexpected DSN: %q, %v", dsn, err)
			}
		})
	}
}

func TestODBCAdditionalOptions(t *testing.T) {
	c := config.ConnectionConfig{Host: "db.example", User: "app", Database: "finance", ODBCOptions: map[string]string{"Encrypt": "yes"}}
	dsn, err := odbcConnectionString(c, "sqlserver", "secret", "vendor")
	if err != nil || !strings.Contains(dsn, "ENCRYPT={yes};") || !strings.Contains(dsn, "DATABASE={finance};") {
		t.Fatalf("ODBC connection attributes: %v", err)
	}
}

func TestODBCRequiresDatabaseEngine(t *testing.T) {
	t.Setenv("DBX_DRIVER_DIR", t.TempDir())
	dir := writeConfig(t, "invalid", "[connection]\ndriver=\"odbc\"\nallow_actions=[\"query\"]\n")
	_, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "invalid"})
	if err == nil || !strings.Contains(err.Error(), "engine") {
		t.Errorf("ODBC connection without an engine must fail, got %v", err)
	}
}

func TestSQLiteODBCDisplayTargetDoesNotExposeDSN(t *testing.T) {
	const password = "synthetic-password-marker"
	spec, err := finalize(Spec{
		Engine: "sqlite", Driver: "odbc", AllowActions: []action.Action{action.Query},
		DSN: "DRIVER={vendor};DATABASE={};PWD={" + password + "};",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(spec.DisplayTarget, password) || strings.Contains(spec.DisplayTarget, "PWD=") {
		t.Fatalf("display target exposed the ODBC connection string")
	}
}
func TestODBCOptionsCannotChangeDriverOrCredentials(t *testing.T) {
	for _, key := range []string{"Driver", "DSN", "FILEDSN", "SAVEFILE", "pwd", "Password", "UID", "Server", "bad;key"} {
		c := config.ConnectionConfig{Host: "localhost", ODBCOptions: map[string]string{key: "untrusted"}}
		if _, err := odbcConnectionString(c, "sqlserver", "secret", "vendor"); err == nil {
			t.Fatalf("accepted %s override", key)
		}
	}
}
func TestODBCRejectsRawDSNBypass(t *testing.T) {
	for _, engine := range []string{"oracle", "dm", "sqlserver"} {
		_, err := Resolve(context.Background(), ResolveInput{DSN: "DRIVER={elsewhere};PWD=private", Engine: engine, Action: action.Query})
		if err == nil || !strings.Contains(err.Error(), "named connection") || strings.Contains(err.Error(), "private") {
			t.Fatalf("unexpected bypass handling: %v", err)
		}
	}
}
