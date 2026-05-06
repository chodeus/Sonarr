package mediafiles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// Service ports `src/NzbDrone.Core/MediaFiles/DiskScanService.cs:31-318`
// with the IExecute<RescanSeriesCommand> handler factored out into a
// separate slot in `internal/commands/handlers` (slice 5). The
// implementation here is the [IDiskScanService.Scan] surface plus the
// private `CleanMediaFiles` / `CompletedScanning` /
// `RemoveEmptySeriesFolder` / `SetPermissions` helpers DiskScanService
// uses internally.
//
// All dependencies are narrow interfaces (see [deps.go]) so tests can
// stub each axis independently and the Service can ship before some
// of its real backing services are ported (Import* + UpdateMediaInfo
// slots receive no-op stubs at boot until the EpisodeImport +
// MediaInfo-update subsystems land).
//
// The Service holds a `*messaging.Bus` and publishes
// [SeriesScannedEvent] / [SeriesScanSkippedEvent] directly via
// [messaging.Publish] — same pattern as `episodefiles.Service` and
// `series.Service`.
type Service struct {
	disk             DiskProvider
	cfg              Config
	rootFolders      RootFolderResolver
	seriesGet        SeriesGetter
	episodeFiles     EpisodeFileService
	mediaFileCleanup MediaFileTableCleanup
	importDecide     ImportDecisionMaker
	importApprove    ImportApprovedEpisodes
	mediaInfo        UpdateMediaInfo
	bus              *messaging.Bus
	log              *slog.Logger
}

// NewService builds a Service with all dependencies bound. Every
// non-bus / non-log argument MUST be non-nil — there's no sensible
// fallback for any of them and a silent nil would mask real boot-time
// misconfiguration. The bus is optional (nil disables event publish,
// useful in narrow tests); log defaults to [slog.Default].
//
// Returns a wrapped error naming the missing dependency for fast
// diagnosis at the host wiring layer.
func NewService(
	disk DiskProvider,
	cfg Config,
	rootFolders RootFolderResolver,
	seriesGet SeriesGetter,
	episodeFiles EpisodeFileService,
	mediaFileCleanup MediaFileTableCleanup,
	importDecide ImportDecisionMaker,
	importApprove ImportApprovedEpisodes,
	mediaInfo UpdateMediaInfo,
	bus *messaging.Bus,
	log *slog.Logger,
) (*Service, error) {
	switch {
	case disk == nil:
		return nil, errors.New("mediafiles: NewService: nil DiskProvider")
	case cfg == nil:
		return nil, errors.New("mediafiles: NewService: nil Config")
	case rootFolders == nil:
		return nil, errors.New("mediafiles: NewService: nil RootFolderResolver")
	case seriesGet == nil:
		return nil, errors.New("mediafiles: NewService: nil SeriesGetter")
	case episodeFiles == nil:
		return nil, errors.New("mediafiles: NewService: nil EpisodeFileService")
	case mediaFileCleanup == nil:
		return nil, errors.New("mediafiles: NewService: nil MediaFileTableCleanup")
	case importDecide == nil:
		return nil, errors.New("mediafiles: NewService: nil ImportDecisionMaker")
	case importApprove == nil:
		return nil, errors.New("mediafiles: NewService: nil ImportApprovedEpisodes")
	case mediaInfo == nil:
		return nil, errors.New("mediafiles: NewService: nil UpdateMediaInfo")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		disk:             disk,
		cfg:              cfg,
		rootFolders:      rootFolders,
		seriesGet:        seriesGet,
		episodeFiles:     episodeFiles,
		mediaFileCleanup: mediaFileCleanup,
		importDecide:     importDecide,
		importApprove:    importApprove,
		mediaInfo:        mediaInfo,
		bus:              bus,
		log:              log,
	}, nil
}

