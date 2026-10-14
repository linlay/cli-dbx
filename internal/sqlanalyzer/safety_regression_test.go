package sqlanalyzer

import (
	"reflect"
	"strings"
	"testing"
)

func TestSafetyRegressionAnalyzeObjects(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		objects []string
	}{
		{"select '/*' as marker from secrets", []string{"secrets"}},
		{"select '--' as marker from secrets", []string{"secrets"}},
		{"select 'from phantom, join nope' from users", []string{"users"}},
		{"delete from users using secrets where users.id=secrets.id", []string{"secrets", "users"}},
		{"merge into users using secrets on users.id=secrets.id when matched then update set active=0", []string{"secrets", "users"}},
		{"delete from users using (select id from secrets) s where users.id=s.id", []string{"secrets", "users"}},
		{"select * from \"private data\".\"user table\"", []string{"private data.user table"}},
		{"select [from] from [dbo].[user]]table]", []string{"dbo.user]table"}},
		{"with \"recent\" as (select * from users) select * from \"recent\" join orders on true", []string{"orders", "users"}},
		{"select * from users join orders using (id)", []string{"orders", "users"}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			if a := Analyze(tc.sql); !reflect.DeepEqual(a.Objects, tc.objects) {
				t.Errorf("objects = %#v; want %#v", a.Objects, tc.objects)
			}
		})
	}
}

func TestSafetyRegressionAnalyzeBoundaries(t *testing.T) {
	for _, sql := range []string{
		"select ';' from users; -- trailing comment",
		"select $q$ ; delete from users; $q$ from users",
		"select [semi;colon] from users",
		"select 1 /* outer /* inner */ still comment */ from users",
	} {
		if a := Analyze(sql); a.MultiStatement || a.ValidateSingleStatement() != nil {
			t.Errorf("rejected single statement %q: %+v", sql, a)
		}
	}
	for _, sql := range []string{
		"select 'unterminated", "select (1", "select 1 /* unclosed", "select 1 /*! ; delete from users */",
	} {
		if a := Analyze(sql); a.ValidateSingleStatement() == nil {
			t.Errorf("accepted uncertain statement %q: %+v", sql, a)
		}
	}
	for _, sql := range []string{
		"with c as (select id from users where id=1) delete from users",
		"update users set active=(select 1 where 1=1)",
		"update users set name=' where '",
	} {
		if a := Analyze(sql); !a.HasUnsafeWrite || a.Statements[0].HasWhere {
			t.Errorf("accepted unsafe write %q: %+v", sql, a)
		}
	}
}

func TestSafetyRegressionDialectAmbiguity(t *testing.T) {
	for _, tc := range []struct{ engine, sql string }{
		{"mysql", "select 1 /* outer /* inner */ ; delete from users; -- */"},
		{"sqlite", "select 1 /* outer /* inner */ ; delete from users; -- */"},
		{"mysql", "select 1--1; delete from users"},
		{"postgres", "select array[(select id from secrets)] from users"},
		{"mysql", "select $$; delete from users; $$"},
	} {
		if a := AnalyzeForEngine(tc.engine, tc.sql); a.ValidateSingleStatement() == nil {
			t.Errorf("accepted ambiguous %s SQL %q", tc.engine, tc.sql)
		}
	}
	for _, tc := range []struct{ engine, sql string }{
		{"postgres", `select E'escaped \' ; /* string' from users`},
		{"postgres", "select $$ ; delete from users $$ from users"},
		{"sqlserver", "select [semi;colon] from [users]"},
		{"postgres", "select 1 /* outer /* inner */ still comment */ from users"},
	} {
		if a := AnalyzeForEngine(tc.engine, tc.sql); a.ValidateSingleStatement() != nil {
			t.Errorf("rejected known %s quote %q: %+v", tc.engine, tc.sql, a)
		}
	}
}

func TestSafetyRegressionGroupedRelations(t *testing.T) {
	for _, query := range []string{
		"select * from users cross join 'secrets'",
		"select * from users cross join ('secrets')",
		"select '/*' from users, 'secrets'",
		"select * from (secrets cross join users)",
		"select * from ((secrets)) cross join users",
		"select * from (secrets, users)",
		"select * from users join (secrets) s on users.id=s.id",
		"select * from users join only (secrets) s on users.id=s.id",
		"delete from users using (secrets) where users.id=secrets.id",
		"select * from (select id from secrets) s cross join users",
	} {
		if a := Analyze(query); !reflect.DeepEqual(a.Objects, []string{"secrets", "users"}) {
			t.Errorf("grouped relations %q: %#v", query, a.Objects)
		}
	}
}

