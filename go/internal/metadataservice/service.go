// Package metadataservice is the Go port of the consumer-facing
// `NzbDrone.Core.Extras.Metadata.MetadataService` orchestrator that
// fans an event (series scan, episode import, rename, etc.) out
// across every enabled [metadata.Provider] writer and persists the
// resulting on-disk + database state.
//
// Queue [W6] of Phase 4 focused-subset 6 (METADATA-WRITERS family).
// Slice 4.BI ships sub-arc W6a — Service struct foundation + 8
// private process/save/download helpers + the dedup-aware
// per-consumer file lookup. Sub-arc W6b ships the 8 public
// IManageExtraFiles methods (CreateAfterX / Move / Import etc.).
// Sub-arc W6c wires main.go and closes the queue.
//
// The C# class inherits `ExtraFileManager<MetadataFile>` which the
// Go port does NOT yet have a base for — Subtitles + Other Extras
// extra-file managers haven't ported. The Go service is therefore a
// concrete struct without an abstract-base hierarchy; once a sibling
// extras-manager ports we can revisit a shared `extras.Manager`
// interface.
//
// All C# cites lead each type or function with a file:line reference.
package metadataservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// DiskWriter narrows the disk surface [Service] needs to write
// metadata bodies + check / copy image files. Mirrors the C# `IDiskProvider`
// methods at:
//   - `WriteAllText(path, contents)` → C# `:509`.
//   - `FileExists(path)` → C# `:350`, `:387`, `:425`, `:479`.
//   - `CopyFile(src, dst)` → C# `:481`.
//   - `GetParentFolder(path)` → C# `:529` for recyclebin sub-folder.
type DiskWriter interface {
	WriteAllText(path, contents string) error
	FileExists(path string) bool
	CopyFile(src, dst string) error
}

// HTTPDownloader narrows the HTTP client surface — only the
// download-to-disk helper used by [Service.DownloadImage]. Mirrors
// C# `IHttpClient.DownloadFile(url, fileName)` at `:477`.
type HTTPDownloader interface {
	DownloadFile(ctx context.Context, url, fileName string) error
}

// MetadataFileStore narrows the [metadatafiles.Repo] surface to the
// Insert / Update / Delete methods [Service] needs for the MetadataFiles
// table writes. Production binding is `*metadatafiles.Repo` directly.
type MetadataFileStore interface {
	// Insert adds a new row + populates its ID via SetID.
	Insert(ctx context.Context, m *metadatafiles.MetadataFile) (int, error)

	// Update writes the row in place.
	Update(ctx context.Context, m *metadatafiles.MetadataFile) error

	// Delete removes the row by ID; ErrNotFound on missing.
	Delete(ctx context.Context, id int) error
}

// MediaFileAttributes narrows the helper that adjusts the file's
// permissions after write. The C# `IMediaFileAttributeService.SetFilePermissions`
// is a no-op on Windows and sets file mode on POSIX. Mirrors C# `:490`,
// `:510`. The Go port keeps the narrow surface — `RecycleBin` and the
// real `mediafileattributes` package will satisfy this when wired by
// main.go.
type MediaFileAttributes interface {
	SetFilePermissions(path string) error
}

// RecycleBin narrows the file-delete-to-recyclebin helper. Mirrors
// C# `IRecycleBinProvider.DeleteFile(path, subfolder)` at `:530`.
type RecycleBin interface {
	DeleteFile(path, subfolder string) error
}

// OtherExtraRenamer narrows the cross-extras filename-collision
// renamer. Mirrors C# `IOtherExtraFileRenamer.RenameOtherExtraFile`
// at `:300`, `:356`, `:393`, `:431`. The Go port has no other extra
// types yet (subtitles + other extras are not in scope), so the
// production binding is a no-op. The interface stays so the port
// faithfully threads the C# call sites; nil-tolerant.
type OtherExtraRenamer interface {
	RenameOtherExtraFile(s *series.Series, fullPath string) error
}

