// DeleteBadMediaCovers housekeeper. Ports the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/DeleteBadMediaCovers.cs`.
//
// Sweeps MetadataFiles rows whose on-disk .jpg payload is actually an
// HTML error page — the residue of a metadata-source HTTP fetch that
// returned a 4xx/5xx body instead of the requested image. These rows
// poison the metadata-write pipeline forever otherwise.
//
// # One-shot semantics
//
// The C# housekeeper at `:33-66` reads
// `ConfigService.CleanupMetadataImages`, performs the sweep when the
// flag is true, and writes the flag back to false at the end. So the
// task fires once on the first housekeeping pass after install (the
// flag defaults to true at `ConfigService.cs:381`); subsequent passes
// short-circuit at the gate. The Go port preserves this exactly — a
// re-trigger requires an operator to flip the flag back to true via
// the config table.
//
// # Detection algorithm
//
// For each candidate row (Type doesn't matter — every row's path is
// inspected), the C# code at `:74-91`:
//
//  1. Opens the file via DiskProvider.OpenReadStream.
//  2. Checks `Length < 10 bytes` — short files are invalid.
//  3. Reads the first 10 bytes.
//  4. Decodes them as `Encoding.Default` (Windows default, mostly
//     ASCII-compatible) and tests for substring "html"
//     (case-insensitive).
//
// The Go port mirrors the algorithm: read up to 10 bytes, fail-fast
// if fewer arrived, lowercase the ASCII view, test for "html". The
// C# `Encoding.Default` is locale-dependent on Windows but the first
// 10 bytes of an HTML response are always ASCII (`<!DOCTYPE`, `<html`,
// `<HTML`, ...) so the simpler ASCII-only path is equivalent in
// practice.
//
// # Date + extension filter
//
// The C# code at `:44-45` restricts to rows with
// `LastUpdated > 2014-12-27` AND `RelativePath.EndsWith(".jpg")`. The
// date pins the regression to v2-era affected releases — earlier rows
// are from before the bug introduced and are NOT poisoned. The Go
// port preserves both predicates verbatim.

package housekeeping

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/metadatafiles"
)

// OSMediaCoverImageStore is the production [MediaCoverImageStore]
// backed by the stdlib os package. Zero value is usable.
type OSMediaCoverImageStore struct{}

// OpenReadStream implements MediaCoverImageStore.
func (OSMediaCoverImageStore) OpenReadStream(path string) (io.ReadCloser, error) {
	return os.Open(path) //nolint:gosec // path is constructed from metadata-files RelativePath under series root — trusted source.
}

// FileSize implements MediaCoverImageStore.
func (OSMediaCoverImageStore) FileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DeleteFile implements MediaCoverImageStore.
func (OSMediaCoverImageStore) DeleteFile(path string) error {
	return os.Remove(path)
}

// badMediaCoversCutoff is the LastUpdated lower bound from C#
// `DeleteBadMediaCovers.cs:45`. Rows older than this are NOT inspected
// — the corrupt-image bug only affected releases after this date.
var badMediaCoversCutoff = time.Date(2014, 12, 27, 0, 0, 0, 0, time.UTC)

// MediaCoverImageStore narrows the disk surface this task reaches
// for. Tests inject a fake; the production binding is
// [OSMediaCoverImageStore].
type MediaCoverImageStore interface {
	// OpenReadStream opens the supplied path for reading. The
	// returned ReadCloser must be closed by the caller. Errors
	// propagate (missing file, permission denied, ...) and are
	// per-file non-fatal (logged + skipped).
	OpenReadStream(path string) (io.ReadCloser, error)

	// FileSize returns the size of the supplied file in bytes.
	// Errors propagate and are per-file non-fatal.
	FileSize(path string) (int64, error)

	// DeleteFile removes the supplied file. Errors propagate.
	DeleteFile(path string) error
}

// MediaCoverConfig narrows the config surface this task reaches for
// — the one-shot CleanupMetadataImages flag.
type MediaCoverConfig interface {
	CleanupMetadataImages() bool
	SetCleanupMetadataImages(ctx context.Context, v bool) error
}

// MetadataFileSweepRepo narrows the metadatafiles repo to the two
// methods this task reaches for.
type MetadataFileSweepRepo interface {
	AllBySeriesID(ctx context.Context, seriesID int) ([]metadatafiles.MetadataFile, error)
	Delete(ctx context.Context, id int) error
}

