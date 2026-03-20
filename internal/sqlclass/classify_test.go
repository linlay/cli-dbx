package sqlclass

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"select * from users":                                       "read",
		"update users set a = 1":                                    "write-data",
		"alter table users add x":                                   "ddl",
		"grant select on x":                                         "admin",
		"/* comment */ insert into users values (1)":                "write-data",
		"with recent as (select * from users) select * from recent": "read",
		"with doomed as (select * from users) delete from users":    "write-data",
		"select 1; delete from users":                               "read",
	}
	for input, want := range cases {
		if got := string(Classify(input)); got != want {
			t.Fatalf("%q => %s, want %s", input, got, want)
		}
	}
}

func TestHasUnsafeWrite(t *testing.T) {
	if !HasUnsafeWrite("delete from users") {
		t.Fatal("expected missing WHERE to be unsafe")
	}
	if HasUnsafeWrite("delete from users where id = 1") {
		t.Fatal("expected WHERE clause to be accepted")
	}
	if !HasUnsafeWrite("with doomed as (select * from users) delete from users") {
		t.Fatal("expected WITH DELETE without WHERE to be unsafe")
	}
}
