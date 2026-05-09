// Boot-time adapters for parsingservice-package narrow interfaces.

package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/scenemappings"
)

// sceneMappingFinderAdapter bridges *scenemappings.Service to
// [parsingservice.SceneMappingFinder]. The narrow interface is
// ctx-less and errorless (the C# call sites at
// `Parser/ParsingService.cs:178,579` invoke a service whose
// underlying impl never throws on a no-match — it returns null).
// The Go-port *scenemappings.Service adds (ctx, error) — the
// adapter swallows ctx via [context.Background] and logs+nil on
// any error.
type sceneMappingFinderAdapter struct {
	svc *scenemappings.Service
	log *slog.Logger
}

// FindSceneMapping mirrors the narrow interface; returns nil on
// any lookup failure.
func (a sceneMappingFinderAdapter) FindSceneMapping(seriesTitle, releaseTitle string, seasonNumber int) *scenemappings.Mapping {
	if a.svc == nil {
		return nil
	}
	m, err := a.svc.FindSceneMapping(context.Background(), seriesTitle, releaseTitle, seasonNumber)
	if err != nil {
		a.log.Warn("scenemappings: FindSceneMapping failed; treating as no match",
			"seriesTitle", seriesTitle, "releaseTitle", releaseTitle,
			"seasonNumber", seasonNumber, "err", err)
		return nil
	}
	return m
}

// GetSceneSeasonNumber mirrors the narrow interface; returns nil
// on any lookup failure.
func (a sceneMappingFinderAdapter) GetSceneSeasonNumber(seriesTitle, releaseTitle string) *int {
	if a.svc == nil {
		return nil
	}
	n, err := a.svc.GetSceneSeasonNumber(context.Background(), seriesTitle, releaseTitle)
	if err != nil {
		a.log.Warn("scenemappings: GetSceneSeasonNumber failed; treating as no override",
			"seriesTitle", seriesTitle, "releaseTitle", releaseTitle, "err", err)
		return nil
	}
	return n
}

// Compile-time guard.
var _ parsingservice.SceneMappingFinder = sceneMappingFinderAdapter{}
