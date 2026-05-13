package notifications

import (
	"fmt"

	"sonarr.tv/sonarr/internal/series"
)

// MetadataLinkType mirrors C# `Notifications/MetadataLinkType.cs:1-19`.
// Used by Telegram + Gotify (and any future provider that surfaces
// metadata links). Ordered to match the C# enum values — DB rows
// persist as integers.
type MetadataLinkType int

// MetadataLinkType values mirror C# enum-name → int.
const (
	MetadataLinkImdb   MetadataLinkType = 0
	MetadataLinkTvdb   MetadataLinkType = 1
	MetadataLinkTvmaze MetadataLinkType = 2
	MetadataLinkTrakt  MetadataLinkType = 3
)

// MetadataLink is the typed (type, label, URL) triple. Mirrors C#
// `NotificationMetadataLink` at `:1-15`.
type MetadataLink struct {
	Type  MetadataLinkType
	Label string
	URL   string
}

// GenerateMetadataLinks ports C# `NotificationMetadataLinkGenerator
// .GenerateLinks` at the static helper file. Walks the configured
// link-type list and emits the matching links the series row's
// identifiers support (ImdbID for IMDb, TvdbID for TVDb + Trakt,
// TvMazeID for TVMaze).
//
// nil series → empty list. Unknown link-type values are silently
// skipped — matches the C# behaviour where the switch ignores
// out-of-range enum values.
func GenerateMetadataLinks(ser *series.Series, requested []int) []MetadataLink {
	if ser == nil {
		return nil
	}
	var out []MetadataLink
	for _, id := range requested {
		switch MetadataLinkType(id) {
		case MetadataLinkImdb:
			if ser.ImdbID != "" {
				out = append(out, MetadataLink{
					Type: MetadataLinkImdb, Label: "IMDb",
					URL: fmt.Sprintf("https://www.imdb.com/title/%s", ser.ImdbID),
				})
			}
		case MetadataLinkTvdb:
			if ser.TvdbID > 0 {
				out = append(out, MetadataLink{
					Type: MetadataLinkTvdb, Label: "TVDb",
					URL: fmt.Sprintf("http://www.thetvdb.com/?tab=series&id=%d", ser.TvdbID),
				})
			}
		case MetadataLinkTrakt:
			if ser.TvdbID > 0 {
				out = append(out, MetadataLink{
					Type: MetadataLinkTrakt, Label: "Trakt",
					URL: fmt.Sprintf("http://trakt.tv/search/tvdb/%d?id_type=show", ser.TvdbID),
				})
			}
		case MetadataLinkTvmaze:
			if ser.TvMazeID > 0 {
				out = append(out, MetadataLink{
					Type: MetadataLinkTvmaze, Label: "TVMaze",
					URL: fmt.Sprintf("http://www.tvmaze.com/shows/%d/_", ser.TvMazeID),
				})
			}
		}
	}
	return out
}
