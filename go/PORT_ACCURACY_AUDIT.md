# Sonarr Go-Port — Accuracy Audit (deep dive)

**Audit date:** 2026-05-20
**Author:** External review session (companion to `TEST_QUALITY_AUDIT.md`, separate from the porting session that maintains `STATUS.md`)
**Question asked:** *"Do a deep dive — make sure the Go port is accurate."*

**Note to the porting Claude session:** Findings can be folded into `STATUS.md` / `PHASE_5_AUDIT.md` / `PHASE_6_SCOPE.md` as appropriate. The most important meta-finding is in §0 — `PHASE_5_AUDIT.md` is now stale (dated 2026-05-18, 2 days behind the actual tree); please refresh or supersede it.

---

## 0. Meta-finding: PHASE_5_AUDIT.md is stale (HIGH PRIORITY)

`PHASE_5_AUDIT.md` claims, as of 2026-05-18:

- "Zero write endpoints under /api/v3" — **no longer true.** ~40 POST/PUT/DELETE routes are wired in `cmd/sonarr/host_wiring.go:1110-1280` with real handler bodies (validation → service → DB write → response). Verified examples: `autotagging` (POST/PUT/DELETE), `customformat` (POST/PUT/DELETE+bulk), `customfilter`, `delayprofile` (+reorder), `tag`, `series`, `command` (POST+DELETE), `blocklist` (DELETE+bulk), full provider CUD for `downloadclient` / `importlist` / `indexer` / `metadata` / `notification`, and `config/host`+`config/ui` PUT.
- "Error response shape drift (text/plain vs C# JSON envelope)" — **fixed.** `internal/httperr/httperr.go:103-178` ships the C# `ErrorModel` envelope (5xx/4xx) and the `[{propertyName, errorMessage, severity}, …]` 400 validation array. Zero remaining `http.Error()` calls under `internal/api/v3/`.
- "18 download clients schema-only" — **partially out of date.** qBittorrent (`internal/downloadclients/qbittorrent/qbittorrent.go` — ~2000 LOC, real Download/GetItems/GetStatus/RemoveItem/MarkItemAsImported), Deluge, and NZBGet now have real implementations. ~14 remain stub, not 18.

The 7 stub indexers and the unwired contract suite in CI (carried over from `TEST_QUALITY_AUDIT.md`) remain accurate.

**Why this matters:** the porting session uses `STATUS.md` as its session-resumption pointer. If it also reads `PHASE_5_AUDIT.md` as a current gap list, it will rework things already done and skip things that are still gaps. Suggest replacing PHASE_5_AUDIT.md headline with: *"Snapshot 2026-05-18; superseded — see STATUS.md for current slice state."*

---

## 1. Critical findings (real wire-shape breaks, fix before merging)

### 1.1 `omitempty` regression on ~115 v3 fields (REGRESSION since 2026-05-12 sweep)

`JSON_CONVENTIONS.md:132` claims a full sweep landed 2026-05-12 dropping 242 `omitempty` occurrences. Spot-checking the current tree shows **`omitempty` has been reintroduced on JSON-emitting types** in handler files added or modified after the sweep — likely by Phase 5 write-path slices that copied an idiomatic-Go template instead of the project's wire-compat template.

**Verified examples:**

`internal/api/v3/episode.go:41-46`:
```go
AirDate                    string     `json:"airDate,omitempty"`        // C# `public string AirDate` → SHOULD have NO omitempty
AirDateUTC                 *time.Time `json:"airDateUtc,omitempty"`     // C# `public DateTime? AirDateUtc` → SHOULD have NO omitempty
LastSearchTime             *time.Time `json:"lastSearchTime,omitempty"` // same — nullable DateTime, must emit explicit null
FinaleType                 string     `json:"finaleType,omitempty"`     // C# `public string FinaleType` → SHOULD have NO omitempty
Overview                   string     `json:"overview,omitempty"`       // same
```

Confirmed C# side at `src/Sonarr.Api.V3/Episodes/EpisodeResource.cs:22-23`:
```csharp
public string AirDate { get; set; }                  // non-nullable string → emits "" not omitted
public DateTime? AirDateUtc { get; set; }            // nullable → emits null, not omitted
```

