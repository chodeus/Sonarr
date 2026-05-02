# Configuration (settings, autotagging, tags, root folders, remote path mappings, disk space)

**Source:** `src/NzbDrone.Core/{Configuration,AutoTagging,Tags,RootFolders,RemotePathMappings,DiskSpace}/**`, plus `src/NzbDrone.Common/Options/**`, `src/NzbDrone.Core/Datastore/PostgresOptions.cs`, `src/NzbDrone.Mono/Disk/**`, `src/NzbDrone.Windows/Disk/**`, and the V3/V5 controllers under `src/Sonarr.Api.V3/{Config,Tags,RootFolders,RemotePathMappings,AutoTagging,DiskSpace}/**` and `src/Sonarr.Api.V5/{Settings,Tags,RootFolders,RemotePathMappings,DiskSpace}/**`.
**Target Go package(s):** `go/internal/config/...` (one sub-package per area)
**Status:** drafted

This card consolidates six small-but-load-bearing subsystems that together form Sonarr's static configuration plane:

1. **Configuration** — every persisted "setting" the rest of the app reads. Two stores: an XML config file (host-level: ports, SSL, Postgres, log level, branch, API key) and the `Config` table (everything user-editable from the UI: media management, indexer defaults, UI prefs, etc.).
2. **AutoTagging** — declarative "if series matches X then add tag Y" rules; evaluated whenever a series is added/edited/refreshed.
3. **Tags** — single-table label registry referenced by Series, ImportLists, Notifications, Indexers, DownloadClients, DelayProfiles, ReleaseProfiles, AutoTagging.
4. **RootFolders** — registered top-level paths under which series live. The same record carries a transient view (Accessible / FreeSpace / TotalSpace / UnmappedFolders) that is recomputed on every read.
5. **RemotePathMappings** — `(host, remotePath) → localPath` translation table used by the download-client integration when the download client lives on a different host than Sonarr.
6. **DiskSpace** — read-only "free space per disk" rollup used by the dashboard.

All six share one trait the porter must preserve: their state is small, mutable from the UI, written rarely, and read on every UI page-load. Each has a `MemoryCache`-backed read path to keep the read hot, and each invalidates by clearing the cache then publishing a `*UpdatedEvent`.

---

## 1. Configuration

### Purpose
Two providers serve all settings:
- `IConfigService` / `ConfigService` (`src/NzbDrone.Core/Configuration/ConfigService.cs:24`) — reads/writes the `Config` table (key/value rows). Cached in a process-static `Dictionary<string,string>` (line 29). Used for "user settings" — UI prefs, media management, indexer defaults, etc.
- `IConfigFileProvider` / `ConfigFileProvider` (`src/NzbDrone.Core/Configuration/ConfigFileProvider.cs:25`) — reads/writes the XML config file (`<dataDir>/config.xml`). Each property has an `IOptions<T>` override that takes precedence (env vars / `appsettings.json`). Used for "host settings" — port, bind address, SSL, API key, branch, Postgres, logs.

The two providers are independent — they share no cache and no transaction. The settings UI saves to BOTH in the same request (`src/Sonarr.Api.V5/Settings/SettingsController.cs:47-48`).

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `Config` | `Configuration/Config.cs:5` | DB row. `Key` (lowercased on set, line 12) + `Value`, both strings. |
| `IConfigRepository` / `ConfigRepository` | `Configuration/ConfigRepository.cs:7` | `IBasicRepository<Config>` plus `Get(string key)` / `Upsert(string key, string value)`. |
| `IConfigService` / `ConfigService` | `Configuration/IConfigService.cs:11`, `Configuration/ConfigService.cs:24` | Strongly-typed accessors over `Config` table; static `Dictionary<string,string>` cache (line 29). |
| `IConfigFileProvider` / `ConfigFileProvider` | `Configuration/ConfigFileProvider.cs:25,76` | Strongly-typed accessors over XML file; `ICached<string>` cache, file `Mutex` (line 93). Implements `IHandleAsync<ApplicationStartedEvent>` and `IExecute<ResetApiKeyCommand>`. |
| `ConfigKey` (enum) | `Configuration/ConfigService.cs:19` | Single-value enum (`DownloadedEpisodesFolder`) — vestigial; the only key still using it. |
| `RescanAfterRefreshType` (enum) | `Configuration/RescanAfterRefreshType.cs:3` | `Always`, `AfterManual`, `Never`. Default `Always` (`ConfigService.cs:253`). |
| `ResetApiKeyCommand` | `Configuration/ResetApiKeyCommand.cs:5` | `Command` with `SendUpdatesToClient = true`. Handled by `ConfigFileProvider.Execute` (line 510). |
| `InvalidConfigFileException` | `Configuration/InvalidConfigFileException.cs:6` | Thrown when XML is empty, unparseable, control-char-only, or missing the single `<Config>` root. |
| `IDeploymentInfoProvider` / `DeploymentInfoProvider` | `Configuration/DeploymentInfoProvider.cs:11,28` | Reads `<bin>/../package_info` and `<bin>/release_info` plain-text files into `Package*` / `Release*` properties; computes `DefaultBranch` and `DefaultUpdateMechanism`. Constructor-time only — no event subscription, no rescan. |
| `ConfigSavedEvent` | `Configuration/Events/ConfigSavedEvent.cs:5` | Empty marker event raised by `ConfigService.SaveConfigDictionary` when at least one key changed. |
| `ConfigFileSavedEvent` | `Configuration/Events/ConfigFileSavedEvent.cs:5` | Empty marker event raised by `ConfigFileProvider.SaveConfigDictionary` when at least one key changed. |

### Public services
- `IConfigService.SaveConfigDictionary(Dictionary<string,object>)` — diff-and-write; raises `ConfigSavedEvent` once if anything changed (`ConfigService.cs:55-81`).
- `IConfigService.IsDefined(string key)` — returns true only if the row exists (no defaulting). Lowercases the key first.
- `IConfigFileProvider.SaveConfigDictionary` — diff-and-write; raises `ConfigFileSavedEvent` once if anything changed; **silently ignores any value with key `"ApiKey"`** (line 144). Caller must re-PUT through `ResetApiKeyCommand` to rotate it.
- `IConfigFileProvider.GetConfigDictionary()` — reflection-walk over public properties; returns the *current effective* values (env-override applied).
- `IConfigFileProvider.EnsureDefaultConfigFile()` — if the XML file does not exist, save the current dictionary so all defaults get persisted.
- `IConfigFileProvider.HandleAsync(ApplicationStartedEvent)` — runs `MigrateConfigFile()`, `EnsureDefaultConfigFile()`, then `DeleteOldValues()` (the latter strips any `<Config>` child that does not match a property of the provider; `ConfigFileProvider.cs:434-441`).

### DB tables
- `Config` — `Id`, `Key` (unique), `Value`. Created in `Datastore/Migration/001_initial_setup.cs:11-13`. Mapped in `Datastore/TableMapping.cs:61`.

### Config keys — full ledger

#### IConfigService (DB-backed, `Config` table)

| Key (DB lowercased) | Property | Type | Default | Source notes |
| --- | --- | --- | --- | --- |
| `downloadclientworkingfolders` | `DownloadClientWorkingFolders` | string | `"_UNPACK_|_FAILED_"` | `ConfigService.cs:182` |
| `downloadclienthistorylimit` | `DownloadClientHistoryLimit` | int | `60` | `ConfigService.cs:189` |
| `enablecompleteddownloadhandling` | `EnableCompletedDownloadHandling` | bool | `true` | `ConfigService.cs:141` |
| `autoredownloadfailed` | `AutoRedownloadFailed` | bool | `true` | `ConfigService.cs:148` |
| `autoredownloadfailedfrominteractivesearch` | `AutoRedownloadFailedFromInteractiveSearch` | bool | `true` | `ConfigService.cs:155` |
| `autounmonitorpreviouslydownloadedepisodes` | `AutoUnmonitorPreviouslyDownloadedEpisodes` | bool | `false` | `ConfigService.cs:90` |
| `recyclebin` | `RecycleBin` | string | `""` | `ConfigService.cs:102` |
| `recyclebincleanupdays` | `RecycleBinCleanupDays` | int | `7` | `ConfigService.cs:108` |
| `downloadpropersandrepacks` | `DownloadPropersAndRepacks` | enum (`ProperDownloadTypes`) | `PreferAndUpgrade` | `ConfigService.cs:134` |
| `createemptyseriesfolders` | `CreateEmptySeriesFolders` | bool | `false` | `ConfigService.cs:162` |
| `deleteemptyfolders` | `DeleteEmptyFolders` | bool | `false` | `ConfigService.cs:169` |
| `filedate` | `FileDate` | enum (`FileDateType`) | `None` | `ConfigService.cs:176` |
| `skipfreespacecheckwhenimporting` | `SkipFreeSpaceCheckWhenImporting` | bool | `false` | `ConfigService.cs:197` (TODO: rename to "Skip Free Space Check") |
| `minimumfreespacewhenimporting` | `MinimumFreeSpaceWhenImporting` | int (MB) | `100` | `ConfigService.cs:204` |
| `copyusinghardlinks` | `CopyUsingHardlinks` | bool | `true` | `ConfigService.cs:211` |
| `enablemediainfo` | `EnableMediaInfo` | bool | `true` | `ConfigService.cs:218` |
| `usescriptimport` | `UseScriptImport` | bool | `false` | `ConfigService.cs:225` |
| `scriptimportpath` | `ScriptImportPath` | string | `""` | `ConfigService.cs:232` |
| `importextrafiles` | `ImportExtraFiles` | bool | `false` | `ConfigService.cs:239` |
| `extrafileextensions` | `ExtraFileExtensions` | string (csv) | `"srt"` | `ConfigService.cs:246` |
| `rescanafterrefresh` | `RescanAfterRefresh` | enum (`RescanAfterRefreshType`) | `Always` | `ConfigService.cs:253` |
| `episodetitlerequired` | `EpisodeTitleRequired` | enum (`EpisodeTitleRequiredType`) | `Always` | `ConfigService.cs:260` |
| `userrejectedextensions` | `UserRejectedExtensions` | string (csv) | `""` | `ConfigService.cs:267` |
| `seasonpackupgrade` | `SeasonPackUpgrade` | enum (`SeasonPackUpgradeType`) | `All` | `ConfigService.cs:273` |
| `seasonpackupgradethreshold` | `SeasonPackUpgradeThreshold` | double | `100.0` | `ConfigService.cs:279` |
| `setpermissionslinux` | `SetPermissionsLinux` | bool | `false` | `ConfigService.cs:285` |
| `chmodfolder` | `ChmodFolder` | string (octal) | `"755"` | `ConfigService.cs:292` |
| `chowngroup` | `ChownGroup` | string | `""` | `ConfigService.cs:299` |
| `retention` | `Retention` | int (days) | `0` | `ConfigService.cs:96` |
| `rsssyncinterval` | `RssSyncInterval` | int (minutes) | `15` | `ConfigService.cs:114`. `0` disables; `<10` clamped to `10` by `TaskManager`. |
| `maximumsize` | `MaximumSize` | int (MB) | `0` | `ConfigService.cs:121` |
| `minimumage` | `MinimumAge` | int (minutes) | `0` | `ConfigService.cs:127` |
| `listsynclevel` | `ListSyncLevel` | enum (`ListSyncLevelType`) | `Disabled` | `ConfigService.cs:306` |
| `listsynctag` | `ListSyncTag` | int (tag id) | `0` | `ConfigService.cs:312` |
| `firstdayofweek` | `FirstDayOfWeek` | int | `(int)CultureInfo.CurrentCulture.DateTimeFormat.FirstDayOfWeek` | `ConfigService.cs:318` |
| `calendarweekcolumnheader` | `CalendarWeekColumnHeader` | string | `"ddd M/D"` | `ConfigService.cs:324` |
| `shortdateformat` | `ShortDateFormat` | string | `"MMM D YYYY"` | `ConfigService.cs:331` |
| `longdateformat` | `LongDateFormat` | string | `"dddd, MMMM D YYYY"` | `ConfigService.cs:338` |
| `timeformat` | `TimeFormat` | string | `"h(:mm)a"` | `ConfigService.cs:345` |
| `timezone` | `TimeZone` | string | `""` | `ConfigService.cs:352` |
| `showrelativedates` | `ShowRelativeDates` | bool | `true` | `ConfigService.cs:359` |
| `enablecolorimpairedmode` | `EnableColorImpairedMode` | bool | `false` | `ConfigService.cs:366` |
| `uilanguage` | `UILanguage` | int | `(int)Language.English` | `ConfigService.cs:373` |
| `cleanupmetadataimages` | `CleanupMetadataImages` | bool | `true` | `ConfigService.cs:380` |
| `plexclientidentifier` | `PlexClientIdentifier` | string (guid) | new `Guid` (persisted) | `ConfigService.cs:386` |
| `rijndaelpassphrase` | `RijndaelPassphrase` | string (guid) | new `Guid` (persisted) | `ConfigService.cs:388` |
| `hmacpassphrase` | `HmacPassphrase` | string (guid) | new `Guid` (persisted) | `ConfigService.cs:390` |
| `rijndaelsalt` | `RijndaelSalt` | string (guid) | new `Guid` (persisted) | `ConfigService.cs:392` |
| `hmacsalt` | `HmacSalt` | string (guid) | new `Guid` (persisted) | `ConfigService.cs:394` |
| `proxyenabled` | `ProxyEnabled` | bool | `false` | `ConfigService.cs:396` |
| `proxytype` | `ProxyType` | enum (`ProxyType`) | `Http` | `ConfigService.cs:398` |
| `proxyhostname` | `ProxyHostname` | string | `""` | `ConfigService.cs:400` |
| `proxyport` | `ProxyPort` | int | `8080` | `ConfigService.cs:402` |
| `proxyusername` | `ProxyUsername` | string | `""` | `ConfigService.cs:404` |
| `proxypassword` | `ProxyPassword` | string | `""` | `ConfigService.cs:406` |
| `proxybypassfilter` | `ProxyBypassFilter` | string | `""` | `ConfigService.cs:408` |
| `proxybypasslocaladdresses` | `ProxyBypassLocalAddresses` | bool | `true` | `ConfigService.cs:410` |
| `backupfolder` | `BackupFolder` | string | `"Backups"` | `ConfigService.cs:412` (relative to AppData if not rooted) |
| `backupinterval` | `BackupInterval` | int (days) | `7` | `ConfigService.cs:414` (clamped to `[1,7]` by `TaskManager`) |
| `backupretention` | `BackupRetention` | int (days) | `28` | `ConfigService.cs:416`; UI validates `[1,90]` (`Sonarr.Api.V5/Settings/GeneralSettingsController.cs:72`) |
| `certificatevalidation` | `CertificateValidation` | enum (`CertificateValidationType`) | `Enabled` | `ConfigService.cs:418` |
| `applicationurl` | `ApplicationUrl` | string | `""` | `ConfigService.cs:421` |
| `trustcgnatipaddresses` | `TrustCgnatIpAddresses` | bool | `false` | `ConfigService.cs:425` (also exposed via `IConfigFileProvider.TrustCgnatIpAddresses` — see CFG-Q1) |

