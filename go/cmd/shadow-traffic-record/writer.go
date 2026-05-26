// Package main — writer.go implements the async JSONL log writer used
// by the recording proxy. Each captured request becomes one line in a
// file format compatible with cmd/shadow-traffic-replay's LogReader.
//
// The writer runs a single background goroutine that drains a buffered
// channel into the on-disk file. Producer goroutines (one per inbound
// request) never block on disk I/O — the channel buffer absorbs bursts
// up to its size, and when the buffer is full a request's record is
// dropped + counted rather than blocking the request hot path. This
// trade keeps the recording proxy's latency hit small (best case:
// channel-send only) at the cost of best-effort capture under heavy
// load.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RecordedEntry mirrors the JSONL log shape consumed by
// cmd/shadow-traffic-replay's LogEntry — kept as a separate type so
// this binary doesn't import a sibling cmd/.
type RecordedEntry struct {
	Timestamp time.Time         `json:"ts"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	Status    int               `json:"status,omitempty"`
}

// LogWriter is an async JSONL writer with a bounded queue.
type LogWriter struct {
	path       string
	queue      chan RecordedEntry
	logger     *slog.Logger
	maxBodyLen int

	mu     sync.Mutex
	file   *os.File
	bw     *bufio.Writer
	closed atomic.Bool

	// Stats — atomic, safe to read from any goroutine.
	written atomic.Int64
	dropped atomic.Int64

	wg sync.WaitGroup
}

// LogWriterOptions configures a LogWriter.
type LogWriterOptions struct {
	// QueueSize is the in-memory buffer between producers and the
	// disk-writer goroutine. Recommend ≥ peak QPS × expected disk
	// stall (e.g. 1000 QPS × 200ms = 200 entries).
	QueueSize int
	// FlushInterval controls how often the buffered writer is
	// flushed to disk. Records persist on the next flush rather
	// than per-write to amortise fsync cost.
	FlushInterval time.Duration
	// MaxBodyLen caps the per-request body size committed to the
	// log. 0 = no cap. Long bodies (full library refresh response,
	// huge custom-format bulk POSTs) bloat the log without aiding
	// shadow-diff replay; set this to e.g. 64KiB for a balance.
	MaxBodyLen int
	// SyncOnClose forces an fsync at Close time. Recommended for
	// production captures; tests can leave it off for speed.
	SyncOnClose bool
	Logger      *slog.Logger
}

// NewLogWriter opens path (creating + appending) and starts the
// background drain goroutine. The caller owns Close lifetime.
func NewLogWriter(path string, opts LogWriterOptions) (*LogWriter, error) {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 256
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	w := &LogWriter{
		path:       path,
		queue:      make(chan RecordedEntry, opts.QueueSize),
		logger:     opts.Logger,
		file:       f,
		bw:         bufio.NewWriterSize(f, 64*1024),
		maxBodyLen: opts.MaxBodyLen,
	}
	w.wg.Add(1)
	go w.drain(opts.FlushInterval, opts.SyncOnClose)
	return w, nil
}

// Enqueue adds an entry to the writer's queue. Non-blocking: when the
// queue is full the entry is dropped + dropped-counter is bumped.
// Returns true if the entry made it into the queue.
func (w *LogWriter) Enqueue(e RecordedEntry) bool {
	if w.closed.Load() {
		w.dropped.Add(1)
		return false
	}
	if w.maxBodyLen > 0 && len(e.Body) > w.maxBodyLen {
		// Truncate + tag so replay tools see the truncation.
		e.Body = e.Body[:w.maxBodyLen] + "...<truncated>"
	}
	select {
	case w.queue <- e:
		return true
	default:
		w.dropped.Add(1)
		return false
	}
}

// Close signals the drain goroutine to flush + exit, then waits.
// Safe to call multiple times — subsequent calls are no-ops.
func (w *LogWriter) Close(ctx context.Context) error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(w.queue)
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns a snapshot.
func (w *LogWriter) Stats() WriterStats {
	return WriterStats{
		Written: w.written.Load(),
		Dropped: w.dropped.Load(),
	}
}

// WriterStats counters.
type WriterStats struct {
	Written int64
	Dropped int64
}

// String formats stats for human consumption.
func (s WriterStats) String() string {
	return fmt.Sprintf("written=%d dropped=%d", s.Written, s.Dropped)
}

// drain reads from queue and writes to disk. Exits when the queue is
// closed AND drained.
func (w *LogWriter) drain(flushEvery time.Duration, syncOnClose bool) {
	defer w.wg.Done()
	flushTicker := time.NewTicker(flushEvery)
	defer flushTicker.Stop()

	enc := json.NewEncoder(w.bw)
	enc.SetEscapeHTML(false)

	flush := func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if err := w.bw.Flush(); err != nil {
			w.logger.Warn("log flush failed", "err", err)
		}
	}

	for {
		select {
		case e, ok := <-w.queue:
			if !ok {
				// Channel closed — drain anything still
				// buffered, flush, and exit.
				flush()
				if syncOnClose {
					w.mu.Lock()
					_ = w.file.Sync()
					_ = w.file.Close()
					w.mu.Unlock()
				} else {
					w.mu.Lock()
					_ = w.file.Close()
					w.mu.Unlock()
				}
				return
			}
			w.mu.Lock()
			if err := enc.Encode(e); err != nil {
				w.logger.Warn("log encode failed", "err", err, "path", e.Path)
				w.dropped.Add(1)
			} else {
				w.written.Add(1)
			}
			w.mu.Unlock()
		case <-flushTicker.C:
			flush()
		}
	}
}

// stripHopHeaders returns a copy of h with hop-by-hop entries removed
// + a single representative value per key. Matches the sanitization
// applied by both the recording proxy here and the replay binary's
// dispatch, so captured-then-replayed traffic is internally consistent.
func stripHopHeaders(h map[string][]string, dropAPIKeys bool) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if isHopHeader(k) {
			continue
		}
		if dropAPIKeys && (strings.EqualFold(k, "X-Api-Key") || strings.EqualFold(k, "Authorization")) {
			continue
		}
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}
