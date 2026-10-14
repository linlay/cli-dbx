//go:build dbmod

package main

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestWorkerParentCloseDoesNotWaitForUncooperativeOperation(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	operation := make(chan struct{})
	defer close(operation)
	started := make(chan struct{})
	exited := make(chan error, 1)
	go func() {
		exited <- serveUntilParentCloses(context.Background(), reader, func(in io.Reader) error {
			close(started)
			go func() { var b [1]byte; in.Read(b[:]) }()
			<-operation // Simulate a vendor call that ignores context cancellation.
			return nil
		})
	}()
	<-started
	writer.Close()
	select {
	case err := <-exited:
		if err == nil {
			t.Fatal("parent closure was reported as success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker waited for the vendor after its parent disappeared")
	}
}
