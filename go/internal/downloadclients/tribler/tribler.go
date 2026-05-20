package tribler

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

// TriblerDownloadClient is the [downloadclients.Provider]
// implementation for the C# `TriblerDownloadClient` class at
// `src/NzbDrone.Core/Download/Clients/Tribler/TriblerDownloadClient.cs:1-298`.
//
// Functionally complete after the 5.J81 → 5.J89 arc: Test runs
// against /api/settings (auth probe); Download dispatches via PUT
// /api/downloads (magnet-only — torrent files surface
// ErrTorrentFileUnsupported); GetItems walks /api/downloads with
// the 6-arm status table + ETA clamp + OutputPath resolution
// (single-file suffix from /api/downloads/<hash>/files); RemoveItem
// fires DELETE /api/downloads/<hash>; GetStatus reads SaveAs from
// /api/settings + appends `/.<TvCategory>` (note leading dot — see
// the GetStatus doc comment) + RemotePathMapper translation.
// J96 wires the HasReachedSeedLimit cleanup-gate via the extended
// Config (SeedingMode/SeedingRatio/SeedingTime now surfaced from
// /api/settings). Tribler's gate diverges from RQBit (J94) / Flood
// (J95): it consumes Tribler's OWN per-client config rather than
// the per-grab IDownloadSeedConfigProvider, so the J91/J92/J93 chain
// is NOT plumbed through this DC.
type TriblerDownloadClient struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy

	// rpm is the optional remote-path mapper consulted by
	// GetItems to translate the Tribler-side Destination into
	// Sonarr-side paths.
	rpm downloadclients.RemotePathMapper

	// now is the injectable clock the SeedingModeTime gate reads.
	// Defaults to time.Now; tests pin a fixed time to step over
	// the TimeAdded + SeedingTime predicate.
	now func() time.Time
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
	return &TriblerDownloadClient{settings: settings, def: def, log: log, proxy: proxy, now: time.Now}
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

// GetItems ports `TriblerDownloadClient.GetItems` at C# `:47-156`.
// Walks the download list via [Proxy.GetDownloads], skips magnet-
// metadata torrents (Size==nil or 0 at C# `:58`), and projects each
// into a [downloadclients.Item] with the 5-arm status table.
//
// OutputPath is composed as `<Destination>/<filename>` where the
// suffix is the single file's Name when GetDownloadFiles returns
// exactly one (matches C# `:74-83`), otherwise the torrent Title.
// The bound [RemotePathMapper] (when present) translates the
// resulting path Sonarr-side.
//
// Status table priority order (matches C# `:110-148`):
//
//   - Hashchecking / Waiting4HashCheck / Circuits / Exitnodes /
//     Downloading → Downloading.
//   - Metadata / AllocatingDiskspace → Queued.
//   - Seeding / Stopped → Completed.
//   - StoppedOnError → Failed.
//   - Loading / unknown → Downloading + "Unknown download state"
//     message (matches C# `:131-135`).
//   - Override: Stopped AND Progress < 1 → Paused (matches C# `:139`).
//   - Override: non-empty Error → Warning + Message=Error
//     (matches C# `:144-148`).
//
// ETA clamping mirrors C# `:91-102`: negative → 0, >= 365 days →
// 365 days, otherwise direct conversion.
//
// Cleanup-gate wired at slice 5.J96 via [TriblerDownloadClient.applyCleanupGate]
// which ports C# `HasReachedSeedLimit` at `:180-215`. Tribler's gate
// consults its OWN /api/settings response (SeedingMode +
// SeedingRatio + SeedingTime) — NOT the per-grab
// IDownloadSeedConfigProvider — so J91/J92/J93 do not plumb through
// this DC. The Config is fetched ONCE per GetItems call (matches C#
// `:49` `configAsync = _proxy.GetConfig(Settings)`) and shared
// across the per-torrent gate evaluations.
//
// Errors from GetConfig are logged at Debug and the gate degrades
// to "never satisfy" — preserves the pre-J96 false-default posture
// when the config endpoint is unreachable.
func (t *TriblerDownloadClient) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if t.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	downloads, err := t.proxy.GetDownloads(ctx, t.settings)
	if err != nil {
		return nil, fmt.Errorf("tribler: GetDownloads: %w", err)
	}
	config, cfgErr := t.proxy.GetConfig(ctx, t.settings)
	if cfgErr != nil {
		t.log.Debug("tribler: GetConfig failed; cleanup gate disabled this poll", "err", cfgErr)
		config = nil
	}
	info := t.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(downloads))
	for _, d := range downloads {
		if d == nil {
			continue
		}
		if d.Size == nil || *d.Size == 0 {
			// Magnet still fetching metadata — skip until the
			// torrent has a size.
			continue
		}
		out = append(out, t.toItem(ctx, info, d, config))
	}
	return out, nil
}

