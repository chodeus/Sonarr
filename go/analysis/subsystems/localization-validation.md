# Localization, Validation, Languages

**Source:** `src/NzbDrone.Core/Localization/**`, `src/NzbDrone.Core/Validation/**`, `src/NzbDrone.Core/Languages/**` (with cross-references into `src/Sonarr.Api.V3`, `src/Sonarr.Api.V5`, `src/Sonarr.Http`)
**Target Go package(s):** `go/internal/localization/`, `go/internal/validation/`, `go/internal/languages/`
**Status:** drafted

## Purpose
Three small, mostly-orthogonal subsystems are bundled together because they are all "metadata about strings + IDs" and they all sit on the API contract:

1. **Localization** is a per-instance translation dictionary served to the frontend via `GET /api/v{3,5}/localization`. The Core also pulls translated strings into a handful of backend-generated user-facing messages (health-checks and indexer test failures). The bundle is shipped as 44 JSON files copied into the build output under `Localization/Core/*.json`. Lookup falls back from the active culture to its 2-letter base to English, and a missing key returns the requested key verbatim.
2. **Validation** is Sonarr's wrapper around FluentValidation 9.5.4 (`Sonarr.Core.csproj:23`). It defines a small library of reusable `PropertyValidator` subclasses (path-existence, root-folder ancestry, URL/host/IP/port shape, etc.), a fluent `RuleBuilder` extension set, and a `ValidationFailure` subclass that adds `IsWarning`, `DetailedDescription`, and `InfoLink`. Failures bubble up as `FluentValidation.ValidationException` and are caught by `SonarrErrorPipeline` which writes the FluentValidation `Errors` collection as the 400 response body.
3. **Languages** is the `Language` value-type registry — a hand-maintained list of 54 distinct (id, name) entries (53 real + `Original` sentinel + `Unknown`) that doubles as a contract surface for Quality Profiles, Custom Formats, parsed release languages, original-language metadata, and the UI-language config key. Three converters expose filtered subsets to the dynamic-form schema, and a custom `IComparer<List<Language>>` orders multi-language tuples for sort comparisons.

Backend Core code consumes localization (health-check/indexer-validation messages) and validation extensively. Languages are referenced everywhere from `Tv.Series.OriginalLanguage` through `QualityProfile.Languages` to `Parser.IsoLanguages` (which lives in the Parser subsystem and maps Language ↔ ISO 639 codes).

---

# Localization

## Public types
| Type | File | Kind | Notes |
|------|------|------|-------|
| `ILocalizationService` | `Localization/LocalizationService.cs:19` | interface | `GetLocalizationDictionary()`, `GetLocalizedString(phrase)`, `GetLocalizedString(phrase, tokens)`, `GetLanguageIdentifier()`. |
| `LocalizationService` | `Localization/LocalizationService.cs:28` | service | Caches via `ICached<Dictionary<string,string>>`; implements `IHandleAsync<ConfigSavedEvent>` to bust cache. |
| `LocalizationResource` (V3) | `src/Sonarr.Api.V3/Localization/LocalizationResource.cs:31` | API DTO | `Strings : Dictionary<string,string>` with custom `JsonConverter` that emits keys verbatim (no camelCasing). |
| `LocalizationLanguageResource` (V3) | `src/Sonarr.Api.V3/Localization/LocalizationLanguageResource.cs:3` | API DTO | `{ Identifier : string }`. |
| `LocalizationResource` (V5) | `src/Sonarr.Api.V5/Localization/LocalizationResource.cs:29` | API DTO | Same shape as V3, mirrored. |
| `LocalizationController` (V3/V5) | `src/Sonarr.Api.V3/Localization/LocalizationController.cs:9`, `src/Sonarr.Api.V5/Localization/LocalizationController.cs:11` | API controller | `GET /api/v3/localization`, `GET /api/v3/localization/language` (both mirrored on V5). |

## Resource bundle layout
- `EmbeddedResource` is **not** used. Files are bundled via `<Content Include="Localization\Core\**"><CopyToOutputDirectory>PreserveNewest</CopyToOutputDirectory>` (`Sonarr.Core.csproj:38-42`). At runtime they live next to the binary under `<StartUpFolder>/Localization/Core/<lang>.json` (`LocalizationService.cs:126-129`).
- Each file is a flat JSON object — `{ "About": "About", "Absolute": "Absolute", ... }` — with ~2200 keys in `en.json` (en.json: 2271 lines).
- Shipped languages (44 culture files):
  `ar`, `bg`, `bn`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `es_MX`, `et`, `fa`, `fi`, `fr`, `he`, `hi`, `hr`, `hu`, `id`, `is`, `it`, `ja`, `ko`, `lt`, `lv`, `nb_NO`, `nl`, `pl`, `pt`, `pt_BR`, `ro`, `ru`, `sk`, `sr`, `sv`, `ta`, `th`, `tr`, `uk`, `vi`, `zh_CN`, `zh_Hans`, `zh_TW` (`src/NzbDrone.Core/Localization/Core/`).

