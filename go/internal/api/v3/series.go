package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/seriesstats"
	"sonarr.tv/sonarr/internal/signalr"
)

// SeriesResource is the JSON shape produced by GET /api/v3/series.
// Mirrors `src/Sonarr.Api.V3/Series/SeriesResource.cs`.
//
// The projection covers the scalar columns the persistence layer
// carries (Id+Title+SortTitle+Status+Path+Monitored+...) plus the
// J366 model-intrinsic field group decoded straight off the row
// (Seasons, OriginalLanguage, Genres, Ratings, Added, FirstAired,
// LastAired, AddOptions). The fields that still need a service join or
// an unported subsystem — Statistics (per-season counts),
// AlternateTitles (scene-mapping table), Images (MediaCover local-URL
// rewrite), EpisodesChanged, NextAiring/PreviousAiring, IsExcluded —
// remain DEFERRED until the consumer ports converge. The empty-DB
// recording has zero records so the remaining gaps are unobservable
// today; each lands in a follow-up slice keyed to its service port.
//
// Field-source mapping (C# → Go), passthrough subset wired here:
//
//	id              → Series.ID (omitempty per RestResource)
//	title           → Series.Title
//	sortTitle       → Series.SortTitle
//	status          → Series.Status (int enum → camelCase string)
//	overview        → Series.Overview
//	network         → Series.Network
//	airTime         → Series.AirTime
//	year            → Series.Year
//	path            → Series.Path
//	qualityProfileId→ Series.QualityProfileID
//	seasonFolder    → Series.SeasonFolder
//	monitored       → Series.Monitored
//	useSceneNumbering→ Series.UseSceneNumbering
//	runtime         → Series.Runtime
//	tvdbId          → Series.TvdbID
//	tvRageId        → Series.TvRageID
//	tvMazeId        → Series.TvMazeID
//	tmdbId          → Series.TmdbID
//	firstAired      → Series.FirstAired (omitempty)
//	lastAired       → Series.LastAired (omitempty)
//	seriesType      → Series.SeriesType (int enum → camelCase string)
//	cleanTitle      → Series.CleanTitle
//	imdbId          → Series.ImdbID
//	titleSlug       → Series.TitleSlug
//	rootFolderPath  → Series.RootFolderPath (runtime-only)
//	certification   → Series.Certification
//	added           → Series.Added (always; zero-time when unset)
//	tags            → Series.Tags ([]int → emitted as set; nil → [])
//	monitorNewItems → Series.MonitorNewItems (int enum → string)
//	ended           → computed (Status == Ended)
//	languageProfileId→ hard-coded 1 (C# `=> 1`)
//
// J366 model-intrinsic projection: seasons, originalLanguage, genres,
// ratings, added, firstAired, lastAired, addOptions are now populated
// directly from the persisted Series row (the fields the C#
// SeriesResourceMapper.ToResource sets straight off `model` with no
// cross-row service join). See ToSeriesResource for the per-field
// source map.
//
// DEFERRED (populated-row only — these need a service join or an
// unported subsystem, so they stay out of the J366 model-intrinsic
// slice): images + seasons[].images (MediaCover local-URL rewrite via
// the mediacover service), statistics + seasons[].statistics +
// nextAiring + previousAiring + episodesChanged (the episode /
// episodefile statistics join), remotePoster (lookup-flow only),
// folder (Path-derived display field the C# mapper itself leaves
// unset). AlternateTitles wire-shape pinned at J329 via
// pointer-to-slice; population is gated on the future scene-mapping
// service join (see AlternateTitleResource doc-comment). IsExcluded
// is gated on the importlistexclusion service join (see below).
//
// J326 v5-additive widening: 4 fields the C# v5 SeriesResource carries
// that don't appear on the C# v3 SeriesResource (deliberate v3
// divergence on populated rows — v3 clients ignore unknown JSON keys
// so additive emission is harmless):
//   - MalIds []int json:"malIds,omitempty" — v5 `HashSet<int>? MalIds`
//     at v5 SeriesResource.cs:32. Persisted as `series.Series.MalIDs`
//     at internal/series/series.go:104. nil/empty drops the key.
//   - AniListIds []int json:"aniListIds,omitempty" — v5 `HashSet<int>?
//     AniListIds` at v5 SeriesResource.cs:33. Persisted as
//     `series.Series.AniListIDs` at series.go:105. nil/empty drops.
//   - OriginalCountry string json:"originalCountry,omitempty" — v5
//     `string? OriginalCountry` at v5 SeriesResource.cs:46. Persisted
//     as `series.Series.OriginalCountry` at series.go:106. Empty
//     string drops the key (matches C# WhenWritingNull for null + the
//     Go-typical empty-string collapse).
//   - IsExcluded *bool json:"isExcluded,omitempty" — v5 `bool?
//     IsExcluded` at v5 SeriesResource.cs:55-58. Computed at projection
//     time by joining against the ImportListExclusion table — the
//     Go-port's importlistexclusion service-join projection is
//     DEFERRED; the field stays nil and omitempty drops the key. v5
//     C# tags this with `[JsonIgnore(Condition = JsonIgnoreCondition.
//     WhenWritingDefault)]` which is equivalent to the Go pointer +
//     omitempty pattern.
type SeriesResource struct {
	ID                int                       `json:"id,omitempty"`
	Title             string                    `json:"title"`
	SortTitle         string                    `json:"sortTitle"`
	Status            string                    `json:"status"`
	Overview          string                    `json:"overview"`
	Network           string                    `json:"network"`
	AirTime           string                    `json:"airTime"`
	Year              int                       `json:"year"`
	Path              string                    `json:"path"`
	QualityProfileID  int                       `json:"qualityProfileId"`
	SeasonFolder      bool                      `json:"seasonFolder"`
	Monitored         bool                      `json:"monitored"`
	UseSceneNumbering bool                      `json:"useSceneNumbering"`
	Runtime           int                       `json:"runtime"`
	TvdbID            int                       `json:"tvdbId"`
	TvRageID          int                       `json:"tvRageId"`
	TvMazeID          int                       `json:"tvMazeId"`
	TmdbID            int                       `json:"tmdbId"`
	MalIds            []int                     `json:"malIds,omitempty"`
	AniListIds        []int                     `json:"aniListIds,omitempty"`
	SeriesType        string                    `json:"seriesType"`
	CleanTitle        string                    `json:"cleanTitle"`
	ImdbID            string                    `json:"imdbId"`
	TitleSlug         string                    `json:"titleSlug"`
	RootFolderPath    string                    `json:"rootFolderPath"`
	Certification     string                    `json:"certification"`
	Tags              []int                     `json:"tags"`
	MonitorNewItems   string                    `json:"monitorNewItems"`
	Ended             bool                      `json:"ended"`
	LanguageProfileID int                       `json:"languageProfileId"`
	Genres            []string                  `json:"genres"`
	OriginalCountry   string                    `json:"originalCountry,omitempty"`
	IsExcluded        *bool                     `json:"isExcluded,omitempty"`
	Seasons           []SeasonResource          `json:"seasons"`
	Images            []MediaCoverResource      `json:"images"`
	OriginalLanguage  LanguageRef               `json:"originalLanguage"`
	Ratings           *RatingsResource          `json:"ratings,omitempty"`
	Added             time.Time                 `json:"added"`
	FirstAired        *time.Time                `json:"firstAired,omitempty"`
	LastAired         *time.Time                `json:"lastAired,omitempty"`
	NextAiring        *time.Time                `json:"nextAiring,omitempty"`
	PreviousAiring    *time.Time                `json:"previousAiring,omitempty"`
	Statistics        *SeriesStatisticsResource `json:"statistics,omitempty"`
	AddOptions        *json.RawMessage          `json:"addOptions,omitempty"`
	AlternateTitles   *[]AlternateTitleResource `json:"alternateTitles,omitempty"`
}

