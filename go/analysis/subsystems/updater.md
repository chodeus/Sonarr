# Updater (`NzbDrone.Update` standalone binary)

**Source:** `src/NzbDrone.Update/**`
**Target Go package(s):** `go/cmd/sonarr-update/...` (entry-point), `go/internal/updater/...` (engine)
**Status:** drafted

## Purpose

`NzbDrone.Update` is a *separate* executable assembly (`Sonarr.Update.dll`, `OutputType=WinExe` per `Sonarr.Update.csproj:3`) that the in-core `InstallUpdateService` (Sonarr.Core, see `system.md` §9) shells out to in order to actually replace the installed Sonarr files on disk. It cannot live inside the main process because the very files it needs to overwrite are loaded by that process — so it ships as its own binary that runs after the parent Sonarr has terminated.

The flow at a glance: in-core `InstallUpdateService` downloads + verifies + extracts the update tarball / zip into a sandbox folder, copies the extracted `Sonarr.Update/*` sub-folder out to the parent of the sandbox, then `Process.Start`s `Sonarr.Update[.exe]` with the parent's PID + sandbox path + the parent's executable path. The standalone `Sonarr.Update` then waits for the parent PID to die, mirrors the new files over the install dir, and re-launches Sonarr.

The standalone has its own DI container (a *minimal* DryIoc setup that only scans the `Sonarr.Update` assembly itself plus `Sonarr.Common` and the per-OS native assembly), its own NLog config writing to `<AppData>/UpdateLogs/yyyy.MM.dd-HH.mm.txt`, and its own Sentry DSN distinct from the main app.

This card covers ONLY the standalone binary. The in-core decision logic (when to update, version-checking, Skyhook-served `UpdatePackage`, branch redirection, install-script handoff, recycle of old update logs) is documented in `subsystems/system.md` §9. Cross-citations point there.

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `UpdateApp` | `src/NzbDrone.Update/UpdateApp.cs:17` | class with `Main` | Entry-point and the per-instance class resolved from DI to drive the update. |
| `AppType` | `src/NzbDrone.Update/AppType.cs:3` | enum | `Normal, Console, Service`. Drives the re-launch path. |
| `UpdateStartupContext` | `src/NzbDrone.Update/UpdateStartupContext.cs:3` | DTO | `ProcessId`, `ExecutingApplication`, `UpdateLocation`. Populated from `argv` by `UpdateApp.ParseArgs`. |
| `IInstallUpdateService` / `InstallUpdateService` | `src/NzbDrone.Update/UpdateEngine/InstallUpdateService.cs:11,16` | service | The orchestrator — implements the full update sequence top-to-bottom. **Distinct from** the same-named Core service `NzbDrone.Core.Update.InstallUpdateService` (`system.md` §9). |
| `IBackupAndRestore` / `BackupAndRestore` | `src/NzbDrone.Update/UpdateEngine/BackupAndRestore.cs:8,14` | service | Mirror current install → backup folder, restore on failure. |
| `IBackupAppData` / `BackupAppData` | `src/NzbDrone.Update/UpdateEngine/BackupAppData.cs:9,14` | service | Copies `config.xml` and `sonarr.db` to `<sandbox>/sonarr_appdata_backup/`. |
| `IDetectApplicationType` / `DetectApplicationType` | `src/NzbDrone.Update/UpdateEngine/DetectApplicationType.cs:7,12` | service | Picks `AppType` based on running services / processes / OS. |
| `IDetectExistingVersion` / `DetectExistingVersion` | `src/NzbDrone.Update/UpdateEngine/DetectExistingVersion.cs:7,12` | service | Reads `Sonarr.dll` `FileVersionInfo.ProductVersion` for log line. |
| `IStartNzbDrone` / `StartNzbDrone` | `src/NzbDrone.Update/UpdateEngine/StartNzbDrone.cs:12,17` | service | Re-launches Sonarr (Service / Console / WinForm) — Windows path. |
| `ITerminateNzbDrone` / `TerminateNzbDrone` | `src/NzbDrone.Update/UpdateEngine/TerminateNzbDrone.cs:10,15` | service | Stops the Windows service, kills the PID + named processes. |

## Entry-point: `UpdateApp.Main`

`UpdateApp.Main(string[] args)` (`src/NzbDrone.Update/UpdateApp.cs:30-53`) does in order:

