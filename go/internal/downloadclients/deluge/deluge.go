package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the Deluge download client. Mirrors C# class names `Deluge` +
// `DelugeSettings` (see
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs` and
// `DelugeSettings.cs:18`). The (Implementation, ConfigContract) pair
// is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "Deluge"
	ConfigContract = "DelugeSettings"
)

// Deluge is the [downloadclients.Provider] implementation for the
// Deluge torrent client. Ports C# `Deluge` at
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs` + the
// `DelugeProxy.cs` JSON-RPC body.
//
// Slice 5.G2c scope: Name / Protocol / Definition / RequestAction /
// GetImportItem / Test live. Download / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type Deluge struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a Deluge Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Deluge {
	if log == nil {
		log = slog.Default()
	}
	return &Deluge{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `Deluge.Name`
// (inherited default — the class name itself; see
// `DownloadClientBase.cs:62`).
func (d *Deluge) Name() string { return "Deluge" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (d *Deluge) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClients row.
func (d *Deluge) Definition() *downloadclients.DownloadClient { return d.def }

// Settings returns the resolved Settings struct. Exposed for the
// 5.G2d+ slices and tests.
func (d *Deluge) Settings() Settings { return d.settings }

// Proxy returns the bound proxy.
func (d *Deluge) Proxy() *Proxy { return d.proxy }

// Download ports the C# Deluge add orchestrator at
// `Download/Clients/Deluge/Deluge.cs:58-110` — both AddFromMagnetLink
// and AddFromTorrentFile share the same post-add steps (seed-config,
// label, queue-top), so the Go port collapses them into one method.
//
// Add-path selection:
//
//   - TorrentBody non-empty → AddTorrentFromFile (base64-encoded).
//   - MagnetURL non-empty → AddTorrentFromMagnet.
//   - DownloadURL only, no MagnetURL and no TorrentBody → error.
//     Deluge has no "fetch this URL" add — the orchestrator should
//     pre-fetch the .torrent bytes into req.TorrentBody before
//     dispatch. The C# tree's TorrentClientBase pre-fetches at a
//     layer above the per-impl Download method.
//
// Post-add best-effort RPCs (log + continue on failure, matches
// the C# pattern that doesn't propagate post-add failures since
// the torrent is already loaded into deluge):
//
//   - SetTorrentSeedingConfiguration when req.SeedConfiguration != nil.
//   - SetTorrentLabel when settings.TvCategory is non-empty.
//   - MoveTorrentToTopInQueue when (IsRecent + RecentTvPriority==First)
//     OR (!IsRecent + OlderTvPriority==First). Matches C#
//     `:74-80,101-107`.
//
// Returns the upper-cased info-hash for the DownloadID handoff
// (matches the GetItems DownloadID convention).
func (d *Deluge) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("deluge: Download: nil request")
	}
	if d.proxy == nil {
		return "", errors.New("deluge: Download: nil proxy")
	}

	var (
		hash string
		err  error
	)
	switch {
	case len(req.TorrentBody) > 0:
		hash, err = d.proxy.AddTorrentFromFile(ctx, fallbackFilename(req), req.TorrentBody, d.settings)
	case strings.TrimSpace(req.MagnetURL) != "":
		hash, err = d.proxy.AddTorrentFromMagnet(ctx, req.MagnetURL, d.settings)
	default:
		return "", errors.New("deluge: Download: GrabRequest has neither TorrentBody nor MagnetURL; orchestrator must pre-fetch DownloadURL")
	}
	if err != nil {
		return "", fmt.Errorf("deluge: Download(%q): %w", req.ReleaseTitle, err)
	}

	// Post-add steps — warn-and-continue (C# tree at :67-79,94-106
	// doesn't propagate post-add failures; the torrent is already
	// loaded).
	if req.SeedConfiguration != nil {
		if seedErr := d.proxy.SetTorrentSeedingConfiguration(ctx, hash, req.SeedConfiguration, d.settings); seedErr != nil {
			d.log.Warn("deluge: SetTorrentSeedingConfiguration failed",
				"hash", hash, "err", seedErr)
		}
	}
	if cat := strings.TrimSpace(d.settings.TvCategory); cat != "" {
		if labErr := d.proxy.SetTorrentLabel(ctx, hash, cat, d.settings); labErr != nil {
			d.log.Warn("deluge: SetTorrentLabel failed",
				"hash", hash, "label", cat, "err", labErr)
		}
	}
	if shouldMoveToTop(req.IsRecent, d.settings) {
		if qErr := d.proxy.MoveTorrentToTopInQueue(ctx, hash, d.settings); qErr != nil {
			d.log.Warn("deluge: MoveTorrentToTopInQueue failed",
				"hash", hash, "err", qErr)
		}
	}

	return strings.ToUpper(hash), nil
}

