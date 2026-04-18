package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/hookbridge"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// Client is the top-level entry point to the Codex SDK. A Client owns a
// single `codex app-server` subprocess and exposes thread lifecycle + turn
// execution via the Thread type.
//
// Create with NewClient, then call Connect once before any thread
// operations. Close on shutdown — the Client never reconnects.
type Client struct {
	opts   *types.CodexOptions
	logger *sdklog.Logger
	tr     *transport.AppServer
	demux  *jsonrpc.Demux

	// InitializeResult from the handshake. Populated during Connect.
	initResult InitializeResult

	mu      sync.Mutex
	threads map[string]*Thread

	// Dispatcher lifecycle.
	dispatcherCtx    context.Context
	dispatcherCancel context.CancelFunc
	dispatcherDone   chan struct{}

	// Hook bridge — populated only when HookCallback is registered.
	hookListener *hookbridge.Listener
	// hookHooksJSONPath is the absolute path to the user's
	// ~/.codex/hooks.json that the SDK overwrote during Connect.
	hookHooksJSONPath string
	// hookBackupPath, when non-empty, is the absolute path to the byte-for-byte
	// backup of the user's pre-Connect hooks.json. Empty when no hooks.json
	// existed before Connect (Close removes the SDK-written hooks.json instead).
	hookBackupPath string
	// hookHadUserConfig records whether a hooks.json existed at Connect time.
	hookHadUserConfig bool

	connected atomic.Bool
	closed    atomic.Bool
}

// InitializeResult is the response payload from the `initialize` RPC,
// exposed for callers that want to inspect the server's environment.
type InitializeResult struct {
	UserAgent      string `json:"userAgent,omitempty"`
	CodexHome      string `json:"codexHome,omitempty"`
	PlatformFamily string `json:"platformFamily,omitempty"`
	PlatformOs     string `json:"platformOs,omitempty"`
}

// NewClient constructs a Client. Options must be non-nil. The returned
// Client is NOT yet connected — call Connect before any thread calls.
func NewClient(ctx context.Context, opts *types.CodexOptions) (*Client, error) {
	if opts == nil {
		return nil, fmt.Errorf("codex.NewClient: opts must not be nil")
	}
	logger := sdklog.NewLoggerFromZap(opts.Logger)
	if opts.Logger == nil && opts.Verbose {
		logger = sdklog.NewLogger(true)
	}
	return &Client{
		opts:    opts,
		logger:  logger,
		threads: make(map[string]*Thread),
	}, nil
}

// Connect spawns the subprocess, sends the initialize RPC, waits for the
// response, and sends the initialized notification. Starts the dispatcher
// goroutine that routes inbound notifications to Thread inboxes.
//
// Calling Connect more than once returns an error — create a new Client
// for a new session.
func (c *Client) Connect(ctx context.Context) error {
	if !c.connected.CompareAndSwap(false, true) {
		return fmt.Errorf("codex.Client.Connect: already connected")
	}
	if c.closed.Load() {
		return fmt.Errorf("codex.Client.Connect: client is closed")
	}

	// v0.3.0: hook-bridge auto-wiring is end-to-end. When HookCallback is
	// set, the SDK starts a Unix socket listener under
	// ~/.cache/codex-sdk/, backs up the user's ~/.codex/hooks.json (if
	// any), and writes a generated hooks.json that points codex at the
	// shim. Close restores the user's original config byte-for-byte. See
	// setupHookBridge for the full lifecycle.
	extraEnv := append([]string(nil), c.opts.Env...)
	if c.opts.HookCallback != nil {
		if err := c.setupHookBridge(&extraEnv); err != nil {
			return fmt.Errorf("codex.Client.Connect: hook bridge: %w", err)
		}
	}

	c.tr = transport.NewAppServer(transport.AppServerConfig{
		CLIPath:        c.opts.CLIPath,
		ExtraArgs:      c.opts.ExtraArgs,
		Env:            extraEnv,
		Logger:         c.logger,
		ReadBufferSize: c.opts.ReadBufferSize,
	})
	if err := c.tr.Connect(ctx); err != nil {
		return fmt.Errorf("codex.Client.Connect: transport: %w", err)
	}
	c.demux = c.tr.Demux()

	// Send initialize.
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    c.opts.ClientName,
			"version": c.opts.ClientVersion,
			"title":   c.opts.ClientTitle,
		},
		"capabilities": map[string]any{
			"experimentalApi": false,
		},
	}
	resp, err := c.demux.Send(ctx, "initialize", params)
	if err != nil {
		_ = c.tr.Close(context.Background())
		return fmt.Errorf("codex.Client.Connect: initialize: %w", err)
	}
	if resp.Error != nil {
		_ = c.tr.Close(context.Background())
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	if err := json.Unmarshal(resp.Result, &c.initResult); err != nil {
		c.logger.Warn("codex.Client.Connect: initialize response shape unrecognized",
			zap.Error(err))
	}

	// Send initialized notification.
	if err := c.demux.Notify("initialized", nil); err != nil {
		_ = c.tr.Close(context.Background())
		return fmt.Errorf("codex.Client.Connect: initialized: %w", err)
	}

	// Start dispatcher.
	c.dispatcherCtx, c.dispatcherCancel = context.WithCancel(context.Background())
	c.dispatcherDone = make(chan struct{})
	go c.dispatch()

	c.logger.Debug("codex client connected",
		zap.String("user_agent", c.initResult.UserAgent),
		zap.String("codex_home", c.initResult.CodexHome))
	return nil
}

