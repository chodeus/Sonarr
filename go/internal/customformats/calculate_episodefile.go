package customformats

import (
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// ParseCustomFormatForFile is the EpisodeFile-driven entry point of
// the CustomFormat matching engine. Mirrors C#
// `CustomFormatCalculationService.ParseCustomFormat(EpisodeFile, Series, allCustomFormats)`
// at `CustomFormats/CustomFormatCalculationService.cs:170-211`.
//
// # Algorithm (mirrors C# `:170-211`)
//
//  1. Resolve ReleaseTitle from the first non-empty of:
//     a. file.SceneName (post-rename scene-name capture).
//     b. filepath.Base(file.OriginalFilePath) (the import-time path).
//     c. filepath.Base(file.RelativePath) (current path).
//     d. "" (no signal — matching specs degrade to never-match).
//  2. Build a [parser.ParsedEpisodeInfo] from series.Title +
//     file's persisted Quality (decoded via [quality.Model.UnmarshalJSON],
//     same posture as [decisionengine.AlreadyImportedSpec.sameQualityModel])
//     + Languages + ReleaseGroup.
//  3. Bundle into [CustomFormatInput] with file.Size +
//     file.IndexerFlags + file.ReleaseType + Filename =
//     filepath.Base(file.RelativePath).
//  4. Walk [Calculate] against allFormats; matched typed [*Format]
//     values are converted back to the persisted-row form
//     [[]CustomFormat] for assignment to caller-side
//     LocalEpisode.OldFiles[*].CustomFormats / spec evaluation
//     fields.
//
// # Why the result type is []CustomFormat (the persisted shape)
//
// Callers are usually preparing data for a spec that compares
// against another already-decoded `LocalEpisode.CustomFormats` field
// (e.g. UpgradeSpec / AlreadyImportedSpec) — that field is the
// persisted []CustomFormat shape. Returning the typed [*Format]
// would force every caller to convert. The cost is a fresh slice
// allocation per call; the cache hit (Service.All) is unchanged.
//
// nil file returns nil. Empty allFormats returns empty.
//
// # Callers (production)
//
// Production consumers wrap a [*Service.All] result + this fn behind
// the narrow [episodeimport.CustomFormatCalculator] interface (slice
// 3.AP wire-out) and the future
// `decisionengine.UpgradeAllowedSpecification` /
// `UpgradeDiskSpecification` ports.
func ParseCustomFormatForFile(file *episodefiles.File, series *series.Series, allFormats []*Format) []CustomFormat {
	if file == nil {
		return nil
	}
	releaseTitle := releaseTitleForEpisodeFile(file)
	episodeInfo := &parser.ParsedEpisodeInfo{
		ReleaseTitle: releaseTitle,
		SeriesTitle:  seriesTitle(series),
		Quality:      decodeFileQuality(file.Quality),
		Languages:    languagesFromIDs(file.Languages),
		ReleaseGroup: file.ReleaseGroup,
	}
	input := CustomFormatInput{
		EpisodeInfo:  episodeInfo,
		Series:       series,
		Size:         file.Size,
		IndexerFlags: parser.IndexerFlags(file.IndexerFlags),
		Languages:    languagesFromIDs(file.Languages),
		Filename:     filepath.Base(file.RelativePath),
		ReleaseType:  parser.ReleaseType(file.ReleaseType),
		// POST-download: the persisted probe (nil for pre-225 rows /
		// files never probed) drives the MediaInfo-based specs.
		MediaInfo: file.MediaInfo,
	}
	matched := Calculate(input, allFormats)
	out := make([]CustomFormat, 0, len(matched))
	for _, f := range matched {
		if f == nil {
			continue
		}
		out = append(out, CustomFormat{
			ID:                              f.ID,
			Name:                            f.Name,
			IncludeCustomFormatWhenRenaming: f.IncludeCustomFormatWhenRenaming,
		})
	}
	return out
}

// releaseTitleForEpisodeFile mirrors C# `:172-188` — three-tier
// fallback for the ReleaseTitle field. The C# tree logs a Trace
// line per arm; the Go port skips the per-call log because the
// invocation cadence is per-spec-evaluation and would flood the
// trace channel.
func releaseTitleForEpisodeFile(file *episodefiles.File) string {
	if t := strings.TrimSpace(file.SceneName); t != "" {
		return t
	}
	if t := strings.TrimSpace(file.OriginalFilePath); t != "" {
		return filepath.Base(t)
	}
	if t := strings.TrimSpace(file.RelativePath); t != "" {
		return filepath.Base(t)
	}
	return ""
}

// seriesTitle returns the matched series' title or "" for a nil
// receiver. The C# tree dereferences `series.Title` directly and
// would NPE on nil — the Go port swallows since callers may pass
// a nil series for files whose series row was deleted out-of-band.
func seriesTitle(s *series.Series) string {
	if s == nil {
		return ""
	}
	return s.Title
}

// decodeFileQuality parses the persisted Quality JSON into a typed
// [quality.Model]. Mirrors the per-row Quality decode at
// `episodefiles.File.ParsedQuality()` — but inlined here to avoid
// the extra (Model, error) return value the consumer doesn't need
// (a malformed Quality JSON degrades to a zero Model, matching the
// C# `QualityModel == null` arm at the spec call sites).
func decodeFileQuality(raw string) quality.Model {
	var m quality.Model
	if raw == "" {
		return m
	}
	_ = m.UnmarshalJSON([]byte(raw))
	return m
}

// languagesFromIDs maps the persisted []int Languages column on
// EpisodeFile into the typed []language.Language consumed by
// [CustomFormatInput.Languages]. Unknown IDs are skipped (matches
// the C# `LanguageParser.ParseLanguages` posture which drops
// unrecognised codes silently).
//
// Returns a fresh slice — independent of the caller's input slice
// so the CustomFormatInput stays decoupled from EpisodeFile mutation.
func languagesFromIDs(ids []int) []language.Language {
	if len(ids) == 0 {
		return nil
	}
	out := make([]language.Language, 0, len(ids))
	for _, id := range ids {
		if l, ok := language.FindByID(id); ok {
			out = append(out, l)
		}
	}
	return out
}
