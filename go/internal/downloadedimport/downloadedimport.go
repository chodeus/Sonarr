// Package downloadedimport is the Go port of
// `NzbDrone.Core.MediaFiles.DownloadedEpisodesImportService`
// (`src/NzbDrone.Core/MediaFiles/DownloadedEpisodesImportService.cs:1-428`).
// The orchestrator-of-orchestrators that processes a download-client
// drop folder (or a manual import path) and routes each candidate
// file through the EpisodeImport pipeline.
//
// This is the production caller that finally ties the slice-3.55→3.72
// EpisodeImport sub-arc into the host's command bus — once all three
// public methods (ProcessRootFolder / ProcessPath / ShouldDeleteFolder)
// are wired, the DownloadedEpisodesScan command from
// `MediaFiles/Commands/DownloadedEpisodesScanCommand.cs` can dispatch
// to this Service in `internal/commands/handlers/`.
//
// # Slice plan
//
// The C# class is ~428 LOC with three public methods and a tangle of
// private helpers. The Go port lands across multiple slices:
//
//   - 3.79a (this slice) — package skeleton, narrow interfaces,
//     [Service.ProcessPath] dispatcher, [Service.ShouldDeleteFolder],
//     and the helper builders (cleanedUpFolderName / fileIsLockedResult /
//     unknownSeriesResult / rejectionResult).
//   - 3.79b (next) — [Service.processFolder] (both overloads at C#
//     `:159-245`) + the multi-season + locked-file gates.
//   - 3.79c — [Service.processFile] (both overloads at C# `:247-346`)
//   - the extension-rejection ladder (DangerousExtensions /
//     ExecutableExtensions / UserRejectedExtensions /
//     MediaFileExtensions).
//   - 3.79d — [Service.ProcessRootFolder] + [Service.checkEmptyResultForIssue]
//   - the wiring slice that lifts disk.Provider / config.Provider /
//     the EpisodeImport orchestrator into `cmd/sonarr/main.go`.
//
// Each slice keeps the package compileable and the deferred methods
// behind `errNotPorted` returns so the slice ordering can be
// interrupted cleanly.
//
// # Narrow interfaces
//
// Nine deps. Four are narrow slices of existing Go ports
// (`*disk.Provider`, `*mediafiles.Service`, `*series.Service`,
// `*parsingservice.Service`); five sit behind interfaces because
// either (a) the Go port hasn't crossed the orchestrator yet
// (ImportDecider, ImportRunner) or (b) the production binding will
// be a free-function wrapper (SampleDetector). The 9th — Logger — is
// just `*slog.Logger`.
//
// Production wiring composes the existing concrete types via Go's
// structural typing; tests pass per-method stubs.
//
// # Deferred (this slice ships only ProcessPath + ShouldDeleteFolder)
//
//   - ProcessRootFolder (`:61-78`) — iterates GetDirectories +
//     GetVideoFiles, dispatches each to processFolder / processFile.
//     Lands in 3.79d.
//   - processFolder×2 (`:159-262`) — folder-route flow including
//     SeriesPathExists guard, IsFileLocked check, multi-season check,
//     ImportDecisionMaker + ImportApprovedEpisodes invocation, and
//     post-import folder-delete branch. Lands in 3.79b.
//   - processFile×2 (`:247-346`) — file-route flow including the
//     extension-rejection ladder. Lands in 3.79c.
//   - logInaccessiblePathError (`:396-426`) — Windows-aware error
//     message generation; the Go port currently logs a single
//     "path not accessible" line and defers the runtime/Windows-mount
//     dispatch behind a Linux-first comment.
//
// # Wiring
//
// At the host:
//
//   - DiskOps  ← *disk.Provider directly.
//   - DiskScanner ← *mediafiles.Service (or a free-function wrapper
//     around `disk.Provider.GetFiles` for the GetVideoFiles arm; the
//     C# class's IDiskScanService is mostly a video-extension filter
//     so a thin adapter suffices).
//   - SeriesPathChecker ← *series.Service.
//   - SeriesParser ← *parsingservice.Service.
//   - ImportDecider ← the future Phase-3 episodeimport.ImportDecisionMaker
//     once its consumer-facing surface ports.
//   - ImportRunner ← *episodeimport.Service.
//   - SampleDetector ← *episodeimport.SampleChecker (slice 3.55).
//   - ConfigReader ← *config.Provider.
package downloadedimport

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// errNotPorted is the sentinel returned by deferred methods. Callers
// never see this in production until the slice that ports the
// method lands; tests assert errors.Is to detect the deferred state.
var errNotPorted = errors.New("downloadedimport: method not yet ported")

