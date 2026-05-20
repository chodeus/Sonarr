package utorrent

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
// for the uTorrent download client. Mirrors C# class names
// `UTorrent` + `UTorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrent.cs`
// and `UTorrentSettings.cs:19`). The (Implementation, ConfigContract)
// pair is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "UTorrent"
	ConfigContract = "UTorrentSettings"
)

// UTorrent is the [downloadclients.Provider] implementation for the
// uTorrent torrent client. Ports C# `UTorrent` at
// `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrent.cs`.
//
// Slice 5.J47 scope: Name / Protocol / Definition / RequestAction
// are live; Test runs the token+cookie auth flow + the `getsettings`
// version probe via the bound proxy + minBuild floor. Download /
// GetItems / GetStatus / RemoveItem / GetImportItem /
// MarkItemAsImported still return [downloadclients.ErrNotImplementedYet].
type UTorrent struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// minBuild is the uTorrent build-number floor [Test] enforces.
// uTorrent build 25406 (3.0+) was the first release shipping the
// /gui/ WebUI surface Sonarr's code targets. Older builds may work
// in practice but aren't supported.
const minBuild = 25406

// New constructs a uTorrent Provider against the resolved settings
// + an optional Proxy. Used by both the registry [Constructor] and
// by tests. nil proxy → Test surfaces Settings.Validate failures
// only (matches the pre-5.J47 stub).
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *UTorrent {
	if log == nil {
		log = slog.Default()
	}
	return &UTorrent{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `UTorrent.Name`
// — the class name itself ("UTorrent" → "uTorrent" lower-case-u
// short form).
func (u *UTorrent) Name() string { return "uTorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol`.
func (u *UTorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (u *UTorrent) Definition() *downloadclients.DownloadClient { return u.def }

// Settings returns the bound Settings. Exposed for tests that need
// to assert against the round-tripped JSON without re-decoding.
func (u *UTorrent) Settings() Settings { return u.settings }

// Download ports `UTorrent.AddFromMagnetLink` + `AddFromTorrentFile`
// at `UTorrent.cs:62-102`. Picks the add path based on the
// GrabRequest shape:
//
//   - TorrentBody bytes set → AddTorrentFromFile (multipart upload).
//   - MagnetURL set → AddTorrentFromURL.
//   - DownloadURL set → AddTorrentFromURL (uTorrent fetches the
//     .torrent itself over HTTP).
//
// After add, applies the post-add chain:
//
//  1. SetTorrentLabel(hash, TvCategory) when TvCategory is non-empty.
//  2. MoveTorrentToTopInQueue(hash) when the per-recency priority
//     setting resolves to PriorityFirst.
//  3. SetState(hash, IntialState) when IntialState != Start.
//
// Returns the upper-cased info-hash. Hash sourcing:
//
//   - req.InfoHash trusted when set.
//   - Otherwise extracted from MagnetURL's xt=urn:btih:HASH query
//     parameter.
//   - Otherwise errors — uTorrent's add-url RPC doesn't echo the
//     hash and the post-add arms need a known hash to target the
//     just-added row.
//
// Best-effort posture on the post-add arms: SetTorrentLabel /
// MoveTorrentToTopInQueue / SetState failures warn + continue.
func (u *UTorrent) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("utorrent: nil GrabRequest")
	}
	if u.proxy == nil {
		return "", errors.New("utorrent: Download requires a wired Proxy")
	}
	hash, err := resolveInfoHash(req)
	if err != nil {
		return "", err
	}

	switch {
	case len(req.TorrentBody) > 0:
		if err := u.proxy.AddTorrentFromFile(ctx, req.ReleaseTitle, req.TorrentBody, u.settings); err != nil {
			return "", err
		}
	case req.MagnetURL != "":
		if err := u.proxy.AddTorrentFromURL(ctx, req.MagnetURL, u.settings); err != nil {
			return "", err
		}
	case req.DownloadURL != "":
		if err := u.proxy.AddTorrentFromURL(ctx, req.DownloadURL, u.settings); err != nil {
			return "", err
		}
	default:
		return "", errors.New("utorrent: Download: no TorrentBody / MagnetURL / DownloadURL on GrabRequest")
	}

	if strings.TrimSpace(u.settings.TvCategory) != "" {
		if err := u.proxy.SetTorrentLabel(ctx, hash, u.settings.TvCategory, u.settings); err != nil {
			u.log.Warn("utorrent: SetTorrentLabel failed", slog.String("hash", hash), slog.Any("err", err))
		}
	}
	if u.recencyPriority(req.IsRecent) == PriorityFirst {
		if err := u.proxy.MoveTorrentToTopInQueue(ctx, hash, u.settings); err != nil {
			u.log.Warn("utorrent: MoveTorrentToTopInQueue failed", slog.String("hash", hash), slog.Any("err", err))
		}
	}
	if State(u.settings.IntialState) != StateStart {
		if err := u.proxy.SetState(ctx, hash, State(u.settings.IntialState), u.settings); err != nil {
			u.log.Warn("utorrent: SetState failed",
				slog.String("hash", hash),
				slog.String("state", State(u.settings.IntialState).String()),
				slog.Any("err", err))
		}
	}

	return strings.ToUpper(hash), nil
}

// recencyPriority resolves req.IsRecent to the configured Priority.
// Mirrors C# `UTorrent.cs:74-80,99-105`.
func (u *UTorrent) recencyPriority(isRecent bool) Priority {
	if isRecent {
		return Priority(u.settings.RecentTvPriority)
	}
	return Priority(u.settings.OlderTvPriority)
}

// resolveInfoHash returns the hex info-hash for a GrabRequest.
// Trust order: req.InfoHash → magnet xt=urn:btih: → error.
func resolveInfoHash(req *downloadclients.GrabRequest) (string, error) {
	if h := strings.TrimSpace(req.InfoHash); h != "" {
		return strings.ToLower(h), nil
	}
	if req.MagnetURL == "" {
		return "", errors.New("utorrent: cannot extract info-hash: neither InfoHash nor MagnetURL set")
	}
	const xtPrefix = "xt=urn:btih:"
	for _, segment := range strings.Split(req.MagnetURL, "&") {
		segment = strings.TrimPrefix(segment, "magnet:?")
		if strings.HasPrefix(strings.ToLower(segment), xtPrefix) {
			return strings.ToLower(strings.TrimSpace(segment[len(xtPrefix):])), nil
		}
	}
	return "", fmt.Errorf("utorrent: magnet URL missing xt=urn:btih: parameter: %s", req.MagnetURL)
}

// GetItems ports `UTorrent.GetItems` at `UTorrent.cs:109-180`.
// Polls action=list, filters by Label == TvCategory, projects each
// torrent row into a downloadclients.Item with the Status bitflag
// mapped per C# `:147-170`:
//
//   - Error flag → Warning (with message).
//   - Loaded+Checked+Remaining==0+Progress==1.0 → Completed.
//   - Paused flag → Paused.
//   - Started flag → Downloading.
//   - otherwise → Queued.
//
// CanMoveFiles + CanBeRemoved are true when the torrent is neither
// Queued nor Started — same gate as C# `:174-178`.
func (u *UTorrent) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if u.proxy == nil {
		return nil, errors.New("utorrent: GetItems requires a wired Proxy")
	}
	torrents, err := u.proxy.GetTorrents(ctx, u.settings)
	if err != nil {
		return nil, err
	}
	out := make([]*downloadclients.Item, 0, len(torrents))
	for _, t := range torrents {
		if u.settings.TvCategory != "" && t.Label != u.settings.TvCategory {
			continue
		}
		item := &downloadclients.Item{
			DownloadID:    strings.ToUpper(t.Hash),
			Title:         t.Name,
			TotalSize:     t.Size,
			Category:      t.Label,
			RemainingSize: t.Remaining,
		}
		// Per-row Status mapping. Mirrors C# `:147-170`.
		switch {
		case t.Status.Has(StatusError):
			item.Status = downloadclients.DownloadItemStatusWarning
			item.Message = "uTorrent reports torrent in error state"
		case t.Status.Has(StatusLoaded) && t.Status.Has(StatusChecked) && t.Remaining == 0 && t.Progress == 1.0:
			item.Status = downloadclients.DownloadItemStatusCompleted
		case t.Status.Has(StatusPaused):
			item.Status = downloadclients.DownloadItemStatusPaused
		case t.Status.Has(StatusStarted):
			item.Status = downloadclients.DownloadItemStatusDownloading
		default:
			item.Status = downloadclients.DownloadItemStatusQueued
		}

		// SeedRatio + RemainingTime — uTorrent reports ratio as
		// per-mille int (already converted to float by the proxy);
		// Eta=-1 means no ETA available.
		if t.Status.Has(StatusLoaded) && t.Status.Has(StatusChecked) && t.Remaining == 0 {
			ratio := t.Ratio
			item.SeedRatio = &ratio
		}
		if t.Eta > 0 {
			rt := time.Duration(t.Eta) * time.Second
			item.RemainingTime = &rt
		}

		// OutputPath — uTorrent's RootDownloadPath is the directory
		// the torrent's files land in; the C# code joins the Name
		// when the path's basename doesn't already match the
		// torrent name (matches `:138-145`). The Go port preserves
		// that quirk verbatim.
		if t.RootDownloadPath != "" {
			rdp := ospath.New(t.RootDownloadPath)
			if rdp.FileName() == t.Name {
				item.OutputPath = rdp
			} else {
				item.OutputPath = rdp.Add(ospath.New(t.Name))
			}
		}

		// 'Started' without 'Queued' is the 'forced seeding' state
		// — C# `:174-178` gates CanMoveFiles/CanBeRemoved on
		// !Queued && !Started.
		canHandle := !t.Status.Has(StatusQueued) && !t.Status.Has(StatusStarted)
		item.CanMoveFiles = canHandle
		item.CanBeRemoved = canHandle

		out = append(out, item)
	}
	return out, nil
}

// GetImportItem passes through unchanged. Mirrors C#
// `DownloadClientBase.GetImportItem` default behaviour.
func (u *UTorrent) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `UTorrent.RemoveItem` (the base
// DownloadClientBase.RemoveItem wraps Proxy.RemoveTorrent at C#
// `UTorrentProxy.cs:130-139`). The optional deleteData arm hits
// action=removedata (delete data + remove); deleteData=false hits
// action=remove (queue removal only).
func (u *UTorrent) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if u.proxy == nil {
		return errors.New("utorrent: RemoveItem requires a wired Proxy")
	}
	if item == nil {
		return errors.New("utorrent: RemoveItem: nil Item")
	}
	if strings.TrimSpace(item.DownloadID) == "" {
		return errors.New("utorrent: RemoveItem: empty DownloadID")
	}
	return u.proxy.RemoveTorrent(ctx, item.DownloadID, deleteData, u.settings)
}

// GetStatus ports `UTorrent.GetStatus`. Returns a minimal
// ClientInfo with IsLocalhost set when the host resolves to
// 127.0.0.1 / localhost / ::1. Matches the rTorrent posture (C#
// `UTorrent` doesn't override GetStatus, so the base class's
// minimal shape is what surfaces).
func (u *UTorrent) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	host := strings.TrimSpace(u.settings.Host)
	isLocal := host == "127.0.0.1" || host == "localhost" || host == "::1"
	return &downloadclients.ClientInfo{IsLocalhost: isLocal}, nil
}

