package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// seriesAddedFolderConfig is the narrow `*config.Provider` slice
// the folder-creation subscriber needs — the two boolean settings
// that gate the C# `DiskScanService.cs:102-117` "create missing
// series folder" arm. Defined as a small interface so the test
// suite can swap a stub without standing up a full config Provider.
//
// `*config.Provider` satisfies this structurally via the existing
// CreateEmptySeriesFolders + DeleteEmptyFolders accessors.
type seriesAddedFolderConfig interface {
	CreateEmptySeriesFolders() bool
	DeleteEmptyFolders() bool
}

// seriesAddedFolderDisk is the narrow `*disk.Provider` slice the
// subscriber needs — FolderExists for the gate + CreateFolder for
// the side effect.
type seriesAddedFolderDisk interface {
	FolderExists(path string) bool
	CreateFolder(path string) error
}

// subscribeSeriesAddedFolderCreate wires a [series.SeriesAddedEvent]
// subscriber onto bus that creates the series-folder on disk when
// the C# gate at `DiskScanService.cs:102-117` would fire. Mirrors the
// C# flow: `SeriesAddedEvent` → `SeriesAddedHandler.Handle` pushes
// `RefreshSeriesCommand` → `RefreshSeriesService.Execute` calls
// `DiskScanService.Scan` → the
// `if (!seriesFolderExists && CreateEmptySeriesFolders &&
// !DeleteEmptyFolders) { CreateFolder; }` arm fires.
//
// The Go-port DOES NOT yet have a RefreshSeries handler (audit B.3
// row: BLOCKED on SkyHook M3) or the DiskScan service ported, so
// slice 5.J185 short-circuits the chain — the folder-creation arm is
// the user-visible piece that matters for the import-list sync flow
// (J183/J184): a freshly-added series row should have its on-disk
// folder created before any subsequent scan/import work runs. The
// rest of the C# DiskScan side-effects (FilterPaths + GetVideoFiles
// + CleanMediaFiles + the actual scan pipeline) all require the
// DiskScan service port which lands separately. SetPermissions is
// also deferred (Linux-only and gated on a separate config flag
// not yet ported).
//
// When the gate is OFF (CreateEmptySeriesFolders=false, the default)
// the subscriber is a no-op — preserves the C# behaviour where
// freshly-added series have NO folder until an actual file lands
// there, matching the user expectation when they explicitly disable
// the auto-create.
//
// Errors during CreateFolder degrade to a warn log; the subscriber
// returns nil so the bus doesn't accumulate failed-handler state
// (same posture as every other Phase 4 bus subscriber).
//
// The subscription tag includes "seriesadded.folder" so the bus
// can route the right handler if multiple SeriesAddedEvent
// subscribers ever land.
func subscribeSeriesAddedFolderCreate(
	bus *messaging.Bus,
	cfg seriesAddedFolderConfig,
	disk seriesAddedFolderDisk,
	log *slog.Logger,
) {
	messaging.Subscribe(bus, "seriesadded.folder.create",
		func(ctx context.Context, ev series.SeriesAddedEvent) error {
			handleSeriesAddedForFolderCreate(ctx, ev, cfg, disk, log)
			return nil
		})
}

// handleSeriesAddedForFolderCreate factors out the gate logic so
// tests can drive it without a real *messaging.Bus or a full
// subscribe-then-publish round-trip. Returns no value — the event
// loop is fire-and-forget; errors are logged.
func handleSeriesAddedForFolderCreate(
	ctx context.Context,
	ev series.SeriesAddedEvent,
	cfg seriesAddedFolderConfig,
	disk seriesAddedFolderDisk,
	log *slog.Logger,
) {
	if ev.Series == nil {
		return
	}
	path := ev.Series.Path
	if path == "" {
		// Series rows with no on-disk path (rare — usually only
		// transient AddOptions=NoFolder paths) skip the gate
		// entirely. Matches C# `DiskScanService` returning early
		// when there's no folder to scan.
		return
	}
	if !cfg.CreateEmptySeriesFolders() {
		log.LogAttrs(ctx, slog.LevelDebug,
			"seriesadded: folder creation skipped (CreateEmptySeriesFolders=false)",
			slog.Int("seriesId", ev.Series.ID),
			slog.String("path", path))
		return
	}
	if cfg.DeleteEmptyFolders() {
		// Mirrors C# `:106-109` — when both flags are on, the
		// would-be-created folder would immediately be deleted by
		// the CleanUpRecycleBin scheduled task, so C# logs the
		// no-op and returns. Same posture here.
		log.LogAttrs(ctx, slog.LevelDebug,
			"seriesadded: folder creation skipped (DeleteEmptyFolders=true would immediately drop it)",
			slog.Int("seriesId", ev.Series.ID),
			slog.String("path", path))
		return
	}
	if disk.FolderExists(path) {
		log.LogAttrs(ctx, slog.LevelDebug,
			"seriesadded: folder already exists, no creation needed",
			slog.Int("seriesId", ev.Series.ID),
			slog.String("path", path))
		return
	}
	log.LogAttrs(ctx, slog.LevelInfo,
		"seriesadded: creating missing series folder",
		slog.Int("seriesId", ev.Series.ID),
		slog.String("path", path))
	if err := disk.CreateFolder(path); err != nil {
		log.LogAttrs(ctx, slog.LevelWarn,
			"seriesadded: CreateFolder failed (folder will be retried on next scan)",
			slog.Int("seriesId", ev.Series.ID),
			slog.String("path", path),
			slog.Any("err", err))
	}
}