// Scan ports `DiskScanService.Scan(Series)` at `:77-188`. Five
// distinct branches, in the order the C# tree evaluates them:
//
//  1. Series folder missing AND root folder missing → publish
//     [SeriesScanSkippedEvent] with [SeriesScanSkippedReasonRootFolderDoesNotExist]
//     and return without touching disk.
//  2. Series folder missing AND root folder empty → publish
//     [SeriesScanSkippedEvent] with [SeriesScanSkippedReasonRootFolderIsEmpty]
//     and return.
//  3. Series folder missing, root folder OK,
//     [Config.CreateEmptySeriesFolders] true,
//     [Config.DeleteEmptyFolders] true → log skip, fall through to
//     [Service.cleanMediaFiles] + [Service.completedScanning] with
//     an empty file list.
//  4. Series folder missing, root folder OK,
//     [Config.CreateEmptySeriesFolders] true,
//     [Config.DeleteEmptyFolders] false → CreateFolder + apply
//     permissions, fall through to clean+complete with empty file
//     list.
//  5. Series folder exists → full happy path:
//     GetVideoFiles + FilterPaths → mediaFileList → cleanMediaFiles
//     → GetFilesBySeries + FilterExistingFiles → unmappedFiles →
//     GetImportDecisions → ImportApproved → size-update loop with
//     UpdateMediaInfo / UpdateMany fallback → RemoveEmptySeriesFolder
//     → optional GetNonVideoFiles + FilterPaths extras pass →
//     completedScanning publish.
//
// Returns an error only when a disk / repo operation fails in a way
// the C# tree would let propagate. Permission-set failures are
// log+swallow per the C# `try/catch` at `:281-285`. Decision-engine
// + import-approve + update-mediainfo errors propagate; the C# tree
// has no catch around them either.
func (s *Service) Scan(ctx context.Context, ser *series.Series) error {
	if ser == nil {
		return errors.New("mediafiles: Scan: nil series")
	}

	rootFolder, err := s.rootFolders.GetBestRootFolderPath(ctx, ser.Path)
	if err != nil {
		return fmt.Errorf("mediafiles: Scan: GetBestRootFolderPath: %w", err)
	}

	seriesFolderExists := s.disk.FolderExists(ser.Path)

	if !seriesFolderExists {
		if !s.disk.FolderExists(rootFolder) {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"mediafiles: series root folder doesn't exist",
				slog.String("rootFolder", rootFolder),
				slog.Int("seriesId", ser.ID))
			return s.publishSkipped(ctx, ser, SeriesScanSkippedReasonRootFolderDoesNotExist)
		}
		if s.disk.FolderEmpty(rootFolder) {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"mediafiles: series root folder is empty",
				slog.String("rootFolder", rootFolder),
				slog.Int("seriesId", ser.ID))
			return s.publishSkipped(ctx, ser, SeriesScanSkippedReasonRootFolderIsEmpty)
		}
	}

	s.log.LogAttrs(ctx, slog.LevelInfo,
		"mediafiles: scanning",
		slog.String("title", ser.Title),
		slog.Int("seriesId", ser.ID))

	if !seriesFolderExists {
		if s.cfg.CreateEmptySeriesFolders() {
			if s.cfg.DeleteEmptyFolders() {
				s.log.LogAttrs(ctx, slog.LevelDebug,
					"mediafiles: not creating missing series folder because delete-empty-folders is enabled",
					slog.String("path", ser.Path))
			} else {
				s.log.LogAttrs(ctx, slog.LevelDebug,
					"mediafiles: creating missing series folder",
					slog.String("path", ser.Path))
				if err := s.disk.CreateFolder(ser.Path); err != nil {
					return fmt.Errorf("mediafiles: Scan: CreateFolder: %w", err)
				}
				s.applyPermissions(ctx, ser.Path)
			}
		} else {
			s.log.LogAttrs(ctx, slog.LevelDebug,
				"mediafiles: series folder doesn't exist",
				slog.String("path", ser.Path))
		}
		if err := s.cleanMediaFiles(ctx, ser, nil); err != nil {
			return err
		}
		return s.completedScanning(ctx, ser, nil)
	}

	rawFiles, err := GetVideoFiles(s.disk, ser.Path, true)
	if err != nil {
		return fmt.Errorf("mediafiles: Scan: GetVideoFiles: %w", err)
	}
	mediaFileList, err := FilterPaths(ser.Path, rawFiles, true)
	if err != nil {
		return fmt.Errorf("mediafiles: Scan: FilterPaths(media): %w", err)
	}

	if err := s.cleanMediaFiles(ctx, ser, mediaFileList); err != nil {
		return err
	}

	seriesFiles, err := s.episodeFiles.GetFilesBySeries(ctx, ser.ID)
	if err != nil {
		return fmt.Errorf("mediafiles: Scan: GetFilesBySeries: %w", err)
	}

	unmappedFiles, err := s.episodeFiles.FilterExistingFiles(ctx, mediaFileList, ser)
	if err != nil {
		return fmt.Errorf("mediafiles: Scan: FilterExistingFiles: %w", err)
	}

	decisions, err := s.importDecide.GetImportDecisions(ctx, unmappedFiles, ser, false)
	if err != nil {
		return fmt.Errorf("mediafiles: Scan: GetImportDecisions: %w", err)
	}
	if err := s.importApprove.Import(ctx, decisions, false); err != nil {
		return fmt.Errorf("mediafiles: Scan: Import: %w", err)
	}

	if err := s.reconcileSizes(ctx, ser, seriesFiles); err != nil {
		return err
	}

	if err := s.removeEmptySeriesFolder(ser.Path); err != nil {
		return fmt.Errorf("mediafiles: Scan: removeEmptySeriesFolder: %w", err)
	}

	var possibleExtraFiles []string
	if s.disk.FolderExists(ser.Path) {
		rawExtras, err := GetNonVideoFiles(s.disk, ser.Path, true)
		if err != nil {
			return fmt.Errorf("mediafiles: Scan: GetNonVideoFiles: %w", err)
		}
		possibleExtraFiles, err = FilterPaths(ser.Path, rawExtras, true)
		if err != nil {
			return fmt.Errorf("mediafiles: Scan: FilterPaths(extras): %w", err)
		}
	}

	return s.completedScanning(ctx, ser, possibleExtraFiles)
}

