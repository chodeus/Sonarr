package flood

import (
	"context"
	"encoding/base64"
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

	// rpm is the optional remote-path mapper consulted by GetItems
	// to translate Flood-side `directory` into Sonarr-side paths.
	// Nil → pass-through.
	rpm downloadclients.RemotePathMapper
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

// Download ports C# `Flood.cs:90-102` end-to-end. Dispatches:
//
//   - TorrentBody non-empty → [Proxy.AddTorrentByFile] with the
//     base64-encoded body + composed tags.
//   - MagnetURL non-empty → [Proxy.AddTorrentByUrl] with the magnet
//     URL + composed tags.
//
// Returns req.InfoHash uppercased (matches C# `:94,101` `return hash`
// — the orchestrator pre-computes the hash; C# uses lower-case-as-
// passed but Sonarr's other torrent clients consistently uppercase
// the queue join key — same convention applied here).
//
// Tag composition for 5.J72 covers `Settings.Tags` directly. The
// `Settings.AdditionalTags` arm (which expands TitleSlug / Quality /
// Languages / ReleaseGroup / Year / Indexer / Network from the
// Subject + ParsedEpisodeInfo) is deferred — requires either a new
// GrabRequest field surface or a decisionengine type-assertion that
// would pull heavy transitive imports for one tag-list expansion.
// Documented as a follow-up; for now `Settings.Tags` alone matches
// the most common "tag with 'sonarr'" deploy.
func (f *Flood) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("flood: nil GrabRequest")
	}
	if f.proxy == nil {
		return "", errors.New("flood: nil Proxy")
	}
	if strings.TrimSpace(req.InfoHash) == "" {
		return "", errors.New("flood: GrabRequest.InfoHash is empty (orchestrator must pre-compute)")
	}
	tags := composeTags(f.settings)

	switch {
	case len(req.TorrentBody) > 0:
		body := base64.StdEncoding.EncodeToString(req.TorrentBody)
		if err := f.proxy.AddTorrentByFile(ctx, f.settings, body, tags); err != nil {
			return "", fmt.Errorf("flood: AddTorrentByFile(%q): %w", req.ReleaseTitle, err)
		}
	case strings.TrimSpace(req.MagnetURL) != "":
		if err := f.proxy.AddTorrentByUrl(ctx, f.settings, req.MagnetURL, tags); err != nil {
			return "", fmt.Errorf("flood: AddTorrentByUrl(%q): %w", req.ReleaseTitle, err)
		}
	default:
		return "", errors.New("flood: GrabRequest has neither TorrentBody nor MagnetURL")
	}
	return strings.ToUpper(req.InfoHash), nil
}

// composeTags returns the tag list applied to every add-torrent
// call. Skeleton coverage: returns `Settings.Tags` verbatim (filters
// blank entries). The full AdditionalTags arm at C# `Flood.cs:42-85`
// — which composes per-Subject tags — lands in a follow-up; see the
// divergence note on [Flood.Download].
func composeTags(s Settings) []string {
	out := make([]string, 0, len(s.Tags))
	for _, t := range s.Tags {
		if strings.TrimSpace(t) != "" {
			out = append(out, t)
		}
	}
	return out
}

