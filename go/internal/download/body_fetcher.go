package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// BodyFetcher is the narrow surface the orchestrator consumes to
// pre-fetch the NZB / torrent file body from the indexer that
// produced a release, before dispatching the [downloadclients.Provider.Download]
// call. Slice 6.A4 — fills the gap documented on GrabRequest.NzbBody
// + GrabRequest.TorrentBody at `internal/downloadclients/provider.go:223-241`
// ("orchestrator fetches DownloadURL via the indexer's HTTP-request
// builder ... and stuffs the bytes here so the concrete client can
// POST them directly").
//
// Production implementation lives in indexers-land (the per-indexer
// HTTP request builder attaches Cookie / Authorization headers
// before sending the GET against DownloadURL). The orchestrator only
// needs the IndexerID + DownloadURL pair: implementations look up
// the indexer row, ask its concrete impl for an HTTP request with
// auth attached, send it, and return the body. Mirrors the C#
// `IIndexer.GetDownloadRequest(string url)` shape at
// `src/NzbDrone.Core/Indexers/IIndexer.cs` that
// `UsenetClientBase.Download` + `TorrentClientBase.Download` consume
// at `:40-91` / `:48-95` of their respective C# files.
//
// Returns the raw body bytes (the .nzb XML / .torrent bencode blob)
// on success, or an error wrapping the cause. The orchestrator's
// posture on Fetch errors is to bucket the decision into Pending
// without tripping the per-protocol short-circuit (the indexer is
// the issue, not the download client) — see the dispatch arm at
// process_decisions.go.
type BodyFetcher interface {
	Fetch(ctx context.Context, indexerID int, downloadURL string) ([]byte, error)
}

// HTTPBodyFetcher is the default [BodyFetcher] implementation —
// a plain HTTP GET with no auth attached. Useful as a baseline that
// works for unauthenticated public-feed indexers + as a test
// double. Production code that needs cookie / auth-header
// attachment binds a per-indexer-aware fetcher that wraps this one.
//
// Slice 6.A4 ships this stub so the orchestrator can exercise the
// fetch path end-to-end against an httptest server while the
// per-indexer authenticated fetcher lands as a follow-up.
type HTTPBodyFetcher struct {
	// Client is the *http.Client used for the GET. nil = http.DefaultClient.
	Client *http.Client
}

// Fetch issues a GET against downloadURL and returns the body bytes.
// indexerID is IGNORED by this implementation — the per-indexer
// authentication seam isn't yet wired. A non-2xx status returns
// an error wrapping ErrFetchNon2xx so callers can branch on the
// failure shape.
//
// Mirrors C# `UsenetClientBase.Download` HTTP-fetch arm at
// `:43-65` (the request-from-IIndexer + HttpClient.Get path).
func (f *HTTPBodyFetcher) Fetch(ctx context.Context, _ int, downloadURL string) ([]byte, error) {
	if downloadURL == "" {
		return nil, errors.New("download: HTTPBodyFetcher.Fetch: empty downloadURL")
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download: HTTPBodyFetcher.Fetch: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: HTTPBodyFetcher.Fetch: GET %s: %w", downloadURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: GET %s returned %d", ErrFetchNon2xx, downloadURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download: HTTPBodyFetcher.Fetch: read body %s: %w", downloadURL, err)
	}
	return body, nil
}

// ErrFetchNon2xx is the sentinel returned by [HTTPBodyFetcher.Fetch]
// (and conventional for third-party implementations) when the GET
// returns a non-2xx HTTP status. Callers can errors.Is against this
// to branch on "indexer host returned, but rejected the request"
// (which is different from a connection-refused class of failure).
var ErrFetchNon2xx = errors.New("download: body fetch returned non-2xx status")
