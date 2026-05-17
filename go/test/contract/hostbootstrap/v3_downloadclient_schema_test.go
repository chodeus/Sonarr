package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3DownloadClientSchemaContract pins /api/v3/downloadclient/
// schema to the C#-recorded golden. 20 providers (Aria2 / Deluge /
// Torrent+UsenetDownloadStation / Flood / TorrentFreeboxDownload /
// Hadouken / NZBGet / NZBVortex / Pneumatic / qBittorrent / RQBit /
// rTorrent / SABnzbd / TorrentBlackhole / Transmission / Tribler /
// UsenetBlackhole / uTorrent / Vuze) sorted by ImplementationName
// ASC with the Torrent-flavoured DownloadStation before the Usenet
// sibling (stable tiebreaker matches C# DI scan order). Six impls
// carry static `message` banners (DownloadStation 2FA warning,
// Flood seed-criteria info, rTorrent seed-criteria info, Tribler
// experimental warning, uTorrent malware warning).
func TestV3DownloadClientSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.downloadclient.schema"
	checkEmptyJSONContract(t, entryID, apiv3.DownloadClientSchemaHandler())
}
