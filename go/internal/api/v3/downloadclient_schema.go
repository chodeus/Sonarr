package v3

import (
	"encoding/json"
	"net/http"
)

// DownloadClientSchemaResource is the JSON shape produced by
// GET /api/v3/downloadclient/schema. Mirrors
// `src/Sonarr.Api.V3/DownloadClient/DownloadClientResource.cs`
// extending `ProviderResource<DownloadClientResource>` at
// `src/Sonarr.Api.V3/ProviderResource.cs`.
//
// Schema-row field source (C# → Go), every key on the wire:
//
//	configContract            → from ProviderResource (base)
//	enable                    → bool — false on schemas
//	fields                    → SchemaBuilder.ToSchema(definition.Settings)
//	implementation            → struct name
//	implementationName        → user-facing label
//	infoLink                  → "https://wiki.servarr.com/sonarr/supported#" + lower(Implementation)
//	message                   → optional ProviderMessage (only some impls
//	                            carry a static info/warning banner)
//	name                      → "" on schemas (the C# factory leaves
//	                            Definition.Name unset on the schema
//	                            template; user supplies it on save)
//	presets                   → [] (the downloadclient factory has no
//	                            presets — distinguishes from indexer
//	                            Newznab which does)
//	priority                  → int — defaults to 1 on every C#
//	                            DownloadClientDefinition ctor
//	protocol                  → "torrent" | "usenet"
//	removeCompletedDownloads  → bool — defaults true
//	removeFailedDownloads     → bool — defaults true
//	tags                      → [] on schemas
//
// Keys omitted (C# WhenWritingNull on null):
//
//	id → null on schemas, omitted.
//	message → omitted on impls that don't carry one.
//
// Wire ordering is alphabetical because the contract-test normalizer
// sorts JSON keys before comparison; Go's reflection-driven marshaler
// emits in declaration order, which is fine here.
type DownloadClientSchemaResource struct {
	ConfigContract           string                 `json:"configContract"`
	Enable                   bool                   `json:"enable"`
	Fields                   []FieldDef             `json:"fields"`
	Implementation           string                 `json:"implementation"`
	ImplementationName       string                 `json:"implementationName"`
	InfoLink                 string                 `json:"infoLink"`
	Message                  *DownloadClientMessage `json:"message,omitempty"`
	Name                     string                 `json:"name"`
	Presets                  []any                  `json:"presets"`
	Priority                 int                    `json:"priority"`
	Protocol                 string                 `json:"protocol"`
	RemoveCompletedDownloads bool                   `json:"removeCompletedDownloads"`
	RemoveFailedDownloads    bool                   `json:"removeFailedDownloads"`
	Tags                     []int                  `json:"tags"`
}

// DownloadClientMessage is the optional static banner some C# impls
// surface on the schema row (Synology Download Station 2FA notice,
// rTorrent seed-criteria explainer, Tribler experimental warning,
// uTorrent malware warning, …). Mirrors
// `src/NzbDrone.Core/Annotations/ProviderMessage.cs`.
type DownloadClientMessage struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// downloadClientSchemas is the canonical 20-entry table in the same
// order the C# `ProviderControllerBase` schema-template projection
// yields: `_factory.GetAvailableProviders().OrderBy(d =>
// d.ImplementationName)` with a stable secondary key falling out of
// the underlying DI scan order (torrent-flavoured impl before the
// usenet sibling when implementationName ties, e.g.
// TorrentDownloadStation before UsenetDownloadStation).
func downloadClientSchemas() []DownloadClientSchemaResource {
	return []DownloadClientSchemaResource{
		aria2Schema(),
		delugeSchema(),
		torrentDownloadStationSchema(),
		usenetDownloadStationSchema(),
		floodSchema(),
		torrentFreeboxDownloadSchema(),
		hadoukenSchema(),
		nzbgetSchema(),
		nzbVortexSchema(),
		pneumaticSchema(),
		qbittorrentSchema(),
		rqbitSchema(),
		rtorrentSchema(),
		sabnzbdSchema(),
		torrentBlackholeSchema(),
		transmissionSchema(),
		triblerSchema(),
		usenetBlackholeSchema(),
		utorrentSchema(),
		vuzeSchema(),
	}
}

