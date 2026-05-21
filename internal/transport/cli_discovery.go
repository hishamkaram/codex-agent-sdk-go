package transport

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

var defaultCLIDiscoveryCache cliDiscoveryCache

var cliFallbackLocations = []string{
	"~/.codex/bin/codex",
	"/opt/homebrew/bin/codex",
	"/usr/local/bin/codex",
	"~/.npm-global/bin/codex",
	"~/.local/bin/codex",
	"~/node_modules/.bin/codex",
}

type cliDiscoveryCache struct {
	mu       sync.Mutex
	path     string
	inFlight *cliDiscoveryFlight
}

type cliDiscoveryFlight struct {
	done chan struct{}
	path string
	err  error
}

func (c *cliDiscoveryCache) find(discover func() (string, error)) (string, error) {
	c.mu.Lock()
	if c.path != "" {
		path := c.path
		c.mu.Unlock()
		return path, nil
	}
	if flight := c.inFlight; flight != nil {
		c.mu.Unlock()
		<-flight.done
		return flight.path, flight.err
	}

	flight := &cliDiscoveryFlight{done: make(chan struct{})}
	c.inFlight = flight
	c.mu.Unlock()

	path, err := discover()

	c.mu.Lock()
	if err == nil {
		c.path = path
	}
	flight.path = path
	flight.err = err
	c.inFlight = nil
	close(flight.done)
	c.mu.Unlock()

	return path, err
}

// FindCLI searches for the codex CLI binary in standard locations:
//  1. PATH via exec.LookPath("codex")
//  2. ~/.codex/bin/codex (codex-installed default)
//  3. /opt/homebrew/bin/codex (macOS ARM brew)
//  4. /usr/local/bin/codex (linux + macOS intel brew + manual installs)
//  5. ~/.npm-global/bin/codex (npm global prefix override)
//  6. ~/.local/bin/codex (pip --user / manual installs)
//  7. ~/node_modules/.bin/codex (local npm install)
//
// Returns the absolute path or a *types.CLINotFoundError with remediation
// guidance.
func FindCLI() (string, error) {
	return defaultCLIDiscoveryCache.find(findCLIUncached)
}

func findCLIUncached() (string, error) {
	if cliPath, err := exec.LookPath("codex"); err == nil {
		return cliPath, nil
	}

	for _, location := range cliFallbackLocations {
		expanded := expandHome(location)
		if _, err := os.Stat(expanded); err == nil {
			return expanded, nil
		}
	}

	return "", types.NewCLINotFoundError(
		"codex CLI not found. Install with:\n" +
			"  npm install -g @openai/codex\n" +
			"\n" +
			"or via Homebrew:\n" +
			"  brew install codex\n" +
			"\n" +
			"Or pass an explicit path via CodexOptions.WithCLIPath(\"/path/to/codex\").",
	)
}

// expandHome expands a leading ~ in path to the user's home directory.
// Returns path unchanged if ~ is not at position 0 or if the home
// directory cannot be determined.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	usr, err := user.Current()
	if err != nil {
		return path
	}
	if path == "~" {
		return usr.HomeDir
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(usr.HomeDir, path[2:])
	}
	return path
}