## Loading + lookup behavior
- Active culture is selected from `IConfigService.UILanguage` (an `int` Language id, default `Language.English` = 1) via `IsoLanguages.Get((Language)_configService.UILanguage)`; if missing, falls back to English (`LocalizationService.cs:89`).
- `GetLanguageIdentifier()` returns `"<two-letter>"` or `"<two-letter>-<COUNTRY>"` (e.g. `"pt-BR"`) (`LocalizationService.cs:87-98`).
- File-name canonicalization replaces `-` with `_` and lower-cases (`pt-BR` → `pt_br`); however `GetResourceFilename` upper-cases the country half (`pt_br` → `pt_BR.json`) (`LocalizationService.cs:114-191`).
- Composition order — last write wins (`LocalizationService.cs:134-158`):
  1. Load `en.json` (the base).
  2. If the culture is hyphenated (e.g. `pt_BR`), load the language base (`pt.json`).
  3. Load the variant file (`pt_BR.json`).
- Lookup is **case-insensitive** by key (`new Dictionary<string,string>(StringComparer.OrdinalIgnoreCase)` at `LocalizationService.cs:141`).
- Missing keys: `GetLocalizedString` returns the requested phrase as-is (`LocalizationService.cs:84`).
- Missing files: `_logger.Error("Missing translation/culture resource: {0}", ...)` then continues (`LocalizationService.cs:162-166`). Service does not throw.
- Caching: a single cached dictionary keyed `"localization"` in `ICached<Dictionary<string,string>>("localization")` (`LocalizationService.cs:131`). Cache key is shared across cultures — see LOC-Q1.

## Token format
- `TokenRegex = @"(?:\{)(?<token>[a-z0-9]+)(?:\})"` — i.e. `{name}`-style placeholders, alphanumeric token names, case-insensitive (`LocalizationService.cs:31-32`).
- `appName` is auto-injected as `"Sonarr"` if not present (`LocalizationService.cs:102`).
- Unknown tokens are emitted unchanged in their literal `{token}` form (`LocalizationService.cs:104-111`).
- No pluralization, no ICU, no positional `{0}` style.

## Active-language change
- When the user saves config, `ConfigService.SaveConfigDictionary` publishes `ConfigSavedEvent` (`Configuration/ConfigService.cs:79`).
- `LocalizationService.HandleAsync(ConfigSavedEvent)` calls `_cache.Clear()` (`LocalizationService.cs:193-196`). Next request rebuilds the dictionary against the new `UILanguage`.
- The `UILanguage` config key itself is validated by `UiConfigController` via `SharedValidator.RuleFor(c => c.UILanguage).Custom(...)` requiring a hit in `Language.All` and `>= 1` (`src/Sonarr.Api.V3/Config/UiConfigController.cs:21-31`).

## Backend usage
Backend Core uses localization in two callsite families (320 call sites total across Core):
- **Health checks** — every `HealthCheckBase` subclass takes `ILocalizationService` and produces messages from keys like `RecycleBinUnableToWriteHealthCheckMessage`, `IndexerSearchNoAutomaticHealthCheckMessage`, `SystemTimeHealthCheckMessage` (`HealthCheck/HealthCheckBase.cs:7-9`, `HealthCheck/Checks/*.cs`).
- **Indexer/provider test failures** — `IndexerBase.Test()` and friends localize per-failure descriptions (e.g. `IndexerValidationUnableToConnect` with `{exceptionMessage}` token) (`Indexers/IndexerBase.cs:120`, `Indexers/HttpIndexerBase.cs:380-478`).

Notification-template strings, log messages, and most validation errors are **not** localized (validators emit hard-coded English in `WithMessage(...)` and `GetDefaultMessageTemplate()`).

## API surface
| Route | Controller | Returns |
|------|-----------|---------|
| `GET /api/v3/localization` | `Sonarr.Api.V3.Localization.LocalizationController.GetLocalization` (`:25`) | `LocalizationResource { Strings: Dict<string,string> }`. |
| `GET /api/v3/localization/language` | same controller, `GetLanguage` (`:33`) | `LocalizationLanguageResource { Identifier: string }`. |
| `GET /api/v5/localization`, `GET /api/v5/localization/language` | `Sonarr.Api.V5.Localization.LocalizationController` | Mirrored shapes; uses `TypedResults.Ok<T>` (`src/Sonarr.Api.V5/Localization/LocalizationController.cs`). |

