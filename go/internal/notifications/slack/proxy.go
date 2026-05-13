package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
// Mirrors the precedent at internal/notifications/webhook/proxy.go.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `SlackExeption` at
// `Notifications/Slack/SlackExeption.cs:5-17`. Yes, the C# class
// name has the typo "SlackExeption" — the Go port renames to the
// stylistically consistent ErrSendFailed.
var ErrSendFailed = errors.New("slack: send failed")

// sendPayload POSTs the JSON-encoded payload to settings.WebHookURL.
// Mirrors C# `SlackProxy.SendPayload` at
// `Notifications/Slack/SlackProxy.cs:21-44`.
func sendPayload(ctx context.Context, doer httpDoer, settings *Settings, payload *Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebHookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("slack: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