Same pattern across `internal/api/v3/command.go:36-51`, `series.go`, `qualityprofile.go`, `queue.go`, `seasonpass.go`, `downloadclient.go`, `importlist.go`, `indexer.go`, `episodefile.go`, `notification.go`, `metadata.go`, `customformat.go`, `releaseprofile.go`, `delayprofile.go`, `autotagging.go`, `remotepathmap.go`, `blocklist.go`, `config_*.go` — **32 files total, ~115 fields**.

**Wire impact:** when the field is null/empty, C# emits the field with a `null` / `""` value; Go omits the key entirely. Any client that does `response.airDateUtc === null` checks (React form-binding, Prowlarr/Overseerr parsers) now hits `undefined` instead. Byte-equal contract tests would catch this — but per `TEST_QUALITY_AUDIT.md` §2-3, the replayer doesn't run in CI, so the regression went unnoticed.

**Non-regressions (don't sweep):**
- `internal/api/v3/fielddef.go:44-55` — schema field defaults intentionally use `omitempty` per inline comments mirroring C# `WhenWritingNull` for ProviderField shape.
- `internal/api/v3/rootfolder.go:41-42` — `FreeSpace *int64` / `TotalSpace *int64` with `omitempty` is correct per C# `RootFolderResource.cs:13-14` (`long?` nullable, but those particular properties use `WhenWritingNull`).

**Fix shape:** add `golangci-lint` custom rule or `go vet`-style check that flags `json:"...,omitempty"` on any struct under `internal/api/v3/`, with an allow-list for fielddef.go and rootfolder.go's two free-space fields. Then do a one-off cleanup pass on the 32 files.

### 1.2 Unstable sort in custom-format calculation (NON-DETERMINISM)

`internal/customformats/calculate.go:140-142`:
```go
sort.Slice(matched, func(i, j int) bool {
    return matched[i].Name < matched[j].Name
})
```

C# equivalent (`src/NzbDrone.Core/CustomFormats/CustomFormatCalculationService.cs`) uses `OrderBy(x => x.Name)` which is **stable**. When two matched custom formats share a name (rare but possible with user-edited definitions, and certain in test fixtures), Go's unstable sort produces non-deterministic ordering.

**Fix:** one-character change to `sort.SliceStable`. The decision engine itself already uses `SliceStable` correctly at `internal/decisionengine/decisionmaker.go:71`, so this looks like a localized oversight rather than a pattern problem.

---

## 2. Important findings (wire-visible but lower-impact)

### 2.1 Cache-Control / Pragma / Expires / Vary headers not emitted

Golden files at `test/contract/golden/v3-empty-state/v3.blocklist.golden.json:5-25` show C# emits:
```
Cache-Control: no-cache, no-store
Pragma: no-cache
Expires: -1
Vary: Accept-Encoding
```

Go handlers emit none of these. Body byte-equality is unaffected (this is header drift), but any client/proxy doing cache-policy-aware behavior will see different caching. PHASE_6_SCOPE.md treats header drift as a Phase 6 shadow-traffic finding rather than a port-blocking issue, which is reasonable.

### 2.2 Indexer registry is empty — `/api/v3/indexer/test` returns 400 unconditionally

`internal/indexers/registry.go:23-30` confirms no concrete indexer registers itself yet. The 7 stubs (BroadcastheNet, Fanzub, FileList, HDBits, IPTorrents, Nyaa, Torrentleech) named in PHASE_5_AUDIT.md remain unimplemented. Newznab/Torznab/TorrentRss are the only working indexers; users on private trackers requiring proprietary protocols cannot add an indexer at all.

### 2.3 Contract replayer not wired to CI (carry-over from TEST_QUALITY_AUDIT.md §2-3)

83 golden files exist; `build_go.yml:53-54` runs `go test ./...` without `-contract.base`, so the replayer self-skips. Without this gate, the §1.1 omitempty regression would not have surfaced in CI — and won't surface for the next one either.

---

## 3. Verified-correct (project did this right)

The following are concerns I'd typically flag in a C#→Go port that this port has handled correctly. Including them so the porting session sees what's working.

| Concern | Status | Evidence |
|---|---|---|
| Error envelope shape (C# `ErrorModel`) | ✓ Fixed | `internal/httperr/httperr.go:103-178`, called from every v3 handler |
| Nil-slice → `[]` (vs `null`) | ✓ Compliant | `series.list`, `tag.list`, `qualityprofile.list` all return `[]` on empty DB |
| DateTime emission format | ✓ Compliant (caveat) | RFC3339 with second precision; golden `lastExecutionTime: "2026-05-02T06:56:55Z"` — **caveat:** if any code constructs `time.Time` with sub-second precision and serializes it, Go's `MarshalJSON` will leak nanoseconds. Worth a `go vet`-style check or a wrapper. |
| Status code mapping (404 / 400 / 409) | ✓ Compliant | `httperr.NotFound` / `BadRequest` / `Conflict` helpers used uniformly |
| Regex translation (C# → RE2) | ✓ Handled | `internal/parser/normalize.go:9-62` explicitly rewrites C# lookbehinds rather than transliterating; named groups preserved |
| Decision-engine sort stability | ✓ Correct | `decisionmaker.go:71` uses `sort.SliceStable` |
| DB nullable coercion (NULL→0) | ✓ Correct | `internal/series/series.go:213-220` coerces `Year` and `QualityProfileID` from `sql.NullInt64.Valid` checks; matches the `MapperT.Scan` pattern documented in JSON_CONVENTIONS.md |
| Time/timezone handling | ✓ Correct | `time.Now().UTC()` used consistently in `internal/historyhandlers/*.go`; no `time.Now()` local-clock misuse spotted |
| String comparison (byte-wise) | ✓ Correct | `parser/parse_title.go:141-142` uses `strings.ToLower` + `strings.Contains`, no culture-sensitive Compare |
| Notifications (25/25) and metadata writers | ✓ Confirmed | Discord/Email/Slack/Telegram/Plex/Trakt all have real proxy + settings + test code; Wdtv/Roksbox/Xbmc metadata writers all have real implementations |
| qBittorrent, Deluge, NZBGet download clients | ✓ Implemented | ~2000 LOC each in their respective dirs under `internal/downloadclients/` |
| `panic("not implemented")` markers | ✓ Zero | No `panic("not implemented")`, `panic("TODO")`, or `panic("unimplemented")` anywhere under `internal/` |

---

## 4. Structural surface that remains incomplete (not a regression — known TODOs)

These are gaps the port hasn't yet closed. They are not accuracy bugs in the code that exists; they are accuracy gaps in the port's coverage of C# Sonarr's surface.

- **Indexers — 7 missing:** BroadcastheNet, Fanzub, FileList, HDBits, IPTorrents, Nyaa, Torrentleech.
- **Download clients — ~14 missing:** Aria2, TorrentDownloadStation, UsenetDownloadStation, Flood, TorrentFreeboxDownload, Hadouken, NzbVortex, Pneumatic, RQBit, rTorrent, TorrentBlackhole, TriblerDownloadClient, UsenetBlackhole, uTorrent, Vuze. (PHASE_5_AUDIT.md said 18; qBittorrent + Deluge + NZBGet have since landed.)
- **GET-by-id missing on ~18 list resources** (per PHASE_5_AUDIT.md §"Per-resource matrix (reads beyond list)" — most still apply).
- **Several large read endpoints** still absent: `/api/v3/calendar/feed`, `/api/v3/manualimport`, `/api/v3/parse`, `/api/v3/rename`, `/api/v3/series/lookup`, `/api/v3/log/*`, `/api/v3/release` (search). Full list in PHASE_5_AUDIT.md §"Other reads missing entirely."
- **SignalR broadcast write-path callers:** `signalr.BroadcastResourceChange` exists; coverage of which write paths invoke it should be audited slice-by-slice as Tier 1 lands.

---

## 5. What I did NOT audit (gaps in this audit's coverage)

Honesty about the audit's own blind spots:

- **Healthcheck logic.** 38 healthcheck files under `internal/healthcheck/` were not individually compared to C# equivalents. A real-vs-stub-passes-always classification would need a per-file pass.
- **Migration parity.** Did not compare `internal/datastore/migrations/sqlite/main/*.sql` against C# FluentMigrator output. Critical for cutover — a migration skew between the two binaries pointed at the same DB is catastrophic.
- **Scheduled-tasks dispatch.** Brief look only; did not enumerate which C# scheduled tasks have Go equivalents.
- **Provider settings JSON shape per provider.** Spot-checked a few; per-provider field-by-field comparison would catch settings drift.
- **SignalR payload shapes.** Read the broadcast plumbing, not the payload comparison.
- **Behavior under load / concurrency.** Reading code, not running it; no race detector run, no fuzz, no replay against a real workload.
- **Deeper LINQ → loop equivalence.** Sampled 4-5 packages; the other 80+ packages may have similar sort-stability or `string.IsNullOrWhiteSpace` translation issues.

A full audit would be ~5–10× the effort of this one and is probably better spent as targeted mutation-testing + shadow-traffic comparison once Phase 6 staging is up.

---

## 6. Recommended actions (in priority order)

### A. Refresh `PHASE_5_AUDIT.md` (HIGHEST LEVERAGE — meta-fix)

Replace its headline with a "snapshot, superseded by STATUS.md as of 2026-05-20" note, OR walk the audit and strike the items that are now closed (writes, error envelope, qBittorrent/Deluge/NZBGet). The doc is currently misleading the session that reads it.

### B. Fix the `omitempty` regression (CRITICAL)

Two-step:
1. Add a lint rule (custom golangci-lint linter or a `make wirecheck` script) that fails on any `json:"...,omitempty"` under `internal/api/v3/` outside the documented allow-list (fielddef.go + the two rootfolder free-space fields).
2. Sweep the 32 files. Per JSON_CONVENTIONS.md table:
   - `string` field whose C# is `public string ...` → drop `omitempty`.
   - `*time.Time` field whose C# is `DateTime?` → drop `omitempty`.
   - `*int` field whose C# is `int?` → drop `omitempty`.

### C. Fix `customformats/calculate.go:140` — `sort.Slice` → `sort.SliceStable` (CRITICAL, one char)

Mirror the C# `OrderBy` stability. Audit elsewhere for the same pattern: `grep -nR "sort.Slice(" go/internal/ | grep -v sort.SliceStable` and judge case-by-case.

### D. Wire the replayer into CI (carry-over from TEST_QUALITY_AUDIT.md)

Without this, regressions like §1.1 will keep slipping through. Build the Go binary in CI, boot it, run `go test ./test/contract/replayer/... -contract.base=http://127.0.0.1:8989 -contract.api-key=$KEY`. The goldens are checked in; no live C# binary required.

### E. Add a migration-parity check (PRE-CUTOVER, hard gate)

Before Phase 6 cutover, the SQLite (and Postgres) schema produced by `internal/datastore/migrations/` MUST be byte-equal (or proven semantically equivalent) to the schema produced by C# FluentMigrator. A mismatch means the two binaries can't share a database during the shadow-traffic period. This was not audited here and is the single most expensive thing to discover late.

### F. Run mutation testing on `internal/parser`, `internal/customformats`, `internal/decisionengine` (carry-over)

Targets `gremlins unleash`. Would have surfaced the unstable-sort bug in §1.2.

### G. Cache-Control / Pragma / Expires header parity (LOW PRIORITY, Phase 6)

Add a middleware that sets these on every `/api/v3` response. Mechanical.

---

## 7. Bottom line

The Go port is in better shape than `PHASE_5_AUDIT.md` (dated 2026-05-18) suggests — writes are wired, the error envelope is fixed, the heavyweight download clients have landed, and the translation gotchas I'd most expect to find (regex semantics, time zones, nullable coercion, sort stability in decision logic) are handled correctly.

The two concrete bugs surfaced here — the `omitempty` regression and the one unstable sort — are both mechanical fixes. The bigger risk is the **measurement layer**: PHASE_5_AUDIT.md is stale, the contract suite doesn't run in CI, and the project's percent-complete signal can't distinguish "read-side projection" from "working replacement." Fix the measurement layer and the next regression like §1.1 surfaces in the green/red of a PR check rather than 2 days later in an external audit.

The most decision-shaping recommendation: do not begin Phase 6 cutover until (a) the contract replayer runs on every PR and (b) the SQLite/Postgres migration output is proven byte-equal to FluentMigrator's. Everything else can be worked iteratively under shadow traffic.
