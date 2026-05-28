// Package seriesstats computes per-series + per-season episode/file
// statistics — the Go port of NzbDrone.Core.SeriesStats
// (SeriesStatistics / SeasonStatistics / SeriesStatisticsService /
// SeriesStatisticsRepository).
//
// The C# tree runs this as two grouped SQL queries (Episodes grouped by
// series+season for the counts/airing, EpisodeFiles grouped by
// series+season for size/release-groups/qualities) merged per
// (seriesId, seasonNumber). The Go port computes the identical
// aggregation in memory from already-loaded Episode + EpisodeFile
// slices via [Compute]: this keeps the correctness-critical counting
// semantics in one pure, exhaustively-testable function and reuses the
// existing episode/episodefile repo read methods at the wiring tier
// (same "faithful-degraded, aggregate-in-Go" posture the wanted /
// cutoff-unmet paths use). The numbers match the C# CASE expressions
// exactly; only the execution strategy (in-memory vs SQL GROUP BY)
// differs.
//
// J368 — statistics core. The API projection (SeriesResource.statistics
// + seasons[].statistics + nextAiring/previousAiring + the lastAired
// fallback) wires this in via a populator in a follow-up slice.
package seriesstats

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
)

// SeasonStatistics mirrors `NzbDrone.Core.SeriesStats.SeasonStatistics`
// (the post-parse typed view — not the raw *String DB columns). One per
// season the series carries episodes for.
type SeasonStatistics struct {
	SeriesID     int
	SeasonNumber int
	// NextAiring is the earliest future air time among MONITORED
	// episodes in the season (AirDateUtc >= now). nil when none.
	NextAiring *time.Time
	// PreviousAiring is the latest past air time among MONITORED
	// episodes in the season (AirDateUtc < now). nil when none.
	PreviousAiring *time.Time
	// LastAired is the latest AirDate (date-only column, regardless of
	// monitored) parsed to a time. nil when none parseable.
	LastAired             *time.Time
	EpisodeFileCount      int
	EpisodeCount          int
	TotalEpisodeCount     int
	MonitoredEpisodeCount int
	// AvailableEpisodeCount mirrors the C# column (aired-by-now OR has
	// a file). Computed for completeness; not surfaced on the v3/v5
	// resource today.
	AvailableEpisodeCount int
	SizeOnDisk            int64
	ReleaseGroups         []string
	ReleaseTypes          []int
	EpisodeFileQualities  []int
}

// SeriesStatistics mirrors `NzbDrone.Core.SeriesStats.SeriesStatistics`
// — the per-series rollup across its SeasonStatistics.
type SeriesStatistics struct {
	SeriesID              int
	NextAiring            *time.Time
	PreviousAiring        *time.Time
	LastAired             *time.Time
	EpisodeFileCount      int
	EpisodeCount          int
	TotalEpisodeCount     int
	MonitoredEpisodeCount int
	SizeOnDisk            int64
	ReleaseGroups         []string
	ReleaseTypes          []int
	EpisodeFileQualities  []int
	SeasonStatistics      []SeasonStatistics
}

