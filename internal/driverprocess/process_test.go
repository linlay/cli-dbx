package driverprocess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/dbcore"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) {
	if os.Getenv("DBX_PROCESS_TEST_CHILD") == "1" {
		if path := os.Getenv("DBX_PROCESS_TEST_STUBBORN"); path != "" {
			var req Request
			if readFrame(os.Stdin, &req) != nil {
				os.Exit(1)
			}
			if os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
				os.Exit(1)
			}
			for {
				time.Sleep(time.Hour) // Deliberately ignore Context and stdin EOF.
			}
		}
		if os.Getenv("DBX_PROCESS_TEST_BAD_RESULT") == "1" {
			var req Request
			if readFrame(os.Stdin, &req) != nil {
				os.Exit(1)
			}
			req.Spec.Engine, req.Spec.Driver = "sqlite", "sqlite"
			if _, err := dispatch(context.Background(), req); err != nil {
				os.Exit(1)
			}
			_ = writeFrame(os.Stdout, frame{Version: ProtocolVersion, Engine: "postgres", Kind: "result"})
			os.Exit(0)
		}
		err := Serve(context.Background(), os.Stdin, os.Stdout, "postgres", func(s *conn.Spec) error {
			s.Engine, s.Driver = "sqlite", "sqlite"
			return nil
		})
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCommittedWriteWithMalformedResponseIsExplicitlyUncertain(t *testing.T) {
	spec := installTestDriver(t)
	t.Setenv("DBX_PROCESS_TEST_BAD_RESULT", "1")
	_, err := Call(context.Background(), spec, Request{Operation: "execute", SQL: "create table already_committed(id integer)"})
	if err == nil || !strings.Contains(err.Error(), "verify database state") {
		t.Fatalf("unclear write error: %v", err)
	}
	t.Setenv("DBX_PROCESS_TEST_BAD_RESULT", "")
	r, err := Call(context.Background(), spec, Request{Operation: "tables"})
	if err != nil || len(r.Tables) != 1 || r.Tables[0].Name != "already_committed" {
		t.Fatalf("test write was not committed: %+v %v", r, err)
	}
}

func TestServeRedactsCredentialsInErrors(t *testing.T) {
	password := "hidden:@/?% value"
	u := url.URL{Scheme: "postgres", Host: "localhost", User: url.UserPassword("app", password)}
	var in, out bytes.Buffer
	_ = writeFrame(&in, Request{Version: 1, Engine: "postgres", Spec: conn.Spec{Engine: "postgres", DSN: u.String()}})
	_ = Serve(context.Background(), &in, &out, "postgres", func(*conn.Spec) error {
		return errors.New("failed " + u.String() + " password " + password + " " + url.QueryEscape(password))
	})
	var result frame
	if err := readFrame(&out, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error == nil {
		t.Fatal("expected error")
	}
	for _, secret := range []string{u.String(), password, url.QueryEscape(password)} {
		if strings.Contains(result.Error.Message, secret) {
			t.Fatal("credential leaked in driver error")
		}
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:len(p)/2]
	}
	return w.Buffer.Write(p)
}
func TestFrameHandlesShortWrites(t *testing.T) {
	var w shortWriter
	if err := writeFrame(&w, map[string]string{"message": "complete"}); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := readFrame(&w.Buffer, &result); err != nil || result["message"] != "complete" {
		t.Fatalf("incomplete frame: %v %v", result, err)
	}
}

func TestTypedCellsRoundTrip(t *testing.T) {
	values := []any{nil, int64(9223372036854775807), int64(-9223372036854775807), float64(1.25), true, "123.450000000000000000001", []byte{0, 255, 1}, time.Date(2026, 9, 30, 12, 0, 0, 123, time.FixedZone("", 28800))}
	for _, value := range values {
		cell, err := encodeValue(value)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(cell)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Value
		if err = json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		got, err := decoded.decode()
		if err != nil {
			t.Fatal(err)
		}
		if expected, ok := value.(time.Time); ok {
			if !expected.Equal(got.(time.Time)) {
				t.Fatalf("time changed: %v", got)
			}
		} else if !reflect.DeepEqual(value, got) {
			t.Fatalf("%T %v -> %T %v", value, value, got, got)
		}
	}
}

