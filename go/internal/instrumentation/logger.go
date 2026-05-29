// Package instrumentation provides logging and (eventually) metrics primitives.
// It is the Go replacement for NLog usage in NzbDrone.Common.Instrumentation.
package instrumentation

import (
	"io"
	"log/slog"
)

// NewLogger returns a structured slog.Logger writing to w at the given level,
// plus the [Fanout] backing it. Output format is text by default; the
// JSON-line layout used in production will be selected via config once the
// config package lands.
//
// The returned Fanout lets the caller attach additional sub-handlers at
// runtime — e.g. the log-DB sink, which can only be wired once the RoleLog
// datastore is open (see internal/logs.Sink). Callers that don't need the
// DB sink can ignore the second return value.
func NewLogger(w io.Writer, level slog.Level) (*slog.Logger, *Fanout) {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	})
	fanout := NewFanout(handler)
	return slog.New(fanout), fanout
}
