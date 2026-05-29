package logs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Sink is an slog.Handler that persists log records into the log
// database — the Go-port equivalent of the C# NLog `DatabaseTarget`
// (`src/NzbDrone.Core/Instrumentation/DatabaseTarget.cs`). Like the C#
// target it writes asynchronously (a background goroutine drains a
// buffered channel) so logging never blocks on a DB write, and it logs
// at Info and above by default (the C# rule is `new LoggingRule("*",
// LogLevel.Info, target)`).
//
// Field mapping mirrors DatabaseTarget.Write:
//   - Time / Level / Message from the record;
//   - Logger from a "component"/"logger" attribute when present (slog
//     has no per-logger name like NLog's LoggerName; the C# target
//     strips the "NzbDrone." prefix — the Go side has no such prefix);
//   - Exception / ExceptionType from an "err"/"error" attribute whose
//     value is an error; the exception message is appended to the log
//     message exactly as the C# target does.
//
// WithGroup is a no-op for the flat fields the sink extracts (groups
// don't change Logger/err/Message); WithAttrs accumulates attributes so
// a `logger.With("component", ...)` still tags the persisted Logger.
//
// Concurrency: the async machinery lives in a shared [sinkCore] so
// WithAttrs can return a lightweight handler that shares the one writer
// goroutine + channel. When the buffer is full a record is dropped
// (counted) rather than blocking the caller — a log sink must never
// stall the application.
type Sink struct {
	core     *sinkCore
	minLevel slog.Level
	attrs    []slog.Attr
}

type sinkCore struct {
	repo      *Repo
	ch        chan Log
	wg        sync.WaitGroup
	dropped   atomic.Int64
	closeOnce sync.Once
}

// NewSink starts the background writer and returns the root Sink.
// minLevel gates Enabled (Info+ matches the C# DatabaseTarget rule);
// buffer is the channel depth (<=0 → 1024). Call Close on shutdown to
// flush the buffer.
func NewSink(repo *Repo, minLevel slog.Level, buffer int) *Sink {
	if buffer <= 0 {
		buffer = 1024
	}
	core := &sinkCore{repo: repo, ch: make(chan Log, buffer)}
	core.wg.Add(1)
	go core.run()
	return &Sink{core: core, minLevel: minLevel}
}

func (c *sinkCore) run() {
	defer c.wg.Done()
	for rec := range c.ch {
		// Best-effort: a DB error must not crash the writer goroutine
		// (the C# target logs to NLog's InternalLogger and moves on).
		_, _ = c.repo.Insert(context.Background(), &rec)
	}
}

// Enabled reports whether the level meets the sink threshold.
func (s *Sink) Enabled(_ context.Context, level slog.Level) bool {
	return level >= s.minLevel
}

// Handle enqueues the record for async persistence. Never blocks: a full
// buffer drops the record and bumps the dropped counter.
func (s *Sink) Handle(_ context.Context, r slog.Record) error {
	entry := s.build(r)
	select {
	case s.core.ch <- entry:
	default:
		s.core.dropped.Add(1)
	}
	return nil
}

// build projects a record (+ accumulated attrs) into a Log row.
func (s *Sink) build(r slog.Record) Log {
	l := Log{Time: r.Time.UTC(), Level: levelName(r.Level), Message: r.Message}
	apply := func(a slog.Attr) {
		switch a.Key {
		case "component", "logger":
			l.Logger = a.Value.String()
		case "err", "error":
			if e, ok := a.Value.Any().(error); ok {
				l.Exception = e.Error()
				l.ExceptionType = fmt.Sprintf("%T", e)
			} else if v := a.Value.String(); v != "" {
				l.Exception = v
			}
		}
	}
	for _, a := range s.attrs {
		apply(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		apply(a)
		return true
	})
	// Mirror DatabaseTarget.Write: fold the exception into the message.
	if l.Exception != "" {
		if l.Message == "" {
			l.Message = l.Exception
		} else {
			l.Message += ": " + l.Exception
		}
	}
	if l.Time.IsZero() {
		l.Time = time.Now().UTC()
	}
	return l
}

// WithAttrs returns a handler that also carries attrs, sharing the same
// async writer.
func (s *Sink) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return s
	}
	merged := make([]slog.Attr, 0, len(s.attrs)+len(attrs))
	merged = append(merged, s.attrs...)
	merged = append(merged, attrs...)
	return &Sink{core: s.core, minLevel: s.minLevel, attrs: merged}
}

// WithGroup is a no-op for the sink's flat field extraction.
func (s *Sink) WithGroup(string) slog.Handler { return s }

// Close stops the writer goroutine after draining buffered records
// (flush-on-shutdown). Idempotent.
func (s *Sink) Close() error {
	s.core.closeOnce.Do(func() { close(s.core.ch) })
	s.core.wg.Wait()
	return nil
}

// Dropped returns the number of records dropped due to a full buffer.
func (s *Sink) Dropped() int64 { return s.core.dropped.Load() }

// levelName maps an slog level to the NLog-style title-case name the C#
// DatabaseTarget persists (and the React UI's level filter expects).
func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "Debug"
	case l < slog.LevelWarn:
		return "Info"
	case l < slog.LevelError:
		return "Warn"
	default:
		return "Error"
	}
}
