package episodeimport_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// --- stubs ---------------------------------------------------------

type idmStubFilter struct {
	out  []string
	err  error
	seen []string
}

func (s *idmStubFilter) FilterExistingFiles(_ context.Context, candidates []string, _ *series.Series) ([]string, error) {
	s.seen = append(s.seen, candidates...)
	if s.err != nil {
		return nil, s.err
	}
	if s.out != nil {
		return s.out, nil
	}
	return candidates, nil
}

type idmStubAggregator struct {
	err       error
	mutate    func(*episodeimport.LocalEpisode)
	calls     int
	lastItem  *downloadclients.Item
	lastLE    *episodeimport.LocalEpisode
	panicWith any
}

func (s *idmStubAggregator) Augment(le *episodeimport.LocalEpisode, item *downloadclients.Item) (*episodeimport.LocalEpisode, error) {
	s.calls++
	s.lastItem = item
	s.lastLE = le
	if s.panicWith != nil {
		panic(s.panicWith)
	}
	if s.mutate != nil {
		s.mutate(le)
	}
	return le, s.err
}

type idmStubDisk struct {
	size int64
	err  error
}

func (s idmStubDisk) GetFileSize(string) (int64, error) { return s.size, s.err }

type idmStubSampler struct {
	results  map[string]episodeimport.DetectSampleResult
	default_ episodeimport.DetectSampleResult
	calls    int
	last     struct {
		path    string
		special bool
		runtime int
	}
}

func (s *idmStubSampler) IsSamplePath(_ context.Context, path string, isSpecial bool, expectedRuntimeMinutes int) episodeimport.DetectSampleResult {
	s.calls++
	s.last.path = path
	s.last.special = isSpecial
	s.last.runtime = expectedRuntimeMinutes
	if r, ok := s.results[path]; ok {
		return r
	}
	return s.default_
}

type idmStubParser struct {
	out    *parser.ParsedEpisodeInfo
	byPath map[string]*parser.ParsedEpisodeInfo
}

func (s *idmStubParser) ParsePath(path string) *parser.ParsedEpisodeInfo {
	if p, ok := s.byPath[path]; ok {
		return p
	}
	return s.out
}

type idmStubTracked struct {
	flags parser.IndexerFlags
	ok    bool
	calls int
	last  string
}

func (s *idmStubTracked) FindIndexerFlags(downloadID string) (parser.IndexerFlags, bool) {
	s.calls++
	s.last = downloadID
	return s.flags, s.ok
}

type idmStubFormats struct{ calls int }

func (s *idmStubFormats) UpdateEpisodeCustomFormats(*episodeimport.LocalEpisode) { s.calls++ }

type idmStubSpec struct {
	name      string
	verdict   *episodeimport.ImportSpecDecision
	panicWith any
	calls     int
}

func (s *idmStubSpec) Name() string { return s.name }
func (s *idmStubSpec) IsSatisfiedBy(*episodeimport.LocalEpisode, *downloadclients.Item) *episodeimport.ImportSpecDecision {
	s.calls++
	if s.panicWith != nil {
		panic(s.panicWith)
	}
	return s.verdict
}

// mkIDM constructs an ImportDecisionMaker with sane test defaults — no
// tracked-download finder, no formats calculator, default-accepting
// stub spec, etc. Tests override the pieces they need.
func mkIDM(
	t *testing.T,
	specs []episodeimport.Specification,
	filter episodeimport.MediaFileFilterer,
	aggregator episodeimport.Aggregator,
	disk episodeimport.FileSizeReader,
	sampler episodeimport.SampleClassifier,
	pathParser episodeimport.EpisodeInfoParser,
	tracked episodeimport.TrackedDownloadFinder,
	formats episodeimport.EpisodeCustomFormatCalculator,
) *episodeimport.ImportDecisionMaker {
	t.Helper()
	idm, err := episodeimport.NewImportDecisionMaker(specs, filter, aggregator, disk, sampler, pathParser, tracked, formats, slog.Default())
	if err != nil {
		t.Fatalf("NewImportDecisionMaker: %v", err)
	}
	return idm
}

// --- constructor ---------------------------------------------------

