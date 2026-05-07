package episodeimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
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

// EpisodeParsingService is the slice of `IParsingService` that
// `AggregateEpisodes` reaches for at
// `AggregateEpisodes.cs:62,79,87`. The full ParsingService is a
// downstream subsystem (matching/lookup against the metadata cache,
// scene-numbering remap, anime-absolute resolution) that hasn't
// ported yet — this narrow surface lets slice 6b ship the aggregator
// against a stub today, with the production binding happening in the
// later ParsingService slice.
//
// ParseSpecialEpisodeTitle mirrors C# `IParsingService.ParseSpecialEpisodeTitle`:
// given a previously-parsed info (may be nil — the C# `parsedEpisodeInfo`
// arg can be null when the file/folder/download-client parses all
// missed) plus the file's bare title (no extension) plus the matched
// series, return a [parser.ParsedEpisodeInfo] for a special episode
// or nil when no special-episode match resolved.
//
// GetEpisodes mirrors C# `IParsingService.GetEpisodes`: given a
// parsed-info + the matched series + a `sceneSource` flag, returns
// the resolved [episodes.Episode] list. Empty result is the
// "couldn't map" signal — the aggregator handles the empty-then-
// special-episode fallback at C# `:81-89`.
type EpisodeParsingService interface {
	ParseSpecialEpisodeTitle(prev *parser.ParsedEpisodeInfo, title string, s *series.Series) *parser.ParsedEpisodeInfo
	GetEpisodes(parsed *parser.ParsedEpisodeInfo, s *series.Series, sceneSource bool) []episodes.Episode
}

// AggregateEpisodes ports
// `Aggregators/AggregateEpisodes.cs:11-113`.
//
// Three-source pick (FileEpisodeInfo / DownloadClientEpisodeInfo /
// FolderEpisodeInfo) followed by a `ValidateForSeriesType` gate and
// a special-episode fallback. The C# class's per-bug behaviour is
// mirrored exactly:
//
//   - The download-client + folder sources are only considered when
//     `OtherVideoFiles=false` AND the file's bare title is NOT itself
//     a scene-named title (`SceneChecker.IsSceneTitle` at C# `:35`).
//     The negation is critical: a scene title in the bare filename
//     means the filename is more authoritative than any folder/client
//     wrapping — file parse wins outright.
//
//   - Within that gate, the download-client info is preferred over
//     the file info when (a) it's not FullSeason AND (b) the
//     [PreferOtherEpisodeInfo] rule says yes. Folder info is the
//     fallback under the same rules.
//
//   - PreferOtherEpisodeInfo (C# `:97-111`) is "true unless the file
//     parse is non-absolute and the other parse is absolute" — the
//     comment at C# `:104` documents the intent: a non-absolute file
//     parse beats an absolute folder/client parse because absolute
//     numbering at the wrapping layer is usually a default-from-bare-
//     parse rather than a real scene mapping.
//
//   - When the three-source pick yields nil, we call
//     [GetSpecialEpisodeInfo] with nil as the seed (C# `:53`) — this
//     is the "no parse anywhere, but it might be a one-off special"
//     escape hatch.
//
//   - When `GetEpisodes` returns empty AND the parsed info reports
//     IsPossibleSpecialEpisode, the C# code re-runs special-episode
//     parsing using the *current* parsed info as the seed (C# `:83`)
//     — NOT nil. This is mirrored verbatim; if the second
//     ParseSpecialEpisodeTitle returns non-nil, the aggregator calls
//     GetEpisodes a second time with that info.
//
// Order is 1 — the C# value at `:13`. Same Order as
// AggregateReleaseGroup, AggregateReleaseHash, AggregateReleaseInfo,
// and the language aggregator; the AggregationService preserves
// registration order for ties.
type AggregateEpisodes struct {
	parsing EpisodeParsingService
}