// DiskTransfer narrows the file-move helper. Mirrors C#
// `IDiskTransferService.TransferFile(src, dst, TransferMode.Move)`
// at `:310`, `:441`.
type DiskTransfer interface {
	TransferFile(src, dst string, mode TransferModeT) error
}

// TransferModeT mirrors the C# `TransferMode` enum subset the
// service uses. The Go port keeps the typed wrapper for parity even
// though only `Move` is ever passed. Future-proofs against a
// callsite that wants Copy.
type TransferModeT int

const (
	// TransferModeMove is the C# `TransferMode.Move`.
	TransferModeMove TransferModeT = iota
	// TransferModeCopy is the C# `TransferMode.Copy` — never used by
	// MetadataService today; kept for parity.
	TransferModeCopy
)

// SeasonsLister narrows the per-series seasons enumerator
// [Service.ProcessSeasonImages] consults. Mirrors C# `series.Seasons`
// — a list of `Tv.Season` rows the C# class iterates at `:381`.
//
// Production binding will decode the series.Series.Seasons JSON-blob
// into `[]metadata.Season` (the same shape Provider.SeasonImages
// already accepts). nil-tolerant — SeasonImages emission is silently
// skipped when nil.
type SeasonsLister interface {
	SeasonsForSeries(s *series.Series) ([]metadata.Season, error)
}

// Deps bundles every dep the [Service] consumes. Every field is
// required EXCEPT OtherRenamer (nil → no-op cross-extra rename).
type Deps struct {
	// Disk is the filesystem surface used for write / exists /
	// copy.
	Disk DiskWriter

	// HTTP is the HTTP client used for image-URL downloads.
	HTTP HTTPDownloader

	// Files is the MetadataFiles-table CRUD surface.
	Files MetadataFileStore

	// Attrs is the file-permission post-write hook.
	Attrs MediaFileAttributes

	// Bin is the recyclebin file-mover.
	Bin RecycleBin

	// OtherRenamer is the cross-extras filename-collision renamer.
	// nil-tolerant.
	OtherRenamer OtherExtraRenamer

	// Transfer is the file-move helper used when EpisodeMetadata /
	// EpisodeImage outputs need to relocate.
	Transfer DiskTransfer

	// Seasons is the per-series seasons lister used by
	// ProcessSeasonImages. nil-tolerant — SeasonImages emission is
	// silently skipped.
	Seasons SeasonsLister

	// Log is the slog.Logger. nil → slog.Default.
	Log *slog.Logger
}

// Service is the metadata-writer orchestrator. Constructed by
// [NewService] with all required deps; goroutine-safe stateless
// after construction.
//
// Mirrors the C# `MetadataService` class at
// `src/NzbDrone.Core/Extras/Metadata/MetadataService.cs:20-536`.
type Service struct {
	disk         DiskWriter
	http         HTTPDownloader
	files        MetadataFileStore
	attrs        MediaFileAttributes
	bin          RecycleBin
	otherRenamer OtherExtraRenamer
	transfer     DiskTransfer
	seasons      SeasonsLister
	log          *slog.Logger
}

// NewService binds the [Deps] bundle. Returns a typed error when
// any required dep is nil; OtherRenamer + Seasons are nil-tolerant.
func NewService(deps Deps) (*Service, error) {
	if deps.Disk == nil {
		return nil, errors.New("metadataservice: NewService: Disk is nil")
	}
	if deps.HTTP == nil {
		return nil, errors.New("metadataservice: NewService: HTTP is nil")
	}
	if deps.Files == nil {
		return nil, errors.New("metadataservice: NewService: Files is nil")
	}
	if deps.Attrs == nil {
		return nil, errors.New("metadataservice: NewService: Attrs is nil")
	}
	if deps.Bin == nil {
		return nil, errors.New("metadataservice: NewService: Bin is nil")
	}
	if deps.Transfer == nil {
		return nil, errors.New("metadataservice: NewService: Transfer is nil")
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		disk:         deps.Disk,
		http:         deps.HTTP,
		files:        deps.Files,
		attrs:        deps.Attrs,
		bin:          deps.Bin,
		otherRenamer: deps.OtherRenamer,
		transfer:     deps.Transfer,
		seasons:      deps.Seasons,
		log:          log,
	}, nil
}