There is **no** `Accept-Language` negotiation — the active language is purely a Sonarr-instance config key.

---

# Languages

## `Language` enum-like registry
- `src/NzbDrone.Core/Languages/Language.cs:8` — `Language : IEmbeddedDocument, IEquatable<Language>` with `int Id`, `string Name`. Equality and `GetHashCode` are by `Id` only (`Language.cs:28-46`).
- Every value is constructed via a private `(int, string)` ctor and exposed as a `static Language X => new Language(id, name);` getter (i.e. each access allocates a new instance — see LANG-Q1).
- Explicit casts: `(Language)int` calls `FindById` (which throws for unknown ids), `(int)Language` returns `Id`, `(Language)string` does case-insensitive name match (`Language.cs:209-229`).

### Full ID table (read from `Language.cs:73-126`)
| Id | Name |
|----|------|
| -2 | Original |
| 0  | Unknown |
| 1  | English |
| 2  | French |
| 3  | Spanish |
| 4  | German |
| 5  | Italian |
| 6  | Danish |
| 7  | Dutch |
| 8  | Japanese |
| 9  | Icelandic |
| 10 | Chinese |
| 11 | Russian |
| 12 | Polish |
| 13 | Vietnamese |
| 14 | Swedish |
| 15 | Norwegian |
| 16 | Finnish |
| 17 | Turkish |
| 18 | Portuguese |
| 19 | Flemish |
| 20 | Greek |
| 21 | Korean |
| 22 | Hungarian |
| 23 | Hebrew |
| 24 | Lithuanian |
| 25 | Czech |
| 26 | Arabic |
| 27 | Hindi |
| 28 | Bulgarian |
| 29 | Malayalam |
| 30 | Ukrainian |
| 31 | Slovak |
| 32 | Thai |
| 33 | Portuguese (Brazil) |
| 34 | Spanish (Latino) |
| 35 | Romanian |
| 36 | Latvian |
| 37 | Persian |
| 38 | Catalan |
| 39 | Croatian |
| 40 | Serbian |
| 41 | Bosnian |
| 42 | Estonian |
| 43 | Tamil |
| 44 | Indonesian |
| 45 | Macedonian |
| 46 | Slovenian |
| 47 | Azerbaijani |
| 48 | Uzbek |
| 49 | Malay |
| 50 | Urdu |
| 51 | Romansh |
| 52 | Georgian |

This list is **API-contract sensitive** — the IDs are persisted in DB columns (e.g. `QualityProfiles.Languages` JSON, `Series.OriginalLanguage`, `EpisodeFiles.Languages`), in URL queries, and on disk in import-list/notification configs. The Go port must keep all 54 ids stable (see LANG-Q2).

The `-2` "Original" sentinel is used by Quality Profiles to mean "match the series' `OriginalLanguage`" without naming a specific language; the `0` "Unknown" sentinel is used by parsers when nothing matched.

## Field converters (dynamic-form `SelectOption` providers)
All implement `ISelectOptionsConverter` from `Annotations/`, used by indexer/notification/import-list provider settings to render a dropdown:

| Converter | File | Filter | Order |
|-----------|------|--------|-------|
| `LanguageFieldConverter` | `Languages/LanguageFieldConverter.cs:7` | All 54 | `OrderBy(Id > 0).ThenBy(Name)` — i.e. `Id <= 0` (`Original`, `Unknown`) come first, then alphabetical by name. |
| `OriginalLanguageFieldConverter` | `Languages/OriginalLanguageFieldConverter.cs:7` | `Id >= 0` (excludes `Original` (-2), keeps `Unknown` (0)) | same order. |
| `RealLanguageFieldConverter` | `Languages/RealLanguageFieldConverter.cs:7` | `!= Language.Unknown` (excludes only `Unknown` (0); keeps `Original` (-2)) | same order. |

The "ordering quirk" of `OrderBy(Id > 0)` is unusual: with sentinels having `Id == 0` or `Id == -2`, the boolean `Id > 0` evaluates to `false` for sentinels (sorted first) and `true` for real entries — so real-language entries are sorted alphabetically below the sentinels. The Go port must mirror this two-stage sort exactly to keep frontend dropdown ordering identical. (LANG-Q3)

## `LanguagesComparer` (sort/equality semantics)
`src/NzbDrone.Core/Languages/LanguagesComparer.cs:6` — `IComparer<List<Language>>` used to sort language tuples. Its rules (`Compare(x, y)`):
1. Both empty → 0.
2. One empty → empty list comes after non-empty (returns `1` if `x` empty).
3. Both have >1 items → larger count comes after (returns `1` if `x.Count > y.Count`).
4. Multi vs single → multi comes after (returns `1` if `x.Count > 1 && y.Count == 1`).
5. Both single → ordinal `string.CompareTo` of `Name`.
6. Else → 0.

