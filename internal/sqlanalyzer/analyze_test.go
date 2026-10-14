package sqlanalyzer

import (
	"testing"

	"github.com/linlay/cli-dbx/internal/action"
)

func TestAnalyzeDetectsObjectsAndUnsafeWrite(t *testing.T) {
	analysis := Analyze("with doomed as (select * from users) delete from users")
	if analysis.StatementClass != "write-data" {
		t.Fatalf("class = %s", analysis.StatementClass)
	}
	if !analysis.HasUnsafeWrite {
		t.Fatal("expected unsafe write")
	}
	if len(analysis.Objects) == 0 || analysis.Objects[0] != "users" {
		t.Fatalf("objects = %#v", analysis.Objects)
	}
}

func TestAnalyzeBlocksMultipleStatements(t *testing.T) {
	analysis := Analyze("select 1; select 2")
	if !analysis.MultiStatement {
		t.Fatal("expected multi statement")
	}
	if err := analysis.ValidateSingleStatement(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestAnalyzeCollectsJoinedAndCTEObjects(t *testing.T) {
	analysis := Analyze("with recent as (select * from users) select * from recent join public.orders o on o.user_id = recent.id")
	want := []string{"public.orders", "users"}
	if len(analysis.Objects) != len(want) {
		t.Fatalf("objects = %#v", analysis.Objects)
	}
	for i := range want {
		if analysis.Objects[i] != want[i] {
			t.Fatalf("objects = %#v, want %#v", analysis.Objects, want)
		}
	}
}

func TestAnalyzeCollectsWriteAndDDLTargets(t *testing.T) {
	cases := map[string]string{
		"insert into users (id) values (1)":              "users",
		"update users set name = 'x' where id = 1":       "users",
		"delete from users where id = 1":                 "users",
		"truncate table users":                           "users",
		"alter table public.users add column email text": "public.users",
	}
	for sql, want := range cases {
		analysis := Analyze(sql)
		if len(analysis.Objects) == 0 || analysis.Objects[0] != want {
			t.Fatalf("%s objects = %#v, want %s", sql, analysis.Objects, want)
		}
	}
}

func TestSelectIntoRequiresSchemaAction(t *testing.T) {
	for _, sql := range []string{
		"select id into archived_users from users",
		"select id into temp table #archived_users from users",
		"with source as (select id from users) select id into archived_users from source",
	} {
		a := Analyze(sql)
		if a.StatementClass != "ddl" || action.FromClass(a.StatementClass) != action.Schema || !a.NeedsAck {
			t.Errorf("SELECT INTO = %s, action %s, needs ack %t: %q", a.StatementClass, action.FromClass(a.StatementClass), a.NeedsAck, sql)
		}
	}
}

func TestSQLServerRejectsAdjacentTopLevelStatements(t *testing.T) {
	for _, sql := range []string{
		"select 1 as n delete from users",
		"update users set active = 1 where id = 1 delete from users",
		"with recent as (select id from users) select id from recent delete from users",
		"select id from users insert into audit(id) values (1)",
		"select 1 set @flag = 1",
		"select 1 if 1=1 delete from users",
		"update users set active = 1 where id = 1 set @flag = 1",
		"select 1 /* outer /* inner */ ( */ delete from users",
		"select 1 -- comment\rdelete from users",
		"select 1 disable trigger all on users",
		"select 1 enable trigger all on users",
		"select 1 kill 52",
		"select 1 shutdown",
		"select 1 reconfigure",
		"select 1 checkpoint",
		"select 1 deny select on users to guest",
		"select 1 while 1=1 delete from users",
	} {
		a := AnalyzeForEngine("sqlserver", sql)
		if !a.MultiStatement || a.ValidateSingleStatement() == nil {
			t.Errorf("accepted adjacent statement: %q: %+v", sql, a)
		}
		if standard := Analyze(sql); standard.MultiStatement {
			t.Errorf("changed baseline parsing for %q", sql)
		}
	}
}

func TestSQLServerAllowsNestedAndQuotedKeywords(t *testing.T) {
	for _, sql := range []string{
		"select 'delete from users' as text, [update] from users",
		"select id from users where id in (select user_id from activity)",
		"with recent as (select id from users) select id from recent",
		"insert into archive(id) select id from users",
		"update users set active = 1 where id = 1",
		"merge into target using source on target.id = source.id when matched then update set val = source.val when not matched then insert (id) values (source.id)",
		"select 1 /* outer /* inner */ still comment */ as n",
		"select 1 -- comment\r\n as n",
	} {
		a := AnalyzeForEngine("sqlserver", sql)
		if a.MultiStatement {
			t.Errorf("rejected single statement: %q", sql)
		}
	}
}
