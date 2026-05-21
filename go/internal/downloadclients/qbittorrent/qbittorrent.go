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
//   - WaitForTorrent at C# `:218-242` (poll-until-visible for the
//     post-add RPC chain) is replaced by an "apply best-effort"
//     posture — the post-add RPCs log + continue on failure, which
//     matches the C# catch arms at `:104-106,116-118,127-129`.
//
// The "magnet without trackers when DHT disabled" guard at C#
// `:77-80` is wired at slice 5.J114 — see [magnetWithoutTrackersErr]
// for the per-grab probe. The probe fires GetConfig ONLY when the
// grab is a magnet without a tracker query parameter, so the
// typical magnet-with-tracker case skips the extra prefs RPC.
//
// InfoHash sourcing:
//
//   - req.InfoHash trusted when set.
//   - Otherwise extracted from MagnetURL's xt=urn:btih:HASH query
//     parameter when present.
//   - Otherwise extracted from TorrentBody via SHA-1 of the bencoded
//     "info" dict (slice 6.H — BEP-3 spec).
//   - Otherwise an error.
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
		if guardErr := q.magnetWithoutTrackersGuard(ctx, req.MagnetURL); guardErr != nil {
			return "", guardErr
		}
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
	// Slice 6.F (closes the MoveTorrentToTopInQueue deferral): when
	// the user's per-recency priority setting resolves to PriorityFirst,
	// move the just-added torrent to the top of qBittorrent's queue.
	// Mirrors C# `:109-119,178-188`. Best-effort: a 409 from
	// qBittorrent (queueing disabled in prefs) is silently swallowed
	// inside the proxy; other failures warn + continue.
	if recencyPriority(req.IsRecent, q.settings) == PriorityFirst {
		if prioErr := q.proxy.MoveTorrentToTopInQueue(ctx, hash, q.settings); prioErr != nil {
			q.log.Warn("qbittorrent: MoveTorrentToTopInQueue failed",
				"hash", hash, "err", prioErr)
		}
	}
	// Slice 6.I (closes the AddSeriesTags deferral): when the
	// AddSeriesTags setting is on and the orchestrator threaded a
	// SeriesTitle through, tag the just-added torrent with the
	// series title. Mirrors C# `:133-143,202-212`. Best-effort:
	// failures warn + continue. The C# tree tags with the resolved
	// per-series Tag labels — the Go port simplifies to
	// SeriesTitle as the single tag (see GrabRequest.SeriesTitle
	// doc-comment for the divergence note).
	if q.settings.AddSeriesTags && req.SeriesTitle != "" {
		if tagErr := q.proxy.AddTags(ctx, hash, req.SeriesTitle, q.settings); tagErr != nil {
			q.log.Warn("qbittorrent: AddTags failed",
				"hash", hash, "series", req.SeriesTitle, "err", tagErr)
		}
	}

	return strings.ToUpper(hash), nil
}

// recencyPriority resolves req.IsRecent to the per-recency
// priority setting. Mirrors C# `:73,76`.
func recencyPriority(isRecent bool, settings Settings) int {
	if isRecent {
		return settings.RecentTvPriority
	}
	return settings.OlderTvPriority
}

