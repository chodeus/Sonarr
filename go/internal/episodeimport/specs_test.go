package episodeimport_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
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

// --- FreeSpaceSpecification ----------------------------------------

type stubFreeSpaceConfig struct {
	skip  bool
	minMB int
}

func (s stubFreeSpaceConfig) SkipFreeSpaceCheckWhenImporting() bool { return s.skip }
func (s stubFreeSpaceConfig) MinimumFreeSpaceWhenImporting() int    { return s.minMB }

type stubAvailableSpace struct {
	free int64
	err  error
	last string
}

func (s *stubAvailableSpace) GetAvailableSpace(path string) (int64, error) {
	s.last = path
	return s.free, s.err
}

func TestFreeSpaceSpecification(t *testing.T) {
	t.Parallel()
	mk := func(t *testing.T, cfg episodeimport.FreeSpaceConfig, dp episodeimport.AvailableSpaceProvider) *episodeimport.FreeSpaceSpecification {
		t.Helper()
		spec, err := episodeimport.NewFreeSpaceSpecification(cfg, dp, nil)
		if err != nil {
			t.Fatalf("NewFreeSpaceSpecification: %v", err)
		}
		return spec
	}

	t.Run("nil deps rejected at construction", func(t *testing.T) {
		if _, err := episodeimport.NewFreeSpaceSpecification(nil, &stubAvailableSpace{}, nil); err == nil {
			t.Errorf("expected nil-cfg error")
		}
		if _, err := episodeimport.NewFreeSpaceSpecification(stubFreeSpaceConfig{}, nil, nil); err == nil {
			t.Errorf("expected nil-disk error")
		}
	})

	t.Run("Skip toggle accepts", func(t *testing.T) {
		dp := &stubAvailableSpace{free: 0}
		spec := mk(t, stubFreeSpaceConfig{skip: true}, dp)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{Path: "/tv/x"}
		le.Size = 1 << 40
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("Skip=true: want Accept")
		}
		if dp.last != "" {
			t.Errorf("disk should not have been queried")
		}
	})

	t.Run("ExistingFile accepts", func(t *testing.T) {
		spec := mk(t, stubFreeSpaceConfig{minMB: 100}, &stubAvailableSpace{free: 0})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{Path: "/tv/x"}
		le.ExistingFile = true
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("ExistingFile: want Accept")
		}
	})

	t.Run("nil Series.Path accepts defensively", func(t *testing.T) {
		spec := mk(t, stubFreeSpaceConfig{minMB: 100}, &stubAvailableSpace{free: 0})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("empty Series.Path: want Accept")
		}
	})

	t.Run("disk error accepts defensively", func(t *testing.T) {
		spec := mk(t, stubFreeSpaceConfig{minMB: 100}, &stubAvailableSpace{err: errors.New("boom")})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{Path: "/tv/x"}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("disk err: want Accept (defensive)")
		}
	})

	t.Run("not enough free space rejects", func(t *testing.T) {
		// 50MB free, file is 60MB, threshold = 60MB+100MB → 160MB; reject.
		dp := &stubAvailableSpace{free: 50 << 20}
		spec := mk(t, stubFreeSpaceConfig{minMB: 100}, dp)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{Path: "/tv/x"}
		le.Size = 60 << 20
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("low free space: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionMinimumFreeSpace {
			t.Errorf("Reason = %v", d.Reason())
		}
	})

	t.Run("enough free space accepts", func(t *testing.T) {
		// 200MB free, 60MB file + 100MB min → 160MB threshold. 200>160 → accept.
		dp := &stubAvailableSpace{free: 200 << 20}
		spec := mk(t, stubFreeSpaceConfig{minMB: 100}, dp)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{Path: "/tv/x"}
		le.Size = 60 << 20
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("ample free space: want Accept")
		}
	})

	if got := mk(t, stubFreeSpaceConfig{}, &stubAvailableSpace{}).Name(); got != "FreeSpaceSpecification" {
		t.Errorf("Name() = %q", got)
	}
}

// --- MatchesGrabSpecification --------------------------------------

