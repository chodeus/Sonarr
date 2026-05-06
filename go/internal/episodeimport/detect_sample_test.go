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
	"sonarr.tv/sonarr/internal/series"
)

// fakeRuntimeReader implements VideoRuntimeReader for tests. nil err
// returns the configured runtime; non-nil err propagates verbatim.
type fakeRuntimeReader struct {
	runtime time.Duration
	err     error
	calls   int
}

func (f *fakeRuntimeReader) GetRunTime(_ context.Context, _ string) (time.Duration, error) {
	f.calls++
	return f.runtime, f.err
}

func TestDetectSampleResult_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   episodeimport.DetectSampleResult
		want string
	}{
		{episodeimport.DetectSampleIndeterminate, "Indeterminate"},
		{episodeimport.DetectSampleSample, "Sample"},
		{episodeimport.DetectSampleNotSample, "NotSample"},
		{episodeimport.DetectSampleResult(99), "DetectSampleResult(99)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSampleDetector_NilLocalEpisode(t *testing.T) {
	t.Parallel()
	d := episodeimport.NewSampleDetector(nil, nil)
	if got := d.IsSample(t.Context(), nil); got != episodeimport.DetectSampleIndeterminate {
		t.Errorf("nil LocalEpisode = %v, want Indeterminate", got)
	}
}

func TestSampleDetector_SpecialSkipped(t *testing.T) {
	t.Parallel()
	reader := &fakeRuntimeReader{runtime: 1 * time.Second}
	d := episodeimport.NewSampleDetector(reader, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 60}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 0}} // special

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleNotSample {
		t.Errorf("special = %v, want NotSample", got)
	}
	if reader.calls != 0 {
		t.Errorf("special path called runtime reader %d times; want 0", reader.calls)
	}
}

func TestSampleDetector_ExtensionSkipped(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".flv", ".strm", ".FLV", ".StRm"} {
		reader := &fakeRuntimeReader{runtime: 1 * time.Second}
		d := episodeimport.NewSampleDetector(reader, nil)

		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/foo/foo" + ext
		le.Series = &series.Series{Runtime: 60}
		le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1}}

		if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleNotSample {
			t.Errorf("ext %s = %v, want NotSample", ext, got)
		}
		if reader.calls != 0 {
			t.Errorf("ext %s called runtime reader %d times; want 0 (extension short-circuit)", ext, reader.calls)
		}
	}
}

func TestSampleDetector_MediaInfoCachedRuntime(t *testing.T) {
	t.Parallel()
	// MediaInfo runtime is preferred over the runtime reader.
	reader := &fakeRuntimeReader{runtime: 1 * time.Second, err: errors.New("should-not-call")}
	d := episodeimport.NewSampleDetector(reader, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 60}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1, Runtime: 60}}
	le.MediaInfo = &mediainfo.Model{RunTime: datastore.STDuration(45 * time.Minute)}

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleNotSample {
		t.Errorf("MediaInfo cached runtime = %v, want NotSample", got)
	}
	if reader.calls != 0 {
		t.Errorf("reader was called %d times; MediaInfo should have short-circuited", reader.calls)
	}
}

func TestSampleDetector_RuntimeBuckets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		expected int           // per-episode minutes
		runtime  time.Duration // file actual
		want     episodeimport.DetectSampleResult
	}{
		// Anime short bucket — threshold 15s.
		{"anime short under threshold", 3, 10 * time.Second, episodeimport.DetectSampleSample},
		{"anime short at threshold", 3, 15 * time.Second, episodeimport.DetectSampleNotSample},
		// Webisode bucket — threshold 90s.
		{"webisode under threshold", 10, 60 * time.Second, episodeimport.DetectSampleSample},
		{"webisode at threshold", 10, 90 * time.Second, episodeimport.DetectSampleNotSample},
		// 30-minute bucket — threshold 5 min.
		{"30min sample", 30, 60 * time.Second, episodeimport.DetectSampleSample},
		{"30min real", 30, 6 * time.Minute, episodeimport.DetectSampleNotSample},
		// 60-minute bucket — threshold 10 min.
		{"60min sample", 60, 5 * time.Minute, episodeimport.DetectSampleSample},
		{"60min real", 60, 12 * time.Minute, episodeimport.DetectSampleNotSample},
		// Long-runtime bucket (catch-all 600s) — same threshold applies for >60min.
		{"120min real", 120, 30 * time.Minute, episodeimport.DetectSampleNotSample},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := &fakeRuntimeReader{runtime: c.runtime}
			d := episodeimport.NewSampleDetector(reader, nil)

			le := episodeimport.NewLocalEpisode()
			le.Path = "/tv/foo/foo.mkv"
			le.Series = &series.Series{Runtime: c.expected}
			le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1}}

			if got := d.IsSample(t.Context(), le); got != c.want {
				t.Errorf("got %v, want %v (runtime=%v expected=%dmin)", got, c.want, c.runtime, c.expected)
			}
		})
	}
}

