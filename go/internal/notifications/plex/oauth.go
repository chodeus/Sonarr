package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// plex.tv host URLs. The OAuth pin-redemption + authorize flow goes
// through the plex.tv backend (NOT the configured Plex Media Server
// host). Mirrors C# constants at
// `PlexTvProxy.cs:14-15` + `PlexTvService.cs:34-35`.
const (
	hostPlexTv     = "https://plex.tv"
	hostPlexTvAuth = "https://app.plex.tv"
)

// Test seam mutables — default to production URLs. Tests override
// via [SetOAuthURLsForTest].
var (
	hostPlexTvVar     = hostPlexTv
	hostPlexTvAuthVar = hostPlexTvAuth
)

// SetOAuthURLsForTest swaps both plex.tv OAuth endpoints. Intended
// only for tests that redirect outbound OAuth calls to an httptest.
// Server.
func SetOAuthURLsForTest(plex, plexAuth string) {
	hostPlexTvVar = plex
	hostPlexTvAuthVar = plexAuth
}

// ErrBadRequest wraps a v3-UI RequestAction call that's missing or
// has malformed query parameters. Mirrors C# `BadRequestException`
// thrown from `PlexServer.RequestAction`.
var ErrBadRequest = fmt.Errorf("plex: bad request")

// PinURLResponse mirrors C# `PlexTvPinUrlResponse` shape returned
// by GetPinUrl. Carries the OAuth pin-poll URL + headers the v3 UI
// needs to launch the OAuth flow.
type PinURLResponse struct {
	Poll    bool              `json:"poll"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// SignInURLResponse mirrors C# `PlexTvSignInUrlResponse` shape
// returned by GetSignInUrl. The v3 UI redirects the browser to
// OauthURL after the user clicks SignIn.
type SignInURLResponse struct {
	OauthURL string `json:"oauthUrl"`
	PinID    int    `json:"pinId"`
}

// pinResponse is the /api/v2/pins/{pinId} response containing the
// post-handshake authToken. Mirrors C# `PlexTvPinResponse`.
type pinResponse struct {
	AuthToken string `json:"authToken"`
}

// getPinURL ports `PlexTvService.GetPinUrl` at `PlexTvService.cs:35-59`.
// Builds (but does not execute) the POST /api/v2/pins request — the
// v3 UI executes the POST itself via the returned URL + headers.
func getPinURL() PinURLResponse {
	q := plexContextQuery()
	q.Set("strong", "true")
	return PinURLResponse{
		Poll: true,
		URL:  hostPlexTvVar + "/api/v2/pins?" + q.Encode(),
		Headers: map[string]string{
			"Accept": "application/json",
		},
	}
}

// getSignInURL ports `PlexTvService.GetSignInUrl` at `PlexTvService.cs:61-84`.
// Returns the OAuth redirect URL the browser is sent to after the
// user clicks SignIn. The hashBang path is inlined per the
// importlists/plex precedent (C# builds the URL via segment-add to
// avoid HttpRequestBuilder stripping `#!`).
func getSignInURL(callbackURL string, pinID int, pinCode string) SignInURLResponse {
	q := url.Values{}
	q.Set("clientID", plexClientIdentifier)
	q.Set("forwardUrl", callbackURL)
	q.Set("code", pinCode)
	q.Set("context[device][product]", plexProduct)
	q.Set("context[device][platform]", plexPlatform)
	q.Set("context[device][platformVersion]", plexPlatformVersion)
	q.Set("context[device][version]", plexVersion)
	return SignInURLResponse{
		OauthURL: hostPlexTvAuthVar + "/auth/hashBang/#!?" + q.Encode(),
		PinID:    pinID,
	}
}

// getAuthToken ports `PlexTvProxy.GetAuthToken` at `PlexTvProxy.cs:30-41`.
// Returns the authToken from /api/v2/pins/{pinId}, or empty string
// when the pin is not yet redeemed. Decode failures map to empty
// string + nil err per the C# `TryDeserialize` fallback.
func getAuthToken(ctx context.Context, doer httpDoer, pinID int) (string, error) {
	q := plexContextQuery()
	endpoint := hostPlexTvVar + "/api/v2/pins/" + strconv.Itoa(pinID) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("plex: build oauth request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("plex: oauth do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("%w: oauth status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("plex: read oauth body: %w (cause: %w)", ErrSendFailed, err)
	}
	var pr pinResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", nil
	}
	return pr.AuthToken, nil
}

// plexContextQuery returns the X-Plex-* context query params every
// plex.tv OAuth request carries. Mirrors C#
// `PlexTvProxy.BuildRequest` at `PlexTvProxy.cs:94-106`.
func plexContextQuery() url.Values {
	q := url.Values{}
	q.Set("X-Plex-Client-Identifier", plexClientIdentifier)
	q.Set("X-Plex-Product", plexProduct)
	q.Set("X-Plex-Platform", plexPlatform)
	q.Set("X-Plex-Platform-Version", plexPlatformVersion)
	q.Set("X-Plex-Device-Name", plexProduct)
	q.Set("X-Plex-Version", plexVersion)
	return q
}
