package httpclient

import (
	"context"
	"errors"
)

// Client is the public HTTP API every Sonarr subsystem (indexers, import
// lists, metadata sources, notifications, download clients) calls. C# cite:
// src/NzbDrone.Common/Http/HttpClient.cs:18-39.
//
// The C# interface exposes both sync and async variants of every operation.
// Goroutines make sync-over-async meaningless in Go, so the Go interface
// is async-only — every method takes a context.Context and may be wrapped
// in a goroutine by the caller.
//
// Sub-arc breakdown reminder (queue [1] of Phase 4):
//   - 1a (this slice): Client interface + Request/Response/Header/URI types,
//     plus a Nop client that returns ErrNotImplemented so wiring code can
//     compile against the surface before the real dispatcher lands.
//   - 1b: cookie-jar + redirect chain + gzip/deflate.
//   - 1c: rate limit + proxy + interceptor wiring.
//   - 1d: per-host status tracker + boot-site wire.
type Client interface {
	// Execute dispatches the request and returns the response. Returns
	// *HTTPError on >=400 status (unless suppressed). Cancellation honoured
	// via ctx.
	Execute(ctx context.Context, req *Request) (*Response, error)

	// Get sets Method=GET and Execute()s. Convenience wrapper.
	Get(ctx context.Context, req *Request) (*Response, error)

	// Head sets Method=HEAD and Execute()s.
	Head(ctx context.Context, req *Request) (*Response, error)

	// Post sets Method=POST and Execute()s.
	Post(ctx context.Context, req *Request) (*Response, error)

	// DownloadFile downloads url to fileName via a temporary .part file
	// (rename-on-success). Mirrors HttpClient.DownloadFileAsync at
	// src/NzbDrone.Common/Http/HttpClient.cs:264-310.
	DownloadFile(ctx context.Context, url, fileName string) error
}

// ErrNotImplemented is returned by NopClient methods. The dispatcher (sub-arc
// 1b) replaces NopClient with a real net/http-backed impl.
var ErrNotImplemented = errors.New("httpclient: real client not yet wired (Phase 4 sub-arc 1b/1c pending)")

// NopClient is a placeholder Client that fails every call with
// ErrNotImplemented. It exists so cmd/sonarr can reserve a Client slot in
// boot wiring before the real dispatcher is ready.
type NopClient struct{}

// NewNopClient returns the placeholder client.
func NewNopClient() *NopClient { return &NopClient{} }

// Execute returns ErrNotImplemented.
func (NopClient) Execute(context.Context, *Request) (*Response, error) {
	return nil, ErrNotImplemented
}

// Get returns ErrNotImplemented.
func (NopClient) Get(context.Context, *Request) (*Response, error) {
	return nil, ErrNotImplemented
}

// Head returns ErrNotImplemented.
func (NopClient) Head(context.Context, *Request) (*Response, error) {
	return nil, ErrNotImplemented
}

// Post returns ErrNotImplemented.
func (NopClient) Post(context.Context, *Request) (*Response, error) {
	return nil, ErrNotImplemented
}

// DownloadFile returns ErrNotImplemented.
func (NopClient) DownloadFile(context.Context, string, string) error {
	return ErrNotImplemented
}

// Compile-time interface check.
var _ Client = (*NopClient)(nil)
