package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/mediaserverqueue"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `PlexServer.cs:17` class name.
const Implementation = "PlexServer"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `PlexServerSettings.cs:20` class name.
const ConfigContract = "PlexServerSettings"

// PlexServer ports the C# `PlexServer` notification provider at
// `Notifications/Plex/Server/PlexServer.cs:17-233`.
//
// Only 6 of the 11 OnX hooks are overridden in C# (OnDownload /
// OnImportComplete / OnRename / OnEpisodeFileDelete / OnSeriesAdd /
// OnSeriesDelete). The other 5 (Grab / HealthIssue / HealthRestored /
// ApplicationUpdate / ManualInteractionRequired) are no-ops.
//
// All 6 active hooks route through updateLibrary() which performs
// the section-discovery + per-path refresh dance. Path-mapping
// (MapFrom/MapTo) is applied to the rootFolderPath before matching
// against Plex's section locations.
type PlexServer struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
	queue    *mediaserverqueue.Queue[bool]
}

// Compile-time guard.
var _ notifications.Provider = (*PlexServer)(nil)

// New constructs a *PlexServer from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("plex: nil definition")
	}
	// Match C# `PlexServerSettings()` constructor defaults at
	// `PlexServerSettings.cs:24-30`: Port=32400, UpdateLibrary=true.
	s := Settings{Port: 32400, UpdateLibrary: true}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("plex: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &PlexServer{
		def:      def,
		settings: s,
		doer:     doer,
		queue:    mediaserverqueue.New[bool](),
	}, nil
}

// Name returns the provider display label. C# `:33`.
func (p *PlexServer) Name() string { return "Plex Media Server" }

// Link returns the docs URL. C# `:32`.
func (p *PlexServer) Link() string { return "https://www.plex.tv/" }

// Definition returns the persisted row.
func (p *PlexServer) Definition() *notifications.Notification { return p.def }

// updateIfEnabled enqueues a per-(host,series) library-refresh.
// Mirrors C# `UpdateIfEnabled` at `:68-77` minus the PlexTv ping (the
// `_plexTvService.Ping` call is deferred — see package doc). The
// actual section discovery + per-path refresh HTTP work happens later
// in [PlexServer.ProcessQueue].
//
// Returns nil for signature parity with the OnX hooks; enqueue is
// in-process and cannot fail.
func (p *PlexServer) updateIfEnabled(_ context.Context, ser *series.Series) error {
	if !p.settings.UpdateLibrary || ser == nil || ser.Path == "" {
		return nil
	}
	// C# adds (host, series, false) at `:75` — the bool is unused at
	// drain time (Plex doesn't have a clean-library concept). We
	// preserve the false marker for parity.
	p.queue.Add(p.settings.Host, ser, false)
	return nil
}

// refreshSeries fires the section-discovery + per-path Plex refresh
// for a single series. Mirrors C# `PlexServerService.UpdateLibrary`
// + the section-matching `UpdateSections` at `:42-150`. Per-series
// flow:
//
//  1. Fetch the show-type sections from Plex.
//  2. Compute the rootFolderPath as filepath.Dir(series.Path) — the
//     C# code uses IRootFolderService.GetBestRootFolderPath, which is
//     DEFERRED in the Go port (see package doc).
//  3. Apply MapFrom/MapTo translation to get the Plex-side mapped
//     root path.
//  4. Find the section whose Location.Path equals the mapped root —
//     update only that section, scoped to the series-relative path.
//  5. If no section matches, fall back to updating every section's
//     locations with the series-relative path.
func (p *PlexServer) refreshSeries(ctx context.Context, ser *series.Series) error {
	if ser == nil || ser.Path == "" {
		return nil
	}
	sections, err := getTvSections(ctx, p.doer, &p.settings)
	if err != nil {
		return err
	}
	rootFolderPath := filepath.Dir(ser.Path)
	mappedRoot := p.settings.applyMapping(rootFolderPath)
	relPath, _ := filepath.Rel(rootFolderPath, ser.Path)
	if relPath == "" {
		relPath = filepath.Base(ser.Path)
	}

	// First pass: look for an exact match on mapped root.
	for _, section := range sections {
		for _, loc := range section.Locations {
			if pathEquals(loc.Path, mappedRoot) {
				return updateSection(ctx, p.doer, &p.settings, section.ID, joinSectionPath(loc.Path, relPath))
			}
		}
	}
	// Fallback: update every section's locations with the relative
	// path. Mirrors C# `:128-136`.
	for _, section := range sections {
		for _, loc := range section.Locations {
			if err := updateSection(ctx, p.doer, &p.settings, section.ID, joinSectionPath(loc.Path, relPath)); err != nil {
				return err
			}
		}
	}
	return nil
}

// pathEquals does a case-insensitive trailing-slash-tolerant compare
// — mirrors C# `IPathExtensions.PathEquals`.
func pathEquals(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, `\/`), strings.TrimRight(b, `\/`))
}