// magnetWithoutTrackersGuard ports C# `:77-80` — when a magnet link
// carries no `tr=` tracker parameter AND the qBittorrent server's
// DHT global is disabled, qBittorrent has no way to discover peers
// and the grab would stall at metaDL forever. The guard surfaces
// [ErrMagnetWithoutTrackersDHTDisabled] so the orchestrator can
// route the failure into the Pending bucket rather than the
// download-client retry-loop (matches the C# `NotSupportedException`
// posture at `:79`).
//
// Cost-management: the C# tree fires GetConfig unconditionally on
// every magnet grab; the Go port only fires when the magnet
// actually lacks a tracker, so the typical magnet-with-tracker
// case skips the extra prefs RPC entirely. GetConfig errors degrade
// to "allow the grab" with a debug log — the guard is a best-effort
// upstream safety check, not load-bearing for the grab itself.
func (q *QBittorrent) magnetWithoutTrackersGuard(ctx context.Context, magnetURL string) error {
	// Match the C# `:77` `!magnetLink.Contains("&tr=")` literal —
	// magnet RFC encodes trackers as `&tr=` (not `?tr=`, which only
	// appears as the first arg and Sonarr-grabbed magnets always
	// place infoHash first). Mirroring the C# check exactly avoids
	// surfacing the guard for valid magnets that happen to use a
	// different parameter ordering.
	if strings.Contains(magnetURL, "&tr=") {
		return nil
	}
	prefs, err := q.proxy.GetConfig(ctx, q.settings)
	if err != nil {
		q.log.Debug("qbittorrent: GetConfig failed during magnet-DHT guard; allowing the grab",
			slog.Any("err", err))
		return nil
	}
	if prefs == nil || prefs.DhtEnabled {
		return nil
	}
	return ErrMagnetWithoutTrackersDHTDisabled
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
	if len(req.TorrentBody) > 0 {
		// Slice 6.H: derive the BEP-3 info-hash from the bencoded
		// .torrent body. Lets indexers ship the raw .torrent without
		// pre-computing the hash — qBittorrent will key the torrent
		// by this value, so DownloadID stays consistent across re-
		// queries.
		if h, err := extractInfoHash(req.TorrentBody); err == nil {
			return h, nil
		}
	}
	return "", errors.New("qbittorrent: Download: GrabRequest has no InfoHash, no urn:btih: in MagnetURL, and no valid bencoded info dict in TorrentBody")
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
//     is wired via the slice 6.E lazy GetConfig probe inside
//     [torrentToItem] — the prefs RPC fires once per GetItems call
//     ONLY when a metaDL / forcedMetaDL state appears, so the
//     typical no-metaDL case skips the extra round-trip entirely.
//     A GetConfig failure degrades the warning to silent (warn-log
//   - treat as Queued).
//   - The remote-path-mapping translation at C# `:346` is wired via
//     [QBittorrent.SetRemotePathMapper] (Phase 6.B) — slice 6.B
//     plumbs `*remotepathmappings.Service` through
//     [downloadclients.Dependencies.RemotePathMappingService] and the
//     constructor casts to [downloadclients.RemotePathMapper]. Nil
//     binding falls through to the raw RPC-side ContentPath /
//     SavePath (matches the pre-6.B posture).
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

	// Slice 6.E: lazy GetConfig probe — only fires when a metaDL /
	// forcedMetaDL state appears AND we haven't already learned
	// DhtEnabled. Mirrors the C# `:316-325` warn-on-metaDL-when-DHT-
	// disabled arm without paying the prefs RPC cost on every
	// scan (the typical case has no torrents in metaDL).
	var (
		prefs    *QBittorrentPreferences
		prefsErr error
		prefsHit bool
	)
	loadPrefs := func() *QBittorrentPreferences {
		if prefsHit {
			return prefs
		}
		prefsHit = true
		prefs, prefsErr = q.proxy.GetConfig(ctx, q.settings)
		if prefsErr != nil {
			q.log.Warn("qbittorrent: GetItems: GetConfig failed; DHT-disabled warning degrades to silent",
				"err", prefsErr)
		}
		return prefs
	}

	items := make([]*downloadclients.Item, 0, len(torrents))
	for i := range torrents {
		items = append(items, q.torrentToItem(ctx, torrents[i], info, loadPrefs))
	}
	return items, nil
}

// torrentToItem builds the typed Item from a qBittorrent torrent.
// Slice 6.B5 routes the resolved OutputPath through
// [QBittorrent.remapOutput] so off-host qBittorrent instances surface
// a Sonarr-side path; nil mapper / no match → raw WebUI-side path.
// Slice 6.E threads a lazy preferences loader so the metaDL arm can
// promote to Warning when DhtEnabled=false (matches C# :316-325).
func (q *QBittorrent) torrentToItem(ctx context.Context, t QBittorrentTorrent, info *downloadclients.ItemClientInfo, loadPrefs func() *QBittorrentPreferences) *downloadclients.Item {
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
		// Slice 6.E (closes the 5.G1d deferral): promote to Warning
		// when DhtEnabled=false. Magnets without DHT can stall in
		// metaDL indefinitely; the warning surfaces the issue in the
		// UI. nil prefs (GetConfig failed, or no loader supplied via
		// a unit-test call site) degrades to plain Queued — matches
		// pre-6.E behaviour. Mirrors C# `:316-325`.
		if prefs := loadPrefs(); prefs != nil && !prefs.DhtEnabled {
			item.Status = downloadclients.DownloadItemStatusWarning
			item.Message = "qBittorrent cannot resolve magnet without DHT — the global DHT setting is disabled"
		} else {
			item.Status = downloadclients.DownloadItemStatusQueued
			item.Message = "Downloading torrent metadata"
		}

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
//   - IsLocalhost — routes through
//     [downloadclients.IsLocalhostHost]. The C# arm at `:434` checks
//     literal equality `Host == "127.0.0.1" || Host == "localhost"`;
//     the Go port (Phase 5.J175) widens to case-insensitive +
//     whitespace-trim + IPv6 loopback (`::1`) via the shared helper,
//     since URL hostnames are case-folded anyway and the wider check
//     is strictly a superset.
//   - OutputRootFolders — when settings.TvCategory is non-empty AND
//     [Proxy.GetLabels] returns a matching label with a non-empty
//     per-category SavePath, that category-specific path is used
//     (slice 6.G — closes the `:407-430` deferral). Otherwise falls
//     back to the global prefs.SavePath. Both arms route through
//     remapOutput so off-host qBittorrent surfaces a Sonarr-side
//     path. A GetLabels failure silently degrades to the global
//     SavePath (best-effort; matches the C# catch-and-continue).
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
		IsLocalhost:               downloadclients.IsLocalhostHost(q.settings.Host),
		RemovesCompletedDownloads: removesCompletedDownloads(prefs),
	}
	savePath := strings.TrimSpace(prefs.SavePath)
	if tv := strings.TrimSpace(q.settings.TvCategory); tv != "" {
		if catPath := q.categorySavePath(ctx, tv); catPath != "" {
			savePath = catPath
		}
	}
	if savePath != "" {
		info.OutputRootFolders = []ospath.OsPath{q.remapOutput(ctx, savePath)}
	}
	return info, nil
}