// shouldMoveToTop ports the priority gate from C# `:76-77,103-104`:
// move-to-top fires when the relevant priority setting is set to
// First (1). Recent episodes use RecentTvPriority; older use
// OlderTvPriority.
func shouldMoveToTop(isRecent bool, settings Settings) bool {
	if isRecent {
		return settings.RecentTvPriority == PriorityFirst
	}
	return settings.OlderTvPriority == PriorityFirst
}

// fallbackFilename returns a reasonable .torrent filename for the
// AddTorrentFromFile request. Deluge uses the filename only for the
// WebUI's pending-torrent display + as the suggested rename — any
// non-empty value works.
func fallbackFilename(req *downloadclients.GrabRequest) string {
	if t := strings.TrimSpace(req.ReleaseTitle); t != "" {
		return t + ".torrent"
	}
	return "release.torrent"
}

// GetItems ports C# `Deluge.GetItems` at
// `Download/Clients/Deluge/Deluge.cs:114-215`. Fetches every torrent
// the WebUI reports (filtered server-side by label when
// settings.TvCategory is non-empty per the C# `GetTorrentsByLabel`
// branch at `:118-121`) and projects each into the orchestrator's
// canonical [downloadclients.Item] shape.
//
// Differences from the C# port:
//
//   - The ReconnectToDaemon arm at C# `:198-208` (when ignored
//     torrents trip a stale-daemon reconnect) is deferred — the
//     reconnect helper lands when 5.G2g GetStatus drives the
//     web.connected probe.
//   - Remote-path-mapping translation at C# `:147` is deferred
//     until the [downloadclients.Dependencies.RemotePathMappingService]
//     port lands (same as 5.G1d / qBittorrent).
//   - Ignored-torrent counter at C# `:128,136-138` collapses to a
//     single slog.Warn per call rather than the stateful "have
//     we tried reconnecting yet" branch.
func (d *Deluge) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if d.proxy == nil {
		return nil, errors.New("deluge: GetItems: nil proxy")
	}
	torrents, err := d.proxy.GetTorrents(ctx, d.settings)
	if err != nil {
		return nil, fmt.Errorf("deluge: GetItems: %w", err)
	}

	info := d.itemClientInfo()
	items := make([]*downloadclients.Item, 0, len(torrents))
	ignored := 0
	for i := range torrents {
		t := torrents[i]
		if strings.TrimSpace(t.Hash) == "" || strings.TrimSpace(t.Name) == "" {
			ignored++
			continue
		}
		items = append(items, d.torrentToItem(t, info))
	}
	if ignored > 0 {
		// Mirrors the C# `:198-202` warn line but without the
		// reconnect-state arm. The Provider's caller can still see
		// the per-torrent items it does have.
		d.log.Warn("deluge: GetItems: torrents missing hash or name were skipped",
			"count", ignored)
	}
	return items, nil
}

