// Package episodemonitored ports the C# `EpisodeMonitoredService` at
// `src/NzbDrone.Core/Tv/EpisodeMonitoredService.cs`. Slice 5.J213 ships
// the modern code path:
//
//   - 11 MonitorType branches (All/Future/Missing/Existing/Pilot/
//     FirstSeason/LastSeason+LatestSeason/Recent/MonitorSpecials/
//     UnmonitorSpecials/None) — each toggles per-episode Monitored via
//     a predicate matching C# `:53-130`.
//   - Skip branch is a no-op (returns immediately, matches C# `:44-47`).
//   - nil MonitoringOptions just persists the series row matching C#
//     `:30-34` ("Update the series without changing the episodes").
//   - Season.Monitored cascade after the per-episode toggles (C#
//     `:132-169`): last season gets monitored when Monitor==All or
//     when Monitor==Future for a continuing/upcoming series; first
//     season gets unmonitored when Monitor==Pilot (the pilot-only
//     case); other seasons inherit "any monitored episode → season
//     monitored" semantics.
//
// Deferred relative to C# (own follow-up slice):
//
//   - `LegacySetEpisodeMonitoredStatus` at C# `:175-232`, the
//     Monitor==Unknown branch used by v2 API endpoints. The Go-port's
//     `series.AddOptions` doesn't expose the `IgnoreEpisodesWithFiles`
//     / `IgnoreEpisodesWithoutFiles` fields the legacy path keys on
//     (see addoptions.go doc-comment) — porting the legacy arm
//     requires extending the AddOptions decode shape first. J213
//     warn-logs and no-ops when Monitor==Unknown rather than silently
//     wrong-behaving.
package episodemonitored

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// MonitoringOptions ports C# `Tv/MonitoringOptions.cs`. Fields are a
// strict subset of the C# class — `IgnoreEpisodesWithFiles` +
// `IgnoreEpisodesWithoutFiles` exist on the C# side but feed only the
// deferred Legacy branch, so the Go shape omits them until that arm
// ports.
type MonitoringOptions struct {
	Monitor series.MonitorType
}

// EpisodeStore narrows [episodes.Service] to the read + bulk-update
// accessors [Service.SetEpisodeMonitoredStatus] uses.
// `*episodes.Service` satisfies via `GetEpisodesBySeries` +
// `UpdateMany`.
type EpisodeStore interface {
	GetEpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)
	UpdateMany(ctx context.Context, list []*episodes.Episode) error
}

// SeriesStore narrows [series.Service] to the row-persist accessor
// [Service.SetEpisodeMonitoredStatus] uses for the Seasons[]
// re-stamp. `*series.Service` satisfies via `UpdateSeries` (the
// `SkipPublishUpdatedEvent` knob mirrors C# `publishUpdatedEvent:
// false` at `:172,231`).
type SeriesStore interface {
	UpdateSeries(ctx context.Context, ns *series.Series, opts series.UpdateOptions) (*series.Series, error)
}

// Service ports `EpisodeMonitoredService`. Single entry point:
// [Service.SetEpisodeMonitoredStatus].
type Service struct {
	episodes EpisodeStore
	seriesS  SeriesStore
	now      func() time.Time
	log      *slog.Logger
}

