package mediafiles

import (
	"context"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/series"
)

// Config is the slice of `IConfigService` the C# `DiskScanService`
// reaches for at `:104, :106, :272, :279`. Five getters cover the
// optional CreateEmptySeriesFolders / DeleteEmptyFolders branches and
// the optional Linux permission set. Pulled out as a narrow interface
// so the host can wire whatever config surface eventually lands
// without dragging the whole `IConfigService` port through.
type Config interface {
	// CreateEmptySeriesFolders mirrors the C# bool getter at `:104`.
	// True → DiskScanService creates the series folder when it's
	// missing and DeleteEmptyFolders is false.
	CreateEmptySeriesFolders() bool

	// DeleteEmptyFolders mirrors the C# bool getter at `:106, :290`.
	// True → DiskScanService skips creating a missing folder AND
	// runs RemoveEmptySubfolders + DeleteFolder at the tail of every
	// happy-path scan.
	DeleteEmptyFolders() bool

	// SetPermissionsLinux mirrors the C# bool getter at `:272`. False
	// short-circuits the permission-set helper to a no-op on every
	// platform — same name as the C# field even though the Go port
	// doesn't gate on runtime OS at this layer.
	SetPermissionsLinux() bool

	// ChmodFolder mirrors the C# string getter at `:279`. Octal mode
	// passed through to `disk.SetPermissions`. Empty string is a
	// valid no-op signal on platforms where chmod is irrelevant.
	ChmodFolder() string

	// ChownGroup mirrors the C# string getter at `:279`. Group name
	// or empty string. Same passthrough semantic as ChmodFolder.
	ChownGroup() string
}

// RootFolderResolver is the slice of `IRootFolderService` the C#
// `DiskScanService` reaches for at `:79`. One method —
// `GetBestRootFolderPath(seriesPath)` — wraps the existing
// `rootfolders.Service.GetBestRootFolderPath`. Pulled out so tests
// can stub the result without spinning up a real rootfolders service.
type RootFolderResolver interface {
	GetBestRootFolderPath(ctx context.Context, path string) (string, error)
}

// SeriesGetter is the slice of `ISeriesService` the C# `DiskScanService`
// reaches for at `:305, :310`. Two methods covering the SeriesId-set
// vs SeriesId-nil command branches. The wider series-service surface
// (Add/Edit/Delete/UpdateMany) lives in `internal/series` already;
// the narrow wrapper here lets tests inject a fixture without dragging
// in a real Postgres-backed series.Service.
type SeriesGetter interface {
	// GetSeries mirrors C# `ISeriesService.GetSeries(int)` —
	// single-series lookup. Returns the row pointer + nil on hit;
	// the existing `series.Service.GetSeries` returns an
	// `errors.Is(err, repo.ErrNotFound)` sentinel when the row
	// doesn't exist, which DiskScanService surfaces via the same
	// path.
	GetSeries(ctx context.Context, id int) (*series.Series, error)

	// GetAllSeries mirrors C# `ISeriesService.GetAllSeries()` —
	// returns every series row. Used by the SeriesId-nil rescan
	// branch which fans out to one Scan call per row.
	GetAllSeries(ctx context.Context) ([]series.Series, error)
}

// EpisodeFileService is the slice of `IMediaFileService` the C#
// `DiskScanService` reaches for at `:136, :171`. Three methods —
// the read for size-update reconciliation, the write for the
// post-scan size-update batch, and the stand-alone helper that
// removes already-imported files from the candidate list before
// the import-decision pass.
//
// Wraps the existing `episodefiles.Service` surface so the host can
// pass `*episodefiles.Service` directly at boot. Tests inject a stub.
type EpisodeFileService interface {
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
	UpdateMany(ctx context.Context, files []*episodefiles.File) error
	FilterExistingFiles(ctx context.Context, candidates []string, parent *series.Series) ([]string, error)
}

// MediaFileTableCleanup is the slice of `IMediaFileTableCleanupService`
// the C# `DiskScanService` reaches for at `:193`. One method — the
// rebalance pass that drops EpisodeFile rows whose on-disk path is
// missing from the supplied list. Wraps the existing
// `episodefiles.CleanupService.Clean` (slice 3.15) which returns
// `(deletedFiles int, clearedEpisodes int, err error)`; the
// DiskScanService caller doesn't read the counts but the wider
// shape matches.
type MediaFileTableCleanup interface {
	Clean(ctx context.Context, parent *series.Series, filesOnDisk []string) (deletedFiles, clearedEpisodes int, err error)
}

// ImportDecisionMaker is the slice of `IMakeImportDecision` the C#
// `DiskScanService` reaches for at `:140`. The full
// `MediaFiles/EpisodeImport` subsystem hasn't ported yet, so the
// returned-decision values are typed `any` for now — Service treats
// them as opaque pass-through values into [ImportApprovedEpisodes].
//
// When the EpisodeImport port lands, `[]any` becomes a real
// `[]ImportDecision` slice; the interface signature stays otherwise
// stable. Production code wires a no-op stub at boot until then.
type ImportDecisionMaker interface {
	GetImportDecisions(ctx context.Context, files []string, parent *series.Series, filterExistingFiles bool) ([]any, error)
}

// ImportApprovedEpisodes is the slice of `IImportApprovedEpisodes`
// the C# `DiskScanService` reaches for at `:143`. Same opaque-value
// posture as [ImportDecisionMaker] — the slice receives whatever
// [ImportDecisionMaker.GetImportDecisions] produced and decides what
// to commit. Production wires a no-op stub until the EpisodeImport
// port lands.
type ImportApprovedEpisodes interface {
	Import(ctx context.Context, decisions []any, copyOnly bool) error
}

// UpdateMediaInfo is the slice of `IUpdateMediaInfo` the C#
// `DiskScanService` reaches for at `:162`. Single method invoked by
// the size-update loop — when a re-stat'd file's size differs from
// the persisted row, DiskScanService asks the MediaInfo subsystem
// to refresh the embedded MediaInfo blob. Returns true if the
// MediaInfo refresh succeeded (which also writes the row); false
// signals "MediaInfo unavailable, please update the row yourself".
//
// The bool return is the trigger for the `filesToUpdate` queue at
// `:164` — a false from Update means the size change is the only
// edit and DiskScanService has to commit it via the bulk
// `mediaFileService.Update(filesToUpdate)` path.
type UpdateMediaInfo interface {
	Update(ctx context.Context, file *episodefiles.File, parent *series.Series) (bool, error)
}