func TestFramesRejectOversizeAndTruncation(t *testing.T) {
	for _, input := range [][]byte{{255, 255, 255, 255}, {0, 0, 0, 10, '{'}} {
		var value map[string]any
		if err := readFrame(bytes.NewReader(input), &value); err == nil {
			t.Fatal("accepted invalid frame")
		}
	}
}

func TestServeRejectsVersionBeforePrepare(t *testing.T) {
	var in, out bytes.Buffer
	if err := writeFrame(&in, Request{Version: 99, Engine: "postgres"}); err != nil {
		t.Fatal(err)
	}
	called := false
	_ = Serve(context.Background(), &in, &out, "postgres", func(*conn.Spec) error { called = true; return nil })
	var f frame
	if err := readFrame(&out, &f); err != nil {
		t.Fatal(err)
	}
	if called || f.Error == nil || f.Error.Code != "driver_protocol_mismatch" {
		t.Fatalf("called=%v response=%+v", called, f)
	}
}

func installTestDriver(t *testing.T) conn.Spec {
	t.Helper()
	t.Setenv("DBX_PROCESS_TEST_CHILD", "1")
	root := t.TempDir()
	dir := root
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "dbx-driver-postgres"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	manifest := Manifest{ProtocolVersion: 1, Engine: "postgres", Version: "test", OS: runtime.GOOS, Arch: runtime.GOARCH, Executable: name, SHA256: hex.EncodeToString(sum[:])}
	b, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return conn.Spec{Engine: "postgres", Driver: "pgx", DriverDir: root, DSN: filepath.Join(t.TempDir(), "test.db"), Timeout: 5 * time.Second}
}

