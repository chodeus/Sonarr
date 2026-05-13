package prowl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// prowlAPIURL is the Prowl REST endpoint Sonarr POSTs to. Mirrors C#
// `PUSH_URL` at `ProwlProxy.cs:20`.
const prowlAPIURL = "https://api.prowlapp.com/publicapi/add"

// applicationName is the value sent as the "application" form field.
// Mirrors C# `BuildInfo.AppName` at `ProwlProxy.cs:40` — Sonarr's
// product name string.
const applicationName = "Sonarr"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `ProwlException`.
var ErrSendFailed = errors.New("prowl: send failed")

// ErrInvalidAPIKey is the distinguished sentinel for 401 — used by
// Test to map per-property validation failures.
var ErrInvalidAPIKey = errors.New("prowl: apikey is invalid")

// sendNotification POSTs the form payload to Prowl. Mirrors C#
// `ProwlProxy.SendNotification` at `:32-62`.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	form := url.Values{}
	form.Set("apikey", settings.ApiKey)
	form.Set("application", applicationName)
	form.Set("event", title)
	form.Set("description", message)
	form.Set("priority", strconv.Itoa(settings.Priority))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prowlAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("prowl: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("prowl: do: %w (cause: %w)", ErrSendFailed, err)
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
