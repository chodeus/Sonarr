// Boot-time stub implementations of the mediafiles.Service narrow
// interfaces whose real ports haven't landed yet. Each one returns
// values that make the full Scan dispatch path traceable end-to-end
// while keeping disk-touching behaviour disabled until the real
// subsystem ports.
//
// Replace each stub by removing it from this file and binding the
// real production type at the cmd/sonarr boot site. The
// `mediafiles.NewService` constructor rejects nil deps so a missed
// rewire surfaces loudly at startup, not on first scan.

package main

import (
	"context"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/series"
)

// Slice 3.AD dropped the stubRootFolderResolver: the boot site now
// wires the real [rootFolderHealthResolverAdapter] (defined in
// healthcheck_wiring.go) into mediafiles.NewService, satisfying
// [mediafiles.RootFolderResolver] via Go's structural typing on the
// shared GetBestRootFolderPath(ctx, path) (string, error) signature.

// stubImportDecisionMaker returns no decisions. Mirrors the C#
// `MakeImportDecision.GetImportDecisions` behaviour when no candidate
// files match — Import receives an empty list and no-ops. Replace
// when the EpisodeImport subsystem ports.
type stubImportDecisionMaker struct{}

func (stubImportDecisionMaker) GetImportDecisions(_ context.Context, _ []string, _ *series.Series, _ bool) ([]any, error) {
	return nil, nil
}

// stubImportApprovedEpisodes accepts any decision list as a no-op.
// Replace when the EpisodeImport subsystem ports.
type stubImportApprovedEpisodes struct{}

func (stubImportApprovedEpisodes) Import(_ context.Context, _ []any, _ bool) error {
	return nil
}

// stubUpdateMediaInfo returns false-without-error so the size-update
// loop falls back to the bulk EpisodeFileService.UpdateMany path.
// Replace when the MediaInfo-update subsystem ports.
type stubUpdateMediaInfo struct{}

func (stubUpdateMediaInfo) Update(_ context.Context, _ *episodefiles.File, _ *series.Series) (bool, error) {
	return false, nil
}

// Compile-time interface satisfaction guards. If a future deps.go
// edit changes a method signature, the build error surfaces here at
// the boot site rather than as a `*mediafiles.Service` constructor
// failure at runtime.
var (
	_ mediafiles.ImportDecisionMaker    = stubImportDecisionMaker{}
	_ mediafiles.ImportApprovedEpisodes = stubImportApprovedEpisodes{}
	_ mediafiles.UpdateMediaInfo        = stubUpdateMediaInfo{}
)