func TestProcessQueryImportMetadataAndRollback(t *testing.T) {
	spec := installTestDriver(t)
	call := func(req Request) Response {
		t.Helper()
		r, err := Call(context.Background(), spec, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	call(Request{Operation: "execute", SQL: "create table records (id integer primary key, name text)"})
	rows, err := EncodeRows([]map[string]any{{"id": int64(9223372036854775807), "name": "original"}, {"id": int64(2), "name": nil}})
	if err != nil {
		t.Fatal(err)
	}
	if r := call(Request{Operation: "import", Table: "records", Columns: []string{"id", "name"}, Rows: rows}); r.Count != 2 {
		t.Fatalf("count=%d", r.Count)
	}
	r := call(Request{Operation: "query", SQL: "select * from records order by id desc", Limit: 1})
	if r.Query.Rows[0]["id"] != int64(9223372036854775807) || !r.Query.Truncated {
		t.Fatalf("query=%+v", r.Query)
	}
	if r := call(Request{Operation: "describe", Table: "records"}); r.Table == nil || len(r.Table.Columns) != 2 {
		t.Fatalf("table=%+v", r.Table)
	}
	_, err = Call(context.Background(), spec, Request{Operation: "guarded", SQL: "update records set name='changed'", MaxRows: 1})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("limit error=%v", err)
	}
	plan := dbcore.TxPlan{Steps: []dbcore.TxStep{{Action: action.Update, SQL: "update records set name='changed' where id=2"}, {Action: action.Update, SQL: "insert into missing values (1)"}}}
	if _, err = Call(context.Background(), spec, Request{Operation: "tx", Plan: plan, MaxRows: 10, Limit: 100}); err == nil {
		t.Fatal("transaction should fail")
	}
	r = call(Request{Operation: "query", SQL: "select name from records where id=2", Limit: 100})
	if r.Query.Rows[0]["name"] != nil {
		t.Fatalf("rollback failed: %+v", r.Query.Rows)
	}
	plan.Steps = []dbcore.TxStep{{Action: action.Update, SQL: "update records set name='committed' where id=2"}, {Action: action.Query, SQL: "select name from records where id=2"}}
	r = call(Request{Operation: "tx", Plan: plan, MaxRows: 10, Limit: 100})
	if r.Tx.Steps[1].Query.Rows[0]["name"] != "committed" {
		t.Fatalf("tx=%+v", r.Tx)
	}
}

func TestMissingAndTamperedDriverFailClosed(t *testing.T) {
	spec := conn.Spec{Engine: "postgres", DriverDir: t.TempDir(), Timeout: time.Second}
	_, err := Call(context.Background(), spec, Request{Operation: "ping"})
	var de *Error
	if !errors.As(err, &de) || de.Code != "driver_missing" {
		t.Fatalf("missing=%v", err)
	}
	spec = installTestDriver(t)
	name := "dbx-driver-postgres"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(spec.DriverDir, name+".json")
	b, _ := os.ReadFile(path)
	var m Manifest
	_ = json.Unmarshal(b, &m)
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"digest", func(m *Manifest) { m.SHA256 = strings.Repeat("0", 64) }},
		{"escape", func(m *Manifest) { m.Executable = "../dbx" }},
		{"protocol", func(m *Manifest) { m.ProtocolVersion = 2 }},
		{"engine", func(m *Manifest) { m.Engine = "oracle" }},
		{"platform", func(m *Manifest) { m.OS = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := m
			tc.mutate(&changed)
			b, _ := json.Marshal(changed)
			_ = os.WriteFile(path, b, 0600)
			if _, err := Call(context.Background(), spec, Request{Operation: "ping"}); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestProcessCancellationReapsChild(t *testing.T) {
	spec := installTestDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Call(ctx, spec, Request{Operation: "query", SQL: "with recursive numbers(x) as (select 1 union all select x+1 from numbers) select sum(x) from numbers", Limit: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel error=%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("child not reaped promptly")
	}
	// A fresh process can still access the same database after cancellation.
	if _, err := Call(context.Background(), spec, Request{Operation: "execute", SQL: "create table after_cancel(id integer)"}); err != nil {
		t.Fatal(err)
	}
}

func TestImportLimitRollsBackAllRowsAcrossProcess(t *testing.T) {
	spec := installTestDriver(t)
	if _, err := Call(context.Background(), spec, Request{Operation: "execute", SQL: "create table imported(id integer)"}); err != nil {
		t.Fatal(err)
	}
	rows, err := EncodeRows([]map[string]any{{"id": int64(1)}, {"id": int64(2)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Call(context.Background(), spec, Request{Operation: "import", Table: "imported", Columns: []string{"id"}, Rows: rows, MaxRows: 1})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected row limit failure: %v", err)
	}
	r, err := Call(context.Background(), spec, Request{Operation: "query", SQL: "select count(*) as n from imported", Limit: 1})
	if err != nil || r.Query.Rows[0]["n"] != int64(0) {
		t.Fatalf("import did not roll back: %+v %v", r, err)
	}
}

func TestDriverDirectoryPrecedence(t *testing.T) {
	spec := installTestDriver(t)
	t.Setenv("DBX_DRIVER_DIR", spec.DriverDir)
	spec.DriverDir = ""
	if _, err := Call(context.Background(), spec, Request{Operation: "ping"}); err != nil {
		t.Fatal(err)
	}
	spec.DriverDir = t.TempDir()
	_, err := Call(context.Background(), spec, Request{Operation: "ping"})
	var de *Error
	if !errors.As(err, &de) || de.Code != "driver_missing" {
		t.Fatalf("configuration did not override environment: %v", err)
	}
}

var _ io.Reader = (*bytes.Buffer)(nil)

func TestProcessUsesExplicitVersionedDriver(t *testing.T) {
	spec := installTestDriver(t)
	name := "dbx-driver-postgres"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	old := filepath.Join(spec.DriverDir, name)
	b, err := os.ReadFile(old + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	versioned := "dbx-driver-postgres-2.0.0"
	if runtime.GOOS == "windows" {
		versioned += ".exe"
	}
	path := filepath.Join(spec.DriverDir, versioned)
	if err := os.Rename(old, path); err != nil {
		t.Fatal(err)
	}
	manifest.Executable, manifest.Version = versioned, "v2.0.0"
	saveManifest(t, path+".json", manifest)
	spec.DriverPath = path
	spec.DriverDir = t.TempDir()
	t.Setenv("DBX_DRIVER_DIR", t.TempDir())
	if _, err := Call(context.Background(), spec, Request{Operation: "ping"}); err != nil {
		t.Fatal(err)
	}
}
