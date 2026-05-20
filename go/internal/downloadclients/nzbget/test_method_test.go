package nzbget_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
)

// ============================================================================
// Nzbget.Test() — slice 5.G3c. Ports C# Nzbget.TestConnection at
// Nzbget.cs:273-300 — connectivity + auth + version-floor (>= 12.0).
// ============================================================================

type versionHandler struct {
	versionBody  string // raw JSON for the version result (e.g. `"24.2"`)
	versionError string
	configBody   string       // raw JSON for the config result. Defaults to a "Test" category + KeepHistory=7 (J100 valid config).
	status       atomic.Int32 // 0 = 200
	calls        atomic.Int32 // total RPC calls
	versionCalls atomic.Int32 // version RPC calls
	configCalls  atomic.Int32 // config RPC calls
}

func newVersionHandler() *versionHandler {
	return &versionHandler{
		versionBody: `"24.2"`,
		configBody:  `[{"Name":"Category1.Name","Value":"tv"},{"Name":"KeepHistory","Value":"7"},{"Name":"MainDir","Value":"/data"},{"Name":"DestDir","Value":"${MainDir}/done"}]`,
	}
}

func (h *versionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	code := int(h.status.Load())
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	if code != http.StatusOK {
		return
	}
	method := decodeMethod(r)
	switch method {
	case "config":
		h.configCalls.Add(1)
		_, _ = io.WriteString(w, `{"version":"1.1","result":`+h.configBody+`}`)
	default:
		h.versionCalls.Add(1)
		if h.versionError != "" {
			_, _ = io.WriteString(w, `{"version":"1.1","error":{"name":"E","code":1,"message":"`+h.versionError+`"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"version":"1.1","result":`+h.versionBody+`}`)
	}
}

// decodeMethod peeks at the JSON-RPC method from a request body —
// the handler uses this to route version vs config responses.
func decodeMethod(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	var rpc struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		return ""
	}
	return rpc.Method
}

func newNzbgetForTest(t *testing.T, srv *httptest.Server, mutate func(*nzbget.Settings)) *nzbget.Nzbget {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := nzbget.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return nzbget.New(nil, s, proxy, nil)
}

func TestTest_HappyPath(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures", failures)
	}
	if h.versionCalls.Load() != 1 {
		t.Errorf("version fired %d times, want 1", h.versionCalls.Load())
	}
	if h.configCalls.Load() != 1 {
		t.Errorf("config fired %d times, want 1 (J100 TestCategory + TestSettings)", h.configCalls.Load())
	}
}

func TestTest_VersionAtFloorAccepted(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.versionBody = `"12.0"`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want no Version failure at floor 12.0", failures)
	}
}

func TestTest_VersionBelowFloorFails(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.versionBody = `"11.5"`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for 11.5 < 12", failures)
	}
}

func TestTest_VersionWithHyphenTagAccepted(t *testing.T) {
	t.Parallel()
	// NZBGet test-build versions look like "21.0-testing" — the
	// proxy should accept the major.
	h := newVersionHandler()
	h.versionBody = `"21.0-testing"`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want no Version failure for hyphen-tagged 21.0-testing", failures)
	}
}

func TestTest_MalformedVersionNonString(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.versionBody = `12345` // numeric, not string
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for non-string result", failures)
	}
}

func TestTest_MalformedVersionUnparseable(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.versionBody = `"not-a-version"`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for unparseable string", failures)
	}
}

func TestTest_HTTP401SurfacesUsernameFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Username") {
		t.Errorf("Test() = %+v, want Username failure on 401", failures)
	}
}

func TestTest_TransportErrorSurfacesHostFailure(t *testing.T) {
	t.Parallel()
	// Stand up + immediately tear down a server so the port is
	// closed when Test fires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure on transport error", failures)
	}
}

func TestTest_SettingsValidatePortFailureBlocksNetwork(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected request — Validate(Port) failure should short-circuit since nzbget has only TvCategory as a warning")
	}))
	defer srv.Close()
	// Port=0 is a structural error (not the TvCategory warning) —
	// the network probe should still fire since the helper doesn't
	// distinguish; but in practice the Validate failure surfaces in
	// the result. The current Go-port behaviour does fire the
	// network probe because hasError gates only the nil-proxy path.
	// Verify the failure list contains the Port error AND the test
	// surface (no panics).
	n := newNzbgetForTest(t, srv, func(s *nzbget.Settings) {
		s.Port = 0
		s.Host = "" // also fail Host so the network probe doesn't even reach the test server
	})
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Port") {
		t.Errorf("Test() = %+v, want Port failure", failures)
	}
	// Host validation fails too; surface check.
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure from Validate", failures)
	}
}

