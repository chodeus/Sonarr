package v3

import (
	"encoding/json"
	"net/http"
	"sort"

	"sonarr.tv/sonarr/internal/language"
)

// IndexerSchemaResource is the JSON shape produced by
// GET /api/v3/indexer/schema and one preset entry inside the
// `presets` array (Newznab carries 9). Mirrors
// `src/Sonarr.Api.V3/Indexers/IndexerResource.cs` extending
// `ProviderResource<IndexerResource>` at
// `src/Sonarr.Api.V3/ProviderResource.cs`.
//
// Schema-row field source (C# → Go), every key on the wire:
//
//	configContract                       → from ProviderResource (base)
//	downloadClientId                     → int — defaults to 0 on schemas
//	enableAutomaticSearch                → bool — IsRssIndexer-style default
//	enableInteractiveSearch              → bool — same
//	enableRss                            → bool — same
//	fields                               → SchemaBuilder.ToSchema(definition.Settings)
//	implementation                       → struct name
//	implementationName                   → user-facing label (top-level rows
//	                                       only; preset rows omit the key)
//	infoLink                             → derived from Implementation.ToLower()
//	name                                 → defaults to Implementation on
//	                                       every provider except Newznab
//	                                       (where the schema row carries
//	                                       name="" since presets supply
//	                                       per-row Name)
//	presets                              → top-level rows always emit
//	                                       (empty `[]` for everyone but
//	                                       Newznab); preset rows omit
//	priority                             → int — defaults to 25 (the
//	                                       IndexerDefinition ctor default)
//	protocol                             → "torrent" | "usenet"
//	seasonSearchMaximumSingleEpisodeAge  → int — defaults to 0
//	supportsRss                          → bool — per-impl static
//	supportsSearch                       → bool — per-impl static
//	tags                                 → [] on schemas
//
// Keys omitted (C# WhenWritingNull on null):
//
//	id / message → null on schemas, omitted on the wire.
//	implementationName, presets → omitted on preset entries (their
//	parent's keys carry the impl-level identification).
//
// Wire ordering is alphabetical because the contract-test normalizer
// sorts JSON keys before comparison; Go's reflection-driven marshaler
// emits in declaration order, which is fine here.
type IndexerSchemaResource struct {
	ConfigContract                      string                   `json:"configContract"`
	DownloadClientID                    int                      `json:"downloadClientId"`
	EnableAutomaticSearch               bool                     `json:"enableAutomaticSearch"`
	EnableInteractiveSearch             bool                     `json:"enableInteractiveSearch"`
	EnableRss                           bool                     `json:"enableRss"`
	Fields                              []FieldDef               `json:"fields"`
	Implementation                      string                   `json:"implementation"`
	ImplementationName                  string                   `json:"implementationName,omitempty"`
	InfoLink                            string                   `json:"infoLink"`
	Name                                string                   `json:"name"`
	Presets                             *[]IndexerSchemaResource `json:"presets,omitempty"`
	Priority                            int                      `json:"priority"`
	Protocol                            string                   `json:"protocol"`
	SeasonSearchMaximumSingleEpisodeAge int                      `json:"seasonSearchMaximumSingleEpisodeAge"`
	SupportsRss                         bool                     `json:"supportsRss"`
	SupportsSearch                      bool                     `json:"supportsSearch"`
	Tags                                []int                    `json:"tags"`
}

// indexerSchemas is the canonical 10-entry table sorted by
// implementationName ASC (matches the C# `ProviderControllerBase`
// schema-template emit order, which is
// `_factory.GetAvailableProviders().OrderBy(d => d.ImplementationName)`).
func indexerSchemas() []IndexerSchemaResource {
	return []IndexerSchemaResource{
		broadcastheNetSchema(),
		fanzubSchema(),
		filelistSchema(),
		hdbitsSchema(),
		ipTorrentsSchema(),
		newznabSchema(),
		nyaaSchema(),
		torrentRssIndexerSchema(),
		torrentleechSchema(),
		torznabSchema(),
	}
}