1. `var startupArgument = new StartupContext(args)` — uses the Common `StartupContext` parser, same flag/arg form as the main app.
2. `NzbDroneLogger.Register(startupArgument, updateApp:true, inConsole:true)` — `updateApp:true` selects the Update-specific Sentry DSN and the file target under `UpdateLogs/` (`src/NzbDrone.Common/Instrumentation/NzbDroneLogger.cs:50-53,174-191`). `inConsole:true` is a no-op when `updateApp:true` (the console branch is in the `else`).
3. Logs `"Starting Sonarr Update Client"`.
4. Builds the DI container:
   ```csharp
   new Container(rules => rules.WithNzbDroneRules())
       .AutoAddServices(new List<string> { "Sonarr.Update" })
       .AddNzbDroneLogger()
       .AddStartupContext(startupArgument)
   ```
   Notably absent vs the main host: no `AddDatabase()`, no API/SignalR/Http/Core assemblies. `AutoAddServices(["Sonarr.Update"])` is the same code path as the main host (`src/NzbDrone.Common/Composition/Extensions.cs:23-39`), but `AssemblyLoader.Load` always appends `Sonarr.Common` and the OS-specific `Sonarr.Mono` / `Sonarr.Windows` (`AssemblyLoader.cs:22-23`) — so the updater also has access to `IDiskProvider`, `IDiskTransferService`, `IProcessProvider`, `IServiceProvider`, `IAppFolderInfo`, etc.
5. `container.Resolve<InitializeLogger>().Initialize()` — final NLog re-configuration (Core/Instrumentation — but note this `InitializeLogger` lives in `NzbDrone.Common.Instrumentation`, not Core, so the updater can resolve it without the Core assembly).
6. `container.Resolve<UpdateApp>().Start(args)` — kicks the update.
7. Catches *all* exceptions at the top level, logs Fatal, returns. Exit code is always `0` — there is no rich exit-code protocol back to the caller (UPD-Q1).

## `UpdateApp.Start` and argv parsing

`UpdateApp.Start(args)` (`UpdateApp.cs:55-61`):
- `var startupContext = ParseArgs(args)` — produces `UpdateStartupContext`.
- `var targetFolder = GetInstallationDirectory(startupContext)` — resolves install dir from the parent's `MainModule.FileName` if `ExecutingApplication` is empty, else from the `ExecutingApplication` arg.
- `_installUpdateService.Start(targetFolder, startupContext.ProcessId)`.

`ParseArgs(args)` (`UpdateApp.cs:63-99`):
- Throws `ArgumentOutOfRangeException` if `args` is null or empty.
- `args[0]` → `ProcessId` (must parse and be > 0; else throws).
- On non-Windows, if `args.Length >= 3`:
  - `args[1]` → `UpdateLocation` (the sandbox folder containing the new files).
  - `args[2]` → `ExecutingApplication` (path to the Sonarr executable that was running).
  - All other args are logged at debug but ignored.
- On Windows, only `args[0]` is read — the other two args may be passed (the in-core `GetUpdaterArgs` always passes them, see below) but are not used by `ParseArgs`. Instead the install dir is derived from the parent process's `MainModule.FileName` via `IProcessProvider.GetProcessById(processId).StartPath` (`UpdateApp.cs:117`). UPD-Q2.

The full argv contract from `NzbDrone.Core/Update/InstallUpdateService.cs:217-223` `GetUpdaterArgs`:

```text
Sonarr.Update.exe <pid> "<sandbox-folder>" "<executing-app-path>" <preserved-args>
```

where `<preserved-args>` is `IStartupContext.PreservedArguments` — a re-stringified `/data=…` and/or `/nobrowser` (`StartupContext.cs:61-79`). These flags persist across the update so the relaunched Sonarr keeps its data path and (forced by `StartNzbDrone`) does not pop a browser.

## Update sequence (`InstallUpdateService.Start`)

Top-to-bottom from `UpdateEngine/InstallUpdateService.cs:86-171`:

1. **Log existing version**. `_detectExistingVersion.GetExistingVersion(installationFolder)` (`DetectExistingVersion.cs:21-39`) reads `Path.Combine(targetFolder, "Sonarr.dll")` `FileVersionInfo.ProductVersion`. Returns `"(unknown)"` if `Sonarr.dll` not present or the read fails. Log line: `"Updating Sonarr from version {old} to version {BuildInfo.Version}"`.

2. **Verify pre-conditions** (`InstallUpdateService.cs:55-84`, called as `Verify(installationFolder, processId)` on line 91):
   - `installationFolder` non-empty and exists on disk.
   - `processId >= 1`.
   - Parent process with that PID exists (`IProcessProvider.Exists(processId)`).
   - The sandbox folder (`_appFolderInfo.GetUpdatePackageFolder()` = `<AppData>/sonarr_update/Sonarr/`) exists.

   Any failure throws and is caught at the top of `Main`.

3. **Special-case bin path**. If installation folder ends in `\bin\Sonarr` or `/bin/Sonarr` (Linux package layout), strip the trailing `bin/Sonarr` so the mirror copies into the parent install dir (`InstallUpdateService.cs:93-97`). This is the canonical Debian/RPM layout where `/usr/lib/sonarr/bin/Sonarr` is the binary and `/usr/lib/sonarr/` owns the install.

