package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ThreadInboxBuffer bounds the queued events of a streamed provider operation.
const ThreadInboxBuffer = 256

// ErrNoActiveTurn is returned by Thread.Interrupt when no turn is currently
// running. Callers can treat it as a recoverable late interrupt.
var ErrNoActiveTurn = errors.New("codex: no active turn")

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

	*threadExecution
}

// newThread constructs a Thread owned by c. Register before exposing it.
func newThread(c *Client, id string) *Thread {
	return &Thread{client: c, id: id, threadExecution: newThreadExecution()}
}

// ID returns the server-assigned thread identifier.
func (t *Thread) ID() string { return t.id }

// Model returns the effective model selected by the Codex app-server for this
// thread. Empty means the connected CLI did not report one.
func (t *Thread) Model() string { return t.model }

// deliverEvent records state before exposing the event to stream readers.
func (t *Thread) deliverEvent(ev types.ThreadEvent) { t.observe(ev) }

func (t *Thread) markClosed() {
	if t.threadExecution != nil {
		t.close()
	}
}

// Interrupt cancels the currently-running turn (if any). Sends turn/interrupt
// to the server. Safe to call from any goroutine.
func (t *Thread) Interrupt(ctx context.Context) error {
	if t.closed.Load() {
		return fmt.Errorf("codex.Thread.Interrupt: %w", types.ErrThreadClosed)
	}
	tid, _ := t.activeTurnID.Load().(string)
	if tid == "" {
		return fmt.Errorf("codex.Thread.Interrupt: %w", ErrNoActiveTurn)
	}
	if err := t.client.InterruptThreadTurn(ctx, t.id, tid); err != nil {
		return fmt.Errorf("codex.Thread.Interrupt: %w", err)
	}
	return nil
}

// RunStreamed sends a user prompt and returns the matching provider events.
// The channel closes on completion, cancellation, or client shutdown. A
// canceled stream closes locally without waiting for provider termination.
// Use the supplied context to distinguish cancellation from completion.
func (t *Thread) RunStreamed(ctx context.Context, prompt string, opts *types.RunOptions) (<-chan types.ThreadEvent, error) {
	events, _, err := t.runStreamed(ctx, prompt, opts)
	return events, err
}

func (t *Thread) runStreamed(ctx context.Context, prompt string, opts *types.RunOptions) (<-chan types.ThreadEvent, *threadOperation, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	input, err := buildTurnInput(prompt, opts)
	if err != nil {
		return nil, nil, err
	}
	params := buildTurnStartParams(t.id, input, opts)
	op, _, err := t.beginOperation(ctx, executeTurn, true, "turn/start", params)
	if err != nil {
		return nil, nil, fmt.Errorf("codex.Thread.RunStreamed: turn/start: %w", err)
	}
	return t.streamOperation(ctx, op), op, nil
}

func (t *Thread) streamOperation(ctx context.Context, op *threadOperation) <-chan types.ThreadEvent {
	out := make(chan types.ThreadEvent, ThreadInboxBuffer)
	go t.streamLoop(ctx, op, out)
	return out
}

func (t *Thread) streamLoop(ctx context.Context, op *threadOperation, out chan<- types.ThreadEvent) {
	defer close(out)
	defer t.consumeDone(op)
	expected := op.ackID
	for {
		select {
		case <-ctx.Done():
			t.startExecutionCleanup(ctx, op, nil)
			return
		case <-t.done:
			return
		case ev, ok := <-op.events:
			if !ok {
				t.forwardStreamEvent(ctx, out, op.terminal)
				return
			}
			if expected == "" {
				if started, ok := ev.(*types.TurnStarted); ok && isRootTurn(started.TurnID, started.RootTurnID) {
					expected = started.TurnID
				}
			}
			id := turnIDFromStreamEvent(ev)
			if (expected == "" || id == "" || id == expected) && !t.forwardStreamEvent(ctx, out, ev) {
				t.startExecutionCleanup(ctx, op, nil)
				return
			}
		}
	}
}

func (t *Thread) forwardStreamEvent(ctx context.Context, out chan<- types.ThreadEvent, ev types.ThreadEvent) bool {
	if ev == nil {
		return true
	}
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
	case <-t.done:
	}
	return false
}

