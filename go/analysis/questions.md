# Phase 0.5 questions ledger

This file is the canonical, triaged register of every open question raised during Phase 0.5 deep-dive analysis. The 24 subsystem cards under `analysis/subsystems/`, the catalogues under `analysis/{embedded-docs,providers,parser}/`, and the inventories under `analysis/{rest,signalr}/` each end with a card-local "## Open questions" section. Those sections remain the historical record. **This ledger is the source of truth for status.** Resolution requires a citation (a card, a catalogue row, a C# file:line, a STATUS.md "Resolved during Batch X" entry, or — in a few cases — an explicit porter decision). Phase 0.5 does not sign off until every question is either resolved here or marked as an accepted Phase-N+ blocker. Prefix conventions: `Q-NN` (the original 18 cross-cutting numbered entries, kept stable because subsystem cards reference them), and `<SCOPE>-Q<N>` for card-local entries (`AUTH-`, `SEC-`, `CFG-`, `MSG-`, `TP-`, `EDOC-`, `PROV-`, `REGEX-`, `INDEXERS-`, `DOWNLOAD-`, `MEDIAFILES-`, `DECISION-`, `NOTIFY-`, `IL-`, `MD-`, `DA-`, `Q-Q-`, `P-Q-`, `CF-Q-`, `ORG-Q-`, `JOBS-`, `QUEUE-`, `HIST-`, `BLOCKLIST-`, `LIFE-`, `HEALTH-`, `HOUSE-`, `BACKUP-`, `UPDATE-`, `UPD-`, `LOC-`, `VAL-`, `LANG-`, `EXTRAS-`, `MONO-`, `WIN-`, `RP-`, `NP-`, `HOST-`, `SH-`, `HTTP-`, `INSTR-`, `ANALYTICS-`, `CFLT-`, `SS-`, `IS-`, `MC-`, `ANNO-`, `EXC-`, `PROP-`, `PROG-`, `AT-`, `TAGS-`, `RF-`, `RPM-`, `DS-`, `REST-`, `SIGNALR-`, `LIFE-`).

---

## Resolved (do not re-discuss)

These questions have an answer with a citation and need no further porter attention. Subsystem cards may still list them as "unresolved" — the cards are not the source of truth.

### resolved-in-batch (STATUS.md "Resolved during Batch X")

- **Q-05** Newtonsoft JSON quirks → resolved by `analysis/subsystems/sonarr-http.md` § JSON serialization. Wire is **System.Text.Json** with documented options (camelCase prop+dict keys, `WhenWritingNull`, `WriteIndented = true`, `AllowTrailingCommas`, `JsonStringEnumConverter(camelCase, allowIntegerValues=true)` plus 4 custom converters: Version, HttpUri, TimeSpan="hh:mm:ss", DateTime="yyyy-MM-ddTHH:mm:ssZ" UTC). Newtonsoft is gone from the wire path. Embedded-doc bridge is also STJson, not Newtonsoft (see EDOC-Q1).
- **Q-09** (partial) Embedded-doc serializer choice → resolved by `analysis/embedded-docs/catalogue.md` (EDOC-Q1). Bridge is STJson per `src/NzbDrone.Core/Datastore/Converters/EmbeddedDocumentConverter.cs:13-29`. The Dapper-style type-handler question proper is still open as Q-09-residual below; the JSON-encoder choice is settled.
- **Q-14** RE2 vs .NET regex → resolved by `analysis/parser/regex-corpus.md`. Of 186 regexes, 4 use genuinely non-rewritable features (1 atomic group `(?>[-._ ])` at `src/NzbDrone.Core/Parser/Parser.cs:398`; named backrefs `\k<sep>` at `Parser.cs:29`,`Parser.cs:33`; `\k<part2>` at `src/NzbDrone.Core/Parser/ReleaseGroupParser.cs:9`). The other 139 lookaround uses are mechanical rewrites. **Decision: rewrite to RE2; do not vendor `dlclark/regexp2`.** Effort ~3 person-weeks plus parity tests against 1,585 test cases.
- **TP-Q1, TP-Q2** Stable string registry input → resolved by `analysis/providers/*.md` (every `Implementation` and `ConfigContract` value catalogued; 71 concrete providers). Two cases need `(impl_id, contract_id)` pairs because `ConfigContract` is shared (`TransmissionSettings` for Vuze+Transmission per `src/NzbDrone.Core/Download/Clients/Transmission/TransmissionSettings.cs`; `DownloadStationSettings` for Usenet+Torrent per `src/NzbDrone.Core/Download/Clients/DownloadStation/DownloadStationSettings.cs`).
- **ANNO-Q1** (partial) `[FieldDefinition]` enumeration → resolved by `analysis/subsystems/thingiprovider.md` (full 18-value `FieldType` list catalogued at `src/NzbDrone.Core/Annotations/FieldType.cs`). The Go-idiom decision (struct tags vs explicit slice) remains open as Phase-1 blocker (kept below).
- **VAL-Q1** FluentValidation DI registration → resolved by `analysis/subsystems/messaging.md` and `analysis/subsystems/host.md`. Discovery is implicit DryIoc `RegisterMany` scan in `src/NzbDrone.Common/Composition/Extensions.cs:27-29`; validators are constructed in-place inside `RuleFor` chains. Go port has no DryIoc analogue — explicit registration list required.
- **SEC-Q3** Forwarded-headers trusted-proxy allowlist (was: documented) → resolved by `analysis/subsystems/host.md` and `analysis/subsystems/sonarr-http.md`. `KnownIPNetworks = RFC1918 + ULA + link-local`, `KnownProxies = empty`, `ForwardLimit = 1` per `src/NzbDrone.Host/Startup.cs:66-68`. (NB: the *security concern* survives — see SEC-Q7 below — but the *behaviour* is documented.)

### resolved-in-card (a later card or catalogue cited the answer)

- **Q-13** `ReleaseTitleNormalizer.cs` does not exist → resolved by `analysis/subsystems/parser.md`. The grep prompt was stale; the closest C# equivalents are `Parser.SimplifyTitle / NormalizeTitle / NormalizeEpisodeTitle / CleanSeriesTitle`, the `ParserCommon.PreSubstitutionRegex[]` arrays, and `LanguageParser.CleanSeriesTitleRegex`. **Port-time decision (logged as port-time below):** consolidate into a single `releasetitle` package façade for the Go port, since the C# layout reflects historical accident, not API contract.
- **Q-17** `PreSubstitutionRegex` loop-mode asymmetry → resolved by `analysis/subsystems/parser.md` § Quirks. The asymmetry is **intentional** (Chinese-release titles need stacked substitutions before parse but only the first match matters for group extraction). Ported verbatim.
- **Q-18** Test fixture row counting → resolved by `analysis/parser/regex-corpus.md`. Verified count is 1,585 (1,567 ParserTests + 18 ParsingServiceTests + 35 hashed); the `static object[][]` form must port verbatim alongside `[TestCase]` rows. The "1,646" count in the original parser card was over-counting comments.
- **EDOC-Q1** datastore.md card claim of Newtonsoft → resolved by `analysis/embedded-docs/catalogue.md`. The bridge is **System.Text.Json** per `src/NzbDrone.Core/Datastore/Converters/EmbeddedDocumentConverter.cs:13-29`. Datastore card needs amendment.
- **EDOC-Q19** Dictionary key-policy asymmetry → resolved by `analysis/embedded-docs/catalogue.md`. STJ `DictionaryKeyPolicy = CamelCase` only affects writes; reads round-trip whatever case the cell stores. **Behaviour: Go port mirrors this asymmetry.**
- **EDOC-Q22** filename/class mismatch in `AutoTagSpecificationConverter.cs` → resolved-in-card; cosmetic, no DB impact.
- **PROV-Q2** Torznab dead `GetDefinition` setting Protocol=Usenet → resolved by `analysis/providers/indexers.md`. Confirmed dead via grep; Go port omits.
- **PROV-Q4 / INDEXERS-Q8** RateLimitKey by Definition.Id rather than host → resolved-in-card with **port-time decision** to switch to host-based.
- **PROV-Q24** Trakt/Simkl/AniList abstract settings bases → resolved-in-card. Skip in Go registry; only concrete subclasses get entries.
- **PROV-Q33** Add a MetadataSource ThingiProvider family → resolved-in-card: **no**, keep SkyHook fixed singleton in v1.
- **PROV-Q37** SkyHookProxy double-assignment → resolved-in-card; drop in Go port.
- **PROV-Q42 / PROV-Q43** MetadataDefinition.Tags / Test() → resolved-in-card; no behaviour change.
- **PROV-Q46 / PROV-Q47** Empty validator boilerplate, shared metadata-settings struct → resolved-in-card cosmetic decisions.
- **TAGS-Q1** Update raises a redundant `TagsUpdatedEvent` → resolved-in-card by configuration card; early-return prevents it.
- **DA-Q5** "Please notify Sonarr developers" wording → resolved as port-time wording fix (no functional change).
- **PROP-Q1** `InternalsVisibleTo` Go equivalent → resolved-in-card. Use same-package tests / `_test` suffix.
- **RP-Q1..RP-Q5** All Sonarr.RuntimePatches questions → resolved-in-card (`analysis/subsystems/native-platform.md`). **Disposition: delete the entire `Sonarr.RuntimePatches` project in the Go port.** Mono#19886 + Mono#16122 do not affect Go's `crypto/tls` / `compress/gzip`. Drop env vars `DISABLE_RUNTIMEPATCHES` and `DEBUG_RUNTIMEPATCHES`.

### resolved-in-source (verifiable directly from the C# tree)

- **Q-02** child-process group/kill semantics → resolved-in-source. `src/NzbDrone.Common/Processes/ProcessProvider.cs` calls `System.Diagnostics.Process.Kill()` which, on .NET 6+, kills only the target PID. **Decision: match exactly with `os.Process.Kill` in Go.** No downstream caller relies on tree-kill (the updater uses `Terminate` against process *names*, which finds and kills each PID separately).
- **Q-04** musl detection for OsInfo → resolved-in-source. `Os.LinuxMusl` only branches in `NzbDroneLogger.RegisterSentry` (Sentry tag) and `OsInfo.SubmitOsTag`. **Decision: ship one Linux binary; probe `/lib/ld-musl-*-*.so.1` at startup and tag accordingly.** No update channel branches on it.
- **Q-06** EpisodeFile JSON-LIKE cutoff query → resolved-in-source for **port-time decision**. `src/NzbDrone.Core/Tv/EpisodeRepository.cs:243-256` builds raw `LIKE '%_quality_: {id},%'` against the `EpisodeFiles.Quality` JSON column. The Postgres branch already uses the same string-LIKE shape (it is portable). **Decision: preserve LIKE on Phase 1.** A quality-id generated/computed column can be added later (Phase 3+) once the embedded-doc shape is locked.
- **Q-07** AbsoluteEpisodeNumberAdded transient field → resolved-in-source. `src/NzbDrone.Core/Tv/RefreshEpisodeService.cs` mutates the same `Episode` instance that flows on `EpisodeInfoRefreshedEvent` to `EpisodeRefreshedService`. The C# event bus is in-process only. **Decision: Go event bus is in-process; the field can stay on the struct (or be promoted onto the event payload).** Both work; cards prefer the latter for explicitness.
- **Q-08** RefreshSeriesCommand scheduled vs per-series → resolved-in-source. `src/NzbDrone.Core/Tv/Commands/RefreshSeriesCommand.cs` `UpdateScheduledTask` returns `true` only when `SeriesIds.Empty()`. **Decision: Go port models a single command shape with two scheduling modes (whole-library = tracked-instance for periodicity; per-series = ad-hoc).** Captured as a JOBS-* note already.
- **Q-10** SQLite LIKE case-sensitivity → resolved-in-source. `WhereBuilderSqlite.cs:354-385` emits raw `LIKE` with no `COLLATE`. SQLite default is `case_sensitive_like = OFF` (ASCII-CI). The Postgres branch uses `ILIKE` already. **Decision: preserve SQLite default in modernc; emit `ILIKE` on the pgx side.**
- **Q-11** Postgres extensions required → resolved-in-source. Migration scan: no `CREATE EXTENSION` in any migration. `AddPostgres15_0` only uses baseline 15.x features (JSONB, FOR UPDATE SKIP LOCKED, generated columns). **Decision: vanilla Postgres 15+ is sufficient; document minimum version.**
- **Q-12** MigrationContext static + concurrency → resolved-in-source. `src/NzbDrone.Core/Datastore/Migration/Framework/MigrationController.cs:71` mutates `MigrationContext.Current` inside `Migrate`. Test framework `NzbDrone.Core.Test.Framework` runs each migration test in its own AppDomain (serialised). **Decision: Go port uses `context.Context`-passed `MigrationContext`, not a package-level var.** Eliminates the race; equivalent semantics.
- **Q-15** Wall-clock dependency in daily-airdate validation → resolved-in-source. `src/NzbDrone.Core/Parser/Parser.cs:1133-1143`. **Decision: inject a clock interface (`internal/clock.Now() time.Time`); fixtures pin to a constant.**
- **Q-16** `RealRegex` over-match TODO → resolved-in-source. The TODO comment at `src/NzbDrone.Core/Parser/QualityParser.cs:700-701` calls out the bug. **Decision: preserve the buggy-but-stable behaviour.** Fixing it changes parsing for a non-trivial number of titles and breaks fixtures. Port-time TODO comment to be carried forward.
- **REST-Q1 / SIGNALR-Q1** Hub mount path → resolved-in-source. `src/NzbDrone.Host/Startup.cs:380` calls `endpoints.MapHub<MessageHub>("/signalr/messages")`. Path is **`/signalr/messages`**.
- **REST-Q2** Default authorization policy → resolved-in-source. `src/NzbDrone.Host/Startup.cs:240-242` sets `options.FallbackPolicy = options.GetPolicy("UI")`. UI policy denies anonymous when `AuthenticationMethod != None`.
- **REST-Q3** Frontend / static-asset routing → resolved-in-source. `src/NzbDrone.Host/Startup.cs:380-440` registers each `IMapHttpRequestsToDisk` mapper as an explicit endpoint. Inventory is enumerable from that file — no separate inventory pass needed if the porter walks `Startup.cs` once.
- **REST-Q4** V3 vs V5 dual-emit → resolved as **port-time decision**: dual-emit during V3 deprecation runway. Once V3 is dropped (post-Sonarr-v6), collapse to single emit + client-side filtering. (Documented under port-time decisions below.)
- **SIGNALR-Q2** Resource derivation for non-versioned controllers → resolved-in-source. `src/Sonarr.Http/REST/RestControllerWithSignalR.cs:23-33` falls back to `new TResource().ResourceName.Trim('/')`; `RestResource.ResourceName` defaults to `controller.Name.ToLowerInvariant()`. **Decision: emit lowercase resource names verbatim (`"series"`, not `"Series"`).**
- **SIGNALR-Q3** Action absent on envelope but present in body → resolved-in-source. `[JsonIgnore]` is intentional — every known SignalR consumer reads `body.action`. The frontend (`frontend/src/Store/Actions/`), Sonarr Mobile (out-of-tree but documented contract), and the C# integration tests all read body.action. **Decision: Go port preserves this — drop envelope.action.**
- **SIGNALR-Q4** OnConnectedAsync broadcasts `version` to all clients → resolved-in-source as **bug** but a deliberate one. `src/NzbDrone.SignalR/MessageHub.cs:41-58` uses `Clients.All` to ping the UI; the UI listens for it as a soft "reconnect/refresh" signal. **Decision: preserve in Go port.** Could be narrowed to `Clients.Caller` in a future enhancement; not Phase 0.5 scope.
- **HOST-Q3** Swagger JSON debug-only → resolved-in-source. `src/NzbDrone.Host/Startup.cs:364`. **Decision: Go port emits OpenAPI in dev only; production users consume the docs site's pre-generated artifact.**
- **HOST-Q6** No URL-ACL flow despite RegisterUrl → resolved-in-source. `src/NzbDrone.Host/AccessControl/` only configures Windows firewall; URL-ACL was dropped when Kestrel replaced HttpListener. **Decision: Go port drops `RegisterUrl` mode entirely.**
- **HOST-Q7** RuntimeInfo.IsWindowsService permanently false → resolved-in-source as **upstream C# bug**. `src/NzbDrone.Host/RuntimeInfo.cs:30` checks `hostLifetime is WindowsServiceLifetime`, but Sonarr registers `RestartableServiceLifetime`. Property is always `false` under the actual service install. **Logged below under "Upstream bugs"; Go port fixes by checking deployment-mode flag instead.**
- **HOST-Q8** Bootstrap.ASSEMBLIES hard-coded → resolved-in-source. `analysis/subsystems/host.md` confirms no plugin/extension story; Go port drops assembly scanning entirely.
- **AUTH-Q5** Hash round-trip on PUT /api/v3/config/host → resolved-in-source. `src/NzbDrone.Core/Authentication/UserService.cs:68` short-circuits when stored hash matches submitted value. **Decision: Go port preserves the contract verbatim — `GET /api/v3/config/host` returns the stored hash; PUT with the same hash is a no-op.** (Cross-references SEC-Q15, which is the security concern that survives.)
- **AUTH-Q7** "Authenticated anonymous" identity for AuthenticationType.None → resolved-in-source. `src/Sonarr.Http/Authentication/NoAuthenticationHandler.cs` issues `ClaimsIdentity("Anonymous")` which ASP.NET treats as `IsAuthenticated == true`. **Decision: Go port reproduces — issue a `*User{Anonymous: true, IsAuthenticated: true}` value.**
- **AT-Q7** SeriesTypeSpecification Order=2 rationale → resolved-in-source. The `Order` value drives display sort only; cosmetic. Drop the magic numbers; sort alphabetically in Go port.
- **EXTRAS-Q1** ImportedSubtitleFiles unreferenced → resolved-in-source via grep; **drop in Go port**.
- **MONO-Q3 / MONO-Q9 / MONO-Q12** GetUserId / IssueFile / Arch matching → resolved-in-source. All three are dead/vestigial; drop or tighten. Documented in card.
- **DOWNLOAD-Q5** Legacy DownloadClientType enum → resolved-in-source. Scan shows only the 4 legacy values consumed; not the source of truth for new clients. **Drop in Go port.**
- **NOTIFY-Q1** Redundant clauses in NotificationDefinition.Enable → resolved-in-source. The `(OnDownload && OnUpgrade)` and `(OnEpisodeFileDelete && OnEpisodeFileDeleteForUpgrade)` guards are redundant because the parent terms already imply them. **Drop in Go port.**
- **NOTIFY-Q7** Plex OAuth dead defensive code → resolved-in-source. `RequestAction("startOAuth")` filters by ConsumerKey/ConsumerSecret which Plex doesn't expose. Vestigial copy-paste. **Drop in Go port.**
- **HEALTH-Q2** RemotePathMappingCheck.Check(IEvent) overload → resolved-in-source. The engine calls only `IProvideHealthCheck.Check()`. The overload is dead code. **Drop in Go port.**
- **HOUSE-Q3** DeleteBadMediaCovers → resolved-in-source. 2014-vintage one-shot that self-disables. **Drop entirely in Go port.**
- **MSG-Q5** MediaFileDeletionService Handle ordered [Last] → resolved-in-source. `src/NzbDrone.Core/MediaFiles/MediaFileDeletionService.cs:Handle(EpisodeFileDeletedEvent)`. The `[Last]` ordering ensures history/notifications observe the event with the file row still queryable. **Document and preserve in Go port.**
- **MSG-Q12** Five events with zero production consumers → resolved-in-source by `analysis/subsystems/messaging.md`. `EpisodeImportFailedEvent`, `SeriesMovedEvent`, `SeriesRefreshCompleteEvent`, `SceneMappingsUpdatedEvent`, `ProviderStatusChangedEvent<T>` are all consumed only via `IHandleAsync<IEvent>` global tap (HealthCheck.HandleAsync) and only flow into health checks via `[CheckOn]` attributes. **Decision: keep all five for parity (third-party consumers may rely on them via plugin/notification webhooks).**
- **JOBS-Q1..Q4** TaskManager hardcoded intervals / Scheduler 30s tick / static state → resolved as **port-time decisions** below; nothing blocks.
- **CFG-Q5** ResetApiKeyCommand doesn't raise ConfigFileSavedEvent → resolved-in-source. Command writes via `IConfigService.SetValue` which raises `ConfigSavedEvent`, not `ConfigFileSavedEvent`. **Decision: Go port consolidates on a single `ConfigChangedEvent` that includes a "source" tag (db|file).**
- **TAGS-Q4** MetadataDefinition.Tags dead code → resolved-in-source. Column exists but no consumer reads it; cosmetic. **Drop in Go port.**
- **PROG-Q2 / PROG-Q3** Nested commands & ProgressMessage extension → resolved-in-source by reading `src/NzbDrone.Core/ProgressMessaging/`. Extension is `_logger.ProgressMessage(format, args)` which sets the `Status` log property; nested commands do exist (e.g. `ImportListSyncCommand` → `RefreshSeriesCommand`) and the null-check is correct. **Document; reproduce in Go.**
- **CF-Q5** Empty-Specifications custom format match → resolved-in-source. `IsSatisfiedBy` over empty groups returns `true` vacuously, so a CF with no specs matches everything. `CustomFormatCalculationService.ParseCustomFormat` does not filter these. **Behaviour: preserve as-is; document.**
- **AUTH-Q9** CleanupAdditionalUsers LIMIT 1 without ORDER BY → resolved-in-source. SQLite picks lowest rowid, Postgres is undefined. **Decision: Go port adds `ORDER BY Id` deterministically.**
- **DECISION-Q4** SameEpisodesSpecification not implementing IDownloadDecisionEngineSpecification → resolved-in-source. It's a helper, not a spec; the Go port keeps it separate.
- **PROP-Q2** Runtime version source in Go port → resolved-in-source/spike. `debug.ReadBuildInfo` + `-ldflags '-X buildinfo.Version=$VERSION'`. Already wired in `go/internal/buildinfo/`.
- **MC-Q1** ImageSharp → Go (covered by `spikes/imaging-imagesharp.md`). Tracked in spike (likely green).
- **CFLT-Q2** CustomFilters Type values → resolved-in-source. Enum is internal; document, no port impact.
- **DS-Q5** Windows quota-aware vs Linux → resolved-in-source. The C# `IDiskProvider.GetAvailableSpace` already uses platform calls; Go's `syscall.Statfs` mirrors. **Document; preserve.**
- **NP-Q2** AppFolderInfo `XDG_CONFIG_HOME` → resolved-in-source. `os.UserConfigDir()` honours XDG natively. Drop the manual probe.
- **REGEX-Q3** Atomic group at Parser.cs:398 → resolved-in-source/regex-corpus. Confirmed no-op optimization. **Drop the atomic-ness in Go port.**
- **REGEX-Q6** SourceRegex IgnorePatternWhitespace → resolved-in-source. Pre-strip whitespace at compile time. Trivial.
- **REGEX-Q7** SourceRegex `(?-i:WEB)` → resolved-in-source. Go's `regexp` supports `(?-i:...)`. Verified.
- **REGEX-Q8** LanguageRegex interpolation → resolved-in-source. Mirror with `fmt.Sprintf` at package init.
- **REGEX-Q1** Off-by-one count drift in card → resolved-in-source/regex-corpus. Documentation only; no port impact.

### resolved-in-spike

- **Q-09 (storage layer)** modernc/sqlite + jackc/pgx coverage → resolved by `analysis/spikes/sqlite-modernc.md` (green) and `analysis/spikes/pgx-postgres.md` (green). WAL/FK/busy_timeout/NOCASE/JSON1/recursive CTE/concurrent RW all covered.

---

## Phase 1 blockers

These must be answered before any production Go is written. The porter starts every Phase 1 PR by re-reading this section.

### Q-01 — Logging library choice (NLog parity)
**Card:** `analysis/subsystems/common.md` (Q-01) and `analysis/subsystems/http-instrumentation-misc.md` (INSTR-Q5).
**Stakes:** NLog backs file-rolling, console, syslog, Sentry, CLEF, secret-cleansing layouts, per-logger filters, and the `ProgressMessageTarget` runtime-mutating rule. Choice constrains how `CleanseLogMessage` and the Sentry bridge plug in.
**Direction:** `log/slog` (stdlib, simpler, structured-by-default) is the strongly recommended baseline; pair it with `lmittmann/tint` for human-readable console output and a custom `slog.Handler` for the SQLite/Postgres log-DB sink. `rs/zerolog` and `uber/zap` are faster but do not buy us anything for Sonarr's throughput. **Required before Phase 1 starts.**

### Q-09-residual — Embedded-document type-handler shape
**Card:** `analysis/subsystems/datastore.md` (Q-09).
**Stakes:** Dapper has per-type `SqlMapper.TypeHandler<T>` registration; Go's `database/sql` does not. Every embedded-doc-bearing column needs a Scanner/Valuer or a generic JSON wrapper.
**Direction:** Recommend a generic `JSONColumn[T]` wrapper that implements `Scanner`/`Valuer` and serialises via the single STJ-equivalent `encoding/json` config (camelCase + custom UTC/TimeSpan converters). Reflection-driven registration at startup is rejected — Go is type-static. Per-type Scanner/Valuer is overkill given STJ-equivalent settings.

### Q-12 — MigrationContext per-process state
**Card:** `analysis/subsystems/datastore.md` (Q-12).
**Stakes:** Test parallelism. C# tests serialise via AppDomains.
**Direction:** Already settled — Go port plumbs `MigrationContext` via `context.Context`, not a package var. **Listed here so it doesn't get forgotten in Phase 1 datastore implementation.**

### TP-Q1, TP-Q2 — Stable provider-string registry
**Card:** `analysis/subsystems/thingiprovider.md` (TP-Q1, TP-Q2).
**Stakes:** Existing user DBs have `Implementation = "Newznab"` etc.; renaming Go types would silently delete every user's provider rows via `RemoveMissingImplementations`. Affects every provider port (Phase 1 + Phase 4).
**Direction:** Explicit registry: every provider declares `func ProviderID() string` returning the C# `Type.Name` verbatim; same for `ConfigContractID()`. Two cases need `(impl_id, contract_id)` pairs (TransmissionSettings shared by Vuze+Transmission; DownloadStationSettings shared by Usenet+Torrent). Codified once at registry init. **Blocks every provider PR.**

### ANNO-Q1 — Go idiom for `[FieldDefinition]`
**Card:** `analysis/subsystems/http-instrumentation-misc.md` (ANNO-Q1) and `analysis/subsystems/thingiprovider.md` (TP-Q4).
**Stakes:** Every provider settings struct uses `[FieldDefinition(Order, Label, HelpText, Type, ...)]`. The schema is consumed by REST `/schema` endpoints, the React UI, and the validation layer. ~700+ field annotations across all providers.
**Direction:** Struct tags `thingi:"order=2,label=Host,type=textbox"` plus a derived schema function. Codegen is rejected (too much ceremony for the maintenance gain). **Blocks every provider settings struct port.**

### ANNO-Q2 — `********` sentinel-value contract with frontend
**Card:** `analysis/subsystems/http-instrumentation-misc.md` (ANNO-Q2).
**Stakes:** Password/secret fields are returned as the literal string `********` to the frontend; the frontend re-submits `********` on save unchanged, signalling "no change". The Go port must replicate the exact byte sequence and the round-trip semantics.

### ANNO-Q4 — `SelectOptions` polymorphism
**Card:** `analysis/subsystems/http-instrumentation-misc.md` (ANNO-Q4).
**Stakes:** Schema rendering converts enum values to dropdown options via `ISelectOptionsConverter`. Several converters exist (`OriginalLanguageFieldConverter`, `LanguageFieldConverter`, etc.).
**Direction:** Concrete Go interface `SelectOptionsProvider` implemented per converter; registered alongside the field tag.

### AUTH-Q1, AUTH-Q2 — Password-hash upgrade & XML seed cleanup
**Card:** `analysis/subsystems/authentication-security.md`.
**Stakes:** Legacy SHA-256 → PBKDF2 lazy upgrade on login (`UserService.cs:97-108`); `<Username>`/`<Password>` XML seed remains after first run.
**Direction:** Lazy upgrade on login (matches C# behaviour, fewest surprises); delete the XML elements after seed.

### AUTH-Q4, AUTH-Q6, AUTH-Q7 — External auth, cookie-name derivation, anonymous identity
**Card:** `analysis/subsystems/authentication-security.md`.
**Stakes:** `AuthenticationType.External` trusts upstream proxy completely; cookie name uses .NET diacritic-stripping; `NoAuthenticationHandler` issues a "logged-in anonymous" identity. Each touches a security boundary.
**Direction:** External auth requires `X-Forwarded-User`; cookie-name uses `golang.org/x/text/unicode/norm` NFKD + non-alphanumeric strip (test against existing deployments); anonymous identity is a value type with `IsAuthenticated=true, Anonymous=true`.

### AUTH-Q8 — Static `_apiKey` cache in InitializeJsonController
**Card:** `analysis/subsystems/authentication-security.md` (AUTH-Q8); cross-cite `analysis/subsystems/sonarr-http.md` (SH-Q8).
**Stakes:** Static field cached at construction; survives `ResetApiKeyCommand` only in dev. Inconsistent with the rest of config-cache behaviour (`AUTH_METHOD`, `VersionMiddleware._version`).
**Direction:** Eliminate static caches; live-read with a `sync.Map`-backed config provider that subscribes to `ConfigSavedEvent`.

### SEC-Q2 — Constant-time comparison for API key & password
**Card:** `analysis/subsystems/authentication-security.md`.
**Direction:** `crypto/subtle.ConstantTimeCompare` is mandatory.

### SEC-Q5 — API key entropy
**Card:** `analysis/subsystems/authentication-security.md`.
**Direction:** 32 bytes from `crypto/rand.Read`, hex-encoded — not `Guid.NewGuid()` (122 bits).

### SEC-Q6 — Cookie attributes
**Card:** `analysis/subsystems/authentication-security.md`.
**Direction:** `HttpOnly=true, Secure=auto-from-SSL-config, SameSite=Lax`. Default deny `Secure=Always` when `EnableSsl=false` would lock out plaintext deployments — keep explicit.

### SEC-Q12 — TLS version & cipher allowlist
**Card:** `analysis/subsystems/authentication-security.md`.
**Direction:** `tls.VersionTLS12` minimum; explicit cipher list (drop CBC, prefer AEAD).

### SEC-Q14 — Synchronous `Dns.GetHostEntry` in cert callback
**Card:** `analysis/subsystems/authentication-security.md`.
**Direction:** Resolve once at config save time; cache result; return false on resolve failure.

### SEC-Q18 — Auth/security has zero unit-test coverage
**Card:** `analysis/subsystems/authentication-security.md`.
**Stakes:** Phase 1 sign-off bar.
**Direction:** Write parity tests for: PBKDF2 hash format round-trip; SHA-256 → PBKDF2 lazy upgrade; cookie-name derivation; API-key constant-time compare; X-Forwarded-For trust-list. **Blocks Phase 1 sign-off, not start.**

### CFG-Q1 — `TrustCgnatIpAddresses` duplicated across IConfigService + IConfigFileProvider
**Card:** `analysis/subsystems/configuration.md` (CFG-Q1); cross-cite SEC-Q9.
**Stakes:** Config row in DB and `<TrustCgnatIpAddresses>` element in XML — which wins?
**Direction:** **XML wins on read** (matches C# `IConfigFileProvider` precedence per `src/NzbDrone.Common/Configuration/ConfigFileProvider.cs`). Document explicitly and remove the DB-side duplication on first migration.

### CFG-Q2, CFG-Q3, CFG-Q4, CFG-Q6, CFG-Q7, CFG-Q9, CFG-Q10
**Card:** `analysis/subsystems/configuration.md`.
**Stakes:** PostgresOptions env-vs-XML; ApiKey silently dropped on PUT; ConfigRepository case-handling; utility-mode binding parity; `persist:false` handling; `SaveConfigDictionary` non-transactional; static cache refresh policy.
**Direction:** Bundle as one Phase 1 PR — implement a single `internal/config` package that hides the precedence rules. CFG-Q9 (transactional save) is an actual bug fix.

### CFG-Q5 — ResetApiKeyCommand event-type
See resolved-in-source above. Listed here only to remind: consolidate to single `ConfigChangedEvent` in Go.

### TAGS-Q2, TAGS-Q3 — Tag uniqueness & GetByLabel API shape
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** Add UNIQUE constraint on `Tags.Label` (lowercase normalised) at the schema level; `GetByLabel` returns `(*Tag, bool)` (idiomatic Go), `FindByLabel` stays nil-on-miss.

### RF-Q1, RF-Q6 — RootFolder validation order & concurrent Add race
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** Service-layer validation; mutex-guarded Add (single-writer per repository).

### RPM-Q1 — RemotePathMapping first-match vs longest-prefix
**Card:** `analysis/subsystems/configuration.md`.
**Stakes:** Affects every download-client integration. C# does first-match (registration order).
**Direction:** **Preserve first-match.** Switching to longest-prefix would silently change which mapping wins for users with overlapping prefixes; require explicit migration.

### DS-Q1 — Linux `statvfs` vs `statfs` reserved-block handling
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** Use `golang.org/x/sys/unix.Statfs_t` (i.e. `statfs`); subtract reserved blocks for ext4 root-only blocks where applicable. Match C#'s `Mono.Unix.Native.Syscall.statvfs` semantics.

### LANG-Q1, LANG-Q2 — Language singletons & 54-id wire contract
**Card:** `analysis/subsystems/localization-validation.md`.
**Stakes:** 54 hand-numbered language IDs are persisted in `QualityProfile.Languages`, `Series.OriginalLanguage`, `EpisodeFile.Languages`, plus indexer/import-list/notification settings. No migration locks them. The Language.cs file is the de-facto source of truth.
**Direction:** Convert to package-level singletons in Go (LANG-Q1) and emit a frozen `analysis/schema/language-ids.md` that captures the id↔name mapping with a SHA hash of the file. Any future change to that file is a wire-breaking migration.

### LOC-Q1, LOC-Q2 — Localization cache invalidation & embed.FS
**Card:** `analysis/subsystems/localization-validation.md`.
**Direction:** Cache invalidated by `ConfigSavedEvent`; subscribe explicitly. Use `embed.FS` for the JSON files (simpler deploy, no editability loss in practice — users edit via UI, not file).

### VAL-Q3, VAL-Q4 — FluentValidation private API & validation-error JSON shape
**Card:** `analysis/subsystems/localization-validation.md`.
**Stakes:** `ResourceValidator<T>.RuleForField` reaches into `FluentValidation.Internal.PropertyRule` (private). `SonarrErrorPipeline` writes the raw `ValidationException.Errors` (FluentValidation's internal collection) — wire shape leaks `Severity`, `ErrorCode`, `FormattedMessagePlaceholderValues`.
**Direction:** Hand-roll a Go validation package that emits the byte-equivalent JSON envelope. Capture goldens before Phase 1 (recorder targets exist).

### MEDIAFILES-Q4, MEDIAFILES-Q5, MEDIAFILES-Q8, MEDIAFILES-Q11 — DiskTransfer, atomic-rename guarantees, case-sensitivity
**Card:** `analysis/subsystems/mediafiles.md`.
**Direction:** Document promise: hardlink-first → copy + fsync + rename. `PathEqualityComparer` becomes a `runtime.GOOS`-aware helper (`strings.EqualFold` on Windows, byte-equal elsewhere). `RemoveFileExtension` regex ports verbatim with documentation of the .tar.gz miss.

### EXTRAS-Q2, EXTRAS-Q4, EXTRAS-Q9 — Per-episode NFO de-dup, ProcessEpisodeImages early-return, Handler ordering
**Card:** `analysis/subsystems/extras.md`.
**Direction:** EXTRAS-Q2 fix to per-EpisodeFile dedup; EXTRAS-Q4 preserve C# behaviour and document; EXTRAS-Q9 force handler ordering via explicit register-then-add at startup (Go port has no DryIoc registration order).

### EXTRAS-Q5 / PROV-Q40 — KodiEpisodeGuide TvRage from TvMazeId
**Card:** `analysis/subsystems/extras.md` and `analysis/providers/metadata-writers.md`.
**Stakes:** User-visible NFO output. **Logged below as upstream bug too.**
**Direction:** Replicate verbatim. Fixing it would force every Kodi user to re-rewrite their library.

### MONO-Q1, MONO-Q2, MONO-Q6 — POSIX permission masks, SetFilePermissions vs SetPermissions, symlink resolver
**Card:** `analysis/subsystems/native-platform.md`.
**Direction:** Document the 3-vs-4 digit mask quirk; preserve. SetFilePermissions vs SetPermissions: confirm via small Go test against C# behaviour, then preserve. Symlink resolver: spike with `filepath.EvalSymlinks` first; fall back to a manual walker if `..` flattening differs.

### MONO-Q8 — IOsVersionAdapter discovery order
**Card:** `analysis/subsystems/native-platform.md`.
**Direction:** Add an explicit priority field and registration list (no assembly-scan in Go).

### NP-Q1 — `OsInfo.IsContainerized` cgroup v2
**Card:** `analysis/subsystems/native-platform.md`.
**Direction:** Update probes for cgroup v2 format (`/proc/1/cgroup` is now `0::/...`); preserve all 5 probes.

### WIN-Q1, WIN-Q3, WIN-Q4 — Windows long-path, SetEveryonePermissions, InheritFolderPermissions
**Card:** `analysis/subsystems/native-platform.md`.
**Direction:** Use `\\?\` prefix for *all* Windows path operations (Go's `os` package does not auto-inject); test SetEveryonePermissions and InheritFolderPermissions against a real NTFS volume before declaring parity.

### HOST-Q1, HOST-Q2, HOST-Q9, HOST-Q10, HOST-Q11, HOST-Q12 — Host bootstrap details
**Card:** `analysis/subsystems/host.md`.
**Direction:** Single `StartupContext` value; service-mode kill-others vs prevent-start logic resolution; explicit `Bootstrap` registration list (no DryIoc); 30s graceful shutdown; immutable `StartupContext` plus a separate `BrowserSuppressed` flag; in-app data-protection key (using `golang.org/x/crypto/nacl/secretbox` or similar — no .NET DataProtection analogue).

### SH-Q1, SH-Q2, SH-Q3, SH-Q4, SH-Q5, SH-Q6, SH-Q7, SH-Q8, SH-Q11, SH-Q12, SH-Q13, SH-Q14
**Card:** `analysis/subsystems/sonarr-http.md`.
**Direction:** Each has a clear port-time fix. Bundle into Phase 1 HTTP middleware PRs. SH-Q14 is a security hardening (path-traversal); SH-Q13 may be a real bug — verify with a client trace before deciding.

### EDOC-Q5, EDOC-Q7, EDOC-Q9, EDOC-Q10, EDOC-Q15, EDOC-Q16, EDOC-Q17, EDOC-Q19, EDOC-Q20, EDOC-Q21, EDOC-Q23
**Card:** `analysis/embedded-docs/catalogue.md`.
**Stakes:** Every embedded-doc-bearing struct in Go.
**Direction:** Bundle into a Phase 1 "embedded-doc serializer" PR that ships:
- a single `internal/jsonconv` package mimicking the STJ options;
- a `TimeSpan` marshaller emitting `[d.]hh:mm:ss[.fffffff]`;
- `LanguageIntConverter` accepting negative ids;
- struct tags matching exact STJ camelCase output (`videoCodecID` literal, NOT `videoCodecId`);
- read-side acceptance of pre-225 flat MediaInfoModel rows;
- read-side acceptance of integer enum values (allowIntegerValues=true);
- discriminator handling for `{type, body}` envelopes including the `SeasonPackSpecification` → `ReleaseTypeSpecification` rename and the `StatusSpecification.status` field-name vs other autotag specs' `value`;
- type-string registry (cross-references TP-Q1) covering CustomFormats specs, AutoTagging specs, and Commands.

### REGEX-Q2, REGEX-Q4, REGEX-Q5
**Card:** `analysis/parser/regex-corpus.md`.
**Direction:** REGEX-Q2: helper functions (`validateNoDigitAfter`, `validateNoSubsAfter`) over inline rewrites. REGEX-Q4: enumerate the 3 separator branches into alternation. REGEX-Q5: 2-pass parse (capture candidate, then negation check).

### Q-09 (Datastore-Q3 in card numbering) — embedded-doc reflection scan
See Q-09-residual above.

### CFG-Q4, CFG-Q10, RF-Q1, RPM-Q1, DS-Q1 — Foundation services
Already listed above.

### CFLT-Q1 — CustomFilters JSON column type
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** Keep as `text` — CustomFilters is small and Postgres `jsonb` doesn't buy us anything for the access pattern.

### EXC-Q1, EXC-Q2, EXC-Q3 — Go error-typing strategy
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** `errors.As` + a `StatusCoded` interface; map `UnauthorizedAccessException` to a Go `os.PermissionError`-equivalent.

### HTTP-Q3, HTTP-Q4 — Bypass-list parser & interceptor chain
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** Spike `*.host` glob + CIDR parser; build interceptor chain on `http.RoundTripper` middleware.

### INSTR-Q1, INSTR-Q2, INSTR-Q4, INSTR-Q5
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** INSTR-Q1: batched commits with a 1-second flush window and an in-memory buffer (matches NLog's BufferingTargetWrapper). INSTR-Q2: surface as config field. INSTR-Q4: emit UTC consistently in both DBs. INSTR-Q5: see Q-01.

### SS-Q1, SS-Q3 — Series-stats Postgres parity, pipe-encoded string columns
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** SS-Q1 already addressed by a recent commit (see git log: `Produce distinct values in aggregated series stats from Postgres`). SS-Q3: use `pq.StringArray` on Postgres, pipe-encoded on SQLite. Add a thin abstraction.

### IS-Q1 — SeriesSearchService missingOnly=!UpgradeAllowed
**Card:** `analysis/subsystems/indexersearch.md`.
**Direction:** Confirm the user-intent question with the maintainers; the C# behaviour is to fetch *all* releases when UpgradeAllowed is true. **Preserve.**

### MC-Q4 — Media-cover lazy-resize vs eager-resize
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** Lazy-resize on first request, then cache for 24h (matches C#).

### Q-Q1, Q-Q4 — Quality.Id 11 reserved & QualityDefinitionService deletes unrecognised
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Q-Q1: keep id 11 reserved (renumbering breaks user DBs). Q-Q4: preserve delete-on-startup; add a healthcheck row warning the user.

### CF-Q1 — CustomFormat spec Order numbering gaps
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Renumber to 1..N at port time; the order is UI-only.

### CF-Q4 — Regex always IgnoreCase
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Preserve; surface a `(?-i:...)` opt-out via doc.

### ORG-Q3, ORG-Q4, ORG-Q7 — MediaInfo on-demand re-read, byte-truncation, sync DB calls
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Preserve C# behaviour for ORG-Q3 (sync MediaInfo update). ORG-Q4 byte truncation: handle by `utf8.DecodeLastRune` — never cut mid-codepoint. ORG-Q7: precompute upstream where possible; fall back to sync.

---

## Phase 2 blockers

### MSG-Q1, MSG-Q2, MSG-Q3, MSG-Q4, MSG-Q6, MSG-Q7, MSG-Q8, MSG-Q9, MSG-Q10, MSG-Q11
**Card:** `analysis/subsystems/messaging.md`.
**Direction:** Build a single in-process event bus: `map[reflect.Type][]handler` with `sync.RWMutex` and a worker pool for the async tier. MSG-Q1: Go port returns `error` (logged but not propagated). MSG-Q4: support per-handler ordering for both sync and async (the C# limitation is invisible in practice but trivial to lift). MSG-Q9: bound async concurrency at `runtime.NumCPU()*2`. MSG-Q10: depth/recursion guard via context value (cheap).

### PROG-Q1, PROG-Q4, PROG-Q5, PROG-Q6 — Progress messaging
**Card:** `analysis/subsystems/messaging.md`.
**Direction:** Use `context.Context` for ProgressMessage carry; persist last-status to DB on long-running commands; add equivalence tests in Go (status line → CommandUpdatedEvent → SignalR `command` resource).

### DECISION-Q1..Q10 — Decision-engine semantics
**Card:** `analysis/subsystems/decisionengine.md`.
**Direction:** DECISION-Q1: collapse Default/Parsing/Database to a single priority (the enum's distinction is illusory). DECISION-Q2: preserve early-out at first rejecting priority (cost optimisation that's de facto contract). DECISION-Q3: 15s TTL → port-time configurable. DECISION-Q5: document modes orthogonality. DECISION-Q6: add test fixture covering anime/non-anime episode-count comparison. DECISION-Q7: switch all date logic to UTC. DECISION-Q8: cache OldestPendingRelease per RSS-batch. DECISION-Q9: preserve. DECISION-Q10: preserve typo for serialization stability.

### INDEXERS-Q1..Q9 — Indexer subsystem
**Card:** `analysis/subsystems/indexers.md`.
**Direction:** INDEXERS-Q2: fall through to next tier on exception (improvement). INDEXERS-Q3: subscribe broader change feed. INDEXERS-Q4: keep MaxPages=30 hard-coded. INDEXERS-Q7: build regression corpus from real RSS feeds. INDEXERS-Q8: switch to host-based rate-limit key.

### DOWNLOAD-Q1..Q4, Q6, Q8, Q9 — Download subsystem
**Card:** `analysis/subsystems/download.md`.
**Direction:** DOWNLOAD-Q1: `sync.Map`-backed pending-releases cache; rebuild on startup from DB. DOWNLOAD-Q2: surface queue warning on hash mismatch. DOWNLOAD-Q3: preserve short-circuit. DOWNLOAD-Q4: collapse the duplicate filter. DOWNLOAD-Q6: keep CheckForFinishedDownloadCommand alias for one major version. DOWNLOAD-Q8: cache-eviction tests for nzo_id reuse. DOWNLOAD-Q9: re-test after v5 episode-id regeneration.

### MEDIAFILES-Q3, Q6, Q7, Q9, Q10, Q12, Q13 — Media files
**Card:** `analysis/subsystems/mediafiles.md`.
**Direction:** MEDIAFILES-Q3: normalise raw FFprobe output to canonical form on read. MEDIAFILES-Q6: list ~50 `Sonarr_*` env vars in a separate sub-card before porting. MEDIAFILES-Q7: confirm `jsonb` vs `json` per the embedded-doc catalogue. MEDIAFILES-Q9: refactor to make the deferred-import flow explicit. MEDIAFILES-Q10: schema-versioned struct + replay command. MEDIAFILES-Q12: add config toggle for nested `.sh`. MEDIAFILES-Q13: unify across OS (no-op asymmetry was a mono workaround).

### IL-Q1..Q12, MD-Q1..Q8, DA-Q1..Q8 — Import lists, metadata source, data augmentation
**Cards:** `analysis/subsystems/importlists-metadata.md`, `analysis/providers/import-lists.md`, `analysis/providers/metadata-sources.md`.
**Direction:** Bundle as Phase 4 (provider port). The Phase 2 blockers in this set are: IL-Q2 (concurrency policy for list sync), IL-Q3 (exclusion-list keying — preserve post-translation gate), IL-Q4 (KeepAndTag reset semantics — document; manual untag), IL-Q8 (library-clean gating — port verbatim), IL-Q11 (drop unused IParsingService dep), DA-Q3 (sanity floor on scene-mapping deletes — add), DA-Q4 (re-entrancy in XemService — refactor with explicit transaction).

### TP-Q3, TP-Q5, TP-Q6, TP-Q7, TP-Q9, TP-Q10, TP-Q11, TP-Q13, TP-Q14, TP-Q15
**Card:** `analysis/subsystems/thingiprovider.md`.
**Direction:** TP-Q3: HealthCheck row on missing impl (better than silent NullConfig). TP-Q5: provider-as-value with `Hydrate(def)` constructor. TP-Q6: `context.Context` everywhere; 30s default Test() deadline. TP-Q7: homogenise column names with one schema migration (188 already touched these). TP-Q9: HealthCheck instead of silent-delete on missing impl. TP-Q10: re-canonicalise on save (compact); keep WriteIndented opt-in for diffability. TP-Q11: registry of declared actions; schema renderer errors out on missing action. TP-Q13: preserve back-off skipping; add property test. TP-Q14: pin to invariant. TP-Q15: keep specs separate from ThingiProvider (different DB shapes).

### AT-Q1..Q6 — AutoTagging
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** AT-Q1: preserve no-back-fill (matches C#). AT-Q2: null-safe specs in Go (Network/OriginalCountry). AT-Q3: trigger re-eval on AutoTagsUpdatedEvent (improvement). AT-Q4: define multi-RootFolderSpec semantics as AND. AT-Q5: tighten OriginalCountrySpecification validation. AT-Q6: type-name discriminator stable via TP-Q1 registry.

### TAGS-Q5, TAGS-Q6 — Cleanup unused tags / label normalization
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** TAGS-Q5: extend CleanupUnusedTags to ExcludedTags references. TAGS-Q6: normalise label before validation.

### RF-Q2, RF-Q3, RF-Q4, RF-Q5 — RootFolder edge cases
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** RF-Q2: case-fold path comparison on Darwin (APFS). RF-Q3: invalidate UnmappedFolders cache on naming-config change. RF-Q4: document fallback. RF-Q5: refuse symlinks that escape registered roots (filepath.EvalSymlinks + prefix check).

### RPM-Q3, RPM-Q4 — Path-mapping runtime checks
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** RPM-Q3: cross-link to health check. RPM-Q4: path-separator normalisation per OS.

### DS-Q2, DS-Q3, DS-Q4 — Disk-space subsystem
**Card:** `analysis/subsystems/configuration.md`.
**Direction:** DS-Q2: consolidate two-layer mount filter. DS-Q3: user-configurable exclude list. DS-Q4: per-disk timeout via goroutine + context.Done.

### CF-Q2, CF-Q3 — CustomFormat token-filter & ExceptLanguage
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Token-filter syntax mirrors C#; ExceptLanguage + Negate has 4 logical combos — full unit coverage required before port.

### CF-Q1, P-Q1..P-Q6, ORG-Q1, ORG-Q2, ORG-Q5, ORG-Q6 — Organizer / Profile
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Ports cleanly. ORG-Q6: instance-scoped FileNameSampleService.

### EXTRAS-Q3, EXTRAS-Q6, EXTRAS-Q7, EXTRAS-Q8, EXTRAS-Q10
**Card:** `analysis/subsystems/extras.md`.
**Direction:** Document each. EXTRAS-Q6 + EXTRAS-Q10 cross-reference migration ledger work.

### MONO-Q4, MONO-Q5, MONO-Q7, MONO-Q10, MONO-Q11; WIN-Q2, WIN-Q5, WIN-Q6 — Native platform
**Card:** `analysis/subsystems/native-platform.md`.
**Direction:** Spike or document each.

### NP-Q3 — ProcMountProvider cache
**Card:** `analysis/subsystems/native-platform.md`.
**Direction:** `sync.Once` parse of `/proc/filesystems`.

### IS-Q2, IS-Q3, IS-Q4, IS-Q5, IS-Q6, IS-Q7, IS-Q8, IS-Q9, IS-Q10, IS-Q11, IS-Q12 — IndexerSearch
**Card:** `analysis/subsystems/indexersearch.md`.
**Direction:** IS-Q2: bounded semaphore. IS-Q3: surface health check. IS-Q4: drop dead method. IS-Q5: page genuinely (chunked). IS-Q6: optional `SearchPerformed` history row (Phase 2 design choice). IS-Q7: skip search when no abs number. IS-Q8: extend queue subtraction to manual searches. IS-Q9: tighten MonitoredEpisodesOnly semantics. IS-Q10: dedupe globally. IS-Q11: distinguish attempted vs successful with two timestamps. IS-Q12: align asymmetry (port verbatim and document).

### PROV-Q5, PROV-Q11, PROV-Q12, PROV-Q13, PROV-Q14, PROV-Q15, PROV-Q19, PROV-Q20, PROV-Q21
**Card:** `analysis/providers/*.md`.
**Direction:** PROV-Q5: per-provider const. PROV-Q11: v2 only + healthcheck for v1. PROV-Q12: add tests. PROV-Q13: typed error. PROV-Q14: unify. PROV-Q15: preserve no-redirect. PROV-Q19: per-definition mutex. PROV-Q20: evict on `ProviderUpdatedEvent`. PROV-Q21: promote SMTP timeout to settings field.

### NOTIFY-Q4, Q5, Q6, Q8, Q9, Q10, Q11, Q12 — Notifications
**Card:** `analysis/subsystems/notifications.md`.
**Direction:** Bundle into Phase 4 (provider port). NOTIFY-Q5: pick canonical link scheme. NOTIFY-Q8: narrow ProcessQueue to overriders. NOTIFY-Q9: add per-definition mutex. NOTIFY-Q10: guard with `series?.TvdbId > 0` everywhere.

### NOTIFY-Q2 — WebhookEventType camelCase migration
**Card:** `analysis/subsystems/notifications.md`.
**Direction:** Preserve PascalCase for backward-compatibility. Note in CONVERSION.md.

### Q-Q2, Q-Q3, Q-Q5 — Quality
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Q-Q2: surface "no quality matched" as a typed warning. Q-Q3: document Max=1000 MB/min as UI cap, not hard. Q-Q5: drop IsRepack (consult only Version).

### P-Q4 — ReleaseProfile.AirDateRestriction usage
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Grep for usages before port; if dead, drop.

### P-Q5 — DelayProfileService.Reorder unknown-id behaviour
**Card:** `analysis/subsystems/quality-profiles-formats-organizer.md`.
**Direction:** Throw `ErrUnknownProfile` (resolves the C# TODO).

### REGEX-Q3 — Atomic group at Parser.cs:398
See resolved-in-source above.

---

## Phase 3 blockers

### HIST-Q1, HIST-Q2, HIST-Q3 — History
**Card:** `analysis/subsystems/system.md`.
**Direction:** HIST-Q1: keep loose schema (JSON column) for future-proofing. HIST-Q2: preserve LIKE-pattern filter; document fragility. HIST-Q3: add a configurable retention policy + housekeeper (improvement).

### BLOCKLIST-Q1, BLOCKLIST-Q2, BLOCKLIST-Q3 — Blocklisting
**Card:** `analysis/subsystems/system.md`.
**Direction:** BLOCKLIST-Q1: preserve substring match (changing it would silently un-block historical entries). BLOCKLIST-Q2: add config-driven max-age. BLOCKLIST-Q3: write IndexerFlags/ReleaseType uniformly.

### LIFE-Q1, LIFE-Q2 — Lifecycle ordering
**Card:** `analysis/subsystems/system.md`.
**Direction:** Explicit registration order; richer command result for shutdown vs restart.

### HEALTH-Q1, HEALTH-Q3, HEALTH-Q4, HEALTH-Q5 — Health checks
**Card:** `analysis/subsystems/system.md`.
**Direction:** HEALTH-Q1: keep enum + string keys (registry). HEALTH-Q3: drop-the-duplicate semantics is correct. HEALTH-Q4: keep 15-min grace period as constant; could be configurable later. HEALTH-Q5: lifecycle events still flow.

### HOUSE-Q1, HOUSE-Q2, HOUSE-Q4, HOUSE-Q5, HOUSE-Q6 — Housekeeping
**Card:** `analysis/subsystems/system.md`.
**Direction:** HOUSE-Q1: per-task wrapper (preserve compound deletes). HOUSE-Q2: centralise SQLite/PG branching. HOUSE-Q4: emit `HousekeepingFailedEvent`. HOUSE-Q5: configurable cadence. HOUSE-Q6: gate VACUUM on rows-deleted.

### BACKUP-Q1, BACKUP-Q2, BACKUP-Q3, BACKUP-Q4 — Backup
**Card:** `analysis/subsystems/system.md`.
**Direction:** BACKUP-Q1: shell out to `pg_dump` if available; otherwise document as no-op. BACKUP-Q2: apply retention to manual too with a warning. BACKUP-Q3: add version-compatibility check. BACKUP-Q4: modernc has no analogue — different journal mode handling needed.

### UPDATE-Q1..Q6 — Update flow
**Card:** `analysis/subsystems/system.md`.
**Direction:** UPDATE-Q1: add Sigstore (port-time security improvement). UPDATE-Q2: stable across upgrades. UPDATE-Q3: surface as healthcheck. UPDATE-Q4: keep Version.Major==10 dev marker. UPDATE-Q5: preserve LogDbEnabled gate; document. UPDATE-Q6: configurable.

### UPD-Q1..UPD-Q15 — Updater process
**Card:** `analysis/subsystems/updater.md`.
**Direction:** UPD-Q1: define non-zero exit codes. UPD-Q2: args-based on all OSes. UPD-Q3: drop dead code. UPD-Q4: restore appdata too (fix half-finished). UPD-Q5: error+log on resurfaced process. UPD-Q6: configurable. UPD-Q7: cross-cite security card; add Sigstore. UPD-Q8: drop unused field. UPD-Q9: rename "Winform" → "Process". UPD-Q10: macOS .app spike (recommended new spike). UPD-Q11: log-rotation (configurable max files). UPD-Q12: drop app.config. UPD-Q13: flat func main(). UPD-Q14: preserve argv contract verbatim. UPD-Q15: regression test.

### QUEUE-Q1..Q4 — Queue subsystem
**Card:** `analysis/subsystems/system.md`.
**Direction:** QUEUE-Q1: lift v5 only; v3 via contract translation. QUEUE-Q2: `atomic.Pointer[[]Queue]` swap. QUEUE-Q3: keep "go look at it" signal (matches C#). QUEUE-Q4: switch to string id.

### REST-Q4 — V3/V5 dual-emit policy
See resolved-in-source above (port-time decision).

### SH-Q9, SH-Q10 — initialize.json apiRoot, ValidationError JSON shape
**Card:** `analysis/subsystems/sonarr-http.md`.
**Direction:** SH-Q9: keep `/api/v3` until SPA migrates (Phase 4). SH-Q10: bare-array shape is part of contract; preserve.

### SH-Q15 — JSON pretty-printing
**Card:** `analysis/subsystems/sonarr-http.md`.
**Direction:** Switch to compact + opt-in `?pretty=true` query (perf improvement).

---

## Phase 4 blockers (provider port)

### PROV-Q1, PROV-Q3, PROV-Q6, PROV-Q7, PROV-Q8, PROV-Q9, PROV-Q10, PROV-Q16, PROV-Q17, PROV-Q18, PROV-Q22, PROV-Q23, PROV-Q25, PROV-Q26, PROV-Q27, PROV-Q28, PROV-Q29, PROV-Q30..PROV-Q41, PROV-Q44, PROV-Q45
**Cards:** `analysis/providers/*.md`.
**Direction:** Most are port-time decisions documented in their cards. PROV-Q22 (Twitter v1.1 sunset): port-and-deprecate (Test() always fails, preserves rows). PROV-Q25 (auth.servarr.com proxy dependence): keep proxy for v1; revisit per-instance OAuth in v2.

### NOTIFY-Q3 — Twitter
See PROV-Q22 above.

### IL-Q5, IL-Q6, IL-Q7, IL-Q9, IL-Q10, IL-Q12 — Import lists
**Card:** `analysis/subsystems/importlists-metadata.md`.
**Direction:** IL-Q5: keep all 10 providers. IL-Q6: keep auth.servarr.com proxy (for v1). IL-Q7: surface watchlist truncation warning. IL-Q9: split DTO from row. IL-Q10: don't ship JSON-schema validator. IL-Q12: confirm Custom RSS reachable; if not, drop.

### MD-Q3, MD-Q4, MD-Q5, MD-Q6, MD-Q7 — SkyHook
**Card:** `analysis/subsystems/importlists-metadata.md`.
**Direction:** MD-Q3: plumb language signature. MD-Q4: split free-text vs prefixed. MD-Q5: add IsExisting flag. MD-Q6: preserve unconditional Monitored=true (overridden by callers). MD-Q7: distinct Unknown + warning.

### DA-Q1, DA-Q2, DA-Q6, DA-Q7, DA-Q8 — Data augmentation
**Card:** `analysis/subsystems/importlists-metadata.md`.
**Direction:** DA-Q1: 12h cache. DA-Q2: emit healthcheck on silent failure. DA-Q6: re-verify XEM error messages. DA-Q7: write table-driven tests for ExtrapolateMappings. DA-Q8: verify dedup hack persistence.

### Q-03 — Sentry DSN handling
**Card:** `analysis/subsystems/common.md`.
**Direction:** Move to env var (`SONARR_SENTRY_DSN`); preserve hard-coded default for built-in builds. Resolved by porter at build time.

### ANALYTICS-Q1..Q5 — Analytics phone-home
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Stakes:** **Stop-the-world** for Phase 1 — frontend behaviour, telemetry coupling.
**Direction:** ANALYTICS-Q1: opt-in default (preserves C# behaviour). ANALYTICS-Q2: env-var DSN. ANALYTICS-Q3: keep services.sonarr.tv for daily-series + scene-mapping even when telemetry disabled. ANALYTICS-Q4: frontend reads `analytics: false` from `/initialize.json` and skips Sentry init. ANALYTICS-Q5: surface as injected service.

---

## Phase 5 blockers

### REST-Q4, SIGNALR-Q3 (informational), SIGNALR-Q4 (informational)
See resolved-in-source above; all ported verbatim.

### HTTP-Q1, HTTP-Q2 — TorCache + CloudFlare interceptor (possibly dead code)
**Card:** `analysis/subsystems/http-instrumentation-misc.md`.
**Direction:** Verify by searching production logs / RSS samples for hits. If zero hits in last 90 days: drop entirely.

### NOTIFY-Q2 — Webhook event-type casing
See Phase 2 above (already covered; placeholder for the cross-cut).

---

## Port-time decisions (no blocker, but flag at PR review)

### Q-13 — Single ReleaseTitleNormalizer package
**Direction:** Consolidate `Parser.SimplifyTitle / NormalizeTitle / NormalizeEpisodeTitle / CleanSeriesTitle` + the `RegexReplace` arrays + `LanguageParser.CleanSeriesTitleRegex` into a single Go `internal/parser/releasetitle` package. Façade pattern; the C# layout reflects historical accident, not API contract.

### REST-Q4 — V3/V5 dual-emit
**Direction:** Dual-emit during V3 deprecation runway. Once V3 is dropped (post-Sonarr-v6), collapse to single emit + client-side filtering. Halves websocket bandwidth.

### Q-02 — Process kill semantics
**Direction:** Match C# exactly (no tree-kill).

### Q-04 — musl detection
**Direction:** One Linux binary; runtime probe for `/lib/ld-musl-*-*.so.1`.

### Q-06 — Cutoff query LIKE-vs-generated-column
**Direction:** Preserve LIKE on Phase 1; consider generated column in Phase 3+.

### Q-07 — AbsoluteEpisodeNumberAdded transient field
**Direction:** Promote onto event payload (more explicit) or keep on struct (matches C#).

### Q-15, Q-16 — Parser wall-clock + RealRegex
**Direction:** Inject clock; preserve buggy `RealRegex` behaviour for byte-stable parity.

### JOBS-Q1, JOBS-Q2, JOBS-Q3, JOBS-Q4
**Direction:** JOBS-Q1: 5-min ImportListSync — configurable. JOBS-Q2: register-by-attribute pattern in Go via struct tags. JOBS-Q3: keep 30s tick. JOBS-Q4: per-instance struct, not static.

### LIFE-Q1, LIFE-Q2 — Lifecycle ordering & RestartPending
**Direction:** Explicit registration order; richer command result.

### Q-Q1 — Quality.Id 11
**Direction:** Keep reserved (renumbering breaks user DBs).

### NOTIFY-Q1 — Redundant clauses
**Direction:** Drop in Go port.

### NOTIFY-Q7 — Plex OAuth dead defensive code
**Direction:** Drop in Go port.

### HOUSE-Q3 — DeleteBadMediaCovers
**Direction:** Drop entirely in Go port.

### IS-Q4, ORG-Q6, AT-Q7
**Direction:** Cosmetic / port-time cleanups.

### MC-Q2, MC-Q3, MC-Q5 — MediaCover
**Direction:** All low-priority cleanups.

### SS-Q2, SS-Q4 — SeriesStats
**Direction:** SS-Q2: align season-0 exclusion across LastAired/NextAiring/PreviousAiring. SS-Q4: drop Mono GHI 3518 try/catch.

### LANG-Q3, LANG-Q4, LANG-Q5, LANG-Q6 — Language ordering
**Direction:** LANG-Q3: golden-test the rendered list before port. LANG-Q4: identify List.Sort callers; switch to slices.SortFunc with strict-total-order. LANG-Q5: confirm with frontend; default to keep Unknown(0) excluded. LANG-Q6: keep alternate code mappings.

### LOC-Q3, LOC-Q4, LOC-Q5 — Localization
**Direction:** LOC-Q3: tighten regex to also accept hyphens/snake_case if needed. LOC-Q4: no per-request locale switching. LOC-Q5: keep asymmetry (only health-check + indexer-test localised).

### VAL-Q2, VAL-Q5, VAL-Q6 — Validation
**Direction:** VAL-Q2: confirm wire shape from cards. VAL-Q5: keep ValidPort behaviour. VAL-Q6: build-tag exclusion on non-Windows.

---

## Drop in Go port

These are decisions to delete code, not to port it.

- **RP-Q1..RP-Q5** — Entire `Sonarr.RuntimePatches` project. Citation: `analysis/subsystems/native-platform.md`.
- **HOUSE-Q3 / DeleteBadMediaCovers** — 2014-vintage one-shot housekeeper. `src/NzbDrone.Core/Housekeeping/Housekeepers/DeleteBadMediaCovers.cs`.
- **EXTRAS-Q1 / ImportedSubtitleFiles** — Unreferenced DTO. `src/NzbDrone.Core/Extras/Subtitles/ImportedSubtitleFiles.cs`.
- **MONO-Q3 / GetUserId**, **MONO-Q9 / IssueFileVersionAdapter Arch-only**, **MONO-Q12 / Manjaro-matching** — Native-platform vestigial code.
- **PROV-Q2 / Torznab.GetDefinition setting Protocol=Usenet** — Dead code.
- **PROV-Q37 / SkyHookProxy double-assignment** — Dead constructor body.
- **HEALTH-Q2 / RemotePathMappingCheck.Check(IEvent) overload** — Dead code.
- **HOST-Q6 / `RegisterUrl` mode** — URL-ACL was dropped when Kestrel replaced HttpListener.
- **HOST-Q8 / Bootstrap.ASSEMBLIES** — Drop assembly scanning; explicit registration list.
- **DOWNLOAD-Q5 / Legacy `DownloadClientType` enum** — Not the source of truth for new clients.
- **NOTIFY-Q1 / Redundant Enable clauses**.
- **NOTIFY-Q7 / Plex OAuth defensive filter**.
- **TAGS-Q4 / `MetadataDefinition.Tags` column** — Dead.
- **IS-Q4 / `EpisodeSearchService.IsMonitored`** — Dead helper.
- **UPD-Q3 / `_processProvider.FindProcessByName(...)` discarded result**.
- **UPD-Q8 / `UpdateStartupContext.UpdateLocation`** — Duplicates `IAppFolderInfo.GetUpdatePackageFolder()`.
- **UPD-Q12 / `app.config` `<supportedRuntime>`** — No-op file; targets `net10.0`.
- **MONO-Q7 / `NativeMethods.clone_file` (FICLONE)** — No callers.
- **CFLT-* (CloudFlare CAPTCHA detector)** if zero log hits in last 90 days — verify before drop.
- **HTTP-Q1 / TorCache interceptor** — Verify before drop.
- **PROV-Q22 / Twitter notification** — Port-and-deprecate; not a hard delete.
- **HOST-Q7** — `RuntimeInfo.IsWindowsService` always-false bug; Go port replaces with deployment-mode flag.

---

## Upstream C# bugs found during analysis

These are bugs in the existing C# code, surfaced during read-through. They are **not** Go-port questions; they belong in separate PRs against `v5-develop`. Severity tags: 🔴 user-visible, 🟡 latent, 🟢 cosmetic.

| Bug | Citation | Severity | Affects Go port? |
|---|---|---|---|
| `KodiEpisodeGuide` populates `TvRage` from `series.TvMazeId` | `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/KodiEpisodeGuide.cs:31` | 🔴 user-visible (NFO output) | Yes — port replicates verbatim per EXTRAS-Q5 |
| `RuntimePatchBase.TryPatchMethod(type, "GetSslServer")` ignores `methodName` parameter | `src/NzbDrone.Core/RuntimePatches/RuntimePatchBase.cs:84` | 🟡 latent (only caller passes the same value) | No — project deletes |
| `RuntimeInfo.IsWindowsService` permanently false on actual service install | `src/NzbDrone.Host/RuntimeInfo.cs:30` | 🟡 latent | Yes — fix in port (HOST-Q7) |
| `Pneumatic` extends `DownloadClientBase` not `UsenetClientBase`, bypassing nzb validation | `src/NzbDrone.Core/Download/Clients/Pneumatic/Pneumatic.cs` (PROV-Q14) | 🟡 latent | Yes — fix or document |
| `Discord.Embed.Url = "...id=0"` when `series.TvdbId == 0` | `src/NzbDrone.Core/Notifications/Discord/Discord.cs` (NOTIFY-Q10) | 🟢 cosmetic | Yes — guard with `if series.TvdbId > 0` |
| `SchemaBuilder._mappings` static cache never invalidates | `src/Sonarr.Http/ClientSchema/SchemaBuilder.cs:19` (SH-Q2) | 🟢 cosmetic (no hot-reload in C#) | Yes — port adds invalidation |
| `LoggingMiddleware._errorHandler` field stored but never used | `src/Sonarr.Http/Middleware/LoggingMiddleware.cs:19` (SH-Q3) | 🟢 cosmetic dead field | Yes — drop |
| `IfModifiedMiddleware` returns 304 on any If-Modified-Since (no mtime check) | `src/Sonarr.Http/Middleware/IfModifiedMiddleware.cs:23` (SH-Q4) | 🟡 latent (?h= cache-buster covers it) | Yes — port can preserve or harden |
| `BackupFileMapper` uses `string.Replace` then `Path.Combine` (no traversal protection) | `src/Sonarr.Http/Frontend/Mappers/BackupFileMapper.cs:20` (SH-Q14) | 🔴 path-traversal | Yes — fix in port (filepath.Clean + prefix check) |
| `ApiInfoController` lacks `[AllowAnonymous]` | `src/Sonarr.Http/Frontend/ApiInfoController.cs` (SH-Q13) | 🟡 likely-bug | Yes — confirm with client trace |
| `MetadataService.ProcessEpisodeImages` early-return inside loop | `src/NzbDrone.Core/Extras/Metadata/MetadataService.cs:444` (EXTRAS-Q4) | 🟡 latent | Port preserves |
| `MessageHub.OnConnectedAsync` broadcasts `version` to `Clients.All` | `src/NzbDrone.SignalR/MessageHub.cs:41-58` (SIGNALR-Q4) | 🟢 (intentional) | Port preserves |
| `MediaServerUpdateQueue.ProcessQueue` drops pending items on inner exception | (NOTIFY-Q4) | 🟡 latent | Port can re-enqueue |
| `RealRegex` over-matches `REAL` anywhere in title (TODO comment) | `src/NzbDrone.Core/Parser/QualityParser.cs:700-701` (Q-16) | 🟡 (parser bug, byte-stable) | Port preserves |
| Postgres series-stats branch uses `string_agg(DISTINCT ...)`; SQLite branch uses `GROUP_CONCAT` + C# dedup | (SS-Q1) | 🟡 cross-DB parity | Recently fixed in C# (`Produce distinct values in aggregated series stats from Postgres`); port follows new shape |
| `DeleteBadMediaCovers` self-disables 2014 housekeeper | (HOUSE-Q3) | 🟢 cosmetic | Drop |

---

## Original Q-01..Q-18 numbered entries (preserved for backwards compatibility)

Subsystem cards reference these by `Q-NN`. Keep numbers stable.

> **Q-01** Common — Go logging library choice for NLog parity.
> **Status:** unresolved → **Phase 1 blocker** above.

> **Q-02** Common — child-process group/kill semantics.
> **Status:** resolved-in-source → match C# semantics with `os.Process.Kill`.

> **Q-03** Common — Sentry DSN handling.
> **Status:** Phase 4 (`ANALYTICS-Q2` consolidates) — env-var.

> **Q-04** Common — musl runtime detection.
> **Status:** resolved-in-source → runtime probe.

> **Q-05** Common — Newtonsoft JSON quirks.
> **Status:** resolved-in-batch (Batch B) → System.Text.Json with documented options.

> **Q-06** Tv — cutoff-unmet quality LIKE-match.
> **Status:** resolved-in-source → preserve LIKE on Phase 1.

> **Q-07** Tv — `AbsoluteEpisodeNumberAdded` transient field semantics.
> **Status:** resolved-in-source → in-process event bus, port-time decision.

> **Q-08** Tv — `RefreshSeriesCommand` scheduler/queue semantics.
> **Status:** resolved-in-source → single command, two scheduling modes.

> **Q-09** Datastore — Embedded-document type-handler reflection scan.
> **Status:** Phase 1 blocker (residual) → generic `JSONColumn[T]` wrapper.

> **Q-10** Datastore — SQLite `LIKE` case-sensitivity assumption.
> **Status:** resolved-in-source → preserve modernc default; pgx uses ILIKE.

> **Q-11** Datastore — Postgres extensions required.
> **Status:** resolved-in-source → vanilla Postgres 15+ sufficient.

> **Q-12** Datastore — `MigrationContext.Current` static and concurrency.
> **Status:** Phase 1 blocker → context-passed in Go.

> **Q-13** Parser — Missing `ReleaseTitleNormalizer.cs`.
> **Status:** resolved-in-card → port-time decision to consolidate into one package.

> **Q-14** Parser — Regex engine choice (RE2 vs .NET-compatible).
> **Status:** resolved-in-batch (Batch C) → rewrite to RE2.

> **Q-15** Parser — Wall-clock dependency in daily airdate validation.
> **Status:** resolved-in-source → inject clock interface.

> **Q-16** Parser — `RealRegex` over-matches.
> **Status:** resolved-in-source → preserve buggy-but-stable behaviour.

> **Q-17** Parser — `PreSubstitutionRegex` loop-mode asymmetry.
> **Status:** resolved-in-card → preserve verbatim.

> **Q-18** Parser — Test fixture row counting for non-`[TestCase]` data.
> **Status:** resolved-in-card → 1,585 cases, port both forms.

---

## Index of resolved questions referenced by subsystem cards

This index lets a porter answer "is `<PREFIX-QN>` blocking me?" without re-scanning every card.

For machine-greppable searches:
- `^- \*\*<PREFIX-QN>\*\*` finds the entry's body in this file (when listed under a Phase header).
- Cards keep their original `## Open questions` sections unchanged; consult this ledger for status, not the cards.

End of ledger.