// categorySavePath looks up the per-category SavePath override for
// the supplied TvCategory via GetLabels. Returns "" when GetLabels
// fails, no label matches the category name (case-sensitive — the
// qBittorrent API treats category names as exact-match keys), or
// the matched label has no per-category SavePath override. Mirrors
// the C# arm at `:407-430`. Slice 6.G.
func (q *QBittorrent) categorySavePath(ctx context.Context, tvCategory string) string {
	labels, err := q.proxy.GetLabels(ctx, q.settings)
	if err != nil {
		q.log.Warn("qbittorrent: GetStatus: GetLabels failed; falling back to global SavePath",
			"category", tvCategory, "err", err)
		return ""
	}
	label, ok := labels[tvCategory]
	if !ok {
		return ""
	}
	return strings.TrimSpace(label.SavePath)
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

	// J104 — extended Test arm matching C# `:454-456` ordering.
	// TestConnection finishing in-place (above) already covers the
	// authentication-version-floor portion; the remaining C# arm
	// (`:494-502` RemovesAtRatioLimit + recommended-category warning)
	// is rolled into testConnectionExtras below so it shares the
	// GetConfig call with TestPrioritySupport.
	prefs, prefsErr := q.proxy.GetConfig(ctx, q.settings)
	var failures []downloadclients.TestFailure
	if prefsErr != nil {
		// C# at :513-524 catches WebException + Exception differently;
		// the Go port pins the config-fetch failure to Host so the
		// user knows it's a connection-layer issue. Subsequent
		// validators that need prefs skip — they're best-effort.
		failures = append(failures, downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to fetch qBittorrent preferences: %v", prefsErr),
			DetailedDescription: prefsErr.Error(),
		})
		return failures, nil
	}
	if f := testConnectionExtras(prefs, q.settings); f != nil {
		failures = append(failures, *f)
	}
	if f := q.testCategory(ctx); f != nil {
		failures = append(failures, *f)
	}
	if f := testPrioritySupport(prefs, q.settings); f != nil {
		failures = append(failures, *f)
	}
	if f := q.testGetTorrents(ctx); f != nil {
		failures = append(failures, *f)
	}
	return failures, nil
}

// testConnectionExtras ports the tail of C# `TestConnection` at
// `:484-502`: empty-category recommendation warning + the
// RemovesAtRatioLimit whole-form failure that fires when qBit is
// configured to remove torrents at max ratio (which would race the
// Sonarr import handoff).
//
// The "recommended-category" arm at C# `:484-492` is surfaced as a
// TvCategory failure with the Sonarr-side "warning" intent — the Go
// TestFailure shape doesn't yet distinguish errors-vs-warnings, so
// downstream UI consumers should treat any TvCategory failure
// missing a DetailedDescription containing "Add a category" as a
// warning. Matches the C# `IsWarning=true` annotation at :489.
func testConnectionExtras(prefs *QBittorrentPreferences, settings Settings) *downloadclients.TestFailure {
	if strings.TrimSpace(settings.TvCategory) == "" {
		return &downloadclients.TestFailure{
			PropertyName:        "TvCategory",
			ErrorMessage:        "Category is recommended for qBittorrent",
			DetailedDescription: "Set a category so Sonarr can manage downloads independently of other torrents.",
		}
	}
	if removesCompletedDownloads(prefs) {
		return &downloadclients.TestFailure{
			PropertyName:        "",
			ErrorMessage:        "qBittorrent is configured to remove torrents when they reach the maximum ratio",
			DetailedDescription: "Sonarr handles the post-import cleanup itself; configure qBittorrent's max-ratio action to Pause (not Remove) to avoid races with the import handoff.",
		}
	}
	return nil
}

