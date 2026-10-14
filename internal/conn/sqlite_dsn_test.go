package conn

import (
	"runtime"
	"testing"
)

func TestBuildSQLiteDSNEncodesLiteralPathAndPreservesQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"relative", "data/中文 # %23 %20.db", "file:data/%E4%B8%AD%E6%96%87%20%23%20%2523%20%2520.db"},
		{"absolute", "/tmp/data # %23.db", "file:/tmp/data%20%23%20%2523.db"},
		{"read only query", "data # %23.db?mode=ro&cache=private", "file:data%20%23%20%2523.db?mode=ro&cache=private"},
		{"raw pragma query", "data#.db?_pragma=busy_timeout%3d4321&_pragma=foreign_keys(1)&mode=rwc", "file:data%23.db?_pragma=busy_timeout%3d4321&_pragma=foreign_keys(1)&mode=rwc"},
		{"empty query", "data#.db?", "file:data%23.db?"},
		{"memory", ":memory:", "file::memory:"},
		{"memory query", ":memory:?cache=shared&_pragma=foreign_keys(1)", "file::memory:?cache=shared&_pragma=foreign_keys(1)"},
		{"default memory", "", "file::memory:"},
		{"empty filename query", "?mode=memory&cache=shared", "file:?mode=memory&cache=shared"},
		{"explicit file URI", "file:data%23.db?mode=ro&_pragma=foreign_keys(1)", "file:data%23.db?mode=ro&_pragma=foreign_keys(1)"},
		{"explicit sqlite form", "sqlite:data%23.db?mode=ro", "sqlite:data%23.db?mode=ro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildSQLiteDSN(Spec{Path: tc.path}); got != tc.want {
				t.Fatalf("DSN = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildSQLiteDSNWindowsDriveAndUNC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows filepath semantics")
	}
	for _, tc := range []struct{ path, want string }{
		{`C:\数据 目录\name#%23.db?mode=ro`, "file:/C:/%E6%95%B0%E6%8D%AE%20%E7%9B%AE%E5%BD%95/name%23%2523.db?mode=ro"},
		{`C:/data/name#%20.db`, "file:/C:/data/name%23%2520.db"},
		{`\\server\share\name#%20.db?mode=ro`, "file:////server/share/name%23%2520.db?mode=ro"},
	} {
		if got := buildSQLiteDSN(Spec{Path: tc.path}); got != tc.want {
			t.Errorf("DSN = %q, want %q", got, tc.want)
		}
	}
}

func TestSQLiteFileURIWindowsAndUnixPathSemantics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		windows bool
		want    string
	}{
		{"Windows drive", `C:\数据 目录\name#%23.db`, true, "file:/C:/%E6%95%B0%E6%8D%AE%20%E7%9B%AE%E5%BD%95/name%23%2523.db"},
		{"Windows slash drive", "C:/data/name#%20.db", true, "file:/C:/data/name%23%2520.db"},
		{"Windows UNC", `\\server\share\name#%20.db`, true, "file:////server/share/name%23%2520.db"},
		{"Windows slash UNC", "//server/share/name#.db", true, "file:////server/share/name%23.db"},
		{"Windows relative", `data\name#.db`, true, "file:data/name%23.db"},
		{"Unix backslash", `data\name#.db`, false, "file:data%5Cname%23.db"},
		{"Unix leading double slash", "//tmp/name#.db", false, "file:////tmp/name%23.db"},
		{"literal question in filename", "name?.db", false, "file:name%3F.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqliteFileURI(tc.path, tc.windows); got != tc.want {
				t.Fatalf("URI = %q, want %q", got, tc.want)
			}
		})
	}
}