// GetMetadataFilesForConsumer filters the per-series existing-
// metadata file list down to entries produced by the given consumer.
// Mirrors C# `:241-244`.
//
// The C# version uses `consumer.GetType().Name` to derive the
// Consumer column key. The Go port accepts a [metadata.Provider] and
// reaches into Definition().Implementation — which is the same C#
// type-name string stored at registration time.
func (s *Service) GetMetadataFilesForConsumer(consumer metadata.Provider, all []metadatafiles.MetadataFile) []metadatafiles.MetadataFile {
	if consumer == nil {
		return nil
	}
	consumerName := consumerName(consumer)
	if consumerName == "" {
		return nil
	}
	out := make([]metadatafiles.MetadataFile, 0, len(all))
	for _, f := range all {
		if f.Consumer == consumerName {
			out = append(out, f)
		}
	}
	return out
}

// GetMetadataFile finds the first row in `existing` matching
// `predicate`. When multiple rows match, the duplicates are
// recyclebin-deleted + removed from the [MetadataFileStore]; the
// first match is returned. Mirrors C# `:513-535`.
//
// Errors from the dup-cleanup path are logged + ignored (matches C#
// which swallows IOException via the IRecycleBinProvider catch).
// The returned row pointer references the SAME slice element — the
// caller may mutate fields in place; the C# tree relies on this.
func (s *Service) GetMetadataFile(ctx context.Context, sr *series.Series, existing []metadatafiles.MetadataFile, predicate func(metadatafiles.MetadataFile) bool) (*metadatafiles.MetadataFile, error) {
	if s == nil || predicate == nil {
		return nil, nil
	}
	matches := make([]*metadatafiles.MetadataFile, 0, len(existing))
	for i := range existing {
		if predicate(existing[i]) {
			matches = append(matches, &existing[i])
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	// Remove duplicates — every match past index 0. Mirrors C# `:522-532`
	// which deletes via the recyclebin + the metadata-file store.
	for _, dup := range matches[1:] {
		path := filepath.Join(sr.Path, dup.RelativePath)
		s.log.Debug("metadataservice: removing duplicate MetadataFile", "path", path)
		subfolder := relativeParentOf(sr.Path, path)
		if binErr := s.bin.DeleteFile(path, subfolder); binErr != nil {
			s.log.Warn("metadataservice: recyclebin DeleteFile failed (continuing)", "path", path, "err", binErr)
		}
		if deleteErr := s.files.Delete(ctx, dup.ID); deleteErr != nil && !errors.Is(deleteErr, metadatafiles.ErrNotFound) {
			s.log.Warn("metadataservice: MetadataFiles Delete failed (continuing)", "id", dup.ID, "err", deleteErr)
		}
	}
	return matches[0], nil
}

// ProcessSeriesMetadata runs one writer's per-series metadata
// emission. Mirrors C# `:246-287`.
//
// Algorithm:
//  1. Call [metadata.Provider.SeriesMetadata];
//  2. RelativePath == "" → writer chose to skip → return nil.
//  3. Compute SHA-256 of contents;
//  4. Look up existing per-series-metadata MetadataFile via
//     [GetMetadataFile]; create-or-reuse;
//  5. If hash unchanged AND relative-path unchanged → return nil
//     (no-op; nothing to write or persist).
//  6. If hash unchanged but relative-path changed → update path-
//     only and return the row for persistence.
//  7. Else → write the new contents to disk via [SaveMetadataFile]
//     and update Hash + RelativePath + Extension + return.
//
// Returns nil + nil-err when nothing changed (the C# returns null in
// the same case). Returns the new/updated row when the caller should
// persist it via [MetadataFileStore.Insert] / [MetadataFileStore.Update].
//
// nil-receiver / nil-consumer / nil-series → (nil, nil).
func (s *Service) ProcessSeriesMetadata(ctx context.Context, consumer metadata.Provider, sr *series.Series, existing []metadatafiles.MetadataFile, reason metadata.SeriesMetadataReason) (*metadatafiles.MetadataFile, error) {
	if s == nil || consumer == nil || sr == nil {
		return nil, nil
	}
	res, err := consumer.SeriesMetadata(ctx, sr, reason)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: ProcessSeriesMetadata: writer %q: %w", consumerName(consumer), err)
	}
	if res.RelativePath == "" {
		return nil, nil
	}

	hash := sha256Hex(res.Contents)
	mf, err := s.GetMetadataFile(ctx, sr, existing, func(f metadatafiles.MetadataFile) bool {
		return metadata.MetadataType(f.Type) == metadata.MetadataTypeSeriesMetadata
	})
	if err != nil {
		return nil, err
	}
	if mf == nil {
		mf = &metadatafiles.MetadataFile{
			SeriesID: sr.ID,
			Consumer: consumerName(consumer),
			Type:     int(metadata.MetadataTypeSeriesMetadata),
		}
	}

	if hash == mf.Hash {
		// Same body. RelativePath may still need updating (writer
		// changed its preferred filename without re-rendering).
		if res.RelativePath != mf.RelativePath {
			mf.RelativePath = res.RelativePath
			return mf, nil
		}
		return nil, nil
	}

	fullPath := filepath.Join(sr.Path, res.RelativePath)
	s.log.Debug("metadataservice: writing series metadata", "path", fullPath)
	if writeErr := s.SaveMetadataFile(fullPath, res.Contents); writeErr != nil {
		return nil, fmt.Errorf("metadataservice: ProcessSeriesMetadata: write %q: %w", fullPath, writeErr)
	}
	mf.Hash = hash
	mf.RelativePath = res.RelativePath
	mf.Extension = filepath.Ext(fullPath)
	return mf, nil
}

// ProcessEpisodeMetadata runs one writer's per-episode metadata
// emission. Mirrors C# `:289-340`.
//
// Algorithm:
//  1. Call [metadata.Provider.EpisodeMetadata];
//  2. RelativePath == "" → skip → nil.
//  3. RenameOtherExtraFile against the new path (clears any sibling
//     extra-file using the same name).
//  4. Look up existing per-episode-metadata MetadataFile via
//     [GetMetadataFile] keyed on (Type, EpisodeFileID).
//  5. If existing.RelativePath != new path → move the existing
//     file via [DiskTransfer.TransferFile] and update RelativePath.
//  6. Hash check: unchanged → nil (no write needed).
//  7. Else write + update Hash + return.
//
// Returns nil + nil-err when nothing changed; otherwise the row.
func (s *Service) ProcessEpisodeMetadata(ctx context.Context, consumer metadata.Provider, sr *series.Series, episodeFile *episodefiles.File, existing []metadatafiles.MetadataFile) (*metadatafiles.MetadataFile, error) {
	if s == nil || consumer == nil || sr == nil || episodeFile == nil {
		return nil, nil
	}
	res, err := consumer.EpisodeMetadata(ctx, sr, episodeFile)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: ProcessEpisodeMetadata: writer %q: %w", consumerName(consumer), err)
	}
	if res.RelativePath == "" {
		return nil, nil
	}

	fullPath := filepath.Join(sr.Path, res.RelativePath)
	if s.otherRenamer != nil {
		if renameErr := s.otherRenamer.RenameOtherExtraFile(sr, fullPath); renameErr != nil {
			s.log.Warn("metadataservice: OtherExtraRenamer failed (continuing)", "path", fullPath, "err", renameErr)
		}
	}

	existingFile, err := s.GetMetadataFile(ctx, sr, existing, func(f metadatafiles.MetadataFile) bool {
		return metadata.MetadataType(f.Type) == metadata.MetadataTypeEpisodeMetadata &&
			f.EpisodeFileID != nil && *f.EpisodeFileID == episodeFile.ID
	})
	if err != nil {
		return nil, err
	}
	if existingFile != nil {
		existingFullPath := filepath.Join(sr.Path, existingFile.RelativePath)
		if !pathEquals(fullPath, existingFullPath) {
			if moveErr := s.transfer.TransferFile(existingFullPath, fullPath, TransferModeMove); moveErr != nil {
				return nil, fmt.Errorf("metadataservice: ProcessEpisodeMetadata: move %q→%q: %w", existingFullPath, fullPath, moveErr)
			}
			existingFile.RelativePath = res.RelativePath
		}
	}

	hash := sha256Hex(res.Contents)
	mf := existingFile
	if mf == nil {
		efID := episodeFile.ID
		mf = &metadatafiles.MetadataFile{
			SeriesID:      sr.ID,
			SeasonNumber:  intPtr(episodeFile.SeasonNumber),
			EpisodeFileID: &efID,
			Consumer:      consumerName(consumer),
			Type:          int(metadata.MetadataTypeEpisodeMetadata),
			RelativePath:  res.RelativePath,
			Extension:     filepath.Ext(fullPath),
		}
	}
	if hash == mf.Hash {
		return nil, nil
	}

	s.log.Debug("metadataservice: writing episode metadata", "path", fullPath)
	if writeErr := s.SaveMetadataFile(fullPath, res.Contents); writeErr != nil {
		return nil, fmt.Errorf("metadataservice: ProcessEpisodeMetadata: write %q: %w", fullPath, writeErr)
	}
	mf.Hash = hash
	return mf, nil
}

