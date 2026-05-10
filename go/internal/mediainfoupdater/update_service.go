// Package mediainfoupdater is the Go port of
// `NzbDrone.Core.MediaFiles.MediaInfo.UpdateMediaInfoService` — the
// per-file MediaInfo refresh pathway. Lives in its own package
// rather than under `internal/mediainfo` to dodge an import cycle:
// `episodefiles.File.MediaInfo` is `*mediainfo.Model`, so
// episodefiles imports mediainfo, and the Update service needs both
// episodefiles + mediainfo. The host-side wiring layer (cmd/sonarr)
// + the slice-3.AE [mediafiles.UpdateMediaInfo] narrow interface
// stitch this package into the broader graph.
//
// Sub-arc breakdown for queue [13]:
//   - 13a (slice 3.BH): UpdateService struct + Update +
//     UpdateMediaInfo + the four narrow deps. Satisfies the
//     existing [mediafiles.UpdateMediaInfo] contract.
//   - 13b: HandleSeriesScanned (the IHandle<SeriesScannedEvent>
//     port) + GetFilesBySeries reader + boot-site wire.
//   - 13c: VideoFileInfoReader port (ffmpeg / MediaInfo native
//     bindings).
package mediainfoupdater

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/series"
)

// MinimumSchemaRevision mirrors C#
// `VideoFileInfoReader.MINIMUM_MEDIA_INFO_SCHEMA_REVISION = 14` at
// `MediaInfo/VideoFileInfoReader.cs:24`. Any persisted MediaInfo row
// whose `SchemaRevision` is below this value is treated as stale and
// re-extracted on the next SeriesScannedEvent fan-out (sub-arc 13b).
//
// The C# tree pins this constant on VideoFileInfoReader; the Go-port
// places it on this package so the slice-3.BH UpdateService can
// reference it without dragging in the (still-deferred) reader
// implementation. Sub-arc 13b's HandleSeriesScanned uses this for
// the "stale rows need re-extract" filter.
const MinimumSchemaRevision = 14

// FileSystem narrows [*disk.Provider] to the one method the C#
// UpdateMediaInfoService reaches for via IDiskProvider — FileExists.
// Defined locally so tests can stub the disk read without standing
// up a real Provider.
//
// Mirrors the C# `IDiskProvider.FileExists(string)` call at
// `UpdateMediaInfoService.cs:74`.
type FileSystem interface {
	FileExists(path string) bool
}

// FileWriter narrows [*episodefiles.Service] to the Update method
// the UpdateMediaInfoService reaches for via IMediaFileService.Update.
//
// Mirrors the C# `IMediaFileService.Update(EpisodeFile)` call at
// `UpdateMediaInfoService.cs:91`. Differs from C# in that the Update
// is context-aware; the spec passes ctx through.
type FileWriter interface {
	Update(ctx context.Context, f *episodefiles.File) error
}

