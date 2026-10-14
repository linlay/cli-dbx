package driverprocess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/linlay/cli-dbx/internal/conn"
	"golang.org/x/sys/windows"
)

func init() {
	if dir := os.Getenv("DBX_LIFETIME_TEST_PARENT"); dir != "" {
		os.Unsetenv("DBX_LIFETIME_TEST_PARENT")
		os.Setenv("DBX_PROCESS_TEST_CHILD", "1")
		_, _ = Call(context.Background(), conn.Spec{Engine: "postgres", DriverDir: dir, Timeout: time.Minute}, Request{Operation: "ping"})
		os.Exit(1) // The test kills the parent while the worker ignores cancellation.
	}
}

func TestWindowsParentTerminationKillsUncooperativeWorker(t *testing.T) {
	spec := installTestDriver(t)
	ready := filepath.Join(t.TempDir(), "worker.pid")
	t.Setenv("DBX_LIFETIME_TEST_PARENT", spec.DriverDir)
	t.Setenv("DBX_PROCESS_TEST_STUBBORN", ready)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(exe)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(ready); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if pid != 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("worker did not receive request")
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	defer windows.TerminateProcess(child, 1) // Only this test's held worker handle.
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	status, err := windows.WaitForSingleObject(child, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("worker survived parent termination: status=%d err=%v", status, err)
	}
}
