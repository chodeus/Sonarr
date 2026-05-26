package main

import (
	"bytes"
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

// newTestProxy spins up the DualProxy against two backends and returns
// a started httptest.Server pointing at it plus the report so tests can
// assert what landed.
func newTestProxy(t *testing.T, primaryHandler, secondaryHandler http.Handler) (*httptest.Server, *Report, *DualProxy, func()) {
	t.Helper()
	primarySrv := httptest.NewServer(primaryHandler)
	secondarySrv := httptest.NewServer(secondaryHandler)

	primaryURL, _ := url.Parse(primarySrv.URL)
	secondaryURL, _ := url.Parse(secondarySrv.URL)

	report := NewReport(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	differ := NewDiffer(DefaultRedactConfig())

	proxy := &DualProxy{
		Primary:         primaryURL,
		Secondary:       secondaryURL,
		PrimaryLabel:    "csharp",
		SecondaryLabel:  "go",
		DispatchTimeout: 5 * time.Second,
		Differ:          differ,
		Report:          report,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	front := httptest.NewServer(proxy)
	cleanup := func() {
		front.Close()
		primarySrv.Close()
		secondarySrv.Close()
	}
	return front, report, proxy, cleanup
}

// waitForDiff polls the report rows for up to 2s to give the
// fire-and-forget recordDiff goroutine time to land.
func waitForDiff(t *testing.T, report *Report, want int) []DiffRow {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		report.mu.Lock()
		got := len(report.rows)
		rows := append([]DiffRow(nil), report.rows...)
		report.mu.Unlock()
		if got >= want {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	report.mu.Lock()
	rows := append([]DiffRow(nil), report.rows...)
	report.mu.Unlock()
	t.Fatalf("timed out waiting for %d diff rows, got %d", want, len(rows))
	return nil
}

func TestDualProxy_BothBackendsAgree_RecordsOK(t *testing.T) {
	body := `{"id":1,"name":"alpha"}`
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	front, report, _, cleanup := newTestProxy(t, h, h)
	defer cleanup()

	resp, err := http.Get(front.URL + "/api/v3/series/1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != body {
		t.Errorf("client got %q, want %q", got, body)
	}

	// Give the background diff goroutine time to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		report.mu.Lock()
		ok := len(report.okBuckets)
		report.mu.Unlock()
		if ok > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	report.mu.Lock()
	defer report.mu.Unlock()
	if len(report.rows) != 0 {
		t.Errorf("got %d diff rows, want 0 (byte-equal should not produce diffs)", len(report.rows))
	}
	if len(report.okBuckets) != 1 {
		t.Errorf("got %d OK buckets, want 1", len(report.okBuckets))
	}
}

func TestDualProxy_StatusMismatch_HighSeverity(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	secondary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})
	front, report, _, cleanup := newTestProxy(t, primary, secondary)
	defer cleanup()

	resp, _ := http.Get(front.URL + "/api/v3/series/1")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("client got %d, want 200 (primary's response)", resp.StatusCode)
	}
	_ = resp.Body.Close()

	rows := waitForDiff(t, report, 1)
	if rows[0].Severity != SeverityHigh {
		t.Errorf("severity got %v, want high", rows[0].Severity)
	}
	if !strings.Contains(rows[0].Diffs[0], "status mismatch") {
		t.Errorf("diff summary %q missing 'status mismatch'", rows[0].Diffs[0])
	}
}

func TestDualProxy_BodyValueDiff_LowSeverity(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"alpha"}`))
	})
	secondary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"BETA"}`))
	})
	front, report, _, cleanup := newTestProxy(t, primary, secondary)
	defer cleanup()

	resp, _ := http.Get(front.URL + "/api/v3/series/1")
	_ = resp.Body.Close()

	rows := waitForDiff(t, report, 1)
	if rows[0].Severity != SeverityLow {
		t.Errorf("severity got %v, want low (pure value mismatch)", rows[0].Severity)
	}
}

func TestDualProxy_BodyMissingField_HighSeverity(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"alpha","extra":"present"}`))
	})
	secondary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"alpha"}`))
	})
	front, report, _, cleanup := newTestProxy(t, primary, secondary)
	defer cleanup()

	resp, _ := http.Get(front.URL + "/api/v3/series/1")
	_ = resp.Body.Close()

	rows := waitForDiff(t, report, 1)
	if rows[0].Severity != SeverityHigh {
		t.Errorf("severity got %v, want high (missing field)", rows[0].Severity)
	}
}

