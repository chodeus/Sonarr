# Sonarr Go-Port — Gap Analysis (audit @ J419, 2026-05-29)

> **Fix status (updated through J429):**
> - ✅ **G1** search→grab — FIXED J423 (search adapters now feed the grabber).
> - ✅ **G2** CleanUpRecycleBin stub — FIXED J421 (dep wired).
> - ✅ **G3** queue/details empty — FIXED J422 (real lister).
> - ✅ **G4** provider fields[] on GET — FIXED J425 (inverse settings→fields mapper; 4 families). Follow-up: customformat WRITE-path persistence shape FIXED J431 (was a CRITICAL bug — POST/PUT-with-specs 500'd through real svc; now stores `{type,body}` envelope). G4-followup fully drained: customformat GET read-half (J432), autotagging specs (J433), `ImplementationName` (J443, all 5 families).
> - ✅ **G7** import-list sync inert — FIXED J424 (providers blank-imported). Sub-gap: `IDTranslator` nil (non-TVDB-id items).
> - ✅ **G6** series/lookup — FIXED J426 (wired to skyhook proxy).
> - ✅ **G8** v5 history season/episode — FIXED J427 (episode) + J428 (season).
> - ✅ **G5** rename previews — FIXED J429 (`internal/renamepreview.Service` diff driver; wired v3+v5 with NamingConfig path builder).
> - ✅ **G9** tag/detail — FIXED J430 (`internal/tagdetail.Service` 9-source aggregator; folds autotag TagSpec values; wired v3+v5).
> - ✅ **T1** flaky shadowtraffic-e2e — FIXED J434 (poll log for entries before SIGTERM; sibling test too; 3× full-suite green).
> - ✅ **Q1** latent metadata-writer — FIXED J435 (subscribeMetadataWriters drives metadataSvc from EpisodeImported/CoversUpdated/SeriesScanned). Tail: SeriesRenamed/DownloadsProcessed/EpisodeFolderCreated events not yet ported/published — wire when publishers land.


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

### G4b — customformat specifications round-trip (write FIXED J431, read OPEN)

**Write half (CRITICAL, FIXED J431):** `customFormatToModel` persisted the on-wire schema shape (`{implementation, fields[]}`) into the `Specifications` column, but `customformats.LoadFromRow`→`UnmarshalSpecs` only accepts the `{type, body}` envelope. So creating/updating a CustomFormat with ANY condition failed write→reload through the real service (the handler unit tests use a fake writer that skips the reload, masking it). Fixed by `customFormatSpecsToEnvelopeJSON` (maps wire spec → envelope, validates via the loader's own codec).

**Read half (FIXED J432):** `toCustomFormatResource` now projects each typed `customformats.Specification` → `CustomFormatSpecificationSchema` via `specToSchema` (clones the golden-verified `customFormatSchemas` FieldDef template, injects the persisted value by field name; adds InfoLink + id-omitempty). With J431 the customformat spec round-trip (create→reload→GET) is whole.

**Autotagging (FIXED J433):** identical dual defect (write persisted schema-shape → CUD-with-specs broke; GET stubbed `[]`) fixed symmetrically — `autoTaggingSpecsToEnvelopeJSON` (write) + `autoTagSpecToSchema` (GET, clones `autoTaggingSchemas` templates, handles `status` body-key + array-valued specs) + id-omitempty. **G4-followup specifications[] now COMPLETE for customformat + autotagging.** `ImplementationName` provider stub FIXED J443 (all 5 families source from schema tables). G4-followup fully drained.

### MEDIUM — endpoint returns empty (needs a focused service/driver port)

**G5 — `GET /rename` + `/rename/bulk` return `[]`.** ✅ FIXED J429. Ported C# `RenameEpisodeFileService.GetRenamePreviews` as `internal/renamepreview.Service` (diff each file's current path vs `organizer.BuildFilePath` output, NamingConfig-driven, file.MediaInfo threaded). Wired v3+v5 via `newRenamePreviewer`. CustomFormats stay nil (same documented divergence as the import path).

**G6 — `GET /series/lookup` returns `[]`.** Need to wire the already-ported `metadatasource.Proxy.SearchForNewSeries` → cover-rewrite + folder-name preview → `SeriesResource`.

**G7 — Import List Sync is inert.** `SyncService` is wired to the scheduled task, but NO importlist provider packages are blank-imported into the binary → the provider registry is empty → every tick adds nothing. Fix: blank-import the provider packages (check which exist). Also `IDTranslator: nil` skips zero-TvdbID items.

**G8 — `GET /api/v5/history/season` + `/history/episode` return `[]`.** Need `history.Service.GetBySeason`/`GetByEpisode` (only `GetBySeries` ported). v5-only routes.

**G9 — `GET /tag/detail` returns empty.** ✅ FIXED J430. `internal/tagdetail.Service` ports `TagService.Details()` — joins Tag rows against all 9 consumer tables via caller-supplied loaders; autotag loader folds `*TagSpec.Value` (ports `GetAutoTagIds`), excluded-RP uses `ExcludedTags`. Wired v3+v5 via `newTagDetailLister`.

### LOW — quality / latent

**T1 — Flaky test `TestTriadComposesEndToEnd`** (cmd/shadowtraffic-e2e, dev tooling). ✅ FIXED J434 — root cause confirmed: the test read the record binary's JSONL immediately after SIGTERM, but under parallel load the subprocess could be force-killed before its async drain flushed. Fix polls the log for the expected entry count (the drain flushes every 50ms, so entries are visible pre-shutdown) before SIGTERM. Same fix applied to the sibling `TestRecordReplayRoundTripsCleanly`. Verified 3× consecutive full-suite green. Original diagnosis below retained for context. Refined diagnosis (J428 audit): the test runs the record proxy as a SUBPROCESS with an async buffered LogWriter (flush-on-Close). Under full-suite parallel load the subprocess is starved, and `stopAndWait`'s graceful-shutdown window likely elapses → the proxy is force-killed before the drain goroutine flushes+fsyncs → "got 0 log entries". Passes 3/3 in isolation. Fix options: lengthen the subprocess shutdown grace in `stopAndWait`, or have the test poll the JSONL for N entries with a timeout before asserting. Dev-tooling only; not app behaviour.

**Q1 — Metadata-writer orchestrator is latent.** ✅ FIXED J435 — `subscribeMetadataWriters` (cmd/sonarr) drives `metadataSvc` from the 3 ported+published events (EpisodeImported→CreateAfterEpisodeImport, CoversUpdated→CreateAfterMediaCoverUpdate, SeriesScanned→CreateAfterSeriesScan). TAIL (deferred, evidence): C# also handles SeriesRenamedEvent / DownloadsProcessedEvent / EpisodeFolderCreatedEvent — these event types are NOT defined/published in the Go port yet (only EpisodeFileRenamedEvent exists, per-file vs C#'s per-series); wire the rename/downloads-processed metadata paths when those publishers land. Separately, metadata only writes when a metadata consumer package is blank-imported + enabled (registry currently empty — orthogonal, same shape as G7).

**Q2 — `internal/repo` has no direct tests** (transitively covered by every repo's suite). Low risk.

---

## Fresh audit findings (J440, after Tier-1/2/3/4 drained)

Method: full-repo gates (build/vet/gofmt `.`/test ./... — all green) + targeted production-wiring traces vs C#. Tier-1 (G1-G9,T1,Q1) confirmed fixed; Tier-2 resource projections complete; Tier-3 v5 route long-tail = 0 missing (79 C# v5 endpoints all wired); Tier-4 write paths done except the items below.

**G10 — indexer Test endpoint inert (dual-registry split). ✅ FIXED (J441 Newznab+Torznab; J442 remaining 8 — all 10 providers now registered).** `POST /api/v3+v5/indexer/test` + `/indexer/testall` go through `registryIndexerTester` → `indexers.Default()` (an `indexers.Registry`), but the 10 concrete indexer provider packages' `init()` blocks register ONLY into `provider.Default()` (the `FamilyIndexer` catalog used by settings hydration + the indexersearch path), NOT into `indexers.Default()`. So `indexers.Registry` is empty at runtime → every test returns 400 "Unknown implementation" (host_wiring.go:2489 comment is accurate, not stale). Contrast: notification/downloadclient providers DO register into their `*.Default()` Registry via `init()` (e.g. `notifications/webhook/webhook.go:270`), so their Test works. FIX (in-scope, ungated, but SIZEABLE): give each indexer provider an `indexers.Registry` constructor (adapting `provider.NewIndexer(client,caps)` → the `indexers.Provider` Test interface) + register it in `init()`, threading `indexers.Dependencies`. ~10 providers + the indexers.Provider interface conformance. Distinct subsystem from both the catalog and the search-adapter path (`dbIndexerSource.buildAdapter` returns `indexersearch.Indexer`, a different interface). Multi-slice; start with Newznab/Torznab (the common case). **J441 did part 1**: `cmd/sonarr/indexer_test_wiring.go` registers Newznab+Torznab Registry constructors into `indexers.Default()` (via `registerIndexerTestProviders(httpClient)` in serve()), reusing the search-path construction + a `concreteIndexerProviderAdapter` (ValidationResult→TestFailure). PART 2 (remaining): add `reg(impl, contract, mk(...))` lines for broadcasthenet/hdbits/filelist/fanzub/iptorrents/nyaa/torrentleech/torrentrss — verify each's `NewIndexer` signature (some take only `client`, not caps — e.g. torrentrss) + ConfigContract key, then register.

**Q3 (NEW, BLOCKED) — ManualImport completion events.** The ManualImport command (J386) imports files but defers publishing `DownloadCompletedEvent` (tracked) / `UntrackedDownloadCompletedEvent` (untracked) + the tracked-download output-folder cleanup + state transition (C# `ManualImportService.cs:572-626`). Evidence it's BLOCKED on unported substrate: `DownloadCompletedEvent` has NO producer anywhere in the Go tree (grep: zero `Publish(...DownloadCompletedEvent)` / `DownloadCompletedEvent{}` construction) — it's the Phase-4 `CompletedDownloadService` completion-publication path, not yet ported; and `UntrackedDownloadCompletedEvent` is not even defined as a Go type. Wiring it from manual import alone would be a partial/inconsistent first producer. Revisit when the Phase-4 download-completion publisher lands.

**Note — redownload !skipRedownload arm is DONE** (not deferred): `redownload.Service` IS subscribed to `DownloadFailedEvent` (`main.go` "redownload.HandleDownloadFailed" → `redownloadSvc.Handle`), so manual queue-remove with `skipRedownload=false` triggers the re-search. The queue_remove_wiring.go "deferred" doc-comment was stale — fixed J440.

---

## Cross-cutting note
G1 (search→grab) and the auto-redownload arc share one root: the search layer returns
decisions but doesn't grab. G4 (provider fields) + customformat/autotagging specs share
one root: no inverse settings→fields mapper. Each root fix closes several findings.

## Autonomy
AUTONOMOUS (no operator input; connect/port existing tested pieces): G1, G2, G3, G4, G7, T1.
Needs a focused service/driver port (still autonomous but larger): G5, G6, G8, G9, Q1.
