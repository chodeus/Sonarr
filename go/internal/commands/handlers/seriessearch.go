// SeriesSearch handler. Ports the IExecute<SeriesSearchCommand>
// implementation at `src/NzbDrone.Core/IndexerSearch/SeriesSearchService.cs:33-75`.
//
// Slice 5.J215.
//
// Two branches mirroring C# `:40-72`:
//
//   - NO seasons monitored → per-episode search:
//     load all episodes via GetEpisodesBySeries, filter to
//     (Monitored && !HasFile && AirDateUtc != nil && AirDateUtc <
//     now), dispatch via per-episode EpisodeSearcher.
//   - At least one season monitored → per-season search:
//     iterate Seasons in SeasonNumber ASC order, skip unmonitored,
//     dispatch via SeasonSearcher.
//
// Per-episode and per-season search failures are warn-logged and
// counted; Execute itself returns nil so the dispatcher doesn't
// classify the run as failed.
//
// Scope deferrals (documented divergence vs C#):
//
//   - C# loads `series.QualityProfile.Value` to compute
//     `missingOnly: !profile.UpgradeAllowed` at `:38, :68`. The Go
//     port hard-codes `missingOnly=false` (the most-permissive setting,
//     matching the C# default UpgradeAllowed=true profile). Wiring the
//     profile lookup is a follow-up — only changes whether existing-
//     with-files episodes participate in the search (i.e. are
//     re-evaluated for quality upgrades).
//   - C# tracks "downloaded count" via
//     `_processDownloadDecisions.ProcessDecisions(decisions)
//     .Grabbed.Count` at `:54-55, :69-70`. The Go port counts decisions
//     (matches J39 MissingEpisodeSearch). The grab pipeline is wired
//     elsewhere in the search service; the count is for the
//     ProgressInfo log line only.
//   - C# CommandTrigger.Manual gate at `:37` keys the
//     `userInvokedSearch` flag passed to EpisodeSearch / SeasonSearch.
//     The Go port currently doesn't surface UserInvokedSearch through
//     SearchSettings — defaults to false. Affects only the search-
//     telemetry tagging C# uses.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesSearchName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors C# short form
// `SeriesSearchCommand` → `SeriesSearch`. NOT a scheduled task —
// invoked only via the API or by SeriesScannedHandler (post-add).
const SeriesSearchName = "SeriesSearch"

// SeriesSearchBody is the JSON shape of the SeriesSearchCommand body.
// Mirrors C# `IndexerSearch/SeriesSearchCommand.cs:5-19` —
// SeriesId is required. Missing → handler errors via NotFound semantics.
type SeriesSearchBody struct {
	SeriesID int `json:"seriesId"`
}

// SeriesSearchLookup narrows [series.Service] to the by-id accessor
// the handler uses. `*series.Service` satisfies via GetSeries.
type SeriesSearchLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// SeriesSearchEpisodesLister narrows [episodes.Service] to the
// per-series enumerator. `*episodes.Service` satisfies via
// GetEpisodesBySeries.
type SeriesSearchEpisodesLister interface {
	GetEpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)
}

// SeasonSearcher narrows [*indexersearch.Service] to the per-season
// search the handler dispatches. The adapter at the host-wiring layer
// wraps SeasonSearch's []decisionengine.Decision shape into the
// decisions-count this interface returns.
type SeasonSearcher interface {
	SeasonSearch(ctx context.Context, seriesID, seasonNumber int, missingOnly bool) (int, error)
}

// SeriesSearch is the IExecute<SeriesSearchCommand> port. Each dep is
// optional — when any of {SeriesLookup, EpisodesLister, EpisodeSearcher,
// SeasonSearcher} is nil the handler logs the stub line + returns nil
// so hosts without a wired indexersearch.Service still register the
// command slot (the API surface returns success for the dispatcher
// rather than 404 "Unknown command").
type SeriesSearch struct {
	seriesLookup   SeriesSearchLookup
	episodesLister SeriesSearchEpisodesLister
	episodeSearch  EpisodeSearcher // J39's per-episode adapter; reused
	seasonSearch   SeasonSearcher
	clock          func() time.Time
	log            *slog.Logger
}

// NewSeriesSearch builds the handler. Deps optional; nil deps fall
// back to the stub-log behaviour matching J39's MissingEpisodeSearch
// pattern. log defaults to slog.Default.
func NewSeriesSearch(
	seriesLookup SeriesSearchLookup,
	episodesLister SeriesSearchEpisodesLister,
	episodeSearch EpisodeSearcher,
	seasonSearch SeasonSearcher,
	log *slog.Logger,
) *SeriesSearch {
	if log == nil {
		log = slog.Default()
	}
	return &SeriesSearch{
		seriesLookup:   seriesLookup,
		episodesLister: episodesLister,
		episodeSearch:  episodeSearch,
		seasonSearch:   seasonSearch,
		clock:          func() time.Time { return time.Now().UTC() },
		log:            log,
	}
}

// SetClock overrides the now() source for tests so the AirDateUtc <
// now filter on the per-episode branch is deterministic.
func (h *SeriesSearch) SetClock(c func() time.Time) {
	if c != nil {
		h.clock = c
	}
}