// ProcessSeriesImages runs one writer's per-series-image emission.
// Mirrors C# `:342-375`.
//
// Algorithm: iterate Provider.SeriesImages; for each result:
//   - skip when the file already exists on disk;
//   - rename any sibling extra-file occupying the same path;
//   - look up the existing per-series-image MetadataFile keyed on
//     (Type, RelativePath); create-or-reuse;
//   - download the image via [DownloadImage];
//   - append the row to the result slice for caller persistence.
//
// Returns the rows to insert/update. Empty slice when nothing
// emitted or every output already on disk.
func (s *Service) ProcessSeriesImages(ctx context.Context, consumer metadata.Provider, sr *series.Series, existing []metadatafiles.MetadataFile) ([]metadatafiles.MetadataFile, error) {
	if s == nil || consumer == nil || sr == nil {
		return nil, nil
	}
	images, err := consumer.SeriesImages(ctx, sr)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: ProcessSeriesImages: writer %q: %w", consumerName(consumer), err)
	}
	return s.processImageList(ctx, consumer, sr, existing, images, imageScope{
		coverType:    metadata.MetadataTypeSeriesImage,
		matchByPath:  true,
		seasonNumber: nil,
		episodeFile:  nil,
	})
}

// ProcessSeasonImages iterates every season in series.Seasons +
// invokes Provider.SeasonImages per season. Mirrors C# `:377-415`.
//
// Returns the rows to insert/update across every season.
//
// nil [SeasonsLister] / no seasons → empty.
func (s *Service) ProcessSeasonImages(ctx context.Context, consumer metadata.Provider, sr *series.Series, existing []metadatafiles.MetadataFile) ([]metadatafiles.MetadataFile, error) {
	if s == nil || consumer == nil || sr == nil || s.seasons == nil {
		return nil, nil
	}
	seasons, err := s.seasons.SeasonsForSeries(sr)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: ProcessSeasonImages: seasons for %d: %w", sr.ID, err)
	}
	var out []metadatafiles.MetadataFile
	for _, season := range seasons {
		images, imgErr := consumer.SeasonImages(ctx, sr, season)
		if imgErr != nil {
			return nil, fmt.Errorf("metadataservice: ProcessSeasonImages: writer %q season %d: %w", consumerName(consumer), season.SeasonNumber, imgErr)
		}
		sn := season.SeasonNumber
		rows, processErr := s.processImageList(ctx, consumer, sr, existing, images, imageScope{
			coverType:    metadata.MetadataTypeSeasonImage,
			matchByPath:  true,
			seasonNumber: &sn,
			episodeFile:  nil,
		})
		if processErr != nil {
			return nil, processErr
		}
		out = append(out, rows...)
	}
	return out, nil
}