func TestNewImportDecisionMakerRejectsRequiredNils(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
		args func() (specs []episodeimport.Specification, f episodeimport.MediaFileFilterer, a episodeimport.Aggregator, d episodeimport.FileSizeReader, s episodeimport.SampleClassifier)
	}{
		{
			name: "nil filter",
			want: "nil MediaFileFilterer",
			args: func() ([]episodeimport.Specification, episodeimport.MediaFileFilterer, episodeimport.Aggregator, episodeimport.FileSizeReader, episodeimport.SampleClassifier) {
				return nil, nil, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}
			},
		},
		{
			name: "nil aggregator",
			want: "nil Aggregator",
			args: func() ([]episodeimport.Specification, episodeimport.MediaFileFilterer, episodeimport.Aggregator, episodeimport.FileSizeReader, episodeimport.SampleClassifier) {
				return nil, &idmStubFilter{}, nil, idmStubDisk{}, &idmStubSampler{}
			},
		},
		{
			name: "nil disk",
			want: "nil FileSizeReader",
			args: func() ([]episodeimport.Specification, episodeimport.MediaFileFilterer, episodeimport.Aggregator, episodeimport.FileSizeReader, episodeimport.SampleClassifier) {
				return nil, &idmStubFilter{}, &idmStubAggregator{}, nil, &idmStubSampler{}
			},
		},
		{
			name: "nil sampler",
			want: "nil SampleClassifier",
			args: func() ([]episodeimport.Specification, episodeimport.MediaFileFilterer, episodeimport.Aggregator, episodeimport.FileSizeReader, episodeimport.SampleClassifier) {
				return nil, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specs, f, a, d, s := tc.args()
			_, err := episodeimport.NewImportDecisionMaker(specs, f, a, d, s, nil, nil, nil, nil)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestNewImportDecisionMakerOptionalNilsAccepted(t *testing.T) {
	t.Parallel()

	idm, err := episodeimport.NewImportDecisionMaker(
		nil, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{},
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("nil optionals: %v", err)
	}
	if idm == nil {
		t.Fatalf("nil idm")
	}
}

// --- GetDecision (public per-LocalEpisode) -------------------------

func TestGetDecisionPublicNoSpecsApproved(t *testing.T) {
	t.Parallel()

	idm := mkIDM(t, nil, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}, nil, nil, nil)
	le := episodeimport.NewLocalEpisode()
	le.Path = "/x.mkv"

	d := idm.GetDecision(le, nil)
	if d == nil {
		t.Fatalf("nil decision")
	}
	if !d.Approved() {
		t.Fatalf("expected approved, got rejections=%v", d.Rejections)
	}
	if d.LocalEpisode != le {
		t.Errorf("decision.LocalEpisode != input")
	}
}

func TestGetDecisionPublicCollectsRejections(t *testing.T) {
	t.Parallel()

	a := &idmStubSpec{
		name:    "AlphaSpec",
		verdict: episodeimport.RejectImportSpec(episodeimport.ImportRejectionFullSeason, "alpha"),
	}
	b := &idmStubSpec{name: "BetaSpec", verdict: episodeimport.AcceptImportSpec()}
	c := &idmStubSpec{
		name:    "GammaSpec",
		verdict: episodeimport.RejectImportSpec(episodeimport.ImportRejectionSplitEpisode, "gamma"),
	}

	idm := mkIDM(t, []episodeimport.Specification{a, b, c}, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}, nil, nil, nil)
	le := episodeimport.NewLocalEpisode()
	d := idm.GetDecision(le, nil)
	if d.Approved() {
		t.Fatalf("expected rejected")
	}
	if len(d.Rejections) != 2 {
		t.Fatalf("rejections=%d, want 2", len(d.Rejections))
	}
	if d.Rejections[0].Reason != episodeimport.ImportRejectionFullSeason ||
		d.Rejections[1].Reason != episodeimport.ImportRejectionSplitEpisode {
		t.Errorf("unexpected order: %+v", d.Rejections)
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 1 {
		t.Errorf("spec calls a=%d b=%d c=%d", a.calls, b.calls, c.calls)
	}
}

func TestGetDecisionPublicSpecPanicMapsToDecisionError(t *testing.T) {
	t.Parallel()

	bad := &idmStubSpec{name: "BoomSpec", panicWith: errors.New("boom")}
	good := &idmStubSpec{name: "GoodSpec", verdict: episodeimport.AcceptImportSpec()}
	idm := mkIDM(t, []episodeimport.Specification{bad, good}, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}, nil, nil, nil)

	d := idm.GetDecision(episodeimport.NewLocalEpisode(), nil)
	if d.Approved() {
		t.Fatalf("expected rejected")
	}
	if len(d.Rejections) != 1 {
		t.Fatalf("rejections=%d, want 1", len(d.Rejections))
	}
	if d.Rejections[0].Reason != episodeimport.ImportRejectionDecisionError {
		t.Errorf("reason=%v, want DecisionError", d.Rejections[0].Reason)
	}
	if !strings.Contains(d.Rejections[0].Message, "BoomSpec") {
		t.Errorf("msg=%q does not mention BoomSpec", d.Rejections[0].Message)
	}
	// good spec ran AFTER the panic — the orchestrator must not
	// short-circuit on a per-spec failure.
	if good.calls != 1 {
		t.Errorf("good spec calls=%d, want 1", good.calls)
	}
}

