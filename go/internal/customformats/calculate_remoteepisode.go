package customformats

import (
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// ParseCustomFormatForRemoteEpisode is the RemoteEpisode-driven entry
// point of the CustomFormat matching engine. Mirrors C#
// `CustomFormatCalculationService.ParseCustomFormat(RemoteEpisode, long)`
// at `CustomFormats/CustomFormatCalculationService.cs:36-49`.
//
// Takes the relevant RemoteEpisode-flat fields directly (instead of a
// `*decisionengine.Subject` pointer) because the decisionengine
// package itself imports the customformats package — a Subject-typed
// parameter would create an import cycle. Callers in cmd/sonarr +
// the API layer hold the Subject and pass the lifted fields.
//
// The fn handles the Parse-handler call shape (size=0, IndexerFlags=0,
// no decision-engine pipeline runs before us) and the future
// ManualImport / RSS-decision call shapes (where size + IndexerFlags
// are non-zero from the upstream release).
//
// Empty allFormats returns nil; nil parsedInfo returns nil (the
// Calculate engine would NPE otherwise since every Specification
// reads EpisodeInfo).
//
// J345.
func ParseCustomFormatForRemoteEpisode(
	parsedInfo *parser.ParsedEpisodeInfo,
	ser *series.Series,
	languages []language.Language,
	indexerFlags parser.IndexerFlags,
	size int64,
	allFormats []*Format,
) []CustomFormat {
	if parsedInfo == nil || len(allFormats) == 0 {
		return nil
	}
	input := CustomFormatInput{
		EpisodeInfo:  parsedInfo,
		Series:       ser,
		Size:         size,
		IndexerFlags: indexerFlags,
		Languages:    languages,
		ReleaseType:  parsedInfo.ReleaseType(),
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
