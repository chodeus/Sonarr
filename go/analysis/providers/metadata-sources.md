# Metadata sources provider matrix

Family-level orchestration is documented in `subsystems/importlists-metadata.md` (the "MetadataSource architecture" section). This is the **upstream** metadata boundary — answering "what is TVDB id N?" and "what TVDB ids match query Q?".

Sonarr ships **one concrete metadata source**: `SkyHookProxy`, hitting Servarr-hosted `https://skyhook.sonarr.tv/v1/tvdb/...`. Unlike `IIndexer`, `IDownloadClient`, `INotification`, `IImportList`, and `IMetadata`, **this is NOT a ThingiProvider-style pluggable family** — there's no `MetadataSourceDefinition` row, no `MetadataSourceFactory`, no schema endpoint, no settings UI. The class is a singleton DI registration that consumers (`AddSeriesService`, `RefreshSeriesService`, `RefreshEpisodeService`, `ImportListSyncService`, `SeriesController.Lookup`) inject directly via the two interfaces.

This file is therefore much shorter than the others — there is exactly one porter assignment, but it's load-bearing for the entire library-add and refresh paths. We also enumerate **TheTVDB-direct** as a hypothetical second source per the file scope ("possibly TheTVDB direct"); the C# tree contains no such direct provider, so this is captured as PROV-Q* for the Go port.

## Family-level contract

| Aspect | Value |
|--------|-------|
| Provider interfaces | `IProvideSeriesInfo` (`src/NzbDrone.Core/MetadataSource/IProvideSeriesInfo.cs:7`) — `Tuple<Series, List<Episode>> GetSeriesInfo(int tvdbSeriesId)`. `ISearchForNewSeries` (`src/NzbDrone.Core/MetadataSource/ISearchForNewSeries.cs:6`) — `SearchForNewSeries(string title)` + 4 typed lookups (Imdb/AniList/Tmdb/MyAnimeList) |
| Comparator | `SearchSeriesComparer : IComparer<Series>` (`src/NzbDrone.Core/MetadataSource/SearchSeriesComparer.cs`) — exact-match → article-stripped → Levenshtein ≤1 → year-aware Levenshtein → `Title:` prefix → weighted Levenshtein with year proximity |
| Definition type | **None.** Not a ThingiProvider family. No DB table for source definitions |
| Factory | **None.** DI singleton (`SkyHookProxy` registered once in `Bootstrap`) |
| DB table | **None for the source itself.** The persisted `Series`/`Episode` rows produced by `GetSeriesInfo` are owned by `tv.md`'s repos |
| Status tracking | **None.** Errors propagate as `SkyHookException` (HTTP 503) or `SeriesNotFoundException` (HTTP 404) — surfaced to the caller, no escalation back-off |
| Test action contract | **None.** No `Test()` method; service health is not user-visible. A failed SkyHook call surfaces in the UI as the failed series-add or refresh action |
| Cache | **None client-side.** Each `GetSeriesInfo`/`SearchForNewSeries` is a fresh HTTP request (MD-Q2). Caller-side caching is the local `Series`/`Episode` DB rows |
| External dependency | `https://skyhook.sonarr.tv/v1/tvdb/{route}/{language}/` — language hardcoded `en` (`NzbDrone.Common/Cloud/SonarrCloudRequestBuilder.cs:18`) |

## Concrete providers

### SkyHookProxy

| Field | Value |
|-------|-------|
| Class name | `SkyHookProxy` |
| Stable string ID needed? | No — not registry-keyed (singleton) |
| Source file | `src/NzbDrone.Core/MetadataSource/SkyHook/SkyHookProxy.cs` |
| Settings class | **None** (not a ThingiProvider) |
| External service | `https://skyhook.sonarr.tv/v1/tvdb/...` (Servarr-operated TVDB proxy) |
| Auth model | None (anonymous; rate-limited at the server) |
| Endpoints used | `GET /v1/tvdb/shows/en/{id}/` (returns `ShowResource`); `GET /v1/tvdb/search/en/?term={query}` (returns `List<ShowResource>`) |
| Rate-limit hint | Not enforced client-side. Relies on `IHttpClient` honouring `Retry-After` and on caller-side serialization (e.g. `RefreshSeriesService` serial command) |
| Notable quirks | `tvdb:`/`tvdbid:` prefixes short-circuit to local `_seriesService.FindByTvdbId(...)` or direct `GetSeriesInfo`. Other prefixes `imdb:`/`tmdb:`/`mal:`/`anilist:` delegate to `SearchForNewSeries($"{prefix}:{id}")`. Title is `ToLowerInvariant`'d so prefixes must be lowercase. `IsPathValid(AnyOs)` pre-filter rejects path-injection attempts (`InvalidSearchTermException` → HTTP 400). Constructor double-assigns `_requestBuilder` (`SkyHookProxy.cs:36-40`) — dead code |
| Mapping (`MapSeries`, `:182`) | Copies TvdbId/TvRageId/TvMazeId/TmdbId/ImdbId/MalIds/AniListIds; computes `CleanTitle`, `SortTitle`; resolves `OriginalLanguage` via `IsoLanguages.Find` (fallback English); parses `FirstAired/LastAired` `yyyy-MM-dd` AssumeUniversal+AdjustToUniversal; `AirTime` `HH:mm`; `Status` mapped `ended→Ended, upcoming→Upcoming, *→Continuing`; **DataAugmentation hook:** `_dailySeriesService.IsDailySeries(tvdbId) → SeriesType=Daily` (line 249); `Seasons` `Monitored = SeasonNumber > 0` (specials default-unmonitored); top-level `Monitored=true` unconditionally (overridden by callers — MD-Q6) |
| Resources | `ShowResource`, `EpisodeResource`, `SeasonResource`, `ActorResource`, `ImageResource`, `RatingResource`, `TimeOfDayResource` (`src/NzbDrone.Core/MetadataSource/SkyHook/Resource/`) |
| Exceptions | `SkyHookException : NzbDroneClientException` (HTTP 503; `src/NzbDrone.Core/MetadataSource/SkyHook/SkyHookException.cs`); `InvalidSearchTermException : NzbDroneClientException` (HTTP 400; `src/NzbDrone.Core/MetadataSource/SkyHook/InvalidSearchTermException.cs`) |
| Test fixtures | `src/NzbDrone.Core.Test/MetadataSource/SkyHook/{SkyHookProxyFixture,SkyHookProxySearchFixture}.cs`; comparator at `src/NzbDrone.Core.Test/MetadataSource/SearchSeriesComparerFixture.cs` |

