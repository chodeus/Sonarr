package metadatasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// skyhook.go ports `SkyHookProxy` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/SkyHookProxy.cs:21-374`.
//
// Slice 4.AT (sub-arc M3a of queue [M3]) — the GetSeriesInfo arm plus
// the full Map* pipeline (MapSeries / MapEpisode / MapSeason / MapImage
// / MapActor / MapRatings / MapSeriesStatus / MapCoverType). The
// SearchForNewSeries arm + 4 typed-id wrappers + the prefix short-
// circuit land in slice 4.AU (sub-arc M3b).
//
// Wiring model:
//
//   - [Proxy] is a singleton DI registration in C# (`Bootstrap`). The
//     Go-port mirrors that — main.go constructs one [Proxy] and
//     injects it into the call-sites (RefreshSeriesService,
//     SeriesController.Lookup, ImportListSyncService's IDTranslator
//     hook). Sub-arc M3b adds the SeriesService dep needed by the
//     prefix short-circuit; this slice only needs HTTP + DailySeries.
//
//   - DailySeriesService is a narrow nil-tolerant interface — when
//     nil, all series default to non-Daily (SeriesType=Standard). The
//     C# tree always wires a real `IDailySeriesService` but the Go
//     port keeps the dep optional so unit tests don't need the
//     daily-series persistence ports just to exercise GetSeriesInfo.
//
//   - HTTP transport is a plain *http.Client — production wiring binds
//     the shared internal/httpclient instance. The C# tree uses
//     `IHttpClient` with the SuppressHttpError + AllowAutoRedirect
//     flags (`:50-51`); Go's net/http honours redirects by default
//     and we read the response body unconditionally so the "suppress"
//     equivalent is just status-code branching at the call site.
//
//   - URL construction: the C# `SonarrCloudRequestBuilder.SkyHookTvdb`
//     expands `{base}/v1/tvdb/{route}/en/` with `.Resource(id)` and
//     `.AddQueryParam`. The Go port takes a `baseURL` string of the
//     form `https://skyhook.sonarr.tv/v1/tvdb` (no trailing slash,
//     no `{route}` template) and assembles the per-call URL
//     internally — `BaseURL/{route}/en/{resource}/` for the shows
//     endpoint, `BaseURL/{route}/en/?term=...` for the search
//     endpoint (slice 4.AU). PROV-Q30 cross-cite: in production the
//     baseURL flows from a config-or-env-or-hardcoded source, with
//     `https://skyhook.sonarr.tv/v1/tvdb` as the default.
//
// Goroutine-safety: Proxy is stateless after construction. The
// underlying *http.Client is goroutine-safe by net/http contract.

// DailySeriesService narrows the C# `IDailySeriesService` to the one
// method the SkyHookProxy reaches for — the
// `_dailySeriesService.IsDailySeries(series.TvdbId)` gate at C#
// `SkyHookProxy.cs:249`. nil-tolerant by design: see [Proxy.MapSeries]
// for the fallback path.
type DailySeriesService interface {
	// IsDailySeries returns true when the TVDB id appears in the
	// DataAugmentation/DailySeries persistence table. Drives the
	// `SeriesType = Daily` override at MapSeries.
	IsDailySeries(tvdbID int) bool
}

// SeriesLookup narrows the C# `ISeriesService` to the one method the
// SkyHookProxy reaches for — `_seriesService.FindByTvdbId(tvdbId)`.
//
// Used by two arms of the search path (slice 4.AU, sub-arc M3b):
//
//   - The `tvdb:` / `tvdbid:` prefix short-circuit at C# `SkyHookProxy.cs:128-141`
//     — when the user types `tvdbid:81189` the proxy returns the
//     existing local Series row if Sonarr already knows about it,
//     else falls back to a fresh GetSeriesInfo + map pass.
//   - MapSearchResult at C# `:170-180` — when the upstream search
//     returns a TVDB id the local DB already has a Series row for,
//     return the local row so the UI shows "this is already in your
//     library".
//
// nil-tolerant: when the proxy isn't wired with a SeriesLookup (the
// unit-test path), both arms skip the lookup and always map fresh.
//
// Error semantics: implementations should return
// [series.ErrNotFound] (or `errors.Is`-compatible) when the TVDB id
// has no local row — the C# tree models this as a null return; the
// Go port uses the sentinel + a (nil, nil) "ok-but-empty" contract
// is NOT used since it conflicts with the typed err signature
// callers expect from the rest of the series package.
type SeriesLookup interface {
	// GetByTvdbID returns the local Series row with the given TVDB
	// id, or wraps [series.ErrNotFound] when no local row exists.
	// Implementations should NOT return a fresh ShowResource-mapped
	// Series — only the existing persisted row.
	GetByTvdbID(ctx context.Context, tvdbID int) (*series.Series, error)
}

