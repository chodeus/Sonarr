package notifications

import "context"

// Provider is the runtime notification abstraction concrete
// implementations (Webhook, Discord, Slack, Email, Pushover, etc.)
// satisfy. Mirrors the C# `INotification` interface at
// `src/NzbDrone.Core/Notifications/INotification.cs:8-37` plus the
// abstract C# `NotificationBase<TSettings>` no-op defaults at
// `src/NzbDrone.Core/Notifications/NotificationBase.cs:48-94`.
//
// Slice 4.AD (sub-arc N2b of queue [N2]) — the package-base slice
// ships the interface contract + Constructor + Dependencies + Registry.
// Concrete providers (Webhook + Discord) land in queue [N3]; the
// per-event fan-out orchestrator (queue [N4] — NotificationService)
// drives this interface.
//
// Method semantics summary (full C# cites on each method):
//
//   - Name, Link, Definition mirror the C# IProvider base contract.
//   - OnX hooks receive a per-event payload struct (see message.go).
//     Concrete providers override only the hooks they support; the
//     base contract returns nil-error from any non-overridden hook.
//   - SupportsOnX reports compile-time capability — C# uses
//     reflection (`HasConcreteImplementation` at
//     `NotificationBase.cs:122-132`); the Go port pushes this to
//     the concrete provider since Go has no abstract-base
//     reflection idiom. Each concrete provider returns a constant.
//   - Test executes the per-provider connection self-check. Mirrors
//     C# `ValidationResult Test()` at `INotification.cs` / each
//     concrete `Test()` override.
//   - ProcessQueue drains the MediaServerUpdateQueue (Kodi/Plex/Emby
//     library-refresh coalescing buffer). C# cite:
//     `NotificationBase.cs:92-94` / `NotificationService.cs:451-454`.
//     Most providers return immediately; only library-refresh
//     providers (Plex/Kodi/Emby/Synology) have a non-trivial body.
//
// Goroutine-safety: implementations MUST tolerate concurrent OnX
// calls from the fan-out orchestrator's worker pool. Test +
// ProcessQueue are called from a single goroutine each.
//
// Error convention: each OnX hook returns a single error. Mirrors
// the C# fan-out's per-provider try/catch at
// `NotificationService.cs:60-70` — a non-nil error is logged and
// recorded via [notificationstatus.Recorder] but does NOT abort
// the fan-out chain. Providers should NOT panic on bad input;
// return an error instead.
type Provider interface {
	// Name returns the user-facing display label. Mirrors C#
	// `Name { get; }` at `NotificationBase.cs:35`.
	Name() string

	// Link returns the per-provider settings/documentation link
	// the v3 UI renders alongside the provider's row. Mirrors C#
	// `Link { get; }` at `INotification.cs:10` / `NotificationBase.cs:46`.
	Link() string

	// Definition returns the persisted Notifications row backing
	// this provider instance. Mirrors C# `Definition { get; set; }`
	// at `NotificationBase.cs:43`. Nil from a test-constructed
	// provider that hasn't been bound to a row.
	Definition() *Notification

	// OnGrab fires when the grab pipeline (queue [15] —
	// ProcessDownloadDecisions) successfully hands a release to a
	// download client. Mirrors C#
	// `void OnGrab(GrabMessage grabMessage)` at
	// `INotification.cs:12`.
	OnGrab(ctx context.Context, msg GrabMessage) error

	// OnDownload fires when a downloaded file completes import.
	// Mirrors C# `void OnDownload(DownloadMessage message)` at
	// `INotification.cs:13`.
	OnDownload(ctx context.Context, msg DownloadMessage) error

	// OnRename fires after a series rename completes. Mirrors C#
	// `void OnRename(Series, List<RenamedEpisodeFile>)` at
	// `INotification.cs:14`. The Go port wraps both args into a
	// [RenameMessage] for hook-signature parallelism — see
	// [RenameMessage] doc-comment.
	OnRename(ctx context.Context, msg RenameMessage) error

	// OnImportComplete fires ONCE per import session after the
	// per-file OnDownload hooks complete. Mirrors C#
	// `void OnImportComplete(ImportCompleteMessage)` at
	// `INotification.cs:15`.
	OnImportComplete(ctx context.Context, msg ImportCompleteMessage) error

	// OnEpisodeFileDelete fires when an EpisodeFile row is deleted
	// (manually or by an upgrade). Mirrors C#
	// `void OnEpisodeFileDelete(EpisodeDeleteMessage)` at
	// `INotification.cs:16`.
	OnEpisodeFileDelete(ctx context.Context, msg EpisodeDeleteMessage) error

	// OnSeriesAdd fires after a series is added to the library.
	// Mirrors C# `void OnSeriesAdd(SeriesAddMessage)` at
	// `INotification.cs:17`.
	OnSeriesAdd(ctx context.Context, msg SeriesAddMessage) error

	// OnSeriesDelete fires after a series is removed from the
	// library. Mirrors C# `void OnSeriesDelete(SeriesDeleteMessage)`
	// at `INotification.cs:18`.
	OnSeriesDelete(ctx context.Context, msg SeriesDeleteMessage) error

	// OnHealthIssue fires when a health-check transitions from ok
	// to failing. Mirrors C#
	// `void OnHealthIssue(HealthCheck.HealthCheck)` at
	// `INotification.cs:19`. The Go port wraps the check in
	// [HealthCheckMessage] for parallel-hook signatures.
	OnHealthIssue(ctx context.Context, msg HealthCheckMessage) error

	// OnHealthRestored fires when a health-check transitions from
	// failing to ok. Mirrors C#
	// `void OnHealthRestored(HealthCheck.HealthCheck previousCheck)`
	// at `INotification.cs:20`. Carries the PRIOR failing check
	// (per the C# arg name `previousCheck`).
	OnHealthRestored(ctx context.Context, msg HealthCheckMessage) error

	// OnApplicationUpdate fires after the in-process updater
	// installs a new version. Mirrors C#
	// `void OnApplicationUpdate(ApplicationUpdateMessage)` at
	// `INotification.cs:21`.
	OnApplicationUpdate(ctx context.Context, msg ApplicationUpdateMessage) error

	// OnManualInteractionRequired fires when the download/import
	// pipeline stalls and needs user intervention. Mirrors C#
	// `void OnManualInteractionRequired(ManualInteractionRequiredMessage)`
	// at `INotification.cs:22`.
	OnManualInteractionRequired(ctx context.Context, msg ManualInteractionRequiredMessage) error

	// ProcessQueue drains the per-provider library-refresh
	// coalescing buffer (Kodi/Plex/Emby). Mirrors C#
	// `void ProcessQueue()` at `INotification.cs:23` /
	// `NotificationBase.cs:92-94`. No-op for non-library-refresh
	// providers.
	ProcessQueue(ctx context.Context) error

	// Test executes the per-provider connection self-check.
	// Returns the validation failures (empty slice = success).
	// Mirrors C# `ValidationResult Test()` at each concrete
	// provider. An error return is reserved for "test was aborted
	// due to an unexpected exception" — the C# code at
	// `NotificationFactory.cs:210-229` records a RecordFailure
	// on any non-valid result.
	Test(ctx context.Context) ([]TestFailure, error)

	// SupportsOnGrab reports whether the provider implements
	// OnGrab non-trivially. Mirrors C#
	// `SupportsOnGrab { get; }` at `INotification.cs:24`. C# uses
	// runtime reflection (`HasConcreteImplementation("OnGrab")`);
	// Go has no equivalent idiom, so each concrete provider
	// returns a constant.
	SupportsOnGrab() bool

	// SupportsOnDownload — see [Provider.SupportsOnGrab].
	SupportsOnDownload() bool

	// SupportsOnUpgrade — see [Provider.SupportsOnGrab]. The C#
	// base ties this to SupportsOnDownload at
	// `NotificationBase.cs:99`; concrete providers may diverge.
	SupportsOnUpgrade() bool

	// SupportsOnImportComplete — see [Provider.SupportsOnGrab].
	SupportsOnImportComplete() bool

	// SupportsOnRename — see [Provider.SupportsOnGrab].
	SupportsOnRename() bool

	// SupportsOnSeriesAdd — see [Provider.SupportsOnGrab].
	SupportsOnSeriesAdd() bool

	// SupportsOnSeriesDelete — see [Provider.SupportsOnGrab].
	SupportsOnSeriesDelete() bool

	// SupportsOnEpisodeFileDelete — see [Provider.SupportsOnGrab].
	SupportsOnEpisodeFileDelete() bool

	// SupportsOnEpisodeFileDeleteForUpgrade — see
	// [Provider.SupportsOnGrab]. C# ties this to
	// SupportsOnEpisodeFileDelete at `NotificationBase.cs:104`;
	// concrete providers may diverge.
	SupportsOnEpisodeFileDeleteForUpgrade() bool

	// SupportsOnHealthIssue — see [Provider.SupportsOnGrab].
	SupportsOnHealthIssue() bool

	// SupportsOnHealthRestored — see [Provider.SupportsOnGrab].
	SupportsOnHealthRestored() bool

	// SupportsOnApplicationUpdate — see [Provider.SupportsOnGrab].
	SupportsOnApplicationUpdate() bool

	// SupportsOnManualInteractionRequired — see [Provider.SupportsOnGrab].
	SupportsOnManualInteractionRequired() bool
}

