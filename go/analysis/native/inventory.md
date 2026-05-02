# Native-platform inventory

This is a flattened porter-assignment catalogue of every OS-specific native call, PInvoke signature, COM binding, and runtime patch in the Sonarr v5 backend. It is synthesized from `subsystems/native-platform.md` (which is the *behavioral* card — what each call does and why), with cross-cites to `subsystems/configuration.md` (`ProcMountProvider` static cache) and `subsystems/host.md` (`AccessControl/FirewallAdapter` Windows COM). Every row here is a Phase 1/2 work item that needs an explicit Go decision: replace, drop, or preserve.

In Sonarr's C# layout, "native" means **anything the .NET BCL does not abstract uniformly across operating systems**. The bulk lives in three projects: `NzbDrone.Mono` (libc/`Mono.Unix.Native.Syscall` for Linux/macOS/BSD), `NzbDrone.Windows` (`kernel32`/`advapi32` PInvoke + NTFS DACL via `System.IO.FileSystem.AccessControl`), and `Sonarr.RuntimePatches` (Harmony bytecode rewrites of Mono BCL bugs). The OS gate lives in `NzbDrone.Common/EnvironmentInfo/OsInfo.cs:14-16` and selects exactly one of `NzbDrone.Mono.Disk.DiskProvider` or `NzbDrone.Windows.Disk.DiskProvider` at DI-registration time; both subclass `NzbDrone.Common/Disk/DiskProviderBase.cs:14`.

The Go port has clean, well-documented replacements for every native surface here:
- **Linux/macOS/BSD:** `golang.org/x/sys/unix` for raw syscalls (`Statfs`, `Chmod`, `Chown`, `Symlink`, `Readlink`, `Link`, `IoctlSetInt`, `Clonefile`); `os` and `syscall` for higher-level helpers; `os/user` for `getpwnam`/`getgrnam`; `filepath.EvalSymlinks` for the `SymbolicLinkResolver` walker.
- **Windows:** `golang.org/x/sys/windows` for PInvoke equivalents (`GetDiskFreeSpaceEx`, `CreateHardLink`); `golang.org/x/sys/windows/registry` for the `IsServer()` `ProductName` probe; `go-ole` (already a known-good MIT-licensed binding) or shell-out to `netsh advfirewall` for the firewall COM call.
- **Runtime patches:** delete entirely. Go's stdlib has none of the bugs the patches target, and there is no Mono BCL to patch in the first place.

This inventory's pull-quotes use `subsystems/native-platform.md` as the master card; if a per-method narrative is missing here, look there. New questions raised by the catalogue itself (vs. those already in the master) are prefixed `NAT-Q*`. Most cross-cite `MONO-Q*` / `WIN-Q*` / `RP-Q*` from the master without renumbering.

---

## Per-OS surface area

### macOS / Linux (`NzbDrone.Mono`)

Every entry below is a method on `NzbDrone.Mono.Disk.DiskProvider` (subclass of `DiskProviderBase`) or a sibling helper in the same project. C# source lines are from `src/NzbDrone.Mono/Disk/DiskProvider.cs` unless stated otherwise.

