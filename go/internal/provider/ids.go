package provider

// This file catalogues every (Impl, Contract) pair the C# tree ships at
// HEAD, plus every CustomFormat / AutoTagging spec type-string the
// embedded JSON envelopes reference. The strings are sourced from
// `analysis/providers/*.md` (verified against the C# source by the
// 2026-05-02 deep-dive) and `analysis/embedded-docs/catalogue.md` §
// Polymorphic envelopes. Updating these strings without a
// corresponding C# rename will break round-trip with existing user
// databases.
//
// Default returns a fresh Registry pre-populated with every known
// identifier. Constructors are nil at Phase 1 — Phase 4 swaps them in
// as each provider class is ported. The catalog is deliberately
// frozen-in-time as data so Phase 1 storage tests can assert
// "Implementation+ConfigContract round-trip into a known pair" without
// also depending on Phase 4 constructor wiring.

// Default returns a Registry seeded with the canonical Sonarr provider
// identifier set. Callers who only need identifier validation should
// share one Default registry per process; callers wiring real
// constructors in Phase 4 should fork their own registry instance via
// NewRegistry() and call Register on each constructor.
func Default() *Registry {
	r := NewRegistry()
	for _, k := range indexerKeys {
		r.MustRegister(FamilyIndexer, k, nil)
	}
	for _, k := range downloadClientKeys {
		r.MustRegister(FamilyDownloadClient, k, nil)
	}
	for _, k := range notificationKeys {
		r.MustRegister(FamilyNotification, k, nil)
	}
	for _, k := range importListKeys {
		r.MustRegister(FamilyImportList, k, nil)
	}
	for _, k := range metadataKeys {
		r.MustRegister(FamilyMetadata, k, nil)
	}
	for _, name := range customFormatSpecTypes {
		r.MustRegister(FamilyCustomFormatSpec, Key{Impl: name}, nil)
	}
	for _, name := range autoTaggingSpecTypes {
		r.MustRegister(FamilyAutoTaggingSpec, Key{Impl: name}, nil)
	}
	return r
}

// indexerKeys — 10 concrete providers per analysis/providers/indexers.md.
var indexerKeys = []Key{
	{Impl: "Newznab", Contract: "NewznabSettings"},
	{Impl: "Torznab", Contract: "TorznabSettings"},
	{Impl: "Fanzub", Contract: "FanzubSettings"},
	{Impl: "BroadcastheNet", Contract: "BroadcastheNetSettings"},
	{Impl: "HDBits", Contract: "HDBitsSettings"},
	{Impl: "IPTorrents", Contract: "IPTorrentsSettings"},
	{Impl: "Nyaa", Contract: "NyaaSettings"},
	{Impl: "Torrentleech", Contract: "TorrentleechSettings"},
	{Impl: "FileList", Contract: "FileListSettings"},
	{Impl: "TorrentRssIndexer", Contract: "TorrentRssIndexerSettings"},
}

// downloadClientKeys — 20 concrete providers per
// analysis/providers/download-clients.md. Two pairs share a Contract:
// (Vuze, Transmission) → TransmissionSettings;
// (UsenetDownloadStation, TorrentDownloadStation) → DownloadStationSettings.
// Pair-keying handles both cases unambiguously.
var downloadClientKeys = []Key{
	{Impl: "Sabnzbd", Contract: "SabnzbdSettings"},
	{Impl: "Nzbget", Contract: "NzbgetSettings"},
	{Impl: "NzbVortex", Contract: "NzbVortexSettings"},
	{Impl: "UsenetDownloadStation", Contract: "DownloadStationSettings"},
	{Impl: "UsenetBlackhole", Contract: "UsenetBlackholeSettings"},
	{Impl: "Pneumatic", Contract: "PneumaticSettings"},
	{Impl: "QBittorrent", Contract: "QBittorrentSettings"},
	{Impl: "Transmission", Contract: "TransmissionSettings"},
	{Impl: "Vuze", Contract: "TransmissionSettings"},
	{Impl: "Deluge", Contract: "DelugeSettings"},
	{Impl: "UTorrent", Contract: "UTorrentSettings"},
	{Impl: "RTorrent", Contract: "RTorrentSettings"},
	{Impl: "Aria2", Contract: "Aria2Settings"},
	{Impl: "RQBit", Contract: "RQbitSettings"},
	{Impl: "Hadouken", Contract: "HadoukenSettings"},
	{Impl: "Flood", Contract: "FloodSettings"},
	{Impl: "TorrentDownloadStation", Contract: "DownloadStationSettings"},
	{Impl: "TorrentFreeboxDownload", Contract: "FreeboxDownloadSettings"},
	{Impl: "TorrentBlackhole", Contract: "TorrentBlackholeSettings"},
	{Impl: "TriblerDownloadClient", Contract: "TriblerDownloadSettings"},
}

