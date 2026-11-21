package session

import (
	"context"
	"encoding/json"
	"fmt"

	sdkevents "github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadconfig"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ThreadHandle carries the provider's metadata and the canonical execution
// record already registered for the thread.
type ThreadHandle struct {
	ID     string
	Model  string
	Cwd    string
	Record *threadexec.Record
}

// StartThread dispatches a prepared creation request and registers its result.
func (s *Session) StartThread(ctx context.Context, request threadconfig.CreationRequest) (ThreadHandle, error) {
	resp, err := s.Demux().Send(ctx, "thread/start", request.Params)
	if err != nil {
		return ThreadHandle{}, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	if resp.Error != nil {
		return ThreadHandle{}, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	id, err := sdkevents.ThreadIDFromReply(resp.Result)
	if err != nil {
		return ThreadHandle{}, err
	}
	cwd, err := request.ResponseCwd(resp.Result, id)
	if err != nil {
		return ThreadHandle{}, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	record := threadexec.NewRecord(true)
	return s.registerThread(id, extractThreadModel(resp.Result), cwd, record), nil
}

// ResumeThread observes a provisional record before dispatch and reconciles
// provider activity before publishing the resumed handle.
func (s *Session) ResumeThread(ctx context.Context, requestedID string, params map[string]any, cwd, reasoningEffort string) (ThreadHandle, error) {
	observationID := threadexec.CanonicalID(requestedID)
	observation := s.Threads().BeginResume(observationID, s.Closed)
	if observation == nil {
		return ThreadHandle{}, fmt.Errorf("codex.Client.ResumeThread: %w", types.ErrClientClosed)
	}
	defer s.Threads().EndResume(observationID, observation)
	resp, idleProof, err := s.requestThreadResume(ctx, params, observation)
	if err != nil {
		return ThreadHandle{}, fmt.Errorf("codex.Client.ResumeThread: %w", err)
	}
	if resp.Error != nil {
		return ThreadHandle{}, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	// Older peers can omit the ID, so the requested identity is the fallback.
	reportedID, _ := sdkevents.ThreadIDFromReply(resp.Result)
	id := reportedID
	if id == "" {
		id = requestedID
	}
	record := threadexec.NewRecord(false)
	status, statusThreadID := record.SeedExternalActivity(resp.Result)
	idleProof.reconcile(status, statusThreadID, reportedID, observationID)
	if policyErr := threadconfig.VerifyResumeReasoningEffort(resp.Result, id, reasoningEffort); policyErr != nil {
		s.Threads().ReconcileRejectedResume(id, record)
		return ThreadHandle{}, fmt.Errorf("codex.Client.ResumeThread: %w", policyErr)
	}
	return s.registerThread(id, extractThreadModel(resp.Result), cwd, record), nil
}

// ForkThread dispatches a prepared fork request and registers the child.
func (s *Session) ForkThread(ctx context.Context, sourceID string, request threadconfig.CreationRequest) (ThreadHandle, *types.ForkResult, error) {
	resp, err := s.Demux().Send(ctx, "thread/fork", request.Params)
	if err != nil {
		return ThreadHandle{}, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	if resp.Error != nil {
		return ThreadHandle{}, nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	newID, err := sdkevents.ThreadIDFromReply(resp.Result)
	if err != nil {
		return ThreadHandle{}, nil, err
	}
	if newID == sourceID {
		return ThreadHandle{}, nil, fmt.Errorf("codex.Client.ForkThread: provider returned the source thread as its child")
	}
	cwd, err := request.ResponseCwd(resp.Result, newID)
	if err != nil {
		return ThreadHandle{}, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	record := threadexec.NewRecord(false)
	record.SeedExternalActivity(resp.Result)
	handle := s.registerThread(newID, extractThreadModel(resp.Result), cwd, record)
	return handle, &types.ForkResult{SourceThreadID: sourceID, NewThreadID: newID}, nil
}

// ArchiveThread dispatches the archive request before updating the registry.
func (s *Session) ArchiveThread(ctx context.Context, threadID string) error {
	resp, err := s.Demux().Send(ctx, "thread/archive", map[string]any{"threadId": threadID})
	if err != nil {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", err)
	}
	if resp.Error != nil {
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	s.Threads().Archive(threadID)
	return nil
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

func (s *Session) registerThread(id, model, cwd string, record *threadexec.Record) ThreadHandle {
	return ThreadHandle{
		ID:     id,
		Model:  model,
		Cwd:    cwd,
		Record: s.Threads().Register(id, record, s.Connected),
	}
}

type idleResumeProof struct {
	execution  *threadexec.Record
	generation uint64
}

func (s *Session) requestThreadResume(ctx context.Context, params map[string]any, execution *threadexec.Record) (jsonrpc.Response, idleResumeProof, error) {
	generation, eligible := execution.ResumeIdleCandidate()
	if eligible && s.Demux().ServerMessages() != nil {
		reply, err := s.Demux().SendOrdered(ctx, "thread/resume", params)
		return reply, idleResumeProof{execution: execution, generation: generation}, err
	}
	reply, err := s.Demux().Send(ctx, "thread/resume", params)
	return reply, idleResumeProof{}, err
}

func (p idleResumeProof) reconcile(status, statusThreadID, reportedID, requestedID string) {
	if p.execution != nil && status == "idle" && threadexec.CanonicalID(statusThreadID) == requestedID && threadexec.CanonicalID(reportedID) == requestedID {
		p.execution.ReconcileIdleResume(p.generation)
	}
}
