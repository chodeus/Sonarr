package twitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Twitter.cs:9` class name.
const Implementation = "Twitter"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `TwitterSettings.cs:30` class name.
const ConfigContract = "TwitterSettings"

// Twitter ports the C# `Twitter` notification provider at
// `Notifications/Twitter/Twitter.cs:9-122`.
type Twitter struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
	now      func() time.Time
}

// Compile-time guard.
var _ notifications.Provider = (*Twitter)(nil)

// New constructs a *Twitter from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("twitter: nil definition")
	}
	// Match C# `TwitterSettings()` constructor default at
	// `TwitterSettings.cs:34-38`: DirectMessage=true.
	s := Settings{DirectMessage: true}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("twitter: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Twitter{def: def, settings: s, doer: doer, now: time.Now}, nil
}

// Name returns the provider display label. C# `:18`.
func (t *Twitter) Name() string { return "Twitter" }

// Link returns the docs URL. C# `:19`.
func (t *Twitter) Link() string { return "https://twitter.com/" }

// Definition returns the persisted row.
func (t *Twitter) Definition() *notifications.Notification { return t.def }

// send dispatches the message through either DM or public-status.
// Mirrors C# `TwitterService.SendNotification` at `:49-65`.
//
// Public-status mode appends " @{Mention}" to the body when Mention
// is set; DM mode takes the target screen-name from Settings.Mention.
func (t *Twitter) send(ctx context.Context, body string) error {
	if t.settings.DirectMessage {
		return sendDirectMessage(ctx, t.doer, &t.settings, body)
	}
	if strings.TrimSpace(t.settings.Mention) != "" {
		body = body + " @" + t.settings.Mention
	}
	return sendStatus(ctx, t.doer, &t.settings, body)
}

// OnGrab mirrors C# `:21-24`. Body prefixed with "Grabbed: ".
func (t *Twitter) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return t.send(ctx, "Grabbed: "+msg.Message)
}

// OnDownload mirrors C# `:26-29`. Body prefixed with "Imported: ".
func (t *Twitter) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return t.send(ctx, "Imported: "+msg.Message)
}

// OnImportComplete mirrors C# `:31-34`. Body prefixed with "Imported: ".
func (t *Twitter) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return t.send(ctx, "Imported: "+msg.Message)
}

// OnRename is a no-op — C# Twitter doesn't override OnRename.
func (t *Twitter) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:36-39`. Body prefixed with
// "Episode Deleted: ".
func (t *Twitter) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return t.send(ctx, "Episode Deleted: "+msg.Message)
}

// OnSeriesAdd mirrors C# `:41-44`. Body prefixed with "Series Added: ".
func (t *Twitter) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return t.send(ctx, "Series Added: "+msg.Message)
}

// OnSeriesDelete mirrors C# `:46-49`. Body prefixed with "Series Deleted: ".
func (t *Twitter) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return t.send(ctx, "Series Deleted: "+msg.Message)
}

// OnHealthIssue mirrors C# `:51-54`. Body prefixed with "Health Issue: ".
func (t *Twitter) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return t.send(ctx, "Health Issue: "+msg.Check.Message)
}

// OnHealthRestored mirrors C# `:56-59`. Body prefixed with
// "Health Issue Resolved: ".
func (t *Twitter) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return t.send(ctx, "Health Issue Resolved: "+msg.Check.Message)
}

// OnApplicationUpdate mirrors C# `:61-64`. Body prefixed with
// "Application Updated: ".
func (t *Twitter) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return t.send(ctx, "Application Updated: "+msg.Message)
}

// OnManualInteractionRequired mirrors C# `:66-69`. Body prefixed
// with "Manual Interaction Required: ".
func (t *Twitter) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return t.send(ctx, "Manual Interaction Required: "+msg.Message)
}

// ProcessQueue is a no-op — Twitter has no queue.
func (t *Twitter) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `TwitterService.Test`
// at `:92-107`. The C# code stamps the body with DateTime.Now;
// the Go port uses the injected clock to keep the message
// deterministic in tests. Failures surface with PropertyName="Host"
// per C# `:103`.
func (t *Twitter) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := t.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	body := "Sonarr: Test Message @ " + t.now().Format(time.RFC1123)
	if err := t.send(ctx, body); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Twitter overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (t *Twitter) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Twitter.SupportsOnGrab]. Returns false.
func (t *Twitter) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Twitter.SupportsOnGrab].
func (t *Twitter) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction implements the C# `Twitter.RequestAction` admin
// path at `:71-111`. Two actions drive the Twitter OAuth1 PIN dance:
//
//   - startOAuth — POSTs /oauth/request_token with the supplied
//     callbackUrl as oauth_callback, then returns
//     `{oauthUrl: "https://api.twitter.com/oauth/authorize?
//     oauth_token=..."}` for the v3 UI to redirect the browser to.
//     Missing callbackUrl returns [ErrBadRequest].
//   - getOAuthToken — POSTs /oauth/access_token with the
//     redirect-returned oauth_token + oauth_verifier; returns the
//     persisted-token tuple `{accessToken, accessTokenSecret}`.
//     Missing oauth_token / oauth_verifier returns [ErrBadRequest].
//
// Settings.Validate().Filter("ConsumerKey", "ConsumerSecret") at
// C# `:75/90` would filter validation failures to those two
// property names — but the v3 UI surfaces those failures via the
// pre-RequestAction form-load Validate call, so the filter is a
// belt-and-suspenders guard. The Go port omits it (matches the
// notifications/plex + importlists/plex decisions).
//
// Unknown actions return `{}` (mirrors C# `:110`).
func (t *Twitter) RequestAction(ctx context.Context, action string, query map[string]string) (any, error) {
	switch action {
	case "startOAuth":
		callbackURL := query["callbackUrl"]
		if callbackURL == "" {
			return nil, fmt.Errorf("%w: callbackUrl invalid", ErrBadRequest)
		}
		oauthURL, err := getOAuthRedirect(ctx, t.doer, t.settings.ConsumerKey, t.settings.ConsumerSecret, callbackURL)
		if err != nil {
			return nil, err
		}
		return map[string]any{"oauthUrl": oauthURL}, nil
	case "getOAuthToken":
		oauthToken := query["oauth_token"]
		oauthVerifier := query["oauth_verifier"]
		if oauthToken == "" {
			return nil, fmt.Errorf("%w: oauth_token invalid", ErrBadRequest)
		}
		if oauthVerifier == "" {
			return nil, fmt.Errorf("%w: oauth_verifier invalid", ErrBadRequest)
		}
		accessToken, accessTokenSecret, err := getOAuthAccessToken(ctx, t.doer, t.settings.ConsumerKey, t.settings.ConsumerSecret, oauthToken, oauthVerifier)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"accessToken":       accessToken,
			"accessTokenSecret": accessTokenSecret,
		}, nil
	default:
		return map[string]any{}, nil
	}
}

// init registers the Twitter constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
