package download_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
)

// stubBodyFetcher satisfies [download.BodyFetcher]. Pinnable return
// + per-call capture so tests can assert IndexerID + DownloadURL.
type stubBodyFetcher struct {
	body        []byte
	err         error
	calls       int
	lastIndexer int
	lastURL     string
}

func (s *stubBodyFetcher) Fetch(_ context.Context, indexerID int, url string) ([]byte, error) {
	s.calls++
	s.lastIndexer = indexerID
	s.lastURL = url
	if s.err != nil {
		return nil, s.err
	}
	return s.body, nil
}

// ------------------------------------------------------------------------
// Orchestrator-side pre-fetch behavior
// ------------------------------------------------------------------------

func TestProcessDecisions_FetchesNzbBodyForUsenet(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadID: "nzo_ok"}
	lister := &stubClientLister{usenet: []downloadclients.Provider{prov}}
	pdd, err := download.NewProcessDownloadDecisions(lister, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewProcessDownloadDecisions: %v", err)
	}
	fetcher := &stubBodyFetcher{body: []byte("<?xml version=\"1.0\"?><nzb/>")}
	pdd.SetBodyFetcher(fetcher)

	dec := mkDecision("Show.S01E01.NZB", indexers.DownloadProtocolUsenet, 1)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Fatalf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1", fetcher.calls)
	}
	if prov.lastReq == nil || string(prov.lastReq.NzbBody) != "<?xml version=\"1.0\"?><nzb/>" {
		t.Errorf("provider did not receive pre-fetched NzbBody; got %q", string(prov.lastReq.NzbBody))
	}
}

func TestProcessDecisions_FetchesTorrentBodyWhenNoMagnet(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "trans", protocol: indexers.DownloadProtocolTorrent, downloadID: "hash_ok"}
	lister := &stubClientLister{torrent: []downloadclients.Provider{prov}}
	pdd, err := download.NewProcessDownloadDecisions(lister, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewProcessDownloadDecisions: %v", err)
	}
	fetcher := &stubBodyFetcher{body: []byte("d8:announce..bencode..e")}
	pdd.SetBodyFetcher(fetcher)

	dec := mkDecision("Show.S01E09.torrent", indexers.DownloadProtocolTorrent, 9)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Fatalf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1", fetcher.calls)
	}
	if prov.lastReq == nil || string(prov.lastReq.TorrentBody) != "d8:announce..bencode..e" {
		t.Errorf("provider did not receive pre-fetched TorrentBody; got %q", string(prov.lastReq.TorrentBody))
	}
}

// TestProcessDecisions_SkipsTorrentFetchWhenMagnetSet — slice 6.A4:
// when MagnetURL is non-empty, the fetcher is NOT invoked. The
// client adds via magnet directly.
func TestProcessDecisions_SkipsTorrentFetchWhenMagnetSet(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "trans", protocol: indexers.DownloadProtocolTorrent, downloadID: "hash"}
	lister := &stubClientLister{torrent: []downloadclients.Provider{prov}}
	pdd, err := download.NewProcessDownloadDecisions(lister, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewProcessDownloadDecisions: %v", err)
	}
	fetcher := &stubBodyFetcher{}
	pdd.SetBodyFetcher(fetcher)

	// Build a torrent decision with MagnetURL set on the TorrentInfo.
	dec := mkDecision("Show.S01E01.torrent", indexers.DownloadProtocolTorrent, 1)
	sub := dec.Subject.(*decisionengine.Subject)
	sub.Release = &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Show.S01E01.torrent",
			DownloadURL:      "https://example.com/Show.S01E01.torrent",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		MagnetURL: "magnet:?xt=urn:btih:HASH",
	}
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Fatalf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher.calls = %d, want 0 (magnet present → no fetch)", fetcher.calls)
	}
	if prov.lastReq == nil || prov.lastReq.MagnetURL != "magnet:?xt=urn:btih:HASH" {
		t.Errorf("MagnetURL not propagated; lastReq = %+v", prov.lastReq)
	}
	if len(prov.lastReq.TorrentBody) != 0 {
		t.Errorf("TorrentBody should be empty when magnet present; got %q", string(prov.lastReq.TorrentBody))
	}
}

