package v3

import (
	"encoding/json"
	"net/http"
)

// NotificationSchemaResource is the JSON shape produced by
// GET /api/v3/notification/schema. Mirrors
// `src/Sonarr.Api.V3/Notifications/NotificationResource.cs` extending
// `ProviderResource<NotificationResource>` at
// `src/Sonarr.Api.V3/ProviderResource.cs`.
//
// 13 on* bool fields toggle which event types fire this notification;
// 13 mirror supportsOn* bool fields say which of those events the
// per-impl C# implementation can actually emit. Every on* defaults
// to false on a fresh schema row; supportsOn* defaults to true per
// the C# `NotificationBase` and per-impl overrides flip specific
// flags to false.
//
// Keys omitted (C# WhenWritingNull on null):
//
//	id → null on schemas, omitted.
//	message → optional ProviderMessage; CustomScript is the only impl
//	          that carries one (warning about EventType=Test).
type NotificationSchemaResource struct {
	ConfigContract                        string               `json:"configContract"`
	Fields                                []FieldDef           `json:"fields"`
	Implementation                        string               `json:"implementation"`
	ImplementationName                    string               `json:"implementationName"`
	IncludeHealthWarnings                 bool                 `json:"includeHealthWarnings"`
	InfoLink                              string               `json:"infoLink"`
	Message                               *NotificationMessage `json:"message,omitempty"`
	Name                                  string               `json:"name"`
	OnApplicationUpdate                   bool                 `json:"onApplicationUpdate"`
	OnDownload                            bool                 `json:"onDownload"`
	OnEpisodeFileDelete                   bool                 `json:"onEpisodeFileDelete"`
	OnEpisodeFileDeleteForUpgrade         bool                 `json:"onEpisodeFileDeleteForUpgrade"`
	OnGrab                                bool                 `json:"onGrab"`
	OnHealthIssue                         bool                 `json:"onHealthIssue"`
	OnHealthRestored                      bool                 `json:"onHealthRestored"`
	OnImportComplete                      bool                 `json:"onImportComplete"`
	OnManualInteractionRequired           bool                 `json:"onManualInteractionRequired"`
	OnRename                              bool                 `json:"onRename"`
	OnSeriesAdd                           bool                 `json:"onSeriesAdd"`
	OnSeriesDelete                        bool                 `json:"onSeriesDelete"`
	OnUpgrade                             bool                 `json:"onUpgrade"`
	Presets                               []any                `json:"presets"`
	SupportsOnApplicationUpdate           bool                 `json:"supportsOnApplicationUpdate"`
	SupportsOnDownload                    bool                 `json:"supportsOnDownload"`
	SupportsOnEpisodeFileDelete           bool                 `json:"supportsOnEpisodeFileDelete"`
	SupportsOnEpisodeFileDeleteForUpgrade bool                 `json:"supportsOnEpisodeFileDeleteForUpgrade"`
	SupportsOnGrab                        bool                 `json:"supportsOnGrab"`
	SupportsOnHealthIssue                 bool                 `json:"supportsOnHealthIssue"`
	SupportsOnHealthRestored              bool                 `json:"supportsOnHealthRestored"`
	SupportsOnImportComplete              bool                 `json:"supportsOnImportComplete"`
	SupportsOnManualInteractionRequired   bool                 `json:"supportsOnManualInteractionRequired"`
	SupportsOnRename                      bool                 `json:"supportsOnRename"`
	SupportsOnSeriesAdd                   bool                 `json:"supportsOnSeriesAdd"`
	SupportsOnSeriesDelete                bool                 `json:"supportsOnSeriesDelete"`
	SupportsOnUpgrade                     bool                 `json:"supportsOnUpgrade"`
	Tags                                  []int                `json:"tags"`
}

// NotificationMessage is the optional static banner some C# impls
// surface on the schema row. CustomScript is the only notification
// type that carries one in current Sonarr. Mirrors
// `src/NzbDrone.Core/Annotations/ProviderMessage.cs`.
type NotificationMessage struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// notificationSchemas is the canonical 25-entry table sorted by
// ImplementationName ASC (case-insensitive: ntfy.sh sits between
// Notifiarr and Plex Media Server in the golden because lowercase
// 'n' compares to 'N' as equal in the C# OrdinalIgnoreCase sort the
// schema-template projection uses).
func notificationSchemas() []NotificationSchemaResource {
	return []NotificationSchemaResource{
		appriseSchema(),
		customScriptSchema(),
		discordSchema(),
		emailSchema(),
		mediaBrowserSchema(),
		gotifySchema(),
		joinSchema(),
		xbmcSchema(),
		mailgunSchema(),
		notifiarrSchema(),
		ntfySchema(),
		plexServerSchema(),
		prowlSchema(),
		pushBulletSchema(),
		pushcutSchema(),
		pushoverSchema(),
		sendGridSchema(),
		signalSchema(),
		simplepushSchema(),
		slackSchema(),
		synologyIndexerSchema(),
		telegramSchema(),
		traktSchema(),
		twitterSchema(),
		webhookSchema(),
	}
}

