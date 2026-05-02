# NzbDrone.Common

**Source:** `src/NzbDrone.Common/**`
**Target Go package(s):** `go/internal/common/...` (split by sub-area)
**Status:** drafted

## Purpose
Foundation library that every other Sonarr project depends on. Provides cross-cutting primitives: app/data path resolution, OS/runtime detection, command-line / startup-context parsing, an in-memory cache, an opinionated HTTP client (with cookies, proxy, rate limiting, Happy Eyeballs, TLS), disk operations (with platform-specific subclasses elsewhere), child-process management, NLog-based logging with secret cleansing and Sentry integration, JSON serialization (both Newtonsoft and System.Text.Json), task primitives (debouncer, rate limiter, lock pool), reflection helpers, and a large pile of `IEnumerable`/`string`/`path` extension methods. The project also embeds a small fluent `Ensure` validation library and a vendored copy of an OAuth 1.0a signer.

## Sub-areas
- **Cache** (`Cache/`): in-memory generic cache. `ICacheManager` returns `ICached<T>` / `ICachedDictionary<T>` keyed by host `Type`. Backed by `ConcurrentDictionary`. Supports rolling expiry, fetch-func dictionaries, and a global `Clear`. Target Go: `internal/common/cache` — typed cache via Go generics over `sync.Map` or `sync.RWMutex`+`map`.
- **Cloud** (`Cloud/`): `SonarrCloudRequestBuilder` — pre-baked `HttpRequestBuilder` factories for Services, Skyhook, Sonarr.tv, GitHub, etc. Target Go: `internal/common/cloud`.
- **Composition** (`Composition/`): assembly scanning and `KnownTypes`. Bridges DryIoc / `Microsoft.Extensions.DependencyInjection`. Target Go: not ported as a unit — replaced by explicit constructor wiring in `cmd/sonarr/main.go` per `CONVERSION.md`.
- **Crypto** (`Crypto/`): `HashConverter`, `HashProvider` (MD5 of a file via `IDiskProvider`). Target Go: `internal/common/crypto` (`crypto/md5`).
- **Disk** (`Disk/`): `IDiskProvider`, `DiskProviderBase` (abstract; Windows/Linux/OSX subclasses live in `NzbDrone.Mono` / `NzbDrone.Windows`), `DiskTransferService`, `OsPath` value type, mount/drive abstractions, path-validation enums, `LongPathSupport`, `SpecialFolders`, `SystemFolders`. Target Go: `internal/common/disk` — interface stays abstract; platform impls go in `disk_unix.go` / `disk_windows.go` build-tagged files. `OsPath` becomes a small value-type struct.
- **EnsureThat** (`EnsureThat/`): vendored fluent argument-validation library (`Ensure.That(x, () => x).IsNotNull()`). Target Go: drop entirely — Go uses early `if x == nil { return errors.New(...) }`. Resource bundle `EnsureThat/Resources/ExceptionMessages.resx` only matters if we want to keep error wording stable.
- **EnvironmentInfo** (`EnvironmentInfo/`): `AppFolderInfo`, `BuildInfo`, `OsInfo` (Windows/Linux/Linux-Musl/OSX/BSD detection + Docker/Podman heuristics), `PlatformInfo`, `RuntimeInfo` (testing/dev/production discrimination, admin check, systemd, tray, service), `StartupContext` (CLI flag/arg parser), `IOsVersionAdapter`. Target Go: `internal/common/envinfo` — `runtime.GOOS`-based detection; CLI parsing through stdlib `flag` or a typed struct.
- **Exceptions** (`Exceptions/`): `NzbDroneException`, `SonarrStartupException`. Target Go: `internal/common/errs` with custom error types implementing `error`.
- **Expansive** (`Expansive/`): vendored mini-template engine for `${var}` interpolation with circular-reference detection (`Tree`, `TreeNode`, `TreeNodeList`, `PatternStyle`). Has its own license. Target Go: `internal/common/expansive` (port verbatim) or replace with `os.Expand` + cycle check.
- **Extensions** (`Extensions/`): a *very* large grab-bag of static extension classes — `Base64`, `DateTime`, `Dictionary`, `DnsEndPoint`, `Exception`, `IEnumerable`, `IpAddress`, `Levensthein`, `Nullable`, `Number`, `Object`, `Path`, `Regex`, `Resource`, `Stream`, `String`, `TryParse`, `Url`, `Xml`. `PathExtensions` alone defines ~40 helpers including all the `GetAppDataPath/GetLogFolder/GetConfigPath/GetDatabase/GetMediaCoverPath/...` getters off `IAppFolderInfo`. Target Go: spread across small focused packages (`internal/common/pathx`, `internal/common/strx`, etc.).
- **Globalization** (`Globalization/`): `AdditionalDiacriticsProvider` (uses `Diacritical.Net`). Target Go: `internal/common/globalization` using `golang.org/x/text/unicode/norm` + a hand-maintained map.
- **Http** (`Http/`): `HttpClient` (interface + impl), `HttpRequest`/`HttpResponse`, `HttpRequestBuilder` (+ factory), `HttpUri`, `HttpHeader`, `HttpFormData`, `HttpAccept`, `HttpException`, `TooManyRequestsException`, `TlsFailureException`, `UnexpectedHtmlContentException`, `BasicNetworkCredential`, `JsonRpcRequestBuilder`, `XmlRpcRequestBuilder`, `IHttpRequestInterceptor`, `UserAgentBuilder`/`UserAgentParser`. Sub-folders:
  - `Http/Dispatchers/`: `IHttpDispatcher`, `ManagedHttpDispatcher` (single underlying `System.Net.Http.HttpClient` per proxy-key, cached), `ICertificateValidationService`.
  - `Http/HappyEyeballs/`: `HappyEyeballs`, `HttpHappyEyeballs` — RFC 8305 dual-stack connect.
  - `Http/Proxy/`: `HttpProxySettings`, `IHttpProxySettingsProvider`, `ManagedWebProxyFactory`, `ProxyType`.
  Target Go: `internal/common/http` wrapping `net/http` with the same `HttpRequestBuilder` ergonomics; Happy Eyeballs is built into Go's `net.Dialer`.
