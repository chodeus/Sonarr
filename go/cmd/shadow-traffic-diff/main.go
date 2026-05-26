// Command shadow-traffic-diff is the Phase 6.1 shadow-traffic harness.
//
// It accepts an incoming HTTP request stream and dispatches each request
// in parallel to two upstream backends — the canonical C# Sonarr binary
// and the Go-port binary. The primary backend's response is returned to
// the caller verbatim; the secondary's response body is buffered and
// diffed against the primary's. A daily diff report grouped by endpoint
// + severity is written to disk.
//
// During Phase 6 the C# binary is canonical (primary), the Go binary is
// secondary, and the team monitors the daily reports for 14+ consecutive
// days of zero unexplained diff before the cutover commit. The roles can
// be swapped via -primary go for the inverse run (useful for spotting
// Go-port regressions that the contract-test corpus doesn't cover).
//
// This binary is intentionally I/O simple — no service discovery, no TLS
// termination, no auth proxying. It assumes both backends are reachable
// at fixed URLs from the host running this process. Production traffic
// replay (capture from a real Sonarr install, feed it back via this
// binary) is handled by a separate tee/replay tool not part of this
// scope.
//
// Reference: go/PHASE_6_SCOPE.md § 6.1.
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
	listen := flag.String("listen", ":9000", "local addr to accept incoming traffic on")
	primaryURL := flag.String("primary-url", "http://localhost:8989", "primary backend URL (response returned to caller)")
	secondaryURL := flag.String("secondary-url", "http://localhost:8990", "secondary backend URL (response diffed against primary)")
	primary := flag.String("primary", "csharp", "label for primary backend (csharp|go)")
	secondary := flag.String("secondary", "go", "label for secondary backend (csharp|go)")
	reportDir := flag.String("report-dir", "./shadow-reports", "directory for daily diff reports")
	redactConfig := flag.String("redact-config", "", "optional JSON file with redact-paths config (see PHASE_6_SCOPE.md)")
	dispatchTimeout := flag.Duration("dispatch-timeout", 30*time.Second, "per-backend dispatch timeout")
	flushInterval := flag.Duration("flush-interval", 60*time.Second, "interval to flush diff report to disk")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	primaryParsed, err := url.Parse(*primaryURL)
	if err != nil {
		logger.Error("invalid -primary-url", "err", err)
		os.Exit(2)
	}
	secondaryParsed, err := url.Parse(*secondaryURL)
	if err != nil {
		logger.Error("invalid -secondary-url", "err", err)
		os.Exit(2)
	}

	var redactCfg *RedactConfig
	if *redactConfig != "" {
		cfg, err := LoadRedactConfig(*redactConfig)
		if err != nil {
			logger.Error("loading -redact-config failed", "path", *redactConfig, "err", err)
			os.Exit(2)
		}
		redactCfg = cfg
	} else {
		redactCfg = DefaultRedactConfig()
	}

	if err := os.MkdirAll(*reportDir, 0o755); err != nil {
		logger.Error("creating -report-dir failed", "dir", *reportDir, "err", err)
		os.Exit(2)
	}

	report := NewReport(*reportDir, logger)
	defer func() {
		// Best-effort final flush — log but don't surface on success
		// path because the signal-handler block also flushes.
		if err := report.Flush(); err != nil {
			logger.Warn("final report flush failed", "err", err)
		}
	}()

	differ := NewDiffer(redactCfg)

	proxy := &DualProxy{
		Primary:         primaryParsed,
		Secondary:       secondaryParsed,
		PrimaryLabel:    *primary,
		SecondaryLabel:  *secondary,
		DispatchTimeout: *dispatchTimeout,
		Differ:          differ,
		Report:          report,
		Logger:          logger,
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Periodic report flush goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(*flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := report.Flush(); err != nil {
					logger.Warn("periodic report flush failed", "err", err)
				}
			}
		}
	}()

	// Graceful-shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutdown signal received — flushing reports + draining")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("shadow-traffic-diff up",
		"listen", *listen,
		"primary", fmt.Sprintf("%s @ %s", *primary, primaryParsed.String()),
		"secondary", fmt.Sprintf("%s @ %s", *secondary, secondaryParsed.String()),
		"report-dir", filepath.Clean(*reportDir),
	)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}

	logger.Info("clean exit")
}
