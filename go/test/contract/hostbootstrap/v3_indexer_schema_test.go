package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3IndexerSchemaContract pins /api/v3/indexer/schema to the
// C#-recorded golden. 10 providers (BroadcasTheNet / Fanzub / FileList
// / HDBits / IP Torrents / Newznab / Nyaa / Torrent RSS Feed /
// TorrentLeech / Torznab) sorted by ImplementationName ASC. Newznab
// uniquely carries 9 preset rows (DOGnzb / DrunkenSlug / Nzb.life /
// NZBCat / NZBFinder.ws / NZBgeek / nzbplanet.net / SimplyNZBs /
// Tabula Rasa) in its `presets` array; preset entries omit
// implementationName and their own presets array.
func TestV3IndexerSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.indexer.schema"
	checkEmptyJSONContract(t, entryID, apiv3.IndexerSchemaHandler())
}
