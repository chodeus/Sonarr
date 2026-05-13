package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// PopularImplementation is the persisted-row identifier for the
// Popular variant. Mirrors C# `TraktPopularImport.cs:9`.
const PopularImplementation = "TraktPopularImport"

// PopularConfigContract is the persisted-row settings type identifier
// for the Popular variant. Mirrors C# `TraktPopularSettings.cs:36`.
const PopularConfigContract = "TraktPopularSettings"

// PopularListType mirrors C# `TraktPopularListType` enum at
// `Popular/TraktPopularListType.cs:5-43`.
type PopularListType int

// PopularListType values mirror C# enum-name → int. Yearly variants
// (5 + 9) are present for legacy-row round-trip but the validator
// rejects them.
const (
	PopularTrending           PopularListType = 0
	PopularPopular            PopularListType = 1
	PopularAnticipated        PopularListType = 2
	PopularTopWatchedByWeek   PopularListType = 3
	PopularTopWatchedByMonth  PopularListType = 4
	PopularTopWatchedByYear   PopularListType = 5
	PopularTopWatchedByAll    PopularListType = 6
	PopularRecommendedByWeek  PopularListType = 7
	PopularRecommendedByMonth PopularListType = 8
	PopularRecommendedByYear  PopularListType = 9
	PopularRecommendedByAll   PopularListType = 10
)

// pathSegment returns the per-list-type Trakt URL path. Mirrors C#
// `Popular/TraktPopularRequestGenerator.cs:26-65`.
func (p PopularListType) pathSegment() string {
	switch p {
	case PopularTrending:
		return "/shows/trending"
	case PopularPopular:
		return "/shows/popular"
	case PopularAnticipated:
		return "/shows/anticipated"
	case PopularTopWatchedByWeek:
		return "/shows/watched/weekly"
	case PopularTopWatchedByMonth:
		return "/shows/watched/monthly"
	case PopularTopWatchedByYear:
		return "/shows/watched/yearly"
	case PopularTopWatchedByAll:
		return "/shows/watched/all"
	case PopularRecommendedByWeek:
		return "/shows/recommended/weekly"
	case PopularRecommendedByMonth:
		return "/shows/recommended/monthly"
	case PopularRecommendedByYear:
		return "/shows/recommended/yearly"
	case PopularRecommendedByAll:
		return "/shows/recommended/all"
	default:
		return ""
	}
}

// PopularSettings ports `TraktPopularSettings` at
// `Popular/TraktPopularSettings.cs:36-64`.
type PopularSettings struct {
	SettingsBase
	// TraktListType is the int-encoded [PopularListType]. Defaults
	// to Popular=1 per C# constructor.
	TraktListType int `json:"traktListType"`
	// Rating is the Trakt rating filter (e.g. "70-100"). C# `:49`.
	Rating string `json:"rating"`
	// Genres is the comma-separated genre filter. C# `:52`.
	Genres string `json:"genres"`
	// Years is the year-or-range filter (e.g. "2020-2024"). C# `:55`.
	Years string `json:"years"`
	// TraktAdditionalParameters is an opaque suffix appended to the
	// query string. C# `:58`.
	TraktAdditionalParameters string `json:"traktAdditionalParameters"`
}

// defaultPopularSettings mirrors C# constructor at `:40-43`:
// TraktListType=Popular.
func defaultPopularSettings() PopularSettings {
	return PopularSettings{
		SettingsBase:  defaultSettingsBase(),
		TraktListType: int(PopularPopular),
	}
}

var ratingPattern = regexp.MustCompile(`^\d+-\d+$`)
var yearsPattern = regexp.MustCompile(`^\d+(-\d+)?$`)

// Validate runs the C# `TraktPopularSettingsValidator` rules at `:9-34`.
func (s *PopularSettings) Validate() []importlists.TestFailure {
	out := s.validateBase()
	listType := PopularListType(s.TraktListType)
	if listType.pathSegment() == "" {
		out = append(out, importlists.TestFailure{PropertyName: "TraktListType", ErrorMessage: "Invalid list type"})
	}
	if listType == PopularTopWatchedByYear || listType == PopularRecommendedByYear {
		out = append(out, importlists.TestFailure{PropertyName: "TraktListType", ErrorMessage: "Yearly lists are no longer supported"})
	}
	if strings.TrimSpace(s.Rating) != "" && !ratingPattern.MatchString(s.Rating) {
		out = append(out, importlists.TestFailure{PropertyName: "Rating", ErrorMessage: "Not a valid rating"})
	}
	if strings.TrimSpace(s.Years) != "" && !yearsPattern.MatchString(s.Years) {
		out = append(out, importlists.TestFailure{PropertyName: "Years", ErrorMessage: "Not a valid year or range of years"})
	}
	return out
}

// PopularImport satisfies importlists.Provider for the Popular variant.
type PopularImport struct {
	*baseImport
	settings PopularSettings
}

// Compile-time guard.
var _ importlists.Provider = (*PopularImport)(nil)

// NewPopular constructs a *PopularImport.
func NewPopular(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("trakt-import-popular: nil definition")
	}
	settings := defaultPopularSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("trakt-import-popular: unmarshal settings: %w", err)
		}
	}
	return &PopularImport{baseImport: newBaseImport(def, deps), settings: settings}, nil
}

