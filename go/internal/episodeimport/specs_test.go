package episodeimport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func ptrInt(v int) *int { return &v }

// --- FullSeasonSpecification ---------------------------------------

func TestFullSeasonSpecification(t *testing.T) {
	t.Parallel()
	spec := episodeimport.NewFullSeasonSpecification(nil)

	if got := spec.Name(); got != "FullSeasonSpecification" {
		t.Errorf("Name() = %q", got)
	}

	// nil LocalEpisode → accept.
	if d := spec.IsSatisfiedBy(nil, nil); !d.Accepted() {
		t.Errorf("nil LocalEpisode: want Accept")
	}

	// nil FileEpisodeInfo → accept (parse failed elsewhere).
	if d := spec.IsSatisfiedBy(episodeimport.NewLocalEpisode(), nil); !d.Accepted() {
		t.Errorf("nil FileEpisodeInfo: want Accept")
	}

	// FileEpisodeInfo.FullSeason=false → accept.
	le := episodeimport.NewLocalEpisode()
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{FullSeason: false}
	if d := spec.IsSatisfiedBy(le, nil); !d.Accepted() {
		t.Errorf("FullSeason=false: want Accept")
	}

	// FileEpisodeInfo.FullSeason=true → reject FullSeason.
	le.FileEpisodeInfo.FullSeason = true
	d := spec.IsSatisfiedBy(le, nil)
	if d.Accepted() {
		t.Errorf("FullSeason=true: want Reject")
	}
	if d.Reason() != episodeimport.ImportRejectionFullSeason {
		t.Errorf("Reason = %v, want FullSeason", d.Reason())
	}
}

// --- SplitEpisodeSpecification -------------------------------------

func TestSplitEpisodeSpecification(t *testing.T) {
	t.Parallel()
	spec := episodeimport.NewSplitEpisodeSpecification(nil)

	if got := spec.Name(); got != "SplitEpisodeSpecification" {
		t.Errorf("Name() = %q", got)
	}

	// nil LocalEpisode → accept.
	if d := spec.IsSatisfiedBy(nil, nil); !d.Accepted() {
		t.Errorf("nil LocalEpisode: want Accept")
	}

	// nil FileEpisodeInfo → accept.
	if d := spec.IsSatisfiedBy(episodeimport.NewLocalEpisode(), nil); !d.Accepted() {
		t.Errorf("nil FileEpisodeInfo: want Accept")
	}

	le := episodeimport.NewLocalEpisode()
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{IsSplitEpisode: false}
	if d := spec.IsSatisfiedBy(le, nil); !d.Accepted() {
		t.Errorf("IsSplitEpisode=false: want Accept")
	}

	le.FileEpisodeInfo.IsSplitEpisode = true
	d := spec.IsSatisfiedBy(le, nil)
	if d.Accepted() {
		t.Errorf("IsSplitEpisode=true: want Reject")
	}
	if d.Reason() != episodeimport.ImportRejectionSplitEpisode {
		t.Errorf("Reason = %v, want SplitEpisode", d.Reason())
	}
}

// --- IsSameEpisodes / SameEpisodesImportSpecification --------------

type fakeEpisodeFinder struct {
	byFile map[int][]episodes.Episode
	err    error
	calls  int
}

func (f *fakeEpisodeFinder) GetEpisodesByFileID(_ context.Context, id int) ([]episodes.Episode, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.byFile[id], nil
}

func TestIsSameEpisodes_NoEpisodes(t *testing.T) {
	t.Parallel()
	finder := &fakeEpisodeFinder{}
	ok, err := episodeimport.IsSameEpisodes(t.Context(), finder, nil)
	if err != nil || !ok {
		t.Errorf("empty input: ok=%v err=%v, want (true, nil)", ok, err)
	}
	if finder.calls != 0 {
		t.Errorf("empty input called finder %d times; want 0", finder.calls)
	}
}

