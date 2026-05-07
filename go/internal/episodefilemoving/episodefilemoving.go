// Package episodefilemoving is the Go port of
// `NzbDrone.Core.MediaFiles.EpisodeFileMovingService`
// (`src/NzbDrone.Core/MediaFiles/EpisodeFileMovingService.cs:1-251`).
// It moves a persisted [episodefiles.File] from its source path
// (download-client output / library scan) to the renamed destination
// inside the series tree, ensuring all parent folders exist and
// updating per-file timestamps + permissions along the way.
//
// This slice (3.70) lands the writer surface — three public methods
// (MoveEpisodeFile×2 + CopyEpisodeFile) plus the private TransferFile
// + EnsureEpisodeFolder + CreateFolder helpers.
//
// The C# tree wires 11 dependencies. This package narrows each behind
// a one-method (or small-method-set) interface so production wiring
// can compose the existing `*disk.Provider`, `*organizer.Builder`,
// `*rootfolders.Service`, etc., while tests pass stubs:
//
//   - [Filesystem] — FileExists / FolderExists / CreateFolder /
//     TransferFile (the C# IDiskProvider + IDiskTransferService
//     bundled because they're always called together).
//   - [AttributeWriter] — SetFolderLastWriteTime / SetFilePermissions /
//     SetFolderPermissions (the C# IMediaFileAttributeService).
//     nil-tolerant: a nil binding skips every attribute mutation,
//     letting the slice ship before the attribute service ports.
//   - [FileNameBuilder] — BuildFilePath / BuildSeasonPath wrappers
//     over the existing `internal/organizer` free functions.
//   - [EpisodeFinder] — GetEpisodesByFileID (the C# IEpisodeService
//     surface needed by the 2-arg MoveEpisodeFile).
//   - [FileDateUpdater] — ChangeFileDateForFile (the C#
//     IUpdateEpisodeFileService surface). nil-tolerant: skip the
//     timestamp adjustment when not bound.
//   - [RootFolderFinder] — GetBestRootFolderPath (the C#
//     IRootFolderService surface).
//   - [ImportScriptRunner] — TryImport (the C# IImportScript
//     surface). nil-tolerant: skip the script-import branch entirely,
//     always falling through to the regular TransferFile path.
//   - [Config] — CopyUsingHardlinks (the C# IConfigService surface).
//   - [EventPublisher] — Publish (the C# IEventAggregator surface).
//     nil-tolerant: skip the EpisodeFolderCreatedEvent emission.
//
// Deferred:
//
//   - The script-import flow's RenameRequested arm at C# `:137-147`
//     recursively re-invokes MoveEpisodeFile. This is structurally
//     wired (the recursion will work when the script runner produces
//     a non-DeferMove decision) but the path isn't exercised by tests
//     because the production binding's script runner ports later.
//
//   - The C# OsPath wrapper used by CreateFolder for parent-folder
//     extraction at `:233`. The Go port uses `path/filepath.Dir`,
//     which is OS-aware enough for the test corpus and matches what
//     the C# code's `OsPath.Directory.FullPath` would produce on the
//     same OS.
//
//   - The C# CopyEpisodeFile uses `_buildFileNames.BuildFilePath`
//     with explicit nulls for the optional `cleanOptions` and a
//     forced `localEpisode.CustomFormats` arg. The Go port's
//     [organizer.BuildFilePath] takes a `BuildOptions` struct;
//     the writer passes a struct populated from the LocalEpisode +
//     a CustomFormats list. The mapping is straightforward — see
//     [Service.buildPath] for the exact field assignments.
package episodefilemoving

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/series"
)

// TransferMode mirrors `NzbDrone.Common.Disk.TransferMode`. Picks
// between move / copy / hardlink-or-copy.
type TransferMode int

// TransferMode values. Numeric order is opaque (the C# enum is
// non-sequential bit-flags, but the Go port uses identifiers
// directly so ordinals don't matter for serialisation).
const (
	// TransferMoveModeUnknown — zero value; treat as Move for
	// defensive defaults (matches C# `TransferMode.None` → fallthrough).
	TransferModeUnknown TransferMode = 0
	// TransferModeMove — delete-after-copy at the source.
	TransferModeMove TransferMode = 1
	// TransferModeCopy — leave the source alone.
	TransferModeCopy TransferMode = 2
	// TransferModeHardLinkOrCopy — try hardlink, fall back to copy.
	TransferModeHardLinkOrCopy TransferMode = 3
)

