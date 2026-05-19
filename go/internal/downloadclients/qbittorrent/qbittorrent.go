package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the qBittorrent download client. Mirrors C# class names
// `QBittorrent` + `QBittorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:23`
// and `QBittorrentSettings.cs:31`). The (Implementation,
// ConfigContract) pair is the registry key — see
// [downloadclients.Registry] doc.
const (
	Implementation = "QBittorrent"
	ConfigContract = "QBittorrentSettings"
)

// minVersion is the qBittorrent product-version floor Test enforces.
// qBittorrent 4.1 was the first release shipping WebUI API v2 — the
// only API surface the Go-port proxy targets. The C# tree applies
// the equivalent floor at the API-version layer
// (`QBittorrent.cs:464` requires WebUI API >= 1.5, available in
// qBittorrent >= 3.2.4); the Go port simplifies by gating on the
// product version exposed by /api/v2/app/version because that
// endpoint is the canonical reachability + version probe.
var minVersion = versionPair{major: 4, minor: 1}

// QBittorrent is the [downloadclients.Provider] implementation for
// the qBittorrent torrent client. Ports C# `QBittorrent` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:23-450`
// + the V2 proxy body at `QBittorrentProxyV2.cs:14-449`.
//
// Slice 5.G1c scope: Name / Protocol / Definition / RequestAction /
// Test are live; Download / GetItems / GetStatus / RemoveItem /
// GetImportItem / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type QBittorrent struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	rpm      downloadclients.RemotePathMapper
	log      *slog.Logger
}

// New constructs a QBittorrent Provider against the resolved
// settings + proxy. Used by both the registry [Constructor] and by
// tests that pin a stub HTTPDoer. Remote-path mapping is bound via
// [QBittorrent.SetRemotePathMapper] after construction.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *QBittorrent {
	if log == nil {
		log = slog.Default()
	}
	return &QBittorrent{settings: settings, proxy: proxy, def: def, log: log}
}

// SetRemotePathMapper binds the [downloadclients.RemotePathMapper]
// the torrentToItem and GetStatus paths consult to translate the
// qBittorrent-side absolute paths (ContentPath / SavePath / global
// SavePath) back to Sonarr-side paths. Optional — nil mapper
// degrades to the raw WebUI-side path at every call site (matches
// the pre-6.B behaviour). Slice 6.B5 (Phase 6.B wire-up arc).
//
// Returns the receiver for chainable construction.
func (q *QBittorrent) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) *QBittorrent {
	q.rpm = rpm
	return q
}

// remapOutput translates a qBittorrent-side absolute path back to a
// Sonarr-side path via the wired [downloadclients.RemotePathMapper].
// Returns the raw path unchanged when the mapper is nil, when the
// input is empty, or when the mapper returns an error (debug-log
// the error; a remap failure shouldn't fail the calling GetItems /
// GetStatus). Centralises the nil-safe lookup.
func (q *QBittorrent) remapOutput(ctx context.Context, raw string) ospath.OsPath {
	if raw == "" {
		return ospath.OsPath{}
	}
	remote := ospath.New(raw)
	if q.rpm == nil {
		return remote
	}
	local, err := q.rpm.RemapRemoteToLocal(ctx, q.settings.Host, remote)
	if err != nil {
		q.log.Debug("qbittorrent: RemapRemoteToLocal failed; using raw path",
			slog.String("host", q.settings.Host),
			slog.String("path", raw),
			slog.Any("err", err))
		return remote
	}
	return local
}

