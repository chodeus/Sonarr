// Boot-time stub implementations of mediafiles.Service narrow
// interfaces whose real ports haven't landed yet.
//
// As of slice 3.AE only stubUpdateMediaInfo remains — the real
// MediaInfo updater is FAIL-CLOSED #8 territory (NEW subsystem).
// The slice-3.AD swap dropped stubRootFolderResolver in favour of
// rootFolderHealthResolverAdapter; the slice-3.AE swap dropped
// stubImportDecisionMaker + stubImportApprovedEpisodes in favour of
// the mediafilesImportDecisionAdapter +
// mediafilesImportApprovedAdapter (mediafiles_wiring.go) wrapping
// the real *episodeimport types.
//
// Replace this stub by removing it and binding the real
// MediaInfo-update type at the cmd/sonarr boot site once it ports.
// `mediafiles.NewService` rejects nil deps, so a missed rewire
// surfaces loudly at startup, not on first scan.

package main

import (
	"context"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/series"
)

// stubUpdateMediaInfo returns false-without-error so the size-update
// loop falls back to the bulk EpisodeFileService.UpdateMany path.
// Replace when the MediaInfo-update subsystem ports.
type stubUpdateMediaInfo struct{}

func (stubUpdateMediaInfo) Update(_ context.Context, _ *episodefiles.File, _ *series.Series) (bool, error) {
	return false, nil
}

// Compile-time interface satisfaction guard.
var _ mediafiles.UpdateMediaInfo = stubUpdateMediaInfo{}