4. **Detect AppType**. `_detectApplicationType.GetAppType()` (`DetectApplicationType.cs:23-43`):
   - Non-Windows → `AppType.Normal` (the binary is `Sonarr` regardless of how it was started; "Console" was a Windows-specific distinction).
   - Windows + Sonarr service installed *and* running → `AppType.Service`.
   - Windows + a process named `Sonarr.Console` is running → `AppType.Console`.
   - Else → `AppType.Normal` (the WinForms tray binary `Sonarr.exe`).

5. **Pre-warm process lookups** (`InstallUpdateService.cs:101-102`) — calls `FindProcessByName` for both `Sonarr.Console` and `Sonarr` and discards the result. UPD-Q3.

6. **Windows: terminate Sonarr** (`InstallUpdateService.cs:104-107`). `_terminateNzbDrone.Terminate(processId)` (`TerminateNzbDrone.cs:28-62`):
   - Stops the Windows service if present and running.
   - `KillAll("Sonarr.Console")` and `KillAll("Sonarr")`.
   - On non-Windows, the same method exists but is *not* called yet — it runs only in the `finally` block (step 11).

7. **Backup**. Inside a `try`:
   - `_backupAndRestore.Backup(installationFolder)` (`BackupAndRestore.cs:27-31`) → `IDiskTransferService.MirrorFolder(installationFolder, <AppData>/sonarr_update/sonarr_backup/)`. Full mirror, not a copy.
   - `_backupAppData.Backup()` (`BackupAppData.cs:32-55`):
     - Empties or creates `<AppData>/sonarr_update/sonarr_appdata_backup/`.
     - `TransferFile(<AppData>/config.xml, <appdata-backup>/config.xml, Copy)`.
     - `TransferFile(<AppData>/sonarr.db, <appdata-backup>/sonarr.db, Copy)`.
     - `try`/`catch`: failure here logs an error but does not abort the update. UPD-Q4.

8. **Windows: re-check parent stayed dead** (`InstallUpdateService.cs:114-121`). After backup, before mirroring, double-check that no `Sonarr.Console` / `Sonarr` process resurfaced. If so, log `"Sonarr was restarted prematurely by external process"` and `return` — leaving the install dir un-mirrored but with a backup ready. UPD-Q5.

9. **Mirror new files** (`InstallUpdateService.cs:123-140`):
   - `_diskTransferService.MirrorFolder(_appFolderInfo.GetUpdatePackageFolder(), installationFolder)` — copies `<AppData>/sonarr_update/Sonarr/*` into the install dir. `MirrorFolder` semantics: identical files are skipped, missing files are removed (full sync — see `common.md` `IDiskTransferService`).
   - On macOS / Linux: `chmod 755` on `Sonarr` and `ffprobe` binaries (`InstallUpdateService.cs:128-133`).
   - On *any* exception during mirror: `_backupAndRestore.Restore(installationFolder)` (`BackupAndRestore.cs:33-38`) reverses the mirror from the backup folder back to the install dir, logs the rolled-back file count, and re-throws. **This is the only rollback path.** Rollback is *not* triggered by step 6's `Terminate` failing, by `BackupAppData` failing, by step 8's "premature restart", by step 11's `Terminate` failing, or by the post-mirror `chmod` failing.

10. **Re-launch (Windows)** (`InstallUpdateService.cs:144-147`, in the `finally`). `_startNzbDrone.Start(appType, installationFolder)` (`StartNzbDrone.cs:32-55`):
    - `AppType.Service` → `_serviceProvider.Start("Sonarr")`. If that throws `InvalidOperationException` (typical: insufficient permissions), warn-log and fall through to console.
    - `AppType.Console` → spawn `<install>/Sonarr.Console.exe` with `IStartupContext.PreservedArguments`.
    - `AppType.Normal` (tray) → spawn `<install>/Sonarr.exe`.
    - In all spawn paths, `StartNzbDrone.Start` first force-adds `nobrowser` to `_startupContext.Flags` so the relaunched Sonarr does not pop a browser tab (`StartNzbDrone.cs:78-81`). This mutation persists in the in-process `IStartupContext` for the rest of the updater's lifetime — but since the updater is single-shot it's a no-op for itself.