- **Instrumentation** (`Instrumentation/`): `NzbDroneLogger` (the entry point — registers Sentry/console/file/debugger/update/auth log targets), `CleanseLogMessage` (regex secret redaction for trackers, paths, Plex/Discord/Telegram tokens, IPs), `CleansingClefLogLayout`, `CleansingConsoleLogLayout`, `CleansingFileTarget`, `CleansingJsonVisitor`, `GlobalExceptionHandlers`, `InitializeLogger`, `LogEventExtensions`, `VersionLayoutRenderer`. Sub-folders:
  - `Instrumentation/Extensions/`: composition + logger + sentry-logger extensions.
  - `Instrumentation/Sentry/`: `SentryTarget` (NLog target → Sentry SDK), `SentryCleanser`, `SentryDebounce`.
  Target Go: `internal/common/log` over `log/slog` (or `zerolog`) with a redaction middleware and `getsentry/sentry-go` integration.
- **Messaging** (`Messaging/`): bare interfaces `IEvent`, `IMessage`, `LifecycleEventAttribute`. Target Go: `internal/common/event` empty marker types.
- **Model** (`Model/`): `ProcessInfo` DTO. Target Go: lives in `internal/common/process`.
- **OAuth** (`OAuth/`): vendored OAuth 1.0a signing toolkit (`OAuthRequest`, `OAuthTools`, `WebParameter`, `WebParameterCollection`, `OAuthSignatureMethod/Treatment`). Has its own LICENSE. Target Go: port verbatim into `internal/common/oauth1` (keep API-compatible — still consumed by Trakt etc.).
- **Options** (`Options/`): typed config records bound from `appsettings.json` / env: `AppOptions`, `AuthOptions`, `LogOptions`, `ServerOptions`, `UpdateOptions`. Target Go: `internal/common/options` populated at startup from `os.Getenv` (per `CONVERSION.md`).
- **Processes** (`Processes/`): `IProcessProvider`/`ProcessProvider` (start/spawn/kill/find/wait, console capture), `ProcessOutput`, `ProcessOutputLine`, `PidFileProvider`. Target Go: `internal/common/process` over `os/exec`.
- **Reflection** (`Reflection/`): `ReflectionExtensions` (load implementations of T from assemblies). Target Go: not directly portable — replaced with explicit registration tables.
- **Serializer** (`Serializer/`):
  - `Newtonsoft.Json/`: `Json` static façade, `HttpUriConverter`, `IntConverter`, `JsonVisitor`, `UnderscoreStringEnumConverter`.
  - `System.Text.Json/`: `STJson` static façade, `STJHttpUriConverter`, `STJTimeSpanConverter`, `STJUtcConverter`, `STJVersionConverter`, `PolymorphicWriteOnlyJsonConverter`.
  Two parallel serializers exist because the API V3 contract was frozen on Newtonsoft semantics. Target Go: `internal/common/serializer` over `encoding/json` with custom `MarshalJSON` to mimic Newtonsoft quirks (camelCase, enum-as-string with underscores).