func TestIsSameEpisodes_NoLinkedFiles(t *testing.T) {
	t.Parallel()
	finder := &fakeEpisodeFinder{}
	eps := []episodes.Episode{
		{ID: 1, EpisodeFileID: nil},       // unlinked
		{ID: 2, EpisodeFileID: ptrInt(0)}, // C# treats 0 as "no file"
	}
	ok, err := episodeimport.IsSameEpisodes(t.Context(), finder, eps)
	if err != nil || !ok {
		t.Errorf("no linked files: ok=%v err=%v, want (true, nil)", ok, err)
	}
	if finder.calls != 0 {
		t.Errorf("no linked files called finder %d times; want 0", finder.calls)
	}
}

func TestIsSameEpisodes_AllLinkedSubset(t *testing.T) {
	t.Parallel()
	// Candidate covers episodes [1,2,3]; existing file 100 covers
	// [1,2] (subset) → safe to import.
	finder := &fakeEpisodeFinder{byFile: map[int][]episodes.Episode{
		100: {{ID: 1}, {ID: 2}},
	}}
	eps := []episodes.Episode{
		{ID: 1, EpisodeFileID: ptrInt(100)},
		{ID: 2, EpisodeFileID: ptrInt(100)},
		{ID: 3, EpisodeFileID: nil},
	}
	ok, err := episodeimport.IsSameEpisodes(t.Context(), finder, eps)
	if err != nil || !ok {
		t.Errorf("subset linked: ok=%v err=%v, want (true, nil)", ok, err)
	}
}

func TestIsSameEpisodes_LinkedFileExtraEpisode(t *testing.T) {
	t.Parallel()
	// Candidate covers [1,2]; existing file 100 covers [1,2,3]
	// (3 is NOT in candidate) → import would orphan 3.
	finder := &fakeEpisodeFinder{byFile: map[int][]episodes.Episode{
		100: {{ID: 1}, {ID: 2}, {ID: 3}},
	}}
	eps := []episodes.Episode{
		{ID: 1, EpisodeFileID: ptrInt(100)},
		{ID: 2, EpisodeFileID: ptrInt(100)},
	}
	ok, err := episodeimport.IsSameEpisodes(t.Context(), finder, eps)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if ok {
		t.Errorf("orphaned-episode case: want false")
	}
}

func TestIsSameEpisodes_NilFinder(t *testing.T) {
	t.Parallel()
	_, err := episodeimport.IsSameEpisodes(t.Context(), nil,
		[]episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(100)}})
	if err == nil {
		t.Errorf("nil finder: want error")
	}
}

func TestIsSameEpisodes_FinderError(t *testing.T) {
	t.Parallel()
	finder := &fakeEpisodeFinder{err: errors.New("boom")}
	_, err := episodeimport.IsSameEpisodes(t.Context(), finder,
		[]episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(100)}})
	if err == nil {
		t.Errorf("finder error: want propagated error")
	}
}

func TestSameEpisodesImportSpecification_NilFinder(t *testing.T) {
	t.Parallel()
	if _, err := episodeimport.NewSameEpisodesImportSpecification(nil, nil); err == nil {
		t.Errorf("nil finder: want constructor error")
	}
}

func TestSameEpisodesImportSpecification_NameAndAccept(t *testing.T) {
	t.Parallel()
	finder := &fakeEpisodeFinder{}
	spec, err := episodeimport.NewSameEpisodesImportSpecification(finder, nil)
	if err != nil {
		t.Fatalf("NewSameEpisodesImportSpecification: %v", err)
	}
	if got := spec.Name(); got != "SameEpisodesImportSpecification" {
		t.Errorf("Name() = %q", got)
	}
	// Empty episodes → accept.
	if d := spec.IsSatisfiedBy(episodeimport.NewLocalEpisode(), nil); !d.Accepted() {
		t.Errorf("empty episodes: want Accept")
	}
}

