package rtorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the rTorrent download client. Mirrors C# class names
// `RTorrent` + `RTorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/RTorrent/RTorrent.cs:14`
// and `RTorrentSettings.cs:19`). The (Implementation, ConfigContract)
// pair is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "RTorrent"
	ConfigContract = "RTorrentSettings"
)

// RTorrent is the [downloadclients.Provider] implementation for the
// rTorrent torrent client. Ports C# `RTorrent` at
// `src/NzbDrone.Core/Download/Clients/RTorrent/RTorrent.cs:14-322`.
//
// Slice 5.J42 scope: Name / Protocol / Definition / RequestAction
// are live; Test runs the system.client_version XML-RPC probe via
// the bound proxy + minVersion floor. Download / GetItems /
// GetStatus / RemoveItem / GetImportItem / MarkItemAsImported still
// return [downloadclients.ErrNotImplementedYet]. Subsequent slices
// fill in each method.
type RTorrent struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// minVersion is the rTorrent product-version floor [Test] enforces.
// rTorrent 0.9.0 was the first release shipping the XML-RPC surface
// Sonarr's d.multicall.2 / load.start_magnet calls target. Older
// builds may work in practice but aren't supported.
var minVersion = versionPair{major: 0, minor: 9}

// New constructs an rTorrent Provider against the resolved settings
// + an optional Proxy. Used by both the registry [Constructor] and
// by tests. nil proxy → Test surfaces Settings.Validate failures
// only (matches the pre-5.J42 stub).
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *RTorrent {
	if log == nil {
		log = slog.Default()
	}
	return &RTorrent{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors the C# `RTorrent.Name`
// override at `RTorrent.cs:25-28` — returns "rTorrent" (lower-case r).
func (r *RTorrent) Name() string { return "rTorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (r *RTorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (r *RTorrent) Definition() *downloadclients.DownloadClient { return r.def }

// Settings returns the bound Settings. Exposed for tests that need
// to assert against the round-tripped JSON without re-decoding.
func (r *RTorrent) Settings() Settings { return r.settings }

// Download ports the C# Download orchestrator at `RTorrent.cs:82-118`
// (AddFromMagnetLink + AddFromTorrentFile). Picks the add path
// based on the GrabRequest shape:
//
//   - TorrentBody bytes set → AddTorrentFromFile (base64 upload).
//   - MagnetURL set → AddTorrentFromURL with the magnet link.
//   - DownloadURL set → AddTorrentFromURL with the http URL.
//
// Returns the upper-cased info-hash. Sourcing:
//
//   - req.InfoHash trusted when set (typical for rss-grabbed
//     torrents — the parser extracts it from the .torrent or
//     magnet upstream).
//   - Otherwise extracted from MagnetURL's `xt=urn:btih:HASH` query
//     parameter.
//   - Otherwise returns an error — rTorrent's load.* RPC doesn't
//     return the hash and the C# code's WaitForTorrent poll relies
//     on a pre-known hash.
//
// WaitForTorrent (C# `:91-99,107-115`) is replaced by a fire-and-
// trust posture — the orchestrator stamps the hash on the queue
// row, and the next refresh-monitored-downloads tick reconciles via
// GetItems. Same divergence as the qBittorrent port (slice 5.G1e).
//
// Per-grab priority resolution: req.IsRecent → RecentTvPriority,
// otherwise OlderTvPriority. Matches C# `:84,103`.
func (r *RTorrent) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("rtorrent: nil GrabRequest")
	}
	if r.proxy == nil {
		return "", errors.New("rtorrent: Download requires a wired Proxy")
	}
	priority := r.recencyPriority(req.IsRecent)

	hash, err := resolveInfoHash(req)
	if err != nil {
		return "", err
	}

	switch {
	case len(req.TorrentBody) > 0:
		if err := r.proxy.AddTorrentFromFile(ctx, req.ReleaseTitle, req.TorrentBody, r.settings.TvCategory, priority, r.settings.TvDirectory, r.settings); err != nil {
			return "", err
		}
	case req.MagnetURL != "":
		if err := r.proxy.AddTorrentFromURL(ctx, req.MagnetURL, r.settings.TvCategory, priority, r.settings.TvDirectory, r.settings); err != nil {
			return "", err
		}
	case req.DownloadURL != "":
		if err := r.proxy.AddTorrentFromURL(ctx, req.DownloadURL, r.settings.TvCategory, priority, r.settings.TvDirectory, r.settings); err != nil {
			return "", err
		}
	default:
		return "", errors.New("rtorrent: Download: no TorrentBody / MagnetURL / DownloadURL on GrabRequest")
	}

	return strings.ToUpper(hash), nil
}

// recencyPriority resolves the per-grab IsRecent flag to the
// Priority value the proxy applies via d.priority.set. Mirrors C#
// `:84,103`.
func (r *RTorrent) recencyPriority(isRecent bool) Priority {
	if isRecent {
		return Priority(r.settings.RecentTvPriority)
	}
	return Priority(r.settings.OlderTvPriority)
}

// resolveInfoHash returns the hex info-hash for a GrabRequest.
// Trust order: req.InfoHash → magnet xt=urn:btih: → error. The
// returned hash is lower-case (the proxy lower-cases at the wire
// boundary anyway; the Download caller upper-cases for the
// orchestrator's queue-item DownloadID).
func resolveInfoHash(req *downloadclients.GrabRequest) (string, error) {
	if h := strings.TrimSpace(req.InfoHash); h != "" {
		return strings.ToLower(h), nil
	}
	if req.MagnetURL == "" {
		return "", errors.New("rtorrent: cannot extract info-hash: neither InfoHash nor MagnetURL set")
	}
	const xtPrefix = "xt=urn:btih:"
	for _, segment := range strings.Split(req.MagnetURL, "&") {
		segment = strings.TrimPrefix(segment, "magnet:?")
		if strings.HasPrefix(strings.ToLower(segment), xtPrefix) {
			return strings.ToLower(strings.TrimSpace(segment[len(xtPrefix):])), nil
		}
	}
	return "", fmt.Errorf("rtorrent: magnet URL missing xt=urn:btih: parameter: %s", req.MagnetURL)
}

// GetItems ports `RTorrent.GetItems` at `RTorrent.cs:124-220`.
// Fires d.multicall.2 via the proxy, filters by TvCategory (when
// set), drops items with an empty / dot-prefixed base path (matches
// C# `:139-149` — rTorrent rows without an absolute download path
// can't be safely imported), and projects to
// []*downloadclients.Item.
//
// Status mapping (C# `:172-184`):
//
//   - IsFinished == true → Completed
//   - IsActive == true   → Downloading
//   - otherwise            → Paused
//
// CanMoveFiles + CanBeRemoved default to true for finished items.
// The C# tree gates these on the per-grab seed-config (ratio + time
// goals); the Go port defers that gating to the orchestrator's
// existing seed-ratio enforcement at the queue layer — documented
// divergence. The orchestrator already consults RemoveCompletedDownloads
// + the SeedConfiguration for the import-cleanup gate.
//
// RemainingTime: when DownRate > 0, ETA = RemainingSize / DownRate
// (seconds). Else nil (queued / paused / completed).
//
// SeedRatio: rTorrent stores the ratio in per-mille (1000 = 1.0);
// the Go port projects to float64 by dividing by 1000.
func (r *RTorrent) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if r.proxy == nil {
		return nil, errors.New("rtorrent: GetItems requires a wired Proxy")
	}
	torrents, err := r.proxy.GetTorrents(ctx, r.settings)
	if err != nil {
		return nil, err
	}
	out := make([]*downloadclients.Item, 0, len(torrents))
	for _, t := range torrents {
		if r.settings.TvCategory != "" && t.Category != r.settings.TvCategory {
			continue
		}
		if strings.TrimSpace(t.Path) == "" {
			r.log.Warn("rtorrent: torrent has empty download path; skipping",
				slog.String("name", t.Name), slog.String("hash", t.Hash))
			continue
		}
		if strings.HasPrefix(t.Path, ".") {
			r.log.Warn("rtorrent: torrent has relative download path; skipping",
				slog.String("name", t.Name), slog.String("path", t.Path))
			continue
		}

		item := &downloadclients.Item{
			DownloadID:    strings.ToUpper(t.Hash),
			Category:      t.Category,
			Title:         t.Name,
			TotalSize:     t.TotalSize,
			RemainingSize: t.RemainingSize,
			OutputPath:    ospath.New(t.Path),
			Status:        torrentStatus(t),
			CanMoveFiles:  true,
			CanBeRemoved:  true,
		}
		if t.DownRate > 0 && t.RemainingSize > 0 {
			etaSec := t.RemainingSize / t.DownRate
			eta := time.Duration(etaSec) * time.Second
			item.RemainingTime = &eta
		}
		// rTorrent reports the ratio in per-mille (1000 = 1.0).
		if t.IsFinished {
			ratio := float64(t.Ratio) / 1000.0
			item.SeedRatio = &ratio
		}
		out = append(out, item)
	}
	return out, nil
}

