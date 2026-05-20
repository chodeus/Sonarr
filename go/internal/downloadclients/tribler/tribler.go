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

// Download is a STUB at 5.J81. Follow-up ports
// `Proxy.AddFromMagnetLink` (PUT `/api/downloads` with the magnet
// URL + anonymity_hops + safe_seeding + destination payload) per
// C# `TriblerDownloadClientProxy.cs:108-117`.
func (t *TriblerDownloadClient) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", fmt.Errorf("%w: Download", ErrNotImplemented)
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

// ErrNotImplemented signals a 5.J81 skeleton stub.
var ErrNotImplemented = errors.New("tribler: not yet implemented in this build")

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
