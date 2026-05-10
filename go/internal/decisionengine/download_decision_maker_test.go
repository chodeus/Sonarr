package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubMapper pins Map responses on a per-call counter. The Map's
// arguments are captured for assertions.
type stubMapper struct {
	subjects []*decisionengine.Subject
	err      error
	calls    int
	lastArgs struct {
		tvdbID, tvRageID int
		imdbID           string
		criteria         *decisionengine.SearchCriteria
	}
}

func (s *stubMapper) Map(_ context.Context, parsedInfo *parser.ParsedEpisodeInfo, tvdbID, tvRageID int, imdbID string, criteria *decisionengine.SearchCriteria) (*decisionengine.Subject, error) {
	s.calls++
	s.lastArgs.tvdbID = tvdbID
	s.lastArgs.tvRageID = tvRageID
	s.lastArgs.imdbID = imdbID
	s.lastArgs.criteria = criteria
	if s.err != nil {
		return nil, s.err
	}
	idx := s.calls - 1
	if idx >= len(s.subjects) {
		// Default: empty subject carrying parsedInfo.
		sub := decisionengine.NewSubject()
		sub.ParsedEpisodeInfo = parsedInfo
		return sub, nil
	}
	sub := s.subjects[idx]
	if sub != nil && sub.ParsedEpisodeInfo == nil {
		sub.ParsedEpisodeInfo = parsedInfo
	}
	return sub, nil
}

type stubAggregator struct {
	called int
	mutate func(*decisionengine.Subject)
}

func (s *stubAggregator) Augment(_ context.Context, sub *decisionengine.Subject) *decisionengine.Subject {
	s.called++
	if s.mutate != nil {
		s.mutate(sub)
	}
	return sub
}

type stubSceneAlias struct {
	tvdbID *int
	err    error
	calls  []struct {
		seriesTitle, releaseTitle string
		seasonNumber              int
	}
}

func (s *stubSceneAlias) FindTvdbID(_ context.Context, seriesTitle, releaseTitle string, seasonNumber int) (*int, error) {
	s.calls = append(s.calls, struct {
		seriesTitle, releaseTitle string
		seasonNumber              int
	}{seriesTitle, releaseTitle, seasonNumber})
	return s.tvdbID, s.err
}

func newDDM(t *testing.T, mapper decisionengine.ReleaseMapper, agg decisionengine.Aggregator, alias decisionengine.SceneAliasFinder) *decisionengine.DownloadDecisionMaker {
	t.Helper()
	dm := decisionengine.NewDecisionMaker(nil, slog.Default())
	ddm, err := decisionengine.NewDownloadDecisionMaker(dm, mapper, agg, alias, slog.Default())
	if err != nil {
		t.Fatalf("NewDownloadDecisionMaker: %v", err)
	}
	return ddm
}

// NewDownloadDecisionMaker rejects nil decisions but tolerates nil
// mapper / aggregator / alias via noop stubs.
func TestNewDownloadDecisionMakerNilDecisionsErrors(t *testing.T) {
	t.Parallel()
	_, err := decisionengine.NewDownloadDecisionMaker(nil, nil, nil, nil, slog.Default())
	if err == nil {
		t.Fatal("nil decisions accepted; want error")
	}
}

func TestNewDownloadDecisionMakerNilOptionalsBindNoopStubs(t *testing.T) {
	t.Parallel()
	dm := decisionengine.NewDecisionMaker(nil, slog.Default())
	ddm, err := decisionengine.NewDownloadDecisionMaker(dm, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewDownloadDecisionMaker: %v", err)
	}
	// noopReleaseMapper returns a Subject with nil Series →
	// UnknownSeries rejection.
	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)
	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if len(out[0].Rejections) != 1 || out[0].Rejections[0].Reason != decisionengine.ReasonUnknownSeries {
		t.Errorf("rejection = %+v, want UnknownSeries", out[0].Rejections)
	}
}

func TestGetRssDecisionEmptyReportsReturnsEmpty(t *testing.T) {
	t.Parallel()
	ddm := newDDM(t, nil, nil, nil)
	out := ddm.GetRssDecision(context.Background(), nil, false)
	if len(out) != 0 {
		t.Errorf("decisions = %v, want empty", out)
	}
}