- **TPL** (`TPL/`): `Debouncer`, `DebounceManager`, `RateLimitService`, `LimitedConcurrencyLevelTaskScheduler`, `LockByIdPool`, `TaskExtensions`. Target Go: `internal/common/tpl` — `Debouncer` becomes `time.Timer`; `RateLimitService` becomes a per-key `time.Time` map under `sync.Mutex`; `LimitedConcurrencyLevelTaskScheduler` becomes a worker-pool over a buffered channel.
- **Top-level files**: `ArchiveService.cs` (SharpZipLib zip/tar.gz), `ConsoleService.cs`, `ConvertBase32.cs`, `HashUtil.cs`, `PathEqualityComparer.cs`, `ServiceFactory.cs`/`ServiceProvider.cs` (DryIoc helpers), `Utf8StringWriter.cs`.

## Public types of note
| Type | File | Role |
| --- | --- | --- |
| `ICacheManager` / `CacheManager` | `Cache/CacheManager.cs:7` | Returns named caches keyed by `Type`. |
| `Cached<T>` | `Cache/Cached.cs:9` | Generic in-memory cache; rolling expiry. |
| `IDiskProvider` | `Disk/IDiskProvider.cs:7` | Disk facade — every file/folder op funnels through this. |
| `DiskProviderBase` | `Disk/DiskProviderBase.cs:14` | Cross-platform parts; abstract permission methods overridden in Mono/Windows. |
| `IDiskTransferService` / `DiskTransferService` | `Disk/DiskTransferService.cs:11` | Move/Copy/Hardlink with verify, retry, casing-fix. |
| `OsPath` | `Disk/OsPath.cs:8` | Value type that knows its `OsPathKind` (Unix/Windows/UNC). |
| `IAppFolderInfo` / `AppFolderInfo` | `EnvironmentInfo/AppFolderInfo.cs:9` | Resolves AppData / temp / startup folders, honoring `--data=`. |
| `OsInfo` | `EnvironmentInfo/OsInfo.cs:9` | Static OS detection (Windows/Linux/Linux-Musl/OSX/BSD) + Docker/Podman. |
| `IRuntimeInfo` / `RuntimeInfo` | `EnvironmentInfo/RuntimeInfo.cs:12` | Dev/Test/Production discrimination, admin, systemd, tray. |
| `IStartupContext` / `StartupContext` | `EnvironmentInfo/StartupContext.cs:5` | Parses `argv` into flags/args. Constants for `data`, `nobrowser`, `restart`, `terminateexisting`. |
| `IHttpClient` / `HttpClient` | `Http/HttpClient.cs:18` | Public HTTP API: `Execute*`, `DownloadFile*`, `Get/Post/Head*`, plus async variants. |
| `IHttpDispatcher` / `ManagedHttpDispatcher` | `Http/Dispatchers/ManagedHttpDispatcher.cs:17` | Owns the underlying `System.Net.Http.HttpClient` cache (per-proxy). |
| `HttpRequest` / `HttpRequestBuilder` | `Http/HttpRequest.cs:12`, `Http/HttpRequestBuilder.cs:12` | Request DTO + fluent builder. |
| `HttpUri` | `Http/HttpUri.cs:1` | Sonarr's normalized URI struct (`+` operator for relative join). |
| `IRateLimitService` / `RateLimitService` | `TPL/RateLimitService.cs:10` | Per-host (+ optional sub-key) sleep-until rate limit. |
| `Debouncer` | `TPL/Debouncer.cs:5` | Timer-based action debouncer (Pause/Resume/Execute). |
| `IProcessProvider` / `ProcessProvider` | `Processes/ProcessProvider.cs:16` | Start/Kill/Find/wait child processes; `SONARR_PROCESS_NAME = "Sonarr"`. |
| `NzbDroneLogger` | `Instrumentation/NzbDroneLogger.cs:13` | NLog config bootstrap (file/console/sentry/auth/update). |
| `CleanseLogMessage` | `Instrumentation/CleanseLogMessage.cs:8` | Secret-redaction regex pipeline applied to every log message. |
| `SentryTarget` | `Instrumentation/Sentry/SentryTarget.cs` | NLog → Sentry bridge with debounce. |
| `IHttpProxySettingsProvider` / `HttpProxySettings` | `Http/Proxy/HttpProxySettings.cs` | Proxy config model. |
| `Json` / `STJson` | `Serializer/Newtonsoft.Json/Json.cs`, `Serializer/System.Text.Json/STJson.cs` | Two parallel serializer façades. |
| `IHashProvider` / `HashProvider` | `Crypto/HashProvider.cs:6` | MD5 of file via `IDiskProvider`. |
| `Ensure` | `EnsureThat/Ensure.cs` | Fluent guard-clause entry point. |