// Proxy is the SkyHook concrete metadata-source provider — the only
// concrete [SeriesInfoProvider]+[SeriesSearcher] impl in Sonarr's tree.
//
// Construct via [NewProxy], inject into the call-sites that resolve
// TVDB ids into Series records.
//
// Compile-time interface guards live at package init (the var _
// assignment block at the bottom of this file).
type Proxy struct {
	httpClient   *http.Client
	baseURL      string
	dailySeries  DailySeriesService
	seriesLookup SeriesLookup
	log          *slog.Logger
}

// NewProxy constructs a Proxy with the supplied dependencies. nil
// httpClient or empty baseURL is a programmer error and returns a
// typed error; dailySeries == nil and seriesLookup == nil are both
// permitted (MapSeries treats all series as non-Daily when daily is
// nil; the search arms skip the local-existing-series shortcut when
// seriesLookup is nil).
func NewProxy(httpClient *http.Client, baseURL string, dailySeries DailySeriesService, seriesLookup SeriesLookup, log *slog.Logger) (*Proxy, error) {
	if httpClient == nil {
		return nil, errors.New("metadatasource: NewProxy: httpClient is nil")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("metadatasource: NewProxy: baseURL is empty")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{
		httpClient:   httpClient,
		baseURL:      strings.TrimRight(baseURL, "/"),
		dailySeries:  dailySeries,
		seriesLookup: seriesLookup,
		log:          log,
	}, nil
}

// GetSeriesInfo ports `SkyHookProxy.GetSeriesInfo(int tvdbSeriesId)`
// at C# `SkyHookProxy.cs:43-71`.
//
// Issues `GET <baseURL>/shows/en/<tvdbSeriesID>/` against the SkyHook
// proxy, decodes the response as ShowResource, then maps the
// resource into a freshly-allocated series.Series + the per-episode
// slice via [mapSeries] / [mapEpisode].
//
// Errors:
//
//   - Upstream 404 → returns nil, nil, *Error wrapping
//     [ErrSeriesNotFound] with the C# message shape
//     "series with TvDb Id N was not found...".
//   - Any other transport-layer failure (network error, non-2xx
//     status, decode error) → returns nil, nil, *Error wrapping
//     [ErrServiceUnavailable]. The Cause chain preserves the
//     underlying error for log diagnostics.
//   - ctx.Err() propagates verbatim (no sentinel wrapping) so callers
//     can distinguish "cancelled" from "skyhook unavailable".
func (p *Proxy) GetSeriesInfo(ctx context.Context, tvdbSeriesID int) (*series.Series, []episodes.Episode, error) {
	if p == nil {
		return nil, nil, errors.New("metadatasource: Proxy is nil")
	}

	reqURL := fmt.Sprintf("%s/shows/en/%d/", p.baseURL, tvdbSeriesID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: build request for tvdbId %d failed", tvdbSeriesID),
			err,
		)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: HTTP request for tvdbId %d failed", tvdbSeriesID),
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, NewSeriesNotFoundError(tvdbSeriesID)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: HTTP %d for tvdbId %d", resp.StatusCode, tvdbSeriesID),
			nil,
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: read body for tvdbId %d failed", tvdbSeriesID),
			err,
		)
	}

	var show ShowResource
	if err := json.Unmarshal(body, &show); err != nil {
		return nil, nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: decode body for tvdbId %d failed", tvdbSeriesID),
			err,
		)
	}

	mapped, err := mapSeries(&show, p.dailySeries)
	if err != nil {
		return nil, nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: map series for tvdbId %d failed", tvdbSeriesID),
			err,
		)
	}

	eps := make([]episodes.Episode, 0, len(show.Episodes))
	for i := range show.Episodes {
		ep, err := mapEpisode(&show.Episodes[i])
		if err != nil {
			return nil, nil, NewServiceUnavailableError(
				fmt.Sprintf("metadatasource: map episode for tvdbId %d failed", tvdbSeriesID),
				err,
			)
		}
		eps = append(eps, ep)
	}

	return mapped, eps, nil
}

