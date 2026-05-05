package healthcheck

import (
	"context"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/ospath"
)

// AppDataLocationCheckName is the canonical Source. Mirrors the C#
// type-name reflection at `HealthCheckService.cs:97` (`c.GetType().Name`).
const AppDataLocationCheckName = "AppDataLocationCheck"

// AppDataLocationCheck warns when the app's data directory sits inside
// (or is the same as) the binary's startup directory. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/AppDataLocationCheck.cs:1-32`.
//
// Scenario the C# tree is guarding against: a user installed Sonarr
// alongside its data dir (often via a self-extracting archive into
// the app's own folder), so an in-place update — which deletes the
// startup folder — would also nuke their settings + database.
type AppDataLocationCheck struct {
	folder appenv.AppFolderInfo
}

// NewAppDataLocationCheck builds the check against the host's
// AppFolderInfo. The struct is value-typed so no nil guard is needed.
func NewAppDataLocationCheck(folder appenv.AppFolderInfo) *AppDataLocationCheck {
	return &AppDataLocationCheck{folder: folder}
}

// Name returns the canonical check identifier.
func (*AppDataLocationCheck) Name() string { return AppDataLocationCheckName }

// CheckOnStartup defaults true (HealthCheckBase default).
func (*AppDataLocationCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true (HealthCheckBase default).
func (*AppDataLocationCheck) CheckOnSchedule() bool { return true }

// Run is the C# Check() port. Returns Warning when AppData equals or
// lives under Startup. Mirrors `:17-29`.
func (c *AppDataLocationCheck) Run(_ context.Context) HealthCheck {
	startup := ospath.New(c.folder.Startup)
	appData := ospath.New(c.folder.AppData)

	// IsParentPath OR PathEquals — Contains returns true for either
	// since equal paths are a degenerate "X contains X" case under
	// the OsPath Contains semantics.
	if startup.Contains(appData) || startup.Equals(appData) {
		return New(
			AppDataLocationCheckName,
			ResultWarning,
			ReasonAppDataLocation,
			appDataLocationMessage,
			"#updating-will-not-be-possible-to-prevent-deleting-appdata-on-update",
		)
	}
	return OK(AppDataLocationCheckName)
}

// appDataLocationMessage is the localised string at C#
// `Localization/Core/en.json` key `AppDataLocationHealthCheckMessage`.
// The Go port pins the English-fallback verbatim until the
// LocalizationService lands; the wiki fragment is explicit so the
// message text can later be localised without changing the URL.
const appDataLocationMessage = "Updating will not be possible to prevent deleting AppData on Update"
