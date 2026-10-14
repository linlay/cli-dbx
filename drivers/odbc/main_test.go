package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/linlay/cli-dbx/internal/conn"
)

func TestPrepareRequiresODBCAndRegularVendorLibrary(t *testing.T) {
	dir := t.TempDir()
	vendor := filepath.Join(dir, "vendor.so")
	if err := os.WriteFile(vendor, []byte("not a live vendor library"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		spec conn.Spec
		good bool
	}{
		{"missing ODBC", conn.Spec{Engine: "oracle", Driver: "odbc", Host: "db", Database: "service"}, false},
		{"native", conn.Spec{Engine: "oracle", Driver: "oracle", Host: "db", Database: "service", ODBC: &conn.ODBCConfig{DriverPath: vendor}}, false},
		{"relative", conn.Spec{Engine: "oracle", Driver: "odbc", Host: "db", Database: "service", ODBC: &conn.ODBCConfig{DriverPath: "vendor.so"}}, false},
		{"directory", conn.Spec{Engine: "oracle", Driver: "odbc", Host: "db", Database: "service", ODBC: &conn.ODBCConfig{DriverPath: dir}}, false},
	}
	if runtime.GOOS != "windows" {
		// Windows requires an installed registry entry for a real DLL.
		cases = append(cases, struct {
			name string
			spec conn.Spec
			good bool
		}{"valid", conn.Spec{Engine: "oracle", Driver: "odbc", Host: "db", Database: "service", ODBC: &conn.ODBCConfig{DriverPath: vendor}}, true})
		link := filepath.Join(dir, "vendor-link.so")
		if err := os.Symlink(vendor, link); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			name string
			spec conn.Spec
			good bool
		}{"vendor symlink", conn.Spec{Engine: "oracle", Driver: "odbc", Host: "db", Database: "service", ODBC: &conn.ODBCConfig{DriverPath: link}}, true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := prepare(&tc.spec)
			if (err == nil) != tc.good {
				t.Fatalf("prepare err = %v", err)
			}
			if tc.good && tc.spec.DSN == "" {
				t.Fatal("no DSN prepared")
			}
		})
	}
}
