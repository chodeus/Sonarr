package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
)

// UpdateResource is the JSON shape produced by GET /api/v3/update.
// Mirrors `src/Sonarr.Api.V3/Update/UpdateResource.cs`. Each entry
// describes one release package the auto-updater knows about.
//
// This slice ships the wire-shape skeleton; populated-row mapping
// awaits the Update package consumer port (RecentUpdateProvider +
// UpdateHistoryService — neither is in the Go tree yet). The empty-
// DB recording has zero records so the gap is unobservable today
// (DEFERRED — STATUS line 9 carries a note).
//
// Field-source mapping (C# → Go):
//
//	version       → UpdatePackage.Version (System.Version → string)
//	branch        → UpdatePackage.Branch
//	releaseDate   → UpdatePackage.ReleaseDate (DateTime → RFC3339)
//	fileName      → UpdatePackage.FileName
//	url           → UpdatePackage.Url
//	installed     → BuildInfo.Version == package.Version
//	installedOn   → UpdateHistory.Date (nullable, omitempty)
//	installable   → computed; latest installable in resource list
//	latest        → set on the highest-version row in the list
//	changes       → UpdateChanges{ New: []string, Fixed: []string }
//	hash          → UpdatePackage.Hash
type UpdateResource struct {
	ID          int           `json:"id,omitempty"`
	Version     string        `json:"version"`
	Branch      string        `json:"branch"`
	ReleaseDate time.Time     `json:"releaseDate"`
	FileName    string        `json:"fileName"`
	URL         string        `json:"url"`
	Installed   bool          `json:"installed"`
	InstalledOn *time.Time    `json:"installedOn,omitempty"`
	Installable bool          `json:"installable"`
	Latest      bool          `json:"latest"`
	Changes     UpdateChanges `json:"changes"`
	Hash        string        `json:"hash"`
}

// UpdateChanges mirrors `UpdateChanges` in the C# tree — the New +
// Fixed bullet lists rendered on the update UI. The C# default
// constructor sets both to empty `List<string>`; we emit `[]` (not
// null) to match.
type UpdateChanges struct {
	New   []string `json:"new"`
	Fixed []string `json:"fixed"`
}

// UpdateLister is the read side the handler depends on. The Go
// tree has no RecentUpdateProvider port yet — cmd/sonarr substitutes
// an empty fixture today. When the provider lands, an adapter
// satisfies this interface and the production wiring takes effect.
type UpdateLister interface {
	RecentUpdates(ctx context.Context) ([]UpdateResource, error)
}

// UpdateHandler returns the http.Handler for GET /api/v3/update.
// Mirrors `UpdateController.GetRecentUpdates` at
// `src/Sonarr.Api.V3/Update/UpdateController.cs:27-44`. The C#
// path computes Latest + Installable flags on the highest-version
// row after sort-desc — for the empty result the flags are moot.
// Populated-row Latest/Installable computation is DEFERRED with
// the provider port.
func UpdateHandler(lister UpdateLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.RecentUpdates(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("update: %w", err), nil)
			return
		}
		if rows == nil {
			rows = []UpdateResource{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(rows)
		_, _ = w.Write(buf)
	})
}