// NewService binds the dependencies. `now` defaults to time.Now().UTC()
// when nil; pinning is for deterministic tests of the
// MonitorFuture / MonitorRecent windows.
func NewService(episodesStore EpisodeStore, seriesStore SeriesStore, now func() time.Time, log *slog.Logger) (*Service, error) {
	if episodesStore == nil {
		return nil, errors.New("episodemonitored: episodes store is nil")
	}
	if seriesStore == nil {
		return nil, errors.New("episodemonitored: series store is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{episodes: episodesStore, seriesS: seriesStore, now: now, log: log}, nil
}

// SetEpisodeMonitoredStatus ports C# `:27-173`. Three early exits:
//
//   - nil MonitoringOptions: persist the series row unchanged and
//     return (C# `:30-34`).
//   - Monitor==Unknown: legacy-path is deferred; warn-log + no-op
//     (returns nil). Documented divergence vs C# which calls
//     `LegacySetEpisodeMonitoredStatus`.
//   - Monitor==Skip: return immediately, do nothing (C# `:44-47`).
//
// Happy path: load all episodes for the series, dispatch on
// MonitorType to set per-episode Monitored, re-stamp
// series.Seasons[].Monitored, persist via UpdateMany + UpdateSeries.
//
// `SkipPublishUpdatedEvent` is set on the UpdateSeries call to match
// the C# `publishUpdatedEvent: false` posture — the caller decides
// whether the wider series-edited fan-out should fire.
//
// Slice 5.J213.
func (s *Service) SetEpisodeMonitoredStatus(ctx context.Context, ser *series.Series, opts *MonitoringOptions) error {
	if s == nil {
		return errors.New("episodemonitored: nil Service")
	}
	if ser == nil {
		return errors.New("episodemonitored: nil series")
	}

	if opts == nil {
		// C# :30-34 — persist the series without changing episodes.
		if _, err := s.seriesS.UpdateSeries(ctx, ser, series.UpdateOptions{SkipPublishUpdatedEvent: true}); err != nil {
			return fmt.Errorf("episodemonitored: UpdateSeries(%d): %w", ser.ID, err)
		}
		return nil
	}

	switch opts.Monitor {
	case series.MonitorUnknown:
		// Legacy path deferred — see package doc-comment.
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"episodemonitored: Monitor=Unknown — legacy path not ported; no-op",
			slog.Int("seriesId", ser.ID),
			slog.String("title", ser.Title))
		return nil
	case series.MonitorSkip:
		// C# :44-47 — no-op.
		return nil
	}

	seasons, err := decodeSeasons(ser.Seasons)
	if err != nil {
		// Decode failures fall back to empty list — matches C#
		// MinOrDefault/MaxOrDefault semantics returning 0 on empty input.
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"episodemonitored: Seasons decode failed — treating as empty",
			slog.Int("seriesId", ser.ID),
			slog.Any("err", err))
		seasons = nil
	}

	firstSeason := minPositiveSeason(seasons) // C# :49 — MinOrDefault on s>0
	lastSeason := maxSeason(seasons)          // C# :50 — MaxOrDefault

	eps, err := s.episodes.GetEpisodesBySeries(ctx, ser.ID)
	if err != nil {
		return fmt.Errorf("episodemonitored: GetEpisodesBySeries(%d): %w", ser.ID, err)
	}

	now := s.now().UTC()
	switch opts.Monitor {
	case series.MonitorMonitorSpecials:
		// C# :113-117. Season 0 only; non-special episodes untouched.
		for i := range eps {
			if eps[i].SeasonNumber == 0 {
				eps[i].Monitored = true
			}
		}
	case series.MonitorUnmonitorSpecials:
		// C# :119-123. Season 0 only.
		for i := range eps {
			if eps[i].SeasonNumber == 0 {
				eps[i].Monitored = false
			}
		}
	default:
		// Predicate selects which episodes should be Monitored=true.
		if pred := monitorPredicate(opts.Monitor, firstSeason, lastSeason, now); pred != nil {
			toggleMonitored(eps, pred)
		}
	}

	// Season cascade (C# :132-169).
	monitoredSeasons := monitoredSeasonsSet(eps)
	newSeasons := cascadeSeasons(seasons, monitoredSeasons, opts.Monitor, firstSeason, lastSeason, ser.Status)

	// Persist.
	encoded, err := encodeSeasons(newSeasons)
	if err != nil {
		return fmt.Errorf("episodemonitored: encode seasons: %w", err)
	}
	ser.Seasons = encoded

	epPtrs := make([]*episodes.Episode, len(eps))
	for i := range eps {
		epPtrs[i] = &eps[i]
	}
	if err := s.episodes.UpdateMany(ctx, epPtrs); err != nil {
		return fmt.Errorf("episodemonitored: UpdateMany: %w", err)
	}
	if _, err := s.seriesS.UpdateSeries(ctx, ser, series.UpdateOptions{SkipPublishUpdatedEvent: true}); err != nil {
		return fmt.Errorf("episodemonitored: UpdateSeries(%d): %w", ser.ID, err)
	}

	s.log.LogAttrs(ctx, slog.LevelDebug,
		"episodemonitored: applied monitor option",
		slog.Int("seriesId", ser.ID),
		slog.String("title", ser.Title),
		slog.Int("monitor", int(opts.Monitor)))
	return nil
}

