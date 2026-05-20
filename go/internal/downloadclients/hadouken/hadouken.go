package hadouken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
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

	// rpm is the optional remote-path mapper. Production binds the
	// shared service via [downloadclients.RemotePathMapper]; nil →
	// the GetItems output uses the Hadouken-side SavePath verbatim
	// (preserves pre-Phase-6.B behaviour for unmapped setups).
	rpm downloadclients.RemotePathMapper
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

// GetItems ports `Hadouken.GetItems` at C# `Hadouken.cs:39-104`.
// Walks every torrent the Hadouken endpoint reports via
// [Proxy.GetTorrents], applies the Settings.Category label gate,
// composes the output path (RemotePathMapper-translated SavePath +
// torrent.Name), and projects each torrent into a
// [downloadclients.Item] with the status table at C# `:73-93`.
//
// Status table (priority order, first match wins):
//
//   - Error non-empty → Warning + Message=Error
//   - IsFinished AND State != CheckingFiles → Completed
//   - State == QueuedForChecking → Queued
//   - State == Paused → Paused
//   - default → Downloading
//
// CanMoveFiles + CanBeRemoved are both true ONLY when
// `RemoveCompletedDownloads AND IsFinished AND State == Paused`
// (matches C# `:95-98` — Hadouken treats Paused-after-finish as the
// "safe to clean up" signal).
func (h *Hadouken) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if h.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	torrents, err := h.proxy.GetTorrents(ctx, h.settings)
	if err != nil {
		return nil, fmt.Errorf("hadouken: GetTorrents: %w", err)
	}
	info := h.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(torrents))
	category := strings.TrimSpace(h.settings.Category)
	for _, t := range torrents {
		if category != "" && t.Label != category {
			continue
		}
		out = append(out, h.toItem(ctx, info, t))
	}
	return out, nil
}

// toItem projects one [Torrent] into a [downloadclients.Item].
// Extracted so the GetItems loop stays terse + tests can drive
// individual rows.
func (h *Hadouken) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, t *Torrent) *downloadclients.Item {
	outputPath := ospath.New(t.SavePath)
	if h.rpm != nil && strings.TrimSpace(t.SavePath) != "" {
		if mapped, err := h.rpm.RemapRemoteToLocal(ctx, h.settings.Host, outputPath); err == nil {
			outputPath = mapped
		} else {
			h.log.Debug("hadouken: RemapRemoteToLocal failed", "savePath", t.SavePath, "err", err)
		}
	}
	if strings.TrimSpace(t.Name) != "" {
		outputPath = ospath.New(filepath.Join(outputPath.FullPath(), t.Name))
	}

	var eta time.Duration
	if t.DownloadRate > 0 && t.TotalSize > 0 {
		eta = time.Duration(float64(t.TotalSize)/float64(t.DownloadRate)) * time.Second
	}
	remaining := eta // RemainingTime in C# is TimeSpan; eta defaults to zero already.

	var seedRatio *float64
	if t.DownloadedBytes > 0 {
		ratio := float64(t.UploadedBytes) / float64(t.DownloadedBytes)
		seedRatio = &ratio
	} else {
		zero := 0.0
		seedRatio = &zero
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         strings.ToUpper(t.InfoHash),
		OutputPath:         outputPath,
		RemainingSize:      t.TotalSize - t.DownloadedBytes,
		RemainingTime:      &remaining,
		Title:              t.Name,
		TotalSize:          t.TotalSize,
		SeedRatio:          seedRatio,
	}

	switch {
	case strings.TrimSpace(t.Error) != "":
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = t.Error
	case t.IsFinished && t.State != TorrentStateCheckingFiles:
		item.Status = downloadclients.DownloadItemStatusCompleted
	case t.State == TorrentStateQueuedForChecking:
		item.Status = downloadclients.DownloadItemStatusQueued
	case t.State == TorrentStatePaused:
		item.Status = downloadclients.DownloadItemStatusPaused
	default:
		item.Status = downloadclients.DownloadItemStatusDownloading
	}

	canCleanup := info != nil && info.RemoveCompletedDownloads &&
		t.IsFinished && t.State == TorrentStatePaused
	item.CanMoveFiles = canCleanup
	item.CanBeRemoved = canCleanup
	return item
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (h *Hadouken) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: h.Protocol(),
		Type:     Implementation,
		Name:     h.Name(),
	}
	if h.def != nil {
		info.ID = h.def.ID
		info.Name = h.def.Name
		info.RemoveCompletedDownloads = h.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional [downloadclients.RemotePathMapper]
// that GetItems consults to translate Hadouken-side SavePath into
// Sonarr-side paths. Mirrors the Transmission/qBittorrent
// SetRemotePathMapper hook the registry constructor uses at
// Phase 6.B wiring time.
func (h *Hadouken) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	h.rpm = rpm
}

