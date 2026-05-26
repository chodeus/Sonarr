package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLogReader_HappyPath(t *testing.T) {
	in := strings.NewReader(`{"ts":"2026-05-26T12:00:00Z","method":"GET","path":"/api/v3/series"}
{"ts":"2026-05-26T12:00:01Z","method":"POST","path":"/api/v3/series","body":"{\"title\":\"alpha\"}"}
# this is a comment, skip me
{"method":"GET","path":"/api/v3/health"}
`)
	r := NewLogReader(in)
	var got []LogEntry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	if got[0].Method != "GET" || got[0].Path != "/api/v3/series" {
		t.Errorf("entry 0: %+v", got[0])
	}
	if got[1].Body != `{"title":"alpha"}` {
		t.Errorf("entry 1 body: %q", got[1].Body)
	}
	if !got[2].Timestamp.IsZero() {
		t.Errorf("entry 2 should have zero timestamp")
	}
}

func TestLogReader_MalformedLine(t *testing.T) {
	in := strings.NewReader(`{"method":"GET","path":"/api/v3/series"}
not-json-at-all
`)
	r := NewLogReader(in)
	_, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Next()
	if err == nil {
		t.Errorf("expected parse error on malformed line, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q should mention line 2", err)
	}
}

func TestLogReader_MissingRequiredFields(t *testing.T) {
	in := strings.NewReader(`{"path":"/foo"}` + "\n")
	r := NewLogReader(in)
	_, err := r.Next()
	if err == nil || !strings.Contains(err.Error(), "method and path are required") {
		t.Errorf("got %v", err)
	}
}

func TestFilter_MethodMatch(t *testing.T) {
	f := Filter{Methods: []string{"GET", "POST"}}
	if !f.Match(LogEntry{Method: "GET", Path: "/x"}) {
		t.Error("GET should pass")
	}
	if !f.Match(LogEntry{Method: "post", Path: "/x"}) {
		t.Error("lowercase post should pass (case-insensitive)")
	}
	if f.Match(LogEntry{Method: "DELETE", Path: "/x"}) {
		t.Error("DELETE should fail")
	}
}

func TestFilter_PathMatch(t *testing.T) {
	f := Filter{PathPrefixes: []string{"/api/v3/series", "/api/v3/episode"}}
	if !f.Match(LogEntry{Method: "GET", Path: "/api/v3/series/1"}) {
		t.Error("/api/v3/series/1 should pass")
	}
	if !f.Match(LogEntry{Method: "GET", Path: "/api/v3/episode"}) {
		t.Error("/api/v3/episode should pass (exact prefix)")
	}
	if f.Match(LogEntry{Method: "GET", Path: "/api/v3/health"}) {
		t.Error("/api/v3/health should fail")
	}
}

func TestFilter_StatusMatch(t *testing.T) {
	f := Filter{StatusCodes: []int{200}, StatusClasses: []string{"5xx"}}
	if !f.Match(LogEntry{Method: "GET", Path: "/x", Status: 200}) {
		t.Error("200 should pass")
	}
	if !f.Match(LogEntry{Method: "GET", Path: "/x", Status: 503}) {
		t.Error("503 should pass (5xx class)")
	}
	if f.Match(LogEntry{Method: "GET", Path: "/x", Status: 404}) {
		t.Error("404 should fail")
	}
}

func TestFilter_EmptyPassesAll(t *testing.T) {
	var f Filter
	if !f.Match(LogEntry{Method: "GET", Path: "/anything"}) {
		t.Error("empty filter should pass anything")
	}
}

func TestParseStatusFilter(t *testing.T) {
	codes, classes, err := ParseStatusFilter("200,4xx,503,5xx")
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 || codes[0] != 200 || codes[1] != 503 {
		t.Errorf("codes: %v", codes)
	}
	if len(classes) != 2 || classes[0] != "4xx" || classes[1] != "5xx" {
		t.Errorf("classes: %v", classes)
	}

	if _, _, err := ParseStatusFilter(""); err != nil {
		t.Error("empty filter should not error")
	}
	if _, _, err := ParseStatusFilter("99"); err == nil {
		t.Error("99 should error (below 100)")
	}
	if _, _, err := ParseStatusFilter("xx2"); err == nil {
		t.Error("xx2 should error (malformed)")
	}
}