// SeasonResource is the v3 SeasonResource wire shape — mirror of
// `src/Sonarr.Api.V3/Series/SeasonResource.cs:8-14`. Sourced from the
// persisted Series.Seasons opaque-JSON column (one entry per season).
//
// The C# resource also carries `statistics` (SeasonStatisticsResource)
// and `images` (List<MediaCover>); both are DEFERRED in the Go port —
// per-season statistics needs the episode/episodefile statistics join
// and the season images need the MediaCover local-URL rewrite (same
// gates as the parent SeriesResource.Statistics / .Images fields). The
// C# mapper itself only emits `images` when `includeImages: true`
// (default false), so seasons[].images is absent on the default wire
// today anyway. When those slices land they add the pointer fields
// additively without disturbing the seasonNumber/monitored contract.
type SeasonResource struct {
	SeasonNumber int                       `json:"seasonNumber"`
	Monitored    bool                      `json:"monitored"`
	Statistics   *SeasonStatisticsResource `json:"statistics,omitempty"`
}

// RatingsResource is the v3 wire shape for `NzbDrone.Core.Tv.Ratings`
// (`src/NzbDrone.Core/Tv/Ratings.cs`): `{votes:int, value:decimal}`.
// Sourced from the persisted Series.Ratings opaque-JSON column. The
// C# Series model leaves Ratings uninitialized (not in the ctor), so
// a null Ratings drops the key under STJson WhenWritingNull — the
// Go port mirrors this via a pointer + omitempty (empty/absent
// persisted JSON → nil → key absent).
type RatingsResource struct {
	Votes int     `json:"votes"`
	Value float64 `json:"value"`
}

// MediaCoverResource is the v3 wire shape for
// `NzbDrone.Core.MediaCover.MediaCover` (`src/NzbDrone.Core/MediaCover/
// MediaCover.cs:16-31`) as surfaced on SeriesResource.Images. Sourced
// from the persisted Series.Images opaque-JSON column.
//
// CoverType is the camelCase enum string ("poster"/"banner"/"fanart"/
// "screenshot"/"headshot"/"clearlogo"/"unknown") — the persisted blob
// stores the legacy int ordinal (the Go-port mediacover.CoverType
// frozen-int representation), so the projection maps it via
// mediacover.CoverType.String() at read time. Url is the LOCAL cover
// URL (`<urlBase>/MediaCover/<seriesId>/<type>.<ext>?lastWrite=<ticks>`),
// which is computed at handler time by the SeriesImagePopulator (the
// persisted Url is empty/stale — C# likewise recomputes it in the
// controller's MapCoversToLocal, not in the mapper). RemoteUrl is the
// upstream metadata-source URL (nullable on C# → omitempty).
type MediaCoverResource struct {
	CoverType string `json:"coverType"`
	Url       string `json:"url"`
	RemoteUrl string `json:"remoteUrl,omitempty"`
}

// SeriesStatisticsResource is the v3 wire shape for
// `Sonarr.Api.V3.Series.SeriesStatisticsResource` — the per-series
// episode/file rollup. Sourced from the seriesstats aggregation and
// linked onto SeriesResource at handler time (mirrors C#
// SeriesController.LinkSeriesStatistics, NOT the mapper).
//
// SeasonCount is computed from the resource's Seasons (count where
// SeasonNumber > 0), not from the stats. PercentOfEpisodes is the C#
// computed property emitted as a number (EpisodeCount==0 → 0, else
// EpisodeFileCount/EpisodeCount*100). ReleaseGroups is always emitted
// as a (possibly empty) array — matches the populated-row C# non-null
// list.
//
// J370 v5-additive widening: MonitoredEpisodeCount, ReleaseTypes
// ([]ReleaseType → camelCase enum strings), and EpisodeFileQualities
// ([]Quality DTOs) are carried on the C# v5 SeriesStatisticsResource but
// NOT the v3 one. Emitted additively on this shared type with omitempty
// (the J318 additive-divergence posture — v5 lights them up on populated
// rows; v3 clients ignore the extra keys; zero/empty collapses to absent,
// the accepted v5-drops-zero trade-off). Sourced from the seriesstats
// core's MonitoredEpisodeCount / ReleaseTypes / EpisodeFileQualities.
type SeriesStatisticsResource struct {
	SeasonCount           int            `json:"seasonCount"`
	EpisodeFileCount      int            `json:"episodeFileCount"`
	EpisodeCount          int            `json:"episodeCount"`
	TotalEpisodeCount     int            `json:"totalEpisodeCount"`
	MonitoredEpisodeCount int            `json:"monitoredEpisodeCount,omitempty"`
	SizeOnDisk            int64          `json:"sizeOnDisk"`
	ReleaseGroups         []string       `json:"releaseGroups"`
	ReleaseTypes          []string       `json:"releaseTypes,omitempty"`
	EpisodeFileQualities  []QualityValue `json:"episodeFileQualities,omitempty"`
	PercentOfEpisodes     float64        `json:"percentOfEpisodes"`
}

// SeasonStatisticsResource is the v3 wire shape for
// `Sonarr.Api.V3.Series.SeasonStatisticsResource` — the per-season
// rollup linked onto each SeasonResource.Statistics. Same J370
// v5-additive widening as SeriesStatisticsResource (MonitoredEpisodeCount
// / ReleaseTypes / EpisodeFileQualities, omitempty additive).
type SeasonStatisticsResource struct {
	NextAiring            *time.Time     `json:"nextAiring,omitempty"`
	PreviousAiring        *time.Time     `json:"previousAiring,omitempty"`
	EpisodeFileCount      int            `json:"episodeFileCount"`
	EpisodeCount          int            `json:"episodeCount"`
	TotalEpisodeCount     int            `json:"totalEpisodeCount"`
	MonitoredEpisodeCount int            `json:"monitoredEpisodeCount,omitempty"`
	SizeOnDisk            int64          `json:"sizeOnDisk"`
	ReleaseGroups         []string       `json:"releaseGroups"`
	ReleaseTypes          []string       `json:"releaseTypes,omitempty"`
	EpisodeFileQualities  []QualityValue `json:"episodeFileQualities,omitempty"`
	PercentOfEpisodes     float64        `json:"percentOfEpisodes"`
}

// percentOfEpisodes mirrors the C# computed property: 0 when
// episodeCount is 0, else fileCount/episodeCount*100.
func percentOfEpisodes(fileCount, episodeCount int) float64 {
	if episodeCount == 0 {
		return 0
	}
	return float64(fileCount) / float64(episodeCount) * 100
}

// ToSeriesStatisticsResource maps the seriesstats rollup + the
// resource's seasons (for SeasonCount) to the wire shape. Mirrors C#
// SeriesStatisticsResourceMapper.ToResource.
func ToSeriesStatisticsResource(st seriesstats.SeriesStatistics, seasons []SeasonResource) *SeriesStatisticsResource {
	seasonCount := 0
	for _, s := range seasons {
		if s.SeasonNumber > 0 {
			seasonCount++
		}
	}
	groups := st.ReleaseGroups
	if groups == nil {
		groups = []string{}
	}
	return &SeriesStatisticsResource{
		SeasonCount:           seasonCount,
		EpisodeFileCount:      st.EpisodeFileCount,
		EpisodeCount:          st.EpisodeCount,
		TotalEpisodeCount:     st.TotalEpisodeCount,
		MonitoredEpisodeCount: st.MonitoredEpisodeCount,
		SizeOnDisk:            st.SizeOnDisk,
		ReleaseGroups:         groups,
		ReleaseTypes:          releaseTypeNames(st.ReleaseTypes),
		EpisodeFileQualities:  toQualityValues(st.EpisodeFileQualities),
		PercentOfEpisodes:     percentOfEpisodes(st.EpisodeFileCount, st.EpisodeCount),
	}
}

