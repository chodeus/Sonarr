package episodeimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// DetectSampleResult is the three-state outcome of the sample-detection
// pass. Mirrors `DetectSampleResult.cs:1-9`.
type DetectSampleResult int

// DetectSampleResult values. Numeric order is wire-stable so the v3
// API + frontend round-trip identically.
const (
	// DetectSampleIndeterminate — the extension/special pass
	// neither confirmed nor ruled out a sample, AND the runtime
	// reader couldn't supply a duration. Caller decides (the
	// orchestrator currently treats this as "not a sample" to
	// minimise false positives).
	DetectSampleIndeterminate DetectSampleResult = 0
	// DetectSampleSample — runtime falls below the per-episode-
	// length threshold, or runtime came back zero (corrupt file).
	DetectSampleSample DetectSampleResult = 1
	// DetectSampleNotSample — special, .flv/.strm extension, or
	// runtime exceeded the threshold.
	DetectSampleNotSample DetectSampleResult = 2
)

// String returns the C# enum-identifier form for log lines.
func (r DetectSampleResult) String() string {
	switch r {
	case DetectSampleIndeterminate:
		return "Indeterminate"
	case DetectSampleSample:
		return "Sample"
	case DetectSampleNotSample:
		return "NotSample"
	default:
		return fmt.Sprintf("DetectSampleResult(%d)", int(r))
	}
}

// VideoRuntimeReader is the slice of `IVideoFileInfoReader` the
// C# DetectSample reaches for at `DetectSample.cs:36`. One method —
// `GetRunTime(path)` returning the file's playback duration. The
// wider reader surface (resolution, codec, full MediaInfoModel) ports
// alongside the rest of MediaFiles/MediaInfo in a later slice.
//
// Pulled out as a narrow interface so DetectSample can test against
// a stub without dragging in the ffprobe binding. Errors propagate;
// the orchestrator side logs and treats as Indeterminate.
type VideoRuntimeReader interface {
	// GetRunTime returns the file's playback duration. Errors
	// surface verbatim (the C# version returns `TimeSpan?` and
	// treats null as a soft failure; Go port uses error so the
	// caller can distinguish "ffprobe missing" from "0-byte file").
	GetRunTime(ctx context.Context, path string) (time.Duration, error)
}

// SampleRuntimeRule pins the per-episode-length sample threshold.
// Mirrors the C# `GetMinimumAllowedRuntime` table at
// `DetectSample.cs:123-145`. Each entry says "if the expected
// per-episode runtime is at most MaxExpectedMinutes, the file must
// be at least MinFileSeconds long to NOT be a sample".
//
// Order matters — the lookup walks entries top-to-bottom returning
// the first matching MaxExpectedMinutes. The default-case entry has
// a sentinel MaxExpectedMinutes of math.MaxInt to act as the
// catch-all 60-minute bucket.
type SampleRuntimeRule struct {
	MaxExpectedMinutes int
	MinFileSeconds     int
}

// sampleRuntimeRules is the threshold table from C#
// `DetectSample.cs:123-145`. Order is load-bearing: the lookup
// walks top-to-bottom returning the first MaxExpectedMinutes match.
var sampleRuntimeRules = []SampleRuntimeRule{
	// Anime short — 15 seconds.
	{MaxExpectedMinutes: 3, MinFileSeconds: 15},
	// Webisodes — 90 seconds.
	{MaxExpectedMinutes: 10, MinFileSeconds: 90},
	// 30-minute episodes — 5 minutes.
	{MaxExpectedMinutes: 30, MinFileSeconds: 300},
	// 60-minute episodes — 10 minutes (catch-all).
	{MaxExpectedMinutes: 1<<31 - 1, MinFileSeconds: 600},
}

// DefaultSeriesRuntimeMinutes is the fallback per-episode runtime
// when neither the LocalEpisode's Episodes carry a runtime nor the
// Series itself does. Mirrors the C# fallback at `DetectSample.cs:71-72`.
const DefaultSeriesRuntimeMinutes = 45

// SampleDetector ports the C# `DetectSample` class at
// `MediaFiles/EpisodeImport/DetectSample.cs:16-146`. Exposes
// IsSample(LocalEpisode) returning a tri-state DetectSampleResult.
//
// Goroutine-safe (every method is read-only over the runtime reader
// + per-call inputs).
type SampleDetector struct {
	reader VideoRuntimeReader
	log    *slog.Logger
}