func TestMatchesGrabSpecification(t *testing.T) {
	t.Parallel()
	spec := episodeimport.NewMatchesGrabSpecification(nil)

	if got := spec.Name(); got != "MatchesGrabSpecification" {
		t.Errorf("Name() = %q", got)
	}

	t.Run("ExistingFile accepts", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.ExistingFile = true
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("ExistingFile: want Accept")
		}
	})

	t.Run("nil Release accepts", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("nil Release: want Accept")
		}
	})

	t.Run("empty EpisodeIDs accepts", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.Release = &episodeimport.GrabbedReleaseInfo{Title: "x", EpisodeIDs: []int{}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("empty EpisodeIDs: want Accept")
		}
	})

	t.Run("all candidates expected accepts", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.Release = &episodeimport.GrabbedReleaseInfo{Title: "x", EpisodeIDs: []int{1, 2, 3}}
		le.Episodes = []episodes.Episode{{ID: 1}, {ID: 2}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("subset match: want Accept")
		}
	})

	t.Run("one unexpected episode rejects with singular form", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.mkv"
		le.Release = &episodeimport.GrabbedReleaseInfo{Title: "MyShow.S01.E01.mkv", EpisodeIDs: []int{1}}
		le.Episodes = []episodes.Episode{
			{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
			{ID: 2, SeasonNumber: 1, EpisodeNumber: 5},
		}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("unexpected episode: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionEpisodeNotFoundInRelease {
			t.Errorf("Reason = %v", d.Reason())
		}
		// "1x05" form expected, singular phrasing.
		if got := d.Message(); got == "" || !contains(got, "Episode 1x05") {
			t.Errorf("Message=%q (want singular Episode form)", got)
		}
	})

	t.Run("multiple unexpected episodes use plural form", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.Release = &episodeimport.GrabbedReleaseInfo{Title: "rel", EpisodeIDs: []int{1}}
		le.Episodes = []episodes.Episode{
			{ID: 2, SeasonNumber: 1, EpisodeNumber: 5},
			{ID: 3, SeasonNumber: 1, EpisodeNumber: 6},
		}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("multiple unexpected: want Reject")
		}
		if got := d.Message(); !contains(got, "Episodes 1x05, 1x06") {
			t.Errorf("Message=%q (want plural form)", got)
		}
	})
}

// --- NotUnpackingSpecification --------------------------------------

type stubUnpackingConfig struct {
	folders string
}

func (s stubUnpackingConfig) DownloadClientWorkingFolders() string { return s.folders }

type stubFileLastWrite struct {
	mtime time.Time
	err   error
}

func (s stubFileLastWrite) FileGetLastWrite(_ string) (time.Time, error) {
	return s.mtime, s.err
}

func TestNotUnpackingSpecification(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	mk := func(t *testing.T, cfg episodeimport.UnpackingConfig, disk episodeimport.FileLastWriteProvider, isWindows bool) *episodeimport.NotUnpackingSpecification {
		t.Helper()
		spec, err := episodeimport.NewNotUnpackingSpecification(cfg, disk, clock, nil)
		if err != nil {
			t.Fatalf("NewNotUnpackingSpecification: %v", err)
		}
		spec.SetWindowsForTest(isWindows)
		return spec
	}

	t.Run("nil deps rejected at construction", func(t *testing.T) {
		if _, err := episodeimport.NewNotUnpackingSpecification(nil, stubFileLastWrite{}, nil, nil); err == nil {
			t.Errorf("expected nil-cfg error")
		}
		if _, err := episodeimport.NewNotUnpackingSpecification(stubUnpackingConfig{}, nil, nil, nil); err == nil {
			t.Errorf("expected nil-disk error")
		}
	})

	t.Run("ExistingFile accepts", func(t *testing.T) {
		spec := mk(t, stubUnpackingConfig{folders: "_UNPACK_"}, stubFileLastWrite{}, false)
		le := episodeimport.NewLocalEpisode()
		le.ExistingFile = true
		le.Path = filepath.Join("/tv", "_UNPACK_x", "f.mkv")
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("ExistingFile: want Accept")
		}
	})

	t.Run("no prefix match accepts", func(t *testing.T) {
		spec := mk(t, stubUnpackingConfig{folders: "_UNPACK_|_FAILED_"}, stubFileLastWrite{}, false)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/normal/MyShow.S01E01.mkv"
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("no prefix match: want Accept")
		}
	})

	t.Run("non-windows + prefix match rejects immediately", func(t *testing.T) {
		spec := mk(t, stubUnpackingConfig{folders: "_UNPACK_|_FAILED_"}, stubFileLastWrite{}, false)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/_UNPACK_show/foo.mkv"
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("non-windows prefix match: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionUnpacking {
			t.Errorf("Reason = %v", d.Reason())
		}
	})

	t.Run("windows + recent mtime rejects", func(t *testing.T) {
		recent := now.Add(-2 * time.Minute)
		spec := mk(t, stubUnpackingConfig{folders: "_UNPACK_"}, stubFileLastWrite{mtime: recent}, true)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/_UNPACK_show/foo.mkv"
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("windows recent mtime: want Reject")
		}
	})

	t.Run("windows + old mtime accepts", func(t *testing.T) {
		old := now.Add(-1 * time.Hour)
		spec := mk(t, stubUnpackingConfig{folders: "_UNPACK_"}, stubFileLastWrite{mtime: old}, true)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/_UNPACK_show/foo.mkv"
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("windows old mtime: want Accept")
		}
	})

	t.Run("windows + stat error rejects", func(t *testing.T) {
		spec := mk(t, stubUnpackingConfig{folders: "_UNPACK_"}, stubFileLastWrite{err: errors.New("missing")}, true)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/_UNPACK_show/foo.mkv"
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("stat error on windows: want Reject")
		}
	})

	if got := mk(t, stubUnpackingConfig{}, stubFileLastWrite{}, false).Name(); got != "NotUnpackingSpecification" {
		t.Errorf("Name() = %q", got)
	}
}

