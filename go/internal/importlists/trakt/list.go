package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// ListImplementation is the persisted-row identifier for the List
// variant. Mirrors C# `TraktListImport.cs:10`.
const ListImplementation = "TraktListImport"

// ListConfigContract is the persisted-row settings type identifier
// for the List variant. Mirrors C# `TraktListSettings.cs:16`.
const ListConfigContract = "TraktListSettings"

// ListSettings ports `TraktListSettings` at
// `List/TraktListSettings.cs:16-30`.
type ListSettings struct {
	SettingsBase
	// Username is the Trakt account that owns the list — REQUIRED.
	Username string `json:"username"`
	// Listname is the human-readable list label that gets slug-
	// transformed into the URL segment — REQUIRED.
	Listname string `json:"listname"`
}

// defaultListSettings returns ListSettings with the base defaults.
func defaultListSettings() ListSettings {
	return ListSettings{SettingsBase: defaultSettingsBase()}
}

// Validate runs the C# `TraktListSettingsValidator` rules at `:7-14`.
func (s *ListSettings) Validate() []importlists.TestFailure {
	out := s.validateBase()
	if strings.TrimSpace(s.Username) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "Username", ErrorMessage: "'Username' must not be empty."})
	}
	if strings.TrimSpace(s.Listname) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "Listname", ErrorMessage: "'Listname' must not be empty."})
	}
	return out
}

// ListImport satisfies importlists.Provider for the List variant.
type ListImport struct {
	*baseImport
	settings ListSettings
}

// Compile-time guard.
var _ importlists.Provider = (*ListImport)(nil)

// NewList constructs a *ListImport.
func NewList(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("trakt-import-list: nil definition")
	}
	settings := defaultListSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("trakt-import-list: unmarshal settings: %w", err)
		}
	}
	return &ListImport{baseImport: newBaseImport(def, deps), settings: settings}, nil
}

// Name returns the provider's display label.
func (l *ListImport) Name() string { return "Trakt List" }

// Definition returns the persisted row.
func (l *ListImport) Definition() *importlists.ImportList { return l.def }

// ListType reports the provider's UI category.
func (l *ListImport) ListType() importlists.ListType { return importlists.ListTypeTrakt }

// MinRefreshInterval is 12 hours.
func (l *ListImport) MinRefreshInterval() time.Duration { return 12 * time.Hour }

// endpoint constructs the per-list URL with the URL-slugged listname.
// Mirrors C# `List/TraktListRequestGenerator.cs:21-26`.
func (l *ListImport) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(l.settings.BaseURL), "/")
	user := url.PathEscape(strings.TrimSpace(l.settings.Username))
	slug := urlSlug(l.settings.Listname)
	return base + "/users/" + user + "/lists/" + slug + "/items/show,season,episode?limit=" + strconv.Itoa(l.settings.Limit)
}

// Fetch executes the list retrieval. The Trakt list endpoint returns
// a heterogeneous list of show/season/episode entries; the Go port
// keeps only entries with a `show` field populated. Mirrors C#
// `TraktParser.ParseResponse`.
func (l *ListImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	l.settingsMu.Lock()
	defer l.settingsMu.Unlock()
	out := importlists.FetchResult{}
	if err := l.refreshIfNeeded(ctx, &l.settings.SettingsBase, func() ([]byte, error) { return json.Marshal(l.settings) }); err != nil {
		out.AnyFailure = true
		l.logger.Debug("trakt-list refresh failed", "list", l.definitionName(), "err", err)
		l.recordFailure(ctx)
		return out, nil
	}
	body, err := fetchJSON(ctx, l.doer, l.endpoint(), l.settings.AccessToken)
	if err != nil {
		out.AnyFailure = true
		l.logger.Debug("trakt-list fetch failed", "list", l.definitionName(), "err", err)
		l.recordFailure(ctx)
		return out, nil
	}
	var wrappers []showResponse
	if err := json.Unmarshal(body, &wrappers); err != nil {
		out.AnyFailure = true
		l.logger.Debug("trakt-list parse failed", "list", l.definitionName(), "err", err)
		l.recordFailure(ctx)
		return out, nil
	}
	items := make([]importlists.ItemInfo, 0, len(wrappers))
	for i := range wrappers {
		if wrappers[i].Show.Title == "" && wrappers[i].Show.IDs.Tvdb == nil {
			// Skip non-show entries (season/episode rows have an empty
			// Show — the heterogeneous endpoint returns them when the
			// list mixes content types).
			continue
		}
		items = append(items, l.itemFromShow(&wrappers[i].Show))
	}
	out.Series = importlists.Dedup(items)
	l.recordSuccess(ctx)
	return out, nil
}

// Test verifies access by attempting a one-off fetch.
func (l *ListImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	l.settingsMu.Lock()
	defer l.settingsMu.Unlock()
	if v := l.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := l.refreshIfNeeded(ctx, &l.settings.SettingsBase, func() ([]byte, error) { return json.Marshal(l.settings) }); err != nil {
		return []importlists.TestFailure{{
			PropertyName:        "SignIn",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	if _, err := fetchJSON(ctx, l.doer, l.endpoint(), l.settings.AccessToken); err != nil {
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

// RequestAction delegates to [baseImport.RequestAction].
func (l *ListImport) RequestAction(ctx context.Context, action string, query map[string]string) (any, error) {
	return l.baseImport.RequestAction(ctx, l.settings.BaseURL, action, query)
}

func init() {
	importlists.Default().MustRegister(ListImplementation, ListConfigContract, NewList)
}