// InitializeResult returns the response payload received during Connect.
// Only meaningful after Connect returns nil.
func (c *Client) InitializeResult() InitializeResult { return c.initResult }

// Close shuts down the dispatcher, closes every Thread's inbox, and
// terminates the subprocess with the 3-stage graceful-shutdown ladder.
// Safe to call multiple times.
func (c *Client) Close(ctx context.Context) error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.dispatcherCancel != nil {
		c.dispatcherCancel()
	}
	if c.dispatcherDone != nil {
		<-c.dispatcherDone
	}

	c.mu.Lock()
	for _, t := range c.threads {
		t.markClosed()
	}
	c.threads = nil
	c.mu.Unlock()

	var trErr error
	if c.tr != nil {
		trErr = c.tr.Close(ctx)
	}
	// Tear down the hook bridge AFTER the transport is stopped so no
	// in-flight hook subprocess can still dial the socket.
	if c.hookListener != nil {
		_ = c.hookListener.Close()
	}
	// Restore the user's original ~/.codex/hooks.json (or remove the
	// SDK-written file when no original existed). Logged but never
	// fatal — Close must remain best-effort.
	c.restoreUserHooksJSON()
	return trErr
}

// hookBackupSuffix identifies SDK-written backup files of the user's
// hooks.json. The PID suffix lets stale-recovery detect crashed prior
// runs without colliding with a live concurrent SDK instance.
const hookBackupSuffix = ".sdk-backup"

// staleBackupAge is the age threshold past which a leftover backup file
// is treated as evidence of a crashed prior run rather than a live
// concurrent SDK instance.
const staleBackupAge = 60 * time.Second

// hooksJSONTimeoutSeconds is the per-hook timeout written into the
// generated hooks.json. MUST exceed c.opts.HookTimeout — the SDK's
// listener kills the callback first; codex's own timeout is the
// outer bound.
const hooksJSONTimeoutSeconds = 30

// setupHookBridge starts the Unix socket listener, resolves the shim
// binary, and installs the generated hooks.json so codex actually
// invokes the shim. Wires the listener path through
// CODEX_SDK_HOOK_SOCKET so the shim can dial back. Calls
// installHooksJSON which backs up the user's existing hooks.json (if
// any). Close calls restoreUserHooksJSON to undo the changes.
//
// On any error after the listener starts, this method tears the listener
// down so the caller can return cleanly.
func (c *Client) setupHookBridge(extraEnv *[]string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	cacheDir := filepath.Join(home, ".cache", "codex-sdk")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return fmt.Errorf("cache dir: %w", err)
	}
	socketPath := filepath.Join(cacheDir, fmt.Sprintf("hook-%d.sock", os.Getpid()))

	shimPath, err := resolveShimPath(c.opts.ShimPath)
	if err != nil {
		return err
	}

	ln, err := hookbridge.New(hookbridge.Config{
		SocketPath: socketPath,
		Handler:    c.opts.HookCallback,
		Timeout:    c.opts.HookTimeout,
		Logger:     c.logger,
	})
	if err != nil {
		return err
	}
	c.hookListener = ln

	if err := c.installHooksJSON(home, shimPath); err != nil {
		_ = ln.Close()
		c.hookListener = nil
		return err
	}

	*extraEnv = append(*extraEnv, "CODEX_SDK_HOOK_SOCKET="+socketPath)
	c.logger.Info("hook bridge ready",
		zap.String("shim", shimPath),
		zap.String("hooks_json", c.hookHooksJSONPath),
		zap.String("socket", socketPath),
		zap.Bool("backed_up_user_config", c.hookHadUserConfig))
	return nil
}

