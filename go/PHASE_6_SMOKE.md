# Phase 6.2 — Manual Smoke Runbook

This is the operator-driven smoke checklist for the Phase 6 cutover gate. It complements the automated Phase 6.1 shadow-traffic harness (response-diff on every API call) and the Phase 6.3 nightly contract suite (corpus replay against staging URLs); 6.2 covers what those don't — **the user-visible UX**. An automated diff says "the JSON bodies match"; the smoke runbook says "the operator can drive the application end-to-end and it behaves the same as C#."

> One operator follows these steps on BOTH binaries (C# canonical AND Go port) against fresh DBs. Sign-off requires user-visible behaviour to match end-to-end. The runbook is not a script — small UI timing or wording differences are acceptable; what's verified is that the operator-experienced workflow completes equivalently.

## Scope

Per the Phase 6 strategic plan ("Add a series, run a search, grab a release, import a file, send notifications, take a backup, restore the backup — end-to-end on both binaries with identical results"), the runbook covers seven scenarios. Each is one workflow that exercises a real production code path the contract harness can't cover (state mutations across multiple endpoints + SignalR fan-out + persistent side effects).

Out of scope:

- Performance benchmarking — covered separately when 6.4 cutover gate opens.
- Migration testing (live DB upgrade from C# v4 → Go-port v5) — separate runbook, Phase 7.
- UI pixel-level diff — too brittle; React behaviour differences vs C# UI are out of band.

## Setup

| Step | C# host | Go-port host |
| --- | --- | --- |
| Fresh DB | Delete `~/.config/Sonarr/sonarr.db*` | Delete `<go-data-dir>/sonarr.db*` |
| Binary | `dotnet bin/Sonarr.exe` on port `:8989` | `go run ./cmd/sonarr -listen :8990 -data <dir>` |
| Browser | `http://localhost:8989` | `http://localhost:8990` |
| Two side-by-side browser windows recommended | | |

For each scenario below, record:

- **C# result** (what C# did — wire status, UI affordance, SignalR event).
- **Go-port result** (same observations).
- **Verdict**: PASS (user-visible behaviour equivalent), DIVERGE-LOW (cosmetic delta — e.g. response timing, log wording), DIVERGE-HIGH (functional delta — different status code, missing field, persistence mismatch).

DIVERGE-LOW does NOT block 6.4; DIVERGE-HIGH does. The shadow-traffic harness should also have caught a DIVERGE-HIGH; if it didn't, that's a redaction-config gap to file separately.

## Scenario 1 — Add a series

### Steps
1. **UI**: Navigate to "Add New" → search the term `breaking bad` → click "Search" → pick the canonical result → click "Add" with default profile + monitor + path settings.
2. **Expected wire**:
   - `GET /api/v3/series/lookup?term=breaking+bad` → `200` + array of series with `tvdbId` populated.
   - `POST /api/v3/series` (with the chosen lookup result + add options) → `201` + persisted `Series` resource with `id` assigned + `path` populated.
3. **Expected UI**: Series appears in main library list within 1-2 seconds (SignalR `SeriesAdded` event drives the live insert).
4. **Expected DB**: `Series` row inserted; `Seasons[]` populated with monitored=true on at least the first season; an initial `RefreshSeries` command queued (visible at `GET /api/v3/command`).

### Assertions
- POST response includes `tvdbId`, `title`, `path` matching what the operator clicked.
- SignalR client receives one `{name:"series",action:"updated"}` envelope within 5 seconds.
- A `RefreshSeries` command appears in the queue and transitions to `completed` within 30 seconds (TVDB roundtrip + image download).
- After RefreshSeries completes, the series detail page renders the full season + episode tree.

### Cross-binary verdict
PASS if C# and Go both end with: series in library + season tree populated + UI route /series/:id renders without 404. DIVERGE-LOW if timing differs by >5 seconds. DIVERGE-HIGH if either binary's POST returns a different status code or persists a different `path`.

## Scenario 2 — Run a search

### Steps
1. **Prereq**: At least one indexer configured (any RSS feed works for smoke — even a paused indexer surfaces the search path).
2. **UI**: From the series detail page, click any episode's "Search Manually" affordance.
3. **Expected wire**:
   - `POST /api/v3/command` with `{name:"EpisodeSearch",episodeIds:[N]}` → `201` + command resource with `id` assigned.
   - `GET /api/v3/release?episodeId=N` → `200` + array of release candidates (may be empty if no indexer has matching releases — that's fine for smoke).

### Assertions
- Command resource transitions `queued` → `started` → `completed` within indexer-timeout (default 30s).
- Release table renders in UI even when empty (no console exception, no infinite spinner).
- SignalR client receives `{name:"command",action:"updated"}` events for each state transition.

### Cross-binary verdict
PASS if the command transitions end at `completed` (not `failed`) on both binaries and the release table renders. DIVERGE-LOW if release counts differ (indexer state changes between runs). DIVERGE-HIGH if either binary returns a different status or the command transitions to `failed` deterministically.

## Scenario 3 — Grab a release

### Steps
1. **Prereq**: Scenario 2 surfaced at least one release in the manual-search table (skip if no releases — the path is exercised by the existing contract harness).
2. **UI**: Click the "Grab" affordance on a release row.
3. **Expected wire**:
   - `POST /api/v3/release` with the release body from the search-result row → `200`. (NOT 201; matches C# `ReleaseController.Accept`.)
   - Subsequent `POST /api/v3/command` queued internally for `DownloadedEpisodesScan` or similar (depends on download-client config).

### Assertions
- 200 response (NOT 404 — Go-port previously had a "couldn't find requested release in cache" 404 if the cache TTL ran out; J237 closed that with the in-memory cache, but operator MUST verify on a real grab).
- Release moves to the active-queue if a download client is configured; otherwise to history (configurable per C# `DownloadDecisionMaker`).
- SignalR client receives one `{name:"queue",action:"updated"}` or `{name:"history",action:"updated"}` event.

### Cross-binary verdict
PASS if grab returns 200 on both binaries and the release shows up in queue OR history. DIVERGE-HIGH if either returns 404 or a different status; that's the J237 cache regression to file.

## Scenario 4 — Import a file

### Steps
1. **Setup**: Drop a manually-prepared `Breaking.Bad.S01E01.x265.mkv` file (any valid filename; doesn't have to be playable — the importer only reads the filename + size) into the configured drop folder.
2. **UI**: Navigate to "Activity → Manual Import" → choose the drop folder → wait for the scan to complete → review the parsed table (series + season + episode + quality).
3. **Expected wire**:
   - `POST /api/v3/manualimport` with `{folder:"<drop>"}` returns array of `ManualImportResource` entries with `series`, `episode`, `quality`, `releaseType` populated. (J238 closes this; runbook MUST verify on fresh data.)
   - Optionally `POST /api/v3/manualimport` again (the REPROCESS endpoint) with the operator's overrides — adjusts the parsed table without committing.
   - `POST /api/v3/command` `{name:"ManualImport",files:[...]}` to commit.
4. **Expected UI**: Imported file moves under the series's root folder; episode shows `hasFile=true` in the season tree.

### Assertions
- POST /api/v3/manualimport returns 200 with non-empty `series` field for any file matching a library series (J238 wired this — runbook is the integration check).
- After commit, `GET /api/v3/episode/{id}` shows `hasFile=true` and `episodeFileId` populated.
- SignalR client receives `{name:"episode",action:"updated"}` + `{name:"episodefile",action:"updated"}`.

### Cross-binary verdict
PASS if file is at the destination path + hasFile=true on both binaries. DIVERGE-HIGH if either binary leaves the file in the drop folder or fails to associate it with the episode.

## Scenario 5 — Test notifications

### Steps
1. **Prereq**: Configure at least one notification provider (any: Discord webhook to a test channel, Pushover, Email — the operator picks the cheapest one to verify).
2. **UI**: Settings → Connect → click "Test" on the configured provider.
3. **Expected wire**:
   - `POST /api/v3/notification/test` with the provider settings → `200` + `{isValid:true}` envelope on success; `4xx` + validation errors on misconfig.
   - The provider receives an out-of-band test message.

### Assertions
- 200 response when settings are valid; the test message arrives at the provider within 30 seconds.
- 4xx with descriptive validation envelope when settings are invalid (e.g. malformed Discord webhook URL).
- For Pushover specifically (J219 wired E2E encryption): an encrypted notification when `encryptionKey` is set; verify the receiving client decrypts cleanly.

### Cross-binary verdict
PASS if both binaries deliver the test message AND surface validation errors the same way. DIVERGE-LOW if error wording differs (acceptable per J233 envelope policy). DIVERGE-HIGH if either binary silently swallows a misconfig or fails to deliver.

## Scenario 6 — Take a backup

### Steps
1. **UI**: Settings → General → click "Backup Now" (or System → Backup → "Backup Now" depending on which UI route the operator picks).
2. **Expected wire**:
   - `POST /api/v3/command` `{name:"Backup"}` → `201` + command resource.
   - Subsequent `GET /api/v3/system/backup` lists the newly-created backup file with size + creation timestamp.

### Assertions
- Command transitions to `completed` within 60 seconds (DB-snapshot + zip).
- Backup file appears in the configured backup directory on disk.
- `GET /api/v3/system/backup` returns an array with the new backup's metadata.
- SignalR client receives `{name:"command",action:"updated"}` + optionally `{name:"backup",action:"created"}` (the latter is a "nice to have" — not all C# versions emit it).

### Cross-binary verdict
PASS if both binaries produce a backup file readable as a zip with at least the sqlite DB + a `config.xml` inside. DIVERGE-HIGH if either binary produces a zero-byte file or fails the command.

## Scenario 7 — Restore the backup

### Steps
1. **Prereq**: A backup file from Scenario 6.
2. **UI**: Settings → General → "Restore Backup" → pick the file from the list (or upload) → confirm.
3. **Expected wire**:
   - `POST /api/v3/system/restore/{id}` (or `POST /api/v3/system/restore/upload` for the upload variant) → `200` + restart-pending status.
   - Server restarts; on next page-load, the library state matches the moment-in-time of the backup.

### Assertions
- The binary restarts cleanly (no crash; the existing `cmd/sonarr` already wires graceful Stop()).
- After restart, the library shows the same series that were present when the backup was taken (Scenario 1 added a series; Scenarios 4-5 added episode-file + notification state — all should be present).
- A new entry in the backup-restored history shows the file ID + timestamp.

### Cross-binary verdict
PASS if both binaries restart and the library state matches the backup. DIVERGE-HIGH if either binary fails to restart or restores partial state (e.g. series present but episode-files missing).

## Reporting

After all seven scenarios:

1. Create a `phase6-smoke-YYYY-MM-DD.md` file in your local notes (NOT committed — operator workspace).
2. Per scenario: paste the C# wire response + Go-port wire response side-by-side + your verdict.
3. Tally: `N PASS / M DIVERGE-LOW / K DIVERGE-HIGH`.
4. If K > 0: file one issue per DIVERGE-HIGH on the project tracker. These ARE 6.4 blockers.
5. If K == 0 and M ≤ 2: smoke gate PASSES.
6. Cite the smoke gate result in the 6.4 cutover commit message.

## Interaction with 6.1 shadow-traffic + 6.3 nightly contract

The runbook does NOT replace those layers — it covers what they can't:

| Layer | What it catches | What it misses |
| --- | --- | --- |
| **6.1 shadow-traffic harness** | JSON body diff on every API call (post-redaction) | UX flow that depends on multi-call state (Scenario 4: scan → review → commit triggers three endpoints; the harness sees each in isolation) |
| **6.3 nightly contract** | Drift in the golden corpus against live staging | Same multi-call state limitation; also misses things not in the corpus (the corpus is per-endpoint, not per-flow) |
| **6.2 smoke (this doc)** | End-to-end UX flow; DB persistence across restarts; SignalR fan-out | Anything performance-related; anything migration-related |

A scenario can pass 6.1+6.3 (each individual API call diffs clean) but fail 6.2 (the workflow as experienced through the UI is broken because of an ordering bug between calls). That's specifically what the runbook protects against.

## Re-running

Re-run the full runbook:

- Before each 6.4 cutover attempt (the gate is "smoke passes on a fresh DB").
- After any change to the seven scenarios' code paths in the Go-port tree (use `git log` on `internal/{series,episodes,downloadclient,notification,backup}/` as the smell test).
- Once monthly during the 6.1 shadow-traffic monitoring period (sanity check that the long-running shadow harness hasn't hidden a slow-burn divergence).

The smoke runbook is intentionally manual — automating it would re-implement the React UI in headless form, which is enormous effort with low signal-to-noise ratio for what it would catch.

## Open questions

- **Backup-file format compatibility** — Scenario 7 assumes the Go-port writes a backup format C# can ALSO read (and vice versa). If the formats diverge, restore is a one-way street and the cutover gate has to drop "can restore C# backups on Go-port" as a requirement. Verification needed early in the smoke run; if formats diverge, file a Phase 7 issue.
- **Notification-provider account access** — the operator running smoke needs at least one provider account (Discord channel, Pushover token). Cheapest is Discord; check that the team has a test channel before kicking off the runbook.
- **Drop-folder permissions on macOS** — Sonarr's manual-import scans drop folders; ensure the operator has read permission on whatever folder they point the scan at. Default macOS Downloads folder works.
