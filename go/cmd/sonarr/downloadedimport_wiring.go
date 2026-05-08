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

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
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
)