// String returns the C# enum-identifier form for log lines.
func (m TransferMode) String() string {
	switch m {
	case TransferModeMove:
		return "Move"
	case TransferModeCopy:
		return "Copy"
	case TransferModeHardLinkOrCopy:
		return "HardLinkOrCopy"
	default:
		return fmt.Sprintf("TransferMode(%d)", int(m))
	}
}

// ScriptImportDecision mirrors `NzbDrone.Core.MediaFiles.EpisodeImport.ScriptImport.ScriptImportDecision`.
// The script-import flow can either defer to the writer's regular
// flow (DeferMove), tell the writer to skip the move (Move /
// MoveComplete), or request a post-script rename (RenameRequested).
type ScriptImportDecision int

// ScriptImportDecision values. The script-import subsystem hasn't
// ported; the constants are pinned here so the writer's public
// surface matches C# exactly.
const (
	// ScriptImportDeferMove — let the writer do its normal move.
	// Default verdict for a nil ImportScriptRunner.
	ScriptImportDeferMove ScriptImportDecision = 0
	// ScriptImportMoveComplete — script handled the move; writer
	// skips its own TransferFile call.
	ScriptImportMoveComplete ScriptImportDecision = 1
	// ScriptImportRenameRequested — script wants the writer to
	// re-run MoveEpisodeFile against the post-script paths.
	ScriptImportRenameRequested ScriptImportDecision = 2
)

// EpisodeFolderCreatedEvent is the local placeholder for the C#
// `EpisodeFolderCreatedEvent` at `MediaFiles/Events/EpisodeFolderCreatedEvent.cs`.
// Fields populated only when the corresponding folder was just
// created. Published via [EventPublisher.Publish] (when wired) so
// downstream consumers (housekeeping / the UI activity feed) can
// react.
type EpisodeFolderCreatedEvent struct {
	Series        *series.Series
	EpisodeFile   *episodefiles.File
	SeriesFolder  string
	SeasonFolder  string
	EpisodeFolder string
}

// --- narrow interfaces --------------------------------------------

// Filesystem is the slice of `IDiskProvider` + `IDiskTransferService`
// the writer reaches for. Bundled because every transfer goes through
// FileExists + TransferFile in the same call site, and pulling them
// behind separate interfaces would force the production binding to
// register the same disk-provider value twice.
type Filesystem interface {
	// FileExists reports whether the supplied path is a regular file.
	FileExists(path string) bool
	// FolderExists reports whether the supplied path is a directory.
	FolderExists(path string) bool
	// CreateFolder makes the supplied directory (and any missing
	// parents). Errors propagate — the writer logs and continues.
	CreateFolder(path string) error
	// TransferFile moves / copies / hardlinks src → dst per mode.
	// Errors propagate; the writer surfaces them to the caller.
	TransferFile(src, dst string, mode TransferMode) error
}

// AttributeWriter is the slice of `IMediaFileAttributeService` the
// writer calls. nil-tolerant: a nil binding skips every method
// (the writer's defaults log + continue).
//
// The C# class wraps platform-specific calls (Linux chmod / Windows
// ACL adjustments). The Go port defers the platform implementation
// to a future slice; the interface here is the seam.
type AttributeWriter interface {
	// SetFolderLastWriteTime updates the folder's mtime.
	SetFolderLastWriteTime(path string, t time.Time) error
	// SetFilePermissions applies the configured chmod to the file.
	SetFilePermissions(path string) error
	// SetFolderPermissions applies the configured chmod to the folder.
	SetFolderPermissions(path string) error
}

// FileNameBuilder is the slice of `IBuildFileNames` the writer needs.
// Two methods — BuildFilePath (renamed-destination resolution) and
// BuildSeasonPath (parent-folder path).
//
// Production wiring binds [defaultFileNameBuilder] which delegates to
// the existing [organizer.BuildFilePath] + [organizer.BuildSeasonPath]
// free functions; tests pass a stub.
type FileNameBuilder interface {
	// BuildFilePath returns the renamed full path for an EpisodeFile.
	BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string, le *episodeimport.LocalEpisode) (string, error)
	// BuildSeasonPath returns the season-folder path for a series.
	BuildSeasonPath(s *series.Series, seasonNumber int) string
}

