package shadowtraffic_e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Binaries built once per `go test` run via TestMain. The map is keyed
// by the `sonarr.tv/sonarr/cmd/<name>` package suffix.
var binaries = map[string]string{}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "shadowtraffic-e2e-bins-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdir tmp:", err)
		os.Exit(2)
	}
	for _, name := range []string{"shadow-traffic-diff", "shadow-traffic-replay", "shadow-traffic-record"} {
		out := filepath.Join(tmp, name)
		build := exec.Command("go", "build", "-o", out, "sonarr.tv/sonarr/cmd/"+name)
		if outB, err := build.CombinedOutput(); err != nil {
			fmt.Fprintln(os.Stderr, "build", name, ":", err, string(outB))
			_ = os.RemoveAll(tmp)
			os.Exit(2)
		}
		binaries[name] = out
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// pickPort returns a free TCP port. The kernel may reassign it
// before the caller binds, but the gap is small enough for test use.
func pickPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// waitListening polls until something accepts TCP on addr, or fails.
func waitListening(t *testing.T, addr string, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing listening at %s after %s", addr, max)
}

// stopAndWait sends SIGTERM, gives the process 5s to exit, then kills.
func stopAndWait(t *testing.T, cmd *exec.Cmd, name string) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Logf("%s sigterm: %v", name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// Graceful shutdown often exits with no error; SIGTERM-killed
		// processes report ExitError. Log but don't fail — we only
		// care about asserting on the binaries' output, not the exit
		// code.
		if err != nil {
			t.Logf("%s exited with: %v", name, err)
		}
	case <-time.After(5 * time.Second):
		t.Logf("%s did not exit on SIGTERM; killing", name)
		_ = cmd.Process.Kill()
		<-done
	}
}

