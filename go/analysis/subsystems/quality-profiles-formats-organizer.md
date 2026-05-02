# Qualities, Profiles, CustomFormats, Organizer

**Source:**
- `src/NzbDrone.Core/Qualities/**`
- `src/NzbDrone.Core/Profiles/**`
- `src/NzbDrone.Core/CustomFormats/**`
- `src/NzbDrone.Core/Organizer/**`

**Target Go package(s):**
- `go/internal/qualities/`
- `go/internal/profiles/qualities/`, `go/internal/profiles/releases/`, `go/internal/profiles/delay/`
- `go/internal/customformats/`
- `go/internal/organizer/`

**Status:** drafted

## Purpose
These four subsystems form the scoring + naming spine that decisions, downloads, imports and renames all hang from. **Qualities** enumerates a fixed set of named release qualities (`SDTV`…`Bluray-2160p Remux`) plus per-quality definitions (size bounds, weight) and a `Revision` (proper/repack/version). **QualityProfiles** turn that flat list into an ordered, per-user "what's allowed, what's the cutoff, how much custom-format score is required" decision input, plus per-item size override and an embedded list of `ProfileFormatItem { Format, Score }`. **CustomFormats** are user-defined matchers — each format is a list of `ICustomFormatSpecification` (regex on title/group, language match, indexer flag, resolution, source, size, release type), and `CustomFormatCalculationService` runs them against a `CustomFormatInput` to return matched formats whose scores then sum into the profile's score. **Organizer** consumes the output of all three (chosen quality, matched custom formats, series/episodes) plus a user-defined token template to produce the final filename and folder paths. The systems are tightly coupled: profiles reference both qualities and formats; the decision engine compares releases by profile index → revision → CF score; rename templates emit quality, format and revision tokens.

---

## Subsystem 1: Qualities

### Quality enumeration
Defined as static factory properties on `Quality.cs` (`src/NzbDrone.Core/Qualities/Quality.cs:77-124`). All 22 qualities live in `Quality.All` (id 0 plus 1-22, with 11 reserved/skipped).

| Id | Name | Source (`QualitySource`) | Resolution |
|----|------|------|------------|
| 0  | Unknown            | Unknown        | 0    |
| 1  | SDTV               | Television     | 480  |
| 2  | DVD                | DVD            | 480  |
| 3  | WEBDL-1080p        | Web            | 1080 |
| 4  | HDTV-720p          | Television     | 720  |
| 5  | WEBDL-720p         | Web            | 720  |
| 6  | Bluray-720p        | Bluray         | 720  |
| 7  | Bluray-1080p       | Bluray         | 1080 |
| 8  | WEBDL-480p         | Web            | 480  |
| 9  | HDTV-1080p         | Television     | 1080 |
| 10 | Raw-HD             | TelevisionRaw  | 1080 |
| 12 | WEBRip-480p        | WebRip         | 480  |
| 13 | Bluray-480p        | Bluray         | 480  |
| 14 | WEBRip-720p        | WebRip         | 720  |
| 15 | WEBRip-1080p       | WebRip         | 1080 |
| 16 | HDTV-2160p         | Television     | 2160 |
| 17 | WEBRip-2160p       | WebRip         | 2160 |
| 18 | WEBDL-2160p        | Web            | 2160 |
| 19 | Bluray-2160p       | Bluray         | 2160 |
| 20 | Bluray-1080p Remux | BlurayRaw      | 1080 |
| 21 | Bluray-2160p Remux | BlurayRaw      | 2160 |
| 22 | Bluray-576p        | Bluray         | 576  |

`QualitySource` enum (`QualitySource.cs`): `Unknown, Television, TelevisionRaw, Web, WebRip, DVD, Bluray, BlurayRaw`.

`Quality` is `IEmbeddedDocument` — equality is by `Id` only, so `Source`/`Resolution`/`Name` are recomputed from the static list when a profile/episode doc is rehydrated.

### Revision
`Revision.cs` — `{ int Version=1, int Real=0, bool IsRepack=false }`. `CompareTo` orders by `Real` first then `Version`; `IsRepack` is metadata only, not part of equality. `ToString()` renders `v1`, `v2 Real:1`, etc. Detection: `QualityParser.cs` has `ProperRegex` (`\bproper\b`), `RepackRegex` (`repack\d?|rerip\d?`), `VersionRegex` (`v2`, `v3`, `repack2`, …), `RealRegex` (`\bREAL\b`).

`ProperDownloadTypes` enum (`ProperDownloadTypes.cs`): `PreferAndUpgrade`, `DoNotUpgrade`, `DoNotPrefer` — config-level toggle that the decision-engine comparer uses to skip the revision tiebreak when set to `DoNotPrefer`.

### Quality detection from release info
Quality is detected in two places:

1. **From a release title or filename string** — `Parser/QualityParser.cs:68 ParseQuality(name)`. Pipeline:
   - `RawHDRegex` short-circuits to `Quality.RAWHD`.
   - `SourceRegex` (huge alternation: `bluray|webdl|webrip|hdtv|bdrip|brrip|dvd|dsr|pdtv|sdtv|tvrip`) picks a source.
   - `ResolutionRegex` picks `360p|480p|540p|576p|720p|1080p|2160p`; `AlternativeResolutionRegex` infers 2160p from `UHD`/`[4K]`.
   - `RemuxRegex` adds `BlurayRaw` when remux is present.
   - `CodecRegex` (x264/h264/xvid/divx) drives some fallbacks.
   - `OtherSourceRegex`/`AnimeBlurayRegex`/`AnimeWebDlRegex`/`HighDefPdtvRegex`/`MPEG2Regex` are anime/edge fallbacks.
   - Resolution + source are then fed to `QualityFinder.FindBySourceAndResolution(source, resolution)` (lines 459, 470, 481, 493).
   - If still `Unknown`, falls back to `MediaFileExtensions.GetQualityForExtension(.ext)` and stamps `QualityDetectionSource.Extension`.
   - Modifiers are layered on by `ParseQualityModifiers` to populate `Revision`.

