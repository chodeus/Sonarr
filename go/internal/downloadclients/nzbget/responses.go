package nzbget

// NzbgetParameter is the {Name, Value} pair NZBGet uses for
// per-NZB custom parameters. Sonarr appends a "drone" parameter on
// every add (via append's P[] param), and reads it back here so
// the queue/history items can cross-reference the original grab.
//
// Mirrors C# `NzbgetParameter` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/NzbgetParameter.cs:3-7`.
// Value is `any` since NZBGet returns the value as string OR int
// depending on how it was set (e.g. the C# tree sets it via
// editqueue's GroupSetParameter which always stringifies; but some
// pre-load paths attach it as a raw integer).
type NzbgetParameter struct {
	Name  string `json:"Name"`
	Value any    `json:"Value"`
}

// NzbgetQueueItem is the per-item shape `listgroups` returns.
// Mirrors C# `NzbgetQueueItem` at `NzbgetQueueItem.cs:5-22`.
//
// FileSize / RemainingSize / PausedSize are split into Lo+Hi 32-bit
// halves by NZBGet's wire format (legacy from when NZBGet ran on
// 32-bit platforms). The combined int64 is computed by [combineLoHi]
// at consumption time — matches C# `MakeInt64` at
// `Nzbget.cs:65-67,126`.
type NzbgetQueueItem struct {
	NzbId           int               `json:"NZBID"`
	FirstId         int               `json:"FirstID"`
	LastId          int               `json:"LastID"`
	NzbName         string            `json:"NZBName"`
	Category        string            `json:"Category"`
	FileSizeLo      uint              `json:"FileSizeLo"`
	FileSizeHi      uint              `json:"FileSizeHi"`
	RemainingSizeLo uint              `json:"RemainingSizeLo"`
	RemainingSizeHi uint              `json:"RemainingSizeHi"`
	PausedSizeLo    uint              `json:"PausedSizeLo"`
	PausedSizeHi    uint              `json:"PausedSizeHi"`
	MinPriority     int               `json:"MinPriority"`
	MaxPriority     int               `json:"MaxPriority"`
	ActiveDownloads int               `json:"ActiveDownloads"`
	Parameters      []NzbgetParameter `json:"Parameters"`
}

// NzbgetHistoryItem is the per-item shape `history` returns.
// Mirrors C# `NzbgetHistoryItem` at `NzbgetHistoryItem.cs:5-19`.
//
// The status fields (ParStatus, UnpackStatus, MoveStatus, ScriptStatus,
// DeleteStatus, MarkStatus) decide the final Sonarr-side status —
// see [SuccessStatus] + [DeleteFailedStatus] + the per-arm cascade
// in [Nzbget.torrentItemFromHistory].
type NzbgetHistoryItem struct {
	Id           int               `json:"ID"`
	Name         string            `json:"Name"`
	Category     string            `json:"Category"`
	FileSizeLo   uint              `json:"FileSizeLo"`
	FileSizeHi   uint              `json:"FileSizeHi"`
	ParStatus    string            `json:"ParStatus"`
	UnpackStatus string            `json:"UnpackStatus"`
	MoveStatus   string            `json:"MoveStatus"`
	ScriptStatus string            `json:"ScriptStatus"`
	DeleteStatus string            `json:"DeleteStatus"`
	MarkStatus   string            `json:"MarkStatus"`
	DestDir      string            `json:"DestDir"`
	FinalDir     string            `json:"FinalDir"`
	Parameters   []NzbgetParameter `json:"Parameters"`
}

// NzbgetGlobalStatus is the `status` RPC response shape. Mirrors C#
// `NzbgetGlobalStatus` at `NzbgetGlobalStatus.cs:3-13`. Consumed by
// [Nzbget.GetItems] for the paused-globally arm + the download-rate
// based RemainingTime estimate.
type NzbgetGlobalStatus struct {
	RemainingSizeLo     uint `json:"RemainingSizeLo"`
	RemainingSizeHi     uint `json:"RemainingSizeHi"`
	DownloadedSizeLo    uint `json:"DownloadedSizeLo"`
	DownloadedSizeHi    uint `json:"DownloadedSizeHi"`
	DownloadRate        int  `json:"DownloadRate"`
	AverageDownloadRate int  `json:"AverageDownloadRate"`
	DownloadLimit       int  `json:"DownloadLimit"`
	DownloadPaused      bool `json:"DownloadPaused"`
}

// NzbgetConfigItem is one {Name, Value} pair returned by the
// `config` RPC. Mirrors C# `NzbgetConfigItem` at
// `NzbgetConfigItem.cs:3-7`. The wire format always stringifies
// Value — even numeric/boolean settings come through as text.
type NzbgetConfigItem struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// NzbgetCategory is one per-category entry NZBGet's preferences
// expose. Mirrors C# `NzbgetCategory` at `NzbgetCategory.cs:3-9`.
// The Sonarr-side GetStatus arm looks up the category matching
// settings.TvCategory and surfaces its DestDir as the
// OutputRootFolders entry.
type NzbgetCategory struct {
	// Name is the user-facing category label (e.g. "tv", "movies").
	Name string `json:"-"`

	// DestDir is the per-category download directory — resolved
	// per the `${MainDir}` substitution + AppendCategoryDir
	// fallback at NzbgetGetCategories at `Nzbget.cs:242-253`.
	DestDir string `json:"-"`

	// Unpack is true when NZBGet auto-unpacks completed downloads
	// in this category.
	Unpack bool `json:"-"`

	// DefScript is the default post-processing script for the
	// category (empty when none).
	DefScript string `json:"-"`

	// Aliases is a comma-separated list of NZB-name patterns that
	// resolve to this category (legacy convenience).
	Aliases string `json:"-"`
}

// SuccessStatus is the set of NZBGet history-item Par/Unpack/Move/
// Script status values that count as success. Mirrors C#
// `_successStatus` at `Nzbget.cs:23` ({"SUCCESS", "NONE"}).
var SuccessStatus = map[string]bool{
	"SUCCESS": true,
	"NONE":    true,
}

// DeleteFailedStatus is the set of NZBGet history-item DeleteStatus
// values that constitute a hard failure (vs a warning). Mirrors C#
// `_deleteFailedStatus` at `Nzbget.cs:24`
// ({"HEALTH","DUPE","SCAN","COPY","BAD"}).
var DeleteFailedStatus = map[string]bool{
	"HEALTH": true,
	"DUPE":   true,
	"SCAN":   true,
	"COPY":   true,
	"BAD":    true,
}

// combineLoHi recombines NZBGet's split 32-bit Lo/Hi halves into a
// single int64 byte count. Mirrors C# `MakeInt64` at
// `Nzbget.cs:65-67` — the wire format is `(uint64(Hi) << 32) |
// uint64(Lo)` then cast to int64.
//
// File sizes >= 2^63 bytes would overflow, but NZBGet itself
// reports sizes only up to 64-bit unsigned anyway, and Sonarr's
// Item.TotalSize is int64 — no realistic NZB exceeds 2^63 bytes
// (8 EiB).
func combineLoHi(hi, lo uint) int64 {
	return int64(uint64(hi)<<32 | uint64(lo))
}
