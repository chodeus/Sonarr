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

// UserImplementation is the persisted-row identifier for the User
// variant. Mirrors C# `TraktUserImport.cs:9`.
const UserImplementation = "TraktUserImport"

// UserConfigContract is the persisted-row settings type identifier
// for the User variant. Mirrors C# `TraktUserSettings.cs:17`.
const UserConfigContract = "TraktUserSettings"

// UserListType mirrors C# `TraktUserListType` enum at
// `User/TraktUserListType.cs:5-13`.
type UserListType int

// UserListType values mirror C# enum-name → int.
const (
	UserListTypeWatchList      UserListType = 0
	UserListTypeWatchedList    UserListType = 1
	UserListTypeCollectionList UserListType = 2
)

// UserWatchedListType mirrors C# `TraktUserWatchedListType` at
// `User/TraktUserWatchedListType.cs:5-13`.
type UserWatchedListType int

// UserWatchedListType values.
const (
	UserWatchedListAll               UserWatchedListType = 0
	UserWatchedListInProgress        UserWatchedListType = 1
	UserWatchedListCompletelyWatched UserWatchedListType = 2
)

// UserWatchSorting mirrors C# `TraktUserWatchSorting` at
// `User/TraktUserSettings.cs:46-52`.
type UserWatchSorting int

// UserWatchSorting values mirror C# enum-name → int.
const (
	UserWatchSortingRank     UserWatchSorting = 0
	UserWatchSortingAdded    UserWatchSorting = 1
	UserWatchSortingTitle    UserWatchSorting = 2
	UserWatchSortingReleased UserWatchSorting = 3
)

// UserSettings ports `TraktUserSettings` at `User/TraktUserSettings.cs:17-44`.
type UserSettings struct {
	SettingsBase
	// TraktListType is the int-encoded [UserListType] filter.
	TraktListType int `json:"traktListType"`
	// TraktWatchedListType is the int-encoded [UserWatchedListType].
	TraktWatchedListType int `json:"traktWatchedListType"`
	// TraktWatchSorting is the int-encoded [UserWatchSorting].
	TraktWatchSorting int `json:"traktWatchSorting"`
	// Username is the optional override for the per-list user. Falls
	// back to SettingsBase.AuthUser when empty per C# `User/TraktUserRequestGenerator.cs:56`.
	Username string `json:"username"`
}

// defaultUserSettings returns UserSettings with the C# constructor
// defaults at `:21-26`: WatchList + All + Rank.
func defaultUserSettings() UserSettings {
	return UserSettings{
		SettingsBase:         defaultSettingsBase(),
		TraktListType:        int(UserListTypeWatchList),
		TraktWatchedListType: int(UserWatchedListAll),
		TraktWatchSorting:    int(UserWatchSortingRank),
	}
}

// Validate runs the C# `TraktUserSettingsValidator` + inherited base
// rules.
func (s *UserSettings) Validate() []importlists.TestFailure {
	out := s.validateBase()
	switch UserListType(s.TraktListType) {
	case UserListTypeWatchList, UserListTypeWatchedList, UserListTypeCollectionList:
	default:
		out = append(out, importlists.TestFailure{PropertyName: "TraktListType", ErrorMessage: "Invalid list type"})
	}
	switch UserWatchedListType(s.TraktWatchedListType) {
	case UserWatchedListAll, UserWatchedListInProgress, UserWatchedListCompletelyWatched:
	default:
		out = append(out, importlists.TestFailure{PropertyName: "TraktWatchedListType", ErrorMessage: "Invalid watched list type"})
	}
	// C# `AuthUser non-empty` rule — the request generator falls back
	// to AuthUser when Username is empty per `User/TraktUserRequestGenerator.cs:56`.
	if strings.TrimSpace(s.AuthUser) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "AuthUser", ErrorMessage: "Must authenticate with Trakt"})
	}
	return out
}

// UserImport satisfies importlists.Provider for the User variant.
type UserImport struct {
	*baseImport
	settings UserSettings
}

// Compile-time guard.
var _ importlists.Provider = (*UserImport)(nil)

// NewUser constructs a *UserImport from a persisted definition +
// dependencies. Registered against the [importlists.Default] registry
// at init().
func NewUser(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("trakt-import-user: nil definition")
	}
	settings := defaultUserSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("trakt-import-user: unmarshal settings: %w", err)
		}
	}
	return &UserImport{baseImport: newBaseImport(def, deps), settings: settings}, nil
}

// Name returns the provider's display label.
func (u *UserImport) Name() string { return "Trakt User List" }

// Definition returns the persisted row.
func (u *UserImport) Definition() *importlists.ImportList { return u.def }

// ListType reports the provider's UI category. Mirrors C# `TraktImportBase.cs:16`.
func (u *UserImport) ListType() importlists.ListType { return importlists.ListTypeTrakt }

// MinRefreshInterval is 12 hours. Mirrors C# `:17`.
func (u *UserImport) MinRefreshInterval() time.Duration { return 12 * time.Hour }