| Function | C# source:line | Underlying call (libc / Mono.Unix.Native) | Go equivalent | Notes |
|----------|----------------|-------------------------------------------|---------------|-------|
| `GetMount(path)` | `Disk/DiskProvider.cs:32-37` | `readlink(2)` chain via `SymbolicLinkResolver`, then `DriveInfo.GetDrives` (`statvfs(2)`) | `filepath.EvalSymlinks` + scan `/proc/mounts` (Linux) / `getmntinfo(3)` via cgo (macOS) | Resolves symlinks before delegating to base; cross-cite `native-platform.md` MONO-Q6 |
| `GetAvailableSpace(path)` | `Disk/DiskProvider.cs:39-52` | `Mono.Unix.UnixDriveInfo.AvailableFreeSpace` = `f_bavail * f_frsize` | `unix.Statfs(path, &s); s.Bavail * s.Bsize` | Surfaces user-available, not root-visible |
| `InheritFolderPermissions(path)` | `Disk/DiskProvider.cs:54-56` | — (no-op on POSIX) | no-op | Empty body; only exists for symmetry with Windows DiskProvider |
| `SetEveryonePermissions(path)` | `Disk/DiskProvider.cs:58-60` | — (no-op on POSIX) | no-op | Same |
| `SetFilePermissions(path, mask, group)` | `Disk/DiskProvider.cs:62-67` | `Syscall.chmod` + `Syscall.chown` (does NOT strip x-bits even on regular files) | `unix.Chmod`, `unix.Chown` | See MONO-Q2 — split semantics from `SetPermissions` |
| `SetPermissions(path, mask, group)` | `Disk/DiskProvider.cs:69-79` | `Syscall.stat` + `Syscall.chmod` (strips x-bits on regular files) + `Syscall.chown` | `unix.Stat`, `unix.Chmod`, `unix.Chown` | 3-digit-mask vs 4-digit-mask quirk preserves setuid/setgid/sticky — MONO-Q1 |
| `IsValidFolderPermissionMask(mask)` | `Disk/DiskProvider.cs:123-147` | `NativeConvert.FromOctalPermissionString` (no syscall) | `strconv.ParseUint(mask, 8, 32)` | Octal parse + bit-validation; no native call |
| `CopyPermissions(src, tgt)` | `Disk/DiskProvider.cs:149-165` | `Syscall.stat` × 2 + `Syscall.chmod` | `unix.Stat`, `unix.Chmod` | Only chmods if `st_mode` differs |
| `GetAllMounts()` | `Disk/DiskProvider.cs:167-205` | `/proc/mounts` parse + `DriveInfo.GetDrives()` | `os.Open("/proc/mounts")` (Linux) / cgo `getmntinfo` (macOS) | Cross-cite `subsystems/configuration.md:692` for the static-cache caveat |
| `IsSpecialMount(IMount)` | `Disk/DiskProvider.cs:207-224` | string prefix only | string prefix | Filters `/var/lib/*` (Docker zfs) and `/snap/*` |
| `GetTotalSize(path)` | `Disk/DiskProvider.cs:226-233` | `statvfs(2)` (`f_blocks * f_frsize`) | `unix.Statfs(path, &s); s.Blocks * s.Bsize` | — |
| `CopyFileInternal(src, dst, overwrite)` | `Disk/DiskProvider.cs:235-271` | `readlink(2)` + `symlink(2)` for symlinks; else `TransferFilePatched(move:false)` | `os.Readlink`, `os.Symlink`; else `io.Copy` (no Mono workaround needed) | The "patched" copy path is a Mono 6.x bug workaround — drop in Go port |
| `MoveFileInternal(src, dst)` | `Disk/DiskProvider.cs:273-312` | `symlink(2)`, `unlink(2)`, `rename(2)` | `os.Rename`; symlink case = `os.Readlink` + `os.Symlink` + `os.Remove` | Falls back to copy+delete on EXDEV |
| `TransferFilePatched(...)` (private) | `Disk/DiskProvider.cs:316-410` | `Syscall.rename`, then file-copy + `Syscall.utime` | `os.Rename` + `io.Copy` + `os.Chtimes` | Mono 6.x "leaves zero-length file" workaround — delete entirely |
| `TryRenameFile(src, dst)` | `Disk/DiskProvider.cs:413-416` | `Syscall.rename(src, dst) == 0` | `os.Rename` (returns error not bool) | Used by `DiskTransferService` to attempt cheap atomic moves first |
| `TryCreateHardLink(src, dst)` | `Disk/DiskProvider.cs:418-450` | `link(2)` via `UnixFileSystemInfo.CreateLink` | `os.Link` | Returns false for symlink sources; catches EXDEV silently |
| `GetUserId(string)` | `Disk/DiskProvider.cs:452-472` | `Syscall.getpwnam` (numeric strings parsed direct) | `os/user.Lookup` | **Currently unreferenced** — MONO-Q3, candidate for deletion |
| `GetGroupId(string)` | `Disk/DiskProvider.cs:474-494` | `Syscall.getgrnam` | `os/user.LookupGroup` | Used by `SetPermissions` |
| `FindDriveType.Find(fs)` | `Disk/FindDriveType.cs:9-18` | static lookup table (no syscall) | `map[string]DriveType` | `apfs`, `zfs`, `fuse.*` etc. → fixed/network |
| `ProcMount` accessors | `Disk/ProcMount.cs:41-71, 75-86` | `UnixDriveInfo` (`statvfs(2)`) | `unix.Statfs` | Wraps overflow on Total/Free, clamps to `int64.Max` |
| `ProcMountProvider.GetMounts()` | `Disk/ProcMountProvider.cs:37-58` | `File.ReadAllLines("/proc/mounts")` + ParseLine | `bufio.Scanner` over `/proc/mounts` | Linux-only |
| `ProcMountProvider.GetFileSystems()` | `Disk/ProcMountProvider.cs:60-96` | `File.ReadAllLines("/proc/filesystems")`, cached in static `Dictionary` | `sync.Once` + `map[string]bool` (or just re-read; cheap) | NAT-Q1 / cross-cite NP-Q3 |
| `ProcMountProvider.ParseLine(...)` | `Disk/ProcMountProvider.cs:98-134` | string ops only | `strings.Split`, octal-escape decode | `OctalRegex = \\\d{3}` re-expanded by `String.FromOctalString` |
| `SymbolicLinkResolver.GetCompleteRealPath(path)` | `Disk/SymbolicLinkResolver.cs:22-50` | `readlink(2)` loop, max 32 iters | `filepath.EvalSymlinks(path)` | Custom walker because `Mono.Unix.UnixPath.GetCanonicalPath` was unreliable; MONO-Q6 |
| `NativeMethods.Ioctl(dst, FICLONE, src)` | `Interop/NativeMethods.cs:15-16` | `[DllImport("libc")] ioctl(fd, 0x40049409, src_fd)` | `unix.IoctlSetInt(dst, unix.FICLONE, int(src.Fd()))` | **No callers in C# tree** — MONO-Q7 |
| `NativeMethods.Open(path, flags)` | `Interop/NativeMethods.cs:18-21` | `Syscall.open` → wrapped in `SafeUnixHandle` | `os.OpenFile` | RAII via `defer file.Close()` |
| `SafeUnixHandle.ReleaseHandle` | `Interop/SafeUnixHandle.cs:7-30` | `Syscall.close(fd)` | implicit via `*os.File.Close()` | — |
| `FreebsdVersionAdapter.Read()` | `EnvironmentInfo/VersionAdapters/FreebsdVersionAdapter.cs:7-27` | shell-out `freebsd-version` | `exec.Command("freebsd-version").Output()` | BSD-only |
| `IssueFileVersionAdapter.Read()` | `EnvironmentInfo/VersionAdapters/IssueFileVersionAdapter.cs:7-50` | `File.ReadAllText("/etc/issue")` | `os.ReadFile("/etc/issue")` | Arch-only; MONO-Q9, MONO-Q12 — likely vestigial |
| `MacOsVersionAdapter.Read()` | `EnvironmentInfo/VersionAdapters/MacOsVersionAdapter.cs:9-62` | `File.ReadAllText("/System/Library/CoreServices/SystemVersion.plist")` + regex | `os.ReadFile` + `regexp.Compile` | Regex `1\d\.\d{1,2}\.?\d{0,2}?` — MONO-Q11 (breaks on macOS 26+) |
| `ReleaseFileVersionAdapter.Read()` | `EnvironmentInfo/VersionAdapters/ReleaseFileVersionAdapter.cs:7-77` | walks `/etc/*release` | `filepath.Glob("/etc/*release")` + parse `KEY=VALUE` | Catch-all for systemd-style `os-release` |
| `SynologyVersionAdapter.Read()` | `EnvironmentInfo/VersionAdapters/SynologyVersionAdapter.cs:7-76` | `File.ReadAllText("/etc.defaults/VERSION")` | `os.ReadFile` | DSM-specific |
| `OsInfo.IsContainerized` (ctor) | `NzbDrone.Common/EnvironmentInfo/OsInfo.cs:84-92` | reads `/.dockerenv`, `/proc/1/cgroup`, `/run/.containerenv`, env `container`, env `DOTNET_RUNNING_IN_CONTAINER` | `os.Stat` + `os.ReadFile` + `os.Getenv` | Cross-cite NP-Q1; cgroup v2 format may have shifted |

