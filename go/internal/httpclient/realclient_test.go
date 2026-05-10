package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDispatcher is a test double for the Dispatcher interface that records
// the requests it was asked to send and returns canned responses keyed by
// step.
type stubDispatcher struct {
	calls   []*Request
	steps   []func(req *Request) (*Response, error)
	stepIdx int
}

func (s *stubDispatcher) Dispatch(_ context.Context, req *Request) (*Response, error) {
	// Capture a snapshot of the request URL/method/cookies header so the
	// tests can assert against them post-hoc; the Client mutates req.URL
	// in place during a redirect chain.
	snap := *req
	if req.Headers != nil {
		hCopy := NewHeader()
		for k, vs := range req.Headers.HTTP() {
			for _, v := range vs {
				hCopy.Add(k, v)
			}
		}
		snap.Headers = hCopy
	}
	s.calls = append(s.calls, &snap)

	if s.stepIdx >= len(s.steps) {
		return nil, errors.New("stub: no more steps")
	}
	step := s.steps[s.stepIdx]
	s.stepIdx++
	return step(req)
}

func mkResp(req *Request, status int, body []byte, hdrs map[string]string) *Response {
	h := NewHeader()
	for k, v := range hdrs {
		h.Add(k, v)
	}
	return NewResponseFromBytes(req, h, body, status, "1.1")
}

func TestRealClient_RedirectChain301PostForcesGet(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		// First hop: 301 with Location → /target
		func(req *Request) (*Response, error) {
			return mkResp(req, http.StatusMovedPermanently, nil, map[string]string{
				"Location": "/target",
			}), nil
		},
		// Second hop: 200 — Method should now be GET, body cleared
		func(req *Request) (*Response, error) {
			if req.Method != http.MethodGet {
				t.Errorf("expected GET after 301-from-POST, got %s", req.Method)
			}
			if req.ContentData != nil {
				t.Errorf("expected body cleared after force-GET, got %q", req.ContentData)
			}
			return mkResp(req, 200, []byte("done"), nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/source")
	req.Method = http.MethodPost
	req.SetContentBytes([]byte("payload"))
	resp, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.ResponseData) != "done" {
		t.Errorf("unexpected: %d %q", resp.StatusCode, resp.ResponseData)
	}
	if len(stub.calls) != 2 {
		t.Errorf("expected 2 dispatches, got %d", len(stub.calls))
	}
}

func TestRealClient_303AlwaysForcesGet(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, http.StatusSeeOther, nil, map[string]string{"Location": "/other"}), nil
		},
		func(req *Request) (*Response, error) {
			if req.Method != http.MethodGet {
				t.Errorf("303 should force GET, got %s", req.Method)
			}
			return mkResp(req, 200, []byte{}, nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	req.Method = http.MethodPut
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestRealClient_307PreservesMethodAndBody(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, http.StatusTemporaryRedirect, nil, map[string]string{"Location": "/new"}), nil
		},
		func(req *Request) (*Response, error) {
			if req.Method != http.MethodPost {
				t.Errorf("307 should preserve POST, got %s", req.Method)
			}
			if string(req.ContentData) != "keep me" {
				t.Errorf("body lost: %q", req.ContentData)
			}
			return mkResp(req, 200, []byte("ok"), nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	req.Method = http.MethodPost
	req.SetContentBytes([]byte("keep me"))
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestRealClient_MaxRedirectsTrips(t *testing.T) {
	steps := []func(req *Request) (*Response, error){}
	for i := 0; i <= MaxRedirects+1; i++ {
		steps = append(steps, func(req *Request) (*Response, error) {
			return mkResp(req, http.StatusFound, nil, map[string]string{"Location": "/loop"}), nil
		})
	}
	stub := &stubDispatcher{steps: steps}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	_, err := c.Execute(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "Too many automatic redirections") &&
		!strings.Contains(err.Error(), "too many automatic redirections") {
		t.Errorf("expected too-many-redirects error, got %v", err)
	}
}

func TestRealClient_HTTPError404(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, 404, []byte("nope"), nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/x")
	_, err := c.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := AsHTTPError(err)
	if !ok || httpErr.Response.StatusCode != 404 {
		t.Errorf("not an HTTPError(404): %v", err)
	}
}

func TestRealClient_429Specific(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, 429, nil, map[string]string{"Retry-After": "10"}), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	_, err := c.Execute(context.Background(), req)
	tre, ok := AsTooManyRequestsError(err)
	if !ok {
		t.Fatalf("expected TooManyRequestsError, got %v", err)
	}
	if tre.RetryAfter.Seconds() != 10 {
		t.Errorf("RetryAfter = %v", tre.RetryAfter)
	}
}

func TestRealClient_SuppressHTTPError(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, 500, nil, nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	req.SuppressHTTPError = true
	resp, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Errorf("expected nil error with SuppressHTTPError, got %v", err)
	}
	if resp == nil || resp.StatusCode != 500 {
		t.Errorf("expected 500 returned")
	}
}

func TestRealClient_SuppressHTTPErrorStatusSet(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 404, nil, nil), nil },
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	req.SuppressHTTPErrorStatusSet = map[int]struct{}{404: {}}
	resp, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Errorf("expected suppressed, got %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestRealClient_RequestCookieMergedIntoHeader(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			c := req.Headers.Get("Cookie")
			if !strings.Contains(c, "session=abc") {
				t.Errorf("Cookie header missing session: %q", c)
			}
			return mkResp(req, 200, nil, nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	req.Cookies["session"] = "abc"
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestRealClient_PersistedCookieReplayedOnNextCall(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		// First call: response sets a cookie, but only a StoreResponseCookie
		// request will persist it.
		func(req *Request) (*Response, error) {
			return mkResp(req, 200, nil, map[string]string{
				"Set-Cookie": "auth=xyz; Path=/",
			}), nil
		},
		// Second call: should carry the persisted cookie.
		func(req *Request) (*Response, error) {
			c := req.Headers.Get("Cookie")
			if !strings.Contains(c, "auth=xyz") {
				t.Errorf("expected persisted cookie, header: %q", c)
			}
			return mkResp(req, 200, nil, nil), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)

	req1, _ := NewRequest("https://example.com/login")
	req1.StoreResponseCookie = true
	if _, err := c.Execute(context.Background(), req1); err != nil {
		t.Fatal(err)
	}

	req2, _ := NewRequest("https://example.com/profile")
	if _, err := c.Execute(context.Background(), req2); err != nil {
		t.Fatal(err)
	}
}

func TestRealClient_DownloadFile_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload bytes"))
	}))
	defer srv.Close()

	c, err := NewClient("Sonarr/test")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "out.bin")
	if err := c.DownloadFile(context.Background(), srv.URL+"/", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("payload bytes")) {
		t.Errorf("got %q", got)
	}
	// .part should be cleaned up.
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part not cleaned up")
	}
}