// Name returns the user-facing label. Mirrors C# `QBittorrent.Name`
// (inherited default — the class name itself; see
// `DownloadClientBase.cs:62`).
func (q *QBittorrent) Name() string { return "qBittorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (q *QBittorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClients row.
func (q *QBittorrent) Definition() *downloadclients.DownloadClient { return q.def }

// Settings returns the resolved Settings struct. Exposed for the
// 5.G1d+ slices and tests; concrete proxy code in this package
// reads these fields directly via the package-internal accessor at
// slice land time.
func (q *QBittorrent) Settings() Settings { return q.settings }

// Proxy returns the bound proxy. Exposed for tests that want to
// poke the proxy directly without driving the full Provider
// surface.
func (q *QBittorrent) Proxy() *Proxy { return q.proxy }

// Download ports the C# Download orchestrator at
// `Download/Clients/QBittorrent/QBittorrent.cs:75-216`. Picks the
// add path (TorrentBody → AddTorrentFromFile, MagnetURL →
// AddTorrentFromURL(magnet), DownloadURL → AddTorrentFromURL(URL)),
// applies the per-grab share limits + force-start + paused/stopped
// initial state, and returns the upper-cased info-hash.
//
// Per-slice scope deferrals:
//
//   - The MoveTorrentToTopInQueue arm at C# `:109-119,178-188` is
//     deferred to a follow-up slice — the priority RPC requires
//     torrent-queueing to be enabled in qBittorrent and returns 409
//     when not; not load-bearing for the common case.
//   - AddSeriesTags at C# `:133-143,202-212` is deferred — the tag
//     repository port lands at a later sub-arc, and AddTags is best-
//     effort in the C# tree anyway (warn + continue on failure).
//   - WaitForTorrent at C# `:218-242` (poll-until-visible for the
//     post-add RPC chain) is replaced by an "apply best-effort"
//     posture — the post-add RPCs log + continue on failure, which
//     matches the C# catch arms at `:104-106,116-118,127-129`.
//   - The "magnet without trackers when DHT disabled" guard at C#
//     `:77-80` is deferred until GetStatus / preferences land
//     (5.G1g). Sonarr-grabbed magnets virtually always include a
//     tracker, so the regression cost is low.
//
// InfoHash sourcing:
//
//   - req.InfoHash trusted when set.
//   - Otherwise extracted from MagnetURL's xt=urn:btih:HASH query
//     parameter when present.
//   - Otherwise an error (the orchestrator's responsibility is to
//     fill InfoHash on Subjects produced from torznab/torrent-file
//     indexers; this guard fails loudly during integration).
func (q *QBittorrent) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("qbittorrent: Download: nil request")
	}
	if q.proxy == nil {
		return "", errors.New("qbittorrent: Download: nil proxy")
	}

	hash, err := resolveInfoHash(req)
	if err != nil {
		return "", err
	}

	stoppedField, _ := q.resolveStoppedFieldName(ctx)

	// "addShareLimits inline" is API >= 2.8.1 (qBittorrent 4.4+).
	// Probing the API version on every grab is expensive; the Go
	// port pessimistically assumes the older shape (no inline
	// share-limits) and applies them via a follow-up call. The C#
	// tree at `:83,152` does the same fallback when the version
	// probe is below 2.8.1.
	switch {
	case len(req.TorrentBody) > 0:
		filename := fallbackFilename(req)
		err = q.proxy.AddTorrentFromFile(ctx, filename, req.TorrentBody, nil, q.settings, stoppedField)
	case strings.TrimSpace(req.MagnetURL) != "":
		err = q.proxy.AddTorrentFromURL(ctx, req.MagnetURL, nil, q.settings, stoppedField)
	case strings.TrimSpace(req.DownloadURL) != "":
		err = q.proxy.AddTorrentFromURL(ctx, req.DownloadURL, nil, q.settings, stoppedField)
	default:
		return "", errors.New("qbittorrent: Download: GrabRequest has neither TorrentBody nor MagnetURL nor DownloadURL")
	}
	if err != nil {
		return "", fmt.Errorf("qbittorrent: Download(%q): %w", req.ReleaseTitle, err)
	}

	// Post-add best-effort RPCs — match the C# warn-and-continue
	// pattern at `:103-106,127-129`.
	if req.SeedConfiguration != nil {
		if seedErr := q.proxy.SetTorrentSeedingConfiguration(ctx, hash, req.SeedConfiguration, q.settings); seedErr != nil {
			q.log.Warn("qbittorrent: SetTorrentSeedingConfiguration failed",
				"hash", hash, "err", seedErr)
		}
	}
	if q.settings.InitialState == InitialStateForceStart {
		if forceErr := q.proxy.SetForceStart(ctx, hash, true, q.settings); forceErr != nil {
			q.log.Warn("qbittorrent: SetForceStart failed",
				"hash", hash, "err", forceErr)
		}
	}

	return strings.ToUpper(hash), nil
}

