// Boot-time adapters and stubs that compose the real production
// services into the slice-3.79's narrow [downloadedimport] interfaces.
// Pattern is the same as `mediafiles_stubs.go`: adapter where a real
// type exists but its signature differs (ctx-threading, package-level
// free fns, missing methods); stub where the real port hasn't landed
// yet. Each stub is drop-in replaceable as the real subsystem ports.
//
// The DownloadedEpisodesScan command path becomes reachable end-to-end
// after this slice — calls to POST /api/v3/command
// {"name":"DownloadedEpisodesScan"} dispatch into
// [downloadedimport.Service.ProcessPath] / .ProcessRootFolder against
// the real disk + mediafiles + series + parsingservice + config
// stack. Decision-making and import-running still no-op until the
// EpisodeImport orchestrator constructor chain lifts to the host
// (ImportDecisionMaker / ImportApprovedEpisodes / SampleDetector
// each have their own dep chain that's heavier than this slice).
//
// Replace each adapter/stub by removing it from this file and binding
// the real production type at the cmd/sonarr boot site once its
// constructor chain is available. The
// `downloadedimport.NewService` constructor rejects nil deps so a
// missed rewire surfaces loudly at startup.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/series"
)

// downloadedImportDiskOps embeds [*disk.Provider] so the seven of
// eight [downloadedimport.DiskOps] methods that already exist on the
// production type satisfy the interface via Go's structural typing.
// Adds one new method — IsFileLocked — as a no-op false return until
// `disk.Provider.IsFileLocked` ports alongside FileSetLastWriteTime
// in a future disk slice. The narrow-interface doc at
// `downloadedimport.go:121-130` explicitly authorises this fallback:
// "the production binding can pass an adapter that returns false
// unconditionally — matches the C# behaviour on filesystems where
// lock detection is unsupported".
type downloadedImportDiskOps struct {
	*disk.Provider
}

// IsFileLocked always returns false. Replace when disk.Provider.IsFileLocked lands.
func (downloadedImportDiskOps) IsFileLocked(_ string) bool { return false }

// downloadedImportDiskScanner adapts the package-level
// [mediafiles.GetVideoFiles] + [mediafiles.FilterPaths] free
// functions to the narrow [downloadedimport.DiskScanner] interface.
// `*mediafiles.Service` doesn't expose these as methods (they were
// shipped as stateless package-level helpers in slice 3.50-ish), so
// the adapter holds the disk handle GetVideoFiles needs as its first
// arg, plus a logger so non-fatal errors get surfaced.
type downloadedImportDiskScanner struct {
	disk *disk.Provider
	log  *slog.Logger
}

// GetVideoFiles delegates to [mediafiles.GetVideoFiles] and swallows
// errors as a logged-warning + nil return. The narrow interface has
// no error channel — matches C# `IDiskScanService.GetVideoFiles`
// which returns IEnumerable<string> and lets enumeration-time errors
// surface through the catch in DownloadedEpisodesImportService.
func (s downloadedImportDiskScanner) GetVideoFiles(path string, recursive bool) []string {
	out, err := mediafiles.GetVideoFiles(s.disk, path, recursive)
	if err != nil {
		s.log.Warn("downloadedimport: GetVideoFiles failed",
			"path", path, "recursive", recursive, "err", err)
		return nil
	}
	return out
}

// FilterPaths delegates to [mediafiles.FilterPaths] with
// filterExtras=true so the production behaviour matches the
// downloaded-import flow (C# `FilterPaths` is always called with the
// extras-filter on for the scan path). On error, returns the input
// unchanged — matches the C# fall-through where a transient filter
// failure doesn't drop candidate files.
func (s downloadedImportDiskScanner) FilterPaths(rootPath string, paths []string) []string {
	out, err := mediafiles.FilterPaths(rootPath, paths, true)
	if err != nil {
		s.log.Warn("downloadedimport: FilterPaths failed",
			"rootPath", rootPath, "len", len(paths), "err", err)
		return paths
	}
	return out
}

// downloadedImportSeriesPaths adapts [*series.Service.SeriesPathExists]
// to the narrow ctx-less [downloadedimport.SeriesPathChecker]
// signature. The narrow interface (slice 3.79a) deliberately drops
// ctx + error to mirror the C# `ISeriesService.SeriesPathExists(path)`
// shape; the orchestrator body never threads ctx through the
// SeriesParser/SeriesPathChecker calls. The adapter uses
// [context.Background] and swallows errors as a logged-warning + false
// return — matches the C# `try { _seriesService.SeriesPathExists(...) }
// catch { return false; }` posture.
type downloadedImportSeriesPaths struct {
	svc *series.Service
	log *slog.Logger
}

// SeriesPathExists adapts the ctx-bearing series.Service method.
func (s downloadedImportSeriesPaths) SeriesPathExists(path string) bool {
	ok, err := s.svc.SeriesPathExists(context.Background(), path)
	if err != nil {
		s.log.Warn("downloadedimport: SeriesPathExists failed",
			"path", path, "err", err)
		return false
	}
	return ok
}

// downloadedImportSeriesParser adapts
// [*parsingservice.Service.GetSeriesFromTitle] (slice 3.81) to the
// narrow [downloadedimport.SeriesParser] signature. Same ctx-less +
// errorless contract as SeriesPathChecker above; on error the adapter
// logs and returns nil so the orchestrator's UnknownSeries rejection
// arm fires — matches the C# `try { _parsingService.GetSeries(...) }
// catch { return null; }` behaviour at
// `DownloadedEpisodesImportService.cs:162-164`.
type downloadedImportSeriesParser struct {
	svc *parsingservice.Service
	log *slog.Logger
}