// --- UpgradeSpecification ------------------------------------------

type stubUpgradeConfig struct {
	mode quality.ProperDownloadType
}

func (s stubUpgradeConfig) DownloadPropersAndRepacks() quality.ProperDownloadType { return s.mode }

type stubProfileResolver struct {
	view *decisionengine.ProfileView
	err  error
}

func (s *stubProfileResolver) GetProfileViewBySeriesID(_ context.Context, _ int) (*decisionengine.ProfileView, error) {
	return s.view, s.err
}

type stubFileResolver struct {
	files map[int]*episodefiles.File
	err   error
}

func (s *stubFileResolver) GetByID(_ context.Context, id int) (*episodefiles.File, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.files[id], nil
}

type stubFormatCalculator struct {
	out []customformats.CustomFormat
	err error
}

func (s *stubFormatCalculator) ParseCustomFormat(_ context.Context, _ *episodefiles.File) ([]customformats.CustomFormat, error) {
	return s.out, s.err
}

// makeProfileView builds a ProfileView with quality items so the
// quality-comparator returns sensible orderings. Two qualities:
// HDTV-720p (id=4), WEBDL-1080p (id=8) — both Allowed; WEBDL is
// "higher" because it's later in the list.
func makeProfileView(t *testing.T) *decisionengine.ProfileView {
	t.Helper()
	hd, _ := quality.FindByID(4)
	wd, _ := quality.FindByID(8)
	prof := &qualityprofiles.Profile{
		Name:           "test",
		UpgradeAllowed: true,
		Cutoff:         8,
	}
	view := &decisionengine.ProfileView{
		Profile: prof,
		Items: []qualityprofiles.QualityItem{
			{Quality: hd, Allowed: true},
			{Quality: wd, Allowed: true},
		},
		FormatItems: nil,
	}
	return view
}

