// Boot-time wiring for the auto-redownload-on-failure handler
// (internal/redownload, J401). Provides the SeasonSearcher adapter; the
// EpisodeSearcher is satisfied by the existing
// episodeRefreshedSearcherAdapter (SearchEpisodes([]int)). The handler
// is constructed + subscribed to DownloadFailedEvent in main.go.
package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/indexersearch"
)

// redownloadSeasonSearchAdapter satisfies redownload.SeasonSearcher by
// dispatching a whole-season search through *indexersearch.Service.
// missingOnly=false: the failed download was the entire season, so
// re-search the full season for a replacement (mirrors the C#
// SeasonSearchCommand which doesn't restrict to missing). SearchSettings
// zero value matches the bulk-default posture the other search adapters
// use (no UserInvokedSearch override).
type redownloadSeasonSearchAdapter struct {
	svc     *indexersearch.Service
	grabber decisionGrabber
	log     *slog.Logger
}

func (a redownloadSeasonSearchAdapter) SearchSeason(ctx context.Context, seriesID, seasonNumber int) error {
	decisions, err := a.svc.SeasonSearch(ctx, seriesID, seasonNumber, false, indexersearch.SearchSettings{})
	if err != nil {
		a.log.LogAttrs(ctx, slog.LevelWarn, "redownload: season search failed",
			slog.Int("seriesId", seriesID), slog.Int("seasonNumber", seasonNumber), slog.Any("err", err))
		return err
	}
	grabSearchDecisions(ctx, a.grabber, decisions) // G1 (J423)
	return nil
}
