# Notifications

**Source:** `src/NzbDrone.Core/Notifications/**`
**Target Go package:** `go/internal/notifications/<provider>/`
**Status:** drafted

## Purpose
Notifications are Sonarr's outbound side-channel: every interesting lifecycle event (release grabbed, episode imported/upgraded, file deleted, series added/removed, library renamed, application updated, health-check failure/restoration, manual interaction required) is fanned out to a configurable set of provider plug-ins. Each provider knows how to translate the same event payload into its own transport — chat webhooks (Discord, Slack), push services (Pushover, Pushbullet, Pushcut, Prowl, Join, Simplepush, Gotify, Ntfy, Apprise, Pushcut, Notifiarr), email (SMTP, SendGrid, Mailgun), media-server library refreshes (Plex, Emby/Jellyfin, Kodi/XBMC, Synology), tracker collection sync (Trakt), generic webhook, social (Twitter), and shell-out (CustomScript). The subsystem is configured through the `ThingiProvider` framework (see `datastore.md`, `embedded-docs/catalogue.md`) and persists `NotificationDefinition` rows with a per-provider `*Settings` JSON payload.

## Architecture

### Core types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `INotification : IProvider` | `Notifications/INotification.cs` | interface | Declares 11 `OnX` event hooks plus a `ProcessQueue()` and 13 `SupportsOnX` capability flags. |
| `NotificationBase<TSettings>` | `Notifications/NotificationBase.cs` | abstract base | Provides default `virtual` no-op implementations of every `OnX`. `SupportsOnX` is computed by reflection: `HasConcreteImplementation(methodName)` checks whether the method's `DeclaringType` is non-abstract — i.e. whether the concrete provider overrides it. Holds branded/un-branded title constants (`EPISODE_GRABBED_TITLE`, `EPISODE_GRABBED_TITLE_BRANDED = "Sonarr - " + EPISODE_GRABBED_TITLE`, …). |
| `WebhookBase<TSettings>` | `Notifications/Webhook/WebhookBase.cs` | abstract | Subclass of `NotificationBase` that builds typed payload DTOs (`WebhookGrabPayload`, `WebhookImportPayload`, `WebhookEpisodeDeletePayload`, etc.). Reused by `Webhook` and `Notifiarr`. |
| `INotificationFactory : IProviderFactory<INotification, NotificationDefinition>` | `Notifications/NotificationFactory.cs` | factory | 13 helper methods (`OnGrabEnabled`, `OnDownloadEnabled`, `OnUpgradeEnabled`, `OnImportCompleteEnabled`, `OnRenameEnabled`, `OnSeriesAddEnabled`, `OnSeriesDeleteEnabled`, `OnEpisodeFileDeleteEnabled`, `OnEpisodeFileDeleteForUpgradeEnabled`, `OnHealthIssueEnabled`, `OnHealthRestoredEnabled`, `OnApplicationUpdateEnabled`, `OnManualInteractionEnabled`) that filter providers whose `NotificationDefinition.OnX == true` and (by default) drop currently-blocked providers via `INotificationStatusService.GetBlockedProviders()`. `Active()` filters by `Enable`. `SetProviderCharacteristics` reflects each provider's `SupportsOnX` onto the definition row. |
| `NotificationDefinition : ProviderDefinition` | `Notifications/NotificationDefinition.cs` | DB row | Stores 13 `OnX` toggles (writable), 13 `SupportsOnX` flags (memberwise-equality-ignored), `IncludeHealthWarnings`. `Enable` is computed: any of the toggles being true. |
| `NotificationSettingsBase<TSettings>` | `Notifications/NotificationSettingsBase.cs` | abstract | Generic base for per-provider `*Settings` types. Memberwise equality. Each subclass owns a `FluentValidation` validator. |
| `INotificationRepository` | `Notifications/NotificationRepository.cs` | repo | `ProviderRepository<NotificationDefinition>` with extra `UpdateSettings(model)` (writes only the `Settings` column — used by Trakt to persist refreshed OAuth tokens without churning the rest of the row). |
| `NotificationService` | `Notifications/NotificationService.cs` | event sink | The fan-out hub — handles 12 sync events + 4 async events. |
| `NotificationStatus : ProviderStatusBase` | `Notifications/NotificationStatus.cs` | DB row | Per-provider escalating-failure tracking. |
| `INotificationStatusService` | `Notifications/NotificationStatusService.cs` | status service | `MinimumTimeSinceInitialFailure = 5 min`, `MaximumEscalationLevel = 5`. |
| `MediaServerUpdateQueue<THost, TInfo>` | `Notifications/MediaServerUpdateQueue.cs` | helper | Per-host coalescing buffer for library-refresh providers (Kodi, Plex, Emby/Jellyfin). Backed by `ICacheManager.GetRollingCache(...)` with a 1-day TTL; deduplicates pending series by `Series.Id` and aggregates `Info` into a `HashSet<TInfo>`. `ProcessQueue` is single-flight per host. |
| `NotificationMetadataLink`, `NotificationMetadataLinkGenerator` | `Notifications/NotificationMetadataLink.cs`, `NotificationMetadataLinkGenerator.cs` | helper | Builds IMDb/TVDb/Trakt/TVMaze deep links from a `Series` for providers that opt in (Gotify, Telegram, Pushcut). Hard-coded URL templates; uses `MetadataLinkType` enum. |

