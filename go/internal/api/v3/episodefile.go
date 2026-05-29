package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// EpisodeFileResource is the JSON shape produced by
// GET /api/v3/episodefile + the SignalR `episodefile/created` +
// `/updated` + `/deleted` envelopes. Mirrors
// `src/Sonarr.Api.V3/EpisodeFiles/EpisodeFileResource.cs`.
//
// This slice ships the scalar-passthrough subset the SignalR
// envelope uses. The richer joined fields — Languages (typed
// Language[] DTO), Quality (full QualityModel with revision +
// proper), CustomFormats + CustomFormatScore (decision-engine
// joins), MediaInfo (per-stream codec metadata via the
// MediaInfoResource subtype), IndexerFlags + ReleaseType + Release
// Hash + OriginalFilePath bool-checked fields — are DEFERRED to a
// follow-up slice that lands the LIST endpoint at GET /api/v3/
// episodefile (currently unwired — the recording corpus exercises
// only the 404 stack-trace path which isn't byte-eq-able from Go).
//
// Field-source mapping (C# → Go), scalar subset wired here:
//
//	id            → File.ID
//	seriesId      → File.SeriesID
//	seasonNumber  → File.SeasonNumber
//	relativePath  → File.RelativePath
//	path          → "" (computed from Series.Path + RelativePath
//	                in C#; the Go port DEFERS the join — emit empty)
//	size          → File.Size
//	dateAdded     → File.DateAdded
//	sceneName     → File.SceneName
//	releaseGroup  → File.ReleaseGroup
//
// Wired for the populated-body emit: languages (ToLanguageRefs),
// indexerFlags + releaseType (J328), quality (J373 — decode the
// persisted reduced JSON → expanded wire shape via
// ToQualityModelResource), mediaInfo (J374 — the UNION v3-flat +
// v5-stream-list projection via ToMediaInfoResource over the typed
// File.MediaInfo model), qualityCutoffNotMet (J375 — computed via the
// EpisodeFileQualityCutoffPopulator optional extension over the ported
// decisionengine.UpgradableSpecification; always emitted as a bool
// matching the C# non-nullable field). Still DEFERRED: episodeHasFile
// (per-episode rollup). The customFormats[] + customFormatScore
// fields are wired at the struct level here (J298, Wave 1
// calculator wire-through slice 3) so future populated-row mappers
// can emit the C# wire shape without re-widening; the underlying
// calculator engine
// ([sonarr.tv/sonarr/internal/customformats.Calculate] +
// [sonarr.tv/sonarr/internal/customformats/calculate_episodefile.go:ParseCustomFormatForEpisodeFile] +
// [sonarr.tv/sonarr/internal/qualityprofiles.CalculateCustomFormatScore])
// is already ported. Constructors should default CustomFormats to
// `[]CustomFormatResource{}` (not nil) so a populated row without
// matched formats emits `customFormats: []` rather than `null` —
// matches the C# Newtonsoft default for non-null
// `List<CustomFormatResource>`. This widening simultaneously
// covers the Wanted, Calendar, Episode, and EpisodeFile families
// (their EpisodeFile subresource picks it up via the shared
// projection).
type EpisodeFileResource struct {
	ID                  int                    `json:"id"`
	SeriesID            int                    `json:"seriesId"`
	SeasonNumber        int                    `json:"seasonNumber"`
	RelativePath        string                 `json:"relativePath"`
	Path                string                 `json:"path"`
	Size                int64                  `json:"size"`
	DateAdded           time.Time              `json:"dateAdded"`
	SceneName           string                 `json:"sceneName,omitempty"`
	ReleaseGroup        string                 `json:"releaseGroup,omitempty"`
	Languages           []LanguageRef          `json:"languages"`
	Quality             *QualityModelResource  `json:"quality,omitempty"`
	IndexerFlags        *int                   `json:"indexerFlags,omitempty"`
	ReleaseType         string                 `json:"releaseType,omitempty"`
	QualityCutoffNotMet bool                   `json:"qualityCutoffNotMet"`
	MediaInfo           *MediaInfoResource     `json:"mediaInfo,omitempty"`
	CustomFormats       []CustomFormatResource `json:"customFormats"`
	CustomFormatScore   int                    `json:"customFormatScore"`
}

