# Fixed: Import blocked season packs once their titles arrive, and don't grab a queued release again

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#4](https://github.com/chodeus/Sonarr/pull/4), which carries [#3](https://github.com/chodeus/Sonarr/pull/3) too: this branch was stacked on #3's, so the two ship as one.

## Blocked season packs

A season pack grabbed as soon as it airs is held back while its episode titles are still TBA (Media Management → Episode Title Required). It should import by itself once a series refresh brings in the titles. Three bugs stopped that from happening.

**The queued download lost its series.** On series and episode events (edit, bulk edit, add, delete, episode removed), `TrackedDownloadService.UpdateCachedItem` remapped each queued download from the download client's title only. `TrackDownload` falls back to the grab history when that title doesn't match a series, but the update path had its own copy of the mapping without that fallback. A torrent named `The.Diplomat.S04…` for the series "The Diplomat (US)" was mapped when first tracked, then lost its series and episodes after the next series event. The queue showed it with no series, so nothing covered those episodes and the season was grabbed again from another release.

**The download then stuck in Importing.** With no episodes mapped, `TrackedDownloadAlreadyImported.IsImported` returned true (`All` over an empty list). `VerifyImport` took the "already imported" path and read `RemoteEpisode.Series.Id` from a null series. `ProcessMonitoredDownloads` logs that exception at Debug, and the download stays in `Importing`, a state `Check` never retries. If a series had still been mapped, the same path would have marked the download imported with nothing imported.

**"Only for Bulk Season Releases" looked up the wrong season.** `EpisodeTitleSpecification` passed the episode number to `GetEpisodesBySeason`, so S04E01 was compared against season 1 and S04E04 against season 4. The existing tests mock the lookup with `It.IsAny<int>()`, so they passed either way.

### Changes

- `TrackDownload`'s mapping (download history read, title, grab history fallback, release info, aggregation, custom formats) moves into `MapRemoteEpisode`, which returns the download history so `TrackDownload` still sets the state from it. `UpdateCachedItem` calls the same method. Queued downloads keep their series, episodes and custom format score after series events.
- The mapping is built on its own objects and applied once it's complete, so another thread reading the queue sees the old mapping or the new one, never a half-built one with no series.
- Its error handling covers the update path as well, so no exception leaves a series event handler and skips the remaining downloads. A remap that fails keeps the download's previous mapping and status messages, so an import-blocked download keeps its series and the reason it's blocked. A new download that can't be mapped shows the warning, as before.
- The grab history fallback maps the grabbed episodes that still exist. TheTVDB can remove a grabbed episode (a TBA placeholder, say) while the pack waits, and looking up a missing ID used to throw, leaving the download with no series for good. The series is looked up without throwing, so a download whose series was deleted, imported or not, is left with no series instead of pointing at it.
- `IsImported` returns false when no episodes are mapped.
- `EpisodeTitleSpecification` looks up the episode's season.

### Tests

- `TrackedDownloadServiceFixture`: a download mapped through its grab history keeps its series and episodes after `SeriesEditedEvent`; a failing title lookup or download history read keeps the previous mapping; a `SeriesDeletedEvent` for a series that's gone leaves the title mapping and doesn't throw.
- `TrackedDownloadServiceRemapFixture` runs the remap through the real `ParsingService`, `SeriesService`, `EpisodeService` and database: an import-blocked season pack keeps its series, the 7 remaining episodes and its blocked reason when a refresh removes one of its 8; a remap that fails partway keeps the previous mapping and reason; an imported download whose series is deleted is left with no series; a reader during a remap sees the previous mapping; a new download that can't be mapped shows the warning.
- `TrackedDownloadAlreadyImportedFixture`: history but no mapped episodes is not imported.
- `ImportFixture`: a download with no series or episodes, grab history and an Unknown Series result ends `ImportBlocked` without `DownloadCompletedEvent`, using the real `TrackedDownloadAlreadyImported`.
- `EpisodeTitleSpecificationFixture`: S04E01 with a TBA title, in a season of 8 episodes that aired together, is rejected while season 1 aired years earlier.

## The same release grabbed again

A queued download is scored from its download client name. When that name lacks tokens the grabbed title had, the queued download scores lower than the grab did, and the same release from another feed passes the queue check as an upgrade and is grabbed again. For example, a release announced as `Series.Title.S01E05.1080p.WEB-DL.DDPA5.1.H.264-Group` whose torrent is named `…DDP5.1…` loses its Atmos score in the queue, and a folder torrent named `…DV.HDR…` holding `…DV.HDR10+….mkv`, grabbed under the file name, loses its HDR10+ score.