func TestGetDecisionPublicNilLocalEpisodeReturnsNil(t *testing.T) {
	t.Parallel()

	idm := mkIDM(t, nil, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}, nil, nil, nil)
	if d := idm.GetDecision(nil, nil); d != nil {
		t.Errorf("got %+v, want nil", d)
	}
}

// --- GetImportDecisions (public batch) -----------------------------

func ts(t *testing.T) *series.Series {
	t.Helper()
	return &series.Series{ID: 7, Title: "Test", Path: "/library/Test", Runtime: 25}
}

// idmHappyAggregator fills LE.Episodes with one entry so the empty-
// triage path is skipped.
func idmHappyAggregator() *idmStubAggregator {
	return &idmStubAggregator{
		mutate: func(le *episodeimport.LocalEpisode) {
			le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 2}}
		},
	}
}

func TestGetImportDecisionsBasicHappyPath(t *testing.T) {
	t.Parallel()

	disk := idmStubDisk{size: 100}
	aggr := idmHappyAggregator()
	parser := &idmStubParser{out: &parser.ParsedEpisodeInfo{SeasonNumber: 1, EpisodeNumbers: []int{2}}}
	spec := &idmStubSpec{name: "OK", verdict: episodeimport.AcceptImportSpec()}

	idm := mkIDM(t, []episodeimport.Specification{spec}, &idmStubFilter{}, aggr, disk, &idmStubSampler{}, parser, nil, nil)
	files := []string{"/in/a.mkv", "/in/b.mkv"}

	got, err := idm.GetImportDecisions(context.Background(), files, ts(t), episodeimport.GetImportDecisionsOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decisions=%d, want 2", len(got))
	}
	for i, d := range got {
		if !d.Approved() {
			t.Errorf("decision[%d] not approved: %+v", i, d.Rejections)
		}
		if d.LocalEpisode.Size != 100 {
			t.Errorf("decision[%d].Size=%d, want 100", i, d.LocalEpisode.Size)
		}
		if d.LocalEpisode.Path != files[i] {
			t.Errorf("decision[%d].Path=%q, want %q", i, d.LocalEpisode.Path, files[i])
		}
	}
}

func TestGetImportDecisionsFilterExistingPropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("db down")
	idm := mkIDM(t, nil, &idmStubFilter{err: wantErr}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}, nil, nil, nil)
	_, err := idm.GetImportDecisions(context.Background(), []string{"/x"}, ts(t), episodeimport.GetImportDecisionsOptions{FilterExistingFiles: true})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wraps %v", err, wantErr)
	}
}