// resolveStoppedFieldName probes /api/v2/app/version to decide
// whether the add endpoint's paused-state field is named "paused"
// (qBittorrent < 5) or "stopped" (qBittorrent >= 5). On any probe
// failure, falls back to "paused" — matches the safe default in the
// C# wire at `QBittorrentProxyV2.cs:251`.
func (q *QBittorrent) resolveStoppedFieldName(ctx context.Context) (string, error) {
	raw, err := q.proxy.GetVersion(ctx, q.settings)
	if err != nil {
		return "paused", err
	}
	v := parseVersion(raw)
	if v == nil {
		return "paused", nil
	}
	if v.major >= 5 {
		return "stopped", nil
	}
	return "paused", nil
}

// resolveInfoHash sources the torrent's info-hash for the
// DownloadID return value. See the [QBittorrent.Download] doc for
// the order of preference.
func resolveInfoHash(req *downloadclients.GrabRequest) (string, error) {
	if h := strings.TrimSpace(req.InfoHash); h != "" {
		return strings.ToLower(h), nil
	}
	if magnet := strings.TrimSpace(req.MagnetURL); magnet != "" {
		// Magnet links can be parsed via url.Parse — they're URI
		// syntax even if not http(s).
		if u, err := url.Parse(magnet); err == nil {
			for _, xt := range u.Query()["xt"] {
				if strings.HasPrefix(xt, "urn:btih:") {
					return strings.ToLower(strings.TrimPrefix(xt, "urn:btih:")), nil
				}
			}
		}
	}
	return "", errors.New("qbittorrent: Download: GrabRequest has no InfoHash and no urn:btih: in MagnetURL")
}

// fallbackFilename returns a reasonable .torrent filename for the
// AddTorrentFromFile multipart part. qBittorrent uses the filename
// only for the WebUI's pending-torrent display; any non-empty value
// works.
func fallbackFilename(req *downloadclients.GrabRequest) string {
	if t := strings.TrimSpace(req.ReleaseTitle); t != "" {
		return t + ".torrent"
	}
	return "release.torrent"
}

// GetItems ports C# `QBittorrent.GetItems` at
// `Download/Clients/QBittorrent/QBittorrent.cs:246-359`. Fetches
// every torrent the WebUI reports (filtered server-side by category
// when settings.TvCategory is non-empty per
// `QBittorrentProxyV2.cs:99-102`) and projects each into the
// orchestrator's canonical [downloadclients.Item] shape.
//
// Differences from the C# port:
//
//   - The DhtEnabled branch at C# `:316-325` (warn-on-metaDL-when-DHT-disabled)
//     reads the /api/v2/app/preferences config which the Go port
//     defers to 5.G1g (GetStatus). 5.G1d treats every metaDL /
//     forcedMetaDL state as Queued unconditionally — the worst case
//     is a metadata stall that takes longer to surface as a
//     warning, which matches qBittorrent's own UI behaviour pre-2.6.
//   - The remote-path-mapping translation at C# `:346` is deferred
//     until the [downloadclients.Dependencies.RemotePathMappingService]
//     port lands (see queue [15] follow-up). 5.G1d emits the raw
//     RPC-side ContentPath / SavePath; the orchestrator's path-
//     mapping helper translates at consumption time.
//   - The version-gate at C# `:342` (ContentPath only at WebUI API
//     >= 2.6.1) is dropped — minVersion at 5.G1c already pins to
//     qBittorrent 4.1+ which always has API >= 2.6.
//   - HasReachedSeedLimit at C# `:273` reads global config; the
//     Go-port implementation honours only the per-torrent ratio_limit
//     / seeding_time_limit fields (-2 = use global is treated as
//     "not reached" — conservative, matches the Transmission
//     family pattern at internal/downloadclients/transmission/transmission.go).
func (q *QBittorrent) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	torrents, err := q.proxy.GetTorrents(ctx, q.settings)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: GetItems: %w", err)
	}

	info := q.itemClientInfo()

	items := make([]*downloadclients.Item, 0, len(torrents))
	for i := range torrents {
		items = append(items, q.torrentToItem(ctx, torrents[i], info))
	}
	return items, nil
}