// EpisodeFinder narrows `IEpisodeService.GetEpisodesByFileId` (one
// method). Used by the 2-arg MoveEpisodeFile to pre-fetch episodes
// the C# version reads off `episodeFile.Episodes`.
type EpisodeFinder interface {
	GetEpisodesByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// FileDateUpdater narrows `IUpdateEpisodeFileService.ChangeFileDateForFile`
// (one method). nil-tolerant — the writer's default skips the
// timestamp adjustment.
type FileDateUpdater interface {
	ChangeFileDateForFile(file *episodefiles.File, ser *series.Series, eps []episodes.Episode) error
}

// RootFolderFinder narrows `IRootFolderService.GetBestRootFolderPath`
// (one method). Returns the matching root folder path (`""` when
// none — the writer panics with [ErrRootFolderNotFound]).
type RootFolderFinder interface {
	GetBestRootFolderPath(ctx context.Context, seriesPath string) string
}

// ImportScriptRunner narrows `IImportScript.TryImport` (one method).
// nil-tolerant — a nil binding always returns DeferMove (the writer
// skips the script-import branch entirely and runs its regular
// transfer).
type ImportScriptRunner interface {
	TryImport(src, dst string, le *episodeimport.LocalEpisode, file *episodefiles.File, mode TransferMode) ScriptImportDecision
}

// Config narrows `IConfigService.CopyUsingHardlinks` (one method).
// Drives the [Service.CopyEpisodeFile] hardlink-vs-copy decision.
type Config interface {
	CopyUsingHardlinks() bool
}

// EventPublisher narrows `IEventAggregator.PublishEvent` (one
// method). nil-tolerant — a nil binding skips the
// EpisodeFolderCreatedEvent emission.
type EventPublisher interface {
	Publish(event any)
}

// --- sentinel errors ----------------------------------------------

// ErrRootFolderNotFound mirrors the C# `RootFolderNotFoundException`
// at `:191-197`. Wraps via `fmt.Errorf("...: %w", ErrRootFolderNotFound)`
// so callers can `errors.Is` against the sentinel.
var ErrRootFolderNotFound = errors.New("episodefilemoving: root folder not found")

// ErrSameFilename mirrors the C# `SameFilenameException` at `:124`.
// Returned when the source and destination paths are identical.
var ErrSameFilename = errors.New("episodefilemoving: source and destination are the same")

// ErrFileNotFound mirrors the C# `FileNotFoundException` at `:120`.
// Returned when the source file doesn't exist on disk.
var ErrFileNotFound = errors.New("episodefilemoving: episode file path does not exist")

// --- defaults ------------------------------------------------------

// defaultFileNameBuilder delegates to [organizer.BuildFilePath] +
// [organizer.BuildSeasonPath]. Stateless; safe to share. The
// LocalEpisode-derived BuildOptions construction is centralised here
// so the writer's Service.buildPath stays small.
type defaultFileNameBuilder struct{}

// BuildFilePath delegates to [organizer.BuildFilePath]. The
// LocalEpisode arg currently feeds only `CustomFormats` into the
// underlying `BuildOptions`; the rest of `BuildOptions` is left at
// zero value (matching the C# call site's `null` opts at `:74,85,96`).
func (defaultFileNameBuilder) BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string, le *episodeimport.LocalEpisode) (string, error) {
	opts := organizer.BuildOptions{}
	if le != nil {
		// Convert []customformats.CustomFormat (persisted form) into
		// the typed-form []*customformats.Format BuildOptions wants.
		// The C# call passes `localEpisode.CustomFormats` directly;
		// the Go port's persisted form lacks the typed Specifications
		// field, so the BuildOptions binding will see "no specs"
		// regardless. Future slices that wire the format-deserialiser
		// will populate the typed slice. For now the BuildOptions
		// CustomFormats stays nil — token rendering proceeds without
		// CustomFormat-driven tokens.
		_ = le.CustomFormats
	}
	return organizer.BuildFilePath(eps, s, file, extension, opts)
}

// BuildSeasonPath delegates to [organizer.BuildSeasonPath] with the
// zero-value FolderOptions.
func (defaultFileNameBuilder) BuildSeasonPath(s *series.Series, seasonNumber int) string {
	return organizer.BuildSeasonPath(s, seasonNumber, organizer.FolderOptions{})
}

