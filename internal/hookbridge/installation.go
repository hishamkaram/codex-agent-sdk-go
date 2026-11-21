package hookbridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// Installation owns the hook listener and the configuration and paths created
// to connect it to the app-server. Its zero value is ready for setup.
type Installation struct {
	mu        sync.Mutex
	resources installationResources
}

type installationResources struct {
	listener   *Listener
	logger     *sdklog.Logger
	hooksPath  string
	backupPath string
	codexHome  string
	socketDir  string
}

// InstallationConfig supplies hook options and the Codex home selected for
// launching the app-server.
type InstallationConfig struct {
	Handler            types.HookHandler
	Timeout            time.Duration
	Logger             *sdklog.Logger
	Mode               types.HookConfigMode
	ShimPath           string
	RequestedCodexHome string
}

// Setup starts the listener and installs its configuration, returning the
// environment additions for the app-server and shim. The caller owns app-server
// lifetime and calls Close after stopping it.
func (i *Installation) Setup(ctx context.Context, cfg InstallationConfig) (_ []string, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	if cfg.Mode == types.HookConfigModeUserHome &&
		!compatibleUserHookHome(filepath.Join(home, ".codex"), cfg.RequestedCodexHome) {
		return nil, fmt.Errorf("user-home hooks require CODEX_HOME to select the default user Codex directory")
	}
	cacheDir := filepath.Join(home, ".cache", "codex-sdk")
	if mkErr := os.MkdirAll(cacheDir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("cache dir: %w", mkErr)
	}
	preferredSocket := filepath.Join(cacheDir, fmt.Sprintf("hook-%d.sock", os.Getpid()))
	socketPath, socketDir := relocateHookSocketUnderPrivateDir(preferredSocket, hookSocketFallbackDir())
	resources := installationResources{logger: cfg.Logger, socketDir: socketDir}
	if resources.logger == nil {
		resources.logger = sdklog.NewLoggerFromZap(nil)
	}
	defer func() {
		if err != nil {
			resources.close()
		}
	}()

	shimPath, err := resolveShimPath(cfg.ShimPath)
	if err != nil {
		return nil, err
	}
	resources.listener, err = New(ctx, Config{
		SocketPath: socketPath,
		Handler:    cfg.Handler,
		Timeout:    cfg.Timeout,
		Logger:     resources.logger,
	})
	if err != nil {
		return nil, err
	}
	codexHome, err := resources.installConfig(home, shimPath, cfg.Mode)
	if err != nil {
		return nil, err
	}

	resources.logger.Info("hook bridge ready",
		zap.String("shim", shimPath),
		zap.String("hooks_json", resources.hooksPath),
		zap.String("socket", socketPath),
		zap.Bool("backed_up_user_config", resources.backupPath != ""))
	i.resources = resources
	return []string{"CODEX_HOME=" + codexHome, "CODEX_SDK_HOOK_SOCKET=" + socketPath}, nil
}

func (r *installationResources) installConfig(home, shimPath string, mode types.HookConfigMode) (string, error) {
	switch mode {
	case "", types.HookConfigModeIsolated:
		codexHome, err := os.MkdirTemp("", "codex-sdk-home-*")
		if err != nil {
			return "", fmt.Errorf("isolated CODEX_HOME: %w", err)
		}
		r.codexHome = codexHome
		if err := r.installIsolatedHooksJSON(codexHome, shimPath); err != nil {
			return "", err
		}
		return codexHome, nil
	case types.HookConfigModeUserHome:
		if err := r.installHooksJSON(home, shimPath); err != nil {
			return "", err
		}
		return filepath.Dir(r.hooksPath), nil
	default:
		return "", fmt.Errorf("unsupported hook config mode %q", mode)
	}
}

// Close claims and releases the installed resources. Release is best effort
// and occurs outside the installation lock because the listener can be serving
// a callback while it closes.
func (i *Installation) Close() {
	resources := i.takeResources()
	resources.close()
}

func (i *Installation) takeResources() installationResources {
	i.mu.Lock()
	defer i.mu.Unlock()

	resources := i.resources
	i.resources = installationResources{}
	return resources
}

func (r *installationResources) close() {
	if r.listener != nil {
		_ = r.listener.Close()
	}
	r.restoreHooksJSON()
	if r.codexHome != "" {
		if err := os.RemoveAll(r.codexHome); err != nil {
			r.logger.Warn("removing isolated CODEX_HOME failed",
				zap.String("codex_home", r.codexHome), zap.Error(err))
		}
	}
	if r.socketDir != "" {
		if err := os.RemoveAll(r.socketDir); err != nil {
			r.logger.Warn("removing hook socket dir failed",
				zap.String("dir", r.socketDir), zap.Error(err))
		}
	}
}
