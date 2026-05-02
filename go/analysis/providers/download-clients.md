# Download clients provider matrix

Family-level orchestration is documented in `subsystems/download.md`; the canonical ThingiProvider plumbing is in `subsystems/thingiprovider.md`. Sonarr ships **20 concrete download client classes** (per `subsystems/download.md` — the card initially says 24 in prose but the per-row inventory lists 20: Sabnzbd, Nzbget, NzbVortex, UsenetDownloadStation, UsenetBlackhole, Pneumatic, QBittorrent, Transmission, Vuze, Deluge, uTorrent, rTorrent, Aria2, RQBit, Hadouken, Flood, TorrentDownloadStation, TorrentFreeboxDownload, TorrentBlackhole, TriblerDownloadClient). Two protocols: `DownloadProtocol.Usenet` and `DownloadProtocol.Torrent`. All inherit the protocol base class hierarchy `IDownloadClient → DownloadClientBase<TSettings> → {TorrentClientBase | UsenetClientBase}` (Pneumatic and the original Blackhole pair attach directly to `DownloadClientBase`).

Each row is a single Phase 4 PR.

## Family-level contract

| Aspect | Value |
|--------|-------|
| Provider interface | `IDownloadClient : IProvider` (`src/NzbDrone.Core/Download/IDownloadClient.cs`) |
| Settings base | `DownloadClientSettingsBase<T>` (`src/NzbDrone.Core/Download/DownloadClientSettingsBase.cs`); torrent settings expose nested `TorrentSeedConfiguration` |
| Abstract bases | `DownloadClientBase<TSettings>` (`src/NzbDrone.Core/Download/DownloadClientBase.cs`); `TorrentClientBase<TSettings>` (`src/NzbDrone.Core/Download/Clients/TorrentClientBase.cs`); `UsenetClientBase<TSettings>` (`src/NzbDrone.Core/Download/Clients/UsenetClientBase.cs`) |
| Definition type | `DownloadClientDefinition : ProviderDefinition` (`src/NzbDrone.Core/Download/DownloadClientDefinition.cs`) — adds `Protocol, Priority, RemoveCompletedDownloads, RemoveFailedDownloads` |
| Factory | `DownloadClientFactory : ProviderFactory<IDownloadClient, DownloadClientDefinition>` (`src/NzbDrone.Core/Download/DownloadClientFactory.cs`) — adds `DownloadHandlingEnabled`, `FilterBlockedClients` |
| Selector | `DownloadClientProvider` (`src/NzbDrone.Core/Download/DownloadClientProvider.cs`) — round-robin per protocol with priority groups, honours `Series.Tags` and `Indexer.DownloadClientId` |
| Repository | `DownloadClientRepository : ProviderRepository<DownloadClientDefinition>` (`src/NzbDrone.Core/Download/DownloadClientRepository.cs`) |
| DB table | `DownloadClients` (mig 042; `Settings` is **not nullable**, `Name` not unique). Status in `DownloadClientStatus` (mig 115; column is `ProviderId`) |
| Status service | `DownloadClientStatusService` — `MinimumTimeSinceInitialFailure=5min`, `MaximumEscalationLevel=5` |
| Test action contract | `Test()` returns `ValidationResult`. `DownloadClientFactory.Test(definition)` records to `IDownloadClientStatusService` only when `Id != 0` (`DownloadClientFactory.cs:111-130`) |
| Polly retry | 2 retries, 3s base + jitter + exponential, on 5xx / 408 / transport errors (`DownloadClientBase.cs:30-60`) |
| Lifecycle events | `EpisodeGrabbedEvent`, `DownloadCompletedEvent`, `DownloadFailedEvent`, `DownloadCanBeRemovedEvent`, `ManualInteractionRequiredEvent`, `TrackedDownloadRefreshedEvent`. `DownloadEventHub` calls `MarkItemAsImported` then `RemoveItem(deleteData=true)` per `RemoveCompleted/Failed` flags |