func TestRealClient_DownloadFile_RejectsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>nope</html>"))
	}))
	defer srv.Close()

	c, _ := NewClient("Sonarr/test")
	dest := filepath.Join(t.TempDir(), "out.bin")
	err := c.DownloadFile(context.Background(), srv.URL+"/", dest)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "html content") {
		t.Errorf("unexpected error: %v", err)
	}
	// Should NOT create the final destination file on rejection.
	if _, err := os.Stat(dest); err == nil {
		t.Errorf("dest file should not exist on HTML rejection")
	}
}

func TestRealClient_RedirectFollowedThroughHTTPServer(t *testing.T) {
	// End-to-end via httptest: dispatcher returns 302, client follows.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Location", "/end")
			w.WriteHeader(http.StatusFound)
		case "/end":
			_, _ = w.Write([]byte("arrived"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, _ := NewClient("Sonarr/test")
	req, _ := NewRequest(srv.URL + "/start")
	resp, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.ResponseData) != "arrived" {
		t.Errorf("body = %q", resp.ResponseData)
	}
	if hits != 2 {
		t.Errorf("expected 2 server hits, got %d", hits)
	}
}

func TestRealClient_AllowAutoRedirectFalseStops(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, 302, nil, map[string]string{"Location": "/never"}), nil
		},
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	req.AllowAutoRedirect = false
	req.SuppressHTTPError = true
	resp, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 302 {
		t.Errorf("expected 302 returned without follow, got %d", resp.StatusCode)
	}
	if len(stub.calls) != 1 {
		t.Errorf("expected 1 dispatch, got %d", len(stub.calls))
	}
}

func TestRealClient_RedirectMissingLocationFails(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 302, nil, nil), nil },
	}}
	c, _ := NewClientWithDispatcher(stub)
	req, _ := NewRequest("https://example.com/")
	_, err := c.Execute(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "no Location") {
		t.Errorf("expected no-location error, got %v", err)
	}
}

func TestRealClient_BasicSmokeRoundtripViaServer(t *testing.T) {
	// Catches regressions where the real Dispatcher path crashes when the
	// stub-driven tests exercise everything except actual net/http.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _ := NewClient("Sonarr/test")
	req, _ := NewRequest(srv.URL + "/echo")
	req.Method = http.MethodPost
	req.SetContentBytes([]byte("ping"))
	resp, err := c.Post(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.ResponseData) != "ping" {
		t.Errorf("echo failed: %q", resp.ResponseData)
	}
}