// installHooksJSON ensures ~/.codex/hooks.json points at the shim. If
// the user already has a hooks.json, it's copied byte-for-byte to a
// PID-suffixed backup that restoreUserHooksJSON consults on Close.
//
// Stale-recovery: if a backup exists from a crashed prior run
// (>staleBackupAge old), this method restores it before installing the
// generated config so the user's data is never lost across crashes.
//
// Concurrent-SDK detection: if a fresh (<staleBackupAge) backup exists
// from a different PID, returns an error rather than silently chaining
// backups (which would corrupt the user's original on Close). v0.3.0
// chose refuse-with-error over last-writer-wins to avoid silent data
// loss; merge-mode is on the v0.3.1 roadmap.
func (c *Client) installHooksJSON(home, shimPath string) error {
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		return fmt.Errorf("ensure codex dir: %w", err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	backupPath := filepath.Join(codexDir, fmt.Sprintf("hooks.json%s-%d", hookBackupSuffix, os.Getpid()))

	c.recoverStaleBackups(codexDir, hooksPath)
	if err := c.detectConcurrentSDK(codexDir); err != nil {
		return err
	}

	original, hadOriginal, err := readIfExists(hooksPath)
	if err != nil {
		return fmt.Errorf("read existing hooks.json: %w", err)
	}
	if hadOriginal {
		if err := os.WriteFile(backupPath, original, 0o600); err != nil {
			return fmt.Errorf("write hooks.json backup: %w", err)
		}
		c.hookBackupPath = backupPath
	}

	hooksJSON, err := hookbridge.GenerateHooksJSON(shimPath, hooksJSONTimeoutSeconds)
	if err != nil {
		// Roll back the backup we just wrote so we don't leave debris.
		if hadOriginal {
			_ = os.Remove(backupPath)
			c.hookBackupPath = ""
		}
		return fmt.Errorf("generate hooks.json: %w", err)
	}
	if err := os.WriteFile(hooksPath, hooksJSON, 0o600); err != nil {
		if hadOriginal {
			_ = os.Remove(backupPath)
			c.hookBackupPath = ""
		}
		return fmt.Errorf("write hooks.json: %w", err)
	}

	c.hookHooksJSONPath = hooksPath
	c.hookHadUserConfig = hadOriginal
	if hadOriginal {
		c.logger.Warn("overwrote ~/.codex/hooks.json for SDK lifetime; original backed up and will be restored on Close",
			zap.String("backup", backupPath))
	}
	return nil
}

// detectConcurrentSDK refuses to install hooks.json when a fresh
// (<staleBackupAge) backup file from a different PID exists in
// codexDir. Such a file means another live SDK Client is currently
// managing this hooks.json — chaining a second install would corrupt
// the user's original on Close because each Close restores from its own
// backup, and the LAST Close would write back the previous SDK's
// generated config instead of the user's true original.
func (c *Client) detectConcurrentSDK(codexDir string) error {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		return nil
	}
	prefix := "hooks.json" + hookBackupSuffix
	myPIDSuffix := fmt.Sprintf("-%d", os.Getpid())
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if strings.HasSuffix(e.Name(), myPIDSuffix) {
			continue // same PID — re-Connect within one process is its own bug; let it fail downstream
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) >= staleBackupAge {
			continue // stale — recoverStaleBackups already handled
		}
		return fmt.Errorf(
			"concurrent codex SDK Client detected (fresh backup at %s); "+
				"v0.3.0 supports only one HookCallback-enabled Client per machine — "+
				"close the other Client first or run without WithHookCallback",
			filepath.Join(codexDir, e.Name()))
	}
	return nil
}