// GetSeriesFromTitle adapts the ctx-bearing parsingservice method.
func (s downloadedImportSeriesParser) GetSeriesFromTitle(title string) *series.Series {
	ser, err := s.svc.GetSeriesFromTitle(context.Background(), title)
	if err != nil {
		s.log.Warn("downloadedimport: GetSeriesFromTitle failed",
			"title", title, "err", err)
		return nil
	}
	return ser
}

// downloadedImportDecider adapts [*episodeimport.ImportDecisionMaker.GetImportDecisions]
// (slice 3.65) to the narrow ctx-less + errorless
// [downloadedimport.ImportDecider] interface from slice 3.79a. Same
// posture as the other ctx-bridge adapters in this file:
// [context.Background] supplied for the underlying call; errors
// surface as a logged-warning + nil-decisions return so the
// orchestrator's [downloadedimport.checkEmptyResultForIssue] arm
// (slice 3.79b) maps the failure to a documented rejection.
//
// Construction site at the cmd/sonarr boot path (slice 3.85) wires:
//
//   - filter      ← *episodefiles.Service (FilterExistingFiles)
//   - aggregator  ← *episodeimport.AggregationService (empty
//     aggregator list — augmenter constructor chain
//     ports in a future slice; an empty list still
//     runs the Size + SceneName fills inside Augment)
//   - disk        ← *disk.Provider (GetFileSize)
//   - sampler     ← *episodeimport.SampleDetector (slice 3.84)
//   - pathParser  ← nil → defaults to parser.ParsePath
//   - tracked     ← nil (no trackeddownloads.Service yet)
//   - formats     ← nil (no LocalEpisodeCustomFormatCalculator yet)
//   - specs       ← nil/empty (every spec ports separately and the
//     ImportDecisionMaker tolerates an empty spec list
//     per its constructor doc — every file is approved
//     by default, matching the C# IoC behaviour with
//     zero IImportDecisionEngineSpecification bindings)
type downloadedImportDecider struct {
	svc *episodeimport.ImportDecisionMaker
	log *slog.Logger
}

// GetImportDecisions adapts the narrow signature to
// ImportDecisionMaker.GetImportDecisions by packing the four
// caller-supplied params into a [episodeimport.GetImportDecisionsOptions]
// struct. SceneSource is left at its zero (false) — the
// downloaded-episodes-scan flow is never scene-numbered (matches the
// C# `IsSceneSource = false` posture at
// `DownloadedEpisodesImportService.cs:124`).
func (d downloadedImportDecider) GetImportDecisions(
	paths []string,
	ser *series.Series,
	downloadClientItem *downloadclients.Item,
	downloadClientItemInfo *parser.ParsedEpisodeInfo,
	folderInfo *parser.ParsedEpisodeInfo,
	filterExistingFiles bool,
) []*episodeimport.ImportDecision {
	out, err := d.svc.GetImportDecisions(
		context.Background(),
		paths,
		ser,
		episodeimport.GetImportDecisionsOptions{
			DownloadItem:        downloadClientItem,
			DownloadItemInfo:    downloadClientItemInfo,
			FolderInfo:          folderInfo,
			FilterExistingFiles: filterExistingFiles,
		},
	)
	if err != nil {
		d.log.Warn("downloadedimport: GetImportDecisions failed",
			"len", len(paths), "err", err)
		return nil
	}
	return out
}

// downloadedImportRunner adapts [*episodeimport.ImportApprovedEpisodes.Import]
// (slice 3.71) to the narrow ctx-less + errorless
// [downloadedimport.ImportRunner] interface from slice 3.79a. Same
// posture as the slice-3.85 [downloadedImportDecider] /
// slice-3.90 [parsingservice] adapters: [context.Background] for the
// underlying call; errors surface as a logged-warning + nil-results
// return so the orchestrator's
// [downloadedimport.checkEmptyResultForIssue] arm (slice 3.79b)
// maps the failure to a documented rejection.
//
// Construction site at the cmd/sonarr boot path (slice 3.98) wires:
//
//   - files          ← *episodefiles.Service (Add + GetFilesWithRelativePath +
//     Delete — all match MediaFileWriter exactly via Go's
//     structural typing)
//   - disk           ← *disk.Provider (GetFileSize for FileSizeReader)
//   - upgrader       ← slice-3.97 *upgrademediafiles.Service
//     (no longer the slice-3.71 noopMediaFileUpgrader —
//     UpgradeEpisodeFile now actually recycles + replaces
//     existing files via the slice-3.96
//     *episodefilemoving.Service writer)
//   - extras         ← nil → noopExtrasImporter (extras subsystem unported)
//   - historyFinder  ← nil → noopHistoryGrabFinder (no HistoryGrabsFinder
//     wiring here yet — slice-3.61 finder exists but
//     MostRecentGrabbed-shape adapter is its own slice)
//   - qcmp           ← nil → noopQualityComparer (decisionengine.CompareQualityModels
//     isn't reachable here yet — defaults to "equal", which
//     collapses ImportApprovedEpisodes.sortApproved to
//     Size-only ordering; matches the C# behaviour when
//     the comparer's QualityProfile lookup yields equal
//     qualities)
//   - events         ← nil → noopImportEventPublisher (the orchestrator-emitted
//     EpisodeImportedEvent / EpisodeImportFailedEvent has no
//     ported subscribers yet; the messaging.Bus generic
//     Publish[E] doesn't satisfy the bare Publish(any)
//     narrow interface without an adapter)
//   - rescan         ← nil → noopRescanCommander (RescanSeriesCommand is
//     wired through the commands.Queue but the
//     PushRescanSeries adapter shape ports separately)
//
// With this slice in place the POST /api/v3/command
// {"name":"DownloadedEpisodesScan"} endpoint actually moves a downloaded
// file into the series library — the last and biggest stub in the
// import pipeline (stubImportRunner since slice 3.79a) is replaced by
// the real production binding. The downstream noops gate ancillary
// behaviours (extras, history enrichment, quality-tiebreak,
// event-emission, rescan) that are NOT required for "files actually
// move" — each is an additive slice that doesn't break the closure
// when wired later.
type downloadedImportRunner struct {
	svc *episodeimport.ImportApprovedEpisodes
	log *slog.Logger
}

