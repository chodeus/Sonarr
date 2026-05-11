package download_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubProvider satisfies [downloadclients.Provider] with pinnable
// Download behaviour. Only Download is meaningful for these tests;
// the other methods return zero values.
type stubProvider struct {
	name        string
	protocol    indexers.DownloadProtocol
	downloadID  string
	downloadErr error
	calls       int
	lastReq     *downloadclients.GrabRequest
}

func (p *stubProvider) Name() string                                { return p.name }
func (p *stubProvider) Protocol() indexers.DownloadProtocol         { return p.protocol }
func (p *stubProvider) Definition() *downloadclients.DownloadClient { return nil }

func (p *stubProvider) Download(_ context.Context, req *downloadclients.GrabRequest) (string, error) {
	p.calls++
	p.lastReq = req
	if p.downloadErr != nil {
		return "", p.downloadErr
	}
	return p.downloadID, nil
}

func (p *stubProvider) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, nil
}

func (p *stubProvider) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

func (p *stubProvider) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return nil
}

func (p *stubProvider) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{}, nil
}

func (p *stubProvider) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return nil
}

func (p *stubProvider) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	return nil, nil
}

// stubClientLister returns pinned per-protocol provider lists.
type stubClientLister struct {
	usenet  []downloadclients.Provider
	torrent []downloadclients.Provider
	err     error
	calls   []indexers.DownloadProtocol
}

func (s *stubClientLister) ListByProtocol(_ context.Context, protocol indexers.DownloadProtocol) ([]downloadclients.Provider, error) {
	s.calls = append(s.calls, protocol)
	if s.err != nil {
		return nil, s.err
	}
	switch protocol {
	case indexers.DownloadProtocolUsenet:
		return s.usenet, nil
	case indexers.DownloadProtocolTorrent:
		return s.torrent, nil
	}
	return nil, nil
}

// ----------------------------------------------------------------------------

func newPDD(t *testing.T, lister *stubClientLister) *download.ProcessDownloadDecisions {
	t.Helper()
	p, err := download.NewProcessDownloadDecisions(lister, slog.Default())
	if err != nil {
		t.Fatalf("NewProcessDownloadDecisions: %v", err)
	}
	return p
}

func mkDecision(title string, protocol indexers.DownloadProtocol, episodeIDs ...int) decisionengine.Decision {
	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 1, Title: "Show"}
	for _, id := range episodeIDs {
		sub.Episodes = append(sub.Episodes, episodes.Episode{ID: id})
	}
	sub.Release = &parser.ReleaseInfo{
		Title:            title,
		DownloadURL:      "https://example.com/" + title,
		DownloadProtocol: protocol,
	}
	return decisionengine.Decision{Subject: sub}
}

func mkRejected(title string, protocol indexers.DownloadProtocol, episodeIDs ...int) decisionengine.Decision {
	dec := mkDecision(title, protocol, episodeIDs...)
	dec.Rejections = []decisionengine.Rejection{decisionengine.NewRejection(decisionengine.ReasonUnknownSeries, "no series")}
	return dec
}

func mkTemporarilyRejected(title string, protocol indexers.DownloadProtocol, episodeIDs ...int) decisionengine.Decision {
	dec := mkDecision(title, protocol, episodeIDs...)
	dec.Rejections = []decisionengine.Rejection{decisionengine.NewTemporaryRejection(decisionengine.ReasonError, "delay")}
	return dec
}

// ----------------------------------------------------------------------------

func TestNewProcessDownloadDecisionsRejectsNilLister(t *testing.T) {
	t.Parallel()
	_, err := download.NewProcessDownloadDecisions(nil, slog.Default())
	if err == nil {
		t.Fatal("nil ClientLister accepted; want error")
	}
}

func TestProcessDecisions_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	pdd := newPDD(t, &stubClientLister{})
	out := pdd.ProcessDecisions(context.Background(), nil)
	if out == nil {
		t.Fatal("ProcessDecisions returned nil")
	}
	if len(out.Grabbed)+len(out.Pending)+len(out.Rejected) != 0 {
		t.Errorf("expected empty result, got %s", out)
	}
}

