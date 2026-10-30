//go:build dbmod && windows

package dbmodule_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// An isolated hidden console lets the helper send a real console event without
// changing this test runner's console or signaling unrelated processes.
func TestWindowsModuleConsoleInterrupt(t *testing.T) {
	if os.Getenv("DBX_TEST_SHARED_HOST") == "" || os.Getenv("DBX_TEST_SHARED_MODULE") == "" {
		t.Skip("run scripts/build-shared-host.ps1 -Test")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsModuleConsoleInterruptHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "DBX_TEST_CONSOLE_INTERRUPT_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated console interrupt failed: %v\n%s", err, out)
	}
}

func TestWindowsModuleConsoleInterruptHelper(t *testing.T) {
	if os.Getenv("DBX_TEST_CONSOLE_INTERRUPT_HELPER") != "1" {
		t.Skip("isolated console helper")
	}
	cmd, trace := fixtureCommand(t, "interrupt_prepare", "query", "fixture", "select 1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
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
			t.Fatal("module Prepare never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("console interrupt returned success")
		}
		decodeOneEnvelope(t, out.Bytes())
		if !strings.Contains(out.String(), "context canceled") {
			t.Fatalf("expected console cancellation, got %s; stderr=%s", out.String(), stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI remained blocked after console interrupt")
	}
}