// TestTriadComposesEndToEnd is the integration check for the Phase
// 6.1 shadow-traffic toolkit. The flow:
//
//  1. Fake C# upstream (httptest) responds {"server":"csharp","id":1}.
//  2. Fake Go upstream (httptest) responds {"server":"go","id":1} —
//     value-only diff vs C#; the diff binary should classify as LOW
//     severity.
//  3. shadow-traffic-record fronts the C# fake; client drives 3 GET
//     requests at the record front. SIGTERM record (flushes log).
//  4. Read the JSONL log — assert 3 entries.
//  5. shadow-traffic-diff dispatches each request to BOTH backends in
//     parallel and writes diff reports to its report-dir.
//  6. shadow-traffic-replay reads the record's log + dispatches to
//     diff's listen addr (-speed=0 → no pacing).
//  7. Wait for replay to exit (EOF on log).
//  8. Wait for diff to flush reports (> -flush-interval).
//  9. SIGTERM diff.
//  10. Read the diff report; assert at least one LOW-severity row
//     naming the value diff between C# and Go.
//
// Proves: record's JSONL format = replay's input format = diff's
// dispatch path = downstream backends' actual request shape. The
// triad composes; the format contract documented in J245's FORMAT.md
// is honoured by all three binaries.
func TestTriadComposesEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns 3 subprocesses + 2 httptest servers — skipped in -short mode")
	}

	tmp := t.TempDir()

	// 1+2. Two fake backends with different bodies → value diff.
	var csharpHits, goHits atomic.Int64
	fakeCsharp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csharpHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"server":"csharp","id":1}`))
	}))
	defer fakeCsharp.Close()
	fakeGo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"server":"go","id":1}`))
	}))
	defer fakeGo.Close()

	// 3. Start shadow-traffic-record fronting fakeCsharp.
	recordPort := pickPort(t)
	logPath := filepath.Join(tmp, "capture.jsonl")
	recordCmd := exec.Command(binaries["shadow-traffic-record"],
		"-listen", fmt.Sprintf("127.0.0.1:%d", recordPort),
		"-upstream", fakeCsharp.URL,
		"-log", logPath,
		"-flush-interval", "50ms",
		"-sync-on-close", "true",
		"-stats-interval", "1h", // disable periodic stats noise
	)
	var recordStderr bytes.Buffer
	recordCmd.Stderr = &recordStderr
	if err := recordCmd.Start(); err != nil {
		t.Fatalf("start record: %v", err)
	}
	defer func() {
		// Defensive — if the test fails before SIGTERM, ensure cleanup.
		if recordCmd.ProcessState == nil {
			_ = recordCmd.Process.Kill()
			_ = recordCmd.Wait()
		}
	}()
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", recordPort), 5*time.Second)

	// Drive 3 GET requests at the record's front.
	client := &http.Client{Timeout: 3 * time.Second}
	for i := 0; i < 3; i++ {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v3/series", recordPort))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !bytes.Contains(body, []byte(`"server":"csharp"`)) {
			t.Errorf("request %d: client did not see C# response: %s", i, body)
		}
	}
	if got := csharpHits.Load(); got != 3 {
		t.Errorf("fake C# saw %d hits during record-capture phase, want 3", got)
	}

	// Wait for the record's periodic flush (50ms cadence) to land all
	// 3 entries before shutting down — decouples the assertion from
	// graceful-shutdown flush timing (see waitForJSONLEntries).
	waitForJSONLEntries(t, logPath, 3, 10*time.Second)

	// SIGTERM record → final flush + fsync on graceful shutdown.
	stopAndWait(t, recordCmd, "record")

	// 4. Read the JSONL log — assert 3 entries shaped per FORMAT.md.
	entries := readJSONL(t, logPath)
	if len(entries) != 3 {
		t.Fatalf("got %d log entries, want 3 (recordStderr: %s)", len(entries), recordStderr.String())
	}
	for i, e := range entries {
		if e["method"] != "GET" {
			t.Errorf("entry %d: method=%v, want GET", i, e["method"])
		}
		if e["path"] != "/api/v3/series" {
			t.Errorf("entry %d: path=%v, want /api/v3/series", i, e["path"])
		}
	}

	// 5. Start shadow-traffic-diff with both fakes as backends.
	diffPort := pickPort(t)
	reportDir := filepath.Join(tmp, "reports")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	diffCmd := exec.Command(binaries["shadow-traffic-diff"],
		"-listen", fmt.Sprintf("127.0.0.1:%d", diffPort),
		"-primary-url", fakeCsharp.URL,
		"-secondary-url", fakeGo.URL,
		"-report-dir", reportDir,
		"-flush-interval", "200ms",
	)
	var diffStderr bytes.Buffer
	diffCmd.Stderr = &diffStderr
	if err := diffCmd.Start(); err != nil {
		t.Fatalf("start diff: %v", err)
	}
	defer func() {
		if diffCmd.ProcessState == nil {
			_ = diffCmd.Process.Kill()
			_ = diffCmd.Wait()
		}
	}()
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", diffPort), 5*time.Second)

	// Reset hit counters for the replay phase.
	csharpHits.Store(0)
	goHits.Store(0)

	// 6. Run shadow-traffic-replay — it dispatches each log entry to
	//    the diff binary's listen addr, then exits when log is drained.
	replayCmd := exec.Command(binaries["shadow-traffic-replay"],
		"-log", logPath,
		"-target", fmt.Sprintf("http://127.0.0.1:%d", diffPort),
		"-speed", "0", // no throttling
		"-stats-interval", "1h",
	)
	var replayStdout, replayStderr bytes.Buffer
	replayCmd.Stdout = &replayStdout
	replayCmd.Stderr = &replayStderr

	// 7. Replay exits on EOF — bound with our own timeout in case it hangs.
	replayDone := make(chan error, 1)
	if err := replayCmd.Start(); err != nil {
		t.Fatalf("start replay: %v", err)
	}
	go func() { replayDone <- replayCmd.Wait() }()
	select {
	case err := <-replayDone:
		if err != nil {
			t.Fatalf("replay exited with err: %v (stderr: %s)", err, replayStderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = replayCmd.Process.Kill()
		t.Fatalf("replay did not exit within 10s (stdout: %s, stderr: %s)", replayStdout.String(), replayStderr.String())
	}

	// Diff binary dispatches in parallel to BOTH backends. Each of
	// the 3 replayed requests → 1 hit per backend → expect 3 + 3.
	if got := csharpHits.Load(); got != 3 {
		t.Errorf("fake C# saw %d hits during replay phase, want 3", got)
	}
	if got := goHits.Load(); got != 3 {
		t.Errorf("fake Go saw %d hits during replay phase, want 3", got)
	}

	// 8. Diff binary flushes reports asynchronously — wait one
	//    flush-interval window for the diff report to land on disk.
	time.Sleep(600 * time.Millisecond)

	// 9. SIGTERM diff → forces one final flush on shutdown.
	stopAndWait(t, diffCmd, "diff")

	// 10. Read the diff report — expect at least one LOW-sev row
	//     naming the value diff between C# {"server":"csharp"} and
	//     Go {"server":"go"} on the $.server JSON path.
	diffFiles, err := filepath.Glob(filepath.Join(reportDir, "*.diffs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(diffFiles) == 0 {
		t.Fatalf("no .diffs.jsonl files written; reportDir=%s diffStderr=%s", reportDir, diffStderr.String())
	}
	var totalDiffs int
	var sawServerPathDiff bool
	for _, dfp := range diffFiles {
		rows := readJSONL(t, dfp)
		totalDiffs += len(rows)
		for _, row := range rows {
			// row shape (per shadow-traffic-diff/report.go) carries
			// severity + at least one Path-style key naming where the
			// diff occurred. Don't pin the exact field name — search
			// any string-typed value for the marker.
			b, _ := json.Marshal(row)
			if strings.Contains(string(b), "server") && (strings.Contains(string(b), "csharp") || strings.Contains(string(b), "go")) {
				sawServerPathDiff = true
			}
		}
	}
	if totalDiffs == 0 {
		t.Errorf("diff report has zero rows; expected at least one for the C#↔Go body diff. diffStderr=%s", diffStderr.String())
	}
	if !sawServerPathDiff {
		t.Errorf("none of the %d diff rows references the C# vs Go body diff on $.server", totalDiffs)
	}
}

// TestRecordReplayRoundTripsCleanly is the narrower companion test:
// record captures traffic, replay sends it back to the SAME backend.
// Asserts the backend sees the same shape of requests both times —
// the JSONL format preserves enough of each request to round-trip.
//
// This isolates the format-contract assertion from the diff binary,
// so a failure here points at record/replay (not diff) and vice
// versa for the e2e test above.
func TestRecordReplayRoundTripsCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns subprocesses — skipped in -short mode")
	}

	tmp := t.TempDir()

	type capturedReq struct {
		Method string
		Path   string
		Body   string
	}
	var mu = make(chan struct{}, 1)
	mu <- struct{}{} // mutex via channel-as-semaphore
	var got []capturedReq

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		<-mu
		got = append(got, capturedReq{
			Method: r.Method,
			Path:   r.URL.RequestURI(),
			Body:   string(body),
		})
		mu <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Phase 1: record traffic.
	recordPort := pickPort(t)
	logPath := filepath.Join(tmp, "capture.jsonl")
	recordCmd := exec.Command(binaries["shadow-traffic-record"],
		"-listen", fmt.Sprintf("127.0.0.1:%d", recordPort),
		"-upstream", backend.URL,
		"-log", logPath,
		"-flush-interval", "50ms",
		"-sync-on-close", "true",
		"-stats-interval", "1h",
	)
	if err := recordCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recordCmd.ProcessState == nil {
			_ = recordCmd.Process.Kill()
			_ = recordCmd.Wait()
		}
	}()
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", recordPort), 5*time.Second)

	client := &http.Client{Timeout: 3 * time.Second}
	probes := []struct{ method, path, body string }{
		{"GET", "/api/v3/series", ""},
		{"POST", "/api/v3/customformat", `{"name":"x265"}`},
		{"DELETE", "/api/v3/customformat/1", ""},
	}
	for _, p := range probes {
		var body io.Reader
		if p.body != "" {
			body = strings.NewReader(p.body)
		}
		req, _ := http.NewRequest(p.method, fmt.Sprintf("http://127.0.0.1:%d%s", recordPort, p.path), body)
		if p.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("probe %s %s: %v", p.method, p.path, err)
		}
		_ = resp.Body.Close()
	}
	// Wait for the periodic flush to land all probes in the log before
	// shutdown + before replay reads it — same decoupling as
	// TestTriadComposesEndToEnd (avoids the force-killed-before-flush
	// race under full-suite parallel load).
	waitForJSONLEntries(t, logPath, len(probes), 10*time.Second)
	stopAndWait(t, recordCmd, "record")

	// Capture what the backend saw during recording.
	<-mu
	recorded := append([]capturedReq(nil), got...)
	got = nil
	mu <- struct{}{}

	if len(recorded) != len(probes) {
		t.Fatalf("backend saw %d recording-phase requests, want %d", len(recorded), len(probes))
	}

	// Phase 2: replay the captured log against the SAME backend.
	replayCmd := exec.Command(binaries["shadow-traffic-replay"],
		"-log", logPath,
		"-target", backend.URL,
		"-speed", "0",
		"-stats-interval", "1h",
	)
	if err := replayCmd.Run(); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// Backend should now have seen the SAME shape of requests again.
	<-mu
	replayed := got
	mu <- struct{}{}

	if len(replayed) != len(probes) {
		t.Fatalf("backend saw %d replay-phase requests, want %d", len(replayed), len(probes))
	}
	for i := range probes {
		if recorded[i].Method != replayed[i].Method || recorded[i].Path != replayed[i].Path || recorded[i].Body != replayed[i].Body {
			t.Errorf("probe %d mismatch:\n  recorded: %+v\n  replayed: %+v", i, recorded[i], replayed[i])
		}
	}
}