// ToSeasonStatisticsResource maps a per-season seriesstats entry to the
// wire shape. Mirrors C# SeasonStatisticsResourceMapper.ToResource.
func ToSeasonStatisticsResource(st seriesstats.SeasonStatistics) *SeasonStatisticsResource {
	groups := st.ReleaseGroups
	if groups == nil {
		groups = []string{}
	}
	return &SeasonStatisticsResource{
		NextAiring:            st.NextAiring,
		PreviousAiring:        st.PreviousAiring,
		EpisodeFileCount:      st.EpisodeFileCount,
		EpisodeCount:          st.EpisodeCount,
		TotalEpisodeCount:     st.TotalEpisodeCount,
		MonitoredEpisodeCount: st.MonitoredEpisodeCount,
		SizeOnDisk:            st.SizeOnDisk,
		ReleaseGroups:         groups,
		ReleaseTypes:          releaseTypeNames(st.ReleaseTypes),
		EpisodeFileQualities:  toQualityValues(st.EpisodeFileQualities),
		PercentOfEpisodes:     percentOfEpisodes(st.EpisodeFileCount, st.EpisodeCount),
	}
}

// releaseTypeNames maps the seriesstats release-type ints to the
// camelCase enum strings the C# JsonStringEnumConverter(CamelCase)
// emits for `NzbDrone.Core.Parser.Model.ReleaseType`
// ("singleEpisode"/"multiEpisode"/"seasonPack"/"unknown"). nil/empty →
// nil so the omitempty additive key drops. J370.
func releaseTypeNames(types []int) []string {
	if len(types) == 0 {
		return nil
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, releaseTypeName(t))
	}
	return out
}

// releaseTypeName mirrors the camelCase ReleaseType enum string.
func releaseTypeName(t int) string {
	switch t {
	case 1:
		return "singleEpisode"
	case 2:
		return "multiEpisode"
	case 3:
		return "seasonPack"
	default:
		return "unknown"
	}
}

// toQualityValues maps the seriesstats distinct quality ids to the
// QualityValue wire DTOs (C# v5 EpisodeFileQualities `List<Quality>`).
// Each id resolves via quality.FindByID (unknown → Quality.Unknown,
// matching C# Quality.FindById). nil/empty → nil so the omitempty
// additive key drops. The seriesstats core returns ids ascending; the
// C# tree re-sorts by quality-profile index — the Go port keeps the
// ascending-id order (a faithful-degraded ordering: identical SET, the
// list order is cosmetic for the UI's quality badges). J370.
func toQualityValues(ids []int) []QualityValue {
	if len(ids) == 0 {
		return nil
	}
	out := make([]QualityValue, 0, len(ids))
	for _, id := range ids {
		q, _ := quality.FindByID(id)
		out = append(out, QualityValue{
			ID:         q.ID,
			Name:       q.Name,
			Source:     QualitySourceName(q.Source),
			Resolution: q.Resolution,
		})
	}
	return out
}

// LinkSeriesStatistics fills a SeriesResource's statistics-sourced
// fields from the seriesstats rollup. Pure projection — mirrors C#
// SeriesController.LinkSeriesStatistics (`:255-271`):
//
//   - LastAired ??= st.LastAired (only when the series row itself has no
//     LastAired — the C# null-coalescing assignment).
//   - PreviousAiring / NextAiring = st values (unconditional).
//   - Statistics = ToSeriesStatisticsResource(st, res.Seasons).
//   - each res.Seasons[i].Statistics = matching st.SeasonStatistics by
//     SeasonNumber (unmatched seasons keep nil).
//
// J369.
func LinkSeriesStatistics(res *SeriesResource, st seriesstats.SeriesStatistics) {
	if res == nil {
		return
	}
	if res.LastAired == nil && st.LastAired != nil {
		v := *st.LastAired
		res.LastAired = &v
	}
	res.PreviousAiring = st.PreviousAiring
	res.NextAiring = st.NextAiring
	res.Statistics = ToSeriesStatisticsResource(st, res.Seasons)

	if len(st.SeasonStatistics) > 0 {
		bySeason := make(map[int]seriesstats.SeasonStatistics, len(st.SeasonStatistics))
		for _, s := range st.SeasonStatistics {
			bySeason[s.SeasonNumber] = s
		}
		for i := range res.Seasons {
			if s, ok := bySeason[res.Seasons[i].SeasonNumber]; ok {
				res.Seasons[i].Statistics = ToSeasonStatisticsResource(s)
			}
		}
	}
}

// SeriesStatsPopulator is the optional-extension interface for the
// series read handlers (List + ByID). When the lister/getter parameter
// ALSO implements it, the handler invokes PopulateSeriesStats per
// resource to compute + link the episode/file statistics (the Go-port
// equivalent of C# SeriesController.LinkSeriesStatistics). Not
// implemented by test fixtures → no-op (statistics stays nil).
//
// J369. Same J339/J345/J367 optional-extension idiom as
// SeriesImagePopulator; the production wrapper at cmd/sonarr implements
// BOTH on one value (the handler passes a single lister/getter).
type SeriesStatsPopulator interface {
	PopulateSeriesStats(ctx context.Context, res *SeriesResource)
}

// ApplySeriesStatsPopulator post-processes a SeriesResource via the
// optional-extension type-assertion. nil-safe; no-op when svc doesn't
// implement SeriesStatsPopulator.
//
// J369.
func ApplySeriesStatsPopulator(ctx context.Context, res *SeriesResource, svc any) {
	if res == nil || svc == nil {
		return
	}
	pop, ok := svc.(SeriesStatsPopulator)
	if !ok || pop == nil {
		return
	}
	pop.PopulateSeriesStats(ctx, res)
}

// AlternateTitleResource is the wire-shape mirror of C#
// `Sonarr.Api.V3.Series.AlternateTitleResource` /
// `Sonarr.Api.V5.Series.AlternateTitleResource` — both surfaces
// share the same 5-field DTO sourced from the scene-mapping table
// (NzbDrone.Core.DataAugmentation.Scene.SceneMapping). Surfaced as
// the inner type of SeriesResource.AlternateTitles `*[]…` pointer-
// to-slice 3-state per the J309 pattern.
//
// Wire shape:
//
//	{
//	  "title":             "<string?>",     // nullable
//	  "seasonNumber":      <int?>,          // nullable
//	  "sceneSeasonNumber": <int?>,          // nullable
//	  "sceneOrigin":       "<string?>",     // nullable
//	  "comment":           "<string?>"      // nullable; populated mapper
//	                                        // substitutes "Limited matching"
//	                                        // when comment is empty AND
//	                                        // filter-regex is non-empty per
//	                                        // C# AlternateTitleResource.cs:24-26
//	}
//
// All 5 fields are nullable on both v3+v5; under STJson
// WhenWritingNull, null drops the key. Go-port mirrors this via
// pointer-to-int for the int fields (Go has no native int? in JSON
// — *int + omitempty matches C# `int?` precisely) + omitempty
// strings.
//
// J329 — wire-shape pin only. Population is gated on the future
// scene-mapping service join (the C# Series.ToResource mapper calls
// `_sceneMappingService.FindAlternateTitles(model)` per
// SeriesResource.cs:108-114; the Go-port's scene-mapping service
// hasn't ported yet, so the field stays nil today and omitempty
// drops the key.
type AlternateTitleResource struct {
	Title             string `json:"title,omitempty"`
	SeasonNumber      *int   `json:"seasonNumber,omitempty"`
	SceneSeasonNumber *int   `json:"sceneSeasonNumber,omitempty"`
	SceneOrigin       string `json:"sceneOrigin,omitempty"`
	Comment           string `json:"comment,omitempty"`
}