// Import adapts the narrow ctx-less + errorless signature to
// [*episodeimport.ImportApprovedEpisodes.Import] by supplying
// [context.Background] for the underlying call and swallowing the
// returned error to a logged-warning + nil-results.
//
// Mirrors the C# `IImportApprovedEpisodes.Import` shape — the C#
// surface is also errorless (the orchestrator catches every internal
// failure and emits per-decision ImportResult.Result entries
// describing the failure, not a top-level throw). The Go port's
// `error` return is a defensive escape hatch for the orchestrator's
// own ctx-cancellation / nil-receiver guards; in practice the real
// orchestrator doesn't surface errors here for a non-cancelled ctx.
func (r downloadedImportRunner) Import(
	decisions []*episodeimport.ImportDecision,
	newDownload bool,
	downloadClientItem *downloadclients.Item,
	mode episodeimport.ImportMode,
) []*episodeimport.ImportResult {
	out, err := r.svc.Import(context.Background(), decisions, newDownload, downloadClientItem, mode)
	if err != nil {
		r.log.Warn("downloadedimport: ImportApprovedEpisodes.Import failed",
			"len", len(decisions), "newDownload", newDownload, "mode", mode, "err", err)
		return nil
	}
	return out
}

// noopVideoRuntimeReader is a stub [episodeimport.VideoRuntimeReader]
// that always returns the package's [episodeimport.ErrRuntimeUnavailable]
// sentinel. Slice 3.84 wires the real *episodeimport.SampleDetector at
// the host but defers the ffprobe-backed reader port — until that
// lands, every reader call surfaces as a "no runtime" Indeterminate
// inside the SampleDetector's classify pipeline. The downstream
// orchestrator's ShouldDeleteFolder gate at C# `:131` treats anything
// other than DetectSampleSample as "don't sample-reject", so the
// production behaviour is conservatively safe — no false-positive
// sample-deletes while ffprobe is missing.
//
// The extension/special short-circuit arms inside SampleDetector still
// fire correctly (.flv / .strm / specials → NotSample), so this stub
// only affects the runtime-bucket arm. Replace when an ffprobe-backed
// reader (or mediainfo-backed equivalent) ports into the host.
type noopVideoRuntimeReader struct{}

// GetRunTime always returns (0, ErrRuntimeUnavailable). The error is
// the package's typed sentinel so SampleDetector's error-log uses the
// right phrasing ("runtime unavailable" rather than "ffprobe missing").
func (noopVideoRuntimeReader) GetRunTime(_ context.Context, _ string) (time.Duration, error) {
	return 0, episodeimport.ErrRuntimeUnavailable
}

// downloadedImportSampleDetector adapts the slice-3.84
// [*episodeimport.SampleDetector.IsSampleForSeries] (3-arg overload)
// to the narrow ctx-less [downloadedimport.SampleDetector] interface.
// Same posture as the other ctx-bridge adapters in this file —
// [context.Background] for the underlying call; the narrow interface
// drops ctx by design at slice 3.79a.
//
// Composition: holds a *episodeimport.SampleDetector that's
// constructed at the boot site with the noopVideoRuntimeReader above.
// Once the real reader ports, the only edit is at the boot site (swap
// noopVideoRuntimeReader for the real binding) — the adapter and the
// SampleDetector struct itself stay unchanged.
type downloadedImportSampleDetector struct {
	svc *episodeimport.SampleDetector
}

// IsSample bridges the narrow signature to IsSampleForSeries.
func (s downloadedImportSampleDetector) IsSample(ser *series.Series, path string, isPossibleSpecial bool) episodeimport.DetectSampleResult {
	return s.svc.IsSampleForSeries(context.Background(), ser, path, isPossibleSpecial)
}

// episodeParsingServiceAdapter bridges *parsingservice.Service to the
// [episodeimport.EpisodeParsingService] narrow interface (slice 3.61).
// The narrow interface deliberately drops ctx + error from the C#-port
// signatures; the adapter supplies [context.Background] for both calls
// and swallows errors as a logged-warning + nil/empty return.
//
// Used by [episodeimport.AggregateEpisodes] (slice 3.61) to derive
// LocalEpisode.Episodes from the file's parsed info via the three-source
// FileEpisodeInfo / DownloadClientEpisodeInfo / FolderEpisodeInfo
// resolution at C# `AggregateEpisodes.cs:11-113`. Adapter posture
// matches the slice-3.82 ctx-bridge convention used for SeriesPaths /
// SeriesParser.
type episodeParsingServiceAdapter struct {
	svc *parsingservice.Service
	log *slog.Logger
}