**Mono divergences from Windows summary** (verbatim from `native-platform.md`'s side-by-side table):

| Operation | Mono | Windows |
|---|---|---|
| Free space / total size | `statvfs` via `UnixDriveInfo` | `kernel32!GetDiskFreeSpaceEx` |
| Hardlink | `link(2)` via `UnixFileSystemInfo.CreateLink` | `kernel32!CreateHardLink` |
| Symlinks | First-class via `UnixSymbolicLinkInfo`; copy/move re-creates them | Treated as reparse points; copy is opaque |
| chmod / chgrp | `Syscall.chmod` + `Syscall.chown` | No-op |
| Inherit folder ACL | No-op | DACL `SetAccessRuleProtection(false,false)` |
| Set Everyone-Modify | No-op | DACL Add `Everyone:Modify` (CI+OI, propagation=InheritOnly) |
| Mount enumeration | `/proc/mounts` + `DriveInfo` | `DriveInfo` + reparse-point walking |
| Cross-device rename | EXDEV → `TransferFilePatched` | `File.Move` throws → caller falls back |
| Reflink/CoW | `ioctl(FICLONE)` available but unused | n/a |
| Long paths | n/a | `\\?\` prefix injected when length > 256 |

---

### Windows (`NzbDrone.Windows`)

Every entry below is a method on `NzbDrone.Windows.Disk.DiskProvider` (subclass of `DiskProviderBase`) or a sibling helper. C# source lines are from `src/NzbDrone.Windows/Disk/DiskProvider.cs` unless stated otherwise. The two PInvoke imports are declared in the class header at `:18-27`.

| Function | C# source:line | Underlying call (kernel32 / advapi32 / etc.) | Go equivalent | Notes |
|----------|----------------|-----------------------------------------------|---------------|-------|
| `GetDiskFreeSpaceExW` (PInvoke) | `Disk/DiskProvider.cs:18-23` | `kernel32!GetDiskFreeSpaceExW(lpDirectoryName, *lpFreeBytesAvailable, *lpTotalNumberOfBytes, *lpTotalNumberOfFreeBytes) -> bool` | `windows.GetDiskFreeSpaceEx` | `CharSet.Auto` → UTF-16 W variant |
| `CreateHardLinkW` (PInvoke) | `Disk/DiskProvider.cs:25-27` | `kernel32!CreateHardLinkW(lpFileName, lpExistingFileName, lpSecurityAttributes=NULL) -> bool` | `windows.CreateHardLink` | Always passes `IntPtr.Zero` for security attrs |
| `GetMount(path)` | `Disk/DiskProvider.cs:29-34` | `DirectoryInfo.Attributes` walking parents (no PInvoke) | parent-walk + `os.Lstat` `Mode()&os.ModeSymlink` | Reparse-point-aware; falls back to `base.GetMount` |
| `GetPathRoot(path)` | `Disk/DiskProvider.cs:36-43` | same | same | — |
| `GetAvailableSpace(path)` | `Disk/DiskProvider.cs:45-57` | `kernel32!GetDiskFreeSpaceEx` (via PInvoke above) | `windows.GetDiskFreeSpaceEx` | Resolves root, ensures it exists first |
| `InheritFolderPermissions(filename)` | `Disk/DiskProvider.cs:59-67` | `FileInfo.GetAccessControl` → `SetAccessRuleProtection(false, false)` → `SetAccessControl` | `windows.SetNamedSecurityInfo` with `PROTECTED_DACL_SECURITY_INFORMATION` cleared | Destructive — clears inherited ACEs without preserving as explicit; WIN-Q4 |
| `SetEveryonePermissions(filename)` | `Disk/DiskProvider.cs:69-107` | `WellKnownSidType.WorldSid` → `SecurityIdentifier`; `DirectorySecurity.ModifyAccessRule(Add)` Allow Everyone Modify CI+OI InheritOnly | `windows.CreateWellKnownSid(WinWorldSid)` + EXPLICIT_ACCESS list + `windows.SetNamedSecurityInfo` | `InheritOnly` → ACE applies to children only; WIN-Q3 |
| `SetFilePermissions` / `SetPermissions` / `CopyPermissions` | `Disk/DiskProvider.cs:109-119` | — (no-op) | no-op | Symmetric to Mono's empty `Inherit*`/`SetEveryone*` |
| `GetTotalSize(path)` | `Disk/DiskProvider.cs:121-133` | `kernel32!GetDiskFreeSpaceEx` | `windows.GetDiskFreeSpaceEx` | — |
| `TryCreateHardLink(src, dst)` | `Disk/DiskProvider.cs:169-185` | `kernel32!CreateHardLink` | `windows.CreateHardLink` | Prepends `\\?\` if `source.Length > 256` and not already prefixed; WIN-Q1 |
| `GetReparsePoint(path)` | `Disk/DiskProvider.cs:187-208` | `DirectoryInfo.Attributes` walking parents looking for `FileAttributes.ReparsePoint` | parent-walk + `os.Lstat` | Catches NTFS junctions and mount-point-as-folder cases |
| `FolderMount.cs` (`IMount` impl) | `FolderMount.cs:1-37` | wraps `DirectoryInfo` | struct around `os.FileInfo` | Hardcoded `DriveFormat = "NTFS"`, `DriveType = Removable` (WIN-Q5) |
| `WindowsVersionInfo.Read()` | `EnvironmentInfo/WindowsVersionInfo.cs:8-49` | `Environment.OSVersion.Version`, `Environment.OSVersion.VersionString`, registry `HKLM\Software\Microsoft\Windows NT\CurrentVersion!ProductName` (Registry32 view) | `windows.RtlGetVersion`, `windows/registry.OpenKey(..., REGISTRY_VIEW_32BIT)` | "Server" detection by case-insensitive substring match on `ProductName` (brittle) |
| `IsServer()` | `EnvironmentInfo/WindowsVersionInfo.cs:25-47` | `RegistryKey.OpenBaseKey(LocalMachine, Registry32)` + `OpenSubKey` + `GetValue("ProductName")` | `registry.OpenKey(LOCAL_MACHINE, ...)` | Catches all exceptions and logs; returns false on read failure |
| `FirewallAdapter.Open(port, ...)` | `src/NzbDrone.Host/AccessControl/FirewallAdapter.cs:22-87` | `Type.GetTypeFromProgID("HNetCfg.FwMgr")` + `Type.GetTypeFromProgID("HNetCfg.FWOpenPort")` (COM via `INetFwMgr`) | `go-ole` MIT binding **OR** `exec.Command("netsh", "advfirewall", "firewall", "add", "rule", ...)` | WIN-Q2; cross-cite `subsystems/host.md` |
| `ProcessProvider.Kill(pid)` | `src/NzbDrone.Common/Processes/ProcessProvider.cs` (cross-platform) | `System.Diagnostics.Process.Kill()` → `kernel32!TerminateProcess` on Windows, `SIGKILL` on Linux | `os.Process.Kill()` (which sends SIGKILL on POSIX, calls `TerminateProcess` on Windows) | No graceful `SIGTERM`/`WM_CLOSE` first; WIN-Q6 / MONO-Q10 |

**No netsh URL-ACL invocation.** Sonarr v5 binds via Kestrel which (on .NET 6+) uses Sockets directly, not http.sys. `grep -r netsh\|urlacl src/` returns zero hits — confirmed in `native-platform.md` line 223. The Go port also binds via raw sockets (`net/http` server on `net.Listen`) — no equivalent step needed.

**No registry writes.** Only `WindowsVersionInfo.IsServer()` reads the registry; nothing writes. `AppFolderInfo` (cross-ref `subsystems/common.md`) uses `%APPDATA%` env, not registry.

**No Win32 SID/ACL beyond `Everyone:Modify`.** The only well-known SID used is `WorldSid` (Everyone). No PUID/PGID-style user mapping happens on Windows.

---

## Runtime patches (`Sonarr.RuntimePatches`)

Project: `src/Sonarr.RuntimePatches/Sonarr.RuntimePatches.csproj` (target `net10.0`, dep `Lib.Harmony 2.4.2`). Bootstrapped from `src/NzbDrone.Console/ConsoleApp.cs:29` as the **first line of `Main`**, before logger init or DI bootstrap. Detection (`MonoRuntimePatchBase.cs:14-39`) probes `Type.GetType("Mono.Runtime")` and reads its non-public static `GetDisplayName()`; on .NET 6+ this returns `null` and the entire patcher becomes a no-op.

| Patch | Source:line | Mono bug | Disposition in Go port |
|-------|-------------|----------|------------------------|
| `BoringTLSVerifyFlagsPatch` | `Mono/BoringTLSVerifyFlagsPatch.cs:10-58` (range Mono 5.0 ≤ v < 7.0) | [Mono #19886] BoringTLS missing `X509_V_FLAG_TRUSTED_FIRST` (0x8000) → cross-signed certs (DST Root CA X3 ↔ ISRG Root X1) build the wrong chain | **Delete entirely.** Go's `crypto/x509` and `crypto/tls` do their own chain-building against `SystemCertPool()` and have no analog of this Mono OpenSSL-binding bug. RP-Q2. |
| `DeflateStreamAsyncPatch` | `Mono/DeflateStreamAsyncPatch.cs:10-50` (range Mono 6.0 ≤ v < 6.0.0.334) | [Mono #16122] `DeflateStream.{Read,Write}AsyncMemory` ship as `throw new NotImplementedException()` stubs → HTTPS+gzip downloads crash | **Delete entirely.** Go's `compress/flate` and `compress/gzip` are first-class implementations. RP-Q3. |

**Latent bug at `RuntimePatchBase.cs:84`** — the helper `TryPatchMethod(string typeName, string methodName, params string[] paramTypes)` ignores its `methodName` parameter and always passes the literal string `"GetSslServer"` to the inner overload. Currently latent because the only caller (`BoringTLSVerifyFlagsPatch`) happens to want that method. RP-Q1; resolved by deleting the project.

**Patch infrastructure (also deleted):**
- `RuntimePatcher.Initialize()` — `RuntimePatcher.cs:10-41`. Must run before any code path the patches modify is JIT-compiled. RP-Q4 — irrelevant in Go port.
- Env-var contract `DISABLE_RUNTIMEPATCHES` / `DEBUG_RUNTIMEPATCHES` — `RuntimePatcher.cs:12-30`. RP-Q5 — drop both env vars; document in CONVERSION.md as removed.
- `RuntimePatchBase` / `MonoRuntimePatchBase` — abstract bases.
- `RuntimePatchExtensions` — IL-shape verification helper (`Matches(this List<CodeInstruction>, params OpCode[])`).

The acceptance gate for skipping the entire `Sonarr.RuntimePatches` project is "verify nothing in `NzbDrone.*` calls into `Sonarr.RuntimePatches.*` types except the single `RuntimePatcher.Initialize()` call from `ConsoleApp.Main`" — confirmed via grep in `native-platform.md` line 338 (only `Sonarr.RuntimePatches/*` and `NzbDrone.Console/ConsoleApp.cs:11,29` reference the namespace).

---

## Cross-cutting findings to flag

These are work items that span the boundary between this card and others. Each is restated here as an explicit Go-port task; the master citation lives in the cross-referenced card.

- **`ProcMountProvider` static cache** — `src/NzbDrone.Mono/Disk/ProcMountProvider.cs:28` reads `/proc/filesystems` once per process into a static `Dictionary<string, bool>`. Mount changes during runtime (USB plug/unplug) are not picked up. Cross-cite `subsystems/configuration.md:692` and `subsystems/native-platform.md` NP-Q3. Go port options: (a) match — `sync.Once` + `map[string]bool`; (b) re-read on every call (cheap on Linux; `/proc/filesystems` is ~2 KB and kernel-cached). Phase 1 (disk provider port).

- **`AccessControl/FirewallAdapter` Windows COM** — `src/NzbDrone.Host/AccessControl/FirewallAdapter.cs:22-87` calls `Type.GetTypeFromProgID("HNetCfg.FwMgr")` and `HNetCfg.FWOpenPort` to add ports to the Standard firewall profile. This is `INetFwMgr` from the Windows Network Configuration COM library. Cross-cite `subsystems/host.md` and `subsystems/native-platform.md` WIN-Q2. Go port options: (a) `go-ole` (MIT) for COM; (b) shell out `netsh advfirewall firewall add rule`; (c) drop the auto-open behaviour and direct users to open the port manually. Phase 2 (host bootstrap port).

- **`ProcessProvider.Kill` SIGKILL only** — `src/NzbDrone.Common/Processes/ProcessProvider.cs` calls `System.Diagnostics.Process.Kill()` cross-platform with no graceful-shutdown phase. On Linux/macOS this is `SIGKILL` direct; on Windows it's `TerminateProcess` direct. Sub-processes Sonarr spawns include ffprobe (for media-info inspection) and the bundled `Sonarr.Update.exe` updater. Cross-cite `subsystems/native-platform.md` MONO-Q10 / WIN-Q6 and `subsystems/common.md` Processes section. Go port options: (a) match — `os.Process.Kill()`; (b) improve — send SIGTERM, wait N seconds, then SIGKILL. Phase 2 (process management).

- **Latent bug `RuntimePatchBase.cs:84`** — hardcoded `"GetSslServer"` instead of using the `methodName` parameter. Cross-cite `subsystems/native-platform.md` RP-Q1. Disposition: delete entirely with the rest of the project. Phase 1 (delete).

- **Confirmed: no `netsh urlacl`** — Kestrel binds via Sockets directly on .NET 6+, so the historical Mono-era `netsh http add urlacl` path is gone. Cross-cite `subsystems/native-platform.md` line 223 (`grep -r netsh\|urlacl src/` returns zero hits). The Go port's `net/http` `Server.ListenAndServe` is the equivalent and similarly needs no per-port ACL setup. No work item.

- **Build-tag layout** — Go port plan in `subsystems/native-platform.md:8-12`: `internal/common/disk/{disk_unix.go, disk_darwin.go, disk_windows.go}` + `internal/common/envinfo/envinfo_*.go` + `internal/common/disk/proc/proc_linux.go`. Cross-cite `go/STATUS.md` for the current scaffolding state. Phase 1.

---

## Open questions

Most native-port questions are already cross-cited from `MONO-Q*` / `WIN-Q*` / `RP-Q*` in `subsystems/native-platform.md`; this section adds only catalogue-level questions that are visible *as a porter assignment* rather than as a behavioral observation.

- **NAT-Q1** unresolved — `ProcMountProvider._fileSystems` is a static cache (`subsystems/native-platform.md` line 110, NP-Q3). Catalogue-level question: should the Go port choose option (a) `sync.Once`-guarded map, or option (b) re-read every call? Recommend (b) — `/proc/filesystems` is kernel-cached and ~2 KB; concurrency-correctness is free. Cross-cite `subsystems/native-platform.md` NP-Q3 + `subsystems/configuration.md:692`. Phase 1 (disk provider port).

- **NAT-Q2** unresolved — Windows firewall opening uses COM (`HNetCfg.FwMgr`). Catalogue decision: pick `go-ole` (MIT-licensed, mature) **or** `netsh advfirewall` shell-out **or** drop the feature entirely (require admin to pre-open the port). Cross-cite `subsystems/native-platform.md` WIN-Q2 and `subsystems/host.md`. Phase 2 (host bootstrap port).

- **NAT-Q3** unresolved — Process-kill graceful-vs-hard. Cross-cite `subsystems/native-platform.md` MONO-Q10/WIN-Q6 and `subsystems/common.md`. Catalogue decision: match (SIGKILL only) or improve (SIGTERM-then-SIGKILL with 5s grace)? Recommend "improve" — costs nothing, helps ffprobe close stdio cleanly on debugger-attach scenarios. Phase 2 (process management).

- **NAT-Q4** unresolved — `NativeMethods.clone_file` (FICLONE) infrastructure exists in `src/NzbDrone.Mono/Interop/NativeMethods.cs:15-16` but has no callers in the C# tree (`grep -r clone_file src/` finds only the definition). Cross-cite `subsystems/native-platform.md` MONO-Q7. Catalogue decision: port it (Linux: `unix.IoctlSetInt(dst, unix.FICLONE, int(src.Fd()))`, macOS: `unix.Clonefile`) and wire it into `CopyFileInternal` for the cross-FS-copy case where source FS supports CoW? Or leave unimplemented like C#? Phase 2 (CoW spike).

- **NAT-Q5** unresolved — `IOsVersionAdapter` registration order is non-deterministic (assembly-scan). For Synology+Linux installs, whichever of `SynologyVersionAdapter` / `ReleaseFileVersionAdapter` registers first wins. Cross-cite `subsystems/native-platform.md` MONO-Q8. Catalogue decision: in Go port, build an explicit ordered list at compile time (build-tagged init) — `[SynologyVersionAdapter, IssueFileVersionAdapter, ReleaseFileVersionAdapter]` for Linux, `[MacOsVersionAdapter]` for Darwin, `[FreebsdVersionAdapter]` for BSD, `[WindowsVersionInfo]` for Windows. Phase 1 (envinfo port).

- **NAT-Q6** unresolved — All four `Sonarr.RuntimePatches` items are tagged "delete entirely" but `RuntimePatcher.Initialize()` is invoked from `src/NzbDrone.Console/ConsoleApp.cs:11,29`. Catalogue decision: when the Go port replaces `ConsoleApp.cs`, do we keep the env-var contract (`DISABLE_RUNTIMEPATCHES` etc.) for transitional safety, or drop them outright? Cross-cite `subsystems/native-platform.md` RP-Q5. Recommend "drop and document" — env vars are silently ignored if a user has them in a stale systemd unit, and the names imply functionality the Go port does not have. Phase 1 (delete).

- **NAT-Q7** unresolved — Windows long-path support. `TryCreateHardLink` is the only operation that injects `\\?\` (`subsystems/native-platform.md` WIN-Q1). The rest rely on .NET 6+'s `<longPathAware>true</longPathAware>` manifest. Catalogue decision: confirm Sonarr's app.manifest setting (separate Phase-0 task), then in Go port either (a) opt into `windows.UTF16PtrFromString` with `\\?\` for paths > 256, or (b) configure the Windows registry `LongPathsEnabled=1` requirement and document. Cross-cite `subsystems/native-platform.md` WIN-Q1. Phase 1 (disk provider port).

- **NAT-Q8** unresolved — Build-tag organisation. The current draft is `disk_unix.go`/`disk_darwin.go`/`disk_windows.go` + `envinfo_*.go`. Linux-only files (`/proc/mounts`, `/proc/filesystems`) need `//go:build linux` separately. Catalogue decision: prefer per-OS files at one directory level (`internal/common/disk/disk_linux.go`, `..._darwin.go`, `..._windows.go`, `..._freebsd.go`) or factor into a `proc` subpackage with Linux-only files? `subsystems/native-platform.md:11` already proposed `internal/common/disk/proc` for Linux — adopt that and add per-GOOS files for the rest. Phase 1 (disk provider port).

- **NAT-Q9** unresolved — `MacOsVersionAdapter` regex `1\d\.\d{1,2}\.?\d{0,2}?` only matches version `1X.YY[.ZZ]` (`subsystems/native-platform.md` MONO-Q11). Catalogue decision: in Go port, widen to `\d{1,3}\.\d{1,2}(\.\d{1,2})?` so macOS 26+, 30+, etc. work without a future patch. Trivial. Phase 1 (envinfo port).

- **NAT-Q10** unresolved — `IssueFileVersionAdapter` recognizes only "Arch Linux" via `Contains` substring match (`subsystems/native-platform.md` MONO-Q9, MONO-Q12). Modern Arch sets `ID=arch` in `/etc/os-release` which `ReleaseFileVersionAdapter` already picks up. Catalogue decision: drop `IssueFileVersionAdapter` entirely from the Go port (it's vestigial and the Manjaro false-match is a real correctness bug). Phase 1 (envinfo port).