// ProcessEpisodeImages runs one writer's per-episode-image emission
// for a specific EpisodeFile. Mirrors C# `:417-466`.
//
// The C# code is subtly different from the Series/Season image path:
// when the existing-MetadataFile row matches by EpisodeFileID and the
// emitted RelativePath differs from the stored one, it MOVES the file
// (instead of re-downloading) and short-circuits the loop by returning
// a single-element list. The Go port mirrors this behaviour.
func (s *Service) ProcessEpisodeImages(ctx context.Context, consumer metadata.Provider, sr *series.Series, episodeFile *episodefiles.File, existing []metadatafiles.MetadataFile) ([]metadatafiles.MetadataFile, error) {
	if s == nil || consumer == nil || sr == nil || episodeFile == nil {
		return nil, nil
	}
	images, err := consumer.EpisodeImages(ctx, sr, episodeFile)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: ProcessEpisodeImages: writer %q file %d: %w", consumerName(consumer), episodeFile.ID, err)
	}
	consumerStr := consumerName(consumer)

	var out []metadatafiles.MetadataFile
	for _, image := range images {
		fullPath := filepath.Join(sr.Path, image.RelativePath)
		if s.disk.FileExists(fullPath) {
			s.log.Debug("metadataservice: episode image already exists", "path", fullPath)
			continue
		}
		if s.otherRenamer != nil {
			if renameErr := s.otherRenamer.RenameOtherExtraFile(sr, fullPath); renameErr != nil {
				s.log.Warn("metadataservice: OtherExtraRenamer failed (continuing)", "path", fullPath, "err", renameErr)
			}
		}

		existingFile, getErr := s.GetMetadataFile(ctx, sr, existing, func(f metadatafiles.MetadataFile) bool {
			return metadata.MetadataType(f.Type) == metadata.MetadataTypeEpisodeImage &&
				f.EpisodeFileID != nil && *f.EpisodeFileID == episodeFile.ID
		})
		if getErr != nil {
			return nil, getErr
		}

		// C# `:436-446` — existing row whose stored path differs from
		// the new one: MOVE the file + short-circuit with a single-
		// element result. Differs from Series/Season image flow.
		if existingFile != nil {
			existingFullPath := filepath.Join(sr.Path, existingFile.RelativePath)
			if !pathEquals(fullPath, existingFullPath) {
				if moveErr := s.transfer.TransferFile(existingFullPath, fullPath, TransferModeMove); moveErr != nil {
					return nil, fmt.Errorf("metadataservice: ProcessEpisodeImages: move %q→%q: %w", existingFullPath, fullPath, moveErr)
				}
				existingFile.RelativePath = image.RelativePath
				return []metadatafiles.MetadataFile{*existingFile}, nil
			}
		}

		mf := existingFile
		if mf == nil {
			efID := episodeFile.ID
			mf = &metadatafiles.MetadataFile{
				SeriesID:      sr.ID,
				SeasonNumber:  intPtr(episodeFile.SeasonNumber),
				EpisodeFileID: &efID,
				Consumer:      consumerStr,
				Type:          int(metadata.MetadataTypeEpisodeImage),
				RelativePath:  image.RelativePath,
				Extension:     filepath.Ext(fullPath),
			}
		}

		if downloadErr := s.DownloadImage(ctx, sr, image); downloadErr != nil {
			s.log.Warn("metadataservice: image download failed (continuing)", "url", image.URL, "series", sr.ID, "err", downloadErr)
		}
		out = append(out, *mf)
	}
	return out, nil
}

