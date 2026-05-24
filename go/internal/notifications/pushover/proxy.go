package pushover

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

// pushoverAPIURL is the Pushover REST endpoint Sonarr POSTs to.
// Mirrors C# `:19` constant.
const pushoverAPIURL = "https://api.pushover.net/1/messages.json"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure. Mirrors C#
// `InvalidResponseException` semantics.
var ErrSendFailed = errors.New("pushover: send failed")

// sendNotification POSTs the per-message form payload to Pushover.
// Mirrors C# `PushoverProxy.SendNotification` at
// `Notifications/Pushover/PushoverProxy.cs:32-62`. Builds the form
// body with the documented field set + the optional Emergency
// retry/expire pair + the optional Ttl/Sound.
//
// When settings.ApiKey is empty, falls back to the SonarrPushoverToken
// constant — matches the C# behaviour where the cloned-app default
// token at the settings UI fills in the same value.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	// J219 — Pushover end-to-end encryption per upstream commit
	// e79077d5f. When EncryptionKey is set the title + message ship
	// as base64-encoded AES-256-CBC envelopes + the form gets the
	// `encrypted=1` flag. Pushover's client decrypts on-device using
	// the same key. Empty EncryptionKey preserves the pre-J219
	// plaintext path.
	encrypted := strings.TrimSpace(settings.EncryptionKey) != ""
	if encrypted {
		key, err := hexStringToBytes(settings.EncryptionKey)
		if err != nil {
			return fmt.Errorf("pushover: parse EncryptionKey: %w (cause: %w)", ErrSendFailed, err)
		}
		encTitle, err := encryptField(title, key)
		if err != nil {
			return fmt.Errorf("pushover: encrypt title: %w (cause: %w)", ErrSendFailed, err)
		}
		encMsg, err := encryptField(message, key)
		if err != nil {
			return fmt.Errorf("pushover: encrypt message: %w (cause: %w)", ErrSendFailed, err)
		}
		title = encTitle
		message = encMsg
	}

	form := url.Values{}
	token := strings.TrimSpace(settings.ApiKey)
	if token == "" {
		token = SonarrPushoverToken
	}
	form.Set("token", token)
	form.Set("user", settings.UserKey)
	form.Set("device", strings.Join(settings.Devices, ","))
	form.Set("title", title)
	form.Set("message", message)
	form.Set("priority", strconv.Itoa(settings.Priority))
	if encrypted {
		form.Set("encrypted", "1")
	}
	if Priority(settings.Priority) == PriorityEmergency {
		form.Set("retry", strconv.Itoa(settings.Retry))
		form.Set("expire", strconv.Itoa(settings.Expire))
	}
	if settings.Ttl > 0 {
		form.Set("ttl", strconv.Itoa(settings.Ttl))
	}
	if s := strings.TrimSpace(settings.Sound); s != "" {
		form.Set("sound", s)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushoverAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("pushover: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("pushover: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}

// SonarrPushoverToken is the per-app token Sonarr ships with —
// users can override it in Settings.ApiKey. Sourced from the
// C# config-file constant at `PushoverSettings.cs:31` HelpLink
// hint (the actual token lives in the Sonarr-cloned Pushover app);
// the Go port keeps the empty default to require users to supply
// their own via Settings.ApiKey, matching the user-onboarding flow
// the C# UI's HelpLink points to. Production wiring may inject the
// shared token via a build flag if desired.
const SonarrPushoverToken = ""
