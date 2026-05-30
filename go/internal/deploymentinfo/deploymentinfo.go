// Package deploymentinfo ports
// `NzbDrone.Core.Configuration.DeploymentInfoProvider` at
// `src/NzbDrone.Core/Configuration/DeploymentInfoProvider.cs`.
//
// Parses the `package_info` (and `release_info`) key=value files that
// DEB / RPM / Docker maintainers ship alongside the Sonarr binary —
// they surface the installer-pinned version, branch, and
// update-mechanism so the in-app updater can pick the right strategy.
//
// File layout (mirrors the C# provider's expectations):
//
//   - `<bin>/../package_info` — written by the OS packager (DEB / RPM
//     postinst, Docker entrypoint). One key=value per line.
//   - `<bin>/release_info` — bundled inside the release tarball; overrides
//     `ReleaseVersion` + the default branch when present.
//
// The C# regex `^Key=(.*)$` (RegexOptions.Multiline) is preserved
// verbatim — values are trimmed at the boundary.
//
// Consumers: the B.5 healthchecks `PackageGlobalMessageCheck` and
// `UpdateCheck` (both deferred until this package lands — see
// `AUDIT_FIX_BACKLOG.md` Open section).
package deploymentinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// UpdateMechanism ports
// `src/NzbDrone.Core/Update/UpdateMechanism.cs:5-12`. Values are pinned
// because the C# enum is persisted across the SignalR + config wires
// (e.g. `IConfigService.UpdateMechanism`).
type UpdateMechanism int

const (
	// MechanismBuiltIn — Sonarr's in-process updater downloads the new
	// build and restarts itself. Default for tarball / Windows installs.
	MechanismBuiltIn UpdateMechanism = 0

	// MechanismScript — calls an external script (configured at
	// `config/host.UpdateScriptPath`) with the new version's path.
	// Default when the operator wants a custom rollout.
	MechanismScript UpdateMechanism = 1

	// MechanismExternal — first "external" mechanism. Anything >= this
	// value is treated as externally-managed by [Provider.IsExternal].
	MechanismExternal UpdateMechanism = 10

	// MechanismApt — Debian / Ubuntu APT package.
	MechanismApt UpdateMechanism = 11

	// MechanismDocker — Docker container.
	MechanismDocker UpdateMechanism = 12
)

// String returns the canonical name — useful for log lines and for
// round-tripping through future SignalR / config wires.
func (m UpdateMechanism) String() string {
	switch m {
	case MechanismBuiltIn:
		return "BuiltIn"
	case MechanismScript:
		return "Script"
	case MechanismExternal:
		return "External"
	case MechanismApt:
		return "Apt"
	case MechanismDocker:
		return "Docker"
	default:
		return fmt.Sprintf("UpdateMechanism(%d)", int(m))
	}
}

// ParseUpdateMechanism is the exported form of [parseUpdateMechanism] —
// the case-insensitive `Enum.TryParse<UpdateMechanism>` used by the
// config getter `configfile.Provider.UpdateMechanism` (C#
// `ConfigFileProvider.UpdateMechanism` parses the config value via
// `GetValueEnum`). Returns false on unrecognized input so callers can
// apply the BuiltIn default.
func ParseUpdateMechanism(s string) (UpdateMechanism, bool) {
	return parseUpdateMechanism(s)
}

// parseUpdateMechanism mirrors the C# `Enum.TryParse<T>(value, true, out ...)`
// case-insensitive parse. Returns false on unrecognized input.
func parseUpdateMechanism(s string) (UpdateMechanism, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "builtin":
		return MechanismBuiltIn, true
	case "script":
		return MechanismScript, true
	case "external":
		return MechanismExternal, true
	case "apt":
		return MechanismApt, true
	case "docker":
		return MechanismDocker, true
	default:
		return MechanismBuiltIn, false
	}
}

// FileSystem is the slice of disk operations [Provider] needs.
// `*disk.Provider` satisfies this via its existing FileExists method
// + a `ReadAllText` wrapper around os.ReadFile; tests pass a stub that
// returns the test fixture's in-memory content.
type FileSystem interface {
	FileExists(path string) bool
	ReadAllText(path string) (string, error)
}

// Provider is the parsed deployment-info snapshot. All getters
// are read-only — the Provider is built once at boot and never
// mutated.
//
// Mirrors C# `IDeploymentInfoProvider` field-by-field; methods
// instead of properties to avoid exposing mutable fields.
type Provider struct {
	packageVersion            string
	packageAuthor             string
	packageGlobalMessage      string
	packageBranch             string
	packageUpdateMechanism    UpdateMechanism
	packageUpdateMechanismMsg string
	releaseVersion            string
	releaseBranch             string
	defaultBranch             string
}