#### IConfigFileProvider (XML-backed, `<dataDir>/config.xml`)

Read order: `_<scope>Options.<Property>` (env / appsettings) **first**, fall back to `GetValue("<XMLKey>", default)`. The `IOptions` lookup is non-persisting.

| XML element | Property | Env-var override | Type | Default | Source notes |
| --- | --- | --- | --- | --- | --- |
| `BindAddress` | `BindAddress` | `Sonarr:Server:BindAddress` | string | `"*"` | `ConfigFileProvider.cs:170-184`. Empty/whitespace falls back to `"*"`. |
| `Port` | `Port` | `Sonarr:Server:Port` | int | `8989` | `ConfigFileProvider.cs:186` |
| `SslPort` | `SslPort` | `Sonarr:Server:SslPort` | int | `9898` | `ConfigFileProvider.cs:188` |
| `EnableSsl` | `EnableSsl` | `Sonarr:Server:EnableSsl` | bool | `false` | `ConfigFileProvider.cs:190`. Auto-disabled if `SslCertHash` legacy element is set or `SslCertPath` is empty (`MigrateConfigFile`, line 414-417). |
| `LaunchBrowser` | `LaunchBrowser` | `Sonarr:App:LaunchBrowser` | bool | `true` | `ConfigFileProvider.cs:192` |
| `ApiKey` | `ApiKey` | `Sonarr:Auth:ApiKey` | string | new `Guid` w/o dashes; **persisted on first read** | `ConfigFileProvider.cs:194-208`. Writes via `SaveConfigDictionary` are silently ignored — must use `ResetApiKeyCommand`. |
| `AuthenticationMethod` | `AuthenticationMethod` | `Sonarr:Auth:Method` | enum (`AuthenticationType`) | `None` | `ConfigFileProvider.cs:210-237`. **Silent rewrites:** legacy `Sonarr:Auth:Enabled=true` rewrites to `Forms`; legacy `Basic` rewrites to `Forms`. Persisted both times. |
| `AuthenticationRequired` | `AuthenticationRequired` | `Sonarr:Auth:Required` | enum (`AuthenticationRequiredType`) | `Enabled` | `ConfigFileProvider.cs:239` |
| `AnalyticsEnabled` | `AnalyticsEnabled` | `Sonarr:Log:AnalyticsEnabled` | bool | `true` | `ConfigFileProvider.cs:246`. **Not persisted to XML** (`persist:false`). |
| `Branch` | `Branch` | `Sonarr:Update:Branch` | string | `"main"` (lowercased) | `ConfigFileProvider.cs:248`. **Silently rewritten by `UpdaterConfigProvider` on every startup if the package's external updater specifies a different branch** (see Notable quirks). |
| `LogLevel` | `LogLevel` | `Sonarr:Log:Level` | string | `"debug"` | `ConfigFileProvider.cs:250` |
| `ConsoleLogLevel` | `ConsoleLogLevel` | `Sonarr:Log:ConsoleLevel` | string | `""` | `ConfigFileProvider.cs:251` (not persisted) |
| `ConsoleLogFormat` | `ConsoleLogFormat` | `Sonarr:Log:ConsoleFormat` | enum (`ConsoleLogFormat`) | `Standard` | `ConfigFileProvider.cs:253` (not persisted) |
| `Theme` | `Theme` | `Sonarr:App:Theme` | string | `"auto"` | `ConfigFileProvider.cs:258` (not persisted) |
| `PostgresHost` | `PostgresHost` | `Sonarr:Postgres:Host` | string | `""` | `ConfigFileProvider.cs:260` (not persisted) |
| `PostgresUser` | `PostgresUser` | `Sonarr:Postgres:User` | string | `""` | `ConfigFileProvider.cs:261` (not persisted) |
| `PostgresPassword` | `PostgresPassword` | `Sonarr:Postgres:Password` | string | `""` | `ConfigFileProvider.cs:262` (not persisted) |
| `PostgresMainDb` | `PostgresMainDb` | `Sonarr:Postgres:MainDb` | string | `"sonarr-main"` | `ConfigFileProvider.cs:263` (not persisted) |
| `PostgresLogDb` | `PostgresLogDb` | `Sonarr:Postgres:LogDb` | string | `"sonarr-log"` | `ConfigFileProvider.cs:264` (not persisted) |
| `PostgresPort` | `PostgresPort` | `Sonarr:Postgres:Port` | int | `5432` | `ConfigFileProvider.cs:265` (treats `0` from options as "unset"; not persisted) |
| `PostgresMainDbConnectionString` | `PostgresMainDbConnectionString` | `Sonarr:Postgres:MainDbConnectionString` | string | `""` | `ConfigFileProvider.cs:266` (not persisted; full DSN override) |
| `PostgresLogDbConnectionString` | `PostgresLogDbConnectionString` | `Sonarr:Postgres:LogDbConnectionString` | string | `""` | `ConfigFileProvider.cs:267` (not persisted; full DSN override) |
| `LogDbEnabled` | `LogDbEnabled` | `Sonarr:Log:DbEnabled` | bool | `true` | `ConfigFileProvider.cs:268` (not persisted) |
| `LogSql` | `LogSql` | `Sonarr:Log:Sql` | bool | `false` | `ConfigFileProvider.cs:269` (not persisted) |
| `LogRotate` | `LogRotate` | `Sonarr:Log:Rotate` | int | `50` | `ConfigFileProvider.cs:270` (not persisted) |
| `LogSizeLimit` | `LogSizeLimit` | `Sonarr:Log:SizeLimit` | int (MB) | `1` (clamped to `[0,10]`) | `ConfigFileProvider.cs:271` (not persisted; UI validates `[1,10]`) |
| `FilterSentryEvents` | `FilterSentryEvents` | `Sonarr:Log:FilterSentryEvents` | bool | `true` | `ConfigFileProvider.cs:272` (not persisted) |
| `SslCertPath` | `SslCertPath` | `Sonarr:Server:SslCertPath` | string | `""` | `ConfigFileProvider.cs:273` |
| `SslKeyPath` | `SslKeyPath` | `Sonarr:Server:SslKeyPath` | string | `""` | `ConfigFileProvider.cs:274` |
| `SslCertPassword` | `SslCertPassword` | `Sonarr:Server:SslCertPassword` | string | `""` | `ConfigFileProvider.cs:275` |
| `UrlBase` | `UrlBase` | `Sonarr:Server:UrlBase` | string | `""` (always normalized to `"/"+trim('/')`) | `ConfigFileProvider.cs:277-289` |
| `UiFolder` | `UiFolder` | n/a (computed) | string | `BuildInfo.IsDebug ? "../UI" : "UI"` | `ConfigFileProvider.cs:292` |
| `InstanceName` | `InstanceName` | `Sonarr:App:InstanceName` | string | `BuildInfo.AppName` (`"Sonarr"`) | `ConfigFileProvider.cs:294-307`. **Silently overridden** to `BuildInfo.AppName` if the configured value does not start or end with `"Sonarr"`. |
| `UpdateAutomatically` | `UpdateAutomatically` | `Sonarr:Update:Automatically` | bool | `OsInfo.IsWindows` (`true` on Windows, `false` elsewhere) | `ConfigFileProvider.cs:309` (not persisted) |
| `UpdateMechanism` | `UpdateMechanism` | `Sonarr:Update:Mechanism` | enum (`UpdateMechanism`) | `BuiltIn` | `ConfigFileProvider.cs:311-314` (not persisted). **Silently rewritten by `UpdaterConfigProvider` on startup** to match the package's external updater (see Notable quirks). |
| `UpdateScriptPath` | `UpdateScriptPath` | `Sonarr:Update:ScriptPath` | string | `""` | `ConfigFileProvider.cs:316` (not persisted) |
| `SyslogServer` | `SyslogServer` | `Sonarr:Log:SyslogServer` | string | `""` | `ConfigFileProvider.cs:318` (not persisted) |
| `SyslogPort` | `SyslogPort` | `Sonarr:Log:SyslogPort` | int | `514` | `ConfigFileProvider.cs:320` (not persisted) |
| `SyslogLevel` | `SyslogLevel` | `Sonarr:Log:SyslogLevel` | string | `LogLevel` (i.e. follows main level) | `ConfigFileProvider.cs:322` (not persisted) |
| `TrustCgnatIpAddresses` | `TrustCgnatIpAddresses` | `Sonarr:Auth:TrustCgnatIpAddresses` | bool | `false` | `ConfigFileProvider.cs:244` (not persisted). **Same name also exists on `IConfigService`** — see CFG-Q1. |
| `ProfilerEnabled` | `ProfilerEnabled` | `Sonarr:App:ProfilerEnabled` | bool | `false` | `ConfigFileProvider.cs:324` (not persisted) |
| `ProfilerPosition` | `ProfilerPosition` | `Sonarr:App:ProfilerPosition` | string | `"bottom-right"` | `ConfigFileProvider.cs:325` (not persisted) |