// ParseSpecialEpisodeTitle adapts the ctx-bearing
// *parsingservice.Service.ParseSpecialEpisodeTitle. nil-result on
// error matches the C# `try { _parsingService.ParseSpecialEpisodeTitle(...) }
// catch { return null; }` posture used at the AggregateEpisodes call
// site.
func (a episodeParsingServiceAdapter) ParseSpecialEpisodeTitle(
	prev *parser.ParsedEpisodeInfo, title string, s *series.Series,
) *parser.ParsedEpisodeInfo {
	out, err := a.svc.ParseSpecialEpisodeTitle(context.Background(), prev, title, s)
	if err != nil {
		a.log.Warn("downloadedimport: ParseSpecialEpisodeTitle failed",
			"title", title, "err", err)
		return nil
	}
	return out
}

// GetEpisodes adapts the ctx-bearing *parsingservice.Service.GetEpisodes.
// Empty-slice on error matches the C# null/empty handling at
// `AggregateEpisodes.cs:67-95` where a non-resolving parse short-circuits
// to "no episodes" rather than propagating.
func (a episodeParsingServiceAdapter) GetEpisodes(
	parsed *parser.ParsedEpisodeInfo, s *series.Series, sceneSource bool,
) []episodes.Episode {
	out, err := a.svc.GetEpisodes(context.Background(), parsed, s, sceneSource)
	if err != nil {
		a.log.Warn("downloadedimport: GetEpisodes failed",
			"sceneSource", sceneSource, "err", err)
		return nil
	}
	return out
}

// fileNamerAdapter bridges *namingconfig.Service + the package-level
// [organizer] free functions to the slice-3.65
// [episodeimport.FileNamer] narrow interface that the
// AbsoluteEpisodeNumberSpecification + EpisodeTitleSpecification
// reach for. The C# `IBuildFileNames` interface (`Organizer/FileNameBuilder.cs:21-31`)
// has constructor-injected `INamingConfigService`; the Go port
// shipped the equivalent primitives as package-level free fns
// (organizer.RequiresAbsoluteEpisodeNumber / RequiresEpisodeTitle)
// taking a [organizer.BuildOptions] bag. The adapter loads the
// active NamingConfig row per call and translates the relevant
// fields into a BuildOptions before forwarding.
//
// Per-call DB hit posture matches the C# IoC default — namingconfig.Service
// doesn't cache, the production singleton-row Service does an
// indexed PK read per GetConfig. A future slice may add a
// ConfigSavedEvent-driven cache wrapper; until then the per-call
// hit is the simplest correct posture.
//
// Errors from GetConfig surface as a logged-warning + zero-value
// BuildOptions return — RenameEpisodes=false there short-circuits
// RequiresEpisodeTitle to false, and an empty AnimeEpisodeFormat
// short-circuits RequiresAbsoluteEpisodeNumber to false. Both are
// Accept-safe defaults (the orchestrator won't reject on a
// transient namingconfig DB outage).
type fileNamerAdapter struct {
	svc *namingconfig.Service
	log *slog.Logger
}

// buildOptions loads the active NamingConfig row and returns a
// BuildOptions bag with the format fields the spec helpers actually
// read. The other BuildOptions fields stay zero — the spec helpers
// don't consult them.
func (f fileNamerAdapter) buildOptions() organizer.BuildOptions {
	cfg, err := f.svc.GetConfig(context.Background())
	if err != nil || cfg == nil {
		f.log.Warn("filenamer: GetConfig failed; falling back to zero BuildOptions",
			"err", err)
		return organizer.BuildOptions{}
	}
	return organizer.BuildOptions{
		RenameEpisodes:        cfg.RenameEpisodes,
		StandardEpisodeFormat: cfg.StandardEpisodeFormat,
		DailyEpisodeFormat:    cfg.DailyEpisodeFormat,
		AnimeEpisodeFormat:    cfg.AnimeEpisodeFormat,
	}
}

// RequiresAbsoluteEpisodeNumber forwards to
// [organizer.RequiresAbsoluteEpisodeNumber] with a freshly-loaded
// BuildOptions.
func (f fileNamerAdapter) RequiresAbsoluteEpisodeNumber() bool {
	return organizer.RequiresAbsoluteEpisodeNumber(f.buildOptions())
}

// RequiresEpisodeTitle forwards to [organizer.RequiresEpisodeTitle]
// with a freshly-loaded BuildOptions and the per-call series +
// episodes.
func (f fileNamerAdapter) RequiresEpisodeTitle(s *series.Series, eps []episodes.Episode) bool {
	return organizer.RequiresEpisodeTitle(s, eps, f.buildOptions())
}

// episodeTitleConfigAdapter bridges *config.Provider to the slice-3.65
// [episodeimport.EpisodeTitleConfig] narrow interface that the
// EpisodeTitleSpecification reaches for. The C# `IConfigService`
// surface returns the enum directly; the Go-port config.Provider
// returns the raw string identifier so the adapter parses on each
// call via [episodeimport.ParseEpisodeTitleRequiredType] (matches
// the comment hint at `episodeimport/specs.go:505`).
//
// Production posture: per-call DB hit through *config.Provider's
// in-memory cache (config.Provider lazily caches every Config row
// and invalidates on ConfigSavedEvent, so this is much cheaper than
// the namingconfig.Service per-call DB hit in fileNamerAdapter
// above).
type episodeTitleConfigAdapter struct {
	cfg *config.Provider
}

