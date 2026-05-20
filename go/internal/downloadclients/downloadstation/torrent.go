package downloadstation

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

// TorrentDownloadStation is the [downloadclients.Provider]
// implementation for the C# `TorrentDownloadStation` class at
// `src/NzbDrone.Core/Download/Clients/DownloadStation/TorrentDownloadStation.cs:1-466`.
//
// Skeleton-only at 5.J75: Test (auth probe via SYNO.API.Auth
// login) + GetStatus (IsLocalhost-only) + identity methods are
// real; Download / GetItems / RemoveItem stub out to
// [ErrNotImplemented] until the V1/V2 task-proxy follow-ups land.
type TorrentDownloadStation struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
}

// New constructs a TorrentDownloadStation Provider. proxy=nil
// binds a default [Proxy]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *TorrentDownloadStation {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log, nil)
	}
	return &TorrentDownloadStation{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `:53`.
func (t *TorrentDownloadStation) Name() string { return "Download Station" }

// Protocol returns Torrent. The Usenet half (UsenetDownloadStation)
// lives in a separate Provider type with the same proxy surface.
func (t *TorrentDownloadStation) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (t *TorrentDownloadStation) Definition() *downloadclients.DownloadClient { return t.def }

// Settings returns the bound Settings.
func (t *TorrentDownloadStation) Settings() Settings { return t.settings }

// Download is a STUB at 5.J75. Follow-up J76: AddFromTorrentFile +
// AddFromMagnetLink via DownloadStationTaskProxyV1.AddTask
// (multipart upload to SYNO.DownloadStation.Task create method).
func (t *TorrentDownloadStation) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", fmt.Errorf("%w: Download", ErrNotImplemented)
}

// GetItems is a STUB at 5.J75. Follow-up J76: SYNO.DownloadStation.Task
// list method + the 13-arm status table from C# `:243-291` + the
// SharedFolderResolver translation in OutputPath.
func (t *TorrentDownloadStation) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough.
func (t *TorrentDownloadStation) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a STUB at 5.J75. Follow-up J76: SYNO.DownloadStation.Task
// delete method (forces force_complete=true matching C# behaviour
// for completed downloads).
func (t *TorrentDownloadStation) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return fmt.Errorf("%w: RemoveItem", ErrNotImplemented)
}

// GetStatus ports the IsLocalhost arm of C# `:392-417`. The
// OutputRootFolders arm (which calls DSMInfoProxy.GetSerialNumber +
// SharedFolderResolver) is deferred to the SharedFolderResolver
// follow-up — IsLocalhost suffices for the healthcheck path.
func (t *TorrentDownloadStation) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(t.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (t *TorrentDownloadStation) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (t *TorrentDownloadStation) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports the connection + auth arm of C# `Test(failures)` at
// `:206-216`. Runs Settings.Validate first, then drives the
// auth-probe via [Proxy.Authenticate].
//
// The C# version also runs `TestOutputPath` (validates the
// configured TvDirectory against SharedFolderResolver) and
// `TestGetTorrents` (lists tasks to verify the API surface is
// reachable post-login). Both are deferred: TestOutputPath
// requires the SharedFolderResolver port (5.J79), TestGetTorrents
// requires the V1/V2 task proxy (5.J76).
func (t *TorrentDownloadStation) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := t.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if _, err := t.proxy.Authenticate(ctx, t.settings, true); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "Username",
				ErrorMessage: fmt.Sprintf("Authentication failed: %v", err),
			}}, nil
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Diskstation: %v", err),
		}}, nil
	}
	return nil, nil
}

// isLocalHost ports the C# `Settings.Host == "127.0.0.1" || ==
// "::1" || == "localhost"` check.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ErrNotImplemented signals a 5.J75 skeleton stub.
var ErrNotImplemented = errors.New("downloadstation: not yet implemented in this build")

// Compile-time guard.
var _ downloadclients.Provider = (*TorrentDownloadStation)(nil)

// constructor is the registry factory for the Torrent half.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("downloadstation: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("downloadstation: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, nil, log), nil
}

func init() {
	downloadclients.Default().MustRegister(TorrentImplementation, ConfigContract, constructor)
}
