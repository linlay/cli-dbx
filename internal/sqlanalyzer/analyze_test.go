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
