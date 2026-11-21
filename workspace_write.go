package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type workspaceWriteSettings struct {
	NetworkAccess *bool    `json:"network_access,omitempty"`
	WritableRoots []string `json:"writable_roots,omitzero"`
}

func resolveWorkspaceWrite(sandbox types.SandboxMode, opts *types.ThreadOptions) (*workspaceWriteSettings, error) {
	if opts == nil || (opts.NetworkAccessEnabled == nil && opts.AdditionalDirectories == nil) {
		return nil, nil
	}
	if sandbox != types.SandboxWorkspaceWrite {
		return nil, errors.New("NetworkAccessEnabled and AdditionalDirectories require an explicit workspace-write sandbox")
	}

	roots, err := validateWritableRoots(opts.AdditionalDirectories)
	if err != nil {
		return nil, err
	}
	settings := &workspaceWriteSettings{WritableRoots: roots}
	if opts.NetworkAccessEnabled != nil {
		network := *opts.NetworkAccessEnabled
		settings.NetworkAccess = &network
	}
	return settings, nil
}

func validateWritableRoots(paths []string) ([]string, error) {
	if paths == nil {
		return nil, nil
	}
	roots := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for i, path := range paths {
		if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || !utf8.ValidString(path) {
			return nil, fmt.Errorf("AdditionalDirectories[%d] must be a valid absolute path", i)
		}
		// Lexically resolving a parent segment across a symlink can
		// select a different directory. Require unambiguous input.
		if slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "..") {
			return nil, fmt.Errorf("AdditionalDirectories[%d] must not contain parent traversal", i)
		}
		if _, exists := seen[path]; !exists {
			seen[path] = struct{}{}
			roots = append(roots, path)
		}
	}
	return roots, nil
}

type workspaceWriteReadback struct {
	Cwd     string `json:"cwd"`
	Sandbox struct {
		Type          string   `json:"type"`
		NetworkAccess *bool    `json:"networkAccess"`
		WritableRoots []string `json:"writableRoots"`
	} `json:"sandbox"`
}

// expectedThreadCwd resolves a specified cwd relative to the subprocess's
// working directory, matching the app-server's cwd override semantics.
func expectedThreadCwd(appServerCwd, requestedCwd string) (string, error) {
	if requestedCwd == "" || filepath.IsAbs(requestedCwd) {
		return requestedCwd, nil
	}
	if !filepath.IsAbs(appServerCwd) {
		return "", errors.New("app-server launch directory is required for relative thread cwd")
	}
	return filepath.Join(appServerCwd, requestedCwd), nil
}

func verifyWorkspaceWrite(result json.RawMessage, threadID, expectedCwd string, request *workspaceWriteSettings) (string, error) {
	var reply workspaceWriteReadback
	if err := json.Unmarshal(result, &reply); err != nil {
		return "", &types.ThreadPolicyMismatchError{ThreadID: threadID, Field: "cwd or sandbox"}
	}
	if !filepath.IsAbs(reply.Cwd) || (expectedCwd != "" && filepath.Clean(expectedCwd) != filepath.Clean(reply.Cwd)) {
		return "", &types.ThreadPolicyMismatchError{ThreadID: threadID, Field: "cwd"}
	}
	if reply.Sandbox.Type != "workspaceWrite" {
		return "", &types.ThreadPolicyMismatchError{ThreadID: threadID, Field: "sandbox.type"}
	}
	if request.NetworkAccess != nil && (reply.Sandbox.NetworkAccess == nil || *reply.Sandbox.NetworkAccess != *request.NetworkAccess) {
		return "", &types.ThreadPolicyMismatchError{ThreadID: threadID, Field: "sandbox.networkAccess"}
	}
	if request.WritableRoots != nil && !matchingWritableRoots(request.WritableRoots, reply.Sandbox.WritableRoots) {
		return "", &types.ThreadPolicyMismatchError{ThreadID: threadID, Field: "sandbox.writableRoots"}
	}
	return reply.Cwd, nil
}

// Check a set of explicit roots. An extra returned root could broaden writes
// even when it contains one of the requested directories.
func matchingWritableRoots(requested, reported []string) bool {
	if reported == nil {
		return false
	}
	roots, err := validateWritableRoots(reported)
	if err != nil || len(roots) != len(requested) {
		return false
	}
	for _, root := range roots {
		if !slices.Contains(requested, root) {
			return false
		}
	}
	return true
}
