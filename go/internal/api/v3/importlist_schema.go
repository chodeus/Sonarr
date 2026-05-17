package v3

import (
	"encoding/json"
	"net/http"
)

// ImportListSchemaResource is the JSON shape produced by
// GET /api/v3/importlist/schema. Mirrors
// `src/Sonarr.Api.V3/ImportLists/ImportListResource.cs` extending
// `ProviderResource<ImportListResource>` at
// `src/Sonarr.Api.V3/ProviderResource.cs`.
//
// Schema-row field source (C# → Go), every key the wire carries:
//
//	configContract           → from ProviderResource (base)
//	enableAutomaticAdd       → bool — defaults to false on schemas
//	fields                   → SchemaBuilder.ToSchema(definition.Settings)
//	implementation           → struct name (AniListImport / TraktListImport / …)
//	implementationName       → user-facing label (AniList List / Sonarr / …)
//	infoLink                 → derived from `Implementation.ToLower()`
//	listOrder                → int — per-impl ordering hint (UI grouping)
//	listType                 → string — provider category (plex/trakt/other/…)
//	minRefreshInterval       → TimeSpan as "HH:MM:SS"
//	monitorNewItems          → enum string — "all" on every C# default ctor
//	presets                  → [] (the importlist factory has no presets)
//	qualityProfileId         → int — defaults to 0 on schemas
//	searchForMissingEpisodes → bool — defaults to false
//	seasonFolder             → bool — defaults to false
//	seriesType               → enum string — "standard" on every default ctor
//	shouldMonitor            → enum string — "unknown" on every default ctor
//	tags                     → [] on schemas (empty HashSet<int> in C#)
//
// Keys NOT on schemas (and so omitted vs the populated-list shape):
//
//	id / name           → null on schemas (ProviderResource WhenWritingNull)
//	rootFolderPath      → null on schemas (string, omit on null)
//	message             → null on schemas
//
// Wire-order is alphabetical because the contract-test normalizer
// sorts JSON keys before comparison; Go's reflection-driven marshaler
// emits in declaration order, which is fine here.
type ImportListSchemaResource struct {
	ConfigContract           string     `json:"configContract"`
	EnableAutomaticAdd       bool       `json:"enableAutomaticAdd"`
	Fields                   []FieldDef `json:"fields"`
	Implementation           string     `json:"implementation"`
	ImplementationName       string     `json:"implementationName"`
	InfoLink                 string     `json:"infoLink"`
	ListOrder                int        `json:"listOrder"`
	ListType                 string     `json:"listType"`
	MinRefreshInterval       string     `json:"minRefreshInterval"`
	MonitorNewItems          string     `json:"monitorNewItems"`
	Presets                  *[]any     `json:"presets,omitempty"`
	QualityProfileID         int        `json:"qualityProfileId"`
	SearchForMissingEpisodes bool       `json:"searchForMissingEpisodes"`
	SeasonFolder             bool       `json:"seasonFolder"`
	SeriesType               string     `json:"seriesType"`
	ShouldMonitor            string     `json:"shouldMonitor"`
	Tags                     []int      `json:"tags"`
}

// importListImportStatusSection is the constant `[FieldDefinition].Section`
// attribute the C# `AniListSettings` Import* fields all carry — kept as
// a top-level const because the AniList schema has 11 fields using it.
const importListImportStatusSection = "Import List Status"

// importListMinDateTime is the C# DateTime.MinValue rendered through
// System.Text.Json as RFC3339 UTC. The OAuth-style providers carry
// an Expires field whose value type renders even at default.
const importListMinDateTime = "0001-01-01T00:00:00Z"

// importListSchemas is the canonical 10-entry table sorted by
// implementationName ASC (matches the C# `ProviderControllerBase`
// schema-template emit order, which is `_factory.GetAvailableProviders()
// .OrderBy(d => d.ImplementationName)`).
var importListSchemas = []ImportListSchemaResource{
	aniListImportSchema(),
	customImportSchema(),
	myAnimeListImportSchema(),
	plexImportSchema(),
	plexRssImportSchema(),
	simklUserImportSchema(),
	sonarrImportSchema(),
	traktListImportSchema(),
	traktPopularImportSchema(),
	traktUserImportSchema(),
}