// toItem projects one [Download] into a [downloadclients.Item].
// Per-file OutputPath resolution may issue a [Proxy.GetDownloadFiles]
// call (matches C# `:72`'s per-torrent lookup). config is the
// /api/settings response shared across one GetItems call (may be nil
// when GetConfig failed; the cleanup gate handles that).
func (t *TriblerDownloadClient) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, d *Download, config *Config) *downloadclients.Item {
	totalSize := int64(0)
	if d.Size != nil {
		totalSize = *d.Size
	}
	progress := 0.0
	if d.Progress != nil {
		progress = *d.Progress
	}
	remaining := int64(float64(totalSize) * (1 - progress))
	if remaining < 0 {
		remaining = 0
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         d.Infohash,
		Title:              d.Name,
		TotalSize:          totalSize,
		RemainingSize:      remaining,
		Message:            d.Error,
	}
	if d.AllTimeRatio != nil {
		ratio := *d.AllTimeRatio
		item.SeedRatio = &ratio
	}
	if d.Eta != nil {
		dur := clampETA(*d.Eta)
		item.RemainingTime = &dur
	}

	// Compose OutputPath = Destination + (single-file Name | Title).
	dest := ospath.New(d.Destination)
	if t.rpm != nil && strings.TrimSpace(d.Destination) != "" {
		if mapped, err := t.rpm.RemapRemoteToLocal(ctx, t.settings.Host, dest); err == nil {
			dest = mapped
		} else {
			t.log.Debug("tribler: RemapRemoteToLocal failed", "destination", d.Destination, "err", err)
		}
	}
	suffix := d.Name
	if files, err := t.proxy.GetDownloadFiles(ctx, t.settings, d.Infohash); err == nil && len(files) == 1 && strings.TrimSpace(files[0].Name) != "" {
		suffix = files[0].Name
	} else if err != nil {
		t.log.Debug("tribler: GetDownloadFiles failed; defaulting OutputPath to title", "infohash", d.Infohash, "err", err)
	}
	if strings.TrimSpace(suffix) != "" {
		item.OutputPath = ospath.New(strings.TrimRight(dest.FullPath(), "/") + "/" + suffix)
	} else {
		item.OutputPath = dest
	}

	// Status table.
	switch d.Status {
	case DownloadStatusHashchecking,
		DownloadStatusWaiting4HashCheck,
		DownloadStatusCircuits,
		DownloadStatusExitnodes,
		DownloadStatusDownloading:
		item.Status = downloadclients.DownloadItemStatusDownloading
	case DownloadStatusMetadata, DownloadStatusAllocatingDiskspace:
		item.Status = downloadclients.DownloadItemStatusQueued
	case DownloadStatusSeeding, DownloadStatusStopped:
		item.Status = downloadclients.DownloadItemStatusCompleted
	case DownloadStatusStoppedOnError:
		item.Status = downloadclients.DownloadItemStatusFailed
	default:
		item.Message = "Unknown download state"
		item.Status = downloadclients.DownloadItemStatusDownloading
	}

	// Overrides.
	if d.Status == DownloadStatusStopped && progress < 1 {
		item.Status = downloadclients.DownloadItemStatusPaused
	}
	if strings.TrimSpace(d.Error) != "" {
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = d.Error
	}

	if t.hasReachedSeedLimit(d, config) {
		item.CanMoveFiles = true
		item.CanBeRemoved = true
	}
	return item
}

// hasReachedSeedLimit ports the C# `HasReachedSeedLimit` predicate
// at `TriblerDownloadClient.cs:180-219`.
//
// Tribler's gate doesn't consult the per-grab IDownloadSeedConfigProvider
// — it reads Tribler's OWN config (SeedingMode + SeedingRatio +
// SeedingTime) and the per-torrent AllTimeRatio / TimeAdded fields.
// The wire shape comes from /api/settings, fetched once per GetItems
// call and threaded through here as `config`.
//
// Algorithm:
//  1. nil config (GetConfig failed) → false. Diverges from C# which
//     throws ArgumentNullException at `:182-185`; the Go port returns
//     false so a transient settings-endpoint failure degrades the
//     gate rather than crashing the GetItems loop.
//  2. d.Status != Stopped → false. C# `:193-196` — a still-running
//     download isn't "finished" in Tribler's sense (note: Tribler's
//     Seeding state is a separate ordinal from Stopped, and only
//     Stopped counts as cleanup-eligible).
//  3. Switch on SeedingMode:
//     - Ratio: `AllTimeRatio.HasValue && AllTimeRatio >= SeedingRatio`.
//     C# `:200-203`. nil SeedingRatio compares to "0" via
//     nullable-double semantics — the C# `>= null` returns false,
//     Go mirrors with a non-nil guard.
//     - Time: `TimeAdded.HasValue && epoch(TimeAdded) + SeedingTime < now`.
//     C# `:205-210`. SeedingTime is in SECONDS (C# `TimeSpan.FromSeconds`).
//     - Never: always true. C# `:212-213`.
//     - Forever / default / unknown: false. C# `:215-217`.
func (t *TriblerDownloadClient) hasReachedSeedLimit(d *Download, config *Config) bool {
	if config == nil {
		return false
	}
	if d.Status != DownloadStatusStopped {
		return false
	}
	mode := SeedingModeUnknown
	if m := config.Settings.LibTorrent.DownloadDefaults.SeedingMode; m != nil {
		mode = *m
	}
	switch mode {
	case SeedingModeRatio:
		ratioGoal := config.Settings.LibTorrent.DownloadDefaults.SeedingRatio
		return d.AllTimeRatio != nil && ratioGoal != nil && *d.AllTimeRatio >= *ratioGoal
	case SeedingModeTime:
		timeGoal := config.Settings.LibTorrent.DownloadDefaults.SeedingTime
		if d.TimeAdded == nil || timeGoal == nil {
			return false
		}
		downloadStarted := time.Unix(*d.TimeAdded, 0)
		maxSeeding := time.Duration(*timeGoal * float64(time.Second))
		return downloadStarted.Add(maxSeeding).Before(t.now())
	case SeedingModeNever:
		return true
	case SeedingModeForever, SeedingModeUnknown:
		fallthrough
	default:
		return false
	}
}

