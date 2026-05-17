package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3ImportListSchemaContract pins /api/v3/importlist/schema to
// the C#-recorded golden — 10 importlist providers in
// implementationName-ASC order (AniList List → Custom List → MyAnimeList
// → Plex Watchlist → Plex Watchlist RSS → Simkl User Watchlist →
// Sonarr → Trakt List → Trakt Popular List → Trakt User). Each
// carries the per-provider settings field list plus the importlist
// wrapper keys (enableAutomaticAdd / listOrder / listType /
// minRefreshInterval / monitorNewItems / qualityProfileId /
// searchForMissingEpisodes / seasonFolder / seriesType /
// shouldMonitor / tags). C# emits Name/Id as null on schemas
// (WhenWritingNull omits) — our Go struct simply has no Name/ID.
func TestV3ImportListSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.importlist.schema"
	checkEmptyJSONContract(t, entryID, apiv3.ImportListSchemaHandler())
}
