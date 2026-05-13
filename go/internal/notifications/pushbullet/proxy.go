package pushbullet

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

// Pushbullet API constants. Mirror C# `:24-25`.
const (
	pushURL   = "https://api.pushbullet.com/v2/pushes"
	deviceURL = "https://api.pushbullet.com/v2/devices"
)

// Test seam mutables — default to production URLs above. Tests
// override via [SetAPIURLsForTest].
var (
	pushURLVar   = pushURL
	deviceURLVar = deviceURL
)

// SetAPIURLsForTest swaps both Pushbullet endpoints. Intended only
// for tests that redirect outbound calls to an httptest.Server.
func SetAPIURLsForTest(push, devices string) {
	pushURLVar = push
	deviceURLVar = devices
}

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status that isn't 401). Mirrors C# `PushBulletException`.
var ErrSendFailed = errors.New("pushbullet: send failed")

// ErrInvalidAccessToken is the distinguished sentinel for 401 — used
// by Test to map per-property validation failures. Mirrors C#
// `:209-213` "Access token is invalid" branch.
var ErrInvalidAccessToken = errors.New("pushbullet: access token is invalid")

// Device mirrors C# `PushBulletDevice` at
// `Notifications/Pushbullet/PushBulletDevice.cs:5-13`. Used by
// GetDevices for the upcoming admin getDevices action.
type Device struct {
	ID           string `json:"iden"`
	Nickname     string `json:"nickname"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
}

// devicesResponse mirrors C# `PushBulletDevicesResponse`.
type devicesResponse struct {
	Devices []Device `json:"devices"`
}

// sendNotification fans out one push per channel tag OR per device
// id; falls back to a single global push when both are empty. Mirrors
// C# `PushBulletProxy.SendNotification` at `:37-97`. Returns the first
// per-target error after iterating every target.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	var firstErr error
	step := func(form url.Values) {
		if err := sendOne(ctx, doer, settings, title, message, form); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	switch {
	case len(settings.ChannelTags) > 0:
		for _, tag := range settings.ChannelTags {
			form := url.Values{}
			if t := strings.TrimSpace(tag); t != "" {
				form.Set("channel_tag", t)
			}
			step(form)
		}
	case len(settings.DeviceIds) > 0:
		for _, id := range settings.DeviceIds {
			form := url.Values{}
			id = strings.TrimSpace(id)
			if id != "" {
				if intID, err := strconv.ParseInt(id, 10, 64); err == nil {
					form.Set("device_id", strconv.FormatInt(intID, 10))
				} else {
					form.Set("device_iden", id)
				}
			}
			step(form)
		}
	default:
		step(url.Values{})
	}
	return firstErr
}

// sendOne mirrors C# `private SendNotification(...HttpRequestBuilder...)`
// at `:188-217`.
func sendOne(ctx context.Context, doer httpDoer, settings *Settings, title, message string, form url.Values) error {
	form.Set("type", "note")
	form.Set("title", title)
	form.Set("body", message)
	if s := strings.TrimSpace(settings.SenderId); s != "" {
		form.Set("source_device_iden", s)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushURLVar, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("pushbullet: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(settings.ApiKey, "")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("pushbullet: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidAccessToken
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}

// GetDevices is the admin device-list helper. Mirrors C#
// `PushBulletProxy.GetDevices` at `:99-124`. Exported for the
// forthcoming Provider RequestAction("getDevices") path; not wired
// into OnX hooks.
func GetDevices(ctx context.Context, doer httpDoer, settings *Settings) ([]Device, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deviceURLVar, nil)
	if err != nil {
		return nil, fmt.Errorf("pushbullet: build devices request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.SetBasicAuth(settings.ApiKey, "")

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pushbullet: devices do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidAccessToken
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("pushbullet: read devices: %w (cause: %w)", ErrSendFailed, err)
	}
	var dr devicesResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return nil, fmt.Errorf("pushbullet: unmarshal devices: %w (cause: %w)", ErrSendFailed, err)
	}
	return dr.Devices, nil
}