// torrentStatus maps a [Torrent] to [downloadclients.DownloadItemStatus].
// Mirrors C# `:172-184`.
func torrentStatus(t Torrent) downloadclients.DownloadItemStatus {
	switch {
	case t.IsFinished:
		return downloadclients.DownloadItemStatusCompleted
	case t.IsActive:
		return downloadclients.DownloadItemStatusDownloading
	default:
		return downloadclients.DownloadItemStatusPaused
	}
}

// GetImportItem passes through unchanged. Mirrors C#
// `DownloadClientBase.GetImportItem` default behaviour
// (`DownloadClientBase.cs:102-105`).
func (r *RTorrent) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a stub.
func (r *RTorrent) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is a stub.
func (r *RTorrent) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported applies the optional TvImportedCategory.
// Stub — depends on the XML-RPC proxy for the
// `d.custom1.set` (label change) call.
func (r *RTorrent) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test runs the per-client connection self-check. Mirrors C#
// `DownloadClientBase.Test()` at `:148-163`: validates the Settings
// first, then (when no failures and a proxy is wired) probes
// `system.client_version` and gates on [minVersion].
func (r *RTorrent) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := r.settings.Validate()
	if len(failures) > 0 || r.proxy == nil {
		return failures, nil
	}
	version, err := r.proxy.GetVersion(ctx, r.settings)
	if err != nil {
		// Network / auth / fault failures surface as a Host-attached
		// connection failure (the C# tree's
		// `_logger.Error(ex, ...) + return new ValidationFailure("Host", ...)`
		// shape at `RTorrent.cs:280-300`).
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to rTorrent: %v", err),
		}}, nil
	}
	if !versionAtLeast(version, minVersion) {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("rTorrent version %s is too old; minimum supported is %d.%d", version, minVersion.major, minVersion.minor),
		}}, nil
	}
	return nil, nil
}

// RequestAction returns (nil, nil) for every action — the default
// C# `DownloadClientBase.RequestAction` shape at `:165-167`.
// rTorrent has no Settings-class actions exposed today (no
// "getCategories"-equivalent — rTorrent labels are free-form text
// the user types).
func (r *RTorrent) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: RTorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*RTorrent)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Transmission factory pattern. When an HTTPDoer is present in
// `deps.HTTPClient` the constructor builds the XML-RPC Proxy; when
// not (e.g. round-trip tests without a wired client), Test surfaces
// only Settings.Validate failures.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("rtorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("rtorrent: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("rtorrent: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		proxy = NewProxy(doer, log)
	}
	return New(def, settings, proxy, log), nil
}

// init registers rTorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