## Concrete providers

### Sabnzbd

| Field | Value |
|-------|-------|
| `Implementation` | `Sabnzbd` |
| `ConfigContract` | `SabnzbdSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Sabnzbd/Sabnzbd.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Sabnzbd/SabnzbdSettings.cs` |
| Protocol / base | Usenet / `UsenetClientBase` |
| Wire protocol | HTTP+JSON `mode=…&apikey=…` |
| Auth model | API key (+ optional username/password for the same web UI gate) |
| Notable quirks | Version regex `(\d+)\.(\d+)\.(\d+|x)` accepts `x` for git builds (`Sabnzbd.cs:38`); checks free space + history age; `GetImportItem` re-fetches the path for retries (only client to override this) |
| Test fixtures | `Download/DownloadClientTests/SabnzbdTests/{SabnzbdFixture,JsonConvertersTests/*}.cs` |

**Settings fields:** `Host` (default `localhost`), `Port` (default 8080), `UseSsl`, `UrlBase`, `ApiKey` (`Privacy=ApiKey`), `Username`, `Password` (`Privacy=Password`), `TvCategory` (default `tv`), `RecentTvPriority` (Select `SabnzbdPriority`), `OlderTvPriority` (Select). All FieldDefinitions follow standard Hostname/Port/UrlBase pattern shared across HTTP-API clients.

### Nzbget

| Field | Value |
|-------|-------|
| `Implementation` | `Nzbget` |
| `ConfigContract` | `NzbgetSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Nzbget/NzbgetSettings.cs` |
| Protocol / base | Usenet / `UsenetClientBase` |
| Wire protocol | JSON-RPC |
| Auth model | Username + password (Basic auth) |
| Notable quirks | `_deleteFailedStatus = HEALTH/DUPE/SCAN/COPY/BAD`; `DownloadClientRejectedReleaseException` on dupes |
| Test fixtures | `Download/DownloadClientTests/NzbgetTests/NzbgetFixture.cs` |

**Settings fields:** `Host`, `Port` (default 6789), `UseSsl`, `UrlBase`, `Username`, `Password` (Privacy=Password), `TvCategory` (default `tv`), `RecentTvPriority`, `OlderTvPriority`, `AddPaused` (Checkbox).

### NzbVortex

| Field | Value |
|-------|-------|
| `Implementation` | `NzbVortex` |
| `ConfigContract` | `NzbVortexSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/NzbVortex/NzbVortex.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/NzbVortex/NzbVortexSettings.cs` |
| Protocol / base | Usenet / `UsenetClientBase` |
| Wire protocol | HTTPS+JSON, session API key |
| Auth model | API key with session establishment |
| Notable quirks | Custom `JsonConverters/` (group state, file state); `NzbVortexAuthenticationException`, `NzbVortexNotLoggedInException` |
| Test fixtures | `Download/DownloadClientTests/NzbVortexTests/NzbVortexFixture.cs` |

**Settings fields:** `Host`, `Port` (default 4321), `UseSsl=true` (default), `UrlBase`, `ApiKey`, `TvCategory`, `RecentTvPriority`, `OlderTvPriority`.

### UsenetDownloadStation

| Field | Value |
|-------|-------|
| `Implementation` | `UsenetDownloadStation` |
| `ConfigContract` | `DownloadStationSettings` (shared with TorrentDownloadStation) |
| Source file | `src/NzbDrone.Core/Download/Clients/DownloadStation/UsenetDownloadStation.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/DownloadStation/DownloadStationSettings.cs` |
| Protocol / base | Usenet / `UsenetClientBase` |
| Wire protocol | Synology DSM API (`DiskStationApi`) |
| Auth model | Username + password (DSM credentials) |
| Notable quirks | Shared folder mapping via `SharedFolderResolver` + `SerialNumberProvider`; supports DSM 7 (`DownloadStation2Task`) |
| Test fixtures | `Download/DownloadClientTests/DownloadStationTests/{UsenetDownloadStationFixture,SharedFolderResolverFixture,SerialNumberProviderFixture,DownloadStationsTaskStatusJsonConverterFixture}.cs` |

