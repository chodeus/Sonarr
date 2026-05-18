package v3

import (
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

// RenameHandler returns GET /api/v3/rename. Mirrors
// `RenameEpisodeController.GetEpisodes` at `RenameEpisodeController.cs:20-30`.
// Required: `seriesId` (int > 0). Optional: `seasonNumber`.
//
// DEFERRED with rationale: the rename-preview comparison itself
// (BuildFileName against each episode file's current path then
// returning the diffs). The Go port has BuildFileName + the file
// builder helpers but the comparison driver (the C# `RenameEpisode
// FileService.GetRenamePreviews`) isn't ported yet. This handler
// emits `[]` matching the steady-state "no renames suggested" wire
// shape - a safe fallback that lets the React UI render "no files
// to rename" instead of 404ing.
func RenameHandler() http.Handler {
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
		// seasonNumber accepted but ignored - the empty wire shape
		// doesn't change.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}

// RenameBulkHandler returns GET /api/v3/rename/bulk. Mirrors
// `RenameEpisodeController.GetEpisodes(seriesIds)` at `:32-47`.
// Required: at least one `seriesIds` query value, each > 0. Same
// `[]` DEFERRED stub semantics as RenameHandler.
func RenameBulkHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query()["seriesIds"]
		if len(raw) == 0 {
			httperr.BadRequest(w, "rename/bulk: seriesIds must be provided")
			return
		}
		for _, s := range raw {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				httperr.BadRequest(w, "rename/bulk: seriesIds must be positive integers")
				return
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}