11. **Re-launch (non-Windows)** (`InstallUpdateService.cs:148-169`). The pattern is intentionally inverted vs Windows:
    - Call `_terminateNzbDrone.Terminate(processId)` *now* (this is the FIRST termination on Linux/macOS — step 6 was Windows-only).
    - Wait up to 10 × 1 s for a `"Sonarr"` process to reappear, on the assumption that systemd / launchd / supervisor will auto-restart the killed process (`InstallUpdateService.cs:152-163`). Variable name `theDakoLimit = 10` (UPD-Q6, etymology unknown).
    - If after 10 s no `"Sonarr"` process is running → fall back to `_startNzbDrone.Start(appType, installationFolder)` (which on non-Windows always spawns `Sonarr` per the `AppType.Normal` mapping in `StartNzbDrone.StartWinform` — `StartNzbDrone.cs:63-66`).

12. **Exit `Main`** with `Logger.Info("Update completed successfully")`. Process exits 0.

## `UpdateEngine/` files (full enumeration)

| File | Role |
|------|------|
| `UpdateEngine/InstallUpdateService.cs` | Orchestrator — see "Update sequence". |
| `UpdateEngine/BackupAndRestore.cs` | `Backup(source)` mirrors install dir → `<sandbox>/sonarr_backup/`. `Restore(target)` mirrors back. Pure file ops. |
| `UpdateEngine/BackupAppData.cs` | Copies `config.xml` + `sonarr.db` to `<sandbox>/sonarr_appdata_backup/`. Errors are warn-logged, not rethrown. |
| `UpdateEngine/DetectApplicationType.cs` | Returns `AppType` per OS / running service / process names. |
| `UpdateEngine/DetectExistingVersion.cs` | Reads `Sonarr.dll` product version for the human-readable "from … to …" log line. Used only for logging. |
| `UpdateEngine/StartNzbDrone.cs` | Re-launches Sonarr per `AppType` (Service via SCM / Console.exe / Sonarr.exe). Force-adds `nobrowser`. |
| `UpdateEngine/TerminateNzbDrone.cs` | Stops the Windows service if running, then `KillAll` for both process names. On non-Windows additionally `Kill(processId)`. |

There is **no** download, no archive extract, no checksum verify in `UpdateEngine/`. The download/verify/extract steps run in the parent process, before the updater is spawned — `NzbDrone.Core.Update.InstallUpdateService` (cross-cite `system.md` §9):

- Download: `IHttpClient.DownloadFile(updatePackage.Url, packageDestination)` (`NzbDrone.Core/Update/InstallUpdateService.cs:126`).
- Verify: SHA-256 of the downloaded file equals `UpdatePackage.Hash` from Skyhook (`NzbDrone.Core/Update/UpdateVerification.cs:20-28`). Hash is hex-compared `CurrentCultureIgnoreCase`. **Algorithm**: SHA-256 only. **What's verified**: the downloaded archive vs the hash served by the Skyhook update endpoint. There is no GPG / Authenticode / signature on the manifest itself — the trust root is HTTPS to `services.sonarr.tv`. Cross-cite `system.md` §9 and UPD-Q7.
- Extract: `IArchiveService.Extract(packageDestination, updateSandboxFolder)` (`NzbDrone.Core/Update/InstallUpdateService.cs:139`) — SharpZipLib zip / tar.gz (`common.md` `ArchiveService.cs`).
- Move the `Sonarr.Update/` sub-folder out of the extracted package to its sibling sandbox so the standalone updater can run from there (`NzbDrone.Core/Update/InstallUpdateService.cs:153`).
- chmod 755 on the updater itself on macOS/Linux (`NzbDrone.Core/Update/InstallUpdateService.cs:163-167`).
- Spawn `_processProvider.Start(updateClientExePath, GetUpdaterArgs(updateSandboxFolder))` (`NzbDrone.Core/Update/InstallUpdateService.cs:172`). **This is the handoff site**.

## `AppType` semantics

`AppType` (`AppType.cs:3-8`):
- `Normal` — default. Non-Windows always falls here. On Windows, this is the tray (`Sonarr.exe`) variant.
- `Console` — Windows-only. Detected by an existing `Sonarr.Console` process. Re-launched as `Sonarr.Console.exe`.
- `Service` — Windows-only. Detected by `IServiceProvider.ServiceExist("Sonarr") && IsServiceRunning("Sonarr")`. Re-launched via `IServiceProvider.Start("Sonarr")` (i.e. the Windows SCM `StartService` API) — does NOT spawn a process directly.

The updater needs `AppType` because Linux/macOS (`Normal` only) uses systemd/launchd auto-restart and does NOT directly relaunch (step 11 above). On Windows there is no auto-restart so the updater must spawn the right binary.

## `UpdateStartupContext` (args passed in)

