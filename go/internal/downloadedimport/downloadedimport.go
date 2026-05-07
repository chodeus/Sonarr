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
// private helpers. The Go port landed across four slices:
//
//   - 3.79a — package skeleton, narrow interfaces,
//     [Service.ProcessPath] dispatcher, [Service.ShouldDeleteFolder],
//     and the helper builders (cleanedUpFolderName / fileIsLockedResult /
//     unknownSeriesResult / rejectionResult).
//   - 3.79b — [Service.processFolder] (both overloads at C#
//     `:159-245`) + the multi-season + locked-file gates +
//     [Service.checkEmptyResultForIssue].
//   - 3.79c — [Service.processFile] (both overloads at C# `:247-346`)
//   - the extension-rejection ladder (DangerousExtensions /
//     ExecutableExtensions / UserRejectedExtensions /
//     MediaFileExtensions).
//   - 3.79d (this slice) — [Service.ProcessRootFolder] body. Closes
//     the arc.
//
// The `cmd/sonarr/main.go` wiring slice + the
// `DownloadedEpisodesCommandService` IExecute<DownloadedEpisodesScanCommand>
// handler port (~91 LOC C# at
// `MediaFiles/DownloadedEpisodesCommandService.cs`) ports separately
// once the rest of the command-bus surface is ready to receive a real
// downloaded-episodes-scan dispatch.
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
// # Deferred (post-3.79d)
//
//   - logInaccessiblePathError (`:396-426`) — Windows-aware error
//     message generation; the Go port currently logs a single
//     "path not accessible" line and defers the runtime/Windows-mount
//     dispatch behind a Linux-first comment. Lands when first-class
//     Windows support arrives.
//   - cmd/sonarr/main.go wiring + DownloadedEpisodesCommandService
//     handler — the production caller that dispatches a
//     DownloadedEpisodesScan command into [Service.ProcessPath] /
//     [Service.ProcessRootFolder].
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
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

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

// dangerousExtensions ports `FileExtensions.cs:33-42` — the
// case-insensitive set of file extensions that trigger a
// DangerousFile rejection. Stored lowercase with a leading dot;
// callers compare via lowerExt(filepath.Ext(name)).
var dangerousExtensions = map[string]struct{}{
	".arj":  {},
	".lnk":  {},
	".lzh":  {},
	".ps1":  {},
	".scr":  {},
	".vbs":  {},
	".zipx": {},
}

// executableExtensions ports `FileExtensions.cs:43-49` — the
// case-insensitive set of file extensions that trigger an
// ExecutableFile rejection. Same shape as [dangerousExtensions].
var executableExtensions = map[string]struct{}{
	".bat": {},
	".cmd": {},
	".exe": {},
	".sh":  {},
}

// archiveExtensions ports `FileExtensions.cs:18-32` — the
// case-insensitive set of archive extensions that trigger an
// ArchiveFile rejection inside CheckEmptyResultForIssue (C# `:388`).
// Same shape as [dangerousExtensions].
//
// Note: ".tar.bz2" / ".tar.gz" in the C# set are full multi-dot
// suffixes that filepath.Ext can't recover (it returns just ".bz2"
// / ".gz"). The Go port stores both forms — the bare suffix from
// filepath.Ext is what gets matched. ".bz2" / ".gz" already cover
// the tar variants from the rejection-rule perspective.
var archiveExtensions = map[string]struct{}{
	".7z":   {},
	".bz2":  {},
	".gz":   {},
	".r00":  {},
	".rar":  {},
	".tar":  {},
	".tb2":  {},
	".tbz2": {},
	".tgz":  {},
	".zip":  {},
}

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

