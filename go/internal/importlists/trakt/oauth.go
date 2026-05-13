package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Trakt + servarr OAuth URLs. Mirror C# `TraktImportBase.cs:19-21`.
const (
	oauthAuthURL  = "https://trakt.tv/oauth/authorize"
	oauthRedirect = "https://auth.servarr.com/v1/trakt_sonarr/auth"
)

// Test seam mutables — defaults match production. Tests override via
// [SetOAuthURLsForTest].
var (
	oauthAuthURLVar  = oauthAuthURL
	oauthRedirectVar = oauthRedirect
)

// SetOAuthURLsForTest swaps the OAuth authorize + redirect URLs.
// Intended only for tests.
func SetOAuthURLsForTest(authorize, redirect string) {
	oauthAuthURLVar = authorize
	oauthRedirectVar = redirect
}

// ErrBadRequest wraps a v3-UI RequestAction call with missing or
// malformed query parameters. Mirrors C# `BadRequestException`.
var ErrBadRequest = errors.New("trakt-import: bad request")

// buildOAuthAuthorizeURL composes the trakt.tv `/oauth/authorize` URL
// pinned to the servarr redirect proxy. Mirrors C#
// `TraktImportBase.RequestAction` startOAuth branch at `:62-72`. The
// `state` param round-trips the v3-UI callback URL so the redirect
// proxy can hand the final token tuple back to the right tab.
func buildOAuthAuthorizeURL(callbackURL string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", oauthRedirectVar)
	q.Set("state", callbackURL)
	return oauthAuthURLVar + "?" + q.Encode()
}

// userSettingsResponse is the /users/settings JSON shape. Mirrors C#
// `UserSettingsResponse` used by `GetUserName`.
type userSettingsResponse struct {
	User struct {
		IDs struct {
			Slug string `json:"slug"`
		} `json:"ids"`
	} `json:"user"`
}

// getUserName GETs {BaseURL}/users/settings with Bearer auth + the
// standard trakt-api headers and returns the trakt slug. Mirrors C#
// `TraktImportBase.GetUserName` at `:88-116`. Failures swallow to
// empty string (matches C# `_logger.Warn(...)` + return null).
func getUserName(ctx context.Context, doer httpDoer, baseURL, accessToken string) string {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/users/settings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", clientID)
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
	return us.User.IDs.Slug
}

// RequestAction implements the C# `TraktImportBase.RequestAction`
// admin path at `:58-86`. Two actions:
//
//   - startOAuth — composes the trakt.tv authorize URL with state =
//     callbackUrl. Returns `{OauthUrl: "..."}` (PascalCase per C#
//     `:71`).
//   - getOAuthToken — accepts the redirect-returned (access_token,
//     expires_in, refresh_token) triple; computes Expires as
//     now+expires_in; resolves the trakt slug via getUserName.
//     Returns `{accessToken, expires, refreshToken, authUser}`.
//     getUserName failure falls back to empty authUser.
//
// Missing fields / non-int expires_in / zero expires_in / missing
// callbackUrl all return [ErrBadRequest]. Unknown actions return
// `{}` (mirrors C# `:85`).
//
// baseURL is the variant's Settings.BaseURL — passed in since the
// base doesn't see the variant's typed Settings struct.
func (b *baseImport) RequestAction(ctx context.Context, baseURL, action string, query map[string]string) (any, error) {
	switch action {
	case "startOAuth":
		callbackURL := query["callbackUrl"]
		if callbackURL == "" {
			return nil, fmt.Errorf("%w: callbackUrl invalid", ErrBadRequest)
		}
		return map[string]any{"OauthUrl": buildOAuthAuthorizeURL(callbackURL)}, nil
	case "getOAuthToken":
		accessToken := query["access_token"]
		expiresIn := query["expires_in"]
		refreshToken := query["refresh_token"]
		if accessToken == "" {
			return nil, fmt.Errorf("%w: access_token invalid", ErrBadRequest)
		}
		if expiresIn == "" {
			return nil, fmt.Errorf("%w: expires_in invalid", ErrBadRequest)
		}
		if refreshToken == "" {
			return nil, fmt.Errorf("%w: refresh_token invalid", ErrBadRequest)
		}
		secs, err := strconv.Atoi(expiresIn)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("%w: expires_in not a positive int", ErrBadRequest)
		}
		expires := b.now().Add(time.Duration(secs) * time.Second)
		return map[string]any{
			"accessToken":  accessToken,
			"expires":      expires,
			"refreshToken": refreshToken,
			"authUser":     getUserName(ctx, b.doer, baseURL, accessToken),
		}, nil
	default:
		return map[string]any{}, nil
	}
}
