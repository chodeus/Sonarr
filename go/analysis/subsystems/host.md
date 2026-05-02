# Host (DI bootstrap, hosting, lifecycle)

**Source:** `src/NzbDrone.Host/**`, `src/NzbDrone.Console/**`, `src/NzbDrone/**`
**Target Go package(s):** `go/cmd/sonarr/...` (entry-points), `go/internal/host/...` (lifecycle, single-instance, browser, access-control), `go/internal/host/startup/...` (HTTP startup wiring)
**Status:** drafted

## Purpose

`NzbDrone.Host` is the glue between an OS-level process (started from `NzbDrone.Console` on Linux/macOS or `NzbDrone` on Windows-tray) and the in-process Sonarr backend. It owns:

- the entry-point pipeline (`Bootstrap.Start`) that turns `argv` into either a utility-mode short-circuit or a full ASP.NET Core `IHost` running Kestrel + SignalR + the v3/v5 HTTP API,
- the DryIoc DI container build (assembly scan of `Sonarr.Host`, `Sonarr.Core`, `Sonarr.SignalR`, `Sonarr.Api.V3`, `Sonarr.Api.V5`, `Sonarr.Http`, plus per-OS `Sonarr.Mono` / `Sonarr.Windows`),
- the ASP.NET `Startup` class that wires Kestrel, ForwardedHeaders, CORS, controllers, SignalR, MiniProfiler, DataProtection, Swagger, authentication / authorization, error handling, then mounts the controllers and the `MessageHub` SignalR hub,
- five Host-only services: `AppLifetime` (start/stop bridge to `IHostApplicationLifetime`), `BrowserService` (open the browser on startup), `SingleInstancePolicy` (refuse / kill / warn duplicate instances), `RestartableServiceLifetime` (custom `IHostLifetime` so an in-process restart works on Windows Service), `UtilityModeRouter` (one-shot CLI commands such as `--registerurl`, `--i`, `--u`),
- Windows access-control adapters under `AccessControl/` (firewall rule + URL ACL via `RemoteAccessAdapter` / `FirewallAdapter`).

Two thin executable projects sit on top of `Host`:

- `NzbDrone.Console` (`Sonarr.Console.csproj` `OutputType=Exe`, AssemblyName `Sonarr` on non-Windows) is the canonical entry-point everywhere except Windows-tray. `ConsoleApp.Main` registers loggers, then calls `Bootstrap.Start(args)` (`src/NzbDrone.Console/ConsoleApp.cs:45`).
- `NzbDrone` (`Sonarr.csproj`, Windows-only, references `System.Windows.Forms`) is the tray entry-point. `WindowsApp.Main` does Windows-Forms init then calls `Bootstrap.Start(args, e => e.ConfigureServices((_, s) => s.AddSingleton<IHostedService, SystemTrayApp>()))` to splice the tray icon into the host's hosted-service set (`src/NzbDrone/WindowsApp.cs:29`).

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `Bootstrap` | `src/NzbDrone.Host/Bootstrap.cs:33` | static class | Single public entry-point: `Start(string[] args, Action<IHostBuilder> trayCallback = null)`. |
| `ApplicationModes` | `src/NzbDrone.Host/ApplicationModes.cs:3` | enum | `Interactive, Help, InstallService, UninstallService, Service, RegisterUrl`. |
| `Startup` | `src/NzbDrone.Host/Startup.cs:45` | class | Standard ASP.NET Core `Startup` class — `ConfigureServices` and `Configure(IApplicationBuilder, …)`. |
| `AppLifetime` | `src/NzbDrone.Host/AppLifetime.cs:13` | `IHostedService`, `IHandle<ApplicationShutdownRequested>` | Bridges Sonarr's `ApplicationStartedEvent` / `ApplicationShutdownRequested` to ASP.NET's `IHostApplicationLifetime`. |
| `IBrowserService` / `BrowserService` | `src/NzbDrone.Host/BrowserService.cs:9,14` | service | `LaunchWebUI()` → opens `http://localhost:{Port}` via `IProcessProvider.OpenDefaultBrowser`. |
| `ISingleInstancePolicy` / `SingleInstancePolicy` | `src/NzbDrone.Host/SingleInstancePolicy.cs:9,16` | service | Process-name-based duplicate detection (no lockfile, no port-bind probe). |
| `RestartableServiceLifetime` | `src/NzbDrone.Host/RestartableServiceLifetime.cs:15` | `IHostLifetime`, `IDisposable` | Replaces stock `WindowsServiceLifetime` so in-process restart works. |
| `IUtilityModeRouter` / `UtilityModeRouter` | `src/NzbDrone.Host/UtilityModeRouter.cs:10,15` | service | One-shot dispatcher for `InstallService` / `UninstallService` / `RegisterUrl` / `Help`. |
| `TerminateApplicationException` | `src/NzbDrone.Host/TerminateApplicationException.cs:5` | `ApplicationException` | Thrown by `SingleInstancePolicy` to signal "exit cleanly". Caught only at the top of `Bootstrap.Start`. |
| `IFirewallAdapter` / `FirewallAdapter` | `src/NzbDrone.Host/AccessControl/FirewallAdapter.cs:10,15` | service | COM-interop (`HNetCfg.FwMgr`) Windows firewall opener. |
| `IRemoteAccessAdapter` / `RemoteAccessAdapter` | `src/NzbDrone.Host/AccessControl/RemoteAccessAdapter.cs:5,10` | service | Calls `FirewallAdapter` only when `OsInfo.IsWindows && IRuntimeInfo.IsAdmin`. |
| `RemoteAccessException` | `src/NzbDrone.Host/AccessControl/RemoteAccessException.cs:6` | `NzbDroneException` | Treated specially in `ConsoleApp.Main` — exits with code `Normal=0` rather than failure. |

The two non-Host entry-point projects:

| Type | File | Notes |
|------|------|-------|
| `ConsoleApp` | `src/NzbDrone.Console/ConsoleApp.cs:15` | `static Main`. Initializes `RuntimePatcher`, `StartupContext`, `NzbDroneLogger.Register(args, updateApp:false, inConsole:true)`, then `Bootstrap.Start(args)`. Catches `SonarrStartupException`, `SocketException`, `IOException` (with `AddressInUseException` inner), `RemoteAccessException`, then generic `Exception` and exits with one of four `ExitCodes` (`Normal=0`, `UnknownFailure=1`, `RecoverableFailure=2`, `NonRecoverableFailure=3`). |
| `WindowsApp` | `src/NzbDrone/WindowsApp.cs:13` | Windows-Forms tray entry. Sets `HighDpiMode.SystemAware`, then calls `Bootstrap.Start(args, e => e.ConfigureServices((_, s) => s.AddSingleton<IHostedService, SystemTrayApp>()))`. The tray callback adds `SystemTrayApp` (a `Form : IHostedService`) to the hosted-service collection so the tray icon comes up alongside Kestrel. |
| `SystemTrayApp` | `src/NzbDrone/SysTray/SysTrayApp.cs:12` | `Form, IHostedService`. `StartAsync` spins up an STA thread that runs `Application.Run(this)`. Tray menu has `Launch Browser` (calls `IBrowserService.LaunchWebUI`) and `Exit` (calls `ILifecycleService.Shutdown`). |