func TestDualProxy_SecondaryDown_HighSeverity(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	// Build a proxy whose secondary URL points at a closed listener.
	primarySrv := httptest.NewServer(primary)
	defer primarySrv.Close()
	primaryURL, _ := url.Parse(primarySrv.URL)
	// Address that nothing listens on.
	secondaryURL, _ := url.Parse("http://127.0.0.1:1")

	report := NewReport(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	differ := NewDiffer(DefaultRedactConfig())
	proxy := &DualProxy{
		Primary:         primaryURL,
		Secondary:       secondaryURL,
		PrimaryLabel:    "csharp",
		SecondaryLabel:  "go",
		DispatchTimeout: 1 * time.Second,
		Differ:          differ,
		Report:          report,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, _ := http.Get(front.URL + "/api/v3/series/1")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("client got %d, want 200 (primary alive, secondary irrelevant to client)", resp.StatusCode)
	}
	_ = resp.Body.Close()

	rows := waitForDiff(t, report, 1)
	if rows[0].Severity != SeverityHigh {
		t.Errorf("severity got %v, want high (secondary down)", rows[0].Severity)
	}
	if !strings.Contains(rows[0].Diffs[0], "secondary backend failed") {
		t.Errorf("diff summary %q missing 'secondary backend failed'", rows[0].Diffs[0])
	}
}

func TestDualProxy_PrimaryDown_502ToClient(t *testing.T) {
	primaryURL, _ := url.Parse("http://127.0.0.1:1")
	secondary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	secondarySrv := httptest.NewServer(secondary)
	defer secondarySrv.Close()
	secondaryURL, _ := url.Parse(secondarySrv.URL)

	report := NewReport(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	differ := NewDiffer(DefaultRedactConfig())
	proxy := &DualProxy{
		Primary:         primaryURL,
		Secondary:       secondaryURL,
		PrimaryLabel:    "csharp",
		SecondaryLabel:  "go",
		DispatchTimeout: 1 * time.Second,
		Differ:          differ,
		Report:          report,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, _ := http.Get(front.URL + "/api/v3/series/1")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("client got %d, want 502 (primary down)", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Primary failure is recorded synchronously in the request path,
	// no waitForDiff needed but use it anyway for symmetry.
	rows := waitForDiff(t, report, 1)
	if rows[0].Severity != SeverityHigh {
		t.Errorf("severity got %v, want high", rows[0].Severity)
	}
}

func TestDualProxy_BothBackendsSeeBody_POSTReplay(t *testing.T) {
	var primaryGot, secondaryGot atomic.Value
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		primaryGot.Store(string(b))
		_, _ = w.Write([]byte(`{"echo":"ok"}`))
	})
	secondary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		secondaryGot.Store(string(b))
		_, _ = w.Write([]byte(`{"echo":"ok"}`))
	})
	front, _, _, cleanup := newTestProxy(t, primary, secondary)
	defer cleanup()

	payload := `{"hello":"world"}`
	resp, err := http.Post(front.URL+"/api/v3/foo", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got := primaryGot.Load().(string); got != payload {
		t.Errorf("primary got %q, want %q", got, payload)
	}
	if got := secondaryGot.Load().(string); got != payload {
		t.Errorf("secondary got %q, want %q", got, payload)
	}
}

func TestDualProxy_RedactedFieldDifference_NoDiff(t *testing.T) {
	// /api/v3/system/task has $[*].lastDuration redacted in the
	// default config — different values on that field should NOT
	// surface as a diff.
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"RSSSync","lastDuration":"00:00:01.234"}]`))
	})
	secondary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"RSSSync","lastDuration":"99:99:99.999"}]`))
	})
	front, report, _, cleanup := newTestProxy(t, primary, secondary)
	defer cleanup()

	resp, _ := http.Get(front.URL + "/api/v3/system/task")
	_ = resp.Body.Close()

	// Give the background diff goroutine time to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		report.mu.Lock()
		ok := len(report.okBuckets)
		report.mu.Unlock()
		if ok > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	report.mu.Lock()
	defer report.mu.Unlock()
	if len(report.rows) != 0 {
		t.Errorf("got %d diff rows, want 0 (lastDuration is redacted): %+v", len(report.rows), report.rows)
	}
}