**Settings fields:** `Host`, `Port` (default 5000), `UseSsl`, `Username`, `Password`, `TvCategory`, `TvDirectory`.

### UsenetBlackhole

| Field | Value |
|-------|-------|
| `Implementation` | `UsenetBlackhole` |
| `ConfigContract` | `UsenetBlackholeSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Blackhole/UsenetBlackhole.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Blackhole/UsenetBlackholeSettings.cs` |
| Protocol / base | Usenet / `UsenetClientBase` |
| Wire protocol | filesystem (write `.nzb` to NzbFolder, scan WatchFolder) |
| Auth model | None (filesystem) |
| Notable quirks | Returns `null` download id; relies on `ScanWatchFolder` + `ScanGracePeriod` to surface items |
| Test fixtures | `Download/DownloadClientTests/Blackhole/{ScanWatchFolderFixture,UsenetBlackholeFixture}.cs` |

**Settings fields:** `NzbFolder` (Path), `WatchFolder` (Path).

### Pneumatic

| Field | Value |
|-------|-------|
| `Implementation` | `Pneumatic` |
| `ConfigContract` | `PneumaticSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Pneumatic/Pneumatic.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Pneumatic/PneumaticSettings.cs` |
| Protocol / base | Usenet / `DownloadClientBase` directly (no UsenetClientBase) |
| Wire protocol | filesystem only — writes `.strm` for XBMC/Kodi |
| Auth model | None |
| Notable quirks | No queue; `MarkItemAsImported` not supported (default `NotSupportedException`) |
| Test fixtures | `Download/DownloadClientTests/PneumaticProviderFixture.cs` |

**Settings fields:** `NzbFolder`, `StrmFolder`.

### QBittorrent

| Field | Value |
|-------|-------|
| `Implementation` | `QBittorrent` |
| `ConfigContract` | `QBittorrentSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrentSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | HTTP WebUI v1 *or* v2 selected by `QBittorrentProxySelector` |
| Auth model | Username + password (cookie session) |
| Notable quirks | `MarkItemAsImported` switches torrent to `TvImportedCategory`; `_seedingTimeCache`; consults `ITagRepository` for label-tag mapping; `QBittorrentContentLayout` enum |
| Test fixtures | `Download/DownloadClientTests/QBittorrentTests/QBittorrentFixture.cs` |

**Settings fields:** `Host`, `Port` (8080), `UseSsl`, `UrlBase`, `Username`, `Password`, `TvCategory` (`tv-sonarr` default), `TvImportedCategory`, `RecentTvPriority` (Select `QBittorrentPriority`), `OlderTvPriority`, `InitialState` (Select `QBittorrentState`), `SequentialOrder` (Checkbox), `FirstAndLast`, `ContentLayout` (Select `QBittorrentContentLayout`).

### Transmission

| Field | Value |
|-------|-------|
| `Implementation` | `Transmission` |
| `ConfigContract` | `TransmissionSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Transmission/Transmission.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Transmission/TransmissionSettings.cs` |
| Protocol / base | Torrent / `TransmissionBase` (extends `TorrentClientBase`) |
| Wire protocol | RPC over JSON |
| Auth model | Username + password (Basic) |
| Notable quirks | `SupportsLabels = HasClientVersion(4, 0)`; uses labels for `MarkItemAsImported` only on ≥4.0 |
| Test fixtures | `Download/DownloadClientTests/TransmissionTests/{TransmissionFixture,TransmissionFixtureBase}.cs` |

**Settings fields:** `Host`, `Port` (9091), `UseSsl`, `UrlBase` (default `/transmission/`), `Username`, `Password`, `TvCategory`, `TvDirectory`, `RecentTvPriority`, `OlderTvPriority`, `AddPaused`.

### Vuze

| Field | Value |
|-------|-------|
| `Implementation` | `Vuze` |
| `ConfigContract` | `TransmissionSettings` (shared with Transmission) |
| Source file | `src/NzbDrone.Core/Download/Clients/Vuze/Vuze.cs` |
| Settings class | (shared `TransmissionSettings`) |
| Protocol / base | Torrent / `TransmissionBase` |
| Wire protocol | Vuze's Transmission-compat plugin |
| Auth model | Username + password |
| Notable quirks | `MINIMUM_SUPPORTED_PROTOCOL_VERSION = 14` (`Vuze.cs:16`); no labels |
| Test fixtures | `Download/DownloadClientTests/VuzeTests/VuzeFixture.cs` |

**Settings fields:** identical to Transmission; reuse the same `ConfigContract` row representation. **Important for the registry**: two providers share one `ConfigContract` string. The Go registry must handle (Implementation, ConfigContract) pairs where ConfigContract is shared — see PROV-Q7.

### Deluge

| Field | Value |
|-------|-------|
| `Implementation` | `Deluge` |
| `ConfigContract` | `DelugeSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Deluge/DelugeSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | JSON-RPC over HTTP, cookie session |
| Auth model | Password |
| Notable quirks | `_hasAttemptedReconnecting` flag for session expiry retry (single-threaded reconnect, NOT thread-safe — DOWNLOAD card flag); `DelugeLabel`, `DelugeError`, `DelugeUpdateUIResult` DTOs |
| Test fixtures | `Download/DownloadClientTests/DelugeTests/DelugeFixture.cs` |

