// Package dbmodule validates independently distributed Go object modules.
// Modules are trusted current-user code, executed in the CLI process.
package dbmodule

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const FormatVersion = 2
const APIVersion = 1
const MaxModuleBytes = 32 << 20
const MaxDecodedBytes = 128 << 20

type Manifest struct {
	FormatVersion   int    `json:"format_version"`
	APIVersion      int    `json:"api_version"`
	ProtocolVersion int    `json:"protocol_version"`
	Engine          string `json:"engine"`
	Version         string `json:"version"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	Toolchain       string `json:"toolchain"`
	HostSHA256      string `json:"host_sha256"`
	Module          string `json:"module"`
	SHA256          string `json:"sha256"`
	DecodedBytes    int64  `json:"decoded_bytes"`
}

func Entry(engine string) string { return "github.com/linlay/cli-dbx/modules/" + engine }

func Digest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify returns the exact bytes whose digest was checked. The CLI loads
// these bytes instead of reopening a path after validation.
func Verify(path, engine, host string) (Manifest, []byte, error) {
	var m Manifest
	fail := func(message string) (Manifest, []byte, error) { return m, nil, fmt.Errorf("%s", message) }
	parent, err := os.Lstat(filepath.Dir(path))
	if os.IsNotExist(err) {
		return fail("module directory is missing")
	}
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return fail("module directory must be a regular directory")
	}
	for _, file := range []string{path, path + ".json"} {
		info, err := os.Lstat(file)
		if os.IsNotExist(err) {
			return fail("module or matching manifest is missing")
		}
		if err != nil || !info.Mode().IsRegular() {
			return fail("module package must contain regular files")
		}
	}
	f, err := os.Open(path + ".json")
	if err != nil {
		return m, nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	f.Close()
	if err != nil || len(b) > 65536 {
		return fail("module manifest is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(new(any)) != io.EOF {
		return fail("invalid module manifest")
	}
	if m.FormatVersion != FormatVersion || m.APIVersion != APIVersion || m.ProtocolVersion != 1 {
		return fail("module format, API or protocol mismatch")
	}
	switch engine {
	case "postgres", "oracle", "dm", "sqlserver":
	default:
		return fail("unsupported module engine")
	}
	if m.Engine != engine || m.Version == "" || m.Module != filepath.Base(path) {
		return fail("module engine or filename mismatch")
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH || m.Toolchain != runtime.Version() {
		return fail("module platform or Go toolchain mismatch")
	}
	if m.DecodedBytes <= 0 || m.DecodedBytes > MaxDecodedBytes {
		return fail("invalid decoded module size")
	}
	hostDigest, err := Digest(host)
	if err != nil {
		return m, nil, err
	}
	if m.HostSHA256 != hostDigest {
		return fail("module was built for a different host; install the matching module package")
	}
	f, err = os.Open(path)
	if err != nil {
		return m, nil, err
	}
	content, err := io.ReadAll(io.LimitReader(f, MaxModuleBytes+1))
	f.Close()
	if err != nil || len(content) > MaxModuleBytes {
		return fail("module is too large")
	}
	digest := sha256.Sum256(content)
	if m.SHA256 != hex.EncodeToString(digest[:]) {
		return fail("module SHA-256 does not match its manifest")
	}
	return m, content, nil
}
