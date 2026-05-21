package webhook

// EventType is the discriminator string the webhook payload's
// `eventType` field carries. Mirrors C# `WebhookEventType` enum at
// `src/NzbDrone.Core/Notifications/Webhook/WebhookEventType.cs:9-22`
// — the C# enum is wire-serialised with [StringEnumConverter +
// DefaultNamingStrategy] which preserves PascalCase, so the Go port
// uses the same literal strings.
//
// The TODO comment at C# `:7` notes the v4 plan is to switch to
// camelCase; the Go port mirrors the current wire format exactly
// (PascalCase) so existing webhook consumers don't break.
type EventType string

// EventType discriminator values mirror the C# enum names at
// `WebhookEventType.cs:11-21` exactly — the v3 wire format
// uses PascalCase per the StringEnumConverter applied at the C#
// enum level.
const (
	EventTypeTest                      EventType = "Test"
	EventTypeGrab                      EventType = "Grab"
	EventTypeDownload                  EventType = "Download"
	EventTypeRename                    EventType = "Rename"
	EventTypeSeriesAdd                 EventType = "SeriesAdd"
	EventTypeSeriesDelete              EventType = "SeriesDelete"
	EventTypeEpisodeFileDelete         EventType = "EpisodeFileDelete"
	EventTypeHealth                    EventType = "Health"
	EventTypeApplicationUpdate         EventType = "ApplicationUpdate"
	EventTypeHealthRestored            EventType = "HealthRestored"
	EventTypeManualInteractionRequired EventType = "ManualInteractionRequired"
)

// Method is the HTTP method int the C# UI Select widget posts via
// [Settings.Method]. Mirrors C# `WebhookMethod` enum at
// `Notifications/Webhook/WebhookMethod.cs:3-7`.
type Method int

const (
	// MethodPOST = 1 mirrors C# `WebhookMethod.POST = 1`.
	MethodPOST Method = 1
	// MethodPUT = 2 mirrors C# `WebhookMethod.PUT = 2`.
	MethodPUT Method = 2
)

// BasePayload is the shared shape every webhook event carries.
// Mirrors C# `WebhookPayload` at
// `Notifications/Webhook/WebhookPayload.cs:3-8`.
//
// Per-event payloads embed this struct and add their own fields.
// JSON field-name order matches the C# Newtonsoft default
// serialisation (PascalCase) for wire-format compatibility with
// existing consumers — see [EventType] note about the v4 camelCase
// plan.
type BasePayload struct {
	// EventType is the discriminator the consumer switches on.
	EventType EventType `json:"eventType"`

	// InstanceName is the configured Sonarr instance label
	// (config file `InstanceName`). C# WebhookBase pulls this from
	// `IConfigFileProvider.InstanceName` at WebhookBase.cs:40+;
	// the Go port reads from config.Provider when wired (queue
	// [N4] orchestrator binds this).
	InstanceName string `json:"instanceName"`

	// ApplicationUrl is the configured external Sonarr URL
	// (the URL users browse to). C# WebhookBase pulls from
	// `IConfigService.ApplicationUrl`. Omitted on Health payloads
	// (the C# `BuildHealthPayload` at WebhookBase.cs:160-170
	// leaves it null).
	ApplicationURL string `json:"applicationUrl"`
}

