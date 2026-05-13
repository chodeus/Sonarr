package simkl

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

// renewURL is the auth.servarr.com token-refresh endpoint. Mirrors C#
// `SimklImportBase.RenewUri` at `:22`.
//
// Mutable via [SetRenewURLForTest] for httptest seams.
var renewURL = "https://auth.servarr.com/v1/simkl_sonarr/renew"

// SetRenewURLForTest swaps the renew endpoint. Tests redirect outbound
// calls to an httptest.Server.
func SetRenewURLForTest(u string) { renewURL = u }

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure. Mirrors the C#
// HttpException raised by IHttpClient.
var ErrRequestFailed = errors.New("simkl: request failed")

// ErrUnauthorized is the distinguished sentinel for 401 — Test()
// maps this to a SignIn property failure (matches the C# validator
// override).
var ErrUnauthorized = errors.New("simkl: unauthorized")

// isValidRootURL is the Go port of `IsValidUrl` for the ValidRootUrl
// rule.
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

// getUserList GETs /sync/all-items/{showType}/{listType} with the
// simkl-api-key + Bearer auth headers. Mirrors C#
// `SimklUserRequestGenerator.GetSeriesRequest` at
// `User/SimklUserRequestGenerator.cs:22-36`.
func getUserList(ctx context.Context, doer httpDoer, settings *Settings) (*listResponse, error) {
	base := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: empty BaseURL", ErrRequestFailed)
	}
	showSeg := ShowType(settings.ShowType).queryString()
	listSeg := ListType(settings.ListType).queryString()
	if showSeg == "" || listSeg == "" {
		return nil, fmt.Errorf("%w: invalid show/list type", ErrRequestFailed)
	}
	endpoint := base + "/sync/all-items/" + showSeg + "/" + listSeg

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("simkl-api-key", clientID)
	if t := strings.TrimSpace(settings.AccessToken); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return doJSON[listResponse](doer, req)
}

// refreshAuthToken GETs the renew endpoint + returns the new token
// bundle. Mirrors C# `SimklImportBase.RefreshToken` at `:153-184`.
// A nil resp with no error indicates the server returned a body
// without an access_token (the C# code swallows that via
// `httpResp?.Resource != null`).
func refreshAuthToken(ctx context.Context, doer httpDoer, refreshToken string) (*authToken, error) {
	endpoint := renewURL + "?refresh_token=" + url.QueryEscape(refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build renew request: %w", ErrRequestFailed, err)
	}
	tok, err := doJSON[authToken](doer, req)
	if err != nil {
		return nil, err
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, nil
	}
	return tok, nil
}

// doJSON is the shared GET-and-decode helper. Returns ErrUnauthorized
// on 401, ErrRequestFailed wrapping the status on other non-2xx.
func doJSON[T any](doer httpDoer, req *http.Request) (*T, error) {
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
	if len(body) == 0 {
		return nil, nil
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %w", ErrRequestFailed, err)
	}
	return &out, nil
}