### Event payload DTOs

All in `src/NzbDrone.Core/Notifications/`:
- `GrabMessage` — `Series`, `RemoteEpisode`, `QualityModel`, `DownloadClientType/Name`, `DownloadId`, formatted `Message`.
- `DownloadMessage` — `Series`, `LocalEpisode EpisodeInfo`, `EpisodeFile`, `List<DeletedEpisodeFile> OldFiles`, `SourcePath`, `DownloadClientItemClientInfo`, `DownloadId`, `GrabbedReleaseInfo Release`.
- `ImportCompleteMessage` — full-season aggregate. Adds `List<Episode> Episodes`, `List<EpisodeFile> EpisodeFiles`, `SourceTitle`, `DestinationPath`, `ReleaseGroup`, `QualityModel ReleaseQuality`.
- `EpisodeDeleteMessage` — `Series`, `EpisodeFile`, `DeleteMediaFileReason Reason`.
- `SeriesAddMessage`, `SeriesDeleteMessage` (constructor builds `DeletedFilesMessage` + `Message`), `ApplicationUpdateMessage`, `ManualInteractionRequiredMessage`.

### Event flow (`NotificationService`)

`NotificationService.cs` implements:

| Source event | Builds | Calls factory list | Skip rules |
|--------------|--------|--------------------|------------|
| `EpisodeGrabbedEvent` | `GrabMessage` | `OnGrabEnabled` | tag intersection (`ShouldHandleSeries`) |
| `EpisodeImportedEvent` (only when `NewDownload`) | `DownloadMessage` | `OnDownloadEnabled` | tag + `OnUpgrade` flag when `OldFiles.Any()` |
| `DownloadCompletedEvent` / `UntrackedDownloadCompletedEvent` | `ImportCompleteMessage` (full-season message variant when `parsedEpisodeInfo.FullSeason`) | `OnImportCompleteEnabled` | tag + `OnImportComplete` flag |
| `SeriesRenamedEvent` | series + renamed file list | `OnRenameEnabled` | tag |
| `UpdateInstalledEvent` | `ApplicationUpdateMessage` | `OnApplicationUpdateEnabled` | none |
| `ManualInteractionRequiredEvent` | `ManualInteractionRequiredMessage` (skipped when message is empty) | `OnManualInteractionEnabled` | tag |
| `EpisodeFileDeletedEvent` | `EpisodeDeleteMessage` (skipped when episode metadata gone) | `OnEpisodeFileDeleteEnabled` | tag + `OnEpisodeFileDeleteForUpgrade` when reason is upgrade |
| `SeriesAddCompletedEvent` | `SeriesAddMessage` | `OnSeriesAddEnabled` | tag |
| `SeriesDeletedEvent` | per-series `SeriesDeleteMessage` | `OnSeriesDeleteEnabled` | tag |
| `HealthCheckFailedEvent` (skip during startup grace) | health check | `OnHealthIssueEnabled` | warning suppressed unless `IncludeHealthWarnings` |
| `HealthCheckRestoredEvent` (skip during startup grace) | previous check | `OnHealthRestoredEnabled` | same warning rule |

Async handlers — `DeleteCompletedEvent`, `DownloadsProcessedEvent`, `RenameCompletedEvent`, `HealthCheckCompleteEvent` — all call `ProcessQueue()` which iterates every available provider and invokes `notification.ProcessQueue()`. This is how Kodi/Plex/Emby drain their `MediaServerUpdateQueue` after a batch of imports completes.

`ShouldHandleSeries`: empty tag set ⇒ true; otherwise only fires when `definition.Tags ∩ series.Tags` is non-empty.

`ShouldHandleHealthFailure`: errors always; warnings only when `IncludeHealthWarnings`.

Every fan-out is wrapped in `try/catch` and records success/failure via `INotificationStatusService.RecordSuccess/RecordFailure(definitionId)`.

### `OnUpgrade` semantics
`SupportsOnUpgrade => SupportsOnDownload` (NotificationBase.cs:99). The runtime "upgrade" gate is the *definition* flag `OnUpgrade`: if `message.OldFiles.Any()` is true and `definition.OnUpgrade` is false, the OnDownload call is suppressed (`NotificationService.cs:189`). There is no separate `OnUpgrade(message)` provider hook.

## Provider inventory

26 concrete `NotificationBase` subclasses (one shared "Emby / Jellyfin" provider, one shared "Plex Media Server" provider, no separate Jellyfin class).

