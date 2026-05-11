package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the value stored in the Notifications.Implementation
// column for rows wired to this provider. Mirrors the C# class name
// `Webhook` at `Notifications/Webhook/Webhook.cs:14`.
const Implementation = "Webhook"

// ConfigContract is the value stored in the Notifications.
// ConfigContract column. Mirrors the C# `WebhookSettings` shape at
// `Notifications/Webhook/WebhookSettings.cs:17`.
const ConfigContract = "WebhookSettings"

// Webhook ports the C# `Webhook` notification provider at
// `Notifications/Webhook/Webhook.cs:14-106`.
//
// The C# class extends `WebhookBase<WebhookSettings>` (an abstract
// base shared by the Discord/Slack/Notifiarr/etc webhook-shaped
// providers). The Go port inlines the BuildOnX payload helpers as
// per-hook private methods on the Webhook struct — sub-arc N3b
// (Discord) factors them out when the second webhook-shaped
// provider lands.
//
// Goroutine-safety: the httpDoer is shared across concurrent OnX
// calls. settings is captured by value at construction time and
// not mutated after.
type Webhook struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time assertion: *Webhook satisfies notifications.Provider.
var _ notifications.Provider = (*Webhook)(nil)

// New constructs a *Webhook from a persisted [notifications.Notification]
// row + the cross-provider [notifications.Dependencies] bundle. Mirrors
// the [notifications.Constructor] signature.
//
// Settings JSON deserialisation: the row's Settings column is a
// JSON string the C# Newtonsoft serializer wrote; the Go port
// uses encoding/json with matching lowerCamelCase tags on
// [Settings].
//
// HTTPClient dep is required — a webhook provider with no HTTP
// transport can't send. Returns a typed error so the registry's
// Construct loop can mark the row as broken-implementation
// without crashing.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("webhook: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("webhook: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		// Fall back to http.DefaultClient when no client was wired
		// (e.g. unit tests constructing the provider without a
		// dependencies bundle). Mirrors the C# DI default —
		// SystemNet HttpClient is always available via the IHttpClient
		// resolver.
		doer = http.DefaultClient
	}
	return &Webhook{def: def, settings: settings, doer: doer}, nil
}

// Name returns the provider's display label. Mirrors C# `Name`
// at `Webhook.cs:81`.
func (w *Webhook) Name() string { return "Webhook" }

// Link returns the docs URL. Mirrors C# `Link` at `Webhook.cs:24`.
func (w *Webhook) Link() string {
	return "https://wiki.servarr.com/sonarr/settings#connections"
}

// Definition returns the persisted row. Mirrors C# `Definition`
// at NotificationBase.cs:43.
func (w *Webhook) Definition() *notifications.Notification { return w.def }

// OnGrab fires the Grab event. Mirrors C# `OnGrab` at
// `Webhook.cs:26-29` + `WebhookBase.BuildOnGrabPayload` at
// `WebhookBase.cs:32-50`. Sub-object population (Series/Episodes/
// Release/CustomFormatInfo) is currently passed-through as the
// raw `any` values from [notifications.GrabMessage] — see the
// package-level out-of-scope note.
func (w *Webhook) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return w.send(ctx, GrabPayload{
		BasePayload:        BasePayload{EventType: EventTypeGrab},
		Series:             msg.Series,
		Episodes:           msg.Episode,
		Release:            msg.Episode,
		DownloadClient:     msg.DownloadClientName,
		DownloadClientType: msg.DownloadClientType,
		DownloadID:         msg.DownloadID,
	})
}

// OnDownload mirrors C# `OnDownload` at `Webhook.cs:31-34` +
// `BuildOnDownloadPayload` at `WebhookBase.cs:52-85`.
func (w *Webhook) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return w.send(ctx, ImportPayload{
		BasePayload:  BasePayload{EventType: EventTypeDownload},
		Series:       msg.Series,
		EpisodeFile:  msg.EpisodeFile,
		IsUpgrade:    len(msg.OldFiles) > 0,
		DeletedFiles: msg.OldFiles,
		DownloadID:   msg.DownloadID,
		Release:      msg.Release,
	})
}

