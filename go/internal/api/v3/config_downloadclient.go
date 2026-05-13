package v3

import (
	"encoding/json"
	"net/http"
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
