# Phase 6 — Cutover Scope

This document scopes Phase 6 of the C# → Go port. It exists because Phase 5 has structurally closed (REST surface + SignalR fan-out + frontend wiring all wired and lint-clean) and the next session needs a clear entry point. The authoritative strategic plan still lives at `~/.claude/plans/curried-discovering-meadow.md`; this doc is the operational expansion of that plan's two-line "Phase 6 — Cutover" entry.

## Phase 5 closure summary

| Sub-arc                | Status                       | Commits     |
| ---------------------- | ---------------------------- | ----------- |
| REST V3 endpoints      | 41/51 byte-equal              | 4.ED → 4.EO |
| SignalR fan-out (7 families) | wired end-to-end + WS transport + 5s debouncer | 4.EP → 4.FB |
| Frontend bundle wiring | `--frontenddir`, `WithStaticDir`, `WithStaticFS`, `WithURLBase`, `//go:embed` fallback | 4.FC → 4.FG |
| Mop-up                 | queue/status Pause/Resume bracket, end-to-end integration smoke, graceful-shutdown Stop() wired | 4.FH → 4.FJ |

The 10 REST endpoints NOT byte-equal are documented below — none are blockers for Phase 6 entrance because Phase 6 introduces a shadow-traffic comparison that runs against LIVE Go + C# state simultaneously, so the timing-sensitive endpoints (which can never byte-equal a frozen golden) get exercised properly against a moving reference.

## 10 unmatched REST endpoints

Each is unmatched for a reason; the column "Phase 6 strategy" says how shadow-traffic will exercise it.

| Endpoint                            | Why unmatched                                            | Phase 6 strategy                                    |
| ----------------------------------- | -------------------------------------------------------- | --------------------------------------------------- |
| `GET /api/v3/system/task`           | `lastDuration` = `LastExecution - LastStartTime` is non-deterministic TimeSpan; corpus golden frozen at recording moment | Shadow-traffic compares projection shape + interval values; redact lastDuration |
| `GET /api/v3/system/routes`         | 448 KB DOT graph; ASP.NET Core route-discovery infra has no clean Go equivalent | Skip in shadow-traffic; manually verify the static route list against `/api/v3` |
| `GET /api/v3/diskspace`             | Environment-dependent (mount table differs per host)     | Shadow-traffic on the same staging host → identical mounts on both binaries |
| `GET /api/v3/log`                   | Log records depend on what each binary logged at boot    | Skip byte-equal; spot-check shape + pagination keys |
| `GET /api/v3/command`               | Command IDs + timestamps non-deterministic; queue contents differ per-run | Shadow-traffic compares projection shape; redact dynamic fields |
| `GET /api/v3/episodefile?seriesId=1`| 404 path returns a C# stack trace in the description; verbatim match impossible | Shadow-traffic ignores `description` field on 4xx |
| `GET /api/v3/importlist/schema`     | 10+ schema entries; per-provider settings shape is large but mechanical | Phase 5 slice candidate (still tractable as byte-equal once ported); otherwise shadow-traffic |
| `GET /api/v3/indexer/schema`        | 10+ schema entries (same as above)                       | Same — Phase 5 candidate / shadow-traffic           |
| `GET /api/v3/notification/schema`   | 25+ schema entries (same)                                | Same                                                |
| `GET /api/v3/downloadclient/schema` | 20+ schema entries (same)                                | Same                                                |

Four of these (the schema endpoints) are pure mechanical work and could close as further Phase 5 slices before Phase 6 begins. The other six are timing/environment-sensitive and properly belong to Phase 6's shadow-traffic harness.

## Remaining Phase 5 SignalR mop-up

Listed in signalr_wiring.go as deferred but not blocking Phase 6:

- `SeriesBulkEditedEvent` / `SeriesImportedEvent` / `SeriesRenamedEvent` Sync emits — events not yet ported in the Go domain layer.
- `EpisodeFileRenamedEvent` SignalR broadcast — broadcast path not wired.
- `ModelEvent<EpisodeFile>` Updated path — Go port doesn't fire generic ModelEvent.
- `Series.Path` join for `EpisodeFile` Added — `EpisodeFileAddedEvent` doesn't carry Series; would need either event-shape change or broadcast-time repo join.
- Per-tag Created/Updated/Deleted SignalR projections — Go tags.Service only fires generic `TagsUpdatedEvent`; per-row events would be richer than current C# behaviour.
- `ModelEvent<ScheduledTask>` for `system/task` SignalR — Go scheduledtasks doesn't publish per-row mutation events.
- Long-polling SignalR transport fallback — WebSockets-only today; modern clients are fine.

None of the above breaks production wire compatibility; each is a "more complete C# parity" item, not a "Go diverges" item.

## Phase 6 — shape