// SeriesLister narrows the series.Service surface to the read path
// the handler needs. *series.Service satisfies it via GetAllSeries.
type SeriesLister interface {
	GetAllSeries(ctx context.Context) ([]series.Series, error)
}

// SeriesByIDGetter narrows series.Service to the GetSeries-by-id
// read. *series.Service satisfies it via GetSeries.
type SeriesByIDGetter interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// SeriesByIDHandler returns GET /api/v3/series/{id}. Mirrors C#
// `SeriesController.GetResourceById` at SeriesController.cs:153-159.
// `includeSeasonImages` query param is accepted but ignored - the
// existing SeriesResource skeleton DEFERS the seasons/images
// projection (see resource doc).
func SeriesByIDHandler(svc SeriesByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "series: invalid id")
			return
		}
		s, err := svc.GetSeries(r.Context(), id)
		if err != nil {
			if errors.Is(err, series.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("series %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: get %d: %w", id, err), nil)
			return
		}
		res := ToSeriesResource(*s)
		ApplySeriesStatsPopulator(r.Context(), &res, svc)
		ApplySeriesImagePopulator(&res, svc)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// SeriesListHandler returns the http.Handler for GET /api/v3/series.
// Mirrors `SeriesController.AllSeries` at
// `src/Sonarr.Api.V3/Series/SeriesController.cs:109-126`. The
// `tvdbId` + `includeSeasonImages` query params from the C# branch
// aren't exercised by the recorded corpus today and are DEFERRED.
func SeriesListHandler(lister SeriesLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.GetAllSeries(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: %w", err), nil)
			return
		}
		out := make([]SeriesResource, 0, len(rows))
		for _, s := range rows {
			out = append(out, ToSeriesResource(s))
		}
		for i := range out {
			ApplySeriesStatsPopulator(r.Context(), &out[i], lister)
			ApplySeriesImagePopulator(&out[i], lister)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ToSeriesResource maps the persisted Series to the wire shape. The
// J366 model-intrinsic field group (seasons, originalLanguage, genres,
// ratings, added, firstAired, lastAired, addOptions) is populated
// directly off the row; the service-join fields (statistics, images,
// nextAiring/previousAiring, alternateTitles, isExcluded) stay
// DEFERRED — see the SeriesResource doc comment for the full list.
//
// Status + SeriesType + MonitorNewItems are int enums in the
// persistence layer; the C# wire converts them via
// JsonStringEnumConverter+CamelCase. The Go port's mapping tables
// live alongside the per-domain conversion helpers (qualitySource
// in qualitydefinition.go; the series-side ones land here so the
// table stays close to the only caller).
func ToSeriesResource(s series.Series) SeriesResource {
	tags := s.Tags
	if tags == nil {
		tags = []int{}
	}
	added := time.Time{}
	if s.Added != nil {
		added = *s.Added
	}
	return SeriesResource{
		ID:                s.ID,
		Title:             s.Title,
		SortTitle:         s.SortTitle,
		Status:            seriesStatusName(s.Status),
		Overview:          s.Overview,
		Network:           s.Network,
		AirTime:           s.AirTime,
		Year:              s.Year,
		Path:              s.Path,
		QualityProfileID:  s.QualityProfileID,
		SeasonFolder:      s.SeasonFolder,
		Monitored:         s.Monitored,
		UseSceneNumbering: s.UseSceneNumbering,
		Runtime:           s.Runtime,
		TvdbID:            s.TvdbID,
		TvRageID:          s.TvRageID,
		TvMazeID:          s.TvMazeID,
		TmdbID:            s.TmdbID,
		MalIds:            s.MalIDs,
		AniListIds:        s.AniListIDs,
		SeriesType:        seriesTypeName(s.SeriesType),
		CleanTitle:        s.CleanTitle,
		ImdbID:            s.ImdbID,
		TitleSlug:         s.TitleSlug,
		RootFolderPath:    s.RootFolderPath,
		Certification:     s.Certification,
		Tags:              tags,
		MonitorNewItems:   monitorNewItemsName(s.MonitorNewItems),
		Ended:             s.Status == seriesStatusEnded,
		LanguageProfileID: 1,
		Genres:            decodeSeriesGenres(s.Genres),
		OriginalCountry:   s.OriginalCountry,
		Seasons:           decodeSeriesSeasons(s.Seasons),
		Images:            decodeSeriesImages(s),
		OriginalLanguage:  ToLanguageRef(s.OriginalLanguage),
		Ratings:           decodeSeriesRatings(s.Ratings),
		Added:             added,
		FirstAired:        s.FirstAired,
		LastAired:         s.LastAired,
		AddOptions:        rawJSONOrNil(s.AddOptions),
		// IsExcluded *bool stays nil — populated by the future v5
		// `importlistexclusion.Service`-join projection when that
		// service-port slice lands. omitempty drops the key today.
	}
}

// decodeSeriesGenres parses the persisted Series.Genres opaque-JSON
// column (`["Drama","Comedy"]`) into a []string. Empty/invalid input
// yields a non-nil empty slice so the wire emits `"genres":[]` —
// matches the C# `Genres = new List<string>()` non-null ctor default.
func decodeSeriesGenres(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

// decodeSeriesSeasons parses the persisted Series.Seasons opaque-JSON
// column into the wire SeasonResource subset (seasonNumber +
// monitored). The persisted blob carries the same camelCase keys the
// C# STJson embedded-document serializer wrote (confirmed against the
// v5 season-monitor handler's `row["seasonNumber"]` read). Per-season
// statistics + images are intentionally ignored here (DEFERRED — see
// SeasonResource doc). Empty/invalid input yields a non-nil empty
// slice so the wire emits `"seasons":[]` — matches the C#
// `Seasons = new List<Season>()` non-null ctor default + the mapper's
// always-non-null `model.Seasons.ToResource(...)`.
func decodeSeriesSeasons(raw string) []SeasonResource {
	out := []SeasonResource{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var rows []struct {
		SeasonNumber int  `json:"seasonNumber"`
		Monitored    bool `json:"monitored"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return out
	}
	for _, r := range rows {
		out = append(out, SeasonResource{SeasonNumber: r.SeasonNumber, Monitored: r.Monitored})
	}
	return out
}

// decodeSeriesImages parses the persisted Series.Images opaque-JSON
// column into the wire MediaCoverResource shape. The persisted blob
// stores coverType as the frozen int ordinal; this maps it to the
// camelCase enum string via mediacover.CoverType.String(). Url carries
// the persisted value (empty/stale on a fresh row) — the handler-tier
// SeriesImagePopulator rewrites it to the local form, mirroring the C#
// controller's MapCoversToLocal. Empty/invalid input yields a non-nil
// empty slice so the wire emits `"images":[]` — matches the C#
// `Images = new List<MediaCover>()` non-null ctor default + the
// always-non-null `model.Images.JsonClone()` in the mapper.
func decodeSeriesImages(s series.Series) []MediaCoverResource {
	return decodeMediaCovers(s.Images)
}

// decodeMediaCovers parses a persisted MediaCover opaque-JSON blob (the
// shape stored in Series.Images / Episode.Images: a JSON array of
// `{"coverType":<int>,"url":"…","remoteUrl":"…"}`) into the wire
// MediaCoverResource shape, mapping the frozen-int coverType ordinal to
// the camelCase enum string via mediacover.CoverType.String(). Url +
// RemoteUrl pass through verbatim (the series path rewrites Url to the
// local form via its handler-tier populator; the episode path emits the
// persisted Url as C# does — see the per-caller docs). Empty/invalid →
// non-nil empty slice. J371 generalised this from decodeSeriesImages so
// the episode images projection can share it.
func decodeMediaCovers(raw string) []MediaCoverResource {
	out := []MediaCoverResource{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var covers []mediacover.Cover
	if err := json.Unmarshal([]byte(raw), &covers); err != nil {
		return out
	}
	for _, c := range covers {
		out = append(out, MediaCoverResource{
			CoverType: c.CoverType.String(),
			Url:       c.Url,
			RemoteUrl: c.RemoteUrl,
		})
	}
	return out
}

// SeriesImagePopulator is the optional-extension interface for the
// series read handlers (List + ByID). When the handler's
// lister/getter parameter ALSO implements this interface, each handler
// invokes PopulateSeriesImages per resource to rewrite the
// MediaCoverResource.Url fields to the local cover URL
// (`<urlBase>/MediaCover/<seriesId>/<type>.<ext>?lastWrite=<ticks>`).
// When NOT implemented (typical test fixtures), the fallback leaves
// the persisted Url untouched — preserving the pre-J367 wire shape.
//
// This mirrors C# `SeriesController.MapCoversToLocal` (`:231-237`),
// which runs `_coverMapper.ConvertToLocalUrls(resource.Id,
// resource.Images)` AFTER `ToResource` at the controller layer (not
// in the mapper). Production wiring at cmd/sonarr composes
// *series.Service with a wrapper that ALSO implements
// PopulateSeriesImages via *mediacover.Service.ConvertToLocalUrls.
// Same J339/J345 optional-extension idiom.
//
// J367.
type SeriesImagePopulator interface {
	PopulateSeriesImages(seriesID int, images []MediaCoverResource)
}

// ApplySeriesImagePopulator post-processes a freshly-built
// SeriesResource via the optional-extension type-assertion. svc is the
// handler's lister/getter interface (typed as `any` so the helper
// accepts any concrete type the handler holds); if it ALSO implements
// SeriesImagePopulator, this helper rewrites the resource's image URLs
// in place. Otherwise no-op (preserves the persisted-Url fallback).
// nil-safe.
//
// J367.
func ApplySeriesImagePopulator(res *SeriesResource, svc any) {
	if res == nil || svc == nil || len(res.Images) == 0 {
		return
	}
	pop, ok := svc.(SeriesImagePopulator)
	if !ok || pop == nil {
		return
	}
	pop.PopulateSeriesImages(res.ID, res.Images)
}

// decodeSeriesRatings parses the persisted Series.Ratings opaque-JSON
// column (`{"votes":1000,"value":8.5}`) into a *RatingsResource.
// Empty/invalid input yields nil so omitempty drops the key — matches
// the C# null-Ratings WhenWritingNull behaviour (Ratings is not
// initialized in the Series ctor).
func decodeSeriesRatings(raw string) *RatingsResource {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var r RatingsResource
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil
	}
	return &r
}

// rawJSONOrNil returns a *json.RawMessage carrying the persisted
// opaque-JSON column verbatim, or nil when the column is empty/whitespace
// (so omitempty drops the key). Used for AddOptions, whose persisted
// bytes were already written in the camelCase wire shape by the C#
// STJson serializer — verbatim re-emission is byte-faithful and avoids
// re-deriving the AddSeriesOptions/MonitoringOptions DTO shape here.
func rawJSONOrNil(raw string) *json.RawMessage {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	m := json.RawMessage(raw)
	return &m
}

// seriesStatusEnded mirrors `SeriesStatusType.Ended = 1`
// (`src/NzbDrone.Core/Tv/SeriesStatusType.cs`). Used by the Ended
// computed flag on the wire.
const seriesStatusEnded = 1

// SeriesImportHandler returns POST /api/v3/series/import. Mirrors
// C# `SeriesImportController.Import` at
// `src/Sonarr.Api.V3/Series/SeriesImportController.cs:13-20`. The
// React UI's "import existing series from disk" flow and the
// import-list scheduled sync (when an enabled import list discovers
// new series matching the configured criteria) both hit this
// endpoint with a JSON array of SeriesResource.
//
// 5.D2 ships the DB-write boundary WITHOUT the AddSeriesService
// cascade — same pragmatic split as 5.D1a's single-row POST.
// Specifically NOT replicated:
//
//   - TVDB lookup-then-merge (the C# pipeline fetches the upstream
//     SkyHook metadata and overlays the user-supplied row).
//   - Folder creation via DiskProvider when AddOptions requests it.
//   - Season-pass seeding (the C# `SeasonPassService` creates one
//     Episode placeholder per season per series for the upcoming
//     scheduler-pass refresh).
//   - Image download + cover-cache priming.
//   - ImportListExclusion clearing for any TVDB id being added back.
//
// The Go port persists each row as supplied + fires SignalR Created
// per row. The React UI's wizard pre-computes Path via the
// rootfolder + title-slug so the empty-DB byte-equal contract isn't
// broken.
//
// Validation: same minimum as the single-row POST (validateSeriesResource:
// Title + Path + QualityProfileId). The first invalid row aborts
// the batch with a 400 (matches C# `_addSeriesService.AddSeries`
// throwing on the first validator failure — no partial-write
// semantics).
//
// Empty array returns 200 with `[]`. Non-empty success returns 201
// + the persisted resources.
func SeriesImportHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSeriesImportBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("series/import: malformed body: %v", err))
			return
		}
		// Pre-validate the whole batch so a partial-insert failure
		// can't sneak through. Mirrors the C# pipeline which throws
		// from inside the foreach before any DB write happens.
		for i, row := range body {
			if failures := validateSeriesResource(row); len(failures) > 0 {
				// Prefix the property name with the row index so the
				// React UI can surface which item in the batch failed.
				prefixed := make([]httperr.Failure, len(failures))
				for j, f := range failures {
					f.PropertyName = fmt.Sprintf("[%d].%s", i, f.PropertyName)
					prefixed[j] = f
				}
				httperr.WriteValidation(w, prefixed)
				return
			}
		}
		if len(body) == 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		created := make([]SeriesResource, 0, len(body))
		for _, res := range body {
			row := fromSeriesResource(res, nil)
			saved, err := svc.Insert(r.Context(), row)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/import: insert %q: %w", res.Title, err), nil)
				return
			}
			out := ToSeriesResource(*saved)
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "created", func() error {
				return emitter.Created(r.Context(), out)
			})
			created = append(created, out)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(created)
		_, _ = w.Write(buf)
	})
}