// --- narrow interfaces -------------------------------------------

// DiskOps narrows `IDiskProvider` for the seven methods this
// orchestrator reaches for. Production binding wires `*disk.Provider`
// directly — every method exists at the disk package after slice
// 3.78's GetDirectories addition.
type DiskOps interface {
	// FolderExists / FileExists drive the ProcessPath dispatcher
	// at C# `:84,96`.
	FolderExists(path string) bool
	FileExists(path string) bool
	// GetDirectories enumerates the top-level entries inside the
	// drop root for ProcessRootFolder (`:65`).
	GetDirectories(path string) ([]string, error)
	// GetFiles enumerates files for the locked-file scan
	// (`:117-119`), the unknown-archive sweep inside
	// CheckEmptyResultForIssue (`:376`), and the rar-file detection
	// inside ShouldDeleteFolder (`:117-119`).
	GetFiles(path string, recursive bool) ([]string, error)
	// GetFileSize is used by ShouldDeleteFolder (`:139`) to gate the
	// "rar file >10MB" rejection. The C# `10.Megabytes()` resolves to
	// 10*1024*1024 bytes; the Go port pins this as a constant
	// (rarFileSizeThresholdBytes) at the use site.
	GetFileSize(path string) (int64, error)
	// IsFileLocked guards the file-locked rejection inside
	// processFolder (`:196`) and processFile (`:333`). Production
	// implementation lives at `internal/disk` (currently TODO —
	// `disk.Provider.IsFileLocked` ports alongside the
	// FileSetLastWriteTime primitive in a future disk slice).
	//
	// Until that lands, the production binding can pass an adapter
	// that returns false unconditionally — matches the C# behaviour
	// on filesystems where lock detection is unsupported (Linux-via-flock
	// is best-effort).
	IsFileLocked(path string) bool
	// DeleteFolder is the post-import folder-delete arm at C# `:232`.
	DeleteFolder(path string, recursive bool) error
}

// DiskScanner narrows `IDiskScanService` for the two methods this
// orchestrator reaches for. The C# class's IDiskScanService is
// mostly a video-extension filter; the Go port can either alias
// `*mediafiles.Service` or pass a thin adapter.
type DiskScanner interface {
	// GetVideoFiles returns absolute paths of every video-extension
	// file under path. recursive=false stops at the top level.
	// Mirrors `IDiskScanService.GetVideoFiles(path, recursive)` at
	// `:71,116,189`.
	GetVideoFiles(path string, recursive bool) []string
	// FilterPaths drops paths the scanner has marked as ignored
	// (e.g. RemotePathMappings + samples). Mirrors C# `:189`. The Go
	// port can return the input slice unchanged when no filter is
	// configured.
	FilterPaths(rootPath string, paths []string) []string
}

// SeriesPathChecker narrows `ISeriesService.SeriesPathExists` (one
// method). Used by processFolder to reject "import path is mapped to
// a series folder" at C# `:179-186`. Production binds
// `*series.Service`.
type SeriesPathChecker interface {
	SeriesPathExists(path string) bool
}

// SeriesParser narrows `IParsingService.GetSeries(title)` (one
// method) — given a candidate title, returns the matching series or
// nil. Used by processFolder + processFile at C# `:162,249,259`.
//
// Production binding wires `*parsingservice.Service`. Returns nil
// (not error) when the title doesn't resolve — mirrors the C# nil
// return that the orchestrator turns into an UnknownSeries
// rejection.
type SeriesParser interface {
	GetSeriesFromTitle(title string) *series.Series
}

