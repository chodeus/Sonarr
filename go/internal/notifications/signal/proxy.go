package signal

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

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status that isn't 400/401). Mirrors C#
// `InvalidResponseException`.
var ErrSendFailed = errors.New("signal: send failed")

// ErrInvalidCredentials is the distinguished sentinel for 401 —
// mirrors C# `:107-109`.
var ErrInvalidCredentials = errors.New("signal: invalid username or password")

// ErrAPIError is the distinguished sentinel for 400 with an API
// error body. Mirrors C# `:84-104`. Carries the parsed Signal API
// error string for the caller to map per-property failures.
type ErrAPIError struct {
	Body string
}

// Error formats the API-error message.
func (e *ErrAPIError) Error() string { return "signal: api error: " + e.Body }

// ErrSslRequired is a sub-class of ErrAPIError surfacing when the
// API returns the specific "plain HTTP request was sent to HTTPS
// port" 400 body. Mirrors C# `:86-89`.
var ErrSslRequired = errors.New("signal: ssl required")

// signalError mirrors C# `SignalError` at
// `Notifications/Signal/SignalError.cs` — `{"error": "..."}` body
// on 400 responses.
type signalError struct {
	Error string `json:"error"`
}

// sendNotification POSTs the JSON-encoded payload to
// {scheme}://{Host}:{Port}/v2/send. Mirrors C#
// `SignalProxy.SendNotification` at `Notifications/Signal/SignalProxy.cs:33-64`.
//
// Body composition mirrors C# `:35-38` — Title and Message on
// separate lines.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	host := strings.TrimSpace(settings.Host)
	endpoint := scheme + "://" + host + ":" + strconv.Itoa(settings.Port) + "/v2/send"

	payload := Payload{
		Message:    title + "\n" + message + "\n",
		Number:     settings.SenderNumber,
		Recipients: []string{settings.ReceiverId},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("signal: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("signal: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(settings.AuthUsername) != "" && strings.TrimSpace(settings.AuthPassword) != "" {
		req.SetBasicAuth(settings.AuthUsername, settings.AuthPassword)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("signal: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidCredentials
	}
	if resp.StatusCode == http.StatusBadRequest {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if strings.Contains(strings.ToLower(string(raw)), "the plain http request was sent to https port") {
			return ErrSslRequired
		}
		var se signalError
		if json.Unmarshal(raw, &se) == nil && se.Error != "" {
			return &ErrAPIError{Body: se.Error}
		}
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}