// joinSectionPath joins a Plex Location.Path + a series-relative
// path with the separator the Location uses. Mirrors C#
// `UpdateSectionPath` at `:139-150`.
func joinSectionPath(locationPath, seriesRelativePath string) string {
	separator := "/"
	if strings.Contains(locationPath, `\`) {
		separator = `\`
	}
	rel := strings.ReplaceAll(seriesRelativePath, `\`, separator)
	rel = strings.ReplaceAll(rel, "/", separator)
	return strings.TrimRight(locationPath, separator) + separator + rel
}

// OnGrab — not overridden in C#; no-op.
func (p *PlexServer) OnGrab(_ context.Context, _ notifications.GrabMessage) error { return nil }

// OnDownload mirrors C# `:35-38`.
func (p *PlexServer) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return p.updateIfEnabled(ctx, msg.Series)
}

// OnImportComplete mirrors C# `:40-43`.
func (p *PlexServer) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return p.updateIfEnabled(ctx, msg.Series)
}

// OnRename mirrors C# `:45-48`. Series is `any` per the RenameMessage
// signature; assert to *series.Series.
func (p *PlexServer) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	ser, _ := msg.Series.(*series.Series)
	return p.updateIfEnabled(ctx, ser)
}

// OnEpisodeFileDelete mirrors C# `:50-53`.
func (p *PlexServer) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return p.updateIfEnabled(ctx, msg.Series)
}

// OnSeriesAdd mirrors C# `:55-58`.
func (p *PlexServer) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return p.updateIfEnabled(ctx, msg.Series)
}

// OnSeriesDelete mirrors C# `:60-66`. Only fires when DeletedFiles is
// true.
func (p *PlexServer) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	if !msg.DeletedFiles {
		return nil
	}
	return p.updateIfEnabled(ctx, msg.Series)
}

// OnHealthIssue — not overridden in C#; no-op.
func (p *PlexServer) OnHealthIssue(_ context.Context, _ notifications.HealthCheckMessage) error {
	return nil
}

// OnHealthRestored — not overridden in C#; no-op.
func (p *PlexServer) OnHealthRestored(_ context.Context, _ notifications.HealthCheckMessage) error {
	return nil
}

// OnApplicationUpdate — not overridden in C#; no-op.
func (p *PlexServer) OnApplicationUpdate(_ context.Context, _ notifications.ApplicationUpdateMessage) error {
	return nil
}

// OnManualInteractionRequired — not overridden in C#; no-op.
func (p *PlexServer) OnManualInteractionRequired(_ context.Context, _ notifications.ManualInteractionRequiredMessage) error {
	return nil
}

// ProcessQueue drains the per-host coalescing buffer and fires the
// section-discovery + per-path Plex refresh for each batched series.
// Mirrors C# `:79-89`. The whole drain is gated on Settings.
// UpdateLibrary (matches C# `:83`). First per-series error is
// returned; remaining items are still attempted.
func (p *PlexServer) ProcessQueue(ctx context.Context) error {
	if !p.settings.UpdateLibrary {
		return nil
	}
	var firstErr error
	p.queue.Process(p.settings.Host, func(items []*mediaserverqueue.Item[bool]) {
		for _, item := range items {
			if err := p.refreshSeries(ctx, item.Series); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// Test mirrors C# `PlexServerService.Test` at `:152-184`. Fetches the
// sections list; returns the appropriate property failure on 401
// (AuthToken) or other connection failure (Host). Returns a Host
// failure if the section list is empty (Plex has no TV libraries
// configured).
func (p *PlexServer) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := p.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	sections, err := getTvSections(ctx, p.doer, &p.settings)
	if err != nil {
		prop := "Host"
		if errors.Is(err, ErrInvalidAuthToken) {
			prop = "AuthToken"
		}
		failures = append(failures, notifications.TestFailure{
			PropertyName:        prop,
			ErrorMessage:        fmt.Sprintf("Unable to connect to Plex Media Server: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}
	if len(sections) == 0 {
		failures = append(failures, notifications.TestFailure{PropertyName: "Host", ErrorMessage: "No TV library found on the Plex Media Server"})
	}
	return failures, nil
}

// SupportsOnGrab returns false — PlexServer only overrides Download/
// ImportComplete/Rename/EpisodeFileDelete/SeriesAdd/SeriesDelete.
func (p *PlexServer) SupportsOnGrab() bool { return false }

// SupportsOnDownload — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [PlexServer.SupportsOnGrab]. C# base ties
// to SupportsOnDownload.
func (p *PlexServer) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnHealthIssue() bool { return false }

// SupportsOnHealthRestored — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnHealthRestored() bool { return false }

// SupportsOnApplicationUpdate — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnApplicationUpdate() bool { return false }

// SupportsOnManualInteractionRequired — see [PlexServer.SupportsOnGrab].
func (p *PlexServer) SupportsOnManualInteractionRequired() bool { return false }

// init registers the PlexServer constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
