package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/httperr"
)

// UpdateResource is the JSON shape produced by GET /api/v3/update.
// Mirrors `src/Sonarr.Api.V3/Update/UpdateResource.cs`. Each entry
// describes one release package the auto-updater knows about.
//
// The populated-row mapping awaits the RecentUpdateProvider consumer
// port (cmd/sonarr substitutes an empty fixture today), so the marking
// is inert in production until that lands; the projection is correct the
// moment the lister returns real rows.
//
// Field-source mapping (C# → Go):
//
//	version       → UpdatePackage.Version (System.Version → string)
//	branch        → UpdatePackage.Branch
//	releaseDate   → UpdatePackage.ReleaseDate (DateTime → RFC3339)
//	fileName      → UpdatePackage.FileName
//	url           → UpdatePackage.Url
//	installed     → BuildInfo.Version == package.Version (J403 marking)
//	installedOn   → UpdateHistory.Date (nullable, omitempty; J514 — set
//	                from the InstallHistoryLister when the log DB is on)
//	installable   → highest row when newer than the build (J403 marking)
//	latest        → set on the highest-version row in the list (J403)
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

// InstalledVersion is one previously-installed version + the date it was
// installed, supplied by [InstallHistoryLister] for the InstalledOn arm.
type InstalledVersion struct {
	Version string
	Date    time.Time
}

// InstallHistoryLister is the OPTIONAL capability the update lister may
// also expose: the recorded install history (C#
// `UpdateHistoryService.InstalledSince`). [UpdateHandler] type-asserts
// the lister for it and, when present, stamps each update row's
// InstalledOn. Production binds *updatehistory.Service (only when the
// log DB is enabled — the C# `LogDbEnabled` gate), via the same adapter
// that satisfies [UpdateLister]; when the log DB is disabled the adapter
// returns no history so the arm is skipped.
type InstallHistoryLister interface {
	InstalledSince(ctx context.Context, since time.Time) []InstalledVersion
}

// UpdateHandler returns the http.Handler for GET /api/v3/update.
// Mirrors `UpdateController.GetRecentUpdates` at
// `src/Sonarr.Api.V3/Update/UpdateController.cs:27-49`: sort the
// packages by version descending, mark the highest row Latest,
// mark it Installable when it's newer than the running build, and
// mark the row that matches the running build Installed.
//
// J403 ports the [markRecentUpdates] projection (previously the
// handler passed the lister's rows through unmarked). The flags are
// computed against [buildinfo.Version]. NOTE: the production lister
// is still the empty fixture (the RecentUpdateProvider isn't ported
// — see [UpdateLister]), so the marking is inert in production until
// that provider lands; it is exercised here by the unit tests and is
// correct the moment the lister returns real rows.
//
// J514 wires the InstalledOn arm (C# `:50-66`): when the lister also
// exposes [InstallHistoryLister] (the log-DB-backed
// updatehistory.Service, ported at J505), each row whose version was
// previously installed is stamped with its install date. The arm is
// skipped when no history is exposed (log DB disabled).
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
		rows = markRecentUpdates(rows, buildinfo.Version)
		if hist, ok := lister.(InstallHistoryLister); ok {
			applyInstalledOn(r.Context(), rows, hist)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(rows)
		_, _ = w.Write(buf)
	})
}

// applyInstalledOn ports the InstalledOn projection of
// `UpdateController.GetRecentUpdates` (`:55-65`). rows must already be
// sorted version-descending (so the last row carries the oldest
// ReleaseDate, the `InstalledSince` lower bound). For each row whose
// version matches a recorded install, InstalledOn is set to the EARLIEST
// such install date (C# `DistinctBy(Version)` over the Date-ascending
// history keeps the first occurrence). Version equality follows
// System.Version semantics ("4.0.1" == "4.0.1.0") via [compareVersions].
func applyInstalledOn(ctx context.Context, rows []UpdateResource, hist InstallHistoryLister) {
	if len(rows) == 0 {
		return
	}
	history := hist.InstalledSince(ctx, rows[len(rows)-1].ReleaseDate)
	if len(history) == 0 {
		return
	}
	for i := range rows {
		for _, h := range history {
			if compareVersions(h.Version, rows[i].Version) == 0 {
				d := h.Date
				rows[i].InstalledOn = &d
				break
			}
		}
	}
}

// markRecentUpdates ports the marking logic of
// `UpdateController.GetRecentUpdates` (`:30-49`). It sorts rows by
// version descending, then on a non-empty list sets Latest on the
// highest row, Installable on it when it's newer than currentVersion,
// and Installed on the (single) row whose version equals
// currentVersion. Mutates + returns the slice.
func markRecentUpdates(rows []UpdateResource, currentVersion string) []UpdateResource {
	if len(rows) == 0 {
		return rows
	}
	sort.SliceStable(rows, func(i, j int) bool {
		// Descending by version — C# `OrderByDescending(u => u.Version)`.
		return compareVersions(rows[i].Version, rows[j].Version) > 0
	})
	rows[0].Latest = true
	if compareVersions(rows[0].Version, currentVersion) > 0 {
		rows[0].Installable = true
	}
	for i := range rows {
		// C# `SingleOrDefault(r => r.Version == BuildInfo.Version)` —
		// mark the first exact match (versions are unique in practice).
		if compareVersions(rows[i].Version, currentVersion) == 0 {
			rows[i].Installed = true
			break
		}
	}
	return rows
}

// compareVersions compares two dotted-numeric version strings
// component-wise (e.g. "4.0.1.234"), mirroring C# System.Version
// ordering: missing trailing components count as 0 ("4.0.1" ==
// "4.0.1.0") and a non-numeric component is treated as 0 (so the
// "dev" build sorts below any real release). Returns -1 / 0 / +1.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := versionPart(as, i), versionPart(bs, i)
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionPart returns the i-th dotted component as an int, or 0 when
// the component is absent or non-numeric.
func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(parts[i]))
	if err != nil {
		return 0
	}
	return v
}
