// Command shadow-traffic-replay replays a JSONL request log against a
// target URL — typically the listen address of the shadow-traffic-diff
// binary during a Phase 6 staging-host run.
//
// Log format (one JSON object per line, '#' lines are comments):
//
//	{"ts":"2026-05-26T12:00:00Z","method":"GET","path":"/api/v3/series","headers":{"X-Api-Key":"abc"}}
//	{"ts":"2026-05-26T12:00:01Z","method":"POST","path":"/api/v3/series","headers":{...},"body":"{\"title\":\"...\"}"}
//
// Pacing model:
//
//	-speed 0      → no throttle (fire as fast as the network can carry)
//	-speed 1.0    → preserve original cadence between entries (default)
//	-speed 10.0   → replay 10x faster
//	-concurrency N → fan out across N workers (disables -speed timestamp pacing)
//
// Filtering:
//
//	-method GET,POST       → only replay these methods
//	-path /api/v3/series   → only paths starting with this prefix (comma-separated for multiple)
//	-status 2xx,4xx,503    → only entries whose recorded status matches
//
// The binary writes throughput stats to stdout every -stats-interval
// and a final summary on completion.
//
// Reference: go/PHASE_6_SCOPE.md § 6.1 (companion to shadow-traffic-diff).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	logPath := flag.String("log", "", "JSONL log file to replay (required)")
	target := flag.String("target", "http://localhost:9000", "target URL (typically shadow-traffic-diff's listen addr)")
	speed := flag.Float64("speed", 1.0, "playback speed multiplier; 0 = no throttle")
	concurrency := flag.Int("concurrency", 1, "number of parallel dispatch workers; >1 disables timestamp pacing")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout")
	methodFilter := flag.String("method", "", "comma-separated method allowlist (e.g. GET,POST); empty = all")
	pathFilter := flag.String("path", "", "comma-separated path-prefix allowlist; empty = all")
	statusFilter := flag.String("status", "", "comma-separated status allowlist (e.g. 200,4xx); empty = all")
	statsInterval := flag.Duration("stats-interval", 5*time.Second, "interval for stats lines to stdout")
	flag.Parse()

	if *logPath == "" {
		fmt.Fprintln(os.Stderr, "shadow-traffic-replay: -log is required")
		flag.Usage()
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	targetParsed, err := url.Parse(*target)
	if err != nil {
		logger.Error("invalid -target", "err", err)
		os.Exit(2)
	}

	f, src, err := OpenLogFile(*logPath)
	if err != nil {
		logger.Error("open log failed", "path", *logPath, "err", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	statusCodes, statusClasses, err := ParseStatusFilter(*statusFilter)
	if err != nil {
		logger.Error("parse -status failed", "err", err)
		os.Exit(2)
	}

	filter := Filter{
		Methods:       splitCSV(*methodFilter),
		PathPrefixes:  splitCSV(*pathFilter),
		StatusCodes:   statusCodes,
		StatusClasses: statusClasses,
	}

	replayer := &Replayer{
		Target:      targetParsed,
		Speed:       *speed,
		Concurrency: *concurrency,
		Timeout:     *timeout,
		Filter:      filter,
		Logger:      logger,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutdown signal received — draining workers")
		cancel()
	}()

	// Periodic stats reporter.
	go func() {
		ticker := time.NewTicker(*statsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Fprintln(os.Stdout, replayer.Stats().String())
			}
		}
	}()

	logger.Info("shadow-traffic-replay starting",
		"log", *logPath,
		"target", targetParsed.String(),
		"speed", *speed,
		"concurrency", *concurrency,
	)

	start := time.Now()
	if err := replayer.Run(ctx, src); err != nil {
		logger.Error("replay failed", "err", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	final := replayer.Stats()
	fmt.Fprintln(os.Stdout, "---")
	fmt.Fprintf(os.Stdout, "final: %s elapsed=%s rps=%.1f\n",
		final, elapsed.Round(time.Millisecond),
		float64(final.Sent)/elapsed.Seconds(),
	)
	logger.Info("clean exit")
}

// splitCSV splits a comma-separated string, trimming whitespace and
// dropping empty tokens. Returns nil for an empty input so callers can
// use len() == 0 as the wildcard sentinel.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