func TestSafetyRegressionDialectWhere(t *testing.T) {
	for _, tc := range []struct{ engine, sql string }{
		{"mysql", "update users set active=1 # WHERE id=1"},
		{"mysql", "update users set active=1# WHERE id=1"},
		{"oracle", `update users set name=q'[x' where 1=1 --]'`},
		{"oracle", `update users set name=NQ'[x' where 1=1 --]'`},
		{"dm", `update users set name=q'[x' where 1=1 --]'`},
	} {
		a := AnalyzeForEngine(tc.engine, tc.sql)
		if a.ValidateSingleStatement() == nil && !a.HasUnsafeWrite {
			t.Errorf("accepted WHERE in dialect literal/comment: %s %q %+v", tc.engine, tc.sql, a)
		}
	}
	for _, tc := range []struct{ engine, sql string }{
		{"mysql", "update users set name='# WHERE id=1' where id=2"},
		{"mysql", "update users set `# WHERE`=1 where id=2"},
		{"oracle", `update users set name='q''[x]' where id=2`},
		{"oracle", `update users set "q'[WHERE]"=1 where id=2`},
	} {
		a := AnalyzeForEngine(tc.engine, tc.sql)
		if a.ValidateSingleStatement() != nil || a.HasUnsafeWrite {
			t.Errorf("rejected quoted marker: %s %q %+v", tc.engine, tc.sql, a)
		}
	}
}

func TestSafetyRegressionImplicitRecursiveCTEScope(t *testing.T) {
	query := "with c(x) as (select id from users union all select x+1 from c where x<3) select * from c"
	for _, engine := range []string{"sqlite", "sqlserver"} {
		a := AnalyzeForEngine(engine, query)
		if a.ValidateSingleStatement() != nil || !reflect.DeepEqual(a.Objects, []string{"users"}) {
			t.Errorf("%s recursive CTE: %+v", engine, a)
		}
	}
	for _, tc := range []struct {
		engine, query string
		want          []string
	}{
		{"postgres", query, []string{"c", "users"}},
		{"sqlite", "with c as (select id from c union all select id from users) select * from c", []string{"c", "users"}},
		{"sqlserver", "with c as (select id from c union all select id from users) select * from c", []string{"c", "users"}},
		{"postgres", "with c as (select id from c) select * from users", []string{"c", "users"}},
	} {
		if a := AnalyzeForEngine(tc.engine, tc.query); !reflect.DeepEqual(a.Objects, tc.want) {
			t.Errorf("hid physical/anchor table %s %q: %#v", tc.engine, tc.query, a.Objects)
		}
	}
}

func TestSafetyRegressionMergeOptionalInto(t *testing.T) {
	for _, query := range []string{
		"merge secrets using users on secrets.id=users.id when matched then delete;",
		"merge [dbo].[secrets] using users on secrets.id=users.id when matched then update set name=users.name;",
	} {
		a := AnalyzeForEngine("sqlserver", query)
		want := []string{"secrets", "users"}
		if strings.Contains(query, "[dbo]") {
			want = []string{"dbo.secrets", "users"}
		}
		if a.ValidateSingleStatement() != nil || !reflect.DeepEqual(a.Objects, want) {
			t.Errorf("merge target omitted %q: %+v", query, a)
		}
	}
}

func TestSafetyRegressionExplainCTEScope(t *testing.T) {
	a := AnalyzeForEngine("sqlite", "explain query plan with c as (select id from users) select * from c")
	if a.ValidateSingleStatement() != nil || !reflect.DeepEqual(a.Objects, []string{"users"}) {
		t.Fatalf("EXPLAIN CTE scope: %+v", a)
	}
}

func TestSafetyRegressionMergeTopTarget(t *testing.T) {
	for _, query := range []string{
		"merge top(1) secrets using users on secrets.id=users.id when matched then delete;",
		"merge top(10) percent secrets using users on secrets.id=users.id when matched then delete;",
		"merge top(1) into secrets with (holdlock) using users on secrets.id=users.id when matched then delete;",
	} {
		a := AnalyzeForEngine("sqlserver", query)
		if a.ValidateSingleStatement() != nil || !reflect.DeepEqual(a.Objects, []string{"secrets", "users"}) {
			t.Errorf("MERGE TOP target: %+v", a)
		}
	}
}