// ImportDecider narrows `IMakeImportDecision.GetImportDecisions` —
// the entry point into the EpisodeImport decision pipeline.
// Production binding ports alongside slice 3.79b when the
// processFolder body lands.
//
// downloadClientItem may be nil (manual-import path). folderInfo +
// downloadClientItemInfo are both nil-tolerant. filterExistingFiles
// gates whether already-imported files re-enter the pipeline; the
// C# version always passes true for the downloaded-import flow.
type ImportDecider interface {
	GetImportDecisions(
		paths []string,
		ser *series.Series,
		downloadClientItem *downloadclients.Item,
		downloadClientItemInfo *parser.ParsedEpisodeInfo,
		folderInfo *parser.ParsedEpisodeInfo,
		filterExistingFiles bool,
	) []*episodeimport.ImportDecision
}

// ImportRunner narrows `IImportApprovedEpisodes.Import`. Production
// binding wires `*episodeimport.Service` (slice 3.71). Used by
// processFolder + processFile (C# `:217,345`).
type ImportRunner interface {
	Import(
		decisions []*episodeimport.ImportDecision,
		newDownload bool,
		downloadClientItem *downloadclients.Item,
		mode episodeimport.ImportMode,
	) []*episodeimport.ImportResult
}

// SampleDetector narrows `IDetectSample.IsSample` (one method). Used
// by ShouldDeleteFolder at C# `:131`. Production binding wires
// `*episodeimport.SampleChecker` (slice 3.55).
//
// The C# return is the `DetectSampleResult` enum (Sample / NotSample
// / Indeterminate); the Go port mirrors that.
type SampleDetector interface {
	IsSample(ser *series.Series, path string, isPossibleSpecial bool) episodeimport.DetectSampleResult
}

// ConfigReader narrows `IConfigService` for the one getter
// processFile reaches for (UserRejectedExtensions). Defined here so
// the package's narrow-interface convention stays consistent;
// production wires `*config.Provider`.
//
// UserRejectedExtensions returns the configured comma-separated
// extension list (with leading-dot tolerance — the C# parser strips
// dots and re-prepends one). Empty string disables the user filter.
type ConfigReader interface {
	UserRejectedExtensions() string
}

// --- types --------------------------------------------------------

// Service ports `DownloadedEpisodesImportService` (`:25-58`). Composes
// the nine narrow deps into the three public methods (ProcessRootFolder,
// ProcessPath, ShouldDeleteFolder).
//
// Goroutine-safe — no mutable state. The single value is reused
// process-wide.
type Service struct {
	disk         DiskOps
	scanner      DiskScanner
	seriesPaths  SeriesPathChecker
	parser       SeriesParser
	decider      ImportDecider
	runner       ImportRunner
	sampleDetect SampleDetector
	cfg          ConfigReader
	log          *slog.Logger
}

// NewService binds the dependencies. All of disk / scanner /
// seriesPaths / parser / decider / runner / sampleDetect / cfg are
// REQUIRED — nil falls through to a nil Service so a misconfigured
// production binding fails at the next call site rather than
// silently no-opping. log is OPTIONAL — nil falls back to
// [slog.Default].
//
// Mirrors the Phase-3 nil-Service-on-nil-dep convention from
// episodeimport/aggregation.go and recyclebin.NewService.
func NewService(
	disk DiskOps,
	scanner DiskScanner,
	seriesPaths SeriesPathChecker,
	parserSvc SeriesParser,
	decider ImportDecider,
	runner ImportRunner,
	sampleDetect SampleDetector,
	cfg ConfigReader,
	log *slog.Logger,
) *Service {
	if disk == nil || scanner == nil || seriesPaths == nil ||
		parserSvc == nil || decider == nil || runner == nil ||
		sampleDetect == nil || cfg == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		disk:         disk,
		scanner:      scanner,
		seriesPaths:  seriesPaths,
		parser:       parserSvc,
		decider:      decider,
		runner:       runner,
		sampleDetect: sampleDetect,
		cfg:          cfg,
		log:          log,
	}
}

