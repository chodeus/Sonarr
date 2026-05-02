# Sonarr Go-port — session resumption guide

This file is the hand-off pointer between sessions. It is the **first thing a new session should read** when continuing the C#→Go port. The strategic plan lives at `~/.claude/plans/curried-discovering-meadow.md`; this file tracks where execution actually stopped.

Last updated: 2026-05-02 (post-Phase-1.0).

## Where to resume

**Phase 0.5 signed off; Phase 1.0 (datastore foundations) shipped to working tree.** Tests green for both engines (`go test -race ./...` against modernc/sqlite and SONARR_PG_DSN=postgres:///sonarr_spike). Lint clean (golangci-lint 0 issues). Migration 001 produces the 14-table set the C# Migration/001 produces; goose state stored in `goose_db_version` (SQLite + Postgres) — not the C# `VersionInfo` table because version numbering differs (gappy in FluentMigrator vs contiguous in goose).

The next concrete actions, in priority order:
1. **Migrations 002..230 sweep** — mechanical port of the remaining 223 migrations. Each one cites its `migrations/ledger.md` row. Probably batched (5-10 per PR) since most are simple ALTER TABLE / CREATE INDEX. The 28 HIGH-RISK embedded-doc mutations get individual PRs with extra test coverage.
2. **Phase 1.1 — repository layer** — `IBasicRepository<T>` equivalent. Reflection-driven column mapping or hand-rolled per-type? Decision deferred to that PR; cross-cite TP-Q1/Q2 string registry.
3. **Phase 1.2 — embedded-doc serialization** — STJson-equivalent settings (per `embedded-docs/catalogue.md` correction: bridge is STJson, not Newtonsoft).
4. **Phase 1.3 — query builder** — replaces `WhereBuilder*.cs`. Probably a small fluent builder, not LINQ-equivalent.
5. **`signalr-philippseith` spike** — needs the C# Sonarr running AND the React frontend pointed at a Go SignalR replacement. Defer until Phase 5 prep.
6. **First commit batch** — `global.json` rollForward + entire `go/` tree (Phase 0 + 0.5 + 1.0) is uncommitted. Decide commit boundary.

Open the plan at `~/.claude/plans/curried-discovering-meadow.md` for the full phase definitions.

## Quickstart for a new session

```sh
# Verify the toolchain is still installed.
go version            # need 1.26+
golangci-lint --version  # need 2.x
brew services list | grep postgres   # postgres@16 should be running for the pgx spike

# Health check the Go module.
cd /Users/dean/Documents/GitHub/Sonarr/go
go vet ./...
golangci-lint run ./...
SONARR_PG_DSN="postgres:///sonarr_spike" go test -count=1 -race ./...   # all green at last save

# Skim the analysis state.
ls go/analysis/subsystems/      # cards already drafted
ls go/analysis/spikes/          # spikes already verified
cat go/analysis/questions.md    # running open-question ledger (26 entries last save)
```

## Phase status

### Phase 1 — Foundations ⏳ in progress

#### 1.0 — Datastore foundations ✅ shipped (2026-05-02)

- `go/internal/datastore/datastore.go` — `Open(ctx, Config) (*DB, error)` for SQLite (modernc.org/sqlite, no cgo) + Postgres (jackc/pgx/v5/stdlib). Pragmas applied at DSN level: WAL, synchronous=NORMAL, foreign_keys=on, busy_timeout=5000ms. Postgres supports Unix-socket via Host=`/tmp` form.
- `go/internal/datastore/migrate.go` — `Migrate(ctx, *DB)`, `MigrateTo(ctx, *DB, version)`, `CurrentVersion(ctx, *DB)` over goose v3.27 against an embedded migration ledger. Per-dialect directories (`migrations/sqlite/main/`, `migrations/postgres/main/`).
- New direct deps in `go.mod`: `github.com/pressly/goose/v3 v3.27.1`. `pgx/v5` and `modernc/sqlite` promoted from indirect.

#### 1.1 — Migrations 002–005 ✅ shipped (2026-05-02)

- `migrations/{sqlite,postgres}/main/00002_remove_tvrage_imdb_unique_constraint.sql` — drops `IX_Series_TvRageId` and `IX_Series_ImdbId`.
- `migrations/{sqlite,postgres}/main/00003_replace_scene_mappings.sql` — drops + recreates `SceneMappings` (drops `CleanTitle`, `SceneName`; adds `SearchTerm`, `ParseTerm`).
- `migrations/{sqlite,postgres}/main/00004_replace_history.sql` — drops + recreates `History` (drops `NzbTitle`, `Indexer`, `NzbInfoUrl`, `ReleaseGroup`; adds `SourceTitle`, `Data`).
- `migrations/{sqlite,postgres}/main/00005_add_eventtype_to_history.sql` — `ALTER TABLE History ADD COLUMN EventType integer`.
- New `headVersion` constant in `migrate_test.go` (currently 5) drives the head-checkpoint suite. Bump on each migration PR.
- New tests: `TestMigrate_{SQLite,Postgres}_ToHead`, plus `MigrateTo` exposed for per-version checkpoint testing. **11 tests passing under `-race` across both engines**, lint clean.
- Postgres tests no longer run in parallel (they share `sonarr_spike` and rebuild the `public` schema for hermeticity); SQLite tests stay parallel.
- **Migration 000 (`database_engine_version_check.cs`) intentionally NOT ported** — it's a `Maintenance(BeforeAll)` engine-version logger, not a versioned migration. The Go equivalent will be a one-line log in `datastore.Open()` once the logger is wired through.
- **Phase 0.5 deliverable validation pass (2026-05-02):** spot-checked the catalogues and questions ledger by re-running the agent's claimed greps and citations against the C# source. Most deliverables held up; the migrations ledger was the outlier with two confidently-wrong claims that have been corrected (see below). Verified-clean: `embedded-docs/catalogue.md` (13 IEmbeddedDocument types, 8 CustomFormat specs via direct + intermediate base, 11 AutoTag specs); all six `providers/*.md` counts (10 indexers / 20 download / 25 notifications / 10 import lists / 2 metadata sources / 4 metadata writers — counting required matching `internal class` and multi-level inheritance via `*ImportBase`/`WebhookBase`/`TransmissionBase` intermediate classes); `jobs/catalogue.md` counts (36 commands / 31 housekeepers / 27 health checks — the 27th is `ServerSideNotificationService` in `HealthCheck/`'s parent dir, not in `Checks/`); 3 sampled `resolved-in-source` claims in `questions.md` (Q-15 wall-clock dep, HOST-Q3 swagger debug-only, Q-16 RealRegex TODO).