Phase 6 is a 4-week operational period during which both binaries run side-by-side against the same database on a staging host, with response-diffing on every API call and a daily diff report. The strategic plan's risk register names this the "Pre-cutover gate".

### 6.1 Shadow-traffic harness

A small process that:

1. Accepts the same HTTP request stream (replay from production logs, scripted load, or live mirror).
2. Dispatches each request to both `:8989` (C#) and `:8990` (Go) in parallel.
3. Diffs the JSON response bodies (post-redaction of known dynamic fields).
4. Writes a daily diff report grouped by endpoint, severity, and diff signature.

Implementation: a Go binary that wraps `httputil.NewSingleHostReverseProxy` twice + a diff goroutine. Lives at `go/cmd/shadow-traffic-diff/` (NEW). Reuses the existing `test/contract/internal/normalize` redact-paths machinery so the diff layer agrees with the contract-test machinery on what counts as "noise".

### 6.2 Manual smoke checklist

Per the strategic plan: "Add a series, run a search, grab a release, import a file, send notifications, take a backup, restore the backup — end-to-end on both binaries with identical results."

This checklist becomes a runbook at `go/PHASE_6_SMOKE.md` (FOLLOW-UP) — one operator follows the steps on both binaries against fresh DBs and signs off that user-visible behaviour matches.

### 6.3 Nightly contract-suite gate

The existing contract harness (`test/contract/replayer`) already self-skips when `-contract.base` is unset. Phase 6 turns this on in nightly CI:

- Run the contract suite against a freshly-built Go binary at staging URL.
- Run the same suite against the C# binary (also nightly-built).
- Surface drift the moment any byte-equal endpoint regresses.

This is purely CI plumbing — no Go code change needed beyond a workflow file (e.g. `.github/workflows/nightly-contract.yml`).

### 6.4 Cutover commit

When the 4-week shadow period closes with zero unexplained diffs AND the smoke runbook passes, a single commit:

1. Updates `distribution/` (DEB/RPM specs) to install the Go binary at `/usr/lib/sonarr/sonarr`.
2. Updates `docker/Dockerfile` to `COPY` the Go binary instead of `dotnet publish` output.
3. Updates `scripts/` (`update-script.sh`, etc.) to handle the Go-binary update flow.
4. Updates CI to build only the Go tree.
5. Deletes `src/` (the C# tree).
6. Moves `go/*` to repo root and updates `.gitignore` accordingly (optional — depends on whether the team wants the `go/` prefix retained for migration history).

This commit is the cutover. After merge, Phase 7 (idiomatic-Go cleanup pass) begins.

## Phase 6 entrance criteria

- Phase 5 sub-arc 1 closed: 41/51 byte-equal + the 10 unmatched documented (this doc).
- Phase 5 sub-arc 2 closed: 7 SignalR resource families wired + transport tested.
- Phase 5 sub-arc 3 closed: frontend bundle served from a single binary or via `--frontenddir`.
- Phase 5 mop-up: queue/status Pause/Resume (4.FH), integration smoke (4.FI), graceful Stop() (4.FJ) ✓
- Phase 6 entrance is GATED ON: an operational staging host with both binaries deployable, NOT on closing the remaining 4 schema endpoints (those can close in parallel during Phase 6's 4-week window if shadow-traffic surfaces drift).

## Phase 6 exit criteria

- 14+ consecutive days of zero unexplained shadow-traffic diff per endpoint.
- Smoke runbook passes on a fresh DB.
- Contract suite is fully green against the Go binary.
- Manual sign-off from the operator running the staging host.

## Open questions

- **Staging host availability** — does the project have a Sonarr-style staging instance the team can deploy two binaries on? If not, Phase 6.1 (shadow-traffic harness) becomes the bottleneck.
- **Production-log access for traffic replay** — without a real request stream, shadow-traffic falls back to scripted load (acceptable for breadth coverage; less realistic for edge cases).
- **CI compute budget for nightly-contract** — the contract suite is fast (subsecond per endpoint) but a nightly run × 2 binaries × full corpus is non-trivial; nightly cadence may need to drop to weekly if compute is constrained.

## Next slice candidates (post-4.FJ)

These keep the Phase 5 momentum if Phase 6 entrance is blocked on staging infra:

- **4.FK series of slices**: one schema endpoint per slice (importlist.schema, indexer.schema, notification.schema, downloadclient.schema). Each is mechanical per the slice 4.EN pattern.
- **4.FL series**: SignalR mop-up — wire EpisodeFileRenamedEvent broadcast, add Series.Path join for EpisodeFile Added (the cleanest path is option (b) — broadcast-time series lookup, not event-shape change).
- **4.FM**: the shadow-traffic harness skeleton — start with the reverse-proxy binary even before the staging host exists, so the test infra is ready when staging lands.

Pick by what the team can resource — Phase 6 is operational rather than code-focused, and the bottleneck is typically infra rather than implementation.