The history check only covers RSS, and only for 12 hours after the grab, so a search, or a download older than 12 hours, has nothing else stopping a duplicate.

No score is changed here. A duplicate is refused because it is the same release, and the torrent can carry the grabbed name so the queue scores what was actually grabbed.

### Changes

- `QueueSpecification` refuses a release that is the same as a queued download: the same torrent hash as any queued download, or the same title as the client name or grabbed title of a queued download for the same series. Both run before the episode check, because the queue maps episodes from the client name, which can name other episodes than the grabbed title. Titles may differ only in case, separators, accent encoding and a video file extension (`ReleaseTitleExtensions.IsSameReleaseTitle`); `+` counts, so `HDR10` and `HDR10+` stay different, and a blank title never matches. A different release still upgrades by score as before. New rejection reason `QueueSameRelease` (also in the v5 OpenAPI enum).
- The remap keeps the grab event with the rest of the mapping, and `QueueService` copies its title to the new `Queue.GrabbedTitle`. The tracked release title is unchanged, because language detection reads it.
- New qBittorrent setting "Rename to Release Title", off by default. `GrabbedTitleRenameService` handles `TrackedDownloadRefreshedEvent` asynchronously: until a torrent imports (while it downloads, or while its import is blocked), when its grabbed title has a higher quality, or the same quality and a higher custom format score, than its client name, it asks the client to rename the torrent to the grabbed title (`torrents/rename`, API v2 only).
  - It first asks the client whether it can rename (`IDownloadClient.CanRenameItem`: false by default, and for qBittorrent the setting is on and the API is v2), so with the setting off it does no history lookup or score work, and sets no retry delay.
  - Only the torrent name changes. Sonarr takes the output path from qBittorrent's content path, so imports find the files as before. A single-episode import uses the client name as its scene name, so it then scores the grabbed title, as a usenet download already does.
  - Skipped when the grab history names another series or other episodes than the client name maps to, so a rename never moves a download to other episodes.
  - Skipped when the grab matched the series by ID: the import finds the series by the client name, and an indexer title Sonarr can't parse to the series would block it.
  - Skipped when the names already match, because the two scores can still differ after a rename (the client's size against the indexer's), and for downloads whose client couldn't be reached.
  - Attempts for one download are 15 minutes apart, so events republished with the old name before the next refresh don't rename twice, and a failing client isn't retried on every refresh.

### Tests

- `QueueSpecificationFixture`: the same hash in a different case; a torrent without a hash compared by title; the client name with spaces, underscores or `.mkv`; the grabbed title; a different, better release still accepted; blank titles never matched; a hash matched on a queued download with no mapped episodes, but not a failed-pending one; the grabbed title matched when the client name maps to another episode, but not under another series.
- `ReleaseTitleExtensionsFixture`: titles that differ only in separators, case, extension or composed and decomposed accents match; a title with invalid UTF-16 doesn't throw; `DDPA5.1`/`DDP5.1`, `REPACK`, `HDR10`/`HDR10+`, `DD2.0`/`DD+2.0` and an extra word don't.
- `TrackedDownloadServiceFixture`: the grab event is kept and the release title still comes from the client name. `QueueServiceFixture`: queue items carry the grabbed title.
- `GrabbedTitleRenameServiceFixture`: renames on a higher score or quality; not on an equal or lower score, nor a lower quality with a higher score; import-blocked yes; import pending, importing and failed-pending no; no usenet, no unreachable client, no grab history, no partly mapped download, no ID-matched grab; no grab history for another series, other episodes, only some of the episodes or none, while a multi-episode grab for the same episodes renames; names that already match; a republished event renames once; attempts are kept per download client; a failure waits; one failing download doesn't stop the rest; a client that can't rename gets no history lookup or score work, and renames on the next refresh once it can. Release builds don't fail a test on an unexpected warning, so these assert no warnings.
- `QBittorrentRenameFixture`: renames with a lower-case hash when the setting is on; nothing when it is off or on API v1; a rejected rename is left to the caller.

## Both

- The full Core suite passes (5,488 tests) apart from 12 that fail the same way on unmodified develop (`CompletedDownloadServiceTests`, Deluge reconnect and two `InitialUserServiceFixture` seed tests), and so does `ContainerFixture`. An upstream migration test with a one-second window (`should_not_process_file_with_empty_language_tags`) failed once on timing and passed on three reruns.
- Each guard and comparison was broken on purpose (45 mutations for the same-release changes, 5 for the remap, 2 for the rename capability check), and every one failed at least one test.
