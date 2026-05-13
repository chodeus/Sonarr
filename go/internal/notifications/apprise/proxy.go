package apprise

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status that isn't 401 + isn't an Apprise error body).
// Mirrors C# `AppriseException`.
var ErrSendFailed = errors.New("apprise: send failed")

// ErrInvalidCredentials is the distinguished sentinel for 401 — used
// by Test to map per-property validation failures (AuthUsername).
// Mirrors C# `:102-106`.
var ErrInvalidCredentials = errors.New("apprise: invalid http credentials")

// ErrAPIError is the distinguished sentinel for non-2xx responses
// that include an Apprise error JSON body. Mirrors C# `:108-114`.
var ErrAPIError = errors.New("apprise: api error")

// errorResponse mirrors C# `AppriseError` at
// `Notifications/Apprise/AppriseError.cs` — Apprise returns
// `{"error": "..."}` for failures with a body.
type errorResponse struct {
	Error string `json:"error"`
}

// sendNotification POSTs the JSON-encoded payload to the configured
// Apprise endpoint. Mirrors C# `AppriseProxy.SendNotification` at
// `:33-88`.
//
// Endpoint shape:
//   - ConfigurationKey set: {ServerUrl}/notify/{ConfigurationKey}
//   - StatelessUrls set: {ServerUrl}/notify with payload.Urls
//
// Auth: when AuthUsername OR AuthPassword is set, Basic auth is
// attached.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message, posterURL string) error {
	server := strings.TrimRight(strings.TrimSpace(settings.ServerUrl), "/ ")
	endpoint := server + "/notify"
	payload := Payload{
		Title: title,
		Body:  message,
		Type:  NotificationType(settings.NotificationType).String(),
	}
	switch {
	case strings.TrimSpace(settings.ConfigurationKey) != "":
		endpoint = server + "/notify/" + settings.ConfigurationKey
	case strings.TrimSpace(settings.StatelessUrls) != "":
		payload.Urls = settings.StatelessUrls
	}
	if len(settings.Tags) > 0 {
		payload.Tag = strings.Join(settings.Tags, ",")
	}
	if settings.IncludePoster && strings.TrimSpace(posterURL) != "" {
		payload.Attachment = posterURL
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("apprise: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("apprise: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if settings.AuthUsername != "" || settings.AuthPassword != "" {
		req.SetBasicAuth(settings.AuthUsername, settings.AuthPassword)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("apprise: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidCredentials
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