// indexerSchemaShell returns the per-impl wrapper with the defaults
// every C# IndexerDefinition ctor produces (Priority=25, no
// enable*/supports* flips — caller layers per-impl overrides on
// top). Presets defaults to a non-nil empty slice so top-level rows
// emit `presets: []`; preset rows nil it before returning to drop
// the key entirely (omitempty on the pointer).
func indexerSchemaShell() IndexerSchemaResource {
	empty := []IndexerSchemaResource{}
	return IndexerSchemaResource{
		DownloadClientID:                    0,
		Presets:                             &empty,
		Priority:                            25,
		SeasonSearchMaximumSingleEpisodeAge: 0,
		Tags:                                []int{},
	}
}

// indexerMultiLanguageOptions emits the multiLanguages select-option
// list every indexer carries — Sonarr's full Language registry minus
// `Unknown` (id=0), with `Original` (id=-2) pinned first and the
// remaining positive-ID languages alphabetical by Name. Identical
// across all 10 implementations because every C# IndexerSettings.
// MultiLanguages field uses the same `[FieldDefinition(SelectOptions
// = typeof(RealLanguageFieldConverter))]` attribute.
func indexerMultiLanguageOptions() []SelectOption {
	langs := language.All()
	var (
		original *language.Language
		rest     []language.Language
	)
	for i := range langs {
		l := langs[i]
		switch l.ID {
		case 0:
			// skip Unknown
		case -2:
			lc := l
			original = &lc
		default:
			rest = append(rest, l)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	out := make([]SelectOption, 0, len(rest)+1)
	if original != nil {
		out = append(out, SelectOption{Value: original.ID, Name: original.Name})
	}
	for _, l := range rest {
		out = append(out, SelectOption{Value: l.ID, Name: l.Name})
	}
	return out
}

// indexerFailDownloadOptions returns the 3-option FailDownloads
// enum the C# `FailDownloads` field carries on every IndexerSettings.
func indexerFailDownloadOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Executables", Order: 7, Hint: "(0)"},
		{Value: 1, Name: "Potentially Dangerous", Order: 10, Hint: "(1)"},
		{Value: 2, Name: "User Defined Extensions", Order: 13, Hint: "(2)"},
	}
}

// indexerSeedGoalOptions returns the 2-option SeasonPackSeedGoal
// enum carried by every torrent indexer's seedCriteria block.
func indexerSeedGoalOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Use Standard Goals", Order: 7, Hint: "(0)"},
		{Value: 1, Name: "Use Season Pack Goals", Order: 9, Hint: "(1)"},
	}
}

const (
	idxSeedRatioHT       = "The ratio a torrent should reach before stopping, empty uses the download client's default. Ratio should be at least 1.0 and follow the indexers rules"
	idxSeedTimeHT        = "The time a torrent should be seeded before stopping, empty uses the download client's default"
	idxSeasonRatioHT     = "The ratio a season pack torrent should reach before stopping, empty uses the download client's default. Ratio should be at least 1.0 and follow the indexers rules"
	idxSeasonTimeHT      = "The time a season pack torrent should be seeded before stopping, empty uses the download client's default"
	idxSeasonGoalHT      = "Choose whether to use different seeding goals for season packs"
	idxMinSeedersHT      = "Minimum number of seeders required."
	idxRejectBlocklistHT = "If a torrent is blocked by hash it may not properly be rejected during RSS/Search for some indexers, enabling this will allow it to be rejected after the torrent is grabbed, but before it is sent to the client."
	idxMultiLangHT       = "What languages are normally in a multi release on this indexer?"
	idxFailDownloadHT    = "While processing completed downloads Sonarr will treat these selected filetypes as failed downloads."
	idxAnimeStdFmtHT     = "Also search for anime using the standard numbering"
	idxBaseUrlAPIHT      = "Do not change this unless you know what you're doing. Since your API key will be sent to that host."
	idxApiPathHT         = "Path to the api, usually /api"
	idxCategoriesHT      = "Drop down list, leave blank to disable standard/daily shows"
	idxAnimeCatsHT       = "Drop down list, leave blank to disable anime"
	idxAdditionalParamHT = "Please note if you change the category you will have to add required/restricted rules about the subgroups to avoid foreign language releases."
)

