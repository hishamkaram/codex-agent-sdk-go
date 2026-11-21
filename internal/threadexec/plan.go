package threadexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type approvedPlanState uint8

const (
	approvedPlanIdle approvedPlanState = iota
	approvedPlanPreparing
	approvedPlanUnconfirmed
)

// ValidateApprovedPlan checks the caller options before runtime or execution work.
func ValidateApprovedPlan(markdown, attachment string) error {
	if !utf8.ValidString(markdown) || strings.TrimSpace(markdown) == "" {
		return errors.New("approved Markdown must be nonblank UTF-8")
	}
	if attachment != "" && (!filepath.IsAbs(attachment) || !utf8.ValidString(attachment) || strings.ContainsRune(attachment, 0)) {
		return errors.New("attachmentPath must be an absolute UTF-8 path on the app-server host")
	}
	return nil
}

// ApprovedPlanParams is the wire payload for approved-plan preparation.
type ApprovedPlanParams struct {
	ThreadID       string `json:"threadId"`
	Markdown       string `json:"markdown"`
	AttachmentPath string `json:"attachmentPath,omitempty"`
}

// PrepareApprovedPlan holds the reservation until the provider reply has been
// validated or the destination has been fenced. RPC errors are returned
// separately for the caller's public error projection.
func (e *Record) PrepareApprovedPlan(ctx context.Context, method string, params ApprovedPlanParams, wire Wire) (*types.PreparedApprovedPlan, *jsonrpc.RPCError, error) {
	if err := e.reserveApprovedPlan(ctx, params.ThreadID, wire); err != nil {
		return nil, nil, err
	}
	next := approvedPlanIdle
	defer func() { e.finishApprovedPlan(next) }()

	pending, err := wire.PostGuarded(ctx, method, params, func(ctx context.Context) error {
		return e.checkApprovedPlanDispatch(ctx, params.ThreadID, wire)
	})
	if err != nil {
		return nil, nil, err
	}
	defer pending.Release()
	response, err := pending.Wait(ctx)
	if err != nil {
		next = approvedPlanUnconfirmed
		return nil, nil, fmt.Errorf("preparation outcome is unknown: %w", err)
	}
	if response.Error != nil {
		return nil, response.Error, nil
	}
	prepared, err := decodePreparedApprovedPlan(response.Result)
	if err != nil {
		next = approvedPlanUnconfirmed
		return nil, nil, err
	}
	return prepared, nil, nil
}

func (e *Record) reserveApprovedPlan(ctx context.Context, id string, wire Wire) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed.Load() {
		return types.ErrThreadClosed
	}
	if e.approvedPlan != approvedPlanIdle || e.op != nil || e.externalActive || e.resuming != 0 || e.ActiveID() != "" {
		return wire.Unconfirmed(id)
	}
	e.approvedPlan = approvedPlanPreparing
	e.signalLocked()
	return nil
}

func (e *Record) checkApprovedPlanDispatch(ctx context.Context, id string, wire Wire) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkDispatchLocked(ctx, id, e.approvedPlan == approvedPlanPreparing && e.op == nil, wire)
}

func (e *Record) finishApprovedPlan(next approvedPlanState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.approvedPlan = next
	e.signalLocked()
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