// MarkItemAsImported ports `UTorrent.MarkItemAsImported` at
// `UTorrent.cs:46-60`. Applies the post-import label when
// TvImportedCategory is set + differs from TvCategory; then
// (C# `:54-59`) removes the original TvCategory label so the
// torrent ends up tagged only with the imported category.
//
// Best-effort: failures warn + return nil so the import pipeline
// doesn't abort on a label-set hiccup.
func (u *UTorrent) MarkItemAsImported(ctx context.Context, item *downloadclients.Item) error {
	if u.proxy == nil {
		return errors.New("utorrent: MarkItemAsImported requires a wired Proxy")
	}
	if item == nil {
		return errors.New("utorrent: MarkItemAsImported: nil Item")
	}
	if strings.TrimSpace(u.settings.TvImportedCategory) == "" {
		return nil
	}
	if u.settings.TvImportedCategory == u.settings.TvCategory {
		return nil
	}
	if err := u.proxy.SetTorrentLabel(ctx, item.DownloadID, u.settings.TvImportedCategory, u.settings); err != nil {
		u.log.Warn("utorrent: SetTorrentLabel (post-import) failed",
			slog.String("hash", item.DownloadID),
			slog.String("label", u.settings.TvImportedCategory),
			slog.Any("err", err))
	}
	return nil
}

