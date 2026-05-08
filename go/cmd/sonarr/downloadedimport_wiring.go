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
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
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

// stubImportRunner accepts any decision list as a no-op. Mirrors the
// C# `IImportApprovedEpisodes.Import` behaviour when handed an empty
// list — returns an empty result slice without touching disk.
//
// Replace when [episodeimport.ImportApprovedEpisodes] (slice 3.71) is
// constructed at the cmd/sonarr boot site. Its dep chain is the
// writer/upgrade/extras/history/quality-comparer/event-publisher/
// rescan-commander stack; constructing all of those is its own
// follow-up wiring slice.
type stubImportRunner struct{}

// Import always returns nil.
func (stubImportRunner) Import(
	_ []*episodeimport.ImportDecision,
	_ bool,
	_ *downloadclients.Item,
	_ episodeimport.ImportMode,
) []*episodeimport.ImportResult {
	return nil
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

// Compile-time interface satisfaction guards. If a future deps.go
// edit changes a method signature, the build error surfaces here at
// the boot site rather than as a `*downloadedimport.Service`
// constructor failure at runtime.
var (
	_ downloadedimport.DiskOps            = downloadedImportDiskOps{}
	_ downloadedimport.DiskScanner        = downloadedImportDiskScanner{}
	_ downloadedimport.SeriesPathChecker  = downloadedImportSeriesPaths{}
	_ downloadedimport.SeriesParser       = downloadedImportSeriesParser{}
	_ downloadedimport.ImportDecider      = downloadedImportDecider{}
	_ downloadedimport.ImportRunner       = stubImportRunner{}
	_ downloadedimport.SampleDetector     = downloadedImportSampleDetector{}
	_ episodeimport.VideoRuntimeReader    = noopVideoRuntimeReader{}
	_ episodeimport.EpisodeFileResolver   = episodeFileResolverAdapter{}
	_ episodeimport.EpisodeParsingService = episodeParsingServiceAdapter{}
	_ episodeimport.FileNamer             = fileNamerAdapter{}
	_ episodeimport.EpisodeTitleConfig    = episodeTitleConfigAdapter{}
	_ episodefilemoving.RootFolderFinder  = rootFolderFinderAdapter{}
	_ episodefilemoving.FileNameBuilder   = nameBuilderAdapter{}
)