// rarFileSizeThresholdBytes is the C# `10.Megabytes()` constant at
// `DownloadedEpisodesImportService.cs:139` — the rar-file size
// threshold that switches "delete folder" to "manual cleanup".
const rarFileSizeThresholdBytes int64 = 10 * 1024 * 1024

// --- public methods ----------------------------------------------

// ProcessPath ports `DownloadedEpisodesImportService.cs:80-110`.
// Routes the supplied path to either the folder or file flow based
// on existence checks, with a fallback log-and-empty-result path
// when neither check matches.
//
// importMode defaults to [episodeimport.ImportModeAuto] when the
// caller wants the C# `Auto` semantic (move-or-copy decided by
// downloadClientItem.CanMoveFiles inside processFolder).
//
// ser is optional — nil signals "discover the series via
// SeriesParser.GetSeriesFromTitle on the path's last segment".
// dci is optional — nil signals "manual import without a download
// client item".
//
// Returns a non-nil but possibly empty slice in every code path; the
// orchestrator never returns nil. Empty result + log line on
// inaccessible path mirrors the C# `LogInaccessiblePathError + new
// List<ImportResult>()` arm at `:108-109`.
func (s *Service) ProcessPath(
	path string,
	importMode episodeimport.ImportMode,
	ser *series.Series,
	dci *downloadclients.Item,
) []*episodeimport.ImportResult {
	s.log.Debug("downloadedimport: processing path", "path", path, "mode", importMode)

	if s.disk.FolderExists(path) {
		if ser == nil {
			return s.processFolder(path, importMode, dci)
		}
		return s.processFolderWithSeries(path, importMode, ser, dci)
	}
	if s.disk.FileExists(path) {
		if ser == nil {
			return s.processFile(path, importMode, dci)
		}
		return s.processFileWithSeries(path, importMode, ser, dci)
	}
	s.logInaccessiblePathError(path)
	return []*episodeimport.ImportResult{}
}

// ShouldDeleteFolder ports `DownloadedEpisodesImportService.cs:112-157`.
// Returns true when every video file inside path is a sample AND no
// rar files larger than [rarFileSizeThresholdBytes] are present.
//
// Behaviour:
//
//   - Any video file that fails to parse → log warn + return false
//     (mirrors C# `:127-129`).
//   - Any video file that is NOT a sample → log warn + return false
//     (mirrors C# `:131-136`; the C# inverted condition `!= Sample`
//     is preserved exactly).
//   - Any rar file > 10MB → log warn + return false (`:139-143`).
//   - DirectoryNotFoundException → debug log + return false
//     (`:147-151`). The Go port maps this to a GetFiles error that
//     errors.Is(err, fs.ErrNotExist) — surfaced as the same
//     debug-log path.
//   - Any other panic-equivalent error → debug log + return false
//     (`:152-156`). The Go port collapses this into "any non-nil
//     err from GetFiles → debug log + false".
//
// Returns true only when every checked file passes both gates.
func (s *Service) ShouldDeleteFolder(path string, ser *series.Series) bool {
	videoFiles := s.scanner.GetVideoFiles(path, true)

	allFiles, err := s.disk.GetFiles(path, true)
	if err != nil {
		// DirectoryNotFoundException equivalent or other IO error —
		// the C# tree treats both as "can't determine, don't delete".
		s.log.Debug("downloadedimport: ShouldDeleteFolder GetFiles errored",
			"path", path, "err", err)
		return false
	}

	for _, vf := range videoFiles {
		parseResult := parser.ParseTitle(filepath.Base(vf))
		if parseResult == nil {
			s.log.Warn("downloadedimport: unable to parse file on import", "file", vf)
			return false
		}
		// C# `_detectSample.IsSample(series, videoFile, episodeParseResult.IsPossibleSpecialEpisode) != DetectSampleResult.Sample`
		// — strict inequality, so Indeterminate also blocks delete.
		if s.sampleDetect.IsSample(ser, vf, parseResult.IsPossibleSpecialEpisode()) != episodeimport.DetectSampleSample {
			s.log.Warn("downloadedimport: non-sample file detected", "file", vf)
			return false
		}
	}

	for _, f := range allFiles {
		if !strings.EqualFold(filepath.Ext(f), ".rar") {
			continue
		}
		size, err := s.disk.GetFileSize(f)
		if err != nil {
			// Best-effort: treat unreadable size like the C# arm above —
			// log + bail. The C# tree's outer try/catch absorbs this
			// at `:152-156`.
			s.log.Debug("downloadedimport: ShouldDeleteFolder GetFileSize errored",
				"file", f, "err", err)
			return false
		}
		if size > rarFileSizeThresholdBytes {
			s.log.Warn("downloadedimport: RAR file detected, will require manual cleanup",
				"file", f, "size", size)
			return false
		}
	}

	return true
}

