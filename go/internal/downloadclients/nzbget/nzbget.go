package nzbget

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	rpm      downloadclients.RemotePathMapper
	disk     downloadclients.DiskProvider
	log      *slog.Logger
}

// New constructs an Nzbget Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer. Remote-path mapping is bound via
// [Nzbget.SetRemotePathMapper] after construction.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Nzbget {
	if log == nil {
		log = slog.Default()
	}
	return &Nzbget{settings: settings, proxy: proxy, def: def, log: log}
}

// SetRemotePathMapper binds the [downloadclients.RemotePathMapper]
// the historyItemToItem and GetStatus paths consult to translate
// NZBGet-side absolute paths (FinalDir / DestDir / per-category
// DestDir) back to Sonarr-side paths. Optional — nil mapper degrades
// to the raw RPC-side path. Slice 6.B7 (Phase 6.B wire-up arc —
// final per-client slice).
//
// Returns the receiver for chainable construction.
func (n *Nzbget) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) *Nzbget {
	n.rpm = rpm
	return n
}

// SetDiskProvider binds the [downloadclients.DiskProvider] the
// RemoveItem deleteData arm consults to delete item.OutputPath when
// the user asked for "remove from client + delete on-disk data".
// Mandatory for the deleteData=true arm to be load-bearing — NZBGet's
// editqueue API has no files-delete arg, so the Sonarr-side
// DiskProvider.DeleteFolder is the ONLY mechanism that removes
// completed-download data from disk (mirrors the C# tree at
// `Nzbget.cs:204-207`). Optional — when nil, deleteData=true falls
// back to the pre-6.C warn-log behaviour (the queue/history entry
// is still removed; files stay on disk). Slice 6.C3 (Phase 6.C
// wire-up arc — the hard case).
//
// Returns the receiver for chainable construction.
func (n *Nzbget) SetDiskProvider(d downloadclients.DiskProvider) *Nzbget {
	n.disk = d
	return n
}

// remapOutput translates an NZBGet-side absolute path back to a
// Sonarr-side path via the wired [downloadclients.RemotePathMapper].
// Returns the raw path unchanged when the mapper is nil, when the
// input is empty, or when the mapper returns an error (debug-log
// the error).
func (n *Nzbget) remapOutput(ctx context.Context, raw string) ospath.OsPath {
	if raw == "" {
		return ospath.OsPath{}
	}
	remote := ospath.New(raw)
	if n.rpm == nil {
		return remote
	}
	local, err := n.rpm.RemapRemoteToLocal(ctx, n.settings.Host, remote)
	if err != nil {
		n.log.Debug("nzbget: RemapRemoteToLocal failed; using raw path",
			slog.String("host", n.settings.Host),
			slog.String("path", raw),
			slog.Any("err", err))
		return remote
	}
	return local
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

// Download ports C# `Nzbget.AddFromNzbFile` at
// `Download/Clients/Nzbget/Nzbget.cs:39-52`. NZBGet is usenet-only —
// there's no torrent/magnet variant, so this collapses to a single
// `append`-RPC dispatch.
//
// req.NzbBody MUST be non-empty: deluge/NZBGet/SAB are all usenet-
// or torrent-byte-only at the add layer, and the orchestrator
// pre-fetches DownloadURL → NzbBody in queue [15] before invoking
// Provider.Download. An empty NzbBody is a programmer-error path.
//
// Drone-parameter wiring: every Sonarr-grabbed NZB carries a
// freshly-minted 32-char hex drone ID via the PP[] params array.
// The same ID is returned as the DownloadID so GetItems can
// cross-reference the row when listgroups + history surface it
// later (see the `drone` arm in droneOrFallback).
//
// Priority pick: req.IsRecent picks RecentTvPriority; else
// OlderTvPriority. Matches C# `:42`.
//
// Filename: req.ReleaseTitle + ".nzb" suffix (deluge tree at
// `:39` passes the filename through verbatim — Sonarr's
// UsenetClientBase synthesises a "<release>.nzb" filename).
//
// Returns the drone ID as the DownloadID. Non-positive append
// response surfaces as ErrAddFailed via the proxy.
func (n *Nzbget) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("nzbget: Download: nil request")
	}
	if n.proxy == nil {
		return "", errors.New("nzbget: Download: nil proxy")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("nzbget: Download: NzbBody is empty (orchestrator must pre-fetch DownloadURL)")
	}

	droneID, err := newDroneID()
	if err != nil {
		return "", fmt.Errorf("nzbget: Download: generate drone ID: %w", err)
	}

	priority := n.settings.OlderTvPriority
	if req.IsRecent {
		priority = n.settings.RecentTvPriority
	}

	nzbName := strings.TrimSpace(req.ReleaseTitle)
	if nzbName == "" {
		nzbName = "release.nzb"
	} else if !strings.HasSuffix(strings.ToLower(nzbName), ".nzb") {
		nzbName += ".nzb"
	}

	if _, err := n.proxy.AppendNzbV16(ctx, nzbName, req.NzbBody,
		n.settings.TvCategory, priority, n.settings.AddPaused,
		droneID, n.settings); err != nil {
		return "", fmt.Errorf("nzbget: Download(%q): %w", req.ReleaseTitle, err)
	}
	return droneID, nil
}

