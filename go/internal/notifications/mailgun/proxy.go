package mailgun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Mailgun base URLs. Mirror C# constants at
// `MailgunProxy.cs:15-16`.
const (
	baseURLEU = "https://api.eu.mailgun.net/v3"
	baseURLUS = "https://api.mailgun.net/v3"
)

// Test seam mutables — defaults match the production endpoints
// above. Tests override both via [SetAPIBaseURLsForTest].
var (
	baseURLEUVar = baseURLEU
	baseURLUSVar = baseURLUS
)

// SetAPIBaseURLsForTest swaps both Mailgun endpoints. Intended only
// for tests that redirect outbound calls to an httptest.Server.
func SetAPIBaseURLsForTest(eu, us string) {
	baseURLEUVar = eu
	baseURLUSVar = us
}

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `MailgunException`.
var ErrSendFailed = errors.New("mailgun: send failed")

// ErrInvalidAPIKey is the distinguished sentinel for 401 — used by
// Test to map per-property validation failures. Mirrors C# `:36-39`.
var ErrInvalidAPIKey = errors.New("mailgun: api key is invalid")

// sendNotification form-POSTs the per-event email payload. Mirrors
// C# `MailgunProxy.SendNotification` + `BuildRequest` at
// `Notifications/Mailgun/MailgunProxy.cs:27-65`.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, subject, message string) error {
	base := baseURLUSVar
	if settings.UseEuEndpoint {
		base = baseURLEUVar
	}
	endpoint := base + "/" + settings.SenderDomain + "/messages"

	form := url.Values{}
	form.Set("from", settings.From)
	for _, r := range settings.Recipients {
		form.Add("to", r)
	}
	form.Set("subject", subject)
	form.Set("text", message)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("mailgun: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("api", settings.ApiKey)

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("mailgun: do: %w (cause: %w)", ErrSendFailed, err)
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