// ProcessRootFolder ports `:61-78`. Deferred to slice 3.79d. Returns
// errNotPorted-wrapped error at type-level so a host that wires this
// before 3.79d ships fails loudly rather than silently producing
// empty results.
func (s *Service) ProcessRootFolder(_ string) ([]*episodeimport.ImportResult, error) {
	return nil, fmt.Errorf("downloadedimport: ProcessRootFolder: %w", errNotPorted)
}

// --- private dispatchers (deferred bodies) ------------------------

// processFolder mirrors C# `:159-175` (no-series overload). Deferred
// to slice 3.79b. Currently returns the C# UnknownSeries rejection
// shape so the ProcessPath dispatcher's no-series → folder route
// produces a stable result while the body ports.
func (s *Service) processFolder(path string, _ episodeimport.ImportMode, _ *downloadclients.Item) []*episodeimport.ImportResult {
	cleanedUpName := cleanedUpFolderName(filepath.Base(path))
	ser := s.parser.GetSeriesFromTitle(cleanedUpName)
	if ser == nil {
		s.log.Debug("downloadedimport: unknown series", "name", cleanedUpName)
		return []*episodeimport.ImportResult{unknownSeriesResult("Unknown Series", "")}
	}
	// Body deferred to 3.79b — for now route to the typed overload
	// stub which surfaces the deferred state via the result errors
	// list.
	return s.processFolderWithSeries(path, episodeimport.ImportModeAuto, ser, nil)
}

// processFolderWithSeries mirrors C# `:177-245`. Deferred to slice
// 3.79b. Returns a single-element slice with a deferred-port error so
// callers can detect the missing implementation.
func (s *Service) processFolderWithSeries(_ string, _ episodeimport.ImportMode, _ *series.Series, _ *downloadclients.Item) []*episodeimport.ImportResult {
	dec := episodeimport.NewImportDecision(nil)
	return []*episodeimport.ImportResult{
		episodeimport.NewSkippedImportResult(dec, "downloadedimport: processFolder not yet ported (slice 3.79b)"),
	}
}