// torrentToItem builds the typed Item from a qBittorrent torrent.
// Slice 6.B5 routes the resolved OutputPath through
// [QBittorrent.remapOutput] so off-host qBittorrent instances surface
// a Sonarr-side path; nil mapper / no match → raw WebUI-side path.
func (q *QBittorrent) torrentToItem(ctx context.Context, t QBittorrentTorrent, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	category := t.Category
	if strings.TrimSpace(category) == "" {
		category = t.Label
	}

	ratio := t.Ratio
	ratioPtr := &ratio

	remaining := int64(float64(t.Size) * (1.0 - t.Progress))
	if remaining < 0 {
		remaining = 0
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         strings.ToUpper(t.Hash),
		Category:           category,
		Title:              t.Name,
		TotalSize:          t.Size,
		RemainingSize:      remaining,
		SeedRatio:          ratioPtr,
	}

	// ETA: qBittorrent reports 8640000 (100 days) for completed
	// torrents — the C# tree clamps in the Completed arm at `:301`
	// rather than here. We mirror: assign a non-nil duration when
	// the torrent isn't completed; the completed arm below zeroes it.
	if t.Eta > 0 {
		d := time.Duration(t.Eta) * time.Second
		// Guard the int64 nanosecond range — same overflow shield
		// Transmission uses.
		if t.Eta > int64(time.Duration(1<<62)/time.Second) {
			d = time.Duration(t.Eta) * time.Millisecond
		}
		item.RemainingTime = &d
	}

	// Status mapping per C# `QBittorrent.cs:275-340`.
	completed := false
	switch t.State {
	case "error":
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = "qBittorrent reported an error on this torrent"

	case "stoppedDL", "pausedDL":
		item.Status = downloadclients.DownloadItemStatusPaused

	case "queuedDL", "checkingDL", "checkingUP", "checkingResumeData", "queuedForChecking":
		item.Status = downloadclients.DownloadItemStatusQueued

	case "pausedUP", "stoppedUP", "uploading", "stalledUP", "queuedUP", "forcedUP":
		item.Status = downloadclients.DownloadItemStatusCompleted
		zero := time.Duration(0)
		item.RemainingTime = &zero
		completed = true

	case "stalledDL":
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = "The download is stalled with no connections"

	case "missingFiles":
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = "The download is missing files"

	case "metaDL", "forcedMetaDL":
		// C# tree warns when DHT is disabled; deferred to 5.G1g
		// when GetStatus / preferences land. Treat as Queued
		// unconditionally — matches qBittorrent's own UI hint.
		item.Status = downloadclients.DownloadItemStatusQueued
		item.Message = "Downloading torrent metadata"

	case "forcedDL", "moving", "downloading":
		item.Status = downloadclients.DownloadItemStatusDownloading

	default:
		// Unknown state — log + treat as Downloading per C# `:336-339`.
		q.log.Info("qbittorrent: unknown torrent state, defaulting to Downloading",
			"hash", t.Hash, "state", t.State)
		item.Status = downloadclients.DownloadItemStatusDownloading
		item.Message = fmt.Sprintf("Unknown qBittorrent state: %s", t.State)
	}

	// OutputPath. For completed torrents the C# tree prefers
	// ContentPath (`:344-352`); we mirror, with the "ContentPath ==
	// SavePath" arm becoming a Warning to match `:349-352`. Slice
	// 6.B5: each surviving arm hands the resolved raw path to
	// remapOutput so off-host qBittorrent instances surface
	// Sonarr-side paths.
	if completed {
		switch {
		case strings.TrimSpace(t.ContentPath) != "" && t.ContentPath != t.SavePath:
			item.OutputPath = q.remapOutput(ctx, t.ContentPath)
		case strings.TrimSpace(t.ContentPath) != "" && t.ContentPath == t.SavePath:
			item.Status = downloadclients.DownloadItemStatusWarning
			item.Message = "qBittorrent reported the same path for ContentPath and SavePath; the import path is unknown"
		case strings.TrimSpace(t.SavePath) != "":
			item.OutputPath = q.remapOutput(ctx, joinTorrentPath(t.SavePath, t.Name))
		}
	} else if strings.TrimSpace(t.SavePath) != "" {
		item.OutputPath = q.remapOutput(ctx, joinTorrentPath(t.SavePath, t.Name))
	}

	// CanBeRemoved / CanMoveFiles gating per C# `:270-273`:
	// only when paused-after-completion AND the global remove flag
	// is set AND the seed limit was reached.
	pausedAfterComplete := t.State == "pausedUP" || t.State == "stoppedUP"
	item.CanBeRemoved = info != nil && info.RemoveCompletedDownloads &&
		pausedAfterComplete && hasReachedSeedLimit(t)
	item.CanMoveFiles = item.CanBeRemoved

	return item
}

