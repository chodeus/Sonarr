package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// RootFolderResource mirrors
// `src/Sonarr.Api.V3/RootFolders/RootFolderResource.cs`.
//
// FreeSpace and TotalSpace are nullable in C# (`long?`) and elide on
// write when null per STJson.WhenWritingNull; the Go port wraps the
// in-process *int64 with `omitempty` to match.
//
// IsEmpty exists on rootfolders.Folder but NOT on the C# wire shape —
// the mapper drops it. UnmappedFolders is always emitted, defaulting
// to [].
//
// Field-source mapping (C# → Go):
//
//	id              → Folder.ID
//	path            → CleanPath(Folder.Path) (matches C# `Path.GetCleanPath()`)
//	accessible      → Folder.Accessible
//	freeSpace       → Folder.FreeSpace (*int64, omitempty)
//	totalSpace      → Folder.TotalSpace (*int64, omitempty)
//	unmappedFolders → Folder.UnmappedFolders (nil → [])
type RootFolderResource struct {
	ID              int                          `json:"id"`
	Path            string                       `json:"path"`
	Accessible      bool                         `json:"accessible"`
	FreeSpace       *int64                       `json:"freeSpace,omitempty"`
	TotalSpace      *int64                       `json:"totalSpace,omitempty"`
	UnmappedFolders []rootfolders.UnmappedFolder `json:"unmappedFolders"`
}

// RootFolderLister narrows the read surface to the disk-stat populated
// form. *rootfolders.Service.AllWithUnmappedFolders satisfies it
// directly — that's the exact method the C# controller calls.
type RootFolderLister interface {
	AllWithUnmappedFolders(ctx context.Context) ([]rootfolders.Folder, error)
}

// RootFolderHandler returns the http.Handler for
// GET /api/v3/rootfolder. Mirrors
// `RootFolderController.GetRootFolders` at
// `src/Sonarr.Api.V3/RootFolders/RootFolderController.cs`.
func RootFolderHandler(lister RootFolderLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.AllWithUnmappedFolders(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		out := make([]RootFolderResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toRootFolderResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toRootFolderResource(f rootfolders.Folder) RootFolderResource {
	unmapped := f.UnmappedFolders
	if unmapped == nil {
		unmapped = []rootfolders.UnmappedFolder{}
	}
	return RootFolderResource{
		ID:              f.ID,
		Path:            rootfolders.CleanPath(f.Path),
		Accessible:      f.Accessible,
		FreeSpace:       f.FreeSpace,
		TotalSpace:      f.TotalSpace,
		UnmappedFolders: unmapped,
	}
}