// ProcessRootFolder ports `DownloadedEpisodesImportService.cs:61-78`.
// Iterates every immediate subdirectory of the supplied drop-root
// path, dispatching each to [Service.processFolder] in
// [episodeimport.ImportModeAuto]; then iterates every top-level
// video file in the same root, dispatching each to
// [Service.processFile]. Concatenates every per-entry result list
// into the returned slice.
//
// dci is always nil — ProcessRootFolder is the periodic
// drop-folder-scanner entry point (the C# `DownloadedEpisodesScan`
// command), with no association to a specific download-client item.
//
// GetDirectories errors propagate as a wrapped error; the C# tree
// would let the underlying IOException unwind into the IExecute
// dispatcher, the Go port surfaces the error explicitly so callers
// can classify.
//
// GetVideoFiles is a free-function-style call returning a (possibly
// nil) slice — no error path. Mirrors C# `_diskScanService.GetVideoFiles(path, false)`.
func (s *Service) ProcessRootFolder(path string) ([]*episodeimport.ImportResult, error) {
	results := []*episodeimport.ImportResult{}

	subFolders, err := s.disk.GetDirectories(path)
	if err != nil {
		return nil, fmt.Errorf("downloadedimport: ProcessRootFolder GetDirectories(%q): %w", path, err)
	}
	for _, sub := range subFolders {
		results = append(results, s.processFolder(sub, episodeimport.ImportModeAuto, nil)...)
	}

	for _, vf := range s.scanner.GetVideoFiles(path, false) {
		results = append(results, s.processFile(vf, episodeimport.ImportModeAuto, nil)...)
	}

	return results, nil
}

// --- private dispatchers (deferred bodies) ------------------------

// processFolder mirrors C# `:159-175` (no-series overload). Discovers
// the series via [SeriesParser.GetSeriesFromTitle] applied to the
// _UNPACK_/_FAILED_-stripped folder name; nil series →
// UnknownSeriesResult. Otherwise dispatches to the typed overload.
func (s *Service) processFolder(path string, mode episodeimport.ImportMode, dci *downloadclients.Item) []*episodeimport.ImportResult {
	cleanedUpName := cleanedUpFolderName(filepath.Base(path))
	ser := s.parser.GetSeriesFromTitle(cleanedUpName)
	if ser == nil {
		s.log.Debug("downloadedimport: unknown series", "name", cleanedUpName)
		return []*episodeimport.ImportResult{unknownSeriesResult("Unknown Series", "")}
	}
	return s.processFolderWithSeries(path, mode, ser, dci)
}

