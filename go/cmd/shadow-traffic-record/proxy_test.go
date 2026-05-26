package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newRecorder spins up a RecordingProxy fronting a test upstream.
func newRecorder(t *testing.T, upstream http.Handler) (*httptest.Server, *RecordingProxy, *LogWriter, string, func()) {
	t.Helper()
	upstreamSrv := httptest.NewServer(upstream)
	upstreamURL, _ := url.Parse(upstreamSrv.URL)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "capture.jsonl")
	writer, err := NewLogWriter(logPath, LogWriterOptions{
		QueueSize:     32,
		FlushInterval: 50 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	proxy := &RecordingProxy{
		Upstream:    upstreamURL,
		Writer:      writer,
		DropAPIKeys: true,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	front := httptest.NewServer(proxy)
	cleanup := func() {
		front.Close()
		_ = writer.Close(context.Background())
		upstreamSrv.Close()
	}
	return front, proxy, writer, logPath, cleanup
}

func readEntries(t *testing.T, path string) []RecordedEntry {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []RecordedEntry
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e RecordedEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestRecordingProxy_GetRequest_RecordsAndForwards(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	resp, err := http.Get(front.URL + "/api/v3/series/1?foo=bar")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != `{"id":1}` {
		t.Errorf("client body got %q", got)
	}

	// Force a flush.
	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Method != "GET" || e.Path != "/api/v3/series/1?foo=bar" {
		t.Errorf("entry: %+v", e)
	}
	if e.Status != 200 {
		t.Errorf("status: %d", e.Status)
	}
	if e.Timestamp.IsZero() {
		t.Error("timestamp should be populated")
	}
}

func TestRecordingProxy_PostBody_CapturedVerbatim(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	payload := `{"title":"alpha","seriesId":1}`
	resp, err := http.Post(front.URL+"/api/v3/series", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != payload {
		t.Errorf("upstream did not echo body — proxy may not be forwarding correctly: got %q", got)
	}

	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Body != payload {
		t.Errorf("body got %q, want %q", entries[0].Body, payload)
	}
	if entries[0].Status != 201 {
		t.Errorf("status got %d, want 201", entries[0].Status)
	}
}

func TestRecordingProxy_APIKeyStripped(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Upstream still receives the key — only the LOG is stripped.
		if r.Header.Get("X-Api-Key") != "secret123" {
			t.Errorf("upstream got X-Api-Key=%q", r.Header.Get("X-Api-Key"))
		}
		w.WriteHeader(http.StatusOK)
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	req, _ := http.NewRequest("GET", front.URL+"/api/v3/series", nil)
	req.Header.Set("X-Api-Key", "secret123")
	req.Header.Set("Authorization", "Bearer token456")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	if _, ok := entries[0].Headers["X-Api-Key"]; ok {
		t.Errorf("X-Api-Key leaked into log: %+v", entries[0].Headers)
	}
	if _, ok := entries[0].Headers["Authorization"]; ok {
		t.Errorf("Authorization leaked into log: %+v", entries[0].Headers)
	}
}

func TestRecordingProxy_UpstreamDown_502AndRecorded(t *testing.T) {
	upstreamURL, _ := url.Parse("http://127.0.0.1:1")
	dir := t.TempDir()
	logPath := filepath.Join(dir, "capture.jsonl")
	writer, err := NewLogWriter(logPath, LogWriterOptions{QueueSize: 32, FlushInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	proxy := &RecordingProxy{
		Upstream:    upstreamURL,
		Writer:      writer,
		DropAPIKeys: true,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpClient:  &http.Client{Timeout: 200 * time.Millisecond},
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, err := http.Get(front.URL + "/api/v3/series")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("got %d, want 502", resp.StatusCode)
	}

	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, logPath)
	if len(entries) != 1 || entries[0].Status != 0 {
		t.Errorf("entries: %+v; want one entry with Status=0 marking upstream-failure", entries)
	}

	st := proxy.Stats()
	if st.Failures != 1 {
		t.Errorf("failures: %d, want 1", st.Failures)
	}
}

func TestRecordingProxy_HopByHopHeadersStripped(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") != "" {
			t.Errorf("upstream got Connection header — should be stripped")
		}
		w.WriteHeader(http.StatusOK)
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	req, _ := http.NewRequest("GET", front.URL+"/x", nil)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Custom", "preserved")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	_ = writer.Close(context.Background())
	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	if _, ok := entries[0].Headers["Connection"]; ok {
		t.Error("Connection header leaked into log (should be hop-by-hop stripped)")
	}
	if entries[0].Headers["X-Custom"] != "preserved" {
		t.Errorf("X-Custom not preserved: %+v", entries[0].Headers)
	}
}

func TestLogWriter_DropsWhenQueueFull(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "slow.jsonl")
	w, err := NewLogWriter(logPath, LogWriterOptions{
		QueueSize:     2,
		FlushInterval: time.Hour, // effectively disable periodic flush
	})
	if err != nil {
		t.Fatal(err)
	}
	// Enqueue 10 entries against a queue size of 2. The drain
	// goroutine WILL pull some, but burst-load should still trigger
	// at least one drop.
	for i := 0; i < 100; i++ {
		w.Enqueue(RecordedEntry{Method: "GET", Path: "/x", Timestamp: time.Now()})
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := w.Stats()
	if st.Written+st.Dropped != 100 {
		t.Errorf("written+dropped=%d, want 100; stats: %+v", st.Written+st.Dropped, st)
	}
	// At least some should have made it through.
	if st.Written == 0 {
		t.Errorf("nothing written")
	}
}

func TestLogWriter_BodyTruncation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "trunc.jsonl")
	w, err := NewLogWriter(logPath, LogWriterOptions{
		QueueSize:     32,
		FlushInterval: 50 * time.Millisecond,
		MaxBodyLen:    8,
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(RecordedEntry{Method: "POST", Path: "/x", Body: "this is a long body string"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	if !strings.HasSuffix(entries[0].Body, "<truncated>") {
		t.Errorf("body got %q, want truncation marker", entries[0].Body)
	}
}

func TestLogWriter_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "x.jsonl")
	w, err := NewLogWriter(logPath, LogWriterOptions{QueueSize: 1, FlushInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second close — must not deadlock or panic.
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Enqueue after close → drop.
	if ok := w.Enqueue(RecordedEntry{Method: "GET", Path: "/x"}); ok {
		t.Error("Enqueue after Close should return false")
	}
}

func TestLogWriter_AppendMode(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "append.jsonl")
	// First open + write + close.
	w, err := NewLogWriter(logPath, LogWriterOptions{QueueSize: 4, FlushInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(RecordedEntry{Method: "GET", Path: "/first", Timestamp: time.Now()})
	_ = w.Close(context.Background())
	// Second open + write + close on the same file.
	w2, err := NewLogWriter(logPath, LogWriterOptions{QueueSize: 4, FlushInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	w2.Enqueue(RecordedEntry{Method: "GET", Path: "/second", Timestamp: time.Now()})
	_ = w2.Close(context.Background())

	entries := readEntries(t, logPath)
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2 (second open should APPEND, not truncate): %+v", len(entries), entries)
	}
}

func TestRecordingProxy_StatsIncrement(t *testing.T) {
	var got atomic.Int64
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	front, proxy, writer, _, cleanup := newRecorder(t, upstream)
	defer cleanup()

	for i := 0; i < 5; i++ {
		resp, _ := http.Get(front.URL + "/x")
		_ = resp.Body.Close()
	}

	if got.Load() != 5 {
		t.Errorf("upstream got %d, want 5", got.Load())
	}
	if proxy.Stats().Requests != 5 {
		t.Errorf("proxy requests: %d, want 5", proxy.Stats().Requests)
	}
	_ = writer.Close(context.Background())
}

func TestStripHopHeaders(t *testing.T) {
	in := map[string][]string{
		"Connection":    {"keep-alive"},
		"X-Api-Key":     {"secret"},
		"Authorization": {"Bearer x"},
		"X-Custom":      {"preserved", "ignored"},
		"Content-Type":  {"application/json"},
	}
	out := stripHopHeaders(in, true)
	if _, ok := out["Connection"]; ok {
		t.Error("Connection should be hop-stripped")
	}
	if _, ok := out["X-Api-Key"]; ok {
		t.Error("X-Api-Key should be dropped")
	}
	if _, ok := out["Authorization"]; ok {
		t.Error("Authorization should be dropped")
	}
	if out["X-Custom"] != "preserved" {
		t.Errorf("X-Custom: %q (want first value preserved)", out["X-Custom"])
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("Content-Type: %q", out["Content-Type"])
	}

	// With dropAPIKeys=false the keys come through.
	out2 := stripHopHeaders(in, false)
	if out2["X-Api-Key"] != "secret" {
		t.Errorf("with dropAPIKeys=false, X-Api-Key should come through, got %q", out2["X-Api-Key"])
	}
}

func TestWriterStats_String(t *testing.T) {
	s := WriterStats{Written: 100, Dropped: 5}
	if !strings.Contains(s.String(), "written=100") || !strings.Contains(s.String(), "dropped=5") {
		t.Errorf("got %q", s.String())
	}
}

func TestRecordingProxy_LargeResponse_NotBuffered(t *testing.T) {
	// 1MB payload — RecordingProxy should stream it via io.Copy
	// rather than allocating the whole thing.
	payload := strings.Repeat("a", 1024*1024)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	})
	front, _, writer, _, cleanup := newRecorder(t, upstream)
	defer cleanup()

	resp, err := http.Get(front.URL + "/big")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(got) != len(payload) {
		t.Errorf("got %d bytes, want %d", len(got), len(payload))
	}
	_ = writer.Close(context.Background())
}