// hasReachedSeedLimit ports the per-torrent half of C#
// `QBittorrent.HasReachedSeedLimit` at the consumer-callsite level.
// The full C# impl reads global preferences too; the Go-port
// version honours only the per-torrent `ratio_limit` /
// `seeding_time_limit` fields (-2 = "use global" is treated as
// "not reached" — conservative, matches the Transmission family
// pattern). Global-preference reads land at 5.G1g once GetStatus
// pulls /api/v2/app/preferences.
func hasReachedSeedLimit(t QBittorrentTorrent) bool {
	if t.RatioLimit >= 0 && t.Ratio >= t.RatioLimit {
		return true
	}
	if t.SeedingTimeLimit >= 0 && t.SeedingTime != nil && *t.SeedingTime >= t.SeedingTimeLimit*60 {
		return true
	}
	return false
}

// itemClientInfo mirrors C#
// `DownloadClientItemClientInfo.FromDownloadClient` at
// `DownloadClientItemClientInfo.cs`. HasPostImportCategory is true
// when settings.TvImportedCategory is non-empty per C#
// `QBittorrent.cs:262`.
func (q *QBittorrent) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol:              q.Protocol(),
		Type:                  Implementation,
		Name:                  q.Name(),
		HasPostImportCategory: strings.TrimSpace(q.settings.TvImportedCategory) != "",
	}
	if q.def != nil {
		info.ID = q.def.ID
		info.Name = q.def.Name
		info.RemoveCompletedDownloads = q.def.RemoveCompletedDownloads
	}
	return info
}