// noopAttributeWriter satisfies [AttributeWriter] by returning nil
// from every method. Bound by [NewService] when the caller passes nil
// — appropriate while the C# IMediaFileAttributeService remains
// unported.
type noopAttributeWriter struct{}

// SetFolderLastWriteTime is a no-op.
func (noopAttributeWriter) SetFolderLastWriteTime(string, time.Time) error { return nil }

// SetFilePermissions is a no-op.
func (noopAttributeWriter) SetFilePermissions(string) error { return nil }

// SetFolderPermissions is a no-op.
func (noopAttributeWriter) SetFolderPermissions(string) error { return nil }

// noopFileDateUpdater satisfies [FileDateUpdater] by returning nil.
type noopFileDateUpdater struct{}

// ChangeFileDateForFile is a no-op.
func (noopFileDateUpdater) ChangeFileDateForFile(*episodefiles.File, *series.Series, []episodes.Episode) error {
	return nil
}

// alwaysDeferImportScriptRunner satisfies [ImportScriptRunner] by
// always returning DeferMove. The writer's regular transfer path
// runs unconditionally; the script-import branch is skipped.
type alwaysDeferImportScriptRunner struct{}

// TryImport always returns ScriptImportDeferMove.
func (alwaysDeferImportScriptRunner) TryImport(string, string, *episodeimport.LocalEpisode, *episodefiles.File, TransferMode) ScriptImportDecision {
	return ScriptImportDeferMove
}

// noopEventPublisher satisfies [EventPublisher] by dropping events.
type noopEventPublisher struct{}

// Publish drops the event without notifying any subscribers.
func (noopEventPublisher) Publish(any) {}

// --- Service -------------------------------------------------------

// Service ports `EpisodeFileMovingService` at C# `:27-251`. Composes
// the narrow interfaces above into the public Move/Copy operations.
//
// Goroutine-safe post-construction: stateless beyond the wired deps.
type Service struct {
	episodes EpisodeFinder
	dates    FileDateUpdater
	names    FileNameBuilder
	fs       Filesystem
	attrs    AttributeWriter
	scripts  ImportScriptRunner
	roots    RootFolderFinder
	events   EventPublisher
	cfg      Config
	log      *slog.Logger
}