// GetItems ports `Flood.GetItems` at C# `Flood.cs:104-188`. Walks
// the hash-keyed torrent map from [Proxy.GetTorrents], applies the
// Tags + PostImportTags gates, projects each entry into a
// [downloadclients.Item] via the 4-arm status table.
//
// Gates (priority order — first miss skips the row):
//
//   - Tags gate: skip when Settings.Tags has any tag NOT present
//     on the torrent (`!Tags.All(present)` at C# `:114`).
//   - PostImportTags skip: skip when EVERY PostImportTag IS present
//     on the torrent (the torrent has already been imported and
//     marked — C# `:119-122`).
//
// Status table (priority order — first match wins, C# `:144-159`):
//
//   - Status contains "seeding" OR "complete" → Completed
//   - Status contains "stopped" → Paused
//   - Status contains "error" → Warning
//   - Status contains "downloading" → Downloading
//   - default → status field left at zero (the C# code does the same
//     — no fall-through arm).
//
// CanMoveFiles + CanBeRemoved both default to false. The C#
// cleanup-gate at `:161-182` (which reads `IDownloadSeedConfigProvider`
// and flips both flags when the ratio OR seed-time threshold is
// reached) is deferred — the Go port doesn't yet have a
// seed-config-provider surface, and the gate is a "post-import
// cleanup nicety" rather than load-bearing for the import path.
// Documented; follow-up: port the seed-config-provider service +
// wire the cleanup arm.
func (f *Flood) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if f.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	torrents, err := f.proxy.GetTorrents(ctx, f.settings)
	if err != nil {
		return nil, fmt.Errorf("flood: GetTorrents: %w", err)
	}
	hasPostImport := len(f.settings.PostImportTags) > 0
	info := f.itemClientInfo(hasPostImport)
	out := make([]*downloadclients.Item, 0, len(torrents))
	for hash, t := range torrents {
		if !containsAll(t.Tags, f.settings.Tags) {
			continue
		}
		if hasPostImport && containsAll(t.Tags, f.settings.PostImportTags) {
			continue
		}
		out = append(out, f.toItem(ctx, info, hash, t))
	}
	return out, nil
}

// toItem projects one [Torrent] into a [downloadclients.Item].
func (f *Flood) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, hash string, t *Torrent) *downloadclients.Item {
	dir := ospath.New(t.Directory)
	if f.rpm != nil && strings.TrimSpace(t.Directory) != "" {
		if mapped, err := f.rpm.RemapRemoteToLocal(ctx, f.settings.Host, dir); err == nil {
			dir = mapped
		} else {
			f.log.Debug("flood: RemapRemoteToLocal failed", "directory", t.Directory, "err", err)
		}
	}

	var category string
	if len(t.Tags) > 0 {
		category = t.Tags[0]
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         hash,
		Title:              t.Name,
		OutputPath:         dir,
		Category:           category,
		RemainingSize:      t.SizeBytes - t.BytesDone,
		TotalSize:          t.SizeBytes,
		Message:            t.Message,
	}
	ratio := t.Ratio
	item.SeedRatio = &ratio
	if t.Eta > 0 {
		d := time.Duration(t.Eta) * time.Second
		item.RemainingTime = &d
	}
	switch {
	case statusContains(t.Status, "seeding") || statusContains(t.Status, "complete"):
		item.Status = downloadclients.DownloadItemStatusCompleted
	case statusContains(t.Status, "stopped"):
		item.Status = downloadclients.DownloadItemStatusPaused
	case statusContains(t.Status, "error"):
		item.Status = downloadclients.DownloadItemStatusWarning
	case statusContains(t.Status, "downloading"):
		item.Status = downloadclients.DownloadItemStatusDownloading
	}
	return item
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, hasPostImportCategory)`
// at `Flood.cs:126`. hasPostImportCategory is true when
// Settings.PostImportTags is non-empty.
func (f *Flood) itemClientInfo(hasPostImportCategory bool) *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol:              f.Protocol(),
		Type:                  Implementation,
		Name:                  f.Name(),
		HasPostImportCategory: hasPostImportCategory,
	}
	if f.def != nil {
		info.ID = f.def.ID
		info.Name = f.def.Name
		info.RemoveCompletedDownloads = f.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional remote-path mapper used
// by GetItems for the `directory` translation. Same Phase 6.B
// pattern as Transmission/qBittorrent/Hadouken/NzbVortex.
func (f *Flood) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	f.rpm = rpm
}

// containsAll returns true when every entry in `wanted` is present
// in `set`. Empty `wanted` returns true (matches C# `Tags.All`'s
// behaviour on the empty enumerable). nil-safe on both sides.
func containsAll(set, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	have := make(map[string]bool, len(set))
	for _, s := range set {
		have[s] = true
	}
	for _, w := range wanted {
		if !have[w] {
			return false
		}
	}
	return true
}

// statusContains reports whether the status slice has any entry
// equal to needle (case-sensitive, matching C# `Status.Contains`
// which is a List<string>.Contains call).
func statusContains(status []string, needle string) bool {
	for _, s := range status {
		if s == needle {
			return true
		}
	}
	return false
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
	f := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		f.SetRemotePathMapper(rpm)
	}
	return f, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