// joinTorrentPath joins qBittorrent's save_path (always
// '/'-separated, per C# `:386` comment) with the torrent name.
// Trims any trailing slash on the base.
func joinTorrentPath(base, name string) string {
	if base == "" {
		return name
	}
	if name == "" {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(name, "/")
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (q *QBittorrent) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports C# `QBittorrent.RemoveItem` at
// `Download/Clients/QBittorrent/QBittorrent.cs:361-364`. Lowercases
// the DownloadID (matches the C# `:363` `.ToLower()` call —
// qBittorrent rejects mixed-case hashes on /api/v2/torrents/delete)
// and delegates to [Proxy.RemoveTorrent].
func (q *QBittorrent) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if item == nil {
		return errors.New("qbittorrent: RemoveItem: nil item")
	}
	if q.proxy == nil {
		return errors.New("qbittorrent: RemoveItem: nil proxy")
	}
	hash := strings.ToLower(item.DownloadID)
	if err := q.proxy.RemoveTorrent(ctx, hash, deleteData, q.settings); err != nil {
		return fmt.Errorf("qbittorrent: RemoveItem(%s): %w", hash, err)
	}
	return nil
}

// GetStatus ports C# `QBittorrent.GetStatus` at
// `Download/Clients/QBittorrent/QBittorrent.cs:400-438`. Fetches the
// global preferences via /api/v2/app/preferences and projects:
//
//   - IsLocalhost — true when settings.Host is 127.0.0.1 or
//     "localhost" (case-insensitive). The C# arm at `:434` checks
//     literal equality against "localhost"; the Go port relaxes to
//     case-insensitive since URL hostnames are case-folded anyway.
//   - OutputRootFolders — [ospath.New(SavePath)]. The C# tree
//     additionally consults the category-specific savePath via
//     [Proxy.GetLabels] when TvCategory is set (`:407-430`); that
//     branch is deferred to a follow-up slice — the GetLabels
//     endpoint port hasn't landed and the global SavePath is
//     load-bearing for the common case.
//   - RemovesCompletedDownloads — see [removesCompletedDownloads].
func (q *QBittorrent) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	if q.proxy == nil {
		return nil, errors.New("qbittorrent: GetStatus: nil proxy")
	}
	prefs, err := q.proxy.GetConfig(ctx, q.settings)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: GetStatus: %w", err)
	}
	info := &downloadclients.ClientInfo{
		IsLocalhost:               q.settings.Host == "127.0.0.1" || strings.EqualFold(q.settings.Host, "localhost"),
		RemovesCompletedDownloads: removesCompletedDownloads(prefs),
	}
	if strings.TrimSpace(prefs.SavePath) != "" {
		info.OutputRootFolders = []ospath.OsPath{q.remapOutput(ctx, prefs.SavePath)}
	}
	return info, nil
}

// removesCompletedDownloads ports C# `QBittorrent.RemovesCompletedDownloads`
// at `QBittorrent.cs:440-444`. Returns true when qBittorrent will
// itself remove a completed torrent without Sonarr's intervention.
//
// Two predicates AND together:
//
//  1. Either MaxRatioEnabled is on, OR (MaxSeedingTimeEnabled is on
//     AND the cap is under 14 days). The 14-day floor matches the
//     C# `minimumRetention = 60*24*14` literal — qBittorrent users
//     who set a seeding cap above two weeks aren't relying on the
//     client for cleanup at the Sonarr-import timescale.
//  2. MaxRatioAction is Remove or DeleteFiles. If the user picked
//     Pause / EnableSuperSeeding, qBittorrent doesn't actually
//     delete the torrent — so Sonarr can still safely remove it.
func removesCompletedDownloads(prefs *QBittorrentPreferences) bool {
	if prefs == nil {
		return false
	}
	const minimumRetentionMinutes = int64(60 * 24 * 14)
	limitReached := prefs.MaxRatioEnabled ||
		(prefs.MaxSeedingTimeEnabled && prefs.MaxSeedingTime < minimumRetentionMinutes)
	actionRemoves := prefs.MaxRatioAction == MaxRatioActionRemove ||
		prefs.MaxRatioAction == MaxRatioActionDeleteFiles
	return limitReached && actionRemoves
}