// EpisodeTitleRequired returns the typed enum.
func (a episodeTitleConfigAdapter) EpisodeTitleRequired() episodeimport.EpisodeTitleRequiredType {
	return episodeimport.ParseEpisodeTitleRequiredType(a.cfg.EpisodeTitleRequired())
}

// episodeFileResolverAdapter bridges *episodefiles.Service to the
// [episodeimport.EpisodeFileResolver] narrow interface (slice 3.65).
// Method-name mismatch only — the C# narrow interface uses `GetByID`
// while the Go-port service shipped slice 3.2 with `GetFile`. Both
// signatures are identical (ctx + int → *File + error); the adapter
// is a single-line forward.
//
// Used by [AggregateSubtitleInfo] (slice 3.65) which fetches the
// previously-imported EpisodeFile to merge its subtitle paths into
// the LocalEpisode being aggregated. The C# behaviour at
// `AggregateSubtitleInfo.cs:34-38` treats a nil result as "no prior
// file; nothing to merge"; the Go port preserves that.
type episodeFileResolverAdapter struct {
	svc *episodefiles.Service
}

// GetByID forwards to *episodefiles.Service.GetFile.
func (a episodeFileResolverAdapter) GetByID(ctx context.Context, id int) (*episodefiles.File, error) {
	return a.svc.GetFile(ctx, id)
}

// rootFolderFinderAdapter bridges *rootfolders.Repo to the slice-3.70
// [episodefilemoving.RootFolderFinder] narrow interface. The C#
// `IRootFolderService.GetBestRootFolderPath` returns plain string —
// the Go-port rootfolders.Service.GetBestRootFolderPath returns
// (string, error) AND requires an additional DiskProvider with
// FolderWritable + GetTotalSize methods that don't exist on
// `*disk.Provider` yet. The adapter dodges the dep gap by talking
// directly to *rootfolders.Repo + the package-level
// [rootfolders.BestPath] free fn — neither requires the missing
// disk surfaces.
//
// On Repo.All error the adapter returns "" (the writer surfaces
// [episodefilemoving.ErrRootFolderNotFound]); on a normal lookup
// rootfolders.BestPath itself falls back to filepath.Dir(path) when
// no configured root matches, so the empty-string arm is reserved
// for hard DB failures.
//
// Future slice will lift this to consume the real *rootfolders.Service
// once `disk.Provider.FolderWritable` + `GetTotalSize` port; the
// existing interface signature is unchanged and the call site here
// will swap to a thin Service-wrapping adapter.
type rootFolderFinderAdapter struct {
	repo *rootfolders.Repo
	log  *slog.Logger
}

// GetBestRootFolderPath swallows the (string, error) tuple to a
// plain string — empty on error mirrors C# `IRootFolderService` which
// throws inside the writer and propagates upward as
// RootFolderNotFoundException at `EpisodeFileMovingService.cs:191-197`.
// The Go writer's check at `episodefilemoving.go:601-606` treats
// empty as ErrRootFolderNotFound so the propagation is preserved.
func (a rootFolderFinderAdapter) GetBestRootFolderPath(ctx context.Context, path string) string {
	rows, err := a.repo.All(ctx)
	if err != nil {
		a.log.Warn("rootfolders: All failed; returning empty best-path",
			"path", path, "err", err)
		return ""
	}
	folders := make([]rootfolders.Folder, 0, len(rows))
	for _, r := range rows {
		folders = append(folders, rootfolders.Folder{ID: r.ID, Path: r.Path})
	}
	return rootfolders.BestPath(path, folders)
}

// nameBuilderAdapter bridges *namingconfig.Service + the package-level
// [organizer] free fns to the slice-3.70 [episodefilemoving.FileNameBuilder]
// narrow interface. The C# `IBuildFileNames` interface
// (`Organizer/FileNameBuilder.cs:21-31`) wraps the equivalent of
// `BuildFilePath` + `BuildSeasonPath` over a constructor-injected
// `INamingConfigService`. The Go port shipped the equivalent
// primitives as package-level free fns taking a [organizer.BuildOptions]
// bag; this adapter loads the active NamingConfig row per call and
// translates ALL writer-relevant fields into a BuildOptions before
// forwarding.
//
// Distinct from the slice-3.94 fileNamerAdapter — that adapter only
// populates the four format-string fields the AbsoluteEpisodeNumber /
// EpisodeTitle specs read (RenameEpisodes / Standard / Daily / Anime
// formats). The writer needs the FULL BuildOptions including the
// folder-format strings (SeriesFolderFormat / SeasonFolderFormat /
// SpecialsFolderFormat — used by [organizer.BuildSeasonPath]) and
// the ColonReplacement / IllegalCharacter cleaning fields (used by
// [organizer.BuildFileName] when rendering tokens). The two
// adapters can't share a translation function because their
// downstream consumers read different BuildOptions subsets.
//
// Per-call DB hit posture matches the C# IoC default — namingconfig.Service
// doesn't cache, the production singleton-row Service does an indexed
// PK read per GetConfig. A future slice may add a ConfigSavedEvent-driven
// cache wrapper; until then the per-call hit is the simplest correct
// posture. The hit is rare in practice — every BuildFilePath /
// BuildSeasonPath call corresponds to one episode being moved, not
// one user action.
//
// Errors from GetConfig surface as a logged-warning + zero-value
// BuildOptions return. Zero-value RenameEpisodes=false short-circuits
// [organizer.BuildFileName] to "use the original filename + extension"
// (matches `:147-154`), which is a reasonable conservative fallback
// for a transient namingconfig DB outage — the writer still moves the
// file with its original name into the configured destination folder.
type nameBuilderAdapter struct {
	svc *namingconfig.Service
	log *slog.Logger
}

