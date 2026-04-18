// codex-sdk-hook-shim is a single-purpose subprocess binary that bridges
// Codex's subprocess hook handler model to a Go callback registered via
// codex-agent-sdk-go's WithHookCallback option.
//
// Codex spawns this binary as a "command" handler (via
// ~/.codex/hooks.json or a CODEX_HOME override written by the SDK). The
// shim reads the hook payload from stdin, forwards it to the SDK process
// over a Unix socket whose path comes from the CODEX_SDK_HOOK_SOCKET env
// var, waits for the SDK's response, writes the response JSON to stdout,
// and exits with the appropriate code (0 for allow, 2 for block).
//
// Protocol (length-prefixed JSON frames, big-endian uint32 length):
//
//	Shim → SDK: HookRequest { stdin string, env map[string]string, shim_version string }
//	SDK  → Shim: HookResponse { stdout string, stderr string, exit_code int }
//
// If CODEX_SDK_HOOK_SOCKET is unset OR the SDK socket is unreachable, the
// shim exits 0 with no stdout — this matches the "no decision" path codex
// interprets as allow. The shim NEVER blocks codex with a crash: any
// error path logs to stderr and exits 0.
//
// Install: go install github.com/hishamkaram/codex-agent-sdk-go/cmd/codex-sdk-hook-shim@latest
// Build locally: make shim (writes to ./.bin/codex-sdk-hook-shim)
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// ShimVersion is the protocol version the shim speaks. The SDK side may
// refuse unexpected versions.
const ShimVersion = "0.2.0"

// DialTimeout bounds how long the shim will wait to connect to the
// SDK-side socket. Short by design: codex's hook timeout is typically
// ~30s; the shim should fail fast.
const DialTimeout = 3 * time.Second

// IOTimeout bounds how long the shim waits for read/write during frame
// transfer.
const IOTimeout = 30 * time.Second

// HookRequest is what the shim sends to the SDK.
type HookRequest struct {
	ShimVersion string            `json:"shim_version"`
	Stdin       string            `json:"stdin"`
	Env         map[string]string `json:"env,omitempty"`
}

// HookResponse is what the SDK returns to the shim.
type HookResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

func main() {
	// Drain stdin without bound — hook payloads are typically small
	// (<10 KiB) but we shouldn't cap arbitrarily.
	stdinBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-sdk-hook-shim: read stdin: %v\n", err)
		os.Exit(0)
	}

	socket := os.Getenv("CODEX_SDK_HOOK_SOCKET")
	if socket == "" {
		// No SDK listener configured — nothing to do. Exit clean;
		// codex interprets no stdout + exit 0 as "allow".
		return
	}

	resp, err := callSDK(socket, stdinBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-sdk-hook-shim: bridge call failed: %v\n", err)
		// Fail open so we don't brick the user's codex run.
		return
	}

	if resp.Stdout != "" {
		_, _ = os.Stdout.WriteString(resp.Stdout)
	}
	if resp.Stderr != "" {
		_, _ = os.Stderr.WriteString(resp.Stderr)
	}
	os.Exit(resp.ExitCode)
}

// callSDK dials the Unix socket, writes the request, reads the response,
// and returns it. All errors are wrapped for diagnostics.
func callSDK(socket string, stdinBytes []byte) (*HookResponse, error) {
	conn, err := net.DialTimeout("unix", socket, DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %q: %w", socket, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(IOTimeout))

	// Build request.
	req := HookRequest{
		ShimVersion: ShimVersion,
		Stdin:       string(stdinBytes),
		// Future: forward env subset. For now only pass the SDK hook
		// request id if set.
		Env: envSubset("CODEX_SDK_HOOK_REQUEST_ID"),
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if err := writeFrame(conn, reqBytes); err != nil {
		return nil, fmt.Errorf("write frame: %w", err)
	}

	respBytes, err := readFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("read frame: %w", err)
	}
	var resp HookResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

// writeFrame writes a length-prefixed frame: uint32 BE length, then
// payload bytes.
func writeFrame(w io.Writer, payload []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	return nil
}

// readFrame reads a length-prefixed frame. Returns the payload bytes.
// Caps length at 16 MiB to prevent a hostile server from exhausting memory.
func readFrame(r io.Reader) ([]byte, error) {
	const maxFrame = 16 * 1024 * 1024
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > maxFrame {
		return nil, fmt.Errorf("frame too large: %d bytes (max %d)", n, maxFrame)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	return buf, nil
}

// envSubset returns a map of the named env vars (those that are set).
// Used to forward specific env keys without leaking the full env.
func envSubset(keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