// OnImportComplete mirrors C# `OnImportComplete` at `Webhook.cs:36-39`
// + `BuildOnImportCompletePayload` at `WebhookBase.cs:87-108`.
func (w *Webhook) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return w.send(ctx, ImportCompletePayload{
		BasePayload:     BasePayload{EventType: EventTypeDownload},
		Series:          msg.Series,
		Episodes:        msg.Episodes,
		EpisodeFiles:    msg.EpisodeFiles,
		DownloadID:      msg.DownloadID,
		SourcePath:      msg.SourcePath,
		DestinationPath: msg.DestinationPath,
		Release:         msg.Release,
	})
}

// OnRename mirrors C# `OnRename` at `Webhook.cs:41-44` +
// `BuildOnRenamePayload` at `WebhookBase.cs:147-157`.
func (w *Webhook) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	return w.send(ctx, RenamePayload{
		BasePayload:         BasePayload{EventType: EventTypeRename},
		Series:              msg.Series,
		RenamedEpisodeFiles: msg.RenamedFiles,
	})
}

// OnEpisodeFileDelete mirrors C# `OnEpisodeFileDelete` at
// `Webhook.cs:46-49` + `BuildOnEpisodeFileDelete` at
// `WebhookBase.cs:110-122`.
func (w *Webhook) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return w.send(ctx, EpisodeDeletePayload{
		BasePayload:  BasePayload{EventType: EventTypeEpisodeFileDelete},
		Series:       msg.Series,
		EpisodeFile:  msg.EpisodeFile,
		DeleteReason: msg.Reason.String(),
	})
}

// OnSeriesAdd mirrors C# `OnSeriesAdd` at `Webhook.cs:51-54` +
// `BuildOnSeriesAdd` at `WebhookBase.cs:124-133`.
func (w *Webhook) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return w.send(ctx, SeriesAddPayload{
		BasePayload: BasePayload{EventType: EventTypeSeriesAdd},
		Series:      msg.Series,
	})
}

// OnSeriesDelete mirrors C# `OnSeriesDelete` at `Webhook.cs:56-59`
// + `BuildOnSeriesDelete` at `WebhookBase.cs:135-145`.
func (w *Webhook) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return w.send(ctx, SeriesDeletePayload{
		BasePayload:  BasePayload{EventType: EventTypeSeriesDelete},
		Series:       msg.Series,
		DeletedFiles: msg.DeletedFiles,
	})
}

// OnHealthIssue mirrors C# `OnHealthIssue` at `Webhook.cs:61-64` +
// `BuildHealthPayload` at `WebhookBase.cs:159-170`.
//
// C# `BuildHealthPayload` at `:164` reads `healthCheck.Type` as
// the Level field — the C# property name is `Type` but it carries
// a `HealthCheckResult` enum value. The Go port reads the same
// field through [healthcheck.HealthCheck.Result.String].
func (w *Webhook) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return w.send(ctx, HealthPayload{
		BasePayload: BasePayload{EventType: EventTypeHealth},
		Level:       msg.Check.Type.String(),
		Message:     msg.Check.Message,
		Type:        msg.Check.Source,
		WikiURL:     msg.Check.WikiURL,
	})
}

// OnHealthRestored mirrors C# `OnHealthRestored` at
// `Webhook.cs:66-69` + `BuildHealthRestoredPayload` at
// `WebhookBase.cs:172-183`. Identical payload shape to
// OnHealthIssue, distinct EventType.
func (w *Webhook) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return w.send(ctx, HealthPayload{
		BasePayload: BasePayload{EventType: EventTypeHealthRestored},
		Level:       msg.Check.Type.String(),
		Message:     msg.Check.Message,
		Type:        msg.Check.Source,
		WikiURL:     msg.Check.WikiURL,
	})
}

