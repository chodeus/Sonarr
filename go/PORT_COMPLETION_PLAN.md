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

`R` = read/hydration · `W` = write/CRUD+cascade · `M` = middle (logic in service vs `cmd` wiring).
✅ done · 🟡 partial · ❌ gap. Ordered by remaining work. First-pass triage from markers + structure;
each slice refines its own row before acting.

| Resource | R | W | M | Remaining work | Closed by |
|---|---|---|---|---|---|
| manual import | 🟡 | ❌ | ❌ | exec service exists (`episodeimport`/`downloadedimport`) but POST no-ops; wire it; episode-stub projection | |
| series | 🟡 | 🟡 | ❌ | hydrate stats/images (DTOs done); wire delete-cascade (`recyclebin`), path-move (`episodefilemoving`/`organizer`), bulk-move; relocate `seriesCascadeAdapter` logic out of `cmd` | |
| release (grab/push) | 🟡 | 🟡 | 🟡 | grab override branch, indexer/dc name→id resolvers, full ReleaseResource response | |
| episodefile | ✅ | 🟡 | 🟡 | editor Quality/Languages updates; physical delete/recycle-bin move (service exists) | |
| queue | ✅ | 🟡 | 🟡 | write query-params parsed-and-ignored; relocate `queuePagedAdapter` logic | |
| command / scheduled-task | 🟡 | 🟡 | 🟡 | B.3: ~5 task handlers stub; SuppressMessages projection | |
| history | ✅ | n/a | 🟡 | read done (J366–J384); relocate populator/hydrate logic from `cmd` into a service | |
| blocklist | ✅ | n/a | 🟡 | read done (J382); relocate populator/hydrate logic from `cmd` | |
| wanted | ✅ | n/a | 🟡 | read done (J383/J384); relocate hydrate logic from `cmd` | |
| calendar | ✅ | n/a | 🟡 | read done (J376); relocate hydrate logic from `cmd` | |
| episode | ✅ | ✅ | 🟡 | relocate `applyEpisode*` orchestration into the service | |
| importlist | ✅ | 🟡 | ✅ | registry-membership validation | |
| update | 🟡 | n/a | 🟡 | Latest/Installable computation | |
| diskspace / parse / config_host | 🟡 | n/a | 🟡 | small read deferreds (path option, cert-exists) | |
| profiles / customformats / indexers / downloadclients / notifications / tags / config* | ✅ | ✅ | ✅ | thin/CRUD — low risk, spot-check only | |

---

## Priority order (biggest incoherence first; reassess each tick)

1. **SERIES** — hydrate statistics + images (DTOs/mappers already done) into list/by-id handlers; wire
   delete-cascade (`recyclebin`) + path-move (`episodefilemoving`/`organizer`) + bulk-move; relocate
   `seriesCascadeAdapter` logic from `cmd` into a series application service. Multi-slice.
2. **MANUAL IMPORT** — wire the existing `episodeimport`/`downloadedimport` executor into the POST
   (currently no-ops); finish the episode-stub projection.
3. **RELEASE (grab/push)** — grab override branch, indexer/dc name→id resolvers, full ReleaseResource response.
4. **EPISODEFILE write** — editor Quality/Languages updates; physical delete/recycle-bin move (service exists).
5. **QUEUE write** — apply the parsed-and-ignored query params; relocate `queuePagedAdapter` logic.
6. **COMMAND / SCHEDULED-TASK** — B.3 stub handlers; SuppressMessages projection.
7. **MIDDLE-ONLY refactors** for already-coherent read resources (history/blocklist/wanted/calendar/
   episode/queue): relocate populator/hydrate logic from `cmd` into services (no wire change; round-trip
   + contract tests must stay green).

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
