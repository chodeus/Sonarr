# Port Completion Plan — Verticalization & Coherence

Reference doc for the **Verticalization & Coherence Arc** (J386+). The loop cites this for the
target architecture and burns down the resource-completion matrix below. Authored from the
Option-A probe (post-J385).

> **Burn-down rule:** when a slice finishes a resource, update its row in the matrix (R/W/M) and
> note the slice that closed it. This file is the source of truth for "are we done."

---

## Why this arc exists

Prior arcs (J336–J385) were **contract-test-driven horizontal sweeps** — make GET bodies match C#.
That incentive pushed business logic into the composition root: the application/orchestration layer
ended up living in `cmd/sonarr` (the `*Populator` / `*Adapter` / `hydrate*` / `compute*` / `cascade*`
wrappers), not in the domain services. The read/write split made it worse — each resource was
developed twice in isolation, and the domain logic between them landed in `cmd` by default.

**Probe evidence (post-J385):**
- Handlers (`internal/api/v3`): ~23k LOC.
- Domain services (`internal/*`): 11k+ LOC of `service.go`, plus a rich package set —
  `seriesstats`, `aggregation`, `episodefilemoving`, `organizer`, `episodeimport`,
  `recyclebin`, `downloadedimport`, `seriesadd`.
- Composition/wiring (`cmd/sonarr`): ~12.8k LOC across 17 `*_wiring.go` + `host_wiring.go`
  (3.3k alone, 15 wrapper types + ~38 logic functions).

**Key insight (de-risks the write side):** the services the write cascades need MOSTLY ALREADY
EXIST — they are wired into background/import pipelines, not into the API read-hydration/write-cascade
paths. Even series statistics already has its DTOs + mappers (`ToSeriesStatisticsResource`); it just
isn't hydrated into the handler. Most "deferred" items are **connection + relocation work, not
from-scratch porting.** Always VERIFY a service is missing before declaring a block — most exist.

---

## Target architecture (enforce every slice)

| Layer | Owns | Must NOT contain |
|---|---|---|
| **Handler** (`internal/api/v3`, v5 mirror) | HTTP decode → ONE service call → wire projection (DTO mapping) | cross-entity orchestration, hydration, cascade logic |
| **Service / application** (`internal/<resource>`) | ALL business logic: read queries, write mutations, AND cross-entity orchestration (hydration, cascade, scoring), as plain methods | HTTP/wire concerns |
| **Wiring** (`cmd/sonarr`) | construct services + inject dependencies — DI ONLY | `hydrate*`/`compute*`/`project*`/`cascade*`/populator logic |

**Go-idiom rules:**
- Consumer-side interfaces — defined where used (service/handler), not as a wrapper in `main`.
- Explicit dependency injection via constructors.
- Direct calls instead of the C# event-bus/`IHandle` transliteration (already begun — "5.D8
  collapses the event-bus into a direct call chain").
- Every slice MOVES logic OUT of `cmd/sonarr` INTO the service; `host_wiring.go` should shrink.

**Definition of "done" per resource (all four):**
1. GET hydrates via its SERVICE (not a `cmd` populator).
2. POST/PUT/DELETE persists AND runs its real cascade via the service.
3. A read-after-write round-trip test passes.
4. The resource's DEFERRED markers are closed or accurately re-justified (cite the unblocking dependency).

NOT "GET body matches golden."

---

## Resource-completion matrix (burn-down)

**VERIFIED post-audit (ground-truthed against handler bodies + route wiring + command registry, NOT
comments).** Audit performed after J385 by tracing actual call paths. The v3 surface is ~90%+
functionally complete; most prior `DEFERRED` comments were stale. Evidence file:line in the punch-list.

### Verified DONE (do NOT re-do — markers were stale)

- **series**: create / update (+`?moveFiles` → real `MoveFolder`) / delete (+`?deleteFiles` cascade → real
  disk folder + 3 tables) / bulk editor / seasonpass monitor cascade — all real side-effects
  (`series.go:1203/1267/1326`, `seriesCascadeAdapter` `host_wiring.go:799`). Stats+images hydrated (J367/J369).
