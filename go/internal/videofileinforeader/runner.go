// Package videofileinforeader is the Go port of
// `NzbDrone.Core.MediaFiles.MediaInfo.VideoFileInfoReader` — the
// ffprobe-backed media-info extractor that satisfies
// [mediainfoupdater.MediaInfoReader]. Closes queue [13] sub-arc 13c.
//
// Lives in its own package because the C# tree co-locates it under
// MediaInfo/ but the Go-port has [mediainfo] occupied by the
// formatter+model surface (which already imports
// [datastore]/[parser]); placing the reader here keeps the dep
// direction reader→mediainfo intact and matches the parallel
// placement used for [mediainfoupdater] (queue [13] sub-arcs 13a/13b).
//
// The C# tree binds FFMpegCore (managed wrapper around ffprobe).
// The Go-port shells out directly via os/exec — no CGO, no
// vendored ffprobe — and parses the standard ffprobe `-print_format
// json -show_format -show_streams` output. Production wiring relies
// on ffprobe being on PATH; missing-binary surfaces as an exec error
// which the Reader's GetMediaInfo treats as nil-result (matches the
// C# catch-all-Exception → log-and-return-null at
// `VideoFileInfoReader.cs:165-170`).
package videofileinforeader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner narrows the os/exec ffprobe invocation to the one method
// the Reader reaches for. Defined as an interface so tests can stub
// the exec layer with canned JSON bytes (the spike toolchain does
// not have ffprobe installed; the unit-test layer covers the
// JSON-parsing surface, the heavy probe is exercised at integration).
//
// args excludes the binary itself; the implementation prepends
// "ffprobe". stdout is the raw JSON payload; the Reader will
// json.Unmarshal it. Non-zero exit codes / binary-not-found / IO
// errors all propagate as a typed error — Reader.GetMediaInfo
// catches and returns (nil, nil) per the C# behaviour at `:165`.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner is the production [Runner] — shells out to the
// `ffprobe` binary on PATH. The binary name matches the C#-bundled
// FFMpegCore expectation; users supplying a custom path can wrap
// this type with their own Runner.
//
// Goroutine-safe.
type ExecRunner struct {
	// Binary is the ffprobe binary path. Empty defaults to "ffprobe"
	// (resolved via PATH). Override at construction for non-default
	// installs (e.g. the Sonarr-bundled ffprobe under
	// AppDomain.CurrentDomain.BaseDirectory in C#).
	Binary string
}

// NewExecRunner returns a Runner that invokes ffprobe via os/exec.
// Empty binary path resolves "ffprobe" via PATH at run time.
func NewExecRunner(binary string) *ExecRunner {
	return &ExecRunner{Binary: binary}
}

// Run executes ffprobe with the supplied args and returns stdout.
// stderr is included in the error message on non-zero exit so the
// "Unable to parse media info" log line carries the underlying
// ffprobe diagnostic (mirrors the C# logger.Error(ex, ...) path).
func (r *ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if r == nil {
		return nil, errors.New("videofileinforeader: ExecRunner.Run on nil receiver")
	}
	binary := r.Binary
	if binary == "" {
		binary = "ffprobe"
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("videofileinforeader: %s %v: %w (stderr=%q)", binary, args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}
