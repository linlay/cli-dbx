package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/conn"
)

func TestSQLiteLiteralFilePathsCreateAndReopenOnlyRequestedFile(t *testing.T) {
	names := []string{"hash#tail.db", "percent%23.db", "percent%20.db", "percent%.db", "中文 数据.db", "apostrophe'name.db"}
	if runtime.GOOS != "windows" {
		names = append(names, `literal\name#.db`)
	}
	for _, name := range names {
		t.Run(strings.NewReplacer("#", "hash", "%", "percent").Replace(name), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			spec := resolveSQLitePath(t, path)
			connection := openSQLiteSpec(t, spec)
			if _, err := connection.Exec("CREATE TABLE marker (value TEXT NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			if _, err := connection.Exec("INSERT INTO marker VALUES ('requested file')"); err != nil {
				t.Fatal(err)
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			assertSQLiteOnlyFile(t, dir, name)

			// The bare filename bypasses DBX's URI construction and independently
			// verifies the literal filesystem target contains the persisted data.
			reopened := openSQLiteSpec(t, conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: path})
			var value string
			if err := reopened.QueryRow("SELECT value FROM marker").Scan(&value); err != nil {
				t.Fatalf("reopen literal path: %v", err)
			}
			if value != "requested file" {
				t.Fatalf("persisted value = %q", value)
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
			assertSQLiteOnlyFile(t, dir, name)
		})
	}
}

func TestSQLitePathModeReadOnlyRejectsWrites(t *testing.T) {
	for _, name := range []string{"ordinary.db", "只读 #%23.db"} {
		t.Run(strings.NewReplacer("#", "hash", "%", "percent").Replace(name), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			writable := openSQLiteSpec(t, conn.Spec{Engine: "sqlite", Driver: "sqlite", DSN: path})
			if _, err := writable.Exec("CREATE TABLE marker (value TEXT)"); err != nil {
				t.Fatal(err)
			}
			if err := writable.Close(); err != nil {
				t.Fatal(err)
			}

			readOnly := openSQLiteSpec(t, resolveSQLitePath(t, path+"?mode=ro"))
			if err := readOnly.Ping(); err != nil {
				t.Fatalf("open read-only requested file: %v", err)
			}
			if _, err := readOnly.Exec("INSERT INTO marker VALUES ('forbidden')"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "readonly") {
				t.Fatalf("write to mode=ro must fail as readonly, got %v", err)
			}
			if err := readOnly.Close(); err != nil {
				t.Fatal(err)
			}
			assertSQLiteOnlyFile(t, dir, name)
		})
	}
}

func TestSQLiteLeadingDoubleSlashPathIsAFileNotURIAuthority(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows UNC paths require a live network share")
	}
	dir := t.TempDir()
	path := "/" + filepath.Join(dir, "literal#.db")
	connection := openSQLiteSpec(t, resolveSQLitePath(t, path))
	if _, err := connection.Exec("CREATE TABLE marker (value INTEGER)"); err != nil {
		t.Fatalf("open leading double slash filesystem path: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteOnlyFile(t, dir, "literal#.db")
}

func TestSQLitePathMemoryModeSharesCacheWithoutCreatingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "内存 #%23.db")
	spec := resolveSQLitePath(t, path+"?mode=memory&cache=shared")
	first := openSQLiteSpec(t, spec)
	if _, err := first.Exec("CREATE TABLE marker (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec("INSERT INTO marker VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	second := openSQLiteSpec(t, spec)
	var value int
	if err := second.QueryRow("SELECT value FROM marker").Scan(&value); err != nil {
		t.Fatalf("shared memory query: %v", err)
	}
	if value != 42 {
		t.Fatalf("shared memory value = %d", value)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("mode=memory created disk entries: %v", entries)
	}
	third := openSQLiteSpec(t, spec)
	if err := third.QueryRow("SELECT value FROM marker").Scan(&value); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("memory database survived closing all connections: %v", err)
	}
}

func TestSQLitePathPragmasAreApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "参数 #%20.db")
	spec := resolveSQLitePath(t, path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout%3d4321")
	connection := openSQLiteSpec(t, spec)
	var foreignKeys, busyTimeout int
	if err := connection.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || busyTimeout != 4321 {
		t.Fatalf("pragmas: foreign_keys=%d busy_timeout=%d", foreignKeys, busyTimeout)
	}
}

func TestSQLiteExplicitFileURIAndMemoryKeepSemantics(t *testing.T) {
	for _, path := range []string{":memory:", "file::memory:?cache=shared&_pragma=foreign_keys(1)"} {
		t.Run(path, func(t *testing.T) {
			connection := openSQLiteSpec(t, resolveSQLitePath(t, path))
			if _, err := connection.Exec("CREATE TABLE marker (value INTEGER)"); err != nil {
				t.Fatal(err)
			}
		})
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "literal#.db")
	encodedPath := strings.ReplaceAll(filepath.ToSlash(path), "#", "%23")
	if filepath.VolumeName(path) != "" {
		encodedPath = "/" + encodedPath
	}
	spec := resolveSQLitePath(t, "file:"+encodedPath+"?mode=rwc&_pragma=foreign_keys(1)")
	connection := openSQLiteSpec(t, spec)
	if _, err := connection.Exec("CREATE TABLE marker (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteOnlyFile(t, dir, "literal#.db")
}

func TestSQLiteMemoryURIFormsShareCache(t *testing.T) {
	for _, path := range []string{":memory:?cache=shared", "file::memory:?cache=shared"} {
		t.Run(path, func(t *testing.T) {
			spec := resolveSQLitePath(t, path)
			first := openSQLiteSpec(t, spec)
			if _, err := first.Exec("CREATE TABLE marker (value INTEGER)"); err != nil {
				t.Fatal(err)
			}
			if _, err := first.Exec("INSERT INTO marker VALUES (42)"); err != nil {
				t.Fatal(err)
			}
			second := openSQLiteSpec(t, spec)
			var value int
			if err := second.QueryRow("SELECT value FROM marker").Scan(&value); err != nil {
				t.Fatalf("shared memory query: %v", err)
			}
			if value != 42 {
				t.Fatalf("shared memory value = %d", value)
			}
			var sequence int
			var name, file string
			if err := second.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &file); err != nil {
				t.Fatal(err)
			}
			if file != "" {
				t.Fatalf("memory database has disk target %q", file)
			}
		})
	}
}

func resolveSQLitePath(t *testing.T, path string) conn.Spec {
	t.Helper()
	configDir := t.TempDir()
	raw := fmt.Sprintf("[connection]\nengine = 'sqlite'\npath = %q\nallow_actions = ['query', 'update', 'schema']\n", path)
	if err := os.WriteFile(filepath.Join(configDir, "sqlite.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := conn.Resolve(context.Background(), conn.ResolveInput{ConfigPath: configDir, Name: "sqlite", Action: action.Query})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func openSQLiteSpec(t *testing.T, spec conn.Spec) *sql.DB {
	t.Helper()
	connection, err := Open(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	return connection
}

func assertSQLiteOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name || entries[0].IsDir() {
		t.Fatalf("requested file %q; directory contains %v", name, entries)
	}
}
