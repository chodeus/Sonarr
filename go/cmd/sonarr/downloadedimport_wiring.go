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

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodeimport"
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

// stubImportDecider returns no decisions. Mirrors the C#
// `MakeImportDecision.GetImportDecisions` behaviour when no candidate
// files match — the orchestrator receives an empty list and
// short-circuits via [downloadedimport.checkEmptyResultForIssue]
// (slice 3.79b).
//
// Replace when [episodeimport.ImportDecisionMaker] is constructed at
// the cmd/sonarr boot site. The constructor chain — Aggregator with
// per-augmenter wiring + EpisodeInfoParser + custom-format calculator
// + sample classifier — is heavier than this slice's scope.
type stubImportDecider struct{}

// GetImportDecisions always returns nil.
func (stubImportDecider) GetImportDecisions(
	_ []string,
	_ *series.Series,
	_ *downloadclients.Item,
	_ *parser.ParsedEpisodeInfo,
	_ *parser.ParsedEpisodeInfo,
	_ bool,
) []*episodeimport.ImportDecision {
	return nil
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

// stubSampleDetector always classifies as NotSample. The C#
// `IDetectSample.IsSample` would inspect the file's runtime against
// the expected episode runtime; until [episodeimport.SampleDetector]
// (slice 3.55) is wired with a real [episodeimport.VideoRuntimeReader]
// at boot, every candidate is treated as non-sample so it can flow
// through the ShouldDeleteFolder gate.
//
// `episodeimport.SampleDetector` itself is constructed via
// [episodeimport.NewSampleDetector(reader, log)]; replace this stub
// once a real `mediainfo`-backed VideoRuntimeReader is wired.
type stubSampleDetector struct{}

// IsSample always returns DetectSampleNotSample.
func (stubSampleDetector) IsSample(_ *series.Series, _ string, _ bool) episodeimport.DetectSampleResult {
	return episodeimport.DetectSampleNotSample
}

// Compile-time interface satisfaction guards. If a future deps.go
// edit changes a method signature, the build error surfaces here at
// the boot site rather than as a `*downloadedimport.Service`
// constructor failure at runtime.
var (
	_ downloadedimport.DiskOps           = downloadedImportDiskOps{}
	_ downloadedimport.DiskScanner       = downloadedImportDiskScanner{}
	_ downloadedimport.SeriesPathChecker = downloadedImportSeriesPaths{}
	_ downloadedimport.SeriesParser      = downloadedImportSeriesParser{}
	_ downloadedimport.ImportDecider     = stubImportDecider{}
	_ downloadedimport.ImportRunner      = stubImportRunner{}
	_ downloadedimport.SampleDetector    = stubSampleDetector{}
)
