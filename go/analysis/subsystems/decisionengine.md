# DecisionEngine

**Source:** `src/NzbDrone.Core/DecisionEngine/**`
**Target Go package:** `go/internal/decisionengine/`
**Status:** drafted

## Purpose
DecisionEngine is the gate between "we found a release on an indexer" and "we hand it to a download client." It takes parsed `ReleaseInfo` reports, maps them to `RemoteEpisode` (series + episodes + parsed quality + custom formats + scene mapping), then runs each candidate through an ordered pipeline of allow/reject **specifications**. The output is a `DownloadDecision` — either approved (zero rejections) or rejected with one or more typed `DownloadRejection` reasons. A second stage (`DownloadDecisionPriorizationService`) sorts approved decisions per series so the best release is grabbed first. Specifications fall into several conceptual buckets: profile/quality gating, size/age constraints, upgrade-vs-disk/queue/history checks, search-target validation, and protocol/indexer rules.

## Pipeline shape

**Inputs:** `List<ReleaseInfo>` (raw indexer rows). Optional `SearchCriteriaBase` (RSS = null; search has it).

**Callers:**
- `Indexers/RssSyncService.cs:45` — calls `GetRssDecision(reports)` after fetching the RSS feed merge from all indexers.
- `IndexerSearch/ReleaseSearchService.cs:549` — calls `GetSearchDecision(reports, criteriaBase)` for every search type (single episode, season, anime, full-series, daily).

**Per-report flow (`DownloadDecisionMaker.GetDecisions`, `DownloadDecisionMaker.cs:58–178`):**

1. `Parser.Parser.ParseTitle(report.Title)` → `ParsedEpisodeInfo`.
2. If null/special, fall back to `IParsingService.ParseSpecialEpisodeTitle` using TVDB/TVRage/IMDb hints.
3. `IParsingService.Map(parsedEpisodeInfo, ids, searchCriteria)` → `RemoteEpisode { Series, Episodes, ParsedEpisodeInfo, ... }`.
4. Stamp `ReleaseSource` (Rss / ReleasePush / Search / UserInvokedSearch / InteractiveSearch).
5. Short-circuit decisions before specs:
   - `Series == null` and a scene mapping resolves the title to another series → `MatchesAnotherSeries`.
   - `Series == null` otherwise → `UnknownSeries`.
   - `Episodes.Empty()` → `UnknownEpisode`.
6. Otherwise call `IRemoteEpisodeAggregationService.Augment` to fill scene mapping, languages, release group, etc.
7. Compute custom formats: `_formatCalculator.ParseCustomFormat(remoteEpisode, size)` and the score via `qualityProfile.CalculateCustomFormatScore`.
8. `GetDecisionForReport` runs specifications grouped by `SpecificationPriority`, ordered ascending.

**Spec evaluation (`DownloadDecisionMaker.cs:180–219`):** specifications are grouped by `Priority` (`Default = Parsing = Database = 0`, `Disk = 1`). Within each priority group, **all** specs are evaluated and rejections collected. **If any rejections come back at a priority level, the next priority level is skipped** — this is the only short-circuit. So a Default-priority rejection prevents Disk-priority specs (FreeSpace, DeletedEpisodeFile) from running and flagging additional reasons. Inside a priority group there is no ordering guarantee — DI registration order. Exceptions inside a spec become `DecisionError` rejections (the report is never silently dropped).

If `searchCriteria != null` and parsing failed, an `UnableToParse` decision is still emitted so search UIs see the unparseable row.

**Outputs:** `List<DownloadDecision>`. Each carries a `RemoteEpisode` plus zero-or-more `DownloadRejection { Reason, Message, Type, SpecType }`. `Approved` = no rejections; `TemporarilyRejected` = all rejections are `RejectionType.Temporary`; `Rejected` = at least one `Permanent`.

**Prioritization (`DownloadDecisionPriorizationService.cs`):** groups decisions by `Series.Id`, sorts each group descending using `DownloadDecisionComparer`, then unions the `Series == null` decisions back at the end. This is run by `DownloadProcessingService` / `ProcessDownloadDecisions` against the approved set before grabbing.

