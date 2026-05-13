package gotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `GotifyException` at
// `Notifications/Gotify/GotifyException.cs`.
var ErrSendFailed = errors.New("gotify: send failed")

// ErrUnauthorized is the distinguished sentinel surfaced when Gotify
// returns 401 — corresponds to the C# `if status==Unauthorized`
// branch at `GotifyProxy.cs:40-43`.
var ErrUnauthorized = errors.New("gotify: unauthorized - app token is invalid")

// sendNotification POSTs the JSON-encoded payload to
// `{settings.Server}/message?token={settings.AppToken}`. Mirrors C#
// `GotifyProxy.SendNotification` at `:21-47`.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, payload *Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("gotify: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	base := strings.TrimRight(settings.Server, "/")
	endpoint, err := url.Parse(base + "/message")
	if err != nil {
		return fmt.Errorf("gotify: parse url: %w (cause: %w)", ErrSendFailed, err)
	}
	q := endpoint.Query()
	q.Set("token", settings.AppToken)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gotify: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("gotify: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