func TestSameEpisodesImportSpecification_RejectsOnExtraOnDisk(t *testing.T) {
	t.Parallel()
	finder := &fakeEpisodeFinder{byFile: map[int][]episodes.Episode{
		100: {{ID: 1}, {ID: 2}, {ID: 3}},
	}}
	spec, err := episodeimport.NewSameEpisodesImportSpecification(finder, nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	le := episodeimport.NewLocalEpisode()
	le.Episodes = []episodes.Episode{
		{ID: 1, EpisodeFileID: ptrInt(100)},
		{ID: 2, EpisodeFileID: ptrInt(100)},
	}
	d := spec.IsSatisfiedBy(le, nil)
	if d.Accepted() {
		t.Errorf("orphan-on-disk: want Reject")
	}
	if d.Reason() != episodeimport.ImportRejectionExistingFileHasMoreEpisodes {
		t.Errorf("Reason = %v, want ExistingFileHasMoreEpisodes", d.Reason())
	}
}

func TestSameEpisodesImportSpecification_LookupErrorAcceptsDefensively(t *testing.T) {
	t.Parallel()
	finder := &fakeEpisodeFinder{err: errors.New("transient db error")}
	spec, err := episodeimport.NewSameEpisodesImportSpecification(finder, nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	le := episodeimport.NewLocalEpisode()
	le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(100)}}
	if d := spec.IsSatisfiedBy(le, nil); !d.Accepted() {
		t.Errorf("lookup error: want Accept (defensive); got Reject %v", d.Reason())
	}
}

// --- UnverifiedSceneNumberingSpecification ------------------------

func TestUnverifiedSceneNumberingSpecification(t *testing.T) {
	t.Parallel()
	spec := episodeimport.NewUnverifiedSceneNumberingSpecification(nil)

	if got := spec.Name(); got != "UnverifiedSceneNumberingSpecification" {
		t.Errorf("Name() = %q", got)
	}

	// nil LocalEpisode → accept.
	if d := spec.IsSatisfiedBy(nil, nil); !d.Accepted() {
		t.Errorf("nil LocalEpisode: want Accept")
	}

	// ExistingFile=true skips the check even when episodes have
	// UnverifiedSceneNumbering=true.
	le := episodeimport.NewLocalEpisode()
	le.ExistingFile = true
	le.Episodes = []episodes.Episode{{ID: 1, UnverifiedSceneNumbering: true}}
	if d := spec.IsSatisfiedBy(le, nil); !d.Accepted() {
		t.Errorf("ExistingFile=true skips check: want Accept")
	}

	// ExistingFile=false + no unverified scene numbering → accept.
	le.ExistingFile = false
	le.Episodes = []episodes.Episode{{ID: 1, UnverifiedSceneNumbering: false}}
	if d := spec.IsSatisfiedBy(le, nil); !d.Accepted() {
		t.Errorf("no unverified: want Accept")
	}

	// At least one episode with UnverifiedSceneNumbering=true → reject.
	le.Episodes = []episodes.Episode{
		{ID: 1, UnverifiedSceneNumbering: false},
		{ID: 2, UnverifiedSceneNumbering: true},
	}
	d := spec.IsSatisfiedBy(le, nil)
	if d.Accepted() {
		t.Errorf("any unverified: want Reject")
	}
	if d.Reason() != episodeimport.ImportRejectionUnverifiedSceneMapping {
		t.Errorf("Reason = %v, want UnverifiedSceneMapping", d.Reason())
	}
}

// --- HasAudioTrackSpecification -----------------------------------

func TestHasAudioTrackSpecification(t *testing.T) {
	t.Parallel()
	spec := episodeimport.NewHasAudioTrackSpecification(nil)

	if got := spec.Name(); got != "HasAudioTrackSpecification" {
		t.Errorf("Name() = %q", got)
	}

	// nil LocalEpisode → accept.
	if d := spec.IsSatisfiedBy(nil, nil); !d.Accepted() {
		t.Errorf("nil LocalEpisode: want Accept")
	}

	// nil MediaInfo → accept (defensive: pre-aggregation, ffprobe
	// missing).
	if d := spec.IsSatisfiedBy(episodeimport.NewLocalEpisode(), nil); !d.Accepted() {
		t.Errorf("nil MediaInfo: want Accept")
	}

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.MediaInfo = &mediainfo.Model{
		RunTime:      datastore.STDuration(45 * time.Minute),
		AudioStreams: nil,
	}
	d := spec.IsSatisfiedBy(le, nil)
	if d.Accepted() {
		t.Errorf("zero audio tracks: want Reject")
	}
	if d.Reason() != episodeimport.ImportRejectionNoAudio {
		t.Errorf("Reason = %v, want NoAudio", d.Reason())
	}

	le.MediaInfo.AudioStreams = []mediainfo.AudioStream{}
	if d := spec.IsSatisfiedBy(le, nil); d.Accepted() {
		t.Errorf("empty AudioStreams: want Reject")
	}

	le.MediaInfo.AudioStreams = []mediainfo.AudioStream{{Format: "AC3"}}
	if d := spec.IsSatisfiedBy(le, nil); !d.Accepted() {
		t.Errorf("non-empty AudioStreams: want Accept")
	}
}