**`DownloadDecisionComparer.cs:30–44`** ordered tiebreakers (first non-zero wins):
1. Quality profile index, then revision (revision skipped if `DownloadPropersAndRepacks == DoNotPrefer`).
2. Custom-format score.
3. Preferred protocol (per delay profile + series tags).
4. Episode count: full-season > single, except for Anime where the lower count wins.
5. Lowest episode number wins (descending compare on min episode number).
6. Indexer priority (lower numeric value = higher priority).
7. If both torrent: log10(seeders) then log10(peers).
8. If both usenet: age buckets — <1h gets 1000, ≤24h gets 100, ≤7d gets 10, otherwise `-log10(age)` so newer wins after the buckets.
9. Size: closest to `runtime * preferredSize` (rounded to 200 MB), otherwise largest wins (rounded to 200 MB).

## Public types
| Type | File | Kind | Notes |
|------|------|------|-------|
| `IMakeDownloadDecision` | `DownloadDecisionMaker.cs:18` | interface | `GetRssDecision`, `GetSearchDecision` |
| `DownloadDecisionMaker` | `DownloadDecisionMaker.cs` | service | orchestrates parsing → spec pipeline |
| `DownloadDecision` | `DownloadDecision.cs` | model | `RemoteEpisode`, `IEnumerable<DownloadRejection>`, `Approved`/`Rejected`/`TemporarilyRejected` |
| `DownloadRejection` | `DownloadRejection.cs` | model | `Rejection<DownloadRejectionReason>` |
| `DownloadRejectionReason` | `DownloadRejectionReason.cs` | enum | 79 values, used for telemetry/UI grouping |
| `DownloadSpecDecision` | `DownloadSpecDecision.cs` | DTO | `Accept()` / `Reject(reason, msg, args…)` returned by every spec |
| `Rejection<T>` | `Rejection.cs` | base | reason + message + `RejectionType` |
| `RejectionType` | `RejectionType.cs` | enum | `Permanent = 0`, `Temporary = 1` |
| `SpecificationPriority` | `SpecificationPriority.cs` | enum | `Default=Parsing=Database=0`, `Disk=1` |
| `UpgradeableRejectReason` | `UpgradeableRejectReason.cs` | enum | `None`, `BetterQuality`, `BetterRevision`, `QualityCutoff`, `CustomFormatScore`, `CustomFormatCutoff`, `MinCustomFormatScore`, `UpgradesNotAllowed` |
| `ReleaseDecisionInformation` | `ReleaseDecisionInformation.cs` | DTO | `PushedRelease`, `SearchCriteria` — passed to every spec |
| `IDownloadDecisionEngineSpecification` | `Specifications/IDownloadDecisionEngineSpecification.cs` | interface | `Type`, `Priority`, `IsSatisfiedBy(subject, info)` |
| `IUpgradableSpecification` / `UpgradableSpecification` | `Specifications/UpgradableSpecification.cs` | service | shared upgrade-decision helper used by Queue/History/Disk/Repack/AnimeVersion/Delay |
| `IPrioritizeDownloadDecision` / `DownloadDecisionPriorizationService` | `DownloadDecisionPriorizationService.cs` | service | per-series sorting via `DownloadDecisionComparer` |
| `DownloadDecisionComparer` | `DownloadDecisionComparer.cs` | comparer | ordered tiebreakers above |
| `IRejectWithReason` | `IRejectWithReason.cs` | interface | one-property contract used by validation specs outside this subsystem |

## Specification inventory (32 concrete specs)

Legend:
- **Pri**: `D` = Default/Parsing/Database (0), `Disk` = 1.
- **Type**: `P` = Permanent, `T` = Temporary.
- **Phase**: `RSS` = always; `Search` = always; `Both` = always; `RSS-only` = skipped when `SearchCriteria != null`; `Search-only` = skipped when `SearchCriteria == null`.