**Settings fields:** `Host`, `Port` (8112), `UseSsl`, `UrlBase`, `Password`, `TvCategory`, `TvImportedCategory`, `RecentTvPriority`, `OlderTvPriority`, `AddPaused`.

### uTorrent

| Field | Value |
|-------|-------|
| `Implementation` | `UTorrent` |
| `ConfigContract` | `UTorrentSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrent.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrentSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | WebUI HTTP + token |
| Auth model | Username + password + CSRF token |
| Notable quirks | Per-torrent state cache (`UTorrentTorrentCache`); CSRF token bookkeeping; bitfield-encoded `UtorrentState` |
| Test fixtures | `Download/DownloadClientTests/UTorrentTests/UTorrentFixture.cs` |

**Settings fields:** `Host`, `Port` (8080), `UseSsl`, `UrlBase`, `Username`, `Password`, `TvCategory`, `RecentTvPriority`, `OlderTvPriority`, `IntialState` *(sic — verify spelling in source)*.

### rTorrent

| Field | Value |
|-------|-------|
| `Implementation` | `RTorrent` |
| `ConfigContract` | `RTorrentSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/rTorrent/RTorrent.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/rTorrent/RTorrentSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | XML-RPC (or SCGI via HTTP wrapper) |
| Auth model | Username + password |
| Notable quirks | `RTorrentDirectoryValidator` checks subfolder vs flat layout; likely `PreferTorrentFile=true` (rTorrent magnet limited) |
| Test fixtures | `Download/DownloadClientTests/RTorrentTests/RTorrentFixture.cs` |

**Settings fields:** `Host`, `Port` (8080), `UseSsl`, `UrlBase`, `Username`, `Password`, `TvCategory`, `TvDirectory`, `OlderTvPriority`, `RecentTvPriority`, `AddStopped`.

### Aria2

