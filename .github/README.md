# New: Match event releases to episodes by title

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#1](https://github.com/chodeus/Sonarr/pull/1).

Series tagged `title-match` can map releases named by event instead of by episode number. `Formula1.2026.Singapore.Grand.Prix`, `Formula1.2026.Round17.Singapore.Race` and `Formula.1.2026x17.Singapore.Qualifying` map to the episode whose TheTVDB title names the same place and session. Without the tag, every series parses exactly as before.

Stock Sonarr reads the year in these names as a season and episode (`2026` becomes S20E26), so every one is rejected as "Unable to identify correct episode(s)".

## How a release is matched

- The 4-digit year picks the season. `Round17`, `R17` or `2026x17` picks the 17th event weekend, and it must agree with the place if one is named.
- Places match TheTVDB's name or the names releases use, such as Italian/Monza for Italy, Dutch/Zandvoort for Netherlands and Barcelona/Catalunya for Barcelona-Catalunya. A place name that is also a language (Italian, Dutch, Spanish, German…) names the event only right before "Grand Prix" or "GP". Anywhere else it is the audio language, so `Singapore.Race…German` stays German and `Gran.Premio.del.Belgio…ITALIAN` is not Monza.
- Sessions are read most specific first: Sprint Qualifying/Shootout, Sprint, Qualifying/Qualification, Practice 1-3/FP1-3, Warm Up and Race. A name with no session word is the race only if it says "Grand Prix" or "GP"; otherwise it's a programme TheTVDB doesn't list, such as Tech Talk or Grid Walk. On a sprint weekend, Practice 1 or a bare Practice maps to TheTVDB's single unnumbered Practice.
- A release that matches no event maps to no episode. Sonarr doesn't fall back to the parser's numbers, which read `2026x17` as S2026E17 (China Sprint) whatever the event. The exceptions are explicit numbering such as `S2026E94`, which Sonarr's own file names use, and a special Sonarr found by its TheTVDB title. Season packs therefore don't map.
- A release is never matched if:
  - it is a show, press conference, notebook, weekend pack, testing, or a sibling series (Academy, F2, F3, Moto2…);
  - it is ambiguous: two places, two sessions, two different years, or a round that disagrees with the place;
  - it names a session number outside 1-3, such as Practice 4;
  - its event hasn't aired. The check allows a day, because TheTVDB times every session at the series' air time, hours after an Asia-Pacific session has run.

## Where it hooks in

- `ParsingService.Map` adopts the corrected `ParsedEpisodeInfo` as soon as the series is known, so the single-episode and season search specifications compare the right numbers. `GetEpisodes` does the same for its other callers.
- Imports replace the file, folder and download-client parses once, in `AggregationService.Augment`, after the scene name is set (it reads `FullSeason`). Every aggregator and import check then sees the same episode and language as the release did, including a daily-dated name such as `Formula1.2026.10.11.Singapore.Grand.Prix`, which the series type check used to reject.
- The corrected info drops a language that only names the event, so "Italian Grand Prix" no longer marks the release, or its imported file, as Italian audio. A release that names the language again keeps it. Release and import scores therefore agree.
- Subtitles and other extras that don't share the video's name pair with it by episode; their names are read the same way, so `2026x17…Qualifying.English.srt` pairs with the qualifying file and not the race.
- The tag is read through `ITagRepository`, not `ITagService`, because `TagService` depends on the indexer factory and every indexer depends on `ParsingService`. The cached tag is cleared on `TagsUpdatedEvent`, so creating or renaming `title-match` takes effect at once.

## Tests

- `EpisodeTitleMatcherFixture` runs against TheTVDB's full 2026 season, through the real parser (a title the parser rejects fails the test). It covers 22 release names that must match, 25 that must map to no episode, explicit numbering, a special found by its title, the language cases, the tag gate and the not-aired guard.
- `EventTitleMappingFixture` runs the real parser, `ParsingService`, matcher, import aggregation and extras services, with only the database mocked. Every must-match name goes through both the release path and the import path. It also covers a daily-dated file, a file Sonarr named, audio languages that aren't the event's, a language word alone, names with no session word, no fallback to parsed numbers, the release and import languages of an "Italian Grand Prix" download, a special found by its title, extras named unlike their video, and creating the tag.
- `GetEpisodesFixture` covers the mapping, the single-episode search specification on a matched release, the file path, and the fallback to parsed numbers for a series without the tag.
- The parser, decision engine, download, extras, media file, indexer search and messaging suites pass (3,543 tests), and so does `ContainerFixture`. The 9 that fail (`CompletedDownloadServiceTests` and Deluge reconnect) fail the same way on unmodified develop, on Warns from upstream code. Each new rule was broken on purpose (14 mutations), and its tests failed every time.

## Summary by CodeRabbit

* **New Features**
  * Added automatic matching of Formula 1 release titles to aired episodes for tagged series, including weekend and session-based titles.
  * When a title matches, episode lookup uses the matched episode; unmatched titles retain the original parsed episode details.
* **Tests**
  * Added coverage for supported title formats, unmatched or unaired events, and language handling.