| Field | Source | Used for |
|-------|--------|----------|
| `ProcessId` | `args[0]` | wait/kill the parent before mirror; verify parent existed at start; resolve install dir on Windows from `Process.GetProcessById(pid).StartPath` |
| `UpdateLocation` | `args[1]` (non-Windows only) | currently *parsed but unused* — `InstallUpdateService` reads `_appFolderInfo.GetUpdatePackageFolder()` instead, which is `<AppData>/sonarr_update/Sonarr/`. The arg duplicates this and is logged. UPD-Q8. |
| `ExecutingApplication` | `args[2]` (non-Windows only) | resolve install dir on non-Windows (`UpdateApp.GetInstallationDirectory:122-129`) — `Path.GetDirectoryName(args[2])`. |

The remainder of `argv` (`<preserved-args>` from the in-core call site) is parsed by `new StartupContext(args)` for `NzbDroneLogger.Register` — meaning `/data=…` is honoured by the updater for AppData path resolution (so `UpdateLogs/` and `sonarr_update/` go into the same custom AppData folder the parent was using).

## Verification (parent-side)

- Algorithm: SHA-256 over the downloaded archive.
- Comparison: hex string `string.Equals(... CurrentCultureIgnoreCase)` against `UpdatePackage.Hash` from Skyhook.
- **The standalone updater does not re-verify**. Once `Sonarr.Update.exe` runs, the package is trusted unconditionally. There is no signature on the *manifest* served by Skyhook — only HTTPS to the canonical hostname (Skyhook URL is set in `Sonarr.Common.Cloud.SonarrCloudRequestBuilder`, see `common.md`). UPD-Q7.

## Rollback

Rollback (`BackupAndRestore.Restore`) is triggered exactly once: when `MirrorFolder(<sandbox>/Sonarr, <install>)` throws inside the `try` at `InstallUpdateService.cs:123-140`. The inner `catch (Exception e)` calls `_backupAndRestore.Restore(installationFolder)` (which re-mirrors `<sandbox>/sonarr_backup/` back over `<install>/`) then re-throws.

Backup folder lifecycle:
- Created fresh at `<AppData>/sonarr_update/sonarr_backup/` on every update by `BackupAndRestore.Backup` (mirrored from current install).
- Never explicitly cleaned by the updater. The next update overwrites it via `MirrorFolder` (which removes files no longer present). Cleanup of the entire `sonarr_update/` sandbox happens in the parent's *next* update run (`NzbDrone.Core/Update/InstallUpdateService.cs:118-122`).

The `BackupAppData` (`config.xml` + `sonarr.db`) backup is **not** restored on rollback — the `Restore` method only handles the install-dir backup. UPD-Q4.

## Cross-OS branches

The updater has explicit per-OS conditionals — enumerated:

| Site | Windows | Linux/macOS |
|------|---------|-------------|
| `UpdateApp.ParseArgs` (`UpdateApp.cs:75-95`) | only `args[0]` (PID) read | also reads `args[1]`=sandbox, `args[2]`=executing app |
| `UpdateApp.GetInstallationDirectory` (`UpdateApp.cs:113-130`) | derive from parent's `Process.MainModule.FileName` if `ExecutingApplication` empty (which on Windows it always is) | derive from `args[2]` `ExecutingApplication` |
| `InstallUpdateService.Start:104-107` | call `Terminate(processId)` *before* mirror | skip — terminate happens *after* mirror in the `finally` |
| `InstallUpdateService.Start:114-121` | re-check no rogue Sonarr process restarted | not applicable |
| `InstallUpdateService.Start:128-133` | (no-op) | `chmod 755` on `Sonarr` and `ffprobe` |
| `InstallUpdateService.Start:142-169` (`finally`) | always `_startNzbDrone.Start(...)` to actively re-launch | `Terminate` + wait 10 s for systemd auto-restart; only if it doesn't reappear, fall back to `_startNzbDrone.Start` |
| `DetectApplicationType.GetAppType` | full Service / Console / Normal logic | always `Normal` |
| `StartNzbDrone.Start` | per-`AppType`: `StartService` / spawn `Sonarr.Console.exe` / spawn `Sonarr.exe` | always falls through `Normal` → `StartWinform` → spawns `Sonarr` (the Linux/macOS executable name; `ProcessNameToExe` returns the bare name on non-Windows). UPD-Q9 — naming inconsistency. |
| `TerminateNzbDrone.Terminate` | stop service + `KillAll(Sonarr.Console)` + `KillAll(Sonarr)` | `KillAll(Sonarr.Console)` + `KillAll(Sonarr)` + `Kill(processId)` |
| `NzbDroneLogger.RegisterSentry` (Update DSN, `NzbDroneLogger.cs:73-78`) | same DSN both OSes | same |

There is no macOS-specific bundle handling (`.app` / `Contents/MacOS/`) — Sonarr on macOS ships as a flat install dir under `/Applications/Sonarr.app/Contents/MacOS` but the updater treats it as a plain folder mirror. UPD-Q10.

