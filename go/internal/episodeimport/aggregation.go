package episodeimport

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// ErrAugmentingFailed mirrors the C# `AugmentingFailedException` at
// `Aggregation/AggregationFailedException.cs:5-25`. Wrapped via
// `fmt.Errorf("...: %w", ErrAugmentingFailed)` at the unparseable-
// media-file boundary so callers (the orchestrator slice 7) classify
// via `errors.Is`.
var ErrAugmentingFailed = errors.New("episodeimport: augmenting failed")

// LocalEpisodeAggregator ports `IAggregateLocalEpisode` at
// `Aggregation/Aggregators/IAggregateLocalEpisode.cs:6-10`.
//
// Each aggregator transforms a LocalEpisode in place — fills in one
// or more derived fields (Episodes, Languages, Quality, ReleaseGroup,
// ReleaseHash, Release, SubtitleInfo). The service walks them in
// Order ascending; aggregators with the same Order preserve
// registration order.
//
// Aggregators MUST be safe to call with a partially-filled
// LocalEpisode — earlier aggregators may not have run when this one
// fires (multi-rejection paths still iterate every aggregator under
// a try/catch, matching the C# loop at
// `AggregationService.cs:67-80`). Returning the same LocalEpisode
// pointer is the convention; the value is mutated in place.
type LocalEpisodeAggregator interface {
	// Order is the deterministic walk order. Lower runs first.
	Order() int

	// Aggregate mutates and returns the LocalEpisode. Returning a
	// different pointer is allowed but unconventional; the service
	// uses the return value for any subsequent dispatch.
	Aggregate(le *LocalEpisode, item *downloadclients.Item) *LocalEpisode
}

// FileSizeReader is the slice of [disk.Provider] the service reaches
// for at `AggregationService.cs:53` (`GetFileSize`).
type FileSizeReader interface {
	GetFileSize(path string) (int64, error)
}

// VideoMediaInfoReader is the slice of `IVideoFileInfoReader.GetMediaInfo`
// the service reaches for at `:58`. Production wiring binds the real
// FFprobe-based reader once it ports; tests inject a stub.
//
// Returning nil is a valid "couldn't read" signal — the service's
// MediaInfo arm is best-effort and should not block the rest of the
// augmentation pipeline.
type VideoMediaInfoReader interface {
	GetMediaInfo(path string) (*mediainfo.Model, error)
}

// AggregationConfig is the slice of [config.Provider] the service
// reaches for at `:57` (`EnableMediaInfo`).
type AggregationConfig interface {
	EnableMediaInfo() bool
}

// AggregationService ports `AggregationService` at
// `Aggregation/AggregationService.cs:18-79`.
//
// Augment is the public entry point: takes a partially-filled
// LocalEpisode + optional download-client item, fills size + scene
// name + media info, then walks every registered aggregator. Per-
// aggregator panics / errors are recovered with a Warn log so a
// single buggy aggregator can't poison the batch. (Go's recover
// pattern replaces the C# try/catch surrounding the foreach at
// `:67-80`.)
type AggregationService struct {
	aggregators []LocalEpisodeAggregator
	disk        FileSizeReader
	video       VideoMediaInfoReader
	cfg         AggregationConfig
	log         *slog.Logger
}

// NewAggregationService binds a list of aggregators (sorted by Order
// ascending — caller may pass any order; the constructor sorts) plus
// the disk / video / config deps.
//
// disk is required (size is mandatory before specs run). video and
// cfg may be nil — when video is nil, the MediaInfo arm is skipped;
// when cfg is nil the service treats EnableMediaInfo as true (matching
// the C# default at `ConfigService.cs:218`).
func NewAggregationService(
	aggregators []LocalEpisodeAggregator,
	disk FileSizeReader,
	video VideoMediaInfoReader,
	cfg AggregationConfig,
	log *slog.Logger,
) (*AggregationService, error) {
	if disk == nil {
		return nil, errors.New("episodeimport: NewAggregationService: nil FileSizeReader")
	}
	if log == nil {
		log = slog.Default()
	}
	sorted := slices.Clone(aggregators)
	slices.SortStableFunc(sorted, func(a, b LocalEpisodeAggregator) int {
		return a.Order() - b.Order()
	})
	return &AggregationService{
		aggregators: sorted,
		disk:        disk,
		video:       video,
		cfg:         cfg,
		log:         log,
	}, nil
}