// clampETA mirrors C# `:91-102`'s clamp: negative → 0, >= 365 days
// in seconds → 365 days, otherwise direct conversion.
func clampETA(etaSeconds float64) time.Duration {
	yearSeconds := float64(365 * 24 * 60 * 60)
	switch {
	case etaSeconds < 0:
		return 0
	case etaSeconds >= yearSeconds:
		return 365 * 24 * time.Hour
	default:
		return time.Duration(etaSeconds * float64(time.Second))
	}
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (t *TriblerDownloadClient) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: t.Protocol(),
		Type:     Implementation,
		Name:     t.Name(),
	}
	if t.def != nil {
		info.ID = t.def.ID
		info.Name = t.def.Name
		info.RemoveCompletedDownloads = t.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional remote-path mapper used
// by GetItems for the Destination translation.
func (t *TriblerDownloadClient) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	t.rpm = rpm
}

// GetImportItem is the C# base passthrough.
func (t *TriblerDownloadClient) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports C# `TriblerDownloadClient.RemoveItem` at
// `:158-161`. Delegates to [Proxy.RemoveDownload] — DELETE
// `/api/downloads/<hash>` with the `{remove_data:bool}` body
// where deleteData controls whether Tribler erases the on-disk
// files alongside the queue entry.
func (t *TriblerDownloadClient) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if t.proxy == nil {
		return errors.New("tribler: nil Proxy")
	}
	if item == nil {
		return errors.New("tribler: nil item")
	}
	id := strings.TrimSpace(item.DownloadID)
	if id == "" {
		return errors.New("tribler: item has empty DownloadID")
	}
	if err := t.proxy.RemoveDownload(ctx, t.settings, id, deleteData); err != nil {
		return fmt.Errorf("tribler: RemoveDownload(%s): %w", id, err)
	}
	return nil
}

// GetStatus ports `TriblerDownloadClient.GetStatus` at C#
// `:163-178`. Resolves the configured save directory via
// [Proxy.GetConfig] (reads `settings.libtorrent.download_defaults.saveas`),
// then optionally appends `/.<TvCategory>` — note the LEADING DOT
// before TvCategory (matches C# `:170`'s
// `string.Format("{0}/.{1}", destDir, Settings.TvCategory)`).
// This is a Tribler-specific quirk; the Download arm's
// resolveDownloadDirectory joins with `<base>/<category>` (no dot).
// Finally, the RemotePathMapper (when bound) translates the path.
//
// Config-probe failures are NON-fatal — GetStatus swallows the
// error and returns the IsLocalhost-only ClientInfo so the queue
// healthcheck still surfaces the configured host. Same posture as
// the Hadouken / DownloadStation / Freebox GetStatus implementations.
func (t *TriblerDownloadClient) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	info := &downloadclients.ClientInfo{IsLocalhost: isLocalHost(t.settings.Host)}
	if t.proxy == nil {
		return info, nil
	}
	cfg, err := t.proxy.GetConfig(ctx, t.settings)
	if err != nil {
		t.log.Debug("tribler: GetStatus GetConfig failed", "err", err)
		return info, nil
	}
	dir := strings.TrimSpace(cfg.Settings.LibTorrent.DownloadDefaults.SaveAs)
	if dir == "" {
		return info, nil
	}
	if cat := strings.TrimSpace(t.settings.TvCategory); cat != "" {
		dir = strings.TrimRight(dir, "/") + "/." + cat
	}
	remote := ospath.New(dir)
	if t.rpm != nil {
		if mapped, mapErr := t.rpm.RemapRemoteToLocal(ctx, t.settings.Host, remote); mapErr == nil {
			remote = mapped
		} else {
			t.log.Debug("tribler: GetStatus RemapRemoteToLocal failed", "directory", dir, "err", mapErr)
		}
	}
	info.OutputRootFolders = append(info.OutputRootFolders, remote)
	return info, nil
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
	t := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		t.SetRemotePathMapper(rpm)
	}
	return t, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