2. **From an aggregated set of sources** during import — `MediaFiles/EpisodeImport/Aggregation/Aggregators/AggregateQuality.cs:81` calls `QualityFinder.FindBySourceAndResolution(source, resolution)` after combining inputs from the `IAugmentQuality` chain (`AugmentQualityFromDownloadClientItem`, `AugmentQualityFromReleaseName`, `AugmentQualityFromFileName`, `AugmentQualityFromFolder`, `AugmentQualityFromMediaInfo` — see `Augmenters/Quality/`). Each augmenter returns an `AugmentQualityResult` with a `Confidence` (`Fallback < Default < Tag < MediaInfo`) and the highest-confidence value wins per axis.

`QualityModel` (`QualityModel.cs`) wraps `Quality` + `Revision` plus three `[JsonIgnore] QualityDetectionSource` fields (`Source`, `Resolution`, `Revision` detection sources — `Unknown|Name|Extension|MediaInfo`). Default `CompareTo` uses the static `DefaultQualityDefinitions.Weight` — note this differs from per-profile ordering done by `QualityModelComparer`.

`QualityFinder.FindBySourceAndResolution` (`QualityFinder.cs`):
- Single match by `(source, resolution)` wins.
- For sub-720p resolutions, source-only fallback to `SDTV`/`WEBDL480p`/`WEBRip480p`.
- Otherwise searches all qualities at that resolution ordered by `QualitySource` enum value, picks the first whose source ≥ requested source. Logs a warn.

### Quality definition table (per-minute size limits)
`QualityDefinition.cs`: `{ Quality, Title, GroupName, Weight, double? MinSize, double? MaxSize, double? PreferredSize }`. Sizes are in **MB/minute**; `null` MaxSize = unlimited.

Defaults (`Quality.cs:156-180`, weight order):

| Weight | Quality | MinSize MB/min | MaxSize MB/min | Preferred | Group |
|--------|---------|----|----|----|-------|
| 1  | Unknown            | 1  | 199.9 | 95 | – |
| 2  | SDTV               | 2  | 100   | 95 | – |
| 3  | WEBRip-480p        | 2  | 100   | 95 | WEB 480p |
| 3  | WEBDL-480p         | 2  | 100   | 95 | WEB 480p |
| 4  | DVD                | 2  | 100   | 95 | – |
| 5  | Bluray-480p        | 2  | 100   | 95 | – |
| 6  | Bluray-576p        | 2  | 100   | 95 | – |
| 7  | HDTV-720p          | 3  | 125   | 95 | – |
| 8  | HDTV-1080p         | 4  | 125   | 95 | – |
| 9  | Raw-HD             | 4  | null  | 95 | – |
| 10 | WEBRip-720p        | 3  | 130   | 95 | WEB 720p |
| 10 | WEBDL-720p         | 3  | 130   | 95 | WEB 720p |
| 11 | Bluray-720p        | 4  | 130   | 95 | – |
| 12 | WEBRip-1080p       | 4  | 130   | 95 | WEB 1080p |
| 12 | WEBDL-1080p        | 4  | 130   | 95 | WEB 1080p |
| 13 | Bluray-1080p       | 4  | 155   | 95 | – |
| 14 | Bluray-1080p Remux | 35 | null  | 95 | – |
| 15 | HDTV-2160p         | 35 | 199.9 | 95 | – |
| 16 | WEBRip-2160p       | 35 | null  | 95 | WEB 2160p |
| 16 | WEBDL-2160p        | 35 | null  | 95 | WEB 2160p |
| 17 | Bluray-2160p       | 35 | null  | 95 | – |
| 18 | Bluray-2160p Remux | 35 | null  | 95 | – |

`QualityDefinitionLimits.cs`: numeric clamp `Min = 0`, `Max = 1000` (UI/validation bound; in DB the value is a nullable double).

### Service / repository
- `IQualityDefinitionRepository`, `QualityDefinitionRepository` (`QualityDefinitionRepository.cs`) — straight `BasicRepository<QualityDefinition>` over the `QualityDefinitions` table.
- `IQualityDefinitionService`, `QualityDefinitionService` (`QualityDefinitionService.cs`):
  - 5-second `ICached<Dictionary<Quality, QualityDefinition>>` keyed `"all"`.
  - On `ApplicationStartedEvent`: `InsertMissingDefinitions` — diff existing rows vs `Quality.DefaultQualityDefinitions`, insert missing, retain existing customizations, **delete unknown rows**.
  - On `ResetQualityDefinitionsCommand`: only resets `Title` (when `ResetTitles=true`); does NOT reset min/max/preferred sizes.
  - `WithWeight(definition)` re-stamps the in-memory `Weight` from defaults so user-provided weight in the DB is ignored.

