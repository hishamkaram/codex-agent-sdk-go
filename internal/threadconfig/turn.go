package threadconfig

import (
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// BuildTurnStartParams builds the optional wire fields for turn/start.
func BuildTurnStartParams(threadID string, input []map[string]any, opts *types.RunOptions) map[string]any {
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
	if opts.ReasoningEffort != "" {
		params["effort"] = opts.ReasoningEffort
	}
	if opts.CyberAccessProgram != nil {
		params["cyberAccessProgram"] = *opts.CyberAccessProgram
	}
	return params
}

// BuildTurnInput constructs the "input" array for turn/start. Spike
// transcript shape:
//
//	[{"type":"text","text":"..."},
//	 {"type":"skill","name":"skill-name","path":"/abs/SKILL.md"},
//	 {"type":"localImage","path":"/abs/path.png"}]
func BuildTurnInput(prompt string, opts *types.RunOptions) ([]map[string]any, error) {
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

// ValidateTurnProgram checks the requested program before probing its runtime support.
func ValidateTurnProgram(program types.CyberAccessProgram) error {
	switch program {
	case types.CyberAccessProgramStandard, types.CyberAccessProgramDaybreakBlue, types.CyberAccessProgramDaybreakRed:
	default:
		return errors.New("turn/start: invalid cyberAccessProgram")
	}
	return nil
}
