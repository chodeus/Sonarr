package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/series"
)

// EpisodeResource mirrors
// `src/Sonarr.Api.V3/Episodes/EpisodeResource.cs`. The scalar
// projection + the includeEpisodeFile (J340/J342) + includeImages
// (J371) + includeSeries (J372) sub-resource branches are all wired.
// Empty-DB recording has zero records so the populated-row branches are
// unobservable today.
//
// Field-source mapping (C# → Go), passthrough subset:
//
//	id, seriesId, tvdbId, episodeFileId, seasonNumber, episodeNumber
//	title, airDate, airDateUtc, lastSearchTime, runtime, overview
//	hasFile, monitored, absolute/sceneAbsolute/scene* numbering,
//	unverifiedSceneNumbering
//
// finaleType is a persisted passthrough column (mapped in
// ToEpisodeResource). episodeFile is wired via the includeEpisodeFile
// branch (J340/J342). images is wired via the includeImages branch
// (J371 — `*[]MediaCoverResource`, pointer-to-slice 3-state: nil when
// includeImages=false → key absent; `&[]` / populated when true,
// matching C# `resource.Images = episode.Images` direct assignment,
// no local-URL rewrite — unlike the series path).
//
// series is wired via the includeSeries branch (J372 — `*SeriesResource`,
// pointer 3-state: nil when includeSeries=false → key absent; populated
// when true + the lister/getter implements EpisodeSeriesLookup). The
// embed is the BASE series projection (apiv3.ToSeriesResource) with NO
// MapCoversToLocal / LinkStatistics, mirroring C# `resource.Series =
// series.ToResource()` (base ToResource, not the enriched controller
// shape).
//
// J340 widening: EpisodeFile *EpisodeFileResource subresource
// pointer field (J302 pattern). C# v3 declares
// `EpisodeFileResource EpisodeFile` (non-nullable) at v3
// EpisodeResource.cs:25; v5 declares `EpisodeFileResource?
// EpisodeFile` (nullable) at v5 EpisodeResource.cs:24. Populated
// in C# when the request's `includeEpisodeFile=true` query branch
// is set (per the calendar/wanted/episode handlers' joining
// logic). Go-port handlers DEFERRED that branch — the field stays
// nil today and omitempty drops the key. When the populated-mapper
// for the EpisodeResource path lands, it allocates via
// `apiv3.ToEpisodeFileResource(...)` and routes through the J339
// populator wrapper for the inner CustomFormats projection.
//
// J327 widening: 3 new pointer fields — EndTime + GrabDate (both
// v5-additive `DateTime?` at v5 EpisodeResource.cs:30-31) +
// Grabbed (both v3+v5 — `bool` with `[JsonIgnore(Condition =
// JsonIgnoreCondition.WhenWritingDefault)]` at v3 :41 / v5 :40,
// equivalent to Go `*bool` + omitempty). All 3 use omitempty:
// nil/empty drops the key. The Go-port persisted Episode model
// (internal/episodes/episodes.go:36-70) doesn't carry EndTime
// (computed = airDateUtc + runtime in C#) or GrabDate (set by
// the grab-event handler when an episode is grabbed). Both stay
// nil today; the populated-mapper port will allocate when those
// computations land.
type EpisodeResource struct {
	ID                         int                   `json:"id"`
	SeriesID                   int                   `json:"seriesId"`
	TvdbID                     int                   `json:"tvdbId"`
	EpisodeFileID              int                   `json:"episodeFileId"`
	SeasonNumber               int                   `json:"seasonNumber"`
	EpisodeNumber              int                   `json:"episodeNumber"`
	Title                      string                `json:"title"`
	AirDate                    string                `json:"airDate,omitempty"`
	AirDateUTC                 *time.Time            `json:"airDateUtc,omitempty"`
	LastSearchTime             *time.Time            `json:"lastSearchTime,omitempty"`
	Runtime                    int                   `json:"runtime"`
	FinaleType                 string                `json:"finaleType,omitempty"`
	Overview                   string                `json:"overview,omitempty"`
	HasFile                    bool                  `json:"hasFile"`
	Monitored                  bool                  `json:"monitored"`
	AbsoluteEpisodeNumber      *int                  `json:"absoluteEpisodeNumber,omitempty"`
	SceneAbsoluteEpisodeNumber *int                  `json:"sceneAbsoluteEpisodeNumber,omitempty"`
	SceneEpisodeNumber         *int                  `json:"sceneEpisodeNumber,omitempty"`
	SceneSeasonNumber          *int                  `json:"sceneSeasonNumber,omitempty"`
	UnverifiedSceneNumbering   bool                  `json:"unverifiedSceneNumbering"`
	EndTime                    *time.Time            `json:"endTime,omitempty"`
	GrabDate                   *time.Time            `json:"grabDate,omitempty"`
	Grabbed                    *bool                 `json:"grabbed,omitempty"`
	EpisodeFile                *EpisodeFileResource  `json:"episodeFile,omitempty"`
	Images                     *[]MediaCoverResource `json:"images,omitempty"`
	Series                     *SeriesResource       `json:"series,omitempty"`
}