// newDroneID returns a fresh 32-char hex string used as the
// per-grab cross-reference token. Matches C# `NzbgetProxy.cs:60-61`
// (`Guid.NewGuid().ToString().Replace("-", "")`).
func newDroneID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
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
		out = append(out, n.historyItemToItem(ctx, history[i], info))
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
// Slice 6.B7 routes the resolved itemDir through
// [Nzbget.remapOutput] so off-host NZBGet instances surface a
// Sonarr-side path; nil mapper / no match → raw RPC-side path.
func (n *Nzbget) historyItemToItem(ctx context.Context, h NzbgetHistoryItem, info *downloadclients.ItemClientInfo) *downloadclients.Item {
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
		item.OutputPath = n.remapOutput(ctx, itemDir)
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

// RemoveItem ports C# `Nzbget.RemoveItem` at
// `Download/Clients/Nzbget/Nzbget.cs:202-210` + the proxy-side
// lookup at `NzbgetProxy.cs:165-202`.
//
// NZBGet's remove flow is unusual: the DownloadID could refer to
// EITHER an in-progress queue entry (use GroupFinalDelete) OR a
// completed history entry (use HistoryDelete). The lookup logic:
//
//   - DownloadID parses as integer AND is < 10 chars: legacy
//     non-Sonarr-grabbed entry; match queueItem.NzbId / historyItem.Id
//     directly (matches C# `:172-175`).
//   - Otherwise: 32-char hex drone GUID from Sonarr's append-time
//     parameter; match the `drone` parameter Value case-sensitive
//     (matches C# `:177-181`).
//
// Not-found surfaces as a slog.Warn but returns nil — matches the
// C# pattern at `:201` which warns but doesn't throw. EditQueue
// returning false also warns + continues (matches C# `:188-192`).
//
// deleteData=true: the C# tree at `:204-207` calls `DeleteItemData`
// which uses DiskProvider.DeleteFolder against item.OutputPath.
// Slice 6.C3 wires this — when a [downloadclients.DiskProvider] is
// set via [Nzbget.SetDiskProvider] AND item.OutputPath is non-empty,
// the recursive DeleteFolder fires BEFORE the editqueue call (the
// C# ordering at `:204-207`). A disk error is logged but does not
// abort the editqueue step — the queue/history removal is still
// load-bearing even when the on-disk arm fails. When no disk
// provider is wired, falls back to the pre-6.C warn log (preserves
// test-harness compatibility).
func (n *Nzbget) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if item == nil {
		return errors.New("nzbget: RemoveItem: nil item")
	}
	if n.proxy == nil {
		return errors.New("nzbget: RemoveItem: nil proxy")
	}

	if deleteData {
		switch {
		case n.disk == nil:
			n.log.Warn("nzbget: RemoveItem: deleteData=true requested but no DiskProvider is wired; the queue/history entry will be removed but files remain on disk",
				"downloadID", item.DownloadID)
		case item.OutputPath.IsEmpty():
			n.log.Debug("nzbget: RemoveItem: deleteData=true but item.OutputPath is empty; skipping on-disk delete",
				"downloadID", item.DownloadID)
		default:
			if err := n.disk.DeleteFolder(item.OutputPath.FullPath(), true); err != nil {
				n.log.Warn("nzbget: RemoveItem: DiskProvider.DeleteFolder failed; the queue/history entry will still be removed",
					"downloadID", item.DownloadID,
					"path", item.OutputPath.FullPath(),
					"err", err)
			}
		}
	}

	queue, err := n.proxy.GetQueue(ctx, n.settings)
	if err != nil {
		return fmt.Errorf("nzbget: RemoveItem: GetQueue: %w", err)
	}
	history, err := n.proxy.GetHistory(ctx, n.settings)
	if err != nil {
		return fmt.Errorf("nzbget: RemoveItem: GetHistory: %w", err)
	}

	queueItem, historyItem := lookupForRemove(item.DownloadID, queue, history)
	switch {
	case queueItem != nil:
		ok, err := n.proxy.EditQueue(ctx, "GroupFinalDelete", 0, "", queueItem.NzbId, n.settings)
		if err != nil {
			n.log.Warn("nzbget: RemoveItem: editqueue GroupFinalDelete failed",
				"nzbId", queueItem.NzbId, "name", queueItem.NzbName, "err", err)
			return nil
		}
		if !ok {
			n.log.Warn("nzbget: RemoveItem: editqueue GroupFinalDelete returned false",
				"nzbId", queueItem.NzbId, "name", queueItem.NzbName)
		}
	case historyItem != nil:
		ok, err := n.proxy.EditQueue(ctx, "HistoryDelete", 0, "", historyItem.Id, n.settings)
		if err != nil {
			n.log.Warn("nzbget: RemoveItem: editqueue HistoryDelete failed",
				"id", historyItem.Id, "name", historyItem.Name, "err", err)
			return nil
		}
		if !ok {
			n.log.Warn("nzbget: RemoveItem: editqueue HistoryDelete returned false",
				"id", historyItem.Id, "name", historyItem.Name)
		}
	default:
		n.log.Warn("nzbget: RemoveItem: unknown DownloadID — not present in queue or history",
			"downloadID", item.DownloadID)
	}
	return nil
}

