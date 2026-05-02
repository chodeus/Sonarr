# Embedded-document catalogue

An `IEmbeddedDocument` (`src/NzbDrone.Core/Datastore/IEmbeddedDocument.cs`) is a marker interface used by `TableMapping.RegisterEmbeddedConverter` (`src/NzbDrone.Core/Datastore/TableMapping.cs:227-249`) to auto-wire a Dapper `SqlMapper.TypeHandler` around `EmbeddedDocumentConverter<T>` (`src/NzbDrone.Core/Datastore/Converters/EmbeddedDocumentConverter.cs:9-52`). Every implementer is round-tripped to/from a `TEXT` column as a `System.Text.Json` payload using a fixed serializer profile: **camelCase property names**, **camelCase enum-string** (via `JsonStringEnumConverter`), **`WhenWritingNull` ignore for nulls**, **case-insensitive read**, **trailing-comma tolerant**, **indented**, with `STJUtcConverter` (`yyyy'-'MM'-'dd'T'HH':'mm':'ssZ`) for `DateTime` and `STJTimeSpanConverter` for `TimeSpan`. There is no Newtonsoft involvement on the embedded-doc path despite an earlier note in `subsystems/datastore.md`; see EDOC-Q1. The Go port has to reproduce this contract field-for-field — every JSON key, every enum form, every default-on-missing, every nested composition — because the C# code reads existing user databases written by Sonarr v3 / v4 and any drift breaks upgrade. This document is the porter's exhaustive contract. Migrations that mutate any embedded shape are linked per-type and must be replicated byte-for-byte (cross-cutting with `migrations/ledger.md` once that exists).

## Index

The 13 types directly marked `IEmbeddedDocument` (verified by `grep -rln IEmbeddedDocument src/NzbDrone.Core` — matches the count in `subsystems/datastore.md:99-116`):

| # | Type | Source file | Stored on (table.column) | Shape | Notes |
|---|------|-------------|--------------------------|-------|-------|
| 1 | `Quality` | `src/NzbDrone.Core/Qualities/Quality.cs` | composed inside `QualityModel.quality`; never stored standalone | object | Wire form is a **plain int** when written by the `QualityIntConverter` for `QualityModel`; otherwise full object. Polymorphic depending on container. |
| 2 | `QualityModel` | `src/NzbDrone.Core/Qualities/QualityModel.cs` | `EpisodeFiles.Quality`, `History.Quality`, `Blocklist.Quality`, `PendingReleases.ParsedEpisodeInfo` (nested), `DownloadHistory.Release` (indirect via `Release` JSON) | object | Wraps `Quality` (as int via `QualityIntConverter`) + `Revision`. `[JsonIgnore]` on three `*DetectionSource` properties. |
| 3 | `MonitoringOptions` | `src/NzbDrone.Core/Tv/MonitoringOptions.cs` | `Series.AddOptions` | object | `Monitor` is a string-enum (camelCase) from `MonitorTypes` (14 values). |
| 4 | `Actor` | `src/NzbDrone.Core/Tv/Actor.cs` | inside Series JSON arrays (currently **unused** in DB writes — see EDOC-Q3) | object | Composed of `MediaCover[]`. |
| 5 | `Ratings` | `src/NzbDrone.Core/Tv/Ratings.cs` | `Series.Ratings`, `Episodes.Ratings` | object | `Value` is `decimal`; STJ default produces JSON number. |
| 6 | `Season` | `src/NzbDrone.Core/Tv/Season.cs` | `Series.Seasons` | array of object | List in column. |
| 7 | `Language` | `src/NzbDrone.Core/Languages/Language.cs` | `EpisodeFiles.Languages`, `History.Languages`, `Blocklist.Languages`, `DownloadHistory.Languages`, `SubtitleFiles.LanguageTags` (since 195), inside `ParsedEpisodeInfo.Languages`, inside `ReleaseInfo.Languages` | array of int (via `LanguageIntConverter`) | `Language` is registered both as an `EmbeddedDocumentConverter<Language>` (full object form) AND, in the lists wired by `TableMapping.cs:198`, as an int via `LanguageIntConverter`. The list path overrides the per-element converter — list rows are bare ints. EDOC-Q4. |
| 8 | `MediaCover` | `src/NzbDrone.Core/MediaCover/MediaCover.cs` | `Series.Images`, `Episodes.Images`, inside `Actor.Images` | array of object | `CoverType` is a camelCase enum string. |
| 9 | `ProfileFormatItem` | `src/NzbDrone.Core/Profiles/ProfileFormatItem.cs` | `QualityProfiles.FormatItems` | array of object | `Format` is a `CustomFormat` written as **bare int Id** via `CustomFormatIntConverter` (`TableMapping.cs:188`). |
| 10 | `QualityProfileQualityItem` | `src/NzbDrone.Core/Profiles/Qualities/QualityProfileQualityItem.cs` | `QualityProfiles.Items` | array of object | `Quality` written as **bare int** via `QualityIntConverter`. Self-referential `Items` list (groups). `Id` field is `[JsonIgnore(Condition = WhenWritingDefault)]` — only emitted when non-zero. |
| 11 | `MediaInfoModel` | `src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoModel.cs` | `EpisodeFiles.MediaInfo` | object | Largest embedded doc by field count. `RunTime` is `TimeSpan` via `STJTimeSpanConverter`. `Title` is `[JsonIgnore]`. |
| 12 | `MediaInfoAudioStreamModel` | `src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoAudioStreamModel.cs` | inside `MediaInfoModel.AudioStreams` | array of object | Added in migration 225. |
| 13 | `MediaInfoSubtitleStreamModel` | `src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoSubtitleStreamModel.cs` | inside `MediaInfoModel.SubtitleStreams` | array of object | Added in migration 225. |

Additional types that are **not** marked `IEmbeddedDocument` but are JSON-embedded via explicit `EmbeddedDocumentConverter<T>` registrations in `TableMapping.RegisterMappers` (`src/NzbDrone.Core/Datastore/TableMapping.cs:179-213`). These are part of the same wire contract:

| # | Type | Source file | Stored on (table.column) | Shape | Notes |
|---|------|-------------|--------------------------|-------|-------|
| 14 | `ParsedEpisodeInfo` | `src/NzbDrone.Core/Parser/Model/ParsedEpisodeInfo.cs` | `PendingReleases.ParsedEpisodeInfo` | object | Embeds `QualityModel`, `Languages`, `SeriesTitleInfo`. Many derived getters with private setters (`IsDaily`, `IsAbsoluteNumbering`, `ReleaseType`, `Age`, …) that **are** serialised by STJ because they have setters. EDOC-Q5. |
| 15 | `ReleaseInfo` | `src/NzbDrone.Core/Parser/Model/ReleaseInfo.cs` | `PendingReleases.Release`, `DownloadHistory.Release` | object | `IndexerFlags` and `PendingReleaseReason` are `[JsonIgnore]`. `Age*` derived getters serialised via setters (same trap as `ParsedEpisodeInfo`). |
| 16 | `PendingReleaseAdditionalInfo` | `src/NzbDrone.Core/Download/Pending/PendingRelease.cs:21-26` | `PendingReleases.AdditionalInfo` | object | Two int-backed enums, written as camelCase strings. |
| 17 | `Dictionary<string,string>` | (built-in) | `IndexerStatus.Cookies` | object | Generic dictionary; STJ camelCase key policy applies. |
| 18 | `IDictionary<string,string>` | (built-in) | (interface alias) | object | Same as above. |
| 19 | `List<int>` | (built-in) | `Notifications.Tags`, `Indexers.Tags`, `DownloadClients.Tags`, `ImportLists.Tags`, `ReleaseProfiles.Tags`, `DelayProfiles.Tags`, `Blocklist.EpisodeIds`, `ReleaseProfiles.IndexerIds` (since 224), `ReleaseProfiles.ExclusionTags` (since 221) | array of int | Plain JSON int array. |
| 20 | `List<KeyValuePair<string,int>>` | (built-in) | `CustomFilters.Filters` (legacy) | array of object | Each element `{key, value}` — STJ default for `KeyValuePair`. |
| 21 | `KeyValuePair<string,int>` | (built-in) | (composed) | object | `{key, value}`. |
| 22 | `HashSet<int>` | (built-in) | `AutoTagging.Tags` | array of int | Order is unspecified. |
| 23 | `List<string>` | (built-in via `StringListConverter`) | `ReleaseProfiles.Required`, `ReleaseProfiles.Ignored`, `Series.Genres`, etc. | array of string OR comma-separated fallback | `StringListConverter.Parse` (`src/NzbDrone.Core/Datastore/Converters/StringListConverter.cs`) **falls back to comma-split** when the cell does not start with `[` and end with `]`. This is a downgrade-resilience hack for users who cycled Sonarr versions. EDOC-Q6. |
| 24 | `List<Language>` | (handled) | `EpisodeFiles.Languages`, `History.Languages`, `Blocklist.Languages`, etc. | array of int | Wired via `LanguageIntConverter` (`TableMapping.cs:198`). |
| 25 | `List<QualityProfileQualityItem>` | (handled) | `QualityProfiles.Items` | array of object | Wired via `QualityIntConverter` (`TableMapping.cs:187`). |
| 26 | `List<ProfileFormatItem>` | (handled) | `QualityProfiles.FormatItems` | array of object | Wired via `CustomFormatIntConverter` (`TableMapping.cs:188`). |
| 27 | `List<ICustomFormatSpecification>` | polymorphic envelope | `CustomFormats.Specifications` | array of `{type, body}` envelope objects | See §"Polymorphic envelopes". |
| 28 | `List<IAutoTaggingSpecification>` | polymorphic envelope | `AutoTagging.Specifications` | array of `{type, body}` envelope objects | See §"Polymorphic envelopes". |
| 29 | `Command` (and subclasses) | `src/NzbDrone.Core/Messaging/Commands/Command.cs` | `Commands.Body` | object | Class-name-based polymorphism (`name` field) via `CommandConverter` (`src/NzbDrone.Core/Datastore/Converters/CommandConverter.cs`). Class-name-string fragility: TP-Q1 cross-cut. Falls back to `UnknownCommand` when type not found. |

