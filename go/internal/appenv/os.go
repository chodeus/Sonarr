// Package appenv ports the runtime / paths / startup-args portion of
// NzbDrone.Common.EnvironmentInfo. Phase 1 ships the focused subset
// that downstream Phase 1 code already needs:
//
//   - OsInfo — runtime OS detection (linux/darwin/windows/freebsd)
//     plus container detection (Docker / Podman / generic).
//   - AppFolderInfo — the canonical paths the rest of the binary
//     reads (data dir, config file path, temp dir).
//   - StartupContext — minimal CLI arg parser (key=value flags +
//     boolean flags) sufficient to honour the `--data` override.
//
// Phase 5 will fold in the full IOsVersionAdapter chain (parses
// /etc/os-release on Linux, sw_vers on macOS, registry on Windows)
// and the BuildInfo / RuntimeInfo that today live in C# alongside.
// The Phase 1 surface is enough to wire datastore + configfile +
// auth into a single host binary.
package appenv

import (
	"os"
	"runtime"
	"strings"
)

// Os is the runtime OS classification mirroring
// NzbDrone.Common.EnvironmentInfo.Os.
type Os int

const (
	// OsUnknown means runtime.GOOS produced an unrecognised value.
	OsUnknown Os = iota
	// OsLinux includes both glibc and musl. The C# tree has a
	// separate LinuxMusl enum value driven by a build flag; Go has
	// no runtime way to tell glibc from musl, so the Phase 1 port
	// collapses to OsLinux. If musl-specific behaviour ever needs
	// branching, add a build-tag-gated detector.
	OsLinux
	// OsDarwin is macOS.
	OsDarwin
	// OsWindows is any Windows version.
	OsWindows
	// OsBSD covers FreeBSD / NetBSD / OpenBSD.
	OsBSD
)

// CurrentOs returns the runtime OS classification. Computed once at
// package init time (runtime.GOOS is a const).
var CurrentOs = detectOs()

func detectOs() Os {
	switch runtime.GOOS {
	case "linux":
		return OsLinux
	case "darwin":
		return OsDarwin
	case "windows":
		return OsWindows
	case "freebsd", "netbsd", "openbsd":
		return OsBSD
	}
	return OsUnknown
}

// IsLinux reports whether the runtime is any Linux flavour or BSD.
// Mirrors the C# `OsInfo.IsLinux` helper which also bundles BSD.
func IsLinux() bool { return CurrentOs == OsLinux || CurrentOs == OsBSD }

// IsDarwin reports macOS.
func IsDarwin() bool { return CurrentOs == OsDarwin }

// IsWindows reports Windows.
func IsWindows() bool { return CurrentOs == OsWindows }

// IsNotWindows is the affirmative form Sonarr's C# config uses
// pervasively (e.g. "_dataSpecialFolder = IsNotWindows ?
// ApplicationData : CommonApplicationData"). Provided here for direct
// parity in callers that already think in the C# idiom.
func IsNotWindows() bool { return !IsWindows() }

// OsInfo is the per-process instance with container detection. Most
// callers use the package-level CurrentOs / IsXxx helpers; OsInfo is
// for code that wants a single value to inject (matches the C#
// `IOsInfo` injection surface).
type OsInfo struct {
	Os              Os
	IsDocker        bool
	IsPodman        bool
	IsContainerized bool
}

// DetectOs constructs an OsInfo by inspecting filesystem markers.
// Docker / Podman / generic-container detection only runs on Linux —
// the markers don't exist on macOS / Windows hosts.
//
// Markers (matching `OsInfo.cs:84-92`):
//   - Docker: `/.dockerenv` exists OR `/proc/1/cgroup` contains
//     `/docker/`.
//   - Podman: `/run/.containerenv` exists OR the `container`
//     env var is set.
//   - Containerized: Docker || Podman || `DOTNET_RUNNING_IN_CONTAINER`
//     is `"true"` or `"1"`.
func DetectOs() OsInfo {
	info := OsInfo{Os: CurrentOs}
	if !IsLinux() {
		return info
	}
	info.IsDocker = pathExists("/.dockerenv") || cgroupContainsDocker()
	info.IsPodman = pathExists("/run/.containerenv") || os.Getenv("container") != ""
	info.IsContainerized = info.IsDocker || info.IsPodman ||
		isOneOf(os.Getenv("DOTNET_RUNNING_IN_CONTAINER"), "true", "1")
	return info
}

// pathExists is a tiny wrapper that swallows the "is the path there?"
// question while keeping detector code readable.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// cgroupContainsDocker matches the C# `File.ReadAllText("/proc/1/cgroup")
// .Contains("/docker/")` check. Modern container runtimes
// (containerd, cgroup v2) often don't expose the docker substring
// even when running inside Docker, so this is a fallback to
// /.dockerenv. Read errors are treated as "not docker".
func cgroupContainsDocker() bool {
	const path = "/proc/1/cgroup"
	if !pathExists(path) {
		return false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(body), "/docker/")
}

func isOneOf(s string, opts ...string) bool {
	for _, o := range opts {
		if s == o {
			return true
		}
	}
	return false
}