func TestGetImportDecisionsFilterExistingDropsFiles(t *testing.T) {
	t.Parallel()

	filter := &idmStubFilter{out: []string{"/in/keep.mkv"}}
	idm := mkIDM(t, nil, filter, idmHappyAggregator(), idmStubDisk{size: 1}, &idmStubSampler{}, &idmStubParser{}, nil, nil)
	got, err := idm.GetImportDecisions(context.Background(), []string{"/in/keep.mkv", "/in/drop.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{FilterExistingFiles: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("decisions=%d, want 1", len(got))
	}
	if got[0].LocalEpisode.Path != "/in/keep.mkv" {
		t.Errorf("path=%q, want /in/keep.mkv", got[0].LocalEpisode.Path)
	}
	if len(filter.seen) != 2 {
		t.Errorf("filter saw %d files, want 2", len(filter.seen))
	}
}

func TestGetImportDecisionsExistingFileFlag(t *testing.T) {
	t.Parallel()

	idm := mkIDM(t, nil, &idmStubFilter{}, idmHappyAggregator(), idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, nil, nil)
	s := ts(t) // Path = /library/Test
	got, err := idm.GetImportDecisions(context.Background(), []string{
		"/library/Test/Season 01/inside.mkv",
		"/downloads/outside.mkv",
	}, s, episodeimport.GetImportDecisionsOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got[0].LocalEpisode.ExistingFile {
		t.Errorf("inside file: ExistingFile=false, want true")
	}
	if got[1].LocalEpisode.ExistingFile {
		t.Errorf("outside file: ExistingFile=true, want false")
	}
}

func TestGetImportDecisionsSceneSourceFlagsOtherVideoFiles(t *testing.T) {
	t.Parallel()

	sampler := &idmStubSampler{
		results: map[string]episodeimport.DetectSampleResult{
			"/in/sample.mkv": episodeimport.DetectSampleSample,
		},
		default_: episodeimport.DetectSampleNotSample,
	}
	files := []string{"/in/sample.mkv", "/in/full1.mkv", "/in/full2.mkv"}

	idm := mkIDM(t, nil, &idmStubFilter{}, idmHappyAggregator(), idmStubDisk{}, sampler, &idmStubParser{}, nil, nil)
	got, err := idm.GetImportDecisions(context.Background(), files, ts(t), episodeimport.GetImportDecisionsOptions{SceneSource: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sampler.calls != 3 {
		t.Errorf("sampler calls=%d, want 3", sampler.calls)
	}
	// nonSampleVideoFileCount should be 2 (full1, full2) — gives
	// OtherVideoFiles=true on every produced LocalEpisode.
	for _, d := range got {
		if !d.LocalEpisode.OtherVideoFiles {
			t.Errorf("OtherVideoFiles=false on %s", d.LocalEpisode.Path)
		}
	}
}

func TestGetImportDecisionsNonSceneSourceSkipsSampler(t *testing.T) {
	t.Parallel()

	sampler := &idmStubSampler{default_: episodeimport.DetectSampleSample}
	idm := mkIDM(t, nil, &idmStubFilter{}, idmHappyAggregator(), idmStubDisk{}, sampler, &idmStubParser{}, nil, nil)
	files := []string{"/lib/a.mkv", "/lib/b.mkv"}
	got, err := idm.GetImportDecisions(context.Background(), files, ts(t), episodeimport.GetImportDecisionsOptions{SceneSource: false})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sampler.calls != 0 {
		t.Errorf("sampler should not run when SceneSource=false; calls=%d", sampler.calls)
	}
	// non-scene path uses len(videoFiles) = 2 → OtherVideoFiles true
	for _, d := range got {
		if !d.LocalEpisode.OtherVideoFiles {
			t.Errorf("OtherVideoFiles=false on %s", d.LocalEpisode.Path)
		}
	}
}

func TestGetImportDecisionsSceneSourceSpecialFlagPropagates(t *testing.T) {
	t.Parallel()

	sampler := &idmStubSampler{default_: episodeimport.DetectSampleNotSample}
	folderInfo := &parser.ParsedEpisodeInfo{
		// Forces IsPossibleSpecialEpisode → true via the
		// "single E0 sentinel" branch in parser model.
		EpisodeNumbers: []int{0},
	}

	idm := mkIDM(t, nil, &idmStubFilter{}, idmHappyAggregator(), idmStubDisk{}, sampler, &idmStubParser{}, nil, nil)
	_, err := idm.GetImportDecisions(context.Background(), []string{"/in/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{
		SceneSource: true,
		FolderInfo:  folderInfo,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !sampler.last.special {
		t.Errorf("sampler.special=false, want true (folderInfo flagged special)")
	}
	if sampler.last.runtime != 25 {
		t.Errorf("sampler.runtime=%d, want 25 (series.Runtime)", sampler.last.runtime)
	}
}

// --- per-file pipeline rejection arms ------------------------------

func TestPerFileEmptyEpisodesPartialSeason(t *testing.T) {
	t.Parallel()

	// Aggregator leaves Episodes empty.
	aggr := &idmStubAggregator{}
	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{IsPartialSeason: true}}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)

	got, err := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0].Approved() || got[0].Rejections[0].Reason != episodeimport.ImportRejectionPartialSeason {
		t.Errorf("got %+v, want PartialSeason rejection", got[0].Rejections)
	}
}

func TestPerFileEmptyEpisodesSeasonExtra(t *testing.T) {
	t.Parallel()

	aggr := &idmStubAggregator{}
	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{IsSeasonExtra: true}}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)

	got, err := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0].Approved() || got[0].Rejections[0].Reason != episodeimport.ImportRejectionSeasonExtra {
		t.Errorf("got %+v, want SeasonExtra rejection", got[0].Rejections)
	}
}