| Provider | File | Transport | OnX events implemented (subset of full list) | Notable settings |
|----------|------|-----------|----------------------------------------------|------------------|
| Apprise | `Apprise/Apprise.cs` | HTTP POST → Apprise-API server (`/notify/<configKey>` or stateless `/notify`) | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ServerUrl`, `ConfigurationKey` xor `StatelessUrls`, `NotificationType`, `Tags`, `IncludePoster`, `AuthUsername`, `AuthPassword` |
| CustomScript | `CustomScript/CustomScript.cs` | shell exec via `IProcessProvider.StartAndCapture` (no `Arguments` allowed) | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `Path` (validated by `SystemFolderValidator`), `Arguments` (validator forces empty). Rich `Sonarr_*` env-var contract — see Templating section. |
| Discord | `Discord/Discord.cs` | HTTPS POST webhook (Discord Webhook API) | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `WebHookUrl`, `Username`, `Avatar`, `Author`, three `int[]` field-sets `GrabFields`, `ImportFields`, `ManualInteractionFields` (drive embed composition) |
| Email (SMTP) | `Email/Email.cs` | SMTP via `MailKit.Net.Smtp.SmtpClient` + `MimeKit` | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `Server`, `Port` (default 587), `UseEncryption` enum (`Preferred=0/Always=1/Never=2`; `Always` + port 465 ⇒ `SslOnConnect`, else `StartTls`), `Username`, `Password`, `From`, `To`, `Cc`, `Bcc` (any one of To/Cc/Bcc must be non-empty). `ICertificateValidationService.ShouldByPassValidationError` is wired in. |
| Gotify | `Gotify/Gotify.cs` | HTTP POST `/message?token=<AppToken>` | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `Server`, `AppToken`, `Priority` (default 5), `IncludeSeriesPoster`, `MetadataLinks`, `PreferredMetadataLink`. Switches payload `extras.client::display.contentType = text/markdown` when poster or links present. |
| Join | `Join/Join.cs` | HTTPS GET joaoapps.com API | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ApiKey`, `DeviceIds` (deprecated — validator forbids), `DeviceNames`, `Priority` (`JoinPriority`) |
| Mailgun | `Mailgun/Mailgun.cs` | HTTP POST Mailgun API (US or EU endpoint) | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ApiKey`, `UseEuEndpoint`, `From`, `SenderDomain`, `Recipients` |
| MediaBrowser (Emby / Jellyfin) | `MediaBrowser/MediaBrowser.cs` | HTTP API (Emby/Jellyfin REST) | Grab, Download (notify + library queue), ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate; `ProcessQueue` flushes `MediaServerUpdateQueue` | `Host`, `Port` (default 8096), `UseSsl`, `UrlBase`, `ApiKey`, `Notify`, `UpdateLibrary`, `MapFrom`/`MapTo` path remap. Update-type passed through (`Created`/`Modified`/`Deleted`); when a queued series has multiple update types, sends `null` so Emby/Jellyfin decides. |
| Notifiarr | `Notifiarr/Notifiarr.cs` (extends `WebhookBase<NotifiarrSettings>`) | HTTPS POST notifiarr.com API | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ApiKey` (mandatory). Reuses Webhook payload DTOs verbatim. |
| Ntfy | `Ntfy/Ntfy.cs` | HTTP POST topic on ntfy.sh or self-hosted | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ServerUrl`, `AccessToken` xor (`UserName`+`Password`), `Priority` (1-5, default 3), `Topics` (validated against reserved words `announcements`, `app`, `docs`, `settings`, `stats`, `mytopic-rw/-ro/-wo`), `Tags`, `ClickUrl` |
| Plex Media Server | `Plex/Server/PlexServer.cs` | Plex HTTP API + plex.tv OAuth pin flow | Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete (only when `DeletedFiles`); `ProcessQueue` flushes library refresh | `Server` selector, `Host`, `Port` (32400), `UseSsl`, `UrlBase`, `AuthToken` (Plex token), `SignIn` (OAuth pin trigger), `UpdateLibrary`, `MapFrom`/`MapTo`. `RequestAction` exposes `startOAuth`/`continueOAuth`/`pollOAuth`/`getOAuthToken`/`servers`. |
| Prowl | `Prowl/Prowl.cs` | HTTPS POST prowlapp.com | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ApiKey`, `Priority` (`ProwlPriority`, -2..2) |
| PushBullet | `PushBullet/PushBullet.cs` | HTTPS POST pushbullet.com | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ApiKey`, `DeviceIds` (`Device` field type — populated dynamically), `ChannelTags`, `SenderId` |
| Pushcut | `Pushcut/Pushcut.cs` | HTTPS POST pushcut.io | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `NotificationName`, `ApiKey`, `TimeSensitive`, `IncludePoster`, `MetadataLinks` |
| Pushover | `Pushover/Pushover.cs` | HTTPS POST pushover.net | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ApiKey`, `UserKey`, `Devices`, `Priority`, `Retry`/`Expire` (only used with `Emergency`), `Ttl`, `Sound` |
| SendGrid | `SendGrid/SendGrid.cs` | HTTPS POST `https://api.sendgrid.com/v3/mail/send` | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `BaseUrl` (default `https://api.sendgrid.com/v3/`), `ApiKey`, `From`, `Recipients` |
| Signal | `Signal/Signal.cs` | HTTP POST signal-cli-rest-api | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `Host`, `Port`, `UseSsl`, `SenderNumber`, `ReceiverId` (group id or phone), `AuthUsername`, `AuthPassword` |
| Simplepush | `Simplepush/Simplepush.cs` | HTTPS POST simplepush.io | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `Key`, `Event` |
| Slack | `Slack/Slack.cs` | HTTPS POST incoming-webhook | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `WebHookUrl`, `Username`, `Icon` (`:emoji:` triggers `icon_emoji`, otherwise `icon_url`), `Channel` |
| SynologyIndexer | `Synology/SynologyIndexer.cs` | shells out to `synoindex` CLI via `ISynologyIndexerProxy` | Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete | `UpdateLibrary` only. Test refuses on non-Linux (`OsInfo.IsLinux`). |
| Telegram | `Telegram/Telegram.cs` | HTTPS POST `https://api.telegram.org/bot<token>/sendMessage` | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `BotToken`, `ChatId`, `TopicId` (`> 1` or null), `SendSilently`, `IncludeAppNameInTitle`, `IncludeInstanceNameInTitle`, `MetadataLinks`, `LinkPreview` (must be one of selected `MetadataLinks` or `None`) |
| Trakt | `Trakt/Trakt.cs` | HTTPS POST trakt.tv API | Download (`addEpisodeToCollection`), ImportComplete (per file), EpisodeFileDelete (`removeEpisodeFromCollection`), SeriesAdd, SeriesDelete | `AccessToken`, `RefreshToken`, `Expires`, `AuthUser`, `SignIn` (OAuth). Refresh-on-demand: if `Expires < now+5min`, calls `RefreshAuthToken` and persists via `INotificationRepository.UpdateSettings`. Maps Sonarr `QualitySource`/HDR/audio enums to Trakt taxonomy. |
| Twitter | `Twitter/Twitter.cs` | OAuth1 POST x.com (legacy Twitter API) | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `ConsumerKey`, `ConsumerSecret`, `AccessToken`, `AccessTokenSecret`, `Mention` (required when `DirectMessage` is true), `DirectMessage` (default true; non-DM raises a *warning* validation), `AuthorizeNotification` (OAuth) |
| Webhook | `Webhook/Webhook.cs` (extends `WebhookBase<WebhookSettings>`) | HTTP POST/PUT (configurable) with optional Basic-auth + custom headers | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired | `Url`, `Method` (POST/PUT), `Username`/`Password` (Basic auth), `Headers` (key-value list) |
| Xbmc (Kodi) | `Xbmc/Xbmc.cs` | HTTP JSON-RPC `/jsonrpc` | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired; `ProcessQueue` runs library `Update`/`Clean` | `Host`, `Port` (8080), `UseSsl`, `UrlBase` (default `/jsonrpc`), `Username`, `Password`, `DisplayTime` (≥2, default 5), `Notify`, `UpdateLibrary`, `CleanLibrary`, `AlwaysUpdate` |

