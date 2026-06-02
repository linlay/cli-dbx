package sqlanalyzer

import "testing"

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