### DB tables / migrations
- `QualityDefinitions` — `Id, Quality (int), Title, MinSize, MaxSize, PreferredSize`. Migration `084_update_quality_minmax_size.cs`, `181_quality_definition_preferred_size.cs`. (`Weight` and `GroupName` are NOT persisted — they're stamped from `Quality.DefaultQualityDefinitions` at load time.)
- Quality is stored on `EpisodeFile`, `Blocklist`, `EpisodeHistory`, `Series`(via profile) etc. as a serialized `QualityModel` JSON blob; `036_update_with_quality_converters.cs` and `062_convert_quality_models.cs` handled format changes.

### Events / jobs
- `ResetQualityDefinitionsCommand` (`Commands/ResetQualityDefinitionsCommand.cs`) — only `ResetTitles` field; `Execute` handler in `QualityDefinitionService`.
- No dedicated quality events — quality changes flow via downstream `EpisodeFile`/`Series` updates.

### Test files
- `src/NzbDrone.Core.Test/Qualities/QualityFixture.cs`
- `.../QualityFinderFixture.cs`
- `.../QualityModelComparerFixture.cs`
- `.../QualityDefinitionServiceFixture.cs`
- `.../RevisionComparableFixture.cs`
- `.../MediaFiles/EpisodeImport/Aggregation/Aggregators/Augmenters/Quality/*` — augmenter tests.

### Quirks
- `Quality.HDTV480p` (id 11) is **commented out** but the id is still reserved (`Quality.cs:89`).
- `Quality.Equals` is id-only — two `Quality{Id=1, Name="Foo", Resolution=720}` and `Quality.SDTV` are equal. Unit tests rely on this to round-trip embedded docs.
- `Revision.Equals` is `Version + Real`, but `IsRepack` is not part of equality (used only for naming and `decisionengine` ordering tie-breaks).
- `QualitySource` enum order matters: `QualityFinder` uses `OrderBy(q => q.Source)` so the enum integer value drives the source-fallback preference (Unknown=0 < Television=1 < TelevisionRaw=2 < Web=3 < WebRip=4 < DVD=5 < Bluray=6 < BlurayRaw=7).
- `QualityDefinition` rows can be auto-deleted on startup if a quality is removed from `DefaultQualityDefinitions` — destructive when downgrading versions.

### Open questions (Q-Q*)
- **Q-Q1**: Do we keep id 11 reserved in the Go port? Drop it? Renumber?
- **Q-Q2**: `QualityFinder.FindBySourceAndResolution` quietly returns `Unknown` (not error) when nothing matches above threshold — preserve, or surface?
- **Q-Q3**: `QualityDefinitionLimits.Max = 1000` MB/min is documented nowhere on the C# side. Is this a UI cap or hard limit?
- **Q-Q4**: `QualityDefinitionService` deletes unrecognised rows on startup. Is that the desired Go behavior or do we want a softer "ignore unknown" path?
- **Q-Q5**: `Revision.IsRepack` is set by parser but only consulted in `FileNameBuilder.GetQualityProper` (returns "Proper" for any version > 1, regardless of repack flag) — is the flag actually load-bearing anywhere we care about?

---

## Subsystem 2: Profiles

Three sibling profile types live under `Profiles/`: **QualityProfile**, **ReleaseProfile**, **DelayProfile**. They share no base class.

### QualityProfile shape
`Profiles/Qualities/QualityProfile.cs`:
```
{ Id, Name, UpgradeAllowed, Cutoff (int – Quality.Id OR groupId),
  MinFormatScore, CutoffFormatScore, MinUpgradeFormatScore,
  List<ProfileFormatItem> FormatItems,
  List<QualityProfileQualityItem> Items }
```

`QualityProfileQualityItem` (`QualityProfileQualityItem.cs`):
```
{ Id (groupId, ≥1000 if it's a group), Name (group label),
  Quality (single — null if this item is a group),
  List<QualityProfileQualityItem> Items (nested for groups),
  bool Allowed,
  double? MinSize, double? MaxSize, double? PreferredSize }
```
Items are an **ordered** list — index in the list is the upgrade rank. Groups (`Items` non-empty, `Quality` null) have a synthetic `groupId` starting at 1000.

`ProfileFormatItem` (`Profiles/ProfileFormatItem.cs`): `{ CustomFormat Format, int Score }` — one row per existing custom format; the profile keeps a row even with score=0 so the join with formats stays stable.

`QualityIndex` (`QualityIndex.cs`): `{ Index, GroupIndex }` — `respectGroupOrder=true` compares within a group (used for naming/per-group tiebreak); default compare is index-only (so anything in the same group has the same upgrade rank).

`QualityProfile.GetIndex(qualityId, respectGroupOrder=false)` walks `Items`:
- Match by `Item.Quality.Id`.
- Match by `Item.Id == qualityId` (for selecting the group itself).
- Match by `Item.Items[g].Quality.Id` — returns `(i, g)` if `respectGroupOrder` else `(i, 0)`.
- Falls through to `new QualityIndex()` (= `(0,0)`) — i.e. matches Unknown.

`QualityProfile.CalculateCustomFormatScore(formats)` — `FormatItems.Where(x => formats.Contains(x.Format)).Sum(x => x.Score)`.

`QualityModelComparer` (`Qualities/QualityModelComparer.cs`) is the per-profile comparer used by the decision engine and import: it uses `QualityProfile.GetIndex` rather than the static default weight, so profile reorder reorders comparisons.

### Cutoff semantics
- `Cutoff` is either a `Quality.Id` or a `groupId (≥1000)`. A release qualifies as "above cutoff" once its `QualityIndex.Index >= cutoffIndex.Index`.
- `CutoffFormatScore`: once total CF score ≥ this, no further upgrades for CF reasons.
- `MinFormatScore`: a release whose CF score is below this is rejected outright.
- `MinUpgradeFormatScore`: when comparing a candidate vs an existing file, the *delta* must be ≥ this for a CF-only upgrade to fire. Defaults to 1 (`QualityProfileService.GetDefaultProfile`, line 255).
- `UpgradeAllowed`: if false, only the cutoff-and-below set is fetched and never replaced.

### QualityProfile service / repository
`IQualityProfileService`, `QualityProfileService` (`Profiles/Qualities/QualityProfileService.cs`):
- Add/Update/Delete (Delete throws `QualityProfileInUseException` if any series or import list references the profile id).
- `Update` publishes `QualityProfileUpdatedEvent { Id }`.
- Handles `ApplicationStartedEvent` → seeds 6 default profiles: `Any`, `SD`, `HD-720p`, `HD-1080p`, `Ultra-HD`, `HD - 720p/1080p` (lines 91-157).
- Handles `CustomFormatAddedEvent` → inserts a new `ProfileFormatItem { Score=0 }` at the front of every profile's `FormatItems`.
- Handles `CustomFormatDeletedEvent` → removes the format from every profile, and if `FormatItems` becomes empty resets `MinFormatScore=0, CutoffFormatScore=0, MinUpgradeFormatScore=1`.
- `GetDefaultProfile(name, cutoff, allowed[])` — groups defaults by `Weight`, items with same weight become a group (the WEB-480p/720p/1080p/2160p groups), `groupAllowed = any member ∈ allowed`. Allowed flag is set per group; if cutoff falls inside a group, profileCutoff is rewritten to the groupId.
- `UpdateAllSizeLimits(...QualityProfileSizeLimit[])` — bulk-applies size limits from quality definitions into every profile's quality items.

`IQualityProfileRepository`, `QualityProfileRepository` (`QualityProfileRepository.cs`):
- Custom `Query(SqlBuilder)` override — pulls all custom formats once, then re-hydrates `ProfileFormatItem.Format` from the DB id (the JSON only stores the format id via `CustomFormatIntConverter`). Format items whose format no longer exists are silently dropped.

### ReleaseProfile shape
`Profiles/Releases/ReleaseProfile.cs`:
```
{ Id, Name, bool Enabled = true,
  List<string> Required, List<string> Ignored,
  bool AirDateRestriction, int AirDateGracePeriod,
  List<int> IndexerIds,
  HashSet<int> Tags, HashSet<int> ExcludedTags }
```

- Each `Required`/`Ignored` entry can be a literal substring (matched case-insensitively via `CaseInsensitiveTermMatcher`) **or** a Perl-style regex `/pattern/flags` (parsed by `PerlRegexFactory`). `TermMatcherService` chooses the matcher per-term and caches it for 24h (`TermMatcherService.cs`).
- `IReleaseProfileService` (`ReleaseProfileService.cs`) exposes filters: `AllForTag`, `AllForTags`, `AllExcludedForTag`, `EnabledForTags(tagIds, indexerId)` — enabled + (tag matches OR profile has no tags) + (indexer matches OR profile has no indexer restriction).
- Repo: `IRestrictionRepository` / `ReleaseProfileRepository` (`ReleaseProfileRepository.cs`) — note the legacy "restriction" name (migrations `127_rename_release_profiles.cs`, `136_add_indexer_and_enabled_release_profiles.cs`, `154_add_name_release_profile.cs`, `162_release_profile_to_array.cs`).
- `ReleaseProfilePreferredComparer` (in same file as `ReleaseProfile.cs`) — descending compare on the int value of a `KeyValuePair<string,int>`. Used by older "Preferred Words" UI surface — preferred words themselves were removed in v4 (`175_language_profiles_to_custom_formats.cs` migration replaced them).
- TermMatchers (`Profiles/Releases/TermMatchers/`):
  - `ITermMatcher` — `IsMatch(value)`, `MatchingTerm(value)`.
  - `CaseInsensitiveTermMatcher`.
  - `RegexTermMatcher`.

### DelayProfile shape
`Profiles/Delay/DelayProfile.cs`:
```
{ Id, bool EnableUsenet, bool EnableTorrent,
  DownloadProtocol PreferredProtocol,
  int UsenetDelay, int TorrentDelay,
  int Order,
  bool BypassIfHighestQuality, bool BypassIfAboveCustomFormatScore,
  int MinimumCustomFormatScore,
  HashSet<int> Tags }
```
`GetProtocolDelay(protocol)` returns torrent or usenet delay.

`DelayProfileService` (`Profiles/Delay/DelayProfileService.cs`):
- `Order` is contiguous; profile id 1 is the default and is excluded from reordering (`Reorder`, `Delete`).
- `BestForTags(tagIds)` — 30s cache, returns the first profile by `Order` whose `Tags` intersect or which has empty `Tags`.
- Migrations `070_delay_profile.cs`, `156_add_bypass_to_delay_profile.cs`, `182_add_bypass_to_delay_profile.cs`.

### Default profile creation
- QualityProfile: see `QualityProfileService.Handle(ApplicationStartedEvent)` above — six profiles seeded if `All().Any() == false`.
- ReleaseProfile: none seeded.
- DelayProfile: profile id 1 (`Order=1`) is seeded by migration `070_delay_profile.cs` as the always-fallback default.

### DB tables / migrations
- `QualityProfiles` (`054_rename_profiles.cs`, `055_drop_old_profile_columns.cs`, `101_add_ultrahd_quality_in_profiles.cs`, `117_add_webrip_and_br480_qualites_in_profile.cs`, `122_add_remux_qualities_in_profile.cs`, `128_rename_quality_profiles_add_upgrade_allowed.cs`, `175_language_profiles_to_custom_formats.cs`).
- `ReleaseProfiles` (`127_rename_release_profiles.cs`, `136_add_indexer_and_enabled_release_profiles.cs`, `154_add_name_release_profile.cs`, `162_release_profile_to_array.cs`).
- `DelayProfiles` (`070_delay_profile.cs`, `156_add_bypass_to_delay_profile.cs`, `182_add_bypass_to_delay_profile.cs`).
- `LanguageProfiles` was deleted in `175_language_profiles_to_custom_formats.cs` — language is now a `LanguageSpecification` custom format.

### Events / jobs
- `QualityProfileUpdatedEvent` (`Profiles/Qualities/QualityProfileUpdatedEvent.cs`) — `{ int Id }`, raised on every `Update`. Listeners: stats / cache-invalidation.
- `QualityProfileService` listens for `CustomFormatAddedEvent` / `CustomFormatDeletedEvent` to keep `FormatItems` in sync.
- No DelayProfile or ReleaseProfile events.

### Test files
- `src/NzbDrone.Core.Test/Profiles/QualityProfileServiceFixture.cs`
- `.../Profiles/ProfileRepositoryFixture.cs`
- `.../Profiles/Qualities/QualityIndexCompareToFixture.cs`
- `.../Profiles/Delay/DelayProfileServiceFixture.cs`
- `.../Profiles/Release/ReleaseProfileServiceFixture.cs`

### Quirks
- `QualityProfile.Items` may contain items whose `Quality` is null (a group); the comparer/index code handles both shapes.
- Group ids start at 1000 — collisions with future quality ids if the quality table grows past that.
- `QualityProfileService.GetIndex` returns `(0,0)` for an unknown id, which is "Unknown" — silent miss.
- `ReleaseProfilePreferredComparer` is dead-ish code (preferred-words feature migrated out).
- DelayProfile id 1 is "magic" — never deleted, never reordered.
- ReleaseProfile `Required`/`Ignored` regex slashes are stripped by `PerlRegexFactory` — escaping is a known gotcha.

### Open questions (P-Q*)
- **P-Q1**: `QualityProfileQualityItem.Items` nested groups currently support exactly two levels (group → leaves). Is the Go shape going to allow deeper nesting?
- **P-Q2**: When a user deletes a custom format, profiles silently lose its score row. Should the Go port surface this as a warning?
- **P-Q3**: The `MinUpgradeFormatScore` default value `1` is hardcoded; UI sometimes shows 0. Spec'd default?
- **P-Q4**: `ReleaseProfile.AirDateRestriction`/`AirDateGracePeriod` — actually consumed where? Search for usages before porting.
- **P-Q5**: `DelayProfileService.Reorder` has a `// TODO: This should throw` for an unknown id (line 114). Lock down the behavior in the Go port.
- **P-Q6**: Profile JSON format-items round-trip via `CustomFormatIntConverter` — do we need a parallel Go converter, or store full format objects?

---

## Subsystem 3: CustomFormats

### CustomFormat shape
`CustomFormats/CustomFormat.cs`:
```
{ Id, string Name, bool IncludeCustomFormatWhenRenaming,
  List<ICustomFormatSpecification> Specifications }
```
Equality is `Id`-only.

### Specifications
All implement `ICustomFormatSpecification` (`Specifications/ICustomFormatSpecification.cs`):
```
{ int Order, string InfoLink, string ImplementationName,
  string Name, bool Negate, bool Required,
  NzbDroneValidationResult Validate(),
  ICustomFormatSpecification Clone(),
  bool IsSatisfiedBy(CustomFormatInput input) }
```

`CustomFormatSpecificationBase` (`Specifications/CustomFormatSpecificationBase.cs`) implements the `Negate` flip and exposes `IsSatisfiedByWithoutNegate`.

`RegexSpecificationBase` (`Specifications/RegexSpecificationBase.cs`) — adds a compiled, IgnoreCase `Regex Value` field plus `MatchString(compared)`. Subclasses are `ReleaseTitleSpecification` and `ReleaseGroupSpecification`.

| # | Spec | File | Order | Inputs read | Notes |
|---|------|------|-------|-------------|-------|
| 1 | ReleaseTitleSpecification    | `Specifications/ReleaseTitleSpecification.cs`    | 1  | `EpisodeInfo.ReleaseTitle` OR `Filename` | Regex |
| 2 | LanguageSpecification        | `Specifications/LanguageSpecification.cs`        | 3  | `Languages` (vs. `Value` Language id; if `Value=Original`, uses `Series.OriginalLanguage`) | Has `ExceptLanguage` flag and a custom Negate path |
| 3 | IndexerFlagSpecification     | `Specifications/IndexerFlagSpecification.cs`     | 4  | `IndexerFlags.HasFlag((IndexerFlags)Value)` | Bitmask test |
| 4 | SourceSpecification          | `Specifications/SourceSpecification.cs`          | 5  | `EpisodeInfo.Quality.Quality.Source == (QualitySource)Value` | |
| 5 | ResolutionSpecification      | `Specifications/ResolutionSpecification.cs`      | 6  | `EpisodeInfo.Quality.Quality.Resolution == Value` | |
| 6 | SizeSpecification            | `Specifications/SizeSpecification.cs`            | 8  | `Size > Min.Gigabytes() && Size <= Max.Gigabytes()` | Min/Max are gigabytes |
| 7 | ReleaseGroupSpecification    | `Specifications/ReleaseGroupSpecification.cs`    | 9  | `EpisodeInfo.ReleaseGroup` | Regex |
| 8 | ReleaseTypeSpecification     | `Specifications/ReleaseTypeSpecification.cs`     | 10 | `ReleaseType == (ReleaseType)Value` | Single-episode / season-pack / multi-episode |

`Order` slots 2 and 7 are intentional gaps (no spec uses them).

`CustomFormatInput` (`CustomFormatInput.cs`):
```
{ ParsedEpisodeInfo EpisodeInfo, Series Series, long Size,
  IndexerFlags IndexerFlags, List<Language> Languages,
  string Filename, ReleaseType ReleaseType }
```

`SpecificationMatchesGroup` (`SpecificationMatchesGroup.cs`):
- Specs of a custom format are grouped by `GetType()` (all `ReleaseTitleSpecification`s in one group, all `LanguageSpecification`s in another, …).
- A group `DidMatch` iff: NO `Required` spec is `false` AND not ALL specs are `false`. So within a group, specs are OR'd, but a `Required` spec acts as an AND override.
- A custom format matches when **every** group `DidMatch`.

### Scoring math
1. `CustomFormatCalculationService.ParseCustomFormat(input)` (`CustomFormatCalculationService.cs:147-168`) iterates every custom format, evaluates groups, returns matched formats sorted alphabetically.
2. Profile sums score: `QualityProfile.CalculateCustomFormatScore(matchedFormats) = FormatItems.Where(x => matched.Contains(x.Format)).Sum(x => x.Score)`.
3. The result is consumed:
   - `DecisionEngine` for `MinFormatScore`, comparator tiebreak, upgrade vs disk file (see `decisionengine.md` §upgrade reasons).
   - `DelayProfile.BypassIfAboveCustomFormatScore` / `MinimumCustomFormatScore`.
   - `Organizer.{Custom Formats}` / `{Custom Format:foo}` rename tokens — only formats with `IncludeCustomFormatWhenRenaming=true`.

`ParseCustomFormat` has six entry overloads (RemoteEpisode, EpisodeFile×2, Blocklist, EpisodeHistory, LocalEpisode) — all build a `CustomFormatInput` from different sources with different fallbacks for `ReleaseTitle` (scene name → original path → relative path).

### Service / repository
- `ICustomFormatRepository`, `CustomFormatRepository` (`CustomFormatRepository.cs`) — straight `BasicRepository<CustomFormat>` over `CustomFormats` table.
- `ICustomFormatService`, `CustomFormatService` (`CustomFormatService.cs`):
  - Caches `Dictionary<int, CustomFormat>` keyed `"all"` (no expiry — explicit `Clear()` on every mutation).
  - `Insert` publishes `CustomFormatAddedEvent`; `Delete` publishes `CustomFormatDeletedEvent` **before** deleting from the DB, so `QualityProfileService` can clean up format items first.

### DB tables / migrations
- `CustomFormats` — `Id, Name (unique), IncludeCustomFormatWhenRenaming, Specifications (JSON)`. Migration `171_add_custom_formats.cs`, `175_language_profiles_to_custom_formats.cs`.
- `Specifications` is serialised via `CustomFormatSpecificationListConverter` (registered in `Datastore/TableMapping.cs:189`). The list is polymorphic — each entry's concrete spec class is stored alongside its fields.
- Profiles store CF ids only; resolved at load time (see `QualityProfileRepository.Query`).

### Events
- `CustomFormatAddedEvent` (`Events/CustomFormatAddedEvent.cs`) — payload: `CustomFormat`.
- `CustomFormatDeletedEvent` (`Events/CustomFormatDeletedEvent.cs`).

### Test files
- `src/NzbDrone.Core.Test/CustomFormats/CustomFormatsTestHelpers.cs`
- `.../CustomFormats/Specifications/LanguageSpecification/*`

### Quirks
- `LanguageSpecification` is the only spec that overrides `IsSatisfiedBy` itself rather than `IsSatisfiedByWithoutNegate` — its negate semantics differ when `ExceptLanguage` is also set.
- `Language.Original` is special — replaced at runtime with `Series.OriginalLanguage` when both are non-Unknown.
- `RegexSpecificationBase.Value` setter eagerly compiles; an empty string sets `_regex=null` and `MatchString` then returns false (so `Negate=true` flips an empty regex to "always match").
- `SizeSpecification.Min` is **exclusive** lower (`size > Min.Gigabytes()`), `Max` is **inclusive** (`size <= Max.Gigabytes()`).
- Custom formats serialize specifications as a polymorphic list — schema additions need converter updates.
- `SpecificationMatchesGroup.DidMatch` semantics (`Required` AND, others OR within type-group) are non-obvious — port comments verbatim.

### Open questions (CF-Q*)
- **CF-Q1**: Spec `Order` jumps 1, 3, 4, 5, 6, 8, 9, 10. Slots 2, 7 are reserved by something deleted (preferred words / language profile?). Renumber on Go port?
- **CF-Q2**: `IncludeCustomFormatWhenRenaming` — does the Go organizer need to mirror the C# token-filter syntax exactly (`{Custom Format:Foo}` returning empty when not included)?
- **CF-Q3**: `LanguageSpecification.ExceptLanguage` + `Negate` produces four logical combinations — confirm we have full unit coverage before porting.
- **CF-Q4**: `RegexSpecificationBase` always uses `IgnoreCase`. User-supplied case-sensitive regex isn't possible. Lock down or change?
- **CF-Q5**: `CustomFormatCalculationService.ParseCustomFormat` ignores all formats whose `Specifications` list is empty (since `groupBy` of nothing produces no groups, and `All(...)` over empty is vacuously true → format matches everything). Verify.

---

## Subsystem 4: Organizer

### Naming token system
The token grammar lives in `FileNameBuilder.TitleRegex` (`Organizer/FileNameBuilder.cs:48`):

```
\{(?<prefix>[- ._\[(]*)(?<token>[a-z0-9]+(?:(?<separator>[- ._]+)[a-z0-9]+)?)
   (?::(?<customFormat>[ ,a-z0-9+-]+(?<![- ])))?(?<suffix>[- ._)\]]*)\}
```

`{{` and `}}` are literal escapes. A token is **case-aware**: if all letters are lower-case, the replacement is lower-cased; if all upper, upper-cased. A `separator` of `_`/`.`/`-`/space inside the token (e.g. `{Series.Title}`) replaces spaces in the replacement with that separator.

Tokens are matched via `FileNameBuilderTokenEqualityComparer` (`FileNameBuilderTokenEqualityComparer.cs`) — case- and separator-insensitive (`{series_title}` == `{Series Title}` == `{series.title}`).

#### Series tokens (`AddSeriesTokens`, `FileNameBuilder.cs:451-467`)
- `{Series Title}`
- `{Series CleanTitle}`
- `{Series TitleYear}`
- `{Series CleanTitleYear}`
- `{Series TitleWithoutYear}`
- `{Series CleanTitleWithoutYear}`
- `{Series TitleThe}` (moves "The/An/A" to end)
- `{Series CleanTitleThe}`
- `{Series TitleTheYear}`
- `{Series CleanTitleTheYear}`
- `{Series TitleTheWithoutYear}`
- `{Series CleanTitleTheWithoutYear}`
- `{Series TitleFirstCharacter}` (single char A–Z, with diacritic strip; falls back to `_`)
- `{Series Year}`

#### Id tokens (`AddIdTokens`)
- `{ImdbId}`, `{TvdbId}`, `{TvMazeId}`, `{TmdbId}` (TvMaze/Tmdb empty when 0).

#### Season / episode tokens
- `{Season}`, `{season:00}` (`AddSeasonTokens`).
- `{Episode}`, `{episode:00}` — single value or first-last hyphen-joined for multi.
- `{Season Episode}` — composite handled via `AddSeasonEpisodeNumberingTokens` (`SeasonEpisodePatternRegex`, line 60). Pattern templates like `S{season:00}E{episode:00}` produce different output by `MultiEpisodeStyle` (see below).
- `{absolute}`, `{absolute:000}` — anime only, via `AddAbsoluteNumberingTokens`.

#### Air date / episode title tokens (`AddEpisodeTokens`, `AddEpisodeTitleTokens`)
- `{Air Date}` — `YYYY MM DD` (hyphen → space) or `Unknown`.
- `{Episode Title}` — joined with " + " by default, multi-episode shorter via collapse rules.
- `{Episode CleanTitle}` — same with `CleanTitle` applied, joined " and ".
- Both support `:N` truncation suffix (e.g. `{Episode Title:25}`); negative N truncates from the right.

#### Episode file tokens (`AddEpisodeFileTokens`)
- `{Original Title}`, `{Original Filename}` — preserve unrenamed input (used as fallback if `RenameEpisodes=false`).
- `{Release Group}` (defaults to `Sonarr` when empty).
- `{Release Hash}`.

#### Quality tokens (`AddQualityTokens`)
- `{Quality Full}` → `"{Title} {Proper} {Real}"`.
- `{Quality Title}` (from `QualityDefinitionService.Get(quality).Title`).
- `{Quality Proper}` — for anime: `"v{Version}"`; otherwise `"Proper"` if `Version > 1`, else empty.
- `{Quality Real}` — `"REAL"` if `Real > 0`.

#### MediaInfo tokens (`AddMediaInfoTokens`)
- `{MediaInfo Video}` / `{MediaInfo VideoCodec}`
- `{MediaInfo VideoBitDepth}`
- `{MediaInfo Audio}` / `{MediaInfo AudioCodec}`
- `{MediaInfo AudioChannels}`
- `{MediaInfo AudioLanguages}`, `{MediaInfo AudioLanguagesAll}`
- `{MediaInfo SubtitleLanguages}`, `{MediaInfo SubtitleLanguagesAll}`
- `{MediaInfo Simple}` (= `videoCodec audioCodec`)
- `{MediaInfo Full}` (= `videoCodec audioCodec [audioLangs] [subLangs]`)
- `{MediaInfo VideoDynamicRange}` (requires schema rev ≥ 5)
- `{MediaInfo VideoDynamicRangeType}` (requires schema rev ≥ 11)

If a `MediaInfo*` token in the pattern requires a higher schema rev than the file currently has, `UpdateMediaInfoIfNeeded` (line 816) calls `_mediaInfoUpdater.Update(file, series)` to refresh.

Audio/subtitle language tokens accept a custom-format filter:
- `{MediaInfo AudioLanguages:EN+DE}` → intersect with `[EN, DE]`.
- `{MediaInfo AudioLanguages:-EN}` → exclude `EN`.
- Trailing `+` (e.g. `EN+`) appends `--` placeholder if other languages were filtered out.
- Output is `[EN+DE]` style with brackets when `quoted=true`.
- Iso639B → T mapping (`Iso639BTMap`, line 89) covers 21 codes (alb→sqi, ger→deu, etc.).

#### Custom format tokens (`AddCustomFormats`)
- `{Custom Formats}` — joined by space; only formats with `IncludeCustomFormatWhenRenaming=true`.
- `{Custom Formats:Foo,Bar}` — only those names.
- `{Custom Formats:-Foo,Bar}` — exclude those names.
- `{Custom Format:Foo}` — single named format, returns `Foo` if matched, else empty.
- If no `customFormats` arg passed, calls `_formatCalculator.ParseCustomFormat(episodeFile, series)` to compute on the fly.

### File / folder template parser
- `BuildFileName(episodes, series, episodeFile, extension, namingConfig, customFormats)` (`FileNameBuilder.cs:226`) — returns final filename (no path).
  - If `RenameEpisodes=false`, returns `GetOriginalTitle(episodeFile, useCurrentFilenameAsFallback=true) + extension`.
  - Picks pattern by `SeriesType`: `StandardEpisodeFormat` / `DailyEpisodeFormat` (only when daily and seasonNumber > 0) / `AnimeEpisodeFormat` (only when all episodes have absolute numbers OR pattern doesn't contain `{absolute}`).
  - Splits on `\` / `/` to support multi-path templates (e.g. season folder embedded in episode template) and renders each segment independently.
  - Two-pass token rendering: first pass with all tokens except episode title (computes pattern length), then second pass with episode title sized to the remaining byte budget (`maxFilePathLength=260` minus path).
  - `FileNameCleanupRegex` collapses consecutive `[- ._]`; `TrimSeparatorsRegex` trims trailing separators.
  - `{ellipsis}` placeholder (`...`) is reinserted after `CleanFileName` strips dots.
  - `ReplaceReservedDeviceNames` handles `aux/com1-9/con/lpt1-9/nul/prn` → underscore.
- `BuildFilePath(...)` — `Path.Combine(BuildSeasonPath(series, seasonNumber), BuildFileName(...))`.
- `BuildSeasonPath(series, seasonNumber)` — `Path.Combine(series.Path, GetSeasonFolder(...))` if `series.SeasonFolder=true`, else just `series.Path`.
- `GetSeasonFolder(series, seasonNumber, namingConfig)` — uses `SpecialsFolderFormat` if `seasonNumber == 0`, else `SeasonFolderFormat`. Tokens: series tokens + `{Season}`/`{season:00}`.
- `GetSeriesFolder(series, namingConfig)` — uses `SeriesFolderFormat`. Tokens: series + ID tokens only.

### Episode title cleaning (`CleanTitle`, line 302)
- Replace `&` → `and`.
- `ScenifyReplaceChars` (forward slash) → space.
- `ScenifyRemoveChars` strips quoting/punctuation: `,<>;:'"|\`/~!?@$%^*-_=` adjacent to whitespace, possessive `'s/'m/'t/'ve/'ll/'d/'re`, all parentheses/brackets/braces.
- `RemoveDiacritics()`.

`CleanupEpisodeTitle` (line 1085) — `MultiPartCleanupRegex` strips `(2)` / `Part 2` / `Pt. 2` from end (used during multi-episode title joining).

`GetEpisodeTitle` (line 1041) collapses long multi-titles:
- 1 episode: trimmed title.
- 2+ episodes joined with `" + "` (or `" and "` for clean): if total ≤ maxLength, return.
- Otherwise: `firstTitle{ellipsis}lastTitle` if it fits, else `firstTitle{ellipsis}`, else truncate first.

Truncation suffix `:N` (e.g. `{Series Title:30}`) — positive: trim end + `…`; negative: trim start, `…` + reversed-trim. (`Truncate`, line 1207.)

### Multi-episode style handling
`MultiEpisodeStyle` enum (`FileNameBuilder.cs:1259`):

| Id | Name | Format applied (default `S{season:00}E{episode:00}` pattern, eps 1-3) |
|----|------|------|
| 0 | Extend       | `S01E01-02-03` (default fallthrough) |
| 1 | Duplicate    | `S01E01S01E02S01E03` |
| 2 | Repeat       | `S01E01E02E03` |
| 3 | Scene        | `S01E01-E02-E03` |
| 4 | Range        | `S01E01-03` |
| 5 | PrefixedRange | `S01E01-E03` (default for new installs) |

Anime absolute (`AddAbsoluteNumberingTokens`, line 532) has its own switch with very similar branches; `Range`/`PrefixedRange` use only first+last episode.

### Colon replacement
`ColonReplacementFormat` enum (`FileNameBuilder.cs:1269`):
- `0 Delete`
- `1 Dash`         → `-`
- `2 SpaceDash`    → ` -`
- `3 SpaceDashSpace` → ` - `
- `4 Smart` (default — `: ` → ` - `, lone `:` → `-`)
- `5 Custom` (uses `NamingConfig.CustomColonReplacementFormat`)

### NamingConfig
`NamingConfig.cs`:
```
{ RenameEpisodes=false, ReplaceIllegalCharacters=true,
  ColonReplacementFormat=Smart, CustomColonReplacementFormat="",
  MultiEpisodeStyle=PrefixedRange,
  StandardEpisodeFormat = "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}",
  DailyEpisodeFormat    = "{Series Title} - {Air-Date} - {Episode Title} {Quality Full}",
  AnimeEpisodeFormat    = "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}",
  SeriesFolderFormat = "{Series Title}",
  SeasonFolderFormat = "Season {season}",
  SpecialsFolderFormat = "Specials" }
```
Singleton row in DB (one record only, lazily inserted via `NamingConfigService.GetConfig` — note the `lock(_repository)` double-check pattern).

`BadCharacters = ["\","/","<",">","?","*","|",`\""`]` → `GoodCharacters = ["+","+","","","!","-","",""]` when `ReplaceIllegalCharacters=true`, else stripped.

### Validation
`FileNameValidation.cs`:
- `ValidStandardEpisodeFormat` — must contain season+episode pattern OR `{Original Title}`/`{Original Filename}`.
- `ValidDailyEpisodeFormat` — same plus `{Air Date}`.
- `ValidAnimeEpisodeFormat` — same plus `{absolute}` token.
- `ValidSeriesFolderFormat` — must contain a `{Series ...Title...}` token (`SeriesTitleRegex`).
- `ValidSeasonFolderFormat` — must contain `{season}`.
- `ValidSpecialsFolderFormat` — `FilteredSubfolderValidator` rejects names matching `DiskScanService.FilteredSubFolderMatches` (e.g. `extras`, `featurettes`).
- `IllegalCharactersValidator` — `Path.GetInvalidPathChars()`.
- `IllegalColonCharactersValidator` — bad chars + `:` (used for `CustomColonReplacementFormat`).

`FileNameValidationService` (`FileNameValidationService.cs`) — round-trips a sample by re-parsing the produced filename and checking `(seasonNumber, episodeNumbers)` round-trip.

`FileNameSampleService` (`FileNameSampleService.cs`) — fixture data factory used by the UI's "preview" feature.

### Service / repository
- `INamingConfigRepository`, `NamingConfigRepository` (`NamingConfigRepository.cs`).
- `INamingConfigService`, `NamingConfigService` — single-row upsert; default lazily seeded from `NamingConfig.Default`.
- `IBuildFileNames`, `FileNameBuilder` — main consumer; injected into rename, import, and the API.

### DB tables / migrations
- `NamingConfig` (singleton) — `007_add_renameEpisodes_to_naming.cs`, `029_add_formats_to_naming_config.cs`, `030_add_season_folder_format_to_naming_config.cs`, `031_delete_old_naming_config_columns.cs`, `035_add_series_folder_format_to_naming_config.cs`, `093_naming_config_replace_characters.cs`, `134_add_specials_folder_format.cs`, `189_add_colon_replacement_to_naming_config.cs`.

### Events / jobs
- No naming-specific events. Renames are driven by `RenameEpisodeFileService` which calls `IBuildFileNames` directly.

### Test files
- `src/NzbDrone.Core.Test/OrganizerTests/FileNameBuilderTests/*` — 28 fixtures (full token coverage: clean title, multi-episode, colon, custom formats, ID tokens, original title, replace chars, reserved device, series first character, truncation, multi-episode title collapse, …).
- `.../OrganizerTests/BuildFilePathFixture.cs`
- `.../OrganizerTests/CleanFilenameFixture.cs`
- `.../OrganizerTests/GetSeasonFolderFixture.cs`
- `.../OrganizerTests/GetSeriesFolderFixture.cs`

### Quirks
- The token regex demands a leading letter+digit but allows `(?<separator>[- ._]+)` between word parts — so `{Series.Title}` and `{Series_Title}` are both valid. `FileNameBuilderTokenEqualityComparer` strips that to compare.
- A token handler that returns `null` (vs empty string) leaves the original token text in place. `AddEpisodeTitlePlaceholderTokens` uses this to defer `{Episode Title}` resolution until after length-budget computation.
- Lower/UPPER casing applies after `Trim()` of replacement text — `{series title}` lower-cases `Some Show` to `some show`; mixed-case in input retains its case.
- `{ellipsis}` placeholder dance: dots get stripped during `CleanFileName`, so `…` is staged as `{ellipsis}` and substituted last.
- `ScenifyRemoveChars` regex has 25 alternations and is brittle — port verbatim.
- `MultiEpisodeStyle.Extend` is the **default fallthrough** in the switch (not a named case), id=0.
- Anime episode pattern resolution swaps `Repeat` separator to `" "` if blank — only branch that diverges from the standard table.
- `MediaInfo VideoDynamicRange*` tokens trigger an on-demand re-read of the file's mediainfo if the schema rev is too low — side-effecting from the rename pipeline.
- `Iso639BTMap` translations cover language code aliases (alb/sqi etc.). Source URL is a comment (line 88).
- `BadCharacters[]` is mapped to `GoodCharacters[]` index-wise — order coupling.

### Open questions (ORG-Q*)
- **ORG-Q1**: Token equality comparer ignores `[- ._]+` separators inside the token name — does the Go implementation need to mirror this exactly, or normalize templates on save?
- **ORG-Q2**: `RequiresEpisodeTitle` only checks `{Episode Title}`/`{Episode CleanTitle}`. If we add `{Episode TitleSomething}` we need to update this — do we want a derived approach?
- **ORG-Q3**: `MediaInfo`-driven rename triggers a `_mediaInfoUpdater.Update` when schema rev is low. In Go, do we call mediainfo synchronously here or defer?
- **ORG-Q4**: `:N` truncation is byte-based via `GetByteCount` — for non-ASCII titles, current behavior may cut mid-codepoint? Confirm.
- **ORG-Q5**: `CleanFileName` runs after token replacement, but `replacementText` already has `{ellipsis}` baked in — it's stripped, then re-substituted in `BuildFileName`. Confirm Go ordering matches.
- **ORG-Q6**: `FileNameSampleService` static fields are mutated in the constructor — fine for the singleton DI lifetime in C# but a footgun in Go. Make instance-scoped on port.
- **ORG-Q7**: When a custom-format token is used (`{Custom Formats}`) and the caller didn't pre-compute, the builder calls `_formatCalculator.ParseCustomFormat(episodeFile, series)` — synchronous DB-touching call inside rename. Do we precompute upstream in Go?

---

## Cross-subsystem touchpoints

- **DecisionEngine (`go/analysis/subsystems/decisionengine.md`)**: `QualityProfile` drives `QualityAllowedByProfileSpecification`, `CustomFormatAllowedByProfileSpecification`, `UpgradeAllowedSpecification`, `UpgradableSpecification`, `UpgradeDiskSpecification`, `QueueSpecification`, `HistorySpecification`, `DelaySpecification`, `AcceptableSizeSpecification`. Comparator (`DownloadDecisionComparer`) uses profile index + revision + CF score as primary tiebreakers.
- **MediaFiles import**: `AggregateQuality` + augmenters produce the `QualityModel`; `CustomFormatCalculationService.ParseCustomFormat(LocalEpisode, fileName)` evaluates formats on import.
- **History/Blocklist**: store quality/languages/indexer flags; CFs are recomputed on demand from the stored `SourceTitle`.
- **API V3**: profiles + qualities + naming config + custom formats are all surfaced as separate endpoints (frozen Newtonsoft schema).