// mapSeries ports `MapSeries(ShowResource show)` at C#
// `SkyHookProxy.cs:182-260`. Translates the wire-format ShowResource
// into a fresh series.Series with every column populated per C#
// semantics.
//
// Embedded-JSON columns (Seasons / Actors / Ratings / Genres /
// Images) are marshalled via datastore.MarshalEmbedded so the value
// the persistence layer reads back through the Bind/Scan path is
// well-formed. Empty input slices marshal to "[]" which the
// persistence layer's default-JSON treats as a non-null embedded
// list (matches C# default-empty-list constructor behaviour).
//
// Documented divergences from C#:
//
//   - C# top-level `series.Monitored = true` is preserved as-is —
//     RefreshSeriesService and the add-series UI both override this
//     on add per MD-Q6.
//   - C# `series.SeriesType = SeriesTypes.Daily` only fires when
//     [DailySeriesService.IsDailySeries] returns true; nil
//     [Proxy.dailySeries] (test path) treats as Standard.
//   - C# `IsoLanguages.Find(...)?.Language ?? Language.English`
//     falls back to English when the iso lookup returns null. The Go
//     port honours the same fallback path.
//   - C# `series.Year = series.FirstAired.Value.Year` only fires when
//     FirstAired is non-null. Series.Year is *int in Go (matches the
//     persistence schema's nullable Year column); the Go port leaves
//     Year nil when FirstAired is omitted.
//
// MapSearchResult (C# `:170-180`) is NOT ported here — it lives in
// the [M3b] sub-arc with the SearchForNewSeries path that needs the
// SeriesLookup dep. mapSeries is the "fresh map" form; the
// "existing-series-first" form belongs with the lookup wiring.
func mapSeries(show *ShowResource, dailySeries DailySeriesService) (*series.Series, error) {
	s := &series.Series{
		TvdbID:     show.TvdbID,
		ImdbID:     show.ImdbID,
		MalIDs:     show.MalIDs,
		AniListIDs: show.AniListIDs,
		Title:      show.Title,
		CleanTitle: parser.CleanSeriesTitle(show.Title),
		SortTitle:  series.NormalizeTitle(show.Title, show.TvdbID),
		Overview:   show.Overview,
		Network:    show.Network,
		TitleSlug:  show.Slug,
		Status:     mapSeriesStatus(show.Status),
		Monitored:  true, // C# `:257` — unconditional; caller overrides on add.
	}

	if show.TvRageID != nil {
		s.TvRageID = *show.TvRageID
	}
	if show.TvMazeID != nil {
		s.TvMazeID = *show.TvMazeID
	}
	if show.TmdbID != nil {
		s.TmdbID = *show.TmdbID
	}
	if show.Runtime != nil {
		s.Runtime = *show.Runtime
	}

	s.OriginalLanguage = mapOriginalLanguage(show.OriginalLanguage).ID
	s.OriginalCountry = show.OriginalCountry

	if firstAired, ok := parseSkyHookDate(show.FirstAired); ok {
		s.FirstAired = &firstAired
		yr := firstAired.Year()
		s.Year = &yr
	}
	if lastAired, ok := parseSkyHookDate(show.LastAired); ok {
		s.LastAired = &lastAired
	}

	if show.TimeOfDay != nil {
		s.AirTime = fmt.Sprintf("%02d:%02d", show.TimeOfDay.Hours, show.TimeOfDay.Minutes)
	}

	if strings.TrimSpace(show.ContentRating) != "" {
		s.Certification = strings.ToUpper(show.ContentRating)
	}

	if dailySeries != nil && dailySeries.IsDailySeries(show.TvdbID) {
		s.SeriesType = series.SeriesTypeDaily
	}

	ratings, err := datastore.MarshalEmbedded(mapRatings(show.Rating))
	if err != nil {
		return nil, fmt.Errorf("marshal Ratings: %w", err)
	}
	s.Ratings = ratings

	genres := show.Genres
	if genres == nil {
		genres = []string{}
	}
	genresJSON, err := datastore.MarshalEmbedded(genres)
	if err != nil {
		return nil, fmt.Errorf("marshal Genres: %w", err)
	}
	s.Genres = genresJSON

	actors := make([]actorPersist, 0, len(show.Actors))
	for i := range show.Actors {
		actors = append(actors, mapActor(&show.Actors[i]))
	}
	actorsJSON, err := datastore.MarshalEmbedded(actors)
	if err != nil {
		return nil, fmt.Errorf("marshal Actors: %w", err)
	}
	s.Actors = actorsJSON

	seasons := make([]seasonPersist, 0, len(show.Seasons))
	for i := range show.Seasons {
		seasons = append(seasons, mapSeason(&show.Seasons[i]))
	}
	seasonsJSON, err := datastore.MarshalEmbedded(seasons)
	if err != nil {
		return nil, fmt.Errorf("marshal Seasons: %w", err)
	}
	s.Seasons = seasonsJSON

	covers := make([]mediacover.Cover, 0, len(show.Images))
	for i := range show.Images {
		covers = append(covers, mapImage(&show.Images[i]))
	}
	imagesJSON, err := datastore.MarshalEmbedded(covers)
	if err != nil {
		return nil, fmt.Errorf("marshal Images: %w", err)
	}
	s.Images = imagesJSON

	return s, nil
}