Provider settings classes (`IProviderConfig` implementers) are **also** JSON-embedded but live on a per-table `Settings` column where the polymorphism is keyed by the `ConfigContract` *column*, not an inline type field — see §"Provider Settings (IProviderConfig)" below. ~50 such classes exist across indexers, download clients, notifications, import lists, metadata writers; they are catalogued by reference to `subsystems/thingiprovider.md` and the in-progress `providers/*.md` matrix.

---

## JSON serialiser profile (the universal contract)

Every embedded-doc converter (with the exception of polymorphic envelopes which delegate to STJ but write a wrapper) inherits from `EmbeddedDocumentConverter<T>` and uses these `JsonSerializerOptions` (`src/NzbDrone.Core/Datastore/Converters/EmbeddedDocumentConverter.cs:13-29`):

| Setting | Value | Go-port equivalent |
|---------|-------|--------------------|
| `PropertyNamingPolicy` | `JsonNamingPolicy.CamelCase` | per-field `json:"name"` tags or a custom marshaller. Go's `encoding/json` defaults to **field-name passthrough**, not camelCase, so explicit tags are mandatory. |
| `DictionaryKeyPolicy` | `JsonNamingPolicy.CamelCase` | Maps with string keys must be camelCase. |
| `PropertyNameCaseInsensitive` | `true` (read) | `encoding/json` is case-insensitive on read by default — matches. |
| `DefaultIgnoreCondition` | `WhenWritingNull` | `omitempty` on every nullable / reference field. **Critical**: STJ writes the field for **non-null defaults** (e.g. `0`, `false`, empty string), the Go zero-value path under `omitempty` would silently omit those — meaning a freshly written Go-side `false` boolean that C# would emit as `"foo": false` would be dropped. The Go port must NOT use `omitempty` for value-typed fields. EDOC-Q7. |
| `WriteIndented` | `true` | The DB cell contains pretty-printed JSON with newlines + 2-space indent. Cosmetic but every existing user DB has indented payloads. EDOC-Q8: should the Go port match? Probably yes, to make `git diff` of test golden files stable; absolutely doesn't affect parse correctness. |
| `AllowTrailingCommas` | `true` | `encoding/json` rejects trailing commas. Must use a permissive parser or pre-strip. |
| `JsonStringEnumConverter(JsonNamingPolicy.CamelCase, true)` | global | Enums emit as **camelCase strings**, with the third arg `allowIntegerValues=true` meaning **read-side accepts integers OR strings**. Some embedded types pre-date this converter and store ints (e.g. `MonitorTypes` was originally int) — round-trip support both. EDOC-Q9. |
| `STJUtcConverter` | for `DateTime` | Reads any parseable string, writes `yyyy-MM-ddTHH:mm:ssZ` (no fractional seconds, no offset, **always Z**). The Go port must not use `time.RFC3339Nano`. |
| `STJTimeSpanConverter` | for `TimeSpan` | Reads/writes `TimeSpan.ToString()` default form `[d.]hh:mm:ss[.fffffff]`. Go's `time.Duration.String()` (`"1h30m0s"`) is **incompatible** — must hand-roll. EDOC-Q10. |

Per-handler converters injected through the second constructor (`params JsonConverter[]`) augment the base list — e.g. `EmbeddedDocumentConverter<QualityModel>(new QualityIntConverter())` adds a `JsonConverter<Quality>` that writes the int form for the `Quality` property, but **not** for free-standing `Quality` reads via the embedded handler (that path uses the full object form). This per-container override is why `QualityModel.quality` is `7` but a hypothetical free `Quality` write would be `{"id":7,"name":...}`.

---

## Per-type detail

### 1. `Quality` (`src/NzbDrone.Core/Qualities/Quality.cs`)

**Stored on:** never independently. Always nested inside `QualityModel.quality` (where it is reduced to a bare int via `QualityIntConverter`) or inside `QualityProfileQualityItem.quality` (same int reduction). The full object form is reachable via REST only.

**Wire shape (full form, used only for free-standing reads — e.g. tests):**

```json
{
  "id": 7,
  "name": "Bluray-1080p",
  "source": "bluray",
  "resolution": 1080
}
```

**Wire shape (reduced form, the actual DB cell content via int-converter):** `7`

