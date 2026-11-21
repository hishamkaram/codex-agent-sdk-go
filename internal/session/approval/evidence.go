package approval

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Owner owns file approval evidence and identifies this session's callbacks.
// Its zero value is ready for use by the dispatcher.
type Owner struct {
	mu            sync.Mutex
	fileApprovals map[fileApprovalKey]fileApprovalContext
}

type fileApprovalKey struct{ thread, turn, item string }

type fileApprovalContext struct {
	changes []types.FileChangePart
	invalid bool
}

// ClearEvidence drops file context after a stream gap or shutdown.
func (o *Owner) ClearEvidence() {
	o.mu.Lock()
	o.fileApprovals = nil
	o.mu.Unlock()
}

// Observe applies a parsed event to file approval evidence.
func (o *Owner) Observe(event types.ThreadEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch e := event.(type) {
	case *types.ItemStarted:
		file, ok := e.Item.(*types.FileChange)
		if !ok || e.ThreadID == "" || e.TurnID == "" || e.ItemID == "" {
			return
		}
		if o.fileApprovals == nil {
			o.fileApprovals = make(map[fileApprovalKey]fileApprovalContext)
		}
		o.fileApprovals[fileApprovalKey{e.ThreadID, e.TurnID, e.ItemID}] = fileApprovalContext{changes: cloneFileChanges(file.Changes)}
	case *types.ItemCompleted:
		delete(o.fileApprovals, fileApprovalKey{e.ThreadID, e.TurnID, e.ItemID})
	case *types.FileChangePatchUpdated:
		o.updateFileApprovalLocked(e)
	case *types.TurnCompleted:
		o.clearTurnLocked(e.ThreadID, e.TurnID)
	case *types.TurnFailed:
		o.clearTurnLocked(e.ThreadID, e.TurnID)
	}
}

func (o *Owner) clearTurnLocked(threadID, turnID string) {
	for key := range o.fileApprovals {
		if key.thread == threadID && key.turn == turnID {
			delete(o.fileApprovals, key)
		}
	}
}

func (o *Owner) updateFileApprovalLocked(event *types.FileChangePatchUpdated) {
	if event.TurnID == "" || event.ItemID == "" {
		o.fileApprovals = nil
		return
	}
	key := fileApprovalKey{event.ThreadID, event.TurnID, event.ItemID}
	if _, active := o.fileApprovals[key]; !active {
		return
	}
	var changes []types.FileChangePart
	err := json.Unmarshal(event.Changes, &changes)
	// Retain an invalidated entry so legacy inline fields cannot revive stale context.
	o.fileApprovals[key] = fileApprovalContext{changes: changes, invalid: err != nil || changes == nil}
}

func (o *Owner) resolveFileApproval(request *types.FileChangeApprovalRequest) bool {
	request.Changes = nil
	o.mu.Lock()
	cached, known := o.fileApprovals[fileApprovalKey{request.ThreadID, request.TurnID, request.ItemID}]
	changes := cloneFileChanges(cached.changes)
	o.mu.Unlock()
	if cached.invalid || (request.GrantRoot != nil &&
		(strings.TrimSpace(*request.GrantRoot) == "" || request.ThreadID == "" || request.TurnID == "" || request.ItemID == "")) {
		return false
	}
	if !known && request.Path != "" {
		changes = []types.FileChangePart{{Path: request.Path, Operation: request.Operation, Diff: request.Diff}}
	}
	if len(changes) == 0 {
		return request.GrantRoot != nil
	}
	for _, change := range changes {
		if strings.TrimSpace(change.Path) == "" || !slices.Contains([]string{"create", "modify", "delete"}, change.Operation) {
			return false
		}
	}
	request.Changes = changes
	if len(changes) == 1 {
		request.Path, request.Operation, request.Diff = changes[0].Path, changes[0].Operation, changes[0].Diff
	}
	return true
}

func cloneFileChanges(changes []types.FileChangePart) []types.FileChangePart {
	out := slices.Clone(changes)
	for i := range out {
		if kind := out[i].Kind; kind != nil {
			copyKind := *kind
			if kind.MovePath != nil {
				move := *kind.MovePath
				copyKind.MovePath = &move
			}
			out[i].Kind = &copyKind
		}
	}
	return out
}
