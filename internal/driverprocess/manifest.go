package driverprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/linlay/cli-dbx/internal/conn"
)

type Manifest struct {
	ProtocolVersion int               `json:"protocol_version"`
	Engine          string            `json:"engine"`
	Version         string            `json:"version"`
	OS              string            `json:"os"`
	Arch            string            `json:"arch"`
	Executable      string            `json:"executable"`
	SHA256          string            `json:"sha256"`
	Adapter         string            `json:"adapter,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
}

func IsExternal(engine string) bool {
	switch engine {
	case "postgres", "oracle", "dm", "sqlserver":
		return true
	}
	return false
}

func driverRoot(spec conn.Spec) (string, error) {
	root := spec.DriverDir
	if root == "" {
		root = os.Getenv("DBX_DRIVER_DIR")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".config", "dbx", "drivers")
	}
	return filepath.Abs(root)
}

func executable(spec conn.Spec) (string, error) {
	if !IsExternal(spec.Engine) {
		return "", &Error{Code: "driver_invalid", Message: "unsupported process driver engine"}
	}
	path := spec.DriverPath
	if path == "" {
		root, err := driverRoot(spec)
		if err != nil {
			return "", err
		}
		name := "dbx-driver-" + spec.Engine
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path = filepath.Join(root, name)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	manifestPath := path + ".json"
	missing := func() error {
		return &Error{Code: "driver_missing", Message: fmt.Sprintf("%s driver executable or matching .json manifest is missing at %s; install its driver package and set connection.driver_path", spec.Engine, path)}
	}
	for _, file := range []string{dir, path, manifestPath} {
		info, err := os.Lstat(file)
		if os.IsNotExist(err) {
			return "", missing()
		}
		if err != nil {
			return "", &Error{Code: "driver_invalid", Message: "cannot read driver package"}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", &Error{Code: "driver_invalid", Message: "driver package must not use symbolic links"}
		}
		if (file == dir && !info.IsDir()) || (file != dir && !info.Mode().IsRegular()) {
			return "", &Error{Code: "driver_invalid", Message: "driver executable and manifest must be regular files"}
		}
	}
	f, err := os.Open(manifestPath)
	if err != nil {
		return "", &Error{Code: "driver_invalid", Message: "cannot open driver manifest"}
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	f.Close()
	var m Manifest
	if err != nil || len(b) > 65536 || json.Unmarshal(b, &m) != nil {
		return "", &Error{Code: "driver_invalid", Message: "invalid driver manifest"}
	}
	if m.ProtocolVersion != ProtocolVersion {
		return "", &Error{Code: "driver_protocol_mismatch", Message: "driver protocol version does not match DBX"}
	}
	if m.Engine != spec.Engine || m.Version == "" || m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return "", &Error{Code: "driver_invalid", Message: "driver engine, version or platform does not match this installation"}
	}
	adapter := m.Adapter
	if adapter == "" {
		adapter = "native"
	}
	wantAdapter := "native"
	if spec.Driver == "odbc" {
		wantAdapter = "odbc"
	}
	if adapter != wantAdapter {
		return "", &Error{Code: "driver_invalid", Message: "driver adapter does not match connection.driver"}
	}
	if m.Executable == "" || m.Executable == "." || m.Executable == ".." || strings.ContainsAny(m.Executable, "/\\:") || m.Executable != filepath.Base(path) {
		return "", &Error{Code: "driver_invalid", Message: "driver manifest executable must match the selected filename"}
	}
	if err := verifyDriverFile(path, m.SHA256); err != nil {
		return "", err
	}
	for name, digest := range m.Files {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:") || name == m.Executable || name == m.Executable+".json" {
			return "", &Error{Code: "driver_invalid", Message: "driver resources must be distinct files in the same flat directory"}
		}
		if err := verifyDriverFile(filepath.Join(dir, name), digest); err != nil {
			return "", err
		}
	}
	return path, nil
}

func verifyDriverFile(path, digest string) error {
	want, err := hex.DecodeString(digest)
	if err != nil || len(want) != sha256.Size {
		return &Error{Code: "driver_invalid", Message: "invalid driver file SHA-256"}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return &Error{Code: "driver_missing", Message: "a driver package file is missing; reinstall the complete package"}
	}
	if err != nil || !info.Mode().IsRegular() {
		return &Error{Code: "driver_invalid", Message: "driver package files must be regular files, not symbolic links"}
	}
	f, err := os.Open(path)
	if err != nil {
		return &Error{Code: "driver_invalid", Message: "cannot read driver package file"}
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), digest) {
		return &Error{Code: "driver_integrity_failed", Message: "driver package file SHA-256 does not match its manifest"}
	}
	return nil
}
