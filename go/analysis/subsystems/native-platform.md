# Native platform (Mono, Windows, RuntimePatches)

**Source:**
- `src/NzbDrone.Mono/**`
- `src/NzbDrone.Windows/**`
- `src/Sonarr.RuntimePatches/**`

**Target Go package(s):**
- `internal/common/disk` (build-tagged `disk_unix.go`, `disk_darwin.go`, `disk_windows.go`)
- `internal/common/envinfo` (`envinfo_*.go` per GOOS for OS-version detection)
- `internal/common/disk/proc` (Linux-only `/proc/mounts` parser)
- **Sonarr.RuntimePatches: not ported** — Go has no equivalent class of runtime bug; assembly-rewriting a JIT'd Mono BCL is meaningless when there is no Mono BCL.

**Status:** drafted

## Purpose
This card covers the three projects that contain everything the .NET BCL does not abstract uniformly across operating systems. They sit beneath `NzbDrone.Common.Disk.DiskProviderBase` (see `subsystems/common.md`) and `NzbDrone.Common.EnvironmentInfo.OsInfo` (`OsInfo.cs:9`):

- **`NzbDrone.Mono`** — every "Linux/macOS/BSD-only" bit: `chmod`/`chown` via libc syscalls, `/proc/mounts` parsing for mount enumeration, Linux symlink loop detection, `ioctl(FICLONE)` reflink support, BSD/Linux/Synology/macOS OS-version probes, and a pile of Mono-bug workarounds in `TransferFilePatched`.
- **`NzbDrone.Windows`** — Win32-only bits: `kernel32!GetDiskFreeSpaceEx` for free-space, `kernel32!CreateHardLink` for hardlinks, NTFS DACL manipulation (`SetEveryonePermissions`, `InheritFolderPermissions`), reparse-point detection for mount enumeration, registry probe for "is this Windows Server?".
- **`Sonarr.RuntimePatches`** — Harmony-based bytecode rewrites of the Mono runtime to fix Mono-specific bugs Sonarr trips over. Two patches in tree, both gated on `Mono.Runtime` being present at startup.

The Go port absorbs (1) and (2) into build-tagged files under `internal/common/disk` / `internal/common/envinfo`. (3) is deleted entirely (see RP-Q* below). All three projects are dependency-injected at startup; only one of `NzbDrone.Mono.Disk.DiskProvider` or `NzbDrone.Windows.Disk.DiskProvider` is registered, picked via `OsInfo.IsWindows` from `NzbDrone.Common/EnvironmentInfo/OsInfo.cs:14-16`.

---

## NzbDrone.Mono

**Project file:** `src/NzbDrone.Mono/Sonarr.Mono.csproj`
- Target: `net10.0`
- Dependencies:
  - `Mono.Posix.NETStandard 5.20.1.34-servarr24` — provides `Mono.Unix.Native.Syscall` (the libc binding used throughout)
  - `System.IO.FileSystem.AccessControl 6.0.0-preview.5.21301.5` — pulled in transitively for `DiskProviderBase` parity; not actually used on Mono, but referenced because the project references `NzbDrone.Common`.
- Project ref: `NzbDrone.Common`.

The `Mono.Posix.NETStandard` package — published from a Servarr fork (`-servarr24` suffix) — is the binding to libc. **Every** syscall in this project goes through `Mono.Unix.Native.Syscall.*` or `Mono.Unix.UnixFileSystemInfo`. The Go port replaces this with `golang.org/x/sys/unix` for raw syscalls and `os` / `syscall` for the higher-level helpers.

### `Disk/DiskProvider.cs`
Subclasses `DiskProviderBase` (`NzbDrone.Common/Disk/DiskProviderBase.cs:14`). Override-by-override:

| Override | File:line | What it does | Underlying call |
|---|---|---|---|
| `GetMount(string path)` | `DiskProvider.cs:32-37` | Resolves `path` through `_symLinkResolver.GetCompleteRealPath(path)` first, then defers to `base.GetMount` | `readlink(2)` chain via `SymbolicLinkResolver`, then `DriveInfo.GetDrives` (statvfs underneath) |
| `GetAvailableSpace(string path)` | `DiskProvider.cs:39-52` | Looks up `IMount` for `path`, returns `mount.AvailableFreeSpace` | `Mono.Unix.UnixDriveInfo.AvailableFreeSpace` (libc `statvfs(2)`) |
| `InheritFolderPermissions(filename)` | `DiskProvider.cs:54-56` | **No-op** on Linux/macOS | — |
| `SetEveryonePermissions(filename)` | `DiskProvider.cs:58-60` | **No-op** on Linux/macOS | — |
| `SetFilePermissions(path, mask, group)` | `DiskProvider.cs:62-67` | Octal-parses mask, calls `SetPermissions(path, mask, group, perms)` without the `S_IXUSR/IXGRP/IXOTH` strip | `chmod(2)` via `Syscall.chmod`; `chown(2)` via `Syscall.chown` |
| `SetPermissions(path, mask, group)` | `DiskProvider.cs:69-79` | Same as above, but if `path` is a regular file, strips execute bits via `GetFilePermissions`. If `mask.Length < 4`, preserves `~ACCESSPERMS` (setuid/setgid/sticky) from current `stat`. | `Syscall.stat`, `Syscall.chmod`, `Syscall.chown` |
| `IsValidFolderPermissionMask(mask)` | `DiskProvider.cs:123-147` | Octal parse must succeed, no bits outside `ACCESSPERMS`, must include all of `S_IRWXU` (700) | `NativeConvert.FromOctalPermissionString` (no syscall) |
| `CopyPermissions(src, tgt)` | `DiskProvider.cs:149-165` | `stat` both, if `st_mode` differs `chmod` target to source | `Syscall.stat` × 2, `Syscall.chmod` |
| `GetAllMounts()` | `DiskProvider.cs:167-205` | Combines `_procMountProvider.GetMounts()` (Linux only — `/proc/mounts`) with `DriveInfo.GetDrives()`-derived `DriveInfoMount`s, deduplicates by `RootDirectory` | reads `/proc/mounts`, `DriveInfo` (statvfs) |
| `IsSpecialMount(IMount)` | `DiskProvider.cs:207-224` | Filters out `/var/lib/*` (Docker zfs) and `/snap/*` mount points | string prefix only |
| `GetTotalSize(path)` | `DiskProvider.cs:226-233` | Mount lookup → `mount.TotalSize` | `statvfs(2)` |
| `CopyFileInternal(src, dst, overwrite)` | `DiskProvider.cs:235-271` | If source is a symlink: re-creates the symlink at destination (relative if same dir). Else: `TransferFilePatched(..., move:false)` (the Mono-bug workaround) | `readlink(2)`, `symlink(2)`, then file-copy |
| `MoveFileInternal(src, dst)` | `DiskProvider.cs:273-312` | Same symlink logic, then deletes original. Else `TransferFilePatched(..., move:true)` (which tries `Syscall.rename` first) | `symlink(2)`, `unlink(2)`, `rename(2)` |
| `TryRenameFile(src, dst)` | `DiskProvider.cs:413-416` | `Syscall.rename(src, dst) == 0` | `rename(2)` |
| `TryCreateHardLink(src, dst)` | `DiskProvider.cs:418-450` | If source is a symlink, returns `false`. Else `UnixFileSystemInfo.CreateLink(dst)`. Catches `EXDEV` (cross-device, expected) separately | `link(2)` |