#### Where each `IOptions<T>` is bound
`src/NzbDrone.Host/Bootstrap.cs:108-113` and `:185-190` both bind these sections (utility-mode and host-mode):

- `Sonarr:Postgres` → `PostgresOptions` (`src/NzbDrone.Core/Datastore/PostgresOptions.cs:5`).
- `Sonarr:App` → `AppOptions` (`src/NzbDrone.Common/Options/AppOptions.cs:3`): `InstanceName`, `Theme`, `LaunchBrowser`, `ProfilerEnabled`, `ProfilerPosition`.
- `Sonarr:Auth` → `AuthOptions` (`src/NzbDrone.Common/Options/AuthOptions.cs:3`): `ApiKey`, `Enabled`, `Method`, `Required`, `TrustCgnatIpAddresses`.
- `Sonarr:Server` → `ServerOptions` (`src/NzbDrone.Common/Options/ServerOptions.cs:3`): `UrlBase`, `BindAddress`, `Port`, `EnableSsl`, `SslPort`, `SslCertPath`, `SslKeyPath`, `SslCertPassword`.
- `Sonarr:Log` → `LogOptions` (`src/NzbDrone.Common/Options/LogOptions.cs:3`): `Level`, `FilterSentryEvents`, `Rotate`, `SizeLimit`, `Sql`, `ConsoleLevel`, `ConsoleFormat`, `AnalyticsEnabled`, `SyslogServer`, `SyslogPort`, `SyslogLevel`, `DbEnabled`.
- `Sonarr:Update` → `UpdateOptions` (`src/NzbDrone.Common/Options/UpdateOptions.cs:3`): `Mechanism`, `Automatically`, `ScriptPath`, `Branch`.

#### Configuration source precedence
`Bootstrap.GetConfiguration` (`src/NzbDrone.Host/Bootstrap.cs:256-275`) builds the `IConfiguration` in this order — later sources override earlier ones:

1. XML config file at `<dataDir>/config.xml` (`AddXmlFile(configPath, optional:true, reloadOnChange:false)`).
2. In-memory entry: `dataProtectionFolder` (set to `appFolder.GetDataProtectionPath()`).
3. Environment variables (`AddEnvironmentVariables()` — picks up `Sonarr__Server__Port=...`-style keys).

`StartupContext` (`src/NzbDrone.Common/EnvironmentInfo/StartupContext.cs:17`) parses `args` separately into a `HashSet<string> Flags` and `Dictionary<string,string> Args`. Recognized switches: `data` (override AppData folder), `nobrowser`, `i` / `u` (install/uninstall service), `?` (help), `terminateexisting`, `restart`, `registerurl`, `nosingleinstancecheck`, `exitimmediately`. `data=` is the only argument that influences the configuration path lookup.

So overall precedence (highest first):
1. `IOptions<T>` (env-var binding for properties that have an override). Note: most `IConfigService` properties have no `IOptions` override.
2. XML file value (for `IConfigFileProvider`) / DB value (for `IConfigService`).
3. Hard-coded default in the property getter.
4. `--data=...` command-line arg only redirects the AppData folder.

`PostgresOptions.GetOptions()` (`src/NzbDrone.Core/Datastore/PostgresOptions.cs:16-26`) builds an *independent* `ConfigurationBuilder` from env vars only — **the XML config file is not considered for this codepath**. See CFG-Q2.

### Caching
- `ConfigService` uses a process-static `Dictionary<string,string>` (`ConfigService.cs:29`). Loaded once on first read inside a `lock(_cache)`; cleared on every `SetValue`.
- `ConfigFileProvider` uses an `ICached<string>` keyed on `key` (each property cached independently). Cleared per-key on `SetValue`. The XML file itself is locked via a static `object Mutex` (`ConfigFileProvider.cs:93`).
- The `ConfigService` cache is process-static — **reloading the XML config file does NOT clear the `ConfigService` cache** (different field). Every cross-process write (e.g. another Sonarr instance) is invisible until restart. Sonarr's single-instance check makes this rare in practice but not impossible.

### `SaveConfigDictionary` semantics
Both providers implement the same shape:

`ConfigService.SaveConfigDictionary` (`ConfigService.cs:55-81`):
1. Reflect over `this.GetType().GetProperties()` to build `allWithDefaults` (case-insensitive).
2. For each (key, value) in input: skip if either is null; compare as strings; if different, call `SetValue` (which lowercases, upserts, clears cache).
3. After the loop, if any value changed, raise `ConfigSavedEvent` once.

Not transactional: each `SetValue` is its own DB upsert; a crash mid-loop leaves a partial save and no event raised. Validation is the caller's responsibility (the V3/V5 settings controllers wire FluentValidation rules).

`ConfigFileProvider.SaveConfigDictionary` (`ConfigFileProvider.cs:135-168`):
1. Clears the entire cache (line 137).
2. Builds `allWithDefaults` via reflection.
3. Skips any key matching `"ApiKey"` (case-insensitive) — see CFG-Q3.
4. Calls `SetValue(<PascalCase key>, value)` for each diff. `SetValue` rewrites the entire XML file.
5. Raises `ConfigFileSavedEvent` once if anything changed.

### `ResetApiKeyCommand`
- Class: `Configuration/ResetApiKeyCommand.cs:5` (`SendUpdatesToClient = true`).
- Handler: `ConfigFileProvider.Execute(ResetApiKeyCommand)` (`ConfigFileProvider.cs:510-513`) — calls `SetValue("ApiKey", GenerateApiKey())` (new `Guid` minus dashes). The cache write fires inside `SetValue` (line 388); the file is rewritten.
- **No `ConfigFileSavedEvent` is raised** by `Execute` — the Command path bypasses `SaveConfigDictionary`. Subscribers that expect to react to API-key rotation (e.g. UI auth re-handshake) will miss it.

### Events raised
| Event | Payload | Producer | Consumers |
| --- | --- | --- | --- |
| `ConfigSavedEvent` | (empty marker) | `ConfigService.SaveConfigDictionary` (`Configuration/ConfigService.cs:79`) | `Jobs/TaskManager` recomputes RSS-sync / Backup intervals; `Sonarr.Http/Authentication/UiAuthorizationHandler`; `Localization/LocalizationService`; `Download/Pending/PendingReleaseService`. |
| `ConfigFileSavedEvent` | (empty marker) | `ConfigFileProvider.SaveConfigDictionary` (`Configuration/ConfigFileProvider.cs:166`) | `Instrumentation/ReconfigureLogging` (`src/NzbDrone.Core/Instrumentation/ReconfigureLogging.cs:19`). |

### Events consumed
- `ApplicationStartedEvent` — `ConfigFileProvider.HandleAsync` (line 503): runs `MigrateConfigFile` then `EnsureDefaultConfigFile` then `DeleteOldValues`.

### Jobs / commands
- `ResetApiKeyCommand` — handled by `ConfigFileProvider`. No scheduling.

### `DeploymentInfoProvider`
Reads two plain-text files at construction time (no events, no rescan):

| File path | Read keys | Property |
| --- | --- | --- |
| `<startUpFolder>/../package_info` (only when `Path.GetFileName(startUp) == "bin"`) | `PackageVersion`, `PackageAuthor`, `PackageGlobalMessage`, `UpdateMethod`, `UpdateMethodMessage`, `Branch`, `ReleaseVersion` | `PackageVersion`, `PackageAuthor`, `PackageGlobalMessage`, `PackageUpdateMechanism`, `PackageUpdateMechanismMessage`, `PackageBranch`, `ReleaseVersion`. |
| `<startUpFolder>/release_info` | `ReleaseVersion`, `Branch` | `ReleaseVersion` (overrides), `ReleaseBranch`. |

Computed properties:
- `IsExternalUpdateMechanism` = `PackageUpdateMechanism >= UpdateMechanism.External` (`DeploymentInfoProvider.cs:107`).
- `DefaultUpdateMechanism` = `PackageUpdateMechanism` (line 71). Falls back to `UpdateMechanism.BuiltIn` if no `package_info`.
- `DefaultBranch` priority: `ReleaseBranch` if set, else `PackageBranch`, else `"main"` (lines 36-69).

The file format is grep-friendly key=value lines parsed by a multiline regex (`DeploymentInfoProvider.cs:74-83`).

### Notable quirks / TODOs / HACKs
- **Silent `Branch` rewrite on startup**: `UpdaterConfigProvider.Handle(ApplicationStartedEvent)` (`src/NzbDrone.Core/Update/ConfigureUpdateMechanism.cs:50-59`) — if the package is an external updater (`IsExternalUpdateMechanism`) and `PackageBranch` differs from `Branch`, it overwrites `Branch` via `SaveConfigDictionary`. The user does not see a UI prompt; only an info-level log line.
- **Silent `UpdateMechanism` rewrite on startup**: same handler (lines 39-48) — if `UpdateMechanism` does not match the package's external mechanism (or is `BuiltIn` but the package says external), it rewrites it. Info-level log only.
- **Silent `AuthenticationMethod` rewrites**: legacy `Sonarr:Auth:Enabled=true` env var rewrites to `Forms`; legacy DB value of `Basic` rewrites to `Forms` (`ConfigFileProvider.cs:214-233`).
- **Silent `EnableSsl` rewrite on migration**: if SSL is enabled but the config still has the legacy `<SslCertHash>` element or `SslCertPath` is empty, `MigrateConfigFile` flips `EnableSsl` to `false` (`ConfigFileProvider.cs:413-417`).
- **Silent `InstanceName` clamp**: any value not starting/ending with `"Sonarr"` is replaced with `BuildInfo.AppName` (`ConfigFileProvider.cs:298-306`).
- **`AuthenticationEnabled` reads as `false` even when persist is requested**: the call passes `persist:false` (`ConfigFileProvider.cs:214`), so this legacy XML element is never written back from the default.
- **`ApiKey` cannot be set via `SaveConfigDictionary`**: the loop drops it silently (`ConfigFileProvider.cs:144-147`). Only `ResetApiKeyCommand` rotates it, but that path raises NO event.
- **`ConfigSavedEvent` and `ConfigFileSavedEvent` are independent**: a UI save that updates both providers raises both events. Consumers must decide which one they actually need.
- **Reflection-based diff in both providers**: any property added to `ConfigService` or `ConfigFileProvider` automatically becomes a settable key. Removing a property and shipping a new release leaves dead rows in `Config` (no migration). The XML cleanup `DeleteOldValues` does prune stale XML elements, but the DB has no equivalent.
- **`Config` table key normalization is one-way**: `Config.Key.set` lowercases (`Config.cs:12`), but reads via `ConfigRepository.Get(string key)` use the supplied key as-is (`ConfigRepository.cs:21-23`). Callers must lowercase before query (`ConfigService.IsDefined` does — line 85). Direct `Get` callers must also lowercase or get null. See CFG-Q4.
- **`TrustCgnatIpAddresses` is duplicated**: present on both `IConfigService` (DB-backed, settable from API) and `IConfigFileProvider` (env-backed, read-only). The two values are independent. See CFG-Q1.
- **`ConfigService._cache` is a `static` field** (`ConfigService.cs:29`). The `EnsureCache` and `ClearCache` are also static. Multiple `ConfigService` instances share the same cache.
- **PG connection string built from env only**: `PostgresOptions.GetOptions()` builds its `ConfigurationBuilder` with `AddEnvironmentVariables()` only; the XML config file is not consulted. Production uses `IOptions<PostgresOptions>` injection (which IS XML-aware). Two paths give different answers if PG vars are only in XML. See CFG-Q2.