// testCategory ports C# `TestCategory` at `:539-584`. Both
// TvCategory + TvImportedCategory empty → noop. Otherwise the C#
// version-gate at `:546-551` skips the probe entirely on API
// version < 2.0 (qBit 4.1+); the Go port already requires API v2
// for the proxy to work at all (the version-floor at TestConnection
// rejects < 4.1.0), so we always run the label probe here.
//
// Per-category: when the configured label is missing from
// /api/v2/torrents/categories, fire AddLabel + re-fetch; still-
// missing surfaces "label add failure" per C# `:560-566` /
// `:574-580`.
func (q *QBittorrent) testCategory(ctx context.Context) *downloadclients.TestFailure {
	tv := strings.TrimSpace(q.settings.TvCategory)
	tvImported := strings.TrimSpace(q.settings.TvImportedCategory)
	if tv == "" && tvImported == "" {
		return nil
	}
	labels, err := q.proxy.GetLabels(ctx, q.settings)
	if err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        "TvCategory",
			ErrorMessage:        "Unable to fetch qBittorrent categories",
			DetailedDescription: err.Error(),
		}
	}
	if f := q.ensureLabel(ctx, tv, "TvCategory", labels); f != nil {
		return f
	}
	if f := q.ensureLabel(ctx, tvImported, "TvImportedCategory", labels); f != nil {
		return f
	}
	return nil
}

// ensureLabel ports the C# auto-create arm at `:555-580`. Empty
// label → noop. Present → noop. Missing → AddLabel + re-fetch;
// still-missing surfaces a "label add failure" on the configured
// property. AddLabel RPC failure short-circuits with the same
// failure (matches C# behaviour where an AddLabel exception bubbles
// through the surrounding try/catch).
func (q *QBittorrent) ensureLabel(ctx context.Context, label, prop string, labels map[string]QBittorrentLabel) *downloadclients.TestFailure {
	if label == "" {
		return nil
	}
	if _, ok := labels[label]; ok {
		return nil
	}
	if err := q.proxy.AddLabel(ctx, label, q.settings); err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        prop,
			ErrorMessage:        "Failed to add the configured category to qBittorrent",
			DetailedDescription: err.Error(),
		}
	}
	refreshed, err := q.proxy.GetLabels(ctx, q.settings)
	if err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        prop,
			ErrorMessage:        "Unable to re-fetch qBittorrent categories after add",
			DetailedDescription: err.Error(),
		}
	}
	for k, v := range refreshed {
		labels[k] = v
	}
	if _, ok := labels[label]; ok {
		return nil
	}
	return &downloadclients.TestFailure{
		PropertyName:        prop,
		ErrorMessage:        "Failed to add the configured category to qBittorrent",
		DetailedDescription: fmt.Sprintf("qBittorrent accepted the createCategory request for %q but the category is still missing from /api/v2/torrents/categories.", label),
	}
}

// testPrioritySupport ports C# `TestPrioritySupport` at `:586-625`.
// When EITHER RecentTvPriority OR OlderTvPriority is set to First
// (priority that triggers MoveTorrentToTopInQueue), qBittorrent's
// QueueingEnabled global must be on; otherwise the move-to-top RPC
// silently fails. Both priorities at Last → noop (no MoveToTop
// needed). Only one priority at First + queueing off → property-
// pinned failure on the offending priority field.
//
// Shares the prefs already fetched by the caller for the
// RemovesAtRatioLimit check — no extra round-trip.
func testPrioritySupport(prefs *QBittorrentPreferences, settings Settings) *downloadclients.TestFailure {
	if prefs == nil {
		return nil
	}
	recentDefault := settings.RecentTvPriority != PriorityFirst
	olderDefault := settings.OlderTvPriority != PriorityFirst
	if recentDefault && olderDefault {
		return nil
	}
	if prefs.QueueingEnabled {
		return nil
	}
	prop := "RecentTvPriority"
	if recentDefault {
		prop = "OlderTvPriority"
	}
	return &downloadclients.TestFailure{
		PropertyName:        prop,
		ErrorMessage:        "qBittorrent's torrent queueing is disabled",
		DetailedDescription: "Sonarr's First priority setting requires qBittorrent's torrent-queueing global to be enabled. Open qBittorrent > Options > BitTorrent > 'Torrent Queueing' and enable it, or change Sonarr's priority back to Last.",
	}
}

// testGetTorrents ports C# `TestGetTorrents` at `:627-640`. One-shot
// GetTorrents probe against the live daemon; any error surfaces a
// whole-form failure containing the cause. Mirrors the J102
// Transmission TestGetTorrents arm + the J101 Deluge testGetTorrents.
func (q *QBittorrent) testGetTorrents(ctx context.Context) *downloadclients.TestFailure {
	if _, err := q.proxy.GetTorrents(ctx, q.settings); err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Failed to get the list of torrents: %v", err),
			DetailedDescription: "Sonarr was able to authenticate against qBittorrent but the torrent-list query failed; check the qBittorrent log.",
		}
	}
	return nil
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
