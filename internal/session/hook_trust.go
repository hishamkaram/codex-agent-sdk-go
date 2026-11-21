package session

import (
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func (s *Session) loadHookTrust(ctx context.Context, demux *jsonrpc.Demux) error {
	resp, err := demux.Send(ctx, "hooks/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("list generated hooks: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("list generated hooks: %w", types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data))
	}
	if err := s.hooks.SetTrust(resp.Result); err != nil {
		return fmt.Errorf("verify generated hooks: %w", err)
	}
	return nil
}