func decodeSeriesImportBody(r *http.Request) ([]SeriesResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out []SeriesResource
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// SeriesEditorResource mirrors C# `SeriesEditorResource` at
// `src/Sonarr.Api.V3/Series/SeriesEditorResource.cs`. The bulk-edit
// drawer in the React UI's series table POSTs this to PUT
// /api/v3/series/editor (multi-row mutate) and DELETE
// /api/v3/series/editor (multi-row delete).
//
// Differences vs the 5.C-family bulk resources:
//
//   - The id list field is named `seriesIds`, not `ids`.
//   - MoveFiles / DeleteFiles / AddImportListExclusion are
//     TOP-LEVEL booleans on the body (not query params).
//   - 5 optional pointer-typed per-field updates plus a string-typed
//     RootFolderPath (empty-string = "not supplied", matching the
//     C# `string.IsNotNullOrWhiteSpace()` check at line :60).
//   - MonitorNewItems + SeriesType are pointer-typed strings on the
//     wire (C# nullable enums serialise as nullable strings via
//     STJson's JsonStringEnumConverter).
//
// applyTagsAction (defined in indexer.go for 5.C2e) is family-
// agnostic and reused here.
type SeriesEditorResource struct {
	SeriesIDs              []int           `json:"seriesIds"`
	Monitored              *bool           `json:"monitored,omitempty"`
	MonitorNewItems        *string         `json:"monitorNewItems,omitempty"`
	QualityProfileID       *int            `json:"qualityProfileId,omitempty"`
	SeriesType             *string         `json:"seriesType,omitempty"`
	SeasonFolder           *bool           `json:"seasonFolder,omitempty"`
	RootFolderPath         string          `json:"rootFolderPath"`
	Tags                   []int           `json:"tags"`
	ApplyTags              applyTagsAction `json:"applyTags"`
	MoveFiles              bool            `json:"moveFiles"`
	DeleteFiles            bool            `json:"deleteFiles"`
	AddImportListExclusion bool            `json:"addImportListExclusion"`
}

// SeriesEditorUpdateHandler returns PUT /api/v3/series/editor.
// Mirrors C# `SeriesEditorController.SaveAll` at
// `SeriesEditorController.cs:24-101`. Iterates SeriesIDs, applies
// per-field updates only when the body's optional pointer is non-
// nil, broadcasts Updated per row, returns 202 + array.
//
// MoveFiles (J438): when set with a RootFolderPath, each series folder
// is relocated on disk via the optional SeriesMover surface before the
// row is persisted (ports the C# BulkMoveSeriesCommand arm). Reuses the
// same disk-move path as the single-series ?moveFiles=true handler.
func SeriesEditorUpdateHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSeriesEditorBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("series/editor: malformed body: %v", err))
			return
		}
		if len(body.SeriesIDs) == 0 {
			httperr.BadRequest(w, "seriesIds must be provided")
			return
		}
		updated := make([]SeriesResource, 0, len(body.SeriesIDs))
		for _, id := range body.SeriesIDs {
			existing, err := svc.GetSeries(r.Context(), id)
			if errors.Is(err, series.ErrNotFound) {
				continue
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/editor: get %d: %w", id, err), nil)
				return
			}
			oldPath := existing.Path
			applySeriesEditorUpdate(existing, body)
			// moveFiles cascade (C# BulkMoveSeriesCommand arm,
			// SeriesEditorController.cs:60-102): when RootFolderPath is
			// supplied AND MoveFiles is set, relocate the series folder on
			// disk (newRoot/<existing folder name>) BEFORE persisting, so a
			// move failure leaves the DB row unchanged — same ordering +
			// disk path as the single-series ?moveFiles=true arm (5.D9b).
			// Reuses the optional SeriesMover surface. Per-series folder name
			// is the basename of the current path (the title is unchanged by
			// a root-folder move); C# recomputes it from the naming config,
			// which yields the same name for an unchanged title.
			if body.MoveFiles && strings.TrimSpace(body.RootFolderPath) != "" && oldPath != "" {
				newPath := filepath.Join(body.RootFolderPath, filepath.Base(oldPath))
				if newPath != oldPath {
					if mover, ok := svc.(SeriesMover); ok {
						if mErr := mover.MoveFolder(r.Context(), oldPath, newPath); mErr != nil {
							httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/editor: moveFolder %d %q→%q: %w", id, oldPath, newPath, mErr), nil)
							return
						}
						existing.Path = newPath
					}
				}
			}
			if err := svc.Update(r.Context(), existing); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/editor: update %d: %w", id, err), nil)
				return
			}
			res := ToSeriesResource(*existing)
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "updated", func() error {
				return emitter.Updated(r.Context(), res)
			})
			updated = append(updated, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(updated)
		_, _ = w.Write(buf)
	})
}

