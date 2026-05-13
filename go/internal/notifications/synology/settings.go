// Package synology ports the C# `NzbDrone.Core.Notifications.Synology`
// SynologyIndexer concrete notification provider. Slice 4.CI
// (C-cleanup [C3] sub-arc C3h).
//
// Surface: Settings + Validate + Proxy (exec /usr/syno/bin/synoindex
// per-file/folder library-update) + the 6 OnX hooks the C# class
// overrides (no Grab/HealthIssue/HealthRestored/AppUpdate/Manual).
//
// The 5 non-overridden OnX hooks (OnGrab/OnHealthIssue/OnHealthRestored/
// OnApplicationUpdate/OnManualInteractionRequired) are no-ops with
// SupportsOnX returning false — mirrors C# `NotificationBase` defaults.
//
// Platform: Linux-only. Test() returns an error on non-Linux per C#
// `:100-102`. The proxy execs `/usr/syno/bin/synoindex` directly.
package synology

import "sonarr.tv/sonarr/internal/notifications"

// Settings ports `SynologyIndexerSettings` at
// `Notifications/Synology/SynologyIndexerSettings.cs:11-27`.
type Settings struct {
	// UpdateLibrary toggles per-event synoindex execution. Defaults
	// to true. C# `:21`.
	UpdateLibrary bool `json:"updateLibrary"`
}

// Validate ports the empty C# `SynologyIndexerSettingsValidator` at
// `:7-9`. No per-property rules; always returns an empty failure list.
func (s *Settings) Validate() []notifications.TestFailure {
	return nil
}