Note: this is **not** anti-symmetric in all branches (rule 6 returns 0 for any non-handled combination, e.g. it never explicitly compares two `Count==1` and a `Count==0` because rule 2 already handled empty cases — that path is fine — but the comparer is still order-fragile for `IComparer` consumers; see LANG-Q4 for whether any consumer relies on a strict total order).

## `IsoLanguages` mapping (Parser, cross-referenced)
`src/NzbDrone.Core/Parser/IsoLanguages.cs:11-66` — static `HashSet<IsoLanguage>` mapping `(twoLetterCode, countryCode, threeLetterCode) → Language`, plus a small `AlternateIsoCodeMappings` dictionary for `cze→Czech, dut→Dutch, mac→Macedonian, rum→Romanian, yue→Chinese, zhtw→Chinese`. The localization service depends on this for `GetLanguageIdentifier()`. Consumed by parser, by `MetadataSource/SkyHook`, and by the Newznab/Torznab RSS parsers. (Owned by the `parser.md` card; included here only to flag the dependency.)

## Cross-references into other subsystems
- **Quality Profiles / Custom Formats** — `Profiles.Qualities.QualityProfile.Languages : List<Language>` is sorted with `LanguagesComparer` for cutoff comparisons; Custom Formats use `LanguageSpecification` over `Language.Id` and over `Language.Original` to mean "match series original". See `quality-profiles-formats-organizer.md`.
- **Tv** — `Series.OriginalLanguage : Language` (`Tv/Series.cs`). Stored as embedded JSON inside the Series row.
- **MediaFiles / Parser** — `EpisodeFile.Languages : List<Language>` and parsed `LocalEpisode.Languages`. Owned by `mediafiles.md` / `parser.md`.

---

# Validation

## FluentValidation framework binding
- Package: `FluentValidation 9.5.4` (`Sonarr.Core.csproj:23`).
- There is **no DI auto-registration** of validators (`AssemblyScanner.RegisterValidatorsFromAssembly`, `AddFluentValidation`, etc. are absent — see VAL-Q1). Instead, validators are instantiated explicitly:
  - Reusable `PropertyValidator` subclasses are constructed in-place inside `RuleFor(...).SetValidator(new XxxValidator(...))` calls. Their dependencies are resolved by accepting them as ctor args and being created from controllers/services that already hold the dependency (e.g. `RootFolderValidator(IRootFolderService)` — instantiated by the consumer; see `Tv/AddSeriesValidator.cs`).
  - Resource validators (`AbstractValidator<TResource>`) are instantiated and configured in controller constructors. The `RestController<TResource>` base provides four pre-allocated empty `ResourceValidator<TResource>` instances (`PostValidator`, `PutValidator`, `SharedValidator`, `IdValidator`) for subclasses to populate via `SharedValidator.RuleFor(...)` (`src/Sonarr.Http/REST/RestController.cs:28-50`).
- `IdValidator` is auto-seeded with `RuleFor(r => r.Id).ValidId()` (i.e. `> 0`) (`RestController.cs:50`).

## Failure → HTTP envelope
- `RestController<T>.Validate` aggregates `SharedValidator + (PostValidator|PutValidator) + IdValidator` errors and `throw new ValidationException(errors)` (`RestController.cs:134-158`).
- `Sonarr.Http.ErrorManagement.SonarrErrorPipeline.HandleException` catches the global ASP.NET Core exception, branches on type. For `FluentValidation.ValidationException`:
  - status code = `400 BadRequest`,
  - body = `STJson.ToJson(validationException.Errors)` — i.e. **the raw `IList<ValidationFailure>`** with FluentValidation's default JSON shape (`PropertyName`, `ErrorMessage`, `AttemptedValue`, `CustomState`, `Severity`, `ErrorCode`, `FormattedMessagePlaceholderValues`, …) (`Sonarr.Http/ErrorManagement/SonarrErrorPipeline.cs:46-54`).
- For other exceptions the response is wrapped in `ErrorModel { Message, Description, Content }` (`ErrorModel.cs:9`).
- Validation **warnings** are **not** sent through this exception path; the `NzbDroneValidationResult` API is consumed only inside provider `Test()` methods (Indexer/Notification/etc.) where success-with-warnings is signalled in the `ProviderResource.Message`/`Status` fields rather than as an HTTP error. (VAL-Q2)

