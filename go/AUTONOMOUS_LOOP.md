# Autonomous Loop Contract — Sonarr go-port Verticalization Arc

Paste this whole file's **Contract** section as the prompt to `/loop` (self-paced,
no interval) in a fresh session, or run `/loop` and reference `go/AUTONOMOUS_LOOP.md`.
It is self-contained: a new session needs only this file + the repo.

---

## Contract

Continue the Sonarr go-port **Verticalization Arc** autonomously, one slice per
iteration.

### Resume (do this FIRST every iteration)
- Read `go/PORT_COMPLETION_PLAN.md` — the resource-completion **matrix** + functional
  **punch-list** is the source of truth for "what's left / are we done."
- Read the top of `go/PROGRESS.md` — the `HEAD at J<n>` line is the resume pointer;
  continue J-numbering from the highest committed J.
- Pick the **highest-value not-DONE / not-BLOCKED** item (see Suggested order).
- Work in `go/` (the `cd` persists within a turn; the repo root is the parent).
  `src/` is the **READ-ONLY** C# reference — never edit it.

### Per slice (= one coherent unit = one commit)
- Port **faithfully** to the C# source; cite `file:line` in code comments.
- Reuse existing services (the download clients, import pipeline, recyclebin,
  episodefilemoving, organizer, parsing service, etc. are already ported).
- Prefer **narrow consumer-side interfaces** + **optional-extension** wiring
  (discover a richer dep via type-assertion on the handler's existing dep) so
  handler signatures and `buildHostServer` params stay stable. Adapters that carry
  cross-service deps live in `cmd/sonarr`.
- Write real tests: temp-dir / SQLite round-trips for disk+DB side effects;
  fake-collaborator tests for dispatch contracts; real-bus capture for events.

### Hard gates (run from `go/`, all must pass BEFORE committing)
- `go build ./...`
- `go test -race -count=1 ./<touched>/... ./cmd/sonarr/... ./test/contract/...`
  (add the v5 mirror pkg when touched)
- `go vet ./<touched>/...`
- `gofmt -l <changed files>` → must be empty (run `gofmt -w` if not)

### Bookkeeping + commit (every slice)
- Append a **~5-line** `STATUS.md` entry (J379+ cadence — terse; the diff is the record).
- Prepend a one-line `HEAD at J<n>` block to `PROGRESS.md` "Current state".
- Update the matrix row in `PORT_COMPLETION_PLAN.md` (R/W/M + "Done by J<n>").
- `git commit` — stage **only** changed files; **NEVER** stage
  `.claude/scheduled_tasks.lock`; no `--amend`/force; **no AI attribution /
  Co-Authored-By** (per `~/.claude/CLAUDE.md`). Commit message:
  `Phase 5.J<n> (<one-line summary>).`

### Safety (non-negotiable)
- **NEVER no-op a side effect to make a test pass.** Test the real effect.
- If a slice seems to need an unported service/field: **VERIFY it's actually
  missing** (most things exist — the clients + import pipeline are ported; check
  before claiming a block). If genuinely missing, **STOP, flag in one paragraph**,
  and mark the matrix row `BLOCKED` with file:line evidence. Do not fabricate.
- **EXCLUDE from autonomous scope — flag for the operator instead:** `ResetApiKey`
  or anything that rotates auth / mutates config secrets / deploys / pushes / opens
  PRs. Commit locally on `go-port` only; never push without the operator.
- Pace slices **back-to-back (~60–120s)**, don't idle (per the loop-pacing memory).

### Suggested order (highest value first)
1. **Download failure/rejection state machine + auto-redownload** — continues the
   Phase-4 arc (J394–J399 landed queue population + removeFromClient/blocklist +
   happy-path auto-import). Remaining: `CompletedDownloadService` rejection/partial
   handling (ImportBlocked/ImportPending/FailedPending), the auto-redownload-search
   consumer for `DownloadFailedEvent` when `!skipRedownload`, script-import +
   remove-completed-download cleanup + `DownloadCompletedEvent`. See memory
   `phase4_blocks_punchlist` for the exact gaps.
2. **Remaining #6 admin commands** — `ResetQualityDefinitions`, `ClearLog` /
   `DeleteLogFiles` (clean, low-risk). (`ResetApiKey` is EXCLUDED — see Safety.)
3. **#9 read stragglers** — ONLY if their providers have landed (update
   Latest/Installable needs a populated RecentUpdateProvider; diskspace-optional
   needs a mount enumerator). If still provider-gated/unobservable, SKIP + note —
   don't port unobservable computations.
4. **#8** release/push response-shape polish (cosmetic — lowest priority).
5. **Phase 2 — v5 catch-up (backlog C.1)** — per-endpoint mirror of the upstream
   v5-develop surface. Highly loop-friendly once Phase 1 is drained.

### Done
Stop when every matrix row is **DONE** or **BLOCKED** (with evidence) and the
Suggested-order list is exhausted; emit a final summary.

---

## Context the loop should know (verified this session, J386–J399)

- **Punch-list #1–#7 + ClearBlocklist are DONE.** Manual Import (list+execute),
  EpisodeFile editor Quality/Languages + recycle-bin delete, DeleteSeriesFiles /
  Rename{Files,Series} / Move{Series,BulkSeries} commands, v5 season-monitor
  episode cascade, and the **Phase-4 download-loop unblock** (queue populates from
  live clients → removeFromClient/changeCategory/blocklist on delete →
  happy-path auto-import of completed downloads).
- **Earlier "Phase-4 blocked" was over-broad** — the download clients, registry,
  import pipeline, parsing service, and tracked-download cache are all ported. The
  remaining download work is the *failure/rejection* handling, not the plumbing.
- **Patterns to copy:** the command handlers under `internal/commands/handlers/`
  (`manualimport.go`, `deleteseriesfiles.go`, `processmonitoreddownloads.go`); the
  cmd/sonarr adapters (`queue_remove_wiring.go`, `download_tracker_wiring.go`,
  `episodefile_delete_wiring.go`) for cross-service orchestration discovered via
  optional-extension type-assertion.
