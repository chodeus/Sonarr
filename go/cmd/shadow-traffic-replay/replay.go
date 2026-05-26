package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sonarr.tv/sonarr/cmd/internal/shadowtraffic/httputil"
)

// Replayer dispatches LogEntry values from a stream against a target.
//
// Pacing model:
//
//   - Speed > 0  → inter-request delay = (entry.Timestamp - prev.Timestamp) / Speed.
//     Speed=1.0 replays in real-time; 10.0 replays 10x faster.
//   - Speed = 0  → no throttle (fire as fast as the network can carry).
//   - Entry has zero Timestamp → no pacing; replays immediately after
//     the previous entry.
//
// Concurrency: a single in-flight request at a time by default. Set
// Concurrency > 1 to issue requests in parallel via a worker pool.
// High concurrency disables timestamp-based pacing because requests
// no longer have a serial ordering — only Speed=0 makes sense.
type Replayer struct {
	Target      *url.URL
	Speed       float64
	Concurrency int
	Timeout     time.Duration
	Filter      Filter
	Logger      *slog.Logger

	// Client is injectable for tests.
	Client *http.Client

	stats Stats
}

// Stats accumulates replay metrics. All fields are atomically
// incremented and safe to read from multiple goroutines.
type Stats struct {
	Sent         atomic.Int64
	Filtered     atomic.Int64
	Errors       atomic.Int64
	Status2xx    atomic.Int64
	Status4xx    atomic.Int64
	Status5xx    atomic.Int64
	StatusOther  atomic.Int64
	TotalLatency atomic.Int64 // sum in microseconds
}

// Snapshot returns a stable copy of the current counters.
type StatsSnapshot struct {
	Sent         int64
	Filtered     int64
	Errors       int64
	Status2xx    int64
	Status4xx    int64
	Status5xx    int64
	StatusOther  int64
	AvgLatencyUs int64
}

// Snapshot returns the current counters. AvgLatency is sent-weighted.
func (s *Stats) Snapshot() StatsSnapshot {
	sent := s.Sent.Load()
	var avg int64
	if sent > 0 {
		avg = s.TotalLatency.Load() / sent
	}
	return StatsSnapshot{
		Sent:         sent,
		Filtered:     s.Filtered.Load(),
		Errors:       s.Errors.Load(),
		Status2xx:    s.Status2xx.Load(),
		Status4xx:    s.Status4xx.Load(),
		Status5xx:    s.Status5xx.Load(),
		StatusOther:  s.StatusOther.Load(),
		AvgLatencyUs: avg,
	}
}

// String formats a snapshot for human consumption (single line).
func (s StatsSnapshot) String() string {
	return fmt.Sprintf("sent=%d filtered=%d errors=%d 2xx=%d 4xx=%d 5xx=%d other=%d avg=%.1fms",
		s.Sent, s.Filtered, s.Errors, s.Status2xx, s.Status4xx, s.Status5xx, s.StatusOther,
		float64(s.AvgLatencyUs)/1000.0)
}

// client returns the injected client or a default with sensible timeouts.
func (r *Replayer) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// Run drains the LogReader and dispatches each entry. Blocks until
// either the stream is exhausted, ctx is cancelled, or an
// unrecoverable error occurs (malformed log line). Per-request errors
// are counted via Stats but never abort the run — the operator wants
// the full picture, not a halt on the first network blip.
func (r *Replayer) Run(ctx context.Context, src *LogReader) error {
	concurrency := r.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	jobs := make(chan LogEntry, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				r.dispatch(ctx, e)
			}
		}()
	}

	var prevTs time.Time
	for {
		if ctx.Err() != nil {
			break
		}
		e, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			close(jobs)
			wg.Wait()
			return err
		}
		if !r.Filter.Match(e) {
			r.stats.Filtered.Add(1)
			continue
		}
		// Pace only when concurrency==1 — parallel workers
		// destroy serial ordering anyway.
		if concurrency == 1 && !prevTs.IsZero() && !e.Timestamp.IsZero() && r.Speed > 0 {
			gap := e.Timestamp.Sub(prevTs)
			if gap > 0 {
				scaled := time.Duration(float64(gap) / r.Speed)
				if scaled > 0 {
					select {
					case <-ctx.Done():
					case <-time.After(scaled):
					}
				}
			}
		}
		if !e.Timestamp.IsZero() {
			prevTs = e.Timestamp
		}

		select {
		case jobs <- e:
		case <-ctx.Done():
		}
	}

	close(jobs)
	wg.Wait()
	return nil
}

// Stats returns the in-flight counter snapshot.
func (r *Replayer) Stats() StatsSnapshot {
	return r.stats.Snapshot()
}

func (r *Replayer) dispatch(ctx context.Context, e LogEntry) {
	reqCtx := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	u := *r.Target
	u.Path = httputil.SingleJoin(r.Target.Path, e.Path)
	if idx := strings.IndexByte(e.Path, '?'); idx >= 0 {
		u.Path = httputil.SingleJoin(r.Target.Path, e.Path[:idx])
		u.RawQuery = e.Path[idx+1:]
	}

	var body io.Reader
	if e.Body != "" {
		body = bytes.NewReader([]byte(e.Body))
	}

	req, err := http.NewRequestWithContext(reqCtx, e.Method, u.String(), body)
	if err != nil {
		r.stats.Errors.Add(1)
		if r.Logger != nil {
			r.Logger.Warn("build request failed", "method", e.Method, "path", e.Path, "err", err)
		}
		return
	}
	for k, v := range e.Headers {
		if httputil.IsHopHeader(k) || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		req.Header.Set(k, v)
	}
	if e.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "sonarr-shadow-traffic-replay/1")
	}

	start := time.Now()
	resp, err := r.client().Do(req)
	took := time.Since(start)
	r.stats.TotalLatency.Add(took.Microseconds())
	if err != nil {
		r.stats.Errors.Add(1)
		if r.Logger != nil {
			r.Logger.Warn("dispatch failed", "method", e.Method, "path", e.Path, "err", err)
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain the body so the connection is reusable.
	_, _ = io.Copy(io.Discard, resp.Body)

	r.stats.Sent.Add(1)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		r.stats.Status2xx.Add(1)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		r.stats.Status4xx.Add(1)
	case resp.StatusCode >= 500 && resp.StatusCode < 600:
		r.stats.Status5xx.Add(1)
	default:
		r.stats.StatusOther.Add(1)
	}
}

