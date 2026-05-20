// Package freebox ports the C# Freebox Download client at
// `src/NzbDrone.Core/Download/Clients/FreeboxDownload/`.
//
// Freebox Download is the built-in torrent + HTTP download manager
// in Freebox ISP routers (French ISP free.fr). It exposes a JSON-
// over-HTTPS API at `https://<host>:<port>/<api_base>/<version>/`
// (default `https://mafreebox.freebox.fr:443/api/v1/`). Sonarr
// drives it via:
//
//   - `/login` (GET) — fetch the auth challenge.
//   - `/login/session` (POST) — exchange app_id + HMAC-SHA1(
//     challenge, AppToken) for a session_token cached per-
//     (host, appId, appToken).
//   - `/downloads/` (GET) — task list (GetItems).
//   - `/downloads/add` (POST) — add torrent file or URL.
//   - `/downloads/<id>` (PUT/DELETE) — update / remove task.
//   - `/downloads/config/` (GET) — global download config
//     (GetStatus uses it for OutputRootFolders).
//
// Functionally complete across the 5.J79 → 5.J87 arc:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Proxy] with the HMAC-SHA1 session-token flow + the full
//     /downloads surface: GetDownloadConfiguration (Test path
//     resolver), AddTaskFromURL (magnet add via form-encoded POST),
//     AddTaskFromFile (.torrent add via multipart POST),
//     SetTorrentSettings (PUT for status/queue_pos/stop_ratio),
//     GetTasks (list with the snake-case status enum), DeleteTask
//     (cancel via DELETE with optional /erase suffix).
//   - [TorrentFreeboxDownload] Provider implementing every
//     [downloadclients.Provider] method against the Freebox V1 API.
//   - Registry registration via init().
//
// Documented deferred piece: the GetStatus OutputRootFolders arm
// (the C# version composes the OutputRootFolders entry from the
// GetDownloadConfiguration result + RemotePathMapper translation).
package freebox

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `TorrentFreeboxDownload` +
// `FreeboxDownloadSettings`.
const (
	Implementation = "TorrentFreeboxDownload"
	ConfigContract = "FreeboxDownloadSettings"
)

// Priority constants port `FreeboxDownloadPriority.cs:3-7`.
const (
	PriorityLast  = 0
	PriorityFirst = 1
)

// categoryPattern mirrors the C# `^\.?[-a-z]*$/i` at
// `FreeboxDownloadSettings.cs:23`.
var categoryPattern = regexp.MustCompile(`^\.?[-a-zA-Z]*$`)

// Settings ports `FreeboxDownloadSettings` at
// `src/NzbDrone.Core/Download/Clients/FreeboxDownload/FreeboxDownloadSettings.cs:36-89`.
//
// Defaults mirror the C# constructor at `:40-46`:
// Host="mafreebox.freebox.fr", Port=443, UseSsl=true, ApiUrl="/api/v1/".
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the Freebox hostname. C# `:50`.
	Host string `json:"host"`

	// Port is the Freebox HTTPS port. C# `:54`.
	Port int `json:"port"`

	// UseSsl flips http→https; defaults true since the Freebox
	// official endpoint is HTTPS-only. C# `:58`.
	UseSsl bool `json:"useSsl"`

	// ApiUrl is the API base path including the version segment.
	// Default `/api/v1/`. C# `:63`.
	ApiUrl string `json:"apiUrl"`

	// AppId + AppToken drive the HMAC-SHA1 challenge → session-
	// token flow. C# `:66,69`.
	AppId    string `json:"appId"`
	AppToken string `json:"appToken"`

	// DestinationDirectory is the optional Freebox-side absolute
	// path where new tasks land. Mutually exclusive with Category.
	// C# `:72`.
	DestinationDirectory string `json:"destinationDirectory"`

	// Category is the optional sub-folder name appended to the
	// Freebox-side default-destination folder. Mutually exclusive
	// with DestinationDirectory. C# `:75`. Must match
	// [categoryPattern].
	Category string `json:"category"`

	// RecentPriority + OlderPriority pick the queue-position
	// behaviour for recent vs older episode grabs (Last=0, First=1).
	// C# `:78,81`.
	RecentPriority int `json:"recentPriority"`
	OlderPriority  int `json:"olderPriority"`

	// AddPaused starts new tasks in Stopped state. C# `:84`.
	AddPaused bool `json:"addPaused"`
}

// NewSettings returns a Settings with the C# constructor defaults.
func NewSettings() Settings {
	return Settings{
		Host:   "mafreebox.freebox.fr",
		Port:   443,
		UseSsl: true,
		ApiUrl: "/api/v1/",
	}
}

// Validate ports `FreeboxDownloadSettingsValidator` at C# `:10-33`.
//
// Rules:
//
//  1. Host non-empty.
//  2. Port 1..65535.
//  3. ApiUrl non-empty and must start with `/` (C# `ValidUrlBase`).
//  4. AppId required.
//  5. AppToken required.
//  6. Category must match [categoryPattern] when set.
//  7. DestinationDirectory and Category are mutually exclusive.
func (s Settings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Host",
			ErrorMessage: "Host is required",
		})
	}
	if s.Port < 1 || s.Port > 65535 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Port",
			ErrorMessage: "Port must be between 1 and 65535",
		})
	}
	if strings.TrimSpace(s.ApiUrl) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "ApiUrl",
			ErrorMessage: "'API URL' must not be empty.",
		})
	} else if !strings.HasPrefix(s.ApiUrl, "/") {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "ApiUrl",
			ErrorMessage: "API URL must start with '/'",
		})
	}
	if strings.TrimSpace(s.AppId) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "AppId",
			ErrorMessage: "'App ID' must not be empty.",
		})
	}
	if strings.TrimSpace(s.AppToken) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "AppToken",
			ErrorMessage: "'App Token' must not be empty.",
		})
	}
	if s.Category != "" && !categoryPattern.MatchString(s.Category) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Category",
			ErrorMessage: "Allowed characters a-z and -",
		})
	}
	if strings.TrimSpace(s.DestinationDirectory) != "" && strings.TrimSpace(s.Category) != "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Category",
			ErrorMessage: "Cannot use 'Category' and 'Destination Directory' at the same time.",
		})
	}
	return failures
}