// monitorPredicate returns a per-episode selector matching the
// supplied MonitorType. Returns nil for branches that don't fit the
// predicate-based ToggleEpisodesMonitoredState shape — currently
// MonitorSpecials + MonitorUnmonitorSpecials which scope to season 0
// rather than excluding it. Callers handle those branches directly
// before reaching this helper.
//
// Slice 5.J213.
func monitorPredicate(mt series.MonitorType, firstSeason, lastSeason int, now time.Time) func(*episodes.Episode) bool {
	switch mt {
	case series.MonitorAll:
		return func(e *episodes.Episode) bool { return e.SeasonNumber > 0 }
	case series.MonitorFuture:
		return func(e *episodes.Episode) bool {
			return e.SeasonNumber > 0 && (e.AirDateUtc == nil || !e.AirDateUtc.Before(now))
		}
	case series.MonitorMissing:
		return func(e *episodes.Episode) bool {
			return e.SeasonNumber > 0 && !hasFile(e)
		}
	case series.MonitorExisting:
		return func(e *episodes.Episode) bool {
			return e.SeasonNumber > 0 && hasFile(e)
		}
	case series.MonitorPilot:
		return func(e *episodes.Episode) bool {
			return e.SeasonNumber > 0 && e.SeasonNumber == firstSeason && e.EpisodeNumber == 1
		}
	case series.MonitorFirstSeason:
		return func(e *episodes.Episode) bool {
			return e.SeasonNumber > 0 && e.SeasonNumber == firstSeason
		}
	case series.MonitorLastSeason, series.MonitorLatestSeason:
		return func(e *episodes.Episode) bool {
			return e.SeasonNumber > 0 && e.SeasonNumber == lastSeason
		}
	case series.MonitorRecent:
		// C# :105-109. Future-aired OR aired within last 90 days OR no
		// AirDateUtc set. Anything past 90 days ago is unmonitored.
		ninetyDaysAgo := now.AddDate(0, 0, -90)
		return func(e *episodes.Episode) bool {
			if e.SeasonNumber <= 0 {
				return false
			}
			if e.AirDateUtc == nil {
				return true
			}
			t := *e.AirDateUtc
			// Future episodes
			if t.After(now) {
				return true
			}
			// Aired within last 90 days
			return t.Before(now) && t.After(ninetyDaysAgo)
		}
	case series.MonitorMonitorSpecials:
		// Handled separately — affects season 0 only, doesn't toggle
		// the broader episode set.
		return nil
	case series.MonitorUnmonitorSpecials:
		// Same — season 0 only.
		return nil
	case series.MonitorNone:
		// Match-nothing predicate → ToggleEpisodesMonitoredState sets
		// every episode to unmonitored.
		return func(e *episodes.Episode) bool { return false }
	}
	return nil
}

// toggleMonitored sets ep.Monitored = pred(ep) for every episode (C#
// `ToggleEpisodesMonitoredState(episodes, predicate)` overload at
// `:242-246`).
func toggleMonitored(eps []episodes.Episode, pred func(*episodes.Episode) bool) {
	for i := range eps {
		eps[i].Monitored = pred(&eps[i])
	}
}

