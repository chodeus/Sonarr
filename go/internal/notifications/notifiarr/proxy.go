package notifiarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// notifiarrAPIBaseURL is the Notifiarr REST endpoint host. Mirrors
// C# `URL` at `Notifiarr/NotifiarrProxy.cs:16`. Mutable via
// [SetAPIBaseURLForTest] for httptest-driven test seams.
var notifiarrAPIBaseURL = "https://notifiarr.com"

// SetAPIBaseURLForTest swaps the per-package API base URL. Intended
// only for tests that redirect outbound calls to an httptest.Server.
func SetAPIBaseURLForTest(u string) { notifiarrAPIBaseURL = u }

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps an unexpected non-success status. Mirrors the
// C# `NotifiarrException` default branch at
// `NotifiarrProxy.cs:71-73`.
var ErrSendFailed = errors.New("notifiarr: send failed")

// ErrInvalidAPIKey is the distinguished sentinel for 401 — used by
// Test to map per-property validation failures. Mirrors C# `:52-54`.
var ErrInvalidAPIKey = errors.New("notifiarr: api key is invalid")

// ErrServiceUnavailable is the distinguished sentinel for 502-504 —
// Mirrors C# `:60-63`.
var ErrServiceUnavailable = errors.New("notifiarr: service unavailable")

// ErrCloudflare is the distinguished sentinel for 520-524 —
// Mirrors C# `:65-69`.
var ErrCloudflare = errors.New("notifiarr: cloudflare related http error")

// sendNotification POSTs the JSON-encoded payload to the Notifiarr
// per-app endpoint. Mirrors C# `NotifiarrProxy.ProcessNotification`
// at `NotifiarrProxy.cs:31-75`.
//
// 400 is treated as a non-fatal misconfiguration (the C# code logs
// and continues per the inline comment at `:55-58`).
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notifiarr: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	endpoint := notifiarrAPIBaseURL + "/api/v1/notification/sonarr"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notifiarr: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", settings.ApiKey)

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("notifiarr: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrInvalidAPIKey
	case resp.StatusCode == http.StatusBadRequest:
		// Non-fatal misconfiguration per C# inline comment.
		return nil
	case resp.StatusCode >= 502 && resp.StatusCode <= 504:
		return ErrServiceUnavailable
	case resp.StatusCode >= 520 && resp.StatusCode <= 524:
		return ErrCloudflare
	default:
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
}