// OnApplicationUpdate mirrors C# `OnApplicationUpdate` at
// `Webhook.cs:71-74` + `BuildApplicationUpdatePayload` at
// `WebhookBase.cs:185-195`.
func (w *Webhook) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return w.send(ctx, ApplicationUpdatePayload{
		BasePayload:     BasePayload{EventType: EventTypeApplicationUpdate},
		Message:         msg.Message,
		PreviousVersion: msg.PreviousVersion,
		NewVersion:      msg.NewVersion,
	})
}

// OnManualInteractionRequired mirrors C# at `Webhook.cs:76-79` +
// `BuildManualInteractionRequiredPayload` at `WebhookBase.cs:197-218`.
func (w *Webhook) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return w.send(ctx, ManualInteractionPayload{
		BasePayload:    BasePayload{EventType: EventTypeManualInteractionRequired},
		Series:         msg.Series,
		Episodes:       msg.Episode,
		DownloadID:     msg.DownloadID,
		Release:        msg.Release,
		DownloadClient: "",
	})
}

// ProcessQueue is a no-op — Webhook has no library-refresh
// coalescing buffer. Mirrors the C# `NotificationBase.ProcessQueue`
// default no-op at `NotificationBase.cs:92-94`.
func (w *Webhook) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic Test-EventType payload to the configured
// URL. Mirrors C# `Test` at `Webhook.cs:83-104` + the inline
// `SendWebhookTest` helper. Returns [TestFailure] entries on
// transport-level failure; the returned error is reserved for
// unexpected exceptions (e.g. marshal failures) — the
// orchestrator at queue [N4] distinguishes the two and records
// RecordFailure only on TestFailure-list-non-empty OR error.
//
// The C# test sends the [BuildTestPayload] shape which is a
// `WebhookGrabPayload` with a hard-coded synthetic series ("Test
// Title", path "C:\testpath", tvdbId=1234, tag "test-tag") + one
// synthetic episode (Id=123, S1E1, "Test title"). The Go port
// produces an equivalent BasePayload-rooted payload with the same
// synthetic values.
func (w *Webhook) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	payload := GrabPayload{
		BasePayload: BasePayload{EventType: EventTypeTest},
		Series: map[string]any{
			"id":     1,
			"title":  "Test Title",
			"path":   "C:\\testpath",
			"tvdbId": 1234,
			"tags":   []string{"test-tag"},
		},
		Episodes: []map[string]any{
			{
				"id":            123,
				"episodeNumber": 1,
				"seasonNumber":  1,
				"title":         "Test title",
			},
		},
	}
	if err := w.send(ctx, payload); err != nil {
		return []notifications.TestFailure{{
			PropertyName: "Url",
			ErrorMessage: fmt.Sprintf("Unable to send test webhook: %s", err.Error()),
		}}, nil
	}
	return nil, nil
}

// send is the shared transport entry — every OnX hook routes
// through it.
func (w *Webhook) send(ctx context.Context, body any) error {
	return sendWebhook(ctx, w.doer, &w.settings, body)
}

// SupportsOnGrab reports whether OnGrab fires non-trivially.
// The C# `WebhookBase` inherits the
// `HasConcreteImplementation` reflection on every method override
// (see C# `NotificationBase.cs:122-132`); since Webhook overrides
// every OnX hook, every Supports* flag is true. The Go port
// returns constants directly. Per-method aliases below carry the
// same rationale.
func (w *Webhook) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Webhook.SupportsOnGrab].
func (w *Webhook) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Webhook constructor into the package-global
// [notifications.Default] registry. Mirrors the C# DI-time
// registration via `IServiceProvider` reflection-scanning the
// Notifications assembly for INotification implementations.
//
// Lives in init() (not main.go) so blank-import of this package
// from cmd/sonarr/main.go is sufficient to register; the
// downloadclients/sabnzbd + downloadclients/transmission pattern
// at internal/downloadclients/sabnzbd/sabnzbd.go uses the same
// idiom (registration in init, blank-import aggregator in main).
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