func TestProcessDecisions_ApprovedUsenetGrabsViaProvider(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadID: "nzo_abc"}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	dec := mkDecision("Show.S01E01.NZB", indexers.DownloadProtocolUsenet, 1)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Errorf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if prov.calls != 1 {
		t.Errorf("provider.Download calls = %d, want 1", prov.calls)
	}
	if prov.lastReq == nil || prov.lastReq.ReleaseTitle != "Show.S01E01.NZB" {
		t.Errorf("lastReq = %+v, want ReleaseTitle=Show.S01E01.NZB", prov.lastReq)
	}
}

func TestProcessDecisions_RejectedDecisionsBypassProvider(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	dec := mkRejected("Bad.Release", indexers.DownloadProtocolUsenet, 1)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Rejected) != 1 {
		t.Errorf("Rejected = %d, want 1; full result: %s", len(out.Rejected), out)
	}
	if prov.calls != 0 {
		t.Errorf("provider.Download calls = %d, want 0 (rejected bypasses dispatch)", prov.calls)
	}
}

func TestProcessDecisions_NoEpisodesBucketsToRejected(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	dec := mkDecision("Show.NoEpisodes", indexers.DownloadProtocolUsenet) // no episode IDs
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Rejected) != 1 {
		t.Errorf("Rejected = %d, want 1 (IsQualifiedReport fails); full result: %s", len(out.Rejected), out)
	}
}

func TestProcessDecisions_TemporarilyRejectedBucketsToPending(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	dec := mkTemporarilyRejected("Show.Delayed", indexers.DownloadProtocolUsenet, 7)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Pending) != 1 {
		t.Errorf("Pending = %d, want 1; full result: %s", len(out.Pending), out)
	}
	if prov.calls != 0 {
		t.Errorf("provider.Download calls = %d, want 0 (temp-rejected → pending)", prov.calls)
	}
}

func TestProcessDecisions_EpisodeDedupSkipsDuplicateLowerPriority(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadID: "nzo"}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	// Two decisions for the same episode — only the first should
	// reach the provider.
	d1 := mkDecision("Show.S01E01.1080p", indexers.DownloadProtocolUsenet, 1)
	d2 := mkDecision("Show.S01E01.720p", indexers.DownloadProtocolUsenet, 1)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{d1, d2})

	if len(out.Grabbed) != 1 {
		t.Errorf("Grabbed = %d, want 1 (first only); full result: %s", len(out.Grabbed), out)
	}
	if prov.calls != 1 {
		t.Errorf("provider.Download calls = %d, want 1", prov.calls)
	}
}

func TestProcessDecisions_NoClientForProtocolBucketsAllToPending(t *testing.T) {
	t.Parallel()
	pdd := newPDD(t, &stubClientLister{}) // both protocol lists empty

	d1 := mkDecision("Show.S01E01.NZB", indexers.DownloadProtocolUsenet, 1)
	d2 := mkDecision("Show.S01E02.NZB", indexers.DownloadProtocolUsenet, 2)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{d1, d2})

	// First decision: no client → ResultFailed → Pending + trip
	// short-circuit. Second decision: short-circuit already tripped → Pending.
	if len(out.Pending) != 2 {
		t.Errorf("Pending = %d, want 2; full result: %s", len(out.Pending), out)
	}
}

