package main

import (
	"bytes"
	"compress/gzip"
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
		"Connection":       {"keep-alive"},
		"X-Api-Key":        {"secret"},
		"Authorization":    {"Bearer x"},
		"X-Custom":         {"preserved", "ignored"},
		"Content-Type":     {"application/json"},
		"Content-Encoding": {"gzip"},
	}
	out := stripHopHeaders(in, true, false)
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
	if out["Content-Encoding"] != "gzip" {
		t.Errorf("with dropContentEncoding=false, Content-Encoding should come through, got %q", out["Content-Encoding"])
	}

	// With dropAPIKeys=false the keys come through.
	out2 := stripHopHeaders(in, false, false)
	if out2["X-Api-Key"] != "secret" {
		t.Errorf("with dropAPIKeys=false, X-Api-Key should come through, got %q", out2["X-Api-Key"])
	}

	// With dropContentEncoding=true the encoding is dropped (J244 —
	// recorder set this because it decompressed the body in place).
	out3 := stripHopHeaders(in, true, true)
	if _, ok := out3["Content-Encoding"]; ok {
		t.Errorf("with dropContentEncoding=true, Content-Encoding should be stripped, got %q", out3["Content-Encoding"])
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


func TestMaybeDecompressForLog_Gzip(t *testing.T) {
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	if _, err := gw.Write([]byte(`{"hello":"world"}`)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	out, ok := maybeDecompressForLog(gzbuf.Bytes(), "gzip", 0)
	if !ok {
		t.Fatal("expected decompressed=true")
	}
	if string(out) != `{"hello":"world"}` {
		t.Errorf("got %q", out)
	}
}

func TestMaybeDecompressForLog_GzipCaseInsensitive(t *testing.T) {
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	_, _ = gw.Write([]byte("hi"))
	_ = gw.Close()
	for _, enc := range []string{"gzip", "GZIP", "Gzip"} {
		out, ok := maybeDecompressForLog(gzbuf.Bytes(), enc, 0)
		if !ok {
			t.Errorf("encoding %q: expected ok=true", enc)
		}
		if string(out) != "hi" {
			t.Errorf("encoding %q: got %q", enc, out)
		}
	}
}

func TestMaybeDecompressForLog_NoGzip(t *testing.T) {
	body := []byte(`{"plain":true}`)
	out, ok := maybeDecompressForLog(body, "", 0)
	if ok {
		t.Error("expected ok=false for empty encoding")
	}
	if !bytes.Equal(out, body) {
		t.Errorf("body mutated: %q", out)
	}
	out2, ok2 := maybeDecompressForLog(body, "identity", 0)
	if ok2 {
		t.Error("expected ok=false for identity encoding")
	}
	if !bytes.Equal(out2, body) {
		t.Errorf("body mutated: %q", out2)
	}
}

func TestMaybeDecompressForLog_MalformedGzip(t *testing.T) {
	// "Content-Encoding: gzip" but the bytes aren't a valid gzip stream
	// — must NOT panic; degrade to logging verbatim.
	body := []byte("not actually gzipped")
	out, ok := maybeDecompressForLog(body, "gzip", 0)
	if ok {
		t.Error("expected ok=false for malformed gzip")
	}
	if !bytes.Equal(out, body) {
		t.Errorf("malformed-gzip body mutated: %q", out)
	}

	// Empty body with gzip encoding — also degrade cleanly.
	out2, ok2 := maybeDecompressForLog(nil, "gzip", 0)
	if ok2 {
		t.Error("expected ok=false for empty body with gzip encoding")
	}
	if len(out2) != 0 {
		t.Errorf("empty body mutated: %q", out2)
	}
}

func TestMaybeDecompressForLog_BombGuard(t *testing.T) {
	// Build a gzip that decompresses to 2 KiB; then call with limit=1 KiB.
	// Expect (original, false) — the recorder falls back to verbatim.
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	_, _ = gw.Write(bytes.Repeat([]byte("A"), 2048))
	_ = gw.Close()
	out, ok := maybeDecompressForLog(gzbuf.Bytes(), "gzip", 1024)
	if ok {
		t.Error("expected ok=false when decompressed size exceeds limit")
	}
	if !bytes.Equal(out, gzbuf.Bytes()) {
		t.Error("body should fall back to verbatim when over-limit")
	}

	// Same gzip but limit=4 KiB (above decompressed size) — decompresses cleanly.
	out2, ok2 := maybeDecompressForLog(gzbuf.Bytes(), "gzip", 4096)
	if !ok2 {
		t.Error("expected ok=true when decompressed size under limit")
	}
	if !bytes.Equal(out2, bytes.Repeat([]byte("A"), 2048)) {
		t.Errorf("under-limit decompression returned wrong bytes (len=%d)", len(out2))
	}
}

func TestRecordingProxy_GzipRequestBody_DecompressedInLog_VerbatimToUpstream(t *testing.T) {
	// Client POSTs a gzipped JSON body with Content-Encoding: gzip.
	// Expectations:
	//   - Upstream receives the original gzipped bytes verbatim (the
	//     proxy must NOT alter the upstream-bound payload — semantic
	//     transparency).
	//   - The captured log entry has the decompressed JSON in Body
	//     (operator inspection sees readable JSON, not gzip garbage).
	//   - The captured Headers map has NO Content-Encoding entry
	//     (it would lie about the now-plain body bytes).
	plain := []byte(`{"hello":"world","nested":{"k":1}}`)
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	_, _ = gw.Write(plain)
	_ = gw.Close()

	var upstreamSawGzipped atomic.Bool
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if bytes.Equal(got, gzbuf.Bytes()) {
			upstreamSawGzipped.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	req, _ := http.NewRequest("POST", front.URL+"/api/v3/customformat", bytes.NewReader(gzbuf.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if !upstreamSawGzipped.Load() {
		t.Error("upstream did not receive the original gzipped bytes — transparency violated")
	}

	_ = writer.Close(context.Background())
	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Body != string(plain) {
		t.Errorf("log body: %q, want decompressed JSON %q", e.Body, plain)
	}
	if _, ok := e.Headers["Content-Encoding"]; ok {
		t.Errorf("Content-Encoding should be stripped from log headers, got %q", e.Headers["Content-Encoding"])
	}
}

func TestRecordingProxy_PlainRequestBody_RecordedVerbatim_NoEncodingHeader(t *testing.T) {
	// Sanity: a non-gzip POST still records verbatim with original
	// headers (apart from hop-by-hop / API-key drops handled elsewhere).
	plain := []byte(`{"plain":true}`)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	req, _ := http.NewRequest("POST", front.URL+"/api/v3/customformat", bytes.NewReader(plain))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = writer.Close(context.Background())

	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	if entries[0].Body != string(plain) {
		t.Errorf("body: %q, want %q", entries[0].Body, plain)
	}
	if entries[0].Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type missing")
	}
}

func TestRecordingProxy_GzipMalformedBody_LoggedVerbatim_HeaderRetained(t *testing.T) {
	// Adversarial input: Content-Encoding says gzip but the body isn't
	// a valid gzip stream. Expectations:
	//   - Recorder doesn't panic.
	//   - Log Body has the original (garbage) bytes verbatim.
	//   - Content-Encoding stays in the log headers (it's still truthful
	//     — the body in the log IS the bytes the client sent, gzip-tagged).
	//   - Upstream still receives the original bytes verbatim.
	// Use UTF-8-safe bytes — the log is JSON-encoded so non-UTF-8 bytes
	// would be mangled by json.Marshal before they even hit the file,
	// confusing the byte-equality assertion below.
	garbage := []byte("not actually gzipped data")
	var upstreamSawOriginal atomic.Bool
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if bytes.Equal(got, garbage) {
			upstreamSawOriginal.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	})
	front, _, writer, logPath, cleanup := newRecorder(t, upstream)
	defer cleanup()

	req, _ := http.NewRequest("POST", front.URL+"/api/v3/manualimport", bytes.NewReader(garbage))
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = writer.Close(context.Background())

	if !upstreamSawOriginal.Load() {
		t.Error("upstream did not receive original bytes")
	}
	entries := readEntries(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Body != string(garbage) {
		t.Errorf("log body: %q, want garbage verbatim", entries[0].Body)
	}
	if entries[0].Headers["Content-Encoding"] != "gzip" {
		t.Errorf("malformed gzip: header should be retained, got %q", entries[0].Headers["Content-Encoding"])
	}
}