// processFile mirrors C# `:247-262` (no-series overload). Deferred
// to slice 3.79c.
func (s *Service) processFile(path string, _ episodeimport.ImportMode, _ *downloadclients.Item) []*episodeimport.ImportResult {
	ser := s.parser.GetSeriesFromTitle(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if ser == nil {
		s.log.Debug("downloadedimport: unknown series for file", "file", path)
		return []*episodeimport.ImportResult{
			unknownSeriesResult(fmt.Sprintf("Unknown Series for file: %s", filepath.Base(path)), path),
		}
	}
	return s.processFileWithSeries(path, episodeimport.ImportModeAuto, ser, nil)
}

// processFileWithSeries mirrors C# `:264-346`. Deferred to slice
// 3.79c.
func (s *Service) processFileWithSeries(_ string, _ episodeimport.ImportMode, _ *series.Series, _ *downloadclients.Item) []*episodeimport.ImportResult {
	dec := episodeimport.NewImportDecision(nil)
	return []*episodeimport.ImportResult{
		episodeimport.NewSkippedImportResult(dec, "downloadedimport: processFile not yet ported (slice 3.79c)"),
	}
}

// --- helpers ------------------------------------------------------

// cleanedUpFolderName mirrors `:348-354`. Strips the `_UNPACK_` /
// `_FAILED_` prefixes the C# tree's NotUnpackingSpecification adds
// to download-client folders that are still being processed.
func cleanedUpFolderName(folder string) string {
	folder = strings.ReplaceAll(folder, "_UNPACK_", "")
	folder = strings.ReplaceAll(folder, "_FAILED_", "")
	return folder
}

// unknownSeriesResult builds the per-file ImportResult the C# class
// returns when the parser can't resolve a series. Mirrors `:362-367`.
//
// videoFile is optional — empty string means "no LocalEpisode"
// (the folder-route case at `:170` where the rejection has no
// per-file path).
func unknownSeriesResult(message, videoFile string) *episodeimport.ImportResult {
	var le *episodeimport.LocalEpisode
	if videoFile != "" {
		le = &episodeimport.LocalEpisode{Path: videoFile}
	}
	rej := episodeimport.NewImportRejection(episodeimport.ImportRejectionUnknownSeries, "Unknown Series")
	dec := episodeimport.NewImportDecision(le, rej)
	return episodeimport.NewSkippedImportResult(dec, message)
}

// rejectionResult builds an ImportResult with the supplied reason +
// message, no LocalEpisode. Mirrors C# `:369-372`.
func rejectionResult(reason episodeimport.ImportRejectionReason, message string) *episodeimport.ImportResult {
	rej := episodeimport.NewImportRejection(reason, message)
	dec := episodeimport.NewImportDecision(nil, rej)
	return episodeimport.NewSkippedImportResult(dec, message)
}

// fileIsLockedResult builds the per-file ImportResult the C# class
// returns for a file currently locked by another process. Mirrors
// `:356-360`.
//
// log is the per-call logger so the C# `_logger.Debug(...)` arm
// stays attached to the calling Service's logger; defined as a free
// function so the helper can be reused by the deferred processFile
// body in slice 3.79c.
func fileIsLockedResult(log *slog.Logger, videoFile string) *episodeimport.ImportResult {
	if log != nil {
		log.Debug("downloadedimport: file is currently locked, skipping", "file", videoFile)
	}
	le := &episodeimport.LocalEpisode{Path: videoFile}
	rej := episodeimport.NewImportRejection(episodeimport.ImportRejectionFileLocked, "Locked file, try again later")
	dec := episodeimport.NewImportDecision(le, rej)
	return episodeimport.NewSkippedImportResult(dec, "Locked file, try again later")
}

// logInaccessiblePathError mirrors `:396-426` collapsed to the
// Linux-first arm. The C# class branches on OsInfo.IsWindows for
// network-share / UNC-path detection; the Go port logs a single
// "path not accessible" line and defers the runtime/Windows-mount
// dispatch.
//
// The C# Windows arms are documented at C# `:398-422` — when a
// future Windows-first port lands, the build-tag split will live
// in this method.
func (s *Service) logInaccessiblePathError(path string) {
	s.log.Error(
		"downloadedimport: import failed, path does not exist or is not accessible",
		"path", path,
	)
}

// --- compile-time guards ------------------------------------------

// rejectionResult and fileIsLockedResult are reachable only from the
// deferred processFolder / processFile bodies; the build pipeline
// doesn't see callers in this slice. Reference them via package-level
// var assignments so the unparam-style linter pass doesn't flag
// them as dead. Lands as a real callsite in slice 3.79b/3.79c.
var (
	_ = rejectionResult
	_ = fileIsLockedResult
)