## Logging

- The updater writes to `<AppData>/UpdateLogs/yyyy.MM.dd-HH.mm.txt` via the file target registered by `NzbDroneLogger.RegisterUpdateFile` (`src/NzbDrone.Common/Instrumentation/NzbDroneLogger.cs:174-191`). One log file per update run. No rolling, no automatic cleanup (UPD-Q11).
- Sentry: a separate DSN (production: `https://80777986b95f44a1a90d1eb2f3af1e36@sentry.sonarr.tv/11`, dev: `https://6168f0946aba4e60ac23e469ac08eac5@sentry.sonarr.tv/9` — `NzbDroneLogger.cs:74-78`). Logs at `LogLevel.Trace` (vs `Warn` for the main app), so updater telemetry is much chattier.
- Console: only when `RuntimeInfo.IsUserInteractive` and the host is not a Windows service — typically only when the user manually runs `Sonarr.Update.exe` from a terminal.
- The DB log target (`DatabaseTarget`) is *not* registered in the updater (no `AddLogDatabase` was called) — log events stay file/Sentry only.

To troubleshoot a failed update, the user reads `<AppData>/UpdateLogs/<latest>.txt`. The main `<AppData>/logs/sonarr.txt` does not capture updater output (different process, different NLog config).

## Re-launch responsibility

| OS | Who restarts Sonarr? |
|----|----------------------|
| Windows (Service) | The updater calls `_serviceProvider.Start("Sonarr")` in the `finally`. Windows SCM brings the service back up. |
| Windows (Console) | The updater spawns `Sonarr.Console.exe` directly. |
| Windows (Tray) | The updater spawns `Sonarr.exe` directly. |
| Linux (systemd / package install) | The updater kills the parent and *waits* for systemd's `Restart=on-failure` (or similar) to bring it back up. Falls back to direct spawn after 10 s. |
| macOS | Same as Linux — relies on launchd (or whatever) to auto-restart, falls back after 10 s. |
| Linux (Docker) | The updater is **disabled** at the in-core gate (`NzbDrone.Core/Update/InstallUpdateService.cs:258-262` — `_deploymentInfoProvider.PackageUpdateMechanism == UpdateMechanism.Docker`). The standalone binary never runs in Docker because the parent never spawns it. |

So the answer to "who restarts Sonarr": the updater on Windows and as a fallback on Unix; the OS service manager on Unix in the happy path.

## Install scripts

`UpdateMechanism.Script` (cross-cite `system.md` §9) is a *parent-side* feature: when the user has set `IConfigFileProvider.UpdateMechanism = Script`, the in-core `InstallUpdateService.InstallUpdateWithScript` (`NzbDrone.Core/Update/InstallUpdateService.cs:196-215`):
- Deletes `<sandbox>/Sonarr.Update/` (so the standalone updater is *not* spawned).
- Runs `_processProvider.Start(scriptPath, GetUpdaterArgs(updateSandboxFolder))` — passing the same arg shape (`<pid> <sandbox> <execpath> <preserved-args>`) to a user-supplied script.

The `NzbDrone.Update` standalone binary is therefore bypassed entirely in Script mode. The script is responsible for everything: file copy, restart, rollback. There is no contract guaranteeing the script behaves like `NzbDrone.Update` — the user owns it.

## `app.manifest`

