package transport

import (
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestExpandHome(t *testing.T) {
	t.Parallel()
	usr, err := user.Current()
	if err != nil {
		t.Skipf("user.Current failed: %v", err)
	}

	tests := []struct {
		in   string
		want string
	}{
		{"~", usr.HomeDir},
		{"~/.codex/bin/codex", filepath.Join(usr.HomeDir, ".codex/bin/codex")},
		{"/absolute/no/tilde", "/absolute/no/tilde"},
		{"~NotAtilde", "~NotAtilde"}, // ~foo should not expand
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got := expandHome(tt.in)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindCLI_InPATH(t *testing.T) {
	resetCLIDiscoveryCache(t)
	cliPath := writeFakeCodexCLI(t, t.TempDir())
	t.Setenv("PATH", filepath.Dir(cliPath))

	path, err := FindCLI()
	if err != nil {
		t.Fatalf("FindCLI() error = %v", err)
	}
	if path != cliPath {
		t.Fatalf("FindCLI() = %q, want %q", path, cliPath)
	}
}

func TestFindCLI_NotFound(t *testing.T) {
	resetCLIDiscoveryCache(t)
	withFallbackLocations(t, nil)
	t.Setenv("PATH", t.TempDir())

	path, err := FindCLI()
	if err == nil {
		t.Fatalf("FindCLI() = %q, want error", path)
	}
	if !types.IsCLINotFoundError(err) {
		t.Fatalf("FindCLI() error = %T %[1]v, want CLINotFoundError", err)
	}
}

func TestFindCLI_FailedLookupIsNotCached(t *testing.T) {
	resetCLIDiscoveryCache(t)
	withFallbackLocations(t, nil)
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	if path, err := FindCLI(); err == nil {
		t.Fatalf("first FindCLI() = %q, want error", path)
	} else if !types.IsCLINotFoundError(err) {
		t.Fatalf("first FindCLI() error = %T %[1]v, want CLINotFoundError", err)
	}

	cliPath := writeFakeCodexCLI(t, dir)
	path, err := FindCLI()
	if err != nil {
		t.Fatalf("second FindCLI() error = %v", err)
	}
	if path != cliPath {
		t.Fatalf("second FindCLI() = %q, want %q", path, cliPath)
	}
}

func TestFindCLI_ConcurrentCallsShareDiscovery(t *testing.T) {
	var cache cliDiscoveryCache
	wantPath := filepath.Join(t.TempDir(), "codex")
	const callers = 32

	var discoveries atomic.Int64
	// Typed as cliDiscoverFunc so the (string, error) shape is dictated by
	// the cache.find contract; this stub exercises only the success path.
	var discover cliDiscoverFunc = func() (string, error) {
		discoveries.Add(1)
		time.Sleep(50 * time.Millisecond)
		return wantPath, nil
	}

	start := make(chan struct{})
	paths := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			paths[i], errs[i] = cache.find(discover)
		}()
	}
	close(start)
	wg.Wait()

	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d error = %v", i, errs[i])
		}
		if paths[i] != wantPath {
			t.Fatalf("caller %d path = %q, want %q", i, paths[i], wantPath)
		}
	}
	if got := discoveries.Load(); got != 1 {
		t.Fatalf("discoveries after concurrent calls = %d, want 1", got)
	}

	path, err := cache.find(discover)
	if err != nil {
		t.Fatalf("cached find() error = %v", err)
	}
	if path != wantPath {
		t.Fatalf("cached find() = %q, want %q", path, wantPath)
	}
	if got := discoveries.Load(); got != 1 {
		t.Fatalf("discoveries after cached call = %d, want 1", got)
	}
}

func resetCLIDiscoveryCache(t *testing.T) {
	t.Helper()
	defaultCLIDiscoveryCache = cliDiscoveryCache{}
	t.Cleanup(func() {
		defaultCLIDiscoveryCache = cliDiscoveryCache{}
	})
}

func withFallbackLocations(t *testing.T, locations []string) {
	t.Helper()
	previous := cliFallbackLocations
	cliFallbackLocations = locations
	t.Cleanup(func() {
		cliFallbackLocations = previous
	})
}

func writeFakeCodexCLI(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatalf("write fake codex CLI: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake codex CLI: %v", err)
	}
	return path
}
