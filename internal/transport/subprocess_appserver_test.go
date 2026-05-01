package transport

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSplitKV(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in     string
		wantK  string
		wantV  string
		wantOK bool
	}{
		{"FOO=bar", "FOO", "bar", true},
		{"FOO=bar=baz", "FOO", "bar=baz", true},
		{"FOO=", "FOO", "", true},
		{"NOEQ", "NOEQ", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			k, v, ok := splitKV(tt.in)
			if k != tt.wantK || v != tt.wantV || ok != tt.wantOK {
				t.Fatalf("got (%q,%q,%v), want (%q,%q,%v)", k, v, ok, tt.wantK, tt.wantV, tt.wantOK)
			}
		})
	}
}

func TestBuildEnv_AddsAndOverrides(t *testing.T) {
	t.Parallel()
	env := buildEnv([]string{"CODEX_TEST_KEY=abc123", "PATH=/override"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CODEX_TEST_KEY=abc123") {
		t.Fatal("expected CODEX_TEST_KEY override to be present")
	}
	// PATH from os.Environ must be overridden.
	pathCount := 0
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			pathCount++
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected exactly one PATH entry, got %d", pathCount)
	}
	// And the surviving PATH must be our override.
	var gotPath string
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			gotPath = e
			break
		}
	}
	if gotPath != "PATH=/override" {
		t.Fatalf("PATH = %q, want %q", gotPath, "PATH=/override")
	}
}

func TestBuildEnv_EmptyValueUnsets(t *testing.T) {
	t.Parallel()
	env := buildEnv([]string{"PATH="})
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			t.Fatalf("PATH should be unset, still have: %q", e)
		}
	}
}

func TestRingBuffer_GrowsToCapThenWrapsAround(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(8)
	rb.Write([]byte("ABCDE"))
	if got := rb.String(); got != "ABCDE" {
		t.Fatalf("phase-1: %q", got)
	}
	rb.Write([]byte("FGHIJKL")) // total would be ABCDEFGHIJKL, ring keeps last 8
	if got := rb.String(); got != "EFGHIJKL" {
		t.Fatalf("phase-2 (ring wrap): %q, want %q", got, "EFGHIJKL")
	}
	rb.Write([]byte("MNOP"))
	if got := rb.String(); got != "IJKLMNOP" {
		t.Fatalf("phase-3 (continued wrap): %q, want %q", got, "IJKLMNOP")
	}
}

func TestRingBuffer_WriteLargerThanSize(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(4)
	rb.Write([]byte("ABCDEFGHIJ"))
	if got := rb.String(); got != "GHIJ" {
		t.Fatalf("got %q, want %q", got, "GHIJ")
	}
}

func TestRingBuffer_EmptyString(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(4)
	if got := rb.String(); got != "" {
		t.Fatalf("empty ring buffer should be empty string, got %q", got)
	}
}

// sanity: ringBuffer implements io.Writer
func TestRingBuffer_AsWriter(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(16)
	b := bytes.NewBufferString("hello world")
	n, err := b.WriteTo(rb)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("WriteTo wrote %d, want 11", n)
	}
	if rb.String() != "hello world" {
		t.Fatalf("got %q", rb.String())
	}
}

func TestAppServerClassifyExitShutdownRequestedSuppressesExitError(t *testing.T) {
	t.Parallel()
	err := exec.Command("sh", "-c", "exit 7").Run()
	if err == nil {
		t.Fatal("expected command to fail")
	}

	app := &AppServer{}
	if got := app.classifyExit(err, true); got != nil {
		t.Fatalf("classifyExit(shutdownRequested=true) = %v, want nil", got)
	}
	if got := app.classifyExit(err, false); got == nil || !strings.Contains(got.Error(), "exit=7") {
		t.Fatalf("classifyExit(shutdownRequested=false) = %v, want process error with exit=7", got)
	}
}