// GrabPayload is the OnGrab event body. Mirrors C#
// `WebhookGrabPayload` at
// `Notifications/Webhook/WebhookGrabPayload.cs:6-14`.
//
// Series/Episodes/Release/CustomFormatInfo sub-objects are typed
// as `any` because the C# Webhook* sub-payload types
// (WebhookSeries, WebhookEpisode, WebhookRelease,
// WebhookCustomFormatInfo) depend on parser.RemoteEpisode +
// customformats which haven't ported. The Go port carries the
// raw values through unchanged — concrete consumers can serialise
// whatever shape the upstream porter emits.
//
// DownloadClient / DownloadClientType / DownloadID carry
// `omitempty` so empty-string values drop the key. C# sources these
// from `message.DownloadClientName` / `DownloadClientType` /
// `DownloadId` at `WebhookBase.cs:45-47` — all `string` reference-
// type properties that default to null when the grab pipeline has
// no download client info (e.g. manual-import grabs); Newtonsoft's
// `NullValueHandling.Ignore` at `Json.cs:26` (via
// `WebhookProxy.SendWebhook` → `body.ToJson()` at
// `WebhookProxy.cs:39`) drops the keys in those cases. Go strings
// default to `""` rather than null, so without `omitempty` the
// wire body would carry `"downloadClient":""` etc. when the Go
// equivalent of the C# null is in play.
type GrabPayload struct {
	BasePayload
	Series             any    `json:"series"`
	Episodes           any    `json:"episodes"`
	Release            any    `json:"release"`
	DownloadClient     string `json:"downloadClient,omitempty"`
	DownloadClientType string `json:"downloadClientType,omitempty"`
	DownloadID         string `json:"downloadId,omitempty"`
	CustomFormatInfo   any    `json:"customFormatInfo"`
}

// ImportPayload is the OnDownload event body. Mirrors C#
// `WebhookImportPayload` at
// `Notifications/Webhook/WebhookImportPayload.cs:6-16`. The C#
// payload is shared with the OnImportComplete event but at a
// different EventType discriminator (Download vs Download for
// per-file, with the OnImportComplete path emitting a separate
// [ImportCompletePayload] in the C# code at
// `WebhookImportCompletePayload.cs`).
//
// DownloadClient / DownloadClientType / DownloadID carry
// `omitempty` for the same reason documented on [GrabPayload]: C#
// sources them from `message.DownloadClientInfo?.Name` /
// `?.Type` / `message.DownloadId` at `WebhookBase.cs:69-71` —
// nullable strings dropped by `NullValueHandling.Ignore` when the
// download-client info is absent.
type ImportPayload struct {
	BasePayload
	Series             any    `json:"series"`
	Episodes           any    `json:"episodes"`
	EpisodeFile        any    `json:"episodeFile"`
	IsUpgrade          bool   `json:"isUpgrade"`
	DownloadClient     string `json:"downloadClient,omitempty"`
	DownloadClientType string `json:"downloadClientType,omitempty"`
	DownloadID         string `json:"downloadId,omitempty"`
	DeletedFiles       any    `json:"deletedFiles"`
	CustomFormatInfo   any    `json:"customFormatInfo"`
	Release            any    `json:"release"`
}

// ImportCompletePayload is the OnImportComplete event body.
// Mirrors C# `WebhookImportCompletePayload` at
// `Notifications/Webhook/WebhookImportCompletePayload.cs:6-16`.
//
// DownloadClient / DownloadClientType / DownloadID carry
// `omitempty` per the [GrabPayload] rationale; C# sources at
// `WebhookBase.cs:100-102` (`message.DownloadClientInfo?.Name` /
// `?.Type` / `message.DownloadId`).
type ImportCompletePayload struct {
	BasePayload
	Series             any    `json:"series"`
	Episodes           any    `json:"episodes"`
	EpisodeFiles       any    `json:"episodeFiles"`
	DownloadClient     string `json:"downloadClient,omitempty"`
	DownloadClientType string `json:"downloadClientType,omitempty"`
	DownloadID         string `json:"downloadId,omitempty"`
	SourcePath         string `json:"sourcePath"`
	DestinationPath    string `json:"destinationPath"`
	Release            any    `json:"release"`
}

// RenamePayload is the OnRename event body. Mirrors C#
// `WebhookRenamePayload` at
// `Notifications/Webhook/WebhookRenamePayload.cs:5-8`.
type RenamePayload struct {
	BasePayload
	Series              any `json:"series"`
	RenamedEpisodeFiles any `json:"renamedEpisodeFiles"`
}

// EpisodeDeletePayload is the OnEpisodeFileDelete event body.
// Mirrors C# `WebhookEpisodeDeletePayload` at
// `Notifications/Webhook/WebhookEpisodeDeletePayload.cs:6-11`.
type EpisodeDeletePayload struct {
	BasePayload
	Series       any    `json:"series"`
	Episodes     any    `json:"episodes"`
	EpisodeFile  any    `json:"episodeFile"`
	DeleteReason string `json:"deleteReason"`
}

