package episodeimport

import (
	"slices"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
)

// LanguageConfidence ports the C# language-augmenter confidence enum
// at `Aggregation/Aggregators/Augmenters/Language/Confidence.cs:3-10`.
//
// Numeric ordering: AggregateLanguage replaces the running languages
// list when an augmenter's confidence is HIGHER than the previous —
// so the order frozen here drives runtime behaviour.
type LanguageConfidence int

// LanguageConfidence ordinals. Order matches C# `:5-9` exactly.
const (
	// LanguageConfidenceDefault is the initial sentinel before any
	// augmenter weighs in.
	LanguageConfidenceDefault LanguageConfidence = 0

	// LanguageConfidenceFilename — derived from the parsed file name.
	LanguageConfidenceFilename LanguageConfidence = 1

	// LanguageConfidenceFoldername — derived from the parsed folder
	// name.
	LanguageConfidenceFoldername LanguageConfidence = 2

	// LanguageConfidenceDownloadClientItem — derived from the parsed
	// download-client item title.
	LanguageConfidenceDownloadClientItem LanguageConfidence = 3

	// LanguageConfidenceMediaInfo — derived from MediaInfo audio
	// streams' declared language tags. Highest tier.
	LanguageConfidenceMediaInfo LanguageConfidence = 4
)

// String returns the C# enum-identifier form.
func (c LanguageConfidence) String() string {
	switch c {
	case LanguageConfidenceDefault:
		return "Default"
	case LanguageConfidenceFilename:
		return "Filename"
	case LanguageConfidenceFoldername:
		return "Foldername"
	case LanguageConfidenceDownloadClientItem:
		return "DownloadClientItem"
	case LanguageConfidenceMediaInfo:
		return "MediaInfo"
	}
	return "LanguageConfidence(?)"
}

// AugmentLanguageResult is the per-augmenter output. Mirrors
// `Aggregators/Augmenters/Language/AugmentLanguageResult.cs:5-17`.
type AugmentLanguageResult struct {
	Name       string
	Languages  []language.Language
	Confidence LanguageConfidence
}

// NewAugmentLanguageResult is the explicit-component constructor.
func NewAugmentLanguageResult(langs []language.Language, confidence LanguageConfidence) *AugmentLanguageResult {
	return &AugmentLanguageResult{Languages: langs, Confidence: confidence}
}

// LanguageAugmenter ports `IAugmentLanguage` at
// `Aggregators/Augmenters/Language/IAugmentLanguage.cs:6-11`.
//
// Returning nil signals "this augmenter has no opinion" — the
// aggregator skips the merge.
type LanguageAugmenter interface {
	// Order is the deterministic walk order. Lower runs first.
	Order() int

	// AugmenterName mirrors C# `IAugmentLanguage.Name`. Renamed to
	// AugmenterName so it doesn't collide with [Specification.Name]
	// on types that satisfy both contracts.
	AugmenterName() string

	// AugmentLanguage returns the augmenter's verdict, or nil if
	// the augmenter has no opinion for this candidate.
	AugmentLanguage(le *LocalEpisode, item *downloadclients.Item) *AugmentLanguageResult
}

// augmentLanguageFromParsedInfo is the shared body for the three
// "augment from a ParsedEpisodeInfo source" augmenters (FileName,
// Folder, DownloadClientItem). The C# tree has three classes with
// identical bodies modulo (a) which ParsedEpisodeInfo field they
// read and (b) which Confidence tier they tag. Go port collapses to
// one helper.
//
// The C# loop subtracts every language detected in any matched
// episode title from the source list (`languages.Except(episodeTitleLanguage)`)
// — defends against the parser tagging an episode title's "French
// Connection" as actual French audio. Go port mirrors via
// [parser.ParseLanguages] over each episode title and a slice
// difference. Returns nil when info is nil OR info.Languages is nil
// (matches C# `languages == null` at `:14-17`).
func augmentLanguageFromParsedInfo(info *parser.ParsedEpisodeInfo, eps []episodes.Episode, confidence LanguageConfidence) *AugmentLanguageResult {
	if info == nil || info.Languages == nil {
		return nil
	}
	langs := slices.Clone(info.Languages)
	for _, e := range eps {
		titleLangs := parser.ParseLanguages(e.Title)
		langs = sliceLanguageDifference(langs, titleLangs)
	}
	return &AugmentLanguageResult{Languages: langs, Confidence: confidence}
}

// sliceLanguageDifference returns the elements of a not present in b
// (preserving a's order). Mirrors C# `Enumerable.Except` which is
// O(n+m) via a HashSet — Go port uses a small map to match the
// asymptotic shape.
func sliceLanguageDifference(a, b []language.Language) []language.Language {
	if len(b) == 0 {
		return a
	}
	excluded := make(map[language.Language]struct{}, len(b))
	for _, l := range b {
		excluded[l] = struct{}{}
	}
	out := a[:0:0]
	for _, l := range a {
		if _, drop := excluded[l]; drop {
			continue
		}
		out = append(out, l)
	}
	return out
}

// AugmentLanguageFromFileName ports
// `AugmentLanguageFromFileName.cs:7-31`.
type AugmentLanguageFromFileName struct{}

// Order returns 1 — the C# value at `:9`.
func (AugmentLanguageFromFileName) Order() int { return 1 }

