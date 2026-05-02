package instrumentation

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLoggerWritesAtLevel(t *testing.T) {
	tests := []struct {
		name   string
		level  slog.Level
		emit   slog.Level
		expect bool
	}{
		{"info-emits-info", slog.LevelInfo, slog.LevelInfo, true},
		{"info-suppresses-debug", slog.LevelInfo, slog.LevelDebug, false},
		{"debug-emits-debug", slog.LevelDebug, slog.LevelDebug, true},
		{"warn-suppresses-info", slog.LevelWarn, slog.LevelInfo, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger(&buf, tc.level)
			logger.Log(t.Context(), tc.emit, "hello", "k", "v")
			got := strings.Contains(buf.String(), "hello")
			if got != tc.expect {
				t.Fatalf("level=%v emit=%v: got emitted=%v, want %v (output=%q)", tc.level, tc.emit, got, tc.expect, buf.String())
			}
		})
	}
}
