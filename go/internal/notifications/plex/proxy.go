package plex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// plexClientIdentifier is the X-Plex-Client-Identifier surfaced on
// every Plex API call. C# pulls this from IConfigService — the Go
// port hard-codes a Sonarr-Go-port identifier until config plumbing
// lands. Mirrors C# `_configService.PlexClientIdentifier`.
const plexClientIdentifier = "sonarr-go-port"

// plexProduct + plexPlatform + plexVersion populate the matching
// X-Plex-* query params. Mirror C# `BuildInfo.AppName` + the literal
// constants at `PlexServerProxy.cs:100-104`.
const (
	plexProduct         = "Sonarr"
	plexPlatform        = "Windows"
	plexPlatformVersion = "7"
)

// plexVersion is the Sonarr build version surfaced as
// X-Plex-Version. Hard-coded until the build-info plumbing lands.
var plexVersion = "5.0.0.0"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure. Mirrors C#
// `PlexException`.
var ErrSendFailed = errors.New("plex: send failed")

// ErrInvalidAuthToken is the distinguished sentinel for 401 — mirrors
// C# `PlexAuthenticationException`.
var ErrInvalidAuthToken = errors.New("plex: invalid auth token")

// ErrAPIError is the distinguished sentinel for a Plex API error
// body — `{"MediaContainer":{"error":"..."}}` or the legacy shape.
// Mirrors C# `PlexException(error.Error)` at `:167`.
type ErrAPIError struct{ Message string }

// Error formats the API-error message.
func (e *ErrAPIError) Error() string { return "plex: api error: " + e.Message }

// buildURL composes the Plex endpoint URL with the X-Plex-* + token
// query params. Mirrors C# `PlexServerProxy.BuildRequest` at
// `:93-114`.
func buildURL(settings *Settings, resource string) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	base := scheme + "://" + settings.Host + ":" + strconv.Itoa(settings.Port) + settings.UrlBase
	if resource != "" && resource[0] != '/' {
		base += "/"
	}
	base += resource
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("X-Plex-Client-Identifier", plexClientIdentifier)
	q.Set("X-Plex-Product", plexProduct)
	q.Set("X-Plex-Platform", plexPlatform)
	q.Set("X-Plex-Platform-Version", plexPlatformVersion)
	q.Set("X-Plex-Device-Name", plexProduct)
	q.Set("X-Plex-Version", plexVersion)
	if settings.AuthToken != "" {
		q.Set("X-Plex-Token", settings.AuthToken)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// doRequest executes a GET against the configured Plex endpoint and
// returns the body. Maps 401 to ErrInvalidAuthToken + any other non-2xx
// to ErrSendFailed.
func doRequest(ctx context.Context, doer httpDoer, settings *Settings, resource string, extraParams url.Values) ([]byte, error) {
	endpoint := buildURL(settings, resource)
	if len(extraParams) > 0 {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("plex: parse url: %w (cause: %w)", ErrSendFailed, err)
		}
		q := u.Query()
		for k, vs := range extraParams {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("plex: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidAuthToken
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(body))
	}
	// API error body (200 OK + envelope error field). Mirrors C#
	// `CheckForError` at `:151-171`.
	if msg := parseError(body); msg != "" {
		return nil, &ErrAPIError{Message: msg}
	}
	return body, nil
}

// getTvSections fetches the show-type sections from
// /library/sections. Mirrors C# `:34-61`.
func getTvSections(ctx context.Context, doer httpDoer, settings *Settings) ([]Section, error) {
	body, err := doRequest(ctx, doer, settings, "library/sections", nil)
	if err != nil {
		return nil, err
	}
	return parseSections(body)
}

// updateSection sends GET /library/sections/{id}/refresh?path=... to
// trigger a per-path scan. Mirrors C# `:63-73`.
func updateSection(ctx context.Context, doer httpDoer, settings *Settings, sectionID int, path string) error {
	resource := "library/sections/" + strconv.Itoa(sectionID) + "/refresh"
	_, err := doRequest(ctx, doer, settings, resource, url.Values{"path": []string{path}})
	return err
}
