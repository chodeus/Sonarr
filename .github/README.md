# New: Change a series' episode order after its files are imported

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#5](https://github.com/chodeus/Sonarr/pull/5).

v5 picks an episode order (Aired, DVD, International, …) when a series is added, and Edit Series greys the setting out once the series has a file. Deleting and re-adding the series doesn't help: files are matched by the season and episode numbers in their names, so a file named in aired order lands on whichever episode holds that slot in the new order. On a long-running anime with aired and DVD orders, a re-add in DVD order would put about a quarter of the files on the wrong episode and more than a quarter on none.

Unlocking the setting isn't enough either. A refresh matches stored episodes to TVDB's by season and episode number, so switching the order rewrites every slot's metadata in place while each file stays on its old slot.

## Changes

- **Episode TVDB IDs.** SkyHook v2 returns each episode's TVDB ID as `metadataId`, and `EpisodeResource` still read `tvdbId`, so every episode refreshed since the v2 switch has `TvdbId` 0 (Kodi `uniqueid`, webhooks and import-list episode matching all read it). The resource now reads `metadataId`.
- **Refresh matches by TVDB ID first** (`RemoteEpisodeMatcher`), then by season and episode number for the rest, so an episode TVDB moves keeps its record, file, history and monitoring. Episodes with ID 0 match by slot as before and pick up their IDs. The dedupe and field copy move out of `RefreshEpisodeService` into the same type, so the preview applies them exactly as a refresh does.
- **File seasons follow their episodes.** `EpisodeFileSeasonService` sets `EpisodeFile.SeasonNumber` to its episodes' lowest season after a refresh. Season statistics, Plex metadata, Trakt and extras read it.
- **XEM** treats a non-aired order as having no mappings, so the scene numbering left over from aired order is cleared instead of used for searches.
- **Preview** (`GET /api/v5/series/{id}/episodeorder?seasonType=`) fetches the new order, matches it, and builds every file's new path with the rename service's own preview. Each file is `unchanged`, `renamed`, `conflict` (two files share a destination, an untracked file is there, files swap places, or it waits on a conflicted file) or `unmatched` (an episode isn't in the new order by TVDB ID). Nothing is saved.
- **`ChangeEpisodeOrder` command** reruns the preview and refuses while episodes with files have TVDB ID 0 or any file is unmatched. It saves the order without the edit event's own refresh, refreshes the series in-line, checks that every file's episodes landed where the preview said, and only then renames. Renaming repeats while files still move, so a file whose destination another file is leaving gets its turn.
- **Edit Series** enables Episode Order for series with files. Choosing another order loads the preview: a summary, Show Changes (existing → new path per file), warnings for a needed refresh, unmatched files and anime orders without absolute numbers, and Rename and Move Files (on by default). Save stays disabled while the preview loads or the change is refused. The series PUT keeps the stored order and the command applies the new one.

The v5 PUT validator and the v3 API are unchanged, so a PUT still can't change the order of a series with files. On Windows the case-only rename guard in the conflict check is untested: path comparison there is case-insensitive, and the tests run on macOS.

## Tests

- `RemoteEpisodeMatcherFixture`: moved episodes, swaps, slot fallback when either ID is 0 or TVDB replaced the episode, an ID match keeping its old slot from another episode, duplicate IDs.
- `RefreshEpisodeServiceFixture`: two real Game of Thrones episodes swapping numbers keep their files.
- `EpisodeFileSeasonServiceFixture`, and `XemServiceFixture` for a non-aired order (clears scene numbering, never fetches XEM).
- `EpisodeOrderChangeServiceFixture` (20 tests): preview numbers and paths, nothing saved, unknown orders, unmatched and refresh-required files, each conflict kind, and the command's refusals, save → refresh → rename order, verification, rename off, and chained renames.
- `SkyHookProxyFixture` asserts real episodes carry a TVDB ID.
- 93 tests across these fixtures pass in Debug. Each of 30 mutations (one per guard or branch) fails its target tests. The branch merges cleanly with the image's other branches in either order.
