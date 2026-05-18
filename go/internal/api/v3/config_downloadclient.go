package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
)

// DownloadClientConfigResource mirrors
// `src/Sonarr.Api.V3/Config/DownloadClientConfigResource.cs`. Five
// fields plus the singleton Id.
//
// Field-source mapping (C# → Go):
//
//	id                                          → constant 1
//	downloadClientWorkingFolders                → config.Provider.DownloadClientWorkingFolders
//	enableCompletedDownloadHandling             → config.Provider.EnableCompletedDownloadHandling
//	autoRedownloadFailed                        → config.Provider.AutoRedownloadFailed
//	autoRedownloadFailedFromInteractiveSearch   → config.Provider.AutoRedownloadFailedFromInteractiveSearch
type DownloadClientConfigResource struct {
	ID                                        int    `json:"id"`
	DownloadClientWorkingFolders              string `json:"downloadClientWorkingFolders"`
	EnableCompletedDownloadHandling           bool   `json:"enableCompletedDownloadHandling"`
	AutoRedownloadFailed                      bool   `json:"autoRedownloadFailed"`
	AutoRedownloadFailedFromInteractiveSearch bool   `json:"autoRedownloadFailedFromInteractiveSearch"`
}

// DownloadClientConfigGetter is the read side the handler depends
// on. config.Provider satisfies it directly.
type DownloadClientConfigGetter interface {
	DownloadClientWorkingFolders() string
	EnableCompletedDownloadHandling() bool
	AutoRedownloadFailed() bool
	AutoRedownloadFailedFromInteractiveSearch() bool
}

// DownloadClientConfigHandler returns the http.Handler for
// GET /api/v3/config/downloadclient. Mirrors
// `DownloadClientConfigController` — singleton with Id=1.
func DownloadClientConfigHandler(getter DownloadClientConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := DownloadClientConfigResource{
			ID:                              1,
			DownloadClientWorkingFolders:    getter.DownloadClientWorkingFolders(),
			EnableCompletedDownloadHandling: getter.EnableCompletedDownloadHandling(),
			AutoRedownloadFailed:            getter.AutoRedownloadFailed(),
			AutoRedownloadFailedFromInteractiveSearch: getter.AutoRedownloadFailedFromInteractiveSearch(),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ---------- 5.B13: PUT ----------

// DownloadClientConfigWriter is the write surface — per-field setters
// the config.Provider satisfies natively.
type DownloadClientConfigWriter interface {
	DownloadClientConfigGetter
	SetDownloadClientWorkingFolders(ctx context.Context, v string) error
	SetEnableCompletedDownloadHandling(ctx context.Context, v bool) error
	SetAutoRedownloadFailed(ctx context.Context, v bool) error
	SetAutoRedownloadFailedFromInteractiveSearch(ctx context.Context, v bool) error
}

// DownloadClientConfigUpdateHandler returns PUT /api/v3/config/downloadclient/{id}.
// Mirrors `DownloadClientConfigController` (inherits the
// ConfigController.SaveConfig path). 202 + body on success.
//
// C# has no SharedValidator block on this resource — all four fields
// accept any value (booleans clamped naturally; the working-folders
// string is a free-form list of names). The Go port mirrors that
// (no validation chain).
func DownloadClientConfigUpdateHandler(svc DownloadClientConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body DownloadClientConfigResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("config/downloadclient: malformed body: %v", err))
			return
		}
		ctx := r.Context()
		if err := svc.SetDownloadClientWorkingFolders(ctx, body.DownloadClientWorkingFolders); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/downloadclient: %w", err), nil)
			return
		}
		if err := svc.SetEnableCompletedDownloadHandling(ctx, body.EnableCompletedDownloadHandling); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/downloadclient: %w", err), nil)
			return
		}
		if err := svc.SetAutoRedownloadFailed(ctx, body.AutoRedownloadFailed); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/downloadclient: %w", err), nil)
			return
		}
		if err := svc.SetAutoRedownloadFailedFromInteractiveSearch(ctx, body.AutoRedownloadFailedFromInteractiveSearch); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/downloadclient: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(DownloadClientConfigResource{
			ID:                              1,
			DownloadClientWorkingFolders:    svc.DownloadClientWorkingFolders(),
			EnableCompletedDownloadHandling: svc.EnableCompletedDownloadHandling(),
			AutoRedownloadFailed:            svc.AutoRedownloadFailed(),
			AutoRedownloadFailedFromInteractiveSearch: svc.AutoRedownloadFailedFromInteractiveSearch(),
		})
		_, _ = w.Write(buf)
	})
}
