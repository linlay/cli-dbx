// Command package-driver creates a deterministic, independently installable DBX driver package.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type manifest struct {
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

func main() {
	root := flag.String("root", ".", "repository root containing VERSION")
	binary := flag.String("binary", "", "compiled driver path")
	engine := flag.String("engine", "", "postgres, oracle, dm, or sqlserver")
	goos := flag.String("os", "", "target operating system")
	arch := flag.String("arch", "", "target architecture")
	out := flag.String("output-dir", "", "archive directory; default dist/<version>")
	adapter := flag.String("adapter", "native", "native or odbc")
	resources := flag.String("resources", "", "flat directory of companion runtime files")
	flag.Parse()
	version, err := os.ReadFile(filepath.Join(*root, "VERSION"))
	if err == nil {
		v := strings.TrimSpace(string(version))
		if *out == "" {
			*out = filepath.Join(*root, "dist", v)
		}
		var archive string
		archive, err = packageDriverVariant(*binary, *out, *engine, v, *goos, *arch, *adapter, *resources)
		if err == nil {
			fmt.Println(archive)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

type packageFile struct {
	name string
	body []byte
	mode int64
}

func packageDriver(binary, outputDir, engine, version, goos, arch string) (string, error) {
	return packageDriverVariant(binary, outputDir, engine, version, goos, arch, "native", "")
}

func packageDriverVariant(binary, outputDir, engine, version, goos, arch, adapter, resources string) (string, error) {
	if adapter == "" {
		adapter = "native"
	}
	if adapter != "native" && adapter != "odbc" {
		return "", fmt.Errorf("unsupported adapter %q", adapter)
	}
	if adapter == "odbc" && (engine != "oracle" && engine != "dm" && engine != "sqlserver") {
		return "", fmt.Errorf("unsupported ODBC engine %q", engine)
	}
	if adapter == "odbc" && goos == "windows" && arch != "amd64" {
		return "", fmt.Errorf("ODBC Windows packages require amd64")
	}
	if adapter == "native" && resources != "" {
		return "", fmt.Errorf("native packages do not use companion runtime files")
	}
	switch engine {
	case "postgres", "oracle", "dm", "sqlserver":
	default:
		return "", fmt.Errorf("unsupported external driver %q", engine)
	}
	if !releaseVersion.MatchString(version) {
		return "", fmt.Errorf("invalid release version %q", version)
	}
	switch goos + "/" + arch {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64":
	default:
		return "", fmt.Errorf("unsupported target %s/%s", goos, arch)
	}
	info, err := os.Lstat(binary)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("driver binary must be a nonempty regular file")
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	packageEngine := engine
	if adapter == "odbc" {
		packageEngine += "-odbc"
	}
	exe, ext := "dbx-driver-"+packageEngine+"-"+strings.TrimPrefix(version, "v"), ".tar.gz"
	if goos == "windows" {
		exe += ".exe"
		ext = ".zip"
	}
	sum := sha256.Sum256(b)
	manifestData := manifest{ProtocolVersion: 1, Engine: engine, Version: version, OS: goos, Arch: arch, Executable: exe, SHA256: hex.EncodeToString(sum[:])}
	if adapter == "odbc" {
		manifestData.Adapter = adapter
	}
	resourceEntries := []packageFile{}
	if resources != "" {
		items, err := os.ReadDir(resources)
		if err != nil {
			return "", err
		}
		manifestData.Files = map[string]string{}
		for _, item := range items {
			name := item.Name()
			if name == exe || name == exe+".json" || strings.ContainsAny(name, "/\\:") {
				return "", fmt.Errorf("invalid runtime resource name")
			}
			info, err := item.Info()
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("runtime resources must be flat regular files")
			}
			content, err := os.ReadFile(filepath.Join(resources, name))
			if err != nil {
				return "", err
			}
			digest := sha256.Sum256(content)
			manifestData.Files[name] = hex.EncodeToString(digest[:])
			mode := int64(0644)
			if info.Mode().Perm()&0111 != 0 {
				mode = 0755
			}
			resourceEntries = append(resourceEntries, packageFile{name, content, mode})
		}
	}
	m, err := json.MarshalIndent(manifestData, "", "  ")
	if err != nil {
		return "", err
	}
	entries := []packageFile{{exe, b, 0755}, {exe + ".json", append(m, '\n'), 0644}}
	entries = append(entries, resourceEntries...)
	var archive bytes.Buffer
	// Fixed timestamps, order, ownership, and modes make identical inputs reproducible.
	stamp := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	if goos == "windows" {
		zw := zip.NewWriter(&archive)
		for _, entry := range entries {
			h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: stamp}
			h.SetMode(os.FileMode(entry.mode))
			w, err := zw.CreateHeader(h)
			if err != nil {
				return "", err
			}
			if _, err := w.Write(entry.body); err != nil {
				return "", err
			}
		}
		if err := zw.Close(); err != nil {
			return "", err
		}
	} else {
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		for _, entry := range entries {
			h := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), ModTime: stamp, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
			if err := tw.WriteHeader(h); err != nil {
				return "", err
			}
			if _, err := tw.Write(entry.body); err != nil {
				return "", err
			}
		}
		if err := tw.Close(); err != nil {
			return "", err
		}
		if err := gz.Close(); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("dbx-driver-%s_%s_%s_%s%s", packageEngine, version, goos, arch, ext)
	path := filepath.Join(outputDir, name)
	if err := os.WriteFile(path, archive.Bytes(), 0644); err != nil {
		return "", err
	}
	archiveSum := sha256.Sum256(archive.Bytes())
	checksum := hex.EncodeToString(archiveSum[:]) + "  " + name + "\n"
	if err := os.WriteFile(path+".sha256", []byte(checksum), 0644); err != nil {
		return "", err
	}
	return path, nil
}