func TestGetRssDecisionUnknownSeriesRejection(t *testing.T) {
	t.Parallel()
	// Mapper returns Subject with nil Series.
	mapper := &stubMapper{subjects: []*decisionengine.Subject{decisionengine.NewSubject()}}
	ddm := newDDM(t, mapper, &stubAggregator{}, &stubSceneAlias{})

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if len(out[0].Rejections) != 1 || out[0].Rejections[0].Reason != decisionengine.ReasonUnknownSeries {
		t.Errorf("rejection = %+v, want UnknownSeries", out[0].Rejections)
	}
	if subjectOf(t, out[0]).ReleaseSource != decisionengine.ReleaseSourceRSS {
		t.Errorf("ReleaseSource = %v, want RSS", subjectOf(t, out[0]).ReleaseSource)
	}
}

func TestGetRssDecisionSceneAliasMatchEmitsMatchesAnotherSeries(t *testing.T) {
	t.Parallel()
	mapper := &stubMapper{subjects: []*decisionengine.Subject{decisionengine.NewSubject()}}
	tvdb := 9999
	alias := &stubSceneAlias{tvdbID: &tvdb}
	ddm := newDDM(t, mapper, &stubAggregator{}, alias)

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if out[0].Rejections[0].Reason != decisionengine.ReasonMatchesAnotherSeries {
		t.Errorf("rejection = %+v, want MatchesAnotherSeries", out[0].Rejections)
	}
	if len(alias.calls) != 1 {
		t.Errorf("alias calls = %d, want 1", len(alias.calls))
	}
}

func TestGetRssDecisionUnknownEpisodeRejection(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 7, Title: "Show"}
	sub := decisionengine.NewSubject()
	sub.Series = ser
	sub.Episodes = nil // empty episodes triggers UnknownEpisode
	mapper := &stubMapper{subjects: []*decisionengine.Subject{sub}}
	ddm := newDDM(t, mapper, &stubAggregator{}, &stubSceneAlias{})

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if out[0].Rejections[0].Reason != decisionengine.ReasonUnknownEpisode {
		t.Errorf("rejection = %+v, want UnknownEpisode", out[0].Rejections)
	}
}

func TestGetRssDecisionApprovedPathRunsAggregator(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 7, Title: "Show"}
	sub := decisionengine.NewSubject()
	sub.Series = ser
	sub.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1}}
	mapper := &stubMapper{subjects: []*decisionengine.Subject{sub}}
	agg := &stubAggregator{}
	ddm := newDDM(t, mapper, agg, &stubSceneAlias{})

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	// No specs registered → Decide returns approved (empty rejections).
	if len(out[0].Rejections) != 0 {
		t.Errorf("rejections = %+v, want empty (approved)", out[0].Rejections)
	}
	if agg.called != 1 {
		t.Errorf("aggregator called %d times, want 1", agg.called)
	}
	if !subjectOf(t, out[0]).DownloadAllowed {
		t.Errorf("DownloadAllowed = false, want true")
	}
}

func TestGetRssDecisionMapperErrorEmitsErrorRejection(t *testing.T) {
	t.Parallel()
	mapper := &stubMapper{err: errors.New("DB unreachable")}
	ddm := newDDM(t, mapper, &stubAggregator{}, &stubSceneAlias{})

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if out[0].Rejections[0].Reason != decisionengine.ReasonError {
		t.Errorf("rejection = %+v, want Error", out[0].Rejections)
	}
}

func TestGetRssDecisionDropsUnparseableReports(t *testing.T) {
	t.Parallel()
	ddm := newDDM(t, nil, nil, nil)
	// "asdf" doesn't parse to a ParsedEpisodeInfo with a SeriesTitle.
	reports := []any{&parser.ReleaseInfo{Title: "asdf"}}
	out := ddm.GetRssDecision(context.Background(), reports, false)
	if len(out) != 0 {
		t.Errorf("decisions = %v, want empty (unparseable dropped without criteria)", out)
	}
}