// --- NotSampleSpecification ----------------------------------------

// stubSampleChecker returns a fixed verdict and counts calls.
type stubSampleChecker struct {
	verdict episodeimport.DetectSampleResult
	calls   int
}

func (s *stubSampleChecker) IsSample(_ context.Context, _ *episodeimport.LocalEpisode) episodeimport.DetectSampleResult {
	s.calls++
	return s.verdict
}

func TestNotSampleSpecification(t *testing.T) {
	t.Parallel()

	t.Run("nil LocalEpisode accepts", func(t *testing.T) {
		spec := episodeimport.NewNotSampleSpecification(&stubSampleChecker{verdict: episodeimport.DetectSampleSample}, nil)
		if !spec.IsSatisfiedBy(nil, nil).Accepted() {
			t.Errorf("nil LocalEpisode: want Accept")
		}
	})

	t.Run("ExistingFile skips detector", func(t *testing.T) {
		det := &stubSampleChecker{verdict: episodeimport.DetectSampleSample}
		spec := episodeimport.NewNotSampleSpecification(det, nil)
		le := episodeimport.NewLocalEpisode()
		le.ExistingFile = true
		le.Path = "/tv/x.mkv"
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("ExistingFile=true: want Accept")
		}
		if det.calls != 0 {
			t.Errorf("detector called %d times for ExistingFile; want 0", det.calls)
		}
	})

	t.Run("nil detector accepts", func(t *testing.T) {
		spec := episodeimport.NewNotSampleSpecification(nil, nil)
		le := episodeimport.NewLocalEpisode()
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("nil detector: want Accept")
		}
	})

	t.Run("Sample rejects with Sample reason", func(t *testing.T) {
		spec := episodeimport.NewNotSampleSpecification(&stubSampleChecker{verdict: episodeimport.DetectSampleSample}, nil)
		le := episodeimport.NewLocalEpisode()
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("Sample verdict: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionSample {
			t.Errorf("Reason = %v, want Sample", d.Reason())
		}
	})

	t.Run("Indeterminate rejects with SampleIndeterminate reason", func(t *testing.T) {
		spec := episodeimport.NewNotSampleSpecification(&stubSampleChecker{verdict: episodeimport.DetectSampleIndeterminate}, nil)
		le := episodeimport.NewLocalEpisode()
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("Indeterminate verdict: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionSampleIndeterminate {
			t.Errorf("Reason = %v, want SampleIndeterminate", d.Reason())
		}
	})

	t.Run("NotSample accepts", func(t *testing.T) {
		spec := episodeimport.NewNotSampleSpecification(&stubSampleChecker{verdict: episodeimport.DetectSampleNotSample}, nil)
		le := episodeimport.NewLocalEpisode()
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("NotSample verdict: want Accept")
		}
	})

	if got := episodeimport.NewNotSampleSpecification(nil, nil).Name(); got != "NotSampleSpecification" {
		t.Errorf("Name() = %q", got)
	}
}

// --- AbsoluteEpisodeNumberSpecification ----------------------------

// stubFileNamer is a static [episodeimport.FileNamer] for tests.
type stubFileNamer struct {
	requiresAbsolute bool
	requiresTitle    bool
}