## External NuGet dependencies pulled in by this project
From `Sonarr.Common.csproj`:

| NuGet | Purpose | Go replacement |
| --- | --- | --- |
| `Diacritical.Net` 1.0.5 | Diacritic stripping for search | `golang.org/x/text/unicode/norm` + transform.Chain |
| `DryIoc.dll` 5.4.3 | DI container | none — explicit constructor injection (`CONVERSION.md`) |
| `IPAddressRange` 6.3.0 | CIDR/range matching | `net/netip` + small helper |
| `Microsoft.Extensions.DependencyInjection` 10.0.7 | DI scaffolding | none — same as above |
| `Microsoft.Extensions.Hosting.WindowsServices` 10.0.7 | Windows service host | `golang.org/x/sys/windows/svc` (Windows-only build tag) |
| `Newtonsoft.Json` 13.0.4 | Legacy JSON contract | `encoding/json` with custom marshalers preserving Newtonsoft quirks |
| `NLog` 5.5.1 | Logging | `log/slog` (or `zerolog`); see Q-01 |
| `NLog.Layouts.ClefJsonLayout` 1.0.5 | CLEF (Compact Log Event Format) console output | hand-rolled CLEF encoder if `SONARR__LOG__CONSOLEFORMAT=clef` is preserved |
| `NLog.Targets.Syslog` 7.0.0 | syslog target | `log/syslog` (Unix) |
| `NLog.Extensions.Logging` 5.5.0 | NLog ↔ MEL bridge | n/a |
| `Sentry` 5.16.3 | Crash reporting | `getsentry/sentry-go` |
| `SharpZipLib` 1.4.2 | zip/tar/gz extraction | `archive/zip`, `archive/tar`, `compress/gzip` |
| `SourceGear.sqlite3` 3.50.4.5 | bundled SQLite native | `modernc.org/sqlite` (`CONVERSION.md`) |
| `System.Data.SQLite` 2.0.3 | SQLite ADO.NET | `database/sql` + `modernc.org/sqlite` |
| `System.Configuration.ConfigurationManager` 10.0.7 | `app.config` reader | replaced by env vars / typed struct |
| `System.IO.FileSystem.AccessControl` 6.0.0-preview | Windows ACLs | `golang.org/x/sys/windows` ACL helpers (Windows-only build tag) |
| `System.ServiceProcess.ServiceController` 10.0.7 | Windows service control | `golang.org/x/sys/windows/svc/mgr` |

