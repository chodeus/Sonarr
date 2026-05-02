# Notifications provider matrix

Family-level orchestration is in `subsystems/notifications.md`; canonical ThingiProvider plumbing in `subsystems/thingiprovider.md`. **25 concrete `INotification` providers** ship in Sonarr v5 (the subsystem-card prose says 26 but enumerates 25 distinct classes — the Plex `PlexTv` directory is a shared OAuth helper, not a provider; the historical `MediaBrowserMetadata` was removed in mig 124). This file flattens each into a porter-assignment row.

Discord/Slack/Webhook/Notifiarr/Apprise/Gotify/Mailgun/Ntfy/PushBullet/Pushover/Pushcut/Prowl/Telegram/SendGrid/Simplepush/Join — all HTTP webhook-style providers. Email and SendGrid are SMTP/REST-mail. Plex/Emby(MediaBrowser)/Xbmc(Kodi)/Synology — library-refresh providers. Trakt/Twitter — OAuth-token chat-and-collection. CustomScript — local process exec. Signal — self-hosted REST relay.

## Family-level contract

| Aspect | Value |
|--------|-------|
| Provider interface | `INotification : IProvider` (`src/NzbDrone.Core/Notifications/INotification.cs`) — declares 11 `OnX` event hooks + `ProcessQueue()` + 13 `SupportsOnX` capability flags |
| Settings base | `NotificationSettingsBase<TSettings>` (`src/NzbDrone.Core/Notifications/NotificationSettingsBase.cs`) — Equ.MemberwiseEqualityComparer for `hasDefinitionChanged` round-trip; each subclass owns a FluentValidation validator |
| Abstract base | `NotificationBase<TSettings>` (`src/NzbDrone.Core/Notifications/NotificationBase.cs`); webhook-shaped providers extend `WebhookBase<TSettings>` (`Notifications/Webhook/WebhookBase.cs`) |
| Definition type | `NotificationDefinition : ProviderDefinition` (`src/NzbDrone.Core/Notifications/NotificationDefinition.cs`) — 13 writable `OnX` toggles, 13 memberwise-ignored `SupportsOnX` flags, `IncludeHealthWarnings`. `Enable` is computed (any toggle true) |
| Factory | `NotificationFactory : ProviderFactory<INotification, NotificationDefinition>` (`src/NzbDrone.Core/Notifications/NotificationFactory.cs`) — adds 13 helper accessors `OnXEnabled` |
| Repository | `NotificationRepository : ProviderRepository<NotificationDefinition>` (`src/NzbDrone.Core/Notifications/NotificationRepository.cs`) — adds `UpdateSettings(model)` (settings-only column update for Trakt token refresh) |
| DB table | `Notifications` (mig 001 → 025 + many later columns). `Settings` is **nullable**, `Name` not unique. Status in `NotificationStatus` (mig 194; uses `DateTimeOffset` not `DateTime` — TP-Q7 carry-over) |
| Status service | `NotificationStatusService` — `MinimumTimeSinceInitialFailure=5min`, `MaximumEscalationLevel=5` |
| Test action contract | `Test()` returns `ValidationResult`. `NotificationFactory.Test(definition)` (`NotificationFactory.cs:210-229`) records to `INotificationStatusService` only when `Id != 0`. Most providers send a synthetic test message; `Email.Test` actually sends; `CustomScript.Test` exec's the script with `Sonarr_EventType=Test` |
| Event flow | `NotificationService` (`src/NzbDrone.Core/Notifications/NotificationService.cs`) handles 12 sync events + 4 async (`DeleteCompletedEvent`/`DownloadsProcessedEvent`/`RenameCompletedEvent`/`HealthCheckCompleteEvent` drain `MediaServerUpdateQueue` via `ProcessQueue()`) |
| MediaServerUpdateQueue | `Notifications/MediaServerUpdateQueue.cs` — per-host coalescing buffer for Kodi/Plex/Emby library refresh; `ICacheManager.GetRollingCache(...)` 1-day TTL; single-flight per host. NOTIFY-Q4: drops items on inner-throw |

## Concrete providers

### Apprise

| Field | Value |
|-------|-------|
| `Implementation` | `Apprise` |
| `ConfigContract` | `AppriseSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Apprise/Apprise.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Apprise/AppriseSettings.cs` |
| Transport | HTTP POST → Apprise-API server `/notify/<configKey>` (configured) or `/notify` (stateless URLs) |
| Auth model | Optional Basic auth username/password |
| OnX implemented | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired (10 of 13) |
| Notable quirks | XOR between `ConfigurationKey` and `StatelessUrls`; `NotificationType` Select; optional `Tags`, `IncludePoster` |
| Test fixtures | none |