// NewSampleDetector binds a VideoRuntimeReader + logger into a
// SampleDetector. reader may be nil — IsSample falls through to
// Indeterminate when it would have queried runtime and produces a
// debug log explaining the skip. nil reader matches the C# tree's
// optional-IVideoFileInfoReader pattern in tests.
func NewSampleDetector(reader VideoRuntimeReader, log *slog.Logger) *SampleDetector {
	if log == nil {
		log = slog.Default()
	}
	return &SampleDetector{reader: reader, log: log}
}

// IsSample mirrors `DetectSample.cs:47-76` —
// the LocalEpisode-driven path, which also carries the MediaInfo
// blob (when AggregateMediaInfo ran upstream) so the runtime read
// can short-circuit the reader call.
//
// Decision tree:
//
//   - localEpisode.IsSpecial() → NotSample (specials skip the check
//     entirely; a "sample" of a special is rare enough to ignore).
//   - .flv or .strm extension → NotSample (these don't carry a
//     readable runtime so the C# version returns NotSample
//     defensively rather than always-Indeterminate).
//   - aggregate per-episode runtime from LocalEpisode.Episodes,
//     falling back to Series.Runtime, falling back to
//     [DefaultSeriesRuntimeMinutes] when both are zero (the C#
//     fallback at `:71-72`).
//   - if MediaInfo is set, use its RunTime; else if reader is
//     non-nil, ask it via GetRunTime; else log + Indeterminate.
//   - 0 runtime → Sample (C# `:107-111`: a 0-min file is corrupt).
//   - runtime < per-bucket minimum → Sample.
//   - otherwise → NotSample.
func (d *SampleDetector) IsSample(ctx context.Context, le *LocalEpisode) DetectSampleResult {
	if le == nil {
		return DetectSampleIndeterminate
	}

	if extResult := extensionSampleCheck(le.Path, le.IsSpecial(), d.log); extResult != DetectSampleIndeterminate {
		return extResult
	}

	expectedMinutes := expectedRuntimeMinutes(le)
	if expectedMinutes == 0 {
		d.log.Debug("episodeimport: series runtime is 0, defaulting to fallback",
			"path", le.Path, "fallback", DefaultSeriesRuntimeMinutes)
		expectedMinutes = DefaultSeriesRuntimeMinutes
	}

	// Resolve the file's actual runtime: prefer the cached MediaInfo
	// blob, then fall back to the runtime reader.
	if le.MediaInfo != nil {
		fileRuntime := time.Duration(le.MediaInfo.RunTime)
		return d.classify(le.Path, fileRuntime, expectedMinutes)
	}

	if d.reader == nil {
		d.log.Debug("episodeimport: no MediaInfo and no runtime reader; cannot classify sample",
			"path", le.Path)
		return DetectSampleIndeterminate
	}

	fileRuntime, err := d.reader.GetRunTime(ctx, le.Path)
	if err != nil {
		d.log.Error("episodeimport: failed to read runtime for sample check",
			"path", le.Path, "err", err)
		return DetectSampleIndeterminate
	}
	return d.classify(le.Path, fileRuntime, expectedMinutes)
}

// IsSamplePath is the path-based detection variant — used by the
// orchestrator's pre-aggregation pass at `ImportDecisionMaker.cs:216`
// where it filters non-sample files before deciding whether to set
// LocalEpisode.OtherVideoFiles=true. Mirrors `DetectSample.cs:27-45`.
//
// expectedRuntimeMinutes is the per-episode duration the caller
// derived from Series.Runtime; 0 falls back to
// [DefaultSeriesRuntimeMinutes].
func (d *SampleDetector) IsSamplePath(ctx context.Context, path string, isSpecial bool, expectedRuntimeMinutes int) DetectSampleResult {
	if extResult := extensionSampleCheck(path, isSpecial, d.log); extResult != DetectSampleIndeterminate {
		return extResult
	}
	if d.reader == nil {
		d.log.Debug("episodeimport: no runtime reader; cannot classify sample by path",
			"path", path)
		return DetectSampleIndeterminate
	}
	if expectedRuntimeMinutes == 0 {
		expectedRuntimeMinutes = DefaultSeriesRuntimeMinutes
	}
	fileRuntime, err := d.reader.GetRunTime(ctx, path)
	if err != nil {
		d.log.Error("episodeimport: failed to read runtime for sample check",
			"path", path, "err", err)
		return DetectSampleIndeterminate
	}
	return d.classify(path, fileRuntime, expectedRuntimeMinutes)
}