// mapEpisode ports `MapEpisode(EpisodeResource oracleEpisode)` at C#
// `SkyHookProxy.cs:281-308`. Translates the wire-format EpisodeResource
// into an episodes.Episode, including the per-episode screenshot
// (non-empty `Image` → CoverTypeScreenshot Cover in the Images JSON).
//
// Note — C# stores TvdbId on Episode as an `int` (not nullable);
// Go's episodes.Episode persists TvdbID as *int (nullable). The
// port sets the pointer when the upstream value is non-zero.
func mapEpisode(oracle *EpisodeResource) (episodes.Episode, error) {
	ep := episodes.Episode{
		SeasonNumber:             oracle.SeasonNumber,
		EpisodeNumber:            oracle.EpisodeNumber,
		AbsoluteEpisodeNumber:    oracle.AbsoluteEpisodeNumber,
		AiredAfterSeasonNumber:   oracle.AiredAfterSeasonNumber,
		AiredBeforeSeasonNumber:  oracle.AiredBeforeSeasonNumber,
		AiredBeforeEpisodeNumber: oracle.AiredBeforeEpisodeNumber,
		Title:                    oracle.Title,
		AirDate:                  oracle.AirDate,
		AirDateUtc:               oracle.AirDateUtc,
		Runtime:                  oracle.Runtime,
		FinaleType:               oracle.FinaleType,
		Overview:                 oracle.Overview,
	}
	if oracle.TvdbID != 0 {
		id := oracle.TvdbID
		ep.TvdbID = &id
	}

	ratings, err := datastore.MarshalEmbedded(mapRatings(oracle.Rating))
	if err != nil {
		return episodes.Episode{}, fmt.Errorf("marshal Ratings: %w", err)
	}
	ep.Ratings = ratings

	covers := make([]mediacover.Cover, 0, 1)
	if strings.TrimSpace(oracle.Image) != "" {
		covers = append(covers, mediacover.Cover{
			CoverType: mediacover.CoverTypeScreenshot,
			RemoteUrl: oracle.Image,
		})
	}
	imagesJSON, err := datastore.MarshalEmbedded(covers)
	if err != nil {
		return episodes.Episode{}, fmt.Errorf("marshal Images: %w", err)
	}
	ep.Images = imagesJSON

	return ep, nil
}