## Application modes

`ApplicationModes` (`ApplicationModes.cs:3`) is the discriminator the bootstrap uses to decide whether to bring up the full host or run a one-shot utility action. Selection is performed in `Bootstrap.GetApplicationMode` (`Bootstrap.cs:215-254`) in this order:

| Mode | Trigger flag (in `StartupContext`) | OS gate | Behaviour |
|------|------------------------------------|---------|-----------|
| `Help` | `?` (`StartupContext.HELP`) | any | `UtilityModeRouter` falls through to `_consoleService.PrintHelp()`. |
| `RegisterUrl` | `registerurl` (`StartupContext.REGISTER_URL`) | Windows only | `UtilityModeRouter` calls `IRemoteAccessAdapter.MakeAccessible(passive: false)` (which delegates to `FirewallAdapter.MakeAccessible()`) and `IAppFolderFactory.SetPermissions()`. |
| `InstallService` | `i` (`StartupContext.INSTALL_SERVICE`, internal const) | Windows only | If service already installed → `IConsoleService.PrintServiceAlreadyExist`. Otherwise: `_remoteAccessAdapter.MakeAccessible(true)`, `_serviceProvider.Install("Sonarr")`, `_serviceProvider.SetPermissions(...)`, then `sc.exe start Sonarr` via `IProcessProvider.SpawnNewProcess` (`UtilityModeRouter.cs:46-63`). |
| `UninstallService` | `u` (`StartupContext.UNINSTALL_SERVICE`, internal const) | Windows only | If not installed → `PrintServiceDoesNotExist`. Otherwise `_serviceProvider.Uninstall("Sonarr")`. |
| `Service` | none — detected via `WindowsServiceHelpers.IsWindowsService()` (wrapped in try/catch) | Windows only | Treated identically to `Interactive` — falls through to `RunHostUntilShutdown`. The "Service" tag is consumed downstream by `RestartableServiceLifetime` (which detects `IsWindowsService` itself) and by `IRuntimeInfo.IsWindowsService`. |
| `Interactive` | (default) | any | `RunHostUntilShutdown` builds the full `IHostBuilder` and runs `host.Run()` in a do/while loop on `IRuntimeInfo.RestartPending`. |

The full set of CLI flags / args parsed by `StartupContext` (`src/NzbDrone.Common/EnvironmentInfo/StartupContext.cs:19-29`):

| Constant | Form | Effect |
|----------|------|--------|
| `APPDATA` | `data=<path>` | Override AppData folder (`AppFolderInfo` reads this). Also subtly changes single-instance policy: presence of `data=` flips it from `PreventStartIfAlreadyRunning` to `WarnIfAlreadyRunning` (`Startup.cs:391`). |
| `NO_BROWSER` | `nobrowser` | Suppresses the on-startup browser launch in `AppLifetime.OnAppStarted`. Also force-added by the updater so the post-update Sonarr does not spawn a browser (`StartNzbDrone.cs:78-81`). |
| `INSTALL_SERVICE` | `i` | Routes to `InstallService` mode. (Internal const — surfaced via `IStartupContext.InstallService`.) |
| `UNINSTALL_SERVICE` | `u` | Routes to `UninstallService` mode. |
| `HELP` | `?` | Routes to `Help` mode. |
| `TERMINATE` | `terminateexisting` | Causes `EnsureSingleInstance` to call `KillAllOtherInstance()` instead of `PreventStartIfAlreadyRunning()` (`Startup.cs:387-389`). |
| `RESTART` | `restart` | Reserved (the constant is exported but not consumed in the Host project — used by callers e.g. updater handoff). |
| `REGISTER_URL` | `registerurl` | Routes to `RegisterUrl` mode. |
| `NO_SINGLE_INSTANCE_CHECK` | `nosingleinstancecheck` | Skips `EnsureSingleInstance` entirely (`Startup.cs:382-385`). |
| `EXIT_IMMEDIATELY` | `exitimmediately` | When a `NonRecoverableFailure` is logged, `ConsoleApp.Exit` skips the 1-hour "press a key to exit" wait (`ConsoleApp.cs:108-113`). |

`StartupContext.PreservedArguments` reconstructs `/data=…` and `/nobrowser` only — these are the two args that survive an updater-driven re-launch (`StartupContext.cs:61-79`).

## Bootstrap flow (Main → "Sonarr is listening")

The path from `Main()` to a listening Kestrel, top-to-bottom:

1. **Process entry**.
   - Linux/macOS / Windows-console: `ConsoleApp.Main(args)` (`src/NzbDrone.Console/ConsoleApp.cs:27`).
     - `RuntimePatcher.Initialize()` (Sonarr.RuntimePatches — covered separately).
     - `var startupArgs = new StartupContext(args)`.
     - `NzbDroneLogger.Register(startupArgs, updateApp:false, inConsole:true)` — installs Sentry / file / console / auth-log NLog targets via `RegisterAppFile`, `RegisterConsole`, `RegisterAuthLogger`, etc.
     - `Bootstrap.Start(args)`.
   - Windows-tray: `WindowsApp.Main(args)` (`src/NzbDrone/WindowsApp.cs:17`).
     - `Application.EnableVisualStyles()`, `Application.SetHighDpiMode(HighDpiMode.SystemAware)`.
     - `NzbDroneLogger.Register(...)` (same call shape).
     - `Bootstrap.Start(args, trayCallback)` where `trayCallback` adds `SystemTrayApp` to the hosted-service collection.

