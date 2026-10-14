# New: Match event releases to episodes by title

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#1](https://github.com/chodeus/Sonarr/pull/1).

Series tagged `title-match` can map releases named by event instead of by episode number. `Formula1.2026.Singapore.Grand.Prix`, `Formula1.2026.Round17.Singapore.Race` and `Formula.1.2026x17.Singapore.Qualifying` map to the episode whose TheTVDB title names the same place and session. Without the tag, every series parses exactly as before.

Stock Sonarr reads the year in these names as a season and episode (`2026` becomes S20E26), so every one is rejected as "Unable to identify correct episode(s)".

## How a release is matched

- The 4-digit year picks the season. `Round17`, `R17` or `2026x17` picks the 17th event weekend, and it must agree with the place if one is named.
- Places match TheTVDB's name or the names releases use, such as Italian/Monza for Italy, Dutch/Zandvoort for Netherlands and Barcelona/Catalunya for Barcelona-Catalunya.
- Sessions are read most specific first: Sprint Qualifying/Shootout, Sprint, Qualifying, Practice 1-3/FP1-3, Warm Up and Race. A name with no session word is the race. On a sprint weekend, Practice 1 or a bare Practice maps to TheTVDB's single unnumbered Practice.
- A release is never matched if:
  - it is a show, press conference, notebook, weekend pack, testing, or a sibling series (Academy, F2, F3, Moto2…);
  - it is ambiguous: two places, two sessions, two different years, or a round that disagrees with the place;
  - it names a session number outside 1-3, such as Practice 4;
  - its event hasn't aired. The check allows a day, because TheTVDB times every session at the series' air time, hours after an Asia-Pacific session has run.

## Where it hooks in

- `ParsingService.Map` adopts the corrected `ParsedEpisodeInfo` as soon as the series is known, so the single-episode and season search specifications compare the right numbers. `GetEpisodes` does the same for files.
- The corrected info drops a language that only names the event, so "Italian Grand Prix" no longer marks the release as Italian audio. A release that names the language again keeps it.
- The tag is read through `ITagRepository`, not `ITagService`, because `TagService` depends on the indexer factory and every indexer depends on `ParsingService`.

## Tests

- `EpisodeTitleMatcherFixture` runs against TheTVDB's full 2026 season. It covers 21 real release names that must match, 20 that must not, the language cases, the tag gate and the not-aired guard.
- `GetEpisodesFixture` covers the mapping, the single-episode search specification on a matched release, the file path, and the fallback to parsed numbers.
- The parser, decision engine, download aggregation, episode import and indexer search suites pass (2,492 tests), and so does `ContainerFixture`.

## Summary by CodeRabbit

* **New Features**
  * Added automatic matching of Formula 1 release titles to aired episodes for tagged series, including weekend and session-based titles.
  * When a title matches, episode lookup uses the matched episode; unmatched titles retain the original parsed episode details.
* **Tests**
  * Added coverage for supported title formats, unmatched or unaired events, and language handling.
