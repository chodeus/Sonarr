package simplepush

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// simplepushAPIURL is the Simplepush REST endpoint Sonarr POSTs to.
// Mirrors C# `URL` const at `SimplepushProxy.cs:18`.
const simplepushAPIURL = "https://api.simplepush.io/send"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status).
var ErrSendFailed = errors.New("simplepush: send failed")

// sendNotification POSTs the key/event/title/msg form payload to
// Simplepush. Mirrors C# `SimplepushProxy.SendNotification` at
// `Notifications/Simplepush/SimplepushProxy.cs:30-42`.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	form := url.Values{}
	form.Set("key", settings.Key)
	form.Set("event", settings.Event)
	form.Set("title", title)
	form.Set("msg", message)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, simplepushAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("simplepush: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("simplepush: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
