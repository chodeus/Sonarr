# Audit Fix Backlog — for the porting session

**Generated:** 2026-05-20
**By:** External review session (separate from the porting Claude that maintains `STATUS.md`)
**Pairs with:** `TEST_QUALITY_AUDIT.md`, `PORT_ACCURACY_AUDIT.md` (deeper findings; this file is the actionable backlog distilled from both).

---

## How to use this doc

Items are grouped into three tiers (`A.*` critical, `B.*` important, `C.*` hygiene), then a fourth section (`✓`) of things that are **verified correct — do not rework**.

Each item gives: file:line citations, what's wrong, what to do, and how to verify. Items are written to be self-contained — pick one, fix it, mark it done, delete from this file (or replace with a one-line "Closed in slice X.Y" entry).

Suggested execution order is by tier. Within a tier, items are independent unless an explicit blocker is noted.

**Meta-finding before anything else:** `PHASE_5_AUDIT.md` (dated 2026-05-18) is stale. Several items it lists as critical gaps are now closed (write endpoints, error envelope, qBittorrent/Deluge/NZBGet implementations). If the porting session is using it as a current map, decisions are being made against an outdated picture. Consider striking the closed items or adding a "snapshot — see STATUS.md and this file" header to it.

---

# Tier A — Critical

## A.1 — REJECTED: audit was wrong about C# behavior

**Rejected in slice 5.J1.**

**Reason:** The audit asserts "C# emits explicit `null` / `""` / `false`, Go currently OMITS the key." This is empirically false. C# Sonarr configures System.Text.Json globally with `DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull` at [`src/NzbDrone.Common/Serializer/System.Text.Json/STJson.cs:27`](../src/NzbDrone.Common/Serializer/System.Text.Json/STJson.cs), wired into the MVC pipeline at [`src/NzbDrone.Host/Startup.cs:107,113`](../src/NzbDrone.Host/Startup.cs). C# OMITS null / nullable-reference-null properties — exactly matching Go's `omitempty` semantic for nullable types.

