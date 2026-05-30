# Sonarr Go-Port — Gap Analysis (audit @ J419, 2026-05-29)

## RESUMED (J494+, 2026-05-30) — operator picked GAP1 + GAP2

- **GAP1 — series-delete on-disk file cascade (recycle-bin-aware + guarded).** Premise was partly STALE (VERIFY-FIRST): the Go cascade ALREADY deletes the folder (host_wiring.go:982), but via a RAW permanent `disk.DeleteFolder(path,true)` that ignores a configured recycle bin AND lacks the C# parent/equal-path safety guard.
  - ✅ **J494 — recyclebin.Service.DeleteFolder** (slice 1/2). Ported `RecycleBinProvider.DeleteFolder` (RecycleBinProvider.cs:41-68): recycle-bin move-or-permanent-delete + recycle-time mtime refresh. NEW disk.Provider.FolderSetLastWriteTime; extended recyclebin.DiskOps. 4 tests.
  - ✅ **J495 — wire DeleteCascade + guards** (slice 2/2). seriesCascadeAdapter.DeleteCascade now routes the folder delete through recyclebin.Service.DeleteFolder + the C# `HandleAsync(SeriesDeletedEvent)` parent/equal-path guard (`safeToDeleteFolder` + new `pathEquals`); stale series.go:1180 doc fixed. 6 tests. **GAP1 DONE.**
- ✅ **GAP2 — RemoveEmptySubfolders after EpisodeFileDeletedEvent** (J496). Ported `MediaFileDeletionService.Handle(EpisodeFileDeletedEvent)` (MediaFileDeletionService.cs:215-243); NEW cmd/sonarr/mediafile_deleted_folder_subscriber.go (config-gated, reason≠MissingFromDisk, walk-up-prune + empty-root delete), subscribed OrderLast. 6 tests. **GAP2 DONE.**

## AUTONOMOUS EXHAUSTION (J493, 2026-05-30) — campaign J484–J492 closed

Resumed-after-J483 campaign: **9 slices (J484–J492) + 3 fresh-audit rounds**, all gates green, loop now STOPPED.