// NewAggregateEpisodes binds the parsing-service dep. nil parsing
// returns nil — the production wiring (slice that ports
// ParsingService) MUST pass a real implementation.
func NewAggregateEpisodes(parsing EpisodeParsingService) *AggregateEpisodes {
	if parsing == nil {
		return nil
	}
	return &AggregateEpisodes{parsing: parsing}
}

// Order returns 1 — the C# value at `AggregateEpisodes.cs:13`.
func (*AggregateEpisodes) Order() int { return 1 }

// Aggregate fills LocalEpisode.Episodes per the rules above.
func (a *AggregateEpisodes) Aggregate(le *LocalEpisode, _ *downloadclients.Item) *LocalEpisode {
	if le == nil || a == nil {
		return le
	}
	le.Episodes = a.getEpisodes(le)
	return le
}

// getEpisodes mirrors C# `:67-95` (the `GetEpisodes` private method).
// Returns empty slice (never nil) when no episodes resolve.
func (a *AggregateEpisodes) getEpisodes(le *LocalEpisode) []episodes.Episode {
	best := a.getBestEpisodeInfo(le)
	ext := filepath.Ext(le.Path)
	isMediaFile := mediafiles.IsMediaExtension(strings.ToLower(ext))

	if best == nil {
		return []episodes.Episode{}
	}

	// SeriesType==0 is Standard; pass it through ValidateForSeriesType
	// — the C# version takes the Series directly, the Go port took
	// the int field to avoid an import cycle (see validate.go's doc).
	st := 0
	if le.Series != nil {
		st = le.Series.SeriesType
	}
	if !parser.ValidateForSeriesType(best, st) {
		// C# `:77` also passes `isMediaFile` as the warnIfInvalid
		// switch — the Go port collapsed that parameter (see
		// validate.go's doc). The behavioural outcome (return empty)
		// is identical.
		_ = isMediaFile
		return []episodes.Episode{}
	}

	eps := a.parsing.GetEpisodes(best, le.Series, le.SceneSource)
	if len(eps) == 0 && best.IsPossibleSpecialEpisode() {
		// C# `:83` — RE-USE `best` as the seed (not nil). The
		// special-episode parser may consult the previous parse to
		// constrain the matched title.
		special := a.getSpecialEpisodeInfo(le, best)
		if special != nil {
			eps = a.parsing.GetEpisodes(special, le.Series, le.SceneSource)
		}
	}
	if eps == nil {
		eps = []episodes.Episode{}
	}
	return eps
}

// getBestEpisodeInfo mirrors C# `:29-57`.
func (a *AggregateEpisodes) getBestEpisodeInfo(le *LocalEpisode) *parser.ParsedEpisodeInfo {
	parsed := le.FileEpisodeInfo
	dc := le.DownloadClientEpisodeInfo
	folder := le.FolderEpisodeInfo

	bareTitle := strings.TrimSuffix(filepath.Base(le.Path), filepath.Ext(le.Path))
	bareIsScene := parser.IsSceneTitle(bareTitle)

	if !le.OtherVideoFiles && !bareIsScene {
		if dc != nil && !dc.FullSeason && preferOtherEpisodeInfo(parsed, dc) {
			parsed = dc
		} else if folder != nil && !folder.FullSeason && preferOtherEpisodeInfo(parsed, folder) {
			parsed = folder
		}
	}

	if parsed == nil {
		// C# `:53` — pass `parsed` (nil here) as the seed.
		parsed = a.getSpecialEpisodeInfo(le, parsed)
	}
	return parsed
}

// getSpecialEpisodeInfo mirrors C# `:59-65`. The bare-title is the
// filename with extension stripped; the parsing-service is responsible
// for anything heavier.
func (a *AggregateEpisodes) getSpecialEpisodeInfo(le *LocalEpisode, prev *parser.ParsedEpisodeInfo) *parser.ParsedEpisodeInfo {
	bareTitle := strings.TrimSuffix(filepath.Base(le.Path), filepath.Ext(le.Path))
	return a.parsing.ParseSpecialEpisodeTitle(prev, bareTitle, le.Series)
}