## `NzbDroneValidationFailure` / `NzbDroneValidationResult` / `NzbDroneValidationState`
- `NzbDroneValidationFailure : ValidationFailure` adds:
  - `bool IsWarning`,
  - `string DetailedDescription`,
  - `string InfoLink` (`Validation/NzbDroneValidationFailure.cs:5-29`).
  - When constructed from a base `ValidationFailure`, `IsWarning` is hydrated from `CustomState as NzbDroneValidationState` (`:21-28`).
- `NzbDroneValidationState { bool IsWarning }` — the only property; static `NzbDroneValidationState.Warning` singleton (`Validation/NzbDroneValidationState.cs:5`). Plumbed via `RuleBuilderExtensions.AsWarning()` which calls `WithState(v => NzbDroneValidationState.Warning)` (`Validation/RuleBuilderExtensions.cs:66-69`).
- `NzbDroneValidationResult : ValidationResult` partitions failures into `Errors` / `Warnings` lists and overrides `IsValid` to mean "no errors" (warnings are allowed) (`Validation/NzbDroneValidationResult.cs:8-57`). `HasWarnings` is exposed for provider Test endpoints.
- Extensions in `NzbDroneValidationExtensions`:
  - `Filter(params string[] fields)` — keeps only failures whose `PropertyName` is in `fields` (`:10-15`).
  - `ThrowOnError()` — throws `ValidationException(result.Errors)` if `!IsValid` (`:17-23`).
  - `HasErrors(this List<ValidationFailure>)` — true if any non-warning failure (`:25-28`).

## Reusable `PropertyValidator` library

### `Validation/` (top-level)
| Type | File | Default message | Behavior |
|------|------|------|----------|
| `DownloadClientExistsValidator` | `Validation/DownloadClientExistsValidator.cs:6` | `"Download Client does not exist"` | Returns true when value is null or `0`; otherwise calls `IDownloadClientFactory.Exists((int)value)`. |
| `FolderChmodValidator` | `Validation/FolderChmodValidator.cs:6` | `"Must contain a valid Unix permissions octal"` | Calls `IDiskProvider.IsValidFolderPermissionMask(value.ToString())` — Unix-only, used by `MediaManagementConfig.ChmodFolder`. |
| `FolderValidator` | `Validation/FolderValidator.cs:7` | `"Invalid Path: '{path}'"` | `value.ToString().IsPathValid(PathValidationType.CurrentOs)`. |
| `IpValidation.ValidIpAddress` | `Validation/IpValidation.cs:6` | `"Must contain wildcard (*) or a valid IP Address"` | Extension method calling `string.IsValidIpAddress()` from `NzbDrone.Common.Extensions`. |
| `QualityProfileExistsValidator` | `Validation/QualityProfileExistsValidator.cs:6` | `"Quality Profile does not exist"` | Same null/0 short-circuit pattern as `DownloadClientExistsValidator`; calls `IQualityProfileService.Exists((int)value)`. |
| `UrlValidator` (+ `UrlValidation.IsValidUrl`) | `Validation/UrlValidator.cs:6-30` | `"Invalid Url: '{url}'"` | `string.IsValidUrl()` from `NzbDrone.Common.Extensions`. |

