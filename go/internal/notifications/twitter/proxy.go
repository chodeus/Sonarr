package twitter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Twitter API endpoints. Mirror C# `TwitterProxy.cs:53` + `:72`.
const (
	endpointStatus = "https://api.twitter.com/1.1/statuses/update.json"
	endpointDM     = "https://api.twitter.com/1.1/direct_messages/new.json"
)

// Test seam mutables — defaults match production endpoints. Tests
// override via [SetAPIURLsForTest].
var (
	endpointStatusVar = endpointStatus
	endpointDMVar     = endpointDM
)

// SetAPIURLsForTest swaps both Twitter endpoints. Intended only for
// tests that redirect outbound calls to an httptest.Server.
func SetAPIURLsForTest(status, dm string) {
	endpointStatusVar = status
	endpointDMVar = dm
}

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure. Mirrors C#
// `TwitterException`.
var ErrSendFailed = errors.New("twitter: send failed")

// sendStatus POSTs a public-status tweet via statuses/update.json.
// Mirrors C# `TwitterProxy.UpdateStatus` at `:49-66`.
func sendStatus(ctx context.Context, doer httpDoer, settings *Settings, message string) error {
	return postOAuth1(ctx, doer, settings, endpointStatusVar, map[string]string{"status": message})
}

// sendDirectMessage POSTs a DM via direct_messages/new.json. Mirrors
// C# `TwitterProxy.DirectMessage` at `:68-86`.
func sendDirectMessage(ctx context.Context, doer httpDoer, settings *Settings, message string) error {
	return postOAuth1(ctx, doer, settings, endpointDMVar, map[string]string{
		"text":       message,
		"screenname": settings.Mention,
	})
}

// postOAuth1 form-POSTs the body params with an OAuth1-signed
// Authorization header. The form body uses the same param encoding as
// the OAuth signature base (percent-encoded once per RFC 5849).
func postOAuth1(ctx context.Context, doer httpDoer, settings *Settings, endpoint string, bodyParams map[string]string) error {
	signer := newSigner(settings.ConsumerKey, settings.ConsumerSecret, settings.AccessToken, settings.AccessTokenSecret)
	authHeader := signer.AuthorizationHeader("POST", endpoint, bodyParams)

	// Build the form body using the same percent-encoding the signer
	// applied — net/url's QueryEscape uses '+' for spaces which would
	// diverge from the signature base. percentEncode is the source of
	// truth.
	encoded := make([]string, 0, len(bodyParams))
	for k, v := range bodyParams {
		encoded = append(encoded, percentEncode(k)+"="+percentEncode(v))
	}
	body := strings.Join(encoded, "&")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("twitter: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("twitter: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}
	return nil
}
