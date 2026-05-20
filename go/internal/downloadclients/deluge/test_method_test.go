package deluge_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.Test() — slice 5.G2c. Ports C# Deluge.TestConnection at
// Deluge.cs:276-323. No version floor (matches C# which doesn't
// enforce one); auth/connectivity failures pinned per the C#
// branches.
// ============================================================================

type testHandler struct {
	versionBody  string
	versionError string // non-empty → returns error object
	authResult   bool   // false → bad-password path
	loginCalls   atomic.Int32
	versionCalls atomic.Int32
	preLoginPath func(w http.ResponseWriter, r *http.Request) bool

	// J101 - TestCategory + TestGetTorrents probe responses.
	methodsBody    string // raw JSON for system.listMethods result; defaults to a list including the label plugin.
	labelsBody     string // raw JSON for label.get_labels result; defaults to "[\"tv-sonarr\",\"tv-imported\"]".
	torrentsBody   string // raw JSON for web.update_ui result; defaults to "{\"torrents\":{}}".
	methodsCalls   atomic.Int32
	labelsCalls    atomic.Int32
	addLabelCalls  atomic.Int32
	torrentsCalls  atomic.Int32
	addedLabels    []string // labels seen via label.add (for assertions)
	addLabelFailFn func(label string) bool
	labelsHook     func(after int) string // when set, returns labelsBody on each call after N invocations
}

func newTestHandler() *testHandler {
	return &testHandler{
		versionBody:  `"2.1.1"`,
		authResult:   true,
		methodsBody:  `["label.get_labels","label.add","label.set_torrent","core.add_torrent_magnet","web.update_ui"]`,
		labelsBody:   `["tv-sonarr","tv-imported"]`,
		torrentsBody: `{"torrents":{}}`,
	}
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.preLoginPath != nil && h.preLoginPath(w, r) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	bs := string(body)
	switch {
	case stringContains(bs, `"method":"auth.login"`):
		h.loginCalls.Add(1)
		if !h.authResult {
			_, _ = io.WriteString(w, `{"result":false,"error":null,"id":1}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sess"})
		_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
	case stringContains(bs, `"method":"daemon.info"`):
		h.versionCalls.Add(1)
		if h.versionError != "" {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":99,"message":"`+h.versionError+`"},"id":2}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":`+h.versionBody+`,"error":null,"id":2}`)
	case stringContains(bs, `"method":"system.listMethods"`):
		h.methodsCalls.Add(1)
		_, _ = io.WriteString(w, `{"result":`+h.methodsBody+`,"error":null,"id":3}`)
	case stringContains(bs, `"method":"label.get_labels"`):
		h.labelsCalls.Add(1)
		labelsBody := h.labelsBody
		if h.labelsHook != nil {
			labelsBody = h.labelsHook(int(h.labelsCalls.Load()))
		}
		_, _ = io.WriteString(w, `{"result":`+labelsBody+`,"error":null,"id":4}`)
	case stringContains(bs, `"method":"label.add"`):
		count := h.addLabelCalls.Add(1)
		// crude label extraction from the params array — body looks like
		// {"method":"label.add","params":["LABEL"],...}
		labelFromBody := extractFirstParam(bs)
		h.addedLabels = append(h.addedLabels, labelFromBody)
		if h.addLabelFailFn != nil && h.addLabelFailFn(labelFromBody) {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":1,"message":"add failed"},"id":5}`)
			return
		}
		_ = count
		_, _ = io.WriteString(w, `{"result":null,"error":null,"id":5}`)
	case stringContains(bs, `"method":"web.update_ui"`):
		h.torrentsCalls.Add(1)
		_, _ = io.WriteString(w, `{"result":`+h.torrentsBody+`,"error":null,"id":6}`)
	default:
		http.NotFound(w, r)
	}
}

// extractFirstParam parses out the first string entry from a JSON-RPC
// body's "params":["X"...] array. Returns "" if not found.
func extractFirstParam(body string) string {
	idx := stringIndex(body, `"params":["`)
	if idx < 0 {
		return ""
	}
	start := idx + len(`"params":["`)
	end := start
	for end < len(body) && body[end] != '"' {
		end++
	}
	if end >= len(body) {
		return ""
	}
	return body[start:end]
}

func stringIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func newDelugeForTest(t *testing.T, srv *httptest.Server, mutate func(*deluge.Settings)) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(nil, s, proxy, nil)
}

func TestTest_HappyPath(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures", failures)
	}
	if h.loginCalls.Load() != 1 {
		t.Errorf("login fired %d times, want 1", h.loginCalls.Load())
	}
	if h.versionCalls.Load() != 1 {
		t.Errorf("daemon.info fired %d times, want 1", h.versionCalls.Load())
	}
}

func TestTest_BadPasswordPinsToPasswordField(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.authResult = false
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Password") {
		t.Errorf("Test() = %+v, want Password failure", failures)
	}
	if h.versionCalls.Load() != 0 {
		t.Errorf("daemon.info fired despite auth failure")
	}
}

func TestTest_SettingsValidateShortCircuits(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s — Validate failures should short-circuit", r.URL.Path)
	}))
	defer srv.Close()

	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.Port = 0 // Validate failure
	})
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Port") {
		t.Errorf("Test() = %+v, want Port failure", failures)
	}
}

func TestTest_TransportErrorReturnsHostFailure(t *testing.T) {
	t.Parallel()
	// Stand up + immediately tear down a server so the port is
	// closed when Test fires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure on connection-refused", failures)
	}
}

func TestTest_MalformedVersionResultsInVersionFailure(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.versionBody = `12345` // numeric, not string
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for non-string result", failures)
	}
}

func TestTest_EmptyVersionStringFails(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.versionBody = `"   "`
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for empty string", failures)
	}
}