// EpisodeFileReader is the read surface for GET /api/v3/episodefile.
// Satisfied directly by *episodefiles.Service.
type EpisodeFileReader interface {
	GetFile(ctx context.Context, id int) (*episodefiles.File, error)
	GetFiles(ctx context.Context, ids []int) ([]episodefiles.File, error)
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// SeriesExistsChecker reports whether a Series with the given id is
// persisted. Satisfied directly by *series.Service via
// [series.Service.SeriesExists] (added at 5.J180 alongside this
// interface). Defined here rather than in `internal/series` so the
// v3 API package can avoid pulling a wider surface than the
// existence check actually needs.
type SeriesExistsChecker interface {
	SeriesExists(ctx context.Context, id int) (bool, error)
}

// EpisodeFilePopulator is the optional-extension interface for the
// episodefile handlers (List + ByID + Editor + Bulk* + Update +
// Delete). When the handler's reader/writer parameter ALSO
// implements this interface, each handler invokes
// PopulateEpisodeFile per file to compute the matched
// customFormats[] + customFormatScore pair. When NOT implemented
// (typical test fixtures), the empty stub fallback applies
// (CustomFormats: []; score 0) — preserves the pre-J339 wire
// shape.
//
// Production wiring at cmd/sonarr composes *episodefiles.Service
// with a wrapper that ALSO implements PopulateEpisodeFile — the
// wrapper re-uses narrow-interface lookups + the calculator-engine
// helper at internal/customformats.ParseCustomFormatForFile. Same
// J338 optional-extension idiom.
//
// J339.
type EpisodeFilePopulator interface {
	PopulateEpisodeFile(ctx context.Context, f *episodefiles.File) (customFormats []CustomFormatResource, customFormatScore int)
}

// EpisodeFileQualityCutoffPopulator is a SECOND optional-extension
// interface for the episodefile handlers (separate from
// EpisodeFilePopulator so widening one doesn't break the other's
// implementers — the J367/J369 SeriesImagePopulator-vs-SeriesStatsPopulator
// precedent). When the handler's reader/writer parameter implements it,
// ApplyEpisodeFilePopulator computes the qualityCutoffNotMet flag —
// mirrors C# `EpisodeFileResourceMapper.ToResource`'s
// `QualityCutoffNotMet = upgradableSpecification.QualityCutoffNotMet(
// series.QualityProfile.Value, model.Quality)`. When NOT implemented
// (test fixtures), the flag defaults to false (the upgradable-spec
// service isn't reachable in those paths).
//
// Production wiring at cmd/sonarr composes *episodefiles.Service with a
// wrapper that loads the series' quality profile + runs the ported
// decisionengine.UpgradableSpecification.QualityCutoffNotMet.
//
// J375.
type EpisodeFileQualityCutoffPopulator interface {
	PopulateQualityCutoffNotMet(ctx context.Context, f *episodefiles.File) bool
}

// ApplyEpisodeFilePopulator post-processes a freshly-built
// EpisodeFileResource via the optional-extension type-assertion.
// svc is the handler's reader/writer interface (typed as `any` so
// the helper accepts any concrete type the handler holds); if it
// ALSO implements EpisodeFilePopulator, this helper fills the 2
// custom-format fields on the resource. Otherwise no-op
// (preserves the empty-stub fallback). nil-safe.
//
// J339.
func ApplyEpisodeFilePopulator(ctx context.Context, res *EpisodeFileResource, f *episodefiles.File, svc any) {
	if res == nil || f == nil || svc == nil {
		return
	}
	if pop, ok := svc.(EpisodeFilePopulator); ok && pop != nil {
		cf, score := pop.PopulateEpisodeFile(ctx, f)
		if cf != nil {
			res.CustomFormats = cf
		}
		res.CustomFormatScore = score
	}
	// J375: the qualityCutoffNotMet flag is a SEPARATE optional
	// extension (a wrapper may implement one, both, or neither), so it
	// is type-asserted independently of EpisodeFilePopulator. When
	// present, compute it; otherwise it stays false.
	if cutoff, ok := svc.(EpisodeFileQualityCutoffPopulator); ok && cutoff != nil {
		res.QualityCutoffNotMet = cutoff.PopulateQualityCutoffNotMet(ctx, f)
	}
}

// EpisodeFileListHandler returns GET /api/v3/episodefile. Mirrors
// `EpisodeFileController.GetEpisodeFiles` at `:61-92`. Required:
// `seriesId` OR `episodeFileIds` (repeated int). Returns the C#-
// shaped resource list — minus the joined fields (quality,
// languages, customFormats, mediaInfo) which the existing
// EpisodeFileResource skeleton DEFERS.
//
// Slice 5.J180 closes the contract-replay 404-vs-200 drift on the
// `seriesId` arm: when the supplied series doesn't exist, the C#
// controller's `_seriesService.GetSeries(seriesId.Value)` call at
// `:72` throws `ModelNotFoundException`, surfacing as a 404 via the
// global exception filter. The Go-port previously skipped the
// series-existence check and returned 200 + `[]` for any seriesId,
// drifting from the recorded golden (`v3.episodefile.list`'s 404
// status). Behaviour now: when seriesId is supplied, check
// existence first via [SeriesExistsChecker]; return 404 when the
// row is absent. The 200 + `[]` shape stays for the
// series-exists-but-no-files case (matches C# `files == null`
// guard at `:75-78`). The `episodeFileIds`-only arm is unchanged —
// C# `:85-90` doesn't precondition on series existence (it
// per-row joins after the fact via `GetSeries(f.Key)`).
func EpisodeFileListHandler(svc EpisodeFileReader, seriesCheck SeriesExistsChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seriesIDStr := q.Get("seriesId")
		var episodeFileIDs []int
		for _, raw := range q["episodeFileIds"] {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				httperr.BadRequest(w, fmt.Sprintf("episodefile: invalid episodeFileIds entry %q", raw))
				return
			}
			episodeFileIDs = append(episodeFileIDs, n)
		}
		if seriesIDStr == "" && len(episodeFileIDs) == 0 {
			httperr.BadRequest(w, "seriesId or episodeFileIds must be provided")
			return
		}
		var files []episodefiles.File
		if seriesIDStr != "" {
			seriesID, err := strconv.Atoi(seriesIDStr)
			if err != nil || seriesID <= 0 {
				httperr.BadRequest(w, "episodefile: invalid seriesId")
				return
			}
			exists, err := seriesCheck.SeriesExists(r.Context(), seriesID)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: series-exists %d: %w", seriesID, err), nil)
				return
			}
			if !exists {
				httperr.NotFound(w, fmt.Sprintf("Series with ID %d does not exist", seriesID))
				return
			}
			rows, err := svc.GetFilesBySeries(r.Context(), seriesID)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: by series %d: %w", seriesID, err), nil)
				return
			}
			files = rows
		} else {
			rows, err := svc.GetFiles(r.Context(), episodeFileIDs)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: by ids: %w", err), nil)
				return
			}
			files = rows
		}
		out := make([]EpisodeFileResource, 0, len(files))
		for _, f := range files {
			res := ToEpisodeFileResource(f, "")
			file := f // copy for pointer capture
			ApplyEpisodeFilePopulator(r.Context(), &res, &file, svc)
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeFileByIDHandler returns GET /api/v3/episodefile/{id}. Mirrors
// the C# RestController base GetResourceById behaviour. 404 on
// missing id.
func EpisodeFileByIDHandler(svc EpisodeFileReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "episodefile: invalid id")
			return
		}
		f, err := svc.GetFile(r.Context(), id)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("episodefile %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: get %d: %w", id, err), nil)
			return
		}
		res := ToEpisodeFileResource(*f, "")
		ApplyEpisodeFilePopulator(r.Context(), &res, f, svc)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// persistedQualityFromResource encodes the expanded REST QualityModel
