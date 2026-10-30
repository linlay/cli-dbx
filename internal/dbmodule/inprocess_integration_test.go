//go:build dbmod

package dbmodule_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestModuleRunsInCLIProcessAndInitializesOnce(t *testing.T) {
	cmd, trace := fixtureCommand(t, "", "inspect", "schema", "fixture")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("inspect: %v output=%s", err, &out)
	}
	decodeOneEnvelope(t, out.Bytes())
	content, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("initialize %d\n%s", cmd.Process.Pid, strings.Repeat(fmt.Sprintf("prepare %d\n", cmd.Process.Pid), 3))
	if string(content) != want {
		t.Fatalf("module must initialize once in CLI pid %d; trace:\n%s", cmd.Process.Pid, content)
	}
}

func TestModuleCancellationIncludesUncooperativeInitializationAndPrepare(t *testing.T) {
	for _, behavior := range []string{"block_initialize", "block_prepare"} {
		t.Run(behavior, func(t *testing.T) {
			cmd, trace := fixtureCommand(t, behavior, "query", "fixture", "select 1")
			start := time.Now()
			out, err := cmd.Output()
			if err == nil || time.Since(start) > 4*time.Second {
				t.Fatalf("unbounded module cancellation: err=%v output=%s", err, out)
			}
			decodeOneEnvelope(t, out)
			content, err := os.ReadFile(trace)
			if err != nil || !strings.Contains(string(content), "initialize ") || (behavior == "block_prepare" && !strings.Contains(string(content), "prepare ")) {
				t.Fatalf("operation never reached blocking fixture: %s %v", content, err)
			}
		})
	}
}

func TestModulePanicIsSanitized(t *testing.T) {
	cmd, _ := fixtureCommand(t, "panic_prepare", "query", "fixture", "select 1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		t.Fatal("panic returned success")
	}
	decodeOneEnvelope(t, out)
	if bytes.Contains(out, []byte("private credential")) || strings.Contains(stderr.String(), "private credential") {
		t.Fatalf("panic leaked its value: stdout=%s stderr=%s", out, &stderr)
	}
}

func TestModuleInterruptExitsUncooperativePrepare(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not implement Process.Signal(os.Interrupt)")
	}
	cmd, trace := fixtureCommand(t, "interrupt_prepare", "query", "fixture", "select 1")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(4 * time.Second)
	for {
		content, _ := os.ReadFile(trace)
		if bytes.Contains(content, []byte("prepare ")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prepare did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupt returned success")
		}
		decodeOneEnvelope(t, out.Bytes())
		if !bytes.Contains(out.Bytes(), []byte("context canceled")) {
			t.Fatalf("interrupt must cancel the operation, not expire its timeout: %s", &out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI remained blocked after interrupt")
	}
}

func fixtureCommand(t *testing.T, behavior string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	host, module := os.Getenv("DBX_TEST_SHARED_HOST"), os.Getenv("DBX_TEST_SHARED_MODULE")
	if host == "" || module == "" {
		t.Skip("run scripts/build-shared-host.sh --test or .ps1 -Test")
	}
	dir := t.TempDir()
	config, trace := filepath.Join(dir, "fixture.toml"), filepath.Join(dir, "trace")
	timeout := "10s"
	if strings.HasPrefix(behavior, "block_") {
		timeout = "500ms"
	}
	body := fmt.Sprintf("[connection]\nengine='postgres'\ndriver_path=%q\nhost='127.0.0.1'\nuser='fixture'\ndatabase=%q\nsslmode='disable'\nallow_actions=['query','update','schema']\ntimeout=%q\n", module, filepath.Join(dir, "fixture.db"), timeout)
	if err := os.WriteFile(config, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, host, append(args, "--config", config)...)
	cmd.Env = append(os.Environ(), "DBX_TEST_MODULE_TRACE="+trace, "DBX_TEST_MODULE_BEHAVIOR="+behavior)
	return cmd, trace
}

func decodeOneEnvelope(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(out))
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("invalid envelope: %s: %v", out, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("unexpected output after envelope: %s: %v", out, err)
	}
	return result
}