| # | Spec | File | Pri | Type | Phase | Accepts when… | Rejection reason(s) |
|---|------|------|-----|------|-------|---------------|----------------------|
| 1 | AcceptableSizeSpecification | `Specifications/AcceptableSizeSpecification.cs` | D | P | Both | size = 0 OR Special OR within `MinSize..MaxSize` × runtime; runtime falls back to series runtime, and to 45 min if a pilot ep aired within 24h | `BelowMinimumSize`, `AboveMaximumSize`, `UnknownRuntime` |
| 2 | AirDateSpecification | `Specifications/AirDateSpecification.cs` | D (Database) | P | Both | no AirDateRestriction profile OR `releaseDate >= airDate + gracePeriod` for every ep | `BeforeAirDate` |
| 3 | AlreadyImportedSpecification | `Specifications/AlreadyImportedSpecification.cs` | D (Database) | P | Both | CDH disabled OR no prior grab→import history with mismatched qualities OR neither hash nor title matches a previously imported release | `AlreadyImportedSameHash`, `AlreadyImportedSameName` |
| 4 | AnimeVersionUpgradeSpecification | `Specifications/AnimeVersionUpgradeSpecification.cs` | D | P | Both | series isn't Anime OR `DownloadPropersAndRepacks == DoNotPrefer` OR new revision matches existing release group | `UnknownReleaseGroup`, `ReleaseGroupDoesNotMatch` |
| 5 | BlockedIndexerSpecification | `Specifications/BlockedIndexerSpecification.cs` | D (Database) | T | Both | indexer not in 15 s-cached blocklist | `IndexerDisabled` |
| 6 | BlocklistSpecification | `Specifications/BlocklistSpecification.cs` | D (Database) | P | Both | release not blocklisted for this series | `Blocklisted` |
| 7 | CustomFormatAllowedByProfileSpecification | `Specifications/CustomFormatAllowedByProfileSpecification.cs` (class name `CustomFormatAllowedbyProfileSpecification`, lowercase `b`) | D | P | Both | `CustomFormatScore >= profile.MinFormatScore` | `CustomFormatMinimumScore` |
| 8 | FreeSpaceSpecification | `Specifications/FreeSpaceSpecification.cs` | **Disk** | P | Both | `SkipFreeSpaceCheckWhenImporting` OR free space unknown OR `freeSpace - size > MinimumFreeSpaceWhenImporting` | `MinimumFreeSpace` |
| 9 | FullSeasonSpecification | `Specifications/FullSeasonSpecification.cs` | D | P | Both | not a full-season release OR all eps have aired (`AirDateUtc < now + 24h`) | `FullSeasonNotAired` |
| 10 | MaximumSizeSpecification | `Specifications/MaximumSizeSpecification.cs` | D | P | Both | `MaximumSize` config = 0 OR `size <= MaximumSize` (global cap, not per-quality) | `MaximumSizeExceeded` |
| 11 | MinimumAgeSpecification | `Specifications/MinimumAgeSpecification.cs` | D | **T** | Both (usenet only) | non-usenet OR `age >= MinimumAge` minutes | `MinimumAge` |
| 12 | MultiSeasonSpecification | `Specifications/MultiSeasonSpecification.cs` | D | P | Both | `!IsMultiSeason` | `MultiSeason` |
| 13 | NotSampleSpecification | `Specifications/NotSampleSpecification.cs` | D | P | Both | title doesn't contain `sample` OR size ≥ 70 MB | `Sample` |
| 14 | ProtocolSpecification | `Specifications/ProtocolSpecification.cs` | D | P | Both | the matching protocol is enabled in the delay profile for this series | `ProtocolDisabled` |
| 15 | QualityAllowedByProfileSpecification | `Specifications/QualityAllowedByProfileSpecification.cs` | D | P | Both | quality item is `Allowed` in the series quality profile | `QualityNotWanted` |
| 16 | QueueSpecification | `Specifications/QueueSpecification.cs` | D | P | Both | no overlapping queue item, or new release upgrades it (`UpgradableSpecification.IsUpgradable`); skips queue items in `FailedPending` | `QueueCutoffMet`, `QueueHigherPreference`, `QueueHigherRevision`, `QueueCustomFormatCutoffMet`, `QueueCustomFormatScore`, `QueueCustomFormatScoreIncrement`, `QueueUpgradesNotAllowed`, `QueuePropersDisabled` |
| 17 | RawDiskSpecification | `Specifications/RawDiskSpecification.cs` | D | P | Both | title doesn't match disc/full-bluray/MDVD regex AND container ≠ `vob`/`iso`/`m2ts` | `Raw` |
| 18 | ReleaseRestrictionsSpecification | `Specifications/ReleaseRestrictionsSpecification.cs` | D | P | Both | every required-term profile finds at least one match AND no ignored term matches | `MustContainMissing`, `MustNotContainPresent` |
| 19 | RepackSpecification | `Specifications/RepackSpecification.cs` | D (Database) | P | Both | not a repack, OR `DownloadPropersAndRepacks == DoNotPrefer`, OR repack matches existing file's release group | `RepackDisabled`, `RepackUnknownReleaseGroup`, `RepackReleaseGroupDoesNotMatch` |
| 20 | RetentionSpecification | `Specifications/RetentionSpecification.cs` | D | P | Both (usenet only) | non-usenet OR `Retention` = 0 OR `age <= Retention` days | `MaximumAge` |
| 21 | SameEpisodesGrabSpecification | `Specifications/SameEpisodesGrabSpecification.cs` | D | P | Both | every existing episode-file the new release would replace contains only episodes that are also in this release (delegates to `SameEpisodesSpecification` — not its own spec) | `ExistingFileHasMoreEpisodes` |
| 22 | SceneMappingSpecification | `Specifications/SceneMappingSpecification.cs` | D | **T** | Both | no scene mapping, no SceneOrigin, or origin not `mixed` | `AmbiguousNumbering` (when origin starts with `mixed:`); `unknown:` origins log but accept |
| 23 | SeasonPackOnlySpecification | `Specifications/SeasonPackOnlySpecification.cs` | D | P | Both (mostly Search) | not a single-episode search OR full-season release OR last aired episode is within `SeasonSearchMaximumSingleEpisodeAge` days | `NotSeasonPack` |
| 24 | SplitEpisodeSpecification | `Specifications/SplitEpisodeSpecification.cs` | D | P | Both | `!IsSplitEpisode` | `SplitEpisode` |
| 25 | TorrentSeedingSpecification | `Specifications/TorrentSeedingSpecification.cs` | D | P | Both (torrent only) | not torrent, indexer missing, OR `seeders >= MinimumSeeders` (per torrent indexer settings) | `MinimumSeeders` |
| 26 | UpgradeAllowedSpecification | `Specifications/UpgradeAllowedSpecification.cs` | D | P | Both | each existing file's profile permits the upgrade (`UpgradableSpecification.IsUpgradeAllowed`) | `QualityUpgradesDisabled` |
| 27 | UpgradeDiskSpecification | `Specifications/UpgradeDiskSpecification.cs` | D | P | Both | for season packs: enough episodes are upgradable per `SeasonPackUpgrade` mode (Any / All / Threshold%); for singles: cutoff not yet met AND new release passes `IsUpgradable` | `DiskCutoffMet`, `DiskHigherPreference`, `DiskHigherRevision`, `DiskCustomFormatCutoffMet`, `DiskCustomFormatScore`, `DiskCustomFormatScoreIncrement`, `DiskUpgradesNotAllowed`, `DiskNotUpgrade` |
| 28 | RssSync/DelaySpecification | `Specifications/RssSync/DelaySpecification.cs` | D (Database) | **T** | Both unless `UserInvokedSearch` | delay = 0; OR preferred protocol + revision upgrade + `PreferAndUpgrade`; OR `BypassIfHighestQuality` and quality is the best in profile on preferred protocol; OR `BypassIfAboveCustomFormatScore` and CFS ≥ minimum on preferred protocol; OR oldest pending release is older than `delay`; OR release is older than `delay` | `MinimumAgeDelay` |
| 29 | RssSync/DeletedEpisodeFileSpecification | `Specifications/RssSync/DeletedEpisodeFileSpecification.cs` | **Disk** | T | RSS-only | `AutoUnmonitorPreviouslyDownloadedEpisodes` off OR all episode files exist on disk | `EpisodeNotMonitored` |
| 30 | RssSync/HistorySpecification | `Specifications/RssSync/HistorySpecification.cs` | D (Database) | P | RSS-only | most-recent grab event in history doesn't satisfy cutoff, or new release passes `IsUpgradable`; "recent" = within last 12 h; with CDH disabled, even older grabs gate | `HistoryRecentCutoffMet`, `HistoryCdhDisabledCutoffMet`, `HistoryHigherPreference`, `HistoryHigherRevision`, `HistoryCutoffMet`, `HistoryCustomFormatCutoffMet`, `HistoryCustomFormatScore`, `HistoryCustomFormatScoreIncrement`, `HistoryUpgradesNotAllowed` |
| 31 | RssSync/IndexerTagSpecification | `Specifications/RssSync/IndexerTagSpecification.cs` | D | P | Both | indexer has no tags, OR series shares ≥1 tag with the indexer | `NoMatchingTag` |
| 32 | RssSync/MonitoredEpisodeSpecification | `Specifications/RssSync/MonitoredEpisodeSpecification.cs` | D | P | Both unless `MonitoredEpisodesOnly == false` | series.Monitored AND every episode in the release is monitored | `SeriesNotMonitored`, `EpisodeNotMonitored` |
| 33 | RssSync/PendingSpecification | `Specifications/RssSync/PendingSpecification.cs` | D (Database) | T | Push only (skipped for RSS source and `UserInvokedSearch`) | no pending release already covers any of these episodes | `MinimumAgeDelayPushed` |
| 34 | RssSync/ProperSpecification | `Specifications/RssSync/ProperSpecification.cs` | D | P | RSS-only | `DownloadPropersAndRepacks == DoNotPrefer`, OR not a revision upgrade, OR existing file is < 7 days old AND propers aren't disabled | `PropersDisabled`, `ProperForOldFile` |
| 35 | Search/EpisodeRequestedSpecification | `Specifications/Search/EpisodeRequestedSpecification.cs` | D | P | Search-only | parsed episode IDs intersect `SearchCriteria.Episodes` IDs | `WrongEpisode` |
| 36 | Search/SeasonMatchSpecification | `Specifications/Search/SeasonMatchSpecification.cs` | D | P | Search-only | not a `SeasonSearchCriteria`, OR parsed season number matches | `WrongSeason` |
| 37 | Search/SeriesSpecification | `Specifications/Search/SeriesSpecification.cs` | D | P | Search-only | parsed series id matches `SearchCriteria.Series.Id` | `WrongSeries` |
| 38 | Search/SingleEpisodeSearchMatchSpecification | `Specifications/Search/SingleEpisodeSearchMatchSpecification.cs` | D | P | Search-only (Single/Anime) | season/episode matches the criteria; full-season packs rejected during single search except anime season search | `WrongSeason`, `FullSeason`, `WrongEpisode` |