// ilSchemaShell returns the per-impl wrapper with the defaults that
// every C# IImportListSettings default ctor produces — caller fills
// in the per-impl overrides (ConfigContract, Implementation,
// ImplementationName, InfoLink, ListOrder, ListType,
// MinRefreshInterval) and the Fields slice.
func ilSchemaShell() ImportListSchemaResource {
	return ImportListSchemaResource{
		EnableAutomaticAdd: false,
		MonitorNewItems:    "all",
		Presets:            &[]any{},
		QualityProfileID:   0,
		SeriesType:         "standard",
		ShouldMonitor:      "unknown",
		Tags:               []int{},
	}
}

// ilHiddenTextbox is the standard hidden-textbox FieldDef the OAuth
// providers use for AccessToken / RefreshToken / AuthUser — empty
// helpText, no default value, Hidden="hidden".
func ilHiddenTextbox(order int, name, label string) FieldDef {
	return FieldDef{
		Order: order, Name: name, Label: label,
		Type: "textbox", Privacy: "normal", Hidden: "hidden",
	}
}

// ilExpiresField is the recurring DateTime Expires field — hidden
// textbox whose default value renders as DateTime.MinValue.
func ilExpiresField(order int) FieldDef {
	return FieldDef{
		Order: order, Name: "expires", Label: "Expires",
		Type: "textbox", Privacy: "normal", Hidden: "hidden",
	}.WithValue(importListMinDateTime)
}

// ilCheckbox is the AniList "Import …" checkbox shape — sectioned
// into "Import List Status" with helpText + bool default.
func ilCheckbox(order int, name, label, helpText string, value bool) FieldDef {
	return FieldDef{
		Order: order, Name: name, Label: label, HelpText: helpText,
		Type: "checkbox", Privacy: "normal", Section: importListImportStatusSection,
	}.WithValue(value)
}

func aniListImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "AniListSettings"
	s.Implementation = "AniListImport"
	s.ImplementationName = "AniList List"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#anilistimport"
	s.ListOrder = 4
	s.ListType = "other"
	s.MinRefreshInterval = "12:00:00"
	s.Fields = []FieldDef{
		ilHiddenTextbox(0, "accessToken", "Access Token"),
		ilHiddenTextbox(1, "refreshToken", "Refresh Token"),
		ilExpiresField(2),
		{Order: 3, Name: "username", Label: "Username", HelpText: "Username for the List to import from", Type: "textbox", Privacy: "normal"},
		ilCheckbox(4, "importCurrent", "Import Watching", "List: Currently Watching", true),
		ilCheckbox(5, "importPlanning", "Import Planning", "List: Planning to Watch", true),
		ilCheckbox(6, "importCompleted", "Import Completed", "List: Completed Watching", false),
		ilCheckbox(7, "importDropped", "Import Dropped", "List: Dropped", false),
		ilCheckbox(8, "importPaused", "Import Paused", "List: On Hold", false),
		ilCheckbox(9, "importRepeating", "Import Repeating", "List: Currently Re-watching", false),
		ilCheckbox(10, "importFinished", "Import Finished", "Media: All episodes have aired", true),
		ilCheckbox(11, "importReleasing", "Import Releasing", "Media: Currently airing new episodes", true),
		ilCheckbox(12, "importUnreleased", "Import Not Yet Released", "Media: Airing has not yet started", false),
		ilCheckbox(13, "importCancelled", "Import Cancelled", "Media: Series is cancelled", false),
		ilCheckbox(14, "importHiatus", "Import Hiatus", "Media: Series on Hiatus", false),
		FieldDef{Order: 15, Name: "signIn", Label: "Authenticate with AniList", Type: "oAuth", Privacy: "normal"}.WithValue("startOAuth"),
	}
	return s
}

func customImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "CustomSettings"
	s.Implementation = "CustomImport"
	s.ImplementationName = "Custom List"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#customimport"
	s.ListOrder = 5
	s.ListType = "advanced"
	s.MinRefreshInterval = "06:00:00"
	s.Fields = []FieldDef{
		FieldDef{Order: 0, Name: "baseUrl", Label: "List URL", HelpText: "The URL for the series list", Type: "textbox", Privacy: "normal"}.WithValue(""),
	}
	return s
}

func myAnimeListImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "MyAnimeListSettings"
	s.Implementation = "MyAnimeListImport"
	s.ImplementationName = "MyAnimeList"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#myanimelistimport"
	s.ListOrder = 4
	s.ListType = "other"
	s.MinRefreshInterval = "06:00:00"
	s.Fields = []FieldDef{
		FieldDef{
			Order: 0, Name: "listStatus", Label: "List Status",
			HelpText: "Type of list you want to import from, set to 'All' for all lists",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "All", Order: 7, Hint: "(0)"},
				{Value: 1, Name: "Watching", Order: 10, Hint: "(1)"},
				{Value: 2, Name: "Completed", Order: 13, Hint: "(2)"},
				{Value: 3, Name: "On Hold", Order: 16, Hint: "(3)"},
				{Value: 4, Name: "Dropped", Order: 19, Hint: "(4)"},
				{Value: 5, Name: "Plan to Watch", Order: 22, Hint: "(5)"},
			},
		}.WithValue(0),
		ilHiddenTextbox(1, "accessToken", "Access Token"),
		ilHiddenTextbox(2, "refreshToken", "Refresh Token"),
		ilExpiresField(3),
		FieldDef{Order: 4, Name: "minimumScore", Label: "Minimum Score", HelpText: "Minimum score for shows to be imported", Type: "textbox", Privacy: "normal"}.WithValue(0),
		{Order: 5, Name: "signIn", Label: "Authenticate with MyAnimeList", Type: "oAuth", Privacy: "normal"},
	}
	return s
}

func plexImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "PlexListSettings"
	s.Implementation = "PlexImport"
	s.ImplementationName = "Plex Watchlist"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#pleximport"
	s.ListOrder = 1
	s.ListType = "plex"
	s.MinRefreshInterval = "06:00:00"
	s.Fields = []FieldDef{
		ilHiddenTextbox(0, "accessToken", "Access Token"),
		FieldDef{Order: 1, Name: "signIn", Label: "Authenticate with Plex.tv", Type: "oAuth", Privacy: "normal"}.WithValue("startOAuth"),
	}
	return s
}

func plexRssImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "PlexRssImportSettings"
	s.Implementation = "PlexRssImport"
	s.ImplementationName = "Plex Watchlist RSS"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#plexrssimport"
	s.ListOrder = 1
	s.ListType = "plex"
	s.MinRefreshInterval = "06:00:00"
	s.Fields = []FieldDef{
		{
			Order: 0, Name: "url", Label: "RSS URL",
			HelpLink: "https://app.plex.tv/desktop/#!/settings/watchlist",
			Type:     "textbox", Privacy: "normal",
		},
	}
	return s
}

func simklUserImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "SimklUserSettings"
	s.Implementation = "SimklUserImport"
	s.ImplementationName = "Simkl User Watchlist"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#simkluserimport"
	s.ListOrder = 3
	s.ListType = "simkl"
	s.MinRefreshInterval = "06:00:00"
	s.Fields = []FieldDef{
		ilHiddenTextbox(0, "accessToken", "Access Token"),
		ilHiddenTextbox(1, "refreshToken", "Refresh Token"),
		ilExpiresField(2),
		ilHiddenTextbox(3, "authUser", "Auth User"),
		FieldDef{
			Order: 4, Name: "listType", Label: "List Type",
			HelpText: "Type of list you're seeking to import from",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Watching", Order: 7, Hint: "(0)"},
				{Value: 1, Name: "Plan To Watch", Order: 9, Hint: "(1)"},
				{Value: 2, Name: "Hold", Order: 11, Hint: "(2)"},
				{Value: 3, Name: "Completed", Order: 13, Hint: "(3)"},
				{Value: 4, Name: "Dropped", Order: 15, Hint: "(4)"},
			},
		}.WithValue(0),
		FieldDef{
			Order: 5, Name: "showType", Label: "Show Type",
			HelpText: "Type of show you're seeking to import from",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Shows", Order: 0},
				{Value: 1, Name: "Anime", Order: 1},
			},
		}.WithValue(0),
		FieldDef{Order: 6, Name: "signIn", Label: "Authenticate with Simkl", Type: "oAuth", Privacy: "normal"}.WithValue("startOAuth"),
	}
	return s
}

func sonarrImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "SonarrSettings"
	s.Implementation = "SonarrImport"
	s.ImplementationName = "Sonarr"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#sonarrimport"
	s.ListOrder = 0
	s.ListType = "program"
	s.MinRefreshInterval = "00:05:00"
	s.Fields = []FieldDef{
		FieldDef{Order: 0, Name: "baseUrl", Label: "Full URL", HelpText: "URL, including port, of the Sonarr instance to import from", Type: "textbox", Privacy: "normal"}.WithValue(""),
		FieldDef{Order: 1, Name: "apiKey", Label: "API Key", HelpText: "API Key of the Sonarr instance to import from", Type: "textbox", Privacy: "normal"}.WithValue(""),
		FieldDef{Order: 2, Name: "syncSeasonMonitoring", Label: "Sync Season Monitoring", HelpText: "Sync season monitoring from Sonarr instance, if enabled 'Monitor' will be ignored", Type: "checkbox", Privacy: "normal"}.WithValue(false),
		FieldDef{Order: 3, Name: "profileIds", Label: "Quality Profiles", HelpText: "Quality Profiles from the source instance to import from", Type: "select", Privacy: "normal", SelectOptionsProviderAction: "getProfiles"}.WithValue([]int{}),
		FieldDef{Order: 4, Name: "tagIds", Label: "Tags", HelpText: "Tags from the source instance to import from", Type: "select", Privacy: "normal", SelectOptionsProviderAction: "getTags"}.WithValue([]int{}),
		FieldDef{Order: 5, Name: "rootFolderPaths", Label: "Root Folders", HelpText: "Root Folders from the source instance to import from", Type: "select", Privacy: "normal", SelectOptionsProviderAction: "getRootFolders"}.WithValue([]int{}),
		FieldDef{Order: 6, Name: "languageProfileIds", Label: "Language Profiles", HelpText: "Language Profiles from the source instance to import from", Type: "select", Privacy: "normal", SelectOptionsProviderAction: "getLanguageProfiles"}.WithValue([]int{}),
	}
	return s
}

func traktListImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "TraktListSettings"
	s.Implementation = "TraktListImport"
	s.ImplementationName = "Trakt List"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#traktlistimport"
	s.ListOrder = 2
	s.ListType = "trakt"
	s.MinRefreshInterval = "12:00:00"
	s.Fields = []FieldDef{
		ilHiddenTextbox(0, "accessToken", "Access Token"),
		ilHiddenTextbox(1, "refreshToken", "Refresh Token"),
		ilExpiresField(2),
		ilHiddenTextbox(3, "authUser", "Auth User"),
		{Order: 4, Name: "username", Label: "Username", HelpText: "Username for the List to import from", Type: "textbox", Privacy: "normal"},
		{Order: 5, Name: "listname", Label: "List Name", HelpText: "List name for import, list must be public or you must have access to the list", Type: "textbox", Privacy: "normal"},
		FieldDef{Order: 6, Name: "limit", Label: "Limit", HelpText: "Limit the number of series to get", Type: "textbox", Privacy: "normal"}.WithValue(100),
		FieldDef{Order: 7, Name: "signIn", Label: "Authenticate with Trakt", Type: "oAuth", Privacy: "normal"}.WithValue("startOAuth"),
	}
	return s
}

func traktPopularImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "TraktPopularSettings"
	s.Implementation = "TraktPopularImport"
	s.ImplementationName = "Trakt Popular List"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#traktpopularimport"
	s.ListOrder = 2
	s.ListType = "trakt"
	s.MinRefreshInterval = "12:00:00"
	s.Fields = []FieldDef{
		ilHiddenTextbox(0, "accessToken", "Access Token"),
		ilHiddenTextbox(1, "refreshToken", "Refresh Token"),
		ilExpiresField(2),
		ilHiddenTextbox(3, "authUser", "Auth User"),
		FieldDef{
			Order: 4, Name: "traktListType", Label: "List Type",
			HelpText: "Type of list you're seeking to import from",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Trending Shows", Order: 8, Hint: "(0)"},
				{Value: 1, Name: "Popular Shows", Order: 11, Hint: "(1)"},
				{Value: 2, Name: "Anticipated Shows", Order: 14, Hint: "(2)"},
				{Value: 3, Name: "Top Watched Shows By Week", Order: 17, Hint: "(3)"},
				{Value: 4, Name: "Top Watched Shows By Month", Order: 20, Hint: "(4)"},
				{Value: 6, Name: "Top Watched Shows Of All Time", Order: 27, Hint: "(6)"},
				{Value: 7, Name: "Recommended Shows By Week", Order: 30, Hint: "(7)"},
				{Value: 8, Name: "Recommended Shows By Month", Order: 33, Hint: "(8)"},
				{Value: 10, Name: "Recommended Shows Of All Time", Order: 40, Hint: "(10)"},
			},
		}.WithValue(1),
		{Order: 5, Name: "rating", Label: "Rating", HelpText: "Filter series by rating range (0-100)", Type: "textbox", Privacy: "normal"},
		{Order: 6, Name: "genres", Label: "Genres", HelpText: "Filter series by Trakt Genre Slug (Comma Separated) Only for Popular Lists", Type: "textbox", Privacy: "normal"},
		{Order: 7, Name: "years", Label: "Years", HelpText: "Filter series by year or year range", Type: "textbox", Privacy: "normal"},
		{Order: 8, Name: "traktAdditionalParameters", Label: "Additional Parameters", HelpText: "Additional Trakt API parameters", Type: "textbox", Privacy: "normal", Advanced: true},
		FieldDef{Order: 9, Name: "limit", Label: "Limit", HelpText: "Limit the number of series to get", Type: "textbox", Privacy: "normal"}.WithValue(100),
		FieldDef{Order: 10, Name: "signIn", Label: "Authenticate with Trakt", Type: "oAuth", Privacy: "normal"}.WithValue("startOAuth"),
	}
	return s
}

func traktUserImportSchema() ImportListSchemaResource {
	s := ilSchemaShell()
	s.ConfigContract = "TraktUserSettings"
	s.Implementation = "TraktUserImport"
	s.ImplementationName = "Trakt User"
	s.InfoLink = "https://wiki.servarr.com/sonarr/supported#traktuserimport"
	s.ListOrder = 2
	s.ListType = "trakt"
	s.MinRefreshInterval = "12:00:00"
	s.Fields = []FieldDef{
		ilHiddenTextbox(0, "accessToken", "Access Token"),
		ilHiddenTextbox(1, "refreshToken", "Refresh Token"),
		ilExpiresField(2),
		ilHiddenTextbox(3, "authUser", "Auth User"),
		FieldDef{
			Order: 4, Name: "traktListType", Label: "List Type",
			HelpText: "Type of list you're seeking to import from",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "User Watch List", Order: 7, Hint: "(0)"},
				{Value: 1, Name: "User Watched List", Order: 9, Hint: "(1)"},
				{Value: 2, Name: "User Collection List", Order: 11, Hint: "(2)"},
			},
		}.WithValue(0),
		FieldDef{
			Order: 5, Name: "traktWatchedListType", Label: "Watched List Filter",
			HelpText: "If List Type is Watched, select the series type you want to import",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "All", Order: 7, Hint: "(0)"},
				{Value: 1, Name: "In Progress", Order: 9, Hint: "(1)"},
				{Value: 2, Name: "100% Watched", Order: 11, Hint: "(2)"},
			},
		}.WithValue(0),
		FieldDef{
			Order: 6, Name: "traktWatchSorting", Label: "Watch List Sorting",
			HelpText: "If List Type is Watch, select the order to sort the list",
			Type:     "select", Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Rank", Order: 0},
				{Value: 1, Name: "Added", Order: 1},
				{Value: 2, Name: "Title", Order: 2},
				{Value: 3, Name: "Released", Order: 3},
			},
		}.WithValue(0),
		{Order: 7, Name: "username", Label: "Username", HelpText: "Username for the List to import from (leave empty to use Auth User)", Type: "textbox", Privacy: "normal"},
		FieldDef{Order: 8, Name: "limit", Label: "Limit", HelpText: "Limit the number of series to get", Type: "textbox", Privacy: "normal"}.WithValue(100),
		FieldDef{Order: 9, Name: "signIn", Label: "Authenticate with Trakt", Type: "oAuth", Privacy: "normal"}.WithValue("startOAuth"),
	}
	return s
}

// ImportListSchemaHandler returns the http.Handler for
// GET /api/v3/importlist/schema. Mirrors the C# pattern at
// `ImportListController.GetTemplates` (inherited from
// `ProviderControllerBase`) — emits the per-provider schema template
// for every registered IImportList implementation, sorted by
// ImplementationName ASC.
func ImportListSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(importListSchemas)
		_, _ = w.Write(buf)
	})
}