### Test files
- `src/NzbDrone.Core.Test/Configuration/ConfigServiceFixture.cs`
- `src/NzbDrone.Core.Test/Configuration/ConfigCachingFixture.cs`

### Open questions (Configuration)
- **CFG-Q1**: `TrustCgnatIpAddresses` exists on both `IConfigService` (`ConfigService.cs:423`) and `IConfigFileProvider` (`ConfigFileProvider.cs:244`) with the same name but different storage. Which value wins for the actual auth check? Should the Go port collapse them, or preserve the duplication for back-compat with config files that set either?
- **CFG-Q2**: `PostgresOptions.GetOptions()` reads env-only; production uses `IOptions<PostgresOptions>` (env + XML). Where is `GetOptions()` actually invoked, and does that path bypass user XML config? (Blocks Phase 1 datastore wiring decision.)
- **CFG-Q3**: `ConfigFileProvider.SaveConfigDictionary` silently skips `ApiKey`. Why? (Presumably to force callers through `ResetApiKeyCommand`.) Should the Go port reject the key with an error response so the UI knows the call was a no-op?
- **CFG-Q4**: `ConfigRepository.Get(key)` does not lowercase; `Config.Key.set` does. Direct callers must remember to lowercase. Any callers in C# that don't? (grep `_repository.Get(` outside `ConfigService.cs`.) Should the port fix this with a normalized accessor?
- **CFG-Q5**: `ResetApiKeyCommand` does not raise `ConfigFileSavedEvent`. Is this intentional (avoid notify-loops) or an oversight? UI does observe the new key via `SendUpdatesToClient`, but other subsystems (e.g. instance-to-instance auth tokens) may not be informed.
- **CFG-Q6**: Bootstrap binds `Sonarr:*` sections in BOTH the utility-mode `HostBuilder` (`Bootstrap.cs:108-113`) and the host `HostBuilder` (`Bootstrap.cs:185-190`), but the `IOptions` injected into `ConfigFileProvider` is only the host-mode binding. Utility-mode commands (e.g. `--restart`, `--registerurl`) read from `IConfiguration` directly. Is there a shared abstraction the Go port should adopt, or two configuration trees?
- **CFG-Q7**: `ConsoleLogFormat` and `Theme` and many `IConfigFileProvider` properties pass `persist:false`. Which means XML config NEVER captures them — they always come from env or default. Is this intentional UX (env-only switches), or a footgun for users who try to set Theme via UI?
- **CFG-Q8**: `RescanAfterRefreshType.AfterManual` semantics — what counts as "manual" vs "scheduled"? Document the trigger map (likely `CommandTrigger.Manual` vs `Scheduled` in the command queue).
- **CFG-Q9**: `ConfigService.SaveConfigDictionary` is non-transactional. If the DB write of key 7/10 fails, keys 1-6 are persisted and no event is raised. Should the Go port wrap in a `BEGIN`/`COMMIT` and only raise on success?
- **CFG-Q10**: `_cache` in `ConfigService` is `static` — so even if the DI container resolves multiple instances, they share state. Go port: should this be a process singleton with a `sync.RWMutex`, or per-instance state?

---

## 2. AutoTagging

### Purpose
Declarative rules of the form "if this Series matches all required specifications and at least one non-required specification, add these tag IDs to the series; if `RemoveTagsAutomatically` is set and the rule no longer matches, remove the tags." Evaluated by `AutoTaggingService.GetTagChanges(Series)` whenever a Series is added, edited, refreshed, or bulk-edited.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `AutoTag` | `AutoTagging/AutoTag.cs:7` | DB row. `Name` (string, unique), `Specifications` (`List<IAutoTaggingSpecification>` — JSON-embedded), `RemoveTagsAutomatically` (bool), `Tags` (`HashSet<int>` — JSON-embedded). |
| `IAutoTaggingSpecification` | `AutoTagging/Specifications/IAutoTagSpecification.cs:6` | Spec interface. `Order`, `ImplementationName`, `Name`, `Negate`, `Required`, `Validate`, `Clone`, `IsSatisfiedBy(Series)`. |
| `AutoTaggingSpecificationBase` | `AutoTagging/Specifications/AutoTagSpecificationBase.cs:6` | Abstract base. Implements `IsSatisfiedBy` as `IsSatisfiedByWithoutNegate(s) ^ Negate` and `Clone` via `MemberwiseClone`. |
| `AutoTaggingChanges` | `AutoTagging/AutoTaggingChanges.cs:5` | `HashSet<int> TagsToAdd; HashSet<int> TagsToRemove`. |
| `SpecificationMatchesGroup` | `AutoTagging/SpecificationMatchesGroup.cs:7` | Per-spec-type bucket. `DidMatch = !(any Required has Value==false || all Value==false)` (line 11). |
| `IAutoTaggingService` / `AutoTaggingService` | `AutoTagging/AutoTaggingService.cs:11,22` | CRUD + cache + `GetTagChanges`. |
| `IAutoTaggingRepository` / `AutoTaggingRepository` | `AutoTagging/AutoTaggingRepository.cs:6,10` | `IBasicRepository<AutoTag>`. No custom queries. |
| `AutoTagsUpdatedEvent` | `AutoTagging/AutoTagsUpdatedEvent.cs:5` | Empty marker; producer-only — not consumed by service code, only by `Sonarr.Api.V3/V5.Tags.TagController` for SignalR resync. |

### Specifications (every subclass under `AutoTagging/Specifications/`)
Every concrete spec inherits `AutoTaggingSpecificationBase` and exposes `Negate` and `Required`. All are negatable via the base class's `IsSatisfiedBy` wrapper. Order column is decorative for UI sort.

| Spec | File | `Order` | Match logic | Fields | Validation |
| --- | --- | --- | --- | --- | --- |
| `GenreSpecification` | `Specifications/GenreSpecification.cs:19` | 1 | `series.Genres.Any(g => Value.ContainsIgnoreCase(g))` | `IEnumerable<string> Value` | `Value` not empty. |
| `MonitoredSpecification` | `Specifications/MonitoredSpecification.cs:11` | 1 | `series.Monitored` | (none) | always valid. |
| `NetworkSpecification` | `Specifications/NetworkSpecification.cs:19` | 1 | `Value.Any(n => series.Network.EqualsIgnoreCase(n))` | `IEnumerable<string> Value` | `Value` not empty. |
| `OriginalCountrySpecification` | `Specifications/OriginalCountrySpecification.cs:27` | 1 | `Value.Any(c => series.OriginalCountry.EqualsIgnoreCase(c))` | `IEnumerable<string> Value` | not empty + every entry is exactly 3 chars (ISO-3166-1 alpha-3, but not validated against the ISO list). |
| `OriginalLanguageSpecification` | `Specifications/OriginalLanguageSpecification.cs:17` | 1 | `Value == series.OriginalLanguage.Id` | `int Value` | `Value >= 0`. |
| `QualityProfileSpecification` | `Specifications/QualityProfileSpecification.cs:16` | 1 | `Value == series.QualityProfileId` | `int Value` | `Value > 0`. |
| `RootFolderSpecification` | `Specifications/RootFolderSpecification.cs:18` | 1 | `series.RootFolderPath.PathEquals(Value)` | `string Value` | `IsValidPath`. |
| `SeriesTypeSpecification` | `Specifications/SeriesTypeSpecification.cs:16` | **2** | `(int)series.SeriesType == Value` | `int Value` | `IsInEnum<SeriesTypes>`. |
| `StatusSpecification` | `Specifications/StatusSpecification.cs:12` | 1 | `series.Status == (SeriesStatusType)Status` | `int Status` (note: field name is `Status`, not `Value`) | always valid. |
| `TagSpecification` | `Specifications/TagSpecification.cs:16` | 1 | `series.Tags.Contains(Value)` | `int Value` | `Value > 0`. |
| `YearSpecification` | `Specifications/YearSpecification.cs:19` | 1 | `series.Year >= Min && series.Year <= Max` | `int Min`, `int Max` | `Min > 0`, `Max >= Min`. |

All specs are negatable: `Negate` flips `IsSatisfiedByWithoutNegate`'s return (`AutoTaggingSpecificationBase.cs:24-31`).

### `SpecificationMatchesGroup` semantics (AND vs OR)
`AutoTaggingService.GetTagChanges` (`AutoTaggingService.cs:101-109`):
1. Group all specs in the rule by `GetType()`.
2. Each group becomes a `SpecificationMatchesGroup`.
3. Inside a group: `DidMatch = !(any Required spec's match==false || all matches are false)`. So a group of homogeneous specs is "match if at least one matches AND no `Required` one fails".
4. Across groups: `allMatch = specificationMatches.All(g => g.DidMatch)` — every group must match.

So the effective semantics are **OR within type, AND across types**, with `Required` injecting an extra "must-match" constraint inside its type.

### Storage shape
- Table `AutoTagging` (created in `Datastore/Migration/179_add_auto_tagging.cs:11-15`): `Id`, `Name` (unique), `Specifications` (string, default `"[]"`), `RemoveTagsAutomatically` (bool, default `false`), `Tags` (string, default `"[]"`).
- `Specifications` is JSON via `EmbeddedDocumentConverter<List<IAutoTaggingSpecification>>` with a custom `AutoTaggingSpecificationConverter` (`Datastore/Converters/AutoTagSpecificationConverter.cs:10`). The converter wraps each spec as `{ "type": "<ClassName>", "body": <JSON of spec> }`. Type resolution via `Type.GetType($"NzbDrone.Core.AutoTagging.Specifications.{typename}, Sonarr.Core")` (line 46) — strict; renaming a class breaks deserialization.
- `Tags` is JSON via `EmbeddedDocumentConverter<List<int>>` (no custom converter; standard `HashSet<int>` round-trips via `List<int>`).
- TableMapper registration: `Datastore/TableMapping.cs:176`, `Datastore/TableMapping.cs:190`.

### When does auto-tagging run?
`AutoTaggingService.GetTagChanges` is called from `Tv/SeriesService.UpdateTags(Series)` (`Tv/SeriesService.cs:275-307`). `UpdateTags` is invoked by:
- `SeriesService.UpdateSeries(Series, ...)` for single-edit flows (line 225).
- `SeriesService.UpdateSeries(List<Series>, ...)` for bulk edit (line 255).
- `RefreshSeriesService` on every refresh path: lines 226, 240, 272, 278 of `Tv/RefreshSeriesService.cs`.

