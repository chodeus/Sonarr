# Sonarr Go-Port — Gap Analysis (audit @ J419, 2026-05-29)

> **Fix status (updated through J425):**
> - ✅ **G1** search→grab — FIXED J423 (search adapters now feed the grabber).
> - ✅ **G2** CleanUpRecycleBin stub — FIXED J421 (dep wired).
> - ✅ **G3** queue/details empty — FIXED J422 (real lister).
> - ✅ **G4** provider fields[] on GET — FIXED J425 (inverse settings→fields mapper; 4 families). Follow-up: customformat/autotagging `specifications[]` + `ImplementationName` stub still open.
> - ✅ **G7** import-list sync inert — FIXED J424 (providers blank-imported). Sub-gap: `IDTranslator` nil (non-TVDB-id items).
> - ⏳ **G5** rename previews, **G6** series/lookup, **G8** v5 history season/episode, **G9** tag/detail — OPEN (focused service ports, autonomous).
> - ⏳ **T1** flaky shadowtraffic-e2e, **Q1** latent metadata-writer — OPEN.


Full-project audit after the read-surface verticalization work drained. Method: real
full-repo gates (`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` — all
run correctly this time, see note) + three parallel verification agents that traced
production wiring against the C# reference and **verified against code** (the port's
`DEFERRED` doc-comments are systematically stale — many "deferred" items are actually
done, and some "done"-looking paths are actually stubbed).

## Gate health (full-repo, verified)

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l .` — clean (after J419; the prior `gofmt -l ./...` gate was a silent no-op — gofmt doesn't expand the `./...` glob).
- `go test ./...` — **1 failure**: `cmd/shadowtraffic-e2e/TestTriadComposesEndToEnd` (flaky — passes 3/3 in isolation; fails under full-suite parallelism). See FINDING T1.
- Test coverage: only `internal/repo` lacks direct tests (transitively exercised by every repo's tests).

---

## Findings by severity

### CRITICAL — core flow incomplete

**G1 — Search finds releases but never grabs them.** `indexersearch.Service.EpisodeSearch`/`SeasonSearch` terminate at `decisionMaker.GetSearchDecision` and return decisions; they never call `ProcessDecisions`. So **MissingEpisodeSearch, CutoffUnmetEpisodeSearch, SeriesSearch, and auto-redownload-on-failure all search + log a count but download nothing.** Only RSS Sync bridges decisions → the grabber. The grabber (`*download.ProcessDownloadDecisions`, used by RssSync) already exists + is wired. The doc-comment "the search service handles its own grab dispatch" (`missingepisodesearch.go:66`) is FALSE against current code.
- Fix (AUTONOMOUS): route the search-command adapters (`handlerEpisodeSearchAdapter`, `seasonSearchAdapter`, `episodeRefreshedSearcherAdapter`, `redownloadSeasonSearchAdapter`) through the existing `processDownloadDecisions` grabber — same pattern RssSync uses.
- This closes G1 + the auto-redownload half of the Phase-4 arc.

### HIGH — endpoint returns empty / scheduled task inert (cheap fixes, data already exists)

**G2 — `CleanUpRecycleBin` scheduled task is a nil stub.** `handlers.Deps.CleanUpRecycleBin` is never set in main.go, so the 24h task registers `NewCleanUpRecycleBin(nil)` which no-ops. `recyclebin.Service.Cleanup(ctx)` is real + constructed. Fix: one line — `CleanUpRecycleBin: recycleBinSvc` in the Deps block. AUTONOMOUS, trivial.

**G3 — `GET /api/v3+v5/queue/details` returns empty.** Backed by `emptyQueueDetailsLister`. C# returns `queueService.GetQueue() + pendingReleaseService.GetPendingQueue()`; the Go port already has `queueSvc.GetQueue()` (used by the fully-wired `/queue`) + `pendingreleases.Repo` + the `projectQueueResource` mapper. Fix: a real adapter reusing existing pieces. AUTONOMOUS, clean.

### HIGH — resource projection gaps (UI edit-forms depend on these)

**G4 — Provider `fields[]` are never populated on GET (notification / indexer / importlist / downloadclient).** `to*Resource` mappers omit `Fields`; POST/PUT *consume* `fields[]` but GET emits nothing → asymmetric round-trip. Schema endpoints ARE fully populated (the stale "schema-builder DEFERRED" comments are wrong for schemas). Root cause: no inverse `settings-JSON → []ProviderField` mapper. The per-impl `FieldDef` metadata already exists in the `*_schema.go` tables. Fix (AUTONOMOUS): one shared mapper that joins a stored `Settings` map against the matching schema's field list → populates all four families. Also fixes `ImplementationName` (currently copies `Implementation` as a stub) + `customformat`/`autotagging` `specifications[]` (same shape).

### MEDIUM — endpoint returns empty (needs a focused service/driver port)

**G5 — `GET /rename` + `/rename/bulk` return `[]`.** Need a port of C# `RenameEpisodeFileService.GetRenamePreviews` (diff each file's current path vs `BuildFileName` output). `BuildFileName` exists; the comparison driver doesn't.

**G6 — `GET /series/lookup` returns `[]`.** Need to wire the already-ported `metadatasource.Proxy.SearchForNewSeries` → cover-rewrite + folder-name preview → `SeriesResource`.

**G7 — Import List Sync is inert.** `SyncService` is wired to the scheduled task, but NO importlist provider packages are blank-imported into the binary → the provider registry is empty → every tick adds nothing. Fix: blank-import the provider packages (check which exist). Also `IDTranslator: nil` skips zero-TvdbID items.

**G8 — `GET /api/v5/history/season` + `/history/episode` return `[]`.** Need `history.Service.GetBySeason`/`GetByEpisode` (only `GetBySeries` ported). v5-only routes.

**G9 — `GET /tag/detail` returns empty.** Needs a tag-usage aggregator joining Tag rows against every consumer table (DelayProfile/ImportList/Notification/Indexer/DownloadClient/AutoTagging/Series). Larger.

### LOW — quality / latent

**T1 — Flaky test `TestTriadComposesEndToEnd`** (cmd/shadowtraffic-e2e, dev tooling). Capture-enqueue-vs-shutdown-drain race: the 3rd captured entry can be enqueued after the SIGTERM drain starts → "got 0 log entries". Passes in isolation. Fix: have the record proxy fully drain pending captures before closing the log writer, or have the test poll the log for N entries with a timeout.

**Q1 — Metadata-writer orchestrator is latent** — built + reachable but no event source drives it (`metadataSvc` held via `_`). Metadata files (nfo/images) aren't written on import/rename yet.

**Q2 — `internal/repo` has no direct tests** (transitively covered by every repo's suite). Low risk.

---

## Cross-cutting note
G1 (search→grab) and the auto-redownload arc share one root: the search layer returns
decisions but doesn't grab. G4 (provider fields) + customformat/autotagging specs share
one root: no inverse settings→fields mapper. Each root fix closes several findings.

## Autonomy
AUTONOMOUS (no operator input; connect/port existing tested pieces): G1, G2, G3, G4, G7, T1.
Needs a focused service/driver port (still autonomous but larger): G5, G6, G8, G9, Q1.
