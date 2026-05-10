package httpclient

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingInterceptor records each pre/post call for assertion.
type recordingInterceptor struct {
	mu  sync.Mutex
	pre []string
	pos []int
}

func (r *recordingInterceptor) PreRequest(req *Request) *Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pre = append(r.pre, req.URL.String())
	return req
}

func (r *recordingInterceptor) PostResponse(resp *Response) *Response {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pos = append(r.pos, resp.StatusCode)
	return resp
}

func TestClient_InterceptorsRunAroundDispatch(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, 200, []byte("ok"), nil), nil
		},
	}}
	rec := &recordingInterceptor{}
	c, err := NewClientWithOptions(ClientOptions{Dispatcher: stub, Interceptors: []Interceptor{rec}})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := NewRequest("https://example.com/api")
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(rec.pre) != 1 || !strings.HasSuffix(rec.pre[0], "/api") {
		t.Errorf("pre = %v", rec.pre)
	}
	if len(rec.pos) != 1 || rec.pos[0] != 200 {
		t.Errorf("post = %v", rec.pos)
	}
}

func TestClient_InterceptorsAppliedAcrossRedirectChain(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return mkResp(req, http.StatusFound, nil, map[string]string{"Location": "/two"}), nil
		},
		func(req *Request) (*Response, error) {
			return mkResp(req, 200, nil, nil), nil
		},
	}}
	rec := &recordingInterceptor{}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, Interceptors: []Interceptor{rec}})
	req, _ := NewRequest("https://example.com/one")
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(rec.pre) != 2 || len(rec.pos) != 2 {
		t.Errorf("expected pre/post 2 each, got pre=%d post=%d", len(rec.pre), len(rec.pos))
	}
	if !strings.HasSuffix(rec.pre[1], "/two") {
		t.Errorf("redirect target not seen by pre: %v", rec.pre)
	}
}

// stubRateLimiter records all WaitAndPulse invocations.
type stubRateLimiter struct {
	mu    sync.Mutex
	calls []rateCall
}

type rateCall struct {
	key, sub string
	interval time.Duration
}

func (s *stubRateLimiter) WaitAndPulse(_ context.Context, key, sub string, interval time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, rateCall{key, sub, interval})
	return nil
}

func TestClient_RateLimiterCalledWithHostAndKey(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 200, nil, nil), nil },
	}}
	rl := &stubRateLimiter{}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, RateLimiter: rl})

	req, _ := NewRequest("https://api.indexer.tld/search")
	req.RateLimit = 250 * time.Millisecond
	req.RateLimitKey = "user-bucket"
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if len(rl.calls) != 1 {
		t.Fatalf("expected 1 rate-limiter call, got %d", len(rl.calls))
	}
	c0 := rl.calls[0]
	if c0.key != "api.indexer.tld" || c0.sub != "user-bucket" || c0.interval != 250*time.Millisecond {
		t.Errorf("call = %+v", c0)
	}
}

func TestClient_RateLimiterSkippedWhenIntervalZero(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 200, nil, nil), nil },
	}}
	rl := &stubRateLimiter{}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, RateLimiter: rl})

	req, _ := NewRequest("https://api.indexer.tld/search")
	// RateLimit defaults to 0 → no wait expected.
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if len(rl.calls) != 0 {
		t.Errorf("expected no rate-limit calls, got %d", len(rl.calls))
	}
}

func TestClient_NilRateLimiterIgnoresRateLimit(t *testing.T) {
	// When the Client has no rate limiter wired, a Request with RateLimit
	// set should still execute (the field is just ignored).
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 200, nil, nil), nil },
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub})
	req, _ := NewRequest("https://example.com/")
	req.RateLimit = 1 * time.Second
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestClient_InterceptorPreCanRewriteURL(t *testing.T) {
	// A common interceptor pattern: rewrite the request URL before dispatch
	// (e.g. CloudFlare or torcache rewrites). Confirm the rewrite reaches
	// the dispatcher.
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			if !strings.Contains(req.URL.String(), "rewritten=1") {
				t.Errorf("expected URL to be rewritten, got %q", req.URL.String())
			}
			return mkResp(req, 200, nil, nil), nil
		},
	}}
	rewriter := InterceptorFunc{Pre: func(r *Request) *Request {
		r.URL = r.URL.AddQueryParam("rewritten", "1")
		return r
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, Interceptors: []Interceptor{rewriter}})
	req, _ := NewRequest("https://example.com/")
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}