It does NOT run on series add directly — `SeriesService.AddSeries` doesn't call `UpdateTags` at the time of add. The newly-added series gets evaluated on the first refresh that runs after add. (Refresh is queued by add; in practice the user sees auto-tags within seconds.) See AT-Q1.

### `AutoTagsUpdatedEvent`
- Producer: `AutoTaggingService.{Update,Insert,Delete}` (`AutoTaggingService.cs:61,69,79`).
- Consumers: `Sonarr.Api.V3.Tags.TagController` (`src/Sonarr.Api.V3/Tags/TagController.cs:75-78`), `Sonarr.Api.V5.Tags.TagController` (`src/Sonarr.Api.V5/Tags/TagController.cs:78`). Both treat it as a "tag set changed → SignalR push `tag/sync`" trigger because Tag-detail counts include `AutoTagIds`. **No service code listens to this event** — there is no automatic re-run of auto-tagging when a rule is added/edited.

### Cache
`AutoTaggingService` uses `ICached<Dictionary<int,AutoTag>>` keyed `"all"` (line 38, 43). Cleared on every Insert/Update/Delete.

### Quirks / TODOs / HACKs
- `IsSatisfiedByWithoutNegate` calls reference `series.Network`, `series.OriginalCountry`, etc. directly with no null check; if a series has a null `Network` and a `NetworkSpecification` with a non-empty `Value` is evaluated, the call site is `Value.Any(n => series.Network.EqualsIgnoreCase(n))` — `EqualsIgnoreCase` is an extension that handles null-safe comparison only if the extension does so. See AT-Q2.
- `AutoTagsUpdatedEvent` does NOT trigger re-evaluation — adding a rule does not retroactively tag existing series. That happens only on the next per-series refresh (AT-Q3).
- `RootFolderSpecification` matches via `PathEquals` (`Specifications/RootFolderSpecification.cs:30`) on `series.RootFolderPath`. But `Series.RootFolderPath` is `Ignore`d in TableMapping (`Datastore/TableMapping.cs:118`) and **populated lazily** by `AutoTaggingService.GetTagChanges` itself (`AutoTaggingService.cs:99`: `series.RootFolderPath = _rootFolderService.GetBestRootFolderPath(series.Path)`). So this spec only works because the service overwrites the field before evaluation. Direct callers of `IsSatisfiedBy` outside `GetTagChanges` will see stale/empty values.
- `AutoTaggingSpecificationConverter` is fragile to renames — see "Storage shape" above.
- `AutoTagging.Tags` (the JSON field) is `List<int>` on the wire but `HashSet<int>` in the model. Duplicates will round-trip fine but order is not preserved.
- Specs are split by `GroupBy(GetType())`; if a user adds two `RootFolderSpecification`s with `Required=true` on different paths, the group's `DidMatch` becomes `false` because every value is false (a series can match only one root folder). This may be surprising. See AT-Q4.

### Test files
- `src/NzbDrone.Core.Test/AutoTagging/AutoTaggingServiceFixture.cs`