// lookupForRemove walks queue + history looking for the DownloadID.
// Returns the matched (*queueItem, nil) OR (nil, *historyItem); a
// nil/nil return means not found anywhere.
//
// Match rules per C# `NzbgetProxy.RemoveItem` at `:170-186`:
//
//   - DownloadID parses as an integer AND is < 10 chars: match by
//     numeric NzbId/Id directly (legacy non-Sonarr-grabbed entries).
//   - Otherwise: match by the `drone` parameter Value, case-
//     sensitive — drone GUIDs are lowercase hex from
//     [newDroneID].
func lookupForRemove(downloadID string, queue []NzbgetQueueItem, history []NzbgetHistoryItem) (*NzbgetQueueItem, *NzbgetHistoryItem) {
	id := strings.TrimSpace(downloadID)
	if id == "" {
		return nil, nil
	}

	if len(id) < 10 {
		if nzbID, err := strconv.Atoi(id); err == nil {
			for i := range queue {
				if queue[i].NzbId == nzbID {
					return &queue[i], nil
				}
			}
			for i := range history {
				if history[i].Id == nzbID {
					return nil, &history[i]
				}
			}
			return nil, nil
		}
	}

	for i := range queue {
		if hasMatchingDrone(queue[i].Parameters, id) {
			return &queue[i], nil
		}
	}
	for i := range history {
		if hasMatchingDrone(history[i].Parameters, id) {
			return nil, &history[i]
		}
	}
	return nil, nil
}

// hasMatchingDrone reports whether any parameter named "drone"
// carries value == id (case-sensitive). Handles both string-Value
// (the modern shape) and float64-Value (legacy int-stringified)
// arms.
func hasMatchingDrone(params []NzbgetParameter, id string) bool {
	for _, p := range params {
		if p.Name != "drone" {
			continue
		}
		switch v := p.Value.(type) {
		case string:
			if v == id {
				return true
			}
		case float64:
			if strconv.FormatInt(int64(v), 10) == id {
				return true
			}
		}
	}
	return false
}

// GetStatus ports C# `Nzbget.GetStatus` at
// `Download/Clients/Nzbget/Nzbget.cs:212-229`. Fetches the global
// config via `config` RPC, walks the Category${i} keys to find one
// matching settings.TvCategory, and surfaces its DestDir as the
// OutputRootFolders entry.
//
// Empty TvCategory or no matching category → empty
// OutputRootFolders. RemovesCompletedDownloads stays false — NZBGet
// has no global remove-on-completion preference that maps cleanly
// (matches C# behaviour where the field is unset).
//
// Remote-path-mapping translation at C# `:225` deferred to the
// [downloadclients.Dependencies.RemotePathMappingService] port.
func (n *Nzbget) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	if n.proxy == nil {
		return nil, errors.New("nzbget: GetStatus: nil proxy")
	}
	cfg, err := n.proxy.GetConfig(ctx, n.settings)
	if err != nil {
		return nil, fmt.Errorf("nzbget: GetStatus: %w", err)
	}

	info := &downloadclients.ClientInfo{
		IsLocalhost: n.settings.Host == "127.0.0.1" || strings.EqualFold(n.settings.Host, "localhost"),
	}
	if tv := strings.TrimSpace(n.settings.TvCategory); tv != "" {
		for _, cat := range getCategories(cfg) {
			if cat.Name == tv {
				if strings.TrimSpace(cat.DestDir) != "" {
					info.OutputRootFolders = []ospath.OsPath{n.remapOutput(ctx, cat.DestDir)}
				}
				break
			}
		}
	}
	return info, nil
}

