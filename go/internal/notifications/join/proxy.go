package join

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// joinAPIURL is the Join sendPush endpoint. Mirrors C# `URL` at
// `JoinProxy.cs:21`. Mutable via [SetAPIURLForTest] for httptest seams.
var joinAPIURL = "https://joinjoaomgcd.appspot.com/_ah/api/messaging/v1/sendPush"

// SetAPIURLForTest swaps the per-package endpoint. Intended only for
// tests that redirect outbound calls to an httptest.Server.
func SetAPIURLForTest(u string) { joinAPIURL = u }

// Per-event icon URLs. Mirror C# `:101-102`.
const (
	iconURL      = "https://cdn.rawgit.com/Sonarr/Sonarr/main/Logo/256.png"
	smallIconURL = "https://cdn.rawgit.com/Sonarr/Sonarr/main/Logo/96-Outline-White.png"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure. Mirrors C#
// `JoinException`.
var ErrSendFailed = errors.New("join: send failed")

// ErrAuth is the distinguished sentinel for authentication-related
// failures. Mirrors C# `JoinAuthException` at `:118` (userAuthError
// flag) + `:134-137` ("No device to send message to" body).
var ErrAuth = errors.New("join: authentication failed")

// ErrInvalidDevice is the distinguished sentinel for "No devices to
// send to" — mirrors C# `JoinInvalidDeviceException` at `:125-128`.
var ErrInvalidDevice = errors.New("join: invalid device id")

// responseModel mirrors C# `JoinResponseModel` at
// `JoinResponseModel.cs:3-11`.
type responseModel struct {
	Success       bool   `json:"success"`
	UserAuthError bool   `json:"userAuthError"`
	ErrorMessage  string `json:"errorMessage"`
	Kind          string `json:"kind"`
	ETag          string `json:"etag"`
}

// sendNotification GETs the Join sendPush endpoint with title/text/
// priority/icon + auth + target query params. Mirrors C#
// `JoinProxy.SendNotification` private method at `:81-143`.
//
// Target resolution:
//   - DeviceNames set: deviceNames={names}
//   - DeviceIds set: deviceIds={ids}
//   - Neither set: deviceId=group.all (per C# `:95`)
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	endpoint, err := url.Parse(joinAPIURL)
	if err != nil {
		return fmt.Errorf("join: parse url: %w (cause: %w)", ErrSendFailed, err)
	}
	q := endpoint.Query()
	switch {
	case strings.TrimSpace(settings.DeviceNames) != "":
		q.Set("deviceNames", settings.DeviceNames)
	case strings.TrimSpace(settings.DeviceIds) != "":
		q.Set("deviceIds", settings.DeviceIds)
	default:
		q.Set("deviceId", "group.all")
	}
	q.Set("apikey", settings.ApiKey)
	q.Set("title", title)
	q.Set("text", message)
	q.Set("icon", iconURL)
	q.Set("smallicon", smallIconURL)
	q.Set("priority", strconv.Itoa(settings.Priority))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("join: build request: %w (cause: %w)", ErrSendFailed, err)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("join: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return fmt.Errorf("join: read response: %w (cause: %w)", ErrSendFailed, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(body))
	}
	var res responseModel
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("join: unmarshal response: %w (cause: %w)", ErrSendFailed, err)
	}
	if res.Success {
		return nil
	}
	if res.UserAuthError {
		return ErrAuth
	}
	if res.ErrorMessage != "" {
		switch res.ErrorMessage {
		case "No devices to send to":
			return ErrInvalidDevice
		case "No device to send message to":
			return ErrAuth
		}
		return fmt.Errorf("%w: %s", ErrSendFailed, res.ErrorMessage)
	}
	return fmt.Errorf("%w: unknown error", ErrSendFailed)
}