func (s stubFileNamer) RequiresAbsoluteEpisodeNumber() bool { return s.requiresAbsolute }

func (s stubFileNamer) RequiresEpisodeTitle(_ *series.Series, _ []episodes.Episode) bool {
	return s.requiresTitle
}

func TestAbsoluteEpisodeNumberSpecification(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	mkSpec := func(t *testing.T, namer episodeimport.FileNamer) *episodeimport.AbsoluteEpisodeNumberSpecification {
		t.Helper()
		spec, err := episodeimport.NewAbsoluteEpisodeNumberSpecification(namer, clock, nil)
		if err != nil {
			t.Fatalf("NewAbsoluteEpisodeNumberSpecification: %v", err)
		}
		return spec
	}

	t.Run("nil namer rejected at construction", func(t *testing.T) {
		if _, err := episodeimport.NewAbsoluteEpisodeNumberSpecification(nil, nil, nil); err == nil {
			t.Errorf("expected error for nil namer")
		}
	})

	t.Run("non-anime accepts", func(t *testing.T) {
		spec := mkSpec(t, stubFileNamer{requiresAbsolute: true})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{SeriesType: series.SeriesTypeStandard}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("standard series: want Accept")
		}
	})

	t.Run("pattern does not require absolute accepts", func(t *testing.T) {
		spec := mkSpec(t, stubFileNamer{requiresAbsolute: false})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{SeriesType: series.SeriesTypeAnime}
		le.Episodes = []episodes.Episode{{ID: 1}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("pattern not requiring absolute: want Accept")
		}
	})

	t.Run("episode aired > 1d ago is skipped", func(t *testing.T) {
		spec := mkSpec(t, stubFileNamer{requiresAbsolute: true})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{SeriesType: series.SeriesTypeAnime}
		old := now.AddDate(0, 0, -3)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &old, AbsoluteEpisodeNumber: nil}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("old episode without absolute: want Accept")
		}
	})

	t.Run("recent episode missing absolute rejects", func(t *testing.T) {
		spec := mkSpec(t, stubFileNamer{requiresAbsolute: true})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{SeriesType: series.SeriesTypeAnime}
		recent := now.Add(-1 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &recent, AbsoluteEpisodeNumber: nil}}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("recent + missing absolute: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionMissingAbsoluteEpisodeNumber {
			t.Errorf("Reason = %v", d.Reason())
		}
	})

	t.Run("recent episode with absolute accepts", func(t *testing.T) {
		spec := mkSpec(t, stubFileNamer{requiresAbsolute: true})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{SeriesType: series.SeriesTypeAnime}
		recent := now.Add(-1 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &recent, AbsoluteEpisodeNumber: ptrInt(42)}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("recent with absolute: want Accept")
		}
	})

	t.Run("nil AirDateUtc treated as recent (no skip)", func(t *testing.T) {
		spec := mkSpec(t, stubFileNamer{requiresAbsolute: true})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{SeriesType: series.SeriesTypeAnime}
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: nil, AbsoluteEpisodeNumber: nil}}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("nil AirDateUtc + missing absolute: want Reject")
		}
	})

	if got := mkSpec(t, stubFileNamer{}).Name(); got != "AbsoluteEpisodeNumberSpecification" {
		t.Errorf("Name() = %q", got)
	}
}

// --- EpisodeTitleSpecification --------------------------------------

// stubEpisodeTitleConfig is a static [episodeimport.EpisodeTitleConfig].
type stubEpisodeTitleConfig struct {
	v episodeimport.EpisodeTitleRequiredType
}

func (s stubEpisodeTitleConfig) EpisodeTitleRequired() episodeimport.EpisodeTitleRequiredType {
	return s.v
}

// stubEpisodeFinderBySeason is a canned [episodeimport.EpisodeFinderBySeason].
type stubEpisodeFinderBySeason struct {
	episodes                       []episodes.Episode
	err                            error
	calls                          int
	lastSeriesID, lastSeasonNumber int
}