// processFolderWithSeries mirrors C# `:177-245`. The folder-route
// flow with a known series — guards the existing-library path,
// runs IsFileLocked + multi-season checks, dispatches into the
// EpisodeImport pipeline, and optionally deletes the folder after a
// Move-mode import succeeds.
//
// Behaviour, in order:
//
//  1. SeriesPathExists guard (`:179-186`) — if the path is mapped to
//     an existing series folder, refuse to process and return a
//     SeriesFolder rejection. Mirrors C# warn-log + early return.
//
//  2. folderInfo = parser.ParseTitle(folderName) (`:188`).
//     downloadClientItemInfo = parser.ParseTitle(dci.Title) when
//     dci != nil (`:190`).
//
//  3. videoFiles = scanner.FilterPaths(path, scanner.GetVideoFiles(path))
//     (`:189`). The C# call passes recursive=false implicitly via
//     GetVideoFiles's default — the Go port defaults the same.
//
//  4. When dci == nil (manual import), iterate videoFiles and call
//     IsFileLocked on each. First locked → FileIsLockedResult and
//     bail (`:192-204`). When dci != nil the C# tree skips this
//     check because the download client guarantees the file is
//     no-longer-being-written-to.
//
//  5. Multi-season check (`:206-214`) — if downloadClientItemInfo
//     has IsMultiSeason=true, refuse with a MultiSeason rejection.
//
//  6. decisions = decider.GetImportDecisions(videoFiles, ser, dci,
//     downloadClientItemInfo, folderInfo, true) (`:216`).
//
//  7. importResults = runner.Import(decisions, true, dci, mode)
//     (`:217`).
//
//  8. Auto-mode resolution (`:219-222`) — when mode is Auto, resolve
//     to Move when dci is nil OR dci.CanMoveFiles, else Copy.
//
//  9. Post-import folder-delete branch (`:224-238`) — when the
//     resolved mode is Move AND any importResult has Imported AND
//     ShouldDeleteFolder returns true, call DeleteFolder(path,
//     recursive=true). DeleteFolder errors log at debug and don't
//     propagate (matches C# `:230-237` IOException catch).
//
//  10. Empty-results check (`:239-241`) — when no importResults
//     came back, append CheckEmptyResultForIssue's rejection
//     (or skip if it returns nil).
//
// Returns a non-nil slice (possibly empty when both the import
// pipeline and CheckEmptyResultForIssue produce nothing).
func (s *Service) processFolderWithSeries(path string, mode episodeimport.ImportMode, ser *series.Series, dci *downloadclients.Item) []*episodeimport.ImportResult {
	if s.seriesPaths.SeriesPathExists(path) {
		s.log.Warn("downloadedimport: unable to process folder mapped to an existing series", "path", path)
		return []*episodeimport.ImportResult{
			rejectionResult(episodeimport.ImportRejectionSeriesFolder, "Import path is mapped to a series folder"),
		}
	}

	folderInfo := parser.ParseTitle(filepath.Base(path))

	videoFiles := s.scanner.FilterPaths(path, s.scanner.GetVideoFiles(path, false))

	var dciInfo *parser.ParsedEpisodeInfo
	if dci != nil {
		dciInfo = parser.ParseTitle(dci.Title)
	}

	if dci == nil {
		for _, vf := range videoFiles {
			if s.disk.IsFileLocked(vf) {
				return []*episodeimport.ImportResult{fileIsLockedResult(s.log, vf)}
			}
		}
	}

	if dciInfo != nil && dciInfo.IsMultiSeason {
		s.log.Debug("downloadedimport: download client item marked multi-season, skipping",
			"path", path)
		return []*episodeimport.ImportResult{
			rejectionResult(episodeimport.ImportRejectionMultiSeason, "Multi-season download, unable to import automatically"),
		}
	}

	decisions := s.decider.GetImportDecisions(videoFiles, ser, dci, dciInfo, folderInfo, true)
	importResults := s.runner.Import(decisions, true, dci, mode)
	if importResults == nil {
		importResults = []*episodeimport.ImportResult{}
	}

	resolvedMode := mode
	if resolvedMode == episodeimport.ImportModeAuto {
		if dci == nil || dci.CanMoveFiles {
			resolvedMode = episodeimport.ImportModeMove
		} else {
			resolvedMode = episodeimport.ImportModeCopy
		}
	}

	hasImported := false
	for _, r := range importResults {
		if r != nil && r.ResultType() == episodeimport.ImportResultImported {
			hasImported = true
			break
		}
	}

	if resolvedMode == episodeimport.ImportModeMove && hasImported && s.ShouldDeleteFolder(path, ser) {
		s.log.Debug("downloadedimport: deleting folder after importing valid files", "path", path)
		if err := s.disk.DeleteFolder(path, true); err != nil {
			s.log.Debug("downloadedimport: unable to delete folder after importing",
				"path", path, "err", err)
		}
	} else if len(importResults) == 0 {
		if extra := s.checkEmptyResultForIssue(path); extra != nil {
			importResults = append(importResults, extra)
		}
	}

	return importResults
}

// checkEmptyResultForIssue ports `:374-394`. Scans the folder
// (recursive) for dangerous / executable / archive extensions and
// returns a single rejection ImportResult naming the first
// extension class that matched — DangerousFile > ExecutableFile >
// ArchiveFile (mirrors C# fall-through order at `:378-390`).
//
// Returns nil when no flagged extension was found OR when
// disk.GetFiles errors. Mirrors C# returning null on success and
// implicit "any" enumerator-empty.
func (s *Service) checkEmptyResultForIssue(folder string) *episodeimport.ImportResult {
	files, err := s.disk.GetFiles(folder, true)
	if err != nil {
		s.log.Debug("downloadedimport: checkEmptyResultForIssue GetFiles errored",
			"folder", folder, "err", err)
		return nil
	}
	for _, f := range files {
		if _, ok := dangerousExtensions[lowerExt(f)]; ok {
			return rejectionResult(episodeimport.ImportRejectionDangerousFile, "Caution: Found potentially dangerous file")
		}
	}
	for _, f := range files {
		if _, ok := executableExtensions[lowerExt(f)]; ok {
			return rejectionResult(episodeimport.ImportRejectionExecutableFile, "Caution: Found executable file")
		}
	}
	for _, f := range files {
		if _, ok := archiveExtensions[lowerExt(f)]; ok {
			return rejectionResult(episodeimport.ImportRejectionArchiveFile, "Found archive file, might need to be extracted")
		}
	}
	return nil
}