### `Validation/Paths/`
| Type | File | Default message | Behavior |
|------|------|------|----------|
| `FileExistsValidator` | `Paths/FileExistsValidator.cs:6` | `"File '{file}' does not exist"` | `IDiskProvider.FileExists(value)`. Null → false. |
| `FolderWritableValidator` | `Paths/FolderWritableValidator.cs:7` | `"Folder '{path}' is not writable by user '{user}'"` | `IDiskProvider.FolderWritable(value)`; populates `{user}` from `Environment.UserName`. |
| `MappedNetworkDriveValidator` | `Paths/MappedNetworkDriveValidator.cs:9` | `"Mapped Network Drive and Windows Service"` | Windows-service-only check: when running as Windows service AND path matches `[a-z]:\` AND mount is `DriveType.Network`, fails. (Service accounts can't see user-mapped drives.) |
| `PathExistsValidator` | `Paths/PathExistsValidator.cs:6` | `"Path '{path}' does not exist"` | `IDiskProvider.FolderExists(value)`. |
| `PathValidator` (+ `PathValidation.IsValidPath`) | `Paths/PathValidator.cs:8-32` | `"Invalid Path: '{path}'"` | `string.IsPathValid(PathValidationType.CurrentOs)`. |
| `RecycleBinValidator` | `Paths/RecycleBinValidator.cs:7` | `"Path '{path}' is {relationship} configured recycle bin folder"` | Forbids the path being equal to or a child of the configured recycle bin (`relationship` ∈ `"set to"`, `"child of"`). |
| `RootFolderAncestorValidator` | `Paths/RootFolderAncestorValidator.cs:8` | `"Path '{path}' is an ancestor of an existing root folder"` | True iff no existing root folder lives under this path. |
| `RootFolderExistsValidator` | `Paths/RootFolderExistsValidator.cs:8` | `"Root folder '{path}' does not exist"` | True iff `_rootFolderService.All()` contains a path-equal entry whose path is itself `IsPathValid`. |
| `RootFolderValidator` | `Paths/RootFolderValidator.cs:8` | `"Path '{path}' is already configured as a root folder"` | Forbids re-using an existing root folder path. |
| `SeriesAncestorValidator` | `Paths/SeriesAncestorValidator.cs:8` | `"Path '{path}' is an ancestor of an existing series"` | True iff no series path lives under this path. |
| `SeriesExistsValidator` | `Paths/SeriesExistsValidator.cs:8` | `"This series has already been added"` | True iff TVDB id not already present in `_seriesService.AllSeriesTvdbIds()`. |
| `SeriesPathValidator` | `Paths/SeriesPathValidator.cs:9` | `"Path '{path}' is already configured for another series"` | Path-equality scan over all series; uses `dynamic` to reach `instance.Id` and skip self (`:29-30`). |
| `StartupFolderValidator` | `Paths/StartupFolderValidator.cs:7` | `"Path '{path}' cannot be {relationship} the start up folder"` | Forbids the path being equal to or a child of `IAppFolderInfo.StartUpFolder`. |
| `SystemFolderValidator` | `Paths/SystemFolderValidator.cs:7` | `"Path '{path}' is {relationship} system folder {systemFolder}"` | Forbids the path being equal to or a child of any entry in `SystemFolders.GetSystemFolders()` (Common/Disk). |

### `Validation/RuleBuilderExtensions.cs`
| Extension | Behavior |
|-----------|----------|
| `ValidId<T>` | `GreaterThanValidator(0)`. |
| `IsZero<T>` | `EqualValidator(0)`. |
| `HaveHttpProtocol<T>` | Regex `^https?://` (case-insensitive); message `"must start with http:// or https://"`. |
| `ValidHost<T>` | NotEmpty + `HostRegex = ^[-_a-z0-9.]+$` OR `IsValidIpAddress()`; message `"must be valid Host without http://"`. |
| `ValidRootUrl<T>` | NotEmpty + `IsValidUrl()` AND starts with `http`; message `"must be valid URL that starts with http(s)://"`. |
| `ValidUrlBase<T>(example="/sonarr")` | Regex `^(?!\/?https?://[-_a-z0-9.]+)`; rejects bases that begin with a URL host. |
| `ValidPort<T>` | `InclusiveBetweenValidator(1,65535)` AND custom: ports ≤ 1024 are rejected unless they are 80 or 443. (Privileged-port heuristic — applies even when running as root.) |
| `AsWarning<T,TProp>` | `WithState(v => NzbDroneValidationState.Warning)` — the only mechanism to flag a `ValidationFailure` as warning-class. |
| `StartsOrEndsWithSonarr<T>` | NotEmpty + Regex `^Sonarr|Sonarr$`; message `"Must start or end with Sonarr"` — used by `Notifications.Discord` `Username` per V3 contract. |

(Note the duplicate-but-not-identical `Sonarr.Http/Validation/RuleBuilderExtensions.cs:8` set — `ValidId`, `IsZero`, `HaveHttpProtocol`, `NotBlank`, `EmptyCollection`, `IsValidRssSyncInterval`. `Sonarr.Http` has its own `EmptyCollectionValidator<T>` (`Sonarr.Http/Validation/EmptyCollectionValidator.cs:7`) and `RssSyncIntervalValidator` (`:5`) — must be 0 or in `[10,120]`.)

## Per-controller resource validators (`AbstractValidator<TResource>`)
Examples — there are dozens; this card covers the wrappers, not each provider's settings validator:
- `Sonarr.Api.V3.Series.SeriesEditorValidator` (`src/Sonarr.Api.V3/Series/SeriesEditorValidator.cs:8`).
- `Sonarr.Api.V3.Profiles.Quality.QualityProfileResourceValidator` (`src/Sonarr.Api.V3/Profiles/Quality/QualityProfileResourceValidator.cs:6`).
- `Sonarr.Api.V3.Qualities.QualityDefinitionResourceValidator` (`src/Sonarr.Api.V3/Qualities/QualityDefinitionResourceValidator.cs:6`).
- V5 mirrors of all of the above (`src/Sonarr.Api.V5/...`).

`Sonarr.Http.REST.ResourceValidator<TResource>` extends `AbstractValidator<TResource>` and adds `RuleForField<TProp>(fieldListAccessor, fieldName)` for validating into a `List<Field>` collection on `ProviderResource` (uses `FluentValidation.Internal.PropertyRule` directly — version-pinned to FluentValidation 9.x; see VAL-Q3) (`src/Sonarr.Http/REST/ResourceValidator.cs:11-29`).

