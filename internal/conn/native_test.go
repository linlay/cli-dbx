package conn

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/odbcdriver"
	"github.com/microsoft/go-mssqldb/msdsn"
)

func TestNativeDriversDoNotNeedODBCFiles(t *testing.T) {
	t.Setenv("DBX_DRIVER_DIR", filepath.Join(t.TempDir(), "missing"))
	for _, tc := range []struct{ engine, driver string }{
		{"mysql", "mysql"}, {"postgres", "pgx"}, {"sqlite", "sqlite"},
		{"oracle", "oracledb"}, {"dm", "dm"}, {"sqlserver", "sqlserver"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			var defaultDSN string
			for _, selection := range []string{"", "native"} {
				dir := writeConfig(t, "native", "[connection]\nengine=\""+tc.engine+"\"\ndriver=\""+selection+"\"\nhost=\"localhost\"\nuser=\"app\"\ndatabase=\"finance\"\nsslmode=\"require\"\nallow_actions=[\"query\"]\n")
				spec, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "native", Action: action.Query})
				if err != nil {
					t.Fatal(err)
				}
				if spec.Driver != tc.driver || !spec.AllowsAction(action.Query) || spec.AllowsAction(action.Update) {
					t.Fatalf("wrong driver or action policy: driver=%s actions=%v", spec.Driver, spec.AllowActions)
				}
				if selection == "" {
					defaultDSN = spec.DSN
				} else if spec.DSN != defaultDSN {
					t.Fatal("explicit native selection changed the DSN")
				}
				switch tc.engine {
				case "mysql":
					if spec.DSN != "app:@tcp(localhost:3306)/finance?parseTime=true" {
						t.Fatalf("MySQL DSN changed: %s", spec.DSN)
					}
				case "postgres":
					if spec.DSN != "postgres://app:@localhost:5432/finance?sslmode=require" {
						t.Fatalf("PostgreSQL DSN changed: %s", spec.DSN)
					}
				case "sqlite":
					if spec.DSN != "file::memory:" {
						t.Fatalf("SQLite DSN changed: %s", spec.DSN)
					}
				}
			}
		})
	}
}

func TestExplicitODBCSelection(t *testing.T) {
	t.Setenv("DBX_DRIVER_DIR", filepath.Join(t.TempDir(), "missing"))
	for _, engine := range []string{"oracle", "dm", "sqlserver", "mysql", "postgres", "sqlite"} {
		t.Run(engine, func(t *testing.T) {
			dir := writeConfig(t, "fallback", "[connection]\nengine=\""+engine+"\"\ndriver=\"odbc\"\nodbc_driver=\"vendor.so\"\nhost=\"localhost\"\nallow_actions=[\"query\"]\n")
			_, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "fallback", Action: action.Query})
			// File extension differs by OS; either discovery failure is acceptable,
			// but silently resolving to a built-in driver is not.
			var missing *odbcdriver.MissingError
			if !errors.As(err, &missing) && (err == nil || !strings.Contains(err.Error(), "native ODBC library")) {
				t.Fatalf("expected ODBC discovery, got %v", err)
			}
		})
	}
}

func TestRejectUnknownDriverSelection(t *testing.T) {
	dir := writeConfig(t, "bad", "[connection]\nengine=\"mysql\"\ndriver=\"typo\"\nhost=\"localhost\"\nallow_actions=[\"query\"]\n")
	if _, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "bad", Action: action.Query}); err == nil {
		t.Fatal("unknown driver must not silently select a native driver")
	}
}

func TestNativeDSNCredentialsRoundTrip(t *testing.T) {
	password := "p@ss:/?#%&+ 中文"
	for _, engine := range []string{"oracle", "sqlserver"} {
		spec := Spec{Engine: engine, Host: "::1", User: "app", Database: "finance"}
		dsn, err := buildNativeDSN(spec, password, "")
		if err != nil {
			t.Fatal(err)
		}
		if engine == "sqlserver" {
			parsed, err := msdsn.Parse(dsn)
			if err != nil || parsed.Password != password || parsed.Database != "finance" || parsed.Host != "::1" {
				t.Fatal("SQL Server driver did not preserve connection fields")
			}
		} else {
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := parsed.User.Password()
			if got != password || parsed.Hostname() != "::1" || parsed.Path != "/finance" {
				t.Fatal("Oracle connector input did not preserve connection fields")
			}
		}
	}
}

func TestNativeDoesNotSilentlyIgnoreODBCOptions(t *testing.T) {
	dir := writeConfig(t, "bad", "[connection]\nengine=\"sqlserver\"\nhost=\"localhost\"\nallow_actions=[\"query\"]\n[connection.odbc_options]\nEncrypt=\"yes\"\n")
	if _, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "bad", Action: action.Query}); err == nil || !strings.Contains(err.Error(), "driver = odbc") {
		t.Fatalf("ODBC-only settings must not be silently ignored: %v", err)
	}
}

func TestBuiltinTemplatesSelectStandardEngines(t *testing.T) {
	for _, tc := range []struct{ file, engine, driver string }{
		{"config.mysql", "mysql", "mysql"},
		{"config.postgres", "postgres", "pgx"},
		{"config.sqlite", "sqlite", "sqlite"},
		{"config.oracle", "oracle", "oracledb"},
		{"config.dm", "dm", "dm"},
		{"config.sqlserver", "sqlserver", "sqlserver"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			spec, err := Resolve(context.Background(), ResolveInput{ConfigPath: filepath.Join("..", "..", "examples", tc.file+".toml"), Name: tc.file, Action: action.Query})
			if err != nil {
				t.Fatal(err)
			}
			if spec.Engine != tc.engine || spec.Driver != tc.driver {
				t.Fatalf("template selected %s/%s, want %s/%s", spec.Engine, spec.Driver, tc.engine, tc.driver)
			}
		})
	}
}

func TestDatabaseBrandsAreNotEngineAliases(t *testing.T) {
	for _, engine := range []string{"oceanbase", "oceanbase-mysql", "oceanbase_mysql", "oceanbase-oracle", "oceanbase_oracle", "tdsql"} {
		t.Run(engine, func(t *testing.T) {
			dir := writeConfig(t, "brand", "[connection]\nengine=\""+engine+"\"\nhost=\"localhost\"\nallow_actions=[\"query\"]\n")
			if _, err := Resolve(context.Background(), ResolveInput{ConfigPath: dir, Name: "brand"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("brand must not select a protocol: %v", err)
			}
		})
	}
}