// SeriesEditorDeleteHandler returns DELETE /api/v3/series/editor.
// Mirrors C# `SeriesEditorController.DeleteSeries` at :103-108.
// Loops Delete per row, broadcasts Deleted, returns 200 {}.
//
// body.DeleteFiles triggers the DB-row + on-disk cascade per row
// (same type-assert path as SeriesDeleteHandler).
// body.AddImportListExclusion adds each series's TvdbID+Title to
// the ImportListExclusion table BEFORE the delete (type-assert to
// [SeriesExclusionAdder]).
func SeriesEditorDeleteHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSeriesEditorBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("series/editor: malformed body: %v", err))
			return
		}
		if len(body.SeriesIDs) == 0 {
			httperr.BadRequest(w, "seriesIds must be provided")
			return
		}
		adder, hasExclusion := svc.(SeriesExclusionAdder)
		for _, id := range body.SeriesIDs {
			if body.AddImportListExclusion && hasExclusion {
				if eErr := adder.AddImportListExclusion(r.Context(), id); eErr != nil {
					if errors.Is(eErr, series.ErrNotFound) {
						// Skip missing rows silently (same shape as the
						// cascade's missing-id path below).
						continue
					}
					httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/editor: addImportListExclusion %d: %w", id, eErr), nil)
					return
				}
			}
			err := deleteSeriesWithOptionalCascade(r.Context(), svc, id, body.DeleteFiles)
			if errors.Is(err, series.ErrNotFound) {
				continue
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/editor: delete %d: %w", id, err), nil)
				return
			}
			id := id
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "deleted", func() error {
				return emitter.Deleted(r.Context(), SeriesResource{ID: id})
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeSeriesEditorBody(r *http.Request) (SeriesEditorResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out SeriesEditorResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return SeriesEditorResource{}, err
	}
	return out, nil
}

// applySeriesEditorUpdate mutates `existing` per the editor body.
// Mirrors the C# `foreach (var series in seriesToUpdate)` block at
// `SeriesEditorController.cs:32-89`. Per-field non-nil pointer ⇒
// apply; nil ⇒ preserve.
func applySeriesEditorUpdate(existing *series.Series, body SeriesEditorResource) {
	if body.Monitored != nil {
		existing.Monitored = *body.Monitored
	}
	if body.MonitorNewItems != nil {
		existing.MonitorNewItems = monitorNewItemsFromName(*body.MonitorNewItems)
	}
	if body.QualityProfileID != nil {
		existing.QualityProfileID = *body.QualityProfileID
	}
	if body.SeriesType != nil {
		existing.SeriesType = seriesTypeFromName(*body.SeriesType)
	}
	if body.SeasonFolder != nil {
		existing.SeasonFolder = *body.SeasonFolder
	}
	if strings.TrimSpace(body.RootFolderPath) != "" {
		existing.RootFolderPath = body.RootFolderPath
		// The on-disk move for the MoveFiles cascade is handled in
		// SeriesEditorUpdateHandler (it needs the request context + the
		// SeriesMover surface); this only updates the RootFolderPath
		// column.
	}
	if body.Tags != nil {
		switch body.ApplyTags {
		case applyTagsReplace:
			existing.Tags = append([]int(nil), body.Tags...)
		case applyTagsAdd:
			for _, t := range body.Tags {
				if !containsInt(existing.Tags, t) {
					existing.Tags = append(existing.Tags, t)
				}
			}
		case applyTagsRemove:
			out := existing.Tags[:0]
			for _, t := range existing.Tags {
				if !containsInt(body.Tags, t) {
					out = append(out, t)
				}
			}
			existing.Tags = out
		}
	}
}

// SeriesWriter is the CUD surface. *series.Repo satisfies via the
// embedded repo.Repository[Series] generic. The narrow interface
// keeps the handler tests free of the full series.Service dependency
// graph (which pulls in messaging.Bus + Repo).
//
// NOTE: this is INTENTIONALLY at the Repo layer, not the Service
// layer. The C# `_seriesService.UpdateSeries` + `DeleteSeries`
// methods carry side-effects (Tags refresh, MoveSeriesCommand
// dispatch, deleteFiles cascade, ImportListExclusion add). Those are
// DEFERRED to follow-up slices — 5.D1a ships the wire boundary +
// the DB write but no disk side-effects. Operators using the API
// today get DB-only behavior; the React UI's add-series wizard +
// disk-move-on-rename flow won't fire until the cascade ports.
type SeriesWriter interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
	GetAllSeries(ctx context.Context) ([]series.Series, error)
	Insert(ctx context.Context, n *series.Series) (*series.Series, error)
	Update(ctx context.Context, n *series.Series) error
	Delete(ctx context.Context, id int) error
}

