// Package blackhole ports the C# Blackhole download clients at
// `src/NzbDrone.Core/Download/Clients/Blackhole/`. The Blackhole
// clients are filesystem-only — there's no RPC; the download
// methods drop a `.nzb` / `.torrent` / `.magnet` file into a
// configured folder and a separate process (NZBGet / qBittorrent /
// whatever the operator wired up) picks it up.
//
// Slice 5.J57 (this slice) ships the [TorrentBlackhole] half of the
// pair:
//
//   - [TorrentSettings] + [NewTorrentSettings] + Validate.
//   - [TorrentBlackhole] Provider with full Download (file write to
//     TorrentFolder for .torrent bytes, file write to TorrentFolder
//     for magnet links when [TorrentSettings.SaveMagnetFiles] is
//     set) + full Test (folder existence checks) + full RemoveItem
//     (delete-when-deleteData) + full GetStatus.
//   - [TorrentBlackhole.GetItems] returns an empty slice with a
//     documented divergence — the C# `ScanWatchFolder` service is
//     ~200 LOC of disk-scanning + file-locking + 30s grace-period +
//     5-min cache TTL logic that warrants its own slice. Until
//     ScanWatchFolder ports, completed downloads sitting in the
//     watch folder won't surface in the queue UI; the import path
//     still works because the manual-import flow walks the watch
//     folder directly. Documented in [TorrentBlackhole.GetItems].
//
// init() registers `(TorrentBlackhole, TorrentBlackholeSettings)`
// in [downloadclients.Default].
//
// Follow-up slices:
//
//   - [UsenetBlackhole] half (symmetric — .nzb writes; same
//     GetItems gap).
//   - [ScanWatchFolder] port (closes the GetItems gap for both
//     halves; ports the C# `ScanWatchFolder.cs:1-202` file walk +
//     in-progress detection + cache).
//
// C# cites lead each type with file:line references.
package blackhole

import (
	"errors"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// TorrentImplementation + TorrentConfigContract are the
// persisted-row identifiers. Mirrors C# class names `TorrentBlackhole`
// + `TorrentBlackholeSettings`.
const (
	TorrentImplementation = "TorrentBlackhole"
	TorrentConfigContract = "TorrentBlackholeSettings"
)

// TorrentSettings ports `TorrentBlackholeSettings` at
// `src/NzbDrone.Core/Download/Clients/Blackhole/TorrentBlackholeSettings.cs:20-54`.
//
// Defaults mirror the C# constructor at `:22-26`:
// MagnetFileExtension=".magnet", ReadOnly=true,
// SaveMagnetFiles=false.
type TorrentSettings struct {
	downloadclients.BaseTorrentSettings

	// TorrentFolder is the directory the Download method writes
	// .torrent (and optionally .magnet) files into. C# `:32`.
	TorrentFolder string `json:"torrentFolder"`

	// WatchFolder is the directory GetItems scans for in-flight
	// downloads + the queue-cleanup branch deletes from. C# `:35`.
	WatchFolder string `json:"watchFolder"`

	// SaveMagnetFiles toggles the magnet-link arm of Download.
	// false → AddFromMagnetLink throws NotSupportedException
	// (C# `:46-49`). true → write the magnet URL as the body of a
	// `<title>.<MagnetFileExtension>` file. C# `:40`.
	SaveMagnetFiles bool `json:"saveMagnetFiles"`

	// MagnetFileExtension is the suffix the magnet-link file uses
	// when [SaveMagnetFiles] is true. Defaults to ".magnet". C#
	// `:43`. Leading dots are tolerated (the C# code calls
	// `.Trim('.')` at the file-name compose step).
	MagnetFileExtension string `json:"magnetFileExtension"`

	// ReadOnly flips the Item.CanMoveFiles flag the writer
	// consults. true (the C# default) → CanMoveFiles=false →
	// writer copies rather than moves. C# `:48`.
	ReadOnly bool `json:"readOnly"`
}

// NewTorrentSettings returns a TorrentSettings with the C#
// constructor defaults applied.
func NewTorrentSettings() TorrentSettings {
	return TorrentSettings{
		MagnetFileExtension: ".magnet",
		ReadOnly:            true,
	}
}

// Validate ports `TorrentBlackholeSettingsValidator` at C# `:10-18`.
// The C# code rules:
//
//   - TorrentFolder must be a "valid path" (FluentValidation custom
//     rule that delegates to `IsValidPath`). The Go-port mirrors
//     with a non-empty check + an absolute-vs-empty discriminant —
//     full path-syntax validation is the host's responsibility
//     (the same C# rule degrades to non-empty when running outside
//     Windows in practice).
//   - MagnetFileExtension must be non-empty.
//
// The C# `WatchFolder` is not validated by FluentValidation but is
// checked at Test time. The Go port follows the same split:
// Settings.Validate guards what the form-validator catches, Test
// guards what only runs at the network/disk probe boundary.
func (s TorrentSettings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.TorrentFolder) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TorrentFolder",
			ErrorMessage: "TorrentFolder is required",
		})
	}
	if strings.TrimSpace(s.MagnetFileExtension) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "MagnetFileExtension",
			ErrorMessage: "MagnetFileExtension is required",
		})
	}
	return failures
}

// ErrSettingsTypeMismatch is returned when the constructor receives
// a Settings JSON body that doesn't decode cleanly.
var ErrSettingsTypeMismatch = errors.New("blackhole: settings type mismatch")