// Execute parses the body, loads the series, dispatches either
// per-episode or per-season search based on whether any season is
// monitored, and logs a one-line summary.
func (h *SeriesSearch) Execute(ctx context.Context, body string) error {
	if h.seriesLookup == nil || h.episodesLister == nil || h.episodeSearch == nil || h.seasonSearch == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"seriessearch: stub Execute (one or more deps not wired)",
			slog.String("command", SeriesSearchName))
		return nil
	}

	if body == "" {
		return errors.New("seriessearch: empty body (SeriesId required)")
	}
	var parsed SeriesSearchBody
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return fmt.Errorf("seriessearch: malformed body: %w", err)
	}
	if parsed.SeriesID <= 0 {
		return errors.New("seriessearch: SeriesId required and must be > 0")
	}

	ser, err := h.seriesLookup.GetSeries(ctx, parsed.SeriesID)
	if err != nil {
		return fmt.Errorf("seriessearch: get series %d: %w", parsed.SeriesID, err)
	}
	if ser == nil {
		return fmt.Errorf("seriessearch: series %d not found", parsed.SeriesID)
	}

	seasons, err := decodeSeriesSearchSeasons(ser.Seasons)
	if err != nil {
		// Decode failure → treat as no-seasons (fall through to the
		// per-episode branch). Logged as warn so the operator notices.
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"seriessearch: Seasons decode failed — falling back to per-episode branch",
			slog.Int("seriesId", ser.ID),
			slog.Any("err", err))
		seasons = nil
	}

	if !anyMonitoredSeason(seasons) {
		return h.runPerEpisode(ctx, ser)
	}
	return h.runPerSeason(ctx, ser, seasons)
}

// runPerEpisode ports C# `:42-56` — no monitored seasons → load all
// episodes, filter monitored + missing + aired, dispatch per-episode
// search.
func (h *SeriesSearch) runPerEpisode(ctx context.Context, ser *series.Series) error {
	h.log.LogAttrs(ctx, slog.LevelDebug,
		"seriessearch: no seasons monitored — per-episode mode",
		slog.Int("seriesId", ser.ID),
		slog.String("title", ser.Title))
	eps, err := h.episodesLister.GetEpisodesBySeries(ctx, ser.ID)
	if err != nil {
		return fmt.Errorf("seriessearch: load episodes for series %d: %w", ser.ID, err)
	}
	now := h.clock()
	var searched, decisions, failures int
	for _, ep := range eps {
		if !ep.Monitored {
			continue
		}
		// HasFile equivalent — see episoderefreshed.hasFile + episode-monitored hasFile.
		if ep.EpisodeFileID != nil && *ep.EpisodeFileID > 0 {
			continue
		}
		if ep.AirDateUtc == nil {
			continue
		}
		if !ep.AirDateUtc.Before(now) {
			continue
		}
		n, err := h.episodeSearch.EpisodeSearch(ctx, ep.ID)
		searched++
		if err != nil {
			failures++
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"seriessearch: per-episode search failed",
				slog.Int("episodeId", ep.ID),
				slog.Any("err", err))
			continue
		}
		decisions += n
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"seriessearch: per-episode complete",
		slog.Int("seriesId", ser.ID),
		slog.Int("searched", searched),
		slog.Int("decisions", decisions),
		slog.Int("failures", failures))
	return nil
}

// runPerSeason ports C# `:58-72` — at least one season monitored →
// iterate seasons in SeasonNumber ASC order, skip unmonitored,
// dispatch per-season search.
func (h *SeriesSearch) runPerSeason(ctx context.Context, ser *series.Series, seasons []seriesSearchSeason) error {
	sort.Slice(seasons, func(i, j int) bool {
		return seasons[i].SeasonNumber < seasons[j].SeasonNumber
	})
	var searched, decisions, failures, skipped int
	for _, s := range seasons {
		if !s.Monitored {
			h.log.LogAttrs(ctx, slog.LevelDebug,
				"seriessearch: season unmonitored — skipping",
				slog.Int("seriesId", ser.ID),
				slog.Int("seasonNumber", s.SeasonNumber))
			skipped++
			continue
		}
		// missingOnly hard-coded false — see package doc-comment for
		// the deferred quality-profile lookup divergence.
		n, err := h.seasonSearch.SeasonSearch(ctx, ser.ID, s.SeasonNumber, false)
		searched++
		if err != nil {
			failures++
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"seriessearch: per-season search failed",
				slog.Int("seriesId", ser.ID),
				slog.Int("seasonNumber", s.SeasonNumber),
				slog.Any("err", err))
			continue
		}
		decisions += n
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"seriessearch: per-season complete",
		slog.Int("seriesId", ser.ID),
		slog.Int("searched", searched),
		slog.Int("skipped", skipped),
		slog.Int("decisions", decisions),
		slog.Int("failures", failures))
	return nil
}

// seriesSearchSeason is the minimal field-shape needed from the
// series Seasons JSON blob. Duplicates the shape used by
// refreshepisodes/episodemonitored — keeps this handler loosely
// coupled to series internals.
type seriesSearchSeason struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

func decodeSeriesSearchSeasons(raw string) ([]seriesSearchSeason, error) {
	if raw == "" {
		return nil, nil
	}
	var out []seriesSearchSeason
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func anyMonitoredSeason(rows []seriesSearchSeason) bool {
	for _, r := range rows {
		if r.Monitored {
			return true
		}
	}
	return false
}