// endpoint constructs the per-list URL. Mirrors C#
// `User/TraktUserRequestGenerator.GetSeriesRequest:27-64`.
func (u *UserImport) endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(u.settings.BaseURL), "/")
	userName := strings.TrimSpace(u.settings.Username)
	if userName == "" {
		userName = strings.TrimSpace(u.settings.AuthUser)
	}
	var path string
	switch UserListType(u.settings.TraktListType) {
	case UserListTypeWatchList:
		sort := "rank"
		switch UserWatchSorting(u.settings.TraktWatchSorting) {
		case UserWatchSortingAdded:
			sort = "added"
		case UserWatchSortingTitle:
			sort = "title"
		case UserWatchSortingReleased:
			sort = "released"
		}
		path = "/users/" + url.PathEscape(userName) + "/watchlist/shows/" + sort
	case UserListTypeWatchedList:
		path = "/users/" + url.PathEscape(userName) + "/watched/shows"
	case UserListTypeCollectionList:
		path = "/users/" + url.PathEscape(userName) + "/collection/shows"
	}
	u2, err := url.Parse(base + path)
	if err != nil {
		return base + path + "?limit=" + strconv.Itoa(u.settings.Limit)
	}
	q := u2.Query()
	if UserListType(u.settings.TraktListType) == UserListTypeWatchedList {
		q.Set("extended", "full")
	}
	q.Set("limit", strconv.Itoa(u.settings.Limit))
	u2.RawQuery = q.Encode()
	return u2.String()
}

// Fetch executes the user-list retrieval. Mirrors C#
// `TraktImportBase.Fetch` + `TraktUserParser.ParseResponse`.
func (u *UserImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	u.settingsMu.Lock()
	defer u.settingsMu.Unlock()
	out := importlists.FetchResult{}
	if err := u.refreshIfNeeded(ctx, &u.settings.SettingsBase, func() ([]byte, error) { return json.Marshal(u.settings) }); err != nil {
		out.AnyFailure = true
		u.logger.Debug("trakt-user refresh failed", "list", u.definitionName(), "err", err)
		u.recordFailure(ctx)
		return out, nil
	}
	body, err := fetchJSON(ctx, u.doer, u.endpoint(), u.settings.AccessToken)
	if err != nil {
		out.AnyFailure = true
		u.logger.Debug("trakt-user fetch failed", "list", u.definitionName(), "err", err)
		u.recordFailure(ctx)
		return out, nil
	}
	items, err := u.parse(body)
	if err != nil {
		out.AnyFailure = true
		u.logger.Debug("trakt-user parse failed", "list", u.definitionName(), "err", err)
		u.recordFailure(ctx)
		return out, nil
	}
	out.Series = importlists.Dedup(items)
	u.recordSuccess(ctx)
	return out, nil
}

// parse decodes the per-list response based on TraktListType. Mirrors
// C# `TraktUserParser.ParseResponse:19-67`.
func (u *UserImport) parse(body []byte) ([]importlists.ItemInfo, error) {
	switch UserListType(u.settings.TraktListType) {
	case UserListTypeWatchedList:
		var watched []watchedResponse
		if err := json.Unmarshal(body, &watched); err != nil {
			return nil, err
		}
		// Filter per WatchedListType.
		filterType := UserWatchedListType(u.settings.TraktWatchedListType)
		items := make([]importlists.ItemInfo, 0, len(watched))
		for i := range watched {
			watchedEntry := &watched[i]
			show := &watchedEntry.Show
			if filterType != UserWatchedListAll {
				episodeCount := 0
				for _, season := range watchedEntry.Seasons {
					if season.Number == nil || *season.Number <= 0 {
						continue
					}
					episodeCount += len(season.Episodes)
				}
				switch filterType {
				case UserWatchedListInProgress:
					if episodeCount >= show.AiredEpisodes {
						continue
					}
				case UserWatchedListCompletelyWatched:
					if episodeCount != show.AiredEpisodes {
						continue
					}
				}
			}
			items = append(items, u.itemFromShow(show))
		}
		return items, nil
	default:
		// WatchList + CollectionList: both unwrap `{show:...}`.
		var wrappers []showResponse
		if err := json.Unmarshal(body, &wrappers); err != nil {
			return nil, err
		}
		items := make([]importlists.ItemInfo, 0, len(wrappers))
		for i := range wrappers {
			items = append(items, u.itemFromShow(&wrappers[i].Show))
		}
		return items, nil
	}
}

// Test verifies access by attempting a one-off fetch.
func (u *UserImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	u.settingsMu.Lock()
	defer u.settingsMu.Unlock()
	if v := u.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := u.refreshIfNeeded(ctx, &u.settings.SettingsBase, func() ([]byte, error) { return json.Marshal(u.settings) }); err != nil {
		return []importlists.TestFailure{{
			PropertyName:        "SignIn",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	if _, err := fetchJSON(ctx, u.doer, u.endpoint(), u.settings.AccessToken); err != nil {
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

// RequestAction delegates to [baseImport.RequestAction] passing the
// variant's Settings.BaseURL for the GetUserName lookup. See
// [baseImport.RequestAction] for the action semantics — common to
// all three Trakt import variants per C# `TraktImportBase`.
func (u *UserImport) RequestAction(ctx context.Context, action string, query map[string]string) (any, error) {
	return u.baseImport.RequestAction(ctx, u.settings.BaseURL, action, query)
}

// init registers the User variant.
func init() {
	importlists.Default().MustRegister(UserImplementation, UserConfigContract, NewUser)
}