// buildOptions loads the active NamingConfig row and returns a
// BuildOptions populated with every field the writer's downstream
// organizer calls consult. Differs from fileNamerAdapter.buildOptions
// (slice 3.94) in that this populates the folder-format + cleaning
// fields too — the writer's BuildFilePath path renders the season
// folder which depends on SeriesFolderFormat / SeasonFolderFormat /
// SpecialsFolderFormat, and the filename rendering applies the
// configured colon/illegal-character cleaning rules.
func (a nameBuilderAdapter) buildOptions() organizer.BuildOptions {
	cfg, err := a.svc.GetConfig(context.Background())
	if err != nil || cfg == nil {
		a.log.Warn("namebuilder: GetConfig failed; falling back to zero BuildOptions",
			"err", err)
		return organizer.BuildOptions{}
	}
	return organizer.BuildOptions{
		RenameEpisodes:               cfg.RenameEpisodes,
		StandardEpisodeFormat:        cfg.StandardEpisodeFormat,
		DailyEpisodeFormat:           cfg.DailyEpisodeFormat,
		AnimeEpisodeFormat:           cfg.AnimeEpisodeFormat,
		SeriesFolderFormat:           cfg.SeriesFolderFormat,
		SeasonFolderFormat:           cfg.SeasonFolderFormat,
		SpecialsFolderFormat:         cfg.SpecialsFolderFormat,
		MultiEpisodeStyle:            organizer.MultiEpisodeStyle(cfg.MultiEpisodeStyle),
		ReplaceIllegalCharacters:     cfg.ReplaceIllegalCharacters,
		ColonReplacementFormat:       organizer.ColonReplacementFormat(cfg.ColonReplacementFormat),
		CustomColonReplacementFormat: cfg.CustomColonReplacementFormat,
	}
}

// BuildFilePath forwards to [organizer.BuildFilePath] with a
// freshly-loaded BuildOptions. The LocalEpisode arg currently feeds
// only `CustomFormats` into the underlying BuildOptions — but the
// Go-port persisted form ([]customformats.CustomFormat) lacks the
// typed Specifications field BuildOptions wants
// ([]*customformats.Format), so the typed slice stays nil here.
// Mirrors the slice-3.70 defaultFileNameBuilder.BuildFilePath
// behaviour — token rendering proceeds without CustomFormat-driven
// tokens until the format-deserialiser ports.
func (a nameBuilderAdapter) BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string, _ *episodeimport.LocalEpisode) (string, error) {
	return organizer.BuildFilePath(eps, s, file, extension, a.buildOptions())
}

// BuildSeasonPath forwards to [organizer.BuildSeasonPath] with a
// FolderOptions derived from the loaded BuildOptions (via
// BuildOptions.FolderOptions which extracts the four folder-format +
// cleaning fields).
func (a nameBuilderAdapter) BuildSeasonPath(s *series.Series, seasonNumber int) string {
	return organizer.BuildSeasonPath(s, seasonNumber, a.buildOptions().FolderOptions())
}

// importEventPublisherAdapter bridges *messaging.Bus to the slice-3.71
// [episodeimport.ImportEventPublisher] narrow interface. The narrow
// interface signature is `Publish(event any)` (the C# IEventAggregator
// shape) but the Go-port [messaging.Publish] is a top-level GENERIC
// function — `Publish[E any](ctx, *Bus, event E)` — that infers E from
// the value's static type. Calling it with `any` would set E=any,
// which means subscribers must register against `any` to receive the
// event; that's the wrong fan-out shape (subscribers register against
// a concrete event type so the bus's per-type handler map can dispatch
// without a type-switch on every published event).
//
// The adapter therefore type-switches the incoming `any` payload on
// the two concrete event types the slice-3.71 ImportApprovedEpisodes
// orchestrator emits, then calls the generic Publish with the
// rebound concrete static type. Unknown event types log a warn-level
// "unrecognised event" line and drop — defensive for a future event
// type added to the orchestrator without the adapter being updated
// (which would otherwise be silently swallowed by the orchestrator's
// nil-tolerant noopImportEventPublisher fallback).
//
// errors.Is against [messaging.ErrShutdown] is swallowed silently
// because the bus may be in the lifecycle-shutdown window when the
// orchestrator's ImportApprovedEpisodes.processOne emits a final
// EpisodeImportedEvent (e.g. a long-running scan finishing during
// app shutdown); other errors log at warn-level.
//
// Slice 3.99 (post-arc) adds this adapter, replacing the
// noopImportEventPublisher default at the cmd/sonarr boot site so
// any subscribers that port later see events from production
// without further wiring at this site.
type importEventPublisherAdapter struct {
	bus *messaging.Bus
	log *slog.Logger
}