- **episode**: monitor toggle + update — real (`episode.go:393`).
- **queue grab**: real dispatch to download client (`queue_grab.go:183` → `ProcessDownloadDecisions`).
- **release push + download/grab**: fully wired in production; the `503 "not wired"` branches in
  `release_write.go` are DEAD code (fire only under nil test deps).
- **commands**: 20 handlers wired real (RefreshSeries, RssSync, Missing/CutoffUnmet/SeriesSearch, Backup,
  RescanSeries, DownloadedEpisodesScan, Housekeeping, CheckHealth, RefreshMonitoredDownloads, …);
  backlog **B.3 closed at J208**.
- **read surface**: history/blocklist/wanted/calendar/episode/queue all hydrated (J366–J384).

### Verified remaining functional punch-list (the REAL work, by impact)

| # | Gap | State | What's needed (deps that EXIST) | Evidence | Done by |
|---|---|---|---|---|---|
| 1 | Manual import — list candidates | DONE | `GetMediaFiles` ported: disk file/folder walk → GetImportDecisions (J387a), downloadId + library/unmapped modes (J387b), QualityWeight (J387c); reprocess deferrals closed (J388). Remaining: unknown-series recursive fallback + >100-file ProcessDownloadDirectory (documented edge-case deferral) | `internal/api/v3/manual_import.go` + `cmd/sonarr/manual_import_decision_wiring.go` | J387a-c, J388 |
| 2 | Manual import — execute | DONE | `ManualImport` command handler ports `ManualImportService.Execute`; per-file build → Augment → re-apply → `ImportApprovedEpisodes.Import` (real move/rename + episodefile insert) | `internal/commands/handlers/manualimport.go` | J386 |
| 3 | EpisodeFile editor Quality/Languages | DONE | persisted on editor/bulk/single PUT per the C# field matrix (J389) | `internal/api/v3/episodefile.go` | J389 |
| 4 | EpisodeFile delete — disk/recycle-bin | DONE | recycle-then-DB-delete via cmd adapter (ports MediaFileDeletionService); 409 on missing/empty root (J390) | `cmd/sonarr/episodefile_delete_wiring.go` | J390 |
| 5 | Queue delete — client/blocklist effects | DONE | UNBLOCKED: J394 populates the tracked-download cache from client polls (`downloadmonitoring.Refresh` → parse+Map+Track); J395 wires removeFromClient/changeCategory (resolve tracked download → `registry.Construct` live client → `RemoveItem`/`MarkItemAsImported` → StopTracking); J396 wires blocklist (publish `DownloadFailedEvent` → existing blocklist handler). Earlier "blocked" call was over-broad — clients were already ported. Deferred follow-ups: redownload-search consumer for `!skipRedownload`; blocklist Data enrichment (indexer/size) | `internal/api/v3/queue_write.go` + `cmd/sonarr/queue_remove_wiring.go` + `download_tracker_wiring.go` | J394-J396 |
| 6 | Missing commands | PARTIAL (core done) | DeleteSeriesFiles (J391), RenameFiles/RenameSeries (J392), MoveSeries/BulkMoveSeries (J393, disk move + revert + naming-config dest) DONE — the file/folder-management commands the plan named. Remaining: ProcessMonitoredDownloads (BLOCKED — Phase 4, `DownloadProcessingService` unported); lower-pri admin commands: ClearBlocklist DONE (J398); ResetApiKey/ResetQualityDefinitions/ClearLog remain | commands handlers | J391-J393, J398 |
| 7 | v5 season-monitor per-episode cascade | DONE | PUT /api/v5/series/{id}/season cascades episodes via `SetEpisodeMonitoredBySeason` (optional-extension on the seriesCascadeAdapter) | `internal/api/v5/series/series.go` | J397 |
| 8 | release/push response shape | COSMETIC | full ReleaseResource projection (functionally works) | `release_write.go` | |
| 9 | small read stragglers | PARTIAL | update Latest/Installable, diskspace path option, cert-exists | various | |

### Architecture (quality, not functional — opportunistic)