// torrentToItem builds the typed Item from a deluge torrent.
// Pulled into its own helper so the 5.G2d body stays readable.
func (d *Deluge) torrentToItem(t DelugeTorrent, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	ratio := t.Ratio
	ratioPtr := &ratio

	remaining := t.Size - t.BytesDownloaded
	if remaining < 0 {
		remaining = 0
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         strings.ToUpper(t.Hash),
		Category:           d.settings.TvCategory,
		Title:              t.Name,
		TotalSize:          t.Size,
		RemainingSize:      remaining,
		SeedRatio:          ratioPtr,
	}

	// ETA: deluge emits the float64 value as seconds. The C# tree
	// at `:154-160` catches OverflowException and falls back to
	// TimeSpan.MaxValue; the Go port mirrors with a max-duration
	// fallback so the UI doesn't render NaN.
	if t.Eta > 0 && !math.IsInf(t.Eta, 0) && !math.IsNaN(t.Eta) {
		secs := t.Eta
		// time.Duration ceiling is ~292 years; deluge sometimes
		// returns 8640000 ("never" sentinel) — accept up to that
		// scale, clamp anything bigger.
		const maxEtaSeconds = float64(1<<31 - 1)
		if secs > maxEtaSeconds {
			secs = maxEtaSeconds
		}
		d := time.Duration(secs * float64(time.Second))
		item.RemainingTime = &d
	}

	// OutputPath: deluge save_path + torrent name. C# `:147-148`
	// composes via OsPath operator+; the Go port uses a simple
	// '/'-separated join (deluge always emits '/' separators).
	if strings.TrimSpace(t.DownloadPath) != "" {
		item.OutputPath = ospath.New(joinTorrentPath(t.DownloadPath, t.Name))
	}

	// Status mapping per C# `:164-184`.
	switch {
	case t.State == StatusError:
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = "Deluge reported an error on this torrent"
		if strings.TrimSpace(t.Message) != "" {
			item.Message = t.Message
		}
	case t.IsFinished && t.State != StatusChecking:
		// C# `:169-172`: completed-paused torrents stay state="Paused"
		// per deluge convention; IsFinished true → Completed
		// unconditionally except mid-recheck.
		item.Status = downloadclients.DownloadItemStatusCompleted
	case t.State == StatusQueued:
		item.Status = downloadclients.DownloadItemStatusQueued
	case t.State == StatusPaused:
		item.Status = downloadclients.DownloadItemStatusPaused
	default:
		item.Status = downloadclients.DownloadItemStatusDownloading
	}

	// CanBeRemoved + CanMoveFiles per C# `:188-193`. Five-way AND:
	// RemoveCompletedDownloads (on the row) + IsAutoManaged +
	// StopAtRatio + Ratio >= StopRatio + State == "Paused".
	item.CanBeRemoved = info != nil && info.RemoveCompletedDownloads &&
		t.IsAutoManaged && t.StopAtRatio &&
		t.Ratio >= t.StopRatio && t.State == StatusPaused
	item.CanMoveFiles = item.CanBeRemoved

	return item
}

// itemClientInfo mirrors C#
// `DownloadClientItemClientInfo.FromDownloadClient` at
// `DownloadClientItemClientInfo.cs`. HasPostImportCategory is true
// when settings.TvImportedCategory is non-empty per C# `:145`.
func (d *Deluge) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol:              d.Protocol(),
		Type:                  Implementation,
		Name:                  d.Name(),
		HasPostImportCategory: strings.TrimSpace(d.settings.TvImportedCategory) != "",
	}
	if d.def != nil {
		info.ID = d.def.ID
		info.Name = d.def.Name
		info.RemoveCompletedDownloads = d.def.RemoveCompletedDownloads
	}
	return info
}

