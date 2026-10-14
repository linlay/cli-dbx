package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteUTF8BOMFileWorkflow(t *testing.T) {
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
	config := write("local.toml", "[connection]\r\nengine='sqlite'\r\npath='./demo.db'\r\nallow_actions=['query','update','schema']\r\n")
	query := write("query.sql", "select id, name from users order by id\r\n")
	plan := write("plan.json", `{"steps":[{"action":"update","sql":"update users set name = '更新' where id = 1","max_rows_affected":1}]}`)
	csv := write("users.csv", "id,name\r\n1,中文\r\n")
	jsonPath := write("users.json", `[{"id":2,"name":"测试"}]`)
	run := func(args ...string) commandResult {
		t.Helper()
		args = append(args, "--config", config)
		result := runCommand(t, nil, args...)
		if result.code != ExitSuccess {
			t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, result.code, result.stdout, result.stderr)
		}
		return result
	}
	run("schema", "local", "create table users (id integer primary key, name text)")
	run("import", "file", csv, "local", "users")
	run("import", "file", jsonPath, "local", "users")
	run("tx", "local", "--plan", plan)
	result := run("query", "file", "local", query)
	var payload struct {
		Data struct {
			Rows []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Rows) != 2 || payload.Data.Rows[0].ID != 1 || payload.Data.Rows[0].Name != "更新" || payload.Data.Rows[1].ID != 2 || payload.Data.Rows[1].Name != "测试" {
		t.Fatalf("BOM workflow did not preserve data: %s", result.stdout)
	}
}
