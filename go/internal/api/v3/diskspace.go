package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
)

// DiskSpaceResource mirrors
// `src/Sonarr.Api.V3/DiskSpace/DiskSpaceResource.cs`. Four fields,
// no [JsonProperty] overrides — names round-trip as written.
//
// Path is the absolute root path the figures apply to. Label is the
// volume label (empty when the host platform's GetVolumeLabel isn't
// available; the C# implementation returns "" on platforms where the
// disk provider's label probe fails). FreeSpace + TotalSpace are
// raw byte counts.
type DiskSpaceResource struct {
	Path       string `json:"path"`
	Label      string `json:"label"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// FreeSpaceLister is the read surface for /api/v3/diskspace. The
// production adapter (in cmd/sonarr/host_wiring.go) walks the
// rootfolders.Service rows and computes per-row disk stats via the
// existing rootfolders.DiskProvider.
type FreeSpaceLister interface {
	GetFreeSpace(ctx context.Context) ([]DiskSpaceResource, error)
}

// DiskSpaceHandler returns GET /api/v3/diskspace. Mirrors
// `DiskSpaceController.GetFreeSpace` at
// `src/Sonarr.Api.V3/DiskSpace/DiskSpaceController.cs:17-22`. C#
// returns the union of:
//
//   - "Important" paths: the disk roots that any series sits on,
//     deduped via DiskProvider.GetPathRoot.
//   - "Optional" paths: every mounted Fixed/Network drive NOT in the
//     important set, filtered against a `_regexSpecialDrive`
//     denylist (/var/lib/docker, /boot, /etc, time-machine mounts).
//
// The Go port currently ships the **important** path only, sourced
// from the rootfolders table directly (one DiskSpaceResource per
// configured root folder). The DEFERRED optional path needs a
// cross-platform GetMounts adapter the Go side hasn't built yet —
// captured as a STATUS gap. Empty-DB wire shape (`[]`) matches C#
// byte-for-byte.
//
// Ordering matches C# `OrderBy(d => d.Path, StringComparer.OrdinalIgnoreCase)`.
func DiskSpaceHandler(lister FreeSpaceLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		records, err := lister.GetFreeSpace(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("diskspace: %w", err), nil)
			return
		}
		if records == nil {
			records = []DiskSpaceResource{}
		}
		sort.SliceStable(records, func(i, j int) bool {
			return strings.ToLower(records[i].Path) < strings.ToLower(records[j].Path)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(records)
		_, _ = w.Write(buf)
	})
}
