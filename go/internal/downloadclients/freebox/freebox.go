package freebox

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

// TorrentFreeboxDownload is the [downloadclients.Provider]
// implementation for the C# `TorrentFreeboxDownload` class at
// `src/NzbDrone.Core/Download/Clients/FreeboxDownload/TorrentFreeboxDownload.cs:1-232`.
//
// Skeleton-only at 5.J79: Test (auth probe via /login → HMAC-SHA1
// session-token exchange) + GetStatus (IsLocalhost-only) + identity
// methods are real; Download / GetItems / RemoveItem stub out to
// [ErrNotImplemented] until the follow-up slices port the
// /downloads endpoints.
type TorrentFreeboxDownload struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
}

// New constructs a TorrentFreeboxDownload Provider. proxy=nil binds
// a default [Proxy]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *TorrentFreeboxDownload {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &TorrentFreeboxDownload{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `TorrentFreeboxDownload.cs`
// returns `"Freebox Download"`.
func (f *TorrentFreeboxDownload) Name() string { return "Freebox Download" }

// Protocol returns Torrent.
func (f *TorrentFreeboxDownload) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (f *TorrentFreeboxDownload) Definition() *downloadclients.DownloadClient { return f.def }

// Settings returns the bound Settings.
func (f *TorrentFreeboxDownload) Settings() Settings { return f.settings }

// Download is a STUB at 5.J79. Follow-up ports
// [Proxy.AddTaskFromURL] / [Proxy.AddTaskFromFile] —
// /downloads/add POST with download_url or download_file form-
// field, optional download_dir, plus the follow-up
// SetTorrentSettings PUT for status=stopped / queue_pos /
// stop_ratio.
func (f *TorrentFreeboxDownload) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", fmt.Errorf("%w: Download", ErrNotImplemented)
}

// GetItems is a STUB at 5.J79. Follow-up ports
// [Proxy.GetTasks] — /downloads/ GET + the 9-arm status table at
// C# `TorrentFreeboxDownload.cs:70-130`.
func (f *TorrentFreeboxDownload) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough.
func (f *TorrentFreeboxDownload) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a STUB at 5.J79. Follow-up ports
// [Proxy.DeleteTask] — /downloads/<id> DELETE (with /erase suffix
// when deleteData=true).
func (f *TorrentFreeboxDownload) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return fmt.Errorf("%w: RemoveItem", ErrNotImplemented)
}

// GetStatus is IsLocalhost-only at 5.J79. The C# version reads
// /downloads/config/ for the OutputRootFolders arm; the
// GetDownloadConfiguration follow-up lands with the Provider
// wiring slice.
func (f *TorrentFreeboxDownload) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(f.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (f *TorrentFreeboxDownload) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (f *TorrentFreeboxDownload) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `TorrentFreeboxDownload.Test(failures)` (C#
// `:170-185`). Runs Settings.Validate, then drives the full
// session-token flow via [Proxy.GetSessionToken] — this exercises
// /login (challenge fetch) AND /login/session (HMAC-SHA1
// exchange), proving the AppId + AppToken combo works end-to-end.
//
// ErrAuth surfaces as a Password-property failure on AppToken;
// ErrAPI (404) as ApiUrl; ErrUnavailable as Host; ErrRPC as a
// generic field-empty failure carrying the decoded error message.
func (f *TorrentFreeboxDownload) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := f.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if _, err := f.proxy.GetSessionToken(ctx, f.settings, true); err != nil {
		switch {
		case errors.Is(err, ErrAuth):
			return []downloadclients.TestFailure{{
				PropertyName: "AppToken",
				ErrorMessage: fmt.Sprintf("Freebox authentication failed: %v", err),
			}}, nil
		case errors.Is(err, ErrAPI):
			return []downloadclients.TestFailure{{
				PropertyName: "ApiUrl",
				ErrorMessage: fmt.Sprintf("Freebox API not found at the configured URL: %v", err),
			}}, nil
		case errors.Is(err, ErrUnavailable):
			return []downloadclients.TestFailure{{
				PropertyName: "Host",
				ErrorMessage: fmt.Sprintf("Unable to reach Freebox: %v", err),
			}}, nil
		default:
			return []downloadclients.TestFailure{{
				PropertyName: "Host",
				ErrorMessage: fmt.Sprintf("Freebox API error: %v", err),
			}}, nil
		}
	}
	return nil, nil
}

// isLocalHost mirrors the C# `Settings.Host == "127.0.0.1" || ==
// "localhost"` check. mafreebox.freebox.fr resolves locally on a
// Freebox-LAN but isn't `localhost` per se; the check stays exact-
// match for parity.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ErrNotImplemented signals a 5.J79 skeleton stub.
var ErrNotImplemented = errors.New("freebox: not yet implemented in this build")

// Compile-time guard.
var _ downloadclients.Provider = (*TorrentFreeboxDownload)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("freebox: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("freebox: settings unmarshal: %w", err)
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
