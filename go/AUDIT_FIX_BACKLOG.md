# Audit Fix Backlog — open items only

**Last consolidated:** 2026-05-20 (post-5.J22).
**Original audit:** 2026-05-20 external review session. Source docs `PORT_ACCURACY_AUDIT.md` + `TEST_QUALITY_AUDIT.md` deleted in 5.J23 — their findings are now reflected in code (closed) or in this file's Open section (deferred). `PHASE_5_AUDIT.md` kept for its B.6 provider-prioritisation structure.

For closure narrative, see commit history `5.J1` … `5.J22` on `go-port`. The table at the bottom maps each audit ID to the slice that closed it.

---

# Open

## B.2 — SignalR broadcast fan-out

**Status:** CLOSED 2026-05-20 across 10 second-wave slices (5.J24–5.J32 wired, 5.J33 rejected). First wave (5.J20) wired `qualityprofile` + `customformat`. Second wave shipped 9 additional channels: `episodefile`-Update (5.J24), `command` distinct from system/task (5.J25), `episode` monitor (5.J26), `customformat`-Update third subscriber (5.J27), `blocklist` (5.J28), `delayprofile` (5.J29), `customfilter` (5.J30), `releaseprofile` (5.J31), `remotepathmapping` (5.J32). The `config/*` row was rejected (5.J33) — audit misread the UI surface; no SignalR `config/*` channels exist in either C# or Go.

**Wire-shape audit note:** the React UI's `SignalRListener.tsx` doesn't subscribe to several of the second-wave channels (customformat, blocklist, delayprofile, customfilter, releaseprofile, remotepathmapping, customfilter). The Go-port broadcasts on those channels anyway to match C#'s wire shape (C# broadcasts via the inherited `ModelEvent<T>` pipeline). Third-party SignalR clients (Sonarr Mobile, *-arr-suite tooling) can still consume them; the React UI's missing listeners are a UI surface gap rather than a wire-contract design.

**Closed families table (commit history is authoritative):**