func TestReport_FlushAndAppend(t *testing.T) {
	dir := t.TempDir()
	rep := NewReport(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rep.Record(DiffRow{
		Timestamp: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
		Method:    "GET",
		Path:      "/api/v3/series",
		Severity:  SeverityHigh,
		Diffs:     []string{"missing field"},
	})
	rep.RecordOK("GET", "/api/v3/series/1", 200)
	rep.RecordOK("GET", "/api/v3/series/1", 200)
	if err := rep.Flush(); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 2 {
		t.Fatalf("got %d files in report dir, want 2: %v", len(files), files)
	}

	// Second flush adds another row + bumps the OK counter.
	rep.Record(DiffRow{
		Timestamp: time.Date(2026, 5, 26, 13, 0, 0, 0, time.UTC),
		Method:    "GET",
		Path:      "/api/v3/series",
		Severity:  SeverityLow,
		Diffs:     []string{"value mismatch"},
	})
	rep.RecordOK("GET", "/api/v3/series/1", 200)
	if err := rep.Flush(); err != nil {
		t.Fatal(err)
	}

	// Read back the OK summary and check the counter.
	okFile, _ := filepath.Glob(filepath.Join(dir, "*.ok-summary.json"))
	if len(okFile) != 1 {
		t.Fatalf("got %d ok-summary files, want 1", len(okFile))
	}
	b, _ := os.ReadFile(okFile[0])
	var got []OKBucket
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Count != 3 {
		t.Errorf("got %+v, want one bucket with Count=3", got)
	}

	// Read back the diffs file — should have 2 JSONL lines.
	diffFile, _ := filepath.Glob(filepath.Join(dir, "*.diffs.jsonl"))
	if len(diffFile) != 1 {
		t.Fatalf("got %d diffs files, want 1", len(diffFile))
	}
	b, _ = os.ReadFile(diffFile[0])
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	if len(lines) != 2 {
		t.Errorf("got %d JSONL lines, want 2: %s", len(lines), b)
	}
}

func TestSingleJoin(t *testing.T) {
	cases := []struct{ base, req, want string }{
		{"", "/api/v3", "/api/v3"},
		{"/", "/api/v3", "/api/v3"},
		{"/prefix", "/api/v3", "/prefix/api/v3"},
		{"/prefix/", "/api/v3", "/prefix/api/v3"},
		{"/prefix/", "api/v3", "/prefix/api/v3"},
	}
	for _, tc := range cases {
		got := singleJoin(tc.base, tc.req)
		if got != tc.want {
			t.Errorf("singleJoin(%q, %q) = %q, want %q", tc.base, tc.req, got, tc.want)
		}
	}
}

func TestRedactConfig_PathsFor(t *testing.T) {
	cfg := &RedactConfig{
		Rules: []RedactRule{
			{PathPrefix: "/api/v3/system/task", RedactPaths: []string{"$.lastDuration"}},
			{PathPrefix: "", RedactPaths: []string{"$.timestamp"}},
		},
	}
	if got := cfg.RedactPathsFor("/api/v3/system/task"); len(got) != 2 {
		t.Errorf("task path got %v, want both rules", got)
	}
	if got := cfg.RedactPathsFor("/api/v3/series"); len(got) != 1 || got[0] != "$.timestamp" {
		t.Errorf("series path got %v, want just timestamp", got)
	}
	var nilCfg *RedactConfig
	if got := nilCfg.RedactPathsFor("/anything"); got != nil {
		t.Errorf("nil cfg should return nil, got %v", got)
	}
}

func TestLoadRedactConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "redact.json")
	if err := os.WriteFile(path, []byte(`{"rules":[{"path_prefix":"/api/v3/foo","redact_paths":["$.bar"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRedactConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].PathPrefix != "/api/v3/foo" {
		t.Errorf("got %+v", cfg)
	}

	if _, err := LoadRedactConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Errorf("missing file should error")
	}
	badPath := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(badPath, []byte("not-json"), 0o644)
	if _, err := LoadRedactConfig(badPath); err == nil {
		t.Errorf("malformed JSON should error")
	}
}

func TestDiffer_HighSeverity_NonJSON(t *testing.T) {
	d := NewDiffer(nil)
	diffs, sev, err := d.Diff("/anywhere", []byte("<html>nope</html>"), []byte(`{"id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if sev != SeverityHigh || len(diffs) == 0 {
		t.Errorf("got sev=%v diffs=%v, want high + non-empty", sev, diffs)
	}
}

func TestSeverity_String(t *testing.T) {
	if SeverityOK.String() != "ok" {
		t.Error()
	}
	if SeverityLow.String() != "low" {
		t.Error()
	}
	if SeverityHigh.String() != "high" {
		t.Error()
	}
	if Severity(99).String() != "unknown" {
		t.Error()
	}
}
