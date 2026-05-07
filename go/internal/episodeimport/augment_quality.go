package episodeimport

import (
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// LatestGrabFinder is the slice of `IDownloadHistoryService.GetLatestGrab`
// that [AugmentQualityFromReleaseName] reaches for at
// `AugmentQualityFromReleaseName.cs:29`. The full
// DownloadHistoryService is a downstream subsystem (event-handler-
// driven row writer) that hasn't ported yet — only the
// `downloadhistory.Repo` exists today.
//
// GetLatestGrab returns the most-recent `DownloadGrabbed` entry for
// the given DownloadID, or nil when no grab event exists. The
// production binding will compose the existing
// `downloadhistory.Repo.AllByDownloadID` (rows are returned date-
// descending) with a `DownloadHistoryEventType.DownloadGrabbed`
// filter; the wiring slice owns that composition. For tests, callers
// pass a stub that returns the expected entry directly.
type LatestGrabFinder interface {
	GetLatestGrab(downloadID string) *downloadhistory.Entry
}

// QualityConfidence ports the C# quality augmenter confidence enum at
// `Aggregation/Aggregators/Augmenters/Quality/Confidence.cs:3-9`.
//
// Numeric ordering matters: AggregateQuality picks the augmenter with
// the HIGHEST confidence for each component (Source / Resolution /
// Revision). Order is wire-stable so the orchestrator's
// confidence-tier comparisons don't shift under future augmenter
// additions.
type QualityConfidence int

// QualityConfidence ordinals. Order matches C# enum at `:5-8` exactly.
const (
	// QualityConfidenceFallback is the weakest tier — used when the
	// augmenter could only derive the value from a fallback path
	// (e.g. file extension when the parser couldn't tag a source).
	QualityConfidenceFallback QualityConfidence = 0

	// QualityConfidenceDefault is the initial sentinel before any
	// augmenter weighs in. Carries no real evidence.
	QualityConfidenceDefault QualityConfidence = 1

	// QualityConfidenceTag — the value came from a parsed tag in
	// the file/folder/release name.
	QualityConfidenceTag QualityConfidence = 2

	// QualityConfidenceMediaInfo — the value came from MediaInfo
	// reading the actual file. Highest tier; overrides everything.
	QualityConfidenceMediaInfo QualityConfidence = 3
)

// String returns the C# enum-identifier form for log lines.
func (c QualityConfidence) String() string {
	switch c {
	case QualityConfidenceFallback:
		return "Fallback"
	case QualityConfidenceDefault:
		return "Default"
	case QualityConfidenceTag:
		return "Tag"
	case QualityConfidenceMediaInfo:
		return "MediaInfo"
	}
	return "QualityConfidence(?)"
}

// AugmentQualityResult is the per-augmenter output. Mirrors
// `Aggregators/Augmenters/Quality/AugmentQualityResult.cs:5-44`.
//
// Source / Resolution / Revision are independent components — an
// augmenter may set zero, one, or all three. Revision==nil signals
// "no opinion on revision" so the aggregator's revision-merge path
// can keep its current pick.
type AugmentQualityResult struct {
	Name                 string
	Source               quality.QualitySource
	SourceConfidence     QualityConfidence
	Resolution           int
	ResolutionConfidence QualityConfidence
	Revision             *quality.Revision
	RevisionConfidence   QualityConfidence
}

// NewAugmentQualityResult is the explicit-component constructor.
// Mirrors the C# 6-arg ctor at `:18-30`.
func NewAugmentQualityResult(source quality.QualitySource, sourceConfidence QualityConfidence,
	resolution int, resolutionConfidence QualityConfidence,
	revision *quality.Revision, revisionConfidence QualityConfidence,
) *AugmentQualityResult {
	return &AugmentQualityResult{
		Source: source, SourceConfidence: sourceConfidence,
		Resolution: resolution, ResolutionConfidence: resolutionConfidence,
		Revision: revision, RevisionConfidence: revisionConfidence,
	}
}

// AugmentQualitySourceOnly mirrors C# `AugmentQualityResult.SourceOnly`
// at `:32-35`. Resolution=0/Default, Revision=nil/Default.
func AugmentQualitySourceOnly(source quality.QualitySource, sourceConfidence QualityConfidence) *AugmentQualityResult {
	return NewAugmentQualityResult(source, sourceConfidence, 0, QualityConfidenceDefault, nil, QualityConfidenceDefault)
}

// AugmentQualityResolutionOnly mirrors C#
// `AugmentQualityResult.ResolutionOnly` at `:37-40`.
// Source=Unknown/Default, Revision=nil/Default.
func AugmentQualityResolutionOnly(resolution int, resolutionConfidence QualityConfidence) *AugmentQualityResult {
	return NewAugmentQualityResult(quality.SourceUnknown, QualityConfidenceDefault, resolution, resolutionConfidence, nil, QualityConfidenceDefault)
}

// AugmentQualitySourceAndResolution mirrors C#
// `AugmentQualityResult.SourceAndResolutionOnly` at `:42-45`.
// Revision=nil/Default.
func AugmentQualitySourceAndResolution(source quality.QualitySource, sourceConfidence QualityConfidence, resolution int, resolutionConfidence QualityConfidence) *AugmentQualityResult {
	return NewAugmentQualityResult(source, sourceConfidence, resolution, resolutionConfidence, nil, QualityConfidenceDefault)
}

// QualityAugmenter ports `IAugmentQuality` at
// `Aggregators/Augmenters/Quality/IAugmentQuality.cs:6-11`.
//
// The aggregator runs every registered augmenter in Order ascending
// and merges the results component-by-component picking the highest
// confidence per component. Returning nil signals "this augmenter
// has no opinion" — the aggregator skips the merge and moves on.
type QualityAugmenter interface {
	// Order is the deterministic walk order. Lower runs first; ties
	// preserve registration order.
	Order() int

	// AugmenterName mirrors C# `IAugmentQuality.Name` at `:9` — the
	// short label used in trace log lines. Renamed to AugmenterName
	// so it doesn't collide with the [Specification.Name] interface
	// method on types that satisfy both contracts.
	AugmenterName() string

	// AugmentQuality returns the augmenter's verdict, or nil if the
	// augmenter has no opinion for this candidate.
	AugmentQuality(le *LocalEpisode, item *downloadclients.Item) *AugmentQualityResult
}

// augmentQualityFromParsedInfo is the shared body of the three
// "augment from a ParsedEpisodeInfo source" augmenters (FileName,
// Folder, DownloadClientItem). The C# tree has three classes with
// identical bodies modulo the field they read from; the Go port
// collapses them to one helper plus three thin wrapper types.
//
// info is the source-of-truth ParsedEpisodeInfo (one of
// LocalEpisode.FileEpisodeInfo / FolderEpisodeInfo /
// DownloadClientEpisodeInfo). Returns nil when info is nil OR when
// info.Quality is the zero value (matches C# `quality == null` at
// `:13-17`).
//
// The three confidence tiers (Source / Resolution / Revision) each
// promote to Tag when the underlying detection-source was Name (parsed
// out of the title) and Fallback otherwise — same shape across the
// three C# classes.
func augmentQualityFromParsedInfo(info *parser.ParsedEpisodeInfo) *AugmentQualityResult {
	if info == nil || isZeroQualityModel(info.Quality) {
		return nil
	}
	q := info.Quality
	revision := q.Revision
	return &AugmentQualityResult{
		Source:               q.Quality.Source,
		SourceConfidence:     tagOrFallback(q.SourceDetectionSource),
		Resolution:           q.Quality.Resolution,
		ResolutionConfidence: tagOrFallback(q.ResolutionDetectionSource),
		Revision:             &revision,
		RevisionConfidence:   tagOrFallback(q.RevisionDetectionSource),
	}
}

// isZeroQualityModel detects "no quality opinion" — used by the three
// parsed-info augmenters to skip when the source row carries a
// default-zero Model. The C# tree relies on `quality == null` at the
// boundary; Go can't store nil in a value field, so we check the
// constituent zero-value instead.
func isZeroQualityModel(m quality.Model) bool {
	return m.Quality.ID == 0 && m.Quality.Source == quality.SourceUnknown &&
		m.Quality.Resolution == 0 && m.Revision == (quality.Revision{})
}

// tagOrFallback maps the detection-source to the augmenter's
// confidence tier. Only `DetectionName` promotes to Tag; everything
// else (Unknown / Extension / MediaInfo) is Fallback at the parsed-info
// boundary because those tiers are owned by other augmenters.
func tagOrFallback(s quality.DetectionSource) QualityConfidence {
	if s == quality.DetectionName {
		return QualityConfidenceTag
	}
	return QualityConfidenceFallback
}

// AugmentQualityFromFileName ports
// `AugmentQualityFromFileName.cs:7-41`. Reads
// LocalEpisode.FileEpisodeInfo.Quality.
type AugmentQualityFromFileName struct{}

// Order returns 1 — the C# value at `:9`.
func (AugmentQualityFromFileName) Order() int { return 1 }

// AugmenterName returns `"FileName"` per C# `:10`.
func (AugmentQualityFromFileName) AugmenterName() string { return "FileName" }

// AugmentQuality dispatches to [augmentQualityFromParsedInfo].
func (AugmentQualityFromFileName) AugmentQuality(le *LocalEpisode, _ *downloadclients.Item) *AugmentQualityResult {
	if le == nil {
		return nil
	}
	return augmentQualityFromParsedInfo(le.FileEpisodeInfo)
}

// AugmentQualityFromFolder ports
// `AugmentQualityFromFolder.cs:7-41`. Reads
// LocalEpisode.FolderEpisodeInfo.Quality.
type AugmentQualityFromFolder struct{}

// Order returns 2 — the C# value at `:9`.
func (AugmentQualityFromFolder) Order() int { return 2 }

// AugmenterName returns `"FolderName"` per C# `:10`.
func (AugmentQualityFromFolder) AugmenterName() string { return "FolderName" }

// AugmentQuality dispatches to [augmentQualityFromParsedInfo].
func (AugmentQualityFromFolder) AugmentQuality(le *LocalEpisode, _ *downloadclients.Item) *AugmentQualityResult {
	if le == nil {
		return nil
	}
	return augmentQualityFromParsedInfo(le.FolderEpisodeInfo)
}

// AugmentQualityFromDownloadClientItem ports
// `AugmentQualityFromDownloadClientItem.cs:7-41`. Reads
// LocalEpisode.DownloadClientEpisodeInfo.Quality.
type AugmentQualityFromDownloadClientItem struct{}

// Order returns 3 — the C# value at `:9`.
func (AugmentQualityFromDownloadClientItem) Order() int { return 3 }

// AugmenterName returns `"DownloadClientItem"` per C# `:10`.
func (AugmentQualityFromDownloadClientItem) AugmenterName() string { return "DownloadClientItem" }

// AugmentQuality dispatches to [augmentQualityFromParsedInfo].
func (AugmentQualityFromDownloadClientItem) AugmentQuality(le *LocalEpisode, _ *downloadclients.Item) *AugmentQualityResult {
	if le == nil {
		return nil
	}
	return augmentQualityFromParsedInfo(le.DownloadClientEpisodeInfo)
}

// AugmentQualityFromMediaInfo ports
// `AugmentQualityFromMediaInfo.cs:9-85`.
//
// Two-step: (1) parse the MediaInfo Title field for a source tag
// (only honoured when the parse pulled it from the Name detection
// source — MediaInfo title is treated like a release-tag string); (2)
// classify resolution from MediaInfo.Width × Height into the standard
// 480p / 576p / 720p / 1080p / 2160p buckets, all at MediaInfo
// confidence.
//
// Bucket thresholds match C# `:62-83` exactly:
//
//   - W ≥ 3200 OR H ≥ 2100 → 2160p
//   - W ≥ 1800 OR H ≥ 1000 → 1080p
//   - W ≥ 1200 OR H ≥ 700  → 720p
//   - W ≥ 1000 OR H ≥ 560  → 576p
//   - W > 0    OR H > 0    → 480p
type AugmentQualityFromMediaInfo struct {
	log *slog.Logger

	// QualityNameParser is the title-parsing dep. Pulled out as a
	// narrow function-typed field so the augmenter is testable
	// without dragging in the full quality.ParseQualityName ceremony.
	// Production wiring binds quality.ParseQualityName directly.
	QualityNameParser func(name string) quality.Model
}

// NewAugmentQualityFromMediaInfo binds a logger + a name-parser.
// nil parser defaults to [quality.ParseQualityName].
func NewAugmentQualityFromMediaInfo(log *slog.Logger, parser func(string) quality.Model) *AugmentQualityFromMediaInfo {
	if log == nil {
		log = slog.Default()
	}
	if parser == nil {
		parser = quality.ParseQualityName
	}
	return &AugmentQualityFromMediaInfo{log: log, QualityNameParser: parser}
}

// Order returns 4 — the C# value at `:13`.
func (a *AugmentQualityFromMediaInfo) Order() int { return 4 }

// AugmenterName returns `"MediaInfo"` per C# `:14`.
func (a *AugmentQualityFromMediaInfo) AugmenterName() string { return "MediaInfo" }

// AugmentQuality classifies the file's resolution + (optionally) its
// declared source from the MediaInfo Title.
func (a *AugmentQualityFromMediaInfo) AugmentQuality(le *LocalEpisode, _ *downloadclients.Item) *AugmentQualityResult {
	if le == nil || le.MediaInfo == nil {
		return nil
	}
	width, height := le.MediaInfo.Width, le.MediaInfo.Height
	source := quality.SourceUnknown
	sourceConfidence := QualityConfidenceDefault
	title := strings.TrimSpace(le.MediaInfo.Title)
	if title != "" {
		parsed := a.QualityNameParser(title)
		if parsed.Quality.Source != quality.SourceUnknown && parsed.SourceDetectionSource == quality.DetectionName {
			source = parsed.Quality.Source
			sourceConfidence = QualityConfidenceMediaInfo
		}
	}

	switch {
	case width >= 3200 || height >= 2100:
		a.log.Debug("episodeimport: MediaInfo resolution classified", "w", width, "h", height, "bucket", 2160)
		return AugmentQualitySourceAndResolution(source, sourceConfidence, 2160, QualityConfidenceMediaInfo)
	case width >= 1800 || height >= 1000:
		return AugmentQualitySourceAndResolution(source, sourceConfidence, 1080, QualityConfidenceMediaInfo)
	case width >= 1200 || height >= 700:
		return AugmentQualitySourceAndResolution(source, sourceConfidence, 720, QualityConfidenceMediaInfo)
	case width >= 1000 || height >= 560:
		return AugmentQualitySourceAndResolution(source, sourceConfidence, 576, QualityConfidenceMediaInfo)
	case width > 0 || height > 0:
		return AugmentQualitySourceAndResolution(source, sourceConfidence, 480, QualityConfidenceMediaInfo)
	}
	return nil
}

// AugmentQualityFromReleaseName ports
// `Aggregators/Augmenters/Quality/AugmentQualityFromReleaseName.cs:9-53`.
//
// Looks up the latest DownloadGrabbed history entry for the download
// item and parses the persisted `SourceTitle` (the on-wire release
// title) for quality information. The release name is treated as the
// most authoritative external source — when the parsed quality
// reports DetectionName for a component, the augmenter promotes that
// component to Tag confidence; otherwise Fallback (matching C#
// `:38-48`'s ternary triplet).
//
// Skip arms (matching C# `:23-34`):
//
//   - nil download item — manual / library rescan flow.
//   - empty download ID — defensive, treated like the C# null arm.
//   - finder returns nil — no grab history for this download.
//
// Order is 5 — the C# value at `:11` — placing this after the four
// Order=1..4 augmenters (FileName / Folder / DownloadClientItem /
// MediaInfo) so the release-name evidence is the LAST per-component
// vote. Combined with [AggregateQuality]'s "strictly-higher
// confidence wins, equal-confidence revision tiebreak" merge, this
// means a Tag-confidence release-name parse displaces an earlier
// Fallback-confidence file-name parse, which matches C# behaviour.
type AugmentQualityFromReleaseName struct {
	finder LatestGrabFinder

	// QualityParser is the title-parsing dep. nil defaults to
	// [quality.ParseQuality]. Pulled out as a function-typed field
	// for the same reason [AugmentQualityFromMediaInfo].QualityNameParser
	// is — testability without dragging the parser through the call.
	QualityParser func(name string) quality.Model
}

// NewAugmentQualityFromReleaseName binds the latest-grab finder and
// optional parser. nil parser defaults to [quality.ParseQuality].
// nil finder returns nil — production wiring MUST pass a real
// implementation.
func NewAugmentQualityFromReleaseName(finder LatestGrabFinder, parserFn func(string) quality.Model) *AugmentQualityFromReleaseName {
	if finder == nil {
		return nil
	}
	if parserFn == nil {
		parserFn = quality.ParseQuality
	}
	return &AugmentQualityFromReleaseName{finder: finder, QualityParser: parserFn}
}

// Order returns 5 — the C# value at `AugmentQualityFromReleaseName.cs:11`.
func (*AugmentQualityFromReleaseName) Order() int { return 5 }

// AugmenterName returns `"ReleaseName"` per C# `:12`.
func (*AugmentQualityFromReleaseName) AugmenterName() string { return "ReleaseName" }

// AugmentQuality runs the parse-and-promote pipeline.
func (a *AugmentQualityFromReleaseName) AugmentQuality(_ *LocalEpisode, item *downloadclients.Item) *AugmentQualityResult {
	if a == nil || item == nil || item.DownloadID == "" {
		return nil
	}
	entry := a.finder.GetLatestGrab(item.DownloadID)
	if entry == nil {
		return nil
	}
	parsed := a.QualityParser(entry.SourceTitle)
	revision := parsed.Revision
	return &AugmentQualityResult{
		Name:                 "ReleaseName",
		Source:               parsed.Quality.Source,
		SourceConfidence:     tagOrFallback(parsed.SourceDetectionSource),
		Resolution:           parsed.Quality.Resolution,
		ResolutionConfidence: tagOrFallback(parsed.ResolutionDetectionSource),
		Revision:             &revision,
		RevisionConfidence:   tagOrFallback(parsed.RevisionDetectionSource),
	}
}

// Compile-time interface-satisfaction guards.
var (
	_ QualityAugmenter = AugmentQualityFromFileName{}
	_ QualityAugmenter = AugmentQualityFromFolder{}
	_ QualityAugmenter = AugmentQualityFromDownloadClientItem{}
	_ QualityAugmenter = (*AugmentQualityFromMediaInfo)(nil)
	_ QualityAugmenter = (*AugmentQualityFromReleaseName)(nil)
)
