package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestReplacementEnvironmentAcrossPublicLaunchAndProbes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("connected schema probes require Linux runtime identity proof")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SDK57_PUBLIC_PARENT_ONLY", "parent")
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement(
			"CODEX_HOME=/caller/codex-home",
			"CODEX_SDK_HOOK_SOCKET=/caller/socket",
			"SDK57_PUBLIC_SELECTION=old",
			"GORACE=atexit_sleep_ms=0",
		).
		WithEnv("SDK57_PUBLIC_SELECTION=new").
		WithHookCallback(types.DefaultAllowHookHandler).
		WithShimPath(fixture.executable)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := DiscoverRuntimeControls(ctx, opts, nil); err != nil {
		t.Fatal(err)
	}
	opts.WithExperimentalAPI(true)
	if _, err := DiscoverRuntimeFeatures(ctx, opts); err != nil {
		t.Fatal(err)
	}
	before := readLaunchEnvironments(t, fixture.launchEnv)
	for _, record := range before {
		if record.home != "/caller/codex-home" || record.socket != "/caller/socket" {
			t.Fatalf("standalone %s inherited a connection hook environment: %+v", record.stage, record)
		}
	}
	assertProbeStages(t, before, "--help", "--version", "app-server generate-json-schema")

	_, thread := connectTurnProgramFixture(t, opts)
	opts.WithEnvReplacement("SDK57_PUBLIC_SELECTION=changed-after-connect")
	program := types.CyberAccessProgramStandard
	if _, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program}); err != nil {
		t.Fatal(err)
	}
	connected := readLaunchEnvironments(t, fixture.launchEnv)[len(before):]
	assertProbeStages(t, connected, "--version", "app-server --enable", "app-server generate-json-schema")
	for _, record := range connected {
		if record.home != connected[0].home || record.socket != connected[0].socket {
			t.Fatalf("connected %s read different hook additions: %+v", record.stage, record)
		}
	}
	codexHome, socket := connected[0].home, connected[0].socket
	if !filepath.IsAbs(codexHome) || codexHome == "/caller/codex-home" || socket == "/caller/socket" {
		t.Fatalf("SDK hook variables did not override caller entries: %+v", connected[0])
	}
	if _, err := os.Stat(filepath.Join(codexHome, "hooks.json")); err != nil {
		t.Fatalf("isolated hook file: %v", err)
	}
	if info, err := os.Stat(socket); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("hook socket: info=%v err=%v", info, err)
	}
	if got := os.Getenv("SDK57_PUBLIC_PARENT_ONLY"); got != "parent" {
		t.Fatalf("parent environment was mutated: %q", got)
	}
}

type launchEnvironmentRecord struct {
	stage       string
	home        string
	socket      string
	processHome string
}

func readLaunchEnvironments(t *testing.T, path string) []launchEnvironmentRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []launchEnvironmentRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) != 7 || fields[4] != "new" || fields[5] != "unset" {
			t.Fatalf("child command did not receive exact replacement: %q", line)
		}
		records = append(records, launchEnvironmentRecord{
			stage: strings.TrimSpace(fields[0] + " " + fields[1]),
			home:  fields[2], socket: fields[3], processHome: fields[6],
		})
	}
	return records
}

func assertProbeStages(t *testing.T, records []launchEnvironmentRecord, stages ...string) {
	t.Helper()
	for _, stage := range stages {
		found := false
		for _, record := range records {
			found = found || record.stage == stage
		}
		if !found {
			t.Errorf("did not observe CLI stage %q in %+v", stage, records)
		}
	}
}

func TestStandaloneProbesIgnoreLaunchDirectoryAndTrimCLIPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a symlink to the test executable")
	}
	t.Parallel()
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath("  " + fixture.executable + "  ").
		WithCwd(filepath.Join(t.TempDir(), "does-not-exist")).
		WithEnvReplacement("GORACE=atexit_sleep_ms=0")
	if _, err := DiscoverRuntimeControls(t.Context(), opts, nil); err != nil {
		t.Fatalf("standalone controls: %v", err)
	}
	opts.WithExperimentalAPI(true)
	if _, err := DiscoverRuntimeFeatures(t.Context(), opts); err != nil {
		t.Fatalf("standalone features: %v", err)
	}
}

func TestConflictingUserHookHomeRejectedBeforeConfigWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a symlink to the test executable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	const original = `{"original":true}`
	if err := os.WriteFile(hooksPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement("CODEX_HOME=" + t.TempDir()).
		WithHookCallback(types.DefaultAllowHookHandler).
		WithHookConfigMode(types.HookConfigModeUserHome).
		WithShimPath(fixture.executable)
	client, err := NewClient(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	connectErr := client.Connect(t.Context())
	if connectErr == nil || !strings.Contains(connectErr.Error(), "user-home hooks require CODEX_HOME") {
		t.Fatalf("hook conflict error = %v", connectErr)
	}
	current, readErr := os.ReadFile(hooksPath)
	if readErr != nil || string(current) != original {
		t.Fatalf("existing hook config changed before conflict was rejected: %v", readErr)
	}
	if _, probeErr := os.Stat(fixture.launchEnv); !os.IsNotExist(probeErr) {
		t.Fatalf("hook conflict started a CLI process: %v", probeErr)
	}
}

func TestUserHomeHookConfigPinnedAcrossChildHomeReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a symlink to the test executable")
	}
	home, childHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	const original = `{"original":true}`
	if err := os.WriteFile(hooksPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement("HOME="+childHome, "SDK57_PUBLIC_SELECTION=new", "GORACE=atexit_sleep_ms=0").
		WithHookCallback(types.DefaultAllowHookHandler).
		WithHookConfigMode(types.HookConfigModeUserHome).
		WithShimPath(fixture.executable)
	client, _ := connectTurnProgramFixture(t, opts)
	if installed, err := os.ReadFile(hooksPath); err != nil || string(installed) == original {
		t.Fatalf("user-home hooks were not installed: %v", err)
	}
	records := readLaunchEnvironments(t, fixture.launchEnv)
	assertProbeStages(t, records, "--version", "app-server --enable")
	for _, record := range records {
		if record.home != codexHome || record.processHome != childHome {
			t.Fatalf("%s: CODEX_HOME=%q HOME=%q, want %q and %q", record.stage, record.home, record.processHome, codexHome, childHome)
		}
	}

	if err := client.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if restored, err := os.ReadFile(hooksPath); err != nil || string(restored) != original {
		t.Fatalf("Close did not restore the original hooks file: %v", err)
	}
}
