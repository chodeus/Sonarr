// Command shadow-traffic-record is a recording reverse-proxy that tees
// inbound HTTP traffic to a JSONL log file while forwarding each
// request to a single upstream backend.
//
// The captured log is the exact format consumed by
// cmd/shadow-traffic-replay — point the operator's clients at this
// proxy, run their normal workflow (add series, search, etc.), then
// later feed the captured log into the replay binary to drive the
// shadow-traffic-diff harness.
//
// Recording is best-effort: when the in-memory queue is full (under
// burst load), entries are DROPPED rather than blocking the request
// hot path. A counter surfaces drops in the periodic stats output.
//
// Reference: go/PHASE_6_SCOPE.md § 6.1 — companion to
// shadow-traffic-diff (J239) and shadow-traffic-replay (J240).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", ":9001", "local addr to record traffic on")
	upstream := flag.String("upstream", "http://localhost:8989", "upstream backend URL")
	logPath := flag.String("log", "shadow-recording.jsonl", "output JSONL log path (append-mode)")
	queueSize := flag.Int("queue-size", 1024, "in-memory queue size between request hot path and disk writer")
	flushInterval := flag.Duration("flush-interval", 2*time.Second, "buffered-writer flush cadence")
	maxBodyLen := flag.Int("max-body-len", 256*1024, "cap on per-request body bytes committed to the log (0 = unlimited)")
	dropAPIKeys := flag.Bool("drop-api-keys", true, "strip X-Api-Key + Authorization headers from captured records (recommended unless you control replay env)")
	statsInterval := flag.Duration("stats-interval", 10*time.Second, "interval for stats lines to stdout")
	syncOnClose := flag.Bool("sync-on-close", true, "fsync the log file at shutdown")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	upstreamParsed, err := url.Parse(*upstream)
	if err != nil {
		logger.Error("invalid -upstream", "err", err)
		os.Exit(2)
	}

	if dir := filepath.Dir(*logPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Error("create log dir failed", "dir", dir, "err", err)
			os.Exit(2)
		}
	}

	writer, err := NewLogWriter(*logPath, LogWriterOptions{
		QueueSize:     *queueSize,
		FlushInterval: *flushInterval,
		MaxBodyLen:    *maxBodyLen,
		SyncOnClose:   *syncOnClose,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("open log writer failed", "path", *logPath, "err", err)
		os.Exit(1)
	}

	proxy := &RecordingProxy{
		Upstream:    upstreamParsed,
		Writer:      writer,
		DropAPIKeys: *dropAPIKeys,
		Logger:      logger,
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Periodic stats reporter.
	statsCtx, statsCancel := context.WithCancel(context.Background())
	defer statsCancel()
	go func() {
		ticker := time.NewTicker(*statsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-statsCtx.Done():
				return
			case <-ticker.C:
				ps := proxy.Stats()
				ws := writer.Stats()
				fmt.Fprintf(os.Stdout, "requests=%d failures=%d %s\n", ps.Requests, ps.Failures, ws.String())
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutdown signal received — closing log writer + draining")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		if err := writer.Close(shutdownCtx); err != nil {
			logger.Warn("writer close error", "err", err)
		}
	}()

	logger.Info("shadow-traffic-record up",
		"listen", *listen,
		"upstream", upstreamParsed.String(),
		"log", filepath.Clean(*logPath),
		"drop-api-keys", *dropAPIKeys,
	)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}

	final := writer.Stats()
	fmt.Fprintf(os.Stdout, "final: %s\n", final.String())
	logger.Info("clean exit")
}