**Settings fields:** `ServerUrl`, `ConfigurationKey` (xor with `StatelessUrls`), `StatelessUrls`, `NotificationType` (Select), `Tags`, `IncludePoster` (Checkbox), `AuthUsername`, `AuthPassword` (Privacy=Password).

### CustomScript

| Field | Value |
|-------|-------|
| `Implementation` | `CustomScript` |
| `ConfigContract` | `CustomScriptSettings` |
| Source file | `src/NzbDrone.Core/Notifications/CustomScript/CustomScript.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/CustomScript/CustomScriptSettings.cs` |
| Transport | shell exec via `IProcessProvider.StartAndCapture` |
| Auth model | n/a (local exec) |
| OnX implemented | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired (11) |
| Notable quirks | `Path` validated by `SystemFolderValidator`; `Arguments` field deprecated (validator forbids non-empty); env-var contract `Sonarr_*` per `subsystems/notifications.md` (CustomScript-specific lengthy list) |
| Test fixtures | none |

**Settings fields:** `Path` (Type=FilePath, validates exists + outside system folders), `Arguments` (Type=Textbox, Hidden=HiddenIfNotSet — deprecated, validator rejects non-empty).

### Discord

| Field | Value |
|-------|-------|
| `Implementation` | `Discord` |
| `ConfigContract` | `DiscordSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Discord/Discord.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Discord/DiscordSettings.cs` |
| Transport | HTTPS POST Discord Webhook API |
| Auth model | Webhook URL embeds the secret |
| OnX implemented | Grab, Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired (11) |
| Notable quirks | Embed builder; back-tick escaping in title; truncation to 256 with `...`; `GetLinksString` URL inconsistency vs `NotificationMetadataLinkGenerator` (NOTIFY-Q5); IMDb URL hardcoded `imdb.com` not `www.imdb.com` |
| Test fixtures | none — NOTIFY-Q6 |

**Settings fields:** `WebHookUrl` (Privacy=ApiKey), `Username`, `Avatar`, `Author`, `GrabFields` (Select multi `DiscordGrabFieldType`), `ImportFields` (Select multi `DiscordImportFieldType`), `ManualInteractionFields` (Select multi).

### Email (SMTP)

| Field | Value |
|-------|-------|
| `Implementation` | `Email` |
| `ConfigContract` | `EmailSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Email/Email.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Email/EmailSettings.cs` |
| Transport | SMTP via MailKit + MimeKit |
| Auth model | Username + password |
| OnX implemented | Grab, Download, ImportComplete, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate, ManualInteractionRequired (10) |
| Notable quirks | `client.Timeout = 10s` hardcoded (NOTIFY-Q12); `UseEncryption` enum `Preferred=0/Always=1/Never=2`; `Always` + port 465 ⇒ `SslOnConnect`, else StartTls; cert validation honours `ICertificateValidationService.ShouldByPassValidationError`; one of To/Cc/Bcc must be non-empty |
| Test fixtures | `NotificationTests/EmailTests/EmailSettingsValidatorFixture.cs` |

**Settings fields:** `Server`, `Port` (587), `UseEncryption` (Select), `Username`, `Password` (Privacy=Password), `From`, `To`, `Cc`, `Bcc`.

### Gotify

| Field | Value |
|-------|-------|
| `Implementation` | `Gotify` |
| `ConfigContract` | `GotifySettings` |
| Source file | `src/NzbDrone.Core/Notifications/Gotify/Gotify.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Gotify/GotifySettings.cs` |
| Transport | HTTP POST `/message?token=<AppToken>` |
| Auth model | App token |
| OnX implemented | 10 of 13 (no Rename) |
| Notable quirks | Markdown payload when `IncludeSeriesPoster` or `MetadataLinks` set; `extras.client::display.contentType=text/markdown`; default image hardcoded `https://raw.githubusercontent.com/Sonarr/Sonarr/develop/Logo/128.png` |
| Test fixtures | none |

**Settings fields:** `Server`, `AppToken` (Privacy=ApiKey), `Priority` (default 5), `IncludeSeriesPoster`, `MetadataLinks` (Select multi `MetadataLinkType`), `PreferredMetadataLink`.

### Join

