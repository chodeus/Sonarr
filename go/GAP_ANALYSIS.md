# Sonarr Go-Port — Gap Analysis (audit @ J419, 2026-05-29)

## PHASE-2 CONFIRMATION AUDIT (J474, 2026-05-30) — commands / scheduled tasks / event consumers

Broad maturity check (Explore). **Commands: all user-facing C# commands registered in Go** (out-of-scope: ResetApiKey, ApplicationUpdate install, framework Test/Unknown). **Scheduled tasks: all 11 registered.** **Event consumers:** found `TrackedDownloadService`'s `IHandle<SeriesEditedEvent>/<SeriesDeletedEvent>` (+ SeriesAdded/BulkEdited/EpisodeInfoRefreshed) cache-refresh handlers UNPORTED.
- **FIXED J474 (SeriesEdited + SeriesDeleted):** the tracker keeps the cached RemoteEpisode (existing-item arm) + the queue renders from it → after a series rename/delete the queue/activity view showed the stale/dangling title permanently. NEW `RefreshTrackedForSeries` re-resolves matching tracked downloads (ParseTitle+Map) on those events. The doc's "unported event shapes" note was STALE (events published at series/mutations.go:355,490).
- ✅ **SeriesBulkEdited + SeriesAdded handlers — FIXED J475** (completes the family). EpisodeInfoRefreshed handler remains LOW-deferred (removed-episode-id match shape, rare). Also DownloadMonitoringService's event-driven refresh triggers (EpisodeGrabbed/Imported→refresh) are an optimization over the 1-min RefreshMonitoredDownloads scheduled task; PendingReleaseService handlers are part of the deferred Delay/Pending subsystem.

## PHASE-2 FRESH AUDIT (J472, 2026-05-30) — metadata consumers + extra-file importers

Two Explore agents.
- **Metadata writers: 4/4 ported + registered (Xbmc/Roksbox/Wdtv/Plex). GAP: `MapCoversToLocal` dep UNWIRED → FIXED J472.** The provider constructors cast `deps.MapCoversToLocal.(CoverPathLookup)` (non-panicking); main.go's metadataWriterDeps never set it → series cover images (folder.jpg) silently not written for Xbmc/Roksbox/Wdtv. Fixed by `MapCoversToLocal: mediaCoverSvc` (mediacover.Service.GetCoverPath matches the interface). Construct-seam pin test.
  - ✅ **EpisodeLookup + FolderLister deps — FIXED J473.** Added both as `any` fields on metadata.Dependencies; cast in xbmc/roksbox/wdtv constructFromDependencies; wired `EpisodeLookup: episodesRepo` (*episodes.Repo.AllByFileID) + `FolderLister: diskProv` (*disk.Provider.GetDirectories). Episode + season images now emit. Metadata-writer subsystem FULLY wired (covers J472 + episode/season J473).
- **Extra-file IMPORT subsystem: LARGE GAP (genuinely unported).** C# `ExtraService` orchestrates 3 `IManageExtraFiles` (MetadataService order 0, SubtitleService order 1, OtherExtraService order 2) to import sidecar files (subtitles/.nfo/artwork) adjacent to a downloaded episode. The Go import path wires the extras importer as `nil → noopExtrasImporter` (import_approved.go:155; main.go:2331). subtitlefiles/metadatafiles/extrafiles packages are persistence-only — no importer. So **subtitles + other extra files adjacent to a download are NOT imported** alongside the episode. Substantial subsystem port (SubtitleService + OtherExtraService + the ExtraService orchestrator + the import-time CanImportFile/ImportFiles chain) — NOT an autonomous-loop slice. FLAGGED for the operator.

## PHASE-2 FRESH AUDIT (J471, 2026-05-30) — import-time LocalEpisode aggregation