| Field | Value |
|-------|-------|
| `Implementation` | `Aria2` |
| `ConfigContract` | `Aria2Settings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Aria2/Aria2.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Aria2/Aria2Settings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | XML-RPC (aria2 native) |
| Auth model | Secret token |
| Notable quirks | `Aria2Containers` wraps the funky aria2 status response |
| Test fixtures | **None ship** — DOWNLOAD-Q7 |

**Settings fields:** `Host`, `Port` (6800), `UseSsl`, `RpcPath`, `SecretToken` (Privacy=Password).

### RQBit

| Field | Value |
|-------|-------|
| `Implementation` | `RQBit` |
| `ConfigContract` | `RQbitSettings` (note casing in filename: `RQbitSettings.cs`) |
| Source file | `src/NzbDrone.Core/Download/Clients/RQBit/RQBit.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/RQBit/RQbitSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | HTTP API |
| Auth model | None / token |
| Notable quirks | Uses `IDownloadSeedConfigProvider`; lightweight client |
| Test fixtures | `Download/DownloadClientTests/RQBitTests/RQBitFixture.cs` |

**Settings fields:** `Host`, `Port`, `UseSsl`, `UrlBase`, `TvDirectory`. Confirm exact case of class — settings filename is `RQbitSettings.cs` (lowercase `b`) so `ConfigContract` is **`RQbitSettings`** (PROV-Q8).

### Hadouken

| Field | Value |
|-------|-------|
| `Implementation` | `Hadouken` |
| `ConfigContract` | `HadoukenSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Hadouken/Hadouken.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Hadouken/HadoukenSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | JSON-RPC HTTP |
| Auth model | Username + password (Basic) |
| Notable quirks | DTOs in `Models/` (`HadoukenTorrent` etc.) |
| Test fixtures | `Download/DownloadClientTests/HadoukenTests/HadoukenFixture.cs` |

**Settings fields:** `Host`, `Port` (7070), `UseSsl`, `UrlBase`, `Username`, `Password`, `Category`.

### Flood

| Field | Value |
|-------|-------|
| `Implementation` | `Flood` |
| `ConfigContract` | `FloodSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Flood/Flood.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Flood/FloodSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | REST HTTP |
| Auth model | Username + password (Basic via Flood proxy) |
| Notable quirks | Uses `IDownloadSeedConfigProvider`; rich DTO tree under `Models/` and `Types/` |
| Test fixtures | **None** — DOWNLOAD-Q7 |

**Settings fields:** `Host`, `Port` (3000), `UseSsl`, `UrlBase`, `Username`, `Password`, `Destination`, `Tags` (KeyValueList? or Tag list — verify), `AdditionalTags`, `StartOnAdd`.

### TorrentDownloadStation

| Field | Value |
|-------|-------|
| `Implementation` | `TorrentDownloadStation` |
| `ConfigContract` | `DownloadStationSettings` (shared with UsenetDownloadStation) |
| Source file | `src/NzbDrone.Core/Download/Clients/DownloadStation/TorrentDownloadStation.cs` |
| Settings class | (shared `DownloadStationSettings`) |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | DSM API |
| Auth model | DSM username/password |
| Notable quirks | Same shared-folder mapping as Usenet variant |
| Test fixtures | `Download/DownloadClientTests/DownloadStationTests/TorrentDownloadStationFixture.cs` |

### TorrentFreeboxDownload

| Field | Value |
|-------|-------|
| `Implementation` | `TorrentFreeboxDownload` |
| `ConfigContract` | `FreeboxDownloadSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/FreeboxDownload/TorrentFreeboxDownload.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/FreeboxDownload/FreeboxDownloadSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | Freebox HTTP API |
| Auth model | App token (HMAC-derived session) |
| Notable quirks | `FreeboxDownloadEncoding`, `FreeboxDownloadPriority` enums |
| Test fixtures | `Download/DownloadClientTests/FreeboxDownloadTests/TorrentFreeboxDownloadFixture.cs` |

**Settings fields:** `Host`, `Port` (443), `UseSsl=true`, `ApiUrl`, `AppId`, `AppToken`, `Category`, `AddPaused`, `Priority` (Select).

### TorrentBlackhole

| Field | Value |
|-------|-------|
| `Implementation` | `TorrentBlackhole` |
| `ConfigContract` | `TorrentBlackholeSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Blackhole/TorrentBlackhole.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Blackhole/TorrentBlackholeSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | filesystem (`.torrent` or `.magnet` to TorrentFolder) |
| Auth model | None |
| Notable quirks | `SaveMagnetTo` setting; same `ScanWatchFolder` machinery as UsenetBlackhole |
| Test fixtures | `Download/DownloadClientTests/Blackhole/TorrentBlackholeFixture.cs` |