// imageScope captures the predicate + metadata fields shared by the
// per-image-list inner loop in [processImageList].
type imageScope struct {
	coverType    metadata.MetadataType
	matchByPath  bool
	seasonNumber *int // nil for SeriesImage; non-nil for SeasonImage.
	episodeFile  *episodefiles.File
}

// processImageList is the shared per-image loop for ProcessSeriesImages
// + ProcessSeasonImages. Mirrors C# `:344-374` + `:380-414`.
func (s *Service) processImageList(ctx context.Context, consumer metadata.Provider, sr *series.Series, existing []metadatafiles.MetadataFile, images []metadata.ImageFileResult, scope imageScope) ([]metadatafiles.MetadataFile, error) {
	consumerStr := consumerName(consumer)
	var out []metadatafiles.MetadataFile
	for _, image := range images {
		fullPath := filepath.Join(sr.Path, image.RelativePath)
		if s.disk.FileExists(fullPath) {
			s.log.Debug("metadataservice: image already exists", "type", scope.coverType, "path", fullPath)
			continue
		}
		if s.otherRenamer != nil {
			if renameErr := s.otherRenamer.RenameOtherExtraFile(sr, fullPath); renameErr != nil {
				s.log.Warn("metadataservice: OtherExtraRenamer failed (continuing)", "path", fullPath, "err", renameErr)
			}
		}
		mfPtr, lookupErr := s.GetMetadataFile(ctx, sr, existing, func(f metadatafiles.MetadataFile) bool {
			if metadata.MetadataType(f.Type) != scope.coverType {
				return false
			}
			if scope.matchByPath && f.RelativePath != image.RelativePath {
				return false
			}
			if scope.seasonNumber != nil {
				if f.SeasonNumber == nil || *f.SeasonNumber != *scope.seasonNumber {
					return false
				}
			}
			return true
		})
		if lookupErr != nil {
			return nil, lookupErr
		}
		mf := mfPtr
		if mf == nil {
			mf = &metadatafiles.MetadataFile{
				SeriesID:     sr.ID,
				Consumer:     consumerStr,
				Type:         int(scope.coverType),
				RelativePath: image.RelativePath,
				Extension:    filepath.Ext(fullPath),
			}
			if scope.seasonNumber != nil {
				sn := *scope.seasonNumber
				mf.SeasonNumber = &sn
			}
		}

		if downloadErr := s.DownloadImage(ctx, sr, image); downloadErr != nil {
			s.log.Warn("metadataservice: image download failed (continuing)", "url", image.URL, "series", sr.ID, "err", downloadErr)
		}
		out = append(out, *mf)
	}
	return out, nil
}