// MarkItemAsImported ports C# `QBittorrent.MarkItemAsImported` at
// `Download/Clients/QBittorrent/QBittorrent.cs:56-73`. When the user
// has configured a post-import category distinct from TvCategory,
// swaps the torrent's category to it via
// /api/v2/torrents/setCategory.
//
// Failure swallowed (log + return nil): the C# catch arm at `:66-71`
// warns "Failed to set post-import torrent label" and continues —
// the import path has already moved files, so propagating the error
// would block subsequent imports without recovering anything. The
// Go port matches.
func (q *QBittorrent) MarkItemAsImported(ctx context.Context, item *downloadclients.Item) error {
	if item == nil {
		return errors.New("qbittorrent: MarkItemAsImported: nil item")
	}
	if q.proxy == nil {
		return errors.New("qbittorrent: MarkItemAsImported: nil proxy")
	}
	imported := strings.TrimSpace(q.settings.TvImportedCategory)
	current := strings.TrimSpace(q.settings.TvCategory)
	if imported == "" || imported == current {
		return nil
	}
	hash := strings.ToLower(item.DownloadID)
	if err := q.proxy.SetTorrentLabel(ctx, hash, imported, q.settings); err != nil {
		q.log.Warn("qbittorrent: Failed to set post-import torrent label",
			"hash", hash, "label", imported, "err", err)
		return nil
	}
	return nil
}

// Test ports the connection + version-compat probe from C#
// `QBittorrent.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:459-537`.
//
// Flow:
//
//  1. Run Settings.Validate first — structural failures
//     short-circuit so a malformed config never triggers a network
//     round-trip + WebUI lockout.
//  2. Ensure the proxy is bound. A nil proxy is a programmer-error
//     return (non-nil error) rather than a TestFailure — the
//     registry constructor guarantees a non-nil proxy on the
//     production path.
//  3. EnsureAuth runs the login flow (skipped automatically when
//     ApiKey is set). Failures surface as field-level TestFailures
//     pinned to "ApiKey"/"Username"/"Password"/"Host" per the C#
//     branches at `:504-534`.
//  4. GET /api/v2/app/version → version string like "v4.5.0".
//     Non-200 surfaces a "Host" TestFailure (`:518-521`); unparsable
//     bodies surface a "Version" TestFailure; versions below
//     [minVersion] surface a "Version" TestFailure mirroring
//     `:467-472`.
//
// Transport errors (connection refused, TLS handshake, etc.)
// propagate via the returned error — matches the Transmission Test
// pattern at `internal/downloadclients/transmission/transmission.go`
// where transport failures bubble as errors while semantic failures
// stay in the slice.
func (q *QBittorrent) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	if failures := q.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	if q.proxy == nil {
		return nil, errors.New("qbittorrent: Test: nil proxy (constructor not wired)")
	}

	// Step 1: login. EnsureAuth is a no-op for ApiKey + no-creds
	// auth modes; for cookie auth it fires POST /api/v2/auth/login.
	if _, err := q.proxy.EnsureAuth(ctx, q.settings, true); err != nil {
		if authFailure := classifyAuthError(err, q.settings); authFailure != nil {
			return []downloadclients.TestFailure{*authFailure}, nil
		}
		return nil, err
	}

	// Step 2: version probe. The C# code uses /api/v2/app/webapiVersion;
	// the Go port uses /api/v2/app/version because:
	//   - the endpoint exists from qBittorrent 4.1.0 onwards (the
	//     WebUI API v2 floor anyway), so the reachability test is
	//     identical;
	//   - the response body ("v4.5.0") is human-readable + maps
	//     directly to the version-floor failure message.
	status, body, err := q.proxy.DoRequest(ctx, http.MethodGet, "/api/v2/app/version", nil, "", q.settings)
	if err != nil {
		if authFailure := classifyAuthError(err, q.settings); authFailure != nil {
			return []downloadclients.TestFailure{*authFailure}, nil
		}
		return nil, err
	}

	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return []downloadclients.TestFailure{{
			PropertyName:        authProperty(q.settings),
			ErrorMessage:        fmt.Sprintf("Authentication failure (HTTP %d)", status),
			DetailedDescription: fmt.Sprintf("qBittorrent rejected the credentials supplied to %s.", q.Name()),
		}}, nil
	}
	if status != http.StatusOK {
		return []downloadclients.TestFailure{{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to reach qBittorrent (HTTP %d)", status),
			DetailedDescription: truncate(body, 256),
		}}, nil
	}

	raw := strings.TrimSpace(string(body))
	version := parseVersion(raw)
	if version == nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse qBittorrent version: %q", raw),
		}}, nil
	}
	if version.less(minVersion) {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("qBittorrent %s is too old; %s requires at least %d.%d",
				raw, q.Name(), minVersion.major, minVersion.minor),
		}}, nil
	}

	// Beyond TestConnection the C# tree also runs TestCategory /
	// TestPrioritySupport / TestGetTorrents (`:454-456`). Those
	// land in later slices once GetItems / category support arrive.
	return nil, nil
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed auth
// arms. Returns nil for other errors so the caller surfaces them as
// an error return.
func classifyAuthError(err error, settings Settings) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrIPBanned):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "qBittorrent has banned the Sonarr IP due to repeated failed login attempts.",
			DetailedDescription: "Restart qBittorrent or wait for the WebUI ban timeout, then retry.",
		}
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        authProperty(settings),
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to qBittorrent",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// authProperty returns the field name a credentials failure should
// be pinned to. Mirrors the C# branch at `QBittorrent.cs:508` —
// ApiKey-bound credentials surface against "ApiKey", everything
// else against "Username".
func authProperty(settings Settings) string {
	if strings.TrimSpace(settings.ApiKey) != "" {
		return "ApiKey"
	}
	return "Username"
}

