package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the value stored in the Notifications.Implementation
// column for rows wired to this provider. Mirrors C# `Slack.cs:14`.
const Implementation = "Slack"

// ConfigContract is the value stored in the Notifications.
// ConfigContract column. Mirrors C# `SlackSettings.cs:16`.
const ConfigContract = "SlackSettings"

// Slack ports the C# `Slack` notification provider at
// `Notifications/Slack/Slack.cs:14-293`.
//
// Goroutine-safety: httpDoer is shared across concurrent OnX
// calls. settings is captured by value at construction time.
type Slack struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
	now      func() time.Time
}

// Compile-time guard.
var _ notifications.Provider = (*Slack)(nil)

// New constructs a *Slack from a persisted [notifications.Notification]
// row + the [notifications.Dependencies] bundle. Mirrors the
// constructor pattern from internal/notifications/webhook/webhook.go.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("slack: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("slack: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Slack{
		def:      def,
		settings: s,
		doer:     doer,
		now:      time.Now,
	}, nil
}

// Name returns the provider display label. C# `Name` at `:25`.
func (s *Slack) Name() string { return "Slack" }

// Link returns the docs URL. C# `Link` at `:26`.
func (s *Slack) Link() string {
	return "https://my.slack.com/services/new/incoming-webhook/"
}

// Definition returns the persisted row.
func (s *Slack) Definition() *notifications.Notification { return s.def }

// createPayload builds the outer Slack payload — username + icon
// (emoji vs URL discriminator from C# `:249-256`) + channel +
// attachments + top-level text. Shared by every OnX builder.
func (s *Slack) createPayload(message string, attachments []Attachment) *Payload {
	p := &Payload{
		Username:    s.settings.Username,
		Text:        message,
		Attachments: attachments,
	}
	if icon := strings.TrimSpace(s.settings.Icon); icon != "" {
		if strings.HasPrefix(icon, ":") && strings.HasSuffix(icon, ":") {
			p.IconEmoji = icon
		} else {
			p.IconURL = icon
		}
	}
	if ch := strings.TrimSpace(s.settings.Channel); ch != "" {
		p.Channel = ch
	}
	return p
}

// OnGrab mirrors C# `OnGrab` at `Slack.cs:28-43`.
func (s *Slack) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	att := Attachment{
		Fallback: msg.Message,
		Title:    titleFromSeries(msg.Series),
		Text:     msg.Message,
		Color:    ColorWarning,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload(fmt.Sprintf("Grabbed: %s", msg.Message), []Attachment{att}))
}

// OnDownload mirrors C# `OnDownload` at `:45-60`.
func (s *Slack) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	att := Attachment{
		Fallback: msg.Message,
		Title:    titleFromSeries(msg.Series),
		Text:     msg.Message,
		Color:    ColorGood,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload(fmt.Sprintf("Imported: %s", msg.Message), []Attachment{att}))
}

// OnImportComplete mirrors C# `OnImportComplete` at `:62-77`.
func (s *Slack) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	att := Attachment{
		Fallback: msg.Message,
		Title:    titleFromSeries(msg.Series),
		Text:     msg.Message,
		Color:    ColorGood,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload(fmt.Sprintf("Imported all expected episodes: %s", msg.Message), []Attachment{att}))
}

// OnRename mirrors C# `OnRename` at `:79-92`.
func (s *Slack) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	att := Attachment{Title: titleFromSeriesAny(msg.Series)}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Renamed", []Attachment{att}))
}

// OnEpisodeFileDelete mirrors C# `OnEpisodeFileDelete` at `:94-107`.
// The C# version's rich `GetTitle(series, episodeFile.Episodes)` cannot
// be ported faithfully because the Go-port EpisodeDeleteMessage doesn't
// carry the resolved Episode list (EpisodeFile.Episodes is `[]int` of
// IDs, not full Episode rows). Falls back to the series title — the
// reason/message already conveys per-file context.
func (s *Slack) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	att := Attachment{Title: titleFromSeries(msg.Series)}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Episode Deleted", []Attachment{att}))
}

// OnSeriesAdd mirrors C# `OnSeriesAdd` at `:109-122`.
func (s *Slack) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	att := Attachment{Title: titleFromSeries(msg.Series)}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Series Added", []Attachment{att}))
}

// OnSeriesDelete mirrors C# `OnSeriesDelete` at `:124-138`.
func (s *Slack) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	att := Attachment{
		Title: titleFromSeries(msg.Series),
		Text:  msg.DeletedFilesMessage,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Series Deleted", []Attachment{att}))
}

// OnHealthIssue mirrors C# `OnHealthIssue` at `:140-155`. Color is
// "warning" for Warning, "danger" otherwise.
func (s *Slack) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	color := ColorDanger
	if strings.EqualFold(msg.Check.Type.String(), "warning") {
		color = ColorWarning
	}
	att := Attachment{
		Title: msg.Check.Source,
		Text:  msg.Check.Message,
		Color: color,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Health Issue", []Attachment{att}))
}

// OnHealthRestored mirrors C# `OnHealthRestored` at `:157-172`.
func (s *Slack) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	att := Attachment{
		Title: msg.Check.Source,
		Text:  fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message),
		Color: ColorGood,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Health Issue Resolved", []Attachment{att}))
}

// OnApplicationUpdate mirrors C# `OnApplicationUpdate` at
// `:174-189`. C# uses `Environment.MachineName` for the Title; the
// Go port has no portable equivalent — leaves Title empty (the
// per-row Text still surfaces the update message).
func (s *Slack) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	att := Attachment{
		Text:  msg.Message,
		Color: ColorGood,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Application Updated", []Attachment{att}))
}

// OnManualInteractionRequired mirrors C# `OnManualInteractionRequired`
// at `:191-206`.
func (s *Slack) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	att := Attachment{
		Text:  msg.Message,
		Color: ColorWarning,
	}
	return sendPayload(ctx, s.doer, &s.settings, s.createPayload("Manual Interaction Required", []Attachment{att}))
}

// ProcessQueue is a no-op — Slack has no queue to drain.
func (s *Slack) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `Test()` +
// `TestMessage()` at `:208-232`.
func (s *Slack) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := s.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	body := fmt.Sprintf("Test message from Sonarr posted at %s", s.now().Format(time.RFC1123))
	if err := sendPayload(ctx, s.doer, &s.settings, s.createPayload(body, nil)); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "WebHookUrl",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// titleFromSeries returns series.Title or "" when nil. Mirrors C#
// `GetTitle(series, episodes)` simplification at `:269-272`.
func titleFromSeries(s *series.Series) string {
	if s == nil {
		return ""
	}
	return s.Title
}

// titleFromSeriesAny accepts an `any`-typed series field (some
// Message shapes carry it as any pending typed-port). Returns ""
// when the assertion fails.
func titleFromSeriesAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(*series.Series); ok {
		return titleFromSeries(s)
	}
	return ""
}

// SupportsOnGrab reports OnGrab capability. C# Slack overrides
// every OnX hook, so every Supports* flag is true. Mirrors the
// discord precedent.
func (s *Slack) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Slack.SupportsOnGrab].
func (s *Slack) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Slack` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (s *Slack) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Slack constructor with the package-global
// [notifications.Default] registry.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