Notes on the inventory:
- The `Notifications/Plex/PlexTv` directory is a *shared service* (`PlexTvService`, `PlexTvProxy`, `PlexTvResource`, `PlexWatchlistItem`) used by `PlexServer` for OAuth and consumed by import-list code (out of scope here). It is not a notification provider on its own.
- `Notifications/Plex/PlexAuthenticationException.cs`/`PlexException.cs`/`PlexVersionException.cs`/`PlexMediaType.cs` are shared transport types; only `Plex/Server/PlexServer.cs` registers as a notification.
- `Notifications/Webhook/Webhook*` contains 23 payload DTOs (`WebhookGrabPayload`, `WebhookImportPayload`, `WebhookImportCompletePayload`, `WebhookEpisodeDeletePayload`, `WebhookSeriesAddPayload`, `WebhookSeriesDeletePayload`, `WebhookRenamePayload`, `WebhookHealthPayload`, `WebhookApplicationUpdatePayload`, `WebhookManualInteractionPayload`, plus DTO sub-types `WebhookSeries`, `WebhookEpisode`, `WebhookEpisodeFile`, `WebhookEpisodeFileMediaInfo`, `WebhookRelease`, `WebhookGrabbedRelease`, `WebhookCustomFormat`, `WebhookCustomFormatInfo`, `WebhookDownloadClientItem`, `WebhookDownloadStatusMessage`, `WebhookImage`, `WebhookRenamedEpisodeFile`). All built once in `WebhookBase` and reused by both `Webhook` and `Notifiarr`.

## Templating / message formatting

There is no shared template engine. Each provider builds its outbound payload from the strongly-typed `*Message` DTO. Two forms of formatting exist:

1. **Generic message string** generated centrally in `NotificationService.GetMessage(series, episodes, quality)`:
   - Empty episodes ⇒ `"{Title} - [{quality}]"`.
   - Daily series ⇒ `"{Title} - {AirDate} - {EpisodeTitle} [{quality}]"`.
   - Standard ⇒ `"{Title} - {S}xEE...EE - {EpTitle1 + EpTitle2} [{quality}]"`.
   - Quality string adds ` Proper` for revision > 1, or ` v{n}` for anime.
   - Full-season variant: `GetFullSeasonMessage` ⇒ `"{Title} - Season {n} [{quality}]"`.
   This pre-computed string is what most simple providers emit verbatim (Apprise/Join/Pushover/Telegram/Email body/Slack title, etc.).
