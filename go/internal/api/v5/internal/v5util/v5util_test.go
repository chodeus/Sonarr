package v5util

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// fakeHandler emits a configurable status + headers + body and
// records the request seen (for proving the wrapper actually
// dispatches to the wrapped handler).
type fakeHandler struct {
	status      int
	contentType string
	body        []byte
	called      bool
}

func (f *fakeHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.called = true
	if f.contentType != "" {
		w.Header().Set("Content-Type", f.contentType)
	}
	w.WriteHeader(f.status)
	if f.body != nil {
		_, _ = w.Write(f.body)
	}
}

// ----- RewriteSuccessTo204 -----

func TestRewriteSuccessTo204_With200AndEmptyBraces_Demotes(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{status: http.StatusOK, contentType: "application/json", body: []byte(`{}`)}
	rr := httptest.NewRecorder()
	RewriteSuccessTo204(h).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/x", nil))
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped; got %q", ct)
	}
	if !h.called {
		t.Error("wrapped handler should have been called")
	}
}

func TestRewriteSuccessTo204_With200AndEmptyBody_Demotes(t *testing.T) {
	t.Parallel()
	// CustomFilter DELETE pattern — v3 emits 200 + empty body (NOT `{}`).
	// Pre-J256 this case wasn't covered by rewriteSuccessTo204 because
	// the predicate was strict on body=="{}". J256 expands it to also
	// match empty bodies.
	h := &fakeHandler{status: http.StatusOK, body: nil}
	rr := httptest.NewRecorder()
	RewriteSuccessTo204(h).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/x", nil))
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (200+empty should demote per J256)", rr.Code)
	}
}

func TestRewriteSuccessTo204_With200AndNonEmptyBody_PassesThrough(t *testing.T) {
	t.Parallel()
	// A 200 with real content (e.g. a list of resources) MUST pass
	// through verbatim — not be demoted.
	h := &fakeHandler{status: http.StatusOK, contentType: "application/json", body: []byte(`[{"id":1}]`)}
	rr := httptest.NewRecorder()
	RewriteSuccessTo204(h).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (passthrough)", rr.Code)
	}
	if rr.Body.String() != `[{"id":1}]` {
		t.Errorf("body should pass through; got %q", rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type should pass through for non-204")
	}
}

func TestRewriteSuccessTo204_With400_PassesThrough(t *testing.T) {
	t.Parallel()
	// Error envelopes MUST NOT be demoted — the v3 wrapper must
	// preserve 4xx + body verbatim.
	h := &fakeHandler{status: http.StatusBadRequest, contentType: "application/json", body: []byte(`{"message":"bad"}`)}
	rr := httptest.NewRecorder()
	RewriteSuccessTo204(h).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (error passthrough)", rr.Code)
	}
	if rr.Body.String() != `{"message":"bad"}` {
		t.Errorf("body should pass through verbatim; got %q", rr.Body.String())
	}
}

// ----- RewriteAcceptedTo204 -----

func TestRewriteAcceptedTo204_With202AndBraces_Demotes(t *testing.T) {
	t.Parallel()
	// SeasonPass pattern — v3 emits 202 + body `{}`.
	h := &fakeHandler{status: http.StatusAccepted, contentType: "application/json", body: []byte(`{}`)}
	rr := httptest.NewRecorder()
	RewriteAcceptedTo204(h).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (202+{} should demote)", rr.Code)
	}
}

func TestRewriteAcceptedTo204_With200_PassesThrough(t *testing.T) {
	t.Parallel()
	// The Accepted wrapper must NOT demote 200 responses — that's
	// the Success wrapper's job. Passing through preserves the v3
	// status for any handler that happens to emit 200 + `{}` while
	// wrapped by RewriteAcceptedTo204 (defensive — shouldn't happen
	// in production but if it does the wrapper degrades safely).
	h := &fakeHandler{status: http.StatusOK, body: []byte(`{}`)}
	rr := httptest.NewRecorder()
	RewriteAcceptedTo204(h).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (passthrough; wrong wrapper)", rr.Code)
	}
}

// ----- RewriteHappyTo204 (the generic underneath) -----

func TestRewriteHappyTo204_PreservesHeadersExceptContentType(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "preserved")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	rr := httptest.NewRecorder()
	RewriteHappyTo204(http.StatusOK, h).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/x", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "" {
		t.Errorf("Content-Type should be stripped; got %q", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("X-Custom") != "preserved" {
		t.Errorf("X-Custom should be preserved; got %q", rr.Header().Get("X-Custom"))
	}
}

// ----- TranslateIncludeSubresources (J417) -----

// queryCapture records the query the wrapped handler receives.
type queryCapture struct{ got url.Values }

func (q *queryCapture) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	q.got = r.URL.Query()
}

var episodeMapping = map[string]string{
	"series":      "includeSeries",
	"episodefile": "includeEpisodeFile",
	"images":      "includeImages",
}

func TestTranslateIncludeSubresources_CSV(t *testing.T) {
	t.Parallel()
	cap := &queryCapture{}
	h := TranslateIncludeSubresources(cap, episodeMapping)
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/v5/episode?seriesId=1&includeSubresources=series,episodeFile", nil))
	if cap.got.Get("includeSeries") != "true" || cap.got.Get("includeEpisodeFile") != "true" {
		t.Errorf("flags not set: %v", cap.got)
	}
	if cap.got.Get("includeImages") == "true" {
		t.Error("includeImages should not be set (not requested)")
	}
	if cap.got.Get("seriesId") != "1" {
		t.Error("unrelated query param dropped")
	}
	if _, ok := cap.got["includeSubresources"]; ok {
		t.Error("includeSubresources should be removed after translation")
	}
}

func TestTranslateIncludeSubresources_RepeatedAndCaseInsensitive(t *testing.T) {
	t.Parallel()
	cap := &queryCapture{}
	h := TranslateIncludeSubresources(cap, episodeMapping)
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/x?includeSubresources=Series&includeSubresources=IMAGES&includeSubresources=bogus", nil))
	if cap.got.Get("includeSeries") != "true" || cap.got.Get("includeImages") != "true" {
		t.Errorf("repeated/case-insensitive tokens not mapped: %v", cap.got)
	}
	if cap.got.Get("includeEpisodeFile") == "true" {
		t.Error("unrequested flag set")
	}
}

func TestTranslateIncludeSubresources_NoParam_PassThrough(t *testing.T) {
	t.Parallel()
	cap := &queryCapture{}
	h := TranslateIncludeSubresources(cap, episodeMapping)
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/x?includeSeries=true", nil))
	// v3-style bools still work untouched.
	if cap.got.Get("includeSeries") != "true" {
		t.Errorf("passthrough lost the v3 bool: %v", cap.got)
	}
}