// seasonPersist mirrors the C# `Season` shape persisted inside the
// Series.Seasons JSON blob. Three fields: SeasonNumber, Monitored,
// Images (each a mediacover.Cover). The Go-port doesn't have a
// shared series.Season type (see go/internal/series — Seasons is an
// opaque JSON `string` until a future slice ports the typed wrapper),
// so we declare a local persistence record here.
type seasonPersist struct {
	SeasonNumber int                `json:"seasonNumber"`
	Monitored    bool               `json:"monitored"`
	Images       []mediacover.Cover `json:"images"`
}

// mapSeason ports `MapSeason(SeasonResource)` at C# `SkyHookProxy.cs:310-318`.
// Monitored mirrors the C# `SeasonNumber > 0` rule — specials
// (SeasonNumber == 0) default un-monitored.
func mapSeason(s *SeasonResource) seasonPersist {
	out := seasonPersist{
		SeasonNumber: s.SeasonNumber,
		Monitored:    s.SeasonNumber > 0,
		Images:       make([]mediacover.Cover, 0, len(s.Images)),
	}
	for i := range s.Images {
		out.Images = append(out.Images, mapImage(&s.Images[i]))
	}
	return out
}

// actorPersist mirrors the C# `Actor` shape persisted inside the
// Series.Actors JSON blob. Same rationale as [seasonPersist] — no
// shared Go type yet.
type actorPersist struct {
	Name      string             `json:"name"`
	Character string             `json:"character"`
	Images    []mediacover.Cover `json:"images,omitempty"`
}

// mapActor ports `MapActors(ActorResource)` at C# `SkyHookProxy.cs:262-279`.
// When Image is non-empty, wraps it in a single Headshot Cover —
// matches C# `:270-276`.
func mapActor(a *ActorResource) actorPersist {
	out := actorPersist{
		Name:      a.Name,
		Character: a.Character,
	}
	if strings.TrimSpace(a.Image) != "" {
		out.Images = []mediacover.Cover{{
			CoverType: mediacover.CoverTypeHeadshot,
			RemoteUrl: a.Image,
		}}
	}
	return out
}

// mapImage ports `MapImage(ImageResource)` at C# `SkyHookProxy.cs:349-356`.
// The RemoteUrl field captures the SkyHook-served URL; the Url field
// stays empty until mediacover.Service.ConvertToLocalUrls rewrites
// it to a Sonarr-hosted path (per slice 3.BO).
func mapImage(img *ImageResource) mediacover.Cover {
	return mediacover.Cover{
		CoverType: mapCoverType(img.CoverType),
		RemoteUrl: img.URL,
	}
}

// mapCoverType ports `MapCoverType(string)` at C#
// `SkyHookProxy.cs:358-373`. Maps the SkyHook lowercase cover-type
// string to the local mediacover.CoverType enum. Unknown strings
// collapse to CoverTypeUnknown (matches the C# `default` branch).
func mapCoverType(coverType string) mediacover.CoverType {
	switch strings.ToLower(coverType) {
	case "poster":
		return mediacover.CoverTypePoster
	case "banner":
		return mediacover.CoverTypeBanner
	case "fanart":
		return mediacover.CoverTypeFanart
	case "clearlogo":
		return mediacover.CoverTypeClearlogo
	default:
		return mediacover.CoverTypeUnknown
	}
}

// ratingsPersist mirrors the C# `Ratings` shape (Votes + Value) used
// inside Series.Ratings + Episode.Ratings JSON columns. The C#
// Ratings constructor leaves both fields at default zero when given
// a null wire value — [mapRatings] preserves that semantic.
type ratingsPersist struct {
	Votes int     `json:"votes"`
	Value float64 `json:"value"`
}

