package downloadclients

import (
	"context"

	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// Provider is the runtime download-client abstraction concrete
// implementations (Sabnzbd, Transmission, qBittorrent, etc.) satisfy.
// Mirrors the C# `IDownloadClient` at
// `src/NzbDrone.Core/Download/IDownloadClient.cs:9-19` plus the
// `IProvider` base contract (`Name`, `Definition`) the C#
// `DownloadClientBase<TSettings>` implements at
// `src/NzbDrone.Core/Download/DownloadClientBase.cs:62-70`.
//
// Slice 4.W (queue [13]). Concrete clients land in queue [14] (Sabnzbd
// + Transmission); this slice ships the interface contract + Settings
// family + the per-family Constructor [Registry] so the wiring shape
// is locked before the providers themselves arrive.
//
// Direction-of-imports note: downloadclients → decisionengine. The
// Download method takes a *decisionengine.Subject (the post-Map,
// post-Aggregate "release under decision" shape). decisionengine
// already imports downloadclients (TorrentSeedConfiguration on
// Subject) — so the new edge would form a cycle. To avoid that, the
// Download method takes a more general `*GrabRequest` shape defined
// in this package; the orchestrator (ProcessDownloadDecisions, queue
// [15]) translates Subject → GrabRequest at the boundary. See the
// [GrabRequest] doc-comment below.
//
// Method semantics summary (full C# cites on each method):
//
//   - Name returns the concrete client's display label.
//   - Protocol returns Usenet vs Torrent.
//   - Download hands a single release to the client and returns the
//     client-side DownloadID (NZB hash / torrent info-hash).
//   - GetItems lists the client's in-flight downloads.
//   - GetImportItem narrows a queue Item to a writer-import-suitable
//     shape (defaults to the input — the C# base impl at
//     `DownloadClientBase.cs:102-105`).
//   - RemoveItem deletes the item from the client's queue, optionally
//     deleting the data on disk.
//   - GetStatus returns the client's runtime status (localhost flag,
//     output folders, sorting mode).
//   - MarkItemAsImported informs the client an item finished importing
//     so the client can move it to its post-import category. Most
//     clients return NotSupportedException — the Go port returns
//     [ErrMarkAsImportedUnsupported].
//   - Test executes the per-client connection self-check, returning
//     the validation failures (empty slice means success).
//   - Definition returns the persisted [DownloadClient] row this
//     provider was constructed against (for log lines + the v3 API's
//     "definition" field).
//
// Goroutine-safety: implementations must tolerate concurrent
// GetItems / GetStatus calls from the queue UI + scheduler. Download
// + RemoveItem are typically called from a single grab-loop
// goroutine.
type Provider interface {
	// Name returns the user-facing display label. Mirrors the C#
	// abstract `Name { get; }` at `DownloadClientBase.cs:62`.
	Name() string

	// Protocol returns the protocol family this client speaks.
	// Mirrors C# `Protocol { get; }` at `DownloadClientBase.cs:97`.
	Protocol() indexers.DownloadProtocol

	// Definition returns the persisted DownloadClients row backing
	// this provider instance. Mirrors C# `Definition { get; set; }`
	// at `:70`. Nil from a test-constructed provider that hasn't
	// been bound to a row.
	Definition() *DownloadClient

	// Download submits a single grab request to the client.
	// Returns the client-side DownloadID (NZB hash / torrent
	// info-hash) on success. The orchestrator (queue [15] —
	// ProcessDownloadDecisions) catches the returned ID and
	// associates it with the queue Item.
	//
	// Mirrors C# `Task<string> Download(RemoteEpisode, IIndexer)` at
	// `IDownloadClient.cs:12`. The Go port takes a [*GrabRequest]
	// instead of (RemoteEpisode, IIndexer) — see the GrabRequest
	// doc for why.
	Download(ctx context.Context, req *GrabRequest) (string, error)

	// GetItems lists the client's in-flight downloads. Mirrors C#
	// `IEnumerable<DownloadClientItem> GetItems()` at
	// `IDownloadClient.cs:13`. Returns empty slice (never nil) when
	// the client has no items; errors propagate when the client
	// can't be reached.
	GetItems(ctx context.Context) ([]*Item, error)

	// GetImportItem narrows a queue Item to the writer-import-
	// suitable shape. Most clients pass through; SAB-family
	// clients consult their move-after-import category to detect
	// post-import relocations. Mirrors C# `GetImportItem(item,
	// previousImportAttempt)` at `IDownloadClient.cs:14` /
	// `DownloadClientBase.cs:102-105`.
	GetImportItem(ctx context.Context, item *Item, previousAttempt *Item) (*Item, error)

	// RemoveItem deletes the item from the client's queue. When
	// deleteData is true, the on-disk download data is also
	// deleted (the C# base method at `DownloadClientBase.cs:110-146`
	// implements this via DiskProvider.DeleteFolder/DeleteFile).
	// Mirrors C# `RemoveItem(item, deleteData)` at `IDownloadClient.cs:15`.
	RemoveItem(ctx context.Context, item *Item, deleteData bool) error

	// GetStatus returns the client's runtime status snapshot.
	// Mirrors C# `DownloadClientInfo GetStatus()` at
	// `IDownloadClient.cs:16`. Always returns a non-nil value;
	// errors propagate when the status RPC fails.
	GetStatus(ctx context.Context) (*ClientInfo, error)

	// MarkItemAsImported informs the client a grab has been
	// successfully imported. Most usenet clients raise
	// NotSupportedException — the Go port returns
	// [ErrMarkAsImportedUnsupported]. Mirrors C#
	// `MarkItemAsImported(item)` at `IDownloadClient.cs:17` /
	// `DownloadClientBase.cs:189-192`.
	MarkItemAsImported(ctx context.Context, item *Item) error

	// Test runs the per-client connection self-check. Returns the
	// validation failures (empty slice = success). Mirrors C#
	// `ValidationResult Test()` at `DownloadClientBase.cs:148-163`.
	// A returned error is reserved for "test was aborted due to an
	// unexpected exception" — the C# code wraps that as a single
	// failure with an empty property name; the Go port surfaces it
	// as a non-nil error so the orchestrator can distinguish.
	Test(ctx context.Context) ([]TestFailure, error)

	// RequestAction handles the per-impl action handshake some
	// download-client Settings classes expose. Mirrors C#
	// `object RequestAction(string action,
	// IDictionary<string,string> query)` at
	// `src/NzbDrone.Core/ThingiProvider/IProvider.cs:15` and the
	// no-op default at `DownloadClientBase.cs:165-167` (returns
	// null). Concrete clients that don't override return (nil, nil).
	//
	// Common actions across the overriding clients: Sabnzbd
	// `getCategories` enumerates the configured categories; Sabnzbd
	// `getCustomCategories` returns per-app custom-category presets;
	// Transmission `getCustomTorrentFolders` enumerates writable
	// torrent destination paths.
	RequestAction(ctx context.Context, action string, query map[string]string) (any, error)
}

// RemotePathMapper is the narrow projection concrete download clients
// consume for path translation. Mirrors the C# `IRemotePathMappingService`
// surface at `RemotePathMappings/RemotePathMappingService.cs:128-157`
// reduced to the single direction concrete clients actually call:
// remap a client-side absolute path back to a Sonarr-side path. The
// inverse direction (RemapLocalToRemote) isn't reached for from a
// download-client constructor — it's a service-layer concern at the
// queue / import-decision boundary, outside this package.
//
// Production binds *remotepathmappings.Service via Go's structural
// typing; concrete client constructors cast
// [Dependencies.RemotePathMappingService] to this interface. A nil
// mapper degrades to "no remap" at every call site (the per-client
// methods return the raw path unchanged — preserves the pre-Phase-6.B
// behaviour).
//
// Slice 6.B2 (Phase 6.B RemotePathMappingService wire-up arc). The
// interface lives here (not in remotepathmappings) so concrete clients
// don't need to import remotepathmappings just to declare the dep —
// matches the established Dependencies-bundle pattern where typed
// surfaces are defined where they're consumed.
type RemotePathMapper interface {
	RemapRemoteToLocal(ctx context.Context, host string, remotePath ospath.OsPath) (ospath.OsPath, error)
}

// GrabRequest is the post-Map, post-Aggregate shape the orchestrator
// hands a [Provider.Download] call. Carries the resolved release info
// + per-grab torrent seed configuration + the indexer that produced
// the release (for download-URL request building).
//
// Mirrors C# `Download(RemoteEpisode remoteEpisode, IIndexer indexer)`
// at `IDownloadClient.cs:12` but flattens to the minimum the concrete
// implementations actually consume:
//
//   - C# RemoteEpisode → Subject (the Go-port equivalent) is one
//     candidate, but importing decisionengine here would form a
//     cycle. The Subject's Release field is `any` (concretely
//     *parser.ReleaseInfo or *parser.TorrentInfo); the only fields
//     concrete clients actually read are Release.Title,
//     Release.DownloadUrl, and (for torrents) MagnetUrl + InfoHash —
//     so GrabRequest exposes those directly.
//   - C# IIndexer → an interface concrete clients call
//     `GetDownloadRequest(url)` against for per-indexer cookie /
//     auth-header attachment. The Go port exposes a [DownloadRequester]
//     narrow interface that wraps the indexers package's typed
//     GetDownloadRequest method.
//
// The orchestrator (queue [15]) builds a GrabRequest from a Subject
// + the matched indexers.Repo row before invoking Provider.Download.
type GrabRequest struct {
	// ReleaseTitle is Subject.Release.Title — the human-readable
	// release name. Used by the client for log lines + (some
	// clients) as the queue-item display name.
	ReleaseTitle string

	// DownloadURL is Subject.Release.DownloadURL — the URL the
	// client (or the proxy in this package's Download method body)
	// fetches the NZB / torrent file from. Empty when the only
	// available source is MagnetURL.
	DownloadURL string

	// MagnetURL is the (optional) magnet-link variant — set only
	// for torrent releases. Some torrent clients (Transmission
	// classic) prefer adding magnets directly when the URL is
	// already a magnet rather than fetching a .torrent first.
	MagnetURL string

	// InfoHash is the torrent's info-hash (lowercase hex). Set only
	// for torrent releases. Used by clients that accept hash-only
	// adds (rTorrent).
	InfoHash string

	// IndexerID is the persisted indexers row ID the release came
	// from. Used to look up the IIndexer instance for per-indexer
	// cookie / auth-header attachment via [DownloadRequester].
	IndexerID int

	// IndexerName is the user-supplied indexer display name. For
	// log lines.
	IndexerName string

	// Subject is the full *decisionengine.Subject the orchestrator
	// resolved. The Go port carries it through as the canonical
	// release-under-decision shape; concrete clients are free to
	// reach into it for fields not surfaced on GrabRequest
	// directly (e.g. Series for series-id-based categorisation).
	// Typed as `any` to avoid the decisionengine → downloadclients
	// import cycle (decisionengine.Subject embeds TorrentSeed-
	// Configuration from this package, so an explicit
	// *decisionengine.Subject import here would form a cycle).
	// Concrete clients type-assert.
	Subject any

	// SeedConfiguration is the per-grab torrent-seed-policy
	// override. Nil for usenet grabs. Mirrors the C# field
	// `RemoteEpisode.SeedConfiguration` at
	// `Parser/Model/RemoteEpisode.cs:22`.
	SeedConfiguration *TorrentSeedConfiguration

	// NzbBody is the fetched NZB-file bytes for usenet grabs. The
	// orchestrator (queue [15]) fetches DownloadURL via the
	// indexer's HTTP-request builder (cookie / auth-header
	// support) before invoking Provider.Download, and stuffs the
	// bytes here so the concrete client can POST them directly.
	// Empty for torrent grabs. Mirrors the in-method fetch C#
	// `UsenetClientBase.Download` does at `:40-91` — the Go port
	// hoists the fetch into the orchestrator to keep the Provider
	// interface synchronous + side-effect-light.
	NzbBody []byte

	// TorrentBody is the optional .torrent-file bytes for torrent
	// grabs. The orchestrator may pre-fetch a .torrent (when the
	// concrete client prefers torrent files over magnet links — see
	// the C# `TorrentClientBase.PreferTorrentFile` arm at
	// `TorrentClientBase.cs:46`); concrete clients route through
	// AddTorrentFromData when this is non-empty. Empty when the
	// client should fetch via MagnetURL or DownloadURL directly.
	TorrentBody []byte

	// IsRecent is true when at least one matched episode aired in
	// the last 14 days (per [decisionengine.Subject.IsRecentEpisode]).
	// Concrete clients use this to pick between RecentTvPriority +
	// OlderTvPriority settings. Mirrors C# `remoteEpisode.IsRecentEpisode()`
	// at `Sabnzbd.cs:43`.
	IsRecent bool
}

// ClientInfo is the runtime status snapshot a Provider's GetStatus
// returns. Mirrors C# `DownloadClientInfo` at
// `src/NzbDrone.Core/Download/DownloadClientInfo.cs:1-18`.
//
// Renamed from `DownloadClientInfo` to `ClientInfo` because the
// package name already provides the `downloadclients.` qualifier;
// `downloadclients.ClientInfo` reads better than
// `downloadclients.DownloadClientInfo` at call sites.
type ClientInfo struct {
	// IsLocalhost is true when the client RPC endpoint resolves to
	// 127.0.0.1 / ::1. Used by the orchestrator to skip remote-path-
	// mapping translation for downloads that landed on the same
	// host. Mirrors C# `IsLocalhost { get; set; }` at `:13`.
	IsLocalhost bool

	// SortingMode is the client's reported "post-import folder
	// layout" hint (typically empty; SAB returns "folder" or
	// "files"). Mirrors C# `SortingMode { get; set; }` at `:14`.
	SortingMode string

	// RemovesCompletedDownloads is true when the client itself is
	// configured to auto-remove completed downloads (vs Sonarr
	// removing them via RemoveItem). Mirrors C# `:15`.
	RemovesCompletedDownloads bool

	// OutputRootFolders is the list of base paths the client uses
	// for downloads (the SAB "complete dir", the qBittorrent
	// "default save path"). Used by the queue UI's path-mapping
	// helper. Mirrors C# `OutputRootFolders` at `:16`.
	OutputRootFolders []ospath.OsPath
}

// Settings is the marker interface every download-client settings
// type implements. Mirrors the C# `IProviderConfig` at
// `src/NzbDrone.Core/ThingiProvider/IProviderConfig.cs` — the C#
// interface requires a Validate() method; the Go port mirrors with
// [Settings.Validate].
//
// Concrete settings types (SabnzbdSettings, TransmissionSettings,
// etc.) embed [BaseSettings] for the common fields and implement
// Validate() to surface FluentValidation-style field-level failures.
//
// Two convenience marker subtypes — [UsenetSettings] and
// [TorrentSettings] — separate the two protocol families. A concrete
// client's settings type embeds one of those (typically by claiming
// `UsenetClientSettings` / `TorrentClientSettings` shape) so the
// registry can fan out per-protocol without runtime type assertions.
type Settings interface {
	// Validate runs FluentValidation-style field-level checks
	// against the settings instance. Returns the failures (empty
	// slice = valid). Mirrors C# `IProviderConfig.Validate()` /
	// `DownloadClientSettingsBase<T>.Validate()` at
	// `src/NzbDrone.Core/Download/Clients/DownloadClientSettingsBase.cs:13`.
	Validate() []TestFailure
}

// UsenetSettings is the marker interface usenet-protocol settings
// types implement. Mirrors the C# `UsenetClientSettings` shape
// implicit in `UsenetClientBase<TSettings>.cs:17` (the generic
// constraint isn't named in C# but is enforced by every concrete
// subclass).
type UsenetSettings interface {
	Settings

	// UsenetMarker is a zero-method tag that distinguishes usenet
	// settings from torrent settings at the type-system level. Go
	// has no inheritance, so this marker stands in for the C#
	// "abstract base class" relationship. Concrete usenet settings
	// types embed [BaseUsenetSettings] which provides the no-op
	// implementation.
	usenetMarker()
}

// TorrentSettings is the marker interface torrent-protocol settings
// types implement. Mirrors the C# `TorrentClientSettings` shape.
type TorrentSettings interface {
	Settings

	// TorrentMarker — see [UsenetSettings.usenetMarker].
	torrentMarker()
}

// BaseUsenetSettings is the embeddable struct usenet settings types
// can use to satisfy [UsenetSettings] without implementing the marker
// method on every concrete type. Zero-sized; concrete types compose
// it in by embedding.
type BaseUsenetSettings struct{}

// usenetMarker satisfies the unexported marker method on
// [UsenetSettings]. Never called directly — its presence is the
// type-system distinction between usenet and torrent settings shapes.
//
//nolint:unused // satisfies unexported UsenetSettings marker contract.
func (BaseUsenetSettings) usenetMarker() {}

// BaseTorrentSettings — see [BaseUsenetSettings].
type BaseTorrentSettings struct{}

// torrentMarker satisfies the unexported marker method on
// [TorrentSettings]. Never called directly — see
// [BaseUsenetSettings.usenetMarker].
//
//nolint:unused // satisfies unexported TorrentSettings marker contract.
func (BaseTorrentSettings) torrentMarker() {}

// TestFailure is one FluentValidation-style field-level failure
// returned by [Settings.Validate] or [Provider.Test]. Mirrors C#
// `ValidationFailure` at the Provider test boundary.
type TestFailure struct {
	// PropertyName is the dotted-path field this failure applies
	// to. Empty string for whole-form failures (e.g. "Test was
	// aborted due to an error: <message>" — see C#
	// `DownloadClientBase.cs:158-159`).
	PropertyName string

	// ErrorMessage is the human-readable failure description. The
	// v3 React UI renders this verbatim next to the field input.
	ErrorMessage string

	// DetailedDescription is the optional long-form explanation
	// some validators attach (e.g. the C# folder-not-writable
	// failure at `DownloadClientBase.cs:170-184`). Empty when no
	// long-form is available; the UI shows it under "More info" if
	// non-empty.
	DetailedDescription string
}
