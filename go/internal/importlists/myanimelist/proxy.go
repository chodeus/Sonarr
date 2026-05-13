package myanimelist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// renewURL is the auth.servarr.com token-refresh endpoint. Mirrors
// C# `RenewUriPath` at `MyAnimeListImport.cs:18` joined to the
// `ISonarrCloudRequestBuilder.Services` root.
//
// Mutable via [SetRenewURLForTest] for httptest seams.
var renewURL = "https://auth.servarr.com/oauth/myanimelist/renew"

// oauthAuthorizeURL is the servarr cloud-proxied MAL authorize URL.
// Mirrors C# `OAuthPath` at `MyAnimeListImport.cs:16` joined to the
// `ISonarrCloudRequestBuilder.Services` root.
//
// Mutable via [SetOAuthURLsForTest] for httptest seams.
var oauthAuthorizeURL = "https://auth.servarr.com/oauth/myanimelist/authorize"

// oauthRedirectURI is the servarr cloud redirect target the MAL
// authorize page forwards the user to after consent. Mirrors C#
// `RedirectUriPath` at `MyAnimeListImport.cs:17`.
var oauthRedirectURI = "https://auth.servarr.com/oauth/myanimelist/auth"

// SetRenewURLForTest swaps the renew endpoint. Tests redirect outbound
// calls to an httptest.Server.
func SetRenewURLForTest(u string) { renewURL = u }

// SetOAuthURLsForTest swaps the authorize + redirect URLs. Tests use
// this when exercising the RequestAction handshake.
func SetOAuthURLsForTest(authorize, redirect string) {
	oauthAuthorizeURL = authorize
	oauthRedirectURI = redirect
}

// ErrBadRequest wraps a v3-UI RequestAction call with missing or
// malformed query parameters. Mirrors C# `BadRequestException`.
var ErrBadRequest = errors.New("myanimelist: bad request")

// buildOAuthAuthorizeURL composes the servarr-proxied MAL authorize
// URL. Mirrors C# `MyAnimeListImport.RequestAction` startOAuth
// branch at `:49-60`. Note the C# code routes through
// `ISonarrCloudRequestBuilder.Services` so the host is the cloud
// services root, not myanimelist.net directly.
func buildOAuthAuthorizeURL(callbackURL string) string {
	q := url.Values{}
	q.Set("state", callbackURL)
	q.Set("redirect_uri", oauthRedirectURI)
	return oauthAuthorizeURL + "?" + q.Encode()
}

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure. Mirrors the C#
// HttpException raised by IHttpClient.
var ErrRequestFailed = errors.New("myanimelist: request failed")

// ErrUnauthorized is the distinguished sentinel for 401 — Test()
// maps this to a SignIn property failure (matches the C# validator
// override per `MyAnimeListSettings.cs:14`).
var ErrUnauthorized = errors.New("myanimelist: unauthorized")

// isValidRootURL is the Go port of `IsValidUrl` for the
// ValidRootUrl rule.
func isValidRootURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// getAnimeList GETs /users/@me/animelist with Bearer auth + the
// configured ListStatus filter + limit=1000 + fields=list_status.
// Mirrors C# `MyAnimeListRequestGenerator.GetSeriesRequest` at
// `MyAnimeListRequestGenerator.cs:28-47`.
func getAnimeList(ctx context.Context, doer httpDoer, settings *Settings) ([]animeItem, error) {
	base := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: empty BaseURL", ErrRequestFailed)
	}
	u, err := url.Parse(base + "/users/@me/animelist")
	if err != nil {
		return nil, fmt.Errorf("%w: parse url: %w", ErrRequestFailed, err)
	}
	q := u.Query()
	q.Set("fields", "list_status")
	q.Set("limit", "1000")
	if filter := Status(settings.ListStatus).queryString(); filter != "" {
		q.Set("status", filter)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	if t := strings.TrimSpace(settings.AccessToken); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %w", ErrRequestFailed, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrRequestFailed, resp.StatusCode, string(body))
	}
	var out listResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %w", ErrRequestFailed, err)
	}
	return out.Data, nil
}

// refreshAuthToken GETs the auth.servarr.com renew endpoint with the
// current refresh_token + returns the new token bundle. Mirrors C#
// `MyAnimeListImport.RefreshToken` at `:88-119`. A nil resp with no
// error means the upstream returned a non-token body — the C# code
// silently swallows that case via `httpResp?.Resource != null`.
func refreshAuthToken(ctx context.Context, doer httpDoer, refreshToken string) (*authToken, error) {
	endpoint := renewURL + "?refresh_token=" + url.QueryEscape(refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build renew request: %w", ErrRequestFailed, err)
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: renew do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: renew status %d: %s", ErrRequestFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: read renew: %w", ErrRequestFailed, err)
	}
	var tok authToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("%w: unmarshal renew: %w", ErrRequestFailed, err)
	}
	if tok.AccessToken == "" {
		return nil, nil
	}
	return &tok, nil
}
