package log

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewLoggerFiltersVerboseMessages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		verbose bool
	}{
		{name: "quiet"},
		{name: "verbose", verbose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := NewLogger(tc.verbose).Zap()
			for _, level := range []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel} {
				want := tc.verbose || level >= zapcore.WarnLevel
				if got := logger.Core().Enabled(level); got != want {
					t.Errorf("level %s enabled = %v, want %v", level, got, want)
				}
			}
		})
	}
}

func TestLoggerUsesInjectedZapCore(t *testing.T) {
	t.Parallel()
	core, observed := observer.New(zap.DebugLevel)
	injected := zap.New(core).Named("injected")
	logger := NewLoggerFromZap(injected)
	if logger.Zap() != injected {
		t.Fatal("Zap did not return the injected logger")
	}

	const msg = "session state changed"
	logger.Debug(msg)
	logger.Info(msg)
	logger.Warn(msg)
	logger.Error(msg, zap.String("reason", "closed"))
	entries := observed.All()
	want := []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel}
	if len(entries) != len(want) {
		t.Fatalf("logged %d entries, want %d", len(entries), len(want))
	}
	for i, entry := range entries {
		if entry.Level != want[i] || entry.Message != msg || entry.LoggerName != "injected" {
			t.Errorf("entry %d = %+v, want %s from injected logger", i, entry, want[i])
		}
	}
	if got := entries[3].ContextMap()["reason"]; got != "closed" {
		t.Errorf("error reason = %v, want closed", got)
	}
}
