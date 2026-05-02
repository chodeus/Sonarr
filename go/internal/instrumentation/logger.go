// Package instrumentation provides logging and (eventually) metrics primitives.
// It is the Go replacement for NLog usage in NzbDrone.Common.Instrumentation.
package instrumentation

import (
	"io"
	"log/slog"
)

// NewLogger returns a structured slog.Logger writing to w at the given level.
// Output format is text by default; the JSON-line layout used in production
// will be selected via config once the config package lands.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	})
	return slog.New(handler)
}