// SeriesMover is the optional surface for the `?moveFiles=true`
// query param on PUT /series/{id}. When the impl satisfies this
// interface AND the body's Path differs from the existing row's
// Path, the on-disk folder is renamed BEFORE the DB row updates.
//
// Wired via type-assertion on the SeriesWriter argument (same
// backward-compat pattern as SeriesCascader + SeriesExclusionAdder).
//
// The cmd-side adapter calls *disk.Provider.MoveFolder under the
// hood. The C# tree uses a MoveSeriesCommand dispatched on the
// command bus; the Go port collapses to a direct disk call because
// no other consumer reacts to MoveSeriesCommand today.
//
// Failure semantics: if MoveFolder returns a non-nil error, the
// handler aborts with 500 and the DB row stays unchanged. The user
// retries; once the underlying issue (permission, target exists,
// in-use file) is resolved, the next PUT will retry the move.
type SeriesMover interface {
	MoveFolder(ctx context.Context, src, dst string) error
}

// SeriesExclusionAdder is the optional surface for the
// `?addImportListExclusion=true` (single-row) and
// `body.AddImportListExclusion` (editor) flags. The cmd-side
// adapter looks up the series's TvdbID+Title and inserts an
// importlistexclusions row so future import-list syncs skip the
// row. Wired via type-assertion on the SeriesWriter argument —
// when the impl doesn't satisfy this interface the flag is
// silently no-op'd (same backward-compat pattern as
// SeriesCascader).
//
// Failure semantics: if AddImportListExclusion returns a non-nil
// error the handler aborts with 500 BEFORE the delete fires —
// matches C# behaviour where _seriesService.DeleteSeries throws
// from inside the exclusion-add path. The series row stays for
// a retry.
type SeriesExclusionAdder interface {
	AddImportListExclusion(ctx context.Context, id int) error
}

// SeriesCascader is the cascade-aware delete surface. The
// cmd-side adapter (in cmd/sonarr/host_wiring.go) bundles
// episodefiles.Service + episodes.Service + series.Service so a
// single call removes all three families' rows in the correct
// FK-parent-last order.
//
// Wired via type-assertion on the SeriesWriter argument the handler
// receives — if the passed SeriesWriter ALSO implements
// SeriesCascader and the request's deleteFiles flag is true, the
// cascade fires before the series row is deleted. Tests don't have
// to implement this — the type-assertion gracefully falls back to
// the plain svc.Delete path when the flag is false or the impl
// doesn't satisfy SeriesCascader.
//
// IMPORTANT: this slice (5.D5) only cascades DB ROWS (episodes +
// episodefiles + series tables). The C# tree also calls
// DiskProvider.DeleteFolder to remove the on-disk series folder
// when ?deleteFiles=true. That physical-disk cascade is DEFERRED
// — the API-level flag is now honoured for DB rows but the
// actual files remain on disk. A follow-up slice will port the
// DiskProvider wire-through.
type SeriesCascader interface {
	DeleteCascade(ctx context.Context, id int) error
}

// SeriesEmitter mirrors the
// *signalr.ResourceBroadcaster[SeriesResource] subset the write
// handlers call. *signalr.ResourceBroadcaster[SeriesResource]
// satisfies directly.
type SeriesEmitter interface {
	Created(ctx context.Context, resource SeriesResource) error
	Updated(ctx context.Context, resource SeriesResource) error
	Deleted(ctx context.Context, resource SeriesResource) error
}

// SeriesCreateHandler returns POST /api/v3/series. Mirrors C#
// `SeriesController.AddSeries` at SeriesController.cs:169-177.
// Returns 201 + the persisted resource + SignalR Created.
//
// Validation (minimal — matches the C# `SeriesValidator` constraints
// actively enforced at this endpoint):
//
//   - Title non-empty
//   - Path non-empty
//   - QualityProfileId > 0
//
// The richer C# AddSeriesService.AddSeries pipeline (RootFolderPath
// → Path computation via SeriesPathBuilder, TVDB lookup-then-merge,
// folder creation, season-pass seeding, image download) is DEFERRED.
// 5.D1a's POST stores the row as-supplied; the React UI's wizard
// already computes Path client-side via the rootfolder + title-slug
// so the empty-DB byte-equal contract isn't broken.
func SeriesCreateHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSeriesBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("series: malformed body: %v", err))
			return
		}
		if failures := validateSeriesResource(body); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row := fromSeriesResource(body, nil)
		saved, err := svc.Insert(r.Context(), row)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: insert: %w", err), nil)
			return
		}
		res := ToSeriesResource(*saved)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// SeriesUpdateHandler returns PUT /api/v3/series/{id}. Mirrors C#
// `SeriesController.UpdateSeries` at :179-207. The `?moveFiles=true`
// query param triggers MoveSeriesCommand in C# — DEFERRED here. The
// param is parsed and ignored for forward-compat (the React UI's
// rename-folder dialog passes it; ignoring it means the DB row
// updates with the new Path but the on-disk move doesn't happen).
//
// Returns 202 + resource + SignalR Updated.
func SeriesUpdateHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "series: invalid id")
			return
		}
		body, err := decodeSeriesBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("series: malformed body: %v", err))
			return
		}
		existing, err := svc.GetSeries(r.Context(), id)
		if errors.Is(err, series.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("series %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: get %d: %w", id, err), nil)
			return
		}
		if failures := validateSeriesResource(body); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row := fromSeriesResource(body, existing)
		row.ID = id
		// 5.D9b: ?moveFiles=true wire-through. When the impl satisfies
		// SeriesMover AND the new Path differs from the existing Path,
		// rename the on-disk folder BEFORE the DB row update so a
		// folder-move failure doesn't leave the DB pointing at a path
		// the user hasn't actually renamed yet.
		if r.URL.Query().Get("moveFiles") == "true" && existing.Path != row.Path && existing.Path != "" && row.Path != "" {
			if mover, ok := svc.(SeriesMover); ok {
				if mErr := mover.MoveFolder(r.Context(), existing.Path, row.Path); mErr != nil {
					httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: moveFolder %d %q→%q: %w", id, existing.Path, row.Path, mErr), nil)
					return
				}
			}
		}
		if err := svc.Update(r.Context(), row); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: update %d: %w", id, err), nil)
			return
		}
		res := ToSeriesResource(*row)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "updated", func() error {
			return emitter.Updated(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// SeriesDeleteHandler returns DELETE /api/v3/series/{id}. Mirrors C#
// `SeriesController.DeleteSeries` at :209-213. Query params:
//
//   - `deleteFiles=true` triggers the DB-row cascade
//     (episodes + episodefiles + series). Wired in 5.D5 via
//     type-assertion to [SeriesCascader] when the passed
//     SeriesWriter ALSO implements that interface. The on-disk
//     physical-file delete via DiskProvider is still DEFERRED — the
//     flag now cleans up rows in 3 tables but the .mkv/.mp4 files
//     stay on disk. Without ?deleteFiles=true the flag is absent and
//     only the series row is removed (orphaning child rows — same
//     behaviour as 5.D1a's pre-cascade path).
//   - `addImportListExclusion=true` adds the series's TVDB id +
//     title to the ImportListExclusion table so future import-list
//     syncs skip the row. Wired via type-assertion to
//     [SeriesExclusionAdder] when the impl satisfies it. Fires
//     BEFORE the delete (the exclusion needs to read TvdbID + Title
//     from the still-present series row).
//
// Returns 200 with empty `{}` body + SignalR Deleted. Note: the C#
// action signature returns `void` (200 empty body); the Go port
// emits the canonical `{}` envelope-less JSON to match the other
// 5.C-arc deletes.
func SeriesDeleteHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "series: invalid id")
			return
		}
		deleteFiles := r.URL.Query().Get("deleteFiles") == "true"
		addExclusion := r.URL.Query().Get("addImportListExclusion") == "true"
		if addExclusion {
			if adder, ok := svc.(SeriesExclusionAdder); ok {
				if eErr := adder.AddImportListExclusion(r.Context(), id); eErr != nil {
					if errors.Is(eErr, series.ErrNotFound) {
						httperr.NotFound(w, fmt.Sprintf("series %d not found", id))
						return
					}
					httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: addImportListExclusion %d: %w", id, eErr), nil)
					return
				}
			}
		}
		err = deleteSeriesWithOptionalCascade(r.Context(), svc, id, deleteFiles)
		if errors.Is(err, series.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("series %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: delete %d: %w", id, err), nil)
			return
		}
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "deleted", func() error {
			return emitter.Deleted(r.Context(), SeriesResource{ID: id})
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// deleteSeriesWithOptionalCascade is the shared cascade-gated
// delete path used by SeriesDeleteHandler + SeriesEditorDeleteHandler.
// Type-asserts svc to [SeriesCascader] when deleteFiles is true; if
// the impl satisfies the cascade interface we get the multi-table
// cleanup, otherwise we fall through to plain svc.Delete (matches
// the 5.D1a parsed-and-ignored behaviour for fakes / older test
// surfaces that don't implement SeriesCascader).
func deleteSeriesWithOptionalCascade(ctx context.Context, svc SeriesWriter, id int, deleteFiles bool) error {
	if deleteFiles {
		if c, ok := svc.(SeriesCascader); ok {
			return c.DeleteCascade(ctx, id)
		}
	}
	return svc.Delete(ctx, id)
}

func decodeSeriesBody(r *http.Request) (SeriesResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out SeriesResource
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&out); err != nil {
		return SeriesResource{}, err
	}
	return out, nil
}