### Open questions (AutoTagging)
- **AT-Q1**: New `AutoTag` rule does not back-fill existing series. Is the design "rules apply on next series-touch only", or is there a missing `AutoTaggingService.ReEvaluateAll()` job that should exist? (Blocks Phase 1 events wiring.)
- **AT-Q2**: Several specs dereference series fields without null-check (`series.Network.EqualsIgnoreCase`, `series.OriginalCountry.EqualsIgnoreCase`). Confirm `EqualsIgnoreCase` extension is null-safe; if not, document NPE risk for porter.
- **AT-Q3**: Should `AutoTagsUpdatedEvent` cause re-evaluation? UI consumers treat it as "Tag list changed, refresh" only. If the answer is "yes", the Go port should add the missing handler.
- **AT-Q4**: Two `RootFolderSpecification` entries in one rule with `Required=true` make the rule un-satisfiable. Is this a foot-gun the validator should reject, or intentional (so users can build "always-false" rules to cleanly test removal)?
- **AT-Q5**: `OriginalCountrySpecification` validates length-3 only — accepts any 3-char string, e.g. `"xyz"`. Should the port validate against ISO-3166-1 alpha-3? (Probably not; preserve C# behavior to keep migration lossless.)
- **AT-Q6**: The `AutoTaggingSpecificationConverter` resolves spec types by class name with `Type.GetType($"...{typename}, Sonarr.Core", true)`. Renaming a Go-port spec class would break old DB rows. Strategy: keep a stable `kind` discriminator separate from the Go type name?
- **AT-Q7**: `Order = 2` for `SeriesTypeSpecification` while every other spec is `Order = 1`. `Order` is only used by the API schema endpoint (`AutoTaggingController.GetTemplates` orders by it). Why is SeriesType special? Just legacy ordering for the UI dropdown?

---

## 3. Tags

### Purpose
Single flat label registry. Every other entity that wants to be tag-filterable stores `HashSet<int>` of tag IDs. Tags are referenced from at minimum: Series, ImportLists, Notifications, Indexers, DownloadClients, DelayProfiles, ReleaseProfiles (`AllForTag` + `ExcludedTags`), AutoTagging.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `Tag` | `Tags/Tag.cs:5` | DB row. `Id`, `Label` (string). |
| `TagDetails` | `Tags/TagDetails.cs:7` | API view. `Label` plus 9 `List<int>` collections of consumer entity IDs and an `InUse` bool that's `Any` over all of them. |
| `ITagRepository` / `TagRepository` | `Tags/TagRepository.cs:9,16` | `IBasicRepository<Tag>` plus `GetByLabel(string)` (throws if missing), `FindByLabel(string)` (returns null), `GetTags(HashSet<int>)`. |
| `ITagService` / `TagService` | `Tags/TagService.cs:17,30` | CRUD + Details (single + all). |
| `TagsUpdatedEvent` | `Tags/TagsUpdatedEvent.cs:5` | Empty marker — Tag CRUD only. AutoTag mutations raise `AutoTagsUpdatedEvent` separately. |

### Public services
- `TagService.Add(Tag)` — find-by-label first; if exists, return existing. Otherwise lowercase label and Insert. Always raises `TagsUpdatedEvent` (even on duplicate-add no-op? — yes, line 170; subtle bug, see TAGS-Q1). Lowercases the label (line 167).
- `TagService.Update(Tag)` — lowercase label, repo.Update, raise event. No uniqueness check on update — see TAGS-Q2.
- `TagService.Delete(int)` — first calls `Details(tagId)`; if `details.InUse`, throw `ModelConflictException`. Otherwise delete and raise event.
- `TagService.GetTag(string)` — if all-digits, parse and `repo.Get(int)`; else `repo.GetByLabel`. Throws on label-miss (see TAGS-Q3).
- `TagService.Details(int)` — N+1 queries. Walks every consumer service (`IDelayProfileService.AllForTag`, `INotificationFactory.AllForTag`, etc.) and produces a `TagDetails`.
- `TagService.Details()` — bulk version. Calls `All()` once on each consumer factory and aggregates client-side (`TagService.cs:117-151`).

### DB tables
- `Tags` — `Id`, `Label` (NOT NULL, but **not** unique at the DB level). Created in `Datastore/Migration/066_add_tags.cs:11-12`.
- `Series.Tags`, `Notifications.Tags`, `Indexers.Tags`, `DownloadClients.Tags`, `DelayProfiles.Tags`, `ReleaseProfiles.{Tags,ExcludedTags}`, `ImportLists.Tags`, `AutoTagging.Tags`, `Metadata.Tags` (Ignored on read — `Datastore/TableMapping.cs:107`) — every consumer stores `List<int>` JSON.
- `Datastore/Migration/079_dedupe_tags.cs` — earlier migration that deduped `Tags.Label` rows. The migration normalized labels but did not add a UNIQUE constraint, which is why duplicates can re-occur via direct DB write.

### Label normalization
- `Add` lowercases via `Label.ToLowerInvariant()` (line 167).
- `Update` lowercases (line 177).
- No trim. Leading/trailing whitespace is preserved.
- API V3 controller validates `^[a-z0-9-]+$` case-insensitive (`Sonarr.Api.V3/Tags/TagController.cs:31`). V5 has the same regex (verified separately).
- `FindByLabel` / `GetByLabel` are case-sensitive at the SQL level; they only "work" because the writes are pre-lowercased.

### Cascade behavior on delete
`TagService.Delete` checks `Details(tagId).InUse` and throws on conflict. No automatic cascade. But:
- The housekeeper `Housekeeping/Housekeepers/CleanupUnusedTags.cs:11` runs on the `HousekeepingCommand` schedule (daily) and DELETEs any `Tags.Id` that is not referenced by `Series`, `Notifications`, `DelayProfiles`, `ReleaseProfiles`, `ImportLists`, `Indexers`, `AutoTagging`, or `DownloadClients`, or referenced inside an `AutoTagging` JSON-spec body via `TagSpecification`. So orphan-tag rows get garbage-collected eventually.
- Note: `MetadataDefinition.Tags` is **not** scanned by the housekeeper (it's `Ignore`d in TableMapping). Tags referenced only by Metadata rows would get GC'd. See TAGS-Q4.
- Note: `ReleaseProfiles.ExcludedTags` is also referenced only via the `Tags` JSON column in this housekeeper — but `ExcludedTags` is a separate column. Verify the housekeeper covers both. (`CleanupUnusedTags.cs:30` lists `"ReleaseProfiles"` as a table to scan, but `GetUsedTags` reads `"Tags"` column only.) See TAGS-Q5.

### Events raised
| Event | Producer | Payload | Consumers |
| --- | --- | --- | --- |
| `TagsUpdatedEvent` | `TagService.{Add,Update,Delete}` | (empty) | `Sonarr.Api.V3/V5.Tags.TagController` (SignalR `tag/sync`). No service-side handlers. |

### Quirks / TODOs / HACKs
- **`TagsUpdatedEvent` raised on duplicate-add**: `Add` returns existing tag without insert when the label already exists (`TagService.cs:160-165`), but the event is still raised UNCONDITIONALLY at line 170 — wait, re-read: actually the early `return existingTag;` at line 164 does prevent the event from firing. **Correction: the early return prevents the event.** No bug. Strike TAGS-Q1, but document the early return.
- **`Update` does not check label-uniqueness**. Renaming tag B to label of existing tag A produces two rows with the same label. Subsequent `GetByLabel` returns one or the other arbitrarily (`SingleOrDefault` will throw if both, actually — see `TagRepository.cs:25,37`). See TAGS-Q2.
- **`GetByLabel` throws** if not found (`TagRepository.cs:29-31`); `FindByLabel` returns null. `GetTag(string)` calls `GetByLabel`, so a stringly-typed call with an unknown label crashes the request. Most call sites use `GetTag(int)`.
- **`Tags.Label` is not UNIQUE at the DB level**. Migration `079_dedupe_tags` did a one-time dedup but did not add a constraint. Multi-instance write races can re-introduce duplicates.
- **Housekeeper coverage**: see TAGS-Q4 / TAGS-Q5 notes above.
- **`TagDetails.InUse` excludes `Metadata.Tags`** — by omission of `Metadata` from `TagDetails`. So a tag used only by a metadata writer can be deleted without warning, then garbage-collected on the next housekeeping run. See TAGS-Q4.

### Test files
- `src/NzbDrone.Core.Test/Housekeeping/Housekeepers/CleanupUnusedTagsFixture.cs`
- (No dedicated `TagServiceFixture` — covered indirectly.)

### Open questions (Tags)
- **TAGS-Q1**: ~Is `TagsUpdatedEvent` raised on duplicate-add?~ Resolved by re-read of `TagService.cs:160-172` — it is not. Documented.
- **TAGS-Q2**: `Update` does not check label uniqueness. Should the Go port add that, or preserve C# behavior to avoid regressing migrations? (Suggest: add a unique check + DB UNIQUE constraint, accept a small migration.)
- **TAGS-Q3**: `GetByLabel` throws `InvalidOperationException` on miss; `FindByLabel` returns null. The repo interface conflates these. Should the Go port standardize on a `(*Tag, error)` shape with `ErrNotFound`?
- **TAGS-Q4**: `MetadataDefinition.Tags` is `Ignore`d in TableMapping (`Datastore/TableMapping.cs:107`) — i.e. metadata writers cannot actually filter by tag. Is this dead code or a planned feature? `TagDetails` does not include `MetadataIds`.
- **TAGS-Q5**: `CleanupUnusedTags` housekeeper scans `"ReleaseProfiles"` for `Tags` column but does not scan `ExcludedTags`. A tag referenced ONLY via `ExcludedTags` may be GC'd. Need to verify against `ReleaseProfile` schema (`Profiles/Releases/ReleaseProfile.cs`) and migration history.
- **TAGS-Q6**: API V3/V5 regex `^[a-z0-9-]+$` allows `-` but not `_` — UI shows lowercase label. Server-side `Add` does `.ToLowerInvariant()` regardless. Should validation also lower-case the input pre-regex, or is the IgnoreCase flag enough?

---

## 4. RootFolders

### Purpose
Registered top-level series-storage paths. Each `RootFolder` row holds only `Path`; the rest of the model (`Accessible`, `IsEmpty`, `FreeSpace`, `TotalSpace`, `UnmappedFolders`) is recomputed on every read by walking the disk.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `RootFolder` | `RootFolders/RootFolder.cs:6` | DB row. `Path` (string), `Accessible` (bool, transient), `IsEmpty` (bool, transient), `FreeSpace` (long?, transient), `TotalSpace` (long?, transient), `UnmappedFolders` (`List<UnmappedFolder>`, transient). |
| `UnmappedFolder` | `RootFolders/UnmappedFolder.cs:3` | DTO. `Name`, `Path`, `RelativePath`. |
| `IRootFolderRepository` / `RootFolderRepository` | `RootFolders/RootFolderRepository.cs:6,10` | `IBasicRepository<RootFolder>` with `PublishModelEvents = true` (line 17) — emits generic model-changed events on the event bus. |
| `IRootFolderService` / `RootFolderService` | `RootFolders/RootFolderService.cs:16,26` | CRUD plus `GetBestRootFolderPath`, `AllWithUnmappedFolders`. |

### Public services
- `RootFolderService.All()` — DB read, no enrichment.
- `RootFolderService.AllWithUnmappedFolders()` — DB read; per-row enrichment via `GetDetails(folder, seriesPaths, timeout:true)`. Wraps each enrichment in try/catch — failures null out `UnmappedFolders` and continue (`RootFolderService.cs:88-92`).
- `RootFolderService.Add(RootFolder)` — validates: non-empty, rooted (`Path.IsPathRooted`), folder exists, no duplicate (`PathEquals`), folder writable. Then Insert + GetDetails + cache clear. Throws on any failure.
- `RootFolderService.Get(int, bool timeout)` — get by ID, then GetDetails with the supplied timeout. The `timeout` query param defaults to true (5 s), but the API endpoint allows `?timeout=false` (`Sonarr.Api.V5/RootFolders/RootFolderController.cs:47`).
- `RootFolderService.Remove(int)` — Delete + cache clear.
- `RootFolderService.GetBestRootFolderPath(string path)` — cached for 1 day. Returns longest path-prefix match from the registered roots; if no match, returns `OsPath(path).Directory.GetCleanPath()` (i.e. parent directory of the supplied path).

### Validation rules (Add)
1. `string.IsNullOrWhiteSpace(rootFolder.Path)` → `ArgumentException("Invalid path")`.
2. `!Path.IsPathRooted(rootFolder.Path)` → same.
3. `!_diskProvider.FolderExists(...)` → `DirectoryNotFoundException("Can't add root directory that doesn't exist.")`.
4. `all.Exists(r => r.Path.PathEquals(rootFolder.Path))` → `InvalidOperationException("Recent directory already exists.")` (note typo: "Recent" should be "Root").
5. `!_diskProvider.FolderWritable(...)` → `UnauthorizedAccessException("...is not writable by user '<env>'")`.

V3/V5 controllers (`Sonarr.Api.V3/RootFolders/RootFolderController.cs:32-41`, `Sonarr.Api.V5/RootFolders/RootFolderController.cs:33-42`) layer on top of these via FluentValidation: `IsValidPath`, `RootFolderValidator`, `MappedNetworkDriveValidator`, `StartupFolderValidator`, `RecycleBinValidator`, `PathExistsValidator`, `SystemFolderValidator`, `FolderWritableValidator`. (The validators themselves are defined under `NzbDrone.Core/Validation/Paths/`.) The controller is the only check that prevents nesting / reserved-system paths — the service-level `Add` does NOT check nesting (RF-Q1).

### `UnmappedFolder` computation
`GetUnmappedFolders` (`RootFolderService.cs:137-183`):
1. Read `NamingConfig.SeriesFolderFormat`; count `Path.DirectorySeparatorChar` occurrences = `subFolderDepth`.
2. List directories at `path`, then descend `subFolderDepth` levels (so a series-folder format like `{Group}/{Title}` → 1 level deep; default `{Title}` → 0 levels).
3. Take the descended list and `Except` the set of `seriesPaths.Values` using `PathEqualityComparer.Instance`.
4. Convert to `UnmappedFolder { Name = di.Name, Path = di.FullName, RelativePath = path.GetRelativePath(di.FullName) }`.
5. Remove any whose lowercased `DirectoryInfo.Name` matches a hard-coded special-folder set: `$recycle.bin`, `system volume information`, `recycler`, `lost+found`, `.appledb`, `.appledesktop`, `.appledouble`, `@eadir`, `.grab` (`RootFolderService.cs:36-47`).
6. Order by `Name` (invariant-culture, ignore-case).

When invalidated:
- The `_cache` (which only stores `GetBestRootFolderPath` results) is cleared on Add/Remove. `UnmappedFolders` itself is never cached — every API call hits the disk.
- `GetDetails` runs in `Task.Run(...).Wait(5000ms)` (`RootFolderService.cs:202-213`) so a slow / hung mount blocks the request for at most 5 s.

### Path normalization
- `PathEquals` (`src/NzbDrone.Common/Extensions/PathExtensions.cs:55`) does Unicode normalization (`Normalize()`), then compares with `PathStringComparison` (`OrdinalIgnoreCase` on Windows, `Ordinal` elsewhere — `src/NzbDrone.Common/Disk/DiskProviderBase.cs:18-29`), then re-compares the `CleanFilePath` form (which trims trailing slashes; for UNC paths only trims slashes).
- `OsPath` (used throughout the disk layer) handles trailing separator normalization differently per OS.
- `GetCleanPath` strips trailing `/` (and `\` for non-UNC paths).
- For Linux, `PathEquals` is case-sensitive (`Ordinal`). For Windows, it is case-insensitive. For macOS the comparer is `Ordinal` even though the default APFS volume is case-insensitive — see RF-Q2.

### DB tables
- `RootFolders` — `Id`, `Path` (UNIQUE). Created in `Datastore/Migration/001_initial_setup.cs:15-16`. Mapped (with the four transient props ignored) at `Datastore/TableMapping.cs:63-67`.

### Events raised / consumed
- `RootFolderRepository.PublishModelEvents = true` → `BasicRepository` raises generic `ModelEvent<RootFolder>` on Insert/Update/Delete. The producer is `BasicRepository.PublishEvent` (Datastore card). Consumers: any `IHandle<ModelEvent<RootFolder>>` (none in-tree at first scan; SignalR is wired through the controller's `RestControllerWithSignalR` base).

### Quirks / TODOs / HACKs
- "Recent directory already exists" typo (line 114) — `Add`'s duplicate-error message says "Recent" instead of "Root".
- Service-level `Add` does NOT check whether the new path is nested inside an existing root, nor whether an existing root is nested inside the new path. Only the controller-level `RootFolderValidator` does — bypassing the API (e.g. direct DB insert in tests) leaves nested roots possible (RF-Q1).
- `GetDetails` runs Task.Run + .Wait — synchronous wrapper around async work. Bridges to `IDiskProvider` calls that are themselves blocking. Net effect: a 5 s timeout. Could be replaced with a CancellationToken in the Go port.
- `UnmappedFolders` recursion depth = `count of DirectorySeparator in SeriesFolderFormat`. If the user sets a multi-level naming format then renames an existing root, descent depth changes — old folders are no longer unmapped because they're at the wrong depth (RF-Q3).
- `_cache` only caches `GetBestRootFolderPath`. The free-space / unmapped read path is uncached at the service level — every UI tick hits the disk.
- `GetBestRootFolderPathInternal` returns `OsPath(path).Directory.ToString().GetCleanPath()` if no root matches (`RootFolderService.cs:217-223`). I.e. for paths outside ANY registered root, it returns the parent directory — which is then used (e.g.) by `AutoTaggingService.GetTagChanges` to set `series.RootFolderPath`. If the user has no roots configured, every series's `RootFolderPath` becomes its parent dir, and `RootFolderSpecification` becomes useless (RF-Q4).
- `UnmappedFolder.RelativePath` uses `path.GetRelativePath(di.FullName)` — the extension throws `NotParentException` if the child is not under the parent. Since `GetDirectories(path)` returns children under `path`, this should not fire — but symlinks could escape (RF-Q5).
- DB column is `Path` (UNIQUE) but case-sensitivity of UNIQUE depends on collation. SQLite default is `BINARY` (case-sensitive); Postgres default is case-sensitive `text`. Two roots that differ only in case can co-exist on Linux but `PathEquals` may treat them as duplicates on Windows.

### Test files
- `src/NzbDrone.Core.Test/RootFolderTests/RootFolderServiceFixture.cs`
- `src/NzbDrone.Core.Test/RootFolderTests/GetBestRootFolderPathFixture.cs`

### Open questions (RootFolders)
- **RF-Q1**: Service-level `Add` doesn't check nesting; only the V3/V5 controller does. Should the Go port move all path validation into the service so non-API callers get the same check?
- **RF-Q2**: `PathStringComparison` is `Ordinal` on macOS even though APFS is case-insensitive by default. Two macOS paths that differ only in case will be treated as different by `PathEquals`. Intentional or bug? Same issue in Go's `filepath.Clean` — port needs to decide.
- **RF-Q3**: `UnmappedFolders` depth = separator-count in `SeriesFolderFormat`. Changing the naming format invalidates the unmapped-folder list silently. Is this acceptable, or should the port emit a `RescanRootFolders` job after `NamingConfig` changes?
- **RF-Q4**: `GetBestRootFolderPath` falls back to `parent(path).GetCleanPath()` when no root matches. This makes `RootFolderSpecification` useless when no roots are configured. Is this fallback documented anywhere user-facing?
- **RF-Q5**: Symlink escape from a registered root may produce `UnmappedFolder` entries whose `RelativePath` calls explode. Should the port resolve symlinks first (like `Mono/Disk/SymLinkResolver`)?
- **RF-Q6**: `Add` requires `FolderWritable`, but the DB `Path` is `UNIQUE` (case-sensitively in SQLite/PG by default). On Windows, "/foo" and "/Foo" registered separately would dup-pass at the SQL level but fail the in-memory `PathEquals` check inside `Add`. Concurrent `Add` of the same path could produce two rows. Should the Go port add a transactional unique-or-exists guard?

---

## 5. RemotePathMappings

### Purpose
A `(host, RemotePath) → LocalPath` translation table consulted whenever a download client reports a path that lives on a different host (e.g. SABnzbd on machine A, Sonarr on machine B). The service offers two directions:
- `RemapRemoteToLocal(host, remotePath) → localPath` — used when import-scanning a finished download.
- `RemapLocalToRemote(host, localPath) → remotePath` — used when telling a download client where to find a file Sonarr already has.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `RemotePathMapping` | `RemotePathMappings/RemotePathMapping.cs:5` | DB row. `Host`, `RemotePath`, `LocalPath`. |
| `IRemotePathMappingRepository` / `RemotePathMappingRepository` | `RemotePathMappings/RemotePathMappingRepository.cs:6,10` | `IBasicRepository<RemotePathMapping>` with `PublishModelEvents = true`. |
| `IRemotePathMappingService` / `RemotePathMappingService` | `RemotePathMappings/RemotePathMappingService.cs:13,25` | CRUD + RemapRemoteToLocal + RemapLocalToRemote. |

### Algorithm (mapping)
`RemapRemoteToLocal` (`RemotePathMappingService.cs:128-157`):
1. If `remotePath.IsEmpty` → return as-is.
2. Read all mappings (cached 10 s, `cache.Get("all", ..., TimeSpan.FromSeconds(10))` line 48).
3. **Iterate in DB order; first match wins**. Match condition: `host equals(IgnoreCase, Invariant) mapping.Host && OsPath(mapping.RemotePath).Contains(remotePath)`.
4. Compute: `localPath = OsPath(mapping.LocalPath) + (remotePath - OsPath(mapping.RemotePath))`. Returns the rebased path.
5. No match → return `remotePath` unchanged.

`RemapLocalToRemote` mirrors this with `LocalPath` and `RemotePath` swapped (`RemotePathMappingService.cs:159-188`).

**Notable: the algorithm is FIRST-MATCH on iteration order, NOT longest-prefix**. If the user adds a mapping for `/downloads/` and later `/downloads/tv/`, and the rows are returned in insert-order, `/downloads/tv/foo` will match the wider `/downloads/` mapping first. See RPM-Q1.

### Direction
- Both directions are implemented in the same service. Callers pick.
- Used in: `Download/TrackedDownloadService` (and `CompletedDownloadService`) for inbound paths; `Download/UsenetDownloadHandler` and friends for outbound. (Cross-reference `download.md` card.)

### Validation rules
`ValidateMapping` (`RemotePathMappingService.cs:92-126`):
1. `Host` not empty/whitespace.
2. `RemotePath` does not start with a literal space.
3. `RemotePath` not empty.
4. `LocalPath` not empty AND `LocalPath.IsRooted`.
5. `_diskProvider.FolderExists(localPath.FullPath)` — local path must exist at the moment of save.
6. No duplicate `(Host, RemotePath)` in the existing list.

Add/Update both normalize the inputs via `OsPath(value).AsDirectory().FullPath` (line 53-54, 79) — this appends the OS separator so prefix matching works correctly.

### DB tables
- `RemotePathMappings` — `Id`, `Host`, `RemotePath`, `LocalPath`. Created in `Datastore/Migration/063_add_remotepathmappings.cs:11-14`. Mapped at `Datastore/TableMapping.cs:155`.

### Cache
- `_cache.Get("all", ..., TimeSpan.FromSeconds(10))` — 10-second time-based cache. Cleared explicitly on Add/Remove/Update.

### Events raised / consumed
- `RemotePathMappingRepository.PublishModelEvents = true` → generic `ModelEvent<RemotePathMapping>`.
- No subsystem-specific event class.

### Quirks / TODOs / HACKs
- **Iteration-order match, not longest-prefix**: see RPM-Q1.
- Validation forces `_diskProvider.FolderExists(localPath)` — so saving a mapping for a remote machine whose local path does not yet exist on Sonarr's host fails immediately. Probably correct for "Sonarr-side local path", but if Sonarr is the remote in the inbound case, the failure is misleading.
- `RemotePath.StartsWith(" ")` is a special-cased error message; arbitrary leading whitespace is not. Why only space? See RPM-Q2.
- 10-second cache is short — more frequent than the actual DB invalidation events. Probably fine for size of this table (typically <10 rows) but the trade-off is undocumented.
- `OsPath.Contains` semantics depend on platform separators — Windows-style `\` vs Unix-style `/`. A mapping authored on Windows will not match a Unix path. (cross-reference `OsPath` in `common.md`).
- `host.Equals(mapping.Host, StringComparison.InvariantCultureIgnoreCase)` — host comparison ignores case, but does NOT trim whitespace.

### Test files
- `src/NzbDrone.Core.Test/RemotePathMappingsTests/RemotePathMappingServiceFixture.cs`
- `src/NzbDrone.Core.Test/HealthCheck/Checks/RemotePathMappingCheckFixture.cs`

### Open questions (RemotePathMappings)
- **RPM-Q1**: First-match algorithm vs longest-prefix. Is the behavior actually "first match wins" in DB-row order, or is there an implicit ordering guarantee? Should the Go port sort by `len(RemotePath) DESC` to make longest-prefix the behavior?
- **RPM-Q2**: Why is `RemotePath.StartsWith(" ")` a specific error vs general whitespace check? Mistake or compatibility shim?
- **RPM-Q3**: Local path validity is checked at save time. Is there an ongoing health check that re-validates? (`RemotePathMappingCheckFixture.cs` suggests yes — cross-reference `system.md` health-check card.)
- **RPM-Q4**: `OsPath.Contains` cross-platform semantics — when a Windows Sonarr connects to a Linux SAB, the mappings store mixed separators. Is normalization (`AsDirectory().FullPath`) sufficient, or does the porter need explicit per-OS handling?

---

## 6. DiskSpace

### Purpose
Read-only "free + total bytes per disk" rollup, surfaced at `GET /api/v3|v5/diskspace` for the dashboard's "Disk Space" widget. There is no DB persistence.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `DiskSpace` | `DiskSpace/DiskSpace.cs:3` | DTO. `Path`, `Label`, `FreeSpace`, `TotalSpace`. |
| `IDiskSpaceService` / `DiskSpaceService` | `DiskSpace/DiskSpaceService.cs:14,19` | Single method `GetFreeSpace()`. |

### Algorithm
`DiskSpaceService.GetFreeSpace` (`DiskSpace/DiskSpaceService.cs:36-48`):
1. `importantRootFolders` = path-roots of every series's path (mapped through `RootFolderService.GetBestRootFolderPath` and then `_diskProvider.GetPathRoot`), distinct, only paths that actually `FolderExists`.
2. `optionalRootFolders` = paths from `_diskProvider.GetMounts()` filtered to `DriveType.Fixed` or `DriveType.Network`, minus `importantRootFolders`, minus anything matching the regex `^/var/lib/(docker|rancher|kubelet)(/|$)|^/(boot|etc)(/|$)|/docker(/var)?/aufs(/|$)|/\.timemachine` (`DiskSpaceService.cs:26`).
3. For each path in (`importantRootFolders` then `optionalRootFolders`), call `_diskProvider.GetAvailableSpace(path)` + `GetTotalSize(path)` + `GetVolumeLabel(path)`. Skip rows where either size is null. Catch+log per-row exceptions; suppress for optional folders.
4. Sort by `Path` (ordinal-ignore-case).

### How free/total space is computed per OS

#### Linux/macOS — `NzbDrone.Mono.Disk.DiskProvider`
`GetAvailableSpace(path)` (`src/NzbDrone.Mono/Disk/DiskProvider.cs:39-52`) → `GetMount(path)?.AvailableFreeSpace`. `GetMount` first calls `_symLinkResolver.GetCompleteRealPath(path)` to resolve symlinks, then walks `GetAllMounts()` for the longest matching mount.

`GetTotalSize(path)` (line 226-233) → `GetMount(path)?.TotalSize`.

`GetAllMounts` (lines 167-205) merges:
- **`/proc/mounts`** parsed by `ProcMountProvider.GetMounts()` (`src/NzbDrone.Mono/Disk/ProcMountProvider.cs:37-58`). Each line is space-split; octal escapes `\NNN` re-expanded; `name`, `mount`, `type`, `options` extracted. Lines whose `mount` does not start with `/` (e.g. namespace pseudo-mounts) are dropped (line 113-118). Drive type derived from `_fixedTypes` (`ext3,ext2,ext4,vfat,fuseblk,xfs,jfs,msdos,ntfs,minix,hfs,hfsplus,qnx4,ufs,btrfs`), `_networkDriveTypes` (`cifs,nfs,nfs4,nfsd,sshfs`), and the contents of `/proc/filesystems`.
- **`DriveInfo.GetDrives()`** filtered to `Fixed | Network | Removable`, deduped by `RootDirectory`.

`IsSpecialMount` (`src/NzbDrone.Mono/Disk/DiskProvider.cs:207-224`) excludes any mount whose `RootDirectory` starts with `/var/lib/` (Docker on ZFS) or `/snap/`.

The `IMount.AvailableFreeSpace` and `TotalSize` properties of `ProcMount` ultimately defer to `DriveInfo` underneath — so the Linux path goes through .NET's wrapper around `statvfs` (the runtime detail; not directly visible in source). See DS-Q1.

#### Windows — `NzbDrone.Windows.Disk.DiskProvider`
`GetAvailableSpace(path)` (`src/NzbDrone.Windows/Disk/DiskProvider.cs:45-57`) — calls `GetPathRoot` (which itself first checks for a reparse point and returns its `RootDirectory` if so), then `DriveFreeSpaceEx(root)`. `DriveFreeSpaceEx` (line 135-150) is a P/Invoke into `kernel32.dll!GetDiskFreeSpaceEx` returning `lpFreeBytesAvailable` (free bytes available to the calling user, accounting for quota).

`GetTotalSize(path)` mirrors via `DriveTotalSizeEx` returning the second out-param.

`GetMount(path)` (`Windows/Disk/DiskProvider.cs:29-34`) returns a `FolderMount` for any path whose `DirectoryInfo` (or any ancestor) has `FileAttributes.ReparsePoint`, otherwise falls through to `DiskProviderBase.GetMount` (which iterates `GetMounts()`).

#### Cross-platform fallback — `DiskProviderBase`
`GetMounts()` (`src/NzbDrone.Common/Disk/DiskProviderBase.cs:473-475`) → `GetAllMounts().Where(d => !IsSpecialMount(d))`. Default `GetAllMounts` (line 478-484) wraps `DriveInfo.GetDrives()` for `Fixed|Network|Removable`. Default `IsSpecialMount` returns false (line 486-489) — so the platform-specific override is what filters Docker / snap / etc.

### Excluded mount-point patterns
Two filter layers stack:

Layer 1 — inside `DiskSpaceService.GetFixedDisksRootPaths` (`DiskSpace/DiskSpaceService.cs:64-70`), the regex:
```
^/var/lib/(docker|rancher|kubelet)(/|$)|^/(boot|etc)(/|$)|/docker(/var)?/aufs(/|$)|/\.timemachine
```
- `/var/lib/docker`, `/var/lib/rancher`, `/var/lib/kubelet` (k8s data dirs).
- `/boot`, `/etc`.
- `/docker/aufs`, `/docker/var/aufs`.
- Any path containing `/.timemachine`.

Layer 2 — `Mono.Disk.DiskProvider.IsSpecialMount` (line 207-224):
- Any mount under `/var/lib/`.
- Any mount under `/snap/`.

So `/var/lib/foo` is excluded twice (once by Layer 2 because the mount itself is `/var/lib/...`, once by Layer 1 if it leaks through). The two filters do not need to agree — they're independent. See DS-Q2.

`ProcMountProvider`'s own filter (line 113-118) drops non-`/`-rooted mounts (e.g. `nsfs`, `proc`, `cgroup`, `tmpfs` when mounted at `/sys/...`). Pseudo-FSs that DO mount under `/` (e.g. `tmpfs` on `/run`) survive layer 0, are dropped at layer 1 if they don't appear in the fixed-types list AND they aren't `/dev/`-backed.

### DB tables
- None.

### Events raised / consumed
- None.

### Quirks / TODOs / HACKs
- The regex in `DiskSpaceService.cs:26` is hard-coded — Linux-only patterns appear even on Windows (where they will never match). Harmless but cluttering.
- `optionalRootFolders` exclusion list is hard-coded; can't be overridden by user. Users on Unraid / TrueNAS / synology may need additions (DS-Q3).
- `GetVolumeLabel` is best-effort — failures fall through to `null`. The DTO's `Label` is therefore nullable in practice but the C# field is `string` (will serialize as `null`). Verify `DiskSpaceResource` exposes nullability correctly.
- `_diskProvider.GetAvailableSpace` in the Mono path quietly returns `null` if no mount matches the resolved path (`DiskProvider.cs:46-49`). The service skips null rows (line 84). On a misconfigured system the dashboard widget can be empty.
- `Task.Run(...).Wait(5000)` pattern in `RootFolderService.GetDetails` is NOT used here — `DiskSpaceService.GetFreeSpace` runs synchronously. A hung mount can hang the request indefinitely. See DS-Q4.
- `_regexSpecialDrive` is `static readonly Regex` with `RegexOptions.Compiled` — fine.

### Test files
- `src/NzbDrone.Core.Test/DiskSpace/DiskSpaceServiceFixture.cs`

### Open questions (DiskSpace)
- **DS-Q1**: Linux `IMount.AvailableFreeSpace` defers to .NET's `DriveInfo`, which under the hood uses `statvfs(3)` (or `statfs(2)`). Confirm exact syscall and whether it accounts for filesystem-reserved blocks — the user-facing "free" number may differ from `df -h`.
- **DS-Q2**: Two-layer mount filter (`DiskSpaceService` regex + `Mono.DiskProvider.IsSpecialMount`). Is there ever a path the regex catches but `IsSpecialMount` doesn't, or vice versa? Should they merge?
- **DS-Q3**: Excluded-mount list is not user-configurable. Plenty of NAS distros mount user data under `/mnt/user/...` or `/srv/...` which is fine, but `/var/lib/` is an absolute exclude — users running Sonarr on a host where data lives under `/var/lib/sonarr-data/` lose dashboard visibility. Should the Go port expose the regex as config?
- **DS-Q4**: `GetFreeSpace` has no per-disk timeout. A hung NFS mount blocks the dashboard. Should the port wrap each `GetAvailableSpace` call in a `context.WithTimeout`?
- **DS-Q5**: Windows `DriveFreeSpaceEx` returns `lpFreeBytesAvailable` (quota-aware). Linux `statvfs` does not have a direct quota equivalent. Cross-OS the same series-folder may report different "free" numbers. Document for the porter.

---

## Cross-cutting concerns affecting other cards

1. **Event fan-out matrix.** `ConfigSavedEvent` is consumed by `TaskManager` (rebuilds RSS-sync + Backup intervals — see `system.md`), `LocalizationService`, `PendingReleaseService`, and the UI auth handler. The Go port's event bus must keep these subscriptions wired or behavior regresses silently.
2. **`UpdaterConfigProvider`'s silent rewrites** affect both this card AND `system.md`'s Update subsystem. The two cards must agree on which one owns the rewrite logic. Strong recommendation: keep it in `system.md` (Update) and document the cross-link here.
3. **Two parallel `TrustCgnatIpAddresses`** (CFG-Q1) — affects the auth subsystem (`Sonarr.Http.Authentication`). The auth card needs to declare which one wins.
4. **`Series.RootFolderPath` is computed on-demand** by `AutoTaggingService`, not persisted. `tv.md` already documents `RootFolderPath` as `Ignore`d on the entity; this card explains who lazily fills it. The Go port must replicate the timing or `RootFolderSpecification` breaks.
5. **`CleanupUnusedTags` housekeeper** (in `system.md`'s Housekeeping section) inspects the `AutoTagging` table, including JSON-embedded `TagSpecification.Value`. The Go port's housekeeping module needs to read the autotag JSON the same way; coupling the two is unavoidable.
6. **`PostgresOptions` env-only path** (CFG-Q2) — if any utility-mode command (`schema/Datastore`) depends on `PostgresOptions.GetOptions()`, it bypasses XML config. `datastore.md` should pick this up.
7. **`AutoTaggingSpecificationConverter` strict type-name resolution** ties `embedded-docs/catalogue.md` to this card. Renaming Go-port spec types breaks DB rows verbatim.
8. **`GetBestRootFolderPath` 1-day cache** in `RootFolderService` (`RootFolderService.cs:197`). The Go port's cache layer should match this duration or path-resolution drifts when the user adds a new root.
9. **`ProcMountProvider`** is parsed at first read and cached statically (`_fileSystems` field, `ProcMountProvider.cs:28`). The Go port can either parse on each call (cheap on Linux) or cache once at startup; the C# code does the latter. `native/inventory.md` should reflect this.
10. **`InvalidConfigFileException`** is the only fatal-startup config error path. `Bootstrap.cs:71-74` rethrows as `SonarrStartupException`. Other cards (housekeeping, lifecycle) should reference this for "fatal-startup" semantics.

---

## Open questions

### Configuration (CFG-Q*)
- **CFG-Q1** unresolved — `TrustCgnatIpAddresses` duplication. Blocks Phase 1 (auth wiring).
- **CFG-Q2** unresolved — `PostgresOptions.GetOptions()` env-only vs XML-aware path. Blocks Phase 1 (datastore).
- **CFG-Q3** unresolved — `ApiKey` silently dropped in `SaveConfigDictionary`. Blocks Phase 1 (settings API).
- **CFG-Q4** unresolved — case-handling in `ConfigRepository.Get`. Blocks Phase 1 (config service port).
- **CFG-Q5** unresolved — `ResetApiKeyCommand` does not raise `ConfigFileSavedEvent`. Blocks Phase 1 (event wiring).
- **CFG-Q6** unresolved — utility-mode vs host-mode IOptions binding parity. Blocks Phase 1 (host bootstrap).
- **CFG-Q7** unresolved — `persist:false` semantics for non-persistent keys. Blocks Phase 1 (settings UI / port).
- **CFG-Q8** unresolved — `RescanAfterRefreshType.AfterManual` definition. Blocks Phase 2 (refresh job semantics).
- **CFG-Q9** unresolved — `SaveConfigDictionary` non-transactional. Blocks Phase 1 (settings API).
- **CFG-Q10** unresolved — static cache vs per-instance. Blocks Phase 1 (config service port).

### AutoTagging (AT-Q*)
- **AT-Q1** unresolved — new rules don't back-fill. Blocks Phase 2 (autotag jobs).
- **AT-Q2** unresolved — null-safety of `Series.Network`/`OriginalCountry` in specs. Blocks Phase 2 (spec evaluation).
- **AT-Q3** unresolved — should `AutoTagsUpdatedEvent` trigger re-evaluation? Blocks Phase 2 (event wiring).
- **AT-Q4** unresolved — multi-`RootFolderSpecification` semantic ambiguity. Blocks Phase 2 (validation).
- **AT-Q5** unresolved — `OriginalCountrySpecification` validation strength. Phase 2 (validation parity).
- **AT-Q6** unresolved — type-name discriminator stability. Blocks Phase 1 (datastore JSON converter port).
- **AT-Q7** unresolved — `Order=2` for `SeriesTypeSpecification` rationale. Cosmetic; blocks nothing.

### Tags (TAGS-Q*)
- **TAGS-Q1** resolved — early-return prevents redundant event.
- **TAGS-Q2** unresolved — uniqueness on Update + DB UNIQUE. Blocks Phase 1 (tag service port).
- **TAGS-Q3** unresolved — throw-vs-null API of `GetByLabel` / `FindByLabel`. Blocks Phase 1 (tag service port).
- **TAGS-Q4** unresolved — `MetadataDefinition.Tags` is dead code? Blocks Phase 2 (metadata writers card).
- **TAGS-Q5** unresolved — `CleanupUnusedTags` housekeeper coverage of `ExcludedTags`. Blocks Phase 2 (housekeeping port).
- **TAGS-Q6** unresolved — label normalization vs validation order. Phase 2 (validation parity).

### RootFolders (RF-Q*)
- **RF-Q1** unresolved — service-level vs controller-level path validation. Blocks Phase 1 (rootfolder service port).
- **RF-Q2** unresolved — `Ordinal` comparison on case-insensitive macOS APFS. Blocks Phase 2 (path comparison parity).
- **RF-Q3** unresolved — `UnmappedFolders` recursion-depth invalidation. Blocks Phase 2 (naming-config event wiring).
- **RF-Q4** unresolved — `GetBestRootFolderPath` fallback is undocumented. Blocks Phase 2 (autotag spec evaluation parity).
- **RF-Q5** unresolved — symlink escape from registered root. Phase 2 (filesystem hardening).
- **RF-Q6** unresolved — concurrent `Add` race. Phase 1 (rootfolder repository port).

### RemotePathMappings (RPM-Q*)
- **RPM-Q1** unresolved — first-match vs longest-prefix algorithm. Blocks Phase 1 (rmpm service port).
- **RPM-Q2** unresolved — leading-space-only validation. Cosmetic; Phase 1 (validation parity).
- **RPM-Q3** unresolved — runtime re-validation via health check. Cross-references `system.md` health check. Phase 2.
- **RPM-Q4** unresolved — cross-OS path-separator semantics. Blocks Phase 2 (download client integration).

### DiskSpace (DS-Q*)
- **DS-Q1** unresolved — Linux `statvfs` vs `statfs` choice and reserved-block handling. Blocks Phase 1 (disk-provider port).
- **DS-Q2** unresolved — two-layer mount filter consolidation. Phase 1 (disk-provider port).
- **DS-Q3** unresolved — user-configurable exclude list. Phase 2 (UX improvement).
- **DS-Q4** unresolved — per-disk timeout for hung mounts. Blocks Phase 2 (dashboard reliability).
- **DS-Q5** unresolved — Windows quota-aware vs Linux non-quota free-space. Phase 2 (cross-OS parity docs).
