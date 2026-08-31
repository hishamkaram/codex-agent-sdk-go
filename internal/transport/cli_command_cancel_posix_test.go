//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package transport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCLICommandCancellationStopsInheritedOutputProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	startedPath := filepath.Join(t.TempDir(), "started")
	writeExecutableFixture(t, path, fmt.Sprintf(`#!/bin/sh
sleep 30 &
printf started > %q
wait
`, startedPath))

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := RunCLICommand(ctx, path, nil, 2*time.Second, "--version")
		result <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(startedPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-result
			t.Fatal("fake CLI did not start its inherited-output child")
		}
		time.Sleep(5 * time.Millisecond)
	}

	started := time.Now()
	cancel()
	err := <-result
	if err == nil {
		t.Fatal("RunCLICommand() error = nil after cancellation")
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("RunCLICommand() took %v after cancellation", elapsed)
	}
}
