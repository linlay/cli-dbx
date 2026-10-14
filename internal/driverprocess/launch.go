package driverprocess

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/linlay/cli-dbx/internal/dbmodule"
)

func launch(spec conn.Spec) (string, []string, error) {
	path := spec.DriverPath
	if !strings.EqualFold(filepath.Ext(path), ".dbmod") {
		program, err := executable(spec)
		return program, nil, err
	}
	if !dbmodule.Enabled || spec.Driver == "odbc" {
		return "", nil, &Error{Code: "driver_invalid", Message: "this build or adapter does not support Go object modules"}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	host, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	if _, _, err := dbmodule.Verify(path, spec.Engine, host); err != nil {
		code := "driver_invalid"
		if os.IsNotExist(err) || strings.Contains(err.Error(), "is missing") {
			code = "driver_missing"
		}
		return "", nil, &Error{Code: code, Message: err.Error()}
	}
	return host, []string{"--dbx-module-worker", path, spec.Engine}, nil
}
