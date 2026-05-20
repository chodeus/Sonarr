package hadouken

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

// Hadouken is the [downloadclients.Provider] implementation for the
// C# `Hadouken` class at
// `src/NzbDrone.Core/Download/Clients/Hadouken/Hadouken.cs:19-206`.
//
// Skeleton-only at 5.J62: Test + GetStatus + Definition + identity
// methods are real; Download / GetItems / RemoveItem stub out to
// [ErrNotImplemented] until the per-method follow-up slices port the
// concrete proxy calls.
type Hadouken struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
}

// New constructs a Hadouken Provider. proxy=nil binds a default
// [Proxy] wrapping [http.DefaultClient]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Hadouken {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &Hadouken{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `:37`.
func (h *Hadouken) Name() string { return "Hadouken" }

// Protocol returns Torrent. Hadouken inherits from
// `TorrentClientBase`.
func (h *Hadouken) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (h *Hadouken) Definition() *downloadclients.DownloadClient { return h.def }

// Settings returns the bound Settings.
func (h *Hadouken) Settings() Settings { return h.settings }

// Download ports `Hadouken.AddFromTorrentFile` + `AddFromMagnetLink`
// at C# `Hadouken.cs:147-157`. Dispatches:
//
//   - TorrentBody non-empty → [Proxy.AddTorrentFile] (base64-encoded
//     bytes + Settings.Category label). The C# return value is the
//     server response uppercased — when the response is empty (some
//     Hadouken versions reply with just `true`), the Go port falls
//     back to req.InfoHash so the queue join key is still populated.
//   - MagnetURL non-empty → [Proxy.AddTorrentUri] (magnet + label).
//     The C# code discards the server response and returns the
//     pre-computed hash uppercased; the Go port mirrors — using
//     req.InfoHash.
//
// Returns an error when both fields are empty or when the
// resolved infohash is blank (no orchestrator-side pre-parse
// happened). InfoHash is uppercased before return to match the
// C# `.ToUpper()` calls.
func (h *Hadouken) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("hadouken: nil GrabRequest")
	}
	if h.proxy == nil {
		return "", errors.New("hadouken: nil Proxy")
	}

	switch {
	case len(req.TorrentBody) > 0:
		resp, err := h.proxy.AddTorrentFile(ctx, h.settings, req.TorrentBody)
		if err != nil {
			return "", fmt.Errorf("hadouken: AddTorrentFile(%q): %w", req.ReleaseTitle, err)
		}
		id := strings.TrimSpace(resp)
		if id == "" {
			id = req.InfoHash
		}
		if id == "" {
			return "", errors.New("hadouken: AddTorrentFile returned empty response and req.InfoHash is unset")
		}
		return strings.ToUpper(id), nil

	case strings.TrimSpace(req.MagnetURL) != "":
		if err := h.proxy.AddTorrentUri(ctx, h.settings, req.MagnetURL); err != nil {
			return "", fmt.Errorf("hadouken: AddTorrentUri(%q): %w", req.ReleaseTitle, err)
		}
		if strings.TrimSpace(req.InfoHash) == "" {
			return "", errors.New("hadouken: AddTorrentUri requires req.InfoHash to compose the DownloadID")
		}
		return strings.ToUpper(req.InfoHash), nil

	default:
		return "", errors.New("hadouken: GrabRequest has neither TorrentBody nor MagnetURL")
	}
}

// GetItems is a STUB at 5.J62. Follow-up slice ports
// [Proxy.GetTorrents] + state-table conversion + Category filter.
func (h *Hadouken) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough.
func (h *Hadouken) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a STUB at 5.J62. Follow-up slice ports
// [Proxy.RemoveTorrent] / [Proxy.RemoveTorrentAndData] →
// `webui.perform` with action="remove" / "removedata".
func (h *Hadouken) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return fmt.Errorf("%w: RemoveItem", ErrNotImplemented)
}

// GetStatus ports the IsLocalhost arm of C# `:118-134`. The
// OutputRootFolders arm (which calls `webui.getSettings` and reads
// `bittorrent.defaultSavePath`) is deferred to the GetItems
// follow-up — IsLocalhost on its own is enough for the
// remote-path-mapping healthcheck path at this commit.
func (h *Hadouken) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(h.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default — Hadouken has no
// post-import-category concept.
func (h *Hadouken) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (h *Hadouken) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `Hadouken.Test(failures)` at C# `:136-145`. Runs
// Settings.Validate first, then TestConnection (version-gate via
// `core.getSystemInfo`). The C# code also runs TestGetTorrents
// after the version check — deferred to the GetItems follow-up.
func (h *Hadouken) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := h.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, h.testConnection(ctx)...)
	return failures, nil
}

// testConnection ports C# `TestConnection` at `:159-189`. Calls
// `core.getSystemInfo`, parses Versions["hadouken"], rejects
// anything below [minVersion]. Auth failure surfaces as a
// Password-property failure; transport failure surfaces as a
// Host-property failure.
func (h *Hadouken) testConnection(ctx context.Context) []downloadclients.TestFailure {
	info, err := h.proxy.GetSystemInfo(ctx, h.settings)
	if err != nil {
		switch {
		case errors.Is(err, ErrAuth):
			return []downloadclients.TestFailure{{
				PropertyName: "Password",
				ErrorMessage: "Authentication failure",
			}}
		default:
			return []downloadclients.TestFailure{{
				PropertyName: "Host",
				ErrorMessage: fmt.Sprintf("Unable to connect to Hadouken: %v", err),
			}}
		}
	}
	v := strings.TrimSpace(info.Versions["hadouken"])
	if v == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: "Hadouken did not report a version string",
		}}
	}
	if compareVersion(v, minVersion) < 0 {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Hadouken %s is below the required %s", v, minVersion),
		}}
	}
	return nil
}

// isLocalHost mirrors C# `Settings.Host == "127.0.0.1" || Settings.Host == "localhost"`
// at `:125`.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// compareVersion compares two dotted-numeric version strings.
// Returns -1, 0, +1. Non-numeric segments compare as 0. Mirrors
// the C# `Version` ctor's lex-compare semantic at
// `Hadouken.cs:164,166`. Truncates to at most 4 components.
func compareVersion(a, b string) int {
	ai := splitVersion(a)
	bi := splitVersion(b)
	n := len(ai)
	if len(bi) > n {
		n = len(bi)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(ai) {
			av = ai[i]
		}
		if i < len(bi) {
			bv = bi[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// splitVersion parses a dotted-numeric string into its component
// integers. Non-numeric segments parse as 0. Up to 4 components.
func splitVersion(v string) []int {
	parts := strings.SplitN(v, ".", 4)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, r := range strings.TrimSpace(p) {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}

// ErrNotImplemented signals that a Provider method is a 5.J62
// skeleton stub. Follow-up slices replace each call site with the
// concrete proxy call.
var ErrNotImplemented = errors.New("hadouken: not yet implemented in this build")

// Compile-time guard.
var _ downloadclients.Provider = (*Hadouken)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("hadouken: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("hadouken: settings unmarshal: %w", err)
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
