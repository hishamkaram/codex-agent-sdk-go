package transport

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// CLI is a resolved Codex executable, working directory, and child environment.
// The snapshot is immutable and can be shared by the app-server and its probes.
type CLI struct {
	path       string
	cwd        string
	env        []string
	executable os.FileInfo
	image      *os.File // scoped to a probe pinned to a running executable
}

// ResolveCLI snapshots the execution inputs. A missing executable uses the
// SDK's normal discovery. An explicit executable is resolved from the SDK
// process before applying the child's working directory or environment.
func ResolveCLI(path, cwd string, env []string, replace bool) (CLI, error) {
	resolvedPath, err := resolveCLIPath(path)
	if err != nil {
		return CLI{}, err
	}
	resolvedCwd, err := resolveAppServerWorkingDirectory(cwd)
	if err != nil {
		return CLI{}, fmt.Errorf("resolve Codex working directory: %w", err)
	}
	if replace {
		if envErr := validateReplacementEnvironment(env); envErr != nil {
			return CLI{}, envErr
		}
	}
	var parent []string
	if !replace {
		parent = os.Environ()
	}
	entries := mergeEnvironment(parent, env, replace, runtime.GOOS == "windows")
	if runtime.GOOS == "windows" && !environmentHasKey(entries, "SYSTEMROOT") {
		// Snapshot the critical variable Go would otherwise inject at each
		// invocation, including when the caller requests an empty environment.
		entries = append(entries, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return CLI{}, fmt.Errorf("inspect Codex executable: %w", err)
	}
	return CLI{path: resolvedPath, cwd: resolvedCwd, env: entries, executable: info}, nil
}

// CLIResolutionError preserves the not-found classification from discovery
// and classifies other launch resolution errors as connection failures.
func CLIResolutionError(err error) error {
	if types.IsCLINotFoundError(err) {
		return err
	}
	return types.NewCLIConnectionError("resolve Codex runtime", err)
}

func resolveCLIPath(path string) (string, error) {
	if path == "" {
		resolved, err := FindCLI()
		if err != nil {
			return "", err
		}
		path = resolved
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("locate Codex executable: %w", err)
	}
	if filepath.IsAbs(resolved) {
		return resolved, nil
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve Codex executable: %w", err)
	}
	return absolute, nil
}

// PinRunningExecutable opens the kernel's executable image for the app-server
// and verifies it matches the original launch selection. The caller must call
// release after the last probe. Runtime identity proof is supported on Linux
// hosts that permit opening /proc/<pid>/exe.
func (cli CLI) PinRunningExecutable(pid int) (pinned CLI, release func(), err error) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return CLI{}, nil, fmt.Errorf("cannot prove the running Codex executable on this host")
	}
	image, openErr := os.Open(processExecutablePath(pid))
	if openErr != nil {
		return CLI{}, nil, fmt.Errorf("open running Codex executable: %w", openErr)
	}
	info, statErr := image.Stat()
	if statErr != nil {
		_ = image.Close()
		return CLI{}, nil, fmt.Errorf("stat running Codex executable: %w", statErr)
	}
	if !cli.matchesExecutable(info) {
		_ = image.Close()
		return CLI{}, nil, fmt.Errorf("running Codex executable differs from the launch selection")
	}
	cli.image = image
	return cli, func() { _ = image.Close() }, nil
}

// MatchesRunningExecutable rechecks the live image after a schema invocation.
func (cli CLI) MatchesRunningExecutable(pid int) bool {
	if runtime.GOOS != "linux" || pid <= 0 {
		return false
	}
	info, err := os.Stat(processExecutablePath(pid))
	return err == nil && cli.matchesExecutable(info)
}

func processExecutablePath(pid int) string {
	return "/proc/" + strconv.Itoa(pid) + "/exe"
}

func (cli CLI) matchesExecutable(info os.FileInfo) bool {
	return info != nil && cli.executable != nil && os.SameFile(cli.executable, info) &&
		cli.executable.Size() == info.Size() && cli.executable.ModTime().Equal(info.ModTime())
}

// EnvironmentValue returns a value from the resolved child environment.
func (cli CLI) EnvironmentValue(key string) string {
	fold := runtime.GOOS == "windows"
	if fold {
		key = strings.ToLower(key)
	}
	for _, entry := range cli.env {
		if environmentKey(entry, fold) == key {
			_, value, _ := splitEnvironmentEntry(entry, fold)
			return value
		}
	}
	return ""
}

// WithEnvironmentAdditions applies required SDK values to the resolved
// environment without rereading or mutating the parent environment.
func (cli CLI) WithEnvironmentAdditions(entries []string) CLI {
	cli.env = mergeEnvironment(cli.env, entries, false, runtime.GOOS == "windows")
	return cli
}

func (cli CLI) configure(cmd *exec.Cmd) {
	cmd.Dir = cli.cwd
	cmd.Env = cli.env
}

// BuildRuntimeEnvironment overlays keyEqVals on os.Environ. An entry "KEY="
// unsets KEY. Use ResolveCLI for one environment shared across invocations.
func BuildRuntimeEnvironment(keyEqVals []string) []string {
	return mergeEnvironment(os.Environ(), keyEqVals, false, runtime.GOOS == "windows")
}

func validateReplacementEnvironment(entries []string) error {
	for i, entry := range entries {
		key, _, ok := splitEnvironmentEntry(entry, runtime.GOOS == "windows")
		if !ok || key == "" || strings.ContainsRune(entry, 0) {
			return fmt.Errorf("invalid Codex environment entry at index %d: expected KEY=VALUE", i)
		}
	}
	return nil
}

func mergeEnvironment(parent, entries []string, replace, caseInsensitive bool) []string {
	out := make([]string, 0, len(parent)+len(entries))
	lastEntry := make(map[string]int, len(entries))
	for i, entry := range entries {
		lastEntry[environmentKey(entry, caseInsensitive)] = i
	}
	for _, entry := range parent {
		if _, replaced := lastEntry[environmentKey(entry, caseInsensitive)]; !replaced {
			out = append(out, entry)
		}
	}
	for i, entry := range entries {
		_, value, _ := splitEnvironmentEntry(entry, caseInsensitive)
		if lastEntry[environmentKey(entry, caseInsensitive)] == i && (replace || value != "") {
			out = append(out, entry)
		}
	}
	return out
}

func environmentKey(entry string, caseInsensitive bool) string {
	key, _, _ := splitEnvironmentEntry(entry, caseInsensitive)
	if caseInsensitive {
		return strings.ToLower(key)
	}
	return key
}

func environmentHasKey(entries []string, key string) bool {
	for _, entry := range entries {
		name, _, ok := splitEnvironmentEntry(entry, true)
		if ok && strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}

func splitEnvironmentEntry(entry string, windows bool) (key, value string, ok bool) {
	if windows && strings.HasPrefix(entry, "=") {
		// Windows also supports entries of the form "=C:=C:\\path".
		key, value, ok := strings.Cut(entry[1:], "=")
		return "=" + key, value, ok
	}
	return strings.Cut(entry, "=")
}