| Family | Service-side work | Subscriber site |
|---|---|---|
| ~~`delayprofile`~~ | ~~Add `Service.bus` field + publish `DelayProfileUpdatedEvent` on Add/Update/Delete/Reorder. NewService signature change.~~ — **CLOSED 5.J29** via payload-less `DelayProfileUpdatedEvent` from all 4 CUD paths. NewService gained `*messaging.Bus` param. | `delayprofile` Sync broadcaster |
| ~~`customfilter`~~ | ~~Same shape as delayprofile.~~ — **CLOSED 5.J30** via payload-less `CustomFilterUpdatedEvent` from Add/Update/Delete. NewService gained `*messaging.Bus` param. | `customfilter` Sync broadcaster |
| ~~`releaseprofile`~~ | ~~Same.~~ — **CLOSED 5.J31** via payload-less `ReleaseProfileUpdatedEvent` from Add/Update/Delete. NewService gained `*messaging.Bus` param. | `releaseprofile` Sync broadcaster |
| ~~`remotepathmap`~~ | ~~Same.~~ — **CLOSED 5.J32** via payload-less `RemotePathMappingUpdatedEvent` from Add/Update/Remove. NewService gained `*messaging.Bus` param. Channel name is the singular `remotepathmapping` to match the C# resource name (the route is plural `/api/v3/remotepathmapping` but the SignalR channel keys on resource, not route). | `remotepathmapping` Sync broadcaster |
| ~~`blocklist` (delete)~~ | ~~Publish `BlocklistChangedEvent` on Delete + Bulk.~~ — **CLOSED 5.J28** via new `BlocklistChangedEvent` published from `Service.Delete` / `DeleteByIDs` / `DeleteForSeries`. NewService gained an optional `*messaging.Bus` parameter — buildHostServer + v3Routes threaded the host's bus through. | `blocklist` Sync broadcaster |
| ~~`command` (POST/DELETE)~~ | ~~`commands.Service` already publishes; needs distinct broadcaster channel from systemTask.~~ — **CLOSED 5.J25** via new `CommandUpdatedEvent` fired by `Service.Push`/`PushMany`/`Start`/`Complete`/`Fail`/`Cancel` + distinct `command` channel broadcaster. C# `SendUpdatesToClient` body-filter + 100ms debounce remain deferred (every command broadcast, no debounce). | Distinct `command` `ResourceBroadcaster[CommandResource]` |
| ~~`episode` (monitor)~~ | ~~Publish `EpisodeMonitoredChangedEvent` from `episodes.Service.SetMonitored`.~~ — **CLOSED 5.J26** via new `EpisodeMonitoredChangedEvent` fired by `SetEpisodeMonitored` / `SetMonitored` / `SetEpisodeMonitoredBySeason`. | `episode` Sync broadcaster |
| ~~`episodefile` (update/bulk/editor)~~ | ~~Already has Created/Deleted broadcasts; Update + Editor need their own event.~~ — **CLOSED 5.J24** via `EpisodeFileUpdatedEvent` published from `Service.Update` + `Service.UpdateMany`. The api/v3 EditorHandler + BulkUpdateHandler funnel through UpdateMany, so they're implicitly covered. | Third subscriber on existing `episodefile` broadcaster |
| ~~`config/*` (host, ui, naming, mediamanagement, downloadclient, indexer)~~ | ~~`ConfigFileSavedEvent` / `ConfigSavedEvent` already fire — wire the subscriptions per `config/*` channel the React UI listens to.~~ — **REJECTED 5.J33 (audit was mistaken).** Confirmed both ends: (1) C# `src/Sonarr.Api.V3/Config/*Controller.cs` controllers extend `RestController<>` (NOT `RestControllerWithSignalR`) and have no manual `BroadcastResourceChange` calls; (2) C# has zero `IHandle<ConfigSavedEvent>` SignalR subscribers (only consumer is `UiAuthorizationHandler` which doesn't broadcast); (3) the React UI's `frontend/src/Components/SignalRListener.tsx` channel-name switch has no `config/*` case (lists: calendar, command, downloadclient, episode, episodefile, health, importlist, indexer, metadata, connection, qualitydefinition, queue, queue/details, queue/status, rootfolder, series, system/task, tag, version, wanted/cutoff, wanted/missing). The audit row was based on a misreading of the UI listener surface — no config/* SignalR channels exist in either tree. | (none — no broadcaster wired) |
| ~~`customformat` (update)~~ | ~~Publish `CustomFormatUpdatedEvent` on Update (C# `:51-55` doesn't emit; Go-port additive).~~ — **CLOSED 5.J27** via new `CustomFormatUpdatedEvent` published from `Service.Update`. | Third subscriber on existing `customformat` channel |

**Acceptance:** every POST/PUT/DELETE handler in `internal/api/v3/` has either a direct-emitter call on its happy path OR a service-side bus event that signalr_wiring subscribes to.

## B.3 — Scheduled-task handlers (5 stubs / missing)

**Status:** REAL. All 5 still stubbed or absent.

| Handler | Blocker / work |
|---|---|
| `RefreshSeries` | **Blocked on SkyHook port** (queue [M3] in STATUS.md). The handler needs `skyhook.GetSeriesInfo + MapSeries` which isn't fully wired yet. |
| ~~`RefreshMonitoredDownloads`~~ | ~~Needs orchestration: poll every enabled DC's `GetItems()` → update Queue state → fire `QueueUpdatedEvent`.~~ — **CLOSED 5.J37** via new `internal/downloadmonitoring.Service.Refresh(ctx)` which enumerates ListByProtocol(Usenet)+ListByProtocol(Torrent), calls GetItems on each enabled client, then publishes `QueueUpdatedEvent` + `TrackedDownloadRefreshedEvent`. Wired into `handlers.Deps.MonitoredDownloadRefresher`; the RefreshMonitoredDownloads handler now delegates. Per-client poll failures absorbed (warn-and-continue posture matches C# `:91-105`). The failed/completed-download checks + the on-demand debounced refresh (C# `:55-79` ProcessMonitoredDownloads dispatch + `:172-178` IHandle subscribers) remain deferred — the 1-minute scheduled cadence runs Refresh naturally without the debounce wiring. |
| ~~`CheckForFinishedDownloads`~~ | ~~Same DC poll, then trigger import-decision pipeline on completed grabs. Imports already work end-to-end (Phase 6.A6); the trigger seam is missing.~~ — **CLOSED 5.J38** as a deprecated alias to RefreshMonitoredDownloads (mirrors C# `DownloadMonitoringService.cs:166-170` post-deprecation behaviour: warn + delegate). Handler now takes the same Refresher as RefreshMonitoredDownloads via `Deps.MonitoredDownloadRefresher`. The full "completed-import dispatch" trigger seam noted in the original audit is the `ProcessMonitoredDownloads` follow-up (see RefreshMonitoredDownloads row — deferred to a later slice that adds trackeddownloads cache plumbing + completeddownloads service integration). |
| ~~`MissingEpisodeSearchCommand`~~ | ~~Missing entirely. Iterate monitored episodes without files → call `indexersearch.Service.EpisodeSearch`.~~ — **CLOSED 5.J39** via new `MissingEpisodeSearch` handler + `episodes.Repo.EpisodesWithoutFiles(ctx, monitored, now)` + `*indexersearch.Service.EpisodeSearch` adapter. JSON body `{seriesId int?, monitored bool}` parsed (monitored defaults to true matching C# constructor). SeriesId scope filter applied at the handler tier. Queue-subtraction (C# `GetQueuedEpisodeIds`) + `SearchForBulkEpisodes` season-grouping are deferred — documented divergence in the handler doc-comment. |
| ~~`ApplicationCheckUpdateCommand`~~ | ~~Missing entirely. Fetch the update-availability feed (services.sonarr.tv) and surface as a healthcheck.~~ — **CLOSED 5.J40** via new `internal/update` package (Service + UpdatePackage + BranchProvider) + `ApplicationCheckUpdate` handler that delegates to it. Hits services.sonarr.tv/v1/update/{branch} with version+os+arch+runtime=netcore. Adapter satisfies the deferred `healthcheck.UpdateAvailableLookup` from 5.J35 — the install-side (`ApplicationUpdateCommand` Execute, InstallUpdate, GPG-verify) remains deferred until the install pipeline ports. DefaultScheduledTask added at 6h cadence matching C# `TaskManager.cs:127`. |

**Sequencing:** RefreshMonitoredDownloads + CheckForFinishedDownloads are the most user-visible — without them, qBittorrent / SAB items grabbed via the API don't refresh in the UI without manual reload. Worth a slice each.

## B.5 — Missing healthchecks (1 of 10 remains)

**Status:** PARTIAL. 9 closed + 1 partial (5.J10/J11/J12/J13/J14/J18/J34/J36 closed, 5.J35 partial UpdateCheck — see row). 1 fully remains:

| Check | Blocker / size |
|---|---|
| ~~`MountCheck`~~ | ~~Needs `disk.Provider.GetMount(path)` returning a Mount struct with `IsReadOnly`. Platform-specific syscall work — Linux `unix.Statfs_t.Flags & ST_RDONLY`, macOS `unix.Statfs_t.Flags & MNT_RDONLY`, Windows `GetVolumeInformation`.~~ — **CLOSED 5.J36** via new `internal/disk/mount.go` (Mount struct + GetMount platform-dispatch) with build-tagged impls: `mount_unix.go` (statfs), `mount_linux.go` (ST_RDONLY isReadOnly), `mount_darwin.go` (MNT_RDONLY isReadOnly), `mount_windows.go` (GetVolumeInformationW + FILE_READ_ONLY_VOLUME flag). MountCheck at `internal/healthcheck/mount_check.go` ports the C# logic verbatim — de-duplicates by RootDirectory, walks series-ID-sorted for deterministic message ordering. |
| ~~`UpdateCheck`~~ | ~~Needs disk-capability + version-string compare…~~ — **PARTIAL 5.J35.** The three writability concerns (translocation, startup-not-writable, UI-not-writable) ported with the same Auto+BuiltIn+!Docker gate as C#. Step 4 (`update available` banner) is wired through an `UpdateAvailableLookup` interface that returns nil today — the upstream services.sonarr.tv fetcher (`CheckUpdateService` in C#) isn't ported yet; the wiring path is stable so the lookup can be retrofitted in a later slice without re-touching the check. |
| ~~`PackageGlobalMessageCheck`~~ | ~~Needs `DeploymentInfoProvider` port…~~ — **CLOSED 5.J34** via `internal/deploymentinfo` package (new) + `PackageGlobalMessageCheck` in healthcheck registry. Both DEB/RPM `package_info` + tarball `release_info` parses ported; severity-prefix dispatch (`Error:` / `Warn:`) mirrors C#. |
| `RemotePathMappingCheck` | Heaviest single check — 387 LOC of docker-detection logic (cross-references DC path → root folder → remote-path-mapping table). Probably its own multi-slice arc. |

Also still in scope: `ProxyCheck` is wired but stubbed (the `proxyHostConfigStub` always reports ProxyEnabled=false because the HostConfig keyvalues-backed IConfigService hasn't ported). Closing this means porting `IConfigService.ProxyEnabled` / `ProxyHostname` properly — moderate work.

## B.6 — Stub providers (7 indexers + 15 download clients)

**Status:** REAL. The largest single chunk of remaining audit work — each provider is ~1500-2500 LOC of real-network impl.

**Indexers missing:** BroadcastheNet, Fanzub, FileList, HDBits, IPTorrents, Nyaa, Torrentleech (7).

**Download clients missing:** ~~Aria2~~ (**skeleton landed 5.J50**: Settings + registration; all Provider methods return ErrNotImplementedYet except Test which surfaces Settings.Validate failures; XML-RPC Proxy + per-method calls land in follow-up slices), TorrentDownloadStation, UsenetDownloadStation, Flood, TorrentFreeboxDownload, Hadouken, NzbVortex, Pneumatic, RQBit, ~~rTorrent~~ (**CLOSED across 5.J41–J45**: skeleton + XML-RPC Proxy + Test version probe + Download (load.start / load.raw_start) + GetItems (d.multicall.2 with 12-column projection) + RemoveItem (d.erase) + GetStatus + MarkItemAsImported (d.custom1.set for post-import label). Documented divergences: WaitForTorrent post-add poll replaced by fire-and-trust; deleteData on-disk cleanup deferred; PushTorrentUniqueView for the rTorrent UI view deferred; per-grab seed-config gating of CanMoveFiles/CanBeRemoved defers to the queue layer's existing enforcement.), TorrentBlackhole, TriblerDownloadClient, UsenetBlackhole, ~~uTorrent~~ (**CLOSED across 5.J46–J49**: skeleton + token-cookie Proxy + Test version probe + Download (add-url / add-file multipart + post-add chain: SetTorrentLabel + MoveTorrentToTopInQueue + SetState) + GetItems (list=1 with 19+ column row decode + Status bitflag mapping + OutputPath basename quirk + CanMoveFiles gate) + RemoveItem (remove / removedata) + GetStatus + MarkItemAsImported (action=setprops s=label). Documented divergences: cacheID delta optimization on list deferred (always full-refresh — extra bandwidth, no functional gap); RemoveTorrentLabel post-import old-label cleanup deferred — leaves the post-import torrent tagged with BOTH labels which is harmless visually.), Vuze (13 still missing).

**Sequencing (per `PHASE_5_AUDIT.md §Tier 2` and Phase 6 priorities):**

- Tier 1 by real-world share: rTorrent + uTorrent (each ~3-5% of torrent installs).
- Tier 2: Aria2 (multi-protocol downloader, popular in niche setups), Vuze (legacy).
- Tier 3: indexer ports — IPTorrents + Torrentleech are highest-share among the missing.
- Tier 4: everything else only as users report missing-impl gaps.

Each provider arc = its own multi-slice sequence (constructor + Download + GetItems + GetStatus + RemoveItem + tests + registry registration).

---

# Closed (commit history is authoritative)

| Item | Closed in slice | Commit |
|---|---|---|
| A.1 | 5.J1 | 5536fd169 — REJECTED: audit misread C# `WhenWritingNull` config |
| A.2 | 5.J2 | 338d2ac7d — sort.SliceStable at six tiebreaker-prone sites |
| A.3 | 5.J1 | 5536fd169 — PHASE_5_AUDIT.md supersession banner |
| A.4 | 5.J3 | 9844f9b26 — DisabledForLocalAddresses bypass + ?access_token= path-gate |
| B.1 | 5.J7-J8 | 7923938c7 — contract-replay CI job + drift baseline regression guard |
| B.4 | 5.J19 | b2d872809 — /api/v3/release dispatch through indexersearch.Service |
| B.5a | 5.J10 | 87dca111d — IndexerJackettAllCheck |
| B.5b | 5.J11 | e742fd019 — IndexerDownloadClientCheck |
| B.5c | 5.J12 | bdefeda4d — IndexerLongTermStatusCheck |
| B.5d | 5.J13 | 7876b7a7b — DownloadClientSortingCheck + reusable DC-status enumerator |
| B.5e | 5.J14 | a463a0e03 — DownloadClientRemovesCompletedDownloadsCheck |
| B.5f | 5.J14 | a463a0e03 — DownloadClientRootFolderCheck |
| B.5g | 5.J18 | 03c4a219f — ImportMechanismCheck |
| B.7 | 5.J4 | 23e5b0a94 — REJECTED: all GET-by-id endpoints already wired |
| B.8 | 5.J9 | 72a579cec — REJECTED: read endpoints already wired; one audit URL misread |
| B.9 | 5.J17 | bc0c30f8a — Forms (cookie) + External (bypass) auth modes |
| B.10 | 5.J7 | 7923938c7 — REJECTED: first-run seed already wired |
| B.11 | 5.J5 | 23e5b0a94 — Grabbed handler sources ReleaseType from ParsedEpisodeInfo |
| B.12 | 5.J6 | 23e5b0a94 — Sonarr__Section__Key env-var overrides |
| B.2 (partial) | 5.J20 | 084a26f5f — qualityprofile + customformat broadcasts wired |
| C.1 | 5.J15 | beba89780 — Cache-Control/Pragma/Expires/Vary middleware |
| C.2 | 5.J15 | beba89780 — internal/timefmt.ISO8601 wrapper |
| C.3 | 5.J15 | beba89780 — backup.go:368 sort.SliceStable |
| C.4 | 5.J16 | 02dce6dde — PID file + sd_notify primitives |
| C.5 | 5.J16 | 02dce6dde — base-href injection middleware |
| C.6 | 5.J16 | 02dce6dde — RotatingWriter log-rotation primitive |

WebSocket regression from the C.1 wrapper fixed in 5.J21 (`7b64f46fd`). STATUS.md close-out summary in 5.J22 (`aabe4de63`).

---

# Notable follow-ups surfaced during close-out (not in original audit)

These are real findings produced by the audit-fix work itself; they're tracked here so they don't get lost.

- **B.11 wire-shape divergence.** All 5 history handlers (Grabbed/Imported/FileDeleted/FileRenamed/DownloadIgnored) emit `Data["ReleaseType"]` as int-string ("2") while C# `HistoryService.cs:180,233,297,330,360` calls `.ToString()` and emits the PascalCase enum name ("MultiEpisode"). Worth one sweep if byte-equal contract matters for history-data.
- **Contract-replay baseline.** 9 known-failing goldens in CI (per B.1 baseline) are mostly environment-dependent C# recording artifacts. Lowering the baseline means expanding corpus `redact_paths` or re-recording against a clean C# boot.
- **JSON_CONVENTIONS.md inaccuracy.** Doc asserts C# emits nullable as `null`; in reality `WhenWritingNull` omits. Doc rewrite would prevent future sessions from repeating the A.1 misreading.
- **DC checks need IsLocalhost detection per impl.** The `ClientInfo.IsLocalhost` field is currently populated by each concrete DC's GetStatus — if a new DC lands without setting it, `ImportMechanismCheck` will misclassify. Worth a compile-time check or a default-detector based on the request host.

---

# What this audit did NOT cover

(Carried from the original audit. Most addressable by Phase 6 shadow traffic rather than static review.)

- **SignalR payload shapes per event family.** B.2 wires the broadcasts but doesn't byte-compare emitted SignalR payloads against C# `*EventHub` messages.
- **Per-provider settings JSON for unported providers.** Round 1 spot-checked qBittorrent / Deluge / SABnzbd / Discord / Email / Newznab. The 60+ remaining provider Settings classes need confirmation slice-by-slice as they land (B.6).
- **Behavior under load and concurrency.** No race detector run against a real workload. Best addressed once Phase 6 shadow traffic is up.
- **Frontend ↔ backend integration smoke.** The React UI lives at `frontend/` and wasn't exercised against the Go binary. Manual smoke checklist in `PHASE_6_SCOPE.md §6.2`.
- **`internal/disk`, `internal/auth`, `internal/repo` test coverage.** Flagged in the original `TEST_QUALITY_AUDIT.md`; not deepened.
- **Per-handler error path coverage.** Verified the envelope shape + status-code mapping; didn't enumerate every error condition per handler.
- **Logging output format and field names.** C# uses NLog with specific field conventions; Go uses slog — wire-format of log lines not compared.
- **Update / self-update flow** (`update-script.sh`, `internal/update/` if present). The B.5 UpdateCheck above touches this.
- **Frontend API surface vs Go-implemented routes.** No exhaustive cross-reference; PRs that wire missing endpoints surface in user reports.
