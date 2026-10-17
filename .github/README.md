# Fixed: Stop re-grabbing a torrent qBittorrent already has

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#6](https://github.com/chodeus/Sonarr/pull/6).

When qBittorrent already holds a torrent, adding it again fails with 409 Conflict. (Sonarr also handles a "Fails." reply, which older qBittorrent versions send; this change covers the 409 only.) Sonarr turns that into "rejected by download client, possible duplicate", logs "Couldn't add release" and records nothing. The next RSS sync makes the same decision and tries again, every sync, until the listing leaves the feed.

It happens when the copy qBittorrent holds sits in a category Sonarr's queue doesn't watch: a torrent Sonarr imported and moved to its post-import category, or one a cross-seed tool added from another tracker. A tracker can also title the same files differently, for example adding "REPACK", so its listing scores as an upgrade over the imported file. The queue's own same-release checks never see it.

## Changes

- `QBittorrent` answers a 409 by asking qBittorrent whether it has that hash (`IsTorrentLoaded`). If it does, it throws `ReleaseAlreadyInDownloadClientException`, a kind of `DownloadClientRejectedReleaseException`, so everything that handled the rejection before still does. If the hash isn't there, or the check fails, the conflict stays a plain rejection.
- `DownloadService` remembers that release for 12 hours (`DuplicateReleaseCache`, in memory; 12 hours is the window `HistorySpecification` gives a recent grab), by its hash and by the listing's GUID on that indexer.
- `AlreadyInDownloadClientSpecification` rejects a remembered release with "<client> already has this release". A listing with a hash matches by hash, whichever indexer lists it. One without a hash matches only the same listing (its GUID on the same indexer and protocol, which an RSS item keeps from sync to sync), so another torrent posted under the same title isn't affected. A listing with neither matches nothing.
- Before rejecting, Sonarr asks the client that refused it whether it still has the torrent. Once you delete it from qBittorrent, the next sync can grab the release again; if the client can't be asked, the release isn't rejected.
- Nothing is written to the blocklist or the database. After 12 hours, or a restart, Sonarr tries once more, and if qBittorrent still has the torrent the rejection starts again.

## Tests

- `QBittorrentFixture`: a 409 for a torrent file and for a magnet link the client already has (the hash it checks is the torrent's), any other 409, and a check that fails.
- `DuplicateReleaseFixture` runs the real `DownloadService`, cache and specification, with only the download client mocked: the same listing on a later sync, the same hash from another indexer, the same title from another indexer, another listing with the same title on the same indexer, the same title with another hash, pushed listings told apart by GUID, a listing with no GUID, the same GUID from another indexer, a usenet listing with the same GUID, a usenet release with the same title, another kind of rejection, and a release nothing refused. It also covers the recheck: the client that refused the torrent is asked about it, a torrent the client no longer has is accepted (and isn't rejected again afterwards), and a client that can't be checked doesn't block the grab.
- The download, decision engine, indexer search and messaging suites pass (1,183 tests), and so does `ContainerFixture`. The 10 that fail (`CompletedDownloadServiceTests` and Deluge reconnect) fail the same way on unmodified develop, on Warns from upstream code those tests don't expect. Each new check was broken on purpose (13 mutations), and its tests failed every time.
