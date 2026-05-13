package customformats

import (
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// ParseCustomFormatForBlocklist is the Blocklist-driven entry point
// of the CustomFormat matching engine. Mirrors C#
// `CustomFormatCalculationService.ParseCustomFormat(Blocklist, Series)`
// at `CustomFormatCalculationService.cs:61-85`.
//
// # Algorithm (mirrors C# :61-85)
//
//  1. Parse the Blocklist row's SourceTitle via [parser.ParseTitle].
//     The parsed [parser.ParsedEpisodeInfo] supplies the ReleaseTitle
//     + ReleaseGroup fields the spec engine reads.
//  2. Build a [parser.ParsedEpisodeInfo] from series.Title +
//     parser-extracted ReleaseTitle (or SourceTitle when ParseTitle
//     returns nil) + Quality (decoded from the persisted JSON) +
//     Languages + parser-extracted ReleaseGroup.
//  3. Bundle into [CustomFormatInput] with entry.Size +
//     entry.IndexerFlags + entry.ReleaseType.
//  4. Walk [Calculate] against allFormats; matched typed [*Format]
//     values are converted back to []CustomFormat (the persisted-row
//     shape) for symmetry with [ParseCustomFormatForFile].
//
// nil entry returns nil. Empty allFormats returns empty.
//
// Slice 4.BX (C-cleanup [C12] sub-arc C12a).
func ParseCustomFormatForBlocklist(entry *blocklist.Entry, ser *series.Series, allFormats []*Format) []CustomFormat {
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

	episodeInfo := &parser.ParsedEpisodeInfo{
		SeriesTitle:  seriesTitle(ser),
		ReleaseTitle: releaseTitle,
		Quality:      decodeFileQuality(entry.Quality),
		Languages:    languagesFromIDs(entry.Languages),
		ReleaseGroup: releaseGroup,
	}

	var size int64
	if entry.Size != nil {
		size = *entry.Size
	}

	input := CustomFormatInput{
		EpisodeInfo:  episodeInfo,
		Series:       ser,
		Size:         size,
		IndexerFlags: parser.IndexerFlags(entry.IndexerFlags),
		Languages:    languagesFromIDs(entry.Languages),
		ReleaseType:  parser.ReleaseType(entry.ReleaseType),
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
