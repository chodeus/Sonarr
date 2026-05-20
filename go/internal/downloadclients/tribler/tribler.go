package tribler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// TriblerDownloadClient is the [downloadclients.Provider]
// implementation for the C# `TriblerDownloadClient` class at
// `src/NzbDrone.Core/Download/Clients/Tribler/TriblerDownloadClient.cs:1-298`.
//
// Skeleton-only at 5.J81: Test (auth probe via /api/settings) +
// GetStatus (IsLocalhost-only) + identity methods are real;
// Download / GetItems / RemoveItem stub out to [ErrNotImplemented]
// until the follow-up slices port the /api/downloads endpoints.
type TriblerDownloadClient struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
}

// New constructs a TriblerDownloadClient Provider. proxy=nil binds
// a default [Proxy]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *TriblerDownloadClient {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &TriblerDownloadClient{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns "Tribler" matching C# `:41`.
func (t *TriblerDownloadClient) Name() string { return "Tribler" }

// Protocol returns Torrent.
func (t *TriblerDownloadClient) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (t *TriblerDownloadClient) Definition() *downloadclients.DownloadClient { return t.def }

// Settings returns the bound Settings.
func (t *TriblerDownloadClient) Settings() Settings { return t.settings }

// Download ports C# `TriblerDownloadClient.AddFromMagnetLink` at
// `:221-232`. PUTs `/api/downloads` with the magnet URL, the
// configured anonymity-hops + safe-seeding flags, and a resolved
// destination. Tribler 8.x does not support adding via a .torrent
// file (the C# `AddFromTorrentFile` at `:234-238` throws
// NotSupportedException) — the Go port mirrors that: TorrentBody
// requests return [ErrTorrentFileUnsupported].
//
// Destination resolution mirrors C# `GetDownloadDirectory` at
// `:250-266`: TvDirectory wins when non-empty; else when
// TvCategory is set, [Proxy.GetConfig].Settings.LibTorrent.DownloadDefaults.SaveAs
// + "/" + TvCategory; else empty (Tribler uses its own default).
func (t *TriblerDownloadClient) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("tribler: nil GrabRequest")
	}
	if t.proxy == nil {
		return "", errors.New("tribler: nil Proxy")
	}
	if len(req.TorrentBody) > 0 {
		return "", ErrTorrentFileUnsupported
	}
	magnet := strings.TrimSpace(req.MagnetURL)
	if magnet == "" {
		return "", errors.New("tribler: GrabRequest has neither MagnetURL nor InfoHash (Tribler is magnet-only)")
	}

	dest, err := t.resolveDownloadDirectory(ctx)
	if err != nil {
		return "", err
	}

	hops := int64(t.settings.AnonymityLevel)
	safe := t.settings.SafeSeeding
	hash, err := t.proxy.AddFromMagnetLink(ctx, t.settings, AddDownloadRequest{
		AnonymityHops: &hops,
		SafeSeeding:   &safe,
		Destination:   dest,
		URI:           magnet,
	})
	if err != nil {
		return "", fmt.Errorf("tribler: AddFromMagnetLink: %w", err)
	}
	return hash, nil
}

// resolveDownloadDirectory ports C# `GetDownloadDirectory` at
// `:250-266`. Returns empty when neither TvDirectory nor
// TvCategory is set (Tribler falls back to its own default).
func (t *TriblerDownloadClient) resolveDownloadDirectory(ctx context.Context) (string, error) {
	if dir := strings.TrimSpace(t.settings.TvDirectory); dir != "" {
		return dir, nil
	}
	if strings.TrimSpace(t.settings.TvCategory) == "" {
		return "", nil
	}
	cfg, err := t.proxy.GetConfig(ctx, t.settings)
	if err != nil {
		return "", fmt.Errorf("tribler: resolve download directory: %w", err)
	}
	base := strings.TrimRight(cfg.Settings.LibTorrent.DownloadDefaults.SaveAs, "/")
	if base == "" {
		return "", nil
	}
	return base + "/" + t.settings.TvCategory, nil
}

// GetItems is a STUB at 5.J81. Follow-up ports `Proxy.GetDownloads`
// (GET `/api/downloads`) + the per-state Tribler status table
// from C# `TriblerDownloadClient.cs:47-156`.
func (t *TriblerDownloadClient) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough.
func (t *TriblerDownloadClient) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a STUB at 5.J81. Follow-up ports
// `Proxy.RemoveDownload` (DELETE `/api/downloads/<hash>` with body
// `{remove_data:bool}`) per C# `TriblerDownloadClientProxy.cs:92-106`.
func (t *TriblerDownloadClient) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return fmt.Errorf("%w: RemoveItem", ErrNotImplemented)
}

// GetStatus ports the IsLocalhost arm of C# `:163`. The
// OutputRootFolders arm (which reads `settings.libtorrent.download_defaults.saveas`)
// is deferred to the GetDownloadDirectory follow-up.
func (t *TriblerDownloadClient) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(t.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (t *TriblerDownloadClient) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (t *TriblerDownloadClient) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `TriblerDownloadClient.Test(failures)` at C# `:240-248`.
// Runs Settings.Validate first, then drives `Proxy.GetConfig` as
// the connectivity + auth probe — a successful GET `/api/settings`
// proves both the endpoint is reachable AND the X-Api-Key header
// was accepted.
//
// The C# Test arm at `:268-296` maps DownloadClientAuthenticationException
// to an ApiKey-property failure and DownloadClientUnavailableException
// to Host. The Go port mirrors via the ErrAuth / ErrUnavailable
// sentinels.
func (t *TriblerDownloadClient) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := t.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if _, err := t.proxy.GetConfig(ctx, t.settings); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "ApiKey",
				ErrorMessage: fmt.Sprintf("API Key is incorrect: %v", err),
			}}, nil
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Tribler: %v", err),
		}}, nil
	}
	return nil, nil
}

// isLocalHost mirrors the C# `Settings.Host == "127.0.0.1" || ==
// "localhost"` check.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ErrNotImplemented signals a 5.J81 skeleton stub (GetItems +
// RemoveItem). Removed once those arms wire up.
var ErrNotImplemented = errors.New("tribler: not yet implemented in this build")

// ErrTorrentFileUnsupported mirrors C# `:237`'s NotSupportedException
// — Tribler 8.x does not support adding via a .torrent file
// (only magnet links).
var ErrTorrentFileUnsupported = errors.New("tribler: torrent files are not supported; only magnet links")

// Compile-time guard.
var _ downloadclients.Provider = (*TriblerDownloadClient)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("tribler: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("tribler: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, nil, log), nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