// lowerExt returns the lowercase extension of path, including the
// leading dot. Empty string when path has no extension. Used by the
// extension-set checks that need case-insensitive lookups against
// the lowercase-stored sets.
func lowerExt(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

// processFile mirrors C# `:247-262` (no-series overload). Discovers
// the series from the filename's stem; nil → UnknownSeriesResult.
// Otherwise dispatches to the typed overload.
func (s *Service) processFile(path string, mode episodeimport.ImportMode, dci *downloadclients.Item) []*episodeimport.ImportResult {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ser := s.parser.GetSeriesFromTitle(stem)
	if ser == nil {
		s.log.Debug("downloadedimport: unknown series for file", "file", path)
		return []*episodeimport.ImportResult{
			unknownSeriesResult(fmt.Sprintf("Unknown Series for file: %s", filepath.Base(path)), path),
		}
	}
	return s.processFileWithSeries(path, mode, ser, dci)
}

// processFileWithSeries ports `:264-346` end-to-end. The single-file
// import path applies an extension-rejection ladder before delegating
// into the EpisodeImport pipeline.
//
// Behaviour, in order (matches C# fall-through exactly):
//
//  1. ._-prefix guard (`:266-274`) — files whose stem starts with
//     "._" (macOS resource forks) → InvalidFilePath rejection. The
//     C# StartsWith check is case-sensitive; the Go port matches.
//
//  2. Dangerous extension (`:278-286`) → DangerousFile rejection
//     with the extension interpolated into the message.
//
//  3. Executable extension (`:288-296`) → ExecutableFile rejection.
//
//  4. UserRejectedExtensions (`:298-317`) — split on comma, trim
//     dot+space, re-prepend dot. If the file's extension matches
//     → UserRejectedExtension rejection. Empty config ⇒ skip.
//
//  5. MediaFileExtensions check (`:319-329`) — extension empty OR
//     not in the parser's media-extension set → UnsupportedExtension
//     rejection.
//
//  6. IsFileLocked (`:331-340`) — only when dci == nil. Locked →
//     FileIsLockedResult.
//
//  7. parser.ParseTitle on dci.Title when present (`:342`).
//
//  8. decider.GetImportDecisions on a one-element slice (`:343`).
//
//  9. runner.Import (`:345`) — returns the final result list.
//
// Extension comparisons are case-insensitive: the C# uses
// HashSet<string>(StringComparer.OrdinalIgnoreCase) for the static
// sets; the Go port pre-lowercases via [lowerExt] / strings.ToLower
// and compares against lowercase-keyed maps.
func (s *Service) processFileWithSeries(path string, mode episodeimport.ImportMode, ser *series.Series, dci *downloadclients.Item) []*episodeimport.ImportResult {
	name := filepath.Base(path)
	stem := strings.TrimSuffix(name, filepath.Ext(name))

	// 1. ._ resource-fork prefix.
	if strings.HasPrefix(stem, "._") {
		s.log.Debug("downloadedimport: file starts with '._', skipping", "file", path)
		return []*episodeimport.ImportResult{
			fileRejection(path, episodeimport.ImportRejectionInvalidFilePath, "Invalid video file, filename starts with '._'"),
		}
	}

	rawExt := filepath.Ext(name)
	ext := strings.ToLower(rawExt)

	// 2. Dangerous extension.
	if _, ok := dangerousExtensions[ext]; ok {
		msg := fmt.Sprintf("Caution: Found potentially dangerous file with extension: %s", rawExt)
		return []*episodeimport.ImportResult{
			fileRejection(path, episodeimport.ImportRejectionDangerousFile, msg),
		}
	}

	// 3. Executable extension.
	if _, ok := executableExtensions[ext]; ok {
		msg := fmt.Sprintf("Caution: Found executable file with extension: '%s'", rawExt)
		return []*episodeimport.ImportResult{
			fileRejection(path, episodeimport.ImportRejectionExecutableFile, msg),
		}
	}

	// 4. UserRejectedExtensions.
	if userRej := s.cfg.UserRejectedExtensions(); userRej != "" {
		if userRejectedContains(userRej, ext) {
			msg := fmt.Sprintf("Caution: Found file with user defined rejected extension: '%s'", rawExt)
			return []*episodeimport.ImportResult{
				fileRejection(path, episodeimport.ImportRejectionUserRejectedExtension, msg),
			}
		}
	}

	// 5. Unsupported extension (empty OR not a media file extension).
	if ext == "" || !parser.IsMediaExtension(ext) {
		s.log.Debug("downloadedimport: file has unsupported extension",
			"file", path, "extension", rawExt)
		msg := fmt.Sprintf("Invalid video file, unsupported extension: '%s'", rawExt)
		return []*episodeimport.ImportResult{
			fileRejection(path, episodeimport.ImportRejectionUnsupportedExtension, msg),
		}
	}

	// 6. IsFileLocked when dci == nil.
	if dci == nil && s.disk.IsFileLocked(path) {
		return []*episodeimport.ImportResult{fileIsLockedResult(s.log, path)}
	}

	// 7. dciInfo from dci.Title when present.
	var dciInfo *parser.ParsedEpisodeInfo
	if dci != nil {
		dciInfo = parser.ParseTitle(dci.Title)
	}

	// 8 + 9. decision pipeline + import.
	decisions := s.decider.GetImportDecisions([]string{path}, ser, dci, dciInfo, nil, true)
	results := s.runner.Import(decisions, true, dci, mode)
	if results == nil {
		results = []*episodeimport.ImportResult{}
	}
	return results
}

// fileRejection builds the per-file rejection ImportResult shape
// used by the extension-rejection ladder. Mirrors the inline
// `new ImportResult(new ImportDecision(new LocalEpisode { Path = ... },
// new ImportRejection(reason, message)), message)` blocks at C#
// `:270-274,280-285,290-295,309-315,323-328`.
//
// path → LocalEpisode.Path; reason + message → ImportRejection.
func fileRejection(path string, reason episodeimport.ImportRejectionReason, message string) *episodeimport.ImportResult {
	le := &episodeimport.LocalEpisode{Path: path}
	rej := episodeimport.NewImportRejection(reason, message)
	dec := episodeimport.NewImportDecision(le, rej)
	return episodeimport.NewSkippedImportResult(dec, message)
}

// userRejectedContains parses the comma-separated UserRejectedExtensions
// config string and reports whether ext matches any entry. Mirrors
// the C# parser at `:300-303` — split on comma, trim leading/trailing
// dots and spaces, re-prepend a leading dot, then compare.
//
// Inputs are lowercased: ext is already pre-lowered by the caller;
// the config-side entries are lowered here so comparison stays
// case-insensitive (matches HashSet<string>(StringComparer.OrdinalIgnoreCase)).
func userRejectedContains(userRej, ext string) bool {
	for _, raw := range strings.Split(userRej, ",") {
		entry := strings.Trim(raw, " .")
		if entry == "" {
			continue
		}
		if "."+strings.ToLower(entry) == ext {
			return true
		}
	}
	return false
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

// rejectionResult is reachable from processFolderWithSeries
// (slice 3.79b) and processFileWithSeries (slice 3.79c).
// fileIsLockedResult is reachable from processFolderWithSeries
// already; the slice-3.79c processFile body adds the second caller.
