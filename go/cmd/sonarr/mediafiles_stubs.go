// Boot-time stub implementations of mediafiles.Service narrow
// interfaces whose real ports haven't landed yet.
//
// As of slice 3.BI the slice-3.AE stubUpdateMediaInfo dropped — the
// real *mediainfoupdater.UpdateService now satisfies
// mediafiles.UpdateMediaInfo. What remains is noopMediaInfoReader:
// the placeholder for the VideoFileInfoReader port deferred to
// sub-arc 13c (slice 3.BJ). The noop returns (nil, nil) so the
// UpdateService.UpdateMediaInfo nil-result short-circuit fires
// instead of stamping an actual MediaInfo blob — no behavioural
// change vs the prior stubUpdateMediaInfo while 13c remains
// deferred.
//
// Replace noopMediaInfoReader by removing it and binding the real
// VideoFileInfoReader at the cmd/sonarr boot site once 13c ports.
// `mediainfoupdater.NewUpdateService` rejects nil deps, so a missed
// rewire surfaces loudly at startup, not on first scan.

package main

import (
	"context"

	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/mediainfoupdater"
)

// noopMediaInfoReader satisfies [mediainfoupdater.MediaInfoReader]
// with a (nil, nil) return — the UpdateService's nil-result arm
// then short-circuits without touching DB or modifying file rows,
// preserving the slice-3.AE no-op behaviour. Production swap
// happens when sub-arc 13c lands the ffmpeg-backed reader.
type noopMediaInfoReader struct{}

func (noopMediaInfoReader) GetMediaInfo(_ context.Context, _ string) (*mediainfo.Model, error) {
	return nil, nil
}

// Compile-time interface satisfaction guard.
var _ mediainfoupdater.MediaInfoReader = noopMediaInfoReader{}