// notificationKeys — 25 concrete providers per
// analysis/providers/notifications.md.
var notificationKeys = []Key{
	{Impl: "Apprise", Contract: "AppriseSettings"},
	{Impl: "CustomScript", Contract: "CustomScriptSettings"},
	{Impl: "Discord", Contract: "DiscordSettings"},
	{Impl: "Email", Contract: "EmailSettings"},
	{Impl: "Gotify", Contract: "GotifySettings"},
	{Impl: "Join", Contract: "JoinSettings"},
	{Impl: "Mailgun", Contract: "MailgunSettings"},
	{Impl: "MediaBrowser", Contract: "MediaBrowserSettings"},
	{Impl: "Notifiarr", Contract: "NotifiarrSettings"},
	{Impl: "Ntfy", Contract: "NtfySettings"},
	{Impl: "PlexServer", Contract: "PlexServerSettings"},
	{Impl: "Prowl", Contract: "ProwlSettings"},
	{Impl: "PushBullet", Contract: "PushBulletSettings"},
	{Impl: "Pushcut", Contract: "PushcutSettings"},
	{Impl: "Pushover", Contract: "PushoverSettings"},
	{Impl: "SendGrid", Contract: "SendGridSettings"},
	{Impl: "Signal", Contract: "SignalSettings"},
	{Impl: "Simplepush", Contract: "SimplepushSettings"},
	{Impl: "Slack", Contract: "SlackSettings"},
	{Impl: "SynologyIndexer", Contract: "SynologyIndexerSettings"},
	{Impl: "Telegram", Contract: "TelegramSettings"},
	{Impl: "Trakt", Contract: "TraktSettings"},
	{Impl: "Twitter", Contract: "TwitterSettings"},
	{Impl: "Webhook", Contract: "WebhookSettings"},
	{Impl: "Xbmc", Contract: "XbmcSettings"},
}

// importListKeys — 10 concrete providers per
// analysis/providers/import-lists.md.
var importListKeys = []Key{
	{Impl: "PlexImport", Contract: "PlexListSettings"},
	{Impl: "PlexRssImport", Contract: "PlexRssImportSettings"},
	{Impl: "TraktUserImport", Contract: "TraktUserSettings"},
	{Impl: "TraktPopularImport", Contract: "TraktPopularSettings"},
	{Impl: "TraktListImport", Contract: "TraktListSettings"},
	{Impl: "SimklUserImport", Contract: "SimklUserSettings"},
	{Impl: "AniListImport", Contract: "AniListSettings"},
	{Impl: "MyAnimeListImport", Contract: "MyAnimeListSettings"},
	{Impl: "SonarrImport", Contract: "SonarrSettings"},
	{Impl: "CustomImport", Contract: "CustomSettings"},
}

// metadataKeys — 4 concrete metadata writers per
// analysis/providers/metadata-writers.md. (The 2 metadata-source providers
// in metadata-sources.md feed the SkyHook proxy rather than the Metadata
// table, so they don't appear here.)
var metadataKeys = []Key{
	{Impl: "XbmcMetadata", Contract: "XbmcMetadataSettings"},
	{Impl: "PlexMetadata", Contract: "PlexMetadataSettings"},
	{Impl: "RoksboxMetadata", Contract: "RoksboxMetadataSettings"},
	{Impl: "WdtvMetadata", Contract: "WdtvMetadataSettings"},
}

// customFormatSpecTypes — 8 concrete spec classes per
// analysis/embedded-docs/catalogue.md § CustomFormatSpecificationConverter.
// The strings appear inside the Specifications JSON envelope's `type`
// field, not in any Implementation column. Migration 205 renamed
// "SeasonPackSpecification" → "ReleaseTypeSpecification" in existing
// rows, so SeasonPackSpecification is intentionally absent from the
// HEAD catalog.
var customFormatSpecTypes = []string{
	"ReleaseTitleSpecification",
	"ReleaseGroupSpecification",
	"LanguageSpecification",
	"IndexerFlagSpecification",
	"SourceSpecification",
	"ResolutionSpecification",
	"SizeSpecification",
	"ReleaseTypeSpecification",
}

// autoTaggingSpecTypes — 11 concrete spec classes per
// analysis/embedded-docs/catalogue.md § AutoTagSpecificationConverter.
var autoTaggingSpecTypes = []string{
	"GenreSpecification",
	"MonitoredSpecification",
	"NetworkSpecification",
	"OriginalCountrySpecification",
	"OriginalLanguageSpecification",
	"QualityProfileSpecification",
	"RootFolderSpecification",
	"SeriesTypeSpecification",
	"StatusSpecification",
	"TagSpecification",
	"YearSpecification",
}
