package conn

import "testing"

func TestParseAllowTablesTrimsAndDeduplicates(t *testing.T) {
	got := parseAllowTables([]string{" users", "orders", "public.audit_*", "users", "", "`quoted`"})
	want := []string{"users", "orders", "public.audit_*", "quoted"}
	if len(got) != len(want) {
		t.Fatalf("allow tables = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allow tables = %#v, want %#v", got, want)
		}
	}
}

func TestAllowsTableMatchesSchemaAwareWildcards(t *testing.T) {
	spec := Spec{AllowTables: parseAllowTables([]string{"users", "public.audit_*", "*.orders"})}
	for _, object := range []string{"users", "main.users", "public.audit_log", "sales.orders"} {
		if !spec.AllowsTable(object) {
			t.Fatalf("expected %s to be allowed", object)
		}
	}
	for _, object := range []string{"orders", "private.audit_log", "sales.users_archive"} {
		if spec.AllowsTable(object) {
			t.Fatalf("expected %s to be blocked", object)
		}
	}
}

func TestAllowsTableDefaultsToUnrestricted(t *testing.T) {
	spec := Spec{}
	if !spec.AllowsTable("anything") {
		t.Fatal("empty allow_tables should not restrict tables")
	}
}