// Compute aggregates the given episodes + episode files into the
// per-series + per-season statistics, evaluated against `now` (the
// "current time" the C# repository passes as @currentDate). Episodes
// and files may belong to a single series (the by-id path) — callers
// that compute the whole library group by SeriesID first and call
// Compute per group.
//
// Counting semantics (per season, mirroring SeriesStatisticsRepository
// EpisodesBuilder):
//   - TotalEpisodeCount     = COUNT(*)
//   - AvailableEpisodeCount = COUNT(airedByNow OR hasFile)
//   - EpisodeCount          = COUNT((monitored AND airedByNow) OR hasFile)
//   - EpisodeFileCount      = COUNT(hasFile)
//   - MonitoredEpisodeCount = COUNT(monitored)
//   - NextAiring     = MIN(AirDateUtc) over monitored eps with AirDateUtc >= now
//   - PreviousAiring = MAX(AirDateUtc) over monitored eps with AirDateUtc <  now
//   - LastAired      = MAX(AirDate) over all eps (date-only column)
//
// where airedByNow = AirDateUtc != nil && AirDateUtc <= now, and
// hasFile = EpisodeFileID != nil && > 0.
func Compute(eps []episodes.Episode, files []episodefiles.File, now time.Time) SeriesStatistics {
	type seasonAcc struct {
		ss            SeasonStatistics
		groups        map[string]struct{}
		releaseTypes  map[int]struct{}
		qualities     map[int]struct{}
		qualityOrder  []int
		groupOrder    []string
		relTypeOrder  []int
		seenFileGroup bool
	}
	bySeason := map[int]*seasonAcc{}
	order := []int{}
	get := func(seriesID, season int) *seasonAcc {
		a, ok := bySeason[season]
		if !ok {
			a = &seasonAcc{
				ss:           SeasonStatistics{SeriesID: seriesID, SeasonNumber: season},
				groups:       map[string]struct{}{},
				releaseTypes: map[int]struct{}{},
				qualities:    map[int]struct{}{},
			}
			bySeason[season] = a
			order = append(order, season)
		}
		return a
	}

	for i := range eps {
		e := eps[i]
		a := get(e.SeriesID, e.SeasonNumber)
		s := &a.ss
		s.TotalEpisodeCount++
		hasFile := e.EpisodeFileID != nil && *e.EpisodeFileID > 0
		airedByNow := e.AirDateUtc != nil && !e.AirDateUtc.After(now)
		if airedByNow || hasFile {
			s.AvailableEpisodeCount++
		}
		if (e.Monitored && airedByNow) || hasFile {
			s.EpisodeCount++
		}
		if hasFile {
			s.EpisodeFileCount++
		}
		if e.Monitored {
			s.MonitoredEpisodeCount++
		}
		// NextAiring: monitored, future.
		if e.Monitored && e.AirDateUtc != nil && !e.AirDateUtc.Before(now) {
			if s.NextAiring == nil || e.AirDateUtc.Before(*s.NextAiring) {
				s.NextAiring = cloneTime(e.AirDateUtc)
			}
		}
		// PreviousAiring: monitored, past (strictly before now).
		if e.Monitored && e.AirDateUtc != nil && e.AirDateUtc.Before(now) {
			if s.PreviousAiring == nil || e.AirDateUtc.After(*s.PreviousAiring) {
				s.PreviousAiring = cloneTime(e.AirDateUtc)
			}
		}
		// LastAired: max AirDate (date-only string), all episodes.
		if t, ok := parseAirDate(e.AirDate); ok {
			if s.LastAired == nil || t.After(*s.LastAired) {
				tt := t
				s.LastAired = &tt
			}
		}
	}

	for i := range files {
		f := files[i]
		a := get(f.SeriesID, f.SeasonNumber)
		a.ss.SizeOnDisk += f.Size
		if rg := strings.TrimSpace(f.ReleaseGroup); rg != "" {
			if _, ok := a.groups[rg]; !ok {
				a.groups[rg] = struct{}{}
				a.groupOrder = append(a.groupOrder, rg)
			}
		}
		if _, ok := a.releaseTypes[f.ReleaseType]; !ok {
			a.releaseTypes[f.ReleaseType] = struct{}{}
			a.relTypeOrder = append(a.relTypeOrder, f.ReleaseType)
		}
		if qid, ok := qualityID(f.Quality); ok {
			if _, seen := a.qualities[qid]; !seen {
				a.qualities[qid] = struct{}{}
				a.qualityOrder = append(a.qualityOrder, qid)
			}
		}
	}

	// Finalise per-season slices (release groups sorted; types + qualities
	// ascending for a deterministic core ordering — the API tier re-sorts
	// qualities by quality profile).
	sort.Ints(order)
	out := SeriesStatistics{}
	seriesGroups := map[string]struct{}{}
	seriesTypes := map[int]struct{}{}
	seriesQualities := map[int]struct{}{}
	var seriesGroupOrder []string
	for _, season := range order {
		a := bySeason[season]
		a.ss.ReleaseGroups = sortedStrings(a.groupOrder)
		a.ss.ReleaseTypes = sortedInts(a.relTypeOrder)
		a.ss.EpisodeFileQualities = sortedInts(a.qualityOrder)
		out.SeasonStatistics = append(out.SeasonStatistics, a.ss)

		// Series-level rollup.
		out.EpisodeFileCount += a.ss.EpisodeFileCount
		out.EpisodeCount += a.ss.EpisodeCount
		out.TotalEpisodeCount += a.ss.TotalEpisodeCount
		out.MonitoredEpisodeCount += a.ss.MonitoredEpisodeCount
		out.SizeOnDisk += a.ss.SizeOnDisk
		if out.SeriesID == 0 {
			out.SeriesID = a.ss.SeriesID
		}
		for _, g := range a.ss.ReleaseGroups {
			if _, ok := seriesGroups[g]; !ok {
				seriesGroups[g] = struct{}{}
				seriesGroupOrder = append(seriesGroupOrder, g)
			}
		}
		for _, t := range a.ss.ReleaseTypes {
			seriesTypes[t] = struct{}{}
		}
		for _, q := range a.ss.EpisodeFileQualities {
			seriesQualities[q] = struct{}{}
		}
		// NextAiring = min across seasons.
		if a.ss.NextAiring != nil && (out.NextAiring == nil || a.ss.NextAiring.Before(*out.NextAiring)) {
			out.NextAiring = cloneTime(a.ss.NextAiring)
		}
		// PreviousAiring = max across seasons.
		if a.ss.PreviousAiring != nil && (out.PreviousAiring == nil || a.ss.PreviousAiring.After(*out.PreviousAiring)) {
			out.PreviousAiring = cloneTime(a.ss.PreviousAiring)
		}
		// LastAired = max across seasons with SeasonNumber > 0.
		if season > 0 && a.ss.LastAired != nil && (out.LastAired == nil || a.ss.LastAired.After(*out.LastAired)) {
			out.LastAired = cloneTime(a.ss.LastAired)
		}
	}
	out.ReleaseGroups = sortedStrings(seriesGroupOrder)
	out.ReleaseTypes = setToSortedInts(seriesTypes)
	out.EpisodeFileQualities = setToSortedInts(seriesQualities)
	return out
}

// qualityID extracts the bare-int quality id from the persisted
// EpisodeFile.Quality JSON (`{"quality":<id>,"revision":{...}}` — the
// quality.Model.MarshalJSON shape). Empty/invalid → (0, false).
func qualityID(raw string) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var m struct {
		Quality int `json:"quality"`
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return 0, false
	}
	return m.Quality, true
}

// parseAirDate parses the date-only AirDate column ("2006-01-02") to a
// UTC time. Empty/invalid → (zero, false).
func parseAirDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	// Tolerate a full timestamp in the AirDate column.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func sortedInts(in []int) []int {
	out := append([]int(nil), in...)
	sort.Ints(out)
	if out == nil {
		out = []int{}
	}
	return out
}

func setToSortedInts(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
