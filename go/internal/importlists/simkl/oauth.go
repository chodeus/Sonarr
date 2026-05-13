package simkl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// OAuth + redirect URLs. Mirror C# `SimklImportBase.cs:20-21`.
const (
	oauthAuthURL  = "https://simkl.com/oauth/authorize"
	oauthRedirect = "https://auth.servarr.com/v1/simkl_sonarr/auth"
)

// Test seam mutables — defaults match production.
var (
	oauthAuthURLVar  = oauthAuthURL
	oauthRedirectVar = oauthRedirect
)

// SetOAuthURLsForTest swaps the authorize + redirect URLs. Intended
// only for tests.
func SetOAuthURLsForTest(authorize, redirect string) {
	oauthAuthURLVar = authorize
	oauthRedirectVar = redirect
}

// ErrBadRequest wraps a v3-UI RequestAction call with missing or
// malformed query parameters. Mirrors C# `BadRequestException`.
var ErrBadRequest = errors.New("simkl: bad request")

// buildOAuthAuthorizeURL composes the simkl.com `/oauth/authorize`
// URL pinned to the servarr redirect proxy. Mirrors C# `:74-79`.
func buildOAuthAuthorizeURL(callbackURL string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", oauthRedirectVar)
	q.Set("state", callbackURL)
	return oauthAuthURLVar + "?" + q.Encode()
}

// userSettingsResponse is the /users/settings shape. Mirrors C#
// `UserSettingsResponse` consumed by GetUserId at `:138-142`.
type userSettingsResponse struct {
	Account struct {
		ID int `json:"id"`
	} `json:"account"`
}

// getUserID GETs {BaseURL}/users/settings with Bearer auth + the
// simkl-api-key header and returns the account ID as a string.
// Mirrors C# `SimklImportBase.GetUserId` at `:124-151`. Failures
// swallow to empty string (matches C# `_logger.Warn(...)` + return
// null).
func getUserID(ctx context.Context, doer httpDoer, baseURL, accessToken string) string {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/users/settings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("simkl-api-key", clientID)
	if t := strings.TrimSpace(accessToken); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := doer.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var us userSettingsResponse
	if err := json.Unmarshal(body, &us); err != nil {
		return ""
	}
	if us.Account.ID == 0 {
		return ""
	}
	// C# `:142` calls .ToString() — preserve that conversion.
	return strconv.Itoa(us.Account.ID)
}
