package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type fileApprovalKey struct{ thread, turn, item string }

type fileApprovalContext struct {
	changes []types.FileChangePart
	invalid bool
}

func (c *Client) clearFileApprovals() {
	c.mu.Lock()
	c.fileApprovals = nil
	c.mu.Unlock()
}

func (c *Client) rememberFileApproval(event types.ThreadEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e := event.(type) {
	case *types.ItemStarted:
		file, ok := e.Item.(*types.FileChange)
		if !ok || e.ThreadID == "" || e.TurnID == "" || e.ItemID == "" {
			return
		}
		if c.fileApprovals == nil {
			c.fileApprovals = make(map[fileApprovalKey]fileApprovalContext)
		}
		c.fileApprovals[fileApprovalKey{e.ThreadID, e.TurnID, e.ItemID}] = fileApprovalContext{changes: cloneFileChanges(file.Changes)}
	case *types.ItemCompleted:
		delete(c.fileApprovals, fileApprovalKey{e.ThreadID, e.TurnID, e.ItemID})
	case *types.FileChangePatchUpdated:
		c.updateFileApprovalLocked(e)
	default:
		if !isTurnTerminus(event, "") {
			return
		}
		thread, _ := threadIdentityFromEvent(event)
		turn := turnIDFromTerminus(event)
		for key := range c.fileApprovals {
			if key.thread == thread && key.turn == turn {
				delete(c.fileApprovals, key)
			}
		}
	}
}

func (c *Client) updateFileApprovalLocked(event *types.FileChangePatchUpdated) {
	if event.TurnID == "" || event.ItemID == "" {
		c.fileApprovals = nil
		return
	}
	key := fileApprovalKey{event.ThreadID, event.TurnID, event.ItemID}
	if _, active := c.fileApprovals[key]; !active {
		return
	}
	var changes []types.FileChangePart
	err := json.Unmarshal(event.Changes, &changes)
	// Retain an invalidated entry so legacy inline fields cannot revive stale context.
	c.fileApprovals[key] = fileApprovalContext{changes: changes, invalid: err != nil || changes == nil}
}

func (c *Client) resolveFileApproval(request *types.FileChangeApprovalRequest) bool {
	request.Changes = nil
	c.mu.Lock()
	cached, known := c.fileApprovals[fileApprovalKey{request.ThreadID, request.TurnID, request.ItemID}]
	changes := cloneFileChanges(cached.changes)
	c.mu.Unlock()
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