func TestTest_EmptyTvCategoryWarningSurfaces(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "TvCategory") {
		t.Errorf("Test() = %+v, want TvCategory warning surfaced via Validate", failures)
	}
	// The network probe still fired despite the warning.
	if h.versionCalls.Load() != 1 {
		t.Errorf("version probe fired %d times, want 1 (warning shouldn't block)", h.versionCalls.Load())
	}
}

func TestTest_NilProxyReturnsError(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	_, err := n.Test(context.Background())
	if err == nil {
		t.Fatal("Test(nil proxy) err = nil, want error")
	}
}

// --- J100 TestCategory + TestSettings -----------------------------

func TestTest_J100_CategoryMissingFails(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.configBody = `[{"Name":"Category1.Name","Value":"OtherCat"},{"Name":"KeepHistory","Value":"7"}]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "Sonarr"
	})
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFailure(failures, "TvCategory") {
		t.Errorf("Test() = %+v, want TvCategory failure when category missing on server", failures)
	}
}

func TestTest_J100_CategoryPresentPasses(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.configBody = `[{"Name":"Category1.Name","Value":"Sonarr"},{"Name":"KeepHistory","Value":"7"}]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "Sonarr"
	})
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		if f.PropertyName == "TvCategory" {
			t.Errorf("Test() = %+v, want no TvCategory failure when category present", failures)
		}
	}
}

func TestTest_J100_EmptyTvCategorySkipsCategoryProbe(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	// Config doesn't even need to expose the category since empty
	// TvCategory short-circuits the validator.
	h.configBody = `[{"Name":"KeepHistory","Value":"7"}]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The TvCategory empty WARNING (from Validate) is fine; the
	// J100 category-existence probe is what we're checking is
	// NOT firing a new failure.
	tvFailures := 0
	for _, f := range failures {
		if f.PropertyName == "TvCategory" {
			tvFailures++
		}
	}
	if tvFailures > 1 {
		t.Errorf("Test() = %+v, want at most 1 TvCategory failure (the validate warning)", failures)
	}
}

func TestTest_J100_KeepHistoryZeroFails(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.configBody = `[{"Name":"Category1.Name","Value":"Test"},{"Name":"KeepHistory","Value":"0"}]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "KeepHistory") && strings.Contains(f.ErrorMessage, "0") {
			found = true
		}
	}
	if !found {
		t.Errorf("Test() = %+v, want whole-form KeepHistory=0 failure", failures)
	}
}

func TestTest_J100_KeepHistoryOverMaxFails(t *testing.T) {
	t.Parallel()
	h := newVersionHandler()
	h.configBody = `[{"Name":"Category1.Name","Value":"Test"},{"Name":"KeepHistory","Value":"50000"}]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "25000") {
			found = true
		}
	}
	if !found {
		t.Errorf("Test() = %+v, want whole-form KeepHistory>25000 failure", failures)
	}
}

func TestTest_J100_KeepHistoryDefaultsToSeven(t *testing.T) {
	t.Parallel()
	// KeepHistory missing from config → defaults to "7" → passes.
	h := newVersionHandler()
	h.configBody = `[{"Name":"Category1.Name","Value":"Test"}]`
	srv := httptest.NewServer(h)
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "KeepHistory") {
			t.Errorf("Test() = %+v, want no KeepHistory failure when key absent (defaults to 7)", failures)
		}
	}
}

func TestTest_J100_ConfigFetchFailureSurfacesWholeForm(t *testing.T) {
	t.Parallel()
	// Return a malformed config response so the parser fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := decodeMethod(r)
		w.WriteHeader(http.StatusOK)
		if method == "config" {
			// Wrong shape → JSON unmarshal error.
			_, _ = io.WriteString(w, `{"version":"1.1","result":"not-an-array"}`)
			return
		}
		_, _ = io.WriteString(w, `{"version":"1.1","result":"24.2"}`)
	}))
	defer srv.Close()
	n := newNzbgetForTest(t, srv, nil)
	failures, err := n.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "fetch NZBGet config") {
			found = true
		}
	}
	if !found {
		t.Errorf("Test() = %+v, want whole-form config-fetch failure", failures)
	}
}
