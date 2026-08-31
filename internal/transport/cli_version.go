package transport

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RecommendedCLIVersion is the minimum tested codex CLI version. The SDK
// does NOT reject older versions — the check is soft (probe + warn) per the
// v0.1.0 design. Callers can inspect the version via ProbeCLIVersion.
const (
	RecommendedCLIVersion = "0.144.1"
	cliVersionWaitDelay   = 2 * time.Second
)

// SemVer is a minimal semantic version struct.
type SemVer struct {
	Major int
	Minor int
	Patch int
}

// CLIVersionProbeResult caches one CLI version probe so higher layers can use
// the same compatibility decision and transport log without spawning a second
// `codex --version` process.
type CLIVersionProbeResult struct {
	Version SemVer
	Err     error
}

// String returns "major.minor.patch".
func (v SemVer) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v >= required.
func (v SemVer) AtLeast(required SemVer) bool {
	if v.Major != required.Major {
		return v.Major > required.Major
	}
	if v.Minor != required.Minor {
		return v.Minor > required.Minor
	}
	return v.Patch >= required.Patch
}

var semverRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseSemVer extracts the first major.minor.patch triplet from s.
// A leading "codex " or "v" is tolerated. Build metadata and pre-release
// suffixes are discarded.
func ParseSemVer(s string) (SemVer, error) {
	matches := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if len(matches) != 4 {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: no semver found in %q", s)
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: major: %w", err)
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: minor: %w", err)
	}
	patch, err := strconv.Atoi(matches[3])
	if err != nil {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: patch: %w", err)
	}
	return SemVer{Major: major, Minor: minor, Patch: patch}, nil
}

// ProbeCLIVersion runs `<cliPath> --version` with a 5s timeout and parses
// the semver from the output. Returns the parsed SemVer or an error if the
// binary can't be exec'd or the output doesn't contain a semver.
func ProbeCLIVersion(cliPath string) (SemVer, error) {
	return probeCLIVersionCtx(context.Background(), cliPath)
}

// ProbeCLIVersionContext probes the CLI using the caller's context and runtime
// environment overlay.
func ProbeCLIVersionContext(
	ctx context.Context,
	cliPath string,
	env []string,
) (SemVer, error) {
	return probeCLIVersionWithEnvironment(ctx, cliPath, env)
}

// probeCLIVersionCtx is the context-threading core of ProbeCLIVersion. The
// 5s probe timeout is layered onto the caller's parent ctx so connect-time
// cancellation (e.g. the Connect context being canceled) aborts the probe
// instead of leaving it to run to the full timeout.
func probeCLIVersionCtx(parent context.Context, cliPath string) (SemVer, error) {
	return probeCLIVersionWithEnvironment(parent, cliPath, nil)
}

func probeCLIVersionWithEnvironment(
	parent context.Context,
	cliPath string,
	env []string,
) (SemVer, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	stdout, stderr, err := RunCLICommand(ctx, cliPath, env, cliVersionWaitDelay, "--version")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SemVer{}, fmt.Errorf("transport.ProbeCLIVersion: %w", ctxErr)
		}
		return SemVer{}, fmt.Errorf("transport.ProbeCLIVersion: run %q --version: %w (stderr=%q)",
			cliPath, err, stderr)
	}
	return ParseSemVer(stdout)
}