// indexerMinimumSeedersField is the recurring "Minimum Seeders"
// number field — appears on every torrent indexer (and only the
// torrent indexers).
func indexerMinimumSeedersField(order int) FieldDef {
	return FieldDef{
		Order: order, Name: "minimumSeeders", Label: "Minimum Seeders",
		HelpText: idxMinSeedersHT, Type: "number", Privacy: "normal",
		Advanced: true, Value: 1, ValueSet: true,
	}
}

// indexerSeedCriteriaFields is the 5-field SeedCriteria block every
// torrent indexer carries — seedRatio + seedTime + 3 season-pack
// variants. Returns fields with sequential Order starting at start.
func indexerSeedCriteriaFields(start int) []FieldDef {
	return []FieldDef{
		{
			Order: start, Name: "seedCriteria.seedRatio", Label: "Seed Ratio",
			HelpText: idxSeedRatioHT, Type: "number", Privacy: "normal", IsFloat: true,
		},
		{
			Order: start + 1, Name: "seedCriteria.seedTime", Label: "Seed Time",
			HelpText: idxSeedTimeHT, Type: "number", Privacy: "normal",
			Advanced: true, Unit: "minutes",
		},
		{
			Order: start + 2, Name: "seedCriteria.seasonPackSeedGoal",
			Label: "Seeding Goal for Season Packs", HelpText: idxSeasonGoalHT,
			Type: "select", Privacy: "normal", Advanced: true,
			SelectOptions: indexerSeedGoalOptions(),
			Value:         0, ValueSet: true,
		},
		{
			Order: start + 3, Name: "seedCriteria.seasonPackSeedRatio",
			Label: "Season Pack Seed Ratio", HelpText: idxSeasonRatioHT,
			Type: "number", Privacy: "normal", Advanced: true, IsFloat: true,
		},
		{
			Order: start + 4, Name: "seedCriteria.seasonPackSeedTime",
			Label: "Season Pack Seed Time", HelpText: idxSeasonTimeHT,
			Type: "number", Privacy: "normal", Advanced: true, Unit: "minutes",
		},
	}
}

// indexerRejectBlocklistedField — "Reject Blocklisted Torrent Hashes
// While Grabbing" advanced checkbox on every torrent indexer.
func indexerRejectBlocklistedField(order int) FieldDef {
	return FieldDef{
		Order: order, Name: "rejectBlocklistedTorrentHashesWhileGrabbing",
		Label:    "Reject Blocklisted Torrent Hashes While Grabbing",
		HelpText: idxRejectBlocklistHT,
		Type:     "checkbox", Privacy: "normal", Advanced: true,
		Value: false, ValueSet: true,
	}
}

// indexerMultiLanguagesField — advanced multi-select language picker;
// the SelectOptions list is the same 53-entry registry-minus-Unknown
// for every indexer. Default value is `[]` (empty list).
func indexerMultiLanguagesField(order int) FieldDef {
	return FieldDef{
		Order: order, Name: "multiLanguages", Label: "Multi Languages",
		HelpText: idxMultiLangHT, Type: "select", Privacy: "normal",
		Advanced: true, SelectOptions: indexerMultiLanguageOptions(),
		Value: []int{}, ValueSet: true,
	}
}

// indexerFailDownloadsField — advanced multi-select "treat as failed
// download" file-class picker. Default empty list.
func indexerFailDownloadsField(order int) FieldDef {
	return FieldDef{
		Order: order, Name: "failDownloads", Label: "Fail Downloads",
		HelpText: idxFailDownloadHT, Type: "select", Privacy: "normal",
		Advanced: true, SelectOptions: indexerFailDownloadOptions(),
		Value: []int{}, ValueSet: true,
	}
}

// ---------- per-impl schema constructors ----------

func broadcastheNetSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "BroadcastheNetSettings"
	s.Implementation = "BroadcastheNet"
	s.ImplementationName = "BroadcasTheNet"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#broadcasthenet"
	s.Name = "BroadcastheNet"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = true
	fields := []FieldDef{
		{
			Order: 0, Name: "baseUrl", Label: "API URL",
			HelpText: idxBaseUrlAPIHT, Type: "textbox", Privacy: "normal",
			Advanced: true, Value: "https://api.broadcasthe.net/", ValueSet: true,
		},
		{Order: 1, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		indexerMinimumSeedersField(2),
	}
	fields = append(fields, indexerSeedCriteriaFields(3)...)
	fields = append(fields,
		indexerRejectBlocklistedField(8),
		indexerMultiLanguagesField(9),
		indexerFailDownloadsField(10),
	)
	s.Fields = fields
	return s
}

func fanzubSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "FanzubSettings"
	s.Implementation = "Fanzub"
	s.ImplementationName = "Fanzub"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#fanzub"
	s.Name = "Fanzub"
	s.Protocol = "usenet"
	s.SupportsRss = true
	s.SupportsSearch = true
	s.EnableAutomaticSearch = true
	s.EnableInteractiveSearch = true
	s.EnableRss = true
	s.Fields = []FieldDef{
		{
			Order: 0, Name: "baseUrl", Label: "RSS URL",
			HelpText: "Enter to URL to an Fanzub compatible RSS feed",
			Type:     "textbox", Privacy: "normal",
			Value: "http://fanzub.com/rss/", ValueSet: true,
		},
		{
			Order: 1, Name: "animeStandardFormatSearch",
			Label: "Anime Standard Format Search", HelpText: idxAnimeStdFmtHT,
			Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true,
		},
		indexerMultiLanguagesField(2),
		indexerFailDownloadsField(3),
	}
	return s
}

// filelistCategoryOptions is the per-impl Category list (used by
// `categories` and `animeCategories` on FileList).
func filelistCategoryOptions() []SelectOption {
	return []SelectOption{
		{Value: 24, Name: "Anime", Order: 91, Hint: "(24)"},
		{Value: 15, Name: "Animation", Order: 93, Hint: "(15)"},
		{Value: 27, Name: "TV 4K", Order: 95, Hint: "(27)"},
		{Value: 21, Name: "TV HD", Order: 97, Hint: "(21)"},
		{Value: 23, Name: "TV SD", Order: 99, Hint: "(23)"},
		{Value: 13, Name: "Sport", Order: 101, Hint: "(13)"},
		{Value: 28, Name: "RO Dubbed", Order: 103, Hint: "(28)"},
	}
}

func filelistSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "FileListSettings"
	s.Implementation = "FileList"
	s.ImplementationName = "FileList"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#filelist"
	s.Name = "FileList"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = true
	cats := filelistCategoryOptions()
	fields := []FieldDef{
		{Order: 0, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 1, Name: "passkey", Label: "Passkey", Type: "textbox", Privacy: "apiKey"},
		indexerMultiLanguagesField(2),
		{
			Order: 3, Name: "baseUrl", Label: "API URL",
			HelpText: idxBaseUrlAPIHT, Type: "textbox", Privacy: "normal",
			Advanced: true, Value: "https://filelist.io", ValueSet: true,
		},
		{
			Order: 4, Name: "categories", Label: "Categories",
			HelpText: idxCategoriesHT, Type: "select", Privacy: "normal",
			SelectOptions: cats, Value: []int{23, 21, 27}, ValueSet: true,
		},
		{
			Order: 5, Name: "animeCategories", Label: "Anime Categories",
			HelpText: idxAnimeCatsHT, Type: "select", Privacy: "normal",
			SelectOptions: cats, Value: []int{}, ValueSet: true,
		},
		indexerMinimumSeedersField(6),
	}
	fields = append(fields, indexerSeedCriteriaFields(7)...)
	fields = append(fields,
		indexerRejectBlocklistedField(12),
		indexerFailDownloadsField(13),
	)
	s.Fields = fields
	return s
}

func hdbitsSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "HDBitsSettings"
	s.Implementation = "HDBits"
	s.ImplementationName = "HDBits"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#hdbits"
	s.Name = "HDBits"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = true
	hdbitsAll := "If unspecified, all options are used."
	fields := []FieldDef{
		{
			Order: 0, Name: "baseUrl", Label: "API URL",
			HelpText: idxBaseUrlAPIHT, Type: "textbox", Privacy: "normal",
			Advanced: true, Value: "https://hdbits.org", ValueSet: true,
		},
		{Order: 1, Name: "username", Label: "Username", Type: "textbox", Privacy: "userName"},
		{Order: 2, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		{
			Order: 3, Name: "categories", Label: "Categories",
			HelpText: hdbitsAll, Type: "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 1, Name: "Movie", Order: 79, Hint: "(1)"},
				{Value: 2, Name: "TV", Order: 81, Hint: "(2)"},
				{Value: 3, Name: "Documentary", Order: 83, Hint: "(3)"},
				{Value: 4, Name: "Music", Order: 85, Hint: "(4)"},
				{Value: 5, Name: "Sport", Order: 87, Hint: "(5)"},
				{Value: 6, Name: "Audio Track", Order: 89, Hint: "(6)"},
				{Value: 7, Name: "XXX", Order: 91, Hint: "(7)"},
				{Value: 8, Name: "Misc/Demo", Order: 93, Hint: "(8)"},
			},
			Value: []int{2, 3}, ValueSet: true,
		},
		{
			Order: 4, Name: "codecs", Label: "Codecs",
			HelpText: hdbitsAll, Type: "select", Privacy: "normal", Advanced: true,
			SelectOptions: []SelectOption{
				{Value: 1, Name: "H.264", Order: 99, Hint: "(1)"},
				{Value: 2, Name: "MPEG-2", Order: 101, Hint: "(2)"},
				{Value: 3, Name: "VC-1", Order: 103, Hint: "(3)"},
				{Value: 4, Name: "XviD", Order: 105, Hint: "(4)"},
				{Value: 5, Name: "HEVC", Order: 107, Hint: "(5)"},
			},
			Value: []int{}, ValueSet: true,
		},
		{
			Order: 5, Name: "mediums", Label: "Mediums",
			HelpText: hdbitsAll, Type: "select", Privacy: "normal", Advanced: true,
			SelectOptions: []SelectOption{
				{Value: 1, Name: "Blu-ray/HD DVD", Order: 113, Hint: "(1)"},
				{Value: 3, Name: "Encode", Order: 115, Hint: "(3)"},
				{Value: 4, Name: "Capture", Order: 117, Hint: "(4)"},
				{Value: 5, Name: "Remux", Order: 119, Hint: "(5)"},
				{Value: 6, Name: "WEB-DL", Order: 121, Hint: "(6)"},
			},
			Value: []int{}, ValueSet: true,
		},
		indexerMinimumSeedersField(6),
	}
	fields = append(fields, indexerSeedCriteriaFields(7)...)
	fields = append(fields,
		indexerRejectBlocklistedField(12),
		indexerMultiLanguagesField(13),
		indexerFailDownloadsField(14),
	)
	s.Fields = fields
	return s
}

func ipTorrentsSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "IPTorrentsSettings"
	s.Implementation = "IPTorrents"
	s.ImplementationName = "IP Torrents"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#iptorrents"
	s.Name = "IPTorrents"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = false
	fields := []FieldDef{
		{
			Order: 0, Name: "baseUrl", Label: "Feed URL",
			HelpText: "The full RSS feed url generated by IPTorrents, using only the categories you selected (HD, SD, x264, etc ...)",
			Type:     "textbox", Privacy: "normal",
		},
		indexerMinimumSeedersField(1),
	}
	fields = append(fields, indexerSeedCriteriaFields(2)...)
	fields = append(fields,
		indexerRejectBlocklistedField(7),
		indexerMultiLanguagesField(8),
		indexerFailDownloadsField(9),
	)
	s.Fields = fields
	return s
}

