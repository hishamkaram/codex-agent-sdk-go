package transport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func requireResolvedCLI(t *testing.T, executable, cwd string, env []string, replace bool) CLI {
	t.Helper()
	cli, err := ResolveCLI(executable, cwd, env, replace)
	if err != nil {
		t.Fatal(err)
	}
	return cli
}

func TestCLIUsesResolvedEnvironmentForProbesAndAppServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdio fixture uses a POSIX shell")
	}
	t.Setenv("SDK57_PARENT_SENTINEL", "inherited")
	cliPath := filepath.Join(t.TempDir(), "codex")
	writeExecutableFixture(t, cliPath, `#!/bin/sh
env_fields() {
	printf '"parent":"%s","provided":"%s","empty":"%s"' "${SDK57_PARENT_SENTINEL-unset}" "${SDK57_PROVIDED-unset}" "${SDK57_LITERAL_EMPTY-unset}"
}
case "$1" in
--version)
	[ "$SDK57_PARENT_SENTINEL" != changed-after-resolution ] || exit 3
	printf 'codex 0.171.0\n'
	;;
snapshot)
	printf '{'
	env_fields
	printf '}\n'
	;;
app-server)
	IFS= read -r line || exit 1
	printf '{"id":1,"result":{'
	env_fields
	printf '}}\n'
	while IFS= read -r line; do :; done
	;;
*) exit 2 ;;
esac
`)
	for _, tc := range []struct {
		name    string
		replace bool
		env     []string
		want    string
	}{
		{"default overlay", false, nil, `{"parent":"inherited","provided":"unset","empty":"unset"}`},
		{"overlay deletion", false, []string{"SDK57_PARENT_SENTINEL="}, `{"parent":"unset","provided":"unset","empty":"unset"}`},
		{"empty replacement", true, nil, `{"parent":"unset","provided":"unset","empty":"unset"}`},
		{
			"allowlist", true,
			[]string{"SDK57_PROVIDED=earlier", "SDK57_PROVIDED=new=value", "SDK57_LITERAL_EMPTY="},
			`{"parent":"unset","provided":"new=value","empty":""}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := requireResolvedCLI(t, cliPath, t.TempDir(), tc.env, tc.replace)
			t.Setenv("SDK57_PARENT_SENTINEL", "changed-after-resolution")
			ctx := t.Context()
			if got, err := cli.ProbeVersion(ctx); err != nil || got != (SemVer{Minor: 171}) {
				t.Fatalf("version = %v, err = %v", got, err)
			}
			stdout, _, err := cli.Run(ctx, time.Second, "snapshot")
			if err != nil {
				t.Fatal(err)
			}
			assertEnvironmentSnapshot(t, []byte(stdout), tc.want)

			app := NewAppServer(AppServerConfig{CLI: cli})
			if connectErr := app.Connect(ctx); connectErr != nil {
				t.Fatal(connectErr)
			}
			t.Cleanup(func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if closeErr := app.Close(closeCtx); closeErr != nil {
					t.Error(closeErr)
				}
			})
			result, err := app.Demux().Send(ctx, "snapshot", nil)
			if err != nil {
				t.Fatal(err)
			}
			assertEnvironmentSnapshot(t, result.Result, tc.want)
			if got := os.Getenv("SDK57_PARENT_SENTINEL"); got != "changed-after-resolution" {
				t.Errorf("SDK mutated parent sentinel: %q", got)
			}
		})
	}
}

func assertEnvironmentSnapshot(t *testing.T, got []byte, want string) {
	t.Helper()
	type childValues struct {
		Parent   string `json:"parent"`
		Provided string `json:"provided"`
		Empty    string `json:"empty"`
	}
	var gotValues, wantValues childValues
	if err := json.Unmarshal(got, &gotValues); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValues); err != nil {
		t.Fatal(err)
	}
	if gotValues != wantValues {
		t.Errorf("child environment = %+v, want %+v", gotValues, wantValues)
	}
}

func TestMergeEnvironmentWindowsKeys(t *testing.T) {
	t.Parallel()
	parent := []string{"Path=inherited", "=C:=C:\\work", "CHECK=remove"}
	overlay := []string{"path=new", "CHECK=", "=c:=C:\\new"}
	if got, want := mergeEnvironment(parent, overlay, false, true), []string{"path=new", "=c:=C:\\new"}; !slices.Equal(got, want) {
		t.Errorf("Windows overlay = %q, want %q", got, want)
	}
	replacement := []string{"SystemRoot=old", "SYSTEMROOT=", "Value=old", "vAlUe=new"}
	if got, want := mergeEnvironment(nil, replacement, true, true), []string{"SYSTEMROOT=", "vAlUe=new"}; !slices.Equal(got, want) {
		t.Errorf("Windows replacement = %q, want %q", got, want)
	}
}

func TestCLIResolutionErrorPreservesDiscoveryClassification(t *testing.T) {
	t.Parallel()
	notFound := types.NewCLINotFoundError("no CLI")
	err := CLIResolutionError(notFound)
	if !types.IsCLINotFoundError(err) || types.IsCLIConnectionError(err) {
		t.Fatalf("discovery error classification changed: %T", err)
	}
	if !types.IsCLIConnectionError(CLIResolutionError(os.ErrNotExist)) {
		t.Fatal("explicit launch resolution failure was not classified as a connection error")
	}
}