`cmd/sonarr` holds ~38 logic funcs + 15 wrapper types (populators/adapters/hydrate/compute/cascade). Relocate
into services WHEN you're already touching a resource for the punch-list — do NOT run a separate
relocation-only arc (it churns finished code for no functional gain). Contract + round-trip tests must stay green.

---

## Priority order (verified)

**Phase 1 — functional gaps (interactive/hybrid):**
1. **MANUAL IMPORT** (#1 + #2) — the biggest real feature gap; list candidates + execute command. Wire the
   existing `episodeimport`/`downloadedimport` pipeline.
2. **EPISODEFILE write** (#3 + #4) — editor Quality/Languages persist; delete → recycle-bin/disk.
3. **QUEUE delete** (#5) — apply removeFromClient/blocklist/skipRedownload/changeCategory.
4. **MISSING COMMANDS** (#6) — RenameFiles/RenameSeries, MoveSeries/BulkMove, DeleteSeriesFiles,
   ProcessMonitoredDownloads (reuse `organizer`/`episodefilemoving`/`recyclebin`); then lower-pri admin commands.
5. **stragglers** (#7 #8 #9) — season cascade, release response shape, small read fields.

**Phase 2 — v5 catch-up (backlog C.1):** the genuine bulk of remaining work — mirror ~56 upstream commits
per-endpoint (loop-friendly once Phase 1 lands).

---

## Process per resource

- **Orientation:** read the full stack — handler (`api/v3` + v5 mirror) + service (`internal/<resource>`)
  + `cmd/sonarr` wiring + the C# reference (`src/Sonarr.Api.V3/<Resource>/`, `Core/<Subsystem>/`, READ-ONLY).
- **Plan:** emit a short J\<n\> plan — what logic moves where, what gets wired, the round-trip test — before editing.
- **Hard gates (from `go/`):** `go build ./...` ; `go test -race -count=1 ./<touched>/...` (+ v5 mirror pkg
  + `cmd/sonarr` + `./test/contract/...`) ; `go vet` + `gofmt -l` (empty).
- **Safety:** write cascades touch the filesystem + background pipelines — test with real temp-dir disk ops
  or careful fakes + a round-trip assertion. NEVER no-op a side effect to pass a test. If a slice genuinely
  needs an unported service/field, STOP and flag (after verifying it's actually missing).
- **Bookkeeping:** prepend "HEAD at J\<n\>" to `PROGRESS.md` Current state; append a full `STATUS.md` entry
  (J379–J384 cadence) recording R/W/M for the resource; **update the matrix row above**; commit each slice
  separately, stage only changed files, no amend/force, NO AI attribution (per `~/.claude/CLAUDE.md`).

---

## Scope & execution (decided)

**Definition of done = functional + clean architecture + v5 current.** Three phases:
- **Phase 1 — Functional + clean (this matrix).** Every endpoint does what Sonarr does (wire the
  existing services), AND logic relocates from `cmd/sonarr` into services per the target architecture.
- **Phase 2 — v5 catch-up (backlog C.1).** Mirror the upstream `v5-develop` surface (go-port ~875
  ahead / 58 behind; ~56 upstream commits). Runs after/alongside Phase 1, per-endpoint slices.

**Execution model = hybrid.** Operator drives the hard/architectural resources interactively
(series, manual import, release grab, episodefile/queue write, command); the autonomous loop handles
the mechanical middle-only refactors (history/blocklist/wanted/calendar/episode populator relocation)
unattended. v5 (Phase 2) is loop-friendly per-endpoint slices once Phase 1 settles.

**Lean bookkeeping (efficiency rule):** one RESOURCE = one slice = one commit = one ~5-line STATUS
entry. No multi-paragraph essays — the diff + this matrix + git history are the record. Update the
matrix row when a resource reaches R+W+M.

**Do not touch:** `src/` (C# ref); the v5-develop branch refresh / Phase 6.4 cutover; `~/.config/Sonarr/`.

## Done

Project complete when (1) every matrix row is R+W+M ✅ with round-trip tests, AND (2) the C.1 v5
catch-up surface is mirrored. Phase 1 milestone: "verticalization & coherence arc complete."
