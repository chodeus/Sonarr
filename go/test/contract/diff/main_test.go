package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// jsonHandler returns an http.Handler that responds with the given
// JSON body + 200 status on every request. The body string is the
// raw JSON sent to the wire.
func jsonHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

// statusHandler always responds with the given status code + empty
// JSON body. Used to assert status-mismatch reporting.
func statusHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{}`))
	})
}

// buildPair stands up two httptest servers under the same path
// "/api/v3/foo" + returns the parsed *url.URL bases. Each server's
// handler is supplied so tests can drive identical / divergent /
// erroring shapes.
func buildPair(t *testing.T, primary, secondary http.Handler) (*url.URL, *url.URL) {
	t.Helper()
	mux1 := http.NewServeMux()
	mux1.Handle("/api/v3/foo", primary)
	mux2 := http.NewServeMux()
	mux2.Handle("/api/v3/foo", secondary)
	p := httptest.NewServer(mux1)
	t.Cleanup(p.Close)
	s := httptest.NewServer(mux2)
	t.Cleanup(s.Close)
	pURL, _ := url.Parse(p.URL)
	sURL, _ := url.Parse(s.URL)
	return pURL, sURL
}

// fooEntry is the corpus we drive every sub-test with. One entry,
// matching path, no normalization (the test bodies are already
// stable).
func fooEntry() spec.Entry {
	return spec.Entry{
		ID:      "foo",
		Request: spec.Request{Method: http.MethodGet, Path: "/api/v3/foo"},
	}
}

func TestDiffCorpus_MatchingBodies(t *testing.T) {
	t.Parallel()
	pURL, sURL := buildPair(t, jsonHandler(`{"a":1,"b":[1,2]}`), jsonHandler(`{"a":1,"b":[1,2]}`))
	results, summary, err := diffCorpus(context.Background(), Options{
		Primary: pURL, Secondary: sURL,
		Corpus: &spec.Corpus{Entries: []spec.Entry{fooEntry()}},
	})
	if err != nil {
		t.Fatalf("diffCorpus: %v", err)
	}
	if summary.Matched != 1 || summary.Mismatched != 0 || summary.Errored != 0 {
		t.Errorf("summary %+v", summary)
	}
	if len(results) != 1 || len(results[0].Diffs) != 0 {
		t.Errorf("result %+v", results[0])
	}
}

func TestDiffCorpus_DivergentBodies(t *testing.T) {
	t.Parallel()
	pURL, sURL := buildPair(t, jsonHandler(`{"a":1,"b":[1,2]}`), jsonHandler(`{"a":1,"b":[1,3]}`))
	results, summary, err := diffCorpus(context.Background(), Options{
		Primary: pURL, Secondary: sURL,
		Corpus: &spec.Corpus{Entries: []spec.Entry{fooEntry()}},
	})
	if err != nil {
		t.Fatalf("diffCorpus: %v", err)
	}
	if summary.Mismatched != 1 || summary.Matched != 0 {
		t.Errorf("summary %+v", summary)
	}
	if !results[0].BodyMismatch || len(results[0].Diffs) != 1 {
		t.Errorf("expected exactly one diff; got %+v", results[0])
	}
	if results[0].Diffs[0].Path != "$.b[1]" {
		t.Errorf("path: %q", results[0].Diffs[0].Path)
	}
}

func TestDiffCorpus_StatusMismatch(t *testing.T) {
	t.Parallel()
	pURL, sURL := buildPair(t, statusHandler(http.StatusOK), statusHandler(http.StatusInternalServerError))
	results, summary, err := diffCorpus(context.Background(), Options{
		Primary: pURL, Secondary: sURL,
		Corpus: &spec.Corpus{Entries: []spec.Entry{fooEntry()}},
	})
	if err != nil {
		t.Fatalf("diffCorpus: %v", err)
	}
	if summary.Mismatched != 1 {
		t.Errorf("summary %+v", summary)
	}
	if results[0].StatusMatch {
		t.Errorf("expected status mismatch on 200 vs 500")
	}
	if results[0].StatusA != 200 || results[0].StatusB != 500 {
		t.Errorf("status %d/%d", results[0].StatusA, results[0].StatusB)
	}
}

func TestDiffCorpus_NetworkError(t *testing.T) {
	t.Parallel()
	pURL, _ := buildPair(t, jsonHandler(`{}`), jsonHandler(`{}`))
	// Point the secondary at a deliberately-closed port so the
	// request errors out. 127.0.0.1:1 is conventionally unbound.
	deadURL, _ := url.Parse("http://127.0.0.1:1")
	_, summary, err := diffCorpus(context.Background(), Options{
		Primary: pURL, Secondary: deadURL,
		Corpus: &spec.Corpus{Entries: []spec.Entry{fooEntry()}},
	})
	if err != nil {
		t.Fatalf("diffCorpus: %v", err)
	}
	if summary.Errored != 1 {
		t.Errorf("expected 1 errored; got %+v", summary)
	}
}

func TestDiffCorpus_NilCorpus(t *testing.T) {
	t.Parallel()
	pURL, sURL := buildPair(t, jsonHandler(`{}`), jsonHandler(`{}`))
	if _, _, err := diffCorpus(context.Background(), Options{Primary: pURL, Secondary: sURL}); err == nil {
		t.Error("expected error on nil corpus")
	}
}

func TestDiffCorpus_NilBaseURL(t *testing.T) {
	t.Parallel()
	if _, _, err := diffCorpus(context.Background(), Options{Corpus: &spec.Corpus{}}); err == nil {
		t.Error("expected error on nil base URLs")
	}
}

// TestPrintReport_StableOrdering asserts that printReport emits
// entries in sorted-ID order so the shadow-traffic daily report is
// stable + greppable.
func TestPrintReport_StableOrdering(t *testing.T) {
	t.Parallel()
	results := []Result{
		{ID: "z.last", StatusMatch: true},
		{ID: "a.first", StatusMatch: true, BodyMismatch: true, Diffs: nil},
		{ID: "m.middle", Err: fmt.Errorf("boom")},
	}
	var buf bytes.Buffer
	printReport(&buf, results, Summary{Total: 3, Errored: 1, Mismatched: 1, Matched: 1}, true)
	out := buf.String()
	// Sorted order: a.first < m.middle < z.last
	if !strings.Contains(out, "a.first") || !strings.Contains(out, "m.middle") || !strings.Contains(out, "z.last") {
		t.Fatalf("missing ID in output: %q", out)
	}
	idxA := strings.Index(out, "a.first")
	idxM := strings.Index(out, "m.middle")
	idxZ := strings.Index(out, "z.last")
	if idxA >= idxM || idxM >= idxZ {
		t.Errorf("not sorted; positions a=%d m=%d z=%d in %q", idxA, idxM, idxZ, out)
	}
	if !strings.Contains(out, "Summary: 3 total, 1 matched, 1 mismatched, 1 errored") {
		t.Errorf("missing summary line: %q", out)
	}
}

// TestPrintReport_VerboseExpandsDiffs asserts the per-diff path
// lines surface when verbose=true; without verbose the count alone
// appears on the DIFF line.
func TestPrintReport_VerboseExpandsDiffs(t *testing.T) {
	t.Parallel()
	pURL, sURL := buildPair(t, jsonHandler(`{"a":1}`), jsonHandler(`{"a":2}`))
	results, summary, err := diffCorpus(context.Background(), Options{
		Primary: pURL, Secondary: sURL,
		Corpus: &spec.Corpus{Entries: []spec.Entry{fooEntry()}},
	})
	if err != nil {
		t.Fatalf("diffCorpus: %v", err)
	}

	var quiet, loud bytes.Buffer
	printReport(&quiet, results, summary, false)
	printReport(&loud, results, summary, true)
	if strings.Contains(quiet.String(), "$.a") {
		t.Errorf("quiet mode should not include per-diff path: %q", quiet.String())
	}
	if !strings.Contains(loud.String(), "$.a") {
		t.Errorf("verbose mode should include $.a: %q", loud.String())
	}
}
