package transport

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RecommendedCLIVersion is the minimum tested codex CLI version. The SDK
// does NOT reject older versions — the check is soft (probe + warn) per the
// v0.1.0 design. Callers can inspect the version via ProbeCLIVersion.
const RecommendedCLIVersion = "0.121.0"

// SemVer is a minimal semantic version struct.
type SemVer struct {
	Major int
	Minor int
	Patch int
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
	maj, err := strconv.Atoi(matches[1])
	if err != nil {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: major: %w", err)
	}
	min, err := strconv.Atoi(matches[2])
	if err != nil {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: minor: %w", err)
	}
	pat, err := strconv.Atoi(matches[3])
	if err != nil {
		return SemVer{}, fmt.Errorf("transport.ParseSemVer: patch: %w", err)
	}
	return SemVer{Major: maj, Minor: min, Patch: pat}, nil
}

// ProbeCLIVersion runs `<cliPath> --version` with a 5s timeout and parses
// the semver from the output. Returns the parsed SemVer or an error if the
// binary can't be exec'd or the output doesn't contain a semver.
func ProbeCLIVersion(cliPath string) (SemVer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliPath, "--version")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return SemVer{}, fmt.Errorf("transport.ProbeCLIVersion: run %q --version: %w (stderr=%q)",
			cliPath, err, stderr.String())
	}
	return ParseSemVer(stdout.String())
}