// recoverStaleBackups looks for SDK backup files older than
// staleBackupAge in codexDir. A backup that old means a prior SDK run
// crashed before Close could restore. Restore the oldest such backup
// over hooks.json (so the user's original survives the crash) and then
// remove all stale backups. Live concurrent SDK runs (whose backups are
// fresher than staleBackupAge) are left alone.
func (c *Client) recoverStaleBackups(codexDir, hooksPath string) {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		return
	}
	prefix := "hooks.json" + hookBackupSuffix
	now := time.Now()
	type candidate struct {
		path  string
		mtime time.Time
	}
	var stale []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < staleBackupAge {
			continue
		}
		stale = append(stale, candidate{
			path:  filepath.Join(codexDir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	if len(stale) == 0 {
		return
	}
	// Restore the OLDEST stale backup — that's the one most likely to be
	// the user's true original (newer ones may themselves be SDK-written
	// configs that another crashed run backed up).
	oldest := stale[0]
	for _, s := range stale[1:] {
		if s.mtime.Before(oldest.mtime) {
			oldest = s
		}
	}
	data, err := os.ReadFile(oldest.path)
	if err != nil {
		c.logger.Warn("stale hooks.json backup found but unreadable; leaving in place",
			zap.String("path", oldest.path), zap.Error(err))
		return
	}
	if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
		c.logger.Warn("stale hooks.json backup found but restore failed",
			zap.String("path", oldest.path), zap.Error(err))
		return
	}
	c.logger.Warn("recovered hooks.json from stale SDK backup (prior SDK run crashed before Close)",
		zap.String("backup", oldest.path),
		zap.Duration("age", now.Sub(oldest.mtime)))
	for _, s := range stale {
		_ = os.Remove(s.path)
	}
}

// restoreUserHooksJSON is the Close-time inverse of installHooksJSON.
// If a backup exists, it's renamed back over hooks.json byte-for-byte.
// If no backup exists (user had no hooks.json before Connect), the
// SDK-written hooks.json is removed. Best-effort — failures are logged
// but never propagated.
func (c *Client) restoreUserHooksJSON() {
	if c.hookHooksJSONPath == "" {
		return
	}
	if c.hookBackupPath != "" {
		// Read backup, write back over hooks.json. We use read+write
		// (not rename) so a same-mountpoint guarantee isn't required.
		data, err := os.ReadFile(c.hookBackupPath)
		if err != nil {
			c.logger.Warn("hooks.json backup unreadable; leaving SDK-written config in place",
				zap.String("backup", c.hookBackupPath), zap.Error(err))
			return
		}
		if err := os.WriteFile(c.hookHooksJSONPath, data, 0o600); err != nil {
			c.logger.Warn("hooks.json restore failed; backup retained",
				zap.String("backup", c.hookBackupPath), zap.Error(err))
			return
		}
		_ = os.Remove(c.hookBackupPath)
		c.logger.Debug("restored user hooks.json from backup",
			zap.String("hooks_json", c.hookHooksJSONPath))
		return
	}
	// No prior config — remove what we wrote.
	if err := os.Remove(c.hookHooksJSONPath); err != nil && !os.IsNotExist(err) {
		c.logger.Warn("removing SDK-written hooks.json failed",
			zap.String("hooks_json", c.hookHooksJSONPath), zap.Error(err))
		return
	}
	c.logger.Debug("removed SDK-written hooks.json (no prior user config)",
		zap.String("hooks_json", c.hookHooksJSONPath))
}

// readIfExists returns the file's contents if present. If the file does
// not exist, returns (nil, false, nil). Other I/O errors are propagated.
func readIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

// resolveShimPath finds the codex-sdk-hook-shim binary. Order:
//  1. explicit ShimPath option
//  2. exec.LookPath (PATH)
//  3. $GOPATH/bin, $HOME/go/bin, ./.bin
func resolveShimPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("shim at %q: %w", explicit, err)
		}
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	if p, err := exec.LookPath("codex-sdk-hook-shim"); err == nil {
		return p, nil
	}
	// Fall-back search locations.
	var roots []string
	if gp := os.Getenv("GOPATH"); gp != "" {
		roots = append(roots, filepath.Join(gp, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "go", "bin"))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(cwd, ".bin"))
	}
	for _, root := range roots {
		candidate := filepath.Join(root, "codex-sdk-hook-shim")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("codex-sdk-hook-shim not found on PATH, $GOPATH/bin, $HOME/go/bin, or ./.bin (install: go install github.com/hishamkaram/codex-agent-sdk-go/cmd/codex-sdk-hook-shim@latest)")
}

// dispatch runs the event-routing goroutine. Reads notifications and
// server-initiated requests from the demux and fans them out.
func (c *Client) dispatch() {
	defer close(c.dispatcherDone)
	for {
		select {
		case <-c.dispatcherCtx.Done():
			return
		case note, ok := <-c.demux.Notifications():
			if !ok {
				return
			}
			c.handleNotification(note)
		case sreq, ok := <-c.demux.ServerRequests():
			if !ok {
				return
			}
			c.handleServerRequest(sreq)
		}
	}
}