// getCategories walks the Category${i}.Name + Category${i}.DestDir
// keys in the supplied config map. Mirrors C# `Nzbget.GetCategories`
// at `Nzbget.cs:231-263`. Stops at the first missing Category${i}.Name
// (NZBGet's preferences shape is dense from 1 onwards — no holes).
//
// DestDir resolution per C# `:242-253`:
//
//  1. Use the per-category Category${i}.DestDir directly when non-
//     empty.
//  2. Else read the global DestDir + MainDir, substitute
//     `${MainDir}` into the global DestDir, and append the
//     category name as a subfolder when AppendCategoryDir is
//     "yes" (which is the default).
//
// Unpack / DefScript / Aliases are surfaced on the returned struct
// even though [GetStatus] only reads DestDir — the future
// label-aware probes (e.g. the import path's per-category-script
// detection) will use them.
func getCategories(config map[string]string) []NzbgetCategory {
	out := make([]NzbgetCategory, 0, 4)
	mainDir := config["MainDir"]
	globalDestDir := config["DestDir"]
	appendCategoryDir := config["AppendCategoryDir"]
	if appendCategoryDir == "" {
		appendCategoryDir = "yes" // C# `:249` default
	}

	for i := 1; i < 100; i++ {
		name, ok := config[fmt.Sprintf("Category%d.Name", i)]
		if !ok {
			break
		}
		destDir := config[fmt.Sprintf("Category%d.DestDir", i)]
		if strings.TrimSpace(destDir) == "" {
			destDir = strings.ReplaceAll(globalDestDir, "${MainDir}", mainDir)
			if appendCategoryDir == "yes" {
				destDir = joinNzbgetPath(destDir, name)
			}
		}
		out = append(out, NzbgetCategory{
			Name:      name,
			DestDir:   destDir,
			Unpack:    config[fmt.Sprintf("Category%d.Unpack", i)] == "yes",
			DefScript: config[fmt.Sprintf("Category%d.DefScript", i)],
			Aliases:   config[fmt.Sprintf("Category%d.Aliases", i)],
		})
	}
	return out
}

// joinNzbgetPath joins the global DestDir with a category name as
// a subfolder. NZBGet runs on both Unix and Windows; the path
// separator from the existing DestDir wins (mirrors the C#
// `Path.Combine` which inherits from the parent's separator).
func joinNzbgetPath(base, name string) string {
	if base == "" {
		return name
	}
	if name == "" {
		return base
	}
	sep := "/"
	if strings.ContainsRune(base, '\\') && !strings.ContainsRune(base, '/') {
		sep = "\\"
	}
	return strings.TrimRight(base, "/\\") + sep + name
}