// Augment mirrors C# `:42-78`. Steps (in order):
//
//  1. Pre-check: if the candidate is a media-file extension AND no
//     parsed-episode-info source is set, fail with
//     [ErrAugmentingFailed] (matches the C# throw at `:51`).
//  2. Fill `Size` from disk.GetFileSize.
//  3. Fill `SceneName` via [GetSceneName] when SceneSource=true.
//  4. If a media file AND (!ExistingFile OR cfg.EnableMediaInfo()),
//     fill `MediaInfo` from video.GetMediaInfo.
//  5. Walk every registered aggregator under a panic guard.
//
// Disk / video errors at steps 2-4 are logged-and-swallowed — the
// orchestrator's downstream specs handle a zero-Size / nil-MediaInfo
// candidate defensively (per the existing
// HasAudioTrackSpecification's nil-MediaInfo accept, etc.).
func (s *AggregationService) Augment(le *LocalEpisode, item *downloadclients.Item) (*LocalEpisode, error) {
	if le == nil {
		return nil, errors.New("episodeimport: AggregationService.Augment: nil LocalEpisode")
	}
	ext := strings.ToLower(filepath.Ext(le.Path))
	isMediaFile := mediafiles.IsMediaExtension(ext)

	if le.DownloadClientEpisodeInfo == nil &&
		le.FolderEpisodeInfo == nil &&
		le.FileEpisodeInfo == nil &&
		isMediaFile {
		return nil, fmt.Errorf("%w: unable to parse episode info from path: %s",
			ErrAugmentingFailed, le.Path)
	}

	if size, err := s.disk.GetFileSize(le.Path); err != nil {
		s.log.Debug("episodeimport: GetFileSize failed; leaving Size=0",
			"path", le.Path, "err", err)
	} else {
		le.Size = size
	}

	if le.SceneSource {
		le.SceneName = GetSceneName(le)
	}

	if isMediaFile && s.video != nil && (!le.ExistingFile || s.cfgEnableMediaInfo()) {
		mi, err := s.video.GetMediaInfo(le.Path)
		if err != nil {
			s.log.Debug("episodeimport: GetMediaInfo failed; leaving MediaInfo=nil",
				"path", le.Path, "err", err)
		} else {
			le.MediaInfo = mi
		}
	}

	for _, ag := range s.aggregators {
		s.runAggregator(ag, le, item)
	}
	return le, nil
}

// cfgEnableMediaInfo returns the EnableMediaInfo config value with
// nil-cfg defaulting to true (matches C# `ConfigService.cs:218`).
func (s *AggregationService) cfgEnableMediaInfo() bool {
	if s.cfg == nil {
		return true
	}
	return s.cfg.EnableMediaInfo()
}

// runAggregator wraps a per-aggregator dispatch with a panic guard.
// Mirrors the C# try/catch at `:67-80` — a buggy aggregator must not
// poison the batch.
func (s *AggregationService) runAggregator(ag LocalEpisodeAggregator, le *LocalEpisode, item *downloadclients.Item) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Warn("episodeimport: aggregator panicked; skipping",
				"path", le.Path, "panic", r)
		}
	}()
	ag.Aggregate(le, item)
}

// AggregateReleaseGroup ports
// `Aggregators/AggregateReleaseGroup.cs:7-49`.
//
// Five-priority pick (first non-empty wins):
//
//  1. DownloadClientEpisodeInfo (skip if FullSeason — season-pack
//     groups are too coarse).
//  2. FolderEpisodeInfo (skip if FullSeason).
//  3. FileEpisodeInfo (no FullSeason skip — file-level data trumps).
//  4. DownloadClientEpisodeInfo (no FullSeason skip — fallback when
//     file parse missed it).
//  5. FolderEpisodeInfo (no FullSeason skip — last fallback).
type AggregateReleaseGroup struct{}

