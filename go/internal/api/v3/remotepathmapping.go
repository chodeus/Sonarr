package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/remotepathmappings"
)

// RemotePathMappingResource mirrors
// `src/Sonarr.Api.V3/RemotePathMappings/RemotePathMappingResource.cs`.
// Three plain-string fields plus the inherited Id — every mapping
// row is a direct passthrough.
//
// Field-source mapping (C# → Go):
//
//	id          → Mapping.ID
//	host        → Mapping.Host
//	remotePath  → Mapping.RemotePath
//	localPath   → Mapping.LocalPath
type RemotePathMappingResource struct {
	ID         int    `json:"id"`
	Host       string `json:"host"`
	RemotePath string `json:"remotePath"`
	LocalPath  string `json:"localPath"`
}

// RemotePathMappingLister narrows the read surface to All().
// *remotepathmappings.Service satisfies it.
type RemotePathMappingLister interface {
	All(ctx context.Context) ([]remotepathmappings.Mapping, error)
}

// RemotePathMappingHandler returns the http.Handler for
// GET /api/v3/remotepathmapping. Mirrors
// `RemotePathMappingController.GetMappings` at
// `src/Sonarr.Api.V3/RemotePathMappings/RemotePathMappingController.cs`.
func RemotePathMappingHandler(lister RemotePathMappingLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("remotepathmappings: %v", err), http.StatusInternalServerError)
			return
		}
		out := make([]RemotePathMappingResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, RemotePathMappingResource{
				ID:         row.ID,
				Host:       row.Host,
				RemotePath: row.RemotePath,
				LocalPath:  row.LocalPath,
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