func TestPerFileEmptyEpisodesInvalidSeasonOrEpisode(t *testing.T) {
	t.Parallel()

	aggr := &idmStubAggregator{}
	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{}}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)

	got, err := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0].Approved() || got[0].Rejections[0].Reason != episodeimport.ImportRejectionInvalidSeasonOrEpisode {
		t.Errorf("got %+v, want InvalidSeasonOrEpisode rejection", got[0].Rejections)
	}
}

func TestPerFileEmptyEpisodesPartialSeasonPriorityOverExtra(t *testing.T) {
	t.Parallel()

	// Both flags set on file info — PartialSeason wins (matches the
	// C# switch order at `:128-138` evaluating IsPartialSeason first).
	aggr := &idmStubAggregator{}
	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{IsPartialSeason: true, IsSeasonExtra: true}}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)

	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if got[0].Rejections[0].Reason != episodeimport.ImportRejectionPartialSeason {
		t.Errorf("got %v, want PartialSeason", got[0].Rejections[0].Reason)
	}
}

func TestPerFileAggregatingFailedMapsToUnableToParse(t *testing.T) {
	t.Parallel()

	aggr := &idmStubAggregator{err: episodeimport.ErrAugmentingFailed}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, nil, nil)
	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if got[0].Rejections[0].Reason != episodeimport.ImportRejectionUnableToParse {
		t.Errorf("got %v, want UnableToParse", got[0].Rejections[0].Reason)
	}
}

func TestPerFileGenericAggregatorErrorMapsToError(t *testing.T) {
	t.Parallel()

	aggr := &idmStubAggregator{err: errors.New("synthetic")}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, nil, nil)
	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if got[0].Rejections[0].Reason != episodeimport.ImportRejectionError {
		t.Errorf("got %v, want Error", got[0].Rejections[0].Reason)
	}
}

func TestPerFileAggregatorPanicMapsToError(t *testing.T) {
	t.Parallel()

	aggr := &idmStubAggregator{panicWith: "blew up"}
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, nil, nil)
	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if got[0].Rejections[0].Reason != episodeimport.ImportRejectionError {
		t.Errorf("got %v, want Error", got[0].Rejections[0].Reason)
	}
}

func TestPerFileFileSizeErrorMapsToError(t *testing.T) {
	t.Parallel()

	idm := mkIDM(t, nil, &idmStubFilter{}, idmHappyAggregator(), idmStubDisk{err: errors.New("EIO")}, &idmStubSampler{}, &idmStubParser{}, nil, nil)
	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if got[0].Rejections[0].Reason != episodeimport.ImportRejectionError {
		t.Errorf("got %v, want Error", got[0].Rejections[0].Reason)
	}
}

// --- enrichment arms -----------------------------------------------

func TestPerFileTrackedDownloadEnrichesIndexerFlags(t *testing.T) {
	t.Parallel()

	tracked := &idmStubTracked{flags: parser.IndexerFlags(4), ok: true}
	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, tracked, nil)

	item := &downloadclients.Item{DownloadID: "abc123"}
	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{DownloadItem: item})
	if got[0].LocalEpisode.IndexerFlags != parser.IndexerFlags(4) {
		t.Errorf("IndexerFlags=%v, want 4", got[0].LocalEpisode.IndexerFlags)
	}
	if tracked.last != "abc123" {
		t.Errorf("tracked.last=%q, want abc123", tracked.last)
	}
}