func TestUpgradeSpecification(t *testing.T) {
	t.Parallel()

	view := makeProfileView(t)
	mk := func(t *testing.T, cfg episodeimport.UpgradeConfig, pr episodeimport.UpgradeQualityProfileResolver, fr episodeimport.EpisodeFileResolver, fc episodeimport.CustomFormatCalculator) *episodeimport.UpgradeSpecification {
		t.Helper()
		spec, err := episodeimport.NewUpgradeSpecification(cfg, pr, fr, fc, nil)
		if err != nil {
			t.Fatalf("NewUpgradeSpecification: %v", err)
		}
		return spec
	}

	t.Run("nil deps rejected at construction", func(t *testing.T) {
		good := stubUpgradeConfig{}
		gpr := &stubProfileResolver{view: view}
		gfr := &stubFileResolver{}
		gfc := &stubFormatCalculator{}
		if _, err := episodeimport.NewUpgradeSpecification(nil, gpr, gfr, gfc, nil); err == nil {
			t.Errorf("nil cfg")
		}
		if _, err := episodeimport.NewUpgradeSpecification(good, nil, gfr, gfc, nil); err == nil {
			t.Errorf("nil profileResolver")
		}
		if _, err := episodeimport.NewUpgradeSpecification(good, gpr, nil, gfc, nil); err == nil {
			t.Errorf("nil fileResolver")
		}
		if _, err := episodeimport.NewUpgradeSpecification(good, gpr, gfr, nil, nil); err == nil {
			t.Errorf("nil formatCalculator")
		}
	})

	t.Run("no episodes accepts", func(t *testing.T) {
		spec := mk(t,
			stubUpgradeConfig{mode: quality.ProperPreferAndUpgrade},
			&stubProfileResolver{view: view},
			&stubFileResolver{},
			&stubFormatCalculator{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("no episodes: want Accept")
		}
	})

	t.Run("episode without file accepts", func(t *testing.T) {
		spec := mk(t,
			stubUpgradeConfig{mode: quality.ProperPreferAndUpgrade},
			&stubProfileResolver{view: view},
			&stubFileResolver{},
			&stubFormatCalculator{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		le.Episodes = []episodes.Episode{{ID: 1}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("episode w/o file: want Accept")
		}
	})

	t.Run("profile resolver error accepts defensively", func(t *testing.T) {
		spec := mk(t,
			stubUpgradeConfig{},
			&stubProfileResolver{err: errors.New("boom")},
			&stubFileResolver{},
			&stubFormatCalculator{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(7)}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("profile err: want Accept (defensive)")
		}
	})

	t.Run("candidate quality < existing rejects NotQualityUpgrade", func(t *testing.T) {
		// Existing file: WEBDL-1080p; candidate: HDTV-720p
		fr := &stubFileResolver{files: map[int]*episodefiles.File{
			7: {ID: 7, Quality: `{"quality":8,"revision":{"version":1}}`},
		}}
		spec := mk(t,
			stubUpgradeConfig{},
			&stubProfileResolver{view: view},
			fr,
			&stubFormatCalculator{})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		hd, _ := quality.FindByID(4)
		le.Quality = quality.NewModel(hd)
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(7)}}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("downgrade quality: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionNotQualityUpgrade {
			t.Errorf("Reason = %v", d.Reason())
		}
	})

	t.Run("same quality + propers ON + new revision < existing rejects NotRevisionUpgrade", func(t *testing.T) {
		fr := &stubFileResolver{files: map[int]*episodefiles.File{
			7: {ID: 7, Quality: `{"quality":8,"revision":{"version":2}}`},
		}}
		spec := mk(t,
			stubUpgradeConfig{mode: quality.ProperPreferAndUpgrade},
			&stubProfileResolver{view: view},
			fr,
			&stubFormatCalculator{})
		wd, _ := quality.FindByID(8)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		le.Quality = quality.NewModelWithRevision(wd, quality.Revision{Version: 1})
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(7)}}
		d := spec.IsSatisfiedBy(le, nil)
		if d.Accepted() {
			t.Errorf("revision downgrade: want Reject")
		}
		if d.Reason() != episodeimport.ImportRejectionNotRevisionUpgrade {
			t.Errorf("Reason = %v", d.Reason())
		}
	})

	t.Run("same quality + propers DoNotPrefer skips revision check", func(t *testing.T) {
		fr := &stubFileResolver{files: map[int]*episodefiles.File{
			7: {ID: 7, Quality: `{"quality":8,"revision":{"version":2}}`},
		}}
		spec := mk(t,
			stubUpgradeConfig{mode: quality.ProperDoNotPrefer},
			&stubProfileResolver{view: view},
			fr,
			&stubFormatCalculator{})
		wd, _ := quality.FindByID(8)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		le.Quality = quality.NewModelWithRevision(wd, quality.Revision{Version: 1})
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(7)}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("DoNotPrefer + worse revision: want Accept")
		}
	})

	t.Run("upgrade quality accepts", func(t *testing.T) {
		// Existing HDTV-720p (id=4); candidate WEBDL-1080p (id=8) → upgrade.
		fr := &stubFileResolver{files: map[int]*episodefiles.File{
			7: {ID: 7, Quality: `{"quality":4,"revision":{"version":1}}`},
		}}
		spec := mk(t,
			stubUpgradeConfig{},
			&stubProfileResolver{view: view},
			fr,
			&stubFormatCalculator{})
		wd, _ := quality.FindByID(8)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{ID: 1}
		le.Quality = quality.NewModel(wd)
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(7)}}
		if !spec.IsSatisfiedBy(le, nil).Accepted() {
			t.Errorf("real upgrade: want Accept")
		}
	})

	if got := mk(t,
		stubUpgradeConfig{},
		&stubProfileResolver{view: view},
		&stubFileResolver{},
		&stubFormatCalculator{}).Name(); got != "UpgradeSpecification" {
		t.Errorf("Name() = %q", got)
	}
}

// contains is a minimal substring helper for table-driven message
// assertions; avoids pulling strings into the import block of tests
// that don't otherwise need it.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
