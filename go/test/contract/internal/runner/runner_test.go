package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/test/contract/internal/spec"
)

func TestRunCapturesAndNormalizes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "secret" {
			t.Errorf("X-Api-Key: got %q want %q", got, "secret")
		}
		if r.URL.Query().Get("a") != "1" {
			t.Errorf("query a not forwarded: %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Date", "yesterday")
		_, _ = io.WriteString(w, `{"id":1,"added":"2025-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	base, _ := url.Parse(srv.URL)
	c := &Client{Base: base, APIKey: "secret"}

	g, err := c.Run(t.Context(), spec.Entry{
		ID: "x",
		Request: spec.Request{
			Method: "GET",
			Path:   "/api/v3/series",
			Query:  map[string][]string{"a": {"1"}},
		},
		Normalize: spec.Normalize{
			RedactJSONPaths: []string{"$.added"},
			RedactHeaders:   []string{"Date"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if g.Status != 200 {
		t.Fatalf("status: %d", g.Status)
	}
	if _, ok := g.Headers["Date"]; ok {
		t.Errorf("Date header not dropped")
	}
	if !g.BodyIsJSON {
		t.Fatalf("expected json body, got %+v", g)
	}
	if !strings.Contains(string(g.BodyJSON), `"<redacted>"`) {
		t.Errorf("body not redacted: %s", g.BodyJSON)
	}
	var decoded map[string]any
	if err := json.Unmarshal(g.BodyJSON, &decoded); err != nil {
		t.Errorf("body not json: %v", err)
	}
}

func TestRunSendsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"hello"`) {
			t.Errorf("body not posted: %q", b)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type not set: %q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	base, _ := url.Parse(srv.URL)
	c := &Client{Base: base}

	_, err := c.Run(t.Context(), spec.Entry{
		ID: "x",
		Request: spec.Request{
			Method: "POST",
			Path:   "/api/v3/series",
			Body:   map[string]string{"name": "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}