// monitoredSeasonsSet returns the distinct set of SeasonNumbers that
// have at least one monitored episode. Used by the cascade to decide
// whether a non-final-season should be marked Monitored.
func monitoredSeasonsSet(eps []episodes.Episode) map[int]bool {
	out := make(map[int]bool, len(eps))
	for _, e := range eps {
		if e.Monitored {
			out[e.SeasonNumber] = true
		}
	}
	return out
}

// seasonRow is the wire/persistence shape of one entry in the series
// Seasons JSON blob. Mirrors C# `Season.cs` (`SeasonNumber` +
// `Monitored`). Decode falls back to empty list on malformed input —
// callers warn-log.
type seasonRow struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

func decodeSeasons(raw string) ([]seasonRow, error) {
	if raw == "" {
		return nil, nil
	}
	var out []seasonRow
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeSeasons(rows []seasonRow) (string, error) {
	if rows == nil {
		// Preserve "no seasons" → empty array, not null.
		rows = []seasonRow{}
	}
	buf, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// minPositiveSeason returns the smallest SeasonNumber > 0, or 0 if no
// such season exists. Mirrors C# `:49 MinOrDefault` filtered on s>0.
func minPositiveSeason(rows []seasonRow) int {
	first := 0
	for _, r := range rows {
		if r.SeasonNumber <= 0 {
			continue
		}
		if first == 0 || r.SeasonNumber < first {
			first = r.SeasonNumber
		}
	}
	return first
}

// maxSeason returns the largest SeasonNumber, or 0 if rows is empty.
// Mirrors C# `:50 MaxOrDefault` (which does NOT filter on s>0, so
// season 0 can be the max if it's the only season).
func maxSeason(rows []seasonRow) int {
	max := 0
	first := true
	for _, r := range rows {
		if first || r.SeasonNumber > max {
			max = r.SeasonNumber
			first = false
		}
	}
	return max
}

// cascadeSeasons applies the C# `:137-169` season-loop logic to the
// supplied rows. Returns a new slice; does NOT mutate input.
//
// Per-season decision tree:
//
//   - seasonNumber > 0 AND seasonNumber == lastSeason AND
//     (Monitor == All OR (Monitor == Future AND Status ∈
//     {Continuing, Upcoming})) → true (C# :146-152).
//   - seasonNumber == firstSeason AND Monitor == Pilot → false
//     (pilot-only doesn't pull the whole first season; C# :153-157).
//   - monitoredSeasons[seasonNumber] → true (C# :158-162).
//   - else → false (C# :164-168).
//
// MonitorSpecials/UnmonitorSpecials are handled outside this function
// (they don't fit the C# switch).
func cascadeSeasons(rows []seasonRow, monitoredSeasons map[int]bool, mt series.MonitorType, firstSeason, lastSeason int, status int) []seasonRow {
	statusContinuingOrUpcoming := status == series.SeriesStatusContinuing || status == series.SeriesStatusUpcoming
	out := make([]seasonRow, len(rows))
	for i, r := range rows {
		sn := r.SeasonNumber
		switch {
		case sn > 0 && sn == lastSeason && (mt == series.MonitorAll || (mt == series.MonitorFuture && statusContinuingOrUpcoming)):
			r.Monitored = true
		case sn == firstSeason && mt == series.MonitorPilot:
			r.Monitored = false
		case monitoredSeasons[sn]:
			r.Monitored = true
		default:
			r.Monitored = false
		}
		out[i] = r
	}
	return out
}

// hasFile mirrors C# `Episode.HasFile` derived property — EpisodeFileID
// non-nil AND > 0. Duplicated here vs episoderefreshed/hasFile to keep
// the package self-contained (and the helper is one line).
func hasFile(ep *episodes.Episode) bool {
	if ep == nil || ep.EpisodeFileID == nil {
		return false
	}
	return *ep.EpisodeFileID > 0
}