// wire shape back into the persisted bare-int `{"quality":<id>,
// "revision":{...}}` JSON string that the episodefiles row stores
// (the inverse of ToEpisodeFileResource's decode). Returns ("", false)
// for a nil resource. J389 — the EpisodeFile editor Quality persist.
func persistedQualityFromResource(res *QualityModelResource) (string, bool) {
	if res == nil {
		return "", false
	}
	q, ok := quality.FindByID(res.Quality.ID)
	if !ok {
		q = quality.Quality{ID: res.Quality.ID, Name: res.Quality.Name, Resolution: res.Quality.Resolution}
	}
	m := quality.Model{
		Quality: q,
		Revision: quality.Revision{
			Version:  res.Revision.Version,
			Real:     res.Revision.Real,
			IsRepack: res.Revision.IsRepack,
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// languageIDsFromRefs maps the wire LanguageRef list to the persisted
// `[]int` id list, dropping `Original` (C# `SetPropertiesBulk` rejects
// `Language.Original`: a file can't be tagged with the synthetic
// "use series original" sentinel). J389.
func languageIDsFromRefs(refs []LanguageRef) []int {
	out := make([]int, 0, len(refs))
	for _, r := range refs {
		if r.ID == language.Original.ID {
			continue
		}
		out = append(out, r.ID)
	}
	return out
}

// EpisodeFileWriter is the write surface for DELETE /api/v3/episodefile
// + PUT /api/v3/episodefile/{id} + bulk variants. Satisfied directly
// by *episodefiles.Service.
type EpisodeFileWriter interface {
	GetFile(ctx context.Context, id int) (*episodefiles.File, error)
	GetFiles(ctx context.Context, ids []int) ([]episodefiles.File, error)
	Delete(ctx context.Context, f *episodefiles.File, reason episodefiles.DeleteMediaFileReason) error
	Update(ctx context.Context, f *episodefiles.File) error
	UpdateMany(ctx context.Context, files []*episodefiles.File) error
}

// episodeFileEditorBody mirrors C# `EpisodeFileListResource` — the
// DELETE-bulk body plus the editor's metadata override fields. Quality
// + Languages persist as of J389 (Quality decoded to the bare-int
// model column; Languages to the id list, dropping Original).
type episodeFileEditorBody struct {
	EpisodeFileIDs []int                 `json:"episodeFileIds"`
	SceneName      string                `json:"sceneName,omitempty"`
	ReleaseGroup   string                `json:"releaseGroup,omitempty"`
	Quality        *QualityModelResource `json:"quality,omitempty"`
	Languages      []LanguageRef         `json:"languages,omitempty"`
}

// EpisodeFileEditorHandler returns PUT /api/v3/episodefile/editor.
// Mirrors `EpisodeFileController.SetQuality` (the `[HttpPut("editor")]`
// overload at `:115-150`) — applies one set of metadata overrides
// (SceneName/ReleaseGroup/Quality/Languages, J389) to ALL files in the
// EpisodeFileIds list. The C# action is marked `[Obsolete]`
// in favour of PUT /bulk but is still served; the Go port mirrors
// that surface preservation.
func EpisodeFileEditorHandler(svc EpisodeFileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body episodeFileEditorBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episodefile/editor: malformed body: %v", err))
			return
		}
		if len(body.EpisodeFileIDs) == 0 {
			httperr.BadRequest(w, "episodefile/editor: episodeFileIds is required")
			return
		}
		files, err := svc.GetFiles(r.Context(), body.EpisodeFileIDs)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile/editor: get: %w", err), nil)
			return
		}
		if len(files) == 0 {
			httperr.NotFound(w, "episodefile/editor: no matching files")
			return
		}
		qualityStr, hasQuality := persistedQualityFromResource(body.Quality)
		ptrs := make([]*episodefiles.File, len(files))
		for i := range files {
			f := files[i]
			if body.SceneName != "" {
				f.SceneName = body.SceneName
			}
			if body.ReleaseGroup != "" {
				f.ReleaseGroup = body.ReleaseGroup
			}
			if hasQuality {
				f.Quality = qualityStr
			}
			if body.Languages != nil {
				f.Languages = languageIDsFromRefs(body.Languages)
			}
			ptrs[i] = &f
		}
		if err := svc.UpdateMany(r.Context(), ptrs); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile/editor: update: %w", err), nil)
			return
		}
		out := make([]EpisodeFileResource, 0, len(ptrs))
		for _, f := range ptrs {
			res := ToEpisodeFileResource(*f, "")
			ApplyEpisodeFilePopulator(r.Context(), &res, f, svc)
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeFileBulkUpdateHandler returns PUT /api/v3/episodefile/bulk.
// Mirrors `EpisodeFileController.SetPropertiesBulk` at `:182-210` —
// accepts an array of EpisodeFileResource, each entry matched by id
// against the loaded files, with per-row override application.
// Returns the updated rows projected.
func EpisodeFileBulkUpdateHandler(svc EpisodeFileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body []EpisodeFileResource
		dec := json.NewDecoder(req.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episodefile/bulk: malformed body: %v", err))
			return
		}
		if len(body) == 0 {
			httperr.BadRequest(w, "episodefile/bulk: body must be non-empty array")
			return
		}
		ids := make([]int, 0, len(body))
		byID := make(map[int]EpisodeFileResource, len(body))
		for _, entry := range body {
			if entry.ID <= 0 {
				httperr.BadRequest(w, "episodefile/bulk: each entry must include positive id")
				return
			}
			ids = append(ids, entry.ID)
			byID[entry.ID] = entry
		}
		files, err := svc.GetFiles(req.Context(), ids)
		if err != nil {
			httperr.WriteErr(w, req, http.StatusInternalServerError, fmt.Errorf("episodefile/bulk: get: %w", err), nil)
			return
		}
		if len(files) == 0 {
			httperr.NotFound(w, "episodefile/bulk: no matching files")
			return
		}
		ptrs := make([]*episodefiles.File, len(files))
		for i := range files {
			f := files[i]
			if entry, ok := byID[f.ID]; ok {
				if entry.SceneName != "" {
					f.SceneName = entry.SceneName
				}
				if entry.ReleaseGroup != "" {
					f.ReleaseGroup = entry.ReleaseGroup
				}
				if qualityStr, hasQuality := persistedQualityFromResource(entry.Quality); hasQuality {
					f.Quality = qualityStr
				}
				if entry.Languages != nil {
					f.Languages = languageIDsFromRefs(entry.Languages)
				}
			}
			ptrs[i] = &f
		}
		if err := svc.UpdateMany(req.Context(), ptrs); err != nil {
			httperr.WriteErr(w, req, http.StatusInternalServerError, fmt.Errorf("episodefile/bulk: update: %w", err), nil)
			return
		}
		out := make([]EpisodeFileResource, 0, len(ptrs))
		for _, f := range ptrs {
			res := ToEpisodeFileResource(*f, "")
			ApplyEpisodeFilePopulator(req.Context(), &res, f, svc)
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeFileUpdateHandler returns PUT /api/v3/episodefile/{id}. Mirrors
// `EpisodeFileController.SetQuality` at `:94-113`. Updates Quality (J389)
// + SceneName + ReleaseGroup on the persisted row; returns 202 + the
// updated resource. Per C#, the single-id endpoint does NOT touch
// Languages (only the editor/bulk overloads do).
//
// DEFERRED with rationale: IsSceneTitle regex validation on SceneName
// (C# guards the assignment with `SceneChecker.IsSceneTitle` which
// validates the proper scene naming pattern - the Go port accepts any
// non-empty value today and trusts the React UI to send well-formed
// data).
func EpisodeFileUpdateHandler(svc EpisodeFileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "episodefile: invalid id")
			return
		}
		var body EpisodeFileResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episodefile: malformed body: %v", err))
			return
		}
		f, err := svc.GetFile(r.Context(), id)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("episodefile %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: get %d: %w", id, err), nil)
			return
		}
		if body.SceneName != "" {
			f.SceneName = body.SceneName
		}
		if body.ReleaseGroup != "" {
			f.ReleaseGroup = body.ReleaseGroup
		}
		// C# single SetQuality (`:94-113`) sets Quality but NOT Languages.
		if qualityStr, hasQuality := persistedQualityFromResource(body.Quality); hasQuality {
			f.Quality = qualityStr
		}
		if err := svc.Update(r.Context(), f); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: update %d: %w", id, err), nil)
			return
		}
		res := ToEpisodeFileResource(*f, "")
		ApplyEpisodeFilePopulator(r.Context(), &res, f, svc)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// episodeFileBulkBody mirrors C# `EpisodeFileListResource` —