**Settings fields:** `TorrentFolder`, `WatchFolder`, `SaveMagnetTo` (Select: `MagnetFile`/`Convert`...), `ReadOnly` (Checkbox).

### TriblerDownloadClient

| Field | Value |
|-------|-------|
| `Implementation` | `TriblerDownloadClient` |
| `ConfigContract` | `TriblerDownloadSettings` |
| Source file | `src/NzbDrone.Core/Download/Clients/Tribler/TriblerDownloadClient.cs` |
| Settings class | `src/NzbDrone.Core/Download/Clients/Tribler/TriblerDownloadSettings.cs` |
| Protocol / base | Torrent / `TorrentClientBase` |
| Wire protocol | Tribler REST API |
| Auth model | API key |
| Notable quirks | Pairs with the (out-of-scope) Tribler indexer; **no fixtures** — DOWNLOAD-Q7 |
| Test fixtures | none |

**Settings fields:** `Host`, `Port`, `UseSsl`, `UrlBase`, `ApiKey`, `Destination`, `AddPaused`.

## Open questions

- **PROV-Q7** — `TransmissionSettings` is shared by both `Transmission` and `Vuze` (two `Implementation`s, one `ConfigContract`). Confirm the Go registry stores this as `(implementationID, configContractID)` pairs rather than collapsing on `configContractID`. Cross-cite TP-Q1/Q2.
- **PROV-Q8** — `RQbitSettings.cs` filename uses lowercase `b` while the class name in code is the same. Confirm `ConfigContract` string is `RQbitSettings` (case-sensitive C# `Type.Name` would emit exactly that). Inspect the file before locking the registry key.
- **PROV-Q9** — `DownloadStationSettings` is shared by both `UsenetDownloadStation` and `TorrentDownloadStation` — same pattern as PROV-Q7.
- **PROV-Q10** — Per `subsystems/download.md` DOWNLOAD-Q5: legacy `DownloadClientType` enum still exists with only the original 4 entries (`Sabnzbd, Blackhole, Pneumatic, Nzbget`). Identify any Go-port code that would need a parallel enum, or confirm we can drop it (it's not the source of truth for new clients).
- **PROV-Q11** — qBittorrent v1 vs v2 proxy split (DOWNLOAD-Q10): Go port can ship one HTTP API surface or both. Recommend starting with v2 only and surfacing a clear health-check error on v1; this is a behavioural change worth flagging.
- **PROV-Q12** — Aria2, Flood, Tribler ship without C# fixtures (DOWNLOAD-Q7). The Go port should add table-driven HTTP-recording tests for these three before declaring porter-ready, otherwise we have zero regression coverage.
- **PROV-Q13** — `MarkItemAsImported` is opt-in (default throws `NotSupportedException`). The Go port can drop the throw and return a typed error; decide if this needs a registry-level capability flag rather than per-call rescue.
- **PROV-Q14** — `Pneumatic` extends `DownloadClientBase` directly (not `UsenetClientBase`), which means it bypasses `IValidateNzbs.Validate`. Preserve in Go port or unify under `UsenetClientBase` and have Pneumatic skip the validate step?
- **PROV-Q15** — `TorrentClientBase`'s manual redirect handling (`TorrentClientBase.cs:130-167`) is required because Polly is set to `AllowAutoRedirect=false` (to capture magnet `Location:` headers). Go port must preserve this — Go's default `http.Client` follows redirects unless `CheckRedirect` is set to return `http.ErrUseLastResponse`. Document this in the Go HTTP client wrapper.