// EpisodeBySeriesLister narrows the read surface to the
// seriesId-filtered path the C# controller's seriesId branch uses.
// *episodes.Service.GetEpisodesBySeries satisfies it.
type EpisodeBySeriesLister interface {
	GetEpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)
}

// The three optional-extension listers below back the non-seriesId
// branches of `EpisodeController.GetEpisodes` (seasonNumber /
// episodeIds / episodeFileId). They are optional so the narrow test
// fixtures (which only implement EpisodeBySeriesLister) keep compiling;
// production passes the episodesWithEpisodeFileLookup composite whose
// embedded *episodes.Service satisfies all three.
type episodesBySeasonLister interface {
	GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error)
}

type episodesByIDsLister interface {
	GetEpisodes(ctx context.Context, ids []int) ([]episodes.Episode, error)
}

type episodesByFileLister interface {
	GetEpisodesByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// parseEpisodeIDList flattens repeated `episodeIds=1&episodeIds=2`
// query values (each value optionally comma-separated) into an int
// slice, matching ASP.NET's `[FromQuery]List<int>` binding plus the
// comma-list tolerance some clients use.
func parseEpisodeIDList(vals []string) ([]int, error) {
	out := make([]int, 0, len(vals))
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// EpisodeFileLookupForEpisode is the optional-extension interface
// for the Episode handlers. When the handler's lister/getter
// parameter ALSO implements this interface AND the `includeEpisodeFile`
// query param is true, the handler attaches the episode's
// EpisodeFile subresource (J340 wire-shape pin) via the standard
// J339 populator chain (ToEpisodeFileResource → ApplyEpisodeFilePopulator).
//
// nil-safe per the helper. Test fixtures don't implement the
// extension so the empty fallback fires (the J340 omitempty pointer
// stays nil → key absent).
//
// J342.
type EpisodeFileLookupForEpisode interface {
	GetFile(ctx context.Context, id int) (*episodefiles.File, error)
}

// applyEpisodeFileSubresource conditionally populates an EpisodeResource's
// EpisodeFile field when ALL of: (a) includeEpisodeFile=true; (b)
// the lister/svc parameter implements EpisodeFileLookupForEpisode;
// (c) the episode has a non-zero EpisodeFileID. The looked-up file
// is projected via ToEpisodeFileResource + optionally enriched via
// ApplyEpisodeFilePopulator when the lister/svc ALSO implements
// EpisodeFilePopulator. nil-safe everywhere.
//
// J342.
func applyEpisodeFileSubresource(ctx context.Context, res *EpisodeResource, ep *episodes.Episode, lister any, includeEpisodeFile bool) {
	if res == nil || ep == nil || !includeEpisodeFile || ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
		return
	}
	lookup, ok := lister.(EpisodeFileLookupForEpisode)
	if !ok {
		return
	}
	f, err := lookup.GetFile(ctx, *ep.EpisodeFileID)
	if err != nil || f == nil {
		return
	}
	epFile := ToEpisodeFileResource(*f, "")
	ApplyEpisodeFilePopulator(ctx, &epFile, f, lister)
	res.EpisodeFile = &epFile
}

// ApplyEpisodeFileSubresource is the exported wrapper over
// applyEpisodeFileSubresource so out-of-package list adapters (e.g.
// cmd/sonarr's wantedRepoLister, J384) can reuse the same conditional
// EpisodeFile-embed projection the v3 episode handlers use. The lister
// arg must implement EpisodeFileLookupForEpisode (+ optionally
// EpisodeFilePopulator). Semantics are identical.
func ApplyEpisodeFileSubresource(ctx context.Context, res *EpisodeResource, ep *episodes.Episode, lister any, includeEpisodeFile bool) {
	applyEpisodeFileSubresource(ctx, res, ep, lister, includeEpisodeFile)
}

// applyEpisodeImages populates an EpisodeResource's Images field from
// the persisted Episode.Images blob when includeImages is true. Mirrors
// C# EpisodeControllerWithSignalR.MapToResource `resource.Images =
// episode.Images` — a direct assignment of the model's MediaCover list
// with NO local-URL rewrite (the episode path differs from the series
// path here). Pointer-to-slice 3-state (J302): nil → key absent;
// `&[]MediaCoverResource{}` → `"images":[]` (includeImages=true, no
// images, matching the C# non-null List default); populated → the list.
//
// J371.
func applyEpisodeImages(res *EpisodeResource, e episodes.Episode, includeImages bool) {
	if res == nil || !includeImages {
		return
	}
	imgs := decodeMediaCovers(e.Images)
	res.Images = &imgs
}

// EpisodeSeriesLookup is the optional-extension interface for the
// includeSeries branch. When the handler's lister/getter parameter ALSO
// implements this interface AND `includeSeries=true`, the handler embeds
// the episode's owning series as a BASE SeriesResource. *series.Service
// satisfies it; the cmd/sonarr episode composite forwards GetSeries to
// the production series service.
//
// Test fixtures don't implement the extension so the empty fallback
// fires (the J372 omitempty pointer stays nil → key absent).
//
// J372.
type EpisodeSeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// applyEpisodeSeries conditionally populates an EpisodeResource's Series
// field when ALL of: (a) includeSeries=true; (b) the lister/svc
// parameter implements EpisodeSeriesLookup; (c) the episode has a
// non-zero SeriesID. The looked-up series is projected via the BASE
// apiv3.ToSeriesResource (NO MapCoversToLocal / LinkStatistics — mirrors
// C# EpisodeControllerWithSignalR.MapToResource `resource.Series =
// series.ToResource()`, the base ToResource not the enriched controller
// shape).
//
// cache (optional) dedupes the series→resource lookup across list rows:
// the list path repeats series ids, and C# keeps a per-request seriesDict
// for exactly this. A nil cache disables it (the by-id path passes nil).
// The cached pointer is shared across rows with the same seriesId; that
// is safe because the embed is read-only projection output.
//
// nil-safe everywhere.
//
// J372.
func applyEpisodeSeries(ctx context.Context, res *EpisodeResource, seriesID int, lister any, includeSeries bool, cache map[int]*SeriesResource) {
	if res == nil || !includeSeries || seriesID == 0 {
		return
	}
	lookup, ok := lister.(EpisodeSeriesLookup)
	if !ok {
		return
	}
	if cache != nil {
		if cached, hit := cache[seriesID]; hit {
			res.Series = cached
			return
		}
	}
	s, err := lookup.GetSeries(ctx, seriesID)
	if err != nil || s == nil {
		return
	}
	sr := ToSeriesResource(*s)
	res.Series = &sr
	if cache != nil {
		cache[seriesID] = &sr
	}
}

// ApplyEpisodeSeries is the exported wrapper over applyEpisodeSeries so
// out-of-package list adapters (e.g. cmd/sonarr's wantedRepoLister, J383)
// can reuse the same conditional BASE-Series-embed projection the v3
// episode handlers use. Semantics are identical.
func ApplyEpisodeSeries(ctx context.Context, res *EpisodeResource, seriesID int, lister any, includeSeries bool, cache map[int]*SeriesResource) {
	applyEpisodeSeries(ctx, res, seriesID, lister, includeSeries, cache)
}

// ApplyEpisodeImages is the exported wrapper over applyEpisodeImages so
// out-of-package list adapters (J383) can reuse the includeImages
// projection. Semantics are identical.
func ApplyEpisodeImages(res *EpisodeResource, e episodes.Episode, includeImages bool) {
	applyEpisodeImages(res, e, includeImages)
}

// EpisodeListHandler returns the http.Handler for GET /api/v3/episode.
// Mirrors `EpisodeController.GetEpisodes` at
// `src/Sonarr.Api.V3/Episodes/EpisodeController.cs:28-49`, which
// dispatches in precedence order:
//
//	seriesId + seasonNumber → GetEpisodesBySeason
//	seriesId                → GetEpisodeBySeries
//	episodeIds[]            → GetEpisodes(ids)
//	episodeFileId          → GetEpisodesByFileId
//	(none)                 → 400 BadRequest
//
// The seasonNumber/episodeIds/episodeFileId branches use optional-
// extension listers (see episodesBySeasonLister et al.); production's
// composite satisfies all of them. Missing every selector returns 400
// to match C# `throw new BadRequestException("seriesId or episodeIds
// must be provided")`.
func EpisodeListHandler(lister EpisodeBySeriesLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ctx := r.Context()
		var rows []episodes.Episode
		var err error
		switch {
		case q.Get("seriesId") != "":
			seriesID, convErr := strconv.Atoi(q.Get("seriesId"))
			if convErr != nil {
				httperr.BadRequest(w, "invalid seriesId")
				return
			}
			if seasonStr := q.Get("seasonNumber"); seasonStr != "" {
				season, sErr := strconv.Atoi(seasonStr)
				if sErr != nil {
					httperr.BadRequest(w, "invalid seasonNumber")
					return
				}
				bySeason, ok := lister.(episodesBySeasonLister)
				if !ok {
					httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodes: seasonNumber filter unsupported by lister"), nil)
					return
				}
				rows, err = bySeason.GetEpisodesBySeason(ctx, seriesID, season)
			} else {
				rows, err = lister.GetEpisodesBySeries(ctx, seriesID)
			}
		case len(q["episodeIds"]) > 0:
			ids, convErr := parseEpisodeIDList(q["episodeIds"])
			if convErr != nil {
				httperr.BadRequest(w, "invalid episodeIds")
				return
			}
			byIDs, ok := lister.(episodesByIDsLister)
			if !ok {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodes: episodeIds filter unsupported by lister"), nil)
				return
			}
			rows, err = byIDs.GetEpisodes(ctx, ids)
		case q.Get("episodeFileId") != "":
			fileID, convErr := strconv.Atoi(q.Get("episodeFileId"))
			if convErr != nil {
				httperr.BadRequest(w, "invalid episodeFileId")
				return
			}
			byFile, ok := lister.(episodesByFileLister)
			if !ok {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodes: episodeFileId filter unsupported by lister"), nil)
				return
			}
			rows, err = byFile.GetEpisodesByFileID(ctx, fileID)
		default:
			httperr.BadRequest(w, "seriesId or episodeIds must be provided")
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodes: %w", err), nil)
			return
		}
		// J342: parse includeEpisodeFile query param + populate the
		// J340-pinned subresource when both the param is true and the
		// lister implements EpisodeFileLookupForEpisode.
		includeEpisodeFile := r.URL.Query().Get("includeEpisodeFile") == "true"
		includeImages := r.URL.Query().Get("includeImages") == "true"
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		seriesCache := map[int]*SeriesResource{}
		out := make([]EpisodeResource, 0, len(rows))
		for _, row := range rows {
			res := ToEpisodeResource(row)
			ep := row
			applyEpisodeFileSubresource(r.Context(), &res, &ep, lister, includeEpisodeFile)
			applyEpisodeImages(&res, row, includeImages)
			applyEpisodeSeries(r.Context(), &res, row.SeriesID, lister, includeSeries, seriesCache)
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeByIDGetter narrows episodes.Service to GetEpisode(id).
// Satisfied directly by *episodes.Service.
type EpisodeByIDGetter interface {
	GetEpisode(ctx context.Context, id int) (*episodes.Episode, error)
}

// EpisodeByIDHandler returns GET /api/v3/episode/{id}. Mirrors
// `EpisodeControllerWithSignalR.GetResourceById` at
// `EpisodeControllerWithSignalR.cs:54-59` which delegates through
// MapToResource with includeSeries+includeEpisodeFile+includeImages
// all true.
//
// includeEpisodeFile (J340/J342) + includeImages (J371) + includeSeries
// (J372) branches are all honoured.
func EpisodeByIDHandler(svc EpisodeByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "episode: invalid id")
			return
		}
		ep, err := svc.GetEpisode(r.Context(), id)
		if err != nil {
			if errors.Is(err, episodes.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("episode %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode: get %d: %w", id, err), nil)
			return
		}
		// J342: same includeEpisodeFile path as EpisodeListHandler.
		// Note: C# EpisodeControllerWithSignalR.GetResourceById ALWAYS
		// passes includeEpisodeFile=true (at C# line 54-59 — the
		// individual-episode GET hard-codes all 3 include* flags to
		// true). The Go-port honors the query-param explicitly for
		// caller flexibility; absent query-param defaults to false
		// matching the list-handler default — clients wanting the
		// C# byID populated shape send includeEpisodeFile=true.
		includeEpisodeFile := r.URL.Query().Get("includeEpisodeFile") == "true"
		includeImages := r.URL.Query().Get("includeImages") == "true"
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		res := ToEpisodeResource(*ep)
		applyEpisodeFileSubresource(r.Context(), &res, ep, svc, includeEpisodeFile)
		applyEpisodeImages(&res, *ep, includeImages)
		applyEpisodeSeries(r.Context(), &res, ep.SeriesID, svc, includeSeries, nil)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// EpisodeMonitorWriter is the write surface for PUT /episode/monitor.
// Satisfied directly by *episodes.Service.
type EpisodeMonitorWriter interface {
	EpisodeBySeriesLister
	GetEpisodes(ctx context.Context, ids []int) ([]episodes.Episode, error)
	SetEpisodeMonitored(ctx context.Context, episodeID int, monitored bool) error
	SetMonitored(ctx context.Context, ids []int, monitored bool) error
}

// episodesMonitoredBody mirrors C# `EpisodesMonitoredResource` —
// `{episodeIds: int[], monitored: bool}`.
type episodesMonitoredBody struct {
	EpisodeIDs []int `json:"episodeIds"`
	Monitored  bool  `json:"monitored"`
}

// EpisodeMonitorHandler returns PUT /api/v3/episode/monitor. Mirrors
// `EpisodeController.SetEpisodesMonitored` at `:62-78` — sets
// monitored flag for a single id or a batch, then returns the
// updated rows projected to EpisodeResource. C# returns 202 Accepted.
//
// J371: the includeImages query param is honoured (the reloaded rows
// carry their MediaCover list when includeImages=true).
func EpisodeMonitorHandler(svc EpisodeMonitorWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body episodesMonitoredBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episode/monitor: malformed body: %v", err))
			return
		}
		if len(body.EpisodeIDs) == 0 {
			httperr.BadRequest(w, "episode/monitor: episodeIds is required")
			return
		}
		ctx := r.Context()
		var err error
		if len(body.EpisodeIDs) == 1 {
			err = svc.SetEpisodeMonitored(ctx, body.EpisodeIDs[0], body.Monitored)
		} else {
			err = svc.SetMonitored(ctx, body.EpisodeIDs, body.Monitored)
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode/monitor: %w", err), nil)
			return
		}
		rows, err := svc.GetEpisodes(ctx, body.EpisodeIDs)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode/monitor: reload: %w", err), nil)
			return
		}
		includeImages := r.URL.Query().Get("includeImages") == "true"
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		seriesCache := map[int]*SeriesResource{}
		out := make([]EpisodeResource, 0, len(rows))
		for _, row := range rows {
			res := ToEpisodeResource(row)
			applyEpisodeImages(&res, row, includeImages)
			applyEpisodeSeries(ctx, &res, row.SeriesID, svc, includeSeries, seriesCache)
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeByIDPutHandler returns PUT /api/v3/episode/{id} — the
// per-id monitored-set endpoint. Mirrors
// `EpisodeController.SetEpisodeMonitored(int id, EpisodeResource)`
// at `src/Sonarr.Api.V3/Episodes/EpisodeController.cs:51-60`:
//
//	_episodeService.SetEpisodeMonitored(id, resource.Monitored);
//	return Accepted(resource);
//
// C# echoes the input resource with 202 Accepted; the Go-port sets the
// flag then re-fetches and returns the canonical EpisodeResource (a
// strictly-more-correct body that still agrees on Monitored) with the
// same 202 status. Body is decoded leniently — only `monitored` is
// read — so a full client EpisodeResource payload round-trips without
// a DisallowUnknownFields rejection. 400 on invalid id / malformed
// body; 404 on missing episode; 500 on service error.
func EpisodeByIDPutHandler(svc EpisodeMonitorWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "episode: invalid id")
			return
		}
		var body struct {
			Monitored bool `json:"monitored"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episode: malformed body: %v", err))
			return
		}
		ctx := r.Context()
		if err := svc.SetEpisodeMonitored(ctx, id, body.Monitored); err != nil {
			if errors.Is(err, episodes.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("episode %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode: SetEpisodeMonitored %d: %w", id, err), nil)
			return
		}
		rows, err := svc.GetEpisodes(ctx, []int{id})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode: reload %d: %w", id, err), nil)
			return
		}
		if len(rows) == 0 {
			httperr.NotFound(w, fmt.Sprintf("episode %d not found after update", id))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(ToEpisodeResource(rows[0]))
		_, _ = w.Write(buf)
	})
}

// ToEpisodeResource maps the persisted Episode to the wire shape.
// Exported so the cmd/sonarr wanted-episodes adapter can reuse the
// same projection rather than re-rolling the field copy.
// The optional embedded resources (series/episodeFile/images) are
// populated by the handler-tier branches (applyEpisodeSeries /
// applyEpisodeFileSubresource / applyEpisodeImages) when their
// include* query params fire — ToEpisodeResource itself emits the
// scalar projection only.
func ToEpisodeResource(e episodes.Episode) EpisodeResource {
	r := EpisodeResource{
		ID:                       e.ID,
		SeriesID:                 e.SeriesID,
		SeasonNumber:             e.SeasonNumber,
		EpisodeNumber:            e.EpisodeNumber,
		Title:                    e.Title,
		AirDate:                  e.AirDate,
		Runtime:                  e.Runtime,
		FinaleType:               e.FinaleType,
		Overview:                 e.Overview,
		Monitored:                e.Monitored,
		UnverifiedSceneNumbering: e.UnverifiedSceneNumbering,
	}
	if e.TvdbID != nil {
		r.TvdbID = *e.TvdbID
	}
	if e.EpisodeFileID != nil {
		r.EpisodeFileID = *e.EpisodeFileID
		r.HasFile = *e.EpisodeFileID > 0
	}
	if e.AirDateUtc != nil && !e.AirDateUtc.IsZero() {
		t := *e.AirDateUtc
		r.AirDateUTC = &t
	}
	if e.LastSearchTime != nil && !e.LastSearchTime.IsZero() {
		t := *e.LastSearchTime
		r.LastSearchTime = &t
	}
	if e.AbsoluteEpisodeNumber != nil {
		v := *e.AbsoluteEpisodeNumber
		r.AbsoluteEpisodeNumber = &v
	}
	if e.SceneAbsoluteEpisodeNumber != nil {
		v := *e.SceneAbsoluteEpisodeNumber
		r.SceneAbsoluteEpisodeNumber = &v
	}
	if e.SceneEpisodeNumber != nil {
		v := *e.SceneEpisodeNumber
		r.SceneEpisodeNumber = &v
	}
	if e.SceneSeasonNumber != nil {
		v := *e.SceneSeasonNumber
		r.SceneSeasonNumber = &v
	}
	return r
}
