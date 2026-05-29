// EpisodeSearch + SeasonSearch handlers (G11). Port the
// IExecute<EpisodeSearchCommand> + IExecute<SeasonSearchCommand>
// implementations at `src/NzbDrone.Core/IndexerSearch/EpisodeSearchService.cs:110-119`
// and `SeasonSearchService.cs:23-31`.
//
// These are the per-episode and per-season MANUAL search commands the
// React UI fires (the "search" button on an episode row / a season) via
// POST /api/v3/command {"name":"EpisodeSearch","episodeIds":[...]} and
// {"name":"SeasonSearch","seriesId":N,"seasonNumber":M}. Distinct from
// MissingEpisodeSearch (all-missing sweep) and SeriesSearch (whole
// series). They reuse the already-wired EpisodeSearcher / SeasonSearcher
// adapters (which run the search AND feed decisions into the grabber,
// G1/J423), so a click actually downloads approved releases.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// EpisodeSearchName / SeasonSearchName are the registry keys +
// Commands.Name. Mirror the C# short forms.
const (
	EpisodeSearchName = "EpisodeSearch"
	SeasonSearchName  = "SeasonSearch"
)

// EpisodeSearch is the IExecute<EpisodeSearchCommand> port — searches
// each requested episode id and grabs approved releases.
type EpisodeSearch struct {
	searcher EpisodeSearcher
	log      *slog.Logger
}

// NewEpisodeSearch builds the handler. A nil searcher logs the stub
// line + returns nil (registry slot still satisfies "command exists").
func NewEpisodeSearch(searcher EpisodeSearcher, log *slog.Logger) *EpisodeSearch {
	if log == nil {
		log = slog.Default()
	}
	return &EpisodeSearch{searcher: searcher, log: log}
}

type episodeSearchBody struct {
	EpisodeIDs []int `json:"episodeIds"`
}

// Execute ports `EpisodeSearchService.Execute(EpisodeSearchCommand)`
// (`:110-119`): per episode id, search + grab. Per-episode failures are
// absorbed (warn-logged + counted) so one bad id doesn't fail the run.
func (h *EpisodeSearch) Execute(ctx context.Context, body string) error {
	if h.searcher == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo, "episodesearch: stub Execute (Searcher not wired)",
			slog.String("command", EpisodeSearchName))
		return nil
	}
	var parsed episodeSearchBody
	if body != "" {
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			return fmt.Errorf("episodesearch: parse body: %w", err)
		}
	}
	if len(parsed.EpisodeIDs) == 0 {
		h.log.LogAttrs(ctx, slog.LevelInfo, "episodesearch: no episodeIds in body",
			slog.String("command", EpisodeSearchName))
		return nil
	}
	var decisions, failures int
	for _, id := range parsed.EpisodeIDs {
		n, err := h.searcher.EpisodeSearch(ctx, id)
		if err != nil {
			failures++
			h.log.LogAttrs(ctx, slog.LevelWarn, "episodesearch: per-episode search failed",
				slog.Int("episodeId", id), slog.Any("err", err))
			continue
		}
		decisions += n
	}
	h.log.LogAttrs(ctx, slog.LevelInfo, "episodesearch: complete",
		slog.String("command", EpisodeSearchName),
		slog.Int("episodes", len(parsed.EpisodeIDs)),
		slog.Int("decisions", decisions),
		slog.Int("failures", failures))
	return nil
}

// SeasonSearch is the IExecute<SeasonSearchCommand> port — searches a
// whole season + grabs approved releases.
type SeasonSearch struct {
	searcher SeasonSearcher
	log      *slog.Logger
}

// NewSeasonSearch builds the handler. A nil searcher logs the stub
// line + returns nil.
func NewSeasonSearch(searcher SeasonSearcher, log *slog.Logger) *SeasonSearch {
	if log == nil {
		log = slog.Default()
	}
	return &SeasonSearch{searcher: searcher, log: log}
}

type seasonSearchBody struct {
	SeriesID     int `json:"seriesId"`
	SeasonNumber int `json:"seasonNumber"`
}

// Execute ports `SeasonSearchService.Execute(SeasonSearchCommand)`
// (`:23-31`): a single season search + grab. C# passes
// `missingOnly: false` (search the whole season, not just missing).
func (h *SeasonSearch) Execute(ctx context.Context, body string) error {
	if h.searcher == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo, "seasonsearch: stub Execute (Searcher not wired)",
			slog.String("command", SeasonSearchName))
		return nil
	}
	var parsed seasonSearchBody
	if body != "" {
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			return fmt.Errorf("seasonsearch: parse body: %w", err)
		}
	}
	if parsed.SeriesID <= 0 {
		h.log.LogAttrs(ctx, slog.LevelInfo, "seasonsearch: no seriesId in body",
			slog.String("command", SeasonSearchName))
		return nil
	}
	decisions, err := h.searcher.SeasonSearch(ctx, parsed.SeriesID, parsed.SeasonNumber, false)
	if err != nil {
		return fmt.Errorf("seasonsearch: series %d season %d: %w", parsed.SeriesID, parsed.SeasonNumber, err)
	}
	h.log.LogAttrs(ctx, slog.LevelInfo, "seasonsearch: complete",
		slog.String("command", SeasonSearchName),
		slog.Int("seriesId", parsed.SeriesID),
		slog.Int("seasonNumber", parsed.SeasonNumber),
		slog.Int("decisions", decisions))
	return nil
}
