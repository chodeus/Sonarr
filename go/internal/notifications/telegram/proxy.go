package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// telegramAPIBaseURL is the Telegram bot HTTP-API host the proxy POSTs
// to. Mirrors C# `URL` const at
// `Notifications/Telegram/TelegramProxy.cs:24`. The full endpoint is
// constructed by appending `/bot{token}/sendMessage`.
var telegramAPIBaseURL = "https://api.telegram.org"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure (DNS / TCP /
// non-2xx HTTP status). Mirrors the C# `WebException` / `HttpException`
// fan-out at `TelegramProxy.cs:90-118` — the Go port collapses both
// into one sentinel.
var ErrSendFailed = errors.New("telegram: send failed")

// sendNotification POSTs the per-event payload to the Telegram bot
// API. Mirrors C# `TelegramProxy.SendNotification` at
// `Notifications/Telegram/TelegramProxy.cs:39-70`.
//
// Text-encoding rules (HTML parse_mode):
//   - The title is wrapped in <b>...</b> and HTML-escaped.
//   - The message body is HTML-escaped on a new line.
//   - Each link renders as <a href="..."><label></a>, with the label
//     HTML-escaped. The URL is NOT escaped — it's used as-is per the
//     C# code's $"<a href=\"{link.Link}\">..." interpolation.
func sendNotification(ctx context.Context, doer httpDoer, settings *Settings, title, message string, links []notifications.MetadataLink) error {
	var text strings.Builder
	text.WriteString("<b>")
	text.WriteString(html.EscapeString(title))
	text.WriteString("</b>\n")
	text.WriteString(html.EscapeString(message))
	text.WriteString("\n")
	for _, l := range links {
		text.WriteString(`<a href="`)
		text.WriteString(l.URL)
		text.WriteString(`">`)
		text.WriteString(html.EscapeString(l.Label))
		text.WriteString("</a>\n")
	}

	payload := Payload{
		ChatID:              settings.ChatId,
		ParseMode:           "HTML",
		Text:                text.String(),
		DisableNotification: settings.SendSilently,
		MessageThreadID:     settings.TopicId,
		LinkPreviewOptions:  buildPreviewOption(links, settings),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	endpoint, err := url.JoinPath(telegramAPIBaseURL, "bot"+settings.BotToken, "sendmessage")
	if err != nil {
		return fmt.Errorf("telegram: join url: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(excerpt))
	}
	return nil
}

// buildPreviewOption mirrors the C# `TelegramLinkPreviewOptions`
// constructor at `Telegram/TelegramLinkPreviewOptions.cs:15-19`.
// IsDisabled=true when Settings.LinkPreview == None; URL is the
// first link whose Type matches Settings.LinkPreview (or "" if no
// match — the C# `.FirstOrDefault(...)?.Link` collapses to null).
func buildPreviewOption(links []notifications.MetadataLink, settings *Settings) *PreviewOption {
	opt := &PreviewOption{
		IsDisabled: LinkPreviewType(settings.LinkPreview) == LinkPreviewNone,
	}
	for _, l := range links {
		if int(l.Type) == settings.LinkPreview {
			opt.URL = l.URL
			break
		}
	}
	return opt
}
