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
	if t.record == nil || t.isClosed() {
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

func (t *Thread) prepareApprovedPlan(ctx context.Context, params approvedPlanParams) (*types.PreparedApprovedPlan, error) {
	var prepared *types.PreparedApprovedPlan
	response, err := t.record.Prepare(ctx, t.id, prepareApprovedPlanMethod, params, t.executionWire(), func(raw json.RawMessage) error {
		var decodeErr error
		prepared, decodeErr = decodePreparedApprovedPlan(raw)
		return decodeErr
	})
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, classifyRPCError(prepareApprovedPlanMethod, response.Error)
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