func (s *stubEpisodeFinderBySeason) GetEpisodesBySeason(_ context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error) {
	s.calls++
	s.lastSeriesID, s.lastSeasonNumber = seriesID, seasonNumber
	if s.err != nil {
		return nil, s.err
	}
	return s.episodes, nil
}

func TestEpisodeTitleSpecification(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	mk := func(t *testing.T, cfg episodeimport.EpisodeTitleConfig, namer episodeimport.FileNamer, finder episodeimport.EpisodeFinderBySeason) *episodeimport.EpisodeTitleSpecification {
		t.Helper()
		spec, err := episodeimport.NewEpisodeTitleSpecification(cfg, namer, finder, clock, nil)
		if err != nil {
			t.Fatalf("NewEpisodeTitleSpecification: %v", err)
		}
		return spec
	}

	t.Run("nil deps rejected at construction", func(t *testing.T) {
		if _, err := episodeimport.NewEpisodeTitleSpecification(nil, stubFileNamer{}, &stubEpisodeFinderBySeason{}, nil, nil); err == nil {
			t.Errorf("expected nil-cfg error")
		}
		if _, err := episodeimport.NewEpisodeTitleSpecification(stubEpisodeTitleConfig{}, nil, &stubEpisodeFinderBySeason{}, nil, nil); err == nil {
			t.Errorf("expected nil-namer error")
		}
		if _, err := episodeimport.NewEpisodeTitleSpecification(stubEpisodeTitleConfig{}, stubFileNamer{}, nil, nil, nil); err == nil {
			t.Errorf("expected nil-finder error")
		}
	})

	t.Run("ExistingFile accepts", func(t *testing.T) {
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredAlways}, stubFileNamer{requiresTitle: true}, &stubEpisodeFinderBySeason{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		le.ExistingFile = true
		le.Episodes = []episodes.Episode{{ID: 1}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("ExistingFile: want Accept")
		}
	})

	t.Run("Never accepts even when title missing", func(t *testing.T) {
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredNever}, stubFileNamer{requiresTitle: true}, &stubEpisodeFinderBySeason{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		recent := now.Add(-1 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &recent, Title: ""}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("Never: want Accept")
		}
	})

	t.Run("pattern does not require title accepts", func(t *testing.T) {
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredAlways}, stubFileNamer{requiresTitle: false}, &stubEpisodeFinderBySeason{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		recent := now.Add(-1 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &recent, Title: ""}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("pattern not requiring title: want Accept")
		}
	})

	t.Run("Always rejects recent missing title", func(t *testing.T) {
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredAlways}, stubFileNamer{requiresTitle: true}, &stubEpisodeFinderBySeason{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		recent := now.Add(-1 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &recent, Title: ""}}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("missing title: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionTitleMissing {
			t.Errorf("Reason = %v, want TitleMissing", d.Reason())
		}
	})

	t.Run("Always rejects recent TBA title", func(t *testing.T) {
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredAlways}, stubFileNamer{requiresTitle: true}, &stubEpisodeFinderBySeason{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		recent := now.Add(-1 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &recent, Title: "TBA"}}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("TBA: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionTitleTba {
			t.Errorf("Reason = %v, want TitleTba", d.Reason())
		}
	})

	t.Run("aired > 48h ago is skipped", func(t *testing.T) {
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredAlways}, stubFileNamer{requiresTitle: true}, &stubEpisodeFinderBySeason{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		old := now.Add(-72 * time.Hour)
		le.Episodes = []episodes.Episode{{ID: 1, AirDateUtc: &old, Title: ""}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("old empty title: want Accept")
		}
	})

	t.Run("BulkSeasonReleases skips when same-day count < 4", func(t *testing.T) {
		air := now.Add(-1 * time.Hour)
		// finder returns 3 same-day episodes — below the bulk threshold.
		finder := &stubEpisodeFinderBySeason{episodes: []episodes.Episode{
			{ID: 10, AirDateUtc: &air}, {ID: 11, AirDateUtc: &air}, {ID: 12, AirDateUtc: &air},
		}}
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredBulkSeasonReleases}, stubFileNamer{requiresTitle: true}, finder)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		le.Episodes = []episodes.Episode{
			{ID: 1, SeriesID: 7, EpisodeNumber: 3, AirDateUtc: &air, Title: ""},
			{ID: 2, SeriesID: 7, EpisodeNumber: 4, AirDateUtc: &air, Title: ""},
		}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("bulk threshold not met: want Accept")
		}
		// Verify C# faithfulness: EpisodeNumber passed as seasonNumber.
		if finder.lastSeriesID != 7 || finder.lastSeasonNumber != 3 {
			t.Errorf("GetEpisodesBySeason called with (%d,%d); want (7,3) [first.EpisodeNumber]",
				finder.lastSeriesID, finder.lastSeasonNumber)
		}
	})

	t.Run("BulkSeasonReleases rejects when same-day count >= 4", func(t *testing.T) {
		air := now.Add(-1 * time.Hour)
		finder := &stubEpisodeFinderBySeason{episodes: []episodes.Episode{
			{ID: 10, AirDateUtc: &air}, {ID: 11, AirDateUtc: &air},
			{ID: 12, AirDateUtc: &air}, {ID: 13, AirDateUtc: &air},
		}}
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredBulkSeasonReleases}, stubFileNamer{requiresTitle: true}, finder)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		le.Episodes = []episodes.Episode{
			{ID: 1, SeriesID: 7, AirDateUtc: &air, Title: ""},
		}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("bulk threshold met + empty title: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionTitleMissing {
			t.Errorf("Reason = %v", d.Reason())
		}
	})

	t.Run("BulkSeasonReleases finder error accepts defensively", func(t *testing.T) {
		air := now.Add(-1 * time.Hour)
		finder := &stubEpisodeFinderBySeason{err: errors.New("boom")}
		spec := mk(t, stubEpisodeTitleConfig{v: episodeimport.EpisodeTitleRequiredBulkSeasonReleases}, stubFileNamer{requiresTitle: true}, finder)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		le.Episodes = []episodes.Episode{{ID: 1, SeriesID: 7, AirDateUtc: &air, Title: ""}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("finder error: want Accept (defensive)")
		}
	})

	if got := mk(t, stubEpisodeTitleConfig{}, stubFileNamer{}, &stubEpisodeFinderBySeason{}).Name(); got != "EpisodeTitleSpecification" {
		t.Errorf("Name() = %q", got)
	}
}

