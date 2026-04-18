// Package log provides a minimal zap wrapper for the SDK.
//
// The SDK never uses a global logger — a *Logger is constructor-injected into
// transport, demux, and client code. Callers that want no log output pass
// NewLogger(false) or NewLoggerFromZap(zap.NewNop()).
package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps a *zap.Logger for the SDK.
type Logger struct {
	zap *zap.Logger
}

// NewLogger creates a new Logger. When verbose is true, Debug and Info
// messages are emitted; otherwise only Warn and Error messages are logged.
//
// Output goes to stderr. Timestamps are omitted for concise SDK output.
func NewLogger(verbose bool) *Logger {
	level := zapcore.WarnLevel
	if verbose {
		level = zapcore.DebugLevel
	}

	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Encoding:         "console",
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			MessageKey:     "msg",
			LevelKey:       "level",
			TimeKey:        "",
			EncodeLevel:    zapcore.CapitalLevelEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
		},
	}

	l, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return &Logger{zap: zap.NewNop()}
	}
	return &Logger{zap: l.Named("codex-sdk")}
}

// NewLoggerFromZap wraps an existing *zap.Logger. Useful for tests or when
// the caller already has a configured logger.
func NewLoggerFromZap(z *zap.Logger) *Logger {
	if z == nil {
		return &Logger{zap: zap.NewNop()}
	}
	return &Logger{zap: z}
}

// Debug logs a debug-level message with structured fields.
func (l *Logger) Debug(msg string, fields ...zap.Field) { l.zap.Debug(msg, fields...) }

// Info logs an info-level message with structured fields.
func (l *Logger) Info(msg string, fields ...zap.Field) { l.zap.Info(msg, fields...) }

// Warn logs a warning-level message with structured fields.
func (l *Logger) Warn(msg string, fields ...zap.Field) { l.zap.Warn(msg, fields...) }

// Error logs an error-level message with structured fields.
func (l *Logger) Error(msg string, fields ...zap.Field) { l.zap.Error(msg, fields...) }

// Zap returns the underlying *zap.Logger for direct access.
func (l *Logger) Zap() *zap.Logger { return l.zap }
