package sqlclass

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"select * from users":     "read",
		"update users set a = 1":  "write-data",
		"alter table users add x": "ddl",
		"grant select on x":       "admin",
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
}