**DONE (J484–J492):**
- **Delay/Pending subsystem, end-to-end (J484–J490):** persist the Pending bucket (J484 — `ProcessDecisions` never called `AddMany`); maintenance trio RemoveGrabbed + DeleteBySeriesIDs (J485) + RemoveRejected (J490 — written-but-unwired); DelaySpecification (J486); PendingSpecification (J487); pending-queue DISPLAY projection so delayed releases appear in Activity→Queue (J489 — C# `queue.Concat(pending)` merge the Go adapter omitted).
- **SceneMappingService on-demand UpdateMappings handlers (J491)** — unwired + the doc falsely claimed wired; fresh/restarted installs had empty TheXEM mappings until the daily task.
- **SceneMappingSpecification (J488)** — the "BLOCKED on scenemappings.Service signature" label was stale; the spec is logger-only.
- **HistorySpecification (J492)** — last missing download-decision spec; RSS grab→import re-grab guard. Substrate ("needs history+upgradable") was stale — all present.

**REMAINING — none is a clean high-value ungated slice (why the loop stopped):**
- **GAP1 (HIGH-impact, DEFERRED + DESTRUCTIVE + needs substrate):** series-delete with `deleteFiles=true` leaves the on-disk folder in place. C# `MediaFileDeletionService.HandleAsync(SeriesDeletedEvent)` → `RecycleBinProvider.DeleteFolder(series.Path)`. Go documents this as DEFERRED (series.go:1183/1318); needs a NEW `recyclebin.Service.DeleteFolder` (only `DeleteFile` exists) + the SeriesDeletedEvent file-delete cascade. Flagged for the operator — destructive (deletes user media), so not autonomously rushed.
- **GAP2 (LOW/cosmetic, clean+ungated):** `MediaFileDeletionService.Handle(EpisodeFileDeletedEvent)` prunes empty parent folders (config `DeleteEmptyFolders`) after a file delete. Go has no handler. Substrate exists (config.DeleteEmptyFolders + disk.RemoveEmptySubfolders) — a small J463-style slice if the operator wants it; low value (empty folders left after manual delete).
- **GAP3 (LOW):** `UpdateEpisodeFileService.Handle(SeriesScannedEvent)` sets on-disk file mtime to air-date per config `FileDate` (defaults to None → no-op for most users); needs a disk mtime-change helper.
- **GAP4 (GATED):** import-list clean-library on list-delete (`ImportListSyncService.HandleAsync(ProviderDeletedEvent<IImportList>)`) — gated on the deferred CleanLibrary substrate (config.ListSyncLevel/importlistitems unported, sub-arc I4c).
- **MARGINAL:** SeasonMatchSpecification + SingleEpisodeSearchMatchSpecification (risky SearchCriteria criteria-discriminator refactor; EpisodeRequested J467 covers the bulk).
- **NOTIFICATION (gated-deferred):** OnRename / OnApplicationUpdate / OnManualInteractionRequired + 3 ProcessQueue drains — gated on update.Service / trackeddownloads manual-emit / batch-rename event.

**VERIFIED CLEAN this campaign (no gaps):** event subscribe-vs-publish sweep; `any`-typed nil-cast deps (metadata/importlists/notifications/downloadclients); trackeddownloads import→history/downloadhistory correlation; API v3/v5 write-path side-effects; registry counts (import-decision specs 14/14, aggregators, health checks, housekeepers, scheduled tasks, commands, provider catalogs).

**KEY LESSON (reinforced ~17×):** the dominant gap class remains "silently MISSING from a list/registry/deps-bundle/subscription/merge in cmd/sonarr" — a Service constructed but a method never called (J484/J490 AddMany/RemoveRejected), a handler written+tested but never Subscribe'd (J485/J491), a feature labelled BLOCKED with no blocker (J488), a read-side merge the adapter omits (J489), a doc claiming wired when it isn't (J491), a spec missing from the chain (J492). Doc-comments/"needs substrate"/BLOCKED notes are stale in BOTH directions — always grep the real wiring.

## DELAY/PENDING SUBSYSTEM (J484+, 2026-05-30) — resumed after exhaustion

Operator picked the next-largest remaining item (DelaySpecification + PendingSpecification). **VERIFY-FIRST VERDICT: NOT BLOCKED.** The "complex side-effecting substrate" framing was overbroad — `internal/pendingreleases` is heavily built (Service.Add/AddMany; remove.go RemoveGrabbed/RemoveRejected/OldestPending/DeleteBySeriesIDs/RemoveByQueueID; pendingreleaseshandlers; CleanupDownloadClientUnavailable housekeeper; api/v3 queue/grab re-hydration + queue/status count). The real gap was a classic SILENT-DROP: `download.ProcessDownloadDecisions.ProcessDecisions` builds the Pending bucket but the C# `AddMany` flush (`ProcessDownloadDecisions.cs:119-121`) was DEFERRED → the table was never populated; the entire subsystem was inert.

Decomposition (multi-slice):
- ✅ **J484 — persist the Pending bucket** (PreparePending `:178-194` + AddMany `:119-122`). Optional `PendingWriter` dep + SetPendingWriter; reason mapping Delay/DownloadClientUnavailable/Fallback; main.go now constructs the full `pendingreleases.Service`. Activates queue display + restart survival for temp-rejected (MinimumAge) + client-unavailable + failed-dispatch releases.
- ✅ **J485 — wire pendingreleaseshandlers.** EpisodeGrabbedEvent→RemoveGrabbed + SeriesDeletedEvent→DeleteBySeriesIDs now subscribed in main.go (were written + unit-tested but never subscribed). Integration test proves the real Service clears the row on each event. **RemoveRejected (RssSyncCompleteEvent) STILL DEFERRED:** the Go RssSyncCompleteEvent carries only counts, not the rejected decisions' (Title,PublishDate,Indexer) tuples — needs event enrichment first. Minor cleanup (purges a now-permanently-rejected release from pending); low priority.
- ✅ **J486 — DelaySpecification** (`RssSync/DelaySpecification.cs`). Ported + wired into buildDecisionEngineSpecs. Temporary-reject spec: delay-profile wait, all bypasses (proper/repack via IsRevisionUpgrade, highest-quality via NEW ProfileView.LastAllowedQuality, custom-format-score, OldestPending age). NEW oldestPendingAgeAdapter (unmarshals pending ReleaseInfo→AgeMinutes). 11 tests. The Go chain previously had NO delay enforcement — delay profiles were ignored. **NOW the dominant real-world Delay path works** (temp-reject → J484 persist → re-eval).
- ✅ **J487 — PendingSpecification** (`RssSync/PendingSpecification.cs`). Ported + wired. Blocks a pushed/non-user-search release when a matching release is already pending (RSS + user-search bypass). **NO GetPendingQueue projection needed** — reuses the J486 PendingReleaseAgeReader `found` bool. 6 tests.

- ✅ **J492 — HistorySpecification** (2nd fresh-audit find; the last genuinely-missing download-decision spec). RSS-only Permanent gate: rejects re-grabbing/downgrading an episode whose most-recent history event is a Grabbed (recent <12h, or CDH off) that already meets the profile — guards the grab→import window. All substrate pre-existed (ParseCustomFormatForHistory, CutoffNotMet/IsUpgradable, 9 reason codes, historyByEp). NEW customFormatForHistoryAdapter. 8 tests. **Download-decision spec chain now == C# set minus marginal SeasonMatch/SingleEpisodeSearchMatch.** 2nd-audit otherwise clean (TestCommand/UnknownCommand framework-only; SeriesSpecification documented not-a-gap; ManualInteraction/TrackedDownloadsRemoved = gated LOW monitoring-refresh optimization; history↔downloadhistory correlation wired).
- ✅ **J491 — SceneMappingService on-demand UpdateMappings handlers** (fresh-audit find). IHandle<SeriesRefreshStarting/SeriesAdded/SeriesImported>→UpdateMappings were unwired + the doc falsely claimed wired; fresh/restarted installs had empty scene mappings until the daily task. Added updatedAfterStartup gate + 2 handlers + WithClock, subscribed 3 events. 4 tests. **Fresh audit otherwise: nil-cast deps CLEAN; 6 notification handlers gated-deferred; other event consumers gated on unported subsystems.**
- ✅ **J490 — RemoveRejected on RssSyncCompleteEvent.** Service.RemoveRejected existed+tested but was never called (written-but-unwired). Enriched the event with the rejected match-tuples (via NEW download.RejectedInputFrom) + subscribed the closure. Completes the pending-maintenance trio (RemoveGrabbed/DeleteBySeriesIDs/RemoveRejected). The "LOW — needs event enrichment" note was right about the mechanism but it was a clean ~1-slice port. 3 tests.
- ✅ **J489 — pending-queue DISPLAY projection.** C# QueueController.GetQueue merges `GetPendingQueue()` into /queue; the Go /queue listed tracked downloads only, so J484's persisted delayed releases were invisible in Activity→Queue. NEW internal/pendingqueue.Projector (rehydrate Series+Episodes, ect/timeLeft, Status from Reason) wired into all 4 queue routes (v3+v5 × queue+details) via `withPending`. 6 tests. Deferred (display-only): best-quality dedup + DownloadClient-name lookup. **Delay/Pending subsystem + UI display now FULLY COMPLETE.**

**DELAY/PENDING ARC COMPLETE (J484–J487).** The verify-first verdict held: the subsystem was NOT blocked — its substrate (pendingreleases Service + remove + handlers + housekeeping + queue/grab) all pre-existed; the gaps were (a) ProcessDecisions never persisting its Pending bucket, (b) the maintenance handlers never subscribed, (c) no Delay/Pending specs in the chain. All four closed. Remaining pending follow-ups are LOW: RemoveRejected on RssSyncCompleteEvent (event needs enriching with the rejected match-tuples) + a full GetPendingQueue RemoteEpisode projection for the queue/pending per-row DISPLAY (count already wired).

## AUTONOMOUS EXHAUSTION (J475, 2026-05-30, campaign J460–J475)

After 16 slices + a fresh full audit across every high-yield dimension, the clean / in-scope / ungated / non-cosmetic / non-large autonomous work is **drained**. The loop is stopped (no further wakeup scheduled) per the operator's exhaustion clause.

**DONE this campaign (J460–J475):**
- Phase-1 follow-ups (J460–J463): notification InstanceName fix (Discord/Telegram) + 25-provider coverage; queue-delete×hub faithful pin; **DownloadIgnoredEvent producer** (dead-consumer); rename RemoveEmptySubfolders.
- Subsystem audits (each found+fixed its gaps): health checks (J464 DownloadClientStatusCheck), housekeepers (J465 TrimLogDatabase), notification providers (J460), metadata writers (J472 MapCoversToLocal + J473 EpisodeLookup/FolderLister — covers/episode/season images), import aggregators (J471 ReleaseGroup/ReleaseHash).
- Download-decision specs: 5 ported (J466 MonitoredEpisode — RSS grabbed unmonitored; J467 EpisodeRequested; J468 IndexerTag; J469 Proper; J470 DeletedEpisodeFile).
- Event consumers: trackeddownloads series-cache-refresh fully ported (J474 SeriesEdited/Deleted + J475 SeriesBulkEdited/Added).
- **Verified CLEAN (no gaps):** provider deps-bundle silent-nil sweep (importlists/indexers/downloadclients); all user-facing commands registered; all 11 scheduled tasks registered; import-decision specs (14/14); V3/V5 API write/action endpoint side-effects (command dispatch, release grab, queue grab, series editor, provider Test/Action, manual import, series import — all execute, no stubs).

**REMAINING (all out-of-scope for the autonomous loop):**
- **LARGE / operator-flagged:** the extras-IMPORT subsystem (SubtitleService + OtherExtraService + the ExtraService orchestrator) — subtitles/sidecars adjacent to a download are not imported. Substantial subsystem port.
- **COMPLEX / deferred (side-effecting substrate):** DelaySpecification + PendingSpecification (route a release to the pending-release queue / hold for the delay-profile wait); the documented release-push / manual-import follow-up arms (ShouldOverride, full ImportDecisionMaker pipeline rejections, indexer/dc name→id resolve) — tracked, not silent stubs.
- ~~**BLOCKED:** SceneMappingSpecification~~ → **FIXED J488.** The "blocked on scenemappings.Service signature" label was STALE — the C# spec is logger-only (reads Subject.SceneMapping, no service dep). Ported + wired.
- **MARGINAL (skipped, documented):** SeasonMatchSpecification + SingleEpisodeSearchMatchSpecification (EpisodeRequested J467 covers the bulk; faithful port needs a risky SearchCriteria criteria-discriminator refactor for one narrow case).
- **LOW:** trackeddownloads EpisodeInfoRefreshedEvent handler (removed-episode-id match, rare); DownloadMonitoring event-driven refresh (optimization over the 1-min scheduled task); v3-over-emits-v5-fields shared-struct tradeoffs.

**KEY LESSON (this campaign):** the highest-yield gap class by far was "an entry/field SILENTLY MISSING from a list / registry / deps-bundle assembled in cmd/sonarr" — especially non-panicking `any`-field type-asserts that yield nil and silently disable a feature (J459 dispatch, J460 ConfigProvider, J462 producer, J464/J465 registry entries, J471 aggregators, J472/J473 metadata deps). Audit every such assembly site. The port's DEFERRED/"unported" doc-comments are SYSTEMATICALLY STALE — verify against code first.

## PHASE-2 CONFIRMATION AUDIT (J474, 2026-05-30) — commands / scheduled tasks / event consumers

Broad maturity check (Explore). **Commands: all user-facing C# commands registered in Go** (out-of-scope: ResetApiKey, ApplicationUpdate install, framework Test/Unknown). **Scheduled tasks: all 11 registered.** **Event consumers:** found `TrackedDownloadService`'s `IHandle<SeriesEditedEvent>/<SeriesDeletedEvent>` (+ SeriesAdded/BulkEdited/EpisodeInfoRefreshed) cache-refresh handlers UNPORTED.
- **FIXED J474 (SeriesEdited + SeriesDeleted):** the tracker keeps the cached RemoteEpisode (existing-item arm) + the queue renders from it → after a series rename/delete the queue/activity view showed the stale/dangling title permanently. NEW `RefreshTrackedForSeries` re-resolves matching tracked downloads (ParseTitle+Map) on those events. The doc's "unported event shapes" note was STALE (events published at series/mutations.go:355,490).
- ✅ **SeriesBulkEdited + SeriesAdded handlers — FIXED J475** (completes the family). EpisodeInfoRefreshed handler remains LOW-deferred (removed-episode-id match shape, rare). Also DownloadMonitoringService's event-driven refresh triggers (EpisodeGrabbed/Imported→refresh) are an optimization over the 1-min RefreshMonitoredDownloads scheduled task; PendingReleaseService handlers are part of the deferred Delay/Pending subsystem.

## PHASE-2 FRESH AUDIT (J472, 2026-05-30) — metadata consumers + extra-file importers

Two Explore agents.
- **Metadata writers: 4/4 ported + registered (Xbmc/Roksbox/Wdtv/Plex). GAP: `MapCoversToLocal` dep UNWIRED → FIXED J472.** The provider constructors cast `deps.MapCoversToLocal.(CoverPathLookup)` (non-panicking); main.go's metadataWriterDeps never set it → series cover images (folder.jpg) silently not written for Xbmc/Roksbox/Wdtv. Fixed by `MapCoversToLocal: mediaCoverSvc` (mediacover.Service.GetCoverPath matches the interface). Construct-seam pin test.
  - ✅ **EpisodeLookup + FolderLister deps — FIXED J473.** Added both as `any` fields on metadata.Dependencies; cast in xbmc/roksbox/wdtv constructFromDependencies; wired `EpisodeLookup: episodesRepo` (*episodes.Repo.AllByFileID) + `FolderLister: diskProv` (*disk.Provider.GetDirectories). Episode + season images now emit. Metadata-writer subsystem FULLY wired (covers J472 + episode/season J473).
- **Extra-file IMPORT subsystem: PORTED + WIRED LIVE J476-J479** (was the LARGE flagged gap; the operator's "resume" picked it up). NEW `internal/extras` package: foundation (J476: extension sets, transfer seam, importFile helper), Manager iface + OtherExtraService (J477), SubtitleService (J478), ExtraService orchestrator (J479) — wired into ImportApprovedEpisodes via cmd/sonarr/extras_wiring.go (replaced noopExtrasImporter). Subtitles/.nfo/artwork companions now import next to the episode file on the auto-import path (gated by config.ImportExtraFiles, default off; ExtraFileExtensions default "srt"). Remaining LOW follow-ups: ~~post-rename companion moves~~ DONE J480-J481 (subtitlefiles.AllBySeriesID + Manager.MoveFilesAfterRename + Other/Subtitle impls + ExtraService.MoveFilesAfterRename, wired into the import script-rename arm + a SeriesRenamedEvent subscription — subtitles/.nfo now follow renamed episodes). ~~Still LOW/deferred: the script-import ImportExtraFiles arm, file-permissions.~~ BOTH DONE: J482 (ImportExtraFiles → ExistingExtraService: companions a script left are matched to their EpisodeFile via parsingSvc + tracked without moving) + J483 (file-permissions → SetFilePermissions on each imported companion via mediaFileAttrsSvc). **EXTRAS-IMPORT SUBSYSTEM FULLY COMPLETE (J476-J483).** All three ExtrasImporter seam methods (ImportEpisode, MoveFilesAfterRename, ImportExtraFiles) are live; only the cross-cutting MetadataService-import-arm is intentionally a C# no-op. The MetadataService import-arm is a C# no-op (CanImportFile=false), so nothing to port there. Original gap note (now historical):

- ~~**Extra-file IMPORT subsystem: LARGE GAP.**~~ C# `ExtraService` orchestrates 3 `IManageExtraFiles` (MetadataService order 0, SubtitleService order 1, OtherExtraService order 2) to import sidecar files (subtitles/.nfo/artwork) adjacent to a downloaded episode. The Go import path wires the extras importer as `nil → noopExtrasImporter` (import_approved.go:155; main.go:2331). subtitlefiles/metadatafiles/extrafiles packages are persistence-only — no importer. So **subtitles + other extra files adjacent to a download are NOT imported** alongside the episode. Substantial subsystem port (SubtitleService + OtherExtraService + the ExtraService orchestrator + the import-time CanImportFile/ImportFiles chain) — NOT an autonomous-loop slice. FLAGGED for the operator.

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