// NewService binds the dependencies. fs / names / roots / cfg /
// episodes are REQUIRED. dates / attrs / scripts / events / log are
// OPTIONAL (nil-tolerant). Mirrors the slice-3.55+ constructor
// pattern.
//
// Returns error rather than panicking on missing required deps so
// the caller's startup wiring path can surface the misuse cleanly.
func NewService(
	fs Filesystem,
	names FileNameBuilder,
	roots RootFolderFinder,
	cfg Config,
	finder EpisodeFinder,
	dates FileDateUpdater,
	attrs AttributeWriter,
	scripts ImportScriptRunner,
	events EventPublisher,
	log *slog.Logger,
) (*Service, error) {
	if fs == nil {
		return nil, errors.New("episodefilemoving: NewService: nil Filesystem")
	}
	if names == nil {
		names = defaultFileNameBuilder{}
	}
	if roots == nil {
		return nil, errors.New("episodefilemoving: NewService: nil RootFolderFinder")
	}
	if cfg == nil {
		return nil, errors.New("episodefilemoving: NewService: nil Config")
	}
	if finder == nil {
		return nil, errors.New("episodefilemoving: NewService: nil EpisodeFinder")
	}
	if dates == nil {
		dates = noopFileDateUpdater{}
	}
	if attrs == nil {
		attrs = noopAttributeWriter{}
	}
	if scripts == nil {
		scripts = alwaysDeferImportScriptRunner{}
	}
	if events == nil {
		events = noopEventPublisher{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		episodes: finder,
		dates:    dates,
		names:    names,
		fs:       fs,
		attrs:    attrs,
		scripts:  scripts,
		roots:    roots,
		events:   events,
		cfg:      cfg,
		log:      log,
	}, nil
}

// MoveEpisodeFile ports the 2-arg public overload at C# `:66-70`.
// Resolves the matching episodes via [EpisodeFinder] then delegates
// to the 3-arg private path.
func (s *Service) MoveEpisodeFile(ctx context.Context, file *episodefiles.File, ser *series.Series) (*episodefiles.File, error) {
	if s == nil {
		return nil, errors.New("episodefilemoving: MoveEpisodeFile on nil receiver")
	}
	if file == nil {
		return nil, errors.New("episodefilemoving: MoveEpisodeFile: nil file")
	}
	if ser == nil {
		return nil, errors.New("episodefilemoving: MoveEpisodeFile: nil series")
	}
	eps, err := s.episodes.GetEpisodesByFileID(ctx, file.ID)
	if err != nil {
		return nil, fmt.Errorf("episodefilemoving: GetEpisodesByFileID: %w", err)
	}
	return s.moveEpisodeFile(ctx, file, ser, eps)
}

// MoveEpisodeFileWithLocal ports the 3-arg public overload at C#
// `:83-92` (`MoveEpisodeFile(EpisodeFile, LocalEpisode)`). The Go
// name disambiguates from the simpler overload; renaming both to
// MoveEpisodeFile would force a single signature.
func (s *Service) MoveEpisodeFileWithLocal(ctx context.Context, file *episodefiles.File, le *episodeimport.LocalEpisode) (*episodefiles.File, error) {
	if s == nil {
		return nil, errors.New("episodefilemoving: MoveEpisodeFileWithLocal on nil receiver")
	}
	if file == nil || le == nil {
		return nil, errors.New("episodefilemoving: MoveEpisodeFileWithLocal: nil file or LocalEpisode")
	}
	dst, err := s.names.BuildFilePath(le.Episodes, le.Series, file, filepath.Ext(le.Path), le)
	if err != nil {
		return nil, fmt.Errorf("episodefilemoving: BuildFilePath: %w", err)
	}
	if err := s.ensureEpisodeFolder(ctx, file, le.Series, localEpisodeSeasonNumber(le), dst); err != nil {
		return nil, err
	}
	s.log.Debug("episodefilemoving: moving episode file",
		"src", episodeFilePath(file, le.Series), "dst", dst)
	return s.transferFile(ctx, file, le.Series, le.Episodes, dst, TransferModeMove, le)
}

// CopyEpisodeFile ports `:94-108`. When CopyUsingHardlinks is set,
// uses HardLinkOrCopy; otherwise straight Copy.
func (s *Service) CopyEpisodeFile(ctx context.Context, file *episodefiles.File, le *episodeimport.LocalEpisode) (*episodefiles.File, error) {
	if s == nil {
		return nil, errors.New("episodefilemoving: CopyEpisodeFile on nil receiver")
	}
	if file == nil || le == nil {
		return nil, errors.New("episodefilemoving: CopyEpisodeFile: nil file or LocalEpisode")
	}
	dst, err := s.names.BuildFilePath(le.Episodes, le.Series, file, filepath.Ext(le.Path), le)
	if err != nil {
		return nil, fmt.Errorf("episodefilemoving: BuildFilePath: %w", err)
	}
	if err := s.ensureEpisodeFolder(ctx, file, le.Series, localEpisodeSeasonNumber(le), dst); err != nil {
		return nil, err
	}
	mode := TransferModeCopy
	if s.cfg.CopyUsingHardlinks() {
		mode = TransferModeHardLinkOrCopy
		s.log.Debug("episodefilemoving: attempting to hardlink",
			"src", le.Path, "dst", dst)
	} else {
		s.log.Debug("episodefilemoving: copying", "src", le.Path, "dst", dst)
	}
	return s.transferFile(ctx, file, le.Series, le.Episodes, dst, mode, le)
}

// moveEpisodeFile is the 3-arg private form at C# `:72-81`. Pulled
// out of the public path so MoveEpisodeFile can call it directly.
func (s *Service) moveEpisodeFile(ctx context.Context, file *episodefiles.File, ser *series.Series, eps []episodes.Episode) (*episodefiles.File, error) {
	dst, err := s.names.BuildFilePath(eps, ser, file, filepath.Ext(file.RelativePath), nil)
	if err != nil {
		return nil, fmt.Errorf("episodefilemoving: BuildFilePath: %w", err)
	}
	seasonNumber := 0
	if len(eps) > 0 {
		seasonNumber = eps[0].SeasonNumber
	}
	if err := s.ensureEpisodeFolder(ctx, file, ser, seasonNumber, dst); err != nil {
		return nil, err
	}
	s.log.Debug("episodefilemoving: renaming episode file",
		"src", episodeFilePath(file, ser), "dst", dst)
	return s.transferFile(ctx, file, ser, eps, dst, TransferModeMove, nil)
}

// transferFile ports `:110-175`. Validates source + destination,
// updates the EpisodeFile.RelativePath, runs the script-import path
// (no-op default), runs the actual disk transfer, then updates
// timestamps + permissions.
//
// Returns the (mutated) file pointer for parity with the C# return.
func (s *Service) transferFile(ctx context.Context, file *episodefiles.File, ser *series.Series, eps []episodes.Episode, dst string, mode TransferMode, le *episodeimport.LocalEpisode) (*episodefiles.File, error) {
	if file == nil {
		return nil, errors.New("episodefilemoving: transferFile: nil file")
	}
	if ser == nil {
		return nil, errors.New("episodefilemoving: transferFile: nil series")
	}
	if strings.TrimSpace(dst) == "" {
		return nil, errors.New("episodefilemoving: transferFile: empty destination path")
	}

	src := episodeFilePath(file, ser)
	if !s.fs.FileExists(src) {
		return nil, fmt.Errorf("episodefilemoving: %w: %s", ErrFileNotFound, src)
	}
	if src == dst {
		return nil, fmt.Errorf("episodefilemoving: %w: %s", ErrSameFilename, src)
	}

	rel, err := relativeTo(ser.Path, dst)
	if err != nil {
		return nil, fmt.Errorf("episodefilemoving: relativise: %w", err)
	}
	file.RelativePath = rel

	if le != nil {
		// C# `:132` mutates LocalEpisode.FileNameBeforeRename so the
		// post-script extras importer sees the pre-rename name.
		le.FileNameBeforeRename = rel
	}

	// Script-import branch — C# `:135-148`. The default runner always
	// returns DeferMove which routes us straight to the regular
	// transfer at line 151. Non-DeferMove decisions skip TransferFile
	// (the script handled the move).
	if le != nil {
		decision := s.scripts.TryImport(src, dst, le, file, mode)
		if decision != ScriptImportDeferMove {
			if decision == ScriptImportRenameRequested {
				// C# `:139-147`: re-run the regular MoveEpisodeFile
				// path against the (possibly script-renamed) file.
				if _, err := s.moveEpisodeFile(ctx, file, ser, eps); err != nil {
					if errors.Is(err, ErrSameFilename) {
						s.log.Debug("episodefilemoving: no rename was required after script import",
							"path", src)
					} else {
						return nil, err
					}
				}
			}
			// fall through to the post-transfer attribute updates
		} else {
			if err := s.fs.TransferFile(src, dst, mode); err != nil {
				return nil, fmt.Errorf("episodefilemoving: TransferFile: %w", err)
			}
		}
	} else {
		if err := s.fs.TransferFile(src, dst, mode); err != nil {
			return nil, fmt.Errorf("episodefilemoving: TransferFile: %w", err)
		}
	}

	if err := s.dates.ChangeFileDateForFile(file, ser, eps); err != nil {
		s.log.Warn("episodefilemoving: ChangeFileDateForFile failed", "err", err)
	}

	// C# `:156-170`: best-effort folder-mtime updates. Errors here
	// are swallowed with a warn — the file move already succeeded.
	if err := s.attrs.SetFolderLastWriteTime(ser.Path, file.DateAdded); err != nil {
		s.log.Warn("episodefilemoving: SetFolderLastWriteTime(series)", "err", err)
	}
	if ser.SeasonFolder {
		seasonFolder := filepath.Dir(dst)
		if err := s.attrs.SetFolderLastWriteTime(seasonFolder, file.DateAdded); err != nil {
			s.log.Warn("episodefilemoving: SetFolderLastWriteTime(season)", "err", err)
		}
	}

	if err := s.attrs.SetFilePermissions(dst); err != nil {
		s.log.Warn("episodefilemoving: SetFilePermissions", "err", err)
	}

	return file, nil
}

// ensureEpisodeFolder ports the C# `:177-227` overload pair. Verifies
// the root folder exists, creates the series / season / episode
// folders if missing, and emits an EpisodeFolderCreatedEvent if any
// folder was created.
func (s *Service) ensureEpisodeFolder(ctx context.Context, file *episodefiles.File, ser *series.Series, seasonNumber int, filePath string) error {
	episodeFolder := filepath.Dir(filePath)
	seasonFolder := s.names.BuildSeasonPath(ser, seasonNumber)
	seriesFolder := ser.Path

	rootFolder := s.roots.GetBestRootFolderPath(ctx, seriesFolder)
	if strings.TrimSpace(rootFolder) == "" {
		return fmt.Errorf("episodefilemoving: %w: %q is not a subdirectory of a defined root folder", ErrRootFolderNotFound, seriesFolder)
	}
	if !s.fs.FolderExists(rootFolder) {
		return fmt.Errorf("episodefilemoving: %w: %q", ErrRootFolderNotFound, rootFolder)
	}

	changed := false
	evt := EpisodeFolderCreatedEvent{Series: ser, EpisodeFile: file}

	if !s.fs.FolderExists(seriesFolder) {
		if err := s.createFolder(seriesFolder); err != nil {
			return err
		}
		evt.SeriesFolder = seriesFolder
		changed = true
	}

	if seriesFolder != seasonFolder && !s.fs.FolderExists(seasonFolder) {
		if err := s.createFolder(seasonFolder); err != nil {
			return err
		}
		evt.SeasonFolder = seasonFolder
		changed = true
	}

	if seasonFolder != episodeFolder && !s.fs.FolderExists(episodeFolder) {
		if err := s.createFolder(episodeFolder); err != nil {
			return err
		}
		evt.EpisodeFolder = episodeFolder
		changed = true
	}

	if changed {
		s.events.Publish(evt)
	}
	return nil
}

// createFolder ports `:229-249`. Recursively creates the parent
// folder if missing, then the supplied folder, then sets folder
// permissions.
//
// IO errors from CreateFolder are logged + swallowed (matching C#
// `:243-246` `_logger.Error(ex, ...)` without throwing). The C#
// version doesn't return an error from CreateFolder; only the Ensure
// guard at `:231` (empty path) raises. The Go port returns nil for
// the IO error path so the caller can continue, just like the C#
// flow.
func (s *Service) createFolder(directory string) error {
	if strings.TrimSpace(directory) == "" {
		return errors.New("episodefilemoving: createFolder: empty path")
	}
	parent := filepath.Dir(directory)
	if parent != directory && !s.fs.FolderExists(parent) {
		if err := s.createFolder(parent); err != nil {
			return err
		}
	}
	if err := s.fs.CreateFolder(directory); err != nil {
		s.log.Error("episodefilemoving: CreateFolder failed", "path", directory, "err", err)
	} else if err := s.attrs.SetFolderPermissions(directory); err != nil {
		s.log.Warn("episodefilemoving: SetFolderPermissions failed", "path", directory, "err", err)
	}
	return nil
}

// episodeFilePath ports the C# `episodeFile.Path ?? Path.Combine(...)`
// idiom at `:116`. The Go [episodefiles.File] doesn't carry a Path
// field — it's always derived from the series root + RelativePath.
func episodeFilePath(file *episodefiles.File, ser *series.Series) string {
	if file == nil || ser == nil {
		return ""
	}
	if filepath.IsAbs(file.RelativePath) {
		return file.RelativePath
	}
	return filepath.Join(ser.Path, file.RelativePath)
}

// localEpisodeSeasonNumber unwraps [episodeimport.LocalEpisode.SeasonNumber]
// to a plain int; the (int, error) → int adapter lets the writer
// pass a season into BuildSeasonPath. On error (zero / multi-season
// LocalEpisode), returns 0 — the BuildSeasonPath fallback for the
// "no season" case mirrors C# behaviour where an unset SeasonNumber
// joins the series-root specials folder.
func localEpisodeSeasonNumber(le *episodeimport.LocalEpisode) int {
	if le == nil {
		return 0
	}
	n, err := le.SeasonNumber()
	if err != nil {
		return 0
	}
	return n
}

// relativeTo wraps `filepath.Rel` with a result that doesn't include
// a leading "." path component. Mirrors the C# `string.GetRelativePath`
// extension at `:128`.
func relativeTo(base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	return rel, nil
}

// Compile-time interface-satisfaction guards. Refactor breakage at
// build time rather than wiring time.
var (
	_ FileNameBuilder    = defaultFileNameBuilder{}
	_ AttributeWriter    = noopAttributeWriter{}
	_ FileDateUpdater    = noopFileDateUpdater{}
	_ ImportScriptRunner = alwaysDeferImportScriptRunner{}
	_ EventPublisher     = noopEventPublisher{}
)