// preferOtherEpisodeInfo mirrors C# `:97-111`. Returns true (prefer
// the "other" / wrapping parse) UNLESS the file parse is
// non-absolute and the other parse is absolute — see C# `:104` for
// the rationale comment about absolute parses being too coarse.
//
// nil file parse always falls through to the "prefer other" branch
// at C# `:99-102`.
func preferOtherEpisodeInfo(file, other *parser.ParsedEpisodeInfo) bool {
	if file == nil {
		return true
	}
	if !file.IsAbsoluteNumbering() && other.IsAbsoluteNumbering() {
		return false
	}
	return true
}

// HistoryGrabsFinder is the slice of `IHistoryService.FindByDownloadId`
// that [AggregateReleaseInfo] reaches for at
// `AggregateReleaseInfo.cs:27`. Production wiring binds the future
// `history.Service` (not yet ported); for now only the repo-level
// `AllByDownloadID` exists, and the wiring slice will compose the
// EventType filter at the service boundary.
//
// The interface returns the unfiltered list — the aggregator does
// the EventType==Grabbed filter (mirroring C# `:28`) so any future
// service can keep its `FindByDownloadId` clean.
//
// Returning a non-nil error logs and skips the aggregator — the
// orchestrator continues without a Release populated.
type HistoryGrabsFinder interface {
	FindGrabsByDownloadID(ctx context.Context, downloadID string) ([]history.Entry, error)
}

// EpisodeHistoryEventTypeGrabbed is the persisted EventType integer
// the C# `EpisodeHistoryEventType.Grabbed` enum maps to. Mirrors
// `History/EpisodeHistory.cs:39` (`Grabbed = 1`). Wire-stable —
// the persisted history rows carry this integer.
const EpisodeHistoryEventTypeGrabbed = 1

// AggregateReleaseInfo ports
// `Aggregators/AggregateReleaseInfo.cs:9-40`.
//
// Skip arms (matching C# `:22-34`):
//
//   - nil download item — manual / library rescan flow, no grab
//     history to consult.
//   - history finder returns no Grabbed-event entries — the C# code's
//     `grabbedHistories.Empty()` arm at `:31-34` returns the
//     LocalEpisode unchanged.
//   - finder error — logged and skipped (treated like the empty arm).
//
// On success, populates [LocalEpisode.Release] with the
// [GrabbedReleaseInfo] derived from the matched history entries.
//
// Slice 6b populates only `Title` from the history's SourceTitle —
// the remaining fields (Indexer / Size / IndexerFlags / ReleaseType
// / EpisodeIDs) require heavier history-service-side construction
// (the C# `GrabbedReleaseInfo(List<EpisodeHistory>)` ctor at
// `Parser/Model/GrabbedReleaseInfo.cs`); those land with the writer
// slice + history-service slice, as already documented on the
// [GrabbedReleaseInfo] type itself.
//
// Order is 1 — the C# value at `:11`. AggregationService preserves
// registration order for ties.
type AggregateReleaseInfo struct {
	finder HistoryGrabsFinder
	log    *slog.Logger
}

// NewAggregateReleaseInfo binds the history-grabs finder and a logger.
// nil log defaults to slog.Default(). nil finder returns nil.
func NewAggregateReleaseInfo(finder HistoryGrabsFinder, log *slog.Logger) *AggregateReleaseInfo {
	if finder == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &AggregateReleaseInfo{finder: finder, log: log}
}

// Order returns 1 — the C# value at `AggregateReleaseInfo.cs:11`.
func (*AggregateReleaseInfo) Order() int { return 1 }

// Aggregate fills LocalEpisode.Release from the matched grab history.
func (a *AggregateReleaseInfo) Aggregate(le *LocalEpisode, item *downloadclients.Item) *LocalEpisode {
	if le == nil || a == nil {
		return le
	}
	if item == nil || item.DownloadID == "" {
		return le
	}
	all, err := a.finder.FindGrabsByDownloadID(context.Background(), item.DownloadID)
	if err != nil {
		a.log.Debug("episodeimport: AggregateReleaseInfo: history lookup failed; skipping",
			"path", le.Path, "downloadId", item.DownloadID, "err", err)
		return le
	}
	grabs := filterGrabbed(all)
	if len(grabs) == 0 {
		return le
	}
	le.Release = newGrabbedReleaseInfoFromHistory(grabs)
	return le
}

