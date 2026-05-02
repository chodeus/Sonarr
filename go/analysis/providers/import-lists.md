# Import lists provider matrix

Family-level orchestration is documented in `subsystems/importlists-metadata.md`; canonical ThingiProvider plumbing in `subsystems/thingiprovider.md`. Sonarr ships **10 concrete `IImportList` providers**: Plex Watchlist (API), Plex Watchlist (RSS), Trakt User, Trakt Popular, Trakt List, Simkl User, AniList, MyAnimeList, Sonarr (cross-instance), Custom. Each row is a single Phase 4 PR.

`RssImportBase<TSettings>` is registered as a base type but the only concrete RSS provider is `PlexRssImport`; per IL-Q12 the generic RSS list is not user-addable. Confirm before porting.

## Family-level contract

| Aspect | Value |
|--------|-------|
| Provider interface | `IImportList : IProvider` (`src/NzbDrone.Core/ImportLists/IImportList.cs:6`) — `ImportListType ListType { get; }`, `TimeSpan MinRefreshInterval { get; }`, `ImportListFetchResult Fetch()` |
| Settings interface | `IImportListSettings : IProviderConfig` (`src/NzbDrone.Core/ImportLists/IImportListSettings.cs:5`) — only `BaseUrl` is required by interface |
| Settings base | `ImportListSettingsBase<T>` (`src/NzbDrone.Core/ImportLists/ImportListSettingsBase.cs`) — Equ memberwise comparer |
| Abstract base | `ImportListBase<TSettings>` (`src/NzbDrone.Core/ImportLists/ImportListBase.cs:31`) → `HttpImportListBase<TSettings>` (`src/NzbDrone.Core/ImportLists/HttpImportListBase.cs:20`) for HTTP providers; `RssImportBase<TSettings>` for RSS |
| Definition type | `ImportListDefinition : ProviderDefinition` (`src/NzbDrone.Core/ImportLists/ImportListDefinition.cs:8`) — `EnableAutomaticAdd, SearchForMissingEpisodes, ShouldMonitor (MonitorTypes), MonitorNewItems (NewItemMonitorTypes), QualityProfileId, SeriesType, SeasonFolder, RootFolderPath, Tags`. Plus `[MemberwiseEqualityIgnore]` runtime-only `Status, ListType, MinRefreshInterval`. `Enable => EnableAutomaticAdd` |
| Factory | `ImportListFactory : ProviderFactory<IImportList, ImportListDefinition>` (`src/NzbDrone.Core/ImportLists/ImportListFactory.cs`) — adds `AutomaticAddEnabled`, `FilterBlockedImportLists` |
| Repository | `ImportListRepository : ProviderRepository<ImportListDefinition>` (`src/NzbDrone.Core/ImportLists/ImportListRepository.cs`) — adds `UpdateSettings(model)` for OAuth token persistence |
| DB table | `ImportLists` (mig 142; `Settings` and `ConfigContract` are **nullable**, `Name` is unique). Status in `ImportListStatus` (mig 142; column is `ProviderId`; adds `LastSyncListInfo` string) |
| Status service | `ImportListStatusService` — adds `LastInfoSync`, `HasRemovedItemSinceLastClean` over base `ProviderStatusBase`. `RecordFailure(retryAfter)` honoured for OAuth proxy errors with `TimeSpan.FromHours(1)` minimum |
| Test action contract | `Test()` returns `ValidationResult`; `ImportListFactory.Test(definition)` (`ImportListFactory.cs:74-93`) records to `IImportListStatusService` only when `Id != 0` |
| Sync trigger | `ImportListSyncCommand` on schedule + on `ProviderUpdated/Added` via `ImportListUpdatedHandler`. Service `ImportListSyncService` (`src/NzbDrone.Core/ImportLists/ImportListSyncService.cs`) orchestrates fetch → ID-translate → exclusion-filter → `IAddSeriesService.AddSeries` |
| Cleanup contract | `ListSyncLevelType` ∈ `Disabled, LogOnly, KeepAndUnmonitor, KeepAndTag` (`ImportLists/ListSyncLevelType.cs`); applied in `TryCleanLibrary` |

