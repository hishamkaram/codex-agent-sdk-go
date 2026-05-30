package hookbridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// TestNewSocketIsOwnerOnly verifies the hook bridge socket is created 0700
// (owner-only). The socket carries tool-approval (allow/deny) decisions, so a
// same-host non-owner must not be able to connect — especially when the socket
// lives outside the 0700 ~/.cache/codex-sdk dir (the /tmp sun_path-overflow
// fallback). Before the chmod, net.Listen left the socket at the process umask
// (commonly 0775, group-connectable).
func TestNewSocketIsOwnerOnly(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "hook.sock")

	ln, err := New(Config{
		SocketPath: socket,
		Handler:    types.DefaultAllowHookHandler,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("socket perm = %#o, want 0700 (owner-only)", perm)
	}
}