func TestTest_DaemonInfoRPCFailureBubbles(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.versionError = "internal explosion"
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	// Non-auth RPC failure (code=99) doesn't match any
	// classifyAuthError arm → bubbles as a non-nil error.
	_, err := d.Test(context.Background())
	if err == nil {
		t.Fatal("Test() err = nil, want RPC failure")
	}
}

func TestTest_NilProxyReturnsError(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	_, err := d.Test(context.Background())
	if err == nil {
		t.Fatal("Test(nil proxy) err = nil, want error")
	}
}

func TestTest_DaemonInfoVersion1xAccepted(t *testing.T) {
	t.Parallel()
	// Deluge 1.x is still functional with the JSON-RPC API; the C#
	// tree doesn't enforce a version floor, neither does the Go
	// port.
	h := newTestHandler()
	h.versionBody = `"1.3.15"`
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures for Deluge 1.3.x", failures)
	}
}

// --- J101 TestCategory + TestGetTorrents --------------------------

func TestTest_J101_BothCategoriesEmptySkipsLabelProbe(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.TvCategory = ""
		s.TvImportedCategory = ""
	})
	if _, err := d.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.methodsCalls.Load() != 0 {
		t.Errorf("methods fired %d times, want 0 (both categories empty)", h.methodsCalls.Load())
	}
	if h.labelsCalls.Load() != 0 {
		t.Errorf("labels fired %d times, want 0", h.labelsCalls.Load())
	}
}

func TestTest_J101_LabelPluginMissingFailsTvCategory(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.methodsBody = `["core.add_torrent_magnet","web.update_ui"]` // no label.* methods
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFailure(failures, "TvCategory") {
		t.Errorf("Test() = %+v, want TvCategory failure when label plugin absent", failures)
	}
	if h.labelsCalls.Load() != 0 {
		t.Errorf("labels fired %d times despite missing plugin; want 0", h.labelsCalls.Load())
	}
}

func TestTest_J101_TvCategoryAutoCreatedWhenMissing(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	// First label list returns empty; the next list (after label.add) includes the new label.
	calls := 0
	h.labelsHook = func(_ int) string {
		calls++
		if calls == 1 {
			return `[]`
		}
		return `["tv-sonarr"]`
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.TvImportedCategory = ""
	})
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures (auto-create succeeded)", failures)
	}
	if h.addLabelCalls.Load() != 1 {
		t.Errorf("label.add fired %d times, want 1", h.addLabelCalls.Load())
	}
	if len(h.addedLabels) != 1 || h.addedLabels[0] != "tv-sonarr" {
		t.Errorf("addedLabels=%v, want [tv-sonarr]", h.addedLabels)
	}
}

func TestTest_J101_TvCategoryAddPersistentFailureSurfaces(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	// label.get_labels always returns empty — Deluge accepts the
	// label.add RPC but the label still isn't there.
	h.labelsBody = `[]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.TvImportedCategory = ""
	})
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFailure(failures, "TvCategory") {
		t.Errorf("Test() = %+v, want TvCategory failure when auto-create silently fails", failures)
	}
}

func TestTest_J101_TvImportedCategoryAutoCreated(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	calls := 0
	h.labelsHook = func(_ int) string {
		calls++
		// First read: only tv-sonarr present. Second + onward: both.
		if calls == 1 {
			return `["tv-sonarr"]`
		}
		return `["tv-sonarr","tv-imported"]`
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.TvImportedCategory = "tv-imported"
	})
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures", failures)
	}
	if len(h.addedLabels) != 1 || h.addedLabels[0] != "tv-imported" {
		t.Errorf("addedLabels=%v, want [tv-imported]", h.addedLabels)
	}
}

func TestTest_J101_TvImportedCategoryAddFailureSurfaces(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	// First call returns tv-sonarr only (matches TvCategory).
	// Subsequent calls keep tv-sonarr only — tv-imported never appears.
	h.labelsBody = `["tv-sonarr"]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.TvImportedCategory = "tv-imported"
	})
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFailure(failures, "TvImportedCategory") {
		t.Errorf("Test() = %+v, want TvImportedCategory failure", failures)
	}
}

func TestTest_J101_TestGetTorrentsFires(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()
	d := newDelugeForTest(t, srv, nil)
	if _, err := d.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.torrentsCalls.Load() != 1 {
		t.Errorf("web.update_ui fired %d times, want 1", h.torrentsCalls.Load())
	}
}

func TestTest_J101_TestGetTorrentsFailureSurfacesWholeForm(t *testing.T) {
	t.Parallel()
	// Tear down the upstream once the version probe passes by serving
	// a malformed torrents response.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	hits := 0
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		hits++
		body, _ := io.ReadAll(r.Body)
		bs := string(body)
		switch {
		case stringContains(bs, `"method":"auth.login"`):
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
		case stringContains(bs, `"method":"daemon.info"`):
			_, _ = io.WriteString(w, `{"result":"2.1.1","error":null,"id":2}`)
		case stringContains(bs, `"method":"system.listMethods"`):
			_, _ = io.WriteString(w, `{"result":["label.get_labels"],"error":null,"id":3}`)
		case stringContains(bs, `"method":"label.get_labels"`):
			_, _ = io.WriteString(w, `{"result":["tv-sonarr","tv-imported"],"error":null,"id":4}`)
		case stringContains(bs, `"method":"web.update_ui"`):
			// Surface an RPC-shaped error.
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":99,"message":"boom"},"id":6}`)
		default:
			http.NotFound(w, r)
		}
	})
	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "" && stringContains(f.ErrorMessage, "list of torrents") {
			found = true
		}
	}
	if !found {
		t.Errorf("Test() = %+v, want whole-form GetTorrents failure", failures)
	}
}