// DownloadImage downloads the per-image source to disk. Mirrors C#
// `:468-505`.
//
// 3-arm dispatch on URL scheme:
//   - "http..." prefix → HTTP download.
//   - existing local file → CopyFile.
//   - else → skip (downloaded==false).
//
// SetFilePermissions runs only when a write actually occurred.
// Errors are logged + swallowed in C# (HttpException / WebException /
// general Exception). The Go port returns the error to give the
// caller observability; the per-image-list loop logs + continues on
// every error already, so the swallow-semantics are preserved at
// that seam.
func (s *Service) DownloadImage(ctx context.Context, sr *series.Series, image metadata.ImageFileResult) error {
	if s == nil || sr == nil {
		return nil
	}
	fullPath := filepath.Join(sr.Path, image.RelativePath)
	downloaded := false

	switch {
	case strings.HasPrefix(image.URL, "http"):
		if err := s.http.DownloadFile(ctx, image.URL, fullPath); err != nil {
			return fmt.Errorf("metadataservice: DownloadImage: HTTP %q: %w", image.URL, err)
		}
		downloaded = true
	case s.disk.FileExists(image.URL):
		if err := s.disk.CopyFile(image.URL, fullPath); err != nil {
			return fmt.Errorf("metadataservice: DownloadImage: copy %q→%q: %w", image.URL, fullPath, err)
		}
		downloaded = true
	default:
		// Neither HTTP nor a local file path — silently skip. Matches
		// C# `:483-486` "downloaded=false" path.
	}

	if downloaded {
		if err := s.attrs.SetFilePermissions(fullPath); err != nil {
			return fmt.Errorf("metadataservice: DownloadImage: SetFilePermissions %q: %w", fullPath, err)
		}
	}
	return nil
}