// versionPair holds a parsed qBittorrent product version (major +
// minor). Mirrors Transmission's helper at
// `internal/downloadclients/transmission/transmission.go::versionPair`.
type versionPair struct {
	major, minor int
}

// less reports whether v < other. Lexicographic on (major, minor).
func (v versionPair) less(other versionPair) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	return v.minor < other.minor
}

// versionPattern extracts the leading "major.minor" pair from a
// qBittorrent /api/v2/app/version reply. qBittorrent prefixes the
// response with a literal 'v' (e.g. "v4.5.0"), but some forks
// (vuetorrent, custom builds) strip the prefix or append a build
// tag. The pattern tolerates both.
var versionPattern = regexp.MustCompile(`(?i)^v?(\d+)\.(\d+)`)

func parseVersion(raw string) *versionPair {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) < 3 {
		return nil
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return &versionPair{major: major, minor: minor}
}

// RequestAction is the per-impl action map. Ports the C# action
// surface from `QBittorrent.cs` — the only action qBittorrent
// exposes is "getCategories", which the v3 React UI uses to
// populate the category-dropdown when the user edits the
// DownloadClient row.
//
// Unknown actions fall through to (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
func (q *QBittorrent) RequestAction(ctx context.Context, action string, _ map[string]string) (any, error) {
	if action != "getCategories" {
		return nil, nil
	}
	if q.proxy == nil {
		return nil, errors.New("qbittorrent: RequestAction(getCategories): nil proxy")
	}
	labels, err := q.proxy.GetLabels(ctx, q.settings)
	if err != nil {
		return nil, err
	}
	// Project to a list-shaped response the v3 UI can render
	// without inferring the keying. Order is stable-but-
	// unspecified — the UI sorts client-side.
	out := make([]QBittorrentLabel, 0, len(labels))
	for k, v := range labels {
		if v.Name == "" {
			v.Name = k
		}
		out = append(out, v)
	}
	return out, nil
}

// Compile-time guard: QBittorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*QBittorrent)(nil)

// constructor is the registry factory. Mirrors the Transmission +
// SAB factory pattern from sub-arc 14a/b.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("qbittorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("qbittorrent: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G1a round-trip suite) get a nil proxy + a Provider that
	// surfaces failures only via Test(); production wiring always
	// supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("qbittorrent: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	qbt := New(def, settings, proxy, log)
	// Slice 6.B5: cast the typed-as-any RemotePathMappingService to
	// the narrow RemotePathMapper interface. nil dep / cast miss
	// leaves qbt.rpm nil; per-call sites fall through to the raw
	// WebUI-side path.
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		qbt.SetRemotePathMapper(rpm)
	}
	return qbt, nil
}

// init registers qBittorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