// Name returns the provider's display label.
func (p *PopularImport) Name() string { return "Trakt Popular List" }

// Definition returns the persisted row.
func (p *PopularImport) Definition() *importlists.ImportList { return p.def }

// ListType reports the provider's UI category.
func (p *PopularImport) ListType() importlists.ListType { return importlists.ListTypeTrakt }

// MinRefreshInterval is 12 hours.
func (p *PopularImport) MinRefreshInterval() time.Duration { return 12 * time.Hour }

// endpoint constructs the per-list URL with rating/genres/years/
// additional filters. Mirrors C# `:23-68`.
func (p *PopularImport) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(p.settings.BaseURL), "/")
	listType := PopularListType(p.settings.TraktListType)
	pathStr := listType.pathSegment()
	if pathStr == "" {
		return base
	}
	// C# concatenates a hand-rolled query string with case-sensitive
	// keys + a free-form `TraktAdditionalParameters` suffix. The Go
	// port reproduces the exact byte-shape so the Trakt API receives
	// the same request.
	q := "?years=" + url.QueryEscape(strings.TrimSpace(p.settings.Years))
	q += "&genres=" + url.QueryEscape(strings.ToLower(strings.TrimSpace(p.settings.Genres)))
	q += "&ratings=" + url.QueryEscape(strings.TrimSpace(p.settings.Rating))
	q += "&limit=" + strconv.Itoa(p.settings.Limit)
	if extras := strings.TrimSpace(p.settings.TraktAdditionalParameters); extras != "" {
		// C# appends raw — preserve leading `&` semantics by always
		// prefixing with `&` if the user didn't supply one.
		if !strings.HasPrefix(extras, "&") {
			q += "&"
		}
		q += extras
	}
	return base + pathStr + q
}

// Fetch executes the popular-list retrieval. Mirrors C#
// `TraktImportBase.Fetch` + `TraktPopularParser.ParseResponse`.
func (p *PopularImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	p.settingsMu.Lock()
	defer p.settingsMu.Unlock()
	out := importlists.FetchResult{}
	if err := p.refreshIfNeeded(ctx, &p.settings.SettingsBase, func() ([]byte, error) { return json.Marshal(p.settings) }); err != nil {
		out.AnyFailure = true
		p.logger.Debug("trakt-popular refresh failed", "list", p.definitionName(), "err", err)
		p.recordFailure(ctx)
		return out, nil
	}
	body, err := fetchJSON(ctx, p.doer, p.endpoint(), p.settings.AccessToken)
	if err != nil {
		out.AnyFailure = true
		p.logger.Debug("trakt-popular fetch failed", "list", p.definitionName(), "err", err)
		p.recordFailure(ctx)
		return out, nil
	}
	items, err := p.parse(body)
	if err != nil {
		out.AnyFailure = true
		p.logger.Debug("trakt-popular parse failed", "list", p.definitionName(), "err", err)
		p.recordFailure(ctx)
		return out, nil
	}
	out.Series = importlists.Dedup(items)
	p.recordSuccess(ctx)
	return out, nil
}

// parse decodes the response shape based on TraktListType. Mirrors C#
// `TraktPopularParser.ParseResponse:18-56`.
//
// "Popular" returns a flat seriesResource[] (no `.show` wrapper); all
// other list types return showResponse[].
func (p *PopularImport) parse(body []byte) ([]importlists.ItemInfo, error) {
	if PopularListType(p.settings.TraktListType) == PopularPopular {
		var shows []seriesResource
		if err := json.Unmarshal(body, &shows); err != nil {
			return nil, err
		}
		items := make([]importlists.ItemInfo, 0, len(shows))
		for i := range shows {
			items = append(items, p.itemFromShow(&shows[i]))
		}
		return items, nil
	}
	var wrappers []showResponse
	if err := json.Unmarshal(body, &wrappers); err != nil {
		return nil, err
	}
	items := make([]importlists.ItemInfo, 0, len(wrappers))
	for i := range wrappers {
		items = append(items, p.itemFromShow(&wrappers[i].Show))
	}
	return items, nil
}

// Test verifies access by attempting a one-off fetch.
func (p *PopularImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	p.settingsMu.Lock()
	defer p.settingsMu.Unlock()
	if v := p.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := p.refreshIfNeeded(ctx, &p.settings.SettingsBase, func() ([]byte, error) { return json.Marshal(p.settings) }); err != nil {
		return []importlists.TestFailure{{
			PropertyName:        "SignIn",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	if _, err := fetchJSON(ctx, p.doer, p.endpoint(), p.settings.AccessToken); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return []importlists.TestFailure{{PropertyName: "SignIn", ErrorMessage: "Access token is invalid; reauthenticate with Trakt"}}, nil
		}
		return []importlists.TestFailure{{
			PropertyName:        "BaseUrl",
			ErrorMessage:        fmt.Sprintf("Unable to fetch list: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	return nil, nil
}

// RequestAction is a no-op.
func (p *PopularImport) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

func init() {
	importlists.Default().MustRegister(PopularImplementation, PopularConfigContract, NewPopular)
}
