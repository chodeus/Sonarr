# Phase 5 Audit — Find & Fix Backlog

This document is the authoritative inventory of gaps in the Go port as of 2026-05-18 (post-slice 4.FW). It exists because the byte-equal contract harness, by design, only proves wire-shape parity on the *read side*. Four independent audit subagents (endpoint coverage, write-path coverage, provider implementation, cross-cutting concerns) ran against the post-4.FW tree and produced the findings below.

Pairs with `go/PHASE_6_SCOPE.md` (which scopes the operational gate). This doc scopes the *code-side gap* the operational gate would otherwise have to find.

## Headline

Phase 5 byte-equal endpoint count is 45/51 (88%). That number is **misleading** as a project-completion signal. The 45 endpoints are all `GET` (read-side). The C# tree exposes dozens of write endpoints, none of which are ported. 25 of the 75 provider schemas the port serves have no behavioural implementation — most damning, **qBittorrent** (the highest-traffic real-world download client). Cross-cutting drift on error envelope shape breaks any client that parses error bodies, including the React UI.

The port is structurally a read-side projection of Sonarr, not a working replacement.

## Audit summary by severity

| Severity | Finding | Surface |
| -------- | ------- | ------- |
| 🔴 Critical | Zero write endpoints under `/api/v3` | Every POST/PUT/DELETE absent |
| 🔴 Critical | 25 / 75 providers schema-only | 7 indexers, 18 download clients |
| 🔴 Critical | Error response shape drift (`text/plain` vs C# JSON envelope) | ~25 handler files |
| 🟡 Important | Inherited `GET /{id}` absent on every list resource | ~18 resources |
| 🟡 Important | Many read endpoints absent entirely | command, episodefile, system/task, system/backup, log, parse, manualimport, mediacover, filesystem, indexerflag, release search, rename, series/lookup, importlistexclusion, calendar/feed, queue/details, system/shutdown/restart, diskspace, filesystem, language profile (legacy — skip), config/* PUT, config/naming/examples, qualitydefinition PUT/update, qualitydefinition/limits |
| 🟡 Important | Zero handler-side logging — 500-class errors vanish | All v3 handlers |
| 🟡 Important | SignalR broadcast plumbing exists but never invoked | All write sites would need it |
| 🟡 Important | FluentValidation analog not wired | All write sites would need it |
| 🟢 Nit | `?access_token=` query accepted on REST endpoints (looser than C#) | `go/internal/host/auth.go:60-66` |
| ✅ Pass | Auth middleware uniformity | `go/internal/host/server.go:341-345` |
| ✅ Pass | HTTP method handling (Go 1.22 ServeMux auto-405) | stdlib |
| ✅ Pass | Sensitive-value logging | clean |

## Per-resource matrix (writes)

C# RestController + ProviderControllerBase exposes POST/PUT/DELETE on every resource below. Every cell is "MISSING" today.

```
              list create update delete bulk-PUT bulk-DEL test testall action
series         ✓     ✗      ✗      ✗      ✗        ✗
episode        ✓     —      ✗      —      ✗(monitor)
episodefile    ✗     —      ✗      ✗      ✗        ✗
indexer        ✓     ✗      ✗      ✗      ✗        ✗      ✗    ✗      ✗
importlist     ✓     ✗      ✗      ✗      ✗        ✗      ✗    ✗      ✗
downloadclient ✓     ✗      ✗      ✗      ✗        ✗      ✗    ✗      ✗
notification   ✓     ✗      ✗      ✗      ✗        ✗      ✗    ✗      ✗
metadata       ✓     ✗      ✗      ✗      ✗        ✗      ✗    ✗      ✗
customformat   ✓     ✗      ✗      ✗      ✗        ✗
releaseprofile ✓     ✗      ✗      ✗
qualityprofile ✓     ✗      ✗      ✗
qualitydefn    ✓     —      ✗      —      ✗(update)
tag            ✓     ✗      ✗      ✗
rootfolder     ✓     ✗      —      ✗
customfilter   ✓     ✗      ✗      ✗
autotagging    ✓     ✗      ✗      ✗
delayprofile   ✓     ✗      ✗      ✗      —        —      —    —      reorder/{id}
remotepathmap  ✓     ✗      ✗      ✗
importlistexcl ✗     ✗      ✗      ✗      —        ✗(bulk)
namingconfig   ✓     —      ✗      —
config/* (6)   ✓     —      ✗      —
blocklist      ✓     —      —      ✗      —        ✗(bulk)
queue          ✓     —      —      ✗      —        ✗(bulk) grab/{id}, grab/bulk POST
command        ✗     ✗      —      ✗
history        ✓     —      —      —      —        — failed/{id} POST
manualimport   ✗     ✗
system/backup  ✗     —      —      ✗      —        — restore/{id} POST, restore/upload POST
system/shutdown ✗ POST
system/restart  ✗ POST
```

## Per-resource matrix (reads beyond list)

GET-by-id missing on all 18: autotagging, customfilter, customformat, delayprofile, downloadclient, importlist, indexer, metadata, notification, qualityprofile, releaseprofile, remotepathmapping, rootfolder, tag, series, episode, command, queue, blocklist.

Other reads missing entirely (representative — full list in audit-1 output):
- `GET /api/v3/calendar/feed` (Sonarr.ics)
- `GET /api/v3/command`, `GET /…/{id}`
- `GET /api/v3/diskspace`
- `GET /api/v3/episodefile`, `GET /…/{id}`
- `GET /api/v3/filesystem`, `/type`, `/mediafiles`
- `GET /api/v3/history/since`, `/series`
- `GET /api/v3/importlistexclusion`, `/paged`, `/{id}`
- `GET /api/v3/indexerflag`
- `GET /api/v3/release` (search)
- `GET /api/v3/log`, `/log/file`, `/log/file/{filename}`, `/log/file/update`, `/log/file/update/{filename}`
- `GET /api/v3/manualimport`
- `GET /api/v3/mediacover/{seriesId}/{filename}`
- `GET /api/v3/parse`
- `GET /api/v3/qualitydefinition/limits`
- `GET /api/v3/queue/details`
- `GET /api/v3/rename`, `/rename/bulk`
- `GET /api/v3/series/lookup`
- `GET /api/v3/series/{id}/folder`
- `GET /api/v3/system/backup`
- `GET /api/v3/system/routes`, `/routes/duplicate`
- `GET /api/v3/system/task`, `…/{id}`
- `GET /api/v3/config/naming/examples`

Intentional skip (do not flag): `languageprofile` controllers (V3 legacy; v5-deprecated and folded into per-quality-profile language fields). `internal-tooling routes` not in C#'s public surface.

## Provider implementation matrix

Classification across the 75 schemas shipped in 4.FT–4.FW.

```
                     full  settings-only  stub  missing
Notifications (25)    25         0         0       0
Importlists  (10)     10         0         0       0
Indexers     (10)      3         0         0       7
DownloadCli  (20)      2         0         0      18
                    ─────                       ─────
Total                 40         0         0      25
```

### Missing-impl rosters

**Indexers (7)**: BroadcastheNet, Fanzub, FileList, HDBits, IPTorrents, Nyaa, Torrentleech.

**Download clients (18)**: Aria2, Deluge, TorrentDownloadStation, UsenetDownloadStation, Flood, TorrentFreeboxDownload, Hadouken, Nzbget, NzbVortex, Pneumatic, QBittorrent, RQBit, RTorrent, TorrentBlackhole, TriblerDownloadClient, UsenetBlackhole, UTorrent, Vuze.

### High-traffic flagged providers (priority)

| Provider | Bucket | Note |
| -------- | ------ | ---- |
| qBittorrent | MISSING | Likely the highest-traffic Sonarr download client. Schema served, no behaviour. |
| Deluge | MISSING | Second-most-common torrent client. |
| NZBGet | MISSING | One of two dominant usenet clients (other is SABnzbd, which IS implemented). |
| rTorrent | MISSING | Common power-user choice. |
| uTorrent | MISSING | Still common despite warning banner. |
| SABnzbd | full | ✓ |
| Transmission | full | ✓ |
| Discord | full | ✓ |
| Telegram | full | ✓ |
| Plex (notification + importlist) | full | ✓ (incl. OAuth) |
| Sonarr (importlist) | full | ✓ |
| Trakt (4 variants) | full | ✓ (incl. OAuth) |

## Cross-cutting findings

### Error envelope drift (🔴 critical)

C# emits via `Sonarr.Http/ErrorManagement/ErrorModel.cs`:

- 5xx / 4xx (non-validation): `{"message": "...", "description": "...", "content": null}` JSON
- 400 (FluentValidation): `[{"propertyName": "...", "errorMessage": "...", "severity": "error"}, ...]`

Go port emits `text/plain` via `http.Error(w, "...", status)` everywhere. Sample hit list (~25 handler files):

```
go/internal/api/v3/episode.go:76,81,86
go/internal/api/v3/blocklist.go:47
go/internal/api/v3/rootfolder.go:55
go/internal/api/v3/config_ui.go:64
go/internal/api/v3/downloadclient.go:42
go/internal/api/v3/importlist.go:44
go/internal/api/v3/qualityprofile.go:131,136,149,216,221,232
go/internal/api/v3/system_status.go:112
go/internal/api/v3/wanted.go:47
go/internal/api/v3/autotagging.go:72
go/internal/api/v3/customfilter.go:45
go/internal/api/v3/metadata_schema.go:191
go/internal/api/v3/update.go:77
```

Also leaks raw error text (DB driver messages, file paths) into the response body via `fmt.Sprintf("episodes: %v", err)`. **Fix shape**: introduce `httperr.Write(w, status, msg, err)` helper that emits the C# envelope; sed across all handler files.

### Handler logging (🟡 important)

No v3 handler imports or threads `*slog.Logger`. The `host` package logs at boot and on auth failures; v3 errors are silent. Operators see nothing when a DB read fails — only the 500 reaches the client.

**Fix shape**: thread `*slog.Logger` through `v3Routes(...)` and have the http-error helper do `logger.Error("handler error", "path", r.URL.Path, "err", err)` at the 5xx threshold.

### SignalR broadcast unwired (🟡 important — gated by writes)

`go/internal/signalr/resource.go` and `signalr.go:199` define `BroadcastResourceChange`. No write-path callers exist, so the function is dead today. Becomes relevant the moment any POST/PUT/DELETE lands.

### FluentValidation analog (🟡 important — gated by writes)

C# write handlers rely on injected `IValidator<T>` chains (RootFolderValidator, SeriesPathValidator, QualityProfileExistsValidator, etc.). No Go equivalent. Becomes relevant once writes start landing. Suggested approach: per-resource validator interface + `RuleFor`-style fluent helpers, or just inline validation with consistent error-shape emission.

## Prioritized fix-mission backlog (largest chunk first)

This is the order the loop prompt should consume. Each tier is many slices; tier transitions are checkpoint moments.

### Tier 1 — Write paths (largest, multi-week)

The full POST/PUT/DELETE matrix above. Subdivided by resource family:

1. **Foundational infra** (lands once, unblocks everything):
   - 5.A1: `internal/httperr` package — JSON error envelope helper matching C# `ErrorModel` shape (5xx) + FluentValidation array shape (400).
   - 5.A2: Validation primitive — `internal/validation` with `Rule` + `RuleSet` + per-field error accumulator emitting the C# 400 shape.
   - 5.A3: SignalR broadcast write-site adapter — wrap `hub.Broadcast` calls behind a per-resource `Emitter` so handlers can fire without knowing the hub.
   - 5.A4: Sweep `http.Error` → `httperr.Write` across all existing handlers (cross-cutting, lands behind 5.A1).
   - 5.A5: Thread `*slog.Logger` into `v3Routes` + per-handler 5xx logging (lands behind 5.A1).

2. **Simplest write paths first** (set the pattern):
   - 5.B1: Tag CRUD (POST/PUT/DELETE) — smallest data model, has SignalR Sync.
   - 5.B2: Root folder CUD (POST/DELETE only — no PUT in C#).
   - 5.B3: Custom filter CRUD.
   - 5.B4: Custom format CRUD + bulk.
   - 5.B5: Quality profile CRUD.
   - 5.B6: Release profile CRUD.
   - 5.B7: Quality definition PUT + bulk update + `/limits`.
   - 5.B8: Delay profile CRUD + reorder.
   - 5.B9: Remote path mapping CRUD.
   - 5.B10: Autotagging CRUD.
   - 5.B11: Import list exclusion CRUD + bulk.
   - 5.B12: Naming config PUT + `/examples`.
   - 5.B13: Config/* PUTs (host, ui, mediamanagement, indexer, downloadclient, importlist).

3. **Provider CUD** (apply ProviderControllerBase pattern uniformly):
   - 5.C1: Indexer POST/PUT/DELETE + bulk + test/testall/action.
   - 5.C2: ImportList POST/PUT/DELETE + bulk + test/testall/action.
   - 5.C3: DownloadClient POST/PUT/DELETE + bulk + test/testall/action.
   - 5.C4: Notification POST/PUT/DELETE + bulk + test/testall/action.
   - 5.C5: Metadata POST/PUT/DELETE + bulk + test/testall/action.

4. **Series / episode / episodefile** (heaviest domain logic):
   - 5.D1: Series POST/PUT/DELETE + `?deleteFiles` + `?addImportListExclusion` query handling.
   - 5.D2: Series editor PUT/DELETE.
   - 5.D3: Series import POST.
   - 5.D4: Episode PUT/{id} + `PUT /episode/monitor`.
   - 5.D5: Episode file PUT/DELETE/bulk + `PUT /episodefile/editor`.
   - 5.D6: Season pass POST.

5. **Queue / command / blocklist / history**:
   - 5.E1: Command POST (the most critical write — no scheduler entry point without it) + DELETE.
   - 5.E2: Queue DELETE/{id} + DELETE/bulk + POST grab/{id} + POST grab/bulk.
   - 5.E3: Blocklist DELETE/{id} + DELETE/bulk.
   - 5.E4: History POST failed/{id}.

6. **System writes**:
   - 5.F1: System shutdown/restart POST.
   - 5.F2: System backup DELETE/{id} + restore/{id} POST + restore/upload POST.

### Tier 2 — Missing provider implementations (~2-3 days each)

Order by traffic / likelihood-of-being-tested-in-shadow-traffic:

7. **Download clients, by traffic**:
   - 5.G1: qBittorrent (full impl + tests + register).
   - 5.G2: Deluge.
   - 5.G3: NZBGet.
   - 5.G4: rTorrent.
   - 5.G5: uTorrent.
   - 5.G6: Aria2.
   - 5.G7: Vuze.
   - 5.G8: Transmission/Vuze inheritance already done; verify Flood, TorrentBlackhole, UsenetBlackhole are smallest.
   - 5.G9: TorrentDownloadStation + UsenetDownloadStation (share base).
   - 5.G10: Hadouken, TorrentFreeboxDownload, NzbVortex, Pneumatic, RQBit, TriblerDownloadClient — the long tail.

8. **Indexers, by traffic**:
   - 5.H1: FileList.
   - 5.H2: BroadcastheNet.
   - 5.H3: IPTorrents.
   - 5.H4: HDBits.
   - 5.H5: Torrentleech.
   - 5.H6: Nyaa.
   - 5.H7: Fanzub.

### Tier 3 — Missing read endpoints

Mostly mechanical; group by domain:

9. **Inherited GET-by-id (18 resources)** — bulk-port as a single sweep or per-resource as part of each Tier 1 CUD slice. Suggest: do it alongside Tier 1 (each Tier 1 slice that adds POST also adds GET/{id}).

10. **Missing reads** (each ~30min-2hr):
    - 5.I1: Command list + by-id (paired with 5.E1).
    - 5.I2: Episode file list + by-id (paired with 5.D5).
    - 5.I3: Disk space.
    - 5.I4: System task list + by-id.
    - 5.I5: System backup list.
    - 5.I6: System routes + routes/duplicate.
    - 5.I7: Log + log/file + log/file/update.
    - 5.I8: Parse.
    - 5.I9: Manual import.
    - 5.I10: Media cover.
    - 5.I11: Filesystem + filesystem/type + filesystem/mediafiles.
    - 5.I12: Indexer flag.
    - 5.I13: Release search.
    - 5.I14: Rename + rename/bulk.
    - 5.I15: Series lookup + series/{id}/folder.
    - 5.I16: Import list exclusion list + paged + by-id (paired with 5.B11).
    - 5.I17: Calendar feed (Sonarr.ics).
    - 5.I18: Queue details (paired with 5.E2).
    - 5.I19: Config/naming/examples.
    - 5.I20: Quality definition `/limits` (paired with 5.B7).
    - 5.I21: History `/since`, `/series` (paired with 5.E4).

## Acceptance criteria for "done"

A slice in this backlog is done when:

1. The new handler emits the correct wire shape (verify via the hostbootstrap contract test if a golden exists; otherwise add a unit test pinning the JSON shape).
2. If it's a write path, it fires the correct SignalR broadcast.
3. If it's a write path, it validates input through the `internal/validation` package and emits the C# 400 shape on failure.
4. If it's a read path with a stable wire shape, it gets a byte-equal contract test against the C# golden.
5. `go test ./...` is green.
6. `golangci-lint run` is 0 issues in touched packages.
7. Per-slice note appended to `go/STATUS.md` line 9.
8. Commit message follows the existing slice convention (e.g., `Phase 5.B1 (write path): POST/PUT/DELETE /api/v3/tag`).

## Out of scope for Phase 5 fix-mission

Belongs to Phase 6 / Phase 7 per the strategic plan:

- Shadow-traffic harness deployment + replay infra (Phase 6).
- Nightly contract CI workflow (Phase 6).
- The 6 timing/environment-sensitive endpoints `system/task` (`lastDuration`), `system/routes` (DOT graph), `diskspace`, `log`, `command`, `episodefile?seriesId=1` 404 description — all per `go/PHASE_6_SCOPE.md`.
- Idiomatic-Go refactoring pass (Phase 7).
- src/ deletion (Phase 6 cutover).
