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
// DEFERRED for the populated-body emit: languages, quality,
// customFormats, customFormatScore, indexerFlags, releaseType,
// mediaInfo, episodeHasFile (per-episode rollup).
type EpisodeFileResource struct {
	ID           int       `json:"id"`
	SeriesID     int       `json:"seriesId"`
	SeasonNumber int       `json:"seasonNumber"`
	RelativePath string    `json:"relativePath"`
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	DateAdded    time.Time `json:"dateAdded"`
	SceneName    string    `json:"sceneName,omitempty"`
	ReleaseGroup string    `json:"releaseGroup,omitempty"`
}

// EpisodeFileReader is the read surface for GET /api/v3/episodefile.
// Satisfied directly by *episodefiles.Service.
type EpisodeFileReader interface {
	GetFile(ctx context.Context, id int) (*episodefiles.File, error)
	GetFiles(ctx context.Context, ids []int) ([]episodefiles.File, error)
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// EpisodeFileListHandler returns GET /api/v3/episodefile. Mirrors
// `EpisodeFileController.GetEpisodeFiles` at `:61-92`. Required:
// `seriesId` OR `episodeFileIds` (repeated int). Returns the C#-
// shaped resource list — minus the joined fields (quality,
// languages, customFormats, mediaInfo) which the existing
// EpisodeFileResource skeleton DEFERS.
func EpisodeFileListHandler(svc EpisodeFileReader) http.Handler {
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
			out = append(out, ToEpisodeFileResource(f, ""))
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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(ToEpisodeFileResource(*f, ""))
		_, _ = w.Write(buf)
	})
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

// episodeFileEditorBody mirrors C# `EpisodeFileListResource` — same
// shape as the DELETE-bulk body plus the editor's metadata override
// fields (Languages/Quality DEFERRED).
type episodeFileEditorBody struct {
	EpisodeFileIDs []int  `json:"episodeFileIds"`
	SceneName      string `json:"sceneName,omitempty"`
	ReleaseGroup   string `json:"releaseGroup,omitempty"`
}

// EpisodeFileEditorHandler returns PUT /api/v3/episodefile/editor.
// Mirrors `EpisodeFileController.SetQuality` (the `[HttpPut("editor")]`
// overload at `:115-150`) — applies one set of metadata overrides
// (SceneName/ReleaseGroup; Languages/Quality DEFERRED) to ALL files
// in the EpisodeFileIds list. The C# action is marked `[Obsolete]`
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
		ptrs := make([]*episodefiles.File, len(files))
		for i := range files {
			f := files[i]
			if body.SceneName != "" {
				f.SceneName = body.SceneName
			}
			if body.ReleaseGroup != "" {
				f.ReleaseGroup = body.ReleaseGroup
			}
			ptrs[i] = &f
		}
		if err := svc.UpdateMany(r.Context(), ptrs); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile/editor: update: %w", err), nil)
			return
		}
		out := make([]EpisodeFileResource, 0, len(ptrs))
		for _, f := range ptrs {
			out = append(out, ToEpisodeFileResource(*f, ""))
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
			}
			ptrs[i] = &f
		}
		if err := svc.UpdateMany(req.Context(), ptrs); err != nil {
			httperr.WriteErr(w, req, http.StatusInternalServerError, fmt.Errorf("episodefile/bulk: update: %w", err), nil)
			return
		}
		out := make([]EpisodeFileResource, 0, len(ptrs))
		for _, f := range ptrs {
			out = append(out, ToEpisodeFileResource(*f, ""))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeFileUpdateHandler returns PUT /api/v3/episodefile/{id}. Mirrors
// `EpisodeFileController.SetQuality` at `:94-113`. Updates SceneName
// + ReleaseGroup on the persisted row; returns 202 + the updated
// resource.
//
// DEFERRED with rationale: Quality + Languages updates (the
// EpisodeFileResource skeleton doesn't yet model these fields - same
// gap as the existing GET; revisit when the populated-row mapping
// lands); IsSceneTitle regex validation on SceneName (C# guards the
// assignment with `SceneChecker.IsSceneTitle` which validates the
// proper scene naming pattern - the Go port accepts any non-empty
// value today and trusts the React UI to send well-formed data).
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
		if err := svc.Update(r.Context(), f); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodefile: update %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(ToEpisodeFileResource(*f, ""))
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
	return EpisodeFileResource{
		ID:           f.ID,
		SeriesID:     f.SeriesID,
		SeasonNumber: f.SeasonNumber,
		RelativePath: f.RelativePath,
		Path:         path,
		Size:         f.Size,
		DateAdded:    f.DateAdded,
		SceneName:    f.SceneName,
		ReleaseGroup: f.ReleaseGroup,
	}
}