## Cross-cutting concerns
- **HTTP client.** Everything goes through `HttpClient` (`Http/HttpClient.cs:41`). Cookies are stored in a per-`HttpClient` `CookieContainer` cached under `cacheManager.GetCache<CookieContainer>(typeof(HttpClient))`; per-request transient cookies merge with the persistent jar (`InitializeRequestCookies`, `Http/HttpClient.cs:177`). Redirects are followed up to 5 hops with 302/303→GET semantics. Rate limiting is per-host via `IRateLimitService.WaitAndPulseAsync`. The actual socket-level work is delegated to `ManagedHttpDispatcher` (`Http/Dispatchers/ManagedHttpDispatcher.cs:17`), which caches one `System.Net.Http.HttpClient` per proxy-key, attaches `HttpHappyEyeballs` for dual-stack connect, and uses `ICertificateValidationService` for TLS overrides. `IHttpRequestInterceptor` lets indexers inject pre/post hooks. There is a non-trivial sync-over-async pattern (`Task.Run(...).GetAwaiter().GetResult()`) explicitly preserved as a "thread-pool hack" — see Quirks.
- **Logging.** NLog is bootstrapped exactly once from `NzbDroneLogger.Register` (`Instrumentation/NzbDroneLogger.cs:28`). It wires up: a Sentry target (DSN varies by `RuntimeInfo.IsProduction` and updater vs. main), a Debugger target if attached, a Console target (formatted via `CleansingConsoleLogLayout` or CLEF), three rolling file targets (`sonarr.txt` info, `sonarr.debug.txt`, `sonarr.trace.txt`) under `appFolderInfo.GetLogFolder()`, an Auth-only logger (final), and a global filter that pins `System.*`/`Microsoft.*`/`Microsoft.Hosting.Lifetime*` to Warn/Info/Fatal. Every layout runs `CleanseLogMessage` (`Instrumentation/CleanseLogMessage.cs:8`) which redacts API keys, tracker passkeys, Plex/Discord/Telegram tokens, and home-folder paths.
- **Caching.** `CacheManager` returns three shapes: `ICached<T>` (string→T with optional rolling expiry), `ICached<T>` constructed via `GetRollingCache` (default lifetime applied), and `ICachedDictionary<T>` with a fetch func that lazy-populates. Keys are `host.FullName + "_" + name`. Backed by `ConcurrentDictionary<string, CacheItem>` (`Cache/Cached.cs:9`). `RateLimitService` and `ManagedHttpDispatcher` both live in this cache.
- **Disk operations.** `DiskProviderBase` provides everything portable (read/write, glob, mounts via `DriveInfoMount`); the `abstract` methods are exclusively permission/ownership related (`InheritFolderPermissions`, `SetEveryonePermissions`, `SetFilePermissions`, `SetPermissions`, `CopyPermissions`, `GetAvailableSpace`, `GetTotalSize`). Subclasses live outside this project (`NzbDrone.Mono`, `NzbDrone.Windows`). `DiskTransferService` adds Move/Copy/Hardlink retry+verify orchestration. `OsPath` value type normalizes slash direction by inferred `OsPathKind`. Path validation has a `PathValidationType.CurrentOs` vs `AnyOs` enum. `LongPathSupport` exists for Windows MAX_PATH workaround. Platform shims needed in Go: file mode/owner/group, symlink/hardlink semantics, free-space stat, mount enumeration, NTFS ACLs.
- **Process management.** `ProcessProvider` (`Processes/ProcessProvider.cs:34`) wraps `System.Diagnostics.Process`: `Start` redirects stdout/stderr with callback-per-line; `SpawnNewProcess` is fire-and-forget; `StartAndCapture` blocks and returns combined output; `KillAll(name)`/`Kill(id)`/`Exists`/`SetPriority`/`OpenDefaultBrowser`. `WaitForExit` does a busy `WaitForExit(Timeout.Infinite)`. Child processes are tracked only by their PID; there is no group/job-object semantics (Q-02). `PidFileProvider` writes the main process pidfile.

## Notable quirks / TODOs / HACKs
- `Http/HttpClient.cs:203` — `Cookie.Expires = DateTime.Now.AddHours(1)`: explicit comment "Use Now rather than UtcNow to work around Mono cookie expiry bug." See `https://gist.github.com/ta264/7822b1424f72e5b4c961`. Go port should still use the cookie's intended UTC semantics.
- `Http/HttpClient.cs:314` — `Task.Run(() => DownloadFileAsync(url, fileName)).GetAwaiter().GetResult();`: comment links to the MSDN "thread-pool hack" article. The whole sync layer of `IHttpClient` is implemented this way. Goroutines make this trivially natural in Go.
- `Http/HttpHeader.cs:176` — `// TODO: Find encoding by Byte order mask.` Currently relies on `Content-Type` charset only.
- `Http/HttpRequestBuilder.cs:218` — `// TODO: Scan through body to see if we have a boundary collision?` — multipart boundary not validated.
- `Disk/DiskProviderBase.cs:347` — `// This workaround from https://github.com/dotnet/runtime/issues/42790#issuecomment-700362617` for `DirectoryInfo` enumeration on Linux failing on broken symlinks.
- `EnvironmentInfo/RuntimeInfo.cs:152-220` — `InternalIsTesting()` does string-matches on the process name and current directory (`vshost`, `nunit`, `jetbrain`, `resharper`, `_output`, `_tests`, `vsts`, `buildagent`). Brittle. The Go equivalent should use an explicit env var (e.g. `SONARR_TESTING=1`) instead.
- `EnvironmentInfo/RuntimeInfo.cs:233` — `IsOfficialBuild` heuristic: `Major >= 10 || Revision > 10000` flags unofficial. Sonarr's CI sets the revision; deviations from this rule break analytics gating.
- `EnvironmentInfo/AppFolderInfo.cs:38` — Legacy NzbDrone path is computed differently on macOS (`~/.config/NzbDrone`) vs everywhere else. Migration code elsewhere relies on this.
- `EnvironmentInfo/RuntimeInfo.cs:34` — On Windows tray, `IsWindowsTray` is detected via `entry.ModuleName == "Sonarr.exe"`. Tray support may not port to Go on day one.
- `Cache/Cached.cs:42` — `Set` ignores `_rollingExpiry`; only `Get`/`Find` apply rolling. Subtle behavior to preserve.
- `TPL/RateLimitService.cs:67-90` — sub-key rate limiting expands the base-key timer but caps at `now + interval` to prevent runaway extension. The exact `Math.Max(...).Add(interval)` semantics need to round-trip in Go.
- `Instrumentation/NzbDroneLogger.cs:75` — Sentry DSNs are hard-coded for prod vs. dev, both for the main app and the updater. Decision needed: keep DSNs in code, or move to env vars (Q-03).
- The project supports a `linux-musl-x64` / `linux-musl-arm64` `RuntimeIdentifier` via the `ISMUSL` `DefineConstant` (`Sonarr.Common.csproj`). Go solves this with `runtime.GOOS`+CGO toggles; musl detection at runtime needed only for Sentry tagging (Q-04).
- Two parallel JSON serializers exist (`Serializer/Newtonsoft.Json/` and `Serializer/System.Text.Json/`). The codebase is mid-migration. Sonarr v5 API contract still expects Newtonsoft semantics — `encoding/json` quirks (e.g. `omitempty`, enum casing) must be checked field-by-field (Q-05).