2. **Provider-specific structured payloads**:
   - `Discord` builds a typed `Embed` with author/title/url/description/color/timestamp/fields, choosing fields from `Settings.GrabFields`/`ImportFields`/`ManualInteractionFields` (`DiscordGrabFieldType`, `DiscordImportFieldType`, `DiscordManualInteractionFieldType`). Color taxonomy in `DiscordColors.cs`. Title is truncated to 256 chars and back-ticks are escaped.
   - `Slack` posts simple JSON `Attachment` arrays with fixed `color` codes (`good`/`warning`/`danger`).
   - `Webhook`/`Notifiarr` serialize the full `WebhookXPayload` DTO; `WebhookEventType` enum (`Test`, `Grab`, `Download`, `Rename`, `SeriesAdd`, `SeriesDelete`, `EpisodeFileDelete`, `Health`, `ApplicationUpdate`, `HealthRestored`, `ManualInteractionRequired`) is serialized as PascalCase string (a `// TODO: In v4 this will likely be changed to the default camel case.` lives on `WebhookEventType.cs:7`).
   - `CustomScript` exports environment variables `Sonarr_EventType`, `Sonarr_InstanceName`, `Sonarr_ApplicationUrl`, plus per-event keys (`Sonarr_Series_*`, `Sonarr_Release_*`, `Sonarr_EpisodeFile_*`, `Sonarr_DeletedRelativePaths`, `Sonarr_Health_Issue_*`, `Sonarr_Health_Restored_*`, `Sonarr_Update_*`, `Sonarr_Download_*`, `Sonarr_SourcePath`, `Sonarr_DestinationPath`, `Sonarr_EpisodeFile_MediaInfo_*`, …). Pipe-separated for episode lists, comma-separated for ID/number lists.
   - `Trakt` translates Sonarr enums into Trakt taxonomy: `MapResolution` (480/576/720/1080/2160 → `sd_*`/`hd_*`/`uhd_4k`, with `i`/`p` scan suffix from `TraktInterlacedTypes`), `MapHdr` (DolbyVision/HDR10/HDR10+/HLG variants), `MapAudio` (AC3→`dolby_digital`, EAC3→`dolby_digital_plus`, …), `MapMediaType` (`Web`/`WebRip`→`digital`, `Bluray*`→`bluray`, `Television*`→`vhs`, `DVD`→`dvd`).
   - `Gotify` emits Markdown when `IncludeSeriesPoster` or `MetadataLinks` are set; constructs `extras` blob with `client::display.contentType` and `client::notification.click.url`.

`NotificationMetadataLinkGenerator.GenerateLinks` is the shared link-builder for providers that opt in (Gotify, Telegram, Pushcut). Hard-coded URL templates: `https://www.imdb.com/title/{ImdbId}`, `http://www.thetvdb.com/?tab=series&id={TvdbId}`, `http://trakt.tv/search/tvdb/{TvdbId}?id_type=show`, `http://www.tvmaze.com/shows/{TvMazeId}/_`. Discord's `GetLinksString` re-implements this inline (slightly different — uses HTTPS for IMDb/Trakt and skips TVMaze).

## Settings persistence

- `NotificationDefinition` is a `ProviderDefinition` row in table `Notifications`. The `Settings` column is a JSON blob containing the concrete `*Settings` instance (`IEmbeddedDocument` — see `embedded-docs/catalogue.md`).
- `NotificationStatus` is a separate row in `NotificationStatus` (per-provider failure escalation, base `ProviderStatusBase`).
- Field metadata for the UI comes from `[FieldDefinition(order, Label, Type, Privacy, HelpText, HelpLink, …)]` attributes on each settings property — these are surfaced as the `Field` array of the provider definition by the `ProviderFactory` framework.
- `Privacy` levels in use: `ApiKey` (most tokens — Apprise password, Gotify AppToken, Notifiarr ApiKey, Pushover ApiKey, Telegram BotToken, Twitter Consumer*, Plex AuthToken, Ntfy AccessToken, …), `UserName`, `Password`.
- Special field types: `FieldType.OAuth` (Trakt `SignIn`, PlexServer `SignIn`, Twitter `AuthorizeNotification`), `FieldType.Device` (PushBullet `DeviceIds`), `FieldType.Tag` (recipient/topic lists), `FieldType.KeyValueList` (Webhook `Headers`), `FieldType.FilePath` (CustomScript `Path`), `FieldType.Url`/`Number`/`Select`/`Textbox`/`Password`/`Checkbox`.

## Health checks

- `src/NzbDrone.Core/HealthCheck/Checks/NotificationStatusCheck.cs` reacts to `ProviderUpdatedEvent<INotification>`, `ProviderDeletedEvent<INotification>`, and `ProviderStatusChangedEvent<INotification>`. Joins enabled providers against `INotificationStatusService.GetBlockedProviders()`:
  - All enabled providers blocked → `HealthCheckResult.Error`, reason `NotificationStatusAll`, anchor `#notifications-are-unavailable-due-to-failures`.
  - Some blocked → `HealthCheckResult.Warning`, reason `NotificationStatusSingle`, listing names.
  - None blocked → no-op `HealthCheck`.
- `INotificationFactory.Test(definition)` (`NotificationFactory.cs:210`) wraps each provider's `ValidationResult Test()` and records success/failure via `INotificationStatusService` *only* when `definition.Id != 0` (so unsaved test-from-form runs do not pollute status state).
- Per-provider `Test()` typically attempts a benign API call:
  - HTTP webhook-style providers (Discord/Slack/Webhook/Notifiarr/Apprise/Gotify/Mailgun/Ntfy/PushBullet/Pushover/Pushcut/Prowl/Telegram/Simplepush/SendGrid/Join) post a synthetic message and return a localized `NotificationsValidationUnableToSendTestMessage` failure on exception.
  - `Email.Test` actually sends a "Sonarr - Test Notification" email through the configured SMTP server.
  - `CustomScript.Test` requires `Settings.Path` to exist, runs the script with `Sonarr_EventType=Test`, and fails when exit code ≠ 0.
  - `Trakt.Test` calls `proxy.GetUserName(AccessToken)` after running `RefreshTokenIfNecessary`; 401 maps to `NotificationsValidationInvalidAccessToken`.
  - `Plex.PlexServer.Test` pings plex.tv with the auth token then runs `PlexServerService.Test`.
  - `Synology.Test` refuses on non-Linux OS first.
  - `MediaBrowser.Test` calls `MediaBrowserService.Test`.
  - `Xbmc.Test` calls `XbmcService.Test`.