func TestPerFileTrackedDownloadOkFalseSkipsWrite(t *testing.T) {
	t.Parallel()

	tracked := &idmStubTracked{flags: parser.IndexerFlags(8), ok: false}
	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, tracked, nil)

	item := &downloadclients.Item{DownloadID: "missing"}
	got, _ := idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{DownloadItem: item})
	if got[0].LocalEpisode.IndexerFlags != 0 {
		t.Errorf("IndexerFlags=%v, want zero", got[0].LocalEpisode.IndexerFlags)
	}
}

func TestPerFileEmptyDownloadIDSkipsTracked(t *testing.T) {
	t.Parallel()

	tracked := &idmStubTracked{flags: 99, ok: true}
	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, tracked, nil)

	item := &downloadclients.Item{DownloadID: ""}
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{DownloadItem: item})
	if tracked.calls != 0 {
		t.Errorf("tracked.calls=%d, want 0 (empty DownloadID)", tracked.calls)
	}
}

func TestPerFileFormatsCalculatorRunsOnApprovedFiles(t *testing.T) {
	t.Parallel()

	formats := &idmStubFormats{}
	idm := mkIDM(t, nil, &idmStubFilter{}, idmHappyAggregator(), idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, nil, formats)
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv", "/y.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if formats.calls != 2 {
		t.Errorf("formats.calls=%d, want 2", formats.calls)
	}
}

func TestPerFileFormatsCalculatorSkippedOnEmptyEpisodes(t *testing.T) {
	t.Parallel()

	formats := &idmStubFormats{}
	idm := mkIDM(t, nil, &idmStubFilter{}, &idmStubAggregator{}, idmStubDisk{}, &idmStubSampler{}, &idmStubParser{}, nil, formats)
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if formats.calls != 0 {
		t.Errorf("formats.calls=%d, want 0 (empty Episodes path skips formats)", formats.calls)
	}
}

// --- ReleaseType priority chain ------------------------------------

func TestPerFileReleaseTypeDownloadClientPreferred(t *testing.T) {
	t.Parallel()

	dc := &parser.ParsedEpisodeInfo{EpisodeNumbers: []int{1, 2, 3}}        // Multi
	folder := &parser.ParsedEpisodeInfo{EpisodeNumbers: []int{1}}          // Single
	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{FullSeason: true}} // SeasonPack

	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{
		DownloadItemInfo: dc,
		FolderInfo:       folder,
	})

	// aggregator gets the LE pre-set; verify ReleaseType assignment
	if aggr.lastLE.ReleaseType != parser.ReleaseTypeMultiEpisode {
		t.Errorf("ReleaseType=%v, want MultiEpisode (from DC)", aggr.lastLE.ReleaseType)
	}
}

func TestPerFileReleaseTypeFolderFallback(t *testing.T) {
	t.Parallel()

	folder := &parser.ParsedEpisodeInfo{EpisodeNumbers: []int{1}}
	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{FullSeason: true}}
	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{FolderInfo: folder})
	if aggr.lastLE.ReleaseType != parser.ReleaseTypeSingleEpisode {
		t.Errorf("ReleaseType=%v, want Single (from Folder)", aggr.lastLE.ReleaseType)
	}
}

func TestPerFileReleaseTypeFileFallback(t *testing.T) {
	t.Parallel()

	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{FullSeason: true}}
	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if aggr.lastLE.ReleaseType != parser.ReleaseTypeSeasonPack {
		t.Errorf("ReleaseType=%v, want SeasonPack (from File)", aggr.lastLE.ReleaseType)
	}
}

func TestPerFileReleaseTypeUnknownAllNil(t *testing.T) {
	t.Parallel()

	pp := &idmStubParser{out: &parser.ParsedEpisodeInfo{}}
	aggr := idmHappyAggregator()
	idm := mkIDM(t, nil, &idmStubFilter{}, aggr, idmStubDisk{}, &idmStubSampler{}, pp, nil, nil)
	_, _ = idm.GetImportDecisions(context.Background(), []string{"/x.mkv"}, ts(t), episodeimport.GetImportDecisionsOptions{})
	if aggr.lastLE.ReleaseType != parser.ReleaseTypeUnknown {
		t.Errorf("ReleaseType=%v, want Unknown", aggr.lastLE.ReleaseType)
	}
}