// Order returns 1 — the C# value at `:9`.
func (AggregateReleaseGroup) Order() int { return 1 }

// Aggregate fills LocalEpisode.ReleaseGroup.
func (AggregateReleaseGroup) Aggregate(le *LocalEpisode, _ *downloadclients.Item) *LocalEpisode {
	if le == nil {
		return le
	}
	pick := func(info *parser.ParsedEpisodeInfo, skipFullSeason bool) string {
		if info == nil {
			return ""
		}
		if info.FullSeason && skipFullSeason {
			return ""
		}
		return info.ReleaseGroup
	}
	if rg := pick(le.DownloadClientEpisodeInfo, true); rg != "" {
		le.ReleaseGroup = rg
		return le
	}
	if rg := pick(le.FolderEpisodeInfo, true); rg != "" {
		le.ReleaseGroup = rg
		return le
	}
	if rg := pick(le.FileEpisodeInfo, false); rg != "" {
		le.ReleaseGroup = rg
		return le
	}
	if rg := pick(le.DownloadClientEpisodeInfo, false); rg != "" {
		le.ReleaseGroup = rg
		return le
	}
	if rg := pick(le.FolderEpisodeInfo, false); rg != "" {
		le.ReleaseGroup = rg
		return le
	}
	le.ReleaseGroup = ""
	return le
}

// AggregateReleaseHash ports
// `Aggregators/AggregateReleaseHash.cs:7-37`.
//
// Three-priority pick (first non-empty wins). Hashes are file-level
// identifiers; FullSeason rows are always skipped (a hash for a
// 26-episode pack doesn't identify any single file).
type AggregateReleaseHash struct{}

// Order returns 1 — the C# value at `:9`.
func (AggregateReleaseHash) Order() int { return 1 }

// Aggregate fills LocalEpisode.ReleaseHash.
func (AggregateReleaseHash) Aggregate(le *LocalEpisode, _ *downloadclients.Item) *LocalEpisode {
	if le == nil {
		return le
	}
	pick := func(info *parser.ParsedEpisodeInfo) string {
		if info == nil || info.FullSeason {
			return ""
		}
		return info.ReleaseHash
	}
	for _, info := range []*parser.ParsedEpisodeInfo{le.FileEpisodeInfo, le.DownloadClientEpisodeInfo, le.FolderEpisodeInfo} {
		if h := pick(info); h != "" {
			le.ReleaseHash = h
			return le
		}
	}
	le.ReleaseHash = ""
	return le
}

// AggregateLanguage ports
// `Aggregators/AggregateLanguage.cs:9-58`.
//
// Walks every registered [LanguageAugmenter] in Order ascending and
// keeps the highest-confidence non-trivial result. "Trivial" =
// empty list OR a single Unknown — those are treated as no-opinion
// at C# `:35-39`.
//
// Initial pick is `[Series.OriginalLanguage ?? Unknown]` at
// LanguageConfidenceDefault (C# `:23`). The Go-port Series carries
// OriginalLanguage as an int; the constructor takes an injectable
// resolver `func(int) language.Language` so tests pin the lookup
// without depending on the language registry.
type AggregateLanguage struct {
	augmenters       []LanguageAugmenter
	originalLanguage func(id int) language.Language
	log              *slog.Logger
}

// NewAggregateLanguage binds the augmenter list. originalLanguage
// resolves a series.Series.OriginalLanguage int into a language.Language
// — nil defaults to a closure over [language.FindByID] returning
// [language.Unknown] on miss.
func NewAggregateLanguage(augs []LanguageAugmenter, originalLanguage func(int) language.Language, log *slog.Logger) *AggregateLanguage {
	if log == nil {
		log = slog.Default()
	}
	if originalLanguage == nil {
		originalLanguage = func(id int) language.Language {
			l, ok := language.FindByID(id)
			if !ok {
				return language.Unknown
			}
			return l
		}
	}
	sorted := slices.Clone(augs)
	slices.SortStableFunc(sorted, func(a, b LanguageAugmenter) int {
		return a.Order() - b.Order()
	})
	return &AggregateLanguage{augmenters: sorted, originalLanguage: originalLanguage, log: log}
}