## Tv-side validator chains
For full context the AddSeries flow runs (`Tv/AddSeriesValidator.cs:7-23`):
```
RuleFor(c => c.Path).Cascade(Stop)
                    .IsValidPath()
                    .SetValidator(new RootFolderValidator(...))
                    .SetValidator(new SeriesPathValidator(...))
                    .SetValidator(new SeriesAncestorValidator(...));
RuleFor(c => c.TitleSlug).SetValidator(new SeriesTitleSlugValidator(...));
```

`SeriesTitleSlugValidator` does a full series-table scan on every add (`Tv/SeriesTitleSlugValidator.cs`), already noted in `tv.md`.

---

## Notable quirks / TODOs / HACKs
- `LocalizationService.cs:131` — cache key is the literal string `"localization"` regardless of the current `UILanguage`. Cache-bust on `ConfigSavedEvent` covers the language-switch case; but anything that reads `GetLocalizationDictionary()` must rely on this cache invalidation event being published after every UI-language change. (LOC-Q1)
- `LocalizationService.cs:128-129` — `prefix + language` builds a cache key string that is then **discarded** (`var key = prefix + language` is unused; the actual cache call uses `"localization"`). Looks like a left-over from an older multi-key implementation.
- `LocalizationService.cs:131` — runs `GetDictionary(...).GetAwaiter().GetResult()` from a sync method (sync-over-async). The async file-IO path was added but the public API stays sync.
- `Language.cs:73-126` — every static getter (`Language.English` etc.) **allocates** a new `Language` instance on each access. This is acceptable in C# (cheap GC, equality is by Id) but the Go port should emit `var English = Language{Id:1, Name:"English"}` package-level singletons.
- `Languages/LanguagesComparer.cs` — branch 6 returns 0 for any non-handled combination; this means `Compare(x, y)` doesn't always satisfy strict total order properties. Consumers that pass it to `List.Sort` should be audited (LANG-Q4).
- `OriginalLanguageFieldConverter.cs:14` — uses `Where(l => l.Id >= 0)` which keeps `Unknown` (id 0) and excludes `Original` (-2). The naming "Original" converter suggests the intent was the opposite. Verify what the dropdown is used for. (LANG-Q5)
- `SonarrErrorPipeline.cs:46-54` — for validation failures the body is the **raw FluentValidation `Errors` array** (no `ErrorModel` wrapping). The Go port must keep this exact JSON shape — including FluentValidation-specific fields like `Severity`, `ErrorCode`, `FormattedMessagePlaceholderValues` — or break the frontend error display (VAL-Q4).
- `SeriesPathValidator.cs:29` — uses `dynamic` to reach `Id` from the parent context. The Go port can't replicate this directly; needs a typed `IValidationContext<Series>` wrapper.
- The `ValidPort` extension treats ports ≤ 1024 (other than 80/443) as invalid even on Linux where Sonarr can be granted CAP_NET_BIND_SERVICE. This is a settings-validator convention, not a real OS check (VAL-Q5).

## Test files (the port must keep passing equivalents)
- `src/NzbDrone.Core.Test/Localisation/` — confirmed referenced by service tests; not enumerated here.
- `src/NzbDrone.Core.Test/Languages/` — `LanguagesComparerFixture`, language equality tests.
- `src/NzbDrone.Core.Test/Validation/` — path/url validator fixtures.
- `src/NzbDrone.Common.Test/EnsureThat/` — argument-validation equivalents (covered by `common.md`).

---

## Open questions

