package healthcheck

import (
	"context"
	"fmt"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// UpdateCheckName is the canonical Source identifier.
const UpdateCheckName = "UpdateCheck"

// UpdateCheckConfig surfaces the two ConfigFileProvider keys
// UpdateCheck consumes. `*configfile.Provider` doesn't satisfy this
// directly because its accessors take `context.Context` and return
// `(value, error)`; wire-time adapters narrow the surface so the
// check is decoupled from the config provider's transactional API.
type UpdateCheckConfig interface {
	UpdateAutomatically(ctx context.Context) (bool, error)
	// UpdateMechanism returns the configured mechanism as the C#
	// enum string (e.g. "BuiltIn", "Apt", "Docker"). Case-
	// insensitive comparison matches the C# `Enum.Parse(value, true)`.
	UpdateMechanism(ctx context.Context) (string, error)
}

// UpdateCheckOsInfo narrows [appenv.OsInfo] to the two flags the
// check reads.
type UpdateCheckOsInfo interface {
	IsDocker() bool
	IsDarwin() bool
}

// UpdateCheckFolderWritability checks if a folder can be written to.
// `*disk.Provider` satisfies this via its existing FolderWritable
// method.
type UpdateCheckFolderWritability interface {
	FolderWritable(path string) bool
}

// AvailableUpdate is the projection
// [UpdateAvailableLookup.AvailableUpdate] returns when a newer build
// is available from the upstream services.sonarr.tv feed.
//
// Mirrors `src/NzbDrone.Core/Update/UpdatePackage.cs` (subset). The
// fetch+parse pipeline isn't ported yet — UpdateCheck queries
// through the interface so the wiring path is stable; the production
// binding returns a nil lookup today (the UI just doesn't see the
// "update available" banner until the update-fetcher port lands).
type AvailableUpdate struct {
	Version string
}

// UpdateAvailableLookup is the narrow lookup the check uses to ask
// "is a newer build available?". `*update.Service` will satisfy
// this when ported; today the wiring passes a nil lookup which the
// check tolerates.
type UpdateAvailableLookup interface {
	AvailableUpdate(ctx context.Context) (*AvailableUpdate, error)
}

// UpdateCheck surfaces three writability concerns + an available-
// update banner. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/UpdateCheck.cs:1-104`.
//
// Audit fix B.5 (unblocked by the slice-5.J34 DeploymentInfoProvider
// port + the existing disk.Provider.FolderWritable + configfile
// UpdateAutomatically/UpdateMechanism keys).
//
// The C# tree gates steps 1-3 on `UpdateAutomatically &&
// UpdateMechanism == BuiltIn && !IsDocker` — the writability
// concerns only matter when Sonarr itself plans to apply updates.
// External / Apt / Docker / Script mechanisms hand off to the host,
// so the startup folder being read-only is the operator's problem to
// solve outside Sonarr.
//
// Step 4 (update-available banner) is gated on the build being older
// than 14 days. Skipped when `UpdateAvailableLookup` is nil — see
// the [UpdateAvailableLookup] doc-comment.
type UpdateCheck struct {
	disk      UpdateCheckFolderWritability
	startup   string
	cfg       UpdateCheckConfig
	osInfo    UpdateCheckOsInfo
	lookup    UpdateAvailableLookup
	buildDate time.Time
}

// NewUpdateCheck builds the check. `buildDate` is the parsed
// `buildinfo.BuildDate` (RFC3339 UTC); pass `time.Time{}` when the
// build date isn't known — step 4 will then never fire (matches the
// "is the build > 14d old" guard).
func NewUpdateCheck(
	disk UpdateCheckFolderWritability,
	startupFolder string,
	cfg UpdateCheckConfig,
	osInfo UpdateCheckOsInfo,
	lookup UpdateAvailableLookup,
	buildDate time.Time,
) (*UpdateCheck, error) {
	if disk == nil {
		return nil, fmt.Errorf("healthcheck: NewUpdateCheck: nil disk")
	}
	if cfg == nil {
		return nil, fmt.Errorf("healthcheck: NewUpdateCheck: nil cfg")
	}
	if osInfo == nil {
		return nil, fmt.Errorf("healthcheck: NewUpdateCheck: nil osInfo")
	}
	if strings.TrimSpace(startupFolder) == "" {
		return nil, fmt.Errorf("healthcheck: NewUpdateCheck: empty startupFolder")
	}
	return &UpdateCheck{
		disk:      disk,
		startup:   startupFolder,
		cfg:       cfg,
		osInfo:    osInfo,
		lookup:    lookup,
		buildDate: buildDate,
	}, nil
}

// Source returns the canonical identifier.
func (c *UpdateCheck) Source() string { return UpdateCheckName }

// Check evaluates the four conditions in order.
func (c *UpdateCheck) Check(ctx context.Context) HealthCheck {
	if c == nil {
		return OK(UpdateCheckName)
	}

	auto, _ := c.cfg.UpdateAutomatically(ctx)
	mechanism, _ := c.cfg.UpdateMechanism(ctx)
	mechanismLower := strings.ToLower(strings.TrimSpace(mechanism))

	if auto && mechanismLower == "builtin" && !c.osInfo.IsDocker() {
		// Step 1: macOS AppTranslocation guard. The C# helper
		// `GetAncestorFolders().Contains("AppTranslocation")` walks
		// every parent directory; the Go port checks any
		// path segment equals "AppTranslocation".
		if c.osInfo.IsDarwin() && containsSegment(c.startup, "AppTranslocation") {
			return New(UpdateCheckName,
				ResultError,
				ReasonUpdateStartupTranslocation,
				fmt.Sprintf("Cannot install update because startup folder %q is in an App Translocation folder.", c.startup),
				"#cannot-install-update-because-startup-folder-is-in-an-app-translocation-folder.")
		}

		// Step 2: startup folder writable.
		if !c.disk.FolderWritable(c.startup) {
			return New(UpdateCheckName,
				ResultError,
				ReasonUpdateStartupNotWritable,
				fmt.Sprintf("Cannot install update because startup folder %q is not writable by user %q.", c.startup, currentUserName()),
				"#cannot-install-update-because-startup-folder-is-not-writable-by-the-user")
		}

		// Step 3: UI subfolder writable.
		uiFolder := filepath.Join(c.startup, "UI")
		if !c.disk.FolderWritable(uiFolder) {
			return New(UpdateCheckName,
				ResultError,
				ReasonUpdateUiNotWritable,
				fmt.Sprintf("Cannot install update because UI folder %q is not writable by user %q.", uiFolder, currentUserName()),
				"#cannot-install-update-because-ui-folder-is-not-writable-by-the-user")
		}
	}

	// Step 4: build > 14 days old + update available.
	if c.lookup != nil && !c.buildDate.IsZero() && c.buildDate.Before(time.Now().UTC().AddDate(0, 0, -14)) {
		latest, err := c.lookup.AvailableUpdate(ctx)
		if err == nil && latest != nil && strings.TrimSpace(latest.Version) != "" {
			return New(UpdateCheckName,
				ResultWarning,
				ReasonUpdateAvailable,
				fmt.Sprintf("New update is available: v%s.", latest.Version),
				"#new-update-is-available")
		}
	}

	return OK(UpdateCheckName)
}

// containsSegment reports whether any path segment of `p` equals
// `seg`. Splits on the platform separator (`os.PathSeparator`) AND
// the forward-slash so paths from C#-like sources still match.
func containsSegment(p, seg string) bool {
	if p == "" || seg == "" {
		return false
	}
	for _, part := range splitAllSeparators(p) {
		if part == seg {
			return true
		}
	}
	return false
}

func splitAllSeparators(p string) []string {
	// Normalise backslashes to forward slashes, then split.
	p = strings.ReplaceAll(p, `\`, `/`)
	return strings.Split(p, "/")
}

// currentUserName returns os/user's username. Falls back to "user"
// when the lookup fails (containerised runs sometimes have no passwd
// entry); the message still parses cleanly.
func currentUserName() string {
	u, err := user.Current()
	if err != nil || u == nil || u.Username == "" {
		return "user"
	}
	return u.Username
}