// joinTorrentPath joins deluge's save_path with the torrent name.
// Deluge always emits '/' separators in its API responses (matches
// the qBittorrent family pattern from 5.G1d).
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
func (d *Deluge) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports C# `Deluge.RemoveItem` at
// `Download/Clients/Deluge/Deluge.cs:217-220`. Lowercases the
// DownloadID (matches C# `:219` `.ToLower()`) and delegates to
// [Proxy.RemoveTorrent].
func (d *Deluge) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if item == nil {
		return errors.New("deluge: RemoveItem: nil item")
	}
	if d.proxy == nil {
		return errors.New("deluge: RemoveItem: nil proxy")
	}
	hash := strings.ToLower(item.DownloadID)
	if err := d.proxy.RemoveTorrent(ctx, hash, deleteData, d.settings); err != nil {
		return fmt.Errorf("deluge: RemoveItem(%s): %w", hash, err)
	}
	return nil
}

// GetStatus is filled in by slice 5.G2g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G2h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test ports the connectivity + auth probe from C#
// `Deluge.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs:276-323`.
//
// Flow:
//
//  1. Run Settings.Validate first — structural failures
//     short-circuit so a malformed config never triggers a network
//     round-trip.
//  2. Ensure the proxy is bound. Nil proxy is a programmer-error
//     path (non-nil error vs TestFailure) — the registry
//     constructor guarantees a non-nil proxy in production.
//  3. proxy.EnsureAuth(force=true) → field-level TestFailures pinned
//     per C# branches at `:282-319`: ErrAuthFailed → Password,
//     ErrUnavailable → Host (matches `:288-294`).
//  4. Probe daemon.info via proxy.Call → connectivity + version
//     surface. The C# TestConnection at `:280` calls
//     `_proxy.GetVersion(Settings)` which falls back from
//     daemon.get_version to daemon.info per `DelugeProxy.cs:52-65`
//     — the Go port simplifies to a direct daemon.info probe since
//     daemon.info is present in every Deluge version that ships
//     the WebUI JSON-RPC (1.3.6+).
//
// No version floor enforced — the C# tree's TestConnection doesn't
// check version numerics, just connectivity + auth. Matches.
//
// TestCategory + TestGetTorrents at C# `:325-385` are deferred —
// they require label-plugin probes that land at 5.G2h + GetItems
// at 5.G2d.
func (d *Deluge) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	if failures := d.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	if d.proxy == nil {
		return nil, errors.New("deluge: Test: nil proxy (constructor not wired)")
	}

	if _, err := d.proxy.EnsureAuth(ctx, d.settings, true); err != nil {
		if failure := classifyAuthError(err); failure != nil {
			return []downloadclients.TestFailure{*failure}, nil
		}
		return nil, err
	}

	raw, err := d.proxy.Call(ctx, "daemon.info", nil, d.settings)
	if err != nil {
		if failure := classifyAuthError(err); failure != nil {
			return []downloadclients.TestFailure{*failure}, nil
		}
		return nil, err
	}

	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse Deluge version response: %v", err),
		}}, nil
	}
	if strings.TrimSpace(version) == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: "Deluge returned an empty version string",
		}}, nil
	}

	// TestCategory + TestGetTorrents (C# :272-273) deferred to
	// the GetItems + label-plugin slices.
	return nil, nil
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed arms.
// Returns nil for other errors so the caller surfaces them as an
// error return.
//
// Pinning mirrors C# `Deluge.TestConnection` at `:282-319`:
// authentication failure → "Password", transport unavailability →
// "Host" with detailed description.
func classifyAuthError(err error) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        "Password",
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to Deluge",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// RequestAction is the per-impl action map. The Deluge C# tree has
// no concrete actions — returns (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
func (d *Deluge) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: Deluge satisfies the Provider interface.
var _ downloadclients.Provider = (*Deluge)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Transmission + Sabnzbd factory pattern from 5.G1 / sub-arc 14a/b.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("deluge: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("deluge: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G2a round-trip suite) get a nil proxy + a Provider that
	// surfaces programmer-error returns from Test(); production
	// wiring always supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("deluge: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	return New(def, settings, proxy, log), nil
}

// init registers Deluge with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