// validateSeriesResource ports the minimal C# SeriesValidator subset
// applied to this endpoint. Title/Path/QualityProfileId are the
// "must be supplied" rules — the richer rules (path-must-be-absolute,
// rootfolder-must-exist, qualityProfile-must-exist) are DEFERRED
// until the AddSeriesService pipeline ports.
func validateSeriesResource(body SeriesResource) []httperr.Failure {
	failures := make([]httperr.Failure, 0, 3)
	if strings.TrimSpace(body.Title) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Title", ErrorMessage: "'Title' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.Path) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Path", ErrorMessage: "'Path' must not be empty.", Severity: "error"})
	}
	if body.QualityProfileID <= 0 {
		failures = append(failures, httperr.Failure{PropertyName: "QualityProfileId", ErrorMessage: "'Quality Profile' must be selected.", Severity: "error"})
	}
	return failures
}

// fromSeriesResource is the inverse of ToSeriesResource. Maps the
// wire body to a series.Series row. The opaque-JSON fields (Images,
// Seasons, Ratings, Actors, Genres-as-JSON, AddOptions) and the
// AlternateTitles join table are NOT round-tripped — `existing`
// provides the fallback on PUT; on POST they default to "" / nil.
//
// Enum string→int conversion via the inverse helpers below.
func fromSeriesResource(res SeriesResource, existing *series.Series) *series.Series {
	row := &series.Series{
		ID:                res.ID,
		TvdbID:            res.TvdbID,
		TvRageID:          res.TvRageID,
		ImdbID:            res.ImdbID,
		Title:             res.Title,
		TitleSlug:         res.TitleSlug,
		CleanTitle:        res.CleanTitle,
		Status:            seriesStatusFromName(res.Status),
		Overview:          res.Overview,
		AirTime:           res.AirTime,
		Path:              res.Path,
		Monitored:         res.Monitored,
		SeasonFolder:      res.SeasonFolder,
		Runtime:           res.Runtime,
		SeriesType:        seriesTypeFromName(res.SeriesType),
		Network:           res.Network,
		UseSceneNumbering: res.UseSceneNumbering,
		Year:              res.Year,
		Certification:     res.Certification,
		SortTitle:         res.SortTitle,
		QualityProfileID:  res.QualityProfileID,
		Tags:              res.Tags,
		TvMazeID:          res.TvMazeID,
		MonitorNewItems:   monitorNewItemsFromName(res.MonitorNewItems),
		TmdbID:            res.TmdbID,
		RootFolderPath:    res.RootFolderPath,
	}
	if row.Tags == nil {
		row.Tags = []int{}
	}
	if existing != nil {
		// Carry forward the opaque-JSON columns + the time-typed
		// columns the wire SeriesResource doesn't represent yet.
		row.Images = existing.Images
		row.Seasons = existing.Seasons
		row.Actors = existing.Actors
		row.Ratings = existing.Ratings
		row.Genres = existing.Genres
		row.AddOptions = existing.AddOptions
		row.FirstAired = existing.FirstAired
		row.NextAiring = existing.NextAiring
		row.LastAired = existing.LastAired
		row.LastInfoSync = existing.LastInfoSync
		row.LastDiskSync = existing.LastDiskSync
		row.Added = existing.Added
		row.OriginalLanguage = existing.OriginalLanguage
		row.MalIDs = existing.MalIDs
		row.AniListIDs = existing.AniListIDs
		row.OriginalCountry = existing.OriginalCountry
	}
	return row
}

// seriesStatusFromName is the inverse of seriesStatusName.
func seriesStatusFromName(s string) int {
	switch strings.ToLower(s) {
	case "continuing":
		return series.SeriesStatusContinuing
	case "ended":
		return series.SeriesStatusEnded
	case "upcoming":
		return series.SeriesStatusUpcoming
	case "deleted":
		return series.SeriesStatusDeleted
	}
	return series.SeriesStatusContinuing
}

// seriesTypeFromName is the inverse of seriesTypeName.
func seriesTypeFromName(t string) int {
	switch strings.ToLower(t) {
	case "standard":
		return series.SeriesTypeStandard
	case "daily":
		return series.SeriesTypeDaily
	case "anime":
		return series.SeriesTypeAnime
	}
	return series.SeriesTypeStandard
}

// monitorNewItemsFromName is the inverse of monitorNewItemsName.
func monitorNewItemsFromName(m string) int {
	switch strings.ToLower(m) {
	case "all":
		return 0
	case "none":
		return 1
	}
	return 0
}

// seriesStatusName mirrors the C# JsonStringEnumConverter +
// CamelCase applied to `SeriesStatusType`:
//
//	0 → continuing, 1 → ended, 2 → upcoming, 3 → deleted
func seriesStatusName(s int) string {
	switch s {
	case 0:
		return "continuing"
	case 1:
		return "ended"
	case 2:
		return "upcoming"
	case 3:
		return "deleted"
	}
	return "continuing"
}

// seriesTypeName mirrors `SeriesTypes` camelCase mapping:
//
//	0 → standard, 1 → daily, 2 → anime
func seriesTypeName(t int) string {
	switch t {
	case 0:
		return "standard"
	case 1:
		return "daily"
	case 2:
		return "anime"
	}
	return "standard"
}

// monitorNewItemsName mirrors `NewItemMonitorTypes` camelCase
// mapping. C# enum at `MonitorNewItems.cs`:
//
//	0 → all, 1 → none
func monitorNewItemsName(m int) string {
	switch m {
	case 0:
		return "all"
	case 1:
		return "none"
	}
	return "all"
}