// SaveMetadataFile writes the per-metadata-file body to disk and
// runs the post-write SetFilePermissions hook. Mirrors C# `:507-511`.
func (s *Service) SaveMetadataFile(path, contents string) error {
	if s == nil {
		return nil
	}
	if err := s.disk.WriteAllText(path, contents); err != nil {
		return fmt.Errorf("metadataservice: SaveMetadataFile: WriteAllText %q: %w", path, err)
	}
	if err := s.attrs.SetFilePermissions(path); err != nil {
		return fmt.Errorf("metadataservice: SaveMetadataFile: SetFilePermissions %q: %w", path, err)
	}
	return nil
}

// consumerName extracts the Consumer-column value the writer was
// registered under in [metadata.Default]. Mirrors C# `consumer.GetType().Name`
// — every writer's persisted row carries Implementation==<C# class
// name>; the same string is the Consumer key in MetadataFiles rows.
//
// nil-tolerant — empty string on missing definition.
func consumerName(consumer metadata.Provider) string {
	if consumer == nil {
		return ""
	}
	def := consumer.Definition()
	if def == nil {
		return ""
	}
	return def.Implementation
}

// sha256Hex returns the hex-encoded SHA-256 of `body`. Mirrors C#
// `string.SHA256Hash()` extension at `:255`, `:315`. The C# tree
// returns lowercase hex; Go's `encoding/hex` does the same.
func sha256Hex(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// pathEquals mirrors C# `PathExtensions.PathEquals` semantics — a
// case-insensitive path comparison. The C# code uses
// `PathNotEquals` at `:308`, `:439`.
//
// Documented divergence: the C# tree is case-insensitive on every
// OS (matches Windows + macOS + tolerates Linux case-sensitive
// filesystems imperfectly). The Go port matches that — strings.EqualFold
// on filepath.Clean'd inputs.
func pathEquals(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// relativeParentOf computes the recyclebin sub-folder argument the
// C# tree builds via `_diskProvider.GetParentFolder(series.Path)
// .GetRelativePath(_diskProvider.GetParentFolder(path))` at `:529`.
//
// Intuition: the recyclebin keeps files in a parallel hierarchy.
// Given series.Path = "/tv/Show" and path = "/tv/Show/Season 1/x.nfo",
// the sub-folder should be "Show/Season 1" (relative to /tv).
func relativeParentOf(seriesPath, fullPath string) string {
	seriesParent := filepath.Dir(seriesPath)
	pathParent := filepath.Dir(fullPath)
	rel, err := filepath.Rel(seriesParent, pathParent)
	if err != nil {
		return ""
	}
	return rel
}

// intPtr returns a pointer to the given int — replaces the C# tree's
// implicit `int → int?` boxing at the per-field literal sites.
func intPtr(v int) *int { return &v }
