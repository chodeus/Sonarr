package sendgrid

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

// defaultBaseURL is the SendGrid REST base. Mirrors the C# default
// at `SendGridSettings.cs:27`.
const defaultBaseURL = "https://api.sendgrid.com/v3/"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status that isn't 401). Mirrors C# `SendGridException`.
var ErrSendFailed = errors.New("sendgrid: send failed")

// ErrInvalidAPIKey is the distinguished sentinel for 401 — mirrors
// C# `:63-66` "Unauthorized - AuthToken is invalid" branch.
var ErrInvalidAPIKey = errors.New("sendgrid: auth token is invalid")

// sendNotification POSTs the JSON payload to {BaseUrl}/mail/send.
// Mirrors C# `SendGridProxy.SendNotification` at `:22-69`.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	base := strings.TrimSpace(settings.BaseUrl)
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	endpoint := base + "/mail/send"

	personalization := Personalization{Subject: title}
	for _, r := range settings.Recipients {
		personalization.To = append(personalization.To, Email{Email: r})
	}
	payload := Payload{
		From:             Email{Email: settings.From},
		Content:          []Content{{Type: "text/plain", Value: message}},
		Personalizations: []Personalization{personalization},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sendgrid: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sendgrid: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.ApiKey)

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("sendgrid: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidAPIKey
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