**Settings fields:** none. URL is hardcoded in `NzbDrone.Common/Cloud/SonarrCloudRequestBuilder.cs:18`. The only configurable behaviour is the language segment, which is hardcoded `en` and only swappable by editing source.

### TheTVDB direct (placeholder — does not exist in C# v5)

| Field | Value |
|-------|-------|
| Class name | (n/a) |
| Status | **Not implemented in C# tree.** Verified by full-tree grep for `IProvideSeriesInfo` implementations — only `SkyHookProxy` matches |
| Why considered | The file scope notes "possibly TheTVDB direct" as a candidate second metadata source. The Go port could add this if the operator wants to bypass the Servarr proxy (privacy, latency, vendor-independence) |
| External service | `https://api4.thetvdb.com/v4/...` (TVDB direct API v4) |
| Auth model | API key (TVDB requires per-app subscriber key) — would need a settings UI |
| Open question | If introduced, would need to become a ThingiProvider family (with `MetadataSourceDefinition` table, factory, settings, `Test()`). That's a much larger Phase-1 deliverable than porting SkyHook. Recommend deferring to Phase-2 spike. **PROV-Q33** |

## Open questions

- **PROV-Q30** (was MD-Q1) — SkyHook URL is hardcoded in `SonarrCloudRequestBuilder`. The Go port should make the base URL configurable (env var or `Config` row) for self-hosted skyhook mirrors and for testing. Question: how does the `tvdb:`/`tvdbid:` prefix short-circuit behave when the configured server isn't actually skyhook? Recommend the prefix logic is independent of the URL (it just routes to `GetSeriesInfo` directly), so this is safe.
- **PROV-Q31** (was MD-Q2) — No client-side cache means hot-add scenarios (importing N series in a row) generate N HTTP requests. Add an LRU cache `(tvdbId) → ShowResource` with an `If-Modified-Since` flow? Verify SkyHook exposes a `Last-Modified` header. Decide TTL — recommend 1h for `GetSeriesInfo` and no cache for `SearchForNewSeries` (queries are user-typed and unbounded).
- **PROV-Q32** (was MD-Q3) — SkyHook hardcodes `en`. Multi-language metadata is out of scope for v5 v1.0 but the URL template already takes a `{language}` segment. The Go port should plumb `language` through to `IProvideSeriesInfo.GetSeriesInfo(tvdbId, language)` so this becomes a future-proof signature even if v5 ships en-only.
- **PROV-Q33** — Should the Go port introduce a `MetadataSource` ThingiProvider family (table, factory, settings UI) so a TheTVDB-direct provider can be added without code? The C# code does NOT have this. Doing it in Go would be a significant additional design beyond pure port. Recommend: **no**, keep SkyHook as a fixed singleton in v1; revisit if/when a second source ships. Cross-cite TP-Q1 — adding this family later requires picking stable ProviderID strings.
- **PROV-Q34** — `MapSearchResult` returns the existing `Series` row from local DB if the TVDB id matches. The caller cannot distinguish "library hit" from "fresh search hit" (MD-Q5). Add an `IsExisting` flag? Affects the add-series UI but is a pure additive change.
- **PROV-Q35** — `OriginalLanguage` falls back to `Language.English` when upstream omits or sends an unrecognised ISO code (MD-Q7). Use a distinct `Language.Unknown` (or a Go zero-value with a separate `OriginalLanguageMissing bool` flag) and surface a warning? Affects how clients render "Original Language: English" for shows that are actually unknown.
- **PROV-Q36** — `SkyHookProxy` is a singleton with no `Test()`. The Go port should add a healthcheck (one-shot `GET /v1/tvdb/shows/en/81189/` for "Breaking Bad" or similar known-stable id) so operators can detect SkyHook outages without waiting for an actual user-add to fail. Recommend: yes, add one — register under the existing `HealthCheck` framework rather than the ThingiProvider Test path.
- **PROV-Q37** — `SkyHookProxy.cs:36-40` constructor double-assigns `_requestBuilder` (dead code per IL-MD card). Drop in Go port; flag in cleanup-pass docs.
