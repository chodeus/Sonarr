package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Trakt + servarr auth URLs. Mirror C# constants at
// `TraktProxy.cs:22-26`.
const (
	apiBaseURL = "https://api.trakt.tv"
	renewURL   = "https://auth.servarr.com/v1/trakt_sonarr/renew"
	clientID   = "d44ba57cab40c31eb3f797dcfccd203500796539125b333883ec1d94aa62ed4c"
)

// Test seam mutables — default to production URLs. Tests override via
// [SetURLsForTest].
var (
	apiBaseURLVar = apiBaseURL
	renewURLVar   = renewURL
)

// SetURLsForTest swaps both Trakt endpoints. Intended only for tests
// that redirect outbound calls to an httptest.Server.
func SetURLsForTest(api, renew string) {
	apiBaseURLVar = api
	renewURLVar = renew
}

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure. Mirrors C#
// `TraktException`.
var ErrSendFailed = errors.New("trakt: send failed")

// ErrInvalidAccessToken is the distinguished sentinel for 401 — mirrors
// C# Test() 401 branch at `Trakt.cs:79-83`.
var ErrInvalidAccessToken = errors.New("trakt: access token is invalid")

// addToCollection POSTs the payload to /sync/collection with Bearer
// auth. Mirrors C# `TraktProxy.AddToCollection` at `:37-45`.
func addToCollection(ctx context.Context, doer httpDoer, accessToken string, payload *CollectShowsPayload) error {
	return postBearer(ctx, doer, apiBaseURLVar+"/sync/collection", accessToken, payload)
}

// removeFromCollection POSTs to /sync/collection/remove. Mirrors C#
// `TraktProxy.RemoveFromCollection` at `:47-55`.
func removeFromCollection(ctx context.Context, doer httpDoer, accessToken string, payload *CollectShowsPayload) error {
	return postBearer(ctx, doer, apiBaseURLVar+"/sync/collection/remove", accessToken, payload)
}

// getUserName GETs /users/settings and returns the trakt slug.
// Mirrors C# `TraktProxy.GetUserName` at `:57-63`.
func getUserName(ctx context.Context, doer httpDoer, accessToken string) (string, error) {
	endpoint := apiBaseURLVar + "/users/settings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("trakt: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	addTraktHeaders(req, accessToken)
	resp, err := doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("trakt: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrInvalidAccessToken
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("trakt: read: %w (cause: %w)", ErrSendFailed, err)
	}
	var us UserSettingsResource
	if err := json.Unmarshal(body, &us); err != nil {
		return "", fmt.Errorf("trakt: unmarshal user settings: %w (cause: %w)", ErrSendFailed, err)
	}
	return us.User.IDs.Slug, nil
}

// refreshAuthToken GETs the renew endpoint with the current refresh
// token + returns the new token bundle. Mirrors C#
// `TraktProxy.RefreshAuthToken` at `:75-83`.
func refreshAuthToken(ctx context.Context, doer httpDoer, refreshToken string) (*AuthRefreshResource, error) {
	endpoint := renewURLVar + "?refresh_token=" + refreshToken
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("trakt: build renew request: %w (cause: %w)", ErrSendFailed, err)
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: renew do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("trakt: read renew: %w (cause: %w)", ErrSendFailed, err)
	}
	var out AuthRefreshResource
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("trakt: unmarshal renew: %w (cause: %w)", ErrSendFailed, err)
	}
	return &out, nil
}

// postBearer is the shared collection-payload POST. trakt-api-version
// + trakt-api-key + Bearer Authorization headers per C#
// `TraktProxy.BuildRequest` at `:85-102`.
func postBearer(ctx context.Context, doer httpDoer, endpoint, accessToken string, payload *CollectShowsPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("trakt: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("trakt: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	addTraktHeaders(req, accessToken)
	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("trakt: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidAccessToken
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}

// addTraktHeaders sets the trakt-api-* + Bearer headers. Mirrors C#
// `:93-99`.
func addTraktHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", clientID)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
}