// reconcileSizes ports the size-update loop at `:147-172`. For each
// persisted file: re-stat, skip if size unchanged; otherwise ask
// UpdateMediaInfo to refresh the row, queueing for a bare size-only
// Update call if MediaInfo declines. GetFileSize errors log+continue
// (the C# version returns 0 on missing files which would fail the
// `f.Size == fileSize` comparison and queue a phantom update — the
// Go port drops the row from the iteration instead, which matches
// the practical effect for missing files).
func (s *Service) reconcileSizes(ctx context.Context, ser *series.Series, seriesFiles []episodefiles.File) error {
	var filesToUpdate []*episodefiles.File
	for i := range seriesFiles {
		f := seriesFiles[i]
		path := filepath.Join(ser.Path, f.RelativePath)
		size, err := s.disk.GetFileSize(path)
		if err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"mediafiles: GetFileSize failed; skipping size reconciliation for file",
				slog.String("path", path),
				slog.Any("err", err))
			continue
		}
		if f.Size == size {
			continue
		}
		f.Size = size
		updated, err := s.mediaInfo.Update(ctx, &f, ser)
		if err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"mediafiles: UpdateMediaInfo failed; falling back to size-only update",
				slog.String("path", path),
				slog.Any("err", err))
			updated = false
		}
		if !updated {
			cp := f
			filesToUpdate = append(filesToUpdate, &cp)
		}
	}
	if len(filesToUpdate) > 0 {
		if err := s.episodeFiles.UpdateMany(ctx, filesToUpdate); err != nil {
			return fmt.Errorf("mediafiles: reconcileSizes: UpdateMany: %w", err)
		}
	}
	return nil
}