// NewDeleteBadMediaCovers ports
// `Housekeepers/DeleteBadMediaCovers.cs:1-93`. One-shot housekeeper
// gated on ConfigService.CleanupMetadataImages.
func NewDeleteBadMediaCovers(
	cfg MediaCoverConfig,
	repo MetadataFileSweepRepo,
	seriesPaths SeriesPaths,
	disk MediaCoverImageStore,
	log *slog.Logger,
) (Task, error) {
	if cfg == nil {
		return nil, fmt.Errorf("housekeeping: NewDeleteBadMediaCovers: nil MediaCoverConfig")
	}
	if repo == nil {
		return nil, fmt.Errorf("housekeeping: NewDeleteBadMediaCovers: nil MetadataFileSweepRepo")
	}
	if seriesPaths == nil {
		return nil, fmt.Errorf("housekeeping: NewDeleteBadMediaCovers: nil SeriesPaths")
	}
	if disk == nil {
		disk = OSMediaCoverImageStore{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &deleteBadMediaCovers{cfg: cfg, repo: repo, seriesPaths: seriesPaths, disk: disk, log: log}, nil
}

type deleteBadMediaCovers struct {
	cfg         MediaCoverConfig
	repo        MetadataFileSweepRepo
	seriesPaths SeriesPaths
	disk        MediaCoverImageStore
	log         *slog.Logger
}

// Name implements Task.
func (t *deleteBadMediaCovers) Name() string {
	return "DeleteBadMediaCovers"
}

// Clean implements Task. Gate -> per-series sweep -> reset flag.
//
// Per-image validation errors are logged + skipped (matches C#
// try/catch at `:58-61`); the loop continues across the failure.
// Per-series fetch errors propagate immediately (the C# loop has no
// catch around `GetFilesBySeries`).
func (t *deleteBadMediaCovers) Clean(ctx context.Context) error {
	if !t.cfg.CleanupMetadataImages() {
		return nil
	}
	paths, err := t.seriesPaths.AllSeriesPaths(ctx)
	if err != nil {
		return fmt.Errorf("housekeeping: DeleteBadMediaCovers: AllSeriesPaths: %w", err)
	}
	for seriesID, seriesPath := range paths {
		files, err := t.repo.AllBySeriesID(ctx, seriesID)
		if err != nil {
			return fmt.Errorf("housekeeping: DeleteBadMediaCovers (series=%d): %w", seriesID, err)
		}
		for _, f := range files {
			if !t.candidate(f) {
				continue
			}
			full := filepath.Join(seriesPath, f.RelativePath)
			valid, err := t.isValidImage(full)
			if err != nil {
				t.log.LogAttrs(ctx, slog.LevelError,
					"housekeeping: DeleteBadMediaCovers: validate image",
					slog.Int("series", seriesID),
					slog.Int("metadataFile", f.ID),
					slog.String("path", full),
					slog.Any("err", err))
				continue
			}
			if valid {
				continue
			}
			t.log.LogAttrs(ctx, slog.LevelInfo,
				"housekeeping: deleting invalid image file",
				slog.String("path", full))
			if err := t.deleteMetadata(ctx, f.ID, full); err != nil {
				return fmt.Errorf("housekeeping: DeleteBadMediaCovers (series=%d, id=%d): %w", seriesID, f.ID, err)
			}
		}
	}
	if err := t.cfg.SetCleanupMetadataImages(ctx, false); err != nil {
		return fmt.Errorf("housekeeping: DeleteBadMediaCovers: reset flag: %w", err)
	}
	return nil
}

// candidate mirrors C# `:44-45` — LastUpdated after the cutoff AND
// .jpg extension (case-insensitive).
func (t *deleteBadMediaCovers) candidate(f metadatafiles.MetadataFile) bool {
	if !f.LastUpdated.After(badMediaCoversCutoff) {
		return false
	}
	if !strings.EqualFold(filepath.Ext(f.RelativePath), ".jpg") {
		return false
	}
	return true
}

// isValidImage mirrors C# `:74-91` — open, size>=10, read first 10
// bytes, check for "html" substring (case-insensitive).
func (t *deleteBadMediaCovers) isValidImage(path string) (bool, error) {
	size, err := t.disk.FileSize(path)
	if err != nil {
		return false, fmt.Errorf("size: %w", err)
	}
	const headerLen = 10
	if size < headerLen {
		return false, nil
	}
	rc, err := t.disk.OpenReadStream(path)
	if err != nil {
		return false, fmt.Errorf("open: %w", err)
	}
	defer rc.Close() //nolint:errcheck // close-after-read on a read-only stream has no useful err.
	buf := make([]byte, headerLen)
	if _, err := io.ReadFull(rc, buf); err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	return !strings.Contains(strings.ToLower(string(buf)), "html"), nil
}

// deleteMetadata mirrors C# `:68-72` — drop the DB row, then the
// disk file. The C# tree's order matters: on a Delete failure the
// disk file stays untouched, surfacing the failure as a per-row
// error rather than a half-state.
func (t *deleteBadMediaCovers) deleteMetadata(ctx context.Context, id int, path string) error {
	if err := t.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("repo delete: %w", err)
	}
	if err := t.disk.DeleteFile(path); err != nil {
		return fmt.Errorf("disk delete: %w", err)
	}
	return nil
}