// Publish dispatches the event by concrete type to the typed bus.
// Mirrors the C# IEventAggregator.PublishEvent overload-resolution
// behaviour where the static type drives subscriber dispatch.
func (a importEventPublisherAdapter) Publish(event any) {
	switch e := event.(type) {
	case episodeimport.EpisodeImportedEvent:
		if err := messaging.Publish(context.Background(), a.bus, e); err != nil &&
			!errors.Is(err, messaging.ErrShutdown) {
			a.log.Warn("episodeimport: publish EpisodeImportedEvent",
				"localPath", localEpisodePath(e.LocalEpisode), "err", err)
		}
	case episodeimport.EpisodeImportFailedEvent:
		if err := messaging.Publish(context.Background(), a.bus, e); err != nil &&
			!errors.Is(err, messaging.ErrShutdown) {
			a.log.Warn("episodeimport: publish EpisodeImportFailedEvent",
				"localPath", localEpisodePath(e.LocalEpisode), "underlying", e.Err, "err", err)
		}
	default:
		a.log.Warn("episodeimport: importEventPublisherAdapter: unrecognised event type — dropping",
			"type", fmt.Sprintf("%T", event))
	}
}

// localEpisodePath safely extracts the LocalEpisode.Path for log
// lines without panicking on a nil pointer (the orchestrator
// constructs both events with le non-nil, but the defensive guard
// keeps the adapter robust against future code-paths that emit a
// nil LocalEpisode).
func localEpisodePath(le *episodeimport.LocalEpisode) string {
	if le == nil {
		return ""
	}
	return le.Path
}

// rescanSeriesCommanderAdapter bridges *commands.Service to the
// slice-3.71 [episodeimport.RescanSeriesCommander] narrow interface.
// The narrow interface is a single fire-and-forget method
// `PushRescanSeries(seriesID int)` (mirrors the C# `IManageCommandQueue.Push`
// call at `ImportApprovedEpisodes.cs:186`). The adapter encodes the
// payload into the canonical JSON shape the slice-3.49
// `mediafiles.RescanSeriesCommand` expects + delegates to
// [*commands.Service.Push].
//
// Posture mirrors the C# overload's default — `_commandQueueManager.Push(new RescanSeriesCommand(seriesId))`
// goes through the no-arg-trigger overload at `CommandQueueManager.cs:101`
// which defaults Priority=Normal + Trigger=Unspecified. The Go port
// preserves these defaults so the queued row matches a C# row pushed
// from the same call site byte-for-byte (modulo ID + QueuedAt).
//
// The adapter is best-effort: errors from Push log at warn-level and
// drop. The orchestrator's caller at C# `:186` doesn't check the return
// value either — pushing is "send and forget"; if the queue is broken
// the operator hits user-visible failures elsewhere first.
//
// Slice 3.A0 (post-arc) wires this adapter at the cmd/sonarr boot
// site, replacing the noopRescanCommander default the slice-3.98
// ImportApprovedEpisodes constructor binds.
type rescanSeriesCommanderAdapter struct {
	cmds *commands.Service
	log  *slog.Logger
}

// PushRescanSeries enqueues a RescanSeries command scoped to the
// supplied series ID. JSON-encodes the [mediafiles.RescanSeriesCommand]
// payload via the typed marshaller (so the camelCase tag at
// `mediafiles/commands.go:24` survives) then calls
// [*commands.Service.Push] with the canonical name, body, priority,
// trigger.
//
// Errors log at warn-level and drop — best-effort posture matches the
// C# call site's fire-and-forget shape.
func (a rescanSeriesCommanderAdapter) PushRescanSeries(seriesID int) {
	id := seriesID
	body, err := json.Marshal(mediafiles.RescanSeriesCommand{SeriesID: &id})
	if err != nil {
		a.log.Warn("rescan-series: marshal command body",
			"seriesId", seriesID, "err", err)
		return
	}
	if _, err := a.cmds.Push(
		context.Background(),
		mediafiles.RescanSeriesName,
		string(body),
		commands.PriorityNormal,
		commands.TriggerUnspecified,
	); err != nil {
		a.log.Warn("rescan-series: push command",
			"seriesId", seriesID, "err", err)
	}
}

// historyGrabFinderForImportAdapter bridges *history.Service to the
// slice-3.71 [episodeimport.HistoryGrabFinderForImport] narrow
// interface. The narrow interface is `MostRecentGrabbed(ctx, downloadID)
// → (*history.Entry, error)` (mirrors the C#
// `_historyService.FindByDownloadId(...).Where(h => h.EventType == Grabbed).OrderByDescending(h => h.Date).FirstOrDefault()`
// chain at `ImportApprovedEpisodes.cs:92-94`).
//
// The adapter delegates to *history.Service.Find with the typed
// EventTypeGrabbed filter (slice 3.62 ported the Find method that
// applies the EventType=Grabbed filter in-memory after pulling all
// rows for the downloadID — date-DESC ordering preserved by the
// underlying repo). First entry of the filtered result is the most
// recent.
//
// Empty downloadID short-circuits to (nil, nil) — the C# call site
// is gated on `localEpisode.DownloadClientItem != null` AND
// `!string.IsNullOrWhiteSpace(downloadId)`, so the adapter mirrors
// that pre-filter to keep the code paths identical.
//
// Lookup errors propagate to the orchestrator (slice 3.71), which
// catches them at C# `:96-99` with a try/catch + log + skip — the
// Go orchestrator's [HistoryGrabFinderForImport] caller doesn't
// abort the import on a lookup failure either, just skips the
// IndexerFlags / ReleaseType enrichment branches.
type historyGrabFinderForImportAdapter struct {
	svc *history.Service
}