// MarkItemAsImported is a no-op for NZBGet — the C# tree at
// `Download/Clients/Nzbget/Nzbget.cs` does NOT override the
// `DownloadClientBase.MarkItemAsImported` default, which throws
// `NotSupportedException` at `DownloadClientBase.cs:189-192`. The
// Go port surfaces this via [downloadclients.ErrMarkAsImportedUnsupported].
//
// Sonarr's import pipeline calls MarkItemAsImported on every
// download-client item after a successful import to (optionally)
// swap the per-client category to a "imported" variant — see
// qBittorrent 5.G1h / Deluge 5.G2h for the implementations that
// DO override. NZBGet has no equivalent: queue/history entries are
// either dropped via the editqueue HistoryDelete command (5.G3f's
// RemoveItem path) or left in place; there's no re-categorization
// concept.
//
// The orchestrator (queue [15] ProcessDownloadDecisions follow-up)
// treats ErrMarkAsImportedUnsupported as "log + skip" — the import
// path proceeds without complaint.
func (n *Nzbget) MarkItemAsImported(_ context.Context, item *downloadclients.Item) error {
	if item == nil {
		return errors.New("nzbget: MarkItemAsImported: nil item")
	}
	return fmt.Errorf("%w: NZBGet has no post-import-category mechanism", downloadclients.ErrMarkAsImportedUnsupported)
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
// TestCategory + TestSettings at C# :303-343 wired at slice 5.J100
// — both layer on top of the version probe by issuing a single
// `config` call (now that GetConfig + getCategories from the
// GetStatus arm are available). TestCategory fails when TvCategory
// is set but isn't found in the resolved category list (C#
// :303-318). TestSettings fails when NZBGet's `KeepHistory` config
// is 0 or > 25000 (C# :320-343 — KeepHistory=0 turns off history
// retention which breaks Sonarr's import-detection; >25000 is the
// NZBGet upper bound the config UI enforces).
//
// The two probes share a single GetConfig call so a healthy NZBGet
// only sees one extra round-trip during Test. Config-fetch failures
// short-circuit subsequent validators with an "unable to fetch
// settings" failure rather than letting them silently NRE.
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

	cfg, err := n.proxy.GetConfig(ctx, n.settings)
	if err != nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "",
			ErrorMessage: fmt.Sprintf("Unable to fetch NZBGet config: %v", err),
		})
		return failures, nil
	}
	if f := testCategory(cfg, n.settings.TvCategory); f != nil {
		failures = append(failures, *f)
	}
	if f := testKeepHistory(cfg); f != nil {
		failures = append(failures, *f)
	}

	return failures, nil
}

// testCategory ports C# `TestCategory` at `Nzbget.cs:303-318`. Empty
// TvCategory passes (no category requirement); set-but-missing fails
// with "category missing" on the TvCategory field. Match is by
// exact Name string.
func testCategory(cfg map[string]string, tvCategory string) *downloadclients.TestFailure {
	tv := strings.TrimSpace(tvCategory)
	if tv == "" {
		return nil
	}
	for _, cat := range getCategories(cfg) {
		if cat.Name == tv {
			return nil
		}
	}
	return &downloadclients.TestFailure{
		PropertyName: "TvCategory",
		ErrorMessage: fmt.Sprintf("Category %q does not exist on the NZBGet server", tv),
		DetailedDescription: fmt.Sprintf(
			"Add a category named %q to NZBGet under Settings > Categories, or update Sonarr to use one that exists.",
			tv,
		),
	}
}

// testKeepHistory ports C# `TestSettings` at `Nzbget.cs:320-343`.
// Parses `KeepHistory` from the config map (default "7" matches C#
// `:324` `GetValueOrDefault("KeepHistory", "7")`). Returns the
// whole-form failure NZBGet's UI shows when the user has disabled
// history (=0) or set it above the documented limit (>25000).
func testKeepHistory(cfg map[string]string) *downloadclients.TestFailure {
	raw := strings.TrimSpace(cfg["KeepHistory"])
	if raw == "" {
		raw = "7"
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		// C# `:325-326` `int.TryParse` returning false falls through
		// to the value==0 arm (the failure message is the same).
		value = 0
	}
	switch {
	case value == 0:
		return &downloadclients.TestFailure{
			PropertyName:        "",
			ErrorMessage:        "NZBGet's KeepHistory setting is set to 0; Sonarr requires history retention to detect failed downloads",
			DetailedDescription: "Open NZBGet > Settings > DOWNLOAD QUEUE > KeepHistory and set a non-zero value.",
		}
	case value > 25000:
		return &downloadclients.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("NZBGet's KeepHistory setting is %d; the maximum supported value is 25000", value),
			DetailedDescription: "Open NZBGet > Settings > DOWNLOAD QUEUE > KeepHistory and lower the value.",
		}
	}
	return nil
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

	nzb := New(def, settings, proxy, log)
	// Slice 6.B7: cast the typed-as-any RemotePathMappingService to
	// the narrow RemotePathMapper interface. nil dep / cast miss
	// leaves nzb.rpm nil; per-call sites fall through to the raw
	// RPC-side path.
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		nzb.SetRemotePathMapper(rpm)
	}
	// Slice 6.C3: cast the typed-as-any DiskProvider to the narrow
	// downloadclients.DiskProvider interface. nil dep / cast miss
	// leaves nzb.disk nil; the deleteData=true arm falls back to the
	// warn-log behaviour (queue/history entry removed, files remain).
	if d, ok := deps.DiskProvider.(downloadclients.DiskProvider); ok {
		nzb.SetDiskProvider(d)
	}
	return nzb, nil
}

// init registers Nzbget with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