// filterGrabbed keeps only history rows whose EventType marks a
// grab event (mirrors C# `:28` — `EpisodeHistoryEventType.Grabbed`).
// Rows with nil EventType are skipped (old pre-EventType-column rows
// can't be Grabbed events by definition).
func filterGrabbed(rows []history.Entry) []history.Entry {
	out := rows[:0:0]
	for _, r := range rows {
		if r.EventType != nil && *r.EventType == EpisodeHistoryEventTypeGrabbed {
			out = append(out, r)
		}
	}
	return out
}

// newGrabbedReleaseInfoFromHistory builds the GrabbedReleaseInfo
// surface available in slice 6b. Today only Title is populated —
// the C# constructor (`GrabbedReleaseInfo(List<EpisodeHistory>)`)
// also derives Indexer / Size / IndexerFlags / ReleaseType /
// EpisodeIDs from the persisted Data dictionary; that derivation
// belongs in the history-service slice that owns the schema, not in
// the import aggregator. Title comes from the most-recent grab's
// SourceTitle (the rows are returned date-descending by the repo).
func newGrabbedReleaseInfoFromHistory(grabs []history.Entry) *GrabbedReleaseInfo {
	if len(grabs) == 0 {
		return nil
	}
	return &GrabbedReleaseInfo{Title: grabs[0].SourceTitle}
}

// AggregateSubtitleInfo ports
// `Aggregators/AggregateSubtitleInfo.cs:13-71`.
//
// Skip arms (matching C# `:24-37`):
//
//   - file extension is not a subtitle extension (uses the
//     [IsSubtitleExtension] / [subtitleFileExtensionSet] map landed
//     in slice 6a).
//   - LocalEpisode.Episodes is empty — no episode means no
//     EpisodeFile to clean against.
//
// On success, populates [LocalEpisode.SubtitleInfo] with the result
// of [CleanSubtitleTitleInfo]. The episode-file lookup uses the
// existing [EpisodeFileResolver] interface from specs.go (the same
// slice of IEpisodeFileService that
// HasAudioTrackSpecification.GetByID consumes).
//
// Order is 2 — the C# value at `:15` — placing this aggregator
// strictly after the Order=1 group (Episodes / ReleaseGroup /
// ReleaseHash / ReleaseInfo / Language) so Episodes is populated by
// the time we look up its EpisodeFile.
type AggregateSubtitleInfo struct {
	files EpisodeFileResolver
	log   *slog.Logger
}

// NewAggregateSubtitleInfo binds the file resolver and logger.
// nil log defaults to slog.Default(). nil files returns nil.
func NewAggregateSubtitleInfo(files EpisodeFileResolver, log *slog.Logger) *AggregateSubtitleInfo {
	if files == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &AggregateSubtitleInfo{files: files, log: log}
}

// Order returns 2 — the C# value at `AggregateSubtitleInfo.cs:15`.
func (*AggregateSubtitleInfo) Order() int { return 2 }