func TestSampleDetector_ZeroRuntimeIsSample(t *testing.T) {
	t.Parallel()
	reader := &fakeRuntimeReader{runtime: 0}
	d := episodeimport.NewSampleDetector(reader, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 60}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1}}

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleSample {
		t.Errorf("0 runtime = %v, want Sample (corrupt-file defensive)", got)
	}
}

func TestSampleDetector_ReaderError(t *testing.T) {
	t.Parallel()
	reader := &fakeRuntimeReader{err: episodeimport.ErrRuntimeUnavailable}
	d := episodeimport.NewSampleDetector(reader, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 60}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1}}

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleIndeterminate {
		t.Errorf("reader error = %v, want Indeterminate", got)
	}
}

func TestSampleDetector_NoReaderNoMediaInfo(t *testing.T) {
	t.Parallel()
	d := episodeimport.NewSampleDetector(nil, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 60}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1}}

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleIndeterminate {
		t.Errorf("no reader + no MediaInfo = %v, want Indeterminate", got)
	}
}

func TestSampleDetector_PerEpisodeRuntimeOverridesSeries(t *testing.T) {
	t.Parallel()
	// Series.Runtime says 60min but Episode.Runtime says 30min — the
	// 30-min bucket (300s) governs. A 4-minute file should be classed
	// as Sample.
	reader := &fakeRuntimeReader{runtime: 4 * time.Minute}
	d := episodeimport.NewSampleDetector(reader, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 60}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1, Runtime: 30}}

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleSample {
		t.Errorf("got %v, want Sample (per-episode 30min bucket overrides series 60min)", got)
	}
}

func TestSampleDetector_FallbackRuntime(t *testing.T) {
	t.Parallel()
	// Both Episode.Runtime and Series.Runtime are 0 → falls back to
	// DefaultSeriesRuntimeMinutes=45 → 60-minute bucket → 600s
	// threshold. A 4-minute file is Sample.
	reader := &fakeRuntimeReader{runtime: 4 * time.Minute}
	d := episodeimport.NewSampleDetector(reader, nil)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.Series = &series.Series{Runtime: 0}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1, Runtime: 0}}

	if got := d.IsSample(t.Context(), le); got != episodeimport.DetectSampleSample {
		t.Errorf("fallback runtime = %v, want Sample", got)
	}
}

func TestSampleDetector_IsSamplePath(t *testing.T) {
	t.Parallel()
	// IsSamplePath skips MediaInfo entirely — exercises the
	// path-only variant the orchestrator uses pre-aggregation.
	cases := []struct {
		name     string
		path     string
		special  bool
		expected int
		runtime  time.Duration
		readErr  error
		want     episodeimport.DetectSampleResult
	}{
		{"special skip", "/tv/foo/foo.mkv", true, 60, 0, nil, episodeimport.DetectSampleNotSample},
		{"flv skip", "/tv/foo/foo.flv", false, 60, 0, nil, episodeimport.DetectSampleNotSample},
		{"sample by runtime", "/tv/foo/foo.mkv", false, 60, 5 * time.Minute, nil, episodeimport.DetectSampleSample},
		{"real", "/tv/foo/foo.mkv", false, 60, 30 * time.Minute, nil, episodeimport.DetectSampleNotSample},
		{"reader error → indeterminate", "/tv/foo/foo.mkv", false, 60, 0, episodeimport.ErrRuntimeUnavailable, episodeimport.DetectSampleIndeterminate},
		{"zero expected falls back", "/tv/foo/foo.mkv", false, 0, 30 * time.Minute, nil, episodeimport.DetectSampleNotSample},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := &fakeRuntimeReader{runtime: c.runtime, err: c.readErr}
			d := episodeimport.NewSampleDetector(reader, nil)
			if got := d.IsSamplePath(t.Context(), c.path, c.special, c.expected); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestSampleDetector_IsSamplePath_NoReader(t *testing.T) {
	t.Parallel()
	d := episodeimport.NewSampleDetector(nil, nil)
	if got := d.IsSamplePath(t.Context(), "/tv/foo.mkv", false, 60); got != episodeimport.DetectSampleIndeterminate {
		t.Errorf("nil reader = %v, want Indeterminate", got)
	}
}