// AugmenterName returns `"FileName"` per C# `:10`.
func (AugmentLanguageFromFileName) AugmenterName() string { return "FileName" }

// AugmentLanguage dispatches to [augmentLanguageFromParsedInfo].
func (AugmentLanguageFromFileName) AugmentLanguage(le *LocalEpisode, _ *downloadclients.Item) *AugmentLanguageResult {
	if le == nil {
		return nil
	}
	return augmentLanguageFromParsedInfo(le.FileEpisodeInfo, le.Episodes, LanguageConfidenceFilename)
}

// AugmentLanguageFromFolder ports
// `AugmentLanguageFromFolder.cs:7-31`.
type AugmentLanguageFromFolder struct{}

// Order returns 2 — the C# value at `:9`.
func (AugmentLanguageFromFolder) Order() int { return 2 }

// AugmenterName returns `"FolderName"` per C# `:10`.
func (AugmentLanguageFromFolder) AugmenterName() string { return "FolderName" }

// AugmentLanguage dispatches to [augmentLanguageFromParsedInfo].
func (AugmentLanguageFromFolder) AugmentLanguage(le *LocalEpisode, _ *downloadclients.Item) *AugmentLanguageResult {
	if le == nil {
		return nil
	}
	return augmentLanguageFromParsedInfo(le.FolderEpisodeInfo, le.Episodes, LanguageConfidenceFoldername)
}

// AugmentLanguageFromDownloadClientItem ports
// `AugmentLanguageFromDownloadClientItem.cs:7-31`.
type AugmentLanguageFromDownloadClientItem struct{}

// Order returns 3 — the C# value at `:9`.
func (AugmentLanguageFromDownloadClientItem) Order() int { return 3 }

// AugmenterName returns `"DownloadClientItem"` per C# `:10`.
func (AugmentLanguageFromDownloadClientItem) AugmenterName() string { return "DownloadClientItem" }

// AugmentLanguage dispatches to [augmentLanguageFromParsedInfo].
func (AugmentLanguageFromDownloadClientItem) AugmentLanguage(le *LocalEpisode, _ *downloadclients.Item) *AugmentLanguageResult {
	if le == nil {
		return nil
	}
	return augmentLanguageFromParsedInfo(le.DownloadClientEpisodeInfo, le.Episodes, LanguageConfidenceDownloadClientItem)
}

// AugmentLanguageFromMediaInfo ports
// `AugmentLanguageFromMediaInfo.cs:11-39`.
//
// Walks LocalEpisode.MediaInfo.AudioStreams, distinct'ing the
// `Language` tag (an ISO 639-2/B 3-letter code), resolving each
// through [language.FindIso] (the Go-port equivalent of C#
// `IsoLanguages.Find`), and emitting the resulting Language list at
// MediaInfo confidence.
//
// Returns nil when:
//
//   - MediaInfo is nil OR AudioStreams is empty.
//   - Every audio-stream language tag resolves to an unknown / nil
//     entry (the C# code returns null in this branch via
//     `languages.Count == 0` at `:33`).
//
// IsoFind dependency is injectable so tests pin behaviour without
// running the full ISO registry; production wires
// [language.FindIso] directly.
type AugmentLanguageFromMediaInfo struct {
	IsoFinder func(code string) (language.Language, bool)
}

// NewAugmentLanguageFromMediaInfo binds an IsoFinder. nil defaults
// to a closure over [language.FindIso] that returns the bound
// Language (or Unknown,false for misses).
func NewAugmentLanguageFromMediaInfo(finder func(code string) (language.Language, bool)) *AugmentLanguageFromMediaInfo {
	if finder == nil {
		finder = func(code string) (language.Language, bool) {
			iso, ok := language.FindIso(code)
			if !ok {
				return language.Unknown, false
			}
			return iso.Language, true
		}
	}
	return &AugmentLanguageFromMediaInfo{IsoFinder: finder}
}

// Order returns 4 — the C# value at `:9`.
func (a *AugmentLanguageFromMediaInfo) Order() int { return 4 }

// AugmenterName returns `"MediaInfo"` per C# `:10`.
func (a *AugmentLanguageFromMediaInfo) AugmenterName() string { return "MediaInfo" }

// AugmentLanguage walks distinct AudioStream language tags through
// the ISO finder.
func (a *AugmentLanguageFromMediaInfo) AugmentLanguage(le *LocalEpisode, _ *downloadclients.Item) *AugmentLanguageResult {
	if le == nil || le.MediaInfo == nil || len(le.MediaInfo.AudioStreams) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(le.MediaInfo.AudioStreams))
	var langs []language.Language
	for _, stream := range le.MediaInfo.AudioStreams {
		code := stream.Language
		if code == "" {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		l, ok := a.IsoFinder(code)
		if !ok {
			continue
		}
		langs = append(langs, l)
	}
	if len(langs) == 0 {
		return nil
	}
	return &AugmentLanguageResult{Languages: langs, Confidence: LanguageConfidenceMediaInfo}
}

// Compile-time interface-satisfaction guards.
var (
	_ LanguageAugmenter = AugmentLanguageFromFileName{}
	_ LanguageAugmenter = AugmentLanguageFromFolder{}
	_ LanguageAugmenter = AugmentLanguageFromDownloadClientItem{}
	_ LanguageAugmenter = (*AugmentLanguageFromMediaInfo)(nil)
)
