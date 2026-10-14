package driverprocess

import (
	"net/url"
	"strings"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
)

func TestRedactMessagePostgresKeywordDSN(t *testing.T) {
	spec := conn.Spec{Engine: "postgres", DSN: "host=localhost user=app password='s3cret \\'quoted\\'' dbname=app"}
	for _, message := range []string{spec.DSN, "driver rejected password s3cret", "password='s3cret \\'quoted\\''"} {
		if got := redactMessage(message, spec); strings.Contains(got, "s3cret") {
			t.Fatalf("server error leaked keyword password: %q", got)
		}
	}
}

func TestRedactMessageEncodedURLPassword(t *testing.T) {
	password := "p@ss /?#%+"
	spec := conn.Spec{Engine: "postgres", DSN: (&url.URL{Scheme: "postgres", User: url.UserPassword("app", password), Host: "localhost", Path: "/db"}).String()}
	message := spec.DSN + " " + password + " " + url.QueryEscape(password) + " " + url.PathEscape(password) + " " + url.UserPassword("app", password).String()
	got := redactMessage(message, spec)
	for _, leak := range []string{password, url.QueryEscape(password), url.PathEscape(password), url.UserPassword("app", password).String()} {
		if strings.Contains(got, leak) {
			t.Fatalf("server error leaked %q: %q", leak, got)
		}
	}
}

func TestRedactMessagePostgresURLQueryPassword(t *testing.T) {
	spec := conn.Spec{Engine: "postgres", DSN: "postgres://localhost/app?user=app&password=s3cret"}
	if got := redactMessage("driver reported s3cret for "+spec.DSN, spec); strings.Contains(got, "s3cret") {
		t.Fatalf("server error leaked URL query password: %q", got)
	}
}

func TestRedactMessageODBCVendorDetails(t *testing.T) {
	spec := conn.Spec{Engine: "oracle", Driver: "odbc", ODBC: &conn.ODBCConfig{Password: "s3cret", Options: map[string]string{"Token": "option-secret"}}}
	message := "ODBC failed for PWD=s3cret; Token=option-secret; verify database state before retrying a write"
	got := redactMessage(message, spec)
	if strings.Contains(got, "s3cret") || strings.Contains(got, "option-secret") || !strings.Contains(got, "verify database state before retrying a write") {
		t.Fatalf("redaction = %q", got)
	}
}
