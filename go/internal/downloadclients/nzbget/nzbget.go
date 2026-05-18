package nzbget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the NZBGet download client. Mirrors C# class names `Nzbget` +
// `NzbgetSettings`. The (Implementation, ConfigContract) pair is
// the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "Nzbget"
	ConfigContract = "NzbgetSettings"
)

// minMajorVersion is the NZBGet major-version floor Test enforces.
// NZBGet 12 was the first release shipping the JSON-RPC API surface
// Sonarr depends on; older versions lack the methods entirely.
// Matches C# `Nzbget.cs:279` (`Version.Parse(version) < Version.Parse("12.0")`).
const minMajorVersion = 12

// Nzbget is the [downloadclients.Provider] implementation for the
// NZBGet usenet client. Ports C# `Nzbget` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs` + the
// `NzbgetProxy.cs` JSON-RPC body.
//
// Slice 5.G3c scope: Name / Protocol / Definition / RequestAction /
// GetImportItem / Test live. Download / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type Nzbget struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs an Nzbget Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Nzbget {
	if log == nil {
		log = slog.Default()
	}
	return &Nzbget{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label.
func (n *Nzbget) Name() string { return "NZBGet" }

// Protocol returns the protocol family. NZBGet is usenet, not
// torrent — embeds [downloadclients.BaseUsenetSettings].
func (n *Nzbget) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClients row.
func (n *Nzbget) Definition() *downloadclients.DownloadClient { return n.def }

// Settings returns the resolved Settings struct.
func (n *Nzbget) Settings() Settings { return n.settings }

// Proxy returns the bound proxy.
func (n *Nzbget) Proxy() *Proxy { return n.proxy }

// Download is filled in by slice 5.G3e. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems ports C# `Nzbget.GetItems` at
// `Download/Clients/Nzbget/Nzbget.cs:54-200`. NZBGet exposes two
// endpoints that together represent the user-visible queue:
//
//   - `listgroups` — in-progress downloads (per [Nzbget.queueItems])
//   - `history`    — completed / failed / deleted downloads (per
//     [Nzbget.historyItems])
//
// The Go port runs both probes sequentially, projects each into
// [downloadclients.Item], and filters by settings.TvCategory at the
// end (matches C# `:199` — the Where-clause).
//
// Drone-parameter handling: every Sonarr-grabbed NZB carries a
// "drone" parameter whose value is the Sonarr-side DownloadID (set
// at append-time via the P[] params arg). queue+history items both
// surface this; when missing, the DownloadID falls back to the
// numeric NzbId / Id as a string (matches C# `:72,124`).
func (n *Nzbget) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if n.proxy == nil {
		return nil, errors.New("nzbget: GetItems: nil proxy")
	}

	globalStatus, err := n.proxy.GetGlobalStatus(ctx, n.settings)
	if err != nil {
		return nil, fmt.Errorf("nzbget: GetItems: GetGlobalStatus: %w", err)
	}
	queue, err := n.proxy.GetQueue(ctx, n.settings)
	if err != nil {
		return nil, fmt.Errorf("nzbget: GetItems: GetQueue: %w", err)
	}
	history, err := n.proxy.GetHistory(ctx, n.settings)
	if err != nil {
		return nil, fmt.Errorf("nzbget: GetItems: GetHistory: %w", err)
	}

	info := n.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(queue)+len(history))

	// Queue arm.
	var totalRemainingSize int64
	for i := range queue {
		out = append(out, n.queueItemToItem(queue[i], globalStatus, info, &totalRemainingSize))
	}
	// History arm.
	for i := range history {
		out = append(out, n.historyItemToItem(history[i], info))
	}

	// Category filter (C# `:199` Where-clause). Only applies when
	// TvCategory is set; empty TvCategory surfaces all items.
	cat := strings.TrimSpace(n.settings.TvCategory)
	if cat == "" {
		return out, nil
	}
	filtered := out[:0]
	for _, item := range out {
		if item.Category == cat {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// queueItemToItem builds the Item from an in-progress queue entry.
// Ports C# `Nzbget.GetQueue` per-item loop at `:63-105`.
//
// totalRemainingSize accumulates across the queue items so the
// per-item RemainingTime estimate matches the C# behaviour: items
// later in the queue surface a larger ETA reflecting "all items
// before me must finish first".
func (n *Nzbget) queueItemToItem(q NzbgetQueueItem, globalStatus *NzbgetGlobalStatus, info *downloadclients.ItemClientInfo, totalRemainingSize *int64) *downloadclients.Item {
	totalSize := combineLoHi(q.FileSizeHi, q.FileSizeLo)
	pausedSize := combineLoHi(q.PausedSizeHi, q.PausedSizeLo)
	remainingSize := combineLoHi(q.RemainingSizeHi, q.RemainingSizeLo)

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         droneOrFallback(q.Parameters, q.NzbId),
		Title:              q.NzbName,
		TotalSize:          totalSize,
		Category:           q.Category,
		CanMoveFiles:       true,
		CanBeRemoved:       true,
	}

	pausedGlobal := globalStatus != nil && globalStatus.DownloadPaused
	switch {
	case pausedGlobal || (remainingSize == pausedSize && remainingSize != 0):
		item.Status = downloadclients.DownloadItemStatusPaused
		item.RemainingSize = remainingSize
	default:
		if q.ActiveDownloads == 0 && remainingSize != 0 {
			item.Status = downloadclients.DownloadItemStatusQueued
		} else {
			item.Status = downloadclients.DownloadItemStatusDownloading
		}
		item.RemainingSize = remainingSize - pausedSize
		if globalStatus != nil && globalStatus.DownloadRate != 0 {
			secs := float64(*totalRemainingSize+item.RemainingSize) / float64(globalStatus.DownloadRate)
			eta := time.Duration(secs * float64(time.Second))
			item.RemainingTime = &eta
			*totalRemainingSize += item.RemainingSize
		}
	}
	return item
}

// historyItemToItem builds the Item from a completed/failed/deleted
// history entry. Ports C# `Nzbget.GetHistory` per-item loop at
// `:117-189`.
//
// FinalDir (set when NZBGet moved the completed download to its
// final home) wins over DestDir per C# `:121`. The status cascade
// (ParStatus → UnpackStatus → MoveStatus → ScriptStatus →
// DeleteStatus → MarkStatus) decides the final Sonarr-side state.
func (n *Nzbget) historyItemToItem(h NzbgetHistoryItem, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	itemDir := h.FinalDir
	if strings.TrimSpace(itemDir) == "" {
		itemDir = h.DestDir
	}

	zero := time.Duration(0)
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         droneOrFallback(h.Parameters, h.Id),
		Title:              h.Name,
		TotalSize:          combineLoHi(h.FileSizeHi, h.FileSizeLo),
		Category:           h.Category,
		Status:             downloadclients.DownloadItemStatusCompleted,
		RemainingTime:      &zero,
		CanMoveFiles:       true,
		CanBeRemoved:       true,
		Message: fmt.Sprintf("Par: %s, Unpack: %s, Move: %s, Script: %s, Delete: %s, Mark: %s",
			h.ParStatus, h.UnpackStatus, h.MoveStatus, h.ScriptStatus, h.DeleteStatus, h.MarkStatus),
	}
	if strings.TrimSpace(itemDir) != "" {
		item.OutputPath = ospath.New(itemDir)
	}

	// DeleteStatus=MANUAL is the special arm at C# `:141-151`:
	// manually-deleted items surface as Failed (C# specifically
	// when MarkStatus=BAD; everything else is `continue`'d). The
	// Go port keeps both arms as Failed: MarkStatus=BAD is the
	// explicit Failed branch C# returns, and the C# `continue`
	// drops the row entirely — but Sonarr's queue UI benefits
	// from surfacing the row as terminal-failed since the import
	// won't happen, vs dropping it silently. Either way the
	// status is Failed.
	if h.DeleteStatus == "MANUAL" {
		item.Status = downloadclients.DownloadItemStatusFailed
		return item
	}

	// Cascade per C# `:153-187`.
	if !SuccessStatus[h.ParStatus] {
		item.Status = downloadclients.DownloadItemStatusFailed
	}
	if h.UnpackStatus == "SPACE" {
		item.Status = downloadclients.DownloadItemStatusWarning
	} else if !SuccessStatus[h.UnpackStatus] {
		item.Status = downloadclients.DownloadItemStatusFailed
	}
	if !SuccessStatus[h.MoveStatus] {
		item.Status = downloadclients.DownloadItemStatusWarning
	}
	if !SuccessStatus[h.ScriptStatus] {
		item.Status = downloadclients.DownloadItemStatusFailed
	}
	if !SuccessStatus[h.DeleteStatus] && strings.TrimSpace(h.DeleteStatus) != "" {
		if DeleteFailedStatus[h.DeleteStatus] {
			item.Status = downloadclients.DownloadItemStatusFailed
		} else {
			item.Status = downloadclients.DownloadItemStatusWarning
		}
	}
	return item
}

