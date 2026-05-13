package mediabrowser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure.
var ErrSendFailed = errors.New("mediabrowser: send failed")

// ErrInvalidAPIKey is the distinguished sentinel for 401 — mirrors
// C# `MediaBrowserService.Test` 401 mapping at `:69-72`.
var ErrInvalidAPIKey = errors.New("mediabrowser: api key is invalid")

// ErrNotifyNotFound is the distinguished sentinel for the test-only
// 404-on-Notify case. Mirrors C# `:91-95` — when the test runs
// against Jellyfin, the /Notifications/Admin path 404s and the user
// is advised to disable Notify. Per-event Notify calls don't surface
// this distinctly; they just fail with ErrSendFailed.
var ErrNotifyNotFound = errors.New("mediabrowser: notify endpoint not found (likely Jellyfin)")

// baseURL composes the {scheme}://{host}:{port}{urlBase} URL.
// Mirrors C# `MediaBrowserSettings.Address` at `:64` + `MediaBrowserProxy.GetUrl`
// at `:166-170`.
func baseURL(s *Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	return scheme + "://" + s.Host + ":" + strconv.Itoa(s.Port) + s.UrlBase
}

// testConnection mirrors C# `MediaBrowserProxy.TestConnection` at
// `:22-30`. GETs /System/Configuration.
func testConnection(ctx context.Context, doer httpDoer, settings *Settings) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(settings)+"/System/Configuration", nil)
	if err != nil {
		return fmt.Errorf("mediabrowser: build test request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("X-MediaBrowser-Token", settings.ApiKey)
	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("mediabrowser: test do: %w (cause: %w)", ErrSendFailed, err)
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

// notifyPayload mirrors the C# anonymous-object body at `:39-44`.
type notifyPayload struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	ImageURL    string `json:"ImageUrl"`
}

// notify POSTs to /Notifications/Admin with the configured Name/
// Description/ImageUrl. Mirrors C# `MediaBrowserProxy.Notify` at
// `:32-47`.
func notify(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	body, err := json.Marshal(notifyPayload{
		Name:        title,
		Description: message,
		ImageURL:    "https://raw.github.com/Sonarr/Sonarr/develop/Logo/64.png",
	})
	if err != nil {
		return fmt.Errorf("mediabrowser: marshal notify: %w (cause: %w)", ErrSendFailed, err)
	}
	return postJSON(ctx, doer, settings, "/Notifications/Admin", body, true)
}

// updateBody mirrors the C# `Updates:[{Path,UpdateType}]` payload at
// `:128-135`.
type updateBody struct {
	Updates []updateEntry `json:"Updates"`
}

type updateEntry struct {
	Path       string `json:"Path"`
	UpdateType string `json:"UpdateType"`
}

// update POSTs a single Path+UpdateType pair to /Library/Media/Updated.
// Mirrors C# `MediaBrowserProxy.Update` at `:120-139`.
func update(ctx context.Context, doer httpDoer, settings *Settings, path, updateType string) error {
	body, err := json.Marshal(updateBody{Updates: []updateEntry{{Path: path, UpdateType: updateType}}})
	if err != nil {
		return fmt.Errorf("mediabrowser: marshal update: %w (cause: %w)", ErrSendFailed, err)
	}
	return postJSON(ctx, doer, settings, "/Library/Media/Updated", body, false)
}

// postJSON is the shared POST-JSON helper. flagNotifyEndpoint=true
// triggers ErrNotifyNotFound on 404 (used by notify() only — per the
// Jellyfin compatibility C# branch).
func postJSON(ctx context.Context, doer httpDoer, settings *Settings, path string, body []byte, flagNotifyEndpoint bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(settings)+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mediabrowser: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MediaBrowser-Token", settings.ApiKey)
	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("mediabrowser: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidAPIKey
	}
	if flagNotifyEndpoint && resp.StatusCode == http.StatusNotFound {
		return ErrNotifyNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}
	return nil
}
