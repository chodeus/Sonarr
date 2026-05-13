package ntfy

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

// defaultServerURL is the ntfy.sh public host the proxy falls back to
// when Settings.ServerUrl is blank. Mirrors C# `:22`.
const defaultServerURL = "https://ntfy.sh"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors C# `NtfyException` at
// `Notifications/Ntfy/NtfyException.cs`.
var ErrSendFailed = errors.New("ntfy: send failed")

// ErrAuthRequired is the distinguished sentinel for 401/403 — used
// by Test to map per-property validation failures (AccessToken vs
// UserName) per C# `NtfyProxy.Test` at `:81-103`.
var ErrAuthRequired = errors.New("ntfy: authorization required")

// sendNotification POSTs the title+message+priority+tags+click query-
// string to {server}/{topic} for every topic in settings.Topics.
// Mirrors C# `NtfyProxy.SendNotification` at `Ntfy/NtfyProxy.cs:35-60`.
//
// Returns the first non-nil error encountered after iterating every
// topic — C# logs per-topic errors and throws a wrapped exception
// when ANY topic failed. The Go port surfaces the first failure with
// the topic name included.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	server := strings.TrimSpace(settings.ServerUrl)
	if server == "" {
		server = defaultServerURL
	}
	server = strings.TrimRight(server, "/")
	var firstErr error
	for _, topic := range settings.Topics {
		if err := sendOne(ctx, doer, settings, server, topic, title, message); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// sendOne mirrors the C# `private SendNotification(...HttpRequestBuilder...)`
// at `:113-154`. Builds the query string, attaches auth, POSTs.
func sendOne(ctx context.Context, doer httpDoer, settings *Settings, server, topic, title, message string) error {
	endpoint, err := url.Parse(server + "/" + topic)
	if err != nil {
		return fmt.Errorf("ntfy: parse url for topic %q: %w (cause: %w)", topic, ErrSendFailed, err)
	}
	q := endpoint.Query()
	q.Set("title", title)
	q.Set("message", message)
	q.Set("priority", strconv.Itoa(settings.Priority))
	if len(settings.Tags) > 0 {
		q.Set("tags", strings.Join(settings.Tags, ","))
	}
	if c := strings.TrimSpace(settings.ClickUrl); c != "" {
		q.Set("click", c)
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("ntfy: build request for topic %q: %w (cause: %w)", topic, ErrSendFailed, err)
	}
	if tok := strings.TrimSpace(settings.AccessToken); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if strings.TrimSpace(settings.UserName) != "" && strings.TrimSpace(settings.Password) != "" {
		req.SetBasicAuth(settings.UserName, settings.Password)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: do for topic %q: %w (cause: %w)", topic, ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("ntfy: topic %q: %w", topic, ErrAuthRequired)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: topic %q: status %d: %s", ErrSendFailed, topic, resp.StatusCode, string(excerpt))
	}
	return nil
}