// `{episodeFileIds: int[]}`.
type episodeFileBulkBody struct {
	EpisodeFileIDs []int `json:"episodeFileIds"`
}

// EpisodeFileDeleteHandler returns DELETE /api/v3/episodefile/{id}.
// Mirrors `EpisodeFileController.DeleteEpisodeFile` at `:152-165`.
// 404 when the id doesn't match; 200 + `{}` on success matching C#
// `void` action. Calls Service.Delete with reason=Manual so the
// auto-unmonitor + history-write handler chain reacts appropriately.
//
// DEFERRED: physical disk delete / recycle-bin move (C# delegates
// to MediaFileDeletionService which calls DiskProvider.MoveFile to
// recycle bin OR DeleteFile - the Go-port episodefiles.Service.Delete
// removes the DB row + fires the event but doesn't touch disk yet,
// per its own DEFER comment).
func EpisodeFileDeleteHandler(svc EpisodeFileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "episodefile: invalid id")
			return
		}
		f, err := svc.GetFile(r.Context(), id)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("episodefile %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: get %d: %w", id, err), nil)
			return
		}
		if err := svc.Delete(r.Context(), f, episodefiles.ReasonManual); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: delete %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// EpisodeFileBulkDeleteHandler returns DELETE /api/v3/episodefile/bulk.
