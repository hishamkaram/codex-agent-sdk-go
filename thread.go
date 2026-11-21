package codex

import (
	"cmp"
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/session"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadconfig"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Thread is a handle to a single codex conversation. Construct via
// Client.StartThread or Client.ResumeThread. Operations across handles for
// the same provider thread ID share admission in the Client.
type Thread struct {
	client *Client
	id     string
	// model is the effective model returned by thread/start, thread/resume, or
	// thread/fork. It reflects the CLI-resolved default when callers omit one.
	model string
	// cwd is the working directory the thread was started with.
	// Populated by StartThread / ResumeThread / ForkThread from
	// ThreadOptions.Cwd (falls back to Client.opts.DefaultCwd).
	// Used by local-only helpers like Thread.GitDiff; empty string
	// means "no cwd known" — helpers that need one will error.
	cwd string

	record *threadexec.Record
}

// ID returns the server-assigned thread identifier.
func (t *Thread) ID() string { return t.id }

// Model returns the effective model selected by the Codex app-server for this
// thread. Empty means the connected CLI did not report one.
func (t *Thread) Model() string { return t.model }

// Cwd returns the working directory known to the SDK for this thread.
// Empty means no working directory was supplied or verified.
func (t *Thread) Cwd() string { return t.cwd }

// validateThreadWebSearchMode checks managed constraints before Codex can
// substitute its own allowed mode for the SDK's requested mode.
func (c *Client) validateThreadWebSearchMode(ctx context.Context, mode types.WebSearchMode) error {
	if mode == "" {
		return nil
	}
	requirements, err := c.ReadConfigRequirements(ctx)
	if err != nil {
		return err
	}
	return threadconfig.ValidateWebSearchMode(mode, requirements)
}

type threadResumeRequest struct {
	params          map[string]any
	cwd             string
	reasoningEffort string
}

func (c *Client) prepareThreadResume(ctx context.Context, threadID string, opts *types.ResumeOptions) (threadResumeRequest, error) {
	if opts == nil {
		opts = &types.ResumeOptions{}
	}
	overrides := threadconfig.ResolveResumeOverrides(c.opts, opts)
	if err := c.validateThreadWebSearchMode(ctx, overrides.WebSearchMode); err != nil {
		return threadResumeRequest{}, err
	}
	params := overrides.Params(threadID, opts.Cwd)
	if overrides.ReasoningEffort != "" {
		model, err := c.readResumeModel(ctx, threadID)
		if err != nil {
			return threadResumeRequest{}, err
		}
		params["model"] = model
	}
	return threadResumeRequest{params: params, cwd: cmp.Or(opts.Cwd, c.opts.DefaultCwd), reasoningEffort: overrides.ReasoningEffort}, nil
}

// An effort override disables Codex's normal model restoration. Ask the
// provider for the loaded or persisted model before building the resume.
func (c *Client) readResumeModel(ctx context.Context, threadID string) (string, error) {
	result, err := c.sendRaw(ctx, "ReadThread", "thread/read", map[string]any{
		"threadId": threadID, "includeTurns": false,
	})
	if err != nil {
		return "", err
	}
	return threadconfig.DecodeResumeModel(result, threadID)
}

// appServerWorkingDirectory reads the launch directory from the transport
// retained by this connection. Close may clear that transport concurrently.
func (c *Client) appServerWorkingDirectory() string { return c.session.WorkingDirectory() }

func (c *Client) validateThreadApprovalPolicy(opts *types.ThreadOptions) error {
	policy := c.opts.DefaultApprovalPolicy
	if opts != nil && opts.ApprovalPolicy != "" {
		policy = opts.ApprovalPolicy
	}
	return c.validateApprovalPolicy(policy)
}

func (c *Client) validateApprovalPolicy(policy types.ApprovalPolicy) error {
	return c.session.ValidateApprovalPolicy(policy)
}

// StartThread sends thread/start and returns a new Thread. The thread is
// registered in the client's routing table.
func (c *Client) StartThread(ctx context.Context, opts *types.ThreadOptions) (*Thread, error) {
	if c.session.Closed() {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", types.ErrClientNotConnected)
	}
	if err := c.validateThreadApprovalPolicy(opts); err != nil {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	request, err := threadconfig.BuildCreation(c.opts, opts, c.appServerWorkingDirectory())
	if err != nil {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	if policyErr := c.validateThreadWebSearchMode(ctx, request.WebSearchMode); policyErr != nil {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", policyErr)
	}
	handle, err := c.session.StartThread(ctx, request)
	if err != nil {
		return nil, err
	}
	return c.threadFromHandle(handle), nil
}

// ResumeThread applies optional resume overrides and returns a handle for
// the resumed conversation.
func (c *Client) ResumeThread(ctx context.Context, threadID string, opts *types.ResumeOptions) (*Thread, error) {
	if c.session.Closed() {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", types.ErrClientNotConnected)
	}
	if threadID == "" {
		return nil, fmt.Errorf("codex.Client.ResumeThread: threadID must not be empty")
	}
	request, err := c.prepareThreadResume(ctx, threadID, opts)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", err)
	}
	handle, err := c.session.ResumeThread(ctx, threadID, request.params, request.cwd, request.reasoningEffort)
	if err != nil {
		return nil, err
	}
	return c.threadFromHandle(handle), nil
}

// ForkThread branches from an existing thread, returning a new Thread whose
// history is seeded with the source thread. The source thread is
// unaffected. Uses "thread/fork" — if the server doesn't expose this verb
// in the current CLI version, the error is returned as-is.
func (c *Client) ForkThread(ctx context.Context, sourceThreadID string, opts *types.ThreadOptions) (*Thread, *types.ForkResult, error) {
	if c.session.Closed() {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", types.ErrClientNotConnected)
	}
	if sourceThreadID == "" {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: sourceThreadID must not be empty")
	}
	if err := c.validateThreadApprovalPolicy(opts); err != nil {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	request, err := threadconfig.BuildCreation(c.opts, opts, c.appServerWorkingDirectory())
	if err != nil {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	request.Params["threadId"] = sourceThreadID
	if policyErr := c.validateThreadWebSearchMode(ctx, request.WebSearchMode); policyErr != nil {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", policyErr)
	}
	handle, result, err := c.session.ForkThread(ctx, sourceThreadID, request)
	if err != nil {
		return nil, nil, err
	}
	return c.threadFromHandle(handle), result, nil
}

// ArchiveThread marks a thread as archived on the server and unregisters
// it locally.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	if c.session.Closed() {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", types.ErrClientNotConnected)
	}
	return c.session.ArchiveThread(ctx, threadID)
}

func (c *Client) threadFromHandle(handle session.ThreadHandle) *Thread {
	return &Thread{client: c, id: handle.ID, model: handle.Model, cwd: handle.Cwd, record: handle.Record}
}

// Rollback removes the last numTurns turns from this thread's history.
// It does not revert local file changes. numTurns must be at least 1.
//
// Codex runtimes that replaced thread/rollback with thread/revert require
// paginated history. On those runtimes, numTurns counts persisted provider
// turns and must not exceed the available history. Keep other clients from
// changing the same provider thread while a rollback is in progress. If
// the mutation's outcome is unconfirmed, use a fresh connection to inspect
// persisted history before attempting another rollback.
func (t *Thread) Rollback(ctx context.Context, numTurns int) error {
	const caller = "codex.Thread.Rollback"
	if numTurns < 1 {
		return fmt.Errorf("%s: numTurns must be >= 1, got %d", caller, numTurns)
	}
	if t.isClosed() {
		return fmt.Errorf("%s: %w", caller, types.ErrThreadClosed)
	}
	if err := t.client.checkApprovalReentry(ctx); err != nil {
		return fmt.Errorf("%s: %w", caller, err)
	}
	demux := t.client.session.Demux()
	if demux == nil || !t.client.session.Connected() {
		if t.client.session.Closed() {
			return fmt.Errorf("%s: %w", caller, types.ErrClientClosed)
		}
		return fmt.Errorf("%s: %w", caller, types.ErrClientNotConnected)
	}
	if err := t.record.Rollback(ctx, t.id, numTurns, t.executionWire(), demux.Send); err != nil {
		return fmt.Errorf("%s: %w", caller, err)
	}
	return nil
}

// SetName updates this thread's user-facing name. After the RPC ack
// (which is `{}`), codex emits a `thread/name/updated` notification —
// the SDK already parses that into *types.ThreadNameUpdated.
//
// Wire shape (verified live, returns empty `{}` ack):
//
//	{"threadId": "...", "name": "..."}
func (t *Thread) SetName(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("codex.Thread.SetName: name must not be empty")
	}
	if t.isClosed() {
		return fmt.Errorf("codex.Thread.SetName: %w", types.ErrThreadClosed)
	}
	_, err := t.client.sendRaw(ctx, "Thread.SetName", "thread/name/set", map[string]any{
		"threadId": t.id,
		"name":     name,
	})
	return err
}

// CleanBackgroundTerminals stops every background terminal session
// associated with this thread. Equivalent to TUI `/stop`.
//
// REQUIRES experimentalApi capability — call
// types.NewCodexOptions().WithExperimentalAPI(true) on the Client at
// construction time. Without it, this method returns
// *types.FeatureNotEnabledError.
func (t *Thread) CleanBackgroundTerminals(ctx context.Context) error {
	if t.isClosed() {
		return fmt.Errorf("codex.Thread.CleanBackgroundTerminals: %w", types.ErrThreadClosed)
	}
	err := t.client.CleanBackgroundTerminals(ctx, t.id)
	if err != nil && types.IsFeatureNotEnabledError(err) {
		// Augment with concrete remediation advice.
		return fmt.Errorf("%w (set CodexOptions.WithExperimentalAPI(true) at NewClient)", err)
	}
	return err
}

func (t *Thread) isClosed() bool { return t.record == nil || t.record.Closed() }