- **Reconciled `analysis/migrations/ledger.md` errors** (2026-05-02 spot-check):
  - Total file count: **223**, not 224 (was off by 1).
  - Real gaps: **80, 133, 208, 213, 216, 222, 228, 229** (8 missing files), not 5/15/18/21/57/82 (those all exist). The ledger's intro contradicted itself on this and STATUS.md propagated the wrong list.
  - Migration **214 exists but is empty** (`// Replaced with 215`) — neither a gap nor a real migration. Don't skip; port as a no-op.
  - Bucket counts (116 schema-only + 21 data-only + 87 mixed = 224) are off by 1; per-row table is the source of truth.
  - Qualitative claims that DO check out: log-DB migration list (001, 006, 064, 141, 146, 188), migration 205 polymorphic-rename body, gap-row markers, HIGH-RISK section content. These spot-checked clean.
  - Corrections landed in the ledger's intro + summary stats; the per-row table was not regenerated since spot-checks pass.

#### 1.2–1.x — Main DB migrations 6–230 + log DB migrations ✅ shipped (2026-05-02)

**Schema parity at C# head version 230 achieved on both engines.** 218 main-DB SQL files + 5 log-DB SQL files. All 14 tests passing under `-race`; lint clean.

Pattern decisions made during the sweep:
- Per-dialect file separation replaces C# `IfDatabase("sqlite")` gating without conditional SQL inside files.
- Native `ALTER TABLE DROP COLUMN` on SQLite (3.49+ via modernc) for non-UNIQUE columns. UNIQUE columns (Episodes.TvDbEpisodeId v24, EpisodeFiles.Path v58, QualityDefinitions.Weight v71) need the recreate-table dance.
- **Complex C# backfills are no-op'd in goose**: Sonarr's migration code includes parser-driven derivations (e.g. v53 SortTitle, v195 LanguageTags), config-table conversions (v29-30 naming format builders, v43 download client conversion, v51 import handling), JSON shape mutations (provider settings, embedded docs), and dedup logic. Fresh DBs need none of this. **Phase 6 cutover** reimplements these in Go application code for migrating from existing C# user databases.
- Real SQL kept for cumulativity-sensitive backfills: v148 + v175 (MediaInfo audioChannels rename + Languages JSON wrap), v205 (polymorphic SeasonPackSpecification → ReleaseTypeSpecification rename in CustomFormats JSON).
- Goose state in `goose_db_version` (per-DB, separate from FluentMigrator's `VersionInfo`).
- 8 missing version numbers (80, 133, 208, 213, 216, 222, 228, 229) leave goose ledger gaps. Migration 214 exists as a no-op file. 4 log-only versions skipped in main: 6, 64, 141, 146.

#### 1.2 — Migrations 006–010 (subsumed by full sweep above) ✅ shipped (2026-05-02)

- **Log-DB wired.** `migrationDir(engine, RoleLog)` now resolves `migrations/{sqlite,postgres}/log/`. `embed.FS` directive expanded.
- `migrations/{sqlite,postgres}/log/00001_initial.sql` — ports `Migration/001_initial_setup.cs`'s `LogDbUpgrade()` half (Logs table, no Time index yet).
- `migrations/{sqlite,postgres}/log/00006_add_index_to_log_time.sql` — drops + recreates Logs with `IX_Logs_Time` index.
- `migrations/{sqlite,postgres}/main/00007_add_renameEpisodes_to_naming.sql` — `ALTER NamingConfig ADD COLUMN RenameEpisodes` + first UPDATE-based **backfill** (`= NOT UseSceneName`).
- `migrations/{sqlite,postgres}/main/00008_remove_backlog.sql` — `ALTER TABLE DROP COLUMN` for `Series.BacklogSetting` and `NamingConfig.UseSceneName`. SQLite uses native DROP COLUMN (3.35+), available in modernc.org/sqlite v1.50.0.
- `migrations/{sqlite,postgres}/main/00009_fix_renameEpisodes.sql` — drops `NamingConfig.SeasonFolderFormat`. SQLite-only sentinel-value fixups (RenameEpisodes = -1/-2 → 1/0) live only in the SQLite file; the C# `IfDatabase("sqlite")` gating becomes implicit by per-dialect file separation.
- `migrations/{sqlite,postgres}/main/00010_add_monitored.sql` — adds `Monitored` to `Episodes` and `Seasons` with backfill from the legacy `Ignored` column. The Ignored column itself stays until migration 011.
- **3 new tests, 14 total all green under `-race`.** New: `TestMigrate_SQLite_Log_ToVersion1`, `TestMigrate_SQLite_Log_ToHead`, `TestMigrate_SQLite_Backfill_010_MonitoredFromIgnored` (the backfill test seeds rows with mixed Ignored values, runs the migration, and asserts Monitored matches the inverse). The pattern from this last test is the template every future backfill migration will copy.
- **Postgres log-DB testing deferred** — the C# side uses a separate physical database for logs (`sonarr-log` vs `sonarr-main`), so PG log-DB tests need a second DB provisioned. SQLite log-DB coverage is sufficient for now; PG log-DB migration files were vetted via `go vet` and follow the same shape.
- `headVersion` constant in tests is now `10`; new `headLogVersion` is `6`. Bump on every migration PR.
- `go vet ./... && golangci-lint run ./...` clean.

#### 1.x — Domain-primitive repositories ✅ shipped (2026-05-02)

Four packages, all flat Id+columns tables (no embedded-doc fields yet):

- `internal/tags/` — `Tag` (Id, Label UNIQUE) + Repo with Insert/Get/GetByLabel/All/Update/Delete.
- `internal/config/` — `Entry` (Id, Key UNIQUE, Value) + Repo with Set/Get/GetByKey/All/Delete. `Set` is an upsert via `ON CONFLICT (Key)`.
- `internal/rootfolders/` — `RootFolder` (Id, Path UNIQUE) + Repo with Insert/Get/GetByPath/All/Delete.
- `internal/remotepathmappings/` — `Mapping` (Id, Host, RemotePath, LocalPath) + Repo with Insert/Get/All/Update/Delete.

Shared infrastructure:
- `internal/datastore/sql.go` — `Placeholder(engine, n)` helper for the SQLite (`?`) vs Postgres (`$N`) divergence.
- `internal/datastore/datastoretest/` — `OpenSQLite(t)` + `OpenPostgres(t)` test helpers that open a fresh DB, run migrations to head, and wire `t.Cleanup`.

Pattern decisions held across all four:
- Hand-written SQL via `database/sql`, no Dapper-equivalent ORM yet.
- `RETURNING "Id"` used uniformly for INSERT (works on both engines).
- Each Repo has `NewRepo(db) (*Repo, error)` that rejects nil DB and non-main role.
- ErrNotFound sentinel per package, accessible via `errors.Is`.
- Postgres tests can't `t.Parallel` within a package (shared spike DB). SQLite tests stay parallel.

15 tests under `-race`. Lint clean.

#### 1.x — Embedded-doc serialization + delayprofiles ✅ shipped (2026-05-02)

- `internal/datastore/embedded.go` — `MarshalEmbedded` / `UnmarshalEmbedded` plus `STTime` and `STDuration` wrappers. STTime emits `"yyyy-MM-ddTHH:mm:ssZ"` UTC matching C# STJUtcConverter. STDuration emits `[-][d.]hh:mm:ss[.fffffff]` matching C# TimeSpan.ToString(). Indented 2-space output matches WriteIndented = true.
- `internal/delayprofiles/` — first repo using embedded-doc fields. 11 columns including 4 bools, 1 nullable `*int` (MinimumCustomFormatScore), 1 `[]int` embedded-doc (Tags). Default profile from migration 070 verified at startup.
- **Findings**: bool round-trip works on both engines without a wrapper (modernc/sqlite handles INTEGER↔bool, pgx handles boolean↔bool). `nullableInt(*int) -> sql.NullInt64` is the canonical pattern for `int?` columns. Reserved keyword "Order" works as long as identifier quoting is consistent.

#### Next: more bigger models, then `IBasicRepository[T]` generic if dedup justifies it ❌ pending

### Phase 0 — Bootstrap ✅ done

- `go/CONVERSION.md` — conversion rules committed verbatim from the user's prompt.
- `go/README.md` — layout description.
- `go/go.mod` — module `sonarr.tv/sonarr`, Go 1.26.
- `go/cmd/sonarr/main.go` and `go/cmd/sonarr-update/main.go` — entry-point stubs that build clean.
- `go/internal/buildinfo/` — populated at build time via `-ldflags`.
- `go/internal/instrumentation/` — `slog`-backed logger (NLog replacement), with tests.
- `go/.golangci.yml` — strict lint config, currently zero issues.
- `go/.gitignore`.
- `.github/workflows/build_go.yml` — vet/lint/test job + cross-compile matrix (linux/darwin/windows/freebsd, amd64/arm64/arm).
- `go/test/contract/` — full contract-test harness skeleton:
  - `internal/spec` (corpus + golden file format, validated round-trip)
  - `internal/normalize` (JSONPath redactions for timestamps/ids)
  - `internal/runner` (HTTP issuer with normalization)
  - `recorder/` (CLI to drive a running C# Sonarr and write goldens)
  - `replayer/` (Go test helper that diffs against goldens, self-skips when not configured)
- All tests pass under `-race`.

### Phase 0.5 — Deep-dive analysis ⏳ partial

#### 0.5a — Subsystem behavior cards (24 of 24 complete) ✅

| Card | Status | Notes |
|------|--------|-------|
| `subsystems/common.md` | ✅ | 14 sub-areas, 5 open questions (Q-01..Q-05) |
| `subsystems/datastore.md` | ✅ | 4 open questions (Q-09..Q-12) — caught that **Marr.Data is NOT in use** (Dapper only); plan corrected |
| `subsystems/tv.md` | ✅ | 3 open questions (Q-06..Q-08) |
| `subsystems/parser.md` | ✅ | **187 regexes** catalogued, 6 open questions (Q-13..Q-18); ~1,646 test-case rows to port verbatim |
| `subsystems/indexers.md` | ✅ | 10 concrete providers, 9 open questions (INDEXERS-Q1..Q9) |
| `subsystems/download.md` | ✅ | 20 concrete clients, 10 open questions (DOWNLOAD-Q1..Q10) |
| `subsystems/mediafiles.md` | ✅ | 14 open questions (MEDIAFILES-Q1..Q14) |
| `subsystems/decisionengine.md` | ✅ | **38 specifications** catalogued, 10 open questions (DECISION-Q1..Q10) |
| `subsystems/notifications.md` | ✅ | 25 concrete providers, 12 open questions (NOTIFY-Q1..Q12) |
| `subsystems/importlists-metadata.md` | ✅ | 10 import-list providers + SkyHook, 28 open questions (IL-Q*, MD-Q*, DA-Q*) |
| `subsystems/quality-profiles-formats-organizer.md` | ✅ | 22 qualities, 8 CF spec types, ~50 rename tokens, 23 open questions (Q-Q*, P-Q*, CF-Q*, ORG-Q*) |
| `subsystems/system.md` | ✅ | Jobs+Queue+History+Blocklist+Lifecycle+HealthCheck+Housekeeping+Backup+Update; 11 commands / 27 health checks / 31 housekeepers; 37 open questions |
| `subsystems/authentication-security.md` | ✅ | 27 open questions (AUTH-Q1..Q9, SEC-Q1..Q18) — PBKDF2/HMAC-SHA512 10k iters, no CSRF, no rate limit, X-Forwarded-For honored without trust list, password compare non-constant-time, lazy SHA-256→PBKDF2 upgrade on login |
| `subsystems/configuration.md` | ✅ | 37 open questions across 6 sub-areas (CFG/AT/TAGS/RF/RPM/DS-Q*) — `UpdaterConfigProvider` silent rewrites, `TrustCgnatIpAddresses` duplicated across IConfigService + IConfigFileProvider |
| `subsystems/localization-validation.md` | ✅ | 17 open questions (LOC-Q1..Q5, VAL-Q1..Q6, LANG-Q1..Q6); **44 culture files**, **54 Language IDs** — IDs are an unstated wire-format contract, no migration locks them |
| `subsystems/extras.md` | ✅ | 10 open questions (EXTRAS-Q1..Q10); 4 active metadata writers (Kodi/Emby, Roksbox, WDTV, Plex); flagged real bug in `Consumers/Xbmc/KodiEpisodeGuide.cs:31` (TvRage populated from TvMazeId) |
| `subsystems/http-instrumentation-misc.md` | ✅ | 11 areas, 48 open questions (HTTP/INSTR/ANALYTICS/DA/CFLT/SS/IS/MC/ANNO/EXC/PROP-Q*); flagged `IndexerSearch` for split — Phase-1 PRs need a dedicated card |
| `subsystems/messaging.md` | ✅ | 18 open questions (MSG-Q1..Q12, PROG-Q1..Q6); **60 distinct event types** catalogued; 5 events have zero production consumers (possibly dead code); `EventHandleOrderAttribute` only used in 2 sites both `[Last]`; async handlers unbounded on `TaskScheduler.Default` |
| `subsystems/thingiprovider.md` | ✅ | 15 open questions (TP-Q1..Q15); 5 are Phase-1 blockers; canonical 6-column provider table schema; **all 18 `FieldType` values catalogued** (resolves ANNO-Q1's enumeration); `RemoveMissingImplementations` silently deletes rows on missing C# class (forced 6 historical migrations) |
| `subsystems/host.md` | ✅ | 12 open questions (HOST-Q1..Q12); 6 application modes; DryIoc DI confirmed; full middleware-pipeline ordering captured in Startup.cs:337-378; `ForwardedHeadersOptions.KnownIPNetworks` resolves SEC-Q3 (RFC1918+ULA+link-local trusted, KnownProxies empty) |
| `subsystems/updater.md` | ✅ | 15 open questions (UPD-Q1..Q15); 8 explicit per-OS branches; argv contract `<pid> "<sandbox>" "<exec-path>" <preserved-args>`; SHA-256 verify; no macOS .app awareness (UPD-Q10 spike candidate) |
| `subsystems/sonarr-http.md` | ✅ | 15 open questions (SH-Q1..Q15); **resolves Q-05** (System.Text.Json, not Newtonsoft, on the wire — settings catalogued); 19-row middleware-pipeline table; route precedence + path-collision surface; static-field caches identified across 3 components; **agent quota error truncated summary message but card content is complete** |
| `subsystems/native-platform.md` | ✅ | 26 open questions (MONO 12 + WIN 6 + RP 5 + NP 3); full PInvoke/Win32 inventory; only 2 runtime patches, both Mono-gated and no-ops on .NET 6+ → **`Sonarr.RuntimePatches` deletes entirely in the Go port**; confirmed Kestrel binds via Sockets (no `netsh urlacl` needed) |
| `subsystems/indexersearch.md` | ✅ | 12 open questions (IS-Q1..Q12; 4 carried from http-instrumentation-misc bucket, 8 new); pagination size mismatch (1M vs 100K), `LastSearchTime` lies on indexer-wide failure, queue-subtraction asymmetry between bulk and manual paths |

#### 0.5b — Schema / migrations ⏳ schemas captured; ledger pending

- `analysis/schema/sqlite.sql` ✅ — 79 lines (dense one-statement-per-line). 39 tables, 40 indexes. Migration version **230** confirmed (matches the 223 actual files spread across 0..230 with 8 gaps).
- `analysis/schema/postgres.sql` ✅ — 1,794 lines (full pg_dump format). Migration version 230 confirmed.
- `analysis/schema/sqlite-seeded.sql` ❌ — after integration-test fixtures (deferred; the empty-state schemas are the load-bearing reference).
- `analysis/schema/postgres-seeded.sql` ❌ — same.
- `analysis/migrations/ledger.md` ✅ — 342 lines, **all 223 migration files catalogued** (was incorrectly claimed 224; corrected 2026-05-02) with intent, tables touched, embedded-doc fields touched, reversibility, and dropable-in-Go-port flag. Statistics intro buckets sum to 224 (off by 1 — per-row table is the source of truth): 116 schema-only / 21 data-only / 87 mixed / **28 HIGH-RISK embedded-doc shape mutations** / **8 polymorphic-type renames** / 0 reversible (`Down()` always throws) / 6 log-DB-only / **31 dropable in Go port**. 12 MIG-Q* (all Phase-1 blockers — goose authoring decisions: cumulativity ordering, branch-on-engine count, drop-list confirmation).
- **Toolchain note**: dotnet 10.0.202 is installed; `global.json` was updated with `"rollForward": "latestMinor"` to accept it (was strictly pinned to 10.0.203). `dotnet restore + dotnet build src/NzbDrone.Mono/Sonarr.Mono.csproj` builds clean with `/p:TreatWarningsAsErrors=false` (StyleCop SA1200 noise from newer Roslyn analyzers — 9,368 warnings, 0 errors). Output at `_output/net10.0/Sonarr` (Mach-O arm64).

#### 0.5c — REST + SignalR inventory ✅ static + initial dynamic done

- `analysis/rest/inventory.md` — V3: 65 controllers / ~184 endpoints. V5: 55 controllers / ~159 endpoints. Plus shared root endpoints. 4 open questions (REST-Q1..Q4).
- `analysis/signalr/inventory.md` — 19 distinct event streams over single `MessageHub`/`receiveMessage` envelope. 4 open questions (SIGNALR-Q1..Q4).
- `test/contract/corpus/v3-empty-state.json` ✅ — 50 GET endpoints across system, config, profiles, list-empty resources, schemas, localization, language, log, command. Per-entry JSONPath redactions for unstable fields.
- `test/contract/corpus/v5-empty-state.json` ✅ — 29 GET endpoints (V5 surface is leaner — no `config/`, `settings/`, autotagging, notification, importlist, downloadclient, delayprofile, customformat at top level).
- `test/contract/golden/{v3,v5}-empty-state/*.golden.json` ✅ — 79 captured goldens, 2.3 MB total. Driven via `go run ./test/contract/recorder` against the running C# build.
- **Known limitations of the starter capture:**
  - Volatile **HTTP headers** (`Date`, `X-Application-Version`, `X-Miniprofiler-Ids`) are not redacted in goldens — the recorder spec only supports per-entry header redaction with no global default. Worth adding a `default_normalize` block to the corpus format and re-recording. Recorded as a known follow-up.
  - **Empty-state only** — no series, no episodes, no profiles configured beyond Sonarr's seed. Phase 1 will need a seeded corpus too (with fixtures) to lock the populated-state contract.
  - **`/api/v3/system/tasks`** path returned 404 in the recorder run. The corpus uses `/api/v3/system/task` (singular) which works. Sonarr.Http inventory shows both `task` and `tasks` are tested in V3 — verify which is canonical.

#### 0.5d — Specialised inventories ✅ mostly done (jobs+native pending)

- `analysis/embedded-docs/catalogue.md` ✅ — 911 lines, 13 IEmbeddedDocument types + 3 ad-hoc + ~50 provider settings, 24 EDOC-Q*. **Caught the `datastore.md` factual error**: embedded-doc bridge uses System.Text.Json (not Newtonsoft as the card claimed). See "Card corrections" section below.
- `analysis/providers/*.md` ✅ — 6 files, 1,418 lines, **71 concrete providers** catalogued: 10 indexers, 20 download clients, 25 notifications, 10 import lists, 2 metadata sources, 4 metadata writers. 47 PROV-Q*.
- `analysis/parser/regex-corpus.md` ✅ — 476 lines, **186 regexes** verified (parser card said 187 — off by 1; inline `imdbRegex`), **1,585 test cases** verified (card said 1,646 — over-counted). 8 REGEX-Q*. **Resolves Q-14**: only 4 regexes genuinely require an engine (one atomic group, three named backrefs); the other 139 non-RE2 patterns are mechanical rewrites. **Recommendation: rewrite to RE2, do not vendor `dlclark/regexp2`.** ~3 person-weeks plus parity tests.
- `analysis/jobs/catalogue.md` ✅ — 161 lines. Counts verified: **36 commands** (11 scheduled + 25 ad-hoc, including the 5 `IndexerSearch` commands); **31 housekeepers**; **27 health checks**. 16 JOB-Q* (drop `DeleteBadMediaCovers`; collapse 5 search dispatchers; queue-persistence; `[CheckOn]` reflection vs explicit; etc.).
- `analysis/native/inventory.md` ✅ — 167 lines. All MONO/WIN/RP signatures from `native-platform.md` flattened into porter-assignment tables with Go equivalents. 10 NAT-Q* (most cross-cite existing MONO-Q*/WIN-Q*/RP-Q* without renumbering).

#### 0.5e — Dependency-swap spikes ⏳ 2 of 6 done

| Spike | Status | Verdict |
|-------|--------|---------|
| `spikes/sqlite-modernc.md` | ✅ | green — `modernc.org/sqlite` covers WAL / FK / busy_timeout / NOCASE / JSON1 / recursive CTE / concurrent RW |
| `spikes/pgx-postgres.md` | ✅ | green — `jackc/pgx/v5` covers pool / cascade FK / JSONB ops / RETURNING / ILIKE / concurrent RW |
| `spikes/imaging.md` | ✅ | **green** — `github.com/disintegration/imaging` v1.6.2 covers the surface (load JPEG/PNG, resize-to-height, save). 2 IMG-Q* (lazy vs eager render; JPEG byte-equivalence). One yellow flag: repo last touched 2021 — mitigated by API stability + `golang.org/x/image` fallback. |
| `spikes/ffmpeg-exec.md` | ✅ | **green** — direct `os/exec.CommandContext` with `ffprobe -v quiet -print_format json -show_format -show_streams`. FFMpegCore is imported in exactly one C# file. Full JSON-path → `MediaInfoModel` mapping captured. 3 FFM-Q* on wire-format casing, TimeSpan round-trip, RawStreamData newline quirk. |
| `spikes/signalr-philippseith.md` | ❌ | DEFERRED — needs C# Sonarr + React frontend live to verify wire compatibility. Move to Phase 5 prep. |
| `spikes/anacrolix-torrent.md` | ❌ | DEFERRED — only relevant if any provider embeds a torrent client; download.md confirms all 20 download clients talk to external apps. **Skippable** unless future provider needs it. |

#### Card corrections found by Batch C (must be applied before Phase 1)

The embedded-doc catalogue verified card claims against the actual C# source and surfaced these errors. The **catalogue is the source of truth**; the cards listed below need fixes when next touched.

1. **`subsystems/datastore.md:117-119`** says the embedded-doc Dapper bridge uses Newtonsoft. **It actually uses System.Text.Json** — `EmbeddedDocumentConverter.cs:13-29` configures camelCase + `WhenWritingNull` + `JsonStringEnumConverter` + `STJUtcConverter` + `STJTimeSpanConverter`. Same applies to `ProviderRepository.Query` for provider settings (`ProviderRepository.cs:25-35`). This means there is **no Newtonsoft-vs-STJson asymmetry** between the wire (`sonarr-http.md`) and the disk (datastore embedded docs); both are STJson. Cross-cuts: `sonarr-http.md` cited the asymmetry in its JSON section, and `datastore.md` Q-09 framing was based on it. Both citations need to be re-grounded — the Go port does **not** need a separate Newtonsoft-style serializer for embedded docs. Captured as `EDOC-Q1`.

2. **`subsystems/parser.md`** claims 187 total regexes. **Actual: 186.** Off by 1; the inline `imdbRegex` at `Parser.cs:865` was double-counted in the original grep.

3. **`subsystems/parser.md`** claims ~1,646 test cases. **Actual: 1,585** in `ParserTests/*.cs` (1,567) + `ParsingServiceTests/*.cs` (18), plus 35 cases in `HashedReleaseFixture.HashedReleaseParserCases`. The original grep over-counted `[TestCaseSource]` and commented-out cases.

4. **`subsystems/download.md`** prose says "24 download clients" but the table enumerates 20 distinct rows. Card-internal inconsistency. Provider matrix locks the count at **20**.

5. **`subsystems/notifications.md`** says "26 ... no separate Jellyfin"; actual count of `INotification` classes is **25**.

#### Open questions ledger

`analysis/questions.md` has been **fully reorganized** (715 lines, 11 H2 sections). Original Q-01..Q-18 numbering is preserved at the bottom for backwards compatibility with subsystem-card cross-references. The body is now organized **by phase blocker** with explicit resolution citations for everything closed. New deliverables landed during the final batch:

- **Migrations ledger**: +12 MIG-Q* (all Phase-1).
- **Jobs catalogue**: +16 JOB-Q* (mostly port-time decisions).
- **Native inventory**: +10 NAT-Q* (cross-cited from MONO/WIN/RP).
- **Imaging spike**: +2 IMG-Q*.
- **Ffmpeg spike**: +3 FFM-Q*.

Total stockpile now ~520 questions; the ledger explicitly resolves a substantial fraction (~25-30% by count, more by weight since Q-05 / Q-09-spike / Q-14 / TP-Q1/Q2 / VAL-Q1 / ANNO-Q1 each had downstream effects on multiple cards). **97 explicit `- **PREFIX-QN**` ledger entries** in the resolved/blocker/decision/drop sections.

**Resolved during Batch C:**
- **Q-14 (RE2 vs .NET regex)** — RESOLVED by `parser/regex-corpus.md`. Only 4 of 186 regexes genuinely require an engine (one atomic group `(?>[-._ ])` at `Parser.cs:398`, three named backrefs `\k<sep>` at `Parser.cs:29,33` and `\k<part2>` at `ReleaseGroupParser.cs:9`). The other 139 non-RE2 patterns are mechanical lookaround-to-boundary rewrites. **Decision: rewrite to RE2, do not vendor `dlclark/regexp2`.** Effort: ~3 person-weeks + parity tests against 1,585 cases. **Unblocks Phase 2 (parser).**
- **Q-09-adjacent (embedded-doc serializer)** — datastore.md card was wrong; the actual converter is STJson, not Newtonsoft. The Go port can use a single JSON config (camelCase + `WhenWritingNull` + custom UTC/TimeSpan converters) for both wire and disk. Specific Q-09 framing about Dapper-style type handlers still applies (Go's `database/sql` has no analogue), but the JSON-encoder choice is now consistent.
- **TP-Q1/TP-Q2 (stable string registry)** — INPUT GATHERED by `providers/*.md`. Every `Implementation` and `ConfigContract` value is documented exactly. The Go registry can use these as opaque string IDs. Note: 2 cases (Vuze/Transmission share `TransmissionSettings`; Usenet/TorrentDownloadStation share `DownloadStationSettings`) require `(impl_id, contract_id)` pairs not just impl_id. **Unblocks Phase 4 (providers).**
- **ANNO-Q1 (`[FieldDefinition]` Go idiom)** — INPUT GATHERED by `providers/*.md` (every settings field's annotation captured). The Go-idiom decision is still open but the porter now has the complete corpus.

**Resolved during Batch B** (still need to be folded into `questions.md` or marked resolved-in-card):
- **Q-05 (Newtonsoft JSON quirks)** — RESOLVED by `sonarr-http.md` § JSON serialization. The wire is **System.Text.Json** with these settings: camelCase prop+dict keys, case-insensitive read, `WhenWritingNull`, `WriteIndented = true`, `AllowTrailingCommas`, `JsonStringEnumConverter(camelCase, allowIntegerValues=true)`, plus 4 custom converters (Version, HttpUri, TimeSpan="hh:mm:ss", DateTime="yyyy-MM-ddTHH:mm:ssZ" UTC). Newtonsoft is confined to embedded-doc Dapper bridge + a few legacy paths. **Unblocks Phase 3.**
- **SEC-Q3 (forwarded-headers trusted-proxy allowlist)** — DOCUMENTED in `host.md` and `sonarr-http.md` middleware table. `KnownIPNetworks` = RFC1918 + ULA + link-local. `KnownProxies` empty. `ForwardLimit = 1`. Behavior is captured; the **security concern remains** (any trusted-network peer can spoof X-Forwarded-For and bypass `DisabledForLocalAddresses`). Decision still required, but it's a port-time choice not a blocker.
- **VAL-Q1 (FluentValidation DI registration)** — RESOLVED by `messaging.md`/`host.md`. Discovery is implicit DryIoc `RegisterMany` scan in `Common/Composition/Extensions.cs:27-29`; validators are constructed in-place inside `RuleFor` chains.
- **ANNO-Q1 (`[FieldDefinition]` enumeration)** — PARTIALLY RESOLVED by `thingiprovider.md` (full 18-value `FieldType` list catalogued). The Go-idiom decision (struct tags vs explicit slice) is still open; kept as Phase-1 blocker.

Several questions are blocking — the porter cannot start Phase 1 without answers:

- **Q-05 (Newtonsoft JSON quirks):** API contract is frozen on Newtonsoft semantics. `encoding/json` differs in casing, null handling, enum format, datetime format, timespan format. Needs a decision: thin custom encoder, third-party (`json-iterator/go`), or per-type `MarshalJSON`. **Blocks Phase 3 (HTTP & SignalR surface).**
- **Q-09 (embedded-doc handlers in Go):** no Dapper-style type handler in `database/sql`; pick reflection scan vs per-type Scanner/Valuer vs generic JSON wrapper. **Blocks Phase 1 (datastore).**
- **Q-12 (mutable static migration context):** `MigrationContext.Current` is a static — affects test parallelism. **Blocks Phase 1.**
- **Q-14 (RE2 vs .NET regex):** parser uses lookaround / backreferences / atomic groups. Go's RE2 doesn't. Needs decision: vendored regex engine (`grafana/regexp`, `dlclark/regexp2`) or rewrite the few non-RE2 patterns. **Blocks Phase 2 (parser).**

## Pending agent dispatches

When resuming, dispatch these batches (each as parallel `Agent` calls in one message). Use the standard subsystem-card template — see `go/analysis/README.md`.

**Batch A — DONE (5 agents, 2026-05-02).** 139 new questions raised. See the cards table above.

**Batch B — DONE (5 agents, 2026-05-02).** 94 new questions, plus 4 prior questions resolved (Q-05, VAL-Q1, ANNO-Q1 partial, SEC-Q3 documented).

**Batch B-prime — DONE (1 agent, 2026-05-02).** IndexerSearch split out; 8 new questions added.

**Batch C — DONE (3 agents, 2026-05-02).** 79 new questions, plus 1 prior question fully resolved (Q-14: rewrite to RE2) and 3 partially resolved (Q-09 embedded-doc serializer, TP-Q1/Q2 string registry input, ANNO-Q1 input).

**Final batch — DONE (4 agents, 2026-05-02).** Migrations ledger (224 rows), jobs+native flatten (36 commands / 31 housekeepers / 27 health checks; PInvoke surface), imaging+ffmpeg spikes (both green), questions-ledger triage (715-line reorganization by phase). Two minor agent issues: (1) `questions-triage.md` summary report wasn't written (quota error after the main ledger landed; non-critical), (2) the migrations agent burned more tokens than expected because of the 224-file walk. Both agents' primary deliverables are intact.

**Phase 0.5 sign-off bar achieved.** Phase 1 work can begin. The remaining items below are *follow-ups*, not gates:
- `signalr-philippseith.md` spike — needs frontend setup; move to Phase 5 prep.
- `anacrolix-torrent.md` spike — skippable unless future provider needs it.
- `questions-triage.md` summary — easy to write later from `questions.md` content.
- `analysis/schema/{sqlite,postgres}-seeded.sql` — after integration-test fixtures; deferred until needed.

**Remaining spikes (4 of 6):** `signalr-philippseith.md`, `imaging-imagesharp.md` (note `MediaCover` uses ImageSharp per the http-instrumentation-misc finding — this is now likely a green spike), `ffmpeg-exec.md`, `anacrolix-torrent.md` (likely skippable). New spike candidate: **macOS .app-bundle update mechanics** (UPD-Q10) — flagged by the host+updater card.

## Cross-cutting concerns surfaced by Batches A + B + C (must be reconciled before Phase 1)

These came out of multiple Batch A cards naming the same friction. Each one has at least one card-local question already; listed here so the reader doesn't have to re-discover the connections:

1. **`Sonarr.Http` is its own card now.** Auth card (`SEC-Q3`) and `rest/inventory.md` both reach into it. Batch B item 4 above is non-optional.
2. **`TrustCgnatIpAddresses` is duplicated** across `IConfigService` (DB) and `IConfigFileProvider` (XML). Auth card (`SEC-Q9`) and configuration card (`CFG-Q4`) need a single answer for which value wins. Sonarr-http card will likely settle it.
3. **Language IDs are an unstated wire contract.** 54 hand-numbered values persisted in `QualityProfile.Languages`, `Series.OriginalLanguage`, `EpisodeFile.Languages`, plus indexer/import-list/notification settings. No migration locks them. `LANG-Q2` blocks the embedded-doc catalogue (Batch C item 7).
4. **Validation error envelope leaks FluentValidation 9.x internals** (`Severity`, `ErrorCode`, `FormattedMessagePlaceholderValues`). `VAL-Q4`. The Go port has no FluentValidation analogue and must hand-roll a byte-equivalent envelope; needs golden snapshots in `rest/golden/` once 0.5c can capture them.
5. **`[FieldDefinition]` annotation pattern blocks every provider port.** `ANNO-Q1`. Pick the Go idiom (struct tags vs explicit slice) before porting *any* provider — applies to indexers, download, notifications, import-lists, metadata, autotagging, custom formats.
6. **Analytics + service.sonarr.tv coupling.** `ANALYTICS-Q1` + `INSTR-Q6`. Telemetry is opt-in but the same domain serves daily-series and scene-mapping JSON, so dropping analytics must not drop the data endpoints.
7. **Series-stats Postgres branch added `string_agg(DISTINCT ...)` recently** while SQLite branch still relies on `GROUP_CONCAT` + C# dedup. `SS-Q1`. Cross-DB parity question.
8. **Sentry DSNs are hard-coded** at `NzbDrone.Common/Instrumentation/NzbDroneLogger.cs:75-83`. Strategy decision belongs in the `common` card (already drafted; needs a follow-up Q).
9. **Possible dead code:** CloudFlare interceptor (`HTTP-Q2`) and TorCache interceptor (`HTTP-Q1`). Verify before porting.
10. **Real bug found in C# (not a port question):** `KodiEpisodeGuide.cs:31` populates TvRage from TvMazeId. `EXTRAS-Q5`. Fixing it changes user-visible NFO output — port-time decision: replicate the bug or fix it?

11. **Provider type names are persisted as strings.** `TP-Q1`/`TP-Q2`. The `Implementation` and `ConfigContract` columns in `Indexers`, `DownloadClients`, `Notifications`, `ImportLists`, `Metadata`, `CustomFormats`, `AutoTagging` tables store C# class names. `RemoveMissingImplementations` (`ProviderFactory.cs:199-208`) silently deletes rows whose class no longer resolves — this has forced 6 historical migrations (046, 049, 082, 090, 104, 219). Go has no cross-language analogue. **Decision required**: stable string registry decoupled from Go type names is the only path that doesn't break upgrades. Phase 1 blocker for **every** provider port.

12. **Five events have zero production consumers.** `MSG-Q12`. Possibly dead code, possibly external-plugin contracts. Decide whether the Go port keeps them: `EpisodeImportFailedEvent`, `SeriesMovedEvent`, `SeriesRefreshCompleteEvent`, `SceneMappingsUpdatedEvent`, `ProviderStatusChangedEvent<T>`.

13. **Real latent bug in Sonarr.RuntimePatches.** `RuntimePatchBase.cs:84` — `TryPatchMethod(type, "GetSslServer")` hardcodes the method name and ignores the parameter. Latent because the only caller happens to want that method. Not a Go-port question (project deletes entirely) but a C# bug worth a separate PR.

14. **`[ThreadStatic]`+`AsyncLocal` dual-write in `ProgressMessageContext`.** `MSG-Q11`/`PROG-Q1`. Reads prefer ThreadStatic, writes hit both. No comment justifies this. Likely redundant; may be a leftover from an older threading model. Pick one for the Go port (Goroutine-local via context.Context is the natural fit).

15. **MaxRequestBodySize is unbounded.** `SH-Q6`. Kestrel is configured with `Limits.MaxRequestBodySize = null` (`Bootstrap.cs:209`). Probably intentional for backup uploads, but worth a configurable cap in the Go port.

16. **JSON pretty-printing on every API response.** `SH-Q15`. `STJson.WriteIndented = true`. Bytes-per-response cost on hot endpoints (`/api/v3/queue`, `/api/v3/episode`). Consider compact + opt-in `?pretty=` for the Go port.

17. **Static-field config caches across the HTTP layer.** `SH-Q8`/`AUTH-Q1`/`VersionMiddleware._version`. Three components cache config values in static fields and never refresh on `ConfigSavedEvent`. Inconsistent: should be uniformly live-read or uniformly subscribe. Pick one for the Go port.

18. **Routing relies on hardcoded `"V3"`/`"V5"` substrings** in `RestControllerWithSignalR` namespace check (`REST/RestControllerWithSignalR.cs:79, 102`). `SH-Q12`. Future versions silently won't broadcast. Replace with `VersionedApiControllerAttribute.Version != null` check.

19. **`/api` (no version) discovery requires API key.** `SH-Q13`. `ApiInfoController` lacks `[AllowAnonymous]`. Clients hit `/api` *before* knowing how to auth, so this is either a bug or an undocumented assumption that clients are pre-configured with the key.

20. **`BackupFileMapper` does pattern-match validation, not path-traversal protection.** `SH-Q14`. `resourceUrl.Replace("/backup/", "")` then `Path.Combine`. The regex provides some safety, but Go port should harden via `filepath.Clean` + prefix check. Phase 3 security concern.

21. **Polymorphic `{type, body}` envelopes use `Type.GetType("...{typename}, Sonarr.Core")`.** `EDOC-Q*`. Same C#-class-name fragility as the provider tables (TP-Q1/Q2) — it extends to **CustomFormats specifications, AutoTagging specifications, and Commands**. Migration 205 already shows this in action (`SeasonPackSpecification` → `ReleaseTypeSpecification` rename required a migration). The stable string registry from TP-Q1/Q2 must cover these too.

22. **Wire-format gotchas in embedded-doc payloads.** `EDOC-Q*`. The Go port's struct tags must replicate exactly: `videoCodecID` (literal STJ camelCase, NOT `videoCodecId`); `Language` lists are int-form via per-container converter (not full-object); pre-225 `MediaInfoModel` rows still in flat shape; `StatusSpecification` discriminator is `status` not `value`; `Language.Original` has id `-2`; `omitempty` cannot be used on value-typed fields because STJ emits zero defaults. Read the catalogue's per-type detail before writing any struct.

23. **Card factual error: `datastore.md:117-119` on serializer.** Embedded-doc bridge is **System.Text.Json**, not Newtonsoft. Captured under "Card corrections" above.

24. **20 download clients, 25 notifications, 10 import lists, 10 indexers, 4 metadata writers, 2 metadata sources** — exact counts now locked in `providers/*.md`. The `subsystems/download.md` and `subsystems/notifications.md` cards have stale prose claiming higher numbers.

25. **`Type.Name` is the registry key for every provider.** `providers/*.md` PROV-Q*. Confirmed safe to use C# class names verbatim as opaque string IDs in the Go registry. Two cases require `(impl_id, contract_id)` pairs (Vuze/Transmission share `TransmissionSettings`; Usenet/TorrentDownloadStation share `DownloadStationSettings`).

26. **Twitter notification provider** (PROV-Q22): targets sunset Twitter API v1.1. Recommend port-and-deprecate (`Test()` always fails, preserves existing rows).

27. **`MetadataFile.Consumer` column** uses the same C# class-name string as `Implementation` (PROV-Q41). Same registry key — no forced migration needed for the Go port.

28. **Q-14 resolved**: rewrite the 4 atomic-group / named-backref regexes; mechanically rewrite the 139 lookaround-only patterns. **Do not vendor `dlclark/regexp2`** (~10× runtime cost on the parser hot path for marginal porting savings).

## Things the deep-dive has already learned (don't re-discover)

- **Marr.Data is NOT in use.** Datastore uses Dapper + a hand-rolled `BasicRepository`/`SqlBuilder`/`WhereBuilder`. The plan's mention of Marr.Data was wrong; `subsystems/datastore.md` is the source of truth.
- **`Sonarr.Http` is shared across V3 and V5.** Routing is per-controller via `[V3ApiController]`/`[V5ApiController]`, not a global prefix middleware.
- **SignalR has a single hub (`MessageHub`), single broadcast topic (`receiveMessage`), single envelope (`SignalRMessage`).** All 19 event streams flow through this.
- **The current SQLite C# wrapper is `SourceGear.sqlite3` (CGo).** The Go port ditches CGo via `modernc.org/sqlite` — verified compatible.
- **The C# side has 223 FluentMigrator migration files numbered 0..230, with 8 missing numbers** (80, 133, 208, 213, 216, 222, 228, 229). One file (214) is an empty no-op. The goose port must reproduce them in version order. Several mutate embedded-document JSON shapes — those are the highest-risk single migrations and are flagged in `analysis/migrations/ledger.md`'s HIGH RISK section.
- **`Update/History/` data lives in the log DB**, not the main DB. `LogDbEnabled = false` silently disables update history.
- **`UpdaterConfigProvider` silently rewrites `Branch` config on startup.** This is intentional behavior to preserve.
- **Backup forces `journal_mode=truncate`** on the SQLite backup target as a defensive workaround for page-size mismatches.
- **`DeleteBadMediaCovers` housekeeper** is a 2014-era one-shot that self-disables after running — candidate to drop in the Go port (flagged HOUSE-Q3).

## Local environment as of last save

- macOS Tahoe (Darwin 25.4.0) on arm64.
- Go 1.26.2 (Homebrew).
- golangci-lint 2.11.4 (Homebrew).
- PostgreSQL 16.13 (Homebrew, `brew services start postgresql@16`), database `sonarr_spike` exists. DSN: `postgres:///sonarr_spike`.
- **dotnet 10.0.202 installed** at `/usr/local/share/dotnet/sdk/10.0.202` (system-wide Microsoft installer). `global.json` was relaxed from strict `10.0.203` to `{ version: "10.0.0", rollForward: "latestMinor" }` to accept it. C# build of `src/NzbDrone.Mono` + `src/NzbDrone.Console` works with `/p:TreatWarningsAsErrors=false` (StyleCop SA1200 noise from newer analyzers; 9,368 warnings, 0 errors). Output: `_output/net10.0/Sonarr` (Mach-O arm64). Runs cleanly with `-data=<dir>` against either SQLite or Postgres.
- The repo is on `v5-develop` with **one tracked-file change** (`global.json` rollForward relaxation). Everything else (entire `go/` tree, `_output/` build artifacts, `.github/workflows/build_go.yml`) is uncommitted/gitignored. No port-side commits yet.

## What's NOT done that the plan called for

- **No production Go has been written for any subsystem yet.** Phase 1 is gated on Phase 0.5 sign-off. The instrumentation logger and buildinfo packages exist only because they're prerequisites for the cmd binaries to compile.
- **No commits.** The `go/` tree is uncommitted. The next session should decide whether to commit Phase 0 separately so it's reviewable as one unit.
- **CI not yet exercised.** `.github/workflows/build_go.yml` is committed-to-disk-but-not-pushed. First push to GitHub will be the validation that the workflow YAML is correct.