func TestProcessDecisions_PerProtocolFailureShortCircuit(t *testing.T) {
	t.Parallel()
	// Two providers: one Usenet that fails with ErrUnavailable, one
	// Torrent that succeeds. The Usenet failure should trip the
	// Usenet-only short-circuit; subsequent Torrent decisions still
	// grab.
	usenet := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadErr: errors.New("unavailable: connection refused")}
	torrent := &stubProvider{name: "trans", protocol: indexers.DownloadProtocolTorrent, downloadID: "hash1"}
	pdd := newPDD(t, &stubClientLister{
		usenet:  []downloadclients.Provider{usenet},
		torrent: []downloadclients.Provider{torrent},
	})

	decs := []decisionengine.Decision{
		mkDecision("Show.S01E01.NZB", indexers.DownloadProtocolUsenet, 1),      // → Failed → Pending + trip
		mkDecision("Show.S01E02.NZB", indexers.DownloadProtocolUsenet, 2),      // → short-circuited → Pending
		mkDecision("Show.S01E03.torrent", indexers.DownloadProtocolTorrent, 3), // unaffected → Grabbed
	}
	out := pdd.ProcessDecisions(context.Background(), decs)

	if len(out.Pending) != 2 {
		t.Errorf("Pending = %d, want 2 (one Failed → Pending + one short-circuited); full result: %s", len(out.Pending), out)
	}
	if len(out.Grabbed) != 1 {
		t.Errorf("Grabbed = %d, want 1 (Torrent unaffected); full result: %s", len(out.Grabbed), out)
	}
	if usenet.calls != 1 {
		t.Errorf("usenet.calls = %d, want 1 (short-circuit suppresses second attempt)", usenet.calls)
	}
}

func TestProcessDecisions_NotImplementedYetBucketsToSkipped(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadErr: downloadclients.ErrNotImplementedYet}
	pdd := newPDD(t, &stubClientLister{usenet: []downloadclients.Provider{prov}})

	dec := mkDecision("Show.S01E01", indexers.DownloadProtocolUsenet, 1)
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	// ResultSkipped is silent — neither Grabbed nor Pending nor Rejected.
	if len(out.Grabbed)+len(out.Pending)+len(out.Rejected) != 0 {
		t.Errorf("expected all zero-state, got %s", out)
	}
}

func TestProcessDecisions_TorrentGrabPropagatesMagnetAndHash(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "trans", protocol: indexers.DownloadProtocolTorrent, downloadID: "hashABC"}
	pdd := newPDD(t, &stubClientLister{torrent: []downloadclients.Provider{prov}})

	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 1, Title: "Show"}
	sub.Episodes = []episodes.Episode{{ID: 9}}
	sub.Release = &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Show.S01E09.torrent",
			DownloadURL:      "https://example.com/t.torrent",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		MagnetURL: "magnet:?xt=urn:btih:HASHABC",
		InfoHash:  "hashABC",
	}
	dec := decisionengine.Decision{Subject: sub}
	out := pdd.ProcessDecisions(context.Background(), []decisionengine.Decision{dec})

	if len(out.Grabbed) != 1 {
		t.Errorf("Grabbed = %d, want 1; full result: %s", len(out.Grabbed), out)
	}
	if prov.lastReq == nil {
		t.Fatal("lastReq nil")
	}
	if prov.lastReq.MagnetURL != "magnet:?xt=urn:btih:HASHABC" {
		t.Errorf("MagnetURL = %q, want magnet:?xt=urn:btih:HASHABC", prov.lastReq.MagnetURL)
	}
	if prov.lastReq.InfoHash != "hashABC" {
		t.Errorf("InfoHash = %q, want hashABC", prov.lastReq.InfoHash)
	}
}

func TestProcessedDecisions_StringFormat(t *testing.T) {
	t.Parallel()
	pd := &download.ProcessedDecisions{
		Grabbed:  []decisionengine.Decision{{}, {}},
		Pending:  []decisionengine.Decision{{}},
		Rejected: []decisionengine.Decision{},
	}
	got := pd.String()
	want := "Grabbed=2 Pending=1 Rejected=0"
	if got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestProcessedDecisionResult_StringValues(t *testing.T) {
	t.Parallel()
	cases := map[download.ProcessedDecisionResult]string{
		download.ResultGrabbed:  "Grabbed",
		download.ResultPending:  "Pending",
		download.ResultRejected: "Rejected",
		download.ResultFailed:   "Failed",
		download.ResultSkipped:  "Skipped",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", int(r), got, want)
		}
	}
}