Explore agent diffed C# `IAggregateLocalEpisode` augmenters vs Go (ported AND wired into the import aggregation chain at main.go:1149).
- **7/7 aggregators ported; 5/7 were wired — 2-gap FIXED J471.** `AggregateReleaseGroup` + `AggregateReleaseHash` (aggregation.go:210-295) were ported + unit-tested but NEVER added to the production `episodeAggregators` chain. Verify-first: `LocalEpisode.ReleaseGroup` is set NOWHERE else, and `local_episode_to_file.go:73` copies it onto the imported `EpisodeFile` → every import got an EMPTY ReleaseGroup (broke the `{Release Group}` rename token, the release-group UI column/filter, and release-group custom formats) + empty ReleaseHash. Both are zero-dependency aggregators reading the already-parsed File/Folder/DownloadClient infos → appended to the chain (J471). The other 5 (Quality, Language, Episodes, ReleaseInfo, SubtitleInfo) were already wired.

## PHASE-2 FRESH AUDIT (J466, 2026-05-30) — download/import decision specs

Two Explore agents + one focused verification agent diffed C# `IDecisionEngineSpecification` (grab) + `IImportDecisionEngineSpecification` (import) vs Go.
- **Import-decision specs: 14/14 ported + wired — COMPLETE.** (FullSeason, Split, UnverifiedSceneNumbering, HasAudioTrack, MatchesGrab, NotSample, SameEpisodesImport, FreeSpace, NotUnpacking, AbsoluteEpisodeNumber, EpisodeTitle, AlreadyImported, MatchesFolder, Upgrade — all in main.go's importDecisionSpecs chain.)
- **Download-decision specs: ~26 wired; several genuinely absent. MonitoredEpisode → FIXED J466.** Verified findings:
  - ✅ **MonitoredEpisodeSpecification** — was missing → FIXED J466 (RSS sync grabbed unmonitored episodes). Reason codes pre-existed unused.
  - **NOT a gap: SeriesSpecification** — enforced elsewhere (parsingservice.findSeries leaves Episodes empty when the parsed series ≠ searched series → decision maker rejects UnknownEpisode).
  - **GENUINELY ABSENT (follow-up candidates, verify substrate before porting):**
    - ✅ **EpisodeRequestedSpecification** — FIXED J467. **`SeasonMatchSpecification` + `SingleEpisodeSearchMatchSpecification` — SKIPPED as marginal (J471 decision).** EpisodeRequested (J467) already rejects wrong-season/wrong-episode (such a release's episodes don't intersect the searched set). The ONLY distinct behavior is SingleEpisodeSearchMatch's "reject a FULL-SEASON pack returned during a single-episode search" (the pack contains the searched episode, so EpisodeRequested accepts it). A faithful port needs a search-KIND discriminator on `decisionengine.SearchCriteria` — C# dispatches on SeasonSearch/SingleEpisode/AnimeEpisode criteria SUBTYPES, but the Go base is unified and `dispatchSearch` (search.go) drops the typed criteria's outer SeasonNumber/EpisodeNumber/IsSeasonSearch. That's a criteria-interface refactor shared by 7 typed criteria + the indexer request generators — breakage risk for one narrow benefit. Deferred low-value vs cost.
    - **DelaySpecification** (RSS) — delay-profile wait/pending-release hold. Needs delay-profile + pending-release substrate; verify before porting (may be BLOCKED).
    - **HistorySpecification** (RSS) — reject re-grab at/below cutoff quality recently. Needs history-query + upgradable substrate.
    - ✅ **IndexerTagSpecification** — FIXED J468 (clean finder-injection port; substrate all pre-existed). **DelaySpecification** — decisionEngineDeps already wires delayProfiles, but it feeds ProtocolSpecification; the Delay *pending-hold* path needs the unported pending-release-queue substrate → verify/BLOCK. ✅ **ProperSpecification** — FIXED J469 (substrate all pre-existed; uses on-disk file revision + DateAdded, NOT history). ✅ **DeletedEpisodeFileSpecification** — FIXED J470 (substrate all pre-existed). **DelaySpecification / PendingSpecification** — pendingreleases pkg EXISTS (NOT substrate-blocked); these are complex SIDE-EFFECTING specs (route a release to the pending queue / hold for the delay-profile wait) — larger ports, deferred but feasible. **SceneMappingSpecification** — BLOCKED at decisionengine_wiring.go:227 on the scenemappings.Service signature.

## PHASE-2 FRESH AUDIT (J464, 2026-05-30) — health checks + housekeepers

Two Explore agents diffed C# `IProvideHealthCheck` + `IHousekeepingTask` impls vs Go (ported AND wired).
- **Health checks: 25/26 ported+wired; 1 gap = `DownloadClientStatusCheck` → FIXED J464.** Reason codes pre-existed unused; ported faithfully (sibling of indexer/importlist/notification status checks). Go also has an extra `SkyHookCheck` (intentional, no C# parallel).
- **Housekeepers: 30/31 ported+wired; 1 gap = `TrimLogDatabase` → FIXED J465.** Verify-first: `logs.Repo.Trim(ctx, now)` already existed (logs.go:194, deletes >7d rows, matches C# `LogRepository.Trim`) but was never wired into the housekeeping sweep ("method exists, not wired"). Ported `TrimLogDatabase.cs` as `internal/housekeeping/trim_log_database.go`, registered in houseSvc tasks only when `logRepo != nil` (the Go equivalent of the C# `LogDbEnabled` gate). C# post-trim `Vacuum()` deferred (no logs.Repo.Vacuum surface; space-reclaim only). All other 30 housekeepers ported + registered.

**PHASE-2 health-check + housekeeper audit DRAINED — both gaps closed (J464 + J465).**

> **Fix status (updated through J429):**
> - ✅ **G1** search→grab — FIXED J423 (search adapters now feed the grabber).
> - ✅ **G2** CleanUpRecycleBin stub — FIXED J421 (dep wired).
> - ✅ **G3** queue/details empty — FIXED J422 (real lister).
> - ✅ **G4** provider fields[] on GET — FIXED J425 (inverse settings→fields mapper; 4 families). Follow-up: customformat WRITE-path persistence shape FIXED J431 (was a CRITICAL bug — POST/PUT-with-specs 500'd through real svc; now stores `{type,body}` envelope). G4-followup fully drained: customformat GET read-half (J432), autotagging specs (J433), `ImplementationName` (J443, all 5 families).
> - ✅ **G7** import-list sync inert — FIXED J424 (providers blank-imported) + J444 (IDTranslator wired — non-TVDB-id items now resolve via metadata source). Fully drained.
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

## Fresh audit findings (J445, after G4/G7/G10 drained)

Method: full-repo gates (all green) + a command-registry/scheduled-task/event-subscriber diff vs C# (Explore agent) + nil-deps + wired-stub probes.

- **handlers.Deps**: ALL fields non-nil — no `NewXxx(nil)` no-op stubs remain (the old G2 class is gone). Clean.
- **Scheduled tasks**: all 11 C# scheduled commands wired (RefreshMonitoredDownloads, MessagingCleanup, ApplicationUpdateCheck, UpdateSceneMapping, CheckHealth, RefreshSeries, Housekeeping, CleanUpRecycleBin, ImportListSync, Backup, RssSync). Clean.
- **G11 (FIXED J445)**: `EpisodeSearch` + `SeasonSearch` commands (UI per-episode/per-season search buttons) were unregistered → unknown-command. Wired reusing the existing EpisodeSearcher/SeasonSearcher adapters.
- Remaining C# commands NOT in the go registry are all out-of-scope: `DeleteLogFiles`/`DeleteUpdateLogFiles` (manual admin log deletion — low value, no scheduled use), `ApplicationUpdate` (install — triggered by update-check; install path is operator-territory), `ResetApiKey` (SAFETY-RAILED — do not touch), `UnknownCommand`/`TestCommand` (framework internals). None are scheduled or core-flow.
- **Wired stub adapters**: none in production (emptyTagDetailLister exists only as a nil-fallback; the real tagdetail aggregator is wired J430).
- **Gated (unchanged)**: Q3 ManualImport completion events + Q1 metadata rename/downloads-processed tail — both need the unported Phase-4 download-completion / per-series-rename event publishers.

## Fresh audit findings (J446)

- **DeleteLogFiles/DeleteUpdateLogFiles**: FIXED J446 (commands wired + disk.EmptyFolder).
- **G12 — v5 SeriesResource fields — CORRECTED J447: was a STALE-DOC FALSE ALARM.** v5 GET /series ALREADY emits `AniListIds`/`MalIds`/`OriginalCountry`: the shared `apiv3.SeriesResource` carries them (series.go:132/133/145) and `apiv3.ToSeriesResource` populates them from the row (series.go:600/601/613, omitempty); v5 aliases that struct + only strips `languageProfileId`. The v5-package doc claiming DEFERRED was stale (now fixed); pinned by `TestV5SeriesResource_EmitsV5OnlyModelFields`. RESIDUAL (LOW, shared-struct tradeoff, unobserved by empty-DB goldens): (a) v3 GET /series ALSO emits those 3 v5-only fields on populated rows (over-emission vs C# v3) — accepted; (b) `IsExcluded` (v5-only, set only on the series/lookup path, C# SeriesLookupController:53) is not on the shared struct — a v5-lookup-only follow-up. The clean comprehensive fix for both is de-aliasing v5 series to its own resource type (larger; not yet warranted for two LOW residuals). Other v5 resources (Episode/Queue/History/Season) merit the same field-divergence spot-check before believing any 'missing field' claim — verify against the shared struct first.

## AUTONOMOUS EXHAUSTION (J448, campaign J429–J447)

After 19 slices, the clean / in-scope / ungated / non-cosmetic autonomous work is drained. A fresh full audit (tiers 1–6) finds only gated or LOW-cosmetic items.

**DONE this campaign (J429–J447):** G5 rename previews; G9 tag/detail aggregator; G4-followup (customformat write-envelope CRITICAL fix + read projection, autotagging specs, ImplementationName all 5 families); T1 flaky e2e; Q1 metadata writer (import/cover/scan events); ReleaseResource full decision projection incl. SceneMapping; series/editor moveFiles; queue-remove blocklist Indexer/Size; G10 indexer Test/Action (all 10 providers); G7 IDTranslator; G11 EpisodeSearch+SeasonSearch commands; DeleteLogFiles/DeleteUpdateLogFiles + disk.EmptyFolder. Plus verified-complete: Tier-2 projections (Release/Queue/History/Blocklist/Series stats+images), Tier-3 v5 routes (0 missing), Tier-4 write paths, all scheduled tasks + handlers.Deps (no nil stubs), all command-registry production commands, all v5 resource fields.

**GATED (need unported Phase-4 substrate — NOT autonomously portable):**
- Q3 — ManualImport completion events: **DONE (J449 producer + J450 manual arm).** `DownloadCompletedEvent` producer landed J449 (auto-import path). J450 wired the ManualImport handler's completion arm: `UntrackedDownloadCompletedEvent` (defined + published per series/season group) for untracked imports + `DownloadCompletedEvent` (state→Imported, all-imported gate) for tracked imports — ports `ManualImportService.cs:576-625`. Tracked-download **output-folder cleanup** (C# `:606-615`) landed J451 — `ManualImportFolderCleaner` (diskProv FolderExists/DeleteFolder + downloadedimport.ShouldDeleteFolder + Item.CanMoveFiles) wired into the handler's `finalizeTracked` loop, real-temp-dir tested. **Q3 fully closed: J449 producer + J450 events + J451 cleanup.**
- Q1 tail — metadata on rename / downloads-processed: **DONE (J452+J453+J454).** All six C# ExtraService IHandle events are now wired in `subscribeMetadataWriters`: EpisodeImported→CreateAfterEpisodeImport (J435), CoversUpdated→CreateAfterMediaCoverUpdate (J435), SeriesScanned→CreateAfterSeriesScan (J435), EpisodeFolderCreated→CreateAfterEpisodeFolder (J452), SeriesRenamed→MoveFilesAfterRename (J453), DownloadsProcessed→CreateAfterEpisodesImported (J454, with the `_seriesWithImportedFiles` accumulator ported as `importedSeriesAccumulator`). Residual (LOW, orthogonal): C# `RemoveEmptySubfolders` disk cleanup in the rename path (`RenameEpisodeFileService.cs:173`) — needs a disk dep, no metadata impact.
- Provider Test for non-Newznab-protocol indexer families beyond the 10 registered — N/A (all 10 ported providers registered J441-442).

**LOW / accepted (shared-struct tradeoffs, unobserved by goldens; fix cost > value):**
- v3 GET /series over-emits the 3 v5-only fields (malIds/aniListIds/originalCountry) on populated rows — would need a v3-only JSON strip on the hot path.
- `IsExcluded` (v5-only, set only on series/lookup) absent from the shared SeriesResource.
- The clean comprehensive fix for both = de-alias v5 series to its own resource type (larger; not warranted for two LOW residuals).

**RESUMING LATER:** the next genuinely-impactful work is the Phase-4 download-completion pipeline (`CompletedDownloadService` + its event publishers), which unblocks Q1 tail + Q3 — but that's a substantial subsystem port, not an autonomous-loop slice. Flag to the operator.

## RENAME PATH RemoveEmptySubfolders — DONE J463 (Phase-1 #4)

The Go rename handler (`internal/commands/handlers/rename.go`) published the rename events but never pruned the now-empty directories files moved out of. C# `RenameEpisodeFileService.RenameFiles:173` calls `_diskProvider.RemoveEmptySubfolders(series.Path)` once after the per-file loop (gated on `renamed.Any()`), before publishing SeriesRenamedEvent. FIXED J463: optional `EmptySubfolderRemover` dep on RenameDeps (*disk.Provider), called on the `len(renamed) > 0` branch before the event publish; wired `Disk: diskProv` at the cmd/sonarr construction site. **Phase-1 follow-ups #1–4 all drained (J460–J463).**

## EVENT PRODUCER/CONSUMER PATTERN AUDIT — DONE J462 (Phase-1 #3)

Explore fan-out over the whole tree cross-referencing every `messaging.Subscribe` consumer against every non-test `messaging.Publish` producer (incl. optional-publisher adapters wired in cmd/sonarr), plus every `internal/*` service with a `Subscribe(bus)` method vs cmd/sonarr call sites.

- **Class A (dead consumers — subscribed, never produced): 1 found + FIXED J462.** `trackeddownloads.DownloadIgnoredEvent` had two subscribed handlers (`history.HandleDownloadIgnored` main.go:1089, `downloadhistory.HandleDownloadIgnored` main.go:1098) but NO production publisher. Root cause: the C# queue-delete no-flags "ignore" path (`QueueController.Remove` → `IgnoredDownloadService.IgnoreDownload`, `IgnoredDownloadService.cs:26-52`) was unported on the producer side. FIXED by `queueRemoveAdapter.ignore()` (cmd/sonarr/queue_remove_wiring.go) publishing the event on the no-flags branch. See J462.
- **Class B (unwired services — Subscribe(bus)/Handle never called): 0 found.** All services exposing `Subscribe(bus)` are wired (`config.Provider` main.go:625, `namingconfig.Service` :699, `healthcheck.Service` :2081, `notifications.Service` :2680); all Handle* arms are subscribed at their wiring sites.
- Verified NOT dead (published via optional-publisher adapters, not direct messaging.Publish): EpisodeFileRenamedEvent/SeriesRenamedEvent (renameEventPublisher main.go:3520), EpisodeImportedEvent (importEventPublisherAdapter :2340), DownloadCanBeRemovedEvent (:3119), DownloadCompletedEvent (:3110), EpisodeFolderCreatedEvent (downloadedimport_wiring.go:901), EpisodeGrabbedEvent (process_decisions.go:478).

Pattern audit DRAINED — only the one DownloadIgnoredEvent gap, now closed.

## QUEUE-DELETE changeCategory+blocklist × HUB FAILED-ARM — VERIFIED FAITHFUL J461 (Phase-1 #2)

Traced the manual queue-delete (`cmd/sonarr/queue_remove_wiring.go`: `applyClientSideEffects` + `blocklist`) against C# `QueueController.Remove` (`QueueController.cs:333-372`), `FailedDownloadService.MarkAsFailed`, and `DownloadEventHub.Handle(DownloadFailedEvent)` (`DownloadEventHub.cs:29-47`). **No bug — faithful to C#.** The J457 hub failed-arm does NOT remove on every DownloadFailedEvent (the J457 doc-comment framing "handles every DownloadFailedEvent regardless of source" is about the *source* gate, not the side-effect gate); `HandleDownloadFailed` gates on `td.DownloadItem.CanBeRemoved` AND the client's `RemoveFailedDownloads` flag — identical to C#. changeCategory+blocklist therefore runs MarkItemAsImported (recategorize) then publishes DownloadFailedEvent; the hub removes-from-client only when the client is configured to remove failed downloads AND the item is removable. Pinned by `cmd/sonarr/queue_remove_hub_interaction_test.go` (REAL adapter + REAL hub on one bus, 3 gating cases). No follow-up.

## NOTIFICATION PROVIDER COVERAGE SWEEP — DONE J460 (Phase-1 #1)

**Swept all 25 providers after the J459 dispatch wiring.** Verify-first result: the dispatch path is robust — every one of the 22 HTTP providers self-heals a nil doer to `http.DefaultClient` (so a mis-typed `deps.HTTPClient` degrades the 30s timeout but never silently "sends nothing"; the memory's "nil doer → sends nothing" fear is mitigated by the self-heal), `email`/`customscript` take no HTTP dep, and Trakt's `NotificationsRepo` persister + the status svc are all wired. **One real gap found + fixed:** `deps.ConfigProvider` was the app-config `config.Provider`, which has NO `InstanceName` accessor. C# Discord author (`Discord.cs:43`, also :146/:260/:367) and Telegram title suffix (`Telegram.cs:23,28`) read `_configFileProvider.InstanceName`; the Go providers narrow `interface{ InstanceName() string }` off deps.ConfigProvider → the assertion always failed → both always emitted the literal "Sonarr" instead of the configured instance name. Fix (J460): `notificationConfigAdapter` (cmd/sonarr/main.go) adapts the configfile `InstanceName(ctx)(string,error)` → `InstanceName() string` and is wired into notificationDeps (replacing configProv; nothing else reads ConfigProvider). Regression coverage: `notifications/provider_coverage_test.go` (catalog == exactly 25 + construct-all-through-real-registry sweep w/ production deps + Discord author behavioral pin), `telegram/instance_name_test.go` (internal applyBrandAndInstance pin), `cmd/sonarr/notification_config_adapter_test.go` (adapter unit + compile-time surface guard). NO other provider gap remains.

## NOTIFICATION DISPATCH SERVICE — WIRED J459 (was unwired; found J456)

**RESOLVED J459.** `notifications.Service` is now constructed + `Subscribe(bus)`d in serve() (main.go, after httpClient): Factory over the populated `notifications.Default()` registry + Dependencies with a dedicated `*http.Client{30s}` (providers' httpDoer = std `Do(*http.Request)`), configProv, notificationStatusSvc (both blocked-filter + recorder), notificationsRepo. End-to-end verified by `dispatch_integration_test.go` (real registry → concrete Webhook provider HTTP send). Event-driven notifications (grab/import/download-completed/health) are now LIVE. Historical context below.

`notifications.Service` (the event-driven dispatch orchestrator — OnGrab / OnDownload / OnImportComplete / OnSeriesAdd / health / etc.) WAS (pre-J459) **never constructed or subscribed** in cmd/sonarr. Only `notificationsRepo` (the CRUD/API surface), `notificationStatusSvc`, and the schema/test path are wired. Evidence: `notifications.NewService` appears nowhere in cmd/sonarr (grep); no `notificationsSvc.Subscribe(bus)` call exists; the main.go:2734 comment claiming "notifications.Service … subscribed elsewhere" is STALE. **Consequence: the running Go app fires NO event-driven notifications at all** (email/Discord/webhook on grab/import/health). The per-event handlers (incl. the J456 OnImportComplete pair) are all defined + unit-tested but dormant.

This is a sizeable, cross-cutting wiring slice (construct the Service with its Factory + recorder + provider Dependencies, then `Subscribe(bus)`), and activating it would light up many handlers at once — it needs its own verification pass (provider construction, dedupe vs SignalR, per-handler smoke). OUT OF the download-completion arc. **Flagged for the operator as the next high-impact item.**

## PHASE-4 DOWNLOAD-COMPLETION ARC (J449+ campaign — re-opened)

Slice-1 arc map (verified against actual Go code, not doc-comments — C# `CompletedDownloadService.cs` + `DownloadProcessingService.cs`). The arc turned out **smaller than J448 feared**: the import gate, state→Imported transition, and the consumer are ALREADY ported; only the event *producer* + cleanup + a few event *types* are missing.

| # | Behavior | C# anchor | Go state | Verdict |
|---|----------|-----------|----------|---------|
| 1 | Import-on-completion gate | `CompletedDownloadService.cs:66-126` | `downloadprocessing/service.go:142-160` (`shouldImport`: state==Downloading + client Completed + matched series) | DONE (J399). No ValidatePath / grabbed-history check — assumes pre-parsed RemoteEpisode (documented). |
| 2 | Tracked-download state → Imported (all-imported gate) | `CompletedDownloadService.cs:212-236` (`VerifyImport`) | `downloadprocessing/service.go:164-185` (`importOne`: `allImported(results)` → `td.State = StateImported`) | DONE (J399). No history-fallback edge case (`:248-279`). |
| 3 | **DownloadCompletedEvent PRODUCER** | `CompletedDownloadService.cs:229-234, 275-276` | **MISSING** — `importOne` sets Imported but never publishes; event fully defined `trackeddownloads/events.go:190-207` | **J449 target** — narrow optional `CompletionPublisher`, mirrors the `FailurePublisher` (J400) pattern. |
| 4 | DownloadCompletedEvent CONSUMER | `DownloadHistoryService` Handle | `downloadhistoryhandlers/download_completed.go:72` (→ DownloadImported row), wired `main.go:1108-1110` | DONE (J140) — reads only TrackedDownload; ignores EpisodeFiles/Release. |
| 5 | Output-folder cleanup (`ShouldDeleteItemFolder` / RemoveCompletedDownloads + DownloadEventHub MarkItemAsImported) | `DownloadProcessingService.cs:35-45` + `DownloadEventHub.cs:29-85` | DONE (auto path) — manual-import cleanup J451; DownloadEventHub completion arm J455 (MarkItemAsImported + RemoveItem/StopTracking when RemoveCompletedDownloads) + failed arm J457 (RemoveItem/StopTracking when RemoveFailedDownloads). DownloadCanBeRemovedEvent + the RemoveCompletedDownloads sweep landed J458 (`sweepRemovableImports` at end of each pass + `HandleDownloadCanBeRemoved` hub arm) — handles items still seeding/locked at completion. **DownloadEventHub fully ported (completed J455 + failed J457 + can-be-removed J458).** |
| 6 | DownloadsProcessedEvent (marker, end-of-pass) | `DownloadsProcessedEvent.cs`; `DownloadProcessingService.cs:80` | NOT defined / not published | define + publish at end of `ProcessTrackedDownloads` (later slice — needed for Q1 tail). |
| 7 | UntrackedDownloadCompletedEvent | `UntrackedDownloadCompletedEvent.cs` | NOT defined | define for Q3 (ManualImport untracked arm). |
| 8a | SeriesRenamedEvent | `SeriesRenamedEvent.cs:7-16` | NOT defined / not published | define + publish from rename path (Q1 tail). |
| 8b | EpisodeFolderCreatedEvent | `EpisodeFolderCreatedEvent.cs:6-19` | DEFINED + PUBLISHED `episodefilemoving/episodefilemoving.go:140,636` + SUBSCRIBED by metadata (J452) | DONE — metadata `CreateAfterEpisodeFolder` now driven by the event (ports `ExtraService.cs:142-150`). |
