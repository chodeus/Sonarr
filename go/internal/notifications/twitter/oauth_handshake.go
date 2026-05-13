package twitter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Twitter OAuth1 handshake endpoints. Mirror C# `TwitterProxy.cs:34`
// + `:44`. Inlined as constants here (not in proxy.go) so the test
// seam is colocated with the handshake helpers.
const (
	endpointRequestToken = "https://api.twitter.com/oauth/request_token"
	endpointAccessToken  = "https://api.twitter.com/oauth/access_token"
	endpointAuthorize    = "https://api.twitter.com/oauth/authorize"
)

// Test seam mutables — defaults match production. Tests override via
// [SetOAuthURLsForTest].
var (
	endpointRequestTokenVar = endpointRequestToken
	endpointAccessTokenVar  = endpointAccessToken
	endpointAuthorizeVar    = endpointAuthorize
)

// SetOAuthURLsForTest swaps the OAuth1 handshake endpoints. Intended
// only for tests.
func SetOAuthURLsForTest(requestToken, accessToken, authorize string) {
	endpointRequestTokenVar = requestToken
	endpointAccessTokenVar = accessToken
	endpointAuthorizeVar = authorize
}

// ErrBadRequest wraps a v3-UI RequestAction call with missing or
// malformed query parameters. Mirrors C# `BadRequestException`.
var ErrBadRequest = errors.New("twitter: bad request")

// getOAuthRedirect ports C# `TwitterProxy.GetOAuthRedirect` at
// `:30-38`. POSTs to /oauth/request_token with oauth_callback in the
// signed Authorization header, parses the form-encoded response,
// and returns the authorize URL the v3 UI redirects the browser to.
func getOAuthRedirect(ctx context.Context, doer httpDoer, consumerKey, consumerSecret, callbackURL string) (string, error) {
	signer := newSigner(consumerKey, consumerSecret, "", "")
	authHeader := signer.authorizationHeaderWithExtras("POST", endpointRequestTokenVar, nil, map[string]string{
		"oauth_callback": callbackURL,
	})
	values, err := postOAuth1Form(ctx, doer, endpointRequestTokenVar, authHeader)
	if err != nil {
		return "", err
	}
	tok := values.Get("oauth_token")
	if tok == "" {
		return "", fmt.Errorf("%w: missing oauth_token in request_token response", ErrSendFailed)
	}
	return endpointAuthorizeVar + "?oauth_token=" + tok, nil
}

// getOAuthAccessToken ports C# `TwitterProxy.GetOAuthToken` at
// `:40-47`. POSTs to /oauth/access_token with oauth_verifier in the
// signed Authorization header and the temporary oauth_token from
// the previous step; parses the form-encoded response and returns
// (accessToken, accessTokenSecret).
func getOAuthAccessToken(ctx context.Context, doer httpDoer, consumerKey, consumerSecret, oauthToken, oauthVerifier string) (string, string, error) {
	signer := newSigner(consumerKey, consumerSecret, oauthToken, "")
	authHeader := signer.authorizationHeaderWithExtras("POST", endpointAccessTokenVar, nil, map[string]string{
		"oauth_verifier": oauthVerifier,
	})
	values, err := postOAuth1Form(ctx, doer, endpointAccessTokenVar, authHeader)
	if err != nil {
		return "", "", err
	}
	return values.Get("oauth_token"), values.Get("oauth_token_secret"), nil
}

// postOAuth1Form sends an empty-body POST with the supplied
// Authorization header and parses the form-encoded response body.
func postOAuth1Form(ctx context.Context, doer httpDoer, endpoint, authHeader string) (url.Values, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("twitter: build oauth request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter: oauth do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("twitter: read oauth body: %w (cause: %w)", ErrSendFailed, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: oauth status %d: %s", ErrSendFailed, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return url.ParseQuery(string(body))
}
