package httpclient

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestDispatcher() Dispatcher { return NewDispatcher("Sonarr/test") }

func TestDispatcher_Dispatch_BasicGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Sonarr/test" {
			t.Errorf("User-Agent header = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if string(resp.ResponseData) != "hello" {
		t.Errorf("body = %q", resp.ResponseData)
	}
}

func TestDispatcher_GzipAutoDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server sends gzip; net/http auto-decodes when client didn't set
		// Accept-Encoding manually.
		w.Header().Set("Content-Encoding", "gzip")
		gw := gzip.NewWriter(w)
		_, _ = gw.Write([]byte("compressed body"))
		_ = gw.Close()
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.ResponseData) != "compressed body" {
		t.Errorf("body = %q", resp.ResponseData)
	}
}

func TestDispatcher_DeflateDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "deflate")
		zw := zlib.NewWriter(w)
		_, _ = zw.Write([]byte("zlib payload"))
		_ = zw.Close()
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	// Manually set Accept-Encoding so stdlib does NOT auto-decode.
	req.Headers.Set("Accept-Encoding", "deflate")
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.ResponseData) != "zlib payload" {
		t.Errorf("body = %q", resp.ResponseData)
	}
}

func TestDispatcher_PostBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if !bytes.Equal(body, []byte("payload")) {
			t.Errorf("body = %q", body)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	req.Method = http.MethodPost
	req.SetContentBytes([]byte("payload"))
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestDispatcher_HeadersForwarded(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL+"/", AcceptJSON)
	req.Headers.Set("X-Custom", "value")
	if _, err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got.Get("Accept") != "application/json" {
		t.Errorf("Accept lost: %q", got.Get("Accept"))
	}
	if got.Get("X-Custom") != "value" {
		t.Errorf("X-Custom lost: %q", got.Get("X-Custom"))
	}
}

func TestDispatcher_BasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "alice" || p != "secret" {
			t.Errorf("BasicAuth = (%q,%q,%v)", u, p, ok)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	req.BasicAuthUser = "alice"
	req.BasicAuthPass = "secret"
	if _, err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcher_TimeoutDeadlineExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	req.RequestTimeout = 50 * time.Millisecond
	_, err := d.Dispatch(context.Background(), req)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDispatcher_RedirectsNotFollowedAtDispatcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/other")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 returned (not followed) but got %d", resp.StatusCode)
	}
}

func TestDispatcher_ResponseStreamCapturesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("file bytes"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	var sink bytes.Buffer
	req.ResponseStream = &sink
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if sink.String() != "file bytes" {
		t.Errorf("sink = %q", sink.String())
	}
	if resp.ResponseData != nil {
		t.Errorf("expected nil ResponseData when streaming, got %q", resp.ResponseData)
	}
}

func TestDispatcher_HostHeaderOverride(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	req.Headers.Set("Host", "virtual.example")
	if _, err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got != "virtual.example" {
		t.Errorf("Host = %q", got)
	}
}

func TestDispatcher_ConnectionClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") != "close" && !r.Close {
			t.Errorf("expected close, got %q close=%v", r.Header.Get("Connection"), r.Close)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")
	req.ConnectionKeepAlive = false
	if _, err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

// TestDispatcher_ContextCancellation_PropagatesToHTTPClient is the
// J218 / audit-row-C.1.h regression guard. C# upstream commit
// 5477cd262 threaded CancellationToken through the HttpClient async
// surface; the Go-port already uses NewRequestWithContext at the
// dispatcher seam (see buildHTTPRequest), so the parity check is
// purely a regression guard against a future refactor that
// accidentally severs the ctx → request link. A future
// http.NewRequest (without WithContext) at the dispatcher would make
// this test no longer cancel mid-flight.
//
// J218.
func TestDispatcher_ContextCancellation_PropagatesToHTTPClient(t *testing.T) {
	// Server that sleeps long enough for us to cancel via ctx, then
	// returns. The Dispatch call should return a cancellation error
	// long before the server finishes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			// Context was cancelled — return without writing so the
			// client side sees the cancellation propagate.
			return
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte("too late"))
		}
	}))
	defer srv.Close()

	d := newTestDispatcher()
	req, _ := NewRequest(srv.URL + "/")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so the request is in-flight.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := d.Dispatch(ctx, req)
	if err == nil {
		t.Fatal("Dispatch returned nil err on cancelled ctx; want context.Canceled or wrapped")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled (or wrapping it)", err)
	}
}