// FileReader narrows [*episodefiles.Service] to the GetFilesBySeries
// method the SeriesScannedEvent IHandle subscriber reaches for via
// IMediaFileService.GetFilesBySeries.
//
// Mirrors the C# `IMediaFileService.GetFilesBySeries(int)` call at
// `UpdateMediaInfoService.cs:48`. Returns the value-typed slice
// (matches *episodefiles.Service.GetFilesBySeries signature). The
// handler iterates this list looking for rows whose persisted
// MediaInfo is nil OR has a SchemaRevision below
// [MinimumSchemaRevision] — those rows get re-extracted via
// UpdateMediaInfo.
type FileReader interface {
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// MediaInfoReader narrows the as-yet-unported [VideoFileInfoReader]
// to the one method UpdateMediaInfoService reaches for via
// IVideoFileInfoReader.GetMediaInfo. Returns the parsed *Model on
// success, or nil when the underlying ffmpeg / MediaInfo native
// library couldn't read the file (corrupt / unsupported codec).
// Errors fall through as nil — the C# tree treats GetMediaInfo
// failures as "skip this file, log warn" rather than propagating.
//
// Slice 3.BH ships the Service surface against this interface; the
// real reader (the heavy ffmpeg-using probe) ports in sub-arc 13c
// (slice 3.BJ). Until then the boot-site wires a no-op stub that
// always returns nil — every Update call short-circuits at the
// nil-result check, so the audit trail still fires the
// "Updated MediaInfo" debug log per file but no field actually
// updates. Documented as the Phase-3-without-13c posture.
type MediaInfoReader interface {
	GetMediaInfo(ctx context.Context, path string) (*mediainfo.Model, error)
}

// EnableMediaInfoConfigReader narrows [*config.Provider] to the
// EnableMediaInfo flag the UpdateMediaInfoService gates the whole
// Update entry point on.
//
// Mirrors the C# `IConfigService.EnableMediaInfo` getter at
// `UpdateMediaInfoService.cs:42 + :61`. When false, the Service
// returns a no-op false from Update without touching disk or DB.
type EnableMediaInfoConfigReader interface {
	EnableMediaInfo() bool
}

// UpdateService ports `UpdateMediaInfoService` at
// `MediaFiles/MediaInfo/UpdateMediaInfoService.cs:19-99`. Provides
// the per-file MediaInfo refresh pathway the DiskScanService size-
// update loop + the SeriesScannedEvent IHandle subscriber both
// route through.
//
// Goroutine-safe: stateless after construction.
type UpdateService struct {
	disk       FileSystem
	files      FileWriter
	fileReader FileReader
	reader     MediaInfoReader
	cfg        EnableMediaInfoConfigReader
	log        *slog.Logger
}

// NewUpdateService binds the five narrow deps. nil-required-dep
// returns a typed error (sibling spec-constructor posture).
//
// nil-log defaults to slog.Default() so the boot-site can drop a
// nil through if it doesn't have a per-service logger.
func NewUpdateService(
	disk FileSystem,
	files FileWriter,
	fileReader FileReader,
	reader MediaInfoReader,
	cfg EnableMediaInfoConfigReader,
	log *slog.Logger,
) (*UpdateService, error) {
	if disk == nil {
		return nil, errors.New("mediainfoupdater: NewUpdateService: disk is nil")
	}
	if files == nil {
		return nil, errors.New("mediainfoupdater: NewUpdateService: files is nil")
	}
	if fileReader == nil {
		return nil, errors.New("mediainfoupdater: NewUpdateService: fileReader is nil")
	}
	if reader == nil {
		return nil, errors.New("mediainfoupdater: NewUpdateService: reader is nil")
	}
	if cfg == nil {
		return nil, errors.New("mediainfoupdater: NewUpdateService: cfg is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpdateService{
		disk:       disk,
		files:      files,
		fileReader: fileReader,
		reader:     reader,
		cfg:        cfg,
		log:        log,
	}, nil
}

// Update ports the public C# entry-point at
// `UpdateMediaInfoService.cs:59-68`. Mirrors the C# `IUpdateMediaInfo.Update`
// contract — the size-update loop in DiskScanService routes through
// this so the EnableMediaInfo gate fires once per file rather than
// requiring every caller to check the flag.
//
// Returns (false, nil) when EnableMediaInfo is disabled. Otherwise
// delegates to UpdateMediaInfo.
//
// nil-receiver / nil-file / nil-series → typed error so caller bugs
// surface loudly instead of being swallowed as "no update needed".
func (u *UpdateService) Update(ctx context.Context, file *episodefiles.File, parent *series.Series) (bool, error) {
	if u == nil {
		return false, errors.New("mediainfoupdater: Update on nil receiver")
	}
	if file == nil {
		return false, errors.New("mediainfoupdater: Update: nil file")
	}
	if parent == nil {
		return false, errors.New("mediainfoupdater: Update: nil series")
	}
	if !u.cfg.EnableMediaInfo() {
		u.log.Debug("mediainfoupdater: Update skipped — EnableMediaInfo is disabled")
		return false, nil
	}
	return u.UpdateMediaInfo(ctx, file, parent)
}

// UpdateMediaInfo ports `UpdateMediaInfoService.cs:70-97`. Performs
// the actual MediaInfo refresh:
//
//  1. Resolve the on-disk path: `file.Path` if non-empty, else
//     `filepath.Join(parent.Path, file.RelativePath)`. Mirrors the
//     C# `:72` ternary. Note: the Go-port File doesn't carry a
//     [Path] field (the C# tree composes it lazily); the Go-port
//     always falls through to the join arm because the path field
//     isn't surfaced. Documented as a divergence — production
//     wiring populates RelativePath always, so the join arm is the
//     canonical path resolution anyway.
//  2. Skip if the file doesn't exist on disk (early-return false).
//  3. Read the live MediaInfo via the reader; nil result → false.
//  4. Stamp file.MediaInfo with the new value.
//  5. Persist via files.Update IF file.ID != 0 (the C# tree's
//     "skip persistence for not-yet-saved files" arm at `:89`).
//
// Returns true on a successful refresh that updated either the
// in-memory file (ID==0 path) or the persisted row.
//
// Persistence errors propagate; disk-read failures and nil-MediaInfo
// returns are logged at debug and resolve to (false, nil).
//
// Unlike the C# tree this method does NOT re-check EnableMediaInfo
// — callers that bypass [Update] in favor of UpdateMediaInfo
// directly are accepting the responsibility of having checked the
// flag (mirrors C# `:67` direct-call shape).
func (u *UpdateService) UpdateMediaInfo(ctx context.Context, file *episodefiles.File, parent *series.Series) (bool, error) {
	if u == nil {
		return false, errors.New("mediainfoupdater: UpdateMediaInfo on nil receiver")
	}
	if file == nil {
		return false, errors.New("mediainfoupdater: UpdateMediaInfo: nil file")
	}
	if parent == nil {
		return false, errors.New("mediainfoupdater: UpdateMediaInfo: nil series")
	}
	path := resolveFilePath(file, parent)
	if path == "" {
		u.log.Debug("mediainfoupdater: UpdateMediaInfo skipped — empty path")
		return false, nil
	}
	if !u.disk.FileExists(path) {
		u.log.Debug("mediainfoupdater: can't update — file missing", "path", path)
		return false, nil
	}
	updated, err := u.reader.GetMediaInfo(ctx, path)
	if err != nil {
		u.log.Debug("mediainfoupdater: reader error — treating as nil result", "path", path, "err", err)
		return false, nil
	}
	if updated == nil {
		return false, nil
	}
	file.MediaInfo = updated
	if file.ID != 0 {
		if err := u.files.Update(ctx, file); err != nil {
			return false, fmt.Errorf("mediainfoupdater: persist updated file: %w", err)
		}
	}
	u.log.Debug("mediainfoupdater: updated", "path", path)
	return true, nil
}

// resolveFilePath mirrors the C# path-derivation at
// `UpdateMediaInfoService.cs:72`. The C# code prefers `episodeFile.Path`
// when non-empty (the EpisodeFile.Path is a computed property at
// `EpisodeFile.cs` that returns the persisted absolute path when
// stamped, else the lazy series.Path+RelativePath join).
//
// The Go-port File struct has no Path field — RelativePath is the
// only persisted form — so the resolver always uses the join. Empty
// RelativePath returns empty string (the caller short-circuits).
func resolveFilePath(file *episodefiles.File, parent *series.Series) string {
	if file == nil {
		return ""
	}
	if file.RelativePath == "" {
		return ""
	}
	if parent == nil || parent.Path == "" {
		return file.RelativePath
	}
	return filepath.Join(parent.Path, file.RelativePath)
}
