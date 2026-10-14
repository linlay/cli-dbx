package sqlclass

import "testing"

func TestSafetyRegressionClassify(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want StatementClass
	}{
		{"with gone as (delete from users returning *) select * from gone", ClassUnknown},
		{"with changed as (update users set active=0 returning *) select 1", ClassUnknown},
		{"with added as (insert into users values (1) returning *) select 1", ClassUnknown},
		{"with c as (with d as (delete from users returning *) select * from d) select * from c", ClassUnknown},
		{"explain analyze delete from users", ClassUnknown},
		{"explain (analyze true, buffers true) update users set active=0", ClassUnknown},
		{"pragma user_version = 42", ClassAdmin},
		{"pragma user_version(42)", ClassAdmin},
		{"pragma main.journal_mode(WAL)", ClassAdmin},
		{"pragma optimize", ClassAdmin},
		{"select 1 /*! into outfile '/tmp/x' */", ClassUnknown},
		{"with \"delete\" as (select ')' as x from users) select * from \"delete\"", ClassRead},
		{"with c(x) as not materialized (select 'where;/*' from users) select * from c", ClassRead},
		{"with recursive c(x) as (select 1 union all select x+1 from c where x<3) select * from c", ClassRead},
		{"select 'it''s /* where */' from users", ClassRead},
		{"select $$ ); delete from users; /* $$ from users", ClassRead},
		{"select $tag$ ; where ' /* $tag$ from users", ClassRead},
		{"select [into] from [users]", ClassRead},
		{"explain select * from users", ClassRead},
		{"explain query plan select * from users", ClassRead},
		{"explain analyze select * from users", ClassRead},
		{"pragma table_info('users')", ClassRead},
		{"pragma main.table_xinfo(users)", ClassRead},
		{"pragma user_version", ClassRead},
		{"pragma table_info = 'users'", ClassRead},
		{"pragma integrity_check", ClassRead},
		{"pragma foreign_key_check(users)", ClassRead},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			if got := Classify(tc.sql); got != tc.want {
				t.Errorf("got %s; want %s", got, tc.want)
			}
		})
	}
}

func TestSafetyRegressionUnsafeWhere(t *testing.T) {
	for _, sql := range []string{
		"with c as (select id from users where id=1) delete from users",
		"update users set active=(select 1 where 1=1)",
		"update users set name=' where '",
		"update users set name=$q$ where $q$",
		"delete from users /* where id=1 */",
		"update users set [where]=1",
		"update users set name='it''s where '",
	} {
		if !HasUnsafeWrite(sql) {
			t.Errorf("accepted unsafe %q", sql)
		}
	}
	for _, sql := range []string{
		"delete from users where(id=1)",
		"update users set name='/*' where id=1",
		"with c as (select 1) delete from users where\nid=1",
	} {
		if HasUnsafeWrite(sql) {
			t.Errorf("rejected safe %q", sql)
		}
	}
}
