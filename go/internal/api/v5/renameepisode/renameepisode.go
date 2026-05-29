// Package renameepisode is the v5 wire surface for /api/v5/rename
// (and /api/v5/rename/bulk) — the rename-preview endpoint that
// returns the diff between an episode file's current path and the
// path BuildFileName would produce under the current NamingConfig.
//
// Source: src/Sonarr.Api.V5/Episodes/RenameEpisodeController.cs
// (48 LOC). 2 GET endpoints: /rename?seriesId=&seasonNumber= and
// /rename/bulk?seriesIds=.
//
// Wire shape (RenameEpisodeResource): 7 fields (id, seriesId,
// seasonNumber, episodeNumbers[], episodeFileId, existingPath,
// newPath). Byte-identical between v3 and v5.
//
// Pure path-alias slice — same J224/J280/J286 template. v3 emits
// `[]` per the deferred stub at `internal/api/v3/rename.go:37-79`
// (the underlying RenameEpisodeFileService.GetRenamePreviews
// comparison driver isn't ported yet; the React UI degrades to
// "no files to rename" cleanly). v5 inherits the same `[]` shape
// until the comparison driver lands — both surfaces widen
// simultaneously via the shared handler when that happens.
//
// Status codes: GET → 200 (both endpoints); 400 on missing/invalid
// query params (seriesId required + positive on /rename; seriesIds
// non-empty + all positive on /rename/bulk).
package renameepisode

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is the v5 RenameEpisodeResource wire shape — aliased from
// v3 (unchanged in v5).
type Resource = apiv3.RenameEpisodeResource

// GetHandler returns GET /api/v5/rename. Same wire shape + status
// codes as v3 — delegates to the shared v3 handler with the same
// preview driver.
func GetHandler(previewer apiv3.RenamePreviewer) http.Handler {
	return apiv3.RenameHandler(previewer)
}

// BulkHandler returns GET /api/v5/rename/bulk. Same wire shape +
// status codes as v3 — delegates to the shared v3 handler.
func BulkHandler(previewer apiv3.RenamePreviewer) http.Handler {
	return apiv3.RenameBulkHandler(previewer)
}
