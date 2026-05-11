package webhook

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
// Mirrors the [downloadclients.sabnzbd.httpDoer] precedent at
// `internal/downloadclients/sabnzbd/proxy.go` — kept tiny so tests
// can swap in an httptest.Server-backed client without dragging the
// full *httpclient.Client surface through.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `WebhookException` at
// `Notifications/Webhook/WebhookException.cs:5-15` — the C# code
// wraps any HttpException raised by IHttpClient.Execute as a
// WebhookException; the Go port wraps any transport-level error
// or non-2xx response as [ErrSendFailed].
var ErrSendFailed = errors.New("webhook: send failed")

// sendWebhook POSTs (or PUTs, per settings.Method) the JSON-
// encoded body to settings.URL. Mirrors C#
// `WebhookProxy.SendWebhook` at
// `Notifications/Webhook/WebhookProxy.cs:23-57`.
//
// Behaviour matrix:
//
//   - Method=POST → http.MethodPost.
//   - Method=PUT → http.MethodPut.
//   - Any other value → POST (defensive default; C# throws
//     ArgumentOutOfRangeException at `:35` for unknown methods,
//     the Go port logs-and-defaults since the orchestrator's
//     per-provider error-swallowing means a thrown error would
//     just be logged anyway).
//   - Content-Type=application/json + Accept=application/json
//     match the C# `HttpRequestBuilder.Accept(HttpAccept.Json)` +
//     `request.Headers.ContentType = "application/json"` at
//     `:28-38`.
//   - Username AND/OR Password non-empty → Basic-auth header
//     attached via http.Request.SetBasicAuth (mirrors C#
//     `BasicNetworkCredential` at `:43`).
//   - Each [Settings.Headers] entry is added to the outgoing
//     request via Header.Add (NOT Set — matches C#
//     `request.Headers.Add(key, value)` at `:48` which appends).
//
// Non-2xx response → [ErrSendFailed] wrapping the response status
// + body excerpt. Transport-level errors propagate wrapped as
// [ErrSendFailed].
func sendWebhook(ctx context.Context, doer httpDoer, settings *Settings, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w (cause: %w)", ErrSendFailed, err)
	}

	method := http.MethodPost
	if Method(settings.Method) == MethodPUT {
		method = http.MethodPut
	}

	req, err := http.NewRequestWithContext(ctx, method, settings.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	if settings.Username != "" || settings.Password != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	for _, h := range settings.Headers {
		if h.Key == "" {
			continue
		}
		req.Header.Add(h.Key, h.Value)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read up to 512 bytes of the body for the error log.
		// Sufficient to surface "Unauthorized" / API-error JSON
		// without flooding the log on a multi-MB error page.
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
