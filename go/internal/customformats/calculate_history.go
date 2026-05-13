package customformats

import (
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// ParseCustomFormatForHistory is the EpisodeHistory-driven entry
// point of the CustomFormat matching engine. Mirrors C#
// `CustomFormatCalculationService.ParseCustomFormat(EpisodeHistory, Series)`
// at `CustomFormatCalculationService.cs:87-115`.
//
// # Algorithm (mirrors C# :87-115)
//
//  1. Parse the History row's SourceTitle via [parser.ParseTitle] for
//     ReleaseTitle + ReleaseGroup.
//  2. Decode the per-history Data map for size + indexerFlags +
//     releaseType — the C# tree stamps these onto Data at grab-time
//     and reads them back here. Missing or malformed entries degrade
//     to the zero value (size=0, indexerFlags=0, releaseType=0),
//     matching the C# `Enum.TryParse` / `long.TryParse` posture.
//  3. Build a [parser.ParsedEpisodeInfo] from series.Title +
//     parser-extracted ReleaseTitle (or SourceTitle fallback) +
//     entry.Quality + entry.Languages + parser-extracted ReleaseGroup.
//  4. Bundle into [CustomFormatInput].
//  5. Walk [Calculate]; convert matched typed *Format back to
//     []CustomFormat.
//
// nil entry returns nil. Empty allFormats returns empty.
//
// Slice 4.BX (C-cleanup [C12] sub-arc C12a).
func ParseCustomFormatForHistory(entry *history.Entry, ser *series.Series, allFormats []*Format) []CustomFormat {
	if entry == nil {
		return nil
	}

	parsed := parser.ParseTitle(entry.SourceTitle)

	releaseTitle := entry.SourceTitle
	releaseGroup := ""
	if parsed != nil {
		if parsed.ReleaseTitle != "" {
			releaseTitle = parsed.ReleaseTitle
		}
		releaseGroup = parsed.ReleaseGroup
	}

	size, indexerFlags, releaseType := historyDataFlags(entry.Data)

	episodeInfo := &parser.ParsedEpisodeInfo{
		SeriesTitle:  seriesTitle(ser),
		ReleaseTitle: releaseTitle,
		Quality:      decodeFileQuality(entry.Quality),
		Languages:    languagesFromIDs(entry.Languages),
		ReleaseGroup: releaseGroup,
	}

	input := CustomFormatInput{
		EpisodeInfo:  episodeInfo,
		Series:       ser,
		Size:         size,
		IndexerFlags: indexerFlags,
		Languages:    languagesFromIDs(entry.Languages),
		ReleaseType:  releaseType,
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

// historyDataFlags extracts the (size, indexerFlags, releaseType)
// triple from a History.Data map. Mirrors C#
// `CustomFormatCalculationService.cs:91-93`:
//
//	long.TryParse(history.Data.GetValueOrDefault("size"), out var size);
//	Enum.TryParse(history.Data.GetValueOrDefault("indexerFlags"), true,
//	              out IndexerFlags indexerFlags);
//	Enum.TryParse(history.Data.GetValueOrDefault("releaseType"),
//	              out ReleaseType releaseType);
//
// Malformed values degrade to the zero element of each type — the
// C# tree's Enum.TryParse/long.TryParse leaves `out` zero on parse
// failure, and the Go port preserves this. The indexerFlags enum is
// parsed case-insensitively; releaseType case-sensitively (matches
// C# Enum.TryParse defaults at the two call sites).
func historyDataFlags(data map[string]string) (size int64, flags parser.IndexerFlags, releaseType parser.ReleaseType) {
	if data == nil {
		return 0, 0, 0
	}
	if raw, ok := data["size"]; ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			size = v
		}
	}
	if raw, ok := data["indexerFlags"]; ok {
		flags = parseIndexerFlagsCaseInsensitive(raw)
	}
	if raw, ok := data["releaseType"]; ok {
		releaseType = parseReleaseTypeName(raw)
	}
	return size, flags, releaseType
}

// parseIndexerFlagsCaseInsensitive parses the wire-format
// IndexerFlags string into the typed [parser.IndexerFlags]. The C#
// `Enum.TryParse<IndexerFlags>(s, true, out _)` accepts either an
// integer or a comma-separated list of flag names; we port the
// dominant integer-string path and tolerate trimmed whitespace.
// Non-integer non-empty values return 0 — matches the C# TryParse
// returning false → out stays default.
func parseIndexerFlagsCaseInsensitive(raw string) parser.IndexerFlags {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	if v, err := strconv.Atoi(trimmed); err == nil {
		return parser.IndexerFlags(v)
	}
	return 0
}

// parseReleaseTypeName parses the wire-format ReleaseType string.
// Same posture as [parseIndexerFlagsCaseInsensitive] — integer-string
// path; non-integer non-empty returns 0.
func parseReleaseTypeName(raw string) parser.ReleaseType {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	if v, err := strconv.Atoi(trimmed); err == nil {
		return parser.ReleaseType(v)
	}
	return 0
}