// SeriesAddPayload is the OnSeriesAdd event body. Mirrors C#
// `WebhookSeriesAddPayload` at
// `Notifications/Webhook/WebhookSeriesAddPayload.cs:5-7`.
type SeriesAddPayload struct {
	BasePayload
	Series any `json:"series"`
}

// SeriesDeletePayload is the OnSeriesDelete event body. Mirrors
// C# `WebhookSeriesDeletePayload` at
// `Notifications/Webhook/WebhookSeriesDeletePayload.cs:5-9`.
type SeriesDeletePayload struct {
	BasePayload
	Series       any  `json:"series"`
	DeletedFiles bool `json:"deletedFiles"`
}

// HealthPayload is the OnHealthIssue / OnHealthRestored event
// body. Mirrors C# `WebhookHealthPayload` at
// `Notifications/Webhook/WebhookHealthPayload.cs:5-11`. The Level
// field carries the C# `HealthCheckResult` enum as a string
// (Ok/Notice/Warning/Error — see C# enum at
// `HealthCheck/HealthCheckResult.cs`).
//
// Unlike the other per-event payloads HealthPayload does NOT embed
// [BasePayload] — it inlines EventType + InstanceName but omits
// ApplicationURL. C# `BuildHealthPayload` at `WebhookBase.cs:159-170`
// + `BuildHealthRestoredPayload` at `:172-183` explicitly do NOT set
// `WebhookPayload.ApplicationUrl`, leaving it as the C# `string`
// reference-type default (null). The webhook proxy serializes via
// `payload.ToJson()` at `WebhookProxy.cs:39`, routed through
// Newtonsoft's `ToJson` extension at
// `src/NzbDrone.Common/Serializer/Newtonsoft.Json/Json.cs:119` using
// the shared `SerializerSettings` with `NullValueHandling = Ignore`
// at `Json.cs:26`, so the null `applicationUrl` property is OMITTED
// from the wire body. Embedding [BasePayload] would emit
// `"applicationUrl":""` (Go strings default to "", not null), which
// would diverge from the C# omit. Listing the two shared base fields
// inline preserves field order (eventType / instanceName then
// per-event fields) byte-for-byte with C#.
type HealthPayload struct {
	EventType    EventType `json:"eventType"`
	InstanceName string    `json:"instanceName"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Type         string    `json:"type"`
	WikiURL      string    `json:"wikiUrl"`
}

// ApplicationUpdatePayload is the OnApplicationUpdate event body.
// Mirrors C# `WebhookApplicationUpdatePayload` at
// `Notifications/Webhook/WebhookApplicationUpdatePayload.cs:5-8`.
type ApplicationUpdatePayload struct {
	BasePayload
	Message         string `json:"message"`
	PreviousVersion string `json:"previousVersion"`
	NewVersion      string `json:"newVersion"`
}

// ManualInteractionPayload is the OnManualInteractionRequired
// event body. Mirrors C# `WebhookManualInteractionPayload` at
// `Notifications/Webhook/WebhookManualInteractionPayload.cs:6-16`.
//
// DownloadClient / DownloadClientType / DownloadID carry
// `omitempty` per the [GrabPayload] rationale; C# sources at
// `WebhookBase.cs:210-212` (`message.DownloadClientInfo?.Name` /
// `?.Type` / `message.DownloadId`).
type ManualInteractionPayload struct {
	BasePayload
	Series                 any    `json:"series"`
	Episodes               any    `json:"episodes"`
	DownloadInfo           any    `json:"downloadInfo"`
	DownloadClient         string `json:"downloadClient,omitempty"`
	DownloadClientType     string `json:"downloadClientType,omitempty"`
	DownloadID             string `json:"downloadId,omitempty"`
	DownloadStatus         string `json:"downloadStatus"`
	DownloadStatusMessages any    `json:"downloadStatusMessages"`
	CustomFormatInfo       any    `json:"customFormatInfo"`
	Release                any    `json:"release"`
}