func (c *Client) handleNotification(n jsonrpc.Notification) {
	ev, err := events.ParseEvent(n)
	if err != nil {
		c.logger.Warn("parse event failed",
			zap.String("method", n.Method),
			zap.Error(err))
		return
	}
	threadID := extractThreadIDFromEvent(ev)
	if threadID == "" {
		// Global events — configWarning, account/rateLimits/updated, etc.
		// Logged at debug; clients that want them must expose a hook in v1.1.
		c.logger.Debug("unroutable event (no thread_id)",
			zap.String("method", ev.EventMethod()))
		return
	}
	c.mu.Lock()
	t := c.threads[threadID]
	c.mu.Unlock()
	if t == nil {
		// Thread may not be registered yet (event arrived before
		// StartThread stored the Thread). Ignore — the spike transcript
		// showed mcpServer/startupStatus/updated arriving before thread/
		// started which we don't route anyway.
		return
	}
	t.deliverEvent(ev)
}

func (c *Client) handleServerRequest(sreq jsonrpc.ServerRequest) {
	req, err := events.ParseApprovalRequest(sreq.Method, sreq.Params)
	if err != nil {
		c.logger.Warn("parse server-request failed",
			zap.String("method", sreq.Method),
			zap.Error(err))
		_ = c.demux.RespondServerRequest(sreq.ID, nil, &jsonrpc.RPCError{
			Code:    -32000,
			Message: "client parse error: " + err.Error(),
		})
		return
	}
	cb := c.opts.ApprovalCallback
	if cb == nil {
		cb = types.DefaultDenyApprovalCallback
	}
	// Use dispatcherCtx so callbacks get canceled on Close.
	decision := cb(c.dispatcherCtx, req)
	result := events.EncodeApprovalDecision(decision)
	if err := c.demux.RespondServerRequest(sreq.ID, result, nil); err != nil {
		c.logger.Warn("approval response write failed",
			zap.String("method", sreq.Method),
			zap.Error(err))
	}
}

// registerThread stores a Thread in the client's routing table. The caller
// must own the thread's ID (i.e., thread/start or thread/resume succeeded).
func (c *Client) registerThread(t *Thread) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads != nil {
		c.threads[t.id] = t
	}
}

// unregisterThread removes a Thread from routing. Called when the thread
// is archived or the caller drops it.
func (c *Client) unregisterThread(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads != nil {
		delete(c.threads, threadID)
	}
}

// extractThreadIDFromEvent returns the ThreadID field of every event type
// that carries one. Returns "" for events that don't (e.g., raw
// ErrorEvent, UnknownEvent).
func extractThreadIDFromEvent(ev types.ThreadEvent) string {
	switch e := ev.(type) {
	case *types.ThreadStarted:
		return e.ThreadID
	case *types.TurnStarted:
		return e.ThreadID
	case *types.TurnCompleted:
		return e.ThreadID
	case *types.TurnFailed:
		return e.ThreadID
	case *types.ItemStarted:
		return e.ThreadID
	case *types.ItemUpdated:
		return e.ThreadID
	case *types.ItemCompleted:
		return e.ThreadID
	case *types.TokenUsageUpdated:
		return e.ThreadID
	case *types.ContextCompacted:
		return e.ThreadID
	case *types.HookStarted:
		return e.ThreadID
	case *types.HookCompleted:
		return e.ThreadID
	case *types.ThreadArchived:
		return e.ThreadID
	case *types.ThreadUnarchived:
		return e.ThreadID
	case *types.ThreadClosed:
		return e.ThreadID
	case *types.ThreadNameUpdated:
		return e.ThreadID
	case *types.ThreadStatusChanged:
		return e.ThreadID
	case *types.TurnDiffUpdated:
		return e.ThreadID
	case *types.TurnPlanUpdated:
		return e.ThreadID
	case *types.ItemGuardianApprovalReviewStarted:
		return e.ThreadID
	case *types.ItemGuardianApprovalReviewCompleted:
		return e.ThreadID
	case *types.ModelRerouted:
		return e.ThreadID
	case *types.ServerRequestResolved:
		return e.ThreadID
	case *types.ThreadRealtimeStarted:
		return e.ThreadID
	case *types.ThreadRealtimeClosed:
		return e.ThreadID
	case *types.ThreadRealtimeError:
		return e.ThreadID
	case *types.ThreadRealtimeItemAdded:
		return e.ThreadID
	case *types.ThreadRealtimeOutputAudioDelta:
		return e.ThreadID
	case *types.ThreadRealtimeSdp:
		return e.ThreadID
	case *types.ThreadRealtimeTranscriptDelta:
		return e.ThreadID
	case *types.ThreadRealtimeTranscriptDone:
		return e.ThreadID
	}
	return ""
}
