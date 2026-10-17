# Fixed: A series refresh no longer undoes changes saved while it runs

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#7](https://github.com/chodeus/Sonarr/pull/7).

A refresh reads the series, asks TheTVDB for its info, which can take seconds, and then saves the whole series. Anything saved in the meantime is overwritten with what the refresh read first:

- A folder move that finishes during the lookup is undone. The series gets its old path and its pending-move mark back, and Sonarr treats it as mid-move until a restart clears the mark.
- A season monitored or unmonitored during the lookup is switched back, and so are its episodes, because the save applies the season's earlier monitoring again.
- A quality profile, monitoring or episode order change is lost the same way.

The auto-tag step after a refresh has the same problem with a wider window. When the auto-tagging rules change a series' tags, it saves the whole series, and in a full refresh that's the copy read when the refresh started, before any series in the library was looked up.

## Changes

- `RefreshSeriesInfo` looks up TheTVDB with the series it read first, then reads the series again and applies the lookup to that copy. It saves only the fields the refresh sets (`RefreshedFields`: the TheTVDB metadata, the seasons and the auto-tags) through the new `SeriesService.UpdateSeriesFields`. A path, pending move, profile, monitoring or episode order saved meanwhile stays. Existing seasons keep the monitoring stored for them, and a refresh no longer re-monitors episodes.
- The path is still tidied (made absolute and, on Windows, given its real casing), but it's saved only when that changes it and no move is pending. A pending move owns the path until it finishes.
- A series TheTVDB no longer has is marked deleted by saving its status alone.
- `UpdateTags` reads the series by ID, applies the auto-tag changes and saves the tags alone. It still publishes `SeriesEditedEvent`, with the stored series as the old one, so queued and pending downloads pick up the new tags.

## Tests

- `RefreshSeriesConcurrentChangesFixture` runs the refresh against the real `SeriesService` and database, with only TheTVDB, auto-tagging and the disk scan mocked. Another writer saves the series during the lookup, or after the refresh has read it back:
  - a move that finishes: the new path is kept with no pending mark, and the metadata and seasons are refreshed;
  - a season unmonitored: it stays unmonitored, and its episodes aren't re-monitored;
  - a profile and monitoring change;
  - a series TheTVDB no longer has;
  - a failed lookup whose auto-tags change;
  - a full refresh that read every series first.
- It also checks that the auto-tags are saved before `SeriesUpdatedEvent` goes out, that a tidied path is saved, and that a pending move's path is left alone.
- `RefreshSeriesServiceFixture` checks the field-level save in place of `UpdateSeries`.
- The full Core suite passes (5,421 tests), and so does `ContainerFixture`. The 12 that fail (9 `CompletedDownloadServiceTests`, Deluge reconnect and two `InitialUserServiceFixture` seed tests) fail the same way on unmodified develop, on Warns from upstream code those tests don't expect. 8 of the 10 new tests fail on the code before this change; the other two check behaviour it keeps (auto-tags saved before `SeriesUpdatedEvent`, and a tidied path saved). Each guarantee was broken on purpose (12 mutations), and a test failed every time.