// cleanMediaFiles ports the `CleanMediaFiles` private helper at
// `:190-194`. Wraps [MediaFileTableCleanup.Clean]; the C# call site
// ignores the return value but the Go port surfaces errors so a
// future cleanup-impl bug doesn't get swallowed.
func (s *Service) cleanMediaFiles(ctx context.Context, ser *series.Series, mediaFileList []string) error {
	s.log.LogAttrs(ctx, slog.LevelDebug,
		"mediafiles: cleaning up media files in DB",
		slog.Int("seriesId", ser.ID),
		slog.Int("inputFiles", len(mediaFileList)))
	if _, _, err := s.mediaFileCleanup.Clean(ctx, ser, mediaFileList); err != nil {
		return fmt.Errorf("mediafiles: cleanMediaFiles: %w", err)
	}
	return nil
}

// completedScanning ports the `CompletedScanning` private helper at
// `:196-200`. Logs and publishes [SeriesScannedEvent]. Normalises a
// nil extras slice to empty so subscribers can range without a
// nil-guard — same defensive empty-slice posture as the helpers in
// [scan.go].
func (s *Service) completedScanning(ctx context.Context, ser *series.Series, extras []string) error {
	s.log.LogAttrs(ctx, slog.LevelInfo,
		"mediafiles: completed scanning disk",
		slog.String("title", ser.Title),
		slog.Int("seriesId", ser.ID))
	if extras == nil {
		extras = []string{}
	}
	if s.bus == nil {
		return nil
	}
	if err := messaging.Publish(ctx, s.bus, SeriesScannedEvent{Series: ser, PossibleExtraFiles: extras}); err != nil {
		return fmt.Errorf("mediafiles: completedScanning: publish: %w", err)
	}
	return nil
}

// publishSkipped publishes [SeriesScanSkippedEvent] with the supplied
// reason. Mirrors the two scan-skipped publish sites at
// `DiskScanService.cs:88` and `:95`.
func (s *Service) publishSkipped(ctx context.Context, ser *series.Series, reason SeriesScanSkippedReason) error {
	if s.bus == nil {
		return nil
	}
	if err := messaging.Publish(ctx, s.bus, SeriesScanSkippedEvent{Series: ser, Reason: reason}); err != nil {
		return fmt.Errorf("mediafiles: publishSkipped(%s): %w", reason, err)
	}
	return nil
}

// applyPermissions ports the `SetPermissions` private helper at
// `:270-286` — only fires when [Config.SetPermissionsLinux] is true,
// and any error from the underlying call is logged but never
// propagated (matches the C# `try/catch` swallowing).
func (s *Service) applyPermissions(ctx context.Context, path string) {
	if !s.cfg.SetPermissionsLinux() {
		return
	}
	if err := s.disk.SetPermissions(path, s.cfg.ChmodFolder(), s.cfg.ChownGroup()); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"mediafiles: unable to apply permissions",
			slog.String("path", path),
			slog.Any("err", err))
	}
}

// removeEmptySeriesFolder ports the `RemoveEmptySeriesFolder` private
// helper at `:288-299` — gated on [Config.DeleteEmptyFolders]; runs
// the bottom-up subfolder sweep then deletes the top-level folder if
// it ends up empty.
func (s *Service) removeEmptySeriesFolder(path string) error {
	if !s.cfg.DeleteEmptyFolders() {
		return nil
	}
	if err := s.disk.RemoveEmptySubfolders(path); err != nil {
		return fmt.Errorf("RemoveEmptySubfolders: %w", err)
	}
	if s.disk.FolderEmpty(path) {
		if err := s.disk.DeleteFolder(path, true); err != nil {
			return fmt.Errorf("DeleteFolder: %w", err)
		}
	}
	return nil
}