> Note: `SameEpisodesSpecification.cs` is referenced as a count of 32 "specifications proper"; it is a helper that does NOT implement `IDownloadDecisionEngineSpecification` (no `Priority`/`Type`, no `IsSatisfiedBy(subject, info)`). It is consumed by `SameEpisodesGrabSpecification` (#21).

**Pre-pipeline rejection reasons** (set directly inside `DownloadDecisionMaker`, not by any spec): `MatchesAnotherSeries`, `UnknownSeries`, `UnknownEpisode`, `UnableToParse`, `Error`, `DecisionError`. These bypass the priority pipeline.

## Search vs RSS path differences

The same `_specifications` collection is used for both calls; specs branch on `information.SearchCriteria` to opt in/out:

- **Always-on (both paths):** AcceptableSize, AirDate, AlreadyImported, AnimeVersionUpgrade, BlockedIndexer, Blocklist, CustomFormatAllowed, FreeSpace, FullSeason, MaximumSize, MinimumAge, MultiSeason, NotSample, Protocol, QualityAllowed, Queue, RawDisk, ReleaseRestrictions, Repack, Retention, SameEpisodesGrab, SceneMapping, SeasonPackOnly, SplitEpisode, TorrentSeeding, UpgradeAllowed, UpgradeDisk, IndexerTag.
- **RSS-only (skip when `SearchCriteria != null`):** `HistorySpecification` (`Specifications/RssSync/HistorySpecification.cs:37–41`), `DeletedEpisodeFileSpecification` (`...DeletedEpisodeFileSpecification.cs:35–39`), `ProperSpecification` (`...ProperSpecification.cs:28–31`).
- **Search-only:** `EpisodeRequestedSpecification`, `SeasonMatchSpecification`, `SeriesSpecification`, `SingleEpisodeSearchMatchSpecification` — all early-accept when `SearchCriteria == null`.
- **Push vs RSS vs Search:** `PendingSpecification` skips itself for `ReleaseSourceType.Rss` and `UserInvokedSearch`, only firing for the "push" / non-user search path (`...PendingSpecification.cs:23–36`).
- **Bypasses for user-invoked searches:** `DelaySpecification` and `PendingSpecification` accept early when `SearchCriteria.UserInvokedSearch == true`.
- **Monitored bypass:** `MonitoredEpisodeSpecification` accepts when search criteria explicitly sets `MonitoredEpisodesOnly = false` (e.g. interactive search).
- **Scene mapping:** `SceneMappingSpecification` does not change paths but its `Type = Temporary` because mappings can be added later.

`ReleaseSource` is computed in `DownloadDecisionMaker.GetReleaseSource` (`DownloadDecisionMaker.cs:221–240`): `Rss`, `ReleasePush`, `Search`, `UserInvokedSearch`, `InteractiveSearch`. Specs branch on this and on `SearchCriteria` flags.

## Upgrade logic

Centralised in `Specifications/UpgradableSpecification.cs`. The shape of an upgrade decision is:

```
IsUpgradable(profile, currentQuality, currentCustomFormats, newQuality, newCustomFormats) -> UpgradeableRejectReason
```

Decision order (`UpgradableSpecification.cs:31–122`):
1. Compare quality via `QualityModelComparer(profile)`. If `new > current` AND quality cutoff not yet met → upgrade allowed (`None`).
2. If `new < current` quality → `BetterQuality` (existing wins).
3. If `DownloadPropersAndRepacks != DoNotPrefer` AND `new.Revision > current.Revision` → `None` (revision win).
4. If `!profile.UpgradeAllowed` → `UpgradesNotAllowed`.
5. If revision downgrade (and prefer-propers on) → `BetterRevision`.
6. If quality strictly better but cutoff already met → `QualityCutoff`.
7. If new custom-format score ≤ current → `CustomFormatScore`.
8. If current score already ≥ `CutoffFormatScore` → `CustomFormatCutoff`.
9. If new score < current + `MinUpgradeFormatScore` → `MinCustomFormatScore`.
10. Otherwise `None`.

Helpers:
- `QualityCutoffNotMet`: cutoff is `profile.Cutoff` when `UpgradeAllowed`, else `profile.FirststAllowedQuality().Id` (sic — typo preserved in C#). A revision improvement on the same quality also flips this to true.
- `CustomFormatCutoffNotMet`: same selector — `CutoffFormatScore` if upgrades allowed, else `MinFormatScore`.
- `CutoffNotMet` = either of the above.
- `IsRevisionUpgrade`: only true when `currentQuality.Quality == newQuality.Quality` AND `newRevision > currentRevision` (this is intentional — Sonarr won't upgrade a webrip to a webdl-proper or vice versa).
- `IsUpgradeAllowed`: gate used by `UpgradeAllowedSpecification` — accepts a revision upgrade unconditionally; otherwise requires `UpgradeAllowed` if either quality or CF score improves.

This is shared by `QueueSpecification`, `Specifications/RssSync/HistorySpecification.cs`, `UpgradeAllowedSpecification`, `UpgradeDiskSpecification`, `Specifications/RssSync/ProperSpecification.cs`, `RepackSpecification`, `AnimeVersionUpgradeSpecification`, and `Specifications/RssSync/DelaySpecification.cs`.

## Custom-format integration

- Computed once per report in `DownloadDecisionMaker.cs:118–121`: `_formatCalculator.ParseCustomFormat(remoteEpisode, size)` then `qualityProfile.CalculateCustomFormatScore(customFormats)`. The score is cached on `RemoteEpisode.CustomFormatScore`.
- `CustomFormatAllowedByProfileSpecification` enforces `score >= profile.MinFormatScore`.
- `UpgradableSpecification` consumes the score for cutoff/upgrade checks (steps 7–9 above).
- Existing files / queue items / history are scored on demand by re-running `_formatService.ParseCustomFormat(file)` or `ParseCustomFormat(history, series)`.
- `DelaySpecification` honors `BypassIfAboveCustomFormatScore` + `MinimumCustomFormatScore` on the delay profile.
- `DownloadDecisionComparer.CompareCustomFormatScore` is the second tiebreaker after raw quality, ahead of protocol preference.
- The min-upgrade-increment (`profile.MinUpgradeFormatScore`) prevents trivial CF score improvements from triggering upgrades.

## Cutoff handling

Two cutoffs live on `QualityProfile`:
- **Quality cutoff** (`profile.Cutoff`) — quality id at/above which Sonarr stops looking for upgrades.
- **Custom-format cutoff** (`profile.CutoffFormatScore`) — score at/above which Sonarr stops looking for CF upgrades.

`UpgradableSpecification.QualityCutoffNotMet` and `CustomFormatCutoffNotMet` use `UpgradeAllowed` to decide whether the cutoff is the "real" cutoff or just the lowest allowed quality / `MinFormatScore` (ie. when upgrades are off, "cutoff" collapses to "first allowed"). `CutoffNotMet` ORs both.

Specs that surface cutoff-met decisions: `QueueSpecification` (Queue\* cutoff reasons), `HistorySpecification` (History\* cutoff), `UpgradeDiskSpecification` (Disk\* cutoff). Each emits a distinct `DownloadRejectionReason` so the UI can group rejection causes.

## Quirks / TODOs / HACKs

- **Class-name typo:** `Specifications/CustomFormatAllowedByProfileSpecification.cs` declares `CustomFormatAllowedbyProfileSpecification` (lowercase `b`). The file name and class name diverge.
- **Profile-method typo:** `QualityProfile.FirststAllowedQuality()` (triple-`t`) — propagated through `UpgradableSpecification` and `UpgradeDiskSpecification`. The Go port can rename, but DB-stored references may exist; verify.
- **Spec ordering inside a priority group is undefined** — depends on DI registration order. The pipeline collects every rejection at a level before short-circuiting, so semantically order shouldn't matter, but performance can vary (e.g. `Queue` and `History` hit DB even when an earlier cheap spec already rejected).
- **`AcceptableSizeSpecification` 45-min fallback** is duplicated logic vs the import path; fragile if the pilot heuristic ever diverges.
- **`AirDateSpecification.FindBestProfile` is dead code** (not called) — the active selector is inline at line 38–41.
- **`BlockedIndexerSpecification` cache TTL is 15 s** — hard-coded. Means a freshly unblocked indexer can be rejected for up to 15 s.
- **`SeasonPackOnlySpecification`** depends on `ReleaseInfo.SeasonSearchMaximumSingleEpisodeAge`, which is a per-indexer property; spec silently passes when the indexer doesn't set it.
- **`ProperSpecification.cs:51` "ProperForOldFile"** uses `DateTime.Today.AddDays(-7)` — local-time boundary, not UTC.
- **`HistorySpecification` "recent" window is hard-coded to 12 h** — config knob would be nice.
- **`DelaySpecification` uses `_pendingReleaseService.OldestPendingRelease`** — reads pending releases from DB on every release; a hot RSS run can hammer this.
- **`AnimeVersionUpgradeSpecification`** rejects when either side has unknown release group, even though the underlying decision is "should we try a v2/v3 with the same group?". Slightly conservative.
- **`SceneMappingSpecification`** is `Temporary` even though `mixed:` mappings are arguably permanent — the comment says "till there's a mapping" but `mixed` will never resolve.
- **Pre-pipeline `MatchesAnotherSeries` short-circuit** lives in `DownloadDecisionMaker` itself, not a spec — Go port needs the same hook order to preserve behavior for releases that match an alias TVDB id.
- **`DownloadDecisionComparer.CompareSize`** does `(remoteEpisode.Release.Size - preferredEpisodeSize).Round(200.Megabytes())` — note 200 MB rounding, which means sizes within a 200 MB window are tied.
- **`UpgradeDiskSpecification` season-pack mode** has three modes (`Any`, `All`, `Threshold`) with the threshold percent in `_configService.SeasonPackUpgradeThreshold`; only `Any` short-circuits early.

## Test files (`src/NzbDrone.Core.Test/DecisionEngineTests/**`)

Top-level fixtures (one per spec unless noted):
- `AcceptableSizeSpecificationFixture.cs`
- `AirDateSpecificationFixture.cs`
- `AlreadyImportedSpecificationFixture.cs`
- `AnimeVersionUpgradeSpecificationFixture.cs`
- `BlockedIndexerSpecificationFixture.cs`
- `CustomFormatAllowedByProfileSpecificationFixture.cs`
- `DownloadDecisionMakerFixture.cs` (orchestration tests)
- `FreeSpaceSpecificationFixture.cs`
- `FullSeasonSpecificationFixture.cs`
- `MaximumSizeSpecificationFixture.cs`
- `MinimumAgeSpecificationFixture.cs`
- `MonitoredEpisodeSpecificationFixture.cs` (despite living in RSS, its fixture is at top level)
- `MultiSeasonSpecificationFixture.cs`
- `PrioritizeDownloadDecisionFixture.cs` (comparer + per-series ordering)
- `ProtocolSpecificationFixture.cs`
- `QualityAllowedByProfileSpecificationFixture.cs`
- `QueueSpecificationFixture.cs`
- `RawDiskSpecificationFixture.cs`
- `ReleaseRestrictionsSpecificationFixture.cs`
- `RepackSpecificationFixture.cs`
- `RetentionSpecificationFixture.cs`
- `SameEpisodesSpecificationFixture.cs`
- `SingleEpisodeAgeDownloadDecisionFixture.cs`
- `UpgradeAllowedSpecificationFixture .cs` (note trailing space in filename — preserve or rename in port)
- `UpgradeDiskSpecificationFixture.cs`
- `UpgradeSpecificationFixture.cs` (the `UpgradableSpecification` core matrix — this is the most important fixture)

`RssSync/`:
- `DelaySpecificationFixture.cs`
- `DeletedEpisodeFileSpecificationFixture.cs`
- `HistorySpecificationFixture.cs`
- `IndexerTagSpecificationFixture.cs`
- `PendingSpecificationFixture.cs`
- `ProperSpecificationFixture.cs`

`Search/`:
- `SeriesSpecificationFixture.cs`
- `TorrentSeedingSpecificationFixture.cs` (lives under Search, even though spec lives at top level)
- `SingleEpisodeSearchMatchSpecificationTests/` (folder — multiple cases inside)

Notable absences (no fixture found): `BlocklistSpecification`, `EpisodeRequestedSpecification`, `SeasonMatchSpecification`, `SeasonPackOnlySpecification`, `SplitEpisodeSpecification`, `NotSampleSpecification`, `SceneMappingSpecification`, `SameEpisodesGrabSpecification`. The Go port should add these.

## Open questions

- **DECISION-Q1** — Is the `Default = Parsing = Database = 0` priority collapse intentional? The enum implies a 3-tier model but the values make Parsing/Database behave identically to Default. The Go port should either make these distinct (changes ordering) or document the collapse.
- **DECISION-Q2** — `DownloadDecisionMaker.GetDecisionForReport` short-circuits at the first priority level with rejections, but evaluates **every** spec in that level. When two specs at Default level both reject, both reasons surface; when one Default-level rejection occurs, Disk-level specs (FreeSpace, DeletedEpisodeFile) never run. Is this an intentional cost optimisation, or should rejections be exhaustive? The current behavior hides "you'd also have failed FreeSpace" which can confuse users.
- **DECISION-Q3** — `BlockedIndexerSpecification` uses a 15-second TTL cache; should the Go port keep this hard-coded or make it config-driven? RSS sync runs every 15 minutes so the cache is mostly redundant.
- **DECISION-Q4** — `SameEpisodesSpecification` does not implement `IDownloadDecisionEngineSpecification` but lives in the same folder. Should the Go port collapse it into `SameEpisodesGrabSpecification` or keep as a separate helper?
- **DECISION-Q5** — `UpgradeDiskSpecification` has a season-pack mode (`Any`/`All`/`Threshold`) that is partially redundant with `SameEpisodesGrab`/`UpgradeAllowed`. Confirm whether `SeasonPackUpgrade` config is fully orthogonal or if the modes interact in a way that affects ordering.
- **DECISION-Q6** — `DownloadDecisionComparer.CompareEpisodeCount` reverses the comparison for non-anime (more episodes = better) but uses normal direction for anime (fewer = better). Confirm this matches user expectation; the test `PrioritizeDownloadDecisionFixture` should cover this.
- **DECISION-Q7** — `ProperSpecification` uses `DateTime.Today.AddDays(-7)` (local time). All other date logic uses UTC (`AirDateUtc`, `DateTime.UtcNow`). The Go port should pick one to avoid edge-case bugs around midnight.
- **DECISION-Q8** — `DelaySpecification` reads `_pendingReleaseService.OldestPendingRelease` synchronously per release — is the current implementation cached, or does each evaluation hit the DB? If the latter, RSS feeds with hundreds of releases will produce N DB hits.
- **DECISION-Q9** — `AcceptableSizeSpecification` falls back to runtime = 45 only if the pilot episode aired within 24 h of the release. For older series with 0 runtime in DB this means the spec rejects with `UnknownRuntime`. Should the Go port change the fallback to "always 45" or keep the conservative behavior?
- **DECISION-Q10** — Class-name typo `CustomFormatAllowedbyProfileSpecification` (and method `FirststAllowedQuality`) — confirm whether to fix or preserve in Go to avoid breaking serialization keys / log strings.
