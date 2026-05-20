// Package downloadstation ports the Synology Download Station
// download client at
// `src/NzbDrone.Core/Download/Clients/DownloadStation/`.
//
// Download Station is the torrent + usenet client built into
// Synology DiskStation Manager (DSM). It exposes a multi-version
// JSON API at `/webapi/<path>.cgi` where the per-API path is
// discovered via the SYNO.API.Info query endpoint and the
// session is managed via SYNO.API.Auth login → SID. The Sonarr
// C# tree wraps this with:
//
//   - DiskStationProxyBase — API discovery + session cache +
//     ProcessRequest with error-message tables.
//   - DownloadStationTaskProxyV1 / V2 — task add/list/remove for
//     legacy + DSM 7+ API surfaces; the V1/V2 selector picks at
//     runtime based on discovered MaxVersion.
//   - DSMInfoProxy / DownloadStationInfoProxy / FileStationProxy
//     — DSM-side info + file-listing for the path-validation arm.
//   - SharedFolderResolver — translates Sonarr-side paths to the
//     Synology shared-folder root the Download Station can write
//     into.
//   - TorrentDownloadStation + UsenetDownloadStation — symmetric
//     Provider halves sharing the underlying proxies.
//
// Slice 5.J75 (this slice) ships the SKELETON for the Torrent
// half:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Proxy] with API discovery (SYNO.API.Info query) + session
//     login (SYNO.API.Auth) + per-(user,host,port) SID cache
//     with the 6-hour TTL the C# code uses.
//   - [TorrentDownloadStation] Provider with Name + Protocol +
//     Definition + Settings + Test (auth probe) + GetStatus
//     (IsLocalhost-only) + identity methods. Download / GetItems
//     / RemoveItem stub out to [ErrNotImplemented].
//   - Registry registration via init().
//
// Follow-up slices fill in (in roughly the order they layer):
//
//   - J76: DownloadStationTaskProxyV1 + the task-list/add/remove
//     surface against the DSM 6 API set; Download + GetItems +
//     RemoveItem for TorrentDownloadStation.
//   - J77: DownloadStationTaskProxyV2 + the V1/V2 selector at
//     runtime.
//   - J78: UsenetDownloadStation half + nzb-upload path.
//   - J79: SharedFolderResolver + the TestOutputPath arm in Test.
package downloadstation

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the Torrent half. Mirrors C# class names `TorrentDownloadStation`
// + `DownloadStationSettings`.
const (
	TorrentImplementation = "TorrentDownloadStation"
	ConfigContract        = "DownloadStationSettings"
)

// categoryPattern mirrors the C# `[-a-z]*$` regex with `^\.?` prefix
// at `DownloadStationSettings.cs:20` — TvCategory allows the chars
// a-z (case-insensitive) and `-`, with an optional leading dot.
var categoryPattern = regexp.MustCompile(`^\.?[-a-zA-Z]*$`)

// directoryPattern mirrors the C# `^(?!/).+` rule at
// `DownloadStationSettings.cs:16` — TvDirectory cannot start with
// `/`. The C# version uses a negative lookahead which Go's regexp
// engine doesn't support, so the Go port matches the equivalent
// "non-empty + first char not `/`" predicate inline.

// Settings ports `DownloadStationSettings` at
// `src/NzbDrone.Core/Download/Clients/DownloadStation/DownloadStationSettings.cs:28-65`.
//
// Defaults mirror the C# constructor at `:54-58`: Host="127.0.0.1",
// Port=5000 (DSM 6 default; DSM 7 ships 5001 but operators routinely
// re-bind; the Go port preserves the C# default).
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the DSM host. C# `:33`.
	Host string `json:"host"`

	// Port is the DSM port (default 5000). C# `:36`.
	Port int `json:"port"`

	// UseSsl flips http→https. C# `:40`.
	UseSsl bool `json:"useSsl"`

	// Username + Password drive the SYNO.API.Auth login. C# `:43,46`.
	Username string `json:"username"`
	Password string `json:"password"`

	// TvCategory is the optional sub-folder name appended to the
	// DSM default-destination folder. Mutually exclusive with
	// TvDirectory. C# `:49`. Must match [categoryPattern].
	TvCategory string `json:"tvCategory"`

	// TvDirectory is the optional absolute-on-DSM directory where
	// downloads land. Mutually exclusive with TvCategory; must NOT
	// start with `/` (Synology paths are root-less / relative to
	// the shared folder). C# `:52`.
	TvDirectory string `json:"tvDirectory"`
}

// NewSettings returns a Settings with the C# constructor defaults.
func NewSettings() Settings {
	return Settings{
		Host: "127.0.0.1",
		Port: 5000,
	}
}

// Validate ports `DownloadStationSettingsValidator` at C# `:9-25`.
//
// Rules:
//
//  1. Host non-empty.
//  2. Port 1..65535.
//  3. TvDirectory (when non-empty) cannot start with `/`.
//  4. TvCategory must match [categoryPattern] when set.
//  5. TvCategory and TvDirectory are mutually exclusive.
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
	if strings.TrimSpace(s.TvDirectory) != "" && strings.HasPrefix(s.TvDirectory, "/") {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvDirectory",
			ErrorMessage: "Cannot start with /",
		})
	}
	if s.TvCategory != "" && !categoryPattern.MatchString(s.TvCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Allowed characters a-z and -",
		})
	}
	if strings.TrimSpace(s.TvDirectory) != "" && strings.TrimSpace(s.TvCategory) != "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Cannot use Category and Directory",
		})
	}
	return failures
}