// Mirrors `DeleteEpisodeFiles` at `:167-180`. Walks the file list +
// calls Service.Delete on each. Empty list short-circuits to `{}`.
//
// First-error-wins matches the C# foreach pattern (where a thrown
// exception aborts the loop). Caller can retry on the remaining
// ids by inspecting the failure list. The C# action throws on a
// missing first file (`episodeFiles.First()`) - the Go port returns
// 400 with a clearer message instead.
func EpisodeFileBulkDeleteHandler(svc EpisodeFileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body episodeFileBulkBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episodefile/bulk: malformed body: %v", err))
			return
		}
		if len(body.EpisodeFileIDs) == 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		files, err := svc.GetFiles(r.Context(), body.EpisodeFileIDs)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile/bulk: get: %w", err), nil)
			return
		}
		if len(files) == 0 {
			httperr.NotFound(w, "episodefile/bulk: no matching files")
			return
		}
		for i := range files {
			if err := svc.Delete(r.Context(), &files[i], episodefiles.ReasonManual); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile/bulk: delete %d: %w", files[i].ID, err), nil)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// ToEpisodeFileResource projects the persisted episodefiles.File
// to the wire shape. `seriesPath` is the parent series' Path field
// which the C# tree joins to build the absolute Path; pass empty
// when the join isn't available (the resulting Path field on the
// wire is then just the bare RelativePath, matching the C#
// `Path.Combine("", relative)` fallback).
func ToEpisodeFileResource(f episodefiles.File, seriesPath string) EpisodeFileResource {
	path := f.RelativePath
	if seriesPath != "" && path != "" {
		path = seriesPath + "/" + path
	}
	// J328 IndexerFlags: pointer-to-int matches C# `int? IndexerFlags`
	// nullable wire under WhenWritingNull. Persistence stores int with
	// 0 default; emit only when non-zero (matches the C# null-when-
	// default semantic — clients distinguishing 0-flags from no-flags
	// reported isn't a real use case).
	var indexerFlagsPtr *int
	if f.IndexerFlags != 0 {
		v := f.IndexerFlags
		indexerFlagsPtr = &v
	}
	// J328 ReleaseType: camelCase enum name when non-Unknown. Persistence
	// stores int with 0 = Unknown default; the camelCase form (`unknown`,
	// `singleEpisode`, `multiEpisode`, `seasonPack`) matches C#
	// JsonStringEnumConverter(CamelCase) on the ReleaseType enum.
	// omitempty drops empty string so Unknown → key absent (matches C#
	// `ReleaseType?` nullable null under WhenWritingNull).
	releaseType := ""
	if f.ReleaseType != 0 {
		releaseType = CamelCaseEnumName(parser.ReleaseType(f.ReleaseType).String())
	}
	// J373 Quality: the persisted f.Quality is the reduced
	// `{"quality":<id>,"revision":{...}}` JSON string (quality.Model's
	// custom MarshalJSON shape). Decode it via the registry-backed
	// UnmarshalJSON and project to the expanded REST wire shape via
	// ToQualityModelResource. C# always emits `Quality = model.Quality`
	// (non-null QualityModel); the Go port emits whenever the column is
	// non-empty + decodes (an empty string — the unset test-fixture
	// default — leaves the pointer nil so omitempty drops the key).
	var qualityPtr *QualityModelResource
	if f.Quality != "" {
		var qm quality.Model
		if err := json.Unmarshal([]byte(f.Quality), &qm); err == nil {
			qualityPtr = ToQualityModelResource(qm)
		}
	}
	return EpisodeFileResource{
		ID:            f.ID,
		SeriesID:      f.SeriesID,
		SeasonNumber:  f.SeasonNumber,
		RelativePath:  f.RelativePath,
		Path:          path,
		Size:          f.Size,
		DateAdded:     f.DateAdded,
		SceneName:     f.SceneName,
		ReleaseGroup:  f.ReleaseGroup,
		Languages:     ToLanguageRefs(f.Languages),
		IndexerFlags:  indexerFlagsPtr,
		ReleaseType:   releaseType,
		Quality:       qualityPtr,
		MediaInfo:     ToMediaInfoResource(f.MediaInfo, f.SceneName),
		CustomFormats: []CustomFormatResource{},
		// Quality (*QualityModelResource): J373 decodes the persisted
		// f.Quality reduced JSON string (quality.Model) → expanded wire
		// shape via ToQualityModelResource (see qualityPtr above). Empty
		// column → nil → omitempty drops the key.
		// QualityCutoffNotMet: J375 computes it post-mapping via the
		// EpisodeFileQualityCutoffPopulator optional extension (the
		// ApplyEpisodeFilePopulator helper sets it). The mapper itself
		// leaves the false default; always-emitted (no omitempty) to
		// match the C# non-nullable bool + HistoryResource (J312).
		// CustomFormatScore: 0 — populated-row mapper landing
		// alongside the calculator wire-through (J298+).
	}
}