**Field table (full form):**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Id` | `int` | `id` | number | `0` (treated as `Quality.Unknown` per `Quality.FindById`) | Drives the int-reduced form. |
| `Name` | `string` | `name` | string | `null` | Server-side derived from `Id` lookup; not authoritative. |
| `Source` | `QualitySource` (enum) | `source` | camelCase string (`unknown`, `television`, `televisionRaw`, `web`, `webRip`, `dvd`, `bluray`, `blurayRaw`) | `unknown` | `JsonStringEnumConverter` with `allowIntegerValues=true` accepts ints on read. |
| `Resolution` | `int` | `resolution` | number | `0` | |

**Migration history affecting wire shape:**

- `036_update_with_quality_converters.cs` — first conversion of legacy quality-model JSON into this shape.
- `037_add_configurable_qualities.cs`
- `062_convert_quality_models.cs` — table-wide rewrite, quality enum re-numbering.
- `069_quality_proper.cs` — added Real/Repack to Revision (sister class).
- `084_update_quality_minmax_size.cs`

**Open questions:** EDOC-Q11.

---

### 2. `QualityModel` (`src/NzbDrone.Core/Qualities/QualityModel.cs`)

**Stored on:** `EpisodeFiles.Quality`, `History.Quality`, `Blocklist.Quality`, nested inside `ParsedEpisodeInfo.quality` (which is inside `PendingReleases.ParsedEpisodeInfo`).

**Wire shape:**

```json
{
  "quality": 7,
  "revision": {
    "version": 1,
    "real": 0,
    "isRepack": false
  }
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Quality` | `Quality` | `quality` | bare int (via per-container `QualityIntConverter`) | `0` ⇒ `Quality.Unknown` | The int reduction is the per-container override; without it, full object would be written. |
| `Revision` | `Revision` | `revision` | nested object `{version, real, isRepack}` | `{version: 1, real: 0, isRepack: false}` (ctor default) | Sub-shape of `Revision` below. |
| `SourceDetectionSource` | `QualityDetectionSource` | — | — | — | `[JsonIgnore]` |
| `ResolutionDetectionSource` | `QualityDetectionSource` | — | — | — | `[JsonIgnore]` |
| `RevisionDetectionSource` | `QualityDetectionSource` | — | — | — | `[JsonIgnore]` |

**`Revision` sub-shape** (`src/NzbDrone.Core/Qualities/Revision.cs`):

| C# property | JSON key | Form | Default | Notes |
|-------------|----------|------|---------|-------|
| `Version` | `version` | int | `1` | |
| `Real` | `real` | int | `0` | |
| `IsRepack` | `isRepack` | bool | `false` | |

**Migration history:**

- `036_update_with_quality_converters.cs`
- `062_convert_quality_models.cs`
- `069_quality_proper.cs` — adds `Real` to revision.
- `181_quality_definition_preferred_size.cs` — sister `QualityDefinitions` table change; doesn't touch this JSON.

**Open questions:** EDOC-Q11, EDOC-Q12.

---

### 3. `MonitoringOptions` (`src/NzbDrone.Core/Tv/MonitoringOptions.cs`)

**Stored on:** `Series.AddOptions`.

**Wire shape:**

```json
{
  "ignoreEpisodesWithFiles": false,
  "ignoreEpisodesWithoutFiles": false,
  "monitor": "all"
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `IgnoreEpisodesWithFiles` | `bool` | `ignoreEpisodesWithFiles` | bool | `false` | |
| `IgnoreEpisodesWithoutFiles` | `bool` | `ignoreEpisodesWithoutFiles` | bool | `false` | |
| `Monitor` | `MonitorTypes` | `monitor` | camelCase enum string | `unknown` | Values: `unknown`, `all`, `future`, `missing`, `existing`, `firstSeason`, `lastSeason`, `latestSeason` (`[Obsolete]`), `pilot`, `recent`, `monitorSpecials`, `unmonitorSpecials`, `none`, `skip`. **Older DB rows store the enum as integer** — `allowIntegerValues=true` covers read-back. |

**Migration history:** `MonitoringOptions` is set on add only and is occasionally cleared via Series writes; no migration rewrites the JSON. Series-level migrations like `053_add_series_sorttitle.cs`, `166_update_series_sort_title.cs`, `183_update_images_remote_url.cs` touch the `Series` row but not this column directly.

**Open questions:** EDOC-Q13 — `[Obsolete] LatestSeason` enum slot is still present; legacy DBs may have it on disk. The Go port must accept the string `latestSeason` for read.

---

### 4. `Actor` (`src/NzbDrone.Core/Tv/Actor.cs`)

**Stored on:** *currently not used for DB writes in v5* — the `Series` model does not expose an `Actors` JSON column. Retained for legacy parity (older DBs may have rows where Series JSON included actor lists in a sidecar shape; the v5 `Series` migration history does not preserve it). EDOC-Q3.

**Wire shape:**

```json
{
  "name": "...",
  "character": "...",
  "images": [
    { "coverType": "headshot", "url": null, "remoteUrl": "..." }
  ]
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Name` | `string` | `name` | string | `null` | |
| `Character` | `string` | `character` | string | `null` | |
| `Images` | `List<MediaCover>` | `images` | array of `MediaCover` | `[]` (ctor default) | |

**Open questions:** EDOC-Q3.

---

### 5. `Ratings` (`src/NzbDrone.Core/Tv/Ratings.cs`)

**Stored on:** `Series.Ratings`, `Episodes.Ratings`.

**Wire shape:**

```json
{
  "votes": 1234,
  "value": 8.5
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Votes` | `int` | `votes` | int | `0` | |
| `Value` | `decimal` | `value` | number | `0` | STJ writes `decimal` as a number with arbitrary precision (no exponent for typical 0–10 ratings). The Go port should use `float64` only if precision is acceptable (see EDOC-Q14); a `*big.Float` or string round-trip preserves bit-for-bit. |

**Migration history:** `073_clear_ratings.cs` (wipes data).

---

### 6. `Season` (`src/NzbDrone.Core/Tv/Season.cs`)

**Stored on:** `Series.Seasons` (list of object).

**Wire shape (single element):**

```json
{
  "seasonNumber": 1,
  "monitored": true,
  "images": []
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `SeasonNumber` | `int` | `seasonNumber` | int | `0` | |
| `Monitored` | `bool` | `monitored` | bool | `false` | |
| `Images` | `List<MediaCover>` | `images` | array | `[]` (ctor default) | Effectively never populated in v5 — Series-level `Images` covers UI. |

**Migration history:** `021_drop_seasons_table.cs` — folded standalone `Seasons` table into this JSON column.

---

### 7. `Language` (`src/NzbDrone.Core/Languages/Language.cs`)

**Stored on:** `EpisodeFiles.Languages`, `History.Languages`, `Blocklist.Languages`, `DownloadHistory.Languages`, `SubtitleFiles.LanguageTags` (as list-of-int since 195), composed inside `ParsedEpisodeInfo.languages` and `ReleaseInfo.languages`. The `Series.OriginalLanguage` column is a **scalar int** (not JSON) — handled by `DapperLanguageIntConverter`, not this JSON path.

**Wire shape (list-of-int form, the actual DB shape):** `[1, 4]`

**Wire shape (full-object fallback form, never reached for list columns due to per-container `LanguageIntConverter`):**

```json
{ "id": 1, "name": "English" }
```

**Field table (full form):**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Id` | `int` | `id` | int | `0` (`Language.Unknown`) | Drives reduced form. The `Original` sentinel has `id = -2`. |
| `Name` | `string` | `name` | string | `null` | Server-side derived from `FindById`. |

**Read-side null handling:** `LanguageIntConverter.Read` returns `Language.Unknown` for JSON `null` (`HandleNull => true`). The Go port must do the same — a list cell `[null, 1]` should yield `[Unknown, English]`, not an error.

**Migration history:**

- `162_release_profile_to_array.cs` — Required/Ignored arrays.
- `169_add_malayalam_and_ukrainian_languages.cs`
- `175_language_profiles_to_custom_formats.cs` — drops `LanguageProfileId`, collapses scalar `Language` into JSON `Languages` array. Major shape transition.
- `195_parse_language_tags_from_existing_subtitle_files.cs` — populates `SubtitleFiles.LanguageTags` list.

**Open questions:** EDOC-Q4, EDOC-Q15 (the `Original` sentinel `id=-2` round-trip).

---

### 8. `MediaCover` (`src/NzbDrone.Core/MediaCover/MediaCover.cs`)

**Stored on:** `Series.Images`, `Episodes.Images`, composed inside `Actor.Images`, `Season.Images`.

**Wire shape (single element):**

```json
{
  "coverType": "poster",
  "url": null,
  "remoteUrl": "https://..."
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `CoverType` | `MediaCoverTypes` | `coverType` | camelCase enum string | `unknown` | Values: `unknown`, `poster`, `banner`, `fanart`, `screenshot`, `headshot`, `clearlogo`. Backed by ints `0..6`. |
| `Url` | `string` | `url` | string | `null` (omitted under `WhenWritingNull`) | Local URL inside Sonarr's MediaCover cache. |
| `RemoteUrl` | `string` | `remoteUrl` | string | `null` | Source URL on metadata provider. |

**Migration history:** `183_update_images_remote_url.cs` — populates `RemoteUrl` for legacy rows.

---

### 9. `ProfileFormatItem` (`src/NzbDrone.Core/Profiles/ProfileFormatItem.cs`)

**Stored on:** `QualityProfiles.FormatItems` (list).

**Wire shape (single element):**

```json
{ "format": 7, "score": 100 }
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Format` | `CustomFormat` | `format` | bare int Id (via `CustomFormatIntConverter`) | `0` | Per-container int reduction. The full `CustomFormat` object lives in the `CustomFormats` table; this is just the FK. |
| `Score` | `int` | `score` | int | `0` | |

**Migration history:**

- `171_add_custom_formats.cs` — first introduction.
- `175_language_profiles_to_custom_formats.cs` — bulk-populates from old language-profile shape.
- `212_add_minium_upgrade_format_score_to_quality_profiles.cs` — companion `MinUpgradeFormatScore` column on `QualityProfiles`, doesn't touch this JSON.

---

### 10. `QualityProfileQualityItem` (`src/NzbDrone.Core/Profiles/Qualities/QualityProfileQualityItem.cs`)

**Stored on:** `QualityProfiles.Items` (list, recursive).

**Wire shape (group with nested items):**

```json
{
  "name": "WEB 1080p",
  "items": [
    { "quality": 12, "allowed": true },
    { "quality": 14, "allowed": true }
  ],
  "allowed": true
}
```

**Wire shape (leaf):**

```json
{ "quality": 4, "allowed": false }
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Id` | `int` | `id` | int | omitted when `0` (`[JsonIgnore(Condition = WhenWritingDefault)]`) | Only present on group rows after the user saves a custom group. |
| `Name` | `string` | `name` | string | `null` (omitted) | Group label. |
| `Quality` | `Quality` | `quality` | bare int (via `QualityIntConverter`) | `null` for groups, an int for leaves | Mutually exclusive with `Items` — group rows have `null` quality, leaf rows have `null`/empty `items`. |
| `Items` | `List<QualityProfileQualityItem>` | `items` | array | `[]` (ctor default) | Recursive. |
| `Allowed` | `bool` | `allowed` | bool | `false` | |
| `MinSize` | `double?` | `minSize` | number / null | omitted on null | Per-quality override of `QualityDefinition.MinSize`. |
| `MaxSize` | `double?` | `maxSize` | number / null | omitted on null | |
| `PreferredSize` | `double?` | `preferredSize` | number / null | omitted on null | Added in 181. |

**Migration history:**

- `037_add_configurable_qualities.cs`
- `054_rename_profiles.cs` / `055_drop_old_profile_columns.cs`
- `071_unknown_quality_in_profile.cs`
- `084_update_quality_minmax_size.cs`
- `181_quality_definition_preferred_size.cs` — adds the `PreferredSize` field.

---

### 11. `MediaInfoModel` (`src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoModel.cs`)

**Stored on:** `EpisodeFiles.MediaInfo`. Largest doc by field count.

**Wire shape:**

```json
{
  "rawStreamData": "...",
  "schemaRevision": 11,
  "containerFormat": "Matroska",
  "videoFormat": "HEVC",
  "videoCodecID": "V_MPEGH/ISO/HEVC",
  "videoProfile": "Main 10@L5.1@High",
  "videoBitrate": 12345678,
  "videoBitDepth": 10,
  "videoColourPrimaries": "BT.2020",
  "videoTransferCharacteristics": "PQ",
  "videoHdrFormat": "hdr10",
  "height": 2160,
  "width": 3840,
  "runTime": "00:42:15",
  "videoFps": 23.976,
  "audioStreams": [ /* MediaInfoAudioStreamModel objects */ ],
  "subtitleStreams": [ /* MediaInfoSubtitleStreamModel objects */ ],
  "scanType": "Progressive"
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `RawStreamData` | `string` | `rawStreamData` | string | `null` | The entire JSON of MediaInfo's stream output, kept for re-derivation (added in 163). |
| `SchemaRevision` | `int` | `schemaRevision` | int | `0` | Sonarr-internal version of the MediaInfo derivation logic; bumped on every MediaInfo migration. |
| `ContainerFormat` | `string` | `containerFormat` | string | `null` | |
| `VideoFormat` | `string` | `videoFormat` | string | `null` | |
| `VideoCodecID` | `string` | `videoCodecID` | string | `null` | **Casing trap**: the C# property is `VideoCodecID`. STJ camelCase converts it to `videoCodecID` (last char capital preserved by camelCase rules: `xxxID → xxxID` is **NOT** what STJ does — it produces `videoCodecID` because camelCase only lowercases the first character). The Go port must use the literal tag `videoCodecID`. EDOC-Q16. |
| `VideoProfile` | `string` | `videoProfile` | string | `null` | |
| `VideoBitrate` | `long` | `videoBitrate` | int (long) | `0` | |
| `VideoBitDepth` | `int` | `videoBitDepth` | int | `0` | |
| `VideoColourPrimaries` | `string` | `videoColourPrimaries` | string | `null` | British spelling. |
| `VideoTransferCharacteristics` | `string` | `videoTransferCharacteristics` | string | `null` | |
| `VideoHdrFormat` | `HdrFormat` | `videoHdrFormat` | camelCase enum string | `none` | Values: `none`, `unknownHdr`, `pq10`, `hdr10`, `hdr10Plus`, `hlg10`, `dolbyVision`, `dolbyVisionHdr10`, `dolbyVisionSdr`, `dolbyVisionHlg`, `dolbyVisionHdr10Plus`. |
| `Height` | `int` | `height` | int | `0` | |
| `Width` | `int` | `width` | int | `0` | |
| `RunTime` | `TimeSpan` | `runTime` | string `[d.]hh:mm:ss[.fffffff]` (via `STJTimeSpanConverter`) | `00:00:00` | C#'s `TimeSpan.ToString()` default. EDOC-Q10. |
| `VideoFps` | `decimal` | `videoFps` | number | `0` | Decimal precision. |
| `PrimaryAudioStream` | (computed) | — | — | — | No setter — getter-only over `AudioStreams.FirstOrDefault()`. STJ skips properties without setters. |
| `AudioStreams` | `List<MediaInfoAudioStreamModel>` | `audioStreams` | array | `null` (omitted under `WhenWritingNull`) | Added in 225. Pre-225 rows had a flat audio shape — see migration. |
| `SubtitleStreams` | `List<MediaInfoSubtitleStreamModel>` | `subtitleStreams` | array | `null` (omitted) | Added in 225. |
| `ScanType` | `string` | `scanType` | string | `null` | |
| `Title` | `string` | — | — | — | `[JsonIgnore]` |

**Migration history (this column is the most-rewritten JSON in Sonarr):**

- `108_fix_extra_file_extension.cs` (sister)
- `148_mediainfo_channels.cs`
- `163_mediainfo_to_ffmpeg.cs` — switches the underlying probe from MediaInfo native to ffprobe; rewrites every column.
- `225_mediainfo_multiple_streams.cs` — adds `AudioStreams[]` and `SubtitleStreams[]` derived from existing flat fields. Pre-225 rows had:
  - `AudioFormat`, `AudioCodecID`, `AudioProfile`, `AudioBitrate`, `AudioChannels`, `AudioChannelPositions`, `AudioLanguages`, `AudioStreamCount`
  - `Subtitles` (string)
  …all of which are still readable on rows that haven't been re-probed.

**Open questions:** EDOC-Q16, EDOC-Q17 (legacy pre-225 fields).

---

### 12. `MediaInfoAudioStreamModel` (`src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoAudioStreamModel.cs`)

**Stored on:** inside `MediaInfoModel.AudioStreams` (added in 225).

**Wire shape (single element):**

```json
{
  "language": "eng",
  "format": "AC-3",
  "codecId": "A_AC3",
  "profile": null,
  "bitrate": 384000,
  "channels": 6,
  "channelPositions": "3/2/0.1",
  "title": null
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Language` | `string` | `language` | string | `null` | ISO 639-2/B 3-letter code typically. |
| `Format` | `string` | `format` | string | `null` | |
| `CodecId` | `string` | `codecId` | string | `null` | Note `CodecId` (not `CodecID`) — STJ produces `codecId`. |
| `Profile` | `string` | `profile` | string | `null` | |
| `Bitrate` | `long` | `bitrate` | int (long) | `0` | |
| `Channels` | `int` | `channels` | int | `0` | |
| `ChannelPositions` | `string` | `channelPositions` | string | `null` | |
| `Title` | `string` | `title` | string | `null` | |

**Migration history:** `225_mediainfo_multiple_streams.cs` (introduction).

---

### 13. `MediaInfoSubtitleStreamModel` (`src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoSubtitleStreamModel.cs`)

**Stored on:** inside `MediaInfoModel.SubtitleStreams` (added in 225).

**Wire shape:**

```json
{
  "language": "eng",
  "format": "PGS",
  "title": null,
  "forced": false,
  "hearingImpaired": false
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Language` | `string` | `language` | string | `null` | |
| `Format` | `string` | `format` | string | `null` | |
| `Title` | `string` | `title` | string | `null` | |
| `Forced` | `bool?` | `forced` | bool / null | `null` (omitted) | Nullable — distinguishes "no flag set" from "explicitly false". |
| `HearingImpaired` | `bool?` | `hearingImpaired` | bool / null | `null` (omitted) | Nullable, same reasoning. |

**Migration history:** `225_mediainfo_multiple_streams.cs`.

---

### 14. `ParsedEpisodeInfo` (`src/NzbDrone.Core/Parser/Model/ParsedEpisodeInfo.cs`) — registered ad-hoc, not `IEmbeddedDocument`

**Stored on:** `PendingReleases.ParsedEpisodeInfo`. Registered via `EmbeddedDocumentConverter<ParsedEpisodeInfo>(new QualityIntConverter(), new LanguageIntConverter())` (`TableMapping.cs:200`).

**Wire shape (abridged — 25 fields):**

```json
{
  "releaseTitle": "...",
  "seriesTitle": "...",
  "seriesTitleInfo": { "title": "...", "titleWithoutYear": "...", "year": 2020, "allTitles": [...] },
  "quality": { "quality": 7, "revision": { "version": 1, "real": 0, "isRepack": false } },
  "seasonNumber": 1,
  "episodeNumbers": [1, 2],
  "absoluteEpisodeNumbers": [],
  "specialAbsoluteEpisodeNumbers": [],
  "airDate": null,
  "languages": [1],
  "fullSeason": false,
  "isPartialSeason": false,
  "isMultiSeason": false,
  "isSeasonExtra": false,
  "isSplitEpisode": false,
  "isMiniSeries": false,
  "special": false,
  "releaseGroup": "GROUP",
  "releaseHash": null,
  "seasonPart": 0,
  "releaseTokens": null,
  "dailyPart": null,
  "isDaily": false,
  "isAbsoluteNumbering": false,
  "isPossibleSpecialEpisode": false,
  "isPossibleSceneSeasonSpecial": false,
  "releaseType": "singleEpisode"
}
```

**Field table (key fields only — full list in source):**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `ReleaseTitle` | `string` | `releaseTitle` | string | `null` | |
| `SeriesTitle` | `string` | `seriesTitle` | string | `null` | |
| `SeriesTitleInfo` | `SeriesTitleInfo` | `seriesTitleInfo` | nested object `{title, titleWithoutYear, year, allTitles}` | `null` (omitted) | Sub-shape below. |
| `Quality` | `QualityModel` | `quality` | `QualityModel` shape (quality is reduced to int) | `{quality: 0, revision: {...}}` | |
| `SeasonNumber` | `int` | `seasonNumber` | int | `0` | |
| `EpisodeNumbers` | `int[]` | `episodeNumbers` | array of int | `[]` | |
| `AbsoluteEpisodeNumbers` | `int[]` | `absoluteEpisodeNumbers` | array of int | `[]` | |
| `SpecialAbsoluteEpisodeNumbers` | `decimal[]` | `specialAbsoluteEpisodeNumbers` | array of number | `[]` | |
| `AirDate` | `string` | `airDate` | string | `null` | Format: `yyyy-MM-dd`. |
| `Languages` | `List<Language>` | `languages` | array of int (via per-container `LanguageIntConverter`) | `[]` | |
| `FullSeason` | `bool` | `fullSeason` | bool | `false` | |
| `IsPartialSeason` | `bool` | `isPartialSeason` | bool | `false` | |
| `IsMultiSeason` | `bool` | `isMultiSeason` | bool | `false` | |
| `IsSeasonExtra` | `bool` | `isSeasonExtra` | bool | `false` | |
| `IsSplitEpisode` | `bool` | `isSplitEpisode` | bool | `false` | |
| `IsMiniSeries` | `bool` | `isMiniSeries` | bool | `false` | |
| `Special` | `bool` | `special` | bool | `false` | |
| `ReleaseGroup` | `string` | `releaseGroup` | string | `null` | |
| `ReleaseHash` | `string` | `releaseHash` | string | `null` | |
| `SeasonPart` | `int` | `seasonPart` | int | `0` | |
| `ReleaseTokens` | `string` | `releaseTokens` | string | `null` | |
| `DailyPart` | `int?` | `dailyPart` | int / null | `null` (omitted) | |
| `IsDaily` | `bool` (derived getter, **has empty private setter**) | `isDaily` | bool | `false` | STJ serialises this even though it's derived, because the no-op private setter makes the property writable. On read the value is consumed by the setter and discarded. EDOC-Q5. |
| `IsAbsoluteNumbering` | `bool` (derived) | `isAbsoluteNumbering` | bool | `false` | Same trap. |
| `IsPossibleSpecialEpisode` | `bool` (derived) | `isPossibleSpecialEpisode` | bool | `false` | Same trap. |
| `IsPossibleSceneSeasonSpecial` | `bool` (derived) | `isPossibleSceneSeasonSpecial` | bool | `false` | Same trap. |
| `ReleaseType` | `ReleaseType` (enum, getter only — no setter) | `releaseType` | camelCase enum string | (skipped on read; written but discarded) | Only the getter, so STJ writes it and drops it on read. The Go port can omit. |

**`SeriesTitleInfo` sub-shape** (`src/NzbDrone.Core/Parser/Model/SeriesTitleInfo.cs`):

| C# property | JSON key | Form | Default | Notes |
|-------------|----------|------|---------|-------|
| `Title` | `title` | string | `null` | |
| `TitleWithoutYear` | `titleWithoutYear` | string | `null` | |
| `Year` | `year` | int | `0` | |
| `AllTitles` | `allTitles` | array of string / null | `null` (omitted) | |

**Migration history:** none directly rewrites this JSON; `PendingReleases` is short-lived (reset per RSS run) so historical shape drift mostly auto-heals.

**Open questions:** EDOC-Q5.

---

### 15. `ReleaseInfo` (`src/NzbDrone.Core/Parser/Model/ReleaseInfo.cs`) — ad-hoc

**Stored on:** `PendingReleases.Release`, `DownloadHistory.Release`. Registered via `EmbeddedDocumentConverter<ReleaseInfo>` (`TableMapping.cs:201`).

**Wire shape:**

```json
{
  "guid": "...",
  "title": "...",
  "size": 1234567890,
  "downloadUrl": "...",
  "infoUrl": "...",
  "commentUrl": null,
  "indexerId": 5,
  "indexer": "MyIndexer",
  "indexerPriority": 25,
  "seasonSearchMaximumSingleEpisodeAge": 0,
  "downloadProtocol": "torrent",
  "tvdbId": 0,
  "tvRageId": 0,
  "imdbId": null,
  "publishDate": "2024-03-01T12:00:00Z",
  "origin": "...",
  "source": "...",
  "container": "...",
  "codec": "...",
  "resolution": "...",
  "languages": [1],
  "age": 5,
  "ageHours": 120.5,
  "ageMinutes": 7230.0
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `Guid` | `string` | `guid` | string | `null` | |
| `Title` | `string` | `title` | string | `null` | |
| `Size` | `long` | `size` | int (long) | `0` | |
| `DownloadUrl` | `string` | `downloadUrl` | string | `null` | |
| `InfoUrl` | `string` | `infoUrl` | string | `null` | |
| `CommentUrl` | `string` | `commentUrl` | string | `null` | |
| `IndexerId` | `int` | `indexerId` | int | `0` | |
| `Indexer` | `string` | `indexer` | string | `null` | |
| `IndexerPriority` | `int` | `indexerPriority` | int | `0` | |
| `SeasonSearchMaximumSingleEpisodeAge` | `int` | `seasonSearchMaximumSingleEpisodeAge` | int | `0` | |
| `DownloadProtocol` | `DownloadProtocol` | `downloadProtocol` | camelCase enum string | `unknown` | |
| `TvdbId` | `int` | `tvdbId` | int | `0` | |
| `TvRageId` | `int` | `tvRageId` | int | `0` | |
| `ImdbId` | `string` | `imdbId` | string | `null` | |
| `PublishDate` | `DateTime` | `publishDate` | string `yyyy-MM-ddTHH:mm:ssZ` (via `STJUtcConverter`) | epoch | Always UTC. |
| `Origin`, `Source`, `Container`, `Codec`, `Resolution` | `string` | (camelCase) | string | `null` | |
| `Languages` | `List<Language>` | `languages` | array of int | `[]` | Per-container `LanguageIntConverter`? **No** — `ReleaseInfo` is registered without per-container converters (`TableMapping.cs:201`), so `Languages` falls through to `EmbeddedDocumentConverter<List<Language>>` which has `LanguageIntConverter` registered globally for that list type. Net effect: still int-form. EDOC-Q4. |
| `IndexerFlags` | `IndexerFlags` | — | — | — | `[JsonIgnore]` |
| `PendingReleaseReason` | `PendingReleaseReason?` | — | — | — | `[JsonIgnore]` |
| `Age` | `int` (derived getter, empty private setter) | `age` | int | `0` | Computed from `PublishDate`, but STJ writes it. EDOC-Q5. |
| `AgeHours` | `double` (derived) | `ageHours` | number | `0` | Same. |
| `AgeMinutes` | `double` (derived) | `ageMinutes` | number | `0` | Same. |

**Open questions:** EDOC-Q4, EDOC-Q5, EDOC-Q18 (subclasses `TorrentInfo` etc. add fields not seen here — for `PendingReleases.Release` the runtime polymorphism is **lost** because there's no `{type, body}` envelope; STJ serialises whatever class instance was passed in but reads back as the bare `ReleaseInfo`. This is a pre-existing data-loss bug for torrent-only fields).

---

### 16. `PendingReleaseAdditionalInfo` (`src/NzbDrone.Core/Download/Pending/PendingRelease.cs:21-26`)

**Stored on:** `PendingReleases.AdditionalInfo`. Registered via `EmbeddedDocumentConverter<PendingReleaseAdditionalInfo>` (`TableMapping.cs:202`).

**Wire shape:**

```json
{
  "seriesMatchType": "title",
  "releaseSource": "rss"
}
```

**Field table:**

| C# property | C# type | JSON key | JSON value form | Default on missing | Notes |
|-------------|---------|----------|-----------------|--------------------|-------|
| `SeriesMatchType` | `SeriesMatchType` | `seriesMatchType` | camelCase enum string | `unknown` | From `src/NzbDrone.Core/Parser/Model/FindSeriesResult.cs:17`. |
| `ReleaseSource` | `ReleaseSourceType` | `releaseSource` | camelCase enum string | `unknown` | From `src/NzbDrone.Core/Parser/Model/RemoteEpisode.cs:47`. |

---

### 17–22. Generic collection types

These are simple wire shapes registered explicitly:

- `Dictionary<string, string>` / `IDictionary<string, string>` → `{"key1": "v1", "key2": "v2"}` with **camelCase keys** (the `DictionaryKeyPolicy` is camelCase, so any incoming PascalCase key is silently lower-cased on read-back? No: `DictionaryKeyPolicy` only affects **writes**. Reads preserve cell content. Net effect: cells written under v5 will be camelCase; cells written under earlier versions remain whatever case they were. EDOC-Q19.).
- `List<int>` → `[1,2,3]`. Used for tag lists, episode-id lists, indexer-id lists.
- `List<KeyValuePair<string,int>>` → `[{"key":"...","value":0}, ...]`.
- `KeyValuePair<string,int>` → `{"key":"...","value":0}`.
- `HashSet<int>` → `[1,2,3]` (order undefined).

---

### 23. `List<string>` via `StringListConverter`

**Stored on:** `ReleaseProfiles.Required`, `ReleaseProfiles.Ignored` (post-162), `Series.Genres`, etc.

**Standard form:** `["term1", "term2"]`.

**Legacy fallback:** `term1,term2` (comma-split if cell does not start with `[`). This codepath exists to read pre-162 data and any data written by older Sonarr versions a user downgraded to. The Go port must reproduce this fallback verbatim or risk silently dropping release-profile terms on first read after upgrade. EDOC-Q6.

---

## Polymorphic envelopes

Two collection columns use a `{type, body}` envelope pattern handled by hand-written `JsonConverter<T>` subclasses (NOT `EmbeddedDocumentConverter` directly):

### `List<ICustomFormatSpecification>` → `CustomFormats.Specifications`

**Converter:** `src/NzbDrone.Core/Datastore/Converters/CustomFormatSpecificationConverter.cs`

**Wire shape:**

```json
[
  {
    "type": "ReleaseTitleSpecification",
    "body": { "name": "x265", "negate": false, "required": false, "value": "x265" }
  },
  {
    "type": "SizeSpecification",
    "body": { "name": "<5GB", "negate": false, "required": false, "min": 0, "max": 5 }
  }
]
```

**Type-resolution mechanism:** `Type.GetType($"NzbDrone.Core.CustomFormats.{typename}, Sonarr.Core")` — i.e. the `type` field is the **C# class name** (no namespace) and the converter assumes the namespace is fixed at `NzbDrone.Core.CustomFormats`. Cross-cut with TP-Q1/TP-Q2: any rename of the C# class (which Go ports inevitably need) breaks existing user DBs. The Go port must keep an alias map.

**Known type-string values** (from `grep CustomFormatSpecificationBase` — 8 concrete types):

| `type` string | Source | Body fields (in addition to `name`, `negate`, `required`) |
|---------------|--------|-----------------------------------------------------------|
| `ReleaseTitleSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/ReleaseTitleSpecification.cs` | `value` (string regex) |
| `ReleaseGroupSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/ReleaseGroupSpecification.cs` | `value` (string regex) |
| `LanguageSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/LanguageSpecification.cs` | `value` (int — Language id), `exceptLanguage` (bool) |
| `IndexerFlagSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/IndexerFlagSpecification.cs` | `value` (int — IndexerFlags) |
| `SourceSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/SourceSpecification.cs` | `value` (int — QualitySource) |
| `ResolutionSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/ResolutionSpecification.cs` | `value` (int — Resolution enum) |
| `SizeSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/SizeSpecification.cs` | `min` (double GB), `max` (double GB) |
| `ReleaseTypeSpecification` | `src/NzbDrone.Core/CustomFormats/Specifications/ReleaseTypeSpecification.cs` | `value` (int — ReleaseType) |

Common base fields (from `CustomFormatSpecificationBase`):

| C# property | JSON key | Form | Default | Notes |
|-------------|----------|------|---------|-------|
| `Name` | `name` | string | `null` | |
| `Negate` | `negate` | bool | `false` | |
| `Required` | `required` | bool | `false` | |
| `Order` | (not serialised — getter only) | — | — | |
| `ImplementationName` | (not serialised — getter only) | — | — | |
| `InfoLink` | (not serialised — getter only) | — | — | |

**Migration history:**

- `171_add_custom_formats.cs` — initial table.
- `205_rename_season_pack_spec.cs` — **type-string rename** from `SeasonPackSpecification` to `ReleaseTypeSpecification`. Go port must keep the migration to rewrite legacy rows OR keep an alias map at deserialisation time.

**Open questions:** EDOC-Q20 (alias-map vs migration approach for `SeasonPackSpecification`), EDOC-Q21 (do we re-namespace under the Go port? If yes, what is the canonical type-string?).

---

### `List<IAutoTaggingSpecification>` → `AutoTagging.Specifications`

**Converter:** `src/NzbDrone.Core/Datastore/Converters/AutoTagSpecificationConverter.cs` (note: file name is `AutoTagSpecificationConverter.cs` but the class inside is `AutoTaggingSpecificationConverter` — see EDOC-Q22).

**Wire shape:** identical envelope, namespace is `NzbDrone.Core.AutoTagging.Specifications`.

**Known type-string values** (11 concrete types):

| `type` string | Source | Body fields |
|---------------|--------|-------------|
| `GenreSpecification` | `src/NzbDrone.Core/AutoTagging/Specifications/GenreSpecification.cs` | `value` (array of string) |
| `MonitoredSpecification` | `MonitoredSpecification.cs` | (no extra) |
| `NetworkSpecification` | `NetworkSpecification.cs` | `value` (array of string) |
| `OriginalCountrySpecification` | `OriginalCountrySpecification.cs` | `value` (array of string, 3-letter codes) |
| `OriginalLanguageSpecification` | `OriginalLanguageSpecification.cs` | `value` (int — Language id) |
| `QualityProfileSpecification` | `QualityProfileSpecification.cs` | `value` (int — QualityProfile id) |
| `RootFolderSpecification` | `RootFolderSpecification.cs` | `value` (string path) |
| `SeriesTypeSpecification` | `SeriesTypeSpecification.cs` | `value` (int — SeriesTypes) |
| `StatusSpecification` | `StatusSpecification.cs` | `status` (int — SeriesStatusType) **note: not `value`** |
| `TagSpecification` | `TagSpecification.cs` | `value` (int — Tag id) |
| `YearSpecification` | `YearSpecification.cs` | `min` (int), `max` (int) |

Common base fields (from `AutoTaggingSpecificationBase`): same `name`, `negate`, `required` as above.

**Migration history:** introduced (table created) in a migration that's not specifically dedicated; no type-string renames yet.

**Open questions:** EDOC-Q21, EDOC-Q22, EDOC-Q23 (the `status` vs `value` field-name asymmetry on `StatusSpecification`).

---

### `Command` polymorphism (`Commands.Body`)

Not strictly a `{type, body}` envelope — instead the discriminator `name` is a peer of all the other fields:

**Wire shape:**

```json
{
  "name": "RefreshSeries",
  "seriesId": 7,
  "isNewSeries": false,
  "sendUpdatesToClient": true,
  "updateScheduledTask": true,
  "completionMessage": "...",
  "requiresDiskAccess": false,
  ...
}
```

**Type-resolution:** `CommandConverter.Parse` (`src/NzbDrone.Core/Datastore/Converters/CommandConverter.cs:21-37`) reads the `name` property out of the JSON, looks up `<name>Command` in the assembly via `FindTypeByName`, and deserialises the whole object as that type. If lookup fails, falls back to `UnknownCommand` with `ContractName = name`. Same C#-class-name fragility as the spec envelopes.

---

## Provider Settings (IProviderConfig)

Every provider table (`Indexers`, `DownloadClients`, `ImportLists`, `Notifications`, `Metadata`) has a `Settings` JSON column whose shape is the per-implementation `IProviderConfig` type. Polymorphism is keyed by the **`ConfigContract` *column*** (text, value = C# class name), NOT an inline `type` field. `ProviderRepository.Query` (`src/NzbDrone.Core/ThingiProvider/ProviderRepository.cs:38-71`) reads `ConfigContract` first, looks up the C# type via `FindTypeByName(ConfigContract)`, and deserialises `Settings` against it. Failure mode: returns `NullConfig.Instance`.

The serialiser used here is the **same STJ profile** as `EmbeddedDocumentConverter` (lines 25-35 in `ProviderRepository.cs`): camelCase, `WhenWritingNull` ignore, etc. So the wire-format rules are identical to embedded docs — only the polymorphism-discovery mechanism differs.

**Settings classes** (catalogue scope: count, not full per-class field tables — those belong with the in-progress `providers/*.md` matrix):

- **Download clients (`: IProviderConfig` direct + via `DownloadClientSettingsBase`):** ~16 — QBittorrent, Flood, RQbit, Aria2, Deluge, Sabnzbd, Transmission, Tribler, UsenetBlackhole, rTorrent, FreeboxDownload, Nzbget, uTorrent, Pneumatic, DownloadStation, NzbVortex, TorrentBlackhole, Hadouken.
- **Indexers** (via `NewznabSettings` / `TorznabSettings` / hand-rolled): Newznab, Torznab (subclass), Nyaa, FileList, BroadcasTheNet, IPTorrents, MorethanTV, Rarbg, Torrentleech, TorrentRssIndexer, Omgwtfnzbs, … (each is a class extending NewznabSettings or implementing `IProviderConfig` directly). See `subsystems/indexers.md` and `subsystems/thingiprovider.md`.
- **Notifications** (via `NotificationSettingsBase<T>`): ~26 — Twitter, Webhook, Pushcut, SynologyIndexer, Discord, Simplepush, Join, Trakt, SendGrid, Ntfy, MediaBrowser, Apprise, CustomScript, Pushover, Prowl, PlexServer, Telegram, Gotify, PushBullet, Mailgun, Signal, Slack, Notifiarr, Xbmc, Email.
- **Import lists** (via various `*SettingsBase`): Trakt (multiple sub-types), AniList, Plex, MyAnimeList, Custom (RSS), Simkl, Sonarr, Rss.
- **Metadata writers:** Roksbox, Xbmc, Wdtv, Plex.

**Cross-references:**

- `subsystems/thingiprovider.md` (provider lifecycle).
- `subsystems/indexers.md`, `subsystems/download.md`, `subsystems/notifications.md`, `subsystems/importlists-metadata.md`.
- `providers/*.md` (per-provider field-table matrix — being written in parallel).

**Asymmetry: ClientSchema on the way out.** Provider `Settings` are written into the DB as plain STJ JSON. On the way OUT to the UI, controllers use the **frontend ClientSchema** (covered in `subsystems/thingiprovider.md`) which projects `[FieldDefinition]`-annotated fields into a label/help/order schema with `Privacy` masking. So the same field can have one shape in the DB JSON (raw value) and a different shape in the API payload (wrapped in a `Field` object with `value`, `label`, `helpText`, `type`, etc.). The Go port has to keep both shapes in mind for any settings class. EDOC-Q24.

**Migration history (provider settings JSON rewrites):**

- `046_fix_nzb_su_url.cs`, `049_fix_dognzb_url.cs` — Newznab `BaseUrl` rewrites.
- `082_add_fanzub_settings.cs`
- `085_expand_transmission_urlbase.cs`
- `086_pushbullet_device_ids.cs`, `088_pushbullet_devices_channels_list.cs`
- `090_update_kickass_url.cs`, `121_update_animetosho_url.cs`
- `108_fix_extra_file_extension.cs`
- `116_disable_nyaa.cs` — toggles flag
- `152_update_btn_url_to_https.cs`
- `157_email_multiple_addresses.cs`, `201_email_encryption.cs`
- `158_cdh_per_downloadclient.cs`
- `218_telegram_link_preview.cs`
- `219_nzb_su_url_to_nzb_life.cs`

Provider type-string renames are tracked separately in `subsystems/thingiprovider.md` (TP-Q1/TP-Q2).

---

## Open questions

| ID | Question | Status | Blocked phase |
|----|----------|--------|---------------|
| EDOC-Q1 | `subsystems/datastore.md:117-119` claims the embedded-doc bridge uses **Newtonsoft**. Reading `EmbeddedDocumentConverter.cs:13-29` shows it actually uses **System.Text.Json** with a fixed `JsonSerializerOptions` profile (camelCase, `WhenWritingNull`, `JsonStringEnumConverter`, `STJUtcConverter`, `STJTimeSpanConverter`). Confirm the cross-cut update — datastore.md should be amended. The Go port writes against **STJ semantics**, not Newtonsoft. | Open | Phase 1 (datastore port) |
| EDOC-Q2 | Should the Go port preserve `WriteIndented = true`? Cosmetic, but every existing user DB cell has 2-space-indented payloads with newlines. Affects nothing except byte-equality test fixtures. | Open | Phase 1 |
| EDOC-Q3 | `Actor` is `IEmbeddedDocument` and registered via `RegisterEmbeddedConverter`, but no current `Series` or other table has an `Actors` JSON column. Is this a dead type, or used by REST projection only? Confirm with `grep Actor` across the API layer; if dead, document and skip in Go-side type registration. | Open | Phase 1 |
| EDOC-Q4 | When `List<Language>` is the column type, the global `EmbeddedDocumentConverter<List<Language>>(new LanguageIntConverter())` registration applies (int form). When `Language` (single) is the column type, it's registered both as `EmbeddedDocumentConverter<Language>` (full object) AND via `DapperLanguageIntConverter` (Dapper-level int). Disambiguate: is the single-Language JSON path ever hit? Or is `OriginalLanguage` always scalar-int? | Open | Phase 1 |
| EDOC-Q5 | `ParsedEpisodeInfo` and `ReleaseInfo` have several derived getters with empty private setters (`IsDaily`, `Age`, `AgeHours`, …). STJ writes them on serialise and discards on deserialise. The Go port should NOT emit these — but golden test fixtures captured from the C# side WILL have them. Decide: emit-and-discard for fixture parity, or omit and adjust fixtures? | Open | Phase 1 (test golden files) |
| EDOC-Q6 | `StringListConverter.Parse` falls back to comma-split when the cell does not start with `[`. Required for Sonarr-version-cycling users. Reproduce verbatim. | Open | Phase 1 |
| EDOC-Q7 | STJ writes non-null defaults like `0`, `false`, `""`. Go's `encoding/json` with `omitempty` would silently drop them. The Go port must NOT use `omitempty` on value-typed fields where the C# default is emitted. Audit per-field. | Open | Phase 1 |
| EDOC-Q8 | Should `WriteIndented = true` be reproduced? See EDOC-Q2 (same question, restated for clarity). | Open | Phase 1 |
| EDOC-Q9 | `JsonStringEnumConverter(JsonNamingPolicy.CamelCase, allowIntegerValues: true)` — read-side accepts both string and integer for every enum. Pre-camelCase-enum DBs (e.g. early 080-series migrations) may have integer enum cells. Confirm Go port reads both. | Open | Phase 1 |
| EDOC-Q10 | `STJTimeSpanConverter` writes `TimeSpan.ToString()` default — `[d.]hh:mm:ss[.fffffff]`. Go's `time.Duration.String()` uses different units. The port must hand-roll a `TimeSpan` marshaller. | Open | Phase 1 |
| EDOC-Q11 | `Quality.Source` is `QualitySource` enum but always written by the int-reduction path inside `QualityModel.quality`. Confirm there is **no** code path that writes the full `Quality` object (i.e. with `id`, `name`, `source`, `resolution`) into a DB cell — the only place a free `Quality` is registered is the embedded-doc auto-registration, which hits when `Quality` is a column type. None of the schema columns have type `Quality` directly, so this path may be dead. Verify. | Open | Phase 1 |
| EDOC-Q12 | `Revision` is **NOT** marked `IEmbeddedDocument`. It is composed inside `QualityModel`. STJ serialises it via reflection on the parent's options. Is there ever a `Revision`-typed column? grep says no. | Open | Phase 1 |
| EDOC-Q13 | `MonitorTypes.LatestSeason` is `[Obsolete]` but still in the enum. Legacy DBs may have `latestSeason` strings or the integer `7`. Confirm the Go port keeps the value reachable on read (with re-mapping to `lastSeason` semantics if appropriate). | Open | Phase 1 |
| EDOC-Q14 | `Ratings.Value` is `decimal`; STJ writes arbitrary precision number. Using Go `float64` may lose precision for unusual values (Sonarr Ratings are 0–10 floats so it's de-facto safe). Decide: float64, big.Float, or string round-trip. | Open | Phase 1 |
| EDOC-Q15 | `Language.Original` has id `-2` (unusual sentinel). Confirm `LanguageIntConverter.Read` handles negative ids without crashing the Go port. | Open | Phase 1 |
| EDOC-Q16 | `MediaInfoModel.VideoCodecID` — STJ camelCase produces `videoCodecID`, NOT `videoCodecId`. The Go port must use the literal tag value `videoCodecID` (not `videoCodecId`), or read-back fails for legacy rows. | Open | Phase 1 |
| EDOC-Q17 | Pre-225 `MediaInfoModel` rows may still have flat `AudioFormat`, `AudioCodecID`, `AudioChannels`, etc. (no `audioStreams[]` nesting). The Go port reader must accept both shapes; the writer always emits the post-225 shape. Confirm the migration backfilled all rows or whether some are still pre-225 in user DBs. | Open | Phase 1 |
| EDOC-Q18 | `ReleaseInfo` polymorphism is **lost** in the JSON — `TorrentInfo` subclass adds fields (`MagnetUrl`, `Seeders`, `Peers`, `InfoHash`) that get serialised but read back as `ReleaseInfo` (parent class), discarding the subclass fields. Pre-existing C# bug; document for porter awareness. | Open | Phase 1 (prefer fixing in Go) |
| EDOC-Q19 | `DictionaryKeyPolicy = CamelCase` only affects writes, not reads. Old cells with PascalCase keys round-trip as PascalCase; new writes are camelCase. The Go port should mirror this asymmetry to avoid mass-rewriting on first read. | Open | Phase 1 |
| EDOC-Q20 | `ReleaseTypeSpecification` was renamed from `SeasonPackSpecification` in migration 205. Should the Go port keep an alias map at deserialisation time, or commit to running migration 205 verbatim? | Open | Phase 1 |
| EDOC-Q21 | When the Go port re-namespaces specs (`NzbDrone.Core.CustomFormats.X` → `sonarr/customformats.X`), the type-string in stored JSON still says `XSpecification`. Decide: keep the bare class name as the canonical string (Go port maps it to its own type registry), OR migrate every cell to a new namespaced form (data migration). The first option preserves DB compatibility; the second couples the type-string to Go internals. | Open | Phase 1 |
| EDOC-Q22 | File `AutoTagSpecificationConverter.cs` contains class `AutoTaggingSpecificationConverter`. Mismatched filename. Cosmetic only, no DB impact. | Open | none |
| EDOC-Q23 | `StatusSpecification.Status` is named `status` in JSON, but every other autotag spec uses `value` for the discriminator field. This is a wire-format inconsistency that must be preserved on the Go port. | Open | Phase 1 |
| EDOC-Q24 | Provider settings are written to the DB as raw STJ JSON, but read by the UI through the ClientSchema projection (`Field` objects). The Go port has to support both. The DB-write path is the contract here; ClientSchema is documented in `subsystems/thingiprovider.md`. | Open | Phase 1 + REST phase |

---

## Cross-cutting concerns affecting other cards

- **`subsystems/datastore.md`** (line 117-119) needs amendment: the embedded-doc serialiser is **System.Text.Json**, not Newtonsoft. The cross-cut to `sonarr-http.md` ("Dapper-side bridge uses Newtonsoft") is also wrong. EDOC-Q1.
- **`subsystems/thingiprovider.md`** (TP-Q1, TP-Q2): the C#-class-name fragility extends from provider tables (`ConfigContract` column) to the inline `{type, body}` envelopes used by `CustomFormats.Specifications` and `AutoTagging.Specifications`, plus the `name` discriminator on `Commands.Body`. EDOC-Q20, EDOC-Q21.
- **`subsystems/quality-profiles-formats-organizer.md`**: confirms wire shapes for `QualityProfileQualityItem` and `ProfileFormatItem` (rows 9, 10 above).
- **`subsystems/configuration.md`**: `AutoTag.Specifications` and `AutoTag.Tags` JSON shapes are catalogued here (rows 22, 28).
- **`subsystems/mediafiles.md`**: `MediaInfoModel` shape (rows 11-13) plus the 225-migration backfill is the single largest piece of porter risk on the file-metadata pathway.
- **`subsystems/sonarr-http.md`**: REST-side serialisation uses System.Text.Json with camelCase (matches embedded-doc on the wire), but with a different `JsonSerializerOptions` profile (no indentation, possibly different enum settings). Audit for divergence — but this is the REST contract, not the DB contract; no impact on Phase 1 datastore port.
- **`migrations/ledger.md`** (to be written): must cite every JSON-rewriting migration listed in `subsystems/datastore.md:163-183` plus the additions surfaced here (`205_rename_season_pack_spec.cs`, `225_mediainfo_multiple_streams.cs`, etc.).