// RenameMessage wraps the (Series, []RenamedFile) pair the C#
// OnRename hook takes as two positional args. The Go port packs
// them into a single struct so the OnX hook signatures stay
// parallel (one context + one payload).
//
// Mirrors C# `void OnRename(Series series, List<RenamedEpisodeFile> renamedFiles)`
// at `INotification.cs:14` and the C# `RenamedEpisodeFile` type at
// `src/NzbDrone.Core/MediaFiles/RenamedEpisodeFile.cs` (not yet
// ported — the slice carries the rename records as `any` per the
// downloadclients.GrabRequest.Subject precedent).
type RenameMessage struct {
	// Series is the renamed series.
	Series any

	// RenamedFiles is the list of per-file rename records. Each
	// entry carries the prior + new on-disk paths. Typed as []any
	// since MediaFiles.RenamedEpisodeFile hasn't ported; the
	// orchestrator at queue [N4] populates this from the
	// SeriesRenamedEvent the renamer publishes.
	RenamedFiles []any
}

// Constructor builds a [Provider] instance from a persisted
// [Notification] row + the cross-provider [Dependencies] bundle.
// Concrete provider packages (Webhook, Discord — queue [N3] and
// follow-ups) register one Constructor per (Implementation,
// ConfigContract) pair at init() time.
//
// Mirrors the C# pattern where `IServiceProvider` resolves
// `INotification` instances by (Type, Settings) pair at
// `ProviderFactory<TProvider,TProviderDefinition>.GetInstance`
// (in the ThingiProvider base class, outside the Notifications
// directory).
//
// Returning an error covers the same two failure modes as
// [downloadclients.Constructor]:
//
//   - Settings JSON couldn't deserialise into the per-provider
//     Settings struct.
//   - A required dependency in [Dependencies] is nil and the
//     concrete provider refuses to construct without it.
type Constructor func(definition *Notification, deps Dependencies) (Provider, error)

