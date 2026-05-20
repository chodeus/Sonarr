package flood

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

// Flood is the [downloadclients.Provider] implementation for the
// C# `Flood` class at
// `src/NzbDrone.Core/Download/Clients/Flood/Flood.cs:21-275`.
//
// Skeleton-only at 5.J71: Test (AuthVerify) + GetStatus
// (IsLocalhost-only — OutputRootFolders pulled into the
// GetClientSettings follow-up) + identity methods are real;
// Download / GetItems / RemoveItem / MarkItemAsImported /
// GetImportItem stub out to [ErrNotImplemented].
type Flood struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
}

// New constructs a Flood Provider. proxy=nil binds a default
// [Proxy] wrapping [http.DefaultClient]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Flood {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &Flood{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `Flood.cs:87`.
func (f *Flood) Name() string { return "Flood" }

// Protocol returns Torrent.
func (f *Flood) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (f *Flood) Definition() *downloadclients.DownloadClient { return f.def }

// Settings returns the bound Settings.
func (f *Flood) Settings() Settings { return f.settings }

// Download is a STUB at 5.J71. Follow-up J72: AddFromTorrentFile →
// `torrents/add-files` (base64-encoded body + Tags + Destination +
// StartOnAdd), AddFromMagnetLink → `torrents/add-urls` (URL list +
// Tags + Destination + StartOnAdd).
func (f *Flood) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", fmt.Errorf("%w: Download", ErrNotImplemented)
}

// GetItems is a STUB at 5.J71. Follow-up J73: `torrents` GET +
// 4-arm status table from C# `Flood.cs:144-159` (seeding/complete
// → Completed, stopped → Paused, error → Warning, downloading →
// Downloading) + seed-config cleanup gate.
func (f *Flood) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is a STUB at 5.J71. Follow-up J74 ports C#
// `Flood.cs:190-222` — calls torrents/{hash}/contents and resolves
// single-file vs base-directory OutputPath.
func (f *Flood) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a STUB at 5.J71. Follow-up J74: torrents/delete
// POST (body={hashes:[id], deleteData:bool}).
func (f *Flood) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return fmt.Errorf("%w: RemoveItem", ErrNotImplemented)
}

// MarkItemAsImported is a STUB at 5.J71. Follow-up J74 ports
// C# `Flood.cs:224-237` — when PostImportTags is non-empty, GET
// torrents → look up the matching hash → PATCH torrents/tags with
// (existing-tags ∪ PostImportTags).
func (f *Flood) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// GetStatus ports the IsLocalhost arm of C# `Flood.cs:244-258`.
// The OutputRootFolders arm (which reads `client/settings.directoryDefault`
// + applies RemotePathMapper) lands with the GetClientSettings
// follow-up. For now IsLocalhost is enough for the healthcheck path.
func (f *Flood) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(f.settings.Host)}, nil
}

// RequestAction is the C# base no-op.
func (f *Flood) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `Flood.Test(failures)` at C# `:260-274`. Runs
// Settings.Validate first, then drives the auth-verify probe.
// Auth failure surfaces as a Password-property failure (matches
// C# `:266-269`); transport failure surfaces as a Host-property
// failure (matches C# `:270-273`).
func (f *Flood) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := f.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if err := f.proxy.AuthVerify(ctx, f.settings); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "Password",
				ErrorMessage: "Failed to authenticate with Flood",
			}}, nil
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Flood: %v", err),
		}}, nil
	}
	return nil, nil
}

// isLocalHost mirrors C# `Settings.Host == "127.0.0.1" || ==
// "::1" || == "localhost"` at `Flood.cs:255`.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ErrNotImplemented signals a 5.J71 skeleton stub.
var ErrNotImplemented = errors.New("flood: not yet implemented in this build")

// Compile-time guard.
var _ downloadclients.Provider = (*Flood)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("flood: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("flood: settings unmarshal: %w", err)
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