func turnIDFromStreamEvent(ev types.ThreadEvent) string {
	switch event := ev.(type) {
	case *types.TurnStarted:
		return event.TurnID
	case *types.ItemStarted:
		return event.TurnID
	case *types.ItemUpdated:
		return event.TurnID
	case *types.ItemCompleted:
		return event.TurnID
	case *types.TokenUsageUpdated:
		return event.TurnID
	case *types.TurnDiffUpdated:
		return event.TurnID
	case *types.TurnPlanUpdated:
		return event.TurnID
	case *types.TurnModerationMetadata:
		return event.TurnID
	case *types.ContextCompacted:
		return event.TurnID
	default:
		return turnIDFromAuxiliaryEvent(ev)
	}
}

func turnIDFromAuxiliaryEvent(ev types.ThreadEvent) string {
	switch event := ev.(type) {
	case *types.ItemGuardianApprovalReviewStarted:
		return event.TurnID
	case *types.ItemGuardianApprovalReviewCompleted:
		return event.TurnID
	case *types.FileChangePatchUpdated:
		return event.TurnID
	case *types.ModelRerouted:
		return event.TurnID
	case *types.ModelVerification:
		return event.TurnID
	case *types.ModelSafetyBufferingUpdated:
		return event.TurnID
	case *types.ErrorEvent:
		return event.TurnID
	case *types.UnknownEvent:
		return event.TurnID
	default:
		return ""
	}
}

// isTurnTerminus returns true if ev is TurnCompleted or TurnFailed whose
// TurnID matches expected (or if expected is empty, any terminal event).
func isTurnTerminus(ev types.ThreadEvent, expected string) bool {
	switch e := ev.(type) {
	case *types.TurnCompleted:
		return expected == "" || e.TurnID == expected
	case *types.TurnFailed:
		return expected == "" || e.TurnID == expected
	}
	return false
}

func turnIDFromTerminus(ev types.ThreadEvent) string {
	switch e := ev.(type) {
	case *types.TurnCompleted:
		return e.TurnID
	case *types.TurnFailed:
		return e.TurnID
	default:
		return ""
	}
}

// Turn is the buffered result of Thread.Run — all events accumulated
// in order, with convenience fields for final response text and usage.
type Turn struct {
	ID             string
	ThreadID       string
	Status         string
	Items          []types.ThreadItem
	Events         []types.ThreadEvent
	Usage          types.TokenUsage
	UsageAvailable bool   // Usage was measured for this turn, including a measured zero.
	FinalResponse  string // concatenation of every AgentMessage content
}

// Run is a buffered convenience wrapper over RunStreamed. It blocks until
// the turn completes (or fails / the context is canceled), accumulating
// every event into a *Turn.
func (t *Thread) Run(ctx context.Context, prompt string, opts *types.RunOptions) (*Turn, error) {
	events, op, err := t.runStreamed(ctx, prompt, opts)
	if err != nil {
		return nil, err
	}
	turn := &Turn{ID: op.ackID, ThreadID: t.id}
	var turnErr error
	for ev := range events {
		turn.Events = append(turn.Events, ev)
		switch e := ev.(type) {
		case *types.TurnStarted:
			turn.ID = e.TurnID
		case *types.ItemCompleted:
			turn.Items = append(turn.Items, e.Item)
			if msg, ok := e.Item.(*types.AgentMessage); ok {
				turn.FinalResponse = msg.Text
			}
		case *types.TurnCompleted:
			turn.Status = e.Status
		case *types.TurnFailed:
			turn.Status = "failed"
			turnErr = types.NewRPCError(-1, e.Message, nil)
		}
	}
	t.mu.Lock()
	turn.Usage, turn.UsageAvailable = op.finalUsage, op.usageAvailable
	completed := op.complete
	t.mu.Unlock()
	if ctx.Err() != nil {
		return turn, fmt.Errorf("codex.Thread.Run: %w", ctx.Err())
	}
	if !completed {
		return turn, fmt.Errorf("codex.Thread.Run: %w", types.ErrThreadClosed)
	}
	return turn, turnErr
}

func buildTurnStartParams(threadID string, input []map[string]any, opts *types.RunOptions) map[string]any {
	params := map[string]any{
		"threadId": threadID,
		"input":    input,
	}
	if opts == nil {
		return params
	}
	if opts.OutputSchema != nil {
		params["outputSchema"] = opts.OutputSchema
	}
	if opts.CollaborationMode != nil {
		params["collaborationMode"] = opts.CollaborationMode
	}
	return params
}