// Aggregate fills LocalEpisode.SubtitleInfo per the rules above.
func (a *AggregateSubtitleInfo) Aggregate(le *LocalEpisode, _ *downloadclients.Item) *LocalEpisode {
	if le == nil || a == nil {
		return le
	}
	if !IsSubtitleExtension(filepath.Ext(le.Path)) {
		return le
	}
	if len(le.Episodes) == 0 {
		return le
	}
	first := le.Episodes[0]
	if first.EpisodeFileID == nil || *first.EpisodeFileID == 0 {
		// C# treats `EpisodeFile.Value` as a LazyLoaded that may be
		// nil when the episode hasn't been linked to a persisted file
		// yet — the LocalEpisode flow can hit this for fresh imports
		// where the writer slice hasn't yet persisted the EpisodeFile.
		// Mirror by skipping.
		return le
	}
	ef, err := a.files.GetByID(context.Background(), *first.EpisodeFileID)
	if err != nil || ef == nil {
		// Not a recoverable error — log + skip. Subtitle info is
		// best-effort (the C# code would NPE here on a missing file;
		// the Go port chooses to swallow rather than fault).
		if err != nil {
			a.log.Debug("episodeimport: AggregateSubtitleInfo: episode-file lookup failed; skipping",
				"path", le.Path, "episodeFileId", *first.EpisodeFileID, "err", err)
		}
		return le
	}
	info := cleanSubtitleTitleInfo(a.log, ef, le.Path, le.FileNameBeforeRename)
	le.SubtitleInfo = &info
	return le
}

// cleanSubtitleTitleInfo ports `AggregateSubtitleInfo.CleanSubtitleTitleInfo`
// at C# `:46-69`.
//
// Two cleanups applied to the parsed [parser.SubtitleTitleInfo]:
//
//  1. If TitleFirst AND the raw title is contained in either the
//     post-rename file title or the original file title, downgrade
//     to a basic parse (drop the title-aware capture).
//
//  2. From the remaining LanguageTags, drop any tag that's already a
//     substring of the post-rename file title.
//
// Both comparisons are case-insensitive (the C# `StringComparison.OrdinalIgnoreCase`
// — Go uses `strings.EqualFold` after a `strings.ToLower` on both
// sides for substring containment).
func cleanSubtitleTitleInfo(log *slog.Logger, ef *episodefiles.File, path, fileNameBeforeRename string) parser.SubtitleTitleInfo {
	info := parser.ParseSubtitleLanguageInformation(path)

	// C# `:50` — episodeFileTitle = Path.GetFileNameWithoutExtension(
	//   fileNameBeforeRename ?? episodeFile.RelativePath).
	episodeFileSource := fileNameBeforeRename
	if episodeFileSource == "" {
		episodeFileSource = ef.RelativePath
	}
	episodeFileTitle := strings.TrimSuffix(filepath.Base(episodeFileSource), filepath.Ext(episodeFileSource))
	originalEpisodeFileTitle := strings.TrimSuffix(filepath.Base(ef.OriginalFilePath), filepath.Ext(ef.OriginalFilePath))

	if info.TitleFirst && info.RawTitle != "" {
		lower := strings.ToLower(info.RawTitle)
		inEpisode := strings.Contains(strings.ToLower(episodeFileTitle), lower)
		inOriginal := strings.Contains(strings.ToLower(originalEpisodeFileTitle), lower)
		if inEpisode || inOriginal {
			log.Debug("episodeimport: AggregateSubtitleInfo: subtitle title contained in file title; downgrading to basic parse",
				"rawTitle", info.RawTitle, "episodeFileTitle", episodeFileTitle)
			info = parser.ParseBasicSubtitle(path)
		}
	}

	if len(info.LanguageTags) > 0 {
		lowerTitle := strings.ToLower(episodeFileTitle)
		cleaned := info.LanguageTags[:0:0]
		var dropped []string
		for _, t := range info.LanguageTags {
			if strings.Contains(lowerTitle, strings.ToLower(t)) {
				dropped = append(dropped, t)
				continue
			}
			cleaned = append(cleaned, t)
		}
		if len(dropped) > 0 {
			log.Debug("episodeimport: AggregateSubtitleInfo: removed language tags that already appear in file title",
				"removed", dropped, "rawTitle", info.RawTitle)
			info.LanguageTags = cleaned
		}
	}

	return info
}

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
	_ LocalEpisodeAggregator = (*AggregateEpisodes)(nil)
	_ LocalEpisodeAggregator = (*AggregateReleaseInfo)(nil)
	_ LocalEpisodeAggregator = (*AggregateSubtitleInfo)(nil)
)
