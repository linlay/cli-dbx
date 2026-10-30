package dm

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLogFileFailureDoesNotPolluteCLIStdout(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	oldLog, oldStat := LogDir, StatDir
	LogDir, StatDir = blocked+string(os.PathSeparator), blocked+string(os.PathSeparator)
	defer func() { LogDir, StatDir = oldLog, oldStat }()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	oldStdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout; writer.Close() }()
	logFile := new(logWriter).createNewFile()
	statFile := new(statFlusher).createNewFile()
	os.Stdout = oldStdout
	writer.Close()
	output, err := io.ReadAll(reader)
	if err != nil || logFile != nil || statFile != nil || len(output) != 0 {
		t.Fatalf("optional log failure must stay off CLI stdout: output=%q err=%v", output, err)
	}
}