- `NotificationStatusService` inherits `MinimumTimeSinceInitialFailure = 5 min` and `MaximumEscalationLevel = 5` (5-min, 15-min, 1-h, 6-h, 24-h, 7-day approx — same `ProviderStatusServiceBase` cadence as indexers).

## DB tables touched
- `Notifications` — read/write (`INotificationRepository`).
- `NotificationStatus` — read/write (`INotificationStatusRepository`).
- Also writes through the shared cache (`MediaServerUpdateQueue`'s rolling cache, `PlexTvService`'s `ICache<bool>`).

## Events raised (IEventAggregator)

| Event | Payload | Producers | Consumers |
|-------|---------|-----------|-----------|
| `ProviderAddedEvent<INotification>`, `ProviderUpdatedEvent<INotification>`, `ProviderDeletedEvent<INotification>` | `NotificationDefinition` | `ProviderFactory` lifecycle | `NotificationStatusCheck`, anything subscribing to `IProviderFactory` |
| `ProviderStatusChangedEvent<INotification>` | provider id | `INotificationStatusService` (failure/success transitions) | `NotificationStatusCheck` |

## Events consumed

| Event | Handler | Behavior |
|-------|---------|----------|
| `EpisodeGrabbedEvent` | `NotificationService.Handle` | fan-out OnGrab |
| `EpisodeImportedEvent` | `Handle` | fan-out OnDownload (only `NewDownload`, gated by `OnUpgrade`) |
| `DownloadCompletedEvent` | `Handle` | fan-out OnImportComplete |
| `UntrackedDownloadCompletedEvent` | `Handle` | fan-out OnImportComplete (manual import path) |
| `SeriesRenamedEvent` | `Handle` | fan-out OnRename |
| `SeriesAddCompletedEvent` | `Handle` | fan-out OnSeriesAdd |
| `SeriesDeletedEvent` | `Handle` | fan-out OnSeriesDelete (loops over `message.Series`) |
| `EpisodeFileDeletedEvent` | `Handle` | fan-out OnEpisodeFileDelete (gated by `OnEpisodeFileDeleteForUpgrade`) |
| `HealthCheckFailedEvent` | `Handle` | fan-out OnHealthIssue (skipped during startup grace) |
| `HealthCheckRestoredEvent` | `Handle` | fan-out OnHealthRestored |
| `UpdateInstalledEvent` | `Handle` | fan-out OnApplicationUpdate |
| `ManualInteractionRequiredEvent` | `Handle` | fan-out OnManualInteractionRequired |
| `DeleteCompletedEvent`, `DownloadsProcessedEvent`, `RenameCompletedEvent`, `HealthCheckCompleteEvent` | `HandleAsync` | drains all `MediaServerUpdateQueue`s via `notification.ProcessQueue()` |

## Jobs / commands
None directly. Library refresh batching happens implicitly via `ProcessQueue()` triggered by the four async events above — there is no `IExecute<TCommand>` for notifications.

## External services