// MostRecentGrabbed delegates to *history.Service.Find with the
// typed EventTypeGrabbed filter. Returns the first (most recent)
// matching entry, or (nil, nil) when none.
func (a historyGrabFinderForImportAdapter) MostRecentGrabbed(ctx context.Context, downloadID string) (*history.Entry, error) {
	if downloadID == "" {
		return nil, nil
	}
	rows, err := a.svc.Find(ctx, downloadID, history.EventTypeGrabbed)
	if err != nil {
		return nil, fmt.Errorf("history: MostRecentGrabbed(%q): %w", downloadID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// qualityComparerAdapter bridges *series.Service + *qualityprofiles.Service
// to the slice-3.71 [episodeimport.QualityComparer] narrow interface.
// Mirrors the C# `QualityModelComparer.Compare(left, right)` chain at
// `ImportApprovedEpisodes.cs:63` — each comparison resolves the
// per-series QualityProfile, parses Items, and delegates to
// [decisionengine.CompareQualityModels] with respectGroupOrder=false
// (the C# QualityModelComparer constructor's default — the
// upgrade-grouping path that passes true is the
// [decisionengine.UpgradableSpecification.IsRevisionUpgrade] arm,
// which doesn't go through this adapter).
//
// Defensive posture: any lookup/parse failure logs warn-level + returns
// 0 (treats as equal). This collapses to the noopQualityComparer
// behaviour that's been the default since slice 3.98 — the orchestrator's
// sortApproved falls back to Size-only ordering when the comparer
// reports equal qualities, which mirrors the C# behaviour when the
// profile lookup yields equal qualities (the C# QualityModelComparer
// throws on null profile inside Ensure.That but ImportApprovedEpisodes
// wraps the call in a try/catch that logs and continues with a
// degraded-but-stable ordering — same end-state as our defensive 0).
//
// Per-call DB hit posture matches the C# IoC default — neither
// series.Service nor qualityprofiles.Service caches Get(), and the C#
// equivalents go through to the DB on every comparison. Sorting an
// approved-decision batch fires at most O(n log n) comparisons; for
// typical batches (1-100 decisions per scan) the per-comparison hit
// is acceptable. A future slice may add a per-batch cache wrapper.
type qualityComparerAdapter struct {
	series *series.Service
	qp     *qualityprofiles.Service
	log    *slog.Logger
}

// Compare resolves the per-series QualityProfile, parses Items, and
// delegates to [decisionengine.CompareQualityModels]. Returns 0 on
// any lookup/parse failure (defensive; treats as equal so the
// orchestrator's sortApproved falls back to Size-only ordering).
func (a qualityComparerAdapter) Compare(seriesID int, left, right quality.Model) int {
	ser, err := a.series.GetSeries(context.Background(), seriesID)
	if err != nil || ser == nil {
		a.log.Warn("quality-comparer: series lookup failed; treating as equal",
			"seriesId", seriesID, "err", err)
		return 0
	}
	if ser.QualityProfileID == nil {
		a.log.Warn("quality-comparer: series has no QualityProfileID; treating as equal",
			"seriesId", seriesID)
		return 0
	}
	profile, err := a.qp.Get(context.Background(), *ser.QualityProfileID)
	if err != nil || profile == nil {
		a.log.Warn("quality-comparer: profile lookup failed; treating as equal",
			"seriesId", seriesID, "profileId", *ser.QualityProfileID, "err", err)
		return 0
	}
	items, err := profile.ParsedItems()
	if err != nil {
		a.log.Warn("quality-comparer: profile.ParsedItems failed; treating as equal",
			"seriesId", seriesID, "profileId", profile.ID, "err", err)
		return 0
	}
	return decisionengine.CompareQualityModels(items, left, right, false)
}

// Compile-time interface satisfaction guards. If a future deps.go
// edit changes a method signature, the build error surfaces here at
// the boot site rather than as a `*downloadedimport.Service`
// constructor failure at runtime.
var (
	_ downloadedimport.DiskOps                 = downloadedImportDiskOps{}
	_ downloadedimport.DiskScanner             = downloadedImportDiskScanner{}
	_ downloadedimport.SeriesPathChecker       = downloadedImportSeriesPaths{}
	_ downloadedimport.SeriesParser            = downloadedImportSeriesParser{}
	_ downloadedimport.ImportDecider           = downloadedImportDecider{}
	_ downloadedimport.ImportRunner            = downloadedImportRunner{}
	_ downloadedimport.SampleDetector          = downloadedImportSampleDetector{}
	_ episodeimport.VideoRuntimeReader         = noopVideoRuntimeReader{}
	_ episodeimport.EpisodeFileResolver        = episodeFileResolverAdapter{}
	_ episodeimport.EpisodeParsingService      = episodeParsingServiceAdapter{}
	_ episodeimport.FileNamer                  = fileNamerAdapter{}
	_ episodeimport.EpisodeTitleConfig         = episodeTitleConfigAdapter{}
	_ episodefilemoving.RootFolderFinder       = rootFolderFinderAdapter{}
	_ episodefilemoving.FileNameBuilder        = nameBuilderAdapter{}
	_ episodeimport.ImportEventPublisher       = importEventPublisherAdapter{}
	_ episodeimport.RescanSeriesCommander      = rescanSeriesCommanderAdapter{}
	_ episodeimport.HistoryGrabFinderForImport = historyGrabFinderForImportAdapter{}
	_ episodeimport.QualityComparer            = qualityComparerAdapter{}
)
