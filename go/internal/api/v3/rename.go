package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
)

// RenameEpisodeResource mirrors C#
// `src/Sonarr.Api.V3/Episodes/RenameEpisodeResource.cs`. Carries the
// rename-preview shape: the diff between an episode file's current
// path and the path BuildFileName would produce under the current
// NamingConfig. Empty list = nothing to rename.
type RenameEpisodeResource struct {
	ID             int    `json:"id"`
	SeriesID       int    `json:"seriesId"`
	SeasonNumber   int    `json:"seasonNumber"`
	EpisodeNumbers []int  `json:"episodeNumbers"`
	EpisodeFileID  int    `json:"episodeFileId"`
	ExistingPath   string `json:"existingPath"`
	NewPath        string `json:"newPath"`
}

// RenamePreview is the projection-neutral preview row a
// [RenamePreviewer] yields — the API layer maps it to
// [RenameEpisodeResource] without depending on the driver package.
type RenamePreview struct {
	SeriesID       int
	SeasonNumber   int
	EpisodeNumbers []int
	EpisodeFileID  int
	ExistingPath   string
	NewPath        string
}

// RenamePreviewer is the consumer-side interface the rename handlers
// depend on — satisfied by an adapter over the renamepreview.Service
// driver (cmd/sonarr). When nil, the handlers emit `[]` (the
// steady-state "no renames suggested" shape).
type RenamePreviewer interface {
	GetBySeries(ctx context.Context, seriesID int) ([]RenamePreview, error)
	GetBySeason(ctx context.Context, seriesID, seasonNumber int) ([]RenamePreview, error)
	GetBulk(ctx context.Context, seriesIDs []int) ([]RenamePreview, error)
}

// renamePreviewToResource maps the neutral preview row to the wire
// resource. C# `RenameEpisodeResourceMapper.ToResource` sets
// `Id = EpisodeFileId` (`RenameEpisodeResource.cs:28`).
func renamePreviewToResource(p RenamePreview) RenameEpisodeResource {
	nums := p.EpisodeNumbers
	if nums == nil {
		nums = []int{}
	}
	return RenameEpisodeResource{
		ID:             p.EpisodeFileID,
		SeriesID:       p.SeriesID,
		SeasonNumber:   p.SeasonNumber,
		EpisodeNumbers: nums,
		EpisodeFileID:  p.EpisodeFileID,
		ExistingPath:   p.ExistingPath,
		NewPath:        p.NewPath,
	}
}

func writeRenameResources(w http.ResponseWriter, previews []RenamePreview) {
	out := make([]RenameEpisodeResource, 0, len(previews))
	for _, p := range previews {
		out = append(out, renamePreviewToResource(p))
	}
	// json.Marshal (not Encoder) — no trailing newline, preserving the
	// byte-exact `[]` empty shape the contract goldens + handler tests
	// assert.
	body, err := json.Marshal(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// RenameHandler returns GET /api/v3/rename. Mirrors
// `RenameEpisodeController.GetEpisodes` at `RenameEpisodeController.cs:20-30`.
// Required: `seriesId` (int > 0). Optional: `seasonNumber`.
//
// When `previewer` is nil, emits `[]` (preview driver not wired) —
// the React UI degrades to "no files to rename" rather than 404ing.
func RenameHandler(previewer RenamePreviewer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seriesIDStr := r.URL.Query().Get("seriesId")
		if seriesIDStr == "" {
			httperr.BadRequest(w, "rename: seriesId is required")
			return
		}
		seriesID, err := strconv.Atoi(seriesIDStr)
		if err != nil || seriesID <= 0 {
			httperr.BadRequest(w, "rename: invalid seriesId")
			return
		}
		if previewer == nil {
			writeRenameResources(w, nil)
			return
		}

		var previews []RenamePreview
		if seasonStr := r.URL.Query().Get("seasonNumber"); seasonStr != "" {
			seasonNumber, err := strconv.Atoi(seasonStr)
			if err != nil {
				httperr.BadRequest(w, "rename: invalid seasonNumber")
				return
			}
			previews, err = previewer.GetBySeason(r.Context(), seriesID, seasonNumber)
			if err != nil {
				httperr.Internal(w, r, err, nil)
				return
			}
		} else {
			previews, err = previewer.GetBySeries(r.Context(), seriesID)
			if err != nil {
				httperr.Internal(w, r, err, nil)
				return
			}
		}
		writeRenameResources(w, previews)
	})
}

// RenameBulkHandler returns GET /api/v3/rename/bulk. Mirrors
// `RenameEpisodeController.GetEpisodes(seriesIds)` at `:32-47`.
// Required: at least one `seriesIds` query value, each > 0.
func RenameBulkHandler(previewer RenamePreviewer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query()["seriesIds"]
		if len(raw) == 0 {
			httperr.BadRequest(w, "rename/bulk: seriesIds must be provided")
			return
		}
		ids := make([]int, 0, len(raw))
		for _, s := range raw {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				httperr.BadRequest(w, "rename/bulk: seriesIds must be positive integers")
				return
			}
			ids = append(ids, n)
		}
		if previewer == nil {
			writeRenameResources(w, nil)
			return
		}
		previews, err := previewer.GetBulk(r.Context(), ids)
		if err != nil {
			httperr.Internal(w, r, err, nil)
			return
		}
		writeRenameResources(w, previews)
	})
}