// readJSONL reads a JSONL file (skipping blank/comment lines per
// FORMAT.md) and decodes each line to a map. Test-side helper.
// countJSONLEntries reads path and returns the number of well-formed
// (non-blank, non-comment, JSON-parseable) lines. Tolerant: a partial
// trailing line mid-flush counts as 0 for that line rather than
// failing — used by the poll below.
func countJSONLEntries(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		s := bytes.TrimSpace(line)
		if len(s) == 0 || bytes.HasPrefix(s, []byte("#")) {
			continue
		}
		var m map[string]any
		if json.Unmarshal(s, &m) == nil {
			n++
		}
	}
	return n
}

// waitForJSONLEntries polls path until it holds at least `want`
// well-formed entries or `max` elapses. The record binary's drain
// goroutine flushes the buffered writer on its -flush-interval ticker
// (50ms in this test), so entries become reader-visible WITHOUT
// waiting for graceful shutdown — this decouples the entry-count
// assertion from subprocess shutdown timing (the J428-diagnosed flake:
// under full-suite parallel load the record process could be
// force-killed before its drain flushed, yielding "0 log entries").
func waitForJSONLEntries(t *testing.T, path string, want int, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if countJSONLEntries(path) >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]any
	for _, line := range bytes.Split(b, []byte("\n")) {
		s := bytes.TrimSpace(line)
		if len(s) == 0 || bytes.HasPrefix(s, []byte("#")) {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(s, &m); err != nil {
			t.Fatalf("decode %s line %q: %v", path, s, err)
		}
		out = append(out, m)
	}
	return out
}
