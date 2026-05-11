package discord

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
// Pattern-mirror of [internal/notifications/webhook.httpDoer] at
// `internal/notifications/webhook/proxy.go`.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure or non-2xx
// response. Mirrors C# `DiscordException` at
// `Notifications/Discord/DiscordException.cs:5-15` — the C# code
// throws a DiscordException wrapping any HttpException from
// IHttpClient.Execute; the Go port wraps any transport-level error
// or non-2xx HTTP response as [ErrSendFailed].
var ErrSendFailed = errors.New("discord: send failed")

// sendPayload POSTs the JSON-encoded payload to settings.WebHookURL.
// Mirrors C# `DiscordProxy.SendPayload` at
// `Notifications/Discord/DiscordProxy.cs:25-45`.
//
// Differences from the webhook sibling proxy:
//   - Method is always POST (Discord doesn't accept PUT on its
//     webhook endpoint — C# hard-codes POST at `:33`).
//   - No Basic-auth or custom-header support — the webhook URL
//     embeds the channel+token secret. C# `DiscordProxy` reads no
//     Username/Password fields off DiscordSettings.
//   - Content-Type=application/json + Accept=application/json
//     match the C# `HttpRequestBuilder.Accept(HttpAccept.Json)` +
//     `request.Headers.ContentType = "application/json"` at
//     `:30-34`.
//
// Non-2xx response → [ErrSendFailed] wrapping the response status
// + body excerpt. Transport-level errors propagate wrapped as
// [ErrSendFailed].
func sendPayload(ctx context.Context, doer httpDoer, settings *Settings, payload *Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("discord: marshal: %w (cause: %w)", ErrSendFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebHookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("discord: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