**Evidence from recorded goldens** (`test/contract/golden/v3-empty-state/`, recorded against C# Sonarr 10.0.0.26712):

- `v3.qualityprofile.golden.json` — `QualityProfileItemResource` for "Unknown" quality omits `id` and `name`; group entries omit `quality`; "Raw-HD" item omits `maxSize`. Current Go shape with `omitempty` matches.
- `v3.indexer.schema.golden.json` — `FieldDef` for "API Key" field omits `helpText`, `value`, `unit`, `selectOptions`, etc. Go shape matches.
- `v3.command.golden.json` — `CommandResource` omits `message`, `clientUserAgent`, `exception` when null. Go shape matches.

Dropping `omitempty` would have Go EMIT keys C# does not, breaking byte-equality in the opposite direction.

**Caveats worth flagging for a future review (narrow real concerns the broad audit missed):**

1. `internal/api/v3/filesystem.go:23` — `Size int64 \`json:"size,omitempty"\``. C# `FileSystemModel.Size` is `long` (non-nullable). C# emits `0`; Go omits. Dormant today because the empty-state corpus has no filesystem records; live filesystem endpoints could hit it.
2. `JSON_CONVENTIONS.md` is itself incorrect about C# behavior — it describes the System.Text.Json **default** posture, not Sonarr's actual `WhenWritingNull` override. The "Field-by-field rules" table should say nullable → `*T` WITH `omitempty` (omitted when nil; emitted when set). Worth correcting the doc so future slices don't repeat the same misreading.

These two items are tracked here for whoever next touches the JSON-shape policy; neither warrants a Phase-5 sweep.

---

## A.2 — CLOSED in slice 5.J2 (sort.Slice → sort.SliceStable at six flagged sites)

Sites switched to stable sort: `internal/customformats/calculate.go:140`, `internal/healthcheck/indexer_status.go:190`, `internal/healthcheck/importlist_status.go:195`, `internal/healthcheck/removed_series.go:135`, `internal/healthcheck/importlist_rootfolder.go:162,179`, `internal/scenemappings/service.go:505`. (Line numbers shifted since the audit; `grep -n` re-confirmed each pattern.) `backup.go:368` left for C.3. `golangci-lint run` 0 issues; `go test -race -count=1` green for the three touched packages.

---

## A.3 — CLOSED in slice 5.J1 (PHASE_5_AUDIT.md prefaced with supersession banner)

---

## A.4 — CLOSED in slice 5.J3 (localhost bypass + access_token tightening)

Wired in `internal/host/auth.go` + new `internal/host/local_ip.go`:

- New optional `host.AuthPolicy` interface (satisfied by `*configfile.Provider` — no wiring change at the call site). When the resolver also implements it and `AuthenticationRequired = DisabledForLocalAddresses`, the middleware bypasses auth for local peers — mirroring `Sonarr.Http.Authentication.UiAuthorizationHandler.HandleRequirementAsync` at `src/Sonarr.Http/Authentication/UiAuthorizationHandler.cs:25-41`.
- `isLocalAddress` covers loopback, IPv4 private (10/8, 172.16-31/12, 192.168/16) + link-local (169.254/16), IPv6 link-local / unique-local / site-local. Mirrors `src/NzbDrone.Common/Extensions/IpAddressExtensions.cs:8-37`.
- `isCgnatAddress` (100.64.0.0/10) — bypass extended to CGNAT only when `TrustCgnatIpAddresses` is set.
- `?access_token=` query param now path-gated: honored ONLY on /signalr/* routes (the C# SignalR auth scheme), rejected on REST paths. /signalr/* also no longer accepts the REST scheme's `apikey=` query name. Mirrors the dual-scheme registration at `src/Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:57-66`.

Tests: `internal/host/auth_test.go` extended with `TestRequireAPIKeyLocalBypass` (11 IP-class branches) + `TestRequireAPIKeyValidKeyOverridesBypass`; `internal/host/local_ip_test.go` NEW for the helpers. `go test -race -count=1 ./internal/host/...` green; `golangci-lint run ./...` 0 issues.

`Forms` and `External` auth modes remain unimplemented — tracked separately as B.9. The audit's "Verification" curl-from-loopback example is satisfied by `TestRequireAPIKeyLocalBypass/loopback_v4_bypass`.

---

# Tier B — Important

## B.1 — CLOSED in slice 5.J8 (contract-replay job wired with drift baseline)

New `contract-replay` job in `.github/workflows/build_go.yml`:

1. `needs: vet-lint-test` so it only runs once the base tests pass.
2. Builds the Go binary with the same `go build -trimpath` invocation the existing build matrix uses.
3. Boots `/tmp/sonarr -data=/tmp/sonarr-data -nobrowser` in the background with `Sonarr__Auth__ApiKey=ci-replay-test-key` set via env var (B.12 wiring — no config.xml parsing needed).
4. Waits up to 30s for `/ping` to respond, then runs `go test ./test/contract/replayer/...` with the recorded goldens.
5. Counts "contract drift" lines from the test output. Fails if drift count > 9 (the current baseline) — a regression guard. Warns if drift count is BELOW 9 (asking the operator to lower the baseline).

**Baseline-failure inventory (9 entries)** — captured locally before wiring CI:

- `v3.system.status` — `isUserInteractive` differs (env: TTY vs CI headless).
- `v3.system.routes` — C# recording is the full Swashbuckle route table (~336 KB); Go's port has a different surface.
- `v3.system.health` — C# emitted DownloadClientCheck + IndexerSearchCheck; Go emits SkyHookCheck + ApiKeyValidationCheck. Different default healthcheck set.
- `v3.system.tasks` — different scheduled-task default set + the last-execution timestamps weren't redacted.
- `v3.config.host` — C# recording was at port 9898 (non-default); Go boots at 8989. Plus `analyticsEnabled` + `launchBrowser` defaults differ.
- `v3.qualitydefinition` — WEBDL-480p / WEBRip-480p (etc.) IDs are swapped — seed insertion order differs from C#. Worth investigating: either a real bug or normalize the corpus to sort by quality.id.
- `v3.episodefile.list` — Go returns 200 with `[]`; C# returns 404 with a stack trace because the seriesId=1 filter doesn't match. Real wire divergence to fix.
- `v3.diskspace` — C# recording captures the real host's mounts (9 entries); Go on CI ephemeral disk has different mounts.
- `v3.log` — C# recording has 7 historical log records from the recording host; fresh Go boot has 0.

Most of these are environment-dependent (the C# binary was recorded on a particular operator's machine). The corpus normalize rules don't redact host-specific fields like `port`, `isUserInteractive`, disk mounts, or log records. A separate sweep could either:

- Add `redact_paths` entries for the env-dependent values (lower baseline, keep current goldens).
- Re-record goldens against a clean C# boot in a Docker container (defeats some of the value but levels the playing field).
- Tighten the few genuine divergences (`v3.episodefile.list` 404 shape, `v3.qualitydefinition` ID ordering).

The CI job catches NEW drift now even with the baseline budget — if any of the currently-passing 44 entries breaks, drift will tick to 10 and the job will fail.

---

## B.2 — SignalR broadcasts not fired from most write handlers

**Severity:** 🟡 Important — frontend won't update live
**Source:** New finding

**Problem:** Only 8 v3 handler files emit `signalr.FireAndLog` after writes: `importlist`, `series`, `notification`, `indexer`, `downloadclient`, `rootfolder`, `metadata`, `seasonpass`. The other ~62 write endpoints (across `episode`, `episodefile`, `queue`, `blocklist`, `autotagging`, `command`, `qualityprofile`, `customfilter`, `customformat`, `delayprofile`, `releaseprofile`, `tag`, `remotepathmap`, `config_*`) return success but don't broadcast. React UI won't refresh until the user manually reloads.

`PHASE_5_AUDIT.md §"SignalR broadcast unwired"` flagged this generically as "gated by writes." Now that writes exist, the gate is open — but only ~12% of write paths broadcast.

**Fix shape:** for each write handler, identify the equivalent C# `IHandle<ResourceChangeEvent>` or `_signalRBroadcaster.BroadcastMessage(...)` site in `src/Sonarr.Api.V3/...Controller.cs` or its service layer, and wire the equivalent Go broadcast after the DB write succeeds. Use the existing `signalr.FireAndLog` pattern from `importlist.go` / `series.go` as the template.

**Acceptance:** every POST/PUT/DELETE handler in `internal/api/v3/` fires the corresponding broadcast on its happy path; nothing on the error path. Optional: a vet-style lint that warns when a write handler imports `signalr` but doesn't call it.

---

## B.3 — Scheduled-task handlers that are stubs

**Severity:** 🟡 Important — runtime background work doesn't happen
**Source:** New finding

**Problem:** Three scheduled-task command handlers in `internal/commands/handlers/` are registered with real `RegisterHandler` entries but their bodies log "not yet implemented" or equivalent and return OK without doing the work:

- `RefreshSeries` (`refreshseries.go`) — log-only. C# refreshes series metadata via SkyHook + updates DB. Scheduled every 12h.
- `RefreshMonitoredDownloads` (`refreshmonitoreddownloads.go`) — log-only. C# polls active download clients and updates queue state. Scheduled every 1m.
- `CheckForFinishedDownloads` (`checkforfinisheddownloads.go`) — log-only. C# triggers import on completed downloads. Scheduled every 1m.

**Plus two missing entirely:**

- `MissingEpisodeSearchCommand` — periodic search for missing-monitored episodes.
- `ApplicationCheckUpdateCommand` — periodic update-availability poll.

**Why it matters:** the port LOOKS operational (commands fire on cron, return OK) but no actual work happens. Users adding a series won't see metadata refresh, won't see download progress updates, won't see auto-imports.

**Fix shape:** these are not mechanical — each requires real domain logic. RefreshSeries depends on the SkyHook proxy (queue [M3] per STATUS.md), so it may already be partially blocked. Triage:
1. Confirm what's actually blocked vs what can be implemented now.
2. For each, port the C# `Execute(<Command>)` body using existing services.
3. Add unit tests against a seeded DB.

**Verification:** integration test that boots the scheduler, advances time, asserts the handler did the work (DB state changed, events fired).

---

## B.4 — `/api/v3/release` search is fully stubbed

**Severity:** 🟡 Important — core user feature ("Search Releases") is non-functional
**Source:** New finding

**Problem:** `internal/api/v3/release.go:24-40` validates query params (seriesId/episodeId/seasonNumber) but always returns hardcoded `[]`. Comment at line 18-21 says "orchestration driver hasn't been assembled yet." The indexer search service exists (slices 3.AK/3.AL/4.A per STATUS.md) but isn't wired into this handler.

**Fix shape:** wire `internal/indexersearch.Service` (or whatever the search orchestrator is named) into the handler. Accept query params → dispatch parallel queries to enabled indexers → aggregate + score via decision engine → return ranked `ReleaseResource[]`. The C# reference is `src/Sonarr.Api.V3/Indexers/ReleaseController.cs`.

**Verification:** unit test with a fake indexer registered; assert the handler returns the fake's results with decision-engine scoring applied.

---

## B.5 — REAL (verified 2026-05-20 in slice 5.J9; partial progress slice 5.J10+)

**Missing healthchecks (4 remaining; 6 closed in 5.J10–J14):**

- ~~IndexerJackettAllCheck~~ — CLOSED in slice 5.J10. `internal/healthcheck/indexer_jackett_all.go` + test ship a warning when an enabled Torznab indexer's `BaseUrl` or `ApiPath` contains `/torznab/all` or `/api/v2.0/indexers/all/results/torznab`. Wired in `cmd/sonarr/main.go` alongside the other indexer checks.
- ~~IndexerDownloadClientCheck~~ — CLOSED in slice 5.J11. `internal/healthcheck/indexer_downloadclient.go` + test warn when an enabled indexer pins a `DownloadClientId > 0` that doesn't match any enabled download client. Wired in `cmd/sonarr/main.go`. Uses a new narrow `DownloadClientRowLister` interface (the pre-existing `DownloadClientLister` is the count-only surface for DownloadClientCheck — kept distinct).
- ~~IndexerLongTermStatusCheck~~ — CLOSED in slice 5.J12. `internal/healthcheck/indexer_long_term_status.go` + test mirror IndexerStatusCheck but flip the InitialFailure time predicate — flags indexers blocked LONGER than `IndexerRecentFailureWindow` (6h). Reuses the same `EnabledIndexerLister` + `IndexerStatusBlockedLister` interfaces. Wired in `cmd/sonarr/main.go`.
- ~~DownloadClientSortingCheck~~ — CLOSED in slice 5.J13. `internal/healthcheck/downloadclient_sorting.go` + test warn on the first enabled DC reporting a non-empty SortingMode. Also introduced the reusable `DownloadClientStatusEnumerator` interface + `downloadClientStatusEnumeratorAdapter` (late-bound deps via SetDeps so the adapter can register at healthSvc-wire time before httpClient + downloadclients.Dependencies are in scope).
- ~~DownloadClientRemovesCompletedCheck~~ — CLOSED in slice 5.J14. `internal/healthcheck/downloadclient_removes_completed.go` + test warn when an enabled DC has the self-remove-completed feature on. Reuses the B.5d enumerator.
- ~~DownloadClientRootFolderCheck~~ — CLOSED in slice 5.J14. `internal/healthcheck/downloadclient_root_folder.go` + test warn when an enabled DC's output folder equals or is a child of any configured root folder. Reuses the B.5d enumerator + the existing root-folder-path adapter; uses `ospath.IsParent` for the platform-aware ancestry check.
- ImportMechanismCheck — folder watching vs completed handling
- MountCheck — mount point exists and is writable (NEEDS `disk.GetMount` port — IsReadOnly mount detection is platform-specific syscall work; this is genuinely larger than a single slice)
- PackageGlobalMessageCheck — package maintainer messaging hook (NEEDS `DeploymentInfoProvider` port — reads `<bin>/../package_info` key/value file shipped by package maintainers)
- RemotePathMappingCheck — configured remote paths reachable (largest at 387 LOC, complex docker-detection logic)
- UpdateCheck — update folder writable (needs disk capability checks + version-string compare)

**Partial — ProxyCheck (1):** `internal/healthcheck/proxy.go` is fully ported (resolve + ping + status-400 branching) but production wiring at `cmd/sonarr/healthcheck_wiring.go:283-310` injects `proxyHostConfigStub` (always returns ProxyEnabled=false). The check short-circuits to OK in every install. Genuine partial — finishing it needs the C# HostConfig keyvalues-backed `IConfigService` to land.

**SystemTimeCheck — not a stub (audit was wrong):** `internal/healthcheck/system_time.go` ports the full `services.sonarr.tv/v1/time` fetch + drift threshold logic. Functional today. Audit's "verify it returns correct severity when out of sync" is already covered by the existing tests.

**Fix shape:** each missing check is a single Go file matching the existing pattern (`internal/healthcheck/<name>.go`) plus registration in `cmd/sonarr/healthcheck_wiring.go`. Reference: the corresponding C# file under `src/NzbDrone.Core/HealthCheck/Checks/`. Prioritize by ops-impact: MountCheck and UpdateCheck first.

**Verification:** new healthcheck appears in `GET /api/v3/health` response. Unit test for each.

---

## B.6 — Stub indexers (7) and stub download clients (14)

**Severity:** 🟡 Important — limits which user setups the port supports
**Source:** Carried from `PHASE_5_AUDIT.md`, count updated 2026-05-20

**Indexers missing (7):** BroadcastheNet, Fanzub, FileList, HDBits, IPTorrents, Nyaa, Torrentleech. `internal/indexers/registry.go:23-30` documents "zero concrete impls register."

**Download clients missing (~14):** Aria2, TorrentDownloadStation, UsenetDownloadStation, Flood, TorrentFreeboxDownload, Hadouken, NzbVortex, Pneumatic, RQBit, rTorrent, TorrentBlackhole, TriblerDownloadClient, UsenetBlackhole, uTorrent, Vuze. (qBittorrent, Deluge, NZBGet, SABnzbd, Transmission landed since `PHASE_5_AUDIT.md` was written.)

**Fix shape:** each is a per-provider implementation. Priority order per `PHASE_5_AUDIT.md §Tier 2` — rTorrent and uTorrent are highest-traffic among remaining. Use qBittorrent/Deluge as templates.

**Verification:** each provider registers in its registry, schema endpoint returns its definition, full CUD round-trips, behavior tests pass (Download → GetItems → MarkAsImported).

---

## B.7 — REJECTED: audit list is stale; all GET-by-id endpoints already wired

**Rejected in slice 5.J4.**

Verified against `cmd/sonarr/host_wiring.go` GET-route table — all 17 resources the audit claims missing already have a `GET /api/v3/<resource>/{id}` handler:

`autotagging`, `command`, `customfilter`, `customformat`, `delayprofile`, `downloadclient`, `episode`, `episodefile`, `importlist`, `indexer`, `metadata`, `notification`, `qualitydefinition`, `qualityprofile`, `releaseprofile`, `remotepathmapping`, `rootfolder`, `series`, `tag`, `tag/detail`, `system/task`, `wanted/cutoff`, `wanted/missing` — and the related `config/*` family by-id routes are also wired.

The two remaining audit entries (`queue`, `blocklist`) are NOT exposed in C# either:

- `src/Sonarr.Api.V3/Queue/QueueController.cs:64-73` — `GetResourceById` is marked `[NonAction]` and throws `NotImplementedException`. The base inheritance is suppressed.
- `src/Sonarr.Api.V3/Blocklist/BlocklistController.cs` — only `[HttpGet]` for the paged list, `[RestDeleteById]`, and `bulk` delete. No GET-by-id.

So B.7 in its written form is already fully closed. Audit was stale (probably built against the same pre-write-path snapshot that produced the rest of `PHASE_5_AUDIT.md`).

---

## B.8 — REJECTED: audit list is stale; all endpoints already wired

**Rejected in slice 5.J9.**

Verified each route against `cmd/sonarr/host_wiring.go`:

- ✓ `GET /api/v3/calendar/feed` (Sonarr.ics) — wired
- ✓ `GET /api/v3/diskspace` — wired
- ✓ `GET /api/v3/filesystem` (+ `/type`, `/mediafiles`) — wired
- ✓ `GET /api/v3/log` (+ `/file`, `/file/{filename}`, `/file/update`, `/file/update/{filename}`) — wired
- ✓ `GET /api/v3/manualimport` — wired
- ✓ `GET /api/v3/parse` — wired
- ✓ `GET /api/v3/rename` (+ `/bulk`) — wired
- ✓ `GET /api/v3/series/lookup` — wired
- ✓ `GET /api/v3/series/{id}/folder` — wired (the audit's `/api/v3/series/lookup/{id}/folder` is a misread of the C# route; `SeriesFolderController.cs:8,20` uses `[V3ApiController("series")] + [HttpGet("{id}/folder")]` → `/api/v3/series/{id}/folder`, NOT under `lookup`)
- ✓ `GET /api/v3/system/backup` (list) — wired
- ✓ `GET /api/v3/system/task` (+ by-id) — wired
- ✓ `GET /api/v3/mediacover/{seriesId}/{filename}` — wired
- ✓ `GET /api/v3/indexerflag` — wired

`GET /api/v3/release` (the search endpoint) is the only one still stubbed — but that's tracked separately as B.4.

---

## B.9 — Forms and External authentication modes not implemented

**Severity:** 🟡 Important — limits deployment topologies
**Source:** Round-2 audit

**Problem:** `wellknown.go:272-304` recognizes all four C# `AuthenticationMethod` values (`None`, `Basic`, `Forms`, `External`) and includes a legacy "Basic"→"Forms" upgrade path, but only `None` and the API-key path are enforced. Users running C# with `Forms` (cookie-based login form, served at `/login`) or `External` (trusted reverse-proxy headers like `X-Forwarded-User`) cannot switch to Go without changing their auth topology.

**Fix shape:**
- **Forms:** add `/login` POST endpoint that issues a session cookie; middleware that accepts the cookie in lieu of API key. Cookie name and signing should match C# Identity defaults (or use a documented Go-port equivalent — but matching is cheaper for migration).
- **External:** middleware that accepts `X-Forwarded-User` (or whatever header C# trusts) when configured. Document the trusted-proxy IP list to prevent header spoofing.

**Verification:** end-to-end test for each mode against the existing React login flow. Plus security test: spoofed `X-Forwarded-User` from an untrusted IP is rejected in External mode.

---

## B.10 — REJECTED: audit was wrong; first-run seeding already wired

**Rejected in slice 5.J7.**

All three pieces the audit claims missing are actually seeded today:

- **Default QualityProfile + Items + FormatItems** — seeded by `qualityProfilesSvc.SeedDefaults(ctx)` at `cmd/sonarr/main.go:1184`, ported from C# `QualityProfileService.Handle(ApplicationStartedEvent)` at `QualityProfileService.cs:91-157`. Tests at `internal/qualityprofiles/defaults_test.go:180-205` cover the empty-DB case (inserts six defaults: Any / SD / HD-720p / HD-1080p / Ultra-HD / HD-720p-or-1080p) and the no-op-when-any-profile-exists guard.
- **QualityDefinition rows** — seeded by `qualityDefSvc.EnsureDefaults(ctx)` at `cmd/sonarr/main.go:565`, ported from C# `QualityDefinitionService.InsertMissingDefinitions` at `:68-96`. Idempotent insert-missing + delete-orphan pass over `quality.DefaultDefinitions()`.
- **Config table key-value pairs** — neither C# nor Go pre-seeds these. Both use a default-on-read pattern (C# `ConfigService.GetValue(key, default, persist=false)` at `ConfigService.cs:454-474`; Go `config.Provider.getString(key, def)` at `internal/config/provider.go:128-136`). Config rows materialise lazily on writes, exactly as in C#. Audit conflated "rows absent in `Config` table" with "default value not available to consumers" — the second is what matters, and it works.
- **Default DelayProfile** — seeded by migration `00070_delay_profile.sql` (the audit acknowledges this).

So B.10 is closed by code that pre-dates the audit. The audit likely sampled an early-Phase-1 snapshot before the bootstrap wiring landed.

---

## B.11 — CLOSED in slice 5.J5 (Grabbed handler now sources ReleaseType from ParsedEpisodeInfo)

Go's `parser.ParsedEpisodeInfo` already exposes `ReleaseType()` as a derived getter (not a field, but same semantic) at `internal/parser/model.go:114-125`. The audit comment that the field is missing was technically correct but functionally moot. `internal/historyhandlers/grabbed.go:255` now sources from `subject.ParsedEpisodeInfo.ReleaseType()` and emits via `strconv.Itoa`, matching the existing convention used by the four sister handlers (imported, file_deleted, file_renamed, download_ignored).

New test coverage in `grabbed_test.go`: `TestHandleEpisodeGrabbed_ReleaseTypePopulated` (single/multi/seasonpack branches), `TestHandleEpisodeGrabbed_ReleaseTypeNilPEI` (nil guard → "0").

**Follow-up worth flagging:** all 5 history handlers emit the int-string form (e.g. "2") while C# `HistoryService.cs:180,233,297,330,360` calls `ReleaseType.ToString()` and emits the PascalCase enum name (e.g. "MultiEpisode"). This is a wire-shape divergence across the whole family — fixing it requires reworking 5 handlers + their tests, plus deciding whether to migrate any rows written by Go to date. Audit's verification step (`"SeasonPack"` not `"Unknown"`) actually expected the C# form. Tracked in this note for whoever sweeps the history-data wire shape next.

---

## B.12 — CLOSED in slice 5.J6 (Sonarr__Section__Key env-var overrides wired)

C# Sonarr honours `Sonarr__<Section>__<Key>` env vars via ASP.NET's `AddEnvironmentVariables()` at `src/NzbDrone.Host/Bootstrap.cs:266`. The audit's `SONARR__PORT=8989` form is actually not the shape C# accepts — C# expects the section-namespaced form (`Sonarr__Server__Port`), where the section names come from the four Options classes at `src/NzbDrone.Common/Options/{App,Auth,Server,Update}Options.cs`.

The existing `configfile.Overrides` struct already had every field that needed wiring; only the env-var parser was missing. New `internal/configfile/envvars.go` ships `ParseOverridesFromEnv(env []string) Overrides` covering Server (Port/SslPort/BindAddress/UrlBase/EnableSsl), Auth (ApiKey/Method/Required), App (InstanceName/LaunchBrowser), Update (Branch), plus the top-level `Sonarr__LogLevel`. Section/key match case-insensitively to mirror ASP.NET config posture. Unparseable int/bool values are dropped silently (same as C# `config.GetValue<int?>`).

`cmd/sonarr/main.go:196` now passes `configfile.ParseOverridesFromEnv(os.Environ())` instead of an empty struct. The existing `persist=false` semantic on Provider overrides means env-var-only keys never get written to config.xml.

Tests: `internal/configfile/envvars_test.go` covers Server/Auth/App+Update/LogLevel/CaseInsensitive/IgnoresUnrelated/InvalidInts/MissingEquals. `golangci-lint run` 0 issues, `go test -race -count=1 ./internal/configfile/... ./cmd/sonarr/...` green.

---

# Tier C — Hygiene

## C.1 — CLOSED in slice 5.J15 (cache-disable middleware on every API route)

New `disableCacheHeaders` middleware in `internal/host/cache_headers.go` wraps a `cacheHeaderWriter` around the response so the four "do not cache" headers (`Cache-Control: no-cache, no-store`, `Pragma: no-cache`, `Expires: -1`, `Vary: Accept-Encoding`) fire exactly once when the response is committed, plus `Last-Modified` is stripped. Wrapping the ResponseWriter mirrors the C# `CacheHeaderMiddleware` posture where the headers are applied AFTER the inner action so downstream `Set("Last-Modified", ...)` is overridden.

Applied to every API route (`/api`, `/ping`, all `WithRoutes` entries) in `internal/host/server.go:buildMux`, except `/api/v3/mediacover/*` which skips the middleware (matches C# `CacheableSpecification` EnableCache behaviour on that prefix — the React UI long-caches series posters). The static-frontend mount also bypasses cache headers so React bundle filename-hash cache busting still works.

---

## C.2 — CLOSED in slice 5.J15 (ISO8601 wrapper in new internal/timefmt package)

New `internal/timefmt` package ships `ISO8601` — a `time.Time` wrapper whose `MarshalJSON` always emits second-precision UTC (`"YYYY-MM-DDTHH:MM:SSZ"`). `UnmarshalJSON` accepts both RFC3339Nano and the second form, truncating on read. `timefmt.Now()` returns a second-truncated UTC timestamp for sites that previously called `time.Now()` directly.

Existing call sites haven't been migrated yet (the audit acknowledged this is dormant today — every observed golden timestamp has zero subseconds because the values are DB-sourced). The wrapper is in place so that whoever next adds a `time.Now()`-sourced wire-emission path can reach for it without re-discovering the issue.

---

## C.3 — CLOSED in slice 5.J15 (`backup.go:368` switched to sort.SliceStable)

`internal/backup/backup.go:368` (line moved since the audit was written) now uses `sort.SliceStable` so same-timestamp backups preserve insertion order. C# `OrderBy(x => x.Time)` is stable, so the Go-port now matches.

---

## C.4 — CLOSED in slice 5.J16 (PID file + sd_notify primitives)

New `internal/lifecycle` helpers: `WritePIDFile(path)` writes the running PID + creates the parent directory; `RemovePIDFile(path)` is the shutdown counterpart; `SDNotifyReady()` / `SDNotifyStopping()` write `READY=1` / `STOPPING=1` to `$NOTIFY_SOCKET` when set (no-op otherwise). The notify implementation writes a unix datagram directly so the go-systemd module isn't pulled in. Abstract-namespace sockets (Linux `@`-prefixed paths) handled correctly.

Wiring into `cmd/sonarr/main.go` (choose pid path from a future config key, call `WritePIDFile` after listener binds, `SDNotifyReady` immediately after) deferred to the same Phase-6 deployment-polish slice that picks the log destination. The primitives ship today so the wiring work is a 4-line edit when the time comes.

---

## C.5 — CLOSED in slice 5.J16 (base-href injection wired into static handlers)

New `injectBaseHref(handler, urlBase)` middleware in `internal/host/base_href.go` buffers HTML responses, then either rewrites an existing `<base href="...">` tag OR injects one right after `<head>` (or prepends if no `<head>` present). Non-HTML responses (JS/CSS/JSON via Content-Type sniff) pass through on the fast path without buffering. urlBase is normalised to `/<value>/` shape — accepts `sonarr`, `/sonarr`, `/sonarr/`, `////sonarr////` and produces the same `<base href="/sonarr/">` output. Empty / root URL base is a pass-through.

Wired in `buildMux` around BOTH the disk-backed `staticHandler` (WithStaticDir) and the FS-backed `staticHandlerFS` (WithStaticFS) so the embedded-bundle path also rewrites.

---

## C.6 — CLOSED in slice 5.J16 (in-tree RotatingWriter primitive)

New `instrumentation.RotatingWriter` (`internal/instrumentation/rotating_writer.go`) — an `io.WriteCloser` wrapping an on-disk log file that rolls over by renaming to numbered backups (`.1`, `.2`, …) when the current file size would exceed `MaxBytes`. `MaxBackups` controls retention; defaults are 10 MiB / 5 backups (mirrors NLog FileTarget). Thread-safe via internal mutex. Rotation prunes oldest archives in-place. Self-contained — no external dep (the audit suggested lumberjack but adopting a new module is its own go.mod gate; the in-tree impl is ~150 LOC including tests).

Wiring `cmd/sonarr/main.go` to route the slog handler at this writer (vs the current `os.Stderr`) deferred to the same Phase-6 deployment-polish slice that picks the log path + reads the `LogRotate` config key. The primitive ships today so the wiring is a 5-line edit when it lands.

---

# ✓ Verified correct — do not rework

This list exists to prevent the porting session from "fixing" things that already work. Each was checked during the 2026-05-20 audit.

| Concern | Status | Evidence |
|---|---|---|
| Migration parity (HEAD v230, SQLite + Postgres) | ✓ Exact | Both ports at version 230. Schema spot-checks (Series, Episodes, EpisodeFiles, Indexers, Notifications) match column-for-column. Goose `goose_db_version` vs FluentMigrator `VersionInfo` intentionally separate, won't collide. Migration test harness at `internal/datastore/migrate_test.go:40-300` runs to v1, to HEAD, and idempotency check on both engines. |
| Error envelope shape | ✓ Fixed | `internal/httperr/httperr.go:103-178` emits the C# `ErrorModel` (5xx/4xx) and FluentValidation 400 array. Zero `http.Error` calls remain under `internal/api/v3/`. |
| Nil-slice → `[]` (vs `null`) | ✓ Compliant | Verified on `series.list`, `tag.list`, `qualityprofile.list` against goldens. |
| Status code mapping 404 / 400 / 409 | ✓ Compliant | `httperr.NotFound` / `BadRequest` / `Conflict` helpers used uniformly. |
| Regex translation (C# → RE2) | ✓ Handled | `internal/parser/normalize.go:9-62` explicitly rewrites C# lookbehinds rather than transliterating; named groups preserved. |
| Decision-engine sort stability | ✓ Correct | `internal/decisionengine/decisionmaker.go:71` uses `sort.SliceStable`. |
| DB nullable NULL→0 coercion | ✓ Correct | `internal/series/series.go:213-220` coerces `Year` and `QualityProfileID` from `sql.NullInt64.Valid`. |
| Time/timezone handling | ✓ Correct | `time.Now().UTC()` used in `internal/historyhandlers/*.go`; no local-clock misuse. |
| String comparison (byte-wise) | ✓ Correct | `internal/parser/parse_title.go:141-142` uses `strings.ToLower` + `strings.Contains`. |
| Notifications (25/25 implemented) | ✓ Confirmed | Discord/Email/Slack/Telegram/Plex/Trakt all have real proxy + settings + test code. |
| Metadata writers (4/4 implemented) | ✓ Confirmed | Plex, Wdtv, Roksbox, Xbmc all real. |
| qBittorrent / Deluge / NZBGet / SABnzbd / Transmission download clients | ✓ Implemented | ~2000 LOC each, full Download/GetItems/GetStatus/RemoveItem/MarkItemAsImported. |
| Provider settings JSON shape (qBit, Deluge, SAB, Discord, Email, Newznab) | ✓ Match | Field count, order, names, types, Advanced/Hidden flags all match C# `*Resource.cs` definitions. |
| `panic("not implemented")` / `panic("TODO")` markers | ✓ Zero | None anywhere under `internal/`. |
| Auth middleware uniformity (API-key path) | ✓ Pass | `internal/host/auth.go:38-53`; constant-time compare via `subtle.ConstantTimeCompare` at `internal/auth/apikey.go`. (BUT: see A.4 for missing localhost bypass and B.9 for Forms/External modes.) |
| HTTP method handling (auto-405) | ✓ Pass | Go 1.22+ ServeMux. |
| Sensitive-value logging | ✓ Pass | Per `PHASE_5_AUDIT.md`. |
| config.xml read/write parity | ✓ Pass | `internal/configfile/loader.go:144-251` reads, `:284-305` writes. Atomic temp+rename, element order preserved, type coercion matches C#. A C# config.xml loads cleanly and writes back unchanged. |
| AppData path resolution | ✓ Pass | `internal/appenv/appfolder.go:58-72` — Windows `%PROGRAMDATA%\Sonarr`, Unix `$XDG_CONFIG_HOME/Sonarr` or `$HOME/.config/Sonarr`. Matches C# `IAppFolderInfo`. |
| API key auto-generation on missing/empty | ✓ Pass | `internal/configfile/wellknown.go:63-84` — generates and writes back, mirrors C#. |
| URL base parsing/normalization | ✓ Pass | `internal/host/server.go:166-172` (`normaliseURLBase`) + `:298-324` (`stripURLBase`). Out-of-prefix → 404. (BUT: see C.5 for frontend `<base href>` injection still missing.) |
| Default DelayProfile seeded | ✓ Pass | Migration `00070_delay_profile.sql` matches C#. |
| Backup file format (cross-binary compatible) | ✓ Pass | `internal/backup/backup.go:295` — `sonarr_backup_v{V}_{date}.zip`; type folders `scheduled`/`manual`/`update` at `:254-258`. Filename regex at `:75` accepts both legacy `nzbdrone_backup_…` and current forms. C# and Go can restore each other's backups. |
| Backup restore endpoint | ✓ Implemented (contradicts stale PHASE_5_AUDIT) | `internal/api/v3/system_backup.go:102-133` for by-ID, `:181-230` for upload. Extracts ZIP, swaps DB to `DatabaseRestorePath()`, returns `{"restartRequired":true}`. |
| History `EventType` enum ordinals (all 8) | ✓ Match | `internal/history/service.go:22-57` vs C# `EpisodeHistory.cs:37-47`. Frozen wire protocol. |
| History `Data` shape for `Grabbed` (22 keys), `DownloadFolderImported` (10), `DownloadFailed` (7) | ✓ Match (except B.11) | `internal/historyhandlers/grabbed.go:189-261`, `imported.go:184-205`, `download_failed.go:72-200`. The only deviation is `ReleaseType="Unknown"` — see B.11. |
| `GET /api/v3/history` + `/since` + `/series` | ✓ Implemented | `internal/api/v3/history.go:50-216`. Filter set partially deferred. |
| `POST /api/v3/history/failed/{id}` | ✓ Implemented | `internal/api/v3/history.go:197-216`. |
| Quality detection from filename (`QualityParser` parity) | ✓ Match | `internal/quality/parse.go:23-438` vs C# `QualityParser.cs:68-598`. All 22 quality enum values present with stable IDs. 5-sample mental run-through (WEB-DL/HDTV/REMUX/PROPER/WEBRip) all produce matching outputs. |
| Language detection (RE2-rewrite of C# patterns) | ✓ Match | `internal/parser/language.go:1-211`. Negative lookaheads (e.g. `spa(?!\(Latino\))`) re-implemented via separate capture groups + post-match filter at `:100`. All 25 language tokens map correctly. |
| Release group extraction (`-GROUP`, `[GROUP]`) | ✓ Match | `internal/parser/release_group.go:15-555` ports C# `ReleaseGroupParser.cs:3-167`. |
| Episode matching — Standard / Daily / Anime / Scene | ✓ All four modes | `internal/parser/report_title.go` rules with C# line-citation comments. First-match-wins ordering preserved. |
| Custom format scoring on import (not just grab) | ✓ Implemented | `internal/episodeimport/local_format_calculator.go:81-162` — scores both renamed filename and original filename; specs gate on MinFormatScore. |
| File naming token expansion | ✓ Match | `internal/organizer/build.go` + `*_tokens.go`. Illegal-char sanitization at `cleanname.go:8-143` mirrors C# illegal-char set. |
| File move/copy/hardlink with cross-device fallback | ✓ Match | `internal/disk/transfer.go:27-145` — `MoveFile` atomic rename then copy+delete on EXDEV; `CopyFile` uses `O_EXCL`; `TryCreateHardLink` for HardLinkOrCopy. Permission bits preserved. |
| Graceful shutdown drain | ✓ Pass | `internal/host/server.go:135` — 5s drain matches Kestrel implicit default. SIGTERM handled in `cmd/sonarr/main.go:25`. |

---

# What this audit did NOT cover

Listing remaining blind spots after round 2:

- **SignalR payload shapes per event family.** Round 1 verified broadcasts are wired in 8 of ~70 write handlers; did not byte-compare emitted payloads against C# `*EventHub` messages, and did not enumerate every uncovered write path. B.2 lists the uncovered set generically.
- **Per-provider settings JSON for unported providers.** Round 1 spot-checked qBittorrent, Deluge, SABnzbd, Discord, Email, Newznab. The remaining 60+ provider Settings classes need confirmation slice-by-slice as they land.
- **Behavior under load and concurrency.** No race detector run, no fuzz, no replay against a real workload. Best addressed once Phase 6 shadow traffic is up.
- **Frontend ↔ backend integration smoke.** The React UI lives at `frontend/` and was not exercised against the Go binary. Best addressed by the manual smoke checklist in `PHASE_6_SCOPE.md §6.2`.
- **`internal/disk`, `internal/auth`, `internal/repo` test coverage.** Flagged in `TEST_QUALITY_AUDIT.md §5` as under-tested; not deepened here. (Round 2 did verify `internal/auth.EqualAPIKey` uses constant-time compare.)
- **Per-handler error path coverage.** Verified the envelope shape and status-code mapping; did not enumerate which error conditions each handler actually emits across every edge case.
- **Logging output format and field names.** C# uses NLog with specific field conventions; Go uses slog — wire-format of log lines was not compared. Operators parsing logs across the cutover will see different shapes.
- **Update / self-update flow** (`update-script.sh`, `internal/update/` if present). Round 1 noted `UpdateCheck` healthcheck is missing (B.5); the update mechanism itself wasn't audited.
- **Frontend API surface vs Go-implemented routes.** Did not cross-reference every endpoint the React UI calls against the Go route table to find any UI feature that silently calls a missing endpoint.

A bigger audit pass would tackle these — but most are better surfaced by Phase 6's shadow-traffic harness than by static review.

**Closed by round 2 (no longer blind spots):**
- ~~Backup format compatibility~~ — verified byte-compatible, see Verified-correct table.
- ~~History record shape per event type~~ — verified except B.11.
- ~~Quality / language detection fidelity~~ — verified, no drift.
- ~~File move / copy / hardlink semantics~~ — verified, matches C#.
- ~~Config.xml + AppData path + API key handling~~ — verified except A.4 / B.9 / B.10 / B.12.

---

# When you (the porting session) work through this

1. Pick a Tier A item, fix it, run `golangci-lint run` + `go test ./...` + (once B.1 lands) the contract replayer.
2. Mark this file with a one-line closure note (e.g., `## A.1 — CLOSED in slice 5.J1`).
3. When all of Tier A is closed, this file becomes a Tier B/C tracker; consider folding Tier A history into a section of `STATUS.md` and deleting from here.
4. If you find any item is incorrect (the audit was wrong), strike it with a note explaining why — that's useful signal for the next external review.