- Hosted SaaS: Discord (webhook host), Slack (webhook host), Pushover, Pushbullet, Pushcut, Prowl, Join (joaoapps), Simplepush, Notifiarr, Trakt, Twitter, SendGrid, Mailgun (US `api.mailgun.net` or EU `api.eu.mailgun.net`), Plex.tv (OAuth pin, server discovery).
- Self-hostable: Apprise (`apprise-api`), Gotify, Ntfy, Signal-cli REST API, Plex Media Server, Emby/Jellyfin Server, Kodi (XBMC JSON-RPC).
- Local OS: CustomScript (process exec), SynologyIndexer (`synoindex` shell on Linux/Synology DSM only), SMTP (any reachable mail server, MailKit + MimeKit).
- Hard-coded URLs:
  - `https://raw.githubusercontent.com/Sonarr/Sonarr/develop/Logo/256.png` (Discord author icon)
  - `https://raw.githubusercontent.com/Sonarr/Sonarr/develop/Logo/128.png` (Gotify default image)
  - `http://thetvdb.com/?tab=series&id={TvdbId}` (Discord embed URL — note HTTP in C# Discord, HTTPS in `NotificationMetadataLinkGenerator`)
  - `https://plex.tv/api/v2/pins` (Plex OAuth)
  - `https://api.sendgrid.com/v3/`
  - `https://pushover.net/apps/clone/sonarr` (Pushover help link / app clone)

## Quirks / TODOs / HACKs

- **`SupportsOnX` reflection** — `NotificationBase.HasConcreteImplementation` (line 122) reflects on the runtime type to detect overridden methods. Reflection at construction time, on every provider instance. The Go port should compute these statically per provider.
- **`OnUpgrade` is a definition flag, not a method** — `SupportsOnUpgrade => SupportsOnDownload`. The runtime gate sits in `NotificationService.cs:189` — `if (downloadMessage.OldFiles.Empty() || ((NotificationDefinition)notification.Definition).OnUpgrade)`. The OR condition uses C# short-circuit; if `OldFiles` is empty it always sends. Same idea for `OnEpisodeFileDeleteForUpgrade` (line 403).
- **`NotificationDefinition.Enable`** is a computed property with a redundant boolean expression: `OnGrab || OnDownload || (OnDownload && OnUpgrade) || OnImportComplete || ...` — the second clause is subsumed by the first. Same for `OnEpisodeFileDelete || (OnEpisodeFileDelete && OnEpisodeFileDeleteForUpgrade)`. Looks like leftover from a refactor (NOTIFY-Q1).
- **Webhook event-name casing** — `WebhookEventType.cs:7` `// TODO: In v4 this will likely be changed to the default camel case.` Currently PascalCase. Sonarr is on v5 — confirm whether this finally got migrated (NOTIFY-Q2).
- **CustomScript `Arguments` deprecated** — `CustomScriptSettingsValidator` rejects non-empty `Arguments` with "Arguments are no longer supported for custom scripts" but the field is still defined and `ExecuteScript` still passes `Settings.Arguments` to the process. Dead path, hidden via `Hidden = HiddenType.HiddenIfNotSet`.
- **CustomScript variables for `OnDownload` reference `MediaInfo.PrimaryAudioStream` unconditionally** — if `MediaInfo` is null on a manual import, this NREs. Most callers populate it but it's a footgun.
- **CustomScript exports `Sonarr_EpisodeFile_MediaInfo_AudioLanguages` AND `Sonarr_EpisodeFile_MediaInfo_Languages`** — duplicate variables sourcing the same `AudioStreams` list (`CustomScript.cs:127-128`), one with `Distinct()` and one without.
- **Pushover TODO** — `PushoverSettings.cs:30` `// TODO: Get Pushover to change our app name (or create a new app) when we have a new logo`.
- **Twitter TODO** — `TwitterSettings.cs:17` `// TODO: Validate that it is a valid username (numbers, letters and underscores - I think)`. Twitter API v1.1 is dead — verify provider still works (NOTIFY-Q3).
- **Discord title escaping** — back-ticks are escaped with `Replace("`", "\\`")` and the title is truncated at 253 + `...` if > 256. Title ellipsis can leave a dangling backslash; code does `TrimEnd('\\')` afterwards.
- **`NotificationService.GetMessage`** for full-season concatenates `xEE` numbers without a season prefix ⇒ when a season pack hits the standard branch you get `Title - 1x01x02x03 - ...`. The full-season check happens upstream (`DownloadCompletedEvent` ⇒ `parsedEpisodeInfo.FullSeason`) but not for `OnGrab`.
- **Email validator** allows any *one* of To/Cc/Bcc to be non-empty (lines 22-24); a notification with only a Bcc list is valid.
- **Email cert-validation bypass** is delegated to `ICertificateValidationService.ShouldByPassValidationError` (port-wide setting). Self-signed mail servers Just Work if that toggle is on.
- **Trakt persists tokens through `INotificationRepository.UpdateSettings`** — that's a settings-only update path. The Go port must mirror this (don't fall through to `Update` which would overwrite the user's `OnX` toggles in a race with the UI).
- **Plex `RequestAction("startOAuth")` filters by `ConsumerKey, ConsumerSecret`** — those filter names look copy-pasted from Twitter's OAuth flow; Plex doesn't actually have those settings (`PlexServer.cs:106`). The validator filter is a no-op but the code is misleading.
- **`Webhook.SendWebhookTest`** sets the validation field to `"Url"`, but other providers use empty string or provider-specific fields. UI displays under different headings. Cosmetic but inconsistent.
- **`MediaServerUpdateQueue.Refreshing` flag** — single-flight per host. If `update(items)` throws, the catch clears `Refreshing` and rethrows; the items in `queue.Pending` (drained at top) are *lost* — no retry, no re-enqueue. Possible silent drop on transient Plex/Kodi errors (NOTIFY-Q4).
- **`NotificationFactory.SetProviderCharacteristics`** mutates the definition in place after `ProviderFactory` resolves the runtime provider class, so saved DB rows can drift if a provider changes its set of overridden methods between releases. Re-saved on next factory bootstrap.
- **Notifiarr extends `WebhookBase` directly** but exposes only `ApiKey` — the upstream URL is hard-coded inside `NotifiarrProxy`. Means the Go port can't reuse Webhook transport for Notifiarr — must inject the API URL.
- **SignalR/Telegram have separate `MetadataLinkType` enums** — `Telegram/MetadataLinkType.cs` (alias of the global enum but in a different namespace) and `Telegram/TelegramLinkPreviewOptions.cs`. Watch namespace collisions during port.
- **`Discord.GetLinksString`** uses `imdb.com` (no www) and `trakt.tv` HTTPS, while `NotificationMetadataLinkGenerator` uses `www.imdb.com` and `trakt.tv` HTTP. URL inconsistency across providers (NOTIFY-Q5).
- **`Synology.Test` uses `OsInfo.IsLinux`** to gate test failure but the actual proxy will fail at runtime regardless on non-Linux. Test gate doesn't prevent saving; if a user saves on Linux and then migrates the DB to a non-Linux host, the provider will throw silently per event.

## Test files (the port must keep passing equivalents)

- `src/NzbDrone.Core.Test/NotificationTests/NotificationBaseFixture.cs`
- `src/NzbDrone.Core.Test/NotificationTests/NotificationStatusServiceFixture.cs`
- `src/NzbDrone.Core.Test/NotificationTests/EmailTests/EmailSettingsValidatorFixture.cs`
- `src/NzbDrone.Core.Test/NotificationTests/ProwlProviderTest.cs`
- `src/NzbDrone.Core.Test/NotificationTests/SynologyIndexerFixture.cs`
- `src/NzbDrone.Core.Test/NotificationTests/Xbmc/Json/GetSeriesPathFixture.cs`
- `src/NzbDrone.Core.Test/NotificationTests/Xbmc/Json/UpdateFixture.cs`
- `src/NzbDrone.Core.Test/NotificationTests/Xbmc/OnDownloadFixture.cs`

Coverage gaps: most providers (Discord, Slack, Webhook, Notifiarr, Apprise, Gotify, Telegram, Trakt, Twitter, MediaBrowser/Plex, Pushover, Pushbullet, etc.) have no fixtures at all in `NotificationTests/` (NOTIFY-Q6). The Go port should add table-driven payload-shape tests for at least Discord, Slack, Webhook, Apprise.

## Open questions

- **NOTIFY-Q1**: `NotificationDefinition.Enable` (line 66) contains the redundant clauses `(OnDownload && OnUpgrade)` and `(OnEpisodeFileDelete && OnEpisodeFileDeleteForUpgrade)` already implied by the parent terms. Confirm intent — likely safe to drop in the Go port.
- **NOTIFY-Q2**: `WebhookEventType.cs:7` flags a v4 migration to camelCase enum names that never happened. Decide: preserve PascalCase for backwards compatibility with existing webhook consumers, or finally migrate?
- **NOTIFY-Q3**: Twitter provider targets API v1.1 endpoints (`OAuthToken`, direct messages). Twitter API v1.1 was sunset in 2023. Verify the provider still works at all and decide whether to port-and-deprecate.
- **NOTIFY-Q4**: `MediaServerUpdateQueue.ProcessQueue` discards pending items if the inner `update(items)` throws (catch resets `Refreshing` and rethrows; items drained earlier are lost). Should the Go port re-enqueue on failure or preserve drop-on-error semantics?
- **NOTIFY-Q5**: Inconsistent metadata-link URL scheme (HTTP vs HTTPS) and host (imdb.com vs www.imdb.com) across `NotificationMetadataLinkGenerator`, `Discord.GetLinksString`, and `Gotify.SendNotification`. Pick one canonical set for the Go port.
- **NOTIFY-Q6**: Most provider classes have no unit-test coverage in `NotificationTests/`. Should Phase 1 acceptance require a payload-shape golden test per provider, or per transport family?
- **NOTIFY-Q7**: `Plex.RequestAction("startOAuth")` filters validation by `ConsumerKey`, `ConsumerSecret` — Plex doesn't have those fields. Is this dead defensive code or vestigial Twitter-flow copy-paste? Confirm the OAuth flow still works end-to-end.
- **NOTIFY-Q8**: `INotification.ProcessQueue()` is invoked on *all* providers via `_notificationFactory.GetAvailableProviders()` (not the filtered `OnXEnabled` lists) on every `DeleteCompletedEvent`/`DownloadsProcessedEvent`/`RenameCompletedEvent`/`HealthCheckCompleteEvent`. For 25 providers this is 100+ no-op calls per minute under load. Worth narrowing to providers that override `ProcessQueue` (Plex, Kodi, MediaBrowser).
- **NOTIFY-Q9**: `Trakt.OnDownload`/`OnImportComplete`/`OnEpisodeFileDelete`/`OnSeriesAdd`/`OnSeriesDelete` mutate `Settings.AccessToken`/`Expires`/`RefreshToken` and call `_notificationRepository.UpdateSettings` — i.e. a write happens in the OnX path. The Go port needs a clear concurrency story (event handlers run in parallel per definition? or serialized?). C# code is implicitly serialized within a single `NotificationService.Handle(...)` invocation but two events can race.
- **NOTIFY-Q10**: `Discord` builds `Embed.Url = $"http://thetvdb.com/?tab=series&id={series.TvdbId}"` even when `series.TvdbId == 0` (manual interaction without a series), producing `?id=0`. `OnManualInteractionRequired` guards with `series?.TvdbId > 0` but other handlers don't. Bug or accepted?
- **NOTIFY-Q11**: `MediaServerUpdateQueue` uses `cacheManager.GetRollingCache<UpdateQueue>(typeof(TQueueHost), "pendingSeries", TimeSpan.FromDays(1))`. If `Settings.Host` changes (user edits the provider) the queue keyed on the old host is leaked for 24h. Confirm whether `ProviderUpdatedEvent` should evict it.
- **NOTIFY-Q12**: Email's `client.Timeout = 10000` is a hard 10-second SMTP timeout. With `Preferred` encryption + auto-detection on a slow server this can fail intermittently. Consider making it configurable in the Go port.