| Field | Value |
|-------|-------|
| `Implementation` | `Join` |
| `ConfigContract` | `JoinSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Join/Join.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Join/JoinSettings.cs` |
| Transport | HTTPS GET joaoapps API |
| Auth model | API key |
| OnX implemented | 10 |
| Notable quirks | `DeviceIds` is **deprecated** — validator forbids; use `DeviceNames` |
| Test fixtures | none |

**Settings fields:** `ApiKey` (Privacy=ApiKey), `DeviceIds` (deprecated, Hidden=HiddenIfNotSet), `DeviceNames`, `Priority` (Select `JoinPriority`).

### Mailgun

| Field | Value |
|-------|-------|
| `Implementation` | `Mailgun` |
| `ConfigContract` | `MailgunSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Mailgun/Mailgun.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Mailgun/MailgunSettings.cs` |
| Transport | HTTP POST Mailgun API (US `api.mailgun.net` or EU `api.eu.mailgun.net`) |
| Auth model | API key |
| OnX implemented | 10 |
| Test fixtures | none |

**Settings fields:** `ApiKey` (Privacy=ApiKey), `UseEuEndpoint` (Checkbox), `From`, `SenderDomain`, `Recipients`.

### MediaBrowser (Emby/Jellyfin)

| Field | Value |
|-------|-------|
| `Implementation` | `MediaBrowser` |
| `ConfigContract` | `MediaBrowserSettings` |
| Source file | `src/NzbDrone.Core/Notifications/MediaBrowser/MediaBrowser.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/MediaBrowser/MediaBrowserSettings.cs` |
| Transport | HTTP API (Emby/Jellyfin REST) |
| Auth model | API key |
| OnX implemented | Grab, Download (notify+library queue), ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete, HealthIssue, HealthRestored, ApplicationUpdate; `ProcessQueue` flushes `MediaServerUpdateQueue` |
| Notable quirks | Update-type passed through; multi-update collapses to `null` |
| Test fixtures | none |

**Settings fields:** `Host`, `Port` (8096), `UseSsl`, `UrlBase`, `ApiKey` (Privacy=ApiKey), `Notify` (Checkbox), `UpdateLibrary`, `MapFrom`, `MapTo`.

### Notifiarr

| Field | Value |
|-------|-------|
| `Implementation` | `Notifiarr` |
| `ConfigContract` | `NotifiarrSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Notifiarr/Notifiarr.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Notifiarr/NotifiarrSettings.cs` |
| Transport | HTTPS POST notifiarr.com (URL hardcoded inside `NotifiarrProxy`) |
| Auth model | API key |
| OnX implemented | 11 (everything Webhook implements) |
| Notable quirks | Extends `WebhookBase<NotifiarrSettings>` — reuses 23 webhook payload DTOs verbatim; upstream URL is NOT settable so Go port must inject it differently than Webhook |
| Test fixtures | none |

**Settings fields:** `ApiKey` (Privacy=ApiKey).

### Ntfy

| Field | Value |
|-------|-------|
| `Implementation` | `Ntfy` |
| `ConfigContract` | `NtfySettings` |
| Source file | `src/NzbDrone.Core/Notifications/Ntfy/Ntfy.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Ntfy/NtfySettings.cs` |
| Transport | HTTP POST topic on ntfy.sh / self-hosted |
| Auth model | Access token xor (UserName + Password) |
| OnX implemented | 10 |
| Notable quirks | Topic validator rejects reserved names `announcements/app/docs/settings/stats/mytopic-rw/-ro/-wo` |
| Test fixtures | none |

**Settings fields:** `ServerUrl`, `AccessToken` (Privacy=ApiKey), `UserName`, `Password` (Privacy=Password), `Priority` (Select 1-5, default 3), `Topics` (Type=Tag), `Tags`, `ClickUrl`.

### Plex Media Server

| Field | Value |
|-------|-------|
| `Implementation` | `PlexServer` |
| `ConfigContract` | `PlexServerSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Plex/Server/PlexServer.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Plex/Server/PlexServerSettings.cs` |
| Transport | Plex HTTP API + plex.tv OAuth pin flow |
| Auth model | OAuth → AuthToken |
| OnX implemented | Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete (only when `DeletedFiles`); `ProcessQueue` flushes library refresh |
| Notable quirks | RequestAction `startOAuth/continueOAuth/pollOAuth/getOAuthToken/servers`. NOTIFY-Q7 — `RequestAction("startOAuth")` filters by ConsumerKey/ConsumerSecret which Plex doesn't have (cosmetic) |
| Test fixtures | none |