// --- EpisodeTitleRequiredType parser -------------------------------

func TestParseEpisodeTitleRequiredType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want episodeimport.EpisodeTitleRequiredType
	}{
		{"", episodeimport.EpisodeTitleRequiredAlways},
		{"Always", episodeimport.EpisodeTitleRequiredAlways},
		{"always", episodeimport.EpisodeTitleRequiredAlways},
		{"  ALWAYS  ", episodeimport.EpisodeTitleRequiredAlways},
		{"BulkSeasonReleases", episodeimport.EpisodeTitleRequiredBulkSeasonReleases},
		{"bulkseasonreleases", episodeimport.EpisodeTitleRequiredBulkSeasonReleases},
		{"Never", episodeimport.EpisodeTitleRequiredNever},
		{"never", episodeimport.EpisodeTitleRequiredNever},
		{"0", episodeimport.EpisodeTitleRequiredAlways},
		{"1", episodeimport.EpisodeTitleRequiredBulkSeasonReleases},
		{"2", episodeimport.EpisodeTitleRequiredNever},
		{"garbage", episodeimport.EpisodeTitleRequiredAlways},
	}
	for _, tc := range cases {
		if got := episodeimport.ParseEpisodeTitleRequiredType(tc.in); got != tc.want {
			t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	// String round-trip.
	for _, v := range []episodeimport.EpisodeTitleRequiredType{
		episodeimport.EpisodeTitleRequiredAlways,
		episodeimport.EpisodeTitleRequiredBulkSeasonReleases,
		episodeimport.EpisodeTitleRequiredNever,
	} {
		if got := episodeimport.ParseEpisodeTitleRequiredType(v.String()); got != v {
			t.Errorf("round-trip %v: got %v", v, got)
		}
	}
}
