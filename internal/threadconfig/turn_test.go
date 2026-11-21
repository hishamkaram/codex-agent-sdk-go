package threadconfig

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestBuildTurnInput_TextOnly(t *testing.T) {
	t.Parallel()
	got, err := BuildTurnInput("hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0]["type"] != "text" || got[0]["text"] != "hello" {
		t.Fatalf("got %+v", got[0])
	}
}

func TestBuildTurnInput_WithImages(t *testing.T) {
	t.Parallel()
	opts := &types.RunOptions{Images: []string{"/abs/a.png", "/abs/b.jpg"}}
	got, err := BuildTurnInput("describe these", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[1]["type"] != "localImage" || got[1]["path"] != "/abs/a.png" {
		t.Fatalf("image 0: %+v", got[1])
	}
	if got[2]["type"] != "localImage" || got[2]["path"] != "/abs/b.jpg" {
		t.Fatalf("image 1: %+v", got[2])
	}
}

func TestBuildTurnInput_WithSkill(t *testing.T) {
	t.Parallel()
	opts := &types.RunOptions{Skills: []types.SkillInput{
		{Name: "openai-docs", Path: "/abs/openai-docs/SKILL.md"},
	}}
	got, err := BuildTurnInput("$openai-docs explain responses", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0]["type"] != "text" || got[0]["text"] != "$openai-docs explain responses" {
		t.Fatalf("text: %+v", got[0])
	}
	if got[1]["type"] != "skill" || got[1]["name"] != "openai-docs" || got[1]["path"] != "/abs/openai-docs/SKILL.md" {
		t.Fatalf("skill: %+v", got[1])
	}
}

func TestBuildTurnInput_WithSkillAndImage(t *testing.T) {
	t.Parallel()
	opts := &types.RunOptions{
		Skills: []types.SkillInput{{Name: "imagegen", Path: "/abs/imagegen/SKILL.md"}},
		Images: []string{"/abs/a.png"},
	}
	got, err := BuildTurnInput("$imagegen use this", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[1]["type"] != "skill" || got[1]["name"] != "imagegen" {
		t.Fatalf("skill: %+v", got[1])
	}
	if got[2]["type"] != "localImage" || got[2]["path"] != "/abs/a.png" {
		t.Fatalf("image: %+v", got[2])
	}
}

func TestBuildTurnInput_EmptySkillFieldsAreErrors(t *testing.T) {
	t.Parallel()
	if _, err := BuildTurnInput("x", &types.RunOptions{Skills: []types.SkillInput{{Path: "/abs/SKILL.md"}}}); err == nil {
		t.Fatal("expected error for empty skill name")
	}
	if _, err := BuildTurnInput("x", &types.RunOptions{Skills: []types.SkillInput{{Name: "x"}}}); err == nil {
		t.Fatal("expected error for empty skill path")
	}
}

func TestBuildTurnInput_EmptyImagePathIsError(t *testing.T) {
	t.Parallel()
	_, err := BuildTurnInput("x", &types.RunOptions{Images: []string{""}})
	if err == nil {
		t.Fatal("expected error for empty image path")
	}
}

func TestBuildTurnStartParams_WithCollaborationMode(t *testing.T) {
	t.Parallel()
	input := []map[string]any{{"type": "text", "text": "make a plan"}}
	opts := &types.RunOptions{
		CollaborationMode: &types.CollaborationMode{
			Mode: types.CollaborationModePlan,
			Settings: types.CollaborationModeSettings{
				Model: "gpt-5.4",
			},
		},
	}

	got := BuildTurnStartParams("thread-1", input, opts)
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId = %v", got["threadId"])
	}
	if got["input"] == nil {
		t.Fatalf("input missing: %+v", got)
	}
	cm, ok := got["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode = %T", got["collaborationMode"])
	}
	if cm.Mode != types.CollaborationModePlan || cm.Settings.Model != "gpt-5.4" {
		t.Fatalf("collaborationMode = %+v", cm)
	}
}

func TestTurnProgramComposesWithExistingOptions(t *testing.T) {
	t.Parallel()
	program := types.CyberAccessProgramStandard
	opts := &types.RunOptions{
		CyberAccessProgram: &program,
		OutputSchema:       &types.OutputSchema{Schema: map[string]any{"type": "object"}},
		CollaborationMode:  &types.CollaborationMode{Mode: types.CollaborationModePlan},
		ReasoningEffort:    "low",
		Skills:             []types.SkillInput{{Name: "example", Path: "/fixture/SKILL.md"}},
		Images:             []string{"/fixture/example.png"},
	}
	input, err := BuildTurnInput("example", opts)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(BuildTurnStartParams("thread", input, opts))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Program string `json:"cyberAccessProgram"`
		Effort  string `json:"effort"`
		Input   []struct {
			Type string `json:"type"`
		} `json:"input"`
		OutputSchema struct {
			Schema struct {
				Type string `json:"type"`
			} `json:"schema"`
		} `json:"outputSchema"`
		CollaborationMode struct {
			Mode string `json:"mode"`
		} `json:"collaborationMode"`
	}
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Program != "standard" || got.Effort != "low" || got.OutputSchema.Schema.Type != "object" || got.CollaborationMode.Mode != "plan" || len(got.Input) != 3 ||
		got.Input[0].Type != "text" || got.Input[1].Type != "skill" || got.Input[2].Type != "localImage" {
		t.Fatalf("composed turn/start params = %s", wire)
	}
}
