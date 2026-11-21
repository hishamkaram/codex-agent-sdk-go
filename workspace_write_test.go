package codex

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestResolveWorkspaceWritePreservesExplicitValues(t *testing.T) {
	t.Parallel()

	network := false
	got, err := resolveWorkspaceWrite(types.SandboxWorkspaceWrite, &types.ThreadOptions{NetworkAccessEnabled: &network})
	if err != nil {
		t.Fatal(err)
	}
	network = true
	assertThreadConfigJSON(t, got, `{"network_access":false}`)

	empty, err := resolveWorkspaceWrite(types.SandboxWorkspaceWrite, &types.ThreadOptions{AdditionalDirectories: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	assertThreadConfigJSON(t, empty, `{"writable_roots":[]}`)

	base := t.TempDir()
	first, second := filepath.Join(base, "first"), filepath.Join(base, "second")
	opts := &types.ThreadOptions{AdditionalDirectories: []string{first, second + string(filepath.Separator), first}}
	roots, err := resolveWorkspaceWrite(types.SandboxWorkspaceWrite, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roots.WritableRoots, []string{first, second}) {
		t.Fatalf("roots = %q, want %q", roots.WritableRoots, []string{first, second})
	}
	if opts.AdditionalDirectories[1] != second+string(filepath.Separator) {
		t.Fatalf("caller roots mutated: %q", opts.AdditionalDirectories)
	}
	opts.AdditionalDirectories[0] = base
	if roots.WritableRoots[0] != first {
		t.Fatalf("request roots alias caller slice: %q", roots.WritableRoots)
	}
}

func TestResolveWorkspaceWriteRejectsUnclearPolicy(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	sep := string(filepath.Separator)
	boolValue := true
	for _, tc := range []struct {
		name    string
		sandbox types.SandboxMode
		opts    *types.ThreadOptions
	}{
		{"unknown sandbox", "", &types.ThreadOptions{NetworkAccessEnabled: &boolValue}},
		{"read only", types.SandboxReadOnly, &types.ThreadOptions{AdditionalDirectories: []string{}}},
		{"unrestricted", types.SandboxDangerFullAccess, &types.ThreadOptions{NetworkAccessEnabled: &boolValue}},
		{"relative", types.SandboxWorkspaceWrite, &types.ThreadOptions{AdditionalDirectories: []string{"relative"}}},
		{"empty path", types.SandboxWorkspaceWrite, &types.ThreadOptions{AdditionalDirectories: []string{""}}},
		{"nul", types.SandboxWorkspaceWrite, &types.ThreadOptions{AdditionalDirectories: []string{base + "\x00"}}},
		{"parent traversal", types.SandboxWorkspaceWrite, &types.ThreadOptions{AdditionalDirectories: []string{base + sep + "link" + sep + ".."}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := resolveWorkspaceWrite(tc.sandbox, tc.opts); got != nil || err == nil {
				t.Fatalf("got request=%+v error=%v, want rejection", got, err)
			}
		})
	}
	if got, err := resolveWorkspaceWrite(types.SandboxReadOnly, &types.ThreadOptions{}); got != nil || err != nil {
		t.Fatalf("unset policy changed legacy request behavior: request=%+v error=%v", got, err)
	}
}

func TestVerifyWorkspaceWriteRequiresEffectivePolicy(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	cwd, root := filepath.Join(base, "work"), filepath.Join(base, "sibling")
	disabled := false
	type fixture struct {
		name     string
		cwd      string
		sandbox  any
		request  *workspaceWriteSettings
		badField string
	}
	withRoots := &workspaceWriteSettings{NetworkAccess: &disabled, WritableRoots: []string{root}}
	explicitEmpty := &workspaceWriteSettings{WritableRoots: []string{}}
	for _, tc := range []fixture{
		{"matches", cwd, map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{root}}, withRoots, ""},
		{"cwd changed", base, map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{root}}, withRoots, "cwd"},
		{"cwd missing", "", map[string]any{"type": "workspaceWrite"}, withRoots, "cwd"},
		{"sandbox ignored", cwd, map[string]any{"type": "dangerFullAccess"}, withRoots, "sandbox.type"},
		{"sandbox missing", cwd, nil, withRoots, "sandbox.type"},
		{"network missing", cwd, map[string]any{"type": "workspaceWrite", "writableRoots": []string{root}}, withRoots, "sandbox.networkAccess"},
		{"network widened", cwd, map[string]any{"type": "workspaceWrite", "networkAccess": true, "writableRoots": []string{root}}, withRoots, "sandbox.networkAccess"},
		{"extra ancestor", cwd, map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{base, root}}, withRoots, "sandbox.writableRoots"},
		{"wrong root", cwd, map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{base}}, withRoots, "sandbox.writableRoots"},
		{"explicit empty", cwd, map[string]any{"type": "workspaceWrite", "writableRoots": []string{}}, explicitEmpty, ""},
		{"empty request ignored", cwd, map[string]any{"type": "workspaceWrite", "writableRoots": []string{root}}, explicitEmpty, "sandbox.writableRoots"},
		{"roots missing", cwd, map[string]any{"type": "workspaceWrite"}, explicitEmpty, "sandbox.writableRoots"},
		{"malformed policy", cwd, map[string]any{"type": "workspaceWrite", "writableRoots": "unexpected"}, explicitEmpty, "cwd or sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(map[string]any{
				"cwd":                     tc.cwd,
				"sandbox":                 tc.sandbox,
				"activePermissionProfile": map[string]string{"id": ":workspace"},
				"runtimeWorkspaceRoots":   []string{cwd, root},
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := verifyWorkspaceWrite(result, "new-thread", cwd, tc.request)
			if tc.badField == "" {
				if got != cwd || err != nil {
					t.Fatalf("got cwd=%q error=%v, want %q", got, err, cwd)
				}
				return
			}
			var mismatch *types.ThreadPolicyMismatchError
			if !errors.As(err, &mismatch) || mismatch.ThreadID != "new-thread" || mismatch.Field != tc.badField || got != "" {
				t.Fatalf("got cwd=%q error=%v, want mismatch on %s", got, err, tc.badField)
			}
		})
	}
}

func TestExpectedThreadCwdResolvesRelativeToAppServer(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	got, err := expectedThreadCwd(base, "project")
	if want := filepath.Join(base, "project"); err != nil || got != want {
		t.Fatalf("got (%q, %v), want %q", got, err, want)
	}
}