`src/NzbDrone.Update/app.manifest` is a Windows assembly manifest. Notable settings:
- `<requestedExecutionLevel level="asInvoker" uiAccess="false" />` — UAC: do **not** auto-elevate. The updater inherits the parent's privileges. If Sonarr was installed Per-Machine and the user runs as non-admin, the mirror to `Program Files\Sonarr\` will fail (`UpdateFolderNotWritableException` is thrown earlier, parent-side, by `NzbDrone.Core/Update/InstallUpdateService.cs:91-99` before the standalone is ever launched).
- `<supportedOS>` GUIDs for Vista, 7, 8, 8.1, 10. (Windows 11 not listed but will inherit Windows 10's compatibility shim.)
- No DPI-awareness section — the updater has no UI.

## `app.config`

`src/NzbDrone.Update/app.config` is a near-empty .NET Framework `<configuration>` stub with `<supportedRuntime version="v4.0" sku=".NETFramework,Version=v4.6.2" />` — but the project actually targets `net10.0` per `Sonarr.Update.csproj:3`. The file is a vestige and is not honoured by .NET 10 (`app.config` is .NET Framework only). UPD-Q12.

## `Sonarr.Update.csproj` deployment shape

`src/NzbDrone.Update/Sonarr.Update.csproj`:

```xml
<OutputType>WinExe</OutputType>
<TargetFrameworks>net10.0</TargetFrameworks>
<PackageReference Include="DryIoc.dll" Version="5.4.3" />
<PackageReference Include="DryIoc.Microsoft.DependencyInjection" Version="6.2.0" />
<PackageReference Include="NLog" Version="5.5.1" />
<ProjectReference Include="..\NzbDrone.Common\Sonarr.Common.csproj" />
```

- `WinExe` (no console window on Windows). Note: this is misleading on non-Windows where `WinExe` and `Exe` are equivalent.
- Target `net10.0` (single TFM, not multi-targeted).
- Only `Sonarr.Common` referenced — so DryIoc, NLog, and Common's transitive deps (Sentry, NLog.Extensions, etc.) make up the deployable graph. No EF Core, no SQLite, no SignalR.
- Output assembly name `Sonarr.Update.dll` plus an `apphost` exe (`Sonarr.Update[.exe]`) per .NET 10 default.

## DB tables touched

None. The standalone updater does not connect to the database. (The `BackupAppData` step copies `sonarr.db` as a raw file but never opens it.)

## Events raised / consumed

None. The standalone has no `IEventAggregator`; lifecycle events are a Core concept and Core is not loaded.

## Config keys

- `IStartupContext.Args["data"]` — propagated from parent so AppData paths resolve correctly.
- `IStartupContext.Flags` — `nobrowser` (set by `StartNzbDrone` for the relaunched Sonarr).

No `config.xml` / `Sonarr:*` options bound — the updater never reads `config.xml`.

## External services

None directly. Sentry-DSN posts on errors (Update-specific DSN) — same hostname as the main app. The download from Skyhook happens parent-side, not in this binary.

## Notable quirks / TODOs / HACKs

- **Two services named `InstallUpdateService`**. `NzbDrone.Core.Update.InstallUpdateService` (parent, in-core) and `NzbDrone.Update.UpdateEngine.InstallUpdateService` (standalone). Different namespaces, different responsibilities. Be careful when grepping.
- **Verification is parent-side only**. The standalone binary trusts the sandbox completely. Anything with write access to `<AppData>/sonarr_update/Sonarr/` between the parent's verify and the standalone's mirror can swap files (UPD-Q7).
- **`theDakoLimit = 10`** (`InstallUpdateService.cs:153`) — a 10-iteration loop with 1 s sleeps to wait for systemd to bring Sonarr back up. The variable name's etymology is unclear.
- **Args 1 and 2 are non-Windows only** — Windows derives the install dir from the parent's `MainModule.FileName` instead. Inconsistency.
- **No exit-code protocol** — `Main` swallows everything to exit 0. Failure detection from the parent's perspective: the parent has already exited by the time the updater runs, so no one is listening anyway.
- **`app.config` references .NET Framework 4.6.2** — vestigial, ignored by net10.0.
- **No GPG / Authenticode signature** — the only integrity check is SHA-256 vs whatever Skyhook says. HTTPS-to-Skyhook is the trust root.
- **`BackupAppData` errors are swallowed** (`BackupAppData.cs:51`) — if the `config.xml` / `sonarr.db` copy fails, the update proceeds and the user has no app-data backup. `BackupAndRestore.Restore` does not roll the appdata back even if it had a copy.
- **macOS `.app` bundle is not handled specially** — UPD-Q10.
- **`_processProvider.FindProcessByName(...)` called and discarded** at `InstallUpdateService.cs:101-102` — no obvious effect; perhaps a left-over diagnostic.
- **Update logs are never cleaned** — `<AppData>/UpdateLogs/` grows unbounded. UPD-Q11.
- **The standalone has no progress reporting** to the parent — the parent prints `"Sonarr will restart shortly."` and then exits; the user only learns the update failed by reloading the UI and noticing the version did not change, or by reading `UpdateLogs/`.
- **`StartNzbDrone.Start` mutates `IStartupContext.Flags`** at line 80 — this is the only mutation of `IStartupContext` in either binary.

## Test files (the port must keep equivalents passing)

- `src/NzbDrone.Update.Test/UpdateTests/InstallUpdateServiceFixture.cs`
- `src/NzbDrone.Update.Test/UpdateTests/StartNzbDroneFixture.cs`
- `src/NzbDrone.Update.Test/UpdateTests/TerminateNzbDroneFixture.cs`
- `src/NzbDrone.Update.Test/UpdateTests/DetectApplicationTypeFixture.cs`
- `src/NzbDrone.Update.Test/UpdateTests/BackupAndRestoreFixture.cs`
- `src/NzbDrone.Update.Test/UpdateTests/BackupAppDataFixture.cs`
- `src/NzbDrone.Update.Test/UpdateAppFixture.cs`

## Open questions

- **UPD-Q1**: `UpdateApp.Main` always exits 0, even on failure (`UpdateApp.cs:49-53`). The parent process has already exited by the time the updater runs, so no one consumes the exit code anyway — but it would still be valuable for an outer wrapper (systemd / a packaging script). Should the Go port define a non-zero exit-code protocol?
- **UPD-Q2**: On Windows the updater ignores `args[1]` (sandbox) and `args[2]` (executing app) and instead derives the install dir from the parent process's `MainModule.FileName` via `Process.GetProcessById(pid).StartPath` (`UpdateApp.cs:117`). On non-Windows it uses `args[2]`. Why the asymmetry? The Windows path means a parent-process kill before `UpdateApp.Start` runs (e.g. SIGKILL) would race against `Process.GetProcessById` and crash. The Go port should pick a single path — almost certainly args-based.
- **UPD-Q3**: `InstallUpdateService.cs:101-102` calls `_processProvider.FindProcessByName(...)` for both process names and discards the result. No log line, no side effect visible. Diagnostic dead code? Remove in the port unless we find a hidden purpose.
- **UPD-Q4**: `BackupAppData` exists but `BackupAndRestore.Restore` does not restore the appdata backup — only the install-dir backup. So `config.xml` / `sonarr.db` are copied but never used. Either (a) the appdata backup is intended for *manual* recovery only (the user copies them back), or (b) this is half-finished. Confirm intent before porting.
- **UPD-Q5**: `InstallUpdateService.cs:114-121` returns silently if a Sonarr process resurfaced after termination but before mirror — the install dir is left untouched but the backup is in place. The user sees no error, no rollback. Should the Go port make this explicit (log error + Sentry-track + retry)?
- **UPD-Q6**: `theDakoLimit = 10` — 10 × 1 s wait for systemd auto-restart on non-Windows. Is 10 s enough on slow hardware? Should it be configurable? What's the magic-number etymology?
- **UPD-Q7**: SHA-256 verification only. No GPG / X509 / Authenticode. Trust = HTTPS to `services.sonarr.tv` + the integrity of the manifest served there. With sandbox-folder access, an attacker can replace files between parent-verify and standalone-mirror. Worth elevating to a security card concern (cross-cite `authentication-security.md`)?
- **UPD-Q8**: `UpdateStartupContext.UpdateLocation` is parsed (non-Windows only) but never read. It duplicates `IAppFolderInfo.GetUpdatePackageFolder()`. Drop in Go port?
- **UPD-Q9**: `StartNzbDrone.StartWinform` spawns `Sonarr.exe` (`StartNzbDrone.cs:65` — `"Sonarr".ProcessNameToExe()`). On non-Windows `ProcessNameToExe` returns just `"Sonarr"` (no extension). The "Winform" name is misleading on Linux/macOS where there is no WinForms code in the spawned binary. Clarify in the port.
- **UPD-Q10**: macOS app bundles. Sonarr ships at `/Applications/Sonarr.app/Contents/MacOS/Sonarr` (per typical .NET macOS bundling). The updater's `MirrorFolder` is dumb-recursive — it does not know about `Info.plist`, code signing, or quarantine attrs. Untested whether re-signing is needed after mirror or whether macOS Gatekeeper blocks the relaunched binary. Spike before relying on this.
- **UPD-Q11**: `<AppData>/UpdateLogs/yyyy.MM.dd-HH.mm.txt` — no rolling, no MaxArchiveFiles. Every update leaves a file forever. The Go port should expire after N days or N files.
- **UPD-Q12**: `app.config` declares `<supportedRuntime version="v4.0" sku=".NETFramework,Version=v4.6.2" />` but the project targets `net10.0`. The file is a no-op. Delete in the port.
- **UPD-Q13**: The standalone has its own DryIoc container and its own NLog config. The Go port already plans to drop assembly scanning + DI in `cmd/sonarr/`; the same applies here — `cmd/sonarr-update/main.go` should be a flat `func main()` with explicit constructor wiring. Confirm with `CONVERSION.md`.
- **UPD-Q14**: When `UpdateMechanism.Script` is set, the standalone updater is bypassed entirely. The parent runs the user-supplied script with the same argv shape as the standalone (`<pid> <sandbox> <execpath> <preserved-args>`). Should the Go port preserve this contract verbatim (so users' existing scripts keep working)?
- **UPD-Q15**: The Windows tray entry (`Sonarr.exe` / `WindowsApp`) and console entry (`Sonarr.Console.exe`) use different process names but the same install dir. After a `Service` install + a manual tray-launch, both can run simultaneously. The updater's `Terminate` kills *both* by name, but `DetectApplicationType` returns whichever matches first (Service > Console > Normal). If a service is installed but stopped, and a tray instance is running, the updater would re-launch the service (correct) but skip restoring the tray. Edge case worth a regression test.