// mapRatings ports `MapRatings(RatingResource)` at C#
// `SkyHookProxy.cs:335-347`. nil RatingResource → zero Ratings (the
// C# `new Ratings()` empty-constructor path).
func mapRatings(r *RatingResource) ratingsPersist {
	if r == nil {
		return ratingsPersist{}
	}
	return ratingsPersist{
		Votes: r.Count,
		Value: r.Value,
	}
}

// mapSeriesStatus ports `MapSeriesStatus(string)` at C#
// `SkyHookProxy.cs:320-333`. Case-insensitive comparison; the
// "ended" / "upcoming" check matches the C# `Equals(...,
// StringComparison.InvariantCultureIgnoreCase)` call. Anything else
// — including the default empty-string from the wire — maps to
// Continuing.
func mapSeriesStatus(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ended":
		return series.SeriesStatusEnded
	case "upcoming":
		return series.SeriesStatusUpcoming
	default:
		return series.SeriesStatusContinuing
	}
}

// mapOriginalLanguage ports the C# lookup at
// `SkyHookProxy.cs:209-211` —
//
//	show.OriginalLanguage.IsNotNullOrWhiteSpace() ?
//	    IsoLanguages.Find(show.OriginalLanguage.ToLower())?.Language ?? Language.English :
//	    Language.English
//
// Three-way: blank/whitespace → English; ISO-lookup hit → that
// Language; ISO-lookup miss → English. PROV-Q35 documents the
// "fallback always English" design — we mirror exactly until a
// future slice elects to introduce a Language.Unknown distinction.
func mapOriginalLanguage(code string) language.Language {
	if strings.TrimSpace(code) == "" {
		return language.English
	}
	iso, ok := language.FindIso(strings.ToLower(code))
	if !ok {
		return language.English
	}
	return iso.Language
}

// parseSkyHookDate parses SkyHook's "yyyy-MM-dd" date strings,
// matching C# `DateTime.ParseExact(..., DateTimeStyles.AssumeUniversal
// | DateTimeStyles.AdjustToUniversal)`. Empty or unparseable input
// returns (time.Time{}, false) — the caller leaves the destination
// nullable pointer at nil.
func parseSkyHookDate(s string) (time.Time, bool) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// urlEncodeTerm is the search-term percent-encoder used by
// [Proxy.SearchForNewSeries] in slice 4.AU. The C# code calls
// `.AddQueryParam("term", title.ToLower().Trim())` which leaves
// encoding to the HttpRequestBuilder layer; in Go we encode
// manually via url.QueryEscape.
func urlEncodeTerm(term string) string {
	return url.QueryEscape(strings.TrimSpace(strings.ToLower(term)))
}

// looksLikePath ports the C# `IsPathValid(PathValidationType.AnyOs)`
// check the SearchForNewSeries guard uses to reject path-injection
// attempts. C# `PathExtensions.cs:143-184` defines AnyOs as
// `IsPathValidForWindows OR IsPathValidForNonWindows`:
//
//   - Non-Windows: starts with `/`.
//   - Windows: starts with `\\` (UNC) or matches the drive-letter
//     regex `^[a-zA-Z]:\\` (e.g. `C:\foo`).
//
// The Go port matches the same shape — we don't import an OS-specific
// path validator because the guard is "does this look like ANY
// filesystem path?" not "is this a valid path on the current OS?".
func looksLikePath(s string) bool {
	if s == "" {
		return false
	}
	// Non-Windows path: leading "/".
	if s[0] == '/' {
		return true
	}
	// Windows UNC path: leading "\\".
	if len(s) >= 2 && s[0] == '\\' && s[1] == '\\' {
		return true
	}
	// Windows drive-letter path: "[a-zA-Z]:[\\/]".
	if len(s) >= 3 && isASCIILetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	return false
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// Compile-time guards: Proxy implements both metadata-source
// interfaces. The [SeriesSearcher] surface is implemented in
// skyhook_search.go (sub-arc M3b).
var (
	_ SeriesInfoProvider = (*Proxy)(nil)
	_ SeriesSearcher     = (*Proxy)(nil)
)