**Settings fields:** `Server` (Select; populated by `RequestAction("servers")`), `Host`, `Port` (32400), `UseSsl`, `UrlBase`, `AuthToken` (Privacy=ApiKey, Hidden=HiddenIfNotSet), `SignIn` (Type=OAuth — triggers OAuth pin flow), `UpdateLibrary` (Checkbox), `MapFrom`, `MapTo`.

### Prowl

| Field | Value |
|-------|-------|
| `Implementation` | `Prowl` |
| `ConfigContract` | `ProwlSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Prowl/Prowl.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Prowl/ProwlSettings.cs` |
| Transport | HTTPS POST prowlapp.com |
| Auth model | API key |
| OnX implemented | 10 |
| Test fixtures | `NotificationTests/ProwlProviderTest.cs` |

**Settings fields:** `ApiKey` (Privacy=ApiKey), `Priority` (Select `ProwlPriority`, range -2..2).

### PushBullet

| Field | Value |
|-------|-------|
| `Implementation` | `PushBullet` |
| `ConfigContract` | `PushBulletSettings` |
| Source file | `src/NzbDrone.Core/Notifications/PushBullet/PushBullet.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/PushBullet/PushBulletSettings.cs` |
| Transport | HTTPS POST pushbullet.com |
| Auth model | API key |
| OnX implemented | 10 |
| Notable quirks | RequestAction `getDevices` populates the `Device`-typed field |
| Test fixtures | none |

**Settings fields:** `ApiKey` (Privacy=ApiKey), `DeviceIds` (Type=Device, SelectOptionsProviderAction="getDevices"), `ChannelTags`, `SenderId`.

### Pushcut

| Field | Value |
|-------|-------|
| `Implementation` | `Pushcut` |
| `ConfigContract` | `PushcutSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Pushcut/Pushcut.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Pushcut/PushcutSettings.cs` |
| Transport | HTTPS POST pushcut.io |
| Auth model | API key |
| OnX implemented | 10 |
| Test fixtures | none |

**Settings fields:** `NotificationName`, `ApiKey` (Privacy=ApiKey), `TimeSensitive` (Checkbox), `IncludePoster`, `MetadataLinks` (Select multi).

### Pushover

| Field | Value |
|-------|-------|
| `Implementation` | `Pushover` |
| `ConfigContract` | `PushoverSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Pushover/Pushover.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Pushover/PushoverSettings.cs` |
| Transport | HTTPS POST pushover.net |
| Auth model | App API key + user key |
| OnX implemented | 10 |
| Notable quirks | `Retry`/`Expire` only honoured when `Priority=Emergency`; help link `https://pushover.net/apps/clone/sonarr` |
| Test fixtures | none |

**Settings fields:** `ApiKey` (Privacy=ApiKey), `UserKey` (Privacy=ApiKey), `Devices` (Tag), `Priority` (Select `PushoverPriority`), `Retry`, `Expire`, `Ttl`, `Sound`.

### SendGrid

| Field | Value |
|-------|-------|
| `Implementation` | `SendGrid` |
| `ConfigContract` | `SendGridSettings` |
| Source file | `src/NzbDrone.Core/Notifications/SendGrid/SendGrid.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/SendGrid/SendGridSettings.cs` |
| Transport | HTTPS POST `https://api.sendgrid.com/v3/mail/send` |
| Auth model | API key |
| OnX implemented | 10 |
| Test fixtures | none |

**Settings fields:** `BaseUrl` (default `https://api.sendgrid.com/v3/`), `ApiKey` (Privacy=ApiKey), `From`, `Recipients`.

### Signal

| Field | Value |
|-------|-------|
| `Implementation` | `Signal` |
| `ConfigContract` | `SignalSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Signal/Signal.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Signal/SignalSettings.cs` |
| Transport | HTTP POST signal-cli-rest-api |
| Auth model | Optional Basic auth |
| OnX implemented | 10 |
| Test fixtures | none |

**Settings fields:** `Host`, `Port`, `UseSsl`, `SenderNumber`, `ReceiverId` (group id or phone), `AuthUsername`, `AuthPassword` (Privacy=Password).

### Simplepush

| Field | Value |
|-------|-------|
| `Implementation` | `Simplepush` |
| `ConfigContract` | `SimplepushSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Simplepush/Simplepush.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Simplepush/SimplepushSettings.cs` |
| Transport | HTTPS POST simplepush.io |
| Auth model | Key |
| OnX implemented | 10 |
| Test fixtures | none |

**Settings fields:** `Key` (Privacy=ApiKey), `Event`.

