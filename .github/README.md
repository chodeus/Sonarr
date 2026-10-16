# Fixed: Import blocked season packs once their titles arrive

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#3](https://github.com/chodeus/Sonarr/pull/3).

A season pack grabbed as soon as it airs is held back while its episode titles are still TBA (Media Management → Episode Title Required). It should import by itself once a series refresh brings in the titles. Three bugs stopped that from happening.

**The queued download lost its series.** On series and episode events (edit, bulk edit, add, delete, episode removed), `TrackedDownloadService.UpdateCachedItem` remapped each queued download from the download client's title only. `TrackDownload` falls back to the grab history when that title doesn't match a series, but the update path had its own copy of the mapping without that fallback. A torrent named `The.Diplomat.S04…` for the series "The Diplomat (US)" was mapped when first tracked, then lost its series and episodes after the next series event. The queue showed it with no series, so nothing covered those episodes and the season was grabbed again from another release.

**The download then stuck in Importing.** With no episodes mapped, `TrackedDownloadAlreadyImported.IsImported` returned true (`All` over an empty list). `VerifyImport` took the "already imported" path and read `RemoteEpisode.Series.Id` from a null series. `ProcessMonitoredDownloads` logs that exception at Debug, and the download stays in `Importing`, a state `Check` never retries. If a series had still been mapped, the same path would have marked the download imported with nothing imported.

**"Only for Bulk Season Releases" looked up the wrong season.** `EpisodeTitleSpecification` passed the episode number to `GetEpisodesBySeason`, so S04E01 was compared against season 1 and S04E04 against season 4. The existing tests mock the lookup with `It.IsAny<int>()`, so they passed either way.

## Changes

- `TrackDownload`'s mapping (download history read, title, grab history fallback, release info, aggregation, custom formats) moves into `MapRemoteEpisode`, which returns the download history so `TrackDownload` still sets the state from it. `UpdateCachedItem` calls the same method. Queued downloads keep their series, episodes and custom format score after series events.
- The mapping's existing try/catch now covers the update path as well, so no exception leaves a series event handler and skips the remaining downloads. A failure keeps the result of the last step that finished:
  - download history read or title lookup fails: the previous mapping stays;
  - grab history lookup fails: the title step's result stays, which is no mapping when the title doesn't parse, as before. That lookup throws when the data is gone (a series deleted, or an episode removed, while its grab history still exists), and keeping the previous mapping there would point the download at them.
- `IsImported` returns false when no episodes are mapped.
- `EpisodeTitleSpecification` looks up the episode's season.

## Tests

- `TrackedDownloadServiceFixture`: a download mapped through its grab history keeps its series and episodes after `SeriesEditedEvent`; a failing title lookup or download history read keeps the previous mapping; a `SeriesDeletedEvent` whose history lookup throws leaves the title mapping and doesn't throw.
- `TrackedDownloadAlreadyImportedFixture`: history but no mapped episodes is not imported.
- `ImportFixture`: a download with no series or episodes, grab history and an Unknown Series result ends `ImportBlocked` without `DownloadCompletedEvent`, using the real `TrackedDownloadAlreadyImported`.
- `EpisodeTitleSpecificationFixture`: S04E01 with a TBA title, in a season of 8 episodes that aired together, is rejected while season 1 aired years earlier.
- Run with `dotnet test` over these four fixtures (58 tests): all new tests pass, and each fix undone on its own makes its tests fail. Eight existing `CompletedDownloadServiceTests` fail on an unexpected "Import blocked" Warn, which upstream ae7a61c10 added to `SetStateToImportBlocked` without updating them; this PR leaves them as they are.

## Summary by CodeRabbit

* **Bug Fixes**
  * Downloads without mapped episodes are no longer incorrectly treated as already imported, and imports are blocked when a download cannot be matched to a series.
  * Download tracking better preserves mappings from download history when a series is edited or unavailable, including when remapping or history lookups fail.
  * Stale episode mappings are cleared when a title can’t be parsed.
  * Bulk-season releases are checked against the correct season, preventing mismatched episodes from being accepted.