## Concrete providers

### PlexImport (Plex Watchlist API)

| Field | Value |
|-------|-------|
| `Implementation` | `PlexImport` |
| `ConfigContract` | `PlexListSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Plex/PlexImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Plex/PlexListSettings.cs` |
| ListType | `Plex` |
| Transport | `IPlexTvService.GetWatchlist(token,size,offset)`; paged 100, max 10 pages (1000-item silent cap — IL-Q7) |
| Auth model | Plex.tv OAuth (RequestAction `startOAuth/continueOAuth/pollOAuth/getOAuthToken`) |
| `MinRefreshInterval` | 6h; `RateLimit` 5s |
| Notable quirks | `Scope` returns `""` and is never overridden (vestigial OAuth scope hook — IL card flag) |
| Test fixtures | `ImportListTests/Plex/PlexParserFixture.cs` |

**Settings fields:** `BaseUrl` (hidden — Plex.tv hardcoded), `AccessToken` (Privacy=ApiKey, Hidden=HiddenIfNotSet), `SignIn` (Type=OAuth).

### PlexRssImport (Plex Watchlist RSS)

| Field | Value |
|-------|-------|
| `Implementation` | `PlexRssImport` |
| `ConfigContract` | `PlexRssImportSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Rss/Plex/PlexRssImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Rss/Plex/PlexRssImportSettings.cs` |
| ListType | `Plex` |
| Transport | User-supplied RSS URL from `https://app.plex.tv/desktop/#!/settings/watchlist` |
| Auth model | Anonymous (signed URL is the credential) |
| `MinRefreshInterval` | 6h |
| Notable quirks | `PlexRssImportParser` filters `category=show` and parses `imdb://`/`tvdb://`/`tmdb://` guids |
| Test fixtures | `ImportListTests/Plex/PlexParserFixture.cs` |

**Settings fields:** `BaseUrl` (Type=Url, the user RSS URL).

### TraktUserImport

| Field | Value |
|-------|-------|
| `Implementation` | `TraktUserImport` |
| `ConfigContract` | `TraktUserSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Trakt/User/TraktUserImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Trakt/User/TraktUserSettings.cs` |
| ListType | `Trakt` |
| Transport | `https://api.trakt.tv/users/{user}/{watchlist|watched|collection}/shows[/sorting]` |
| Auth model | Trakt OAuth via servarr.com proxy (client_id `d44ba57c…`, redirect `https://auth.servarr.com/v1/trakt_sonarr/auth`, renew `…/renew`) |
| `MinRefreshInterval` | 12h |
| Notable quirks | `Limit` 1-100 (default 100); `TraktUserListType` ∈ `UserWatchList | UserWatchedList | UserCollectionList` |
| Test fixtures | none |

**Settings fields (inherits `TraktSettingsBase`):** `BaseUrl` (default Trakt API), `AccessToken` (Privacy=ApiKey, Hidden), `RefreshToken` (Privacy=ApiKey, Hidden), `Expires` (Hidden), `AuthUser` (Hidden), `SignIn` (Type=OAuth), plus `TraktListType` (Select), `Username`, `TraktAdditionalParameters`, `Limit`, `TraktListSorting` (Select).

### TraktPopularImport

| Field | Value |
|-------|-------|
| `Implementation` | `TraktPopularImport` |
| `ConfigContract` | `TraktPopularSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Trakt/Popular/TraktPopularImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Trakt/Popular/TraktPopularSettings.cs` |
| ListType | `Trakt` |
| Transport | `/shows/{trending|popular|anticipated|watched/{weekly|monthly|all}|recommended/{weekly|monthly|all}}` |
| Auth model | Trakt OAuth (same proxy) |
| `MinRefreshInterval` | 12h |
| Notable quirks | `TopWatchedByYear`/`RecommendedByYear` are `[Obsolete]`; validator rejects with "Yearly lists are no longer supported"; dead `case` arms wrapped in `#pragma warning disable CS0612` (`TraktPopularRequestGenerator.cs:43`) |
| Test fixtures | none |

