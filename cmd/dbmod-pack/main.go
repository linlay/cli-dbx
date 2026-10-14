//go:build dbmod

// dbmod-pack runs only on the build machine. Modules contain selected object
// code and relocation metadata, never a second executable or Go compiler.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	format "github.com/linlay/cli-dbx/internal/dbmodformat"
	"github.com/linlay/cli-dbx/internal/dbmodule"
	"github.com/pkujhd/goloader"
	"github.com/pkujhd/goloader/link"
)

func main() {
	host := flag.String("host", "", "exact host executable")
	pkg := flag.String("package", "", "module package")
	out := flag.String("out", "", "compressed .dbmod")
	flag.Parse()
	if err := pack(*host, *pkg, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func pack(host, pkg, output string) error {
	if err := validateHost(host); err != nil {
		return err
	}
	initialHostHash, err := dbmodule.Digest(host)
	if err != nil {
		return err
	}
	symbols := make(map[string]uintptr)
	if err := goloader.RegSymbolWithPath(symbols, host); err != nil {
		return err
	}
	dbmodule.LocalizeConstants(symbols)
	tags := os.Getenv("DBX_MODULE_BUILD_TAGS")
	if tags == "" {
		tags = "dbmod"
	}
	cmd := exec.Command("go", "list", "-trimpath", "-tags="+tags, "-export", "-deps", "-json", pkg)
	cmd.Stderr = os.Stderr
	data, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	decoder := json.NewDecoder(data)
	var files, packages []string
	var entry string
	for {
		var item struct{ ImportPath, Export string }
		if err = decoder.Decode(&item); err == io.EOF {
			break
		} else if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		if item.Export != "" {
			files = append(files, item.Export)
			packages = append(packages, item.ImportPath)
			if item.ImportPath == pkg {
				entry = item.Export
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return err
	}
	if entry == "" {
		return fmt.Errorf("module export not found")
	}
	linker, err := goloader.ReadObj(entry, pkg)
	if err != nil {
		return err
	}
	unresolved := goloader.UnresolvedSymbols(linker, symbols)
	if err := goloader.ReadDependPackages(linker, files, packages, unresolved, symbols); err != nil {
		return err
	}
	if missing := goloader.UnresolvedSymbols(linker, symbols); len(missing) != 0 {
		return fmt.Errorf("unresolved module symbols: %v", missing)
	}
	// Only dependency order survives at runtime. Export symbol indexes and
	// source/cache paths are build inputs, not part of a loadable module.
	for _, item := range linker.Packages {
		item.Syms, item.CgoImports, item.GoArchive, item.SymIndex = nil, nil, nil, nil
		item.File, item.Arch, item.CUFiles, item.CUOffset = "", "", nil, 0
	}
	linker.ObjSymbolMap, linker.NameMap, linker.CgoFuncs = nil, nil, nil
	stats, err := link.Prelink((*link.Linker)(linker))
	if err != nil {
		return fmt.Errorf("prelink module: %w", err)
	}
	statsJSON, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(output+".stats.json", append(statsJSON, '\n'), 0644); err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := format.Encode((*link.Linker)(linker), &compact); err != nil {
		return err
	}
	restored, err := format.Decode(bytes.NewReader(compact.Bytes()))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(format.RuntimeOnly((*link.Linker)(linker)), restored) {
		return fmt.Errorf("compact module changed runtime graph")
	}
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	z, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	counter := &countWriter{writer: z}
	_, err = counter.Write(compact.Bytes())
	if e := z.Close(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if counter.n > dbmodule.MaxDecodedBytes {
		return fmt.Errorf("module exceeds decoded size limit")
	}
	content, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	if len(content) > dbmodule.MaxModuleBytes {
		return fmt.Errorf("module exceeds file size limit")
	}
	digest := sha256.Sum256(content)
	hostDigest, err := dbmodule.Digest(host)
	if err != nil {
		return err
	}
	if hostDigest != initialHostHash {
		return fmt.Errorf("host changed during module packaging")
	}
	version, err := os.ReadFile("VERSION")
	if err != nil {
		return err
	}
	m := dbmodule.Manifest{FormatVersion: dbmodule.FormatVersion, APIVersion: dbmodule.APIVersion, ProtocolVersion: 1,
		Engine: strings.TrimPrefix(pkg, "github.com/linlay/cli-dbx/modules/"), Version: strings.TrimSpace(string(version)),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Toolchain: runtime.Version(), HostSHA256: hostDigest,
		Module: filepath.Base(output), SHA256: hex.EncodeToString(digest[:]), DecodedBytes: counter.n}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output+".json", append(manifest, '\n'), 0644)
}

type countWriter struct {
	writer io.Writer
	n      int64
}

func (w *countWriter) Write(b []byte) (int, error) {
	n, err := w.writer.Write(b)
	w.n += int64(n)
	return n, err
}

// Matching names alone cannot establish ABI compatibility. The build entry
// additionally verifies a canonical rebuild from this source and dependency graph.
func validateHost(path string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read host Go build information: %w", err)
	}
	if info.GoVersion != runtime.Version() {
		return fmt.Errorf("host/tool Go toolchain mismatch")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return fmt.Errorf("module packaging requires the native host platform")
	}
	return nil
}
