package mediafiles

import (
	"sonarr.tv/sonarr/internal/series"
)

// SeriesScannedEvent ports `MediaFiles/Events/SeriesScannedEvent.cs:1-18`.
// Published at the tail of `DiskScanService.Scan` (`:199`) once the
// scan completes successfully — both the happy path and the
// missing-folder branch publish, with `PossibleExtraFiles` empty in the
// missing-folder case.
//
// `PossibleExtraFiles` is the post-FilterPaths list of non-video files
// under `series.Path` (subtitles, NFOs, fanart, ...). Downstream
// listeners (extras-import, metadata-import) consume the list to
// classify each entry.
type SeriesScannedEvent struct {
	Series             *series.Series
	PossibleExtraFiles []string
}

// SeriesScanSkippedEvent ports `MediaFiles/Events/SeriesScanSkippedEvent.cs:1-17`.
// Published when `DiskScanService.Scan` short-circuits before doing any
// disk work — the two reasons the C# scan emits are
// [SeriesScanSkippedReasonRootFolderDoesNotExist] and
// [SeriesScanSkippedReasonRootFolderIsEmpty]. The other two enum
// entries below are referenced elsewhere in the C# tree (the
// SeriesScanService config option lives on `IConfigService`, not on
// DiskScanService itself) but are part of the public reason enum so
// the Go port carries all four for completeness — slice 5's handler
// surface ports the interplay.
type SeriesScanSkippedEvent struct {
	Series *series.Series
	Reason SeriesScanSkippedReason
}

// SeriesScanSkippedReason enumerates why a series scan was skipped.
// Mirrors the C# enum at
// `MediaFiles/Events/SeriesScanSkippedEvent.cs:18-24` verbatim — names
// and order pin to the C# tree so a future drift is loud.
type SeriesScanSkippedReason int

// SeriesScanSkippedReason constants. Numeric order matches the C# enum
// declaration order (declared without explicit values, so 0..3).
const (
	// SeriesScanSkippedReasonRootFolderDoesNotExist mirrors the C#
	// `RootFolderDoesNotExist` entry. Published when the series'
	// folder is missing AND the root folder it lives under is also
	// missing (`DiskScanService.cs:85-90`).
	SeriesScanSkippedReasonRootFolderDoesNotExist SeriesScanSkippedReason = iota

	// SeriesScanSkippedReasonRootFolderIsEmpty mirrors the C#
	// `RootFolderIsEmpty` entry. Published when the series folder is
	// missing, the root folder exists, but the root folder is empty —
	// the heuristic against scanning into a not-yet-mounted disk
	// (`DiskScanService.cs:92-96`).
	SeriesScanSkippedReasonRootFolderIsEmpty

	// SeriesScanSkippedReasonNeverRescanAfterRefresh mirrors the C#
	// `NeverRescanAfterRefresh` entry. Not published by DiskScanService
	// itself — referenced in the public enum because the wider rescan-
	// gating logic (slice 5 territory) consumes it.
	SeriesScanSkippedReasonNeverRescanAfterRefresh

	// SeriesScanSkippedReasonRescanAfterManualRefreshOnly mirrors the
	// C# `RescanAfterManualRefreshOnly` entry. Not published by
	// DiskScanService itself; same situation as
	// [SeriesScanSkippedReasonNeverRescanAfterRefresh].
	SeriesScanSkippedReasonRescanAfterManualRefreshOnly
)

// String returns the canonical name for each reason — same casing as
// the C# enum so structured-log readers see the same token in both
// trees. Used by stringer-friendly slog rendering and test-assertion
// failure messages.
func (r SeriesScanSkippedReason) String() string {
	switch r {
	case SeriesScanSkippedReasonRootFolderDoesNotExist:
		return "RootFolderDoesNotExist"
	case SeriesScanSkippedReasonRootFolderIsEmpty:
		return "RootFolderIsEmpty"
	case SeriesScanSkippedReasonNeverRescanAfterRefresh:
		return "NeverRescanAfterRefresh"
	case SeriesScanSkippedReasonRescanAfterManualRefreshOnly:
		return "RescanAfterManualRefreshOnly"
	default:
		return "Unknown"
	}
}