// GetImportItem is the C# base passthrough.
func (h *Hadouken) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `Hadouken.RemoveItem` at C# `Hadouken.cs:106-116`.
// Dispatches to [Proxy.RemoveTorrentAndData] when deleteData=true,
// otherwise [Proxy.RemoveTorrent]. The download-id is sent as the
// uppercased infohash the queue layer holds.
func (h *Hadouken) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if h.proxy == nil {
		return errors.New("hadouken: nil Proxy")
	}
	if item == nil {
		return errors.New("hadouken: nil item")
	}
	id := strings.TrimSpace(item.DownloadID)
	if id == "" {
		return errors.New("hadouken: item has empty DownloadID")
	}
	if deleteData {
		if err := h.proxy.RemoveTorrentAndData(ctx, h.settings, id); err != nil {
			return fmt.Errorf("hadouken: RemoveTorrentAndData(%s): %w", id, err)
		}
		return nil
	}
	if err := h.proxy.RemoveTorrent(ctx, h.settings, id); err != nil {
		return fmt.Errorf("hadouken: RemoveTorrent(%s): %w", id, err)
	}
	return nil
}

// GetStatus ports `Hadouken.GetStatus` at C# `Hadouken.cs:118-134`.
// Calls `webui.getSettings`, reads `bittorrent.defaultSavePath`,
// and populates OutputRootFolders with the RemotePathMapper-
// translated path. IsLocalhost is keyed off the configured Host.
//
// Config probe failures are NOT fatal — the GetStatus call still
// returns a populated [downloadclients.ClientInfo] with
// IsLocalhost set and OutputRootFolders empty so the healthcheck
// loop can still report on the configured host (mirrors the C#
// behaviour where ProcessRequest throws → bubbles up, but Sonarr's
// queue probe catches and surfaces as a warning rather than
// blanking the entire client).
func (h *Hadouken) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	info := &downloadclients.ClientInfo{IsLocalhost: isLocalHost(h.settings.Host)}
	if h.proxy == nil {
		return info, nil
	}
	cfg, err := h.proxy.GetConfig(ctx, h.settings)
	if err != nil {
		h.log.Debug("hadouken: GetConfig failed", "err", err)
		return info, nil
	}
	destDir, _ := cfg["bittorrent.defaultSavePath"].(string)
	destDir = strings.TrimSpace(destDir)
	if destDir == "" {
		return info, nil
	}
	remote := ospath.New(destDir)
	if h.rpm != nil {
		if mapped, mapErr := h.rpm.RemapRemoteToLocal(ctx, h.settings.Host, remote); mapErr == nil {
			remote = mapped
		} else {
			h.log.Debug("hadouken: GetStatus RemapRemoteToLocal failed", "savePath", destDir, "err", mapErr)
		}
	}
	info.OutputRootFolders = append(info.OutputRootFolders, remote)
	return info, nil
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
// `core.getSystemInfo`), then (slice 5.J106) TestGetTorrents
// (one-shot list call to confirm the API surface is reachable
// post-auth). Mirrors the J101/J102/J104/J105 pattern of additive
// Test-arm probes shared across the major DCs.
//
// HasErrors guard at C# `:138-143`: TestConnection failures short-
// circuit the follow-on TestGetTorrents — a broken connection
// shouldn't dispatch a second probe that's guaranteed to fail with
// the same root cause.
func (h *Hadouken) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := h.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, h.testConnection(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	if f := h.testGetTorrents(ctx); f != nil {
		failures = append(failures, *f)
	}
	return failures, nil
}

// testGetTorrents ports C# `TestGetTorrents` at `Hadouken.cs:191-204`.
// One probe call against the live daemon; any error surfaces a
// whole-form failure containing the cause.
func (h *Hadouken) testGetTorrents(ctx context.Context) *downloadclients.TestFailure {
	if _, err := h.proxy.GetTorrents(ctx, h.settings); err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Failed to get the list of torrents: %v", err),
			DetailedDescription: "Sonarr was able to authenticate against Hadouken but the webui.list RPC failed; check the Hadouken log.",
		}
	}
	return nil
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
	h := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		h.SetRemotePathMapper(rpm)
	}
	return h, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
