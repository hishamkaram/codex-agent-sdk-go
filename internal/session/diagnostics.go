package session

import (
	"context"
	"errors"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

func (s *Session) closeTransportAfterConnectError(ctx context.Context, tr *transport.AppServer) string {
	if s.closed.Load() {
		return redactedStderrDiagnosticTail(tr.Stderr())
	}
	_ = tr.Close(context.WithoutCancel(ctx))
	return redactedStderrDiagnosticTail(tr.Stderr())
}

func (s *Session) logInitializeFailure(err error, stderrTail string) {
	fields := []zap.Field{zap.String("error_kind", initializeFailureKind(err))}
	var rpcErr *types.RPCError
	if errors.As(err, &rpcErr) {
		fields = append(fields, zap.Int("rpc_error_code", rpcErr.Code))
	}
	if stderrTail != "" {
		fields = append(fields, zap.String("stderr_tail", stderrTail))
	}
	s.logger.Warn("codex.Client.Connect: initialize failed", fields...)
}

const stderrDiagnosticTailLimit = 2048

const (
	redactedStderrLine                    = "<redacted>"
	controlProtocolInitializationFailed   = "control protocol initialization failed"
	initializeFailureKindDeadlineExceeded = "deadline_exceeded"
	initializeFailureKindContextCanceled  = "context_canceled"
	initializeFailureKindRPCError         = "rpc_error"
	initializeFailureKindOther            = "other"
)

func initializeFailureKind(err error) string {
	var rpcErr *types.RPCError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return initializeFailureKindDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return initializeFailureKindContextCanceled
	case errors.As(err, &rpcErr):
		return initializeFailureKindRPCError
	default:
		return initializeFailureKindOther
	}
}

func redactedStderrDiagnosticTail(stderr string) string {
	if stderr == "" {
		return ""
	}
	stderr = redactStderrDiagnosticLines(stderr)
	truncated := false
	if len(stderr) > stderrDiagnosticTailLimit {
		stderr = stderr[len(stderr)-stderrDiagnosticTailLimit:]
		truncated = true
	}
	if truncated {
		return "[truncated]\n" + stderr
	}
	return stderr
}

func redactStderrDiagnosticLines(stderr string) string {
	lines := strings.SplitAfter(stderr, "\n")
	for i, line := range lines {
		lines[i] = redactStderrDiagnosticLine(line)
	}
	return strings.Join(lines, "")
}

func redactStderrDiagnosticLine(line string) string {
	if line == "" {
		return ""
	}
	suffix := ""
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
		suffix = "\n"
	}
	if strings.TrimSpace(line) == "" {
		return suffix
	}
	if strings.HasPrefix(strings.TrimSpace(line), controlProtocolInitializationFailed) {
		return controlProtocolInitializationFailed + suffix
	}
	return redactedStderrLine + suffix
}

func buildInitializeParams(opts *types.CodexOptions) map[string]any {
	capabilities := map[string]any{
		"experimentalApi": opts.ExperimentalAPI,
	}
	if len(opts.OptOutNotificationMethods) > 0 {
		capabilities["optOutNotificationMethods"] = append([]string(nil), opts.OptOutNotificationMethods...)
	}
	return map[string]any{
		"clientInfo": map[string]any{
			"name":    opts.ClientName,
			"version": opts.ClientVersion,
			"title":   opts.ClientTitle,
		},
		"capabilities": capabilities,
	}
}