// TestProcessDecisions_FetchErrorBucketsToPending — slice 6.A4:
// a Fetch error buckets the decision to Pending WITHOUT tripping
// the per-protocol short-circuit. Subsequent same-protocol
// decisions still attempt their own grabs.
func TestProcessDecisions_FetchErrorBucketsToPending(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadID: "ok"}
	lister := &stubClientLister{usenet: []downloadclients.Provider{prov}}
	pdd, err := download.NewProcessDownloadDecisions(lister, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewProcessDownloadDecisions: %v", err)
	}
	// First decision: fetcher fails. Second decision: fetcher succeeds.
	calls := 0
	pdd.SetBodyFetcher(funcFetcher(func(_ context.Context, _ int, url string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("connection refused")
		}
		return []byte("nzb-bytes"), nil
	}))

	decs := []decisionengine.Decision{
		mkDecision("Show.S01E01.NZB", indexers.DownloadProtocolUsenet, 1),
		mkDecision("Show.S01E02.NZB", indexers.DownloadProtocolUsenet, 2),
	}
	out := pdd.ProcessDecisions(context.Background(), decs)

	if len(out.Pending) != 1 {
		t.Errorf("Pending = %d, want 1; full result: %s", len(out.Pending), out)
	}
	if len(out.Grabbed) != 1 {
		t.Errorf("Grabbed = %d, want 1 (second decision should grab — no short-circuit trip); full result: %s", len(out.Grabbed), out)
	}
	if prov.calls != 1 {
		t.Errorf("prov.calls = %d, want 1 (only the second decision reaches the client)", prov.calls)
	}
}

// TestProcessDecisions_FetcherNilSkipsPrefetch — slice 6.A4:
// when no fetcher is wired, NzbBody is empty on the GrabRequest +
// the dispatch path still calls Provider.Download. (Concrete
// clients that need bytes will fail downstream; that's expected.)
func TestProcessDecisions_FetcherNilSkipsPrefetch(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadID: "ok"}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	dec := mkDecision("Show.S01E01.NZB", indexers.DownloadProtocolUsenet, 1)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Fatalf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if prov.lastReq == nil || len(prov.lastReq.NzbBody) != 0 {
		t.Errorf("expected empty NzbBody (no fetcher wired); got %q", string(prov.lastReq.NzbBody))
	}
}

// ------------------------------------------------------------------------
// HTTPBodyFetcher direct tests
// ------------------------------------------------------------------------

func TestHTTPBodyFetcher_Fetch_OK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body-bytes"))
	}))
	t.Cleanup(srv.Close)

	f := &download.HTTPBodyFetcher{}
	body, err := f.Fetch(context.Background(), 7, srv.URL+"/path")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if string(body) != "body-bytes" {
		t.Errorf("body = %q, want %q", string(body), "body-bytes")
	}
}

func TestHTTPBodyFetcher_Fetch_Non2xxWrapsSentinel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	f := &download.HTTPBodyFetcher{}
	_, err := f.Fetch(context.Background(), 1, srv.URL)
	if err == nil {
		t.Fatal("Fetch err = nil, want non-nil for 401")
	}
	if !errors.Is(err, download.ErrFetchNon2xx) {
		t.Errorf("Fetch err = %v, want errors.Is ErrFetchNon2xx", err)
	}
}

func TestHTTPBodyFetcher_Fetch_EmptyURLRejected(t *testing.T) {
	t.Parallel()
	f := &download.HTTPBodyFetcher{}
	_, err := f.Fetch(context.Background(), 1, "")
	if err == nil {
		t.Fatal("Fetch err = nil, want non-nil for empty URL")
	}
}

// ------------------------------------------------------------------------
// Local helpers
// ------------------------------------------------------------------------

type funcFetcher func(ctx context.Context, indexerID int, url string) ([]byte, error)

func (f funcFetcher) Fetch(ctx context.Context, indexerID int, url string) ([]byte, error) {
	return f(ctx, indexerID, url)
}
