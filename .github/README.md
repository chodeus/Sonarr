# Fixed: A series refresh no longer undoes changes saved while it runs

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#7](https://github.com/chodeus/Sonarr/pull/7).

A refresh reads the series, asks TheTVDB for its info, which can take seconds, and then saves the whole series. Anything saved in the meantime is overwritten with what the refresh read first:

- A folder move that finishes during the lookup is undone. The series gets its old path and its pending-move mark back, and Sonarr treats it as mid-move until a restart clears the mark.
- A season monitored or unmonitored during the lookup is switched back, and so are its episodes, because the save applies the season's earlier monitoring again.
- A quality profile, monitoring or episode order change is lost the same way.

The auto-tag step after a refresh has the same problem with a wider window. When the auto-tagging rules change a series' tags, it saves the whole series, and in a full refresh that's the copy read when the refresh started, before any series in the library was looked up.

The episode refresh that follows does the same on a smaller scale. It reads the series' episodes, copies TheTVDB's fields onto them and saves every column, so an episode monitored or unmonitored, linked to a file by an import, or searched in between is put back. That gap is only in-memory work, milliseconds rather than the seconds of the lookup.

## Changes

- `RefreshSeriesInfo` looks up TheTVDB with the series it read first, then reads the series again and applies the lookup to that copy. It saves only the fields the refresh sets (`RefreshedFields`: the TheTVDB metadata and the seasons) through the new `SeriesService.UpdateSeriesFields`. A path, pending move, profile, monitoring or episode order saved meanwhile stays. Existing seasons take their monitoring from the second read, so a change saved during the lookup stays too, and a refresh no longer re-monitors episodes. Between the second read and the save, the refresh only applies the lookup in memory and tidies the path.
- The lookup only fits the TheTVDB ID, language and episode order it asked for. If any of them changed while it ran, the refresh looks the series up once more with the new values; if they change again, the series is left for the next refresh.
- The path is still tidied (made absolute and, on Windows, given its real casing), but it's saved only when that changes it and no move is pending. A pending move owns the path until it finishes.
- The episode refresh saves only the columns it sets on an existing episode (`RefreshEpisodeService.RefreshedFields`: TheTVDB's numbering, title, overview, air dates, runtime, finale type, ratings and images) through the new `EpisodeService.UpdateEpisodeFields`. Monitoring, the file link, the last search time and scene numbering saved meanwhile stay. New episodes are still inserted whole.
- A series TheTVDB no longer has is marked deleted by saving its status alone.
- Auto-tags are worked out after the refresh has saved, by `UpdateTags`. It works out the changes from a read of the series, applies the tags added and removed to a fresh read and saves the tags alone, so a tag edit saved while the rules ran stays. It then publishes `SeriesEditedEvent` with the stored series as the old one, so the UI and queued and pending downloads pick up the new tags. Before, a refresh that succeeded saved its tag changes without that event.
- The rescan after a refresh reads the series again, after the tag step, so a move that finished meanwhile is scanned at its new path. It used to scan the copy read when the refresh started, and a scan of a folder that no longer exists drops every episode-file record.
- A series deleted while a refresh runs gets no tag step and no rescan, and the refresh goes on to the next series, as it did before this change.

## Tests

- `RefreshSeriesConcurrentChangesFixture` runs the refresh against the real `SeriesService` and database, with only TheTVDB, auto-tagging and the disk scan mocked. Another writer saves the series during the lookup, or after the refresh has read it back:
  - a move that finishes: the new path is kept with no pending mark, and the metadata and seasons are refreshed;
  - a season unmonitored: it stays unmonitored, and its episodes aren't re-monitored;
  - a profile and monitoring change;
  - a series TheTVDB no longer has;
  - a move that finishes while the auto-tags are worked out: the rescan uses the new path;
  - a tag added while the auto-tags are worked out: it stays;
  - an episode order change: the series is looked up again with the new order, or left alone when the order keeps changing;
  - a failed lookup whose auto-tags change;
  - a full refresh that read every series first.
- It also checks that auto-tag changes are saved and announced with `SeriesEditedEvent`, that tags the rules take off are removed, that a tidied path is saved, that a pending move's path is left alone, and that a refresh carries on past a series deleted after a full refresh read the library or during its own lookup, without rescanning it.
- `should_save_every_field_the_lookup_sets` guards `RefreshedFields`. Its lookup differs from the stored series in every field, and after the refresh the series it announced must match the stored row in every property. A field `RefreshSeriesInfo` sets but doesn't list, for example one added upstream, fails it by name.
- `RefreshSeriesServiceFixture` checks the field-level save in place of `UpdateSeries`.
- `RefreshEpisodeConcurrentChangesFixture` runs the episode refresh against the real `EpisodeService` and database. Another writer changes an episode's monitoring, file link and last search time after the refresh has read it: all three stay, and the TheTVDB fields are refreshed. `should_save_every_column_the_refresh_sets` guards `RefreshedFields` the same way: a column the refresh changes but doesn't save fails it by name. `RefreshEpisodeServiceFixture` checks the field-level save in place of `UpdateMany`.
- The full Core suite passes (5,430 tests), and so does `ContainerFixture`. The 12 that fail (9 `CompletedDownloadServiceTests`, Deluge reconnect and two `InitialUserServiceFixture` seed tests) fail the same way on unmodified develop, on Warns from upstream code those tests don't expect. 15 of the 19 new tests fail on the code before this change; the other four check behaviour it keeps (tags the rules take off, every looked-up field saved, every refreshed episode column saved, and a tidied path saved). Each guarantee was broken on purpose (22 mutations), and a test failed every time.