## Test files (the port must keep passing equivalents)
- `src/NzbDrone.Common.Test/CacheTests/CachedFixture.cs` — expiry, rolling, concurrent access.
- `src/NzbDrone.Common.Test/CacheTests/CachedDictionaryFixture.cs`, `CachedManagerFixture.cs`.
- `src/NzbDrone.Common.Test/DiskTests/DiskProviderFixtureBase.cs`, `DiskTransferServiceFixture.cs`, `FreeSpaceFixtureBase.cs`, `IsParentFixtureBase.cs`, `DirectoryLookupServiceFixture.cs`, `MockMount.cs`.
- `src/NzbDrone.Common.Test/Http/HttpClientFixture.cs` — large; covers redirects, cookies, gzip, rate limit.
- `src/NzbDrone.Common.Test/Http/HappyEyeballsFixture.cs`, `HttpRequestBuilderFixture.cs`, `HttpRequestFixture.cs`, `HttpHeaderFixture.cs`, `HttpUriFixture.cs`, `UserAgentBuilderFixture.cs`, `UserAgentParserFixture.cs`.
- `src/NzbDrone.Common.Test/InstrumentationTests/CleanseLogMessageFixture.cs` — full regex matrix, must port verbatim.
- `src/NzbDrone.Common.Test/InstrumentationTests/SentryTargetFixture.cs`.
- `src/NzbDrone.Common.Test/TPLTests/DebouncerFixture.cs`, `RateLimitServiceFixture.cs`.
- `src/NzbDrone.Common.Test/EnvironmentInfo/BuildInfoFixture.cs`, `EnvironmentTests/StartupArgumentsFixture.cs`, `EnvironmentTests/EnvironmentProviderTest.cs`.
- `src/NzbDrone.Common.Test/ExtensionTests/IPAddressExtensionsFixture.cs`, `UrlExtensionsFixture.cs`, `NumberExtensionFixture.cs`, `FromOctalStringFixture.cs`, plus `IEnumerableExtensionTests/` and `StringExtensionTests/` sub-folders.
- `src/NzbDrone.Common.Test/OsPathFixture.cs`, `PathExtensionFixture.cs`, `LevenshteinDistanceFixture.cs`, `HashUtilFixture.cs`, `ProcessProviderFixture.cs`, `ConfigFileProviderTest.cs`, `ServiceFactoryFixture.cs`, `ServiceProviderFixture.cs`, `ReflectionTests/ReflectionExtensionFixture.cs`.

## Open questions
- Q-01 — choose Go logging library (NLog parity).
- Q-02 — child-process group/job-object semantics on Windows when porting `ProcessProvider.KillAll`.
- Q-03 — Sentry DSN handling in the Go port (hard-coded vs. env).
- Q-04 — musl detection in Go (relevant only for Sentry tagging / update channel).
- Q-05 — strategy for preserving Newtonsoft JSON quirks in the V3 API while using `encoding/json`.

See `go/analysis/questions.md`.