// New builds a Provider by parsing the package_info (at
// `<binFolder>/../package_info`) and release_info (at
// `<binFolder>/release_info`) files.
//
// The C# constructor at `:33-71` gates the package_info parse on
// `Path.GetFileName(bin) == "bin"` — the OS-packager convention puts
// the executable in `/usr/lib/sonarr/bin` with package_info at
// `/usr/lib/sonarr/package_info`. The Go port preserves the same
// gate.
//
// Behaviour with missing files mirrors C#: no error returned, fields
// left at zero values (empty strings, MechanismBuiltIn). Callers MUST
// tolerate an "empty" Provider for tarball / dev installs.
func New(binFolder string, fs FileSystem) *Provider {
	p := &Provider{
		packageUpdateMechanism: MechanismBuiltIn,
		defaultBranch:          "main",
	}

	packageInfoPath := filepath.Join(binFolder, "..", "package_info")
	releaseInfoPath := filepath.Join(binFolder, "release_info")

	if filepath.Base(binFolder) == "bin" && fs.FileExists(packageInfoPath) {
		if data, err := fs.ReadAllText(packageInfoPath); err == nil {
			p.packageVersion = readValue(data, "PackageVersion", "")
			p.packageAuthor = readValue(data, "PackageAuthor", "")
			p.packageGlobalMessage = readValue(data, "PackageGlobalMessage", "")
			p.packageUpdateMechanism = readEnumValue(data, "UpdateMethod", MechanismBuiltIn)
			p.packageUpdateMechanismMsg = readValue(data, "UpdateMethodMessage", "")
			p.packageBranch = readValue(data, "Branch", "")
			p.releaseVersion = readValue(data, "ReleaseVersion", "")

			if strings.TrimSpace(p.packageBranch) != "" {
				p.defaultBranch = p.packageBranch
			}
		}
	}

	if fs.FileExists(releaseInfoPath) {
		if data, err := fs.ReadAllText(releaseInfoPath); err == nil {
			p.releaseVersion = readValue(data, "ReleaseVersion", p.releaseVersion)
			p.releaseBranch = readValue(data, "Branch", "")

			if strings.TrimSpace(p.releaseBranch) != "" {
				p.defaultBranch = p.releaseBranch
			}
		}
	}

	return p
}

// PackageVersion returns the OS-packager-pinned version string (e.g.
// `4.0.1.929-1`). Empty for non-packaged installs.
func (p *Provider) PackageVersion() string { return p.packageVersion }

// PackageAuthor returns the OS-packager-set author string (e.g.
// `Servarr`). Empty for non-packaged installs.
func (p *Provider) PackageAuthor() string { return p.packageAuthor }

// PackageGlobalMessage returns the OS-packager-set global broadcast
// message. The healthcheck `PackageGlobalMessageCheck` (deferred per
// AUDIT_FIX_BACKLOG.md B.5) surfaces non-empty values as a warning so
// the operator sees maintainer-issued advisories.
func (p *Provider) PackageGlobalMessage() string { return p.packageGlobalMessage }

// PackageBranch returns the OS-packager-pinned update branch (e.g.
// `main` / `develop`). Empty for non-packaged installs.
func (p *Provider) PackageBranch() string { return p.packageBranch }

// PackageUpdateMechanism returns the update mechanism the OS packager
// declared in `package_info`. Defaults to [MechanismBuiltIn] when not
// declared.
func (p *Provider) PackageUpdateMechanism() UpdateMechanism { return p.packageUpdateMechanism }

// PackageUpdateMechanismMessage returns the optional explanatory
// message the packager attached to the chosen mechanism. The
// healthcheck `UpdateCheck` (deferred per AUDIT_FIX_BACKLOG.md B.5)
// surfaces this in the warning text when an external mechanism is
// active.
func (p *Provider) PackageUpdateMechanismMessage() string { return p.packageUpdateMechanismMsg }

// ReleaseVersion returns the Sonarr-build-time version string (e.g.
// `4.0.1.929`). Sourced from package_info first, then overridden by
// release_info if present.
func (p *Provider) ReleaseVersion() string { return p.releaseVersion }

// ReleaseBranch returns the release_info-declared branch. Empty when
// `release_info` is missing or the file omits the Branch key.
func (p *Provider) ReleaseBranch() string { return p.releaseBranch }

// IsExternalUpdateMechanism reports whether the active update
// mechanism is externally-managed (Apt / Docker / generic External).
// Mirrors C# `:80`.
func (p *Provider) IsExternalUpdateMechanism() bool {
	return p.packageUpdateMechanism >= MechanismExternal
}

// DefaultUpdateMechanism returns the mechanism the updater should
// default to. Mirrors C# `:81` (which assigns
// `DefaultUpdateMechanism = PackageUpdateMechanism` post-init).
func (p *Provider) DefaultUpdateMechanism() UpdateMechanism {
	return p.packageUpdateMechanism
}

// DefaultBranch returns the branch the updater should default to.
// Resolution order (matching C# `:38-65`): release_info.Branch →
// package_info.Branch → hard-coded "main".
func (p *Provider) DefaultBranch() string { return p.defaultBranch }

// readValue extracts the value of `key=` from the multi-line file
// content. The C# regex `^Key=(.*)$` with RegexOptions.Multiline is
// preserved (Go's `(?m)` flag enables the same multiline semantic).
// Trims surrounding whitespace from the captured value. Returns
// `defaultValue` when no match.
func readValue(fileData, key, defaultValue string) string {
	// Escape `key` so a literal containing regex metacharacters can't
	// blow up — the call sites use static identifier keys today, but
	// hardening the helper costs one regexp.QuoteMeta call.
	re := regexp.MustCompile("(?m)^" + regexp.QuoteMeta(key) + "=(.*)$")
	if m := re.FindStringSubmatch(fileData); m != nil {
		return strings.TrimSpace(m[1])
	}
	return defaultValue
}

// readEnumValue parses a key value as an [UpdateMechanism].
// Falls back to `defaultValue` on missing key or unrecognized value.
func readEnumValue(fileData, key string, defaultValue UpdateMechanism) UpdateMechanism {
	v := readValue(fileData, key, "")
	if v == "" {
		return defaultValue
	}
	if parsed, ok := parseUpdateMechanism(v); ok {
		return parsed
	}
	return defaultValue
}

// OSFileSystem is a [FileSystem] that delegates to the os stdlib —
// the production binding when no disk.Provider is wired in (e.g. boot-
// time construction before the host-stack wiring runs).
type OSFileSystem struct{}

// FileExists returns true iff the path resolves to a regular file.
func (OSFileSystem) FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// ReadAllText reads the file's full contents as a UTF-8 string.
// Mirrors C# `IDiskProvider.ReadAllText`.
func (OSFileSystem) ReadAllText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
