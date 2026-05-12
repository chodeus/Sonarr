package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServiceTimeFetcher_HappyPath confirms the adapter decodes the
// `{"dateTimeUtc": "..."}` payload C# `ServiceTimeResponse` at
// SystemTimeCheck.cs:56-59 emits and returns the parsed time in UTC.
func TestServiceTimeFetcher_HappyPath(t *testing.T) {
	want := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)

	var seenURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept header = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"dateTimeUtc":%q}`, want.Format(time.RFC3339))
	}))
	defer srv.Close()

	// Override the production URL constant by giving the adapter a
	// client whose request will hit srv. Since serviceTimeURL is a
	// package-level const we can't override it directly — instead we
	// give the client a Transport that rewrites the request URL. This
	// keeps the adapter under test using its real URL composition
	// while we redirect the actual round-trip to httptest.
	client := &http.Client{
		Transport: &rewriteTransport{base: srv.URL},
	}
	fetcher := newServiceTimeFetcher(client)
	got, err := fetcher(t.Context())
	if err != nil {
		t.Fatalf("fetcher: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got = %v, want %v", got, want)
	}
	if !strings.Contains(seenURL, "/v1/time") {
		t.Errorf("seenURL = %q, want path /v1/time", seenURL)
	}
}

// TestServiceTimeFetcher_Non2xx confirms a 5xx returns an error
// (which [SystemTimeCheck.Run] then swallows per C# parity).
func TestServiceTimeFetcher_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	fetcher := newServiceTimeFetcher(client)
	_, err := fetcher(t.Context())
	if err == nil {
		t.Fatal("err = nil, want HTTP-status error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want substring 502", err)
	}
}

// TestServiceTimeFetcher_DecodeError confirms malformed JSON
// surfaces an error.
func TestServiceTimeFetcher_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"dateTimeUtc": "not-a-date"`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	fetcher := newServiceTimeFetcher(client)
	_, err := fetcher(t.Context())
	if err == nil {
		t.Fatal("err = nil, want decode error")
	}
}

// TestServiceTimeFetcher_TransportError confirms a transport-layer
// failure surfaces as an error.
func TestServiceTimeFetcher_TransportError(t *testing.T) {
	client := &http.Client{Transport: &errTransport{err: errors.New("connection refused")}}
	fetcher := newServiceTimeFetcher(client)
	_, err := fetcher(t.Context())
	if err == nil {
		t.Fatal("err = nil, want transport error")
	}
}

// TestServiceTimeFetcher_ContextCancel confirms ctx cancellation
// propagates through.
func TestServiceTimeFetcher_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	fetcher := newServiceTimeFetcher(client)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := fetcher(ctx)
	if err == nil {
		t.Fatal("err = nil, want context deadline error")
	}
}

// rewriteTransport rewrites every outgoing request to use the
// base URL's scheme/host while preserving the path. Used to redirect
// production-URL requests to an httptest.Server.
type rewriteTransport struct {
	base string
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rewrite, err := http.NewRequestWithContext(req.Context(), req.Method, r.base+req.URL.Path, req.Body)
	if err != nil {
		return nil, err
	}
	rewrite.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(rewrite)
}

// errTransport always returns the configured err.
type errTransport struct{ err error }

func (e *errTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, e.err
}

// TestProxyHostConfigStub_ReportsDisabled locks the slice 4.BN
// (C-cleanup [C10]) wiring contract: until HostConfig keyvalues
// ports, the stub reports the proxy as disabled so ProxyCheck.Run
// short-circuits to OK every cadence.
func TestProxyHostConfigStub_ReportsDisabled(t *testing.T) {
	t.Parallel()

	var stub proxyHostConfigStub
	if stub.ProxyEnabled() {
		t.Errorf("ProxyEnabled = true, want false until HostConfig keyvalues port lands")
	}
	if got := stub.ProxyHostname(); got != "" {
		t.Errorf("ProxyHostname = %q, want empty", got)
	}
}

// TestProxyPingerStub_AlwaysErrors confirms the defensive pinger
// returns a descriptive error. It is never invoked while the
// hostconfig stub keeps ProxyEnabled=false; the test exists to catch
// a future wiring change that flips the enable flag without swapping
// the pinger.
func TestProxyPingerStub_AlwaysErrors(t *testing.T) {
	t.Parallel()

	pinger := newProxyPingerStub()
	status, err := pinger(t.Context())
	if err == nil {
		t.Fatalf("expected error from stub pinger, got nil (status=%d)", status)
	}
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if !strings.Contains(err.Error(), "hostconfig") {
		t.Errorf("error %q should mention the deferred HostConfig port", err)
	}
}