// buildTurnInput constructs the "input" array for turn/start. Spike
// transcript shape:
//
//	[{"type":"text","text":"..."},
//	 {"type":"skill","name":"skill-name","path":"/abs/SKILL.md"},
//	 {"type":"localImage","path":"/abs/path.png"}]
func buildTurnInput(prompt string, opts *types.RunOptions) ([]map[string]any, error) {
	out := []map[string]any{{"type": "text", "text": prompt}}
	if opts != nil {
		for _, skill := range opts.Skills {
			if skill.Name == "" {
				return nil, fmt.Errorf("codex.buildTurnInput: skill name is empty")
			}
			if skill.Path == "" {
				return nil, fmt.Errorf("codex.buildTurnInput: skill path is empty")
			}
			out = append(out, map[string]any{
				"type": "skill",
				"name": skill.Name,
				"path": skill.Path,
			})
		}
		for _, path := range opts.Images {
			if path == "" {
				return nil, fmt.Errorf("codex.buildTurnInput: image path is empty")
			}
			out = append(out, map[string]any{
				"type": "localImage",
				"path": path,
			})
		}
	}
	return out, nil
}

// StartThread sends thread/start and returns a new Thread. The thread is
// registered in the client's routing table.
func (c *Client) StartThread(ctx context.Context, opts *types.ThreadOptions) (*Thread, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", types.ErrClientClosed)
	}
	if !c.connected.Load() {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", types.ErrClientNotConnected)
	}
	if err := c.validateThreadApprovalPolicy(opts); err != nil {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	params := buildThreadStartParams(c.opts, opts)
	resp, err := c.demux.Send(ctx, "thread/start", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	if resp.Error != nil {
		return nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	id, err := extractThreadID(resp.Result)
	if err != nil {
		return nil, err
	}
	t := newThread(c, id)
	t.private = true
	t.baseline = usageBaseline{known: true}
	t.model = extractThreadModel(resp.Result)
	t.cwd = resolveCwd(c, opts)
	c.registerThread(t)
	return t, nil
}

// ResumeThread sends thread/resume with optional cwd override and returns
// a new Thread handle for the resumed conversation.
func (c *Client) ResumeThread(ctx context.Context, threadID string, opts *types.ResumeOptions) (*Thread, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", types.ErrClientClosed)
	}
	if !c.connected.Load() {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", types.ErrClientNotConnected)
	}
	if threadID == "" {
		return nil, fmt.Errorf("codex.Client.ResumeThread: threadID must not be empty")
	}
	params := map[string]any{"threadId": threadID}
	cwd := ""
	if opts != nil && opts.Cwd != "" {
		params["cwd"] = opts.Cwd
		cwd = opts.Cwd
	}
	observation := c.beginResumeObservation(threadID)
	if observation == nil {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", types.ErrClientClosed)
	}
	defer c.endResumeObservation(threadID, observation)
	activeGeneration, mayReconcileIdle := observation.resumeIdleCandidate()
	resp, err := c.demux.Send(ctx, "thread/resume", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", err)
	}
	if resp.Error != nil {
		return nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	// Prefer the ID from the response; fall back to what the caller passed.
	id, err := extractThreadID(resp.Result)
	if err != nil || id == "" {
		id = threadID
	}
	t := newThread(c, id)
	status := t.seedExternalActivity(resp.Result)
	if status == "idle" && id == threadID && mayReconcileIdle {
		observation.reconcileIdleResume(activeGeneration)
	}
	t.model = extractThreadModel(resp.Result)
	if cwd == "" {
		cwd = c.opts.DefaultCwd
	}
	t.cwd = cwd
	c.registerThread(t)
	return t, nil
}

// resolveCwd picks the Thread's cwd from ThreadOptions, then the
// Client's DefaultCwd, then "" (unknown).
func resolveCwd(c *Client, opts *types.ThreadOptions) string {
	if opts != nil && opts.Cwd != "" {
		return opts.Cwd
	}
	return c.opts.DefaultCwd
}

// Cwd returns the working directory this thread was started with.
// Empty string means the cwd was never set (rare — occurs only when
// both ThreadOptions.Cwd and CodexOptions.DefaultCwd were empty).
func (t *Thread) Cwd() string { return t.cwd }

// ForkThread branches from an existing thread, returning a new Thread whose
// history is seeded with the source thread. The source thread is
// unaffected. Uses "thread/fork" — if the server doesn't expose this verb
// in the current CLI version, the error is returned as-is.
func (c *Client) ForkThread(ctx context.Context, sourceThreadID string, opts *types.ThreadOptions) (*Thread, *types.ForkResult, error) {
	if c.closed.Load() {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", types.ErrClientClosed)
	}
	if !c.connected.Load() {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", types.ErrClientNotConnected)
	}
	if sourceThreadID == "" {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: sourceThreadID must not be empty")
	}
	if err := c.validateThreadApprovalPolicy(opts); err != nil {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	params := buildThreadStartParams(c.opts, opts)
	params["threadId"] = sourceThreadID
	resp, err := c.demux.Send(ctx, "thread/fork", params)
	if err != nil {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	if resp.Error != nil {
		return nil, nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	newID, err := extractThreadID(resp.Result)
	if err != nil {
		return nil, nil, err
	}
	if newID == sourceThreadID {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: provider returned the source thread as its child")
	}
	t := newThread(c, newID)
	t.seedExternalActivity(resp.Result)
	t.model = extractThreadModel(resp.Result)
	t.cwd = resolveCwd(c, opts)
	c.registerThread(t)
	return t, &types.ForkResult{SourceThreadID: sourceThreadID, NewThreadID: newID}, nil
}

// ArchiveThread marks a thread as archived on the server and unregisters
// it locally.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	if c.closed.Load() {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", types.ErrClientClosed)
	}
	if !c.connected.Load() {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", types.ErrClientNotConnected)
	}
	resp, err := c.demux.Send(ctx, "thread/archive", map[string]any{"threadId": threadID})
	if err != nil {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", err)
	}
	if resp.Error != nil {
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	c.unregisterThread(threadID)
	return nil
}

// buildThreadStartParams merges client-level defaults with per-call
// overrides into the thread/start params payload.
func buildThreadStartParams(clientOpts *types.CodexOptions, opts *types.ThreadOptions) map[string]any {
	p := map[string]any{}
	// Apply client defaults first.
	if clientOpts != nil && clientOpts.DefaultModel != "" {
		p["model"] = clientOpts.DefaultModel
	}
	if clientOpts != nil && clientOpts.DefaultCwd != "" {
		p["cwd"] = clientOpts.DefaultCwd
	}
	if clientOpts != nil && clientOpts.DefaultSandbox != "" {
		p["sandbox"] = string(clientOpts.DefaultSandbox)
	}
	if clientOpts != nil && clientOpts.DefaultApprovalPolicy != "" {
		p["approvalPolicy"] = encodeApprovalPolicy(clientOpts.DefaultApprovalPolicy)
	}
	if servers := resolveMCPServers(clientOpts, opts); len(servers) > 0 {
		p["config"] = map[string]any{
			"mcp_servers": servers,
		}
	}
	// Per-call overrides win.
	if opts != nil {
		if opts.Model != "" {
			p["model"] = opts.Model
		}
		if opts.Cwd != "" {
			p["cwd"] = opts.Cwd
		}
		if opts.Sandbox != "" {
			p["sandbox"] = string(opts.Sandbox)
		}
		if opts.ApprovalPolicy != "" {
			p["approvalPolicy"] = encodeApprovalPolicy(opts.ApprovalPolicy)
		}
	}
	return p
}

func resolveMCPServers(clientOpts *types.CodexOptions, opts *types.ThreadOptions) map[string]types.McpServerConfig {
	if opts != nil && len(opts.MCPServers) > 0 {
		return opts.MCPServers
	}
	if clientOpts != nil && len(clientOpts.DefaultMCPServers) > 0 {
		return clientOpts.DefaultMCPServers
	}
	return nil
}

// extractThreadID pulls thread.id from a thread/start or thread/resume or
// thread/fork response. Accepts both nested ("thread.id") and flat
// ("threadId") shapes.
func extractThreadID(result json.RawMessage) (string, error) {
	var shape struct {
		ThreadID string `json:"threadId"`
		Thread   *struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &shape); err != nil {
		return "", types.NewJSONDecodeError(string(result), err)
	}
	if shape.ThreadID != "" {
		return shape.ThreadID, nil
	}
	if shape.Thread != nil && shape.Thread.ID != "" {
		return shape.Thread.ID, nil
	}
	return "", types.NewMessageParseError("thread response missing thread id", string(result))
}

func extractThreadModel(result json.RawMessage) string {
	var shape struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(result, &shape); err != nil {
		return ""
	}
	return shape.Model
}

// Compile-time check that Thread exposes the stdlib-expected surface.
var _ = (&Thread{}).ID