### Localization (LOC-Q*)
- **LOC-Q1** — `LocalizationService` caches the dictionary under a single key `"localization"` (`LocalizationService.cs:131`). If a consumer reads the dictionary, then `UILanguage` changes but `ConfigSavedEvent` is somehow not published (e.g. config written directly to DB), the cache returns stale data. Is `ConfigSavedEvent` guaranteed to fire on every UI-language change? Status: **unresolved — blocks Phase 1** (Go cache invalidation contract).
- **LOC-Q2** — Should the Go port keep `Localization/Core/*.json` as filesystem files copied next to the binary (matching C# `<Content CopyToOutputDirectory>`), or `embed.FS`-bundle them? Filesystem keeps end-user editability but adds packaging complexity; embed simplifies deployment. Status: **unresolved — Phase 1 packaging decision**.
- **LOC-Q3** — Token regex is `[a-z0-9]+` (case-insensitive). Are any actually-used tokens hyphenated or use snake_case? If yes, the regex doesn't match them and they fall through unchanged. Need to grep token usage in JSON files and call sites. Status: **unresolved — blocks parity tests**.
- **LOC-Q4** — There is no `Accept-Language` header negotiation. The frontend reads `GET /localization/language` once, then applies `Strings` directly. Confirm Sonarr Mobile / API clients don't expect per-request locale switching. Status: **unresolved — Phase 1 contract check**.
- **LOC-Q5** — `LocalizationService` does not localize most validator messages, NLog log lines, or notification template defaults. Should the Go port keep this asymmetry (only health-checks + indexer-test are localized) or normalize? Status: **non-blocking — Phase 1+ scope decision**.

### Validation (VAL-Q*)
- **VAL-Q1** — How is FluentValidation registered into DI? `RegisterValidatorsFromAssembly` is absent; `AddFluentValidation` is absent. Validators appear to be either constructed in-place inside `RuleFor(...)` chains or instantiated directly by controllers. Need to check `NzbDrone.Host/Bootstrap.cs` and the DryIoc composition root for a generic `Composition.RegisterValidators()` step. Status: **unresolved — blocks Phase 1 DI port**.
- **VAL-Q2** — Provider `Test()` failures with warnings: how does the warning state surface to the frontend? `NzbDroneValidationResult.HasWarnings` is read by Test endpoints, but the wire shape is not documented in this card. Cross-reference with `indexers.md`/`notifications.md` Test-endpoint sections. Status: **unresolved — Phase 1 contract check**.
- **VAL-Q3** — `ResourceValidator<T>.RuleForField` uses `FluentValidation.Internal.PropertyRule` directly (private API). FluentValidation 10+ removed/renamed this. The Go port must replicate field-level validation against `List<Field>` independently — no FluentValidation analogue. Status: **unresolved — blocks Phase 1 provider settings validation**.
- **VAL-Q4** — `SonarrErrorPipeline` writes `STJson.ToJson(validationException.Errors)` — the raw FluentValidation collection. The exact JSON shape (camelCase? PascalCase? included fields?) needs golden-snapshotting before Phase 1 so the Go port emits byte-equivalent error envelopes. Status: **unresolved — blocks Phase 1 contract tests** (record corpus to `rest/golden/`).
- **VAL-Q5** — `ValidPort` rejects sub-1024 ports other than 80/443 unconditionally. Is this intended for both bind-port (server) and remote-port (download client) settings? Distinct validators may be needed. Status: **unresolved — non-blocking but worth documenting**.
- **VAL-Q6** — `MappedNetworkDriveValidator` only fires when `OsInfo.IsWindows && _runtimeInfo.IsWindowsService`. Go port runs on non-Windows mostly; should this validator be a no-op on Linux/Mac, or be removed entirely from those builds via build tags? Status: **non-blocking — Phase 1 detail**.

### Languages (LANG-Q*)
- **LANG-Q1** — `Language.English` etc. allocate a new struct on every access. Confirm no caller mutates `Id` or `Name` post-construction; safe to convert to package-level singletons in Go. Status: **unresolved — quick grep gate**.
- **LANG-Q2** — The 54 IDs in `Language.cs` are a hard contract surface (DB JSON, API). Is there a place in `migrations/ledger.md` or schema docs that locks the id↔name mapping, or is the Language.cs file itself the source of truth? Status: **unresolved — Phase 0.5 documentation gap**.
- **LANG-Q3** — `OrderBy(l => l.Id > 0).ThenBy(l => l.Name)` puts `Original` (-2) and `Unknown` (0) first, then alphabetical by name. Frontend consumers may rely on this exact order. Capture the rendered list in a golden test before porting. Status: **unresolved — Phase 1 contract test**.
- **LANG-Q4** — `LanguagesComparer.Compare` doesn't satisfy `IComparer` strict-total-order in all branches (returns 0 for some non-equal inputs at branch 6). Identify every consumer and verify whether they pass it to `List<T>.Sort` (which requires strict order) or to `OrderBy` (more forgiving). Status: **unresolved — blocks Phase 1 sort port** if any consumer is `List.Sort`.
- **LANG-Q5** — `OriginalLanguageFieldConverter` keeps `Unknown` (0) and excludes `Original` (-2), despite its name suggesting the opposite. Confirm with frontend whether this is the intended dropdown for "Series original language" provider field, and if so, whether `Unknown` is the desired "no preference" option. Status: **unresolved — Phase 1 parity**.
- **LANG-Q6** — `IsoLanguages` (`Parser/IsoLanguages.cs`) maps Czech to `cs`/`ces` but `AlternateIsoCodeMappings` accepts `cze` (and similar for `dut`, `mac`, `rum`). Are these alternate codes still emitted by any indexer / parser path the port must support? Status: **non-blocking — covered by `parser.md`**.
