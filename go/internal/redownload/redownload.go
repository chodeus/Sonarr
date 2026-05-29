// Package redownload ports the C# `RedownloadFailedDownloadService`
// (`src/NzbDrone.Core/Download/RedownloadFailedDownloadService.cs`) —
// the auto-redownload-on-failure arm of the download lifecycle. It is
// the `IHandle<DownloadFailedEvent>` that, once the failure arm
// (downloadprocessing.Service, J400) publishes a
// [trackeddownloads.DownloadFailedEvent], re-searches for a replacement
// release when auto-redownload is enabled.
//
// The C# handler pushes an `EpisodeSearchCommand` / `SeasonSearchCommand`
// onto the command queue (`RedownloadFailedDownloadService.cs:32-79`).
// The Go port dispatches via narrow [EpisodeSearcher] / [SeasonSearcher]
// hooks instead — the same indirection [episoderefreshed] uses (J209/
// J210), so the wiring slice can back them with either a command-queue
// pusher or a direct call into `*indexersearch.Service`. C# decorates
// Handle with `[EventHandleOrder(EventHandleOrder.Last)]` so the
// redownload runs after the blocklist + history handlers; the Go port
// preserves that by subscribing this handler after those in main.go.
//
// Decision parity with the C# handler:
//
//   - SkipRedownload set → skip (user explicitly marked failed).
//   - AutoRedownloadFailed disabled → skip.
//   - ReleaseSource==InteractiveSearch && interactive-redownload disabled
//     → skip.
//   - exactly one episode → episode search.
//   - the failed set == the whole season → season search.
//   - otherwise (a double-episode etc.) → episode search of the set.
package redownload

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// Config exposes the two auto-redownload toggles the handler reads.
// *config.Provider satisfies it (AutoRedownloadFailed +
// AutoRedownloadFailedFromInteractiveSearch).
type Config interface {
	AutoRedownloadFailed() bool
	AutoRedownloadFailedFromInteractiveSearch() bool
}

// EpisodeLookup loads the first failed episode (for its season number)
// and that season's episode set (to decide episode-vs-season search).
// *episodes.Service satisfies it.
type EpisodeLookup interface {
	GetEpisode(ctx context.Context, id int) (*episodes.Episode, error)
	GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error)
}

// EpisodeSearcher dispatches a search for a specific episode-id set.
// Mirrors C# `EpisodeSearchCommand(episodeIds)`.
type EpisodeSearcher interface {
	SearchEpisodes(ctx context.Context, episodeIDs []int) error
}

// SeasonSearcher dispatches a whole-season search. Mirrors C#
// `SeasonSearchCommand{SeriesId, SeasonNumber}`.
type SeasonSearcher interface {
	SearchSeason(ctx context.Context, seriesID, seasonNumber int) error
}

// Service is the DownloadFailedEvent handler.
type Service struct {
	cfg          Config
	episodes     EpisodeLookup
	epSearch     EpisodeSearcher
	seasonSearch SeasonSearcher
	log          *slog.Logger
}

// NewService binds the dependencies. All are required.
func NewService(cfg Config, eps EpisodeLookup, epSearch EpisodeSearcher, seasonSearch SeasonSearcher, log *slog.Logger) (*Service, error) {
	if cfg == nil || eps == nil || epSearch == nil || seasonSearch == nil {
		return nil, errors.New("redownload: nil config, episode lookup, or searcher")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg, episodes: eps, epSearch: epSearch, seasonSearch: seasonSearch, log: log}, nil
}

// Handle ports `RedownloadFailedDownloadService.Handle(DownloadFailedEvent)`.
func (s *Service) Handle(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
	if s == nil {
		return nil
	}
	if ev.SkipRedownload {
		s.log.Debug("redownload: skip redownloading requested by user", "downloadId", ev.DownloadID)
		return nil
	}
	if !s.cfg.AutoRedownloadFailed() {
		s.log.Debug("redownload: auto redownloading failed episodes is disabled")
		return nil
	}
	if ev.ReleaseSource == decisionengine.ReleaseSourceInteractiveSearch && !s.cfg.AutoRedownloadFailedFromInteractiveSearch() {
		s.log.Debug("redownload: auto redownloading failed episodes from interactive search is disabled")
		return nil
	}

	switch len(ev.EpisodeIDs) {
	case 0:
		// C# assumes a non-empty set (it dereferences EpisodeIds.First());
		// the Go port guards — nothing to re-search.
		s.log.Debug("redownload: failed download carried no episode ids; nothing to search", "downloadId", ev.DownloadID)
		return nil
	case 1:
		s.log.Debug("redownload: failed download only contains one episode, searching again", "downloadId", ev.DownloadID)
		return s.epSearch.SearchEpisodes(ctx, ev.EpisodeIDs)
	}

	first, err := s.episodes.GetEpisode(ctx, ev.EpisodeIDs[0])
	if err != nil {
		return err
	}
	episodesInSeason, err := s.episodes.GetEpisodesBySeason(ctx, ev.SeriesID, first.SeasonNumber)
	if err != nil {
		return err
	}
	if len(ev.EpisodeIDs) == len(episodesInSeason) {
		s.log.Debug("redownload: failed download was entire season, searching again",
			"seriesId", ev.SeriesID, "seasonNumber", first.SeasonNumber)
		return s.seasonSearch.SearchSeason(ctx, ev.SeriesID, first.SeasonNumber)
	}

	s.log.Debug("redownload: failed download contains multiple episodes, probably a double episode, searching again",
		"downloadId", ev.DownloadID)
	return s.epSearch.SearchEpisodes(ctx, ev.EpisodeIDs)
}
