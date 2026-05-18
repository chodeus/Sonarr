package v3

import (
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
)

// ManualImportHandler returns GET /api/v3/manualimport. Mirrors C#
// `ManualImportController.GetMediaFiles` at
// `src/Sonarr.Api.V3/ManualImport/ManualImportController.cs:25-36`
// which dispatches on (seriesId + seasonNumber) vs (folder/downloadId
// /seriesId/filterExistingFiles) and returns ManualImportResource[]
// — a per-file projection with quality/language inference + rejection
// list.
//
// DEFERRED with rationale: the manual-import service that performs
// the disk walk + parser + decision-engine evaluation isn't ported.
// Today this handler emits `[]` matching the no-results wire shape
// so the React UI's manual-import folder browser gets 200 instead of
// 404. The POST companion (reprocessItem) is intentionally NOT
// stubbed - silently no-opping a POST that the user expects to
// import files would be misleading.
//
// Query params are validated but otherwise ignored.
func ManualImportHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Validate int params if present.
		for _, key := range []string{"seriesId", "seasonNumber"} {
			if v := q.Get(key); v != "" {
				if _, err := strconv.Atoi(v); err != nil {
					httperr.BadRequest(w, "manualimport: invalid "+key)
					return
				}
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}
