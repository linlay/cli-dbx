package main

import (
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
)

func TestPrepareOracleConnector(t *testing.T) {
	spec := &conn.Spec{Engine: "oracle", Driver: "oracle", DSN: "oracle://app:p%40ss@localhost:1521/ORCL"}
	if err := prepare(spec); err != nil {
		t.Fatal(err)
	}
	if spec.Connector == nil {
		t.Fatal("Oracle connector was not initialized")
	}
}

func TestPrepareRejectsMalformedOracleDSNWithoutLeakingIt(t *testing.T) {
	secret := "secret-password"
	spec := &conn.Spec{Engine: "oracle", DSN: "oracle://app:" + secret + "@/ORCL"}
	if err := prepare(spec); err == nil {
		t.Fatal("expected invalid DSN error")
	} else if contains := strings.Contains(err.Error(), secret); contains {
		t.Fatal("error exposed credential")
	}
}
