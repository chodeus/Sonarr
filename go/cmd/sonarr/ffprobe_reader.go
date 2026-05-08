package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodeimport"
)

// ffprobeRuntimeReader is the production binding for the slice-3.84
// [episodeimport.VideoRuntimeReader] interface. Replaces the
// noopVideoRuntimeReader (in this same file's predecessor wiring) which
// returned [episodeimport.ErrRuntimeUnavailable] for every call —
// previously every non-special, non-.flv/.strm candidate file fell
// through the slice-3.87 NotSampleSpecification's runtime-bucket arm
// to Indeterminate, which the orchestrator's
// [episodeimport.ImportDecisionMaker] treats defensively as Accept.
// With the real reader bound, the runtime gate at C# `:213-217`
// finally fires for short-runtime samples.
//
// # Implementation
//
// The Go-port reader shells out to the standard `ffprobe` binary
// (FFmpeg's metadata probe) rather than embedding a Go-native MP4/MKV
// parser. ffprobe is shipped in every Sonarr Docker image AND is
// expected on PATH for every supported install target (Linux pkg
// repos, Homebrew, Windows installer's bundled ffmpeg, macOS).
//
// The C# tree uses `FFMpegCore` (a managed wrapper that itself
// invokes ffprobe under the hood at `VideoFileInfoReader.cs:38-46`);
// the Go port skips the wrapper layer because (a) FFMpegCore is .NET-
// only and (b) the wrapper's only value is JSON parsing which is
// cheaper to do directly with `os/exec` + a single
// strconv.ParseFloat than to drag in a third-party Go ffprobe
// wrapper.
//
// # ffprobe invocation
//
// Command:
//
//	ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 <path>
//
// ffprobe's output for a valid media file is the duration in seconds
// as a decimal (e.g. "1234.567000\n"). Empty output means ffprobe
// couldn't read a duration (usually a corrupt or non-media file) —
// the reader treats that as ErrRuntimeUnavailable rather than zero
// to keep the SampleDetector's "no runtime → Indeterminate" branch
// semantically distinct from "runtime IS zero" (which would itself
// be a Sample-by-runtime decision).
//
// # Binary discovery
//
// ffprobe path is resolved once at constructor time via
// [exec.LookPath]. A missing binary returns nil from the constructor —
// the caller (cmd/sonarr boot) falls back to the noopVideoRuntimeReader
// in the same file so the slice-3.87 Indeterminate cascade resumes.
// This matches the C# `if (!File.Exists(...))` defensive arm at the
// FFMpegCore wrapper boundary.
//
// # Per-call timeout
//
// Every GetRunTime invocation runs under a 10-second timeout. Mirrors
// the C# `_ffProbe.SetExecuteAsync(...)` default timeout — short
// enough that a hung ffprobe doesn't deadlock the orchestrator (a
// download-folder scan with N files would otherwise fan into N×timeout
// wall-clock), long enough that legitimate large-file probes complete
// (a 4K MKV's format=duration probe typically returns under 2s on a
// local SSD).
type ffprobeRuntimeReader struct {
	binary  string
	timeout time.Duration
	log     *slog.Logger
}

// ffprobeDefaultTimeout is the per-call wall-clock bound. Mirrors the
// C# FFMpegCore default at the time of writing.
const ffprobeDefaultTimeout = 10 * time.Second

// newFfprobeRuntimeReader resolves the ffprobe binary path via PATH
// lookup and returns a bound reader. A missing binary returns nil so
// the caller falls back to the noopVideoRuntimeReader. The error
// return is reserved for unexpected failures (none today; the
// LookPath miss is the documented "missing" signal).
//
// log is optional; nil falls back to [slog.Default].
func newFfprobeRuntimeReader(log *slog.Logger) (*ffprobeRuntimeReader, error) {
	if log == nil {
		log = slog.Default()
	}
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		// Documented missing-binary signal — caller treats nil reader
		// as "fall back to noopVideoRuntimeReader". The error is
		// returned for diagnostic logging at the boot site; the caller
		// shouldn't fail on it.
		return nil, fmt.Errorf("ffprobe binary not found on PATH: %w", err)
	}
	return &ffprobeRuntimeReader{
		binary:  path,
		timeout: ffprobeDefaultTimeout,
		log:     log,
	}, nil
}

// GetRunTime probes the supplied file via `ffprobe -show_entries format=duration`
// and returns the parsed duration. Errors propagate as
// [episodeimport.ErrRuntimeUnavailable] (canonical "no runtime"
// signal the SampleDetector treats as Indeterminate) wrapped with
// the underlying cause for diagnostic logging.
func (r *ffprobeRuntimeReader) GetRunTime(ctx context.Context, path string) (time.Duration, error) {
	if path == "" {
		return 0, fmt.Errorf("ffprobe: empty path: %w", episodeimport.ErrRuntimeUnavailable)
	}

	probeCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, r.binary,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	stdout, err := cmd.Output()
	if err != nil {
		// Distinguish ctx-cancellation (caller canceled) from probe
		// failure. Both translate to ErrRuntimeUnavailable from the
		// caller's perspective — the SampleDetector's runtime-bucket
		// arm only checks the sentinel — but the wrapped cause
		// surfaces in logs.
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			r.log.Warn("ffprobe: probe timed out",
				"path", path, "timeout", r.timeout)
			return 0, fmt.Errorf("ffprobe: timeout after %s: %w", r.timeout, episodeimport.ErrRuntimeUnavailable)
		}
		// exec.ExitError carries stderr; bare error is usually
		// ENOENT-on-the-input-file or permission denied. Log at debug
		// (not warn) since the C# behaviour silently returns null —
		// noisy warns would spam every malformed file mid-scan.
		r.log.Debug("ffprobe: probe failed",
			"path", path, "err", err)
		return 0, fmt.Errorf("ffprobe: probe failed: %w", episodeimport.ErrRuntimeUnavailable)
	}

	out := strings.TrimSpace(string(stdout))
	if out == "" {
		// Empty output = ffprobe ran but couldn't read a duration
		// (corrupt or non-media file). Mirrors the C# `info?.RunTime`
		// nil-coalesce path.
		return 0, fmt.Errorf("ffprobe: no duration: %w", episodeimport.ErrRuntimeUnavailable)
	}

	seconds, err := strconv.ParseFloat(out, 64)
	if err != nil {
		// Parse failure = ffprobe output shape changed (e.g. "N/A"
		// for a stream that ffprobe can't compute duration for).
		// Match C# null-coalesce path.
		r.log.Debug("ffprobe: parse duration",
			"path", path, "output", out, "err", err)
		return 0, fmt.Errorf("ffprobe: parse %q: %w", out, episodeimport.ErrRuntimeUnavailable)
	}
	if seconds < 0 {
		return 0, fmt.Errorf("ffprobe: negative duration %g: %w", seconds, episodeimport.ErrRuntimeUnavailable)
	}
	// Multiply by float64(time.Second) before converting to int64 to
	// preserve sub-second precision (ffprobe reports microsecond-level
	// durations for media containers that carry them).
	return time.Duration(seconds * float64(time.Second)), nil
}

// Compile-time interface satisfaction guard.
var _ episodeimport.VideoRuntimeReader = (*ffprobeRuntimeReader)(nil)