### Slack

| Field | Value |
|-------|-------|
| `Implementation` | `Slack` |
| `ConfigContract` | `SlackSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Slack/Slack.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Slack/SlackSettings.cs` |
| Transport | HTTPS POST incoming-webhook |
| Auth model | Webhook URL |
| OnX implemented | 11 |
| Notable quirks | Icon `:emoji:` triggers `icon_emoji`, otherwise `icon_url`; fixed `color` codes `good`/`warning`/`danger` |
| Test fixtures | none |

**Settings fields:** `WebHookUrl` (Privacy=ApiKey), `Username`, `Icon`, `Channel`.

### SynologyIndexer

| Field | Value |
|-------|-------|
| `Implementation` | `SynologyIndexer` |
| `ConfigContract` | `SynologyIndexerSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Synology/SynologyIndexer.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Synology/SynologyIndexerSettings.cs` |
| Transport | shells out to `synoindex` CLI via `ISynologyIndexerProxy` |
| Auth model | n/a (local) |
| OnX implemented | Download, ImportComplete, Rename, EpisodeFileDelete, SeriesAdd, SeriesDelete (6) |
| Notable quirks | `Test` refuses on non-Linux (`OsInfo.IsLinux`); migrating DB to non-Linux silently breaks |
| Test fixtures | `NotificationTests/SynologyIndexerFixture.cs` |

**Settings fields:** `UpdateLibrary` (Checkbox).

### Telegram

| Field | Value |
|-------|-------|
| `Implementation` | `Telegram` |
| `ConfigContract` | `TelegramSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Telegram/Telegram.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Telegram/TelegramSettings.cs` |
| Transport | HTTPS POST `api.telegram.org/bot<token>/sendMessage` |
| Auth model | Bot token |
| OnX implemented | 10 |
| Notable quirks | Topic id `>1` or null; per-namespace `MetadataLinkType` enum collision (NOTIFY card flag) |
| Test fixtures | none |

**Settings fields:** `BotToken` (Privacy=ApiKey), `ChatId`, `TopicId`, `SendSilently` (Checkbox), `IncludeAppNameInTitle`, `IncludeInstanceNameInTitle`, `MetadataLinks` (Select multi), `LinkPreview` (Select).

### Trakt

| Field | Value |
|-------|-------|
| `Implementation` | `Trakt` |
| `ConfigContract` | `TraktSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Trakt/Trakt.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Trakt/TraktSettings.cs` |
| Transport | HTTPS POST trakt.tv API |
| Auth model | OAuth (servarr.com proxy) |
| OnX implemented | Download (`addEpisodeToCollection`), ImportComplete (per file), EpisodeFileDelete, SeriesAdd, SeriesDelete (5) |
| Notable quirks | RefreshAuthToken-on-demand; persists via `INotificationRepository.UpdateSettings` (NOTIFY-Q9 — settings-write inside event handler); maps Sonarr Quality/HDR/audio to Trakt taxonomy |
| Test fixtures | none |

**Settings fields:** `AccessToken` (Privacy=ApiKey, Hidden=HiddenIfNotSet), `RefreshToken` (Privacy=ApiKey, Hidden=HiddenIfNotSet), `Expires` (DateTime, Hidden=HiddenIfNotSet), `AuthUser` (Hidden=HiddenIfNotSet), `SignIn` (Type=OAuth).

### Twitter

| Field | Value |
|-------|-------|
| `Implementation` | `Twitter` |
| `ConfigContract` | `TwitterSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Twitter/Twitter.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Twitter/TwitterSettings.cs` |
| Transport | OAuth1 POST x.com (legacy Twitter API v1.1) |
| Auth model | OAuth1 (Consumer + Access keys) |
| OnX implemented | 10 |
| Notable quirks | NOTIFY-Q3 — Twitter API v1.1 sunset 2023; provider may not work at all. `DirectMessage=false` raises a *warning* validation; `Mention` required when DM=true. TODO comment about validating username (`TwitterSettings.cs:17`) |
| Test fixtures | none |

**Settings fields:** `ConsumerKey` (Privacy=ApiKey), `ConsumerSecret` (Privacy=ApiKey), `AccessToken` (Privacy=ApiKey, Hidden=HiddenIfNotSet), `AccessTokenSecret` (Privacy=ApiKey, Hidden=HiddenIfNotSet), `Mention`, `DirectMessage` (Checkbox, default true), `AuthorizeNotification` (Type=OAuth).

### Webhook