**Settings fields (`TraktSettingsBase` + `TraktPopularSettings`):** Trakt OAuth fields, `TraktListType` (Select `TraktPopularListType`), `Years`, `Genres`, `Rating`, `Certification`, `TraktAdditionalParameters`, `Limit`.

### TraktListImport

| Field | Value |
|-------|-------|
| `Implementation` | `TraktListImport` |
| `ConfigContract` | `TraktListSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Trakt/List/TraktListImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Trakt/List/TraktListSettings.cs` |
| ListType | `Trakt` |
| Transport | `/users/{user}/lists/{list-slug}/items/show,season,episode?limit=N` |
| Auth model | Trakt OAuth |
| `MinRefreshInterval` | 12h |
| Test fixtures | none |

**Settings fields:** Trakt OAuth fields, `Username`, `Listname`, `Limit`, `TraktAdditionalParameters`.

### SimklUserImport

| Field | Value |
|-------|-------|
| `Implementation` | `SimklUserImport` |
| `ConfigContract` | `SimklUserSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Simkl/User/SimklUserImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Simkl/User/SimklUserSettings.cs` |
| ListType | `Simkl` |
| Transport | `https://api.simkl.com/sync/all-items/{shows|anime}/{watching|plantowatch|hold|completed|dropped}` |
| Auth model | Simkl OAuth via servarr.com proxy (client_id `3281c139…`) |
| `MinRefreshInterval` | 6h |
| Notable quirks | Pre-checks `/sync/activities`; if no activity since `LastInfoSync − 2h`, returns empty `ImportListFetchResult { AnyFailure=true }` to inhibit library cleanup (`SimklImportBase.cs:50`) |
| Test fixtures | none |

**Settings fields:** Simkl OAuth (`AccessToken`, `RefreshToken`, `Expires`, `AuthUser`, `SignIn`), `ShowType` (Select `SimklUserShowType` shows/anime), `ListType` (Select `SimklUserListType`).

### AniListImport

| Field | Value |
|-------|-------|
| `Implementation` | `AniListImport` |
| `ConfigContract` | `AniListSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/AniList/List/AniListImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/AniList/List/AniListSettings.cs` |
| ListType | `Other` |
| Transport | GraphQL POST `https://graphql.anilist.co` paged 50 |
| Auth model | AniList OAuth via servarr.com proxy (client_id `13780`) |
| `MinRefreshInterval` | 12h |
| Notable quirks | Overrides `FetchItems` because pagination is response-driven (`pageInfo.hasNextPage`); duplicates the base-class catch ladder (IL card flag — risk of drift) |
| Test fixtures | none |

**Settings fields (`AniListSettingsBase` + `AniListSettings`):** `BaseUrl`, OAuth fields, `Username`, `ImportCurrent` / `ImportPlanning` / `ImportCompleted` / `ImportDropped` / `ImportPaused` / `ImportRepeating` (six Checkboxes), `MediaStatusReleasing` / `MediaStatusFinished` / `MediaStatusUnreleased` / `MediaStatusCancelled` / `MediaStatusHiatus` (Checkboxes).

### MyAnimeListImport

| Field | Value |
|-------|-------|
| `Implementation` | `MyAnimeListImport` |
| `ConfigContract` | `MyAnimeListSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/MyAnimeList/MyAnimeListImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/MyAnimeList/MyAnimeListSettings.cs` |
| ListType | `Other` |
| Transport | `https://api.myanimelist.net/v2/users/@me/animelist?fields=list_status&limit=1000[&status=]` |
| Auth model | MAL OAuth via servarr.com proxy |
| `MinRefreshInterval` | 6h |
| Notable quirks | `Limit=1000` hardcoded server-side; `MyAnimeListStatus` enum All/Watching/Completed/OnHold/Dropped/PlanToWatch |
| Test fixtures | none |

**Settings fields:** OAuth fields, `Status` (Select `MyAnimeListStatus`), `MinimumScore` (0-10).

### SonarrImport (cross-instance)