// Order returns 1 — the C# value at `:11`.
func (a *AggregateLanguage) Order() int { return 1 }

// Aggregate walks every augmenter, picking the highest-confidence
// non-trivial result.
func (a *AggregateLanguage) Aggregate(le *LocalEpisode, item *downloadclients.Item) *LocalEpisode {
	if le == nil {
		return le
	}
	var initial language.Language
	if le.Series != nil {
		initial = a.originalLanguage(le.Series.OriginalLanguage)
	} else {
		initial = language.Unknown
	}
	languages := []language.Language{initial}
	confidence := LanguageConfidenceDefault

	for _, aug := range a.augmenters {
		result := aug.AugmentLanguage(le, item)
		if result == nil {
			continue
		}
		// C# `:35-39`: skip empty list AND skip the
		// `[Unknown]` singleton — those are treated as no-opinion.
		if len(result.Languages) == 0 {
			continue
		}
		if len(result.Languages) == 1 && result.Languages[0] == language.Unknown {
			continue
		}
		// C# `:33-37` REPLACES the running list when the augmenter
		// has any non-trivial verdict — confidence ordering doesn't
		// gate replacement (the strict-ascending Order array
		// already does the gating). Mirror exactly.
		languages = result.Languages
		confidence = result.Confidence
	}
	a.log.Debug("episodeimport: AggregateLanguage selected", "path", le.Path, "languages", languages, "confidence", confidence)
	le.Languages = languages
	return le
}

// AggregateQuality ports
// `Aggregators/AggregateQuality.cs:11-114`.
//
// Walks every registered [QualityAugmenter] and merges the Source /
// Resolution / Revision components independently. Per-component
// merge picks the augmenter with the strictly higher confidence —
// ties keep the previously-selected value (revision has a tiebreak
// "higher revision wins on equal confidence" arm at C# `:64-69`).
//
// Final Quality is resolved via [quality.FindBySourceAndResolution]
// against the merged Source + Resolution. DetectionSource fields
// echo the merge confidences:
//
//   - resolutionConfidence == MediaInfo → ResolutionDetectionMediaInfo.
//   - resolutionConfidence == Fallback  → ResolutionDetectionExtension.
//   - else                              → ResolutionDetectionName.
//   - sourceConfidence     == Fallback  → SourceDetectionExtension.
//   - else                              → SourceDetectionName.
//   - revisionConfidence   == Tag       → RevisionDetectionName.
//   - else                              → RevisionDetectionUnknown.
type AggregateQuality struct {
	augmenters []QualityAugmenter
	log        *slog.Logger
}

// NewAggregateQuality binds the augmenter list (sorted by Order).
func NewAggregateQuality(augs []QualityAugmenter, log *slog.Logger) *AggregateQuality {
	if log == nil {
		log = slog.Default()
	}
	sorted := slices.Clone(augs)
	slices.SortStableFunc(sorted, func(a, b QualityAugmenter) int {
		return a.Order() - b.Order()
	})
	return &AggregateQuality{augmenters: sorted, log: log}
}

// Order returns 1 — the C# value at `:13`.
func (a *AggregateQuality) Order() int { return 1 }