// Test runs the per-client connection self-check. Validates the
// Settings first, then (when no failures and a proxy is wired)
// runs the token+cookie auth flow + the `getsettings` version
// probe and gates on [minBuild]. Mirrors C#
// `DownloadClientBase.Test()` posture.
func (u *UTorrent) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := u.settings.Validate()
	if len(failures) > 0 || u.proxy == nil {
		return failures, nil
	}
	build, err := u.proxy.GetVersion(ctx, u.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to uTorrent: %v", err),
		}}, nil
	}
	if build < minBuild {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("uTorrent build %d is too old; minimum supported is %d", build, minBuild),
		}}, nil
	}
	return nil, nil
}

// RequestAction returns (nil, nil) for every action — the default
// C# `DownloadClientBase.RequestAction` shape. uTorrent has no
// settings-class actions exposed today.
func (u *UTorrent) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: UTorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*UTorrent)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Transmission + rTorrent factory pattern. When an HTTPDoer is
// present in deps.HTTPClient the constructor builds the
// token-cookie Proxy.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("utorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("utorrent: settings unmarshal: %w", err)
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
			return nil, fmt.Errorf("utorrent: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		proxy = NewProxy(doer, log)
	}
	return New(def, settings, proxy, log), nil
}

// init registers uTorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