// downloadClientSchemaShell returns the per-impl wrapper with the
// defaults every C# DownloadClientDefinition ctor produces (Priority
// =1, RemoveCompleted=true, RemoveFailed=true). Presets is always an
// empty slice (the downloadclient factory has no presets).
func downloadClientSchemaShell() DownloadClientSchemaResource {
	return DownloadClientSchemaResource{
		Enable:                   false,
		Presets:                  []any{},
		Priority:                 1,
		RemoveCompletedDownloads: true,
		RemoveFailedDownloads:    true,
		Tags:                     []int{},
	}
}

// dcCategoryHelpText is the recurring "Category" field helpText —
// shared by every download client that exposes a tvCategory textbox.
const dcCategoryHelpText = "Adding a category specific to Sonarr avoids conflicts with unrelated non-Sonarr downloads. Using a category is optional, but strongly recommended."

// dcCategoryHelpTextWithSubdir is the Download Station / Transmission /
// Vuze / TorrentFreeboxDownload variant — appends a "creates a
// [category] subdirectory" hint.
const dcCategoryHelpTextWithSubdir = dcCategoryHelpText + " Creates a [category] subdirectory in the output directory."

// dcImportedCategoryHelpText is the "Post-Import Category" helpText
// shared by Deluge / QBittorrent / RTorrent / Transmission / UTorrent
// / Vuze.
const dcImportedCategoryHelpText = "Category for Sonarr to set after it has imported the download. Sonarr will not remove torrents in that category even if seeding finished. Leave blank to keep same category."

// dcRecentPriorityHelpText / dcOlderPriorityHelpText — Recent/Older
// Priority field helpText, shared by every client that exposes the
// pair (Deluge / Flood / NZBGet / NZBVortex / QBittorrent / RTorrent
// / Sabnzbd / Transmission / UTorrent / Vuze / TorrentFreeboxDownload).
const (
	dcRecentPriorityHelpText = "Priority to use when grabbing episodes that aired within the last 14 days"
	dcOlderPriorityHelpText  = "Priority to use when grabbing episodes that aired over 14 days ago"
)

// dcLastFirstOptions is the 2-option Last/First select used by most
// torrent clients for Recent/Older priority (Deluge / QBittorrent /
// Transmission / UTorrent / Vuze / TorrentFreeboxDownload).
func dcLastFirstOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Last", Order: 0},
		{Value: 1, Name: "First", Order: 1},
	}
}

// dcLastFirstPriorityField builds a Recent/Older priority Select
// field whose options are the standard Last/First pair.
func dcLastFirstPriorityField(order int, name, label, helpText string) FieldDef {
	return FieldDef{
		Order: order, Name: name, Label: label, HelpText: helpText,
		Type:          "select",
		Privacy:       "normal",
		SelectOptions: dcLastFirstOptions(),
		Value:         0, ValueSet: true,
	}
}

// ---------- per-impl ctors ----------

func aria2Schema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "Aria2Settings"
	s.Implementation = "Aria2"
	s.ImplementationName = "Aria2"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#aria2"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "number", Privacy: "normal", Value: 6800, ValueSet: true},
		{Order: 2, Name: "rpcPath", Label: "XML RPC Path", Type: "textbox", Privacy: "normal", Value: "/rpc", ValueSet: true},
		{Order: 3, Name: "useSsl", Label: "Use SSL", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 4, Name: "secretToken", Label: "Secret Token", Type: "password", Privacy: "password", Value: "********", ValueSet: true},
		{
			Order: 5, Name: "directory", Label: "Directory",
			HelpText: "Optional location to put downloads in, leave blank to use the default Aria2 location",
			Type:     "textbox", Privacy: "normal",
		},
	}
	return s
}

func delugeSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "DelugeSettings"
	s.Implementation = "Deluge"
	s.ImplementationName = "Deluge"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#deluge"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8112, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to Deluge", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the deluge json url, see http://[host]:[port]/[urlBase]/json", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "password", Label: "Password", Type: "password", Privacy: "password", Value: "********", ValueSet: true},
		{Order: 5, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "tv-sonarr", ValueSet: true},
		{Order: 6, Name: "tvImportedCategory", Label: "Post-Import Category", HelpText: dcImportedCategoryHelpText, Type: "textbox", Privacy: "normal", Advanced: true},
		dcLastFirstPriorityField(7, "recentTvPriority", "Recent Priority", dcRecentPriorityHelpText),
		dcLastFirstPriorityField(8, "olderTvPriority", "Older Priority", dcOlderPriorityHelpText),
		{Order: 9, Name: "addPaused", Label: "Add Paused", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 10, Name: "downloadDirectory", Label: "Download Directory", HelpText: "Optional location to put downloads in, leave blank to use the default Deluge location", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 11, Name: "completedDirectory", Label: "Move When Completed Directory", HelpText: "Optional location to move completed downloads to, leave blank to use the default Deluge location", Type: "textbox", Privacy: "normal", Advanced: true},
	}
	return s
}

// downloadStationFields builds the 7-field DownloadStation field
// list shared by the Torrent and Usenet variants — they differ only
// in implementation/configContract/protocol.
func downloadStationFields() []FieldDef {
	return []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "127.0.0.1", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 5000, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to Download Station", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 4, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 5, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpTextWithSubdir, Type: "textbox", Privacy: "normal"},
		{Order: 6, Name: "tvDirectory", Label: "Directory", HelpText: "Optional shared folder to put downloads into, leave blank to use the default Download Station location", Type: "textbox", Privacy: "normal"},
	}
}

func downloadStationMessage() *DownloadClientMessage {
	return &DownloadClientMessage{
		Message: "Sonarr is unable to connect to Download Station if 2-Factor Authentication is enabled on your DSM account",
		Type:    "warning",
	}
}

func torrentDownloadStationSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "DownloadStationSettings"
	s.Implementation = "TorrentDownloadStation"
	s.ImplementationName = "Download Station"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#torrentdownloadstation"
	s.Protocol = "torrent"
	s.Message = downloadStationMessage()
	s.Fields = downloadStationFields()
	return s
}

func usenetDownloadStationSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "DownloadStationSettings"
	s.Implementation = "UsenetDownloadStation"
	s.ImplementationName = "Download Station"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#usenetdownloadstation"
	s.Protocol = "usenet"
	s.Message = downloadStationMessage()
	s.Fields = downloadStationFields()
	return s
}

func floodSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "FloodSettings"
	s.Implementation = "Flood"
	s.ImplementationName = "Flood"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#flood"
	s.Protocol = "torrent"
	s.Message = &DownloadClientMessage{
		Message: "Sonarr will handle automatic removal of torrents based on the current seed criteria in Settings -> Indexers",
		Type:    "info",
	}
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 3000, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to Flood", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Flood API, such as [protocol]://[host]:[port]/[urlBase]/api", Type: "textbox", Privacy: "normal"},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "destination", Label: "Destination", HelpText: "Manually specifies download destination, leave blank to use the default", Type: "textbox", Privacy: "normal"},
		{Order: 7, Name: "tags", Label: "Tags", HelpText: "Initial tags of a download. To be recognized, a download must have all initial tags. This avoids conflicts with unrelated downloads.", Type: "tag", Privacy: "normal", Value: []string{"sonarr"}, ValueSet: true},
		{Order: 8, Name: "postImportTags", Label: "Post-Import Tags", HelpText: "Appends tags after a download is imported. Torrents with all these tags will be filtered from the queue and will not removed automatically.", Type: "tag", Privacy: "normal", Advanced: true},
		{
			Order: 9, Name: "additionalTags", Label: "Additional Tags",
			HelpText: "Adds properties of media as tags. Hints are examples.",
			Type:     "select", Privacy: "normal", Advanced: true,
			SelectOptions: []SelectOption{
				{Value: 0, Name: "TitleSlug", Order: 7, Hint: "big-buck-bunny"},
				{Value: 1, Name: "Quality", Order: 10, Hint: "Bluray-2160p"},
				{Value: 2, Name: "Languages", Order: 13, Hint: "English"},
				{Value: 3, Name: "ReleaseGroup", Order: 16, Hint: "Example-Raws"},
				{Value: 4, Name: "Year", Order: 19, Hint: "2020"},
				{Value: 5, Name: "Indexer", Order: 22, Hint: "Torznab"},
				{Value: 6, Name: "Network", Order: 25, Hint: "C-SPAN"},
			},
			Value: []int{}, ValueSet: true,
		},
		{Order: 10, Name: "startOnAdd", Label: "Start on Add", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
	}
	return s
}

func torrentFreeboxDownloadSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "FreeboxDownloadSettings"
	s.Implementation = "TorrentFreeboxDownload"
	s.ImplementationName = "Freebox Download"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#torrentfreeboxdownload"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", HelpText: "Hostname or host IP address of the Freebox, defaults to 'mafreebox.freebox.fr' (will only work if on same network)", Type: "textbox", Privacy: "normal", Value: "mafreebox.freebox.fr", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", HelpText: "Port used to access Freebox interface, defaults to '443'", Type: "textbox", Privacy: "normal", Value: 443, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to Freebox API", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
		{Order: 3, Name: "apiUrl", Label: "API URL", HelpText: "Define Freebox API base URL with API version, eg 'http://[host]:[port]/[api_base_url]/[api_version]/', defaults to '/api/v1/'", Type: "textbox", Privacy: "normal", Advanced: true, Value: "/api/v1/", ValueSet: true},
		{Order: 4, Name: "appId", Label: "App ID", HelpText: "App ID given when creating access to Freebox API (ie 'app_id')", Type: "textbox", Privacy: "normal"},
		{Order: 5, Name: "appToken", Label: "App Token", HelpText: "App token retrieved when creating access to Freebox API (ie 'app_token')", Type: "password", Privacy: "password"},
		{Order: 6, Name: "destinationDirectory", Label: "Destination", HelpText: "Manually specifies download destination, leave blank to use the default", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 7, Name: "category", Label: "Category", HelpText: dcCategoryHelpTextWithSubdir, Type: "textbox", Privacy: "normal"},
		dcLastFirstPriorityField(8, "recentPriority", "Recent Priority", dcRecentPriorityHelpText),
		dcLastFirstPriorityField(9, "olderPriority", "Older Priority", dcOlderPriorityHelpText),
		{Order: 10, Name: "addPaused", Label: "Add Paused", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
	}
	return s
}

func hadoukenSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "HadoukenSettings"
	s.Implementation = "Hadouken"
	s.ImplementationName = "Hadouken"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#hadouken"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 7070, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to Hadouken", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Hadouken url, such as http://[host]:[port]/[urlBase]/api", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "category", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "sonarr-tv", ValueSet: true},
	}
	return s
}

// nzbgetPriorityOptions — NZBGet's 6-priority enum used for both
// Recent and Older priority selects.
func nzbgetPriorityOptions() []SelectOption {
	return []SelectOption{
		{Value: -100, Name: "VeryLow", Order: -100},
		{Value: -50, Name: "Low", Order: -50},
		{Value: 0, Name: "Normal", Order: 0},
		{Value: 50, Name: "High", Order: 50},
		{Value: 100, Name: "VeryHigh", Order: 100},
		{Value: 900, Name: "Force", Order: 900},
	}
}

func nzbgetSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "NzbgetSettings"
	s.Implementation = "Nzbget"
	s.ImplementationName = "NZBGet"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#nzbget"
	s.Protocol = "usenet"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 6789, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to NZBGet", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the NZBGet url, such as http://[host]:[port]/[urlBase]/jsonrpc", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "tv", ValueSet: true},
		{
			Order: 7, Name: "recentTvPriority", Label: "Recent Priority",
			HelpText: dcRecentPriorityHelpText,
			Type:     "select", Privacy: "normal",
			SelectOptions: nzbgetPriorityOptions(), Value: 0, ValueSet: true,
		},
		{
			Order: 8, Name: "olderTvPriority", Label: "Older Priority",
			HelpText: dcOlderPriorityHelpText,
			Type:     "select", Privacy: "normal",
			SelectOptions: nzbgetPriorityOptions(), Value: 0, ValueSet: true,
		},
		{Order: 9, Name: "addPaused", Label: "Add Paused", HelpText: "This option requires at least NzbGet version 16.0", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
	}
	return s
}

