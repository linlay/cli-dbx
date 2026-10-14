package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUTF8BOMInputFiles(t *testing.T) {
	for _, bom := range []string{"", "\xef\xbb\xbf"} {
		t.Run("bom="+boolName(bom != ""), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "中文 空格")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string) string {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(bom+body), 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			sql, err := readSQL("query", "", write("query.sql", "select '中文'\r\n"))
			if err != nil || sql != "select '中文'\r\n" {
				t.Fatalf("SQL=%q err=%v", sql, err)
			}
			plan, err := readTxPlan(write("plan.json", `{"steps":[{"action":"query","sql":"select 1"}]}`))
			if err != nil || len(plan.Steps) != 1 {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			for _, format := range []string{"csv", "json"} {
				body := "id,name\r\n1,中文\r\n"
				if format == "json" {
					body = `[{"id":1,"name":"中文"}]`
				}
				cols, rows, err := readImportFile(write("rows."+format, body))
				if err != nil || len(cols) != 2 || !slices.Contains(cols, "id") || !slices.Contains(cols, "name") || len(rows) != 1 || rows[0]["name"] != "中文" {
					t.Fatalf("%s: cols=%v rows=%v err=%v", format, cols, rows, err)
				}
			}
		})
	}
}

func boolName(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