func TestGetRssDecisionPushedReleaseSourceTagged(t *testing.T) {
	t.Parallel()
	mapper := &stubMapper{subjects: []*decisionengine.Subject{decisionengine.NewSubject()}}
	ddm := newDDM(t, mapper, &stubAggregator{}, &stubSceneAlias{})

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetRssDecision(context.Background(), reports, true)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if subjectOf(t, out[0]).ReleaseSource != decisionengine.ReleaseSourceReleasePush {
		t.Errorf("ReleaseSource = %v, want ReleasePush", subjectOf(t, out[0]).ReleaseSource)
	}
}

// GetSearchDecision: the same per-report engine, plus the "couldn't
// parse" → UnableToParse fallback when criteria != nil.
func TestGetSearchDecisionUnparseableReportEmitsUnableToParse(t *testing.T) {
	t.Parallel()
	ddm := newDDM(t, nil, nil, nil)
	crit := &decisionengine.SearchCriteria{InteractiveSearch: true}
	reports := []any{&parser.ReleaseInfo{Title: "asdf"}}

	out := ddm.GetSearchDecision(context.Background(), reports, crit)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if out[0].Rejections[0].Reason != decisionengine.ReasonUnableToParse {
		t.Errorf("rejection = %+v, want UnableToParse", out[0].Rejections)
	}
	if subjectOf(t, out[0]).ReleaseSource != decisionengine.ReleaseSourceInteractiveSearch {
		t.Errorf("ReleaseSource = %v, want InteractiveSearch", subjectOf(t, out[0]).ReleaseSource)
	}
}

func TestGetSearchDecisionPropagatesCriteriaToMapper(t *testing.T) {
	t.Parallel()
	mapper := &stubMapper{subjects: []*decisionengine.Subject{decisionengine.NewSubject()}}
	ddm := newDDM(t, mapper, &stubAggregator{}, &stubSceneAlias{})
	crit := &decisionengine.SearchCriteria{UserInvokedSearch: true}

	reports := []any{&parser.ReleaseInfo{Title: "Show.S01E01-RlsGroup"}}
	out := ddm.GetSearchDecision(context.Background(), reports, crit)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if mapper.lastArgs.criteria != crit {
		t.Errorf("mapper received criteria = %v, want %v", mapper.lastArgs.criteria, crit)
	}
	if subjectOf(t, out[0]).ReleaseSource != decisionengine.ReleaseSourceUserInvokedSearch {
		t.Errorf("ReleaseSource = %v, want UserInvokedSearch", subjectOf(t, out[0]).ReleaseSource)
	}
}

func TestGetRssDecisionRecognisesTorrentInfo(t *testing.T) {
	t.Parallel()
	mapper := &stubMapper{subjects: []*decisionengine.Subject{decisionengine.NewSubject()}}
	ddm := newDDM(t, mapper, &stubAggregator{}, &stubSceneAlias{})

	torrent := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:    "Show.S01E01-RlsGroup",
			TvdbID:   1234,
			TvRageID: 5678,
			ImdbID:   "tt9999",
		},
	}
	reports := []any{torrent}
	out := ddm.GetRssDecision(context.Background(), reports, false)

	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1", len(out))
	}
	if mapper.lastArgs.tvdbID != 1234 || mapper.lastArgs.tvRageID != 5678 || mapper.lastArgs.imdbID != "tt9999" {
		t.Errorf("mapper args = %+v, want tvdbID=1234, tvRageID=5678, imdbID=tt9999", mapper.lastArgs)
	}
}

func TestGetRssDecisionSkipsNonReleaseEntries(t *testing.T) {
	t.Parallel()
	ddm := newDDM(t, nil, nil, nil)
	// Non-release entries — strings, ints — should be silently dropped.
	reports := []any{"asdf", 42, nil}
	out := ddm.GetRssDecision(context.Background(), reports, false)
	if len(out) != 0 {
		t.Errorf("decisions = %v, want empty", out)
	}
}

// subjectOf type-asserts Decision.Subject to *Subject. Decision.Subject
// is typed as `any` (the cross-pkg placeholder shape); every DDM test
// builds its Subject via NewSubject so the assertion is safe.
func subjectOf(t *testing.T, d decisionengine.Decision) *decisionengine.Subject {
	t.Helper()
	sub, ok := d.Subject.(*decisionengine.Subject)
	if !ok {
		t.Fatalf("Decision.Subject not *Subject: %T", d.Subject)
	}
	return sub
}
