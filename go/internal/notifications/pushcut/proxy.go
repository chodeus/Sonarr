package pushcut

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// pushcutAPIBaseURL is the Pushcut REST endpoint host. The proxy
// targets `{base}/v1/notifications/{NotificationName}`. Mirrors C#
// URL pattern at `PushcutProxy.cs:39`. Mutable via
// [SetAPIBaseURLForTest] for httptest-driven test seams.
var pushcutAPIBaseURL = "https://api.pushcut.io"

// SetAPIBaseURLForTest swaps the per-package API base URL. Intended
// only for tests that redirect outbound calls to an httptest.Server.
func SetAPIBaseURLForTest(u string) { pushcutAPIBaseURL = u }

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `PushcutException`.
var ErrSendFailed = errors.New("pushcut: send failed")

// ErrInvalidAPIKey is the distinguished sentinel for 403 — used by
// Test to map per-property validation failures.
var ErrInvalidAPIKey = errors.New("pushcut: api key is invalid")

// ErrAPIError is the distinguished sentinel for non-success responses
// that include a JSON error body. Used by Test to surface the API's
// error message.
var ErrAPIError = errors.New("pushcut: api error")

// errorResponse mirrors C# `PushcutResponse` at
// `Notifications/Pushcut/PushcutResponse.cs`. The Pushcut API returns
// `{"error": "..."}` for failures with a body.
type errorResponse struct {
	Error string `json:"error"`
}

// sendNotification POSTs the JSON-encoded payload to Pushcut's
// per-notification endpoint. Mirrors C# `PushcutProxy.SendNotification`
// at `:32-76`.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, payload *Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pushcut: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	endpoint := pushcutAPIBaseURL + "/v1/notifications/" + settings.NotificationName

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pushcut: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-Key", settings.ApiKey)

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("pushcut: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		return ErrInvalidAPIKey
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		var er errorResponse
		if json.Unmarshal(raw, &er) == nil && er.Error != "" {
			return fmt.Errorf("%w: %s", ErrAPIError, er.Error)
		}
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(raw))
	}
	return nil
}