// classify is the runtime-vs-bucket compare at the bottom of
// `DetectSample.cs:103-121`. fileRuntime is the actual file duration;
// expectedMinutes is the per-episode duration expected by Series
// metadata.
func (d *SampleDetector) classify(path string, fileRuntime time.Duration, expectedMinutes int) DetectSampleResult {
	minSeconds := minimumAllowedRuntimeSeconds(expectedMinutes)
	if fileRuntime <= 0 {
		// A 0-min file is corrupt. C# `DetectSample.cs:107-111`
		// returns Sample to keep it out of the library.
		d.log.Error("episodeimport: file has 0 runtime, treating as sample (corrupt video?)",
			"path", path)
		return DetectSampleSample
	}
	if fileRuntime.Seconds() < float64(minSeconds) {
		d.log.Debug("episodeimport: file appears to be a sample",
			"path", path,
			"runtimeSeconds", fileRuntime.Seconds(),
			"minSeconds", minSeconds)
		return DetectSampleSample
	}
	d.log.Debug("episodeimport: file does not appear to be a sample",
		"path", path,
		"runtimeSeconds", fileRuntime.Seconds(),
		"minSeconds", minSeconds)
	return DetectSampleNotSample
}

// extensionSampleCheck mirrors C# `DetectSample.cs:78-101`. Returns
// NotSample for .flv/.strm files (no readable runtime, but commonly
// real episodes), NotSample for specials (skip the check entirely),
// Indeterminate otherwise (caller proceeds to the runtime read).
func extensionSampleCheck(path string, isSpecial bool, log *slog.Logger) DetectSampleResult {
	if isSpecial {
		log.Debug("episodeimport: special, skipping sample check", "path", path)
		return DetectSampleNotSample
	}
	ext := lowerExt(path)
	switch ext {
	case ".flv", ".strm":
		log.Debug("episodeimport: skipping sample check for special-extension file",
			"path", path, "ext", ext)
		return DetectSampleNotSample
	}
	return DetectSampleIndeterminate
}

// expectedRuntimeMinutes mirrors C# `DetectSample.cs:56-67` — sums
// per-episode runtimes, falling back to Series.Runtime when an
// individual episode's Runtime is zero.
//
// Returns 0 when the LocalEpisode has no episodes (the caller's
// fallback then applies). Series.Runtime alone (without an episodes
// list) is the C# behaviour for the Path-based variant; here we
// preserve the LocalEpisode-driven semantic where the per-episode
// loop is the source of truth.
func expectedRuntimeMinutes(le *LocalEpisode) int {
	var total int
	seriesRuntime := 0
	if le.Series != nil {
		seriesRuntime = le.Series.Runtime
	}
	for _, ep := range le.Episodes {
		if ep.Runtime > 0 {
			total += ep.Runtime
		} else {
			total += seriesRuntime
		}
	}
	return total
}

// minimumAllowedRuntimeSeconds walks [sampleRuntimeRules] returning
// the first MinFileSeconds whose MaxExpectedMinutes ≥ runtime.
// Mirrors C# `DetectSample.cs:123-145`.
func minimumAllowedRuntimeSeconds(runtimeMinutes int) int {
	for _, rule := range sampleRuntimeRules {
		if runtimeMinutes <= rule.MaxExpectedMinutes {
			return rule.MinFileSeconds
		}
	}
	// Unreachable — the catch-all rule has MaxInt MaxExpectedMinutes.
	return sampleRuntimeRules[len(sampleRuntimeRules)-1].MinFileSeconds
}

// lowerExt extracts the lower-cased file extension including the
// leading dot. Empty string for paths with no `.` after the last
// separator (or no extension after a leading dot like `.bashrc`).
//
// Mirrors `System.IO.Path.GetExtension(...).ToLowerInvariant()`. The
// helper is local to detect_sample.go because no other consumer
// needs it (parser.RemoveFileExtension is regex-driven and stricter).
func lowerExt(path string) string {
	if path == "" {
		return ""
	}
	// Strip directory prefix.
	if idx := strings.LastIndexAny(path, `\/`); idx >= 0 {
		path = path[idx+1:]
	}
	if dot := strings.LastIndex(path, "."); dot > 0 {
		return strings.ToLower(path[dot:])
	}
	return ""
}

// ErrRuntimeUnavailable is the canonical error a [VideoRuntimeReader]
// implementation should return when the underlying tool (ffprobe)
// is unavailable or didn't produce a duration. Callers `errors.Is`
// against this rather than string-matching the underlying error so
// IsSample's "log + Indeterminate" branch lands consistently
// regardless of which reader implementation is wired.
var ErrRuntimeUnavailable = errors.New("episodeimport: runtime unavailable")