2. **`Bootstrap.Start`** (`Bootstrap.cs:47-80`).
   - Logs `"Starting Sonarr - {processPath} - Version {asm.Version}"`.
   - Builds `var startupContext = new StartupContext(args)` again (separate instance from `ConsoleApp`'s — see HOST-Q1).
   - Registers code-page provider (`Encoding.RegisterProvider(CodePagesEncodingProvider.Instance)`).
   - `appMode = GetApplicationMode(startupContext)` — see "Application modes" above.
   - `config = GetConfiguration(startupContext)` — builds an `IConfiguration` from `<AppDataPath>/config.xml` (XML, optional, no reloadOnChange) plus an in-memory pair `dataProtectionFolder=<AppData>/asp/` plus environment variables (`Bootstrap.cs:256-275`). On `InvalidDataException` throws `InvalidConfigFileException` which the outer `try` re-wraps as `SonarrStartupException`.
   - If `appMode is not (Interactive or Service)` → `RunUtilityMode(...)` (one-shot host) and return.
   - Else → `RunHostUntilShutdown(args, startupContext, appMode, trayCallback)`.

3. **`RunUtilityMode`** (`Bootstrap.cs:82-116`). Builds a *minimal* `HostBuilder`:
   - `UseServiceProviderFactory(new DryIocServiceProviderFactory(new Container(rules => rules.WithNzbDroneRules())))`.
   - `ConfigureContainer<IContainer>` runs `c.AutoAddServices(ASSEMBLIES).AddNzbDroneLogger().AddDatabase().AddStartupContext(context)`, then resolves `UtilityModeRouter` and immediately calls `.Route(appMode)` — the side-effect happens during DI configuration.
   - `ConfigureServices` binds the typed-options classes (`PostgresOptions`, `AppOptions`, `AuthOptions`, `ServerOptions`, `LogOptions`, `UpdateOptions`) from `Sonarr:*` configuration sections.
   - `.Build()` is called but `host.Run()` is *not* — the dispatch already happened in `ConfigureContainer`. The `IHost` is discarded.

4. **`RunHostUntilShutdown`** (`Bootstrap.cs:118-138`). Loops:
   - Build host via `CreateConsoleHostBuilder(args, startupContext)`.
   - Apply `trayCallback` if present.
   - `RunWithRestartCheck(builder.Build())`.
   - If `IRuntimeInfo.RestartPending == true` → `NzbDroneLogger.ResetAllTargets(startupContext, false, true)`, sleep 1 s, loop. Otherwise exit.

5. **`CreateConsoleHostBuilder`** (`Bootstrap.cs:140-213`). The full build:
   - Reads server config: `BindAddress` (default `*`), `Port` (default `8989`), `SslPort` (default `9898`), `EnableSsl`, `SslCertPath`, `SslKeyPath`, `SslCertPassword`, `LogDbEnabled` (default `true`). Each is sourced from `Sonarr:Server:*` first, then falls back to the legacy flat `ConfigFileProvider.*` keys (e.g. `BindAddress`).
   - Builds `urls`: always one HTTP URL, plus an HTTPS URL if SSL is enabled and a cert path is set.
   - `new HostBuilder()` chain:
     - `.UseContentRoot(Directory.GetCurrentDirectory())`.
     - `.UseServiceProviderFactory(new DryIocServiceProviderFactory(new Container(rules => rules.WithNzbDroneRules())))` — DryIoc as the DI container, configured with `WithMicrosoftDependencyInjectionRules`, `WithAutoConcreteTypeResolution`, `WithDefaultReuse(Reuse.Singleton)` (`src/NzbDrone.Common/Composition/Extensions.cs:10-15`).
     - `.ConfigureLogging` — silence `Microsoft.AspNetCore.Diagnostics.ExceptionHandlerMiddleware`.
     - `.ConfigureContainer<IContainer>(c => c.AutoAddServices(ASSEMBLIES).AddNzbDroneLogger().AddDatabase().AddStartupContext(context))` plus `c.AddLogDatabase()` or `c.AddDummyLogDatabase()` depending on `LogDbEnabled`.
     - `.ConfigureServices(services => services.Configure<*Options>(...))` — typed-options bindings (same six as utility mode).
     - `.ConfigureWebHost(builder => …)` — see step 6.

6. **`ConfigureWebHost`** (`Bootstrap.cs:192-212`):
   - `builder.UseConfiguration(config)`.
   - `builder.UseUrls(urls.ToArray())`.
   - `builder.UseKestrel(options => …)` — only sets `ServerCertificate` via `ConfigureHttpsDefaults` if SSL on.
   - `builder.ConfigureKestrel(serverOptions => { AllowSynchronousIO = false; Limits.MaxRequestBodySize = null; })`.
   - `builder.UseStartup<Startup>()` — the `Startup` class.

7. **`Startup.ConfigureServices`** (`Startup.cs:54-285`). Wires:
   - Logging: clears providers, sets minimum `Trace`, filters `Microsoft.AspNetCore` to `Warning`, `Sonarr.Http.Authentication.ApiKeyAuthenticationHandler` to `Information`, `Microsoft.AspNetCore.DataProtection.KeyManagement.XmlKeyManager` to `Error`, then `AddNLog()`.
   - `ForwardedHeadersOptions`: `XForwardedFor | XForwardedProto | XForwardedHost`. Trusts RFC 1918 + ULA + link-local networks: `10/8`, `172.16/12`, `192.168/16`, `fc00::/7`, `fe80::/10`. (Cross-cite `authentication-security.md` SEC-Q3.)
   - `AddRouting(o.LowercaseUrls = true)`, `AddResponseCompression()`.
   - `AddCors` — two policies: `VersionedApiControllerAttribute.API_CORS_POLICY` (`AllowAnyOrigin/Method/Header`), and `"AllowGet"` (`AllowAnyOrigin`, methods `GET, OPTIONS`, any header).
   - `AddControllers(o.ReturnHttpNotAcceptable = true)` with three application parts: `SystemController.Assembly` (Sonarr.Api.V3), `SeriesLookupController.Assembly` (Sonarr.Api.V5), `StaticResourceController.Assembly` (Sonarr.Http). `AddJsonOptions(STJson.ApplySerializerSettings)`. `AddControllersAsServices()` — controllers resolved through DryIoc.
   - `ConfigureHttpJsonOptions(STJson.ApplySerializerSettings)` (for minimal-API JSON).
   - `AddSwaggerGen(...)` — two doc names `v3` and `v5`, two API-key security schemes (`X-Api-Key` header, `apikey` query). `DocInclusionPredicate` looks at `[VersionedApiController]` / `[VersionedFeedController]` attributes on the controller type.
   - `AddSignalR().AddJsonProtocol(o.PayloadSerializerOptions = STJson.GetSerializerSettings())`.
   - `AddDataProtection().PersistKeysToFileSystem(<dataProtectionFolder>)` where the path comes from the in-memory config kvp set up in `GetConfiguration` (`<AppData>/asp/`).
   - `AddSingleton<IAuthorizationPolicyProvider, UiAuthorizationPolicyProvider>()`, `AddSingleton<IAuthorizationHandler, UiAuthorizationHandler>()`.
   - `AddAuthorization(...)`: `"SignalR"` policy (scheme = `SignalR`, requires authenticated user); fallback policy = scheme `"API"` + `RequireAuthenticatedUser` (so every endpoint without `[AllowAnonymous]` is auth-gated). (Cross-cite `authentication-security.md`.)
   - `services.AddAppAuthentication()` — defined in Sonarr.Http (out-of-scope for this card; see `authentication-security.md`).
   - `AddOptions<MiniProfilerOptions>().Configure<IConfigFileProvider>(...)`. Routes `/profiler`, picks colour scheme + popup position from `IConfigFileProvider.Theme` / `ProfilerPosition`, ignores `/MediaCover`.
   - `AddMiniProfiler()`.

8. **`Startup.Configure`** (`Startup.cs:287-378`). The constructor receives a long list of DI dependencies — the *order of side effects in the body* is the critical contract:
   1. `initializeLogger.Initialize()` — finalizes NLog config (Core/Instrumentation).
   2. `appFolderFactory.Register()` — creates AppData folder, sets per-OS permissions (cross-cite `native-platform.md`).
   3. `pidFileProvider.Write()` — writes `Sonarr.pid` to the AppData folder.
   4. `configFileProvider.EnsureDefaultConfigFile()` — creates / migrates `config.xml`.
   5. `reconfigureLogging.Reconfigure()` — applies `LogLevel` from config to the NLog rules.
   6. `EnsureSingleInstance(false, startupContext, singleInstancePolicy)` — see `SingleInstancePolicy` section. (Always called with `isService = false`, even when running as Windows Service — see HOST-Q2.)
   7. `_ = mainDatabaseFactory.Value` — eagerly forces `IMainDatabase` resolution, which runs FluentMigrator migrations.
   8. If `LogDbEnabled` → `_ = logDatabaseFactory.Value` + `dbTarget.Register()` (NLog → log-database target). Otherwise the `DummyLogDatabase` registered in step 5.
   9. `SchemaBuilder.Initialize(container)` — registers Dapper type handlers from the container.
   10. On non-Windows: `Console.CancelKeyPress += (...) => NLog.LogManager.Configuration = null;` — Ctrl+C = drop logging.
   11. `eventAggregator.PublishEvent(new ApplicationStartingEvent())` — first lifecycle event broadcast (Cross-cite `messaging.md`, `system.md`).
   12. If `OsInfo.IsWindows && runtimeInfo.IsAdmin` → `firewallAdapter.MakeAccessible()` — opens TCP `Port` (and `SslPort` if SSL on) via Windows COM `HNetCfg.FwMgr`.

   Then the middleware pipeline (`Startup.cs:337-377`), in order:
   1. `app.UseForwardedHeaders()` — must be first to propagate proxy headers.
   2. `app.UseMiddleware<LoggingMiddleware>()` — Sonarr.Http (cross-cite `sonarr-http.md`).
   3. `app.UsePathBase(new PathString(configFileProvider.UrlBase))` — supports running under a reverse-proxy sub-path.
   4. `app.UseExceptionHandler({ AllowStatusCode404Response = true, ExceptionHandler = errorHandler.HandleException })` — `SonarrErrorPipeline.HandleException` (Sonarr.Http).
   5. `app.UseRouting()`.
   6. `app.UseCors()`.
   7. `app.UseAuthentication()`.
   8. `app.UseAuthorization()`.
   9. `app.UseResponseCompression()`.
   10. `app.Properties["host.AppName"] = BuildInfo.AppName`.
   11. `app.UseMiddleware<VersionMiddleware>()`, `<UrlBaseMiddleware>(configFileProvider.UrlBase)`, `<StartingUpMiddleware>`, `<CacheHeaderMiddleware>`, `<IfModifiedMiddleware>`, `<BufferingMiddleware>(["/api/v3/command", "/api/v5/command"])`. (All defined in `Sonarr.Http` — see that card.)
   12. `app.UseWebSockets()`, `app.UseMiniProfiler()`.
   13. If `BuildInfo.IsDebug` → `app.UseSwagger(c.RouteTemplate = "docs/{documentName}/openapi.json")` — Swagger JSON only in debug builds (HOST-Q3).
   14. `app.UseEndpoints(...)`: `MapHub<MessageHub>("/signalr/messages").RequireAuthorization("SignalR")`, `MapPost("/profiler/results", ...).RequireAuthorization("UI")`, `MapControllers()`.

   At this point Kestrel has its endpoints, ASP.NET hands the host back to `host.Run()`.

9. **`AppLifetime` startup callback**. Constructor (`AppLifetime.cs:23-42`) registers two callbacks:
   - `_appLifetime.ApplicationStarted.Register(OnAppStarted)` — fires after Kestrel begins listening. Sets `IRuntimeInfo.IsStarting = false`, opens the browser if `LaunchBrowser && !nobrowser`, then publishes `ApplicationStartedEvent` (the second lifecycle event — many subsystems gate on it; e.g. `TaskManager` reconciles default jobs, see `system.md`).
   - `_appLifetime.ApplicationStopped.Register(OnAppStopped)` — logs whether `RestartPending` is set.

   `AppLifetime` is registered as `IHostedService` by the assembly scan, so `StartAsync` runs (returns `Task.CompletedTask`), but the real wiring is the constructor lambdas.

## DI container (DryIoc)

DryIoc 5.4.3 with `DryIoc.Microsoft.DependencyInjection` 6.2.0 (`src/NzbDrone.Host/Sonarr.Host.csproj:11-12`). Earlier card `localization-validation.md` (VAL-Q1 resolution) confirmed the choice; `WithNzbDroneRules` is the central rule-set.

Container build flow:
1. `new Container(rules => rules.WithNzbDroneRules())` — `Reuse.Singleton` is the *default* lifetime, MS-DI rules are layered on, `WithAutoConcreteTypeResolution` lets the container construct concrete classes that aren't explicitly registered (`src/NzbDrone.Common/Composition/Extensions.cs:10-15`).
2. `c.AutoAddServices(ASSEMBLIES)` — `Bootstrap.ASSEMBLIES` is a hard-coded list (`Bootstrap.cs:37-45`):
   - `Sonarr.Host`, `Sonarr.Core`, `Sonarr.SignalR`, `Sonarr.Api.V3`, `Sonarr.Api.V5`, `Sonarr.Http`.
   - `AssemblyLoader.Load(...)` (`src/NzbDrone.Common/Composition/AssemblyLoader.cs:19-34`) appends `Sonarr.Common` and OS-specific (`Sonarr.Windows` on Windows, else `Sonarr.Mono`). Also registers a native-DLL resolver for `System.Data.SQLite` + `Sonarr.Core` so `libsqlite3.so.0` resolves on Linux (`AssemblyLoader.cs:49-90`).
   - Two scanning passes (`Composition/Extensions.cs:23-39`):
     - `RegisterMany(assemblies, type => type.IsInterface && !type.FullName.StartsWith("System"), Reuse.Singleton)` — every public interface across the loaded assemblies is registered with the singleton lifetime.
     - `RegisterMany(assemblies, type => !type.IsInterface && !type.FullName.StartsWith("System"), Reuse.Transient)` — every concrete class is registered transient (DryIoc reconciles to the singleton instance when an interface lookup hits because of MS-DI rules).
   - Constructs and registers a `KnownTypes` instance carrying every type the assemblies expose (used by `Composition/KnownTypes.cs` for runtime `IEnumerable<T>` resolution of plugin lists — indexers, download clients, etc.).
3. `.AddNzbDroneLogger()` (`src/NzbDrone.Common/Instrumentation/Extensions/CompositionExtensions.cs:8`) — registers the `NLog.Logger` factory delegate so `Logger` constructor parameters are resolved per-consumer.
4. `.AddDatabase()` (`src/NzbDrone.Core/Datastore/Extensions/CompositionExtensions.cs:8`) — `RegisterDelegate<IDbFactory, IMainDatabase>(f => new MainDatabase(f.Create()), Reuse.Singleton)`. Resolution forces FluentMigrator on first access.
5. `.AddStartupContext(context)` (`src/NzbDrone.Common/Composition/Extensions.cs:17`) — `RegisterInstance<IStartupContext>(context, ifAlreadyRegistered: Replace)`. Replace because the assembly scan already registered the type.
6. `c.AddLogDatabase()` *or* `c.AddDummyLogDatabase()` depending on `LogDbEnabled` (`Datastore/Extensions/CompositionExtensions.cs:15-34`).

The categories of registration (NOT a per-type list — that is delegated to the per-subsystem cards):

| Category | Registered by | Where to find detail |
|----------|---------------|-----------------------|
| Interfaces → singleton concretes | `AutoAddServices` first pass | every other subsystem card |
| Concretes (transient binding for DryIoc; effectively-singleton via MS-DI) | `AutoAddServices` second pass | every other subsystem card |
| `IMainDatabase` / `ILogDatabase` | `AddDatabase` / `AddLogDatabase` | `datastore.md` |
| `IStartupContext` | `AddStartupContext` | this card |
| `Logger` (NLog) | `AddNzbDroneLogger` | `common.md` (NzbDroneLogger) |
| `IEventAggregator`, `IManageCommandQueue`, `Lifecycle` events | covered by `AutoAddServices` (in `Sonarr.Core`) | `messaging.md`, `system.md` |
| Controllers (`AddControllersAsServices`) | `Startup.ConfigureServices` | `sonarr-http.md`, REST inventory |
| `MessageHub` SignalR hub | `AutoAddServices` + `MapHub` in `Configure` | `signalr/inventory.md` |
| `IHostedService AppLifetime` | `AutoAddServices` (implements `IHostedService`) | this card |
| `IHostedService SystemTrayApp` (Windows-tray only) | `WindowsApp.Main` adding via `ConfigureServices` callback | this card |
| Typed options (`PostgresOptions`, `AppOptions`, `AuthOptions`, `ServerOptions`, `LogOptions`, `UpdateOptions`) | `Bootstrap.ConfigureServices` | `configuration.md` |
| `IAuthorizationPolicyProvider`, `IAuthorizationHandler`, AppAuthentication | `Startup.ConfigureServices` + `AddAppAuthentication` | `authentication-security.md` |
| `MiniProfilerOptions` | `Startup.ConfigureServices` | `http-instrumentation-misc.md` |
| Forwarded-headers options | `Startup.ConfigureServices` | this card + `authentication-security.md` |

## `SingleInstancePolicy`

`SingleInstancePolicy` (`SingleInstancePolicy.cs`) detects another running instance by *process name only* — not lockfile, not port-bind probe, not OS mutex.

- `GetOtherNzbDroneProcessIds()` (`SingleInstancePolicy.cs:62-86`) calls `IProcessProvider.FindProcessByName(SONARR_CONSOLE_PROCESS_NAME)` (`"Sonarr.Console"`) `.Union(...FindProcessByName(SONARR_PROCESS_NAME))` (`"Sonarr"`), removes the current PID, returns the rest.
- The dispatch in `Startup.EnsureSingleInstance` (`Startup.cs:380-403`):
  - If `nosingleinstancecheck` flag → return immediately.
  - If `terminateexisting` flag → `KillAllOtherInstance()` (loop `IProcessProvider.Kill(pid)` for each found PID).
  - Else if `data=` arg present → `WarnIfAlreadyRunning()` (logs only, does not abort). The presence of an explicit data path is treated as "the user is running multiple disjoint instances on purpose".
  - Else if running as Windows service (the `isService` parameter — but it is hard-coded `false` at the call site!) → `KillAllOtherInstance()`.
  - Else → `PreventStartIfAlreadyRunning()` (`SingleInstancePolicy.cs:31-39`): if any other PID exists, log warn, *open the browser to the running instance*, throw `TerminateApplicationException`. The throw bubbles up through `Bootstrap.Start`'s top-level catch and exits cleanly with no error code (because `LogManager.Configuration` is set to `null`).
- The "open browser to whichever Sonarr is already running" behaviour is intentional UX for double-clicking the tray icon a second time on Windows.

## `AppLifetime` and shutdown flow

`AppLifetime` (`AppLifetime.cs:13`) is two distinct surfaces glued together:

1. As `IHostedService`: trivial `StartAsync` / `StopAsync` returning `Task.CompletedTask`. Only present so DI instantiates it during host start.
2. As `IHandle<ApplicationShutdownRequested>`: bridges Sonarr's domain event (raised by `ILifecycleService.Shutdown` / `Restart` — see `system.md`) into ASP.NET's `IHostApplicationLifetime.StopApplication()`.

Lifecycle event flow:

| Producer | Event | Consumer | Action |
|----------|-------|----------|--------|
| `Startup.Configure` (line 330) | `ApplicationStartingEvent` | many handlers across Core | bring subsystems online |
| `IHostApplicationLifetime.ApplicationStarted` callback | `ApplicationStartedEvent` (raised inside `AppLifetime.OnAppStarted`, `AppLifetime.cs:65`) | `TaskManager`, others | seed scheduled jobs etc. |
| `ILifecycleService.Shutdown()` / `Restart()` | `ApplicationShutdownRequested(restarting)` | `AppLifetime.Handle` | sets `IRuntimeInfo.RestartPending = true` if restart, calls `_appLifetime.StopApplication()` |
| `IHostApplicationLifetime.ApplicationStopped` | (no event) | `AppLifetime.OnAppStopped` (logs only) and `Bootstrap.RunWithRestartCheck` (`Bootstrap.cs:325-330`, reads `IRuntimeInfo.RestartPending` to decide loop continuation) | restart-loop control |

`AppLifetime.Handle(ApplicationShutdownRequested)` (`AppLifetime.cs:88-102`) does:
- if `message.Restarting` → set `_runtimeInfo.RestartPending = true`, log debug.
- else → `LogManager.Configuration = null` (silences NLog so further log calls are no-ops during teardown).
- then `Shutdown()` → `_runtimeInfo.IsExiting = true; _appLifetime.StopApplication()`.

`StopApplication` triggers the host's stop sequence: ASP.NET cancels `CancellationToken`s, `IHostedService.StopAsync` runs in reverse-registration order, Kestrel stops accepting connections, DryIoc disposes singletons (including `IMainDatabase`/`ILogDatabase` which close their connection pools). The teardown ordering of Sonarr's own services is governed by registration order — see HOST-Q4.

There is **no explicit "save state" step** before shutdown — the design is "every domain mutation is already persisted; commands in the in-memory queue are lost; SignalR connections drop". `MessagingCleanupCommand` (every 5 min, see `system.md`) is the only state-trim action.

On Linux/macOS the additional Ctrl+C handler set in `Startup.Configure` (`Startup.cs:325-328`) sets `LogManager.Configuration = null` but does *not* call `StopApplication` — it relies on .NET's default `ConsoleLifetime` to do that.

## `RestartableServiceLifetime`

`RestartableServiceLifetime` (`RestartableServiceLifetime.cs:15`) is a custom `IHostLifetime` registered to allow Sonarr to restart in-process while running as a Windows Service. Stock `WindowsServiceLifetime` would not allow `host.Run` to be called twice in one process because `ServiceBase.Run` can only be called once.

Mechanics:
- Two static fields (`StaticLock`, `_singletonService`, `_serviceBaseRunCalled`) shared across host instances of the same process.
- On non-Windows-Service runs (`!_isWindowsService`), it just delegates everything to the stock `ConsoleLifetime` (`RestartableServiceLifetime.cs:50-53`).
- On Windows Service:
  - `WaitForStartAsync` lazily creates a single `WindowsServiceWrapper : ServiceBase` and starts a single background thread running `ServiceBase.Run(self)` — only once per process. Subsequent calls just re-aim the wrapper at the new `IHostApplicationLifetime` via `PrepareForRestart` (which calls `CleanupForRestart` on the previous lifetime, then resets `_hostStoppedEvent` and completes the new `_waitForStartTask`).
  - `OnStart` / `OnStop` / `OnShutdown` overrides on the wrapper translate Windows SCM messages into completing the start TCS / calling `ApplicationLifetime.StopApplication()`.
  - `StopAsync` waits on `_hostStoppedEvent` (set when the current `ApplicationLifetime.ApplicationStopped` callback fires), so the host's stop returns only after the stop has actually happened.

In effect: "restart" on a Windows-Service install = the same Windows service stays running; `Bootstrap.RunHostUntilShutdown`'s do-while just builds a *new* DI container + Kestrel + middleware pipeline and the wrapper is re-pointed at the new lifetime. The OS service never stops.

Distinction from `AppLifetime`: `RestartableServiceLifetime` is the OS↔host lifetime bridge (low level); `AppLifetime` is the host↔domain-event bridge (high level). They cooperate but live at different layers.

## `BrowserService`

`BrowserService` (`BrowserService.cs:14`) opens `http://localhost:{Port}` (always plain HTTP, never HTTPS, ignores `UrlBase` — see HOST-Q5) via `IProcessProvider.OpenDefaultBrowser(url)`, which delegates to the OS-specific implementation in `Sonarr.Mono` / `Sonarr.Windows` (cross-cite `native-platform.md`):
- Windows: `Process.Start(new ProcessStartInfo(url) { UseShellExecute = true })`.
- Linux/macOS: spawns `xdg-open` / `open` (see `Sonarr.Mono`).

Skipped if `_runtimeInfo.IsUserInteractive` is false (so it never runs from a Windows service or systemd unit). Also skipped if `nobrowser` flag set or `LaunchBrowser` config is false (the gate is in `AppLifetime.OnAppStarted`, not in `BrowserService`).

`BrowserService` is also called from `SingleInstancePolicy.PreventStartIfAlreadyRunning` (focus the existing instance) and from `SystemTrayApp`'s "Launch Browser" menu item.

## `UtilityModeRouter` + `AccessControl/`

`UtilityModeRouter.Route(ApplicationModes)` (`UtilityModeRouter.cs:39-96`) is the dispatcher for one-shot CLI actions — see "Application modes" table above.

The Windows-only sub-flows:
- **InstallService**: `RemoteAccessAdapter.MakeAccessible(passive=true)` → if admin, `FirewallAdapter.MakeAccessible()` opens TCP `Port` (and `SslPort` if SSL on) using COM `HNetCfg.FwMgr` / `HNetCfg.FWOpenPort`. (`FirewallAdapter.cs:65-87`.) Then `IServiceProvider.Install("Sonarr")` (Windows SCM install) and `SetPermissions`. Finally a synchronous `sc.exe start Sonarr`.
- **UninstallService**: just `IServiceProvider.Uninstall("Sonarr")`.
- **RegisterUrl**: `RemoteAccessAdapter.MakeAccessible(passive=false)` (so non-admin throws `RemoteAccessException`) plus `IAppFolderFactory.SetPermissions()` (chmod / ACL on AppData).
- **Help / default**: `IConsoleService.PrintHelp()`.

`AccessControl/` is *purely* Windows firewall management. There is **no URL-ACL / `netsh http add urlacl` flow in this directory** despite the `RegisterUrl` mode name — the URL-ACL was needed in earlier Windows + HttpListener days but Kestrel does not need it. Today `RegisterUrl` only opens the firewall. (HOST-Q6.)

`RemoteAccessException` is caught specially by `ConsoleApp.Main` (`ConsoleApp.cs:75-81`) and exits with `ExitCodes.Normal` (0) rather than a failure — because failing to register the URL is treated as a warning, not a fatal error.

## `TerminateApplicationException`

`TerminateApplicationException` (`TerminateApplicationException.cs:5`) is a subclass of `ApplicationException` whose only purpose is to short-circuit `Bootstrap.Start` cleanly. There is exactly one thrower (`SingleInstancePolicy.PreventStartIfAlreadyRunning`, `SingleInstancePolicy.cs:37`) and exactly one catcher (`Bootstrap.Start`, `Bootstrap.cs:75`):

```csharp
catch (TerminateApplicationException e)
{
    Logger.Info(e.Message);
    LogManager.Configuration = null;
}
```

The `LogManager.Configuration = null` line silences NLog so the rest of the process exit produces no further log noise.

## Hosting model

- **`HostBuilder` (Generic Host), not `WebHostBuilder`**: `Bootstrap.cs:160` uses `new HostBuilder()` with `.ConfigureWebHost(...)` to splice in Kestrel. The Generic Host owns the lifetime; `WebHost` is just one of many hosted services.
- **Kestrel directly** — no IIS, no HTTP.sys. SSL termination via Kestrel's `ConfigureHttpsDefaults` if a cert is configured. The cert can be PEM (`X509ContentType.Cert` → `CreateFromPemFile(cert, key)`) or PKCS#12 (`X509ContentType.Pkcs12` → `LoadPkcs12FromFile(cert, password)`) — `Bootstrap.cs:282-319`.
- **No OWIN bridge.**
- **DI provider is DryIoc**, not the stock MS DI — installed via `DryIocServiceProviderFactory(Container)`. MS-DI extension methods (`AddControllers`, `AddCors`, etc.) work because `WithMicrosoftDependencyInjectionRules` translates them.

## Reverse-proxy / X-Forwarded-* handling

`Startup.ConfigureServices` (lines 66-74) sets `ForwardedHeadersOptions`:
- `ForwardedHeaders.XForwardedFor | XForwardedProto | XForwardedHost`.
- `KnownIPNetworks` populated with all RFC 1918 + ULA + link-local ranges (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`, `fe80::/10`).
- Loopback (`127.0.0.0/8`, `::1/128`) is *not* explicitly added but ASP.NET's defaults trust `localhost` already.

`app.UseForwardedHeaders()` runs first in the middleware pipeline. There is no `KnownProxies` configured (only ranges), no `ForwardLimit` change (default 1), no `RequireHeaderSymmetry` (default false). Cross-cite `authentication-security.md` SEC-Q3 for the security implications of trusting all RFC 1918 ranges blindly.

## Console mode vs Service mode

Decision made by `Bootstrap.GetApplicationMode`:
- `OsInfo.IsWindows && WindowsServiceHelpers.IsWindowsService()` → `ApplicationModes.Service`.
- Otherwise → `ApplicationModes.Interactive`.

Both modes converge on the same `RunHostUntilShutdown` path. Differences emerge inside the host:
- `RestartableServiceLifetime.WaitForStartAsync` checks `WindowsServiceHelpers.IsWindowsService()` again and switches between `ConsoleLifetime` and the `WindowsServiceWrapper` `ServiceBase`.
- `IRuntimeInfo.IsWindowsService` is determined by `hostLifetime is WindowsServiceLifetime` in `RuntimeInfo`'s ctor (`RuntimeInfo.cs:23`) — note: this is *the stock* `WindowsServiceLifetime` type, *not* `RestartableServiceLifetime`. This means `IRuntimeInfo.IsWindowsService` is currently always `false` because the stock type is never registered. (HOST-Q7.)

Service install / uninstall happens in `UtilityModeRouter` only (Windows SCM via `ServiceProvider`).

systemd assumptions:
- `IRuntimeInfo.IsSystemdService` returns `true` if `OsInfo.IsLinux && Environment.GetEnvironmentVariable("INVOCATION_ID") is not null` (`RuntimeInfo.cs:90-110`). This is the standard systemd marker.
- The systemd unit file is *not* in this tree — it ships with the package distros (Cross-cite `native-platform.md` if covered there). The Host code does not register a `systemd` host lifetime — Sonarr under systemd uses the same `ConsoleLifetime` as a console run, which means systemd's `Type=simple` (foreground) is the expected unit type.

## Events raised / consumed

| Event | Source | Consumer |
|-------|--------|----------|
| `ApplicationStartingEvent` | `Startup.Configure:330` (after DBs + appfolder + pid + reconfigure-logging) | many subsystems (cross-cite `system.md`, `messaging.md`) |
| `ApplicationStartedEvent` | `AppLifetime.OnAppStarted:65` (after Kestrel listening) | `TaskManager` (seed default jobs, `system.md` §1) and others |
| `ApplicationShutdownRequested` | `LifecycleService.Shutdown/Restart` (`Lifecycle/LifecycleService.cs:33,40`) | `AppLifetime.Handle:88` |

## Config keys

- `Sonarr:Server:BindAddress` / legacy `BindAddress` (default `*`).
- `Sonarr:Server:Port` / legacy `Port` (default `8989`).
- `Sonarr:Server:SslPort` / legacy `SslPort` (default `9898`).
- `Sonarr:Server:EnableSsl` / legacy `EnableSsl` (default `false`).
- `Sonarr:Server:SslCertPath` / legacy `SslCertPath` (no default; required if SSL on).
- `Sonarr:Server:SslKeyPath` / legacy `SslKeyPath` (PEM only).
- `Sonarr:Server:SslCertPassword` / legacy `SslCertPassword` (PKCS#12 only).
- `Sonarr:Log:DbEnabled` / legacy `LogDbEnabled` (default `true`).
- `dataProtectionFolder` (synthesized in `Bootstrap.GetConfiguration:265` — not user-facing).
- `Sonarr:App`, `Sonarr:Auth`, `Sonarr:Postgres`, `Sonarr:Update` — typed-options sections bound but consumed in other subsystems (`configuration.md`).
- `IConfigFileProvider.UrlBase` — read in `Startup.Configure:339`.
- `IConfigFileProvider.Theme`, `ProfilerPosition` — read in `Startup.ConfigureServices:252-279`.
- `IConfigFileProvider.LaunchBrowser` — read in `AppLifetime.OnAppStarted:60`.

## Notable quirks / TODOs / HACKs

- `Bootstrap.cs:55` parses `StartupContext` from `args` *again* even though `ConsoleApp.Main` already did so — see HOST-Q1.
- `Bootstrap.cs:142` rebuilds the `IConfiguration` inside `CreateConsoleHostBuilder` even though `Start` already built one — re-parse cost on every restart loop iteration.
- `Bootstrap.cs:208` sets `serverOptions.Limits.MaxRequestBodySize = null` (uncapped). Anything posting to `/api/*` can technically POST a multi-GB body. Consider whether the Go port should reinstate a sensible cap (e.g. 512 MB).
- `Startup.cs:312` calls `EnsureSingleInstance(false, ...)` with a hard-coded `isService = false`, even when the host is in `ApplicationModes.Service` — looks like a bug-by-omission. The dispatcher inside `EnsureSingleInstance` therefore never hits its `isService` branch (HOST-Q2).
- `Startup.cs:325-328` registers `Console.CancelKeyPress` only on non-Windows. Windows relies on `RestartableServiceLifetime` / stock `ConsoleLifetime`'s native handling.
- `Startup.cs:364-370` only mounts Swagger JSON in Debug builds. Production builds have no `/docs` endpoint — the `swagger.json` consumed by the Sonarr docs site is generated during the build, not served at runtime.
- `Bootstrap.cs:208` `AllowSynchronousIO = false` is the right Kestrel setting but means any unported Newtonsoft path that reads the body synchronously will throw at runtime.
- `RestartableServiceLifetime` keeps three static fields per process (`StaticLock`, `_singletonService`, `_serviceBaseRunCalled`) — do not run two hosts simultaneously in one process or they fight for the ServiceBase thread.
- `SingleInstancePolicy` does process-name detection only — running Sonarr from a renamed binary defeats it. There is no port-bind or lockfile fallback. Cross-cite `common.md` (`PidFileProvider`) — pid file is written but never consulted by the policy.
- `BrowserService.LaunchWebUI` always uses `http://localhost:{Port}`, never honours `UrlBase` or `EnableSsl` (HOST-Q5).
- `FirewallAdapter` opens *only* the standard Windows firewall profile (`NET_FW_PROFILE_STANDARD`) — domain and public profiles untouched. May be intentional but is undocumented.
- `Bootstrap.cs:79` swallows `TerminateApplicationException` silently — the message is logged at `Info` and `LogManager.Configuration` is dropped. The exit code ends up `0` (Normal).
- The `ASSEMBLIES` list in `Bootstrap.cs:37-45` is hard-coded. Adding a new assembly is a Bootstrap edit. See HOST-Q8.
- `RuntimeInfo.IsWindowsService` (and therefore `RuntimeMode.Service`) is computed against the stock `WindowsServiceLifetime` type, but Sonarr registers `RestartableServiceLifetime` instead — so `RuntimeInfo.IsWindowsService` likely returns `false` always under Sonarr's actual service install (HOST-Q7).

## Test files (the port must keep equivalents passing)

- `src/NzbDrone.Host.Test/AppLifetimeTests.cs`
- `src/NzbDrone.Host.Test/SingleInstancePolicyTests.cs` (plus other tests under `src/NzbDrone.Host.Test/`)
- `src/NzbDrone.Host.Test/ContainerFixture.cs` — verifies `AutoAddServices` produces a resolvable graph for every registered interface.
- `src/NzbDrone.Common.Test/ServiceFactoryFixture.cs` — verifies the same graph minus `AddDatabase`.

## Open questions

- **HOST-Q1**: `ConsoleApp.Main` constructs a `StartupContext(args)` for `NzbDroneLogger.Register`, then `Bootstrap.Start` constructs *another* `StartupContext(args)` (`Bootstrap.cs:55`). They are independent instances; any mutation to one is invisible to the other. Should the port make `StartupContext` a single per-process value resolved through DI from the very start?
- **HOST-Q2**: `Startup.EnsureSingleInstance` is called with `isService=false` hard-coded (`Startup.cs:312`) even when the host is in `ApplicationModes.Service`. The intended Windows-Service-mode behaviour was probably to call `KillAllOtherInstance()`, but currently a service install falls into `PreventStartIfAlreadyRunning` (or `WarnIfAlreadyRunning` if `data=` is set) instead. Bug or intentional? The Go port should resolve this before re-implementing.
- **HOST-Q3**: Swagger JSON is only mounted in Debug builds (`Startup.cs:364`). Production users of `/docs/v3/openapi.json` would 404. Confirm the docs site consumes a build-time-generated artifact, not the runtime endpoint, before deciding whether the Go port's OpenAPI handler should be debug-only.
- **HOST-Q4**: Shutdown ordering of Sonarr's own services is "whatever DryIoc decides" (registration order). There is no explicit `IHostedService.StopAsync` ordering, no "drain queue first, then close DB" sequence. Under load, in-flight commands may be aborted mid-DB-write. Is this acceptable for the Go port, or should it introduce explicit teardown phases?
- **HOST-Q5**: `BrowserService.LaunchWebUI` hard-codes `http://localhost:{Port}` (`BrowserService.cs:31`) — ignores `UrlBase`, ignores `EnableSsl`. If the user has SSL only with no HTTP listener, the launched browser hits a closed port. Honour `UrlBase` + protocol in the Go port.
- **HOST-Q6**: `AccessControl/` only configures the Windows firewall — there is no URL-ACL / `netsh http add urlacl` flow despite `ApplicationModes.RegisterUrl`'s name. Confirm the URL-ACL was dropped when Kestrel replaced HttpListener (so `RegisterUrl` is a misnomer) and document accordingly.
- **HOST-Q7**: `RuntimeInfo.IsWindowsService` is `hostLifetime is WindowsServiceLifetime` — but Sonarr registers `RestartableServiceLifetime` instead. If true, `IRuntimeInfo.IsWindowsService` is permanently `false` under Sonarr's actual service install. Either Sonarr is using the stock lifetime in some path I missed, or this is a quiet bug. Verify and decide whether the Go port should fix it.
- **HOST-Q8**: `Bootstrap.ASSEMBLIES` is hard-coded (`Bootstrap.cs:37-45`). The Go port plans to drop assembly scanning entirely (per `CONVERSION.md`), so this becomes a non-issue — but confirm no plugin / extension story is being broken.
- **HOST-Q9**: `WithAutoConcreteTypeResolution` lets DryIoc construct any reachable concrete type without explicit registration. The Go port (no DI container) needs an exhaustive list of every type that is *actually* resolved this way. Recommend a one-time DryIoc trace pass to enumerate.
- **HOST-Q10**: The host has no first-class graceful-shutdown timeout. `IHost.StopAsync` has a 30 s default, but Sonarr does not configure `HostOptions.ShutdownTimeout`. Long-running commands (e.g. an in-progress import scan) can exceed it and get cut off. Decide on a Go-port default.
- **HOST-Q11**: `nobrowser` flag is mutated mid-process by the updater (`StartNzbDrone.cs:78-81` adds it to `IStartupContext.Flags` so the spawned Sonarr does not open the browser). The flag is `HashSet<string>` and is *mutable* — this is the only mutation in the codebase. The Go port should decide whether `StartupContext` is immutable or whether `OpenBrowser`-suppression is communicated some other way.
- **HOST-Q12**: `dataProtectionFolder` is a synthesized in-memory key in `IConfiguration` (`Bootstrap.cs:265`). ASP.NET's `AddDataProtection().PersistKeysToFileSystem(...)` reads it. The Go port has no equivalent of `Microsoft.AspNetCore.DataProtection` keys (used for cookie / antiforgery / form-encryption), so this entire flow needs an alternative — see `authentication-security.md` for cookie storage approach.