// Dependencies bundles the cross-provider deps every concrete
// notification constructor receives. Mirrors the parameter pack
// shared across `NotificationBase<TSettings>` derivatives in the
// C# tree (each concrete constructor takes IHttpClient + Logger
// at minimum; some take ICacheManager / IConfigService / etc).
//
// Fields are typed as `any` so the actual cross-package dep types
// (httpclient.Client, slog.Logger, config.Provider, etc.) don't
// drag this package's import graph through every consumer. Concrete
// provider packages cast to the typed shape they need at
// construction time. Nil values are explicitly tolerated when the
// per-provider constructor doesn't reach for that dep.
//
// The named struct here is the contract; we evolve it additively
// as concrete providers land.
type Dependencies struct {
	// HTTPClient is the HTTP transport every webhook-style
	// provider uses to reach its REST endpoint. Bound to
	// `*httpclient.Client` in production. Mirrors C# `IHttpClient`
	// injected into every notification constructor.
	HTTPClient any

	// ConfigProvider is the application-config getter. Bound to
	// `*config.Provider` in production. A handful of providers
	// (Plex/Kodi for the localhost-detection check) read config.
	ConfigProvider any

	// Logger is the slog.Logger handle the concrete provider emits
	// against. Bound to the host-supplied logger in production.
	Logger any

	// StatusService is the [*notificationstatus.Service] for
	// back-off escalation. Bound by main.go. The fan-out
	// orchestrator (queue [N4]) calls RecordSuccess /
	// RecordFailure / RecordConnectionFailure on per-provider
	// outcomes; concrete providers do NOT call this directly.
	StatusService any

	// CacheManager backs the MediaServerUpdateQueue coalescing
	// buffer for Kodi/Plex/Emby/Synology library-refresh
	// providers. Mirrors C# `ICacheManager.GetRollingCache(...)` at
	// `Notifications/MediaServerUpdateQueue.cs`. Nil for the
	// Webhook/Discord pair shipped in queue [N3] — they have no
	// library-refresh coalescing.
	CacheManager any

	// NotificationsRepo is the persistence handle the OAuth2 provider
	// (Trakt) uses to round-trip a refreshed AccessToken /
	// RefreshToken / Expires triple back to the DB row. Bound to
	// `*notifications.Repo` in production. Without this dep, refreshed
	// tokens live in process memory only — next provider construction
	// reads the stale token from DB and refreshes again. Nil is
	// tolerated; providers fall back to in-memory-only updates.
	//
	// Mirrors C# `_notificationRepository.UpdateSettings(...)` at
	// `Notifications/Trakt/Trakt.cs:155`.
	NotificationsRepo any
}
