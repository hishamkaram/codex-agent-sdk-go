package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const prepareApprovedPlanMethod = runtimeprobe.PrepareApprovedPlanMethod

type approvedPlanState uint8

const (
	approvedPlanIdle approvedPlanState = iota
	approvedPlanPreparing
	approvedPlanUnconfirmed
)

// PrepareApprovedPlan prepares approved Markdown on a fresh destination
// thread and returns the provider's first-turn prompt. Keep the destination
// for exclusive use through the first Run. The source thread is not involved.
//
// This experimental method requires WithExperimentalAPI(true) and proof from
// the connected native Linux executable. Conflicting activity, such as a turn
// in flight, can cause [ErrThreadExecutionUnconfirmed] before the SDK sends
// the preparation request.
//
// A failed preparation can leave destination history changed if the request
// was sent. Preserve the approved Markdown and use a fresh destination for
// any retry after a sent preparation or first-turn startup fails.
func (t *Thread) PrepareApprovedPlan(ctx context.Context, markdown string, opts *types.PrepareApprovedPlanOptions) (*types.PreparedApprovedPlan, error) {
	const caller = "codex.Thread.PrepareApprovedPlan"
	if ctx == nil {
		return nil, fmt.Errorf("%s: context is required", caller)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	if t.threadExecution == nil || t.isClosed() {
		return nil, fmt.Errorf("%s: %w", caller, types.ErrThreadClosed)
	}
	if err := t.client.checkApprovalReentry(ctx); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	attachment := ""
	if opts != nil {
		attachment = opts.AttachmentPath
	}
	if err := validateApprovedPlan(markdown, attachment); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	if err := t.client.requireConnectedSchema(ctx, connectedSchemaFeature{
		method: prepareApprovedPlanMethod, name: "approved plan preparation", supported: runtimeprobe.SupportsApprovedPlan,
	}); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	prepared, err := t.prepareApprovedPlan(ctx, approvedPlanParams{ThreadID: t.id, Markdown: markdown, AttachmentPath: attachment})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	return prepared, nil
}

func validateApprovedPlan(markdown, attachment string) error {
	if !utf8.ValidString(markdown) || strings.TrimSpace(markdown) == "" {
		return errors.New("approved Markdown must be nonblank UTF-8")
	}
	if attachment != "" && (!filepath.IsAbs(attachment) || !utf8.ValidString(attachment) || strings.ContainsRune(attachment, 0)) {
		return errors.New("attachmentPath must be an absolute UTF-8 path on the app-server host")
	}
	return nil
}

type approvedPlanParams struct {
	ThreadID       string `json:"threadId"`
	Markdown       string `json:"markdown"`
	AttachmentPath string `json:"attachmentPath,omitempty"`
}

// prepareApprovedPlan owns the reservation through the matching reply. The
// request does not start a turn or enter turn accounting. An ambiguous
// response leaves only this execution record fenced.
func (t *Thread) prepareApprovedPlan(ctx context.Context, params approvedPlanParams) (*types.PreparedApprovedPlan, error) {
	if err := t.reserveApprovedPlan(ctx); err != nil {
		return nil, err
	}
	next := approvedPlanIdle
	defer func() { t.finishApprovedPlan(next) }()

	pending, err := t.client.demux.PostGuarded(ctx, prepareApprovedPlanMethod, params, t.checkApprovedPlanDispatch)
	if err != nil {
		return nil, err
	}
	defer pending.Release()
	response, err := pending.Wait(ctx)
	if err != nil {
		next = approvedPlanUnconfirmed
		return nil, fmt.Errorf("preparation outcome is unknown: %w", err)
	}
	if response.Error != nil {
		return nil, classifyRPCError(prepareApprovedPlanMethod, response.Error)
	}
	prepared, err := decodePreparedApprovedPlan(response.Result)
	if err != nil {
		next = approvedPlanUnconfirmed
		return nil, err
	}
	return prepared, nil
}

func decodePreparedApprovedPlan(raw json.RawMessage) (*types.PreparedApprovedPlan, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid UTF-8 in preparation response")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode preparation response: %w", err)
	}
	rawPrompt, ok := result["prompt"]
	if !ok {
		return nil, errors.New("preparation response contains no prompt")
	}
	var prompt string
	if err := json.Unmarshal(rawPrompt, &prompt); err != nil {
		return nil, fmt.Errorf("decode preparation response prompt: %w", err)
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("preparation response contains an empty prompt")
	}
	return &types.PreparedApprovedPlan{Prompt: prompt}, nil
}

func (t *Thread) reserveApprovedPlan(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.closed.Load() {
		return types.ErrThreadClosed
	}
	if t.approvedPlan != approvedPlanIdle || t.op != nil || t.externalActive || t.resuming != 0 || t.activeID() != "" {
		return &ThreadExecutionUnconfirmedError{ThreadID: t.id}
	}
	t.approvedPlan = approvedPlanPreparing
	t.signalLocked()
	return nil
}

func (t *Thread) checkApprovedPlanDispatch(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.checkDispatchLocked(ctx, t.approvedPlan == approvedPlanPreparing && t.op == nil)
}

func (e *threadExecution) finishApprovedPlan(next approvedPlanState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.approvedPlan = next
	e.signalLocked()
}