// Aggregate merges every augmenter's verdict, then resolves the
// final Quality.
func (a *AggregateQuality) Aggregate(le *LocalEpisode, item *downloadclients.Item) *LocalEpisode {
	if le == nil {
		return le
	}
	source := quality.SourceUnknown
	sourceConfidence := QualityConfidenceDefault
	resolution := 0
	resolutionConfidence := QualityConfidenceDefault
	revision := quality.NewRevision()
	revisionConfidence := QualityConfidenceDefault

	for _, aug := range a.augmenters {
		r := aug.AugmentQuality(le, item)
		if r == nil {
			continue
		}
		// Source: take when previous is Unknown OR new confidence
		// is strictly higher AND the new source is non-Unknown.
		if source == quality.SourceUnknown ||
			(r.SourceConfidence > sourceConfidence && r.Source != quality.SourceUnknown) {
			source = r.Source
			sourceConfidence = r.SourceConfidence
		}
		// Resolution: take when previous is 0 OR new confidence is
		// strictly higher AND new resolution is positive.
		if resolution == 0 ||
			(r.ResolutionConfidence > resolutionConfidence && r.Resolution > 0) {
			resolution = r.Resolution
			resolutionConfidence = r.ResolutionConfidence
		}
		// Revision: same merge AND tiebreak — equal confidence with
		// strictly-higher revision also wins, per C# `:64-69`.
		if r.Revision != nil {
			if r.RevisionConfidence > revisionConfidence ||
				(r.RevisionConfidence == revisionConfidence && r.Revision.Compare(revision) > 0) {
				revision = *r.Revision
				revisionConfidence = r.RevisionConfidence
			}
		}
	}

	q := quality.NewModelWithRevision(quality.FindBySourceAndResolution(source, resolution), revision)

	// Detection-source mapping per C# `:78-101`.
	switch resolutionConfidence {
	case QualityConfidenceMediaInfo:
		q.ResolutionDetectionSource = quality.DetectionMediaInfo
	case QualityConfidenceFallback:
		q.ResolutionDetectionSource = quality.DetectionExtension
	default:
		q.ResolutionDetectionSource = quality.DetectionName
	}
	if sourceConfidence == QualityConfidenceFallback {
		q.SourceDetectionSource = quality.DetectionExtension
	} else {
		q.SourceDetectionSource = quality.DetectionName
	}
	if revisionConfidence == QualityConfidenceTag {
		q.RevisionDetectionSource = quality.DetectionName
	} else {
		q.RevisionDetectionSource = quality.DetectionUnknown
	}

	a.log.Debug("episodeimport: AggregateQuality selected", "path", le.Path, "quality", q)
	le.Quality = q
	return le
}

// AggregateSubtitleInfo and its helpers (SubtitleEpisodeFileFinder,
// title-cleanup logic) are deferred to slice 6b alongside the
// remaining heavier aggregators (AggregateEpisodes via ParsingService,
// AggregateReleaseInfo + AugmentQualityFromReleaseName via
// history.Service). Slice 6a stops at the 2000 LOC cap with the five
// no-history-dep aggregators landed.

// subtitleFileExtensionSet ports `SubtitleFileExtensions` at
// `Extras/Subtitles/SubtitleFileExtensions.cs:11-30`. Pre-landed in
// 6a so [IsSubtitleExtension] is available for the orchestrator's
// pre-aggregation file classification (slice 7) and so the AggregateSubtitleInfo
// port in 6b doesn't need to redefine the set. Wire-stable —
// new entries must append. Lowercased keys; lookups use
// `strings.ToLower` over the input ext.
var subtitleFileExtensionSet = map[string]bool{
	".aqt":   true,
	".ass":   true,
	".idx":   true,
	".jss":   true,
	".psb":   true,
	".rt":    true,
	".smi":   true,
	".srt":   true,
	".ssa":   true,
	".sub":   true,
	".txt":   true,
	".utf":   true,
	".utf8":  true,
	".utf-8": true,
	".vtt":   true,
}

// IsSubtitleExtension reports whether ext (e.g. ".srt", ".SRT") is a
// known subtitle extension. Case-insensitive. Empty string returns
// false.
func IsSubtitleExtension(ext string) bool {
	if ext == "" {
		return false
	}
	return subtitleFileExtensionSet[strings.ToLower(ext)]
}

// Compile-time interface-satisfaction guards.
var (
	_ LocalEpisodeAggregator = AggregateReleaseGroup{}
	_ LocalEpisodeAggregator = AggregateReleaseHash{}
	_ LocalEpisodeAggregator = (*AggregateLanguage)(nil)
	_ LocalEpisodeAggregator = (*AggregateQuality)(nil)
)