| Field | Value |
|-------|-------|
| `Implementation` | `Webhook` |
| `ConfigContract` | `WebhookSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Webhook/Webhook.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Webhook/WebhookSettings.cs` |
| Transport | HTTP POST/PUT (configurable) with optional Basic-auth + custom headers |
| Auth model | Optional Basic auth |
| OnX implemented | 11 |
| Notable quirks | `WebhookEventType` enum is PascalCase serialised — TODO at `WebhookEventType.cs:7` says v4 will switch to camelCase; never happened (NOTIFY-Q2). Webhook is the parent transport for `Notifiarr` |
| Test fixtures | none |

**Settings fields:** `Url`, `Method` (Select POST/PUT), `Username`, `Password` (Privacy=Password), `Headers` (Type=KeyValueList).

### Xbmc (Kodi)

| Field | Value |
|-------|-------|
| `Implementation` | `Xbmc` |
| `ConfigContract` | `XbmcSettings` |
| Source file | `src/NzbDrone.Core/Notifications/Xbmc/Xbmc.cs` |
| Settings class | `src/NzbDrone.Core/Notifications/Xbmc/XbmcSettings.cs` |
| Transport | HTTP JSON-RPC `/jsonrpc` |
| Auth model | Username + password |
| OnX implemented | 11 |
| Notable quirks | `ProcessQueue` runs library `Update`/`Clean`; `DisplayTime ≥ 2` |
| Test fixtures | `NotificationTests/Xbmc/Json/{GetSeriesPathFixture,UpdateFixture}.cs`, `NotificationTests/Xbmc/OnDownloadFixture.cs` |

**Settings fields:** `Host`, `Port` (8080), `UseSsl`, `UrlBase` (default `/jsonrpc`), `Username`, `Password` (Privacy=Password), `DisplayTime` (default 5), `Notify` (Checkbox), `UpdateLibrary`, `CleanLibrary`, `AlwaysUpdate`.

## Open questions

- **PROV-Q16** — `WebhookBase` is shared transport for both `Webhook` and `Notifiarr` but they have different Implementation strings and different ConfigContract types. The Go port should keep the abstract `WebhookBase` shared payload-building code in one place but register two distinct providers in the registry. Cross-cite TP-Q1.
- **PROV-Q17** — `NotificationStatus` uses `DateTimeOffset` while `IndexerStatus`/`DownloadClientStatus`/`ImportListStatus` use `DateTime`. The Go port should unify on `time.Time` (UTC) but the SQL type must round-trip — for SQLite this is essentially the same string format, but for Postgres the column types differ (`timestamp with time zone` vs `timestamp`). Confirm migration plan. Cross-cite TP-Q7 / `subsystems/datastore.md` mig 188.
- **PROV-Q18** — `INotification.ProcessQueue()` is invoked on **every** provider via `_notificationFactory.GetAvailableProviders()` on every `DownloadsProcessedEvent` (NOTIFY-Q8). For 25 providers this is ~25 no-op calls per minute. Recommend the Go port narrows to providers that override `ProcessQueue` (Plex, Kodi, MediaBrowser only). Materially less work but a behaviour delta — flag for review.
- **PROV-Q19** — `Trakt.OnX` mutates `Settings.AccessToken`/`Expires`/`RefreshToken` and writes back via `UpdateSettings`. Two events firing concurrently for the same Trakt definition could race the settings write. C# is implicitly serialised within a single `NotificationService.Handle` invocation but two events can overlap. Go port: serialise per-definition (mutex map keyed on Id) or accept last-write-wins?
- **PROV-Q20** — NOTIFY-Q11: `MediaServerUpdateQueue`'s rolling cache is keyed on `Settings.Host`. If a user edits the host on a Plex/Kodi provider, the queue keyed on the old host leaks for 24h. Wire `ProviderUpdatedEvent` to evict it in the Go port.
- **PROV-Q21** — `EmailSettings.client.Timeout = 10s` is hardcoded (NOTIFY-Q12). Promote to a settings field in the Go port? Affects existing user DBs but the field is additive (zero default = 10s preserve).
- **PROV-Q22** — Twitter (NOTIFY-Q3) targets API v1.1 which is sunset. The Go port has three options: (a) port-and-deprecate (existing rows still load but `Test()` always fails), (b) drop entirely from registry — existing rows would become "missing implementation" zombies and silently delete on next startup per `RemoveMissingImplementations`, (c) port-as-broken with a healthcheck warning. Recommend (a) — preserves user data, surfaces the breakage clearly.