// newznabBaseFields returns the 9-field Newznab field list used by
// the top-level schema row AND every preset row (the only differences
// between rows are the baseUrl default + apiPath default; both are
// patched in by the caller).
func newznabBaseFields(baseURL, apiPath string, categoriesDefault []int) []FieldDef {
	baseURLField := FieldDef{Order: 0, Name: "baseUrl", Label: "URL", Type: "textbox", Privacy: "normal"}
	if baseURL != "" {
		baseURLField.Value = baseURL
		baseURLField.ValueSet = true
	}
	return []FieldDef{
		baseURLField,
		{
			Order: 1, Name: "apiPath", Label: "API Path",
			HelpText: idxApiPathHT, Type: "textbox", Privacy: "normal",
			Advanced: true, Value: apiPath, ValueSet: true,
		},
		{Order: 2, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		{
			Order: 3, Name: "categories", Label: "Categories",
			HelpText: idxCategoriesHT, Type: "select", Privacy: "normal",
			SelectOptionsProviderAction: "newznabCategories",
			Value:                       categoriesDefault, ValueSet: true,
		},
		{
			Order: 4, Name: "animeCategories", Label: "Anime Categories",
			HelpText: idxAnimeCatsHT, Type: "select", Privacy: "normal",
			SelectOptionsProviderAction: "newznabCategories",
			Value:                       []int{}, ValueSet: true,
		},
		{
			Order: 5, Name: "animeStandardFormatSearch",
			Label: "Anime Standard Format Search", HelpText: idxAnimeStdFmtHT,
			Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true,
		},
		{
			Order: 6, Name: "additionalParameters", Label: "Additional Parameters",
			HelpText: idxAdditionalParamHT, Type: "textbox", Privacy: "normal", Advanced: true,
		},
		indexerMultiLanguagesField(7),
		indexerFailDownloadsField(8),
	}
}

// newznabPresetEntries builds the 9 preset templates Newznab carries
// on its schema row. Each preset is a fully-shaped schema entry but
// without ImplementationName/Presets (those keys are omitted on the
// wire by being left at their zero values + omitempty / nil).
type newznabPresetTemplate struct {
	name    string
	baseURL string
	apiPath string
	cats    []int
}

func newznabPresets() []IndexerSchemaResource {
	presets := []newznabPresetTemplate{
		{"DOGnzb", "https://api.dognzb.cr", "/api", []int{5030, 5040}},
		{"DrunkenSlug", "https://drunkenslug.com", "/api", []int{5030, 5040}},
		{"Nzb.life", "https://api.nzb.life", "/api", []int{5030, 5040}},
		{"NZBCat", "https://nzb.cat", "/api", []int{5030, 5040}},
		{"NZBFinder.ws", "https://nzbfinder.ws", "/api", []int{5030, 5040, 5045}},
		{"NZBgeek", "https://api.nzbgeek.info", "/api", []int{5030, 5040}},
		{"nzbplanet.net", "https://api.nzbplanet.net", "/api", []int{5030, 5040}},
		{"SimplyNZBs", "https://simplynzbs.com", "/api", []int{5030, 5040}},
		{"Tabula Rasa", "https://www.tabula-rasa.pw", "/api/v1/api", []int{5030, 5040}},
	}
	out := make([]IndexerSchemaResource, 0, len(presets))
	for _, p := range presets {
		entry := indexerSchemaShell()
		entry.ConfigContract = "NewznabSettings"
		entry.Implementation = "Newznab"
		entry.InfoLink = "https://wiki.servarr.com/sonarr/supported#newznab"
		entry.Name = p.name
		entry.Protocol = "usenet"
		entry.SupportsRss = true
		entry.SupportsSearch = true
		entry.Fields = newznabBaseFields(p.baseURL, p.apiPath, p.cats)
		// Preset rows omit implementationName + presets per the C#
		// `ProviderResource` projection of definitions inside the
		// `Presets` collection: ImplementationName is null (omitted
		// by WhenWritingNull) and Presets is null on a preset's own
		// preset-list (no recursion).
		entry.ImplementationName = ""
		entry.Presets = nil
		out = append(out, entry)
	}
	return out
}

func newznabSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "NewznabSettings"
	s.Implementation = "Newznab"
	s.ImplementationName = "Newznab"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#newznab"
	// Newznab is the one indexer whose top-level schema row carries
	// name="" — the C# factory leaves Definition.Name unset because
	// the canonical "Newznab" provider is generic; user configuration
	// supplies the per-row name (or picks one of the presets).
	s.Name = ""
	s.Protocol = "usenet"
	s.SupportsRss = true
	s.SupportsSearch = true
	s.Fields = newznabBaseFields("", "/api", []int{5030, 5040})
	presets := newznabPresets()
	s.Presets = &presets
	return s
}

func nyaaSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "NyaaSettings"
	s.Implementation = "Nyaa"
	s.ImplementationName = "Nyaa"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#nyaa"
	s.Name = "Nyaa"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = true
	fields := []FieldDef{
		{Order: 0, Name: "baseUrl", Label: "Website URL", Type: "textbox", Privacy: "normal", Value: "", ValueSet: true},
		{
			Order: 1, Name: "animeStandardFormatSearch",
			Label: "Anime Standard Format Search", HelpText: idxAnimeStdFmtHT,
			Type: "checkbox", Privacy: "normal", Value: false, ValueSet: true,
		},
		{
			Order: 2, Name: "additionalParameters", Label: "Additional Parameters",
			HelpText: idxAdditionalParamHT, Type: "textbox", Privacy: "normal",
			Advanced: true, Value: "&cats=1_0&filter=1", ValueSet: true,
		},
		indexerMinimumSeedersField(3),
	}
	fields = append(fields, indexerSeedCriteriaFields(4)...)
	fields = append(fields,
		indexerRejectBlocklistedField(9),
		indexerMultiLanguagesField(10),
		indexerFailDownloadsField(11),
	)
	s.Fields = fields
	return s
}

func torrentRssIndexerSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "TorrentRssIndexerSettings"
	s.Implementation = "TorrentRssIndexer"
	s.ImplementationName = "Torrent RSS Feed"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#torrentrssindexer"
	s.Name = "TorrentRssIndexer"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = false
	fields := []FieldDef{
		{Order: 0, Name: "baseUrl", Label: "RSS URL", Type: "textbox", Privacy: "normal", Value: "", ValueSet: true},
		{
			Order: 1, Name: "cookie", Label: "Cookie",
			HelpText: "If your site requires a login cookie to access the rss, you'll have to retrieve it via a browser.",
			Type:     "textbox", Privacy: "normal",
		},
		{
			Order: 2, Name: "allowZeroSize", Label: "Allow Zero Size",
			HelpText: "Enabling this will allow you to use feeds that don't specify release size, but be careful, size related checks will not be performed.",
			Type:     "checkbox", Privacy: "normal", Value: false, ValueSet: true,
		},
		indexerMinimumSeedersField(3),
	}
	fields = append(fields, indexerSeedCriteriaFields(4)...)
	fields = append(fields,
		indexerRejectBlocklistedField(9),
		indexerMultiLanguagesField(10),
		indexerFailDownloadsField(11),
	)
	s.Fields = fields
	return s
}

func torrentleechSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "TorrentleechSettings"
	s.Implementation = "Torrentleech"
	s.ImplementationName = "TorrentLeech"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#torrentleech"
	s.Name = "Torrentleech"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = false
	fields := []FieldDef{
		{
			Order: 0, Name: "baseUrl", Label: "Website URL",
			Type: "textbox", Privacy: "normal",
			Value: "http://rss.torrentleech.org", ValueSet: true,
		},
		{Order: 1, Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey"},
		indexerMinimumSeedersField(2),
	}
	fields = append(fields, indexerSeedCriteriaFields(3)...)
	fields = append(fields,
		indexerRejectBlocklistedField(8),
		indexerMultiLanguagesField(9),
		indexerFailDownloadsField(10),
	)
	s.Fields = fields
	return s
}

func torznabSchema() IndexerSchemaResource {
	s := indexerSchemaShell()
	s.ConfigContract = "TorznabSettings"
	s.Implementation = "Torznab"
	s.ImplementationName = "Torznab"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#torznab"
	s.Name = "Torznab"
	s.Protocol = "torrent"
	s.SupportsRss = true
	s.SupportsSearch = true
	// Torznab shares the Newznab base fields (orders 0-8) plus the
	// torrent-flavored seeder/seedCriteria/reject tail (orders 9-15).
	fields := newznabBaseFields("", "/api", []int{5030, 5040})
	fields = append(fields, indexerMinimumSeedersField(9))
	fields = append(fields, indexerSeedCriteriaFields(10)...)
	fields = append(fields, indexerRejectBlocklistedField(15))
	s.Fields = fields
	return s
}

// IndexerSchemaHandler returns the http.Handler for
// GET /api/v3/indexer/schema. Mirrors the C# pattern at
// `IndexerController.GetTemplates` (inherited from
// `ProviderControllerBase`) — emits the per-provider schema template
// for every registered IIndexer implementation, sorted by
// ImplementationName ASC.
func IndexerSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(indexerSchemas())
		_, _ = w.Write(buf)
	})
}
