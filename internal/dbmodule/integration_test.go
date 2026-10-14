//go:build dbmod

package dbmodule_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These tests launch a real shared-host binary and a separately packed SQLite
// fixture. They do not claim vendor-database compatibility.
func TestSharedHostModuleSQLWorkflow(t *testing.T) {
	host, module := os.Getenv("DBX_TEST_SHARED_HOST"), os.Getenv("DBX_TEST_SHARED_MODULE")
	if host == "" || module == "" {
		t.Skip("run scripts/build-shared-host.ps1 -Test")
	}
	dir := filepath.Join(t.TempDir(), "中文 空格")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+body), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	config := filepath.Join(dir, "fixture.toml")
	database := filepath.Join(dir, "数据库.db")
	configure := func(driver, actions, timeout string) {
		t.Helper()
		write("fixture.toml", fmt.Sprintf("[connection]\nengine='postgres'\ndriver_path=%q\nhost='127.0.0.1'\nuser='test'\ndatabase=%q\nsslmode='disable'\nallow_actions=[%s]\ntimeout=%q\n", driver, database, actions, timeout))
	}
	configure(module, "'query','update','schema'", "10s")
	run := func(ok bool, args ...string) map[string]any {
		t.Helper()
		args = append(args, "--config", config)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, host, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if (err == nil) != ok {
			t.Fatalf("%v: err=%v stdout=%s stderr=%s", args, err, out, stderr.String())
		}
		var result map[string]any
		decoder := json.NewDecoder(bytes.NewReader(out))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil {
			t.Fatalf("invalid result %s: %v", out, err)
		}
		return result
	}
	run(true, "schema", "fixture", "create table users (id integer primary key, name text, payload blob)")
	csv := write("users.csv", "id,name\r\n1,中文\r\n2,测试\r\n")
	run(true, "import", "file", csv, "fixture", "users")
	run(true, "update", "fixture", "insert into users values (9223372036854775807, '精确整数', x'00ff')")
	query := run(true, "query", "fixture", "select id, name, payload from users where id=9223372036854775807")
	row := query["data"].(map[string]any)["rows"].([]any)[0].(map[string]any)
	if row["id"] != json.Number("9223372036854775807") || row["name"] != "精确整数" || row["payload"] != "AP8=" {
		t.Fatalf("type fidelity: %v", row)
	}
	plan := write("plan.json", `{"steps":[{"action":"update","sql":"update users set name='已提交' where id=1","max_rows_affected":1},{"action":"query","sql":"select name from users where id=1"}]}`)
	run(true, "tx", "fixture", "--plan", plan)
	failed := write("failed.json", `{"steps":[{"action":"update","sql":"update users set name='应回滚' where id=1","max_rows_affected":1},{"action":"update","sql":"insert into missing values(1)"}]}`)
	run(false, "tx", "fixture", "--plan", failed)
	query = run(true, "query", "fixture", "select name from users where id=1")
	if query["data"].(map[string]any)["rows"].([]any)[0].(map[string]any)["name"] != "已提交" {
		t.Fatal("transaction did not roll back")
	}
	run(true, "schema", "fixture", "create table imported(id integer, name text)")
	run(false, "import", "file", csv, "fixture", "imported", "--max-rows-affected", "1")
	query = run(true, "query", "fixture", "select count(*) as n from imported")
	if query["data"].(map[string]any)["rows"].([]any)[0].(map[string]any)["n"] != json.Number("0") {
		t.Fatal("over-limit import did not roll back")
	}
	if code := run(false, "update", "fixture", "delete from users")["code"]; code != "missing_where" {
		t.Fatalf("write guard: %v", code)
	}
	configure(filepath.Join(dir, "missing.dbmod"), "'query'", "10s")
	if code := run(false, "schema", "fixture", "create table blocked(id int)")["code"]; code != "action_blocked" {
		t.Fatalf("module launch preceded policy: %v", code)
	}
	if code := run(false, "query", "fixture", "select 1")["code"]; code != "driver_missing" {
		t.Fatalf("missing module: %v", code)
	}
	configure(module, "'query','update','schema'", "200ms")
	start := time.Now()
	run(false, "query", "fixture", "with recursive n(x) as (select 1 union all select x+1 from n) select sum(x) from n")
	if time.Since(start) > 4*time.Second {
		t.Fatal("module cancellation was not bounded")
	}
	configure(module, "'query','update','schema'", "10s")
	run(true, "query", "fixture", "select count(*) from users")
}

// A loopback peer closes each socket immediately. Observing an accepted socket
// proves each actual vendor connector reached its network code, rather than
// merely returning an unknown-driver or configuration error.
func TestSharedHostVendorModulesInitialize(t *testing.T) {
	host := os.Getenv("DBX_TEST_SHARED_HOST")
	if host == "" {
		t.Skip("run scripts/build-shared-host.ps1 -Test")
	}
	for _, engine := range []string{"postgres", "oracle", "dm", "sqlserver"} {
		t.Run(engine, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			accepted := make(chan struct{}, 1)
			go func() {
				peer, err := listener.Accept()
				if err == nil {
					peer.Close()
					accepted <- struct{}{}
					listener.Close()
				}
			}()
			matches, err := filepath.Glob(filepath.Join(filepath.Dir(host), "dbx-driver-"+engine+"-*.dbmod"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("expected one %s module, found %v: %v", engine, matches, err)
			}
			isolated := t.TempDir()
			isolatedHost := filepath.Join(isolated, filepath.Base(host))
			isolatedModule := filepath.Join(isolated, filepath.Base(matches[0]))
			for _, pair := range [][2]string{{host, isolatedHost}, {matches[0], isolatedModule}, {matches[0] + ".json", isolatedModule + ".json"}} {
				content, err := os.ReadFile(pair[0])
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pair[1], content, 0755); err != nil {
					t.Fatal(err)
				}
			}
			config := filepath.Join(isolated, "connection.toml")
			body := fmt.Sprintf("[connection]\nengine=%q\ndriver_path=%q\nhost='127.0.0.1'\nport=%d\nuser='test'\ndatabase='test'\nsslmode='disable'\nallow_actions=['query']\ntimeout='10s'\n", engine, isolatedModule, port)
			if err := os.WriteFile(config, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, isolatedHost, "conn", "test", "connection", "--config", config)
			out, runErr := cmd.Output()
			var result map[string]any
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatalf("%v: %s", runErr, out)
			}
			if runErr == nil || result["code"] != "sql_error" {
				t.Fatalf("module did not reach vendor connection attempt: %v %s", runErr, out)
			}
			select {
			case <-accepted:
			default:
				t.Fatalf("vendor connector did not open a socket: %s", out)
			}
		})
	}
}