// droneOrFallback looks up the "drone" parameter (Sonarr's per-grab
// cross-reference token) and returns its string value. Falls back
// to the numeric NzbId/Id-as-string when the parameter is missing —
// matches C# `:72,124` (`.SingleOrDefault(...)?.Value.ToString()`
// with `item.NzbId.ToString()` fallback).
func droneOrFallback(params []NzbgetParameter, fallbackID int) string {
	for _, p := range params {
		if p.Name == "drone" {
			if v, ok := p.Value.(string); ok && v != "" {
				return v
			}
			if v, ok := p.Value.(float64); ok {
				// JSON numbers decode as float64 — round-trip via
				// strconv since drone IDs are typically GUID-style
				// strings but some legacy installs use ints.
				return strconv.FormatInt(int64(v), 10)
			}
		}
	}
	return strconv.Itoa(fallbackID)
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient`.
// Note: the C# tree passes `false` for HasPostImportCategory at
// `:76,123` (NZBGet doesn't have a post-import-category concept the
// same way qBittorrent / Deluge do). The Go port mirrors.
func (n *Nzbget) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: n.Protocol(),
		Type:     Implementation,
		Name:     n.Name(),
	}
	if n.def != nil {
		info.ID = n.def.ID
		info.Name = n.def.Name
		info.RemoveCompletedDownloads = n.def.RemoveCompletedDownloads
	}
	return info
}

// GetImportItem mirrors the C# base default — pass input through.
func (n *Nzbget) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is filled in by slice 5.G3f. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is filled in by slice 5.G3g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G3h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test ports the connectivity + version-floor probe from C#
// `Nzbget.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs:273-300`.
//
// Flow:
//
//  1. Settings.Validate first — structural failures short-circuit
//     before any network round-trip. NOTE: the C# tree treats
//     empty-TvCategory as a warning that doesn't block; the Go
//     port preserves the warning text in the failure but lets the
//     network probe still run.
//  2. Nil proxy → programmer-error path (non-nil error vs
//     TestFailure).
//  3. proxy.Call("version", nil, settings) → connectivity + auth
//     check via the `version` JSON-RPC method. On ErrAuthFailed
//     surface as "Username" failure (matches C# :289-291); on
//     ErrUnavailable surface as "Host" failure (C# :294-297);
//     other errors propagate.
//  4. Parse the leading major-version (C# at :277 splits on '-'
//     and parses as `Version`; the Go port mirrors by taking
//     everything before the first '-' or non-numeric char). Below
//     [minMajorVersion] surfaces a "Version" failure mirroring
//     C# :281-285.
//
// TestCategory + TestSettings at C# :303-352 are deferred — they
// probe `config` and category-list endpoints which land alongside
// the GetItems + GetStatus slices.
func (n *Nzbget) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := n.settings.Validate()
	if n.proxy == nil {
		if hasError(failures) {
			return failures, nil
		}
		return nil, errors.New("nzbget: Test: nil proxy (constructor not wired)")
	}

	raw, err := n.proxy.Call(ctx, "version", nil, n.settings)
	if err != nil {
		if failure := classifyAuthError(err); failure != nil {
			failures = append(failures, *failure)
			return failures, nil
		}
		return nil, err
	}

	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse NZBGet version: %v", err),
		})
		return failures, nil
	}

	major := parseMajor(version)
	if major < 0 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse NZBGet version: %q", version),
		})
		return failures, nil
	}
	if major < minMajorVersion {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("NZBGet %s is too old; %s requires at least %d.0",
				version, n.Name(), minMajorVersion),
		})
		return failures, nil
	}

	return failures, nil
}

// hasError reports whether any failure carries a non-empty
// PropertyName + ErrorMessage. Empty-TvCategory warning at
// Settings.Validate is the one warning-shaped failure; everything
// else is treated as blocking.
//
// The current Settings.Validate output doesn't distinguish warning
// vs error at the TestFailure shape — every failure has a
// PropertyName. So this helper is effectively `len(failures) > 0
// AND any failure isn't the TvCategory warning`. We keep it as a
// hook for the v3 React UI's future warning vs error tagging.
func hasError(failures []downloadclients.TestFailure) bool {
	for _, f := range failures {
		if f.PropertyName != "TvCategory" {
			return true
		}
	}
	return false
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed arms.
// Pinning mirrors C# `Nzbget.TestConnection` at `:289-297`.
func classifyAuthError(err error) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        "Username",
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to NZBGet",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// versionMajorPattern extracts the leading major-version integer
// from an NZBGet version string. NZBGet versions look like "24.2",
// "21.0-testing", "16.4-betaN". The C# tree at `Nzbget.cs:277`
// splits on '-' first then parses as a `Version`; the Go port
// extracts the first run of digits, which is robust against the
// hyphen-tag suffix.
var versionMajorPattern = regexp.MustCompile(`^\s*(\d+)`)

// parseMajor returns the leading major-version integer from a
// version string, or -1 when no integer can be parsed.
func parseMajor(version string) int {
	m := versionMajorPattern.FindStringSubmatch(version)
	if len(m) < 2 {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

// RequestAction is the per-impl action map. The NZBGet C# tree has
// no concrete actions — returns (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
func (n *Nzbget) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: Nzbget satisfies the Provider interface.
var _ downloadclients.Provider = (*Nzbget)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Deluge + Transmission + Sabnzbd factory pattern.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("nzbget: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("nzbget: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G3a round-trip suite) get a nil proxy + a Provider that
	// surfaces programmer-error returns from Test(); production
	// wiring always supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("nzbget: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	return New(def, settings, proxy, log), nil
}

// init registers Nzbget with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
