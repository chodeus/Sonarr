package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubBlocklistChecker is the minimal BlocklistChecker test double.
type stubBlocklistChecker struct {
	blocked bool
	err     error
	calls   int
	gotID   int
	gotRel  any
}

func (s *stubBlocklistChecker) IsBlocklisted(_ context.Context, seriesID int, release any) (bool, error) {
	s.calls++
	s.gotID = seriesID
	s.gotRel = release
	return s.blocked, s.err
}

// stubBlocklistRepo is the minimal blocklistRepoReader for the
// RepoBlocklistChecker integration tests. Holds a fixed slice; the
// repo interface isn't exported so we test via the spec surface.
type stubBlocklistRepo struct{ rows []blocklist.Entry }

func (s stubBlocklistRepo) AllBySeriesID(_ context.Context, _ int) ([]blocklist.Entry, error) {
	return s.rows, nil
}

func TestBlocklist_NewRejectsNilChecker(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewBlocklistSpecification(nil, nil); err == nil {
		t.Fatalf("expected error on nil checker, got nil")
	}
}

func TestBlocklist_AcceptsNonBlocklistedRelease(t *testing.T) {
	t.Parallel()
	checker := &stubBlocklistChecker{blocked: false}
	spec, err := decisionengine.NewBlocklistSpecification(checker, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	subj := &decisionengine.Subject{
		Series:  &series.Series{ID: 7},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on non-blocklisted release, got %+v", got)
	}
	if checker.calls != 1 {
		t.Errorf("expected 1 checker call, got %d", checker.calls)
	}
	if checker.gotID != 7 {
		t.Errorf("expected seriesID=7 passed, got %d", checker.gotID)
	}
}

func TestBlocklist_RejectsBlocklistedRelease(t *testing.T) {
	t.Parallel()
	checker := &stubBlocklistChecker{blocked: true}
	spec, _ := decisionengine.NewBlocklistSpecification(checker, slog.New(slog.DiscardHandler))
	subj := &decisionengine.Subject{
		Series:  &series.Series{ID: 7},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on blocklisted release, got Accept")
	}
	if got.Reason != decisionengine.ReasonBlocklisted {
		t.Errorf("Reason = %v, want ReasonBlocklisted", got.Reason)
	}
	if got.Message != "Release is blocklisted" {
		t.Errorf("Message = %q, want exact C# string", got.Message)
	}
}

func TestBlocklist_AcceptsOnCheckerError(t *testing.T) {
	t.Parallel()
	checker := &stubBlocklistChecker{err: errors.New("boom")}
	spec, _ := decisionengine.NewBlocklistSpecification(checker, slog.New(slog.DiscardHandler))
	subj := &decisionengine.Subject{
		Series:  &series.Series{ID: 7},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on checker error (defensive), got %+v", got)
	}
}

func TestBlocklist_AcceptsNilSeries(t *testing.T) {
	t.Parallel()
	checker := &stubBlocklistChecker{blocked: true}
	spec, _ := decisionengine.NewBlocklistSpecification(checker, slog.New(slog.DiscardHandler))
	subj := &decisionengine.Subject{Release: &parser.ReleaseInfo{Title: "x"}}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Series, got %+v", got)
	}
	if checker.calls != 0 {
		t.Errorf("expected checker not called, got %d", checker.calls)
	}
}

func TestBlocklist_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	checker := &stubBlocklistChecker{blocked: true}
	spec, _ := decisionengine.NewBlocklistSpecification(checker, slog.New(slog.DiscardHandler))
	subj := &decisionengine.Subject{Series: &series.Series{ID: 7}}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Release, got %+v", got)
	}
}

func TestBlocklist_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, err := decisionengine.NewBlocklistSpecification(&stubBlocklistChecker{}, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.BlocklistSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDatabase {
		t.Errorf("Priority = %v, want Database", iface.Priority())
	}
}

// === RepoBlocklistChecker integration tests ===

// makeBlocklistRow builds a minimal blocklist.Entry. SeriesID is
// pinned to 7 (every test stub returns rows by series via the
// AllBySeriesID interface; the stored value here is informational
// only — the spec's lookup uses the candidate Subject's seriesID).
// published is the stored PublishedDate (nil when zero); size is the
// stored Size (nil when 0); protocol is wrapped in a *int.
func makeBlocklistRow(sourceTitle, indexer, hash string, protocol indexers.DownloadProtocol, published time.Time, size int64) blocklist.Entry {
	p := int(protocol)
	row := blocklist.Entry{
		SeriesID:        7,
		SourceTitle:     sourceTitle,
		Indexer:         indexer,
		TorrentInfoHash: hash,
		Protocol:        &p,
	}
	if !published.IsZero() {
		t := published
		row.PublishedDate = &t
	}
	if size != 0 {
		row.Size = &size
	}
	return row
}

func TestRepoBlocklistChecker_UsenetSamePublishDateMatches(t *testing.T) {
	t.Parallel()
	publish := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01.1080p.WEB-DL", "MyIndexer", "", indexers.DownloadProtocolUsenet, publish, 1_000_000_000),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	rel := &parser.ReleaseInfo{
		Title:            "Show.S01E01.1080p.WEB-DL",
		PublishDate:      publish,
		Size:             1_000_000_000,
		Indexer:          "MyIndexer",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
	got, err := checker.IsBlocklisted(t.Context(), 7, rel)
	if err != nil {
		t.Fatalf("IsBlocklisted: %v", err)
	}
	if !got {
		t.Errorf("expected blocklisted on exact-publish-date match")
	}
}

