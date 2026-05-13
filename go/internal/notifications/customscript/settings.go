// Package customscript ports the C# `NzbDrone.Core.Notifications.CustomScript`
// concrete notification provider. Slice 4.CK (C-cleanup [C3]
// sub-arc C3j).
//
// Surface: Settings + Validate + Proxy (exec the user-configured
// script with per-event env vars set via os/exec.Cmd.Env) + all 11
// OnX hooks.
//
// Deferred (matches C5 #11): a large fraction of the C# env-var
// surface references unported types (parser.RemoteEpisode +
// ParsedEpisodeInfo + Release, MediaFiles.MediaInfo, TrackedDownload,
// CustomFormatInfo, IsoLanguages, ITagRepository tag-label lookup).
// The Go port emits ONLY the headline fields available on the
// ported message + series shapes; unported variables are set to
// empty strings. The orchestrator can wire richer env vars when the
// upstream types port.
package customscript

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `CustomScriptSettings` at
// `Notifications/CustomScript/CustomScriptSettings.cs:26-40`.
type Settings struct {
	// Path is the on-disk path to the script — REQUIRED. C# `:31`.
	Path string `json:"path"`
	// Arguments is rejected by the validator per C# `:20-22` —
	// kept on the struct only for legacy-row round-trip; never
	// passed to the child process.
	Arguments string `json:"arguments"`
}

// Validate ports the C# `CustomScriptSettingsValidator` at `:9-23`.
//
// Rules:
//   - Path non-empty.
//   - Arguments must be empty (legacy field deprecated per C# `:20-22`).
//
// The C# SystemFolderValidator (rejects scripts inside Sonarr's own
// system folder) is deferred — the Go port has no portable
// equivalent yet.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Path) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Path", ErrorMessage: "'Path' must not be empty."})
	}
	if strings.TrimSpace(s.Arguments) != "" {
		out = append(out, notifications.TestFailure{PropertyName: "Arguments", ErrorMessage: "Arguments are no longer supported for custom scripts"})
	}
	return out
}