| Field | Value |
|-------|-------|
| `Implementation` | `SonarrImport` |
| `ConfigContract` | `SonarrSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Sonarr/SonarrImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Sonarr/SonarrSettings.cs` |
| ListType | `Program` |
| Transport | `GET /api/v3/series` + `qualityprofile`/`languageprofile`/`tag`/`rootfolder` via `SonarrV3Proxy` |
| Auth model | API key in `X-Api-Key` |
| `MinRefreshInterval` | 5m |
| Notable quirks | RequestAction `getProfiles/getLanguageProfiles/getTags/getRootFolders` populates UI selects. `LanguageProfileIds` carries deprecation TODO (IL-Q card flag) |
| Test fixtures | none |

**Settings fields:** `BaseUrl`, `ApiKey` (Privacy=ApiKey), `ProfileIds` (Type=Select multi, SelectOptionsProviderAction="getProfiles"), `LanguageProfileIds` (deprecated, Hidden=HiddenIfNotSet), `TagIds` (SelectOptionsProviderAction="getTags"), `RootFolderPaths` (SelectOptionsProviderAction="getRootFolders"), `SyncSeasonMonitoring` (Checkbox).

### CustomImport

| Field | Value |
|-------|-------|
| `Implementation` | `CustomImport` |
| `ConfigContract` | `CustomSettings` |
| Source file | `src/NzbDrone.Core/ImportLists/Custom/CustomImport.cs` |
| Settings class | `src/NzbDrone.Core/ImportLists/Custom/CustomSettings.cs` |
| ListType | `Advanced` |
| Transport | `GET <BaseUrl>` returning `[{Title, TvdbId, TmdbId, ImdbId}, …]` |
| Auth model | None (caller injects auth in URL or upstream) |
| `MinRefreshInterval` | 6h |
| Notable quirks | Substitutes `Title = "TvdbId: {tvdbId}"` when upstream omits title (`CustomImport.cs:46`); `IsValidItem` survives synthetic title (IL card flag) |
| Test fixtures | none |

**Settings fields:** `BaseUrl` only (Type=Url).

## Open questions

- **PROV-Q23** — `RssImportBase<TSettings>` is publicly registerable as `Advanced` but only `PlexRssImport` is wired up. Per IL-Q12, confirm whether the generic RSS list is reachable via the API (do `GET /api/v3/importlist/schema` defaults include it?) and decide whether to ship as a first-class provider in Go.
- **PROV-Q24** — `TraktSettingsBase`, `SimklSettingsBase`, `AniListSettingsBase` are abstract — they don't have a `ConfigContract` string of their own (no rows persist directly). The Go registry can either skip them entirely or surface them as opaque "shared OAuth schema" types. Recommend skip — only the concrete subclasses get registry entries.
- **PROV-Q25** — All four OAuth-based providers (Trakt, Simkl, AniList, MyAnimeList) depend on `auth.servarr.com` proxies. Per IL-Q6, decide for the Go port whether to keep the proxy dependence or pivot to per-instance OAuth (which would require user-supplied client_id/secret per provider — UX regression but eliminates a single point of failure).
- **PROV-Q26** — Trakt's three providers (`TraktUserImport`, `TraktPopularImport`, `TraktListImport`) share the same OAuth tokens but the tokens are persisted per-provider-row. A user with all three configured will run three independent OAuth dances. Consider hoisting the token to a shared `Config` row in Go port? Behaviour change — flag.
- **PROV-Q27** — `SimklImportBase`'s `AnyFailure=true` short-circuit for "no activity" (IL card flag) leaks into `FetchAndParseImportListService`'s control flow. The Go port can model this more cleanly with a `FetchResult{Skip: true, Reason: "no upstream changes"}` field instead of overloading `AnyFailure`. Behavioural-equivalent but cleaner. Decide.
- **PROV-Q28** — `ImportListItemInfo` is reused as both transport DTO and persisted row (IL-Q9). Splitting them in Go port is cleaner but the DB column layout must still match the existing `ImportListItems` schema. Recommend two separate types with explicit conversion.
- **PROV-Q29** — `CustomImport` validates only `BaseUrl` (IL-Q10). The Go port could add a JSON-schema validator on the response shape for porter friendliness, but it's a behaviour delta from C# (which silently fails on bad input). Flag.