// nzbvortexPriorityOptions — NZBVortex's 3-priority enum.
func nzbvortexPriorityOptions() []SelectOption {
	return []SelectOption{
		{Value: -1, Name: "Low", Order: -1},
		{Value: 0, Name: "Normal", Order: 0},
		{Value: 1, Name: "High", Order: 1},
	}
}

func nzbVortexSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "NzbVortexSettings"
	s.Implementation = "NzbVortex"
	s.ImplementationName = "NZBVortex"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#nzbvortex"
	s.Protocol = "usenet"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 4321, ValueSet: true},
		{Order: 2, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the NZBVortex url, such as http://[host]:[port]/[urlBase]/api", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 3, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		{Order: 4, Name: "tvCategory", Label: "Group", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "TV Shows", ValueSet: true},
		{
			Order: 5, Name: "recentTvPriority", Label: "Recent Priority",
			HelpText: dcRecentPriorityHelpText,
			Type:     "select", Privacy: "normal",
			SelectOptions: nzbvortexPriorityOptions(), Value: 0, ValueSet: true,
		},
		{
			Order: 6, Name: "olderTvPriority", Label: "Older Priority",
			HelpText: dcOlderPriorityHelpText,
			Type:     "select", Privacy: "normal",
			SelectOptions: nzbvortexPriorityOptions(), Value: 0, ValueSet: true,
		},
	}
	return s
}

func pneumaticSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "PneumaticSettings"
	s.Implementation = "Pneumatic"
	s.ImplementationName = "Pneumatic"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#pneumatic"
	s.Protocol = "usenet"
	s.Fields = []FieldDef{
		{Order: 0, Name: "nzbFolder", Label: "Nzb Folder", HelpText: "This folder will need to be reachable from XBMC", Type: "path", Privacy: "normal"},
		{Order: 1, Name: "strmFolder", Label: "Strm Folder", HelpText: ".strm files in this folder will be import by drone", Type: "path", Privacy: "normal"},
	}
	return s
}

func qbittorrentSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "QBittorrentSettings"
	s.Implementation = "QBittorrent"
	s.ImplementationName = "qBittorrent"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#qbittorrent"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8080, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use a secure connection. See Options -> Web UI -> 'Use HTTPS instead of HTTP' in qBittorrent.", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the qBittorrent url, such as http://[host]:[port]/[urlBase]/api", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		{Order: 5, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 6, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 7, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "tv-sonarr", ValueSet: true},
		{Order: 8, Name: "tvImportedCategory", Label: "Post-Import Category", HelpText: dcImportedCategoryHelpText, Type: "textbox", Privacy: "normal", Advanced: true},
		dcLastFirstPriorityField(9, "recentTvPriority", "Recent Priority", dcRecentPriorityHelpText),
		dcLastFirstPriorityField(10, "olderTvPriority", "Older Priority", dcOlderPriorityHelpText),
		{
			Order: 11, Name: "initialState", Label: "Initial State",
			HelpText: "Initial state for torrents added to qBittorrent. Note that Forced Torrents do not abide by seed restrictions",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Started", Order: 7, Hint: "(0)"},
				{Value: 1, Name: "Force Started", Order: 10, Hint: "(1)"},
				{Value: 2, Name: "Stopped", Order: 13, Hint: "(2)"},
			},
			Value: 0, ValueSet: true,
		},
		{Order: 12, Name: "sequentialOrder", Label: "Sequential Order", HelpText: "Download in sequential order (qBittorrent 4.1.0+)", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 13, Name: "firstAndLast", Label: "First and Last First", HelpText: "Download first and last pieces first (qBittorrent 4.1.0+)", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{
			Order: 14, Name: "contentLayout", Label: "Content Layout",
			HelpText: "Whether to use qBittorrent's configured content layout, the original layout from the torrent or always create a subfolder (qBittorrent 4.3.2+)",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Default", Order: 0},
				{Value: 1, Name: "Original", Order: 1},
				{Value: 2, Name: "Subfolder", Order: 2},
			},
			Value: 0, ValueSet: true,
		},
		{Order: 15, Name: "addSeriesTags", Label: "Add Series Tags", HelpText: "Add series tags to new torrents added to the download client (qBittorrent 4.1.0+)", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
	}
	return s
}

func rqbitSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "RQbitSettings"
	s.Implementation = "RQBit"
	s.ImplementationName = "RQBit"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#rqbit"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 3030, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", Type: "textbox", Privacy: "normal", Advanced: true, Value: "/", ValueSet: true},
	}
	return s
}

// rtorrentPriorityOptions — rTorrent's 4-priority enum.
func rtorrentPriorityOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "VeryLow", Order: 0},
		{Value: 1, Name: "Low", Order: 1},
		{Value: 2, Name: "Normal", Order: 2},
		{Value: 3, Name: "High", Order: 3},
	}
}

func rtorrentSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "RTorrentSettings"
	s.Implementation = "RTorrent"
	s.ImplementationName = "rTorrent"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#rtorrent"
	s.Protocol = "torrent"
	s.Message = &DownloadClientMessage{
		Message: "rTorrent will not pause torrents when they meet the seed criteria. Sonarr will handle automatic removal of torrents based on the current seed criteria in Settings->Indexers only when Remove Completed is enabled.  After importing it will also set sonarr_imported as an rTorrent view, which can be used in rTorrent scripts to customize behavior.",
		Type:    "info",
	}
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8080, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to rTorrent", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "Url Path", HelpText: "Path to the XMLRPC endpoint, see http(s)://[host]:[port]/[urlPath]. This is usually RPC2 or [path to ruTorrent]/plugins/rpc/rpc.php when using ruTorrent.", Type: "textbox", Privacy: "normal", Value: "RPC2", ValueSet: true},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "tv-sonarr", ValueSet: true},
		{Order: 7, Name: "tvImportedCategory", Label: "Post-Import Category", HelpText: dcImportedCategoryHelpText, Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 8, Name: "tvDirectory", Label: "Directory", HelpText: "Optional location to put downloads in, leave blank to use the default rTorrent location", Type: "textbox", Privacy: "normal", Advanced: true},
		{
			Order: 9, Name: "recentTvPriority", Label: "Recent Priority",
			HelpText: dcRecentPriorityHelpText, Type: "select", Privacy: "normal",
			SelectOptions: rtorrentPriorityOptions(), Value: 2, ValueSet: true,
		},
		{
			Order: 10, Name: "olderTvPriority", Label: "Older Priority",
			HelpText: dcOlderPriorityHelpText, Type: "select", Privacy: "normal",
			SelectOptions: rtorrentPriorityOptions(), Value: 2, ValueSet: true,
		},
		{Order: 11, Name: "addStopped", Label: "Add Stopped", HelpText: "Enabling will add torrents and magnets to rTorrent in a stopped state. This may break magnet files.", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
	}
	return s
}

// sabnzbdPriorityOptions — SABnzbd's 6-priority enum.
func sabnzbdPriorityOptions() []SelectOption {
	return []SelectOption{
		{Value: -100, Name: "Default", Order: -100},
		{Value: -2, Name: "Paused", Order: -2},
		{Value: -1, Name: "Low", Order: -1},
		{Value: 0, Name: "Normal", Order: 0},
		{Value: 1, Name: "High", Order: 1},
		{Value: 2, Name: "Force", Order: 2},
	}
}

func sabnzbdSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "SabnzbdSettings"
	s.Implementation = "Sabnzbd"
	s.ImplementationName = "SABnzbd"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#sabnzbd"
	s.Protocol = "usenet"
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8080, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to Sabnzbd", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Sabnzbd url, such as http://[host]:[port]/[urlBase]/api", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		{Order: 5, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 6, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 7, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "tv", ValueSet: true},
		{
			Order: 8, Name: "recentTvPriority", Label: "Recent Priority",
			HelpText: dcRecentPriorityHelpText, Type: "select", Privacy: "normal",
			SelectOptions: sabnzbdPriorityOptions(), Value: -100, ValueSet: true,
		},
		{
			Order: 9, Name: "olderTvPriority", Label: "Older Priority",
			HelpText: dcOlderPriorityHelpText, Type: "select", Privacy: "normal",
			SelectOptions: sabnzbdPriorityOptions(), Value: -100, ValueSet: true,
		},
	}
	return s
}

func torrentBlackholeSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "TorrentBlackholeSettings"
	s.Implementation = "TorrentBlackhole"
	s.ImplementationName = "Torrent Blackhole"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#torrentblackhole"
	s.Protocol = "torrent"
	s.Fields = []FieldDef{
		{Order: 0, Name: "torrentFolder", Label: "Torrent Folder", HelpText: "Folder in which Sonarr will store the .torrent file", Type: "path", Privacy: "normal"},
		{Order: 1, Name: "watchFolder", Label: "Watch Folder", HelpText: "Folder from which Sonarr should import completed downloads", Type: "path", Privacy: "normal"},
		{Order: 2, Name: "saveMagnetFiles", Label: "Save Magnet Files", HelpText: "Save the magnet link if no .torrent file is available (only useful if the download client supports magnets saved to a file)", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "magnetFileExtension", Label: "Save Magnet Files Extension", HelpText: "Extension to use for magnet links, defaults to '.magnet'", Type: "textbox", Privacy: "normal", Value: ".magnet", ValueSet: true},
		{Order: 4, Name: "readOnly", Label: "Read Only", HelpText: "Instead of moving files this will instruct Sonarr to Copy or Hardlink (depending on settings/system configuration)", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
	}
	return s
}

// transmissionFields is shared by Transmission AND Vuze (the latter
// uses Transmission's RPC and inherits the field shape verbatim; only
// implementation/configContract/port/useSsl helpText/infoLink differ).
// Caller patches the port default + helpText for the "Use SSL" line.
func transmissionFields(port int, sslHelpText string) []FieldDef {
	return []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: port, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: sslHelpText, Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Transmission rpc url, eg http://[host]:[port]/[urlBase]/rpc, defaults to '/transmission/'", Type: "textbox", Privacy: "normal", Advanced: true, Value: "/transmission/", ValueSet: true},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpTextWithSubdir, Type: "textbox", Privacy: "normal", Value: "tv-sonarr", ValueSet: true},
		{Order: 7, Name: "tvImportedCategory", Label: "Post-Import Category", HelpText: dcImportedCategoryHelpText, Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 8, Name: "tvDirectory", Label: "Directory", HelpText: "Optional location to put downloads in, leave blank to use the default Transmission location", Type: "textbox", Privacy: "normal", Advanced: true},
		dcLastFirstPriorityField(9, "recentTvPriority", "Recent Priority", dcRecentPriorityHelpText),
		dcLastFirstPriorityField(10, "olderTvPriority", "Older Priority", dcOlderPriorityHelpText),
		{Order: 11, Name: "addPaused", Label: "Add Paused", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
	}
}

func transmissionSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "TransmissionSettings"
	s.Implementation = "Transmission"
	s.ImplementationName = "Transmission"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#transmission"
	s.Protocol = "torrent"
	s.Fields = transmissionFields(9091, "Use secure connection when connection to Transmission")
	return s
}

func triblerSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "TriblerDownloadSettings"
	s.Implementation = "TriblerDownloadClient"
	s.ImplementationName = "Tribler"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#triblerdownloadclient"
	s.Protocol = "torrent"
	s.Message = &DownloadClientMessage{
		Message: "The tribler integration is highly experimental. Tested against Tribler version 8.0.7.",
		Type:    "warning",
	}
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 20100, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to {clientName}", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the Tribler url, such as http://[host]:[port]/[urlBase]", Type: "textbox", Privacy: "normal", Advanced: true, Value: "", ValueSet: true},
		{Order: 4, Name: "apiKey", Label: "API Key", HelpText: "[api].key from triblerd.conf", Type: "textbox", Privacy: "apiKey"},
		{Order: 5, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal"},
		{Order: 6, Name: "tvDirectory", Label: "Directory", HelpText: "Optional location to put downloads in, leave blank to use the default Tribler location", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 7, Name: "anonymityLevel", Label: "Anonymity level", HelpText: "Number of proxies to use when downloading content. To disable set to 0. Proxies reduce download/upload speed. See https://www.tribler.org/anonymity.html", Type: "number", Privacy: "normal", Value: 1, ValueSet: true},
		{Order: 8, Name: "safeSeeding", Label: "Safe Seeding", HelpText: "When enabled, only seed through proxies.", Type: "checkbox", Privacy: "normal", Value: true, ValueSet: true},
	}
	return s
}

func usenetBlackholeSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "UsenetBlackholeSettings"
	s.Implementation = "UsenetBlackhole"
	s.ImplementationName = "Usenet Blackhole"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#usenetblackhole"
	s.Protocol = "usenet"
	s.Fields = []FieldDef{
		{Order: 0, Name: "nzbFolder", Label: "Nzb Folder", HelpText: "Folder in which Sonarr will store the .nzb file", Type: "path", Privacy: "normal"},
		{Order: 1, Name: "watchFolder", Label: "Watch Folder", HelpText: "Folder from which Sonarr should import completed downloads", Type: "path", Privacy: "normal"},
	}
	return s
}

func utorrentSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "UTorrentSettings"
	s.Implementation = "UTorrent"
	s.ImplementationName = "uTorrent"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#utorrent"
	s.Protocol = "torrent"
	s.Message = &DownloadClientMessage{
		Message: "uTorrent has a history of including cryptominers, malware and ads, we strongly encourage you to choose a different client.",
		Type:    "warning",
	}
	s.Fields = []FieldDef{
		{Order: 0, Name: "host", Label: "Host", Type: "textbox", Privacy: "normal", Value: "localhost", ValueSet: true},
		{Order: 1, Name: "port", Label: "Port", Type: "textbox", Privacy: "normal", Value: 8080, ValueSet: true},
		{Order: 2, Name: "useSsl", Label: "Use SSL", HelpText: "Use secure connection when connection to uTorrent", Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true},
		{Order: 3, Name: "urlBase", Label: "URL Base", HelpText: "Adds a prefix to the uTorrent url, such as http://[host]:[port]/[urlBase]/api", Type: "textbox", Privacy: "normal", Advanced: true},
		{Order: 4, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 5, Name: "password", Label: "Password", Type: "password", Privacy: "password"},
		{Order: 6, Name: "tvCategory", Label: "Category", HelpText: dcCategoryHelpText, Type: "textbox", Privacy: "normal", Value: "tv-sonarr", ValueSet: true},
		{Order: 7, Name: "tvImportedCategory", Label: "Post-Import Category", HelpText: dcImportedCategoryHelpText, Type: "textbox", Privacy: "normal", Advanced: true},
		dcLastFirstPriorityField(8, "recentTvPriority", "Recent Priority", dcRecentPriorityHelpText),
		dcLastFirstPriorityField(9, "olderTvPriority", "Older Priority", dcOlderPriorityHelpText),
		{
			Order: 10, Name: "intialState", Label: "Initial State",
			HelpText: "Initial state for torrents added to uTorrent",
			Type:     "select", Privacy: "normal",
			// `intialState` (typo) preserved verbatim from the C#
			// `UTorrentSettings.IntialState` property name — the
			// wire shape predates the typo fix and changing it would
			// break clients keyed off the misspelled field name.
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Start", Order: 0},
				{Value: 1, Name: "ForceStart", Order: 1},
				{Value: 2, Name: "Pause", Order: 2},
				{Value: 3, Name: "Stop", Order: 3},
			},
			Value: 0, ValueSet: true,
		},
	}
	return s
}

func vuzeSchema() DownloadClientSchemaResource {
	s := downloadClientSchemaShell()
	s.ConfigContract = "TransmissionSettings"
	s.Implementation = "Vuze"
	s.ImplementationName = "Vuze"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#vuze"
	s.Protocol = "torrent"
	// Vuze's "Use SSL" helpText still says "Transmission" in the C#
	// source — the Vuze impl extends TransmissionSettings without
	// overriding the [FieldDefinition].HelpText, so the parent
	// string leaks through. Preserved verbatim.
	s.Fields = transmissionFields(9091, "Use secure connection when connection to Transmission")
	return s
}

// DownloadClientSchemaHandler returns the http.Handler for
// GET /api/v3/downloadclient/schema. Mirrors the C# pattern at
// `DownloadClientController.GetTemplates` (inherited from
// `ProviderControllerBase`) — emits the per-provider schema template
// for every registered IDownloadClient implementation, sorted by
// ImplementationName ASC with a stable tiebreaker that puts the
// torrent-flavoured variant before the usenet sibling (only relevant
// for DownloadStation today).
func DownloadClientSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(downloadClientSchemas())
		_, _ = w.Write(buf)
	})
}
