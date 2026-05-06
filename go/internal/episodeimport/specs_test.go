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