func TestRepoBlocklistChecker_UsenetTitleMismatchSkips(t *testing.T) {
	t.Parallel()
	publish := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01.OTHER", "MyIndexer", "", indexers.DownloadProtocolUsenet, publish, 1_000_000_000),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	rel := &parser.ReleaseInfo{
		Title:            "Show.S01E01.1080p.WEB-DL",
		PublishDate:      publish,
		Size:             1_000_000_000,
		Indexer:          "MyIndexer",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
	got, _ := checker.IsBlocklisted(t.Context(), 7, rel)
	if got {
		t.Errorf("expected not-blocklisted on title mismatch, got blocklisted")
	}
}

func TestRepoBlocklistChecker_UsenetCrossIndexerSizeAndDateMatch(t *testing.T) {
	t.Parallel()
	// Mirrors the C# !HasSameIndexer arm: indexers differ, but date
	// (within ±2min) and size (within ±2MB) match → SAME release.
	storedDate := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	candidateDate := storedDate.Add(time.Minute)
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01", "IndexerA", "", indexers.DownloadProtocolUsenet, storedDate, 1_000_000_000),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	rel := &parser.ReleaseInfo{
		Title:            "Show.S01E01",
		PublishDate:      candidateDate,
		Size:             1_000_500_000, // ~500KB diff, within 2MB
		Indexer:          "IndexerB",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
	got, _ := checker.IsBlocklisted(t.Context(), 7, rel)
	if !got {
		t.Errorf("expected blocklisted on cross-indexer + close-date + close-size, got not blocklisted")
	}
}

func TestRepoBlocklistChecker_UsenetSameIndexerWithoutExactDateNoMatch(t *testing.T) {
	t.Parallel()
	// Same indexer + drifted date + matching size: doesn't match.
	// The C# logic only flags cross-indexer + matching date + size.
	storedDate := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	candidateDate := storedDate.Add(time.Minute)
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01", "IndexerA", "", indexers.DownloadProtocolUsenet, storedDate, 1_000_000_000),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	rel := &parser.ReleaseInfo{
		Title:            "Show.S01E01",
		PublishDate:      candidateDate,
		Size:             1_000_000_000,
		Indexer:          "IndexerA",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
	got, _ := checker.IsBlocklisted(t.Context(), 7, rel)
	if got {
		t.Errorf("expected not blocklisted on same-indexer + drifted-date, got blocklisted")
	}
}

func TestRepoBlocklistChecker_TorrentByInfoHash(t *testing.T) {
	t.Parallel()
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01", "IndexerX", "ABC123DEF456", indexers.DownloadProtocolTorrent, time.Time{}, 0),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	tor := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "DifferentTitle.S01E01",
			Indexer:          "OtherIndexer",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		InfoHash: "abc123def456",
	}
	got, err := checker.IsBlocklisted(t.Context(), 7, tor)
	if err != nil {
		t.Fatalf("IsBlocklisted: %v", err)
	}
	if !got {
		t.Errorf("expected blocklisted on InfoHash match (case-insensitive)")
	}
}

func TestRepoBlocklistChecker_TorrentInfoHashNoMatch(t *testing.T) {
	t.Parallel()
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01", "IndexerX", "ABC123DEF456", indexers.DownloadProtocolTorrent, time.Time{}, 0),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	tor := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{Title: "Show.S01E01", DownloadProtocol: indexers.DownloadProtocolTorrent},
		InfoHash:    "DEADBEEF",
	}
	got, _ := checker.IsBlocklisted(t.Context(), 7, tor)
	if got {
		t.Errorf("expected not blocklisted on InfoHash mismatch")
	}
}

func TestRepoBlocklistChecker_TorrentNoInfoHashTitleAndIndexerMatch(t *testing.T) {
	t.Parallel()
	// Without InfoHash the matcher falls back to title + same-indexer.
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01", "MyIndexer", "", indexers.DownloadProtocolTorrent, time.Time{}, 0),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	tor := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Show.S01E01",
			Indexer:          "MyIndexer",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
	}
	got, _ := checker.IsBlocklisted(t.Context(), 7, tor)
	if !got {
		t.Errorf("expected blocklisted on title + same-indexer fallback")
	}
}

func TestRepoBlocklistChecker_ProtocolMismatch(t *testing.T) {
	t.Parallel()
	publish := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// Stored row is Torrent; candidate is Usenet — protocol gate rejects.
	repo := stubBlocklistRepo{rows: []blocklist.Entry{
		makeBlocklistRow("Show.S01E01", "X", "", indexers.DownloadProtocolTorrent, publish, 1_000_000_000),
	}}
	checker := decisionengine.NewRepoBlocklistChecker(repo)
	rel := &parser.ReleaseInfo{
		Title:            "Show.S01E01",
		PublishDate:      publish,
		Size:             1_000_000_000,
		Indexer:          "X",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
	got, _ := checker.IsBlocklisted(t.Context(), 7, rel)
	if got {
		t.Errorf("expected not blocklisted on protocol mismatch")
	}
}

func TestRepoBlocklistChecker_NilRelease(t *testing.T) {
	t.Parallel()
	checker := decisionengine.NewRepoBlocklistChecker(stubBlocklistRepo{})
	got, err := checker.IsBlocklisted(t.Context(), 7, nil)
	if err != nil {
		t.Fatalf("expected no error on nil release, got %v", err)
	}
	if got {
		t.Errorf("expected not blocklisted on nil release, got blocklisted")
	}
}