// notificationSchemaShell returns the per-impl wrapper with the
// defaults every C# NotificationDefinition ctor produces: all on*
// false, all supportsOn* true (the most common per-impl shape), no
// message banner, name="", presets=[], tags=[]. Per-impl ctors
// flip specific supportsOn* flags to false when the C# impl can't
// emit the corresponding event.
func notificationSchemaShell() NotificationSchemaResource {
	return NotificationSchemaResource{
		Presets:                               []any{},
		Tags:                                  []int{},
		SupportsOnApplicationUpdate:           true,
		SupportsOnDownload:                    true,
		SupportsOnEpisodeFileDelete:           true,
		SupportsOnEpisodeFileDeleteForUpgrade: true,
		SupportsOnGrab:                        true,
		SupportsOnHealthIssue:                 true,
		SupportsOnHealthRestored:              true,
		SupportsOnImportComplete:              true,
		SupportsOnManualInteractionRequired:   true,
		SupportsOnRename:                      true,
		SupportsOnSeriesAdd:                   true,
		SupportsOnSeriesDelete:                true,
		SupportsOnUpgrade:                     true,
	}
}

// notificationMetadataLinksOptions returns the 4-option IMDb/TVDb/
// TVMaze/Trakt select used by Gotify, Pushcut, and Telegram's
// `metadataLinks` field. Identical hints + orders across all three.
func notificationMetadataLinksOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "IMDb", Order: 7, Hint: "(0)"},
		{Value: 1, Name: "TVDb", Order: 10, Hint: "(1)"},
		{Value: 2, Name: "TVMaze", Order: 13, Hint: "(2)"},
		{Value: 3, Name: "Trakt", Order: 16, Hint: "(3)"},
	}
}

// ---------- per-impl ctors ----------

func appriseSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "AppriseSettings"
	s.Implementation = "Apprise"
	s.ImplementationName = "Apprise"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#apprise"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "serverUrl", Label: "Apprise Server URL", HelpText: "Apprise server URL, including http(s):// and port if needed", HelpLink: "https://github.com/caronc/apprise-api", Type: "url", Privacy: "normal", Placeholder: "http://localhost:8000"},
		{Order: 1, Name: "configurationKey", Label: "Apprise Configuration Key", HelpText: "Configuration Key for the Persistent Storage Solution. Leave empty if Stateless URLs is used.", HelpLink: "https://github.com/caronc/apprise-api#persistent-storage-solution", Type: "textbox", Privacy: "normal"},
		{Order: 2, Name: "statelessUrls", Label: "Apprise Stateless URLs", HelpText: "One or more URLs separated by commas identifying where the notification should be sent to. Leave empty if Persistent Storage is used.", HelpLink: "https://github.com/caronc/apprise#productivity-based-notifications", Type: "textbox", Privacy: "normal"},
		{
			Order: 3, Name: "notificationType", Label: "Apprise Notification Type",
			Type: "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Info", Order: 0},
				{Value: 1, Name: "Success", Order: 1},
				{Value: 2, Name: "Warning", Order: 2},
				{Value: 3, Name: "Failure", Order: 3},
			},
			Value: 0, ValueSet: true,
		},
		{Order: 4, Name: "tags", Label: "Apprise Tags", HelpText: "Optionally notify only those tagged accordingly.", Type: "tag", Privacy: "normal", Value: []int{}, ValueSet: true},
		{Order: 5, Name: "includePoster", Label: "Include Poster", HelpText: "Include poster in message", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 6, Name: "authUsername", Label: "Username", HelpText: "HTTP Basic Auth Username", Type: "textbox", Privacy: "userName"},
		{Order: 7, Name: "authPassword", Label: "Password", HelpText: "HTTP Basic Auth Password", Type: "password", Privacy: "password"},
	}
	return s
}

func customScriptSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "CustomScriptSettings"
	s.Implementation = "CustomScript"
	s.ImplementationName = "Custom Script"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#customscript"
	s.Message = &NotificationMessage{
		Message: "Testing will execute the script with the EventType set to Test, ensure your script handles this correctly",
		Type:    "warning",
	}
	s.Fields = []FieldDef{
		{Order: 0, Name: "path", Label: "Path", Type: "filePath", Privacy: "normal"},
		{Order: 1, Name: "arguments", Label: "Arguments", HelpText: "Arguments to pass to the script", Type: "textbox", Privacy: "normal", Hidden: "hiddenIfNotSet"},
	}
	return s
}

// discordGrabFields / discordImportFields / discordManualFields —
// the three multi-select option lists on the Discord schema (orders
// 4-6). Each is a contiguous-value enum kept in declaration order.
func discordGrabFields() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Overview", Order: 0},
		{Value: 1, Name: "Rating", Order: 1},
		{Value: 2, Name: "Genres", Order: 2},
		{Value: 3, Name: "Quality", Order: 3},
		{Value: 4, Name: "Group", Order: 4},
		{Value: 5, Name: "Size", Order: 5},
		{Value: 6, Name: "Links", Order: 6},
		{Value: 7, Name: "Release", Order: 7},
		{Value: 8, Name: "Poster", Order: 8},
		{Value: 9, Name: "Fanart", Order: 9},
		{Value: 10, Name: "Indexer", Order: 10},
		{Value: 11, Name: "CustomFormats", Order: 11},
		{Value: 12, Name: "CustomFormatScore", Order: 12},
	}
}