**`SetPermissions` mask-length quirk** (`DiskProvider.cs:94-97`): if the user provided a 3-digit mask (e.g. `755`), the existing stat's non-access bits (setuid/setgid/sticky) are OR'd back in. A 4-digit mask (e.g. `0755`, `2755`) is taken at face value. The Go port must replicate this exactly — passing 3 vs 4 digits has different semantics. (MONO-Q1)

**`SetFilePermissions` vs `SetPermissions`** (`DiskProvider.cs:62-79`): only `SetPermissions` strips execute bits when the target is a file. `SetFilePermissions` does **not** strip executable. This appears to be a deliberate split: `SetFilePermissions` is called on freshly-imported video files (where you want exact mask), `SetPermissions` is called from validation on user-typed paths where applying `0755` to a file would mean "really 0644". (MONO-Q2)

**Empty `InheritFolderPermissions` / `SetEveryonePermissions`**: these only do work on Windows; the Mono override is a deliberate no-op. The cross-platform `IDiskProvider` keeps the methods abstract so callers don't need to OS-switch (`NzbDrone.Common/Disk/DiskProviderBase.cs:32-33`).

**chown semantics** (`DiskProvider.cs:108`): `Syscall.chown(path, unchecked((uint)-1), groupId)` — passing `(uint)-1` to chown means "do not change UID". A non-empty `group` argument means "set GID". An empty group string yields `UNCHANGED_ID` (`uint.MaxValue` == `(uint)-1`) which chown treats as "don't change". The user-id branch (`GetUserId`, `DiskProvider.cs:452-472`) exists but is currently unreferenced from `SetPermissions` — it's only used if a future caller wants to set ownership. (MONO-Q3)

**`getpwnam`/`getgrnam`** (`DiskProvider.cs:464,486`): if the supplied user/group is not all-digits, the code resolves it via `Syscall.getpwnam`/`getgrnam`. Throws `LinuxPermissionsException` (`Disk/LinuxPermissionsException.cs:5-17`) if unknown. Numeric strings are parsed directly — bypassing nss entirely. The Go port does this through `os/user`'s `LookupGroup`/`Lookup`, which calls `getgrnam_r`/`getpwnam_r` on cgo-enabled builds.

**No special-file detection.** The Mono provider does not check for FIFOs, sockets, block devices, or character devices anywhere — `CopyFileInternal` will happily call into the Mono-bug-patched copy on whatever it's handed. (MONO-Q4)

**No `lchmod`.** `Syscall.chmod` follows symlinks. Sonarr's chmod operates on regular files/directories the user has chosen as media targets, so this is by-design — but see MONO-Q4 for the corollary.

**No `chown`-the-file flow.** `SetPermissions` only chgrp's, never chown's the user. This is why Docker users see "PUID is set but Sonarr only changes group, not owner" reported as expected behavior. (MONO-Q5)

### `Disk/FindDriveType.cs`
Static lookup table mapping fuse/special filesystem strings to `DriveType`:
| FS string | DriveType |
|---|---|
| `afpfs` | `Network` |
| `apfs` | `Fixed` |
| `fuse.mergerfs` | `Fixed` |
| `fuse.shfs` | `Fixed` |
| `fuse.glusterfs` | `Network` |
| `nullfs` | `Fixed` |
| `zfs` | `Fixed` |

(`FindDriveType.cs:9-18`.) Anything not in the table returns `DriveType.Unknown`. `ProcMountProvider.ParseLine` then re-classifies based on `_fixedTypes`/`_networkDriveTypes` and `/proc/filesystems`. The two filters are complementary: `FindDriveType` covers fuse-and-friends that don't appear in `/proc/filesystems`; the proc-based classifier covers kernel filesystems.

### `Disk/LinuxPermissionsException.cs`
Trivial subclass of `NzbDroneException` (`LinuxPermissionsException.cs:5-17`). Two ctors: one with format args, one plain. Used exclusively by `DiskProvider` and `DiskProvider.GetGroupId`/`GetUserId`.

### `Disk/ProcMount.cs`
Implements `IMount` (`NzbDrone.Common/Disk/IMount.cs`). Wraps a `Mono.Unix.UnixDriveInfo` (which is itself libc `statvfs(2)`-based).
- `AvailableFreeSpace`, `TotalFreeSpace`, `TotalSize`: pulled from `UnixDriveInfo`. Total and free wrap an `OverflowException` and clamp to `long.MaxValue` (lines 41-71).
- `VolumeName` (`ProcMount.cs:75-86`): if `VolumeLabel` is empty / starts with `UUID=` / equals `Name`, returns `Name`; else `"{Name} ({VolumeLabel})"`.

`UnixDriveInfo` is part of the `Mono.Posix.NETStandard` shim. Its `AvailableFreeSpace` corresponds to `f_bavail * f_frsize` (user-available, respecting reserved blocks); `TotalFreeSpace` is `f_bfree * f_frsize` (root-visible). Sonarr surfaces only `AvailableFreeSpace`, so the Go port should use `unix.Statfs.Bavail * unix.Statfs.Bsize`. See DS-Q1 in `subsystems/configuration.md` for the cross-card pinning of this choice.

### `Disk/ProcMountProvider.cs`
Already covered partially in `subsystems/configuration.md`'s "Mount-point enumeration" deep-dive (lines 619-624). Re-stating the surface here for completeness:

- `GetMounts()` (`ProcMountProvider.cs:37-58`) — reads `/proc/mounts` line-by-line; on `IOException`, logs once and caches the failure flag (`_hasLoggedProcMountFailure`).
- `ParseLine` (`ProcMountProvider.cs:98-134`) — splits on space (mount-point names are octal-escaped per `mount(8)`'s convention; `OctalRegex = \\\d{3}` re-expanded via `ExpandEscapes` on line 150-153 calling `String.FromOctalString`). Drops any line whose mount field doesn't start with `/` (e.g. `nsfs` namespaces, line 113-118). Drive type is computed by:
  1. `FindDriveType.Find(type)` (handles `apfs`, `fuse.*`, `zfs`, etc.).
  2. If `name.StartsWith("/dev/")` OR the type appears in `/proc/filesystems` and is not marked `nodev`: `DriveType.Fixed`.
  3. Override: if `type` is in `_networkDriveTypes`, force `DriveType.Network`.

- **Default fixed-type set when `/proc/filesystems` unreadable** (`ProcMountProvider.cs:25`): `ext3, ext2, ext4, vfat, fuseblk, xfs, jfs, msdos, ntfs, minix, hfs, hfsplus, qnx4, ufs, btrfs`.
- **Network-type set** (`ProcMountProvider.cs:26`): `cifs, nfs, nfs4, nfsd, sshfs`.
- `GetFileSystems()` (`ProcMountProvider.cs:60-96`) — caches `/proc/filesystems` once in a static `Dictionary<string, bool>`. Key is fs name (col 1), value is `true` iff col 0 is **not** `nodev` (i.e. the FS is backed by a real block device).

The static cache lives for the process lifetime; mount changes (e.g. a USB drive plugged in mid-run) will not be re-read. Cross-cite: `subsystems/configuration.md:692` flagged this. The Go port's choice is a function-call-cheap design (parse on every call) vs the C# cached-once pattern.

The `MountOptions` produced (`ParseLine` line 111) is a comma-split kv-pair of `/proc/mounts` field 4 (e.g. `rw,relatime,fmask=0022`). Consumed downstream for read-only detection only — see `subsystems/configuration.md` (`ProcMountProvider` mention).

### `Disk/SymbolicLinkResolver.cs`
Linux/macOS-only manual symlink walker (`SymbolicLinkResolver.cs:13-168`). The reason it exists: `Mono.Unix.UnixPath.GetCanonicalPath` was unreliable on certain Mono versions; this is a hand-rolled alternative.

Algorithm (`GetCompleteRealPath`, lines 22-50):
1. Loop up to **32** times (`for var links = 0; links < 32`); on overflow log warning and return the original path.
2. Each iteration calls `TryFollowFirstSymbolicLink` which canonicalizes path components left-to-right, stops at the first symlink found, replaces it with `readlink()` output, then restarts.
3. Path components: `GetPathComponents` (lines 52-82) collapses `.`, `..`, and empty segments. `..` at the root pops up; otherwise pushes (the comment-free fallback at line 71 — `target++` on rooted `..` — is explicit "absorb the .. as a literal segment"; this prevents path escapes above `/`).
4. `TryFollowSymbolicLink` (`SymbolicLinkResolver.cs:125-167`): `UnixFileSystemInfo.TryGetFileSystemEntry` → if not a symlink, return as-is; if `readlink` returns null, log trace and treat as "not a symlink" (errno-EINVAL means it isn't actually a symlink).
5. Relative-link resolution: `UnixPath.IsPathRooted(link)` ? assume absolute : `dirname(path) + sep + link`, then `UnixPath.GetCanonicalPath`.

**Loop cap** of 32 is hardcoded; matches Linux's traditional `MAXSYMLINKS` ceiling. The Go port should use `filepath.EvalSymlinks` which has its own limit and surfaces `ELOOP` natively; behavior is equivalent. (MONO-Q6)

### `Interop/NativeMethods.cs`
Two libc bindings, both Linux-only:

| Symbol | Library | Signature | Purpose |
|---|---|---|---|
| `ioctl(fd, FICLONE, src_fd)` | `libc` (linux) | `[DllImport("libc", EntryPoint = "ioctl", SetLastError = true)] int Ioctl(SafeUnixHandle dst_fd, IoctlRequest request, SafeUnixHandle src_fd)` (`NativeMethods.cs:15-16`) | Reflink (CoW clone) on btrfs/xfs/zfs filesystems. Hardcoded request value `0x40049409` = `_IOW(0x94, 9, int)` (line 10). |
| `open(pathname, flags)` | via `Syscall.open` (Mono.Posix wrapper) | `Syscall.open(pathname, flags, FilePermissions.DEFFILEMODE)` (`NativeMethods.cs:18-21`) | wraps result in `SafeUnixHandle` |

`SafeUnixHandle` (`Interop/SafeUnixHandle.cs:7-30`): subclass of `System.Runtime.InteropServices.SafeHandle`. `IsInvalid` returns true when the underlying fd is `-1`. `ReleaseHandle` calls `Syscall.close(fd)`. Standard fd RAII — Go uses `*os.File` and `defer file.Close()` for the same effect.

**`FICLONE` is a Linux-only ioctl** (defined in `linux/fs.h`). On macOS the equivalent is `clonefile(2)`. Sonarr does not call `clonefile` anywhere; macOS users get the regular copy path. The Go port should:
- Linux: `unix.IoctlFileClone(dst, src)` (or raw `unix.IoctlSetInt(dst, unix.FICLONE, int(src.Fd()))`).
- macOS: `golang.org/x/sys/unix.Clonefile` (`CLONE_NOFOLLOW`).
- BSD/Windows: fall back to `io.Copy`.

**Caller of `clone_file`** — currently **none** in the C# tree. `grep -r clone_file src/` finds only the definition. (MONO-Q7) This appears to be vestigial / pre-wired infrastructure.

### `EnvironmentInfo/VersionAdapters/`
Five `IOsVersionAdapter` implementations (`IOsVersionAdapter` defined in `NzbDrone.Common/EnvironmentInfo/IOsVersionAdapter.cs`). `OsInfo` iterates them in registration order and returns the first non-null `Read()` (`OsInfo.cs:51-71`).

| Adapter | File:line | OS gate | Source of truth |
|---|---|---|---|
| `FreebsdVersionAdapter` | `FreebsdVersionAdapter.cs:7-27` | `OsInfo.Os == Os.Bsd` | shells out `freebsd-version`, takes first stdout line |
| `IssueFileVersionAdapter` | `IssueFileVersionAdapter.cs:7-50` | `OsInfo.IsLinux` | reads `/etc/issue`; only recognizes Arch (string-contains "Arch Linux"); returns hardcoded `("Arch", "1.0", "Arch Linux")` |
| `MacOsVersionAdapter` | `MacOsVersionAdapter.cs:9-62` | `OsInfo.IsOsx` | parses `/System/Library/CoreServices/SystemVersion.plist` (or `ServerVersion.plist`) with regex `<key>ProductVersion</key>\s*<string>(?<version>1\d\.\d{1,2}\.?\d{0,2}?)</string>`; defaults to `10.0` if regex doesn't match |
| `ReleaseFileVersionAdapter` | `ReleaseFileVersionAdapter.cs:7-77` | `OsInfo.IsLinux` | walks `/etc/*release`, parses `KEY=VALUE` lines; honors `ID`, `PRETTY_NAME`, `VERSION_ID`. Returns null on no-success-flag (no recognized key) |
| `SynologyVersionAdapter` | `SynologyVersionAdapter.cs:7-76` | `OsInfo.IsLinux` | reads `/etc.defaults/VERSION`; honors `productversion`, `majorversion`, `minorversion`. Synonyms wrapped in `"`. |

**Order matters.** Whichever adapter returns first wins. Registration order is via assembly scan in `Sonarr.Common.Composition`; the order is non-deterministic. In practice:
- macOS users always hit `MacOsVersionAdapter` first (only one with `IsOsx` gate).
- Linux Synology users may or may not — `SynologyVersionAdapter` short-circuits if `/etc.defaults/VERSION` doesn't exist. `IssueFileVersionAdapter` is Arch-only, also short-circuits. `ReleaseFileVersionAdapter` is the catch-all. (MONO-Q8)

**No Windows adapter here** — see `NzbDrone.Windows/EnvironmentInfo/WindowsVersionInfo.cs` below.

**Linux distro detection is path-based, not uname-based.** The Go port's `runtime.GOOS == "linux"` doesn't tell us "what distro" — we'd parse `/etc/os-release` (modern equivalent of the `*release` file walker) and `/etc.defaults/VERSION` (Synology) explicitly. The arch-via-`/etc/issue` branch is questionable and likely vestigial — modern Arch sets `ID=arch` in `/etc/os-release`. (MONO-Q9)

### Mono divergences from Windows (side-by-side)
Already enumerated above per-method, but summary table:

| Operation | Mono | Windows |
|---|---|---|
| Free space | `statvfs` via `UnixDriveInfo` | `kernel32!GetDiskFreeSpaceEx` |
| Total size | same | same |
| Hardlink | `link(2)` via `UnixFileSystemInfo.CreateLink` | `kernel32!CreateHardLink` |
| Symlinks | First-class via `UnixSymbolicLinkInfo`; `CopyFile`/`MoveFile` re-create them | Treated as reparse points; copy is opaque |
| chmod | `Syscall.chmod` + `Syscall.chown` | No-op (`SetPermissions` body empty, `DiskProvider.cs:113-115`) |
| chgrp | `Syscall.chown(_, -1, gid)` | No-op |
| Inherit folder ACL | No-op | DACL `SetAccessRuleProtection(false,false)` |
| Set Everyone-Modify | No-op | DACL Add `Everyone:Modify` (containerInherit+objectInherit, propagation=inheritOnly) |
| Mount enumeration | `/proc/mounts` + `DriveInfo` | `DriveInfo` + reparse-point walking |
| Special-mount filter | `/var/lib/*`, `/snap/*` | `FolderMount` for reparse points |
| Mount-point detection on path | `SymbolicLinkResolver.GetCompleteRealPath` then mount lookup | `GetReparsePoint` walks parents until `FileAttributes.ReparsePoint` (`DiskProvider.cs:187-208`) |
| Cross-device rename | `Syscall.rename` returns `EXDEV` → fall back to copy+delete via `TransferFilePatched` | `File.Move` → throws → caller falls back |
| Reflink/CoW | `ioctl(FICLONE)` available but unused | n/a |
| Long paths | n/a (POSIX paths are unbounded in practice) | `\\?\` prefix injected when length > 256 (`DiskProvider.cs:173-176`) |

---

## NzbDrone.Windows

**Project file:** `src/NzbDrone.Windows/Sonarr.Windows.csproj`
- Target: `net10.0`
- Dependencies:
  - `NLog 5.5.1`
  - `System.IO.FileSystem.AccessControl 6.0.0-preview.5.21301.5` — required for `DirectoryInfo.GetAccessControl` / `SetAccessControl` on .NET 6+ (these moved out of `mscorlib`).
- Project ref: `NzbDrone.Common`.

### `Disk/DiskProvider.cs`
Subclasses `DiskProviderBase`. Two PInvoke imports declared at the top of the class (`DiskProvider.cs:18-27`):

| Symbol | DLL | Signature | Purpose |
|---|---|---|---|
| `GetDiskFreeSpaceExW` | `kernel32.dll` | `bool GetDiskFreeSpaceEx(string lpDirectoryName, out ulong lpFreeBytesAvailable, out ulong lpTotalNumberOfBytes, out ulong lpTotalNumberOfFreeBytes)` (`DiskProvider.cs:18-23`) | Used for free space and total size |
| `CreateHardLinkW` | `kernel32.dll` | `bool CreateHardLink(string lpFileName, string lpExistingFileName, IntPtr lpSecurityAttributes)` (`DiskProvider.cs:25-27`) | Hardlink creation. `lpSecurityAttributes` is always `IntPtr.Zero` |

Note: `CharSet = CharSet.Auto` — the runtime selects `W` (UTF-16) on modern Windows. `SetLastError = true` so the C# layer can read `Marshal.GetLastWin32Error()` if needed (it currently doesn't).

Override-by-override:

| Override | File:line | What it does | Underlying call |
|---|---|---|---|
| `GetMount(path)` | `DiskProvider.cs:29-34` | `GetReparsePoint(path)` first; if it's a junction/reparse-point, return that as the mount; else `base.GetMount` | `DirectoryInfo.Attributes` walking parents |
| `GetPathRoot(path)` | `DiskProvider.cs:36-43` | Reparse-point-aware path-root | same |
| `GetAvailableSpace(path)` | `DiskProvider.cs:45-57` | Resolves root, ensures it exists, then `DriveFreeSpaceEx` | `kernel32!GetDiskFreeSpaceEx` |
| `InheritFolderPermissions(filename)` | `DiskProvider.cs:59-67` | `FileInfo.GetAccessControl(Access)` → `SetAccessRuleProtection(isProtected:false, preserveInheritance:false)` → `SetAccessControl` | NTFS DACL flip (clears `SE_DACL_PROTECTED`) |
| `SetEveryonePermissions(filename)` | `DiskProvider.cs:69-107` | If `Everyone:Modify` not already present, adds an ACE `Allow Everyone Modify` with `ContainerInherit \| ObjectInherit` and `InheritOnly` propagation | `WellKnownSidType.WorldSid` → `SecurityIdentifier`; `DirectorySecurity.ModifyAccessRule(Add)` → `SetAccessControl` |
| `SetFilePermissions(path, mask, group)` | `DiskProvider.cs:109-111` | **No-op** | — |
| `SetPermissions(path, mask, group)` | `DiskProvider.cs:113-115` | **No-op** | — |
| `CopyPermissions(src, tgt)` | `DiskProvider.cs:117-119` | **No-op** | — |
| `GetTotalSize(path)` | `DiskProvider.cs:121-133` | Same as available-space, calls `DriveTotalSizeEx` | `kernel32!GetDiskFreeSpaceEx` |
| `TryCreateHardLink(src, dst)` | `DiskProvider.cs:169-185` | If `source.Length > 256` and not already long-path-prefixed, prepends `\\?\`. Calls `CreateHardLink(destination, source, IntPtr.Zero)` | `kernel32!CreateHardLink` |
| `GetReparsePoint(path)` | `DiskProvider.cs:187-208` | Walks `DirectoryInfo.Parent` chain looking for `FileAttributes.ReparsePoint`. Returns a `FolderMount` wrapping it, else `null` | `DirectoryInfo.Attributes` (no PInvoke) |

**Long-path injection is partial.** Only `TryCreateHardLink` prepends `\\?\`. Everything else (copy, move, delete) relies on .NET's path-length handling. .NET 6+ enables long paths transparently when `<longPathAware>true</longPathAware>` is set in app.manifest, but the Sonarr manifest's status here is undocumented. (WIN-Q1)

**No netsh URL ACL invocation.** Sonarr v5 binds via Kestrel, which on .NET 6+ uses Sockets directly (not http.sys). `netsh http add urlacl` is not needed. The historical Mono-era code path is gone. (Confirmed via `grep -r netsh\\\|urlacl src/` — zero hits.)

**Windows firewall opening uses COM, not netsh.** `src/NzbDrone.Host/AccessControl/FirewallAdapter.cs:22-87` calls `Type.GetTypeFromProgID("HNetCfg.FwMgr")` and `HNetCfg.FWOpenPort` to add ports to the standard profile. This is `INetFwMgr` from the Network Configuration COM library. The Go port either keeps this via `go-ole` or shells out `netsh advfirewall firewall add rule`. (WIN-Q2 — beyond this card's scope but cross-references `subsystems/host.md`.)

**ACL surface details:**
- `SetEveryonePermissions` propagation = `PropagationFlags.InheritOnly` (`DiskProvider.cs:92`). This means the ACE applies to children only, not the directory itself. Combined with `ContainerInherit | ObjectInherit`, descendants get an explicit Everyone-Modify ACE. (WIN-Q3 — is this intentional? Behaviour differs from a non-InheritOnly ACE which would also grant on the dir itself.)
- `InheritFolderPermissions` flips both flags to `false` — which means "stop inheriting from parent AND don't preserve already-inherited entries" (it does **not** translate inherited ACEs to explicit ones first). This is `EXPLICIT-ONLY` semantics and is destructive of inherited ACLs. (WIN-Q4)
- Both methods catch and rethrow on the directory level; only `SetEveryonePermissions` logs the failure first.

**`GetReparsePoint` parent-walking** (`DiskProvider.cs:187-208`): walks up the directory tree until either root (`Parent == null`) or a `ReparsePoint` flag is found. This catches NTFS junction points and mount-point-as-folder cases (e.g. `D:\Storage` mounted as `C:\Data\Storage`). For plain drive letters it returns `null` and the caller falls back to `DriveInfo`-based lookup.

**`FolderMount.cs:1-37`**: `IMount` impl wrapping a `DirectoryInfo` for a junction. Reports:
- `AvailableFreeSpace = 0`, `TotalFreeSpace = 0`, `TotalSize = 0` (it's a junction; free space comes from the underlying volume which is queried separately).
- `DriveFormat = "NTFS"` (hardcoded).
- `DriveType = DriveType.Removable` — odd choice; comment-free. (WIN-Q5)
- `MountOptions = null`.

### `EnvironmentInfo/WindowsVersionInfo.cs`
Single `IOsVersionAdapter` (`WindowsVersionInfo.cs:8-49`).
- `Enabled` gated by `OsInfo.IsWindows`.
- `Read()` calls `IsServer()` to discriminate "Windows" vs "Windows Server", then composes from `Environment.OSVersion.Version.ToString()` and `Environment.OSVersion.VersionString`.
- `IsServer()` (`WindowsVersionInfo.cs:25-47`): opens `HKLM\Software\Microsoft\Windows NT\CurrentVersion` via `RegistryKey.OpenBaseKey(LocalMachine, Registry32)`, reads `ProductName`, returns true if it contains the literal string `"server"` (case-insensitive). Catches and logs all exceptions.

The `RegistryView.Registry32` view selection is deliberate — on 64-bit Windows the registry is virtualized, but the `CurrentVersion` key is identical between the 32 and 64 hives. Using `Registry32` avoids needing the WoW64 redirector.

**Path/data-folder location.** Not in this project — see `NzbDrone.Common/EnvironmentInfo/AppFolderInfo.cs` (covered in `subsystems/common.md`). Defaults to `%APPDATA%\Sonarr` on Windows, `~/.config/Sonarr` on Linux/Mac. The `--data=` CLI flag overrides. No registry keys are consulted for paths.

### Process kill on Windows
**Not in this project.** Lives in `NzbDrone.Common/Processes/ProcessProvider.cs` and uses `System.Diagnostics.Process.Kill()` cross-platform, which on Windows internally calls `kernel32!TerminateProcess`. There is no Windows-specific override. (WIN-Q6 — no graceful-shutdown signal sent first; Linux gets the same `Kill()` which translates to `SIGKILL`. See MONO-Q10 below.)

---

## Sonarr.RuntimePatches

**Project file:** `src/Sonarr.RuntimePatches/Sonarr.RuntimePatches.csproj`
- Target: `net10.0`
- Dependency: `Lib.Harmony 2.4.2` — a .NET runtime IL-patching library. Harmony lets you replace, prefix, postfix, or transpile any method at runtime.

The whole project's purpose is to apply Harmony patches **before** the rest of the application loads, fixing Mono runtime bugs that would otherwise crash or misbehave Sonarr. **All of this is Mono-only**. On .NET 6+ (the current target since `net10.0`), the patches' `ShouldPatch()` returns `false` and nothing happens.

### Lifecycle
Entry point: `RuntimePatcher.Initialize()` (`RuntimePatcher.cs:10-41`).

Called from `src/NzbDrone.Console/ConsoleApp.cs:29` as the **first line of `Main`**, before logger registration, before bootstrap. This ordering is critical — any code path the patches modify must not have been JIT-compiled yet, otherwise Harmony's transpilers can't inject IL.

Environment-variable overrides (`RuntimePatcher.cs:12-30`):
- `DISABLE_RUNTIMEPATCHES=1` → entire patcher is skipped (no patches applied; logger inits normally; if Mono has the bugs, Sonarr misbehaves).
- `DEBUG_RUNTIMEPATCHES=1` → `RuntimePatchBase.IsDebug = true` (verbose patch-log to stdout).
- `DEBUG_RUNTIMEPATCHES=0` → forced off even in DEBUG builds.
- Else → `IsDebug` defaults to true in `#if DEBUG` builds, false in Release.

Failure mode (`RuntimePatcher.cs:34-39`):
- Wraps `ApplyPatches()` in try/catch.
- On exception, prints `"Failed to apply runtime patches, attempting to continue normally.\r\n" + ex.ToString()` to stdout (because logger isn't ready yet) and **continues**. The application starts even if patching failed. If the bugs the patches were meant to fix are present, Sonarr will hit them at runtime.

Discovery (`RuntimePatcher.cs:43-62`):
- `Assembly.GetExecutingAssembly().GetExportedTypes()` enumerates the public types in `Sonarr.RuntimePatches.dll`.
- Filters: `!type.IsAbstract && typeof(RuntimePatchBase).IsAssignableFrom(type)`.
- Each candidate is `Activator.CreateInstance`'d, cast to `RuntimePatchBase`, and asked `ShouldPatch()`.
- Survivors are passed a single shared `Harmony("tv.sonarr")` instance and `patch.Patch(harmony)` is invoked.

### Base classes

#### `RuntimePatchBase.cs`
- Abstract `protected abstract void Patch();` (no-arg overload, called by the public `Patch(Harmony)`).
- `protected void TryPatchMethod(string typeName, string methodName, params string[] paramTypes)` (`RuntimePatchBase.cs:78-90`): looks up `Type.GetType(typeName)`. **Bug-or-feature** (RP-Q1): on line 84, if the type is found, it always calls `TryPatchMethod(type, "GetSslServer")` — the hardcoded method name `"GetSslServer"` is wrong; this method's `methodName` parameter is ignored. Only `BoringTLSVerifyFlagsPatch` calls this overload, and it does pass `"GetSslServer"` as `methodName`, so the bug is currently latent — but any future patch using this overload with a different method name would silently no-op.
- `FindMethod` (`RuntimePatchBase.cs:29-67`): manual reflection lookup. Walks `BindingFlags.Public | NonPublic | Instance | Static`, matches name + parameter-type-by-string-name. Used because some Mono-internal types are non-public so `typeof()` can't reference them.
- `PatchMethod` (`RuntimePatchBase.cs:69-76`): looks up sibling methods named `Prefix_<n>` / `Postfix_<n>` / `Transpiler_<n>` and registers them with Harmony as wrappers/transpilers.
- `Log` writes to `Console.WriteLine` (`RuntimePatchBase.cs:141-144`) — there is no NLog dependency at this layer, again because the logger isn't initialized yet.

#### `MonoRuntimePatchBase.cs`
- Static ctor (lines 14-39): probes for `Type.GetType("Mono.Runtime")` and invokes its non-public static `GetDisplayName()` to extract a Mono version string (e.g. `"6.12.0.122"`). Stored in `MonoVersion : Version`. **If Mono is not present, `MonoVersion` is `null`.**
- `ShouldPatch` (`MonoRuntimePatchBase.cs:41-49`): returns `false` if `MonoVersion == null` (i.e. running under .NET, not Mono). Else `MonoMinVersion <= MonoVersion && MonoMaxVersion > MonoVersion`. Each subclass overrides `MonoMinVersion`/`MonoMaxVersion` to gate on a specific Mono version range.

This is the **detection mechanism** that makes the whole project a no-op on .NET Core / .NET 5+: `Type.GetType("Mono.Runtime")` returns `null` outside Mono, the constructor catches every exception silently, and `ShouldPatch` returns `false`.

#### `RuntimePatchExtensions.cs`
Helper utilities for transpilers:
- `Matches(this List<CodeInstruction>, params OpCode[])` (`RuntimePatchExtensions.cs:13-31`): compares opcode sequence (skipping `Nop`) to a template — used to validate that the bytecode of the target method has the expected shape before transpiling. If the IL has changed in a newer Mono version, the patch refuses to apply (logs `"Method construct different than expected"`).
- `GetParameterTypes(this MethodBase)`, `GetSimplifiedName` for logging.

### The patches themselves

#### `Mono/BoringTLSVerifyFlagsPatch.cs`
- **Mono bug:** [Mono #19886] — Mono's BoringTLS provider does not pass `X509_V_FLAG_TRUSTED_FIRST` (0x8000) to OpenSSL's chain verification. With cross-signed certificates (e.g. Let's Encrypt's old DST Root CA X3 signing the new ISRG Root X1), the chain build prefers the longer (legacy) chain and errors when the legacy root is expired.
- **Range:** Mono 5.0 ≤ v < 7.0 (`BoringTLSVerifyFlagsPatch.cs:20-21`).
- **Target method:** `Mono.Btls.MonoBtlsX509VerifyParam.GetSslServer()` (non-public; located via `Type.GetType("Mono.Btls.MonoBtlsX509VerifyParam, System")`).
- **Mechanism:** transpiler. Replaces `return Lookup("ssl_server", true)` with the equivalent of `var copy = orig.Copy(); orig.Dispose(); copy.SetFlags(0x8000); return copy;` (the readonly singleton has to be cloned before setting flags).
- **Effect on Go port:** zero. Go's `crypto/x509` does its own chain building with Trust Stores; this Mono OpenSSL-binding bug doesn't apply.

#### `Mono/DeflateStreamAsyncPatch.cs`
- **Mono bug:** [Mono #16122] — Mono 6.0 shipped `System.IO.Compression.DeflateStream.ReadAsyncMemory(Memory<byte>, CancellationToken)` and `WriteAsyncMemory(ReadOnlyMemory<byte>, CancellationToken)` as `throw new NotImplementedException()` stubs. These methods are called by `GzipStream` when used through the HTTP stack, so HTTPS+gzip downloads crash.
- **Range:** Mono 6.0 ≤ v < 6.0.0.334 (`DeflateStreamAsyncPatch.cs:17-18`).
- **Target methods:** `DeflateStream.ReadAsyncMemory` and `DeflateStream.WriteAsyncMemory`. These are `internal` in net4.7.2 so `typeof(DeflateStream).GetMethod(...)` finds them via `BindingFlags.NonPublic | Instance`.
- **Mechanism:** transpiler. Replaces `throw new NotImplementedException()` with `return base.ReadAsync(destination, cancellationToken);` (i.e. defers to the `Stream` base class's default impl).
- **Effect on Go port:** zero. Go's `compress/flate` and `compress/gzip` are first-class implementations.

### Patch summary

| Patch | File | Mono version range | Bug | Target | Failure mode if patch breaks |
|---|---|---|---|---|---|
| `BoringTLSVerifyFlagsPatch` | `Mono/BoringTLSVerifyFlagsPatch.cs` | 5.0 ≤ v < 7.0 | Mono#19886 — BoringTLS missing TRUSTED_FIRST | `MonoBtlsX509VerifyParam.GetSslServer` | TLS handshakes against cross-signed chains fail |
| `DeflateStreamAsyncPatch` | `Mono/DeflateStreamAsyncPatch.cs` | 6.0 ≤ v < 6.0.0.334 | Mono#16122 — `DeflateStream.{Read,Write}AsyncMemory` are `throw NotImplemented` | `DeflateStream.ReadAsyncMemory`, `DeflateStream.WriteAsyncMemory` | gzip-encoded HTTPS responses crash |

Both patches verify the IL shape via `Matches(...)` before applying; if Mono ever changed the method body, they log `"Method construct different than expected"` and refuse to patch (the bug then resurfaces). This is the safe-by-default failure mode.

### Go port disposition

**Delete the entire `Sonarr.RuntimePatches` project.** Justification:

1. **No runtime to patch.** Go has no equivalent of Mono's BCL — the standard library is shipped with the toolchain and is identical across all builds.
2. **The bugs don't exist.** Both bugs are Mono-specific implementations of methods that Go either has no equivalent for (no IL/JIT bug class) or implements correctly in `crypto/tls` and `compress/gzip`.
3. **The whole detection-and-patch infrastructure (Harmony) requires a managed runtime with introspection.** No analog needed.
4. **No replacement code.** The Go port simply does not include this layer. The startup ordering constraint (must run before everything else) similarly evaporates.

The `RP-Q*` open questions below all resolve to "delete in Go port" — flagged in case any future Mono-era code we haven't yet read references something here. The acceptance gate for skipping this project is "verify nothing in `NzbDrone.*` calls into `NzbDrone.RuntimePatches.*` types except the single `RuntimePatcher.Initialize()` call from `ConsoleApp.Main`" — confirmed via grep above (only `Sonarr.RuntimePatches/*` and `NzbDrone.Console/ConsoleApp.cs:11,29` reference the namespace).

---

## DB tables touched
None directly. These are infrastructure projects.

## Events raised / consumed
None directly.

## Config keys
None directly. Indirectly:
- `MountOptions` parsed by `ProcMountProvider` is downstream consumed by `subsystems/configuration.md`'s read-only-mount detection.
- `OsInfo.IsDocker` / `IsPodman` / `IsContainerized` (set in `OsInfo` ctor, `OsInfo.cs:84-92`) are set based on `/.dockerenv`, `/proc/1/cgroup`, `/run/.containerenv`, and the `container` and `DOTNET_RUNNING_IN_CONTAINER` env vars. These flow through `IOsInfo` to UI/health-check consumers.
- `DISABLE_RUNTIMEPATCHES` and `DEBUG_RUNTIMEPATCHES` env vars (RuntimePatcher only).

## Jobs / commands
None.

## External services
- `freebsd-version` shell-out (BSD only, `FreebsdVersionAdapter.cs:18`).

## Notable quirks / TODOs / HACKs found in source
- `DiskProvider.cs:316-319` (Mono) — comment block: "Mono 6.x throws errors if permissions or timestamps cannot be set; - In 6.0 it'll leave a full length file; - In 6.6 it'll leave a zero length file". The whole `TransferFilePatched` method is a workaround for this. Specifically, on `UnauthorizedAccessException`, it inspects the destination and if `dst.Length == src.Length`, samples up to 64 KB of the tail to verify content match before swallowing the error and proceeding to `LastWriteTimeUtc` adjustment.
- `DiskProvider.cs:321` (Mono) — comment: "Mono 6.x till 6.10 doesn't properly try use rename first." So `TransferFilePatched(move:true)` first tries `Syscall.rename` directly before delegating to `base.MoveFileInternal`.
- `DiskProvider.cs:413-416` (Mono) — `TryRenameFile` is a thin `Syscall.rename` wrapper used by `DiskTransferService` to attempt cheap atomic moves before falling back to copy+delete.
- `SymbolicLinkResolver.cs:32` — hardcoded loop limit of 32. No reference to `MAXSYMLINKS`/`SYMLOOP_MAX`. Mirrors but doesn't quote Linux's traditional ceiling.
- `NativeMethods.cs:10` — hardcoded ioctl request `0x40049409` for `FICLONE`. The comment explains the macro derivation (`_IOW(0x94, 9, int)`). The value is correct on x86_64; would be wrong on architectures that pack ioctl numbers differently (none of Sonarr's supported targets do, but it's a portability footgun).
- `MacOsVersionAdapter.cs:13` — regex matches version `1\d\.\d{1,2}\.?\d{0,2}?` — i.e. exactly `1X.YY[.ZZ]` form. macOS 26+ (if they jump the version number again) breaks this. (MONO-Q11)
- `IssueFileVersionAdapter.cs:40-43` — only Arch is recognized. Non-Arch distros get `null`-then-`ReleaseFileVersionAdapter`. The Arch test is "fileContent.Contains('Arch Linux')", which would also match an Arch-derivative `/etc/issue` saying "Manjaro Arch Linux" or whatever. (MONO-Q12)
- `WindowsVersionInfo.cs:35` — `productName.ToLower().Contains("server")` — substring match. A future "Windows 12 Server-Edition" would be detected; "Windows for Workgroups" would not. Brittle.
- `RuntimePatchBase.cs:84` — bug: hardcoded `"GetSslServer"` instead of using the `methodName` parameter. Latent because only `BoringTLSVerifyFlagsPatch` uses this overload and happens to want that method. RP-Q1.
- `RuntimePatcher.cs:36-39` — failure mode is "log to Console.WriteLine and continue". On a properly-configured logger the patch failure is invisible to anyone not watching stdout/journalctl on startup.
- `BoringTLSVerifyFlagsPatch.cs:30-31` — comment block explicitly notes that the patch location is suboptimal: "MonoBtlsProvider.GetVerifyParam would be a more 'correct' patch location". The chosen target (`MonoBtlsX509VerifyParam.GetSslServer`) is non-public, so a transpiler is needed.

## Test files (the port must keep passing equivalents)
- `src/NzbDrone.Mono.Test/DiskProviderTests/*` — chmod, free space, mount enumeration, hardlink, symlink copy fixtures. (Verify presence and adapt.)
- `src/NzbDrone.Mono.Test/EnvironmentInfo/VersionAdapterFixture.cs` (or per-adapter fixtures) — distro detection fixtures with sample `/etc/issue`, `/etc/os-release`, `/etc.defaults/VERSION`, `SystemVersion.plist` content.
- `src/NzbDrone.Windows.Test/DiskProviderTests/*` — long-path hardlink, reparse-point mount enumeration.
- `src/NzbDrone.Windows.Test/EnvironmentInfo/WindowsVersionInfoFixture.cs` (or equivalent) — server/desktop distinction.
- No tests for `Sonarr.RuntimePatches` are necessary in the Go port (project deleted).

---

## Open questions

### NzbDrone.Mono (MONO-Q*)

- **MONO-Q1** unresolved — `SetPermissions` mask-length quirk: 3-digit masks preserve setuid/setgid/sticky from existing `stat`, 4-digit masks overwrite. Document and replicate exactly. Blocks Phase 1 (disk provider port).
- **MONO-Q2** unresolved — `SetFilePermissions` does NOT strip executable bits on regular files; `SetPermissions` does. Two near-identical methods with subtly different behavior. Confirm this is deliberate vs accidental, then preserve. Blocks Phase 1 (disk provider port).
- **MONO-Q3** unresolved — `GetUserId` exists (`DiskProvider.cs:452-472`) but is not currently called. Either dead code or pre-wired for a future "set owner" feature. Confirm before deletion. Phase 1 (disk provider port).
- **MONO-Q4** unresolved — no special-file detection in `CopyFileInternal`/`MoveFileInternal`. FIFO/socket/block/char devices will be treated as regular files and copied byte-by-byte. Confirm this is acceptable (Sonarr only ever moves video files, but a misconfigured download client could feed a FIFO). Blocks Phase 2 (filesystem hardening).
- **MONO-Q5** unresolved — `SetPermissions` only chgrps, never chowns. Document explicit choice (matches Docker PUID/PGID convention where group is mapped, user isn't). Cosmetic; phase 1 (disk provider port).
- **MONO-Q6** unresolved — Replace `SymbolicLinkResolver` (32-iter manual walker) with `filepath.EvalSymlinks` in Go? Behavior should match for the cases Sonarr cares about (resolving root folders), but the C# code's `..` flattening rules are subtly custom. Spike before deciding. Blocks Phase 1 (disk provider port).
- **MONO-Q7** unresolved — `NativeMethods.clone_file` (FICLONE) exists but has no callers. Either this was prepared infrastructure for a planned feature, or it was wired up elsewhere and removed. Confirm before deciding whether to port. Phase 2 (CoW spike).
- **MONO-Q8** unresolved — `IOsVersionAdapter` discovery order is non-deterministic (assembly-scan); only one returns first. For Synology+Linux, the registration order between `SynologyVersionAdapter` and `ReleaseFileVersionAdapter` controls which wins. Confirm via test on a real DSM box, or add an explicit priority. Phase 1 (envinfo port).
- **MONO-Q9** unresolved — `IssueFileVersionAdapter` only recognizes Arch. Modern Arch sets `ID=arch` in `/etc/os-release`, which `ReleaseFileVersionAdapter` already picks up. Likely vestigial — confirm and delete. Phase 1 (envinfo port).
- **MONO-Q10** unresolved — `ProcessProvider.Kill` (in NzbDrone.Common) sends an unconditional `SIGKILL`-equivalent on Linux/macOS (`Process.Kill()`). No `SIGTERM`-then-`SIGKILL` graceful shutdown for child processes (e.g. ffprobe). Document; Go port can match this behavior or improve it. Cross-cite `subsystems/common.md:Processes`. Phase 2 (process management).
- **MONO-Q11** unresolved — `MacOsVersionAdapter` regex hardcoded to `1X.YY` form; macOS jumping to "26" or beyond would break detection. Trivial to widen. Phase 1 (envinfo port).
- **MONO-Q12** unresolved — `IssueFileVersionAdapter` "Contains('Arch Linux')" would match Manjaro and other derivatives. Tighten or remove (see MONO-Q9). Phase 1 (envinfo port).

### NzbDrone.Windows (WIN-Q*)

- **WIN-Q1** unresolved — Long-path support (`\\?\` prefix) is only injected in `TryCreateHardLink`. Other operations rely on .NET 6+'s `<longPathAware>true</longPathAware>` manifest. Confirm Sonarr's app.manifest has this flag; if not, copy/move > MAX_PATH may fail silently. Blocks Phase 1 (disk provider port).
- **WIN-Q2** unresolved — `FirewallAdapter` (in `NzbDrone.Host`) uses COM (`HNetCfg.FwMgr`); Go port either uses `go-ole` or shells out `netsh advfirewall`. Cross-references `subsystems/host.md`. Phase 2 (host bootstrap port).
- **WIN-Q3** unresolved — `SetEveryonePermissions` uses `PropagationFlags.InheritOnly`. Effect: Everyone-Modify applies to children but NOT the directory itself. Intentional? Test before porting. Phase 1 (disk provider port).
- **WIN-Q4** unresolved — `InheritFolderPermissions` calls `SetAccessRuleProtection(false, false)` which clears inherited ACLs WITHOUT preserving them as explicit entries. Destructive; intentional? Phase 1 (disk provider port).
- **WIN-Q5** unresolved — `FolderMount.DriveType` is hardcoded to `Removable` for any reparse point. NTFS junctions point at fixed drives; this misclassifies. Phase 2 (mount enumeration parity).
- **WIN-Q6** unresolved — Cross-platform `ProcessProvider.Kill` sends no graceful-shutdown signal on Windows either (`TerminateProcess` only). See MONO-Q10. Cross-cite. Phase 2 (process management).

### Sonarr.RuntimePatches (RP-Q*)

- **RP-Q1** resolved — bug at `RuntimePatchBase.cs:84` (`TryPatchMethod(type, "GetSslServer")` ignores `methodName` parameter). Currently latent because the only caller passes `"GetSslServer"` itself. **Disposition: delete entirely in Go port** — no Harmony, no patches, no project. Phase 1 (delete).
- **RP-Q2** resolved — `BoringTLSVerifyFlagsPatch` workaround for Mono#19886. **Disposition: delete entirely in Go port** — Go's `crypto/tls` does not have this bug. Phase 1 (delete).
- **RP-Q3** resolved — `DeflateStreamAsyncPatch` workaround for Mono#16122. **Disposition: delete entirely in Go port** — Go's `compress/gzip` does not have this bug. Phase 1 (delete).
- **RP-Q4** resolved — `RuntimePatcher.Initialize()` ordering constraint (must run first thing in `Main`). **Disposition: irrelevant in Go port** — no analog. Phase 1 (delete).
- **RP-Q5** resolved — env-var contract `DISABLE_RUNTIMEPATCHES` / `DEBUG_RUNTIMEPATCHES`. **Disposition: drop both env vars** — the Go port has no patch system, and these names will not appear in any documentation. If a user has them in their existing systemd unit, they'll be ignored harmlessly. Phase 1 (delete; mention in CONVERSION.md as removed env vars).

### Cross-cutting

- **NP-Q1** unresolved — `OsInfo.IsContainerized` heuristic (`OsInfo.cs:84-92`) checks `/.dockerenv`, `/proc/1/cgroup` for `/docker/`, `/run/.containerenv`, `container` env, `DOTNET_RUNNING_IN_CONTAINER`. The Go port should preserve all probes (the cgroup v2 case may need updating — `/proc/1/cgroup` format changed). Cross-cite `subsystems/common.md`. Phase 1 (envinfo port).
- **NP-Q2** unresolved — `AppFolderInfo` uses `Environment.SpecialFolder.ApplicationData` (`%APPDATA%` on Windows, `~/.config` on Linux/Mac) plus `XDG_CONFIG_HOME` env override — see `subsystems/common.md` for the full path-resolution chain. The Go port's equivalent should use `os.UserConfigDir()` which honors `XDG_CONFIG_HOME` natively. Phase 1 (envinfo port).
- **NP-Q3** unresolved — Static cache invariants in `ProcMountProvider._fileSystems` (`ProcMountProvider.cs:28`): one-shot read of `/proc/filesystems` per process. The Go port can either match (parse once into a `sync.Once`-guarded map) or re-read every call (cheap on Linux). Cross-cite `subsystems/configuration.md:692`. Phase 1 (disk provider port).