func TestStatusClassMatch(t *testing.T) {
	cases := []struct {
		class string
		code  int
		want  bool
	}{
		{"2xx", 200, true},
		{"2xx", 299, true},
		{"2xx", 300, false},
		{"5xx", 500, true},
		{"5xx", 599, true},
		{"5xx", 600, false},
		{"badclass", 200, false},
		{"6xx", 600, false}, // out of range
	}
	for _, tc := range cases {
		if got := statusClassMatch(tc.class, tc.code); got != tc.want {
			t.Errorf("statusClassMatch(%q, %d) = %v, want %v", tc.class, tc.code, got, tc.want)
		}
	}
}

func TestReplayer_RunDispatchesEachEntry(t *testing.T) {
	var got atomic.Int64
	var bodies sync.Map // path -> body
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies.Store(r.URL.Path, string(b))
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	log := strings.NewReader(`{"method":"GET","path":"/api/v3/series"}
{"method":"POST","path":"/api/v3/series","body":"{\"title\":\"alpha\"}"}
{"method":"DELETE","path":"/api/v3/series/1"}
`)
	r := &Replayer{Target: target, Speed: 0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.Run(context.Background(), NewLogReader(log)); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 3 {
		t.Errorf("server got %d requests, want 3", got.Load())
	}
	if v, _ := bodies.Load("/api/v3/series"); v.(string) != `{"title":"alpha"}` {
		t.Errorf("POST body: %v", v)
	}
	stats := r.Stats()
	if stats.Sent != 3 || stats.Status2xx != 3 {
		t.Errorf("stats: %+v", stats)
	}
}

func TestReplayer_PreservesQueryString(t *testing.T) {
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	log := strings.NewReader(`{"method":"GET","path":"/api/v3/episode?seriesId=1&seasonNumber=2"}` + "\n")
	r := &Replayer{Target: target, Speed: 0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.Run(context.Background(), NewLogReader(log)); err != nil {
		t.Fatal(err)
	}
	if q := gotQuery.Load().(string); q != "seriesId=1&seasonNumber=2" {
		t.Errorf("query got %q", q)
	}
}

func TestReplayer_RespectsFilter(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	log := strings.NewReader(`{"method":"GET","path":"/api/v3/series"}
{"method":"GET","path":"/api/v3/health"}
{"method":"POST","path":"/api/v3/series"}
`)
	r := &Replayer{Target: target, Speed: 0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Filter: Filter{Methods: []string{"GET"}, PathPrefixes: []string{"/api/v3/series"}}}
	if err := r.Run(context.Background(), NewLogReader(log)); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 1 {
		t.Errorf("server got %d, want 1 (only one entry passes both filters)", got.Load())
	}
	stats := r.Stats()
	if stats.Filtered != 2 {
		t.Errorf("stats.Filtered: %d, want 2", stats.Filtered)
	}
}

func TestReplayer_PacesByTimestamp(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	var times []time.Time
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	// Two requests 200ms apart, Speed=2.0 → expect ~100ms gap.
	now := time.Now()
	entry1 := LogEntry{Method: "GET", Path: "/x", Timestamp: now}
	entry2 := LogEntry{Method: "GET", Path: "/x", Timestamp: now.Add(200 * time.Millisecond)}
	logData := jsonl(entry1, entry2)

	r := &Replayer{Target: target, Speed: 2.0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.Run(context.Background(), NewLogReader(strings.NewReader(logData))); err != nil {
		t.Fatal(err)
	}
	if len(times) != 2 {
		t.Fatalf("got %d times, want 2", len(times))
	}
	gap := times[1].Sub(times[0])
	if gap < 50*time.Millisecond || gap > 200*time.Millisecond {
		t.Errorf("gap %v outside expected ~100ms window", gap)
	}
}

func TestReplayer_NoThrottleOnSpeedZero(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	// 3 entries 1 second apart in the log, Speed=0 → all fire
	// immediately.
	now := time.Now()
	logData := jsonl(
		LogEntry{Method: "GET", Path: "/x", Timestamp: now},
		LogEntry{Method: "GET", Path: "/x", Timestamp: now.Add(1 * time.Second)},
		LogEntry{Method: "GET", Path: "/x", Timestamp: now.Add(2 * time.Second)},
	)
	r := &Replayer{Target: target, Speed: 0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	start := time.Now()
	if err := r.Run(context.Background(), NewLogReader(strings.NewReader(logData))); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if got.Load() != 3 {
		t.Errorf("got %d, want 3", got.Load())
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took %v — should be near-instant with Speed=0", elapsed)
	}
}

func TestReplayer_ConcurrencyParallel(t *testing.T) {
	var got atomic.Int64
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur > old {
				if maxInFlight.CompareAndSwap(old, cur) {
					break
				}
				continue
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	var buf bytes.Buffer
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&buf, `{"method":"GET","path":"/x?%d"}`+"\n", i)
	}
	r := &Replayer{Target: target, Speed: 0, Concurrency: 5, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.Run(context.Background(), NewLogReader(&buf)); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 20 {
		t.Errorf("got %d, want 20", got.Load())
	}
	if maxInFlight.Load() < 2 {
		t.Errorf("max in-flight %d, expected concurrency > 1 to actually parallelize", maxInFlight.Load())
	}
}

func TestReplayer_RespectsContextCancel(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	// Long log with a deliberate pacing delay; cancel after the
	// first dispatch.
	now := time.Now()
	logData := jsonl(
		LogEntry{Method: "GET", Path: "/x", Timestamp: now},
		LogEntry{Method: "GET", Path: "/x", Timestamp: now.Add(10 * time.Second)},
	)
	r := &Replayer{Target: target, Speed: 1.0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if err := r.Run(ctx, NewLogReader(strings.NewReader(logData))); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("Run did not honor ctx cancellation, took %v", elapsed)
	}
}

func TestReplayer_CountsErrors(t *testing.T) {
	// Target points at a closed listener so every dispatch fails.
	target, _ := url.Parse("http://127.0.0.1:1")
	log := strings.NewReader(`{"method":"GET","path":"/x"}
{"method":"GET","path":"/y"}
`)
	r := &Replayer{Target: target, Speed: 0, Timeout: 200 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.Run(context.Background(), NewLogReader(log)); err != nil {
		t.Fatal(err)
	}
	stats := r.Stats()
	if stats.Errors != 2 {
		t.Errorf("stats.Errors = %d, want 2", stats.Errors)
	}
	if stats.Sent != 0 {
		t.Errorf("stats.Sent = %d, want 0 (no requests succeeded)", stats.Sent)
	}
}

func TestReplayer_ClassifiesStatusBuckets(t *testing.T) {
	statuses := []int{200, 201, 404, 500, 503, 301}
	idx := atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := idx.Add(1) - 1
		w.WriteHeader(statuses[i])
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	var buf bytes.Buffer
	for range statuses {
		buf.WriteString(`{"method":"GET","path":"/x"}` + "\n")
	}
	r := &Replayer{Target: target, Speed: 0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.Run(context.Background(), NewLogReader(&buf)); err != nil {
		t.Fatal(err)
	}
	stats := r.Stats()
	if stats.Status2xx != 2 || stats.Status4xx != 1 || stats.Status5xx != 2 || stats.StatusOther != 1 {
		t.Errorf("buckets: %+v", stats)
	}
}

func TestOpenLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	if err := os.WriteFile(path, []byte(`{"method":"GET","path":"/x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, r, err := OpenLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	e, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if e.Path != "/x" {
		t.Errorf("got %+v", e)
	}

	if _, _, err := OpenLogFile(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("missing file should error")
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV(""); got != nil {
		t.Errorf("empty → %v, want nil", got)
	}
	if got := splitCSV("a"); len(got) != 1 || got[0] != "a" {
		t.Errorf("single → %v", got)
	}
	got := splitCSV("a, b ,, c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("got %v, want [a b c]", got)
	}
}

func TestStatsSnapshot_String(t *testing.T) {
	s := StatsSnapshot{Sent: 10, Status2xx: 8, Status4xx: 1, Status5xx: 1, AvgLatencyUs: 1500}
	got := s.String()
	if !strings.Contains(got, "sent=10") || !strings.Contains(got, "avg=1.5ms") {
		t.Errorf("formatted: %q", got)
	}
}

// jsonl encodes entries one-per-line as the log format expects.
func jsonl(entries ...LogEntry) string {
	var buf bytes.Buffer
	for _, e := range entries {
		type alias struct {
			Timestamp string            `json:"ts,omitempty"`
			Method    string            `json:"method"`
			Path      string            `json:"path"`
			Headers   map[string]string `json:"headers,omitempty"`
			Body      string            `json:"body,omitempty"`
			Status    int               `json:"status,omitempty"`
		}
		a := alias{Method: e.Method, Path: e.Path, Headers: e.Headers, Body: e.Body, Status: e.Status}
		if !e.Timestamp.IsZero() {
			a.Timestamp = e.Timestamp.Format(time.RFC3339Nano)
		}
		b, _ := json.Marshal(a)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.String()
}