func discordImportFields() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Overview", Order: 0},
		{Value: 1, Name: "Rating", Order: 1},
		{Value: 2, Name: "Genres", Order: 2},
		{Value: 3, Name: "Quality", Order: 3},
		{Value: 4, Name: "Codecs", Order: 4},
		{Value: 5, Name: "Group", Order: 5},
		{Value: 6, Name: "Size", Order: 6},
		{Value: 7, Name: "Languages", Order: 7},
		{Value: 8, Name: "Subtitles", Order: 8},
		{Value: 9, Name: "Links", Order: 9},
		{Value: 10, Name: "Release", Order: 10},
		{Value: 11, Name: "Poster", Order: 11},
		{Value: 12, Name: "Fanart", Order: 12},
		{Value: 13, Name: "CustomFormats", Order: 13},
		{Value: 14, Name: "CustomFormatScore", Order: 14},
	}
}

func discordManualFields() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Overview", Order: 0},
		{Value: 1, Name: "Rating", Order: 1},
		{Value: 2, Name: "Genres", Order: 2},
		{Value: 3, Name: "Quality", Order: 3},
		{Value: 4, Name: "Group", Order: 4},
		{Value: 5, Name: "Size", Order: 5},
		{Value: 6, Name: "Links", Order: 6},
		{Value: 7, Name: "DownloadTitle", Order: 7},
		{Value: 8, Name: "Poster", Order: 8},
		{Value: 9, Name: "Fanart", Order: 9},
	}
}

func discordSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "DiscordSettings"
	s.Implementation = "Discord"
	s.ImplementationName = "Discord"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#discord"
	s.Fields = []FieldDef{
		{Order: 0, Name: "webHookUrl", Label: "Webhook URL", HelpText: "Discord channel webhook url", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "username", Label: "Username", HelpText: "The username to post as, defaults to Discord webhook default", Type: "textbox", Privacy: "userName"},
		{Order: 2, Name: "avatar", Label: "Avatar", HelpText: "Change the avatar that is used for messages from this integration", Type: "textbox", Privacy: "normal"},
		{Order: 3, Name: "author", Label: "Author", HelpText: "Override the embed author that shows for this notification. Blank is instance name", Type: "textbox", Privacy: "normal", Advanced: true},
		{
			Order: 4, Name: "grabFields", Label: "On Grab Fields",
			HelpText: "Change the fields that are passed in for this 'on grab' notification",
			Type:     "select", Privacy: "normal", Advanced: true,
			SelectOptions: discordGrabFields(),
			Value:         []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, ValueSet: true,
		},
		{
			Order: 5, Name: "importFields", Label: "On Import Fields",
			HelpText: "Change the fields that are passed in for this 'on import' notification",
			Type:     "select", Privacy: "normal", Advanced: true,
			SelectOptions: discordImportFields(),
			Value:         []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, ValueSet: true,
		},
		{
			Order: 6, Name: "manualInteractionFields", Label: "On Manual Interaction Fields",
			HelpText: "Change the fields that are passed in for this 'on manual interation' notification",
			Type:     "select", Privacy: "normal", Advanced: true,
			SelectOptions: discordManualFields(),
			Value:         []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, ValueSet: true,
		},
	}
	return s
}

func emailSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "EmailSettings"
	s.Implementation = "Email"
	s.ImplementationName = "Email"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#email"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "server", Label: "Server", HelpText: "Hostname or IP of Email server", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 587, ValueSet: true},
		{
			Order: 2, Name: "useEncryption", Label: "Use Encryption",
			HelpText: "Whether to prefer using encryption if configured on the server, to always use encryption via SSL (Port 465 only) or StartTLS (any other port) or to never use encryption",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Preferred", Order: 0},
				{Value: 1, Name: "Always", Order: 1},
				{Value: 2, Name: "Never", Order: 2},
			},
			Value: 0, ValueSet: true,
		},
		{Order: 3, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 4, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 5, Name: "from", Label: "From Address", Type: "textbox", Privacy: "normal"},
		{Order: 6, Name: "to", Label: "Recipient Address(es)", HelpText: "Comma separated list of email recipients", Type: "textbox", Privacy: "normal", Value: []string{}, ValueSet: true},
		{Order: 7, Name: "cc", Label: "CC Address(es)", HelpText: "Comma separated list of email cc recipients", Type: "textbox", Privacy: "normal", Advanced: true, Value: []string{}, ValueSet: true},
		{Order: 8, Name: "bcc", Label: "BCC Address(es)", HelpText: "Comma separated list of email bcc recipients", Type: "textbox", Privacy: "normal", Advanced: true, Value: []string{}, ValueSet: true},
	}
	return s
}

func mediaBrowserSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "MediaBrowserSettings"
	s.Implementation = "MediaBrowser"
	s.ImplementationName = "Emby / Jellyfin"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#mediabrowser"
	s.SupportsOnManualInteractionRequired = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8096, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Connect to Emby/Jellyfin over HTTPS instead of HTTP", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Emby/Jellyfin url, such as http://[host]:[port]/[urlBase]/mediabrowser", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		{Order: 5, Name: "notify", Label: "Send Notifications", HelpText: "Have Emby send notifications to configured providers. Not supported on Jellyfin.", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 6, Name: "updateLibrary", Label: "Update Library", HelpText: "Update Library on Import, Rename, or Delete", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
		{Order: 7, Name: "mapFrom", Label: "Map Paths From", HelpText: "Sonarr path, used to modify series paths when Emby/Jellyfin sees library path location differently from Sonarr (Requires 'Update Library')", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 8, Name: "mapTo", Label: "Map Paths To", HelpText: "Emby/Jellyfin path, used to modify series paths when Emby/Jellyfin sees library path location differently from Sonarr (Requires 'Update Library')", Type: "textbox", Privacy: "normal", Advanced: true},
	}
	return s
}

func gotifySchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "GotifySettings"
	s.Implementation = "Gotify"
	s.ImplementationName = "Gotify"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#gotify"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "server", Label: "Gotify Server", HelpText: "Gotify server URL, including http(s):// and port if needed", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "appToken", Label: "App Token", HelpText: "The Application Token generated by Gotify", Type: "textbox", Privacy: "apiKey"},
		{
			Order: 2, Name: "priority", Label: "Priority",
			HelpText: "Priority of the notification",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Min", Order: 0},
				{Value: 2, Name: "Low", Order: 2},
				{Value: 5, Name: "Normal", Order: 5},
				{Value: 8, Name: "High", Order: 8},
			},
			Value: 5, ValueSet: true,
		},
		{Order: 3, Name: "includeSeriesPoster", Label: "Include Series Poster", HelpText: "Include series poster in message", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{
			Order: 4, Name: "metadataLinks", Label: "Metadata Links",
			HelpText: "Add a links to series metadata when sending notifications",
			Type:     "select", Privacy: "normal",
			SelectOptions: notificationMetadataLinksOptions(),
			Value:         []int{}, ValueSet: true,
		},
		{
			Order: 5, Name: "preferredMetadataLink", Label: "Preferred Metadata Link",
			HelpText: "Metadata link for clients that only support a single link",
			Type:     "select", Privacy: "normal",
			SelectOptions: notificationMetadataLinksOptions(),
			Value:         1, ValueSet: true,
		},
	}
	return s
}

func joinSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "JoinSettings"
	s.Implementation = "Join"
	s.ImplementationName = "Join"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#join"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "API Key", HelpText: "The API Key from your Join account settings (click Join API button).", HelpLink: "https://joinjoaomgcd.appspot.com/", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "deviceIds", Label: "Device IDs", HelpText: "Deprecated, use Device Names instead. Comma separated list of Device IDs you'd like to send notifications to. If unset, all devices will receive notifications.", Type: "textbox", Privacy: "normal", Hidden: "hiddenIfNotSet"},
		{Order: 2, Name: "deviceNames", Label: "Device Names", HelpText: "Comma separated list of full or partial device names you'd like to send notifications to. If unset, all devices will receive notifications.", HelpLink: "https://joaoapps.com/join/api/", Type: "textbox", Privacy: "normal"},
		{
			Order: 3, Name: "priority", Label: "Notification Priority",
			Type: "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: -2, Name: "Silent", Order: -2},
				{Value: -1, Name: "Quiet", Order: -1},
				{Value: 0, Name: "Normal", Order: 0},
				{Value: 1, Name: "High", Order: 1},
				{Value: 2, Name: "Emergency", Order: 2},
			},
			Value: 0, ValueSet: true,
		},
	}
	return s
}

func xbmcSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "XbmcSettings"
	s.Implementation = "Xbmc"
	s.ImplementationName = "Kodi"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#xbmc"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8080, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Connect to Kodi over HTTPS instead of HTTP", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Kodi url, such as http://[host]:[port]/[urlBase]/kodi", Type: "textbox", Privacy: "normal", Advanced: true, Value: "/jsonrpc", ValueSet: true},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "displayTime", Label: "Display Time", HelpText: "How long the notification will be displayed for (In seconds)", Type: "textbox", Privacy: "normal", Value: 5, ValueSet: true},
		{Order: 7, Name: "notify", Label: "GUI Notification", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 8, Name: "updateLibrary", Label: "Update Library", HelpText: "Update library on Import & Rename?", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 9, Name: "cleanLibrary", Label: "Clean Library", HelpText: "Clean library after update", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 10, Name: "alwaysUpdate", Label: "Always Update", HelpText: "Update library even when a video is playing?", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
	}
	return s
}

func mailgunSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "MailgunSettings"
	s.Implementation = "MailGun"
	s.ImplementationName = "Mailgun"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#mailgun"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "API Key", HelpText: "The API key generated from MailGun", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "useEuEndpoint", Label: "Use EU Endpoint", HelpText: "Enable to use the EU MailGun endpoint", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 2, Name: "from", Label: "From Address", Type: "textbox", Privacy: "normal"},
		{Order: 3, Name: "senderDomain", Label: "Sender Domain", Type: "textbox", Privacy: "normal"},
		{Order: 4, Name: "recipients", Label: "Recipient Address(es)", Type: "tag", Privacy: "normal", Value: []string{}, ValueSet: true},
	}
	return s
}

func notifiarrSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "NotifiarrSettings"
	s.Implementation = "Notifiarr"
	s.ImplementationName = "Notifiarr"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#notifiarr"
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "API Key", HelpText: "Your API key from your profile", HelpLink: "https://notifiarr.com", Type: "textbox", Privacy: "apiKey"},
	}
	return s
}

func ntfySchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "NtfySettings"
	s.Implementation = "Ntfy"
	s.ImplementationName = "ntfy.sh"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#ntfy"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "serverUrl", Label: "Server URL", HelpText: "Leave blank to use public server (https://ntfy.sh)", HelpLink: "https://ntfy.sh/docs/install/", Type: "url", Privacy: "normal"},
		{Order: 1, Name: "accessToken", Label: "Access Token", HelpText: "Optional token-based authorization. Takes priority over username/password", HelpLink: "https://docs.ntfy.sh/config/#access-tokens", Type: "password", Privacy: "apiKey"},
		{Order: 2, Name: "userName", Label: "Username", HelpText: "Optional username", Type: "textbox", Privacy: "userName"},
		{Order: 3, Name: "password", Label: "Password", HelpText: "Optional password", Type: "password", Privacy: "password"},
		{
			Order: 4, Name: "priority", Label: "Priority",
			Type: "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 1, Name: "Min", Order: 1},
				{Value: 2, Name: "Low", Order: 2},
				{Value: 3, Name: "Default", Order: 3},
				{Value: 4, Name: "High", Order: 4},
				{Value: 5, Name: "Max", Order: 5},
			},
			Value: 3, ValueSet: true,
		},
		{Order: 5, Name: "topics", Label: "Topics", HelpText: "List of Topics to send notifications to", Type: "tag", Privacy: "normal", Value: []string{}, ValueSet: true},
		{Order: 6, Name: "tags", Label: "Ntfy Tags and Emojis", HelpText: "Optional list of tags or emojis to use", HelpLink: "https://ntfy.sh/docs/emojis/", Type: "tag", Privacy: "normal"},
		{Order: 7, Name: "clickUrl", Label: "Click Url", HelpText: "Optional link when user clicks notification", Type: "url", Privacy: "normal"},
	}
	return s
}

func plexServerSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "PlexServerSettings"
	s.Implementation = "PlexServer"
	s.ImplementationName = "Plex Media Server"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#plexserver"
	// Plex Media Server can't emit OnGrab / OnHealthIssue /
	// OnHealthRestored / OnApplicationUpdate / OnManualInteractionRequired
	// — those are Sonarr-side events the C# Plex notification impl
	// doesn't surface to Plex's library hook.
	s.SupportsOnGrab = false
	s.SupportsOnHealthIssue = false
	s.SupportsOnHealthRestored = false
	s.SupportsOnApplicationUpdate = false
	s.SupportsOnManualInteractionRequired = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "server", Label: "Server", HelpText: "Select server from plex.tv account after authenticating", Type: "select", Privacy: "normal", SelectOptionsProviderAction: "servers"},
		{Order: 1, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "", ValueSet: true},
		{Order: 2, Name: "port", Label: "Port", Type: "number", Privacy: "normal", Value: 32400, ValueSet: true},
		{Order: 3, Name: "useSsl", Label: "Use SSL", HelpText: "Connect to Plex over HTTPS instead of HTTP", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 4, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Plex url, such as http://[host]:[port]/[urlBase]/plex", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 5, Name: "authToken", Label: "Auth Token", Type: "textbox", Privacy: "apiKey", Advanced: true},
		{Order: 6, Name: "signIn", Label: "Authenticate with Plex.tv", Type: "oAuth", Privacy: "normal", Value: "startOAuth", ValueSet: true},
		{Order: 7, Name: "updateLibrary", Label: "Update Library", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
		{Order: 8, Name: "mapFrom", Label: "Map Paths From", HelpText: "Sonarr path, used to modify series paths when Plex sees library path location differently from Sonarr (Requires 'Update Library')", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 9, Name: "mapTo", Label: "Map Paths To", HelpText: "Plex path, used to modify series paths when Plex sees library path location differently from Sonarr (Requires 'Update Library')", Type: "textbox", Privacy: "normal", Advanced: true},
	}
	return s
}

func prowlSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "ProwlSettings"
	s.Implementation = "Prowl"
	s.ImplementationName = "Prowl"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#prowl"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "API Key", HelpLink: "https://www.prowlapp.com/api_settings.php", Type: "textbox", Privacy: "apiKey"},
		{
			Order: 1, Name: "priority", Label: "Priority",
			Type: "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: -2, Name: "VeryLow", Order: -2},
				{Value: -1, Name: "Low", Order: -1},
				{Value: 0, Name: "Normal", Order: 0},
				{Value: 1, Name: "High", Order: 1},
				{Value: 2, Name: "Emergency", Order: 2},
			},
			Value: 0, ValueSet: true,
		},
	}
	return s
}

func pushBulletSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "PushBulletSettings"
	s.Implementation = "PushBullet"
	s.ImplementationName = "Pushbullet"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#pushbullet"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "Access Token", HelpLink: "https://www.pushbullet.com/#settings/account", Type: "textbox", Privacy: "apiKey"},
		{Order: 1, Name: "deviceIds", Label: "Device IDs", HelpText: "List of device IDs (leave blank to send to all devices)", Type: "device", Privacy: "normal", Value: []string{}, ValueSet: true},
		{Order: 2, Name: "channelTags", Label: "Channel Tags", HelpText: "List of Channel Tags to send notifications to", Type: "tag", Privacy: "normal", Value: []string{}, ValueSet: true},
		{Order: 3, Name: "senderId", Label: "Sender ID", HelpText: "The device ID to send notifications from, use device_iden in the device's URL on pushbullet.com (leave blank to send from yourself)", Type: "textbox", Privacy: "normal"},
	}
	return s
}

func pushcutSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "PushcutSettings"
	s.Implementation = "Pushcut"
	s.ImplementationName = "Pushcut"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#pushcut"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "notificationName", Label: "Notification Name", HelpText: "Notification name from Notifications tab of the Pushcut app", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "apiKey", Label: "API Key", HelpText: "API Keys can be managed in the Account view of the Pushcut app", Type: "textbox", Privacy: "apiKey"},
		{Order: 2, Name: "timeSensitive", Label: "Time Sensitive", HelpText: "Enable to mark the notification as \"Time Sensitive\"", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "includePoster", Label: "Include Poster", HelpText: "Include poster with notification", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{
			Order: 4, Name: "metadataLinks", Label: "Metadata Links",
			HelpText: "Add a links to series metadata when sending notifications",
			Type:     "select", Privacy: "normal",
			SelectOptions: notificationMetadataLinksOptions(),
			Value:         []int{}, ValueSet: true,
		},
	}
	return s
}

func pushoverSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "PushoverSettings"
	s.Implementation = "Pushover"
	s.ImplementationName = "Pushover"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#pushover"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "API Key", HelpLink: "https://pushover.net/apps/clone/sonarr", Type: "textbox", Privacy: "apiKey"},
		{Order: 1, Name: "userKey", Label: "User Key", HelpLink: "https://pushover.net/", Type: "textbox", Privacy: "userName"},
		{Order: 2, Name: "devices", Label: "Devices", HelpText: "List of device names (leave blank to send to all devices)", Type: "tag", Privacy: "normal", Value: []string{}, ValueSet: true},
		{
			Order: 3, Name: "priority", Label: "Priority",
			Type: "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: -2, Name: "Silent", Order: -2},
				{Value: -1, Name: "Quiet", Order: -1},
				{Value: 0, Name: "Normal", Order: 0},
				{Value: 1, Name: "High", Order: 1},
				{Value: 2, Name: "Emergency", Order: 2},
			},
			Value: 0, ValueSet: true,
		},
		{Order: 4, Name: "retry", Label: "Retry", HelpText: "Interval to retry Emergency alerts, minimum 30 seconds", Type: "textbox", Privacy: "normal", Value: 0, ValueSet: true},
		{Order: 5, Name: "expire", Label: "Expire", HelpText: "Maximum time to retry Emergency alerts, maximum 86400 seconds", Type: "textbox", Privacy: "normal", Value: 0, ValueSet: true},
		{Order: 6, Name: "ttl", Label: "Time To Live", HelpText: "Time in seconds before the message expires. Set to 0 for unlimited duration", Type: "textbox", Privacy: "normal", Advanced: true, Value: 0, ValueSet: true},
		{Order: 7, Name: "sound", Label: "Sound", HelpText: "Notification sound, leave blank to use the default", HelpLink: "https://pushover.net/api#sounds", Type: "textbox", Privacy: "normal"},
		// Order 8 added in J230 (C.1.i-follow-1). Upstream commit
		// `e79077d5f` (audit row C.1.f / J219) added the Pushover
		// end-to-end encryption feature: the `pushover.Settings` struct
		// + encryption logic + tests landed at J219, but this
		// hand-written schema didn't expose the field. Verified against
		// live `Sonarr v5.0.0.1658` recording at
		// `~/Code/GitHub/Sonarr-reference/v5-develop-7c41dcbe2/schemas/v3.notification.schema.json`
		// (Pushover entry, index 8).
		{Order: 8, Name: "encryptionKey", Label: "Encryption Key", HelpText: "Optional 64-character hex key (256-bit) to enable end-to-end encryption of the title and message", HelpLink: "https://pushover.net/api#e2ee", Type: "textbox", Privacy: "apiKey", Advanced: true},
	}
	return s
}

func sendGridSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "SendGridSettings"
	s.Implementation = "SendGrid"
	s.ImplementationName = "SendGrid"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#sendgrid"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "apiKey", Label: "API Key", HelpText: "The API Key generated by SendGrid", HelpLink: "https://sendgrid.com/docs/ui/account-and-settings/api-keys/#creating-an-api-key", Type: "textbox", Privacy: "normal"},
		{Order: 1, Name: "from", Label: "From Address", Type: "textbox", Privacy: "normal"},
		{Order: 2, Name: "recipients", Label: "Recipient Address(es)", Type: "tag", Privacy: "normal", Value: []string{}, ValueSet: true},
	}
	return s
}

func signalSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "SignalSettings"
	s.Implementation = "Signal"
	s.ImplementationName = "Signal"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#signal"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Placeholder: "localhost"},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Placeholder: "8080", Value: 0, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Connect to Signal over HTTPS instead of HTTP", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "senderNumber", Label: "Sender Number", HelpText: "Phone number of the sender register in signal-api", Type: "textbox", Privacy: "apiKey"},
		{Order: 4, Name: "receiverId", Label: "Group ID / Phone Number", HelpText: "Group ID / Phone Number of the receiver", Type: "textbox", Privacy: "normal"},
		{Order: 5, Name: "authUsername", Label: "Username", HelpText: "Username used to authenticate requests toward signal-api", Type: "textbox", Privacy: "userName"},
		{Order: 6, Name: "authPassword", Label: "Password", HelpText: "Password used to authenticate requests toward signal-api", Type: "password", Privacy: "password"},
	}
	return s
}

func simplepushSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "SimplepushSettings"
	s.Implementation = "Simplepush"
	s.ImplementationName = "Simplepush"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#simplepush"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "key", Label: "Key", HelpLink: "https://simplepush.io/features", Type: "textbox", Privacy: "apiKey"},
		{Order: 1, Name: "event", Label: "Event", HelpText: "Customize the behavior of push notifications", HelpLink: "https://simplepush.io/features", Type: "textbox", Privacy: "normal"},
	}
	return s
}

func slackSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "SlackSettings"
	s.Implementation = "Slack"
	s.ImplementationName = "Slack"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#slack"
	s.Fields = []FieldDef{
		{Order: 0, Name: "webHookUrl", Label: "Webhook URL", HelpText: "Slack channel webhook url", HelpLink: "https://my.slack.com/services/new/incoming-webhook/", Type: "url", Privacy: "normal"},
		{Order: 1, Name: "username", Label: "Username", HelpText: "Username to post to Slack as", Type: "textbox", Privacy: "userName"},
		{Order: 2, Name: "icon", Label: "Icon", HelpText: "Change the icon that is used for messages posted to Slack (Emoji or URL)", HelpLink: "http://www.emoji-cheat-sheet.com/", Type: "textbox", Privacy: "normal"},
		{Order: 3, Name: "channel", Label: "Channel", HelpText: "Overrides the default channel for the incoming webhook (#other-channel)", Type: "textbox", Privacy: "normal"},
	}
	return s
}

func synologyIndexerSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "SynologyIndexerSettings"
	s.Implementation = "SynologyIndexer"
	s.ImplementationName = "Synology Indexer"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#synologyindexer"
	// Synology Indexer can only react to filesystem-level events
	// (Download/Upgrade/Rename/SeriesAdd/SeriesDelete/EpisodeFile*).
	// It can't surface Grab / Health / ApplicationUpdate /
	// ManualInteractionRequired because there's no synoindex hook
	// for those abstract Sonarr-side events.
	s.SupportsOnGrab = false
	s.SupportsOnHealthIssue = false
	s.SupportsOnHealthRestored = false
	s.SupportsOnApplicationUpdate = false
	s.SupportsOnManualInteractionRequired = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "updateLibrary", Label: "Update Library", HelpText: "Call synoindex on localhost to update a library file", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
	}
	return s
}

func telegramSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "TelegramSettings"
	s.Implementation = "Telegram"
	s.ImplementationName = "Telegram"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#telegram"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "botToken", Label: "Bot Token", HelpLink: "https://core.telegram.org/bots", Type: "textbox", Privacy: "apiKey"},
		{Order: 1, Name: "chatId", Label: "Chat ID", HelpText: "You must start a conversation with the bot or add it to your group to receive messages", HelpLink: "http://stackoverflow.com/a/37396871/882971", Type: "textbox", Privacy: "normal"},
		{Order: 2, Name: "topicId", Label: "Topic ID", HelpText: "Specify a Topic ID to send notifications to that topic. Leave blank to use the general topic (Supergroups only)", HelpLink: "https://stackoverflow.com/a/75178418", Type: "textbox", Privacy: "normal"},
		{Order: 3, Name: "sendSilently", Label: "Send Silently", HelpText: "Sends the message silently. Users will receive a notification with no sound", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 4, Name: "includeAppNameInTitle", Label: "Include Sonarr in Title", HelpText: "Optionally prefix message title with Sonarr to differentiate notifications from different applications", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 5, Name: "includeInstanceNameInTitle", Label: "Include Instance Name in Title", HelpText: "Optionally include Instance name in notification", Type: "checkbox", Privacy: "normal", Advanced: true, Value: false, ValueSet: true},
		{
			Order: 6, Name: "metadataLinks", Label: "Metadata Links",
			HelpText: "Add a links to series metadata when sending notifications",
			Type:     "select", Privacy: "normal",
			SelectOptions: notificationMetadataLinksOptions(),
			Value:         []int{}, ValueSet: true,
		},
		{
			Order: 7, Name: "linkPreview", Label: "Link Preview",
			HelpText: "Determines which link will be previewed in the Telegram notification. Choose 'None' to disable",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: -1, Name: "None", Order: 9, Hint: "(-1)"},
				{Value: 0, Name: "IMDb", Order: 12, Hint: "(0)"},
				{Value: 2, Name: "TVMaze", Order: 19, Hint: "(2)"},
				{Value: 3, Name: "Trakt", Order: 22, Hint: "(3)"},
			},
			Value: -1, ValueSet: true,
		},
	}
	return s
}

func traktSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "TraktSettings"
	s.Implementation = "Trakt"
	s.ImplementationName = "Trakt"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#trakt"
	// Trakt is a scrobble-style sink — it only consumes Import /
	// Upgrade / SeriesAdd / SeriesDelete / EpisodeFile events.
	s.SupportsOnGrab = false
	s.SupportsOnRename = false
	s.SupportsOnHealthIssue = false
	s.SupportsOnHealthRestored = false
	s.SupportsOnApplicationUpdate = false
	s.SupportsOnManualInteractionRequired = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "accessToken", Label: "Access Token", Type: "textbox", Privacy: "normal", Hidden: "hidden"},
		{Order: 1, Name: "refreshToken", Label: "Refresh Token", Type: "textbox", Privacy: "normal", Hidden: "hidden"},
		{Order: 2, Name: "expires", Label: "Expires", Type: "textbox", Privacy: "normal", Hidden: "hidden", Value: "0001-01-01T00:00:00Z", ValueSet: true},
		{Order: 3, Name: "authUser", Label: "Auth User", Type: "textbox", Privacy: "normal", Hidden: "hidden"},
		{Order: 4, Name: "signIn", Label: "Authenticate with Trakt", Type: "oAuth", Privacy: "normal", Value: "startOAuth", ValueSet: true},
	}
	return s
}

func twitterSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "TwitterSettings"
	s.Implementation = "Twitter"
	s.ImplementationName = "Twitter"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#twitter"
	s.SupportsOnRename = false
	s.Fields = []FieldDef{
		{Order: 0, Name: "consumerKey", Label: "Consumer Key", HelpText: "Consumer key from a Twitter application", HelpLink: "https://wiki.servarr.com/useful-tools#twitter-connect", Type: "textbox", Privacy: "apiKey"},
		{Order: 1, Name: "consumerSecret", Label: "Consumer Secret", HelpText: "Consumer secret from a Twitter application", HelpLink: "https://wiki.servarr.com/useful-tools#twitter-connect", Type: "textbox", Privacy: "apiKey"},
		{Order: 2, Name: "accessToken", Label: "Access Token", Type: "textbox", Privacy: "apiKey", Advanced: true},
		{Order: 3, Name: "accessTokenSecret", Label: "Access Token Secret", Type: "textbox", Privacy: "apiKey", Advanced: true},
		{Order: 4, Name: "mention", Label: "Mention", HelpText: "Mention this user in sent tweets", Type: "textbox", Privacy: "normal"},
		{Order: 5, Name: "directMessage", Label: "Direct Message", HelpText: "Send a direct message instead of a public message", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
		{Order: 6, Name: "authorizeNotification", Label: "Connect to Twitter / X", Type: "oAuth", Privacy: "normal", Value: "startOAuth", ValueSet: true},
	}
	return s
}

func webhookSchema() NotificationSchemaResource {
	s := notificationSchemaShell()
	s.ConfigContract = "WebhookSettings"
	s.Implementation = "Webhook"
	s.ImplementationName = "Webhook"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#webhook"
	s.Fields = []FieldDef{
		{Order: 0, Name: "url", Label: "Webhook URL", Type: "url", Privacy: "normal"},
		{
			Order: 1, Name: "method", Label: "Method",
			HelpText: "Which HTTP method to use submit to the Webservice",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 1, Name: "POST", Order: 1},
				{Value: 2, Name: "PUT", Order: 2},
			},
			Value: 1, ValueSet: true,
		},
		{Order: 2, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 3, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 4, Name: "headers", Label: "Headers", Type: "keyValueList", Privacy: "normal", Advanced: true, Value: []any{}, ValueSet: true},
	}
	return s
}

// NotificationSchemaHandler returns the http.Handler for
// GET /api/v3/notification/schema. Mirrors the C# pattern at
// `NotificationController.GetTemplates` (inherited from
// `ProviderControllerBase`) — emits the per-provider schema template
// for every registered INotification implementation, sorted by
// ImplementationName ASC (case-insensitive — ntfy.sh sits between
// Notifiarr and Plex Media Server because lowercase 'n' compares to
// uppercase 'N' as equal under OrdinalIgnoreCase).
func NotificationSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(notificationSchemas())
		_, _ = w.Write(buf)
	})
}
