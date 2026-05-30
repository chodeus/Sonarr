package notifications_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/discord"
	"sonarr.tv/sonarr/internal/series"

	// Blank-import every concrete provider so notifications.Default()
	// is populated with the full catalog — the SAME set cmd/sonarr's
	// provider_imports.go registers. This is the coverage anchor for
	// J460: if a provider is dropped from the registry (or its init
	// registration breaks), the catalog-size assertion below fails.
	_ "sonarr.tv/sonarr/internal/notifications/apprise"
	_ "sonarr.tv/sonarr/internal/notifications/customscript"
	_ "sonarr.tv/sonarr/internal/notifications/email"
	_ "sonarr.tv/sonarr/internal/notifications/gotify"
	_ "sonarr.tv/sonarr/internal/notifications/join"
	_ "sonarr.tv/sonarr/internal/notifications/mailgun"
	_ "sonarr.tv/sonarr/internal/notifications/mediabrowser"
	_ "sonarr.tv/sonarr/internal/notifications/notifiarr"
	_ "sonarr.tv/sonarr/internal/notifications/ntfy"
	_ "sonarr.tv/sonarr/internal/notifications/plex"
	_ "sonarr.tv/sonarr/internal/notifications/prowl"
	_ "sonarr.tv/sonarr/internal/notifications/pushbullet"
	_ "sonarr.tv/sonarr/internal/notifications/pushcut"
	_ "sonarr.tv/sonarr/internal/notifications/pushover"
	_ "sonarr.tv/sonarr/internal/notifications/sendgrid"
	_ "sonarr.tv/sonarr/internal/notifications/signal"
	_ "sonarr.tv/sonarr/internal/notifications/simplepush"
	_ "sonarr.tv/sonarr/internal/notifications/slack"
	_ "sonarr.tv/sonarr/internal/notifications/synology"
	_ "sonarr.tv/sonarr/internal/notifications/telegram"
	_ "sonarr.tv/sonarr/internal/notifications/trakt"
	_ "sonarr.tv/sonarr/internal/notifications/twitter"
	_ "sonarr.tv/sonarr/internal/notifications/webhook"
	_ "sonarr.tv/sonarr/internal/notifications/xbmc"
	// discord is imported (non-blank) above for the instance-name pin.
)

// expectedCatalog is every (Implementation, ConfigContract) pair the
// notification registry must expose — the 25 providers blank-imported
// above (and wired by cmd/sonarr/provider_imports.go). Locking the
// exact set guards against a provider silently dropping out of the
// dispatch fan-out (the J459 dispatch wiring fans out over exactly
// this catalog via notifications.Default()).
var expectedCatalog = map[string]string{
	"Apprise":         "AppriseSettings",
	"CustomScript":    "CustomScriptSettings",
	"Discord":         "DiscordSettings",
	"Email":           "EmailSettings",
	"Gotify":          "GotifySettings",
	"Join":            "JoinSettings",
	"MailGun":         "MailgunSettings",
	"MediaBrowser":    "MediaBrowserSettings",
	"Notifiarr":       "NotifiarrSettings",
	"Ntfy":            "NtfySettings",
	"PlexServer":      "PlexServerSettings",
	"Prowl":           "ProwlSettings",
	"PushBullet":      "PushBulletSettings",
	"Pushcut":         "PushcutSettings",
	"Pushover":        "PushoverSettings",
	"SendGrid":        "SendGridSettings",
	"Signal":          "SignalSettings",
	"Simplepush":      "SimplepushSettings",
	"Slack":           "SlackSettings",
	"SynologyIndexer": "SynologyIndexerSettings",
	"Telegram":        "TelegramSettings",
	"Trakt":           "TraktSettings",
	"Twitter":         "TwitterSettings",
	"Webhook":         "WebhookSettings",
	"Xbmc":            "XbmcSettings",
}

// coverageStubConfig satisfies the `InstanceName() string` surface the
// Discord + Telegram providers narrow off Dependencies.ConfigProvider
// (discord.go:85 / telegram.go instanceNamer). Mirrors the production
// notificationConfigAdapter cmd/sonarr wires from the configfile
// provider.
type coverageStubConfig struct{ name string }

func (c coverageStubConfig) InstanceName() string { return c.name }

// coverageStubPersister satisfies the Trakt provider's settingsPersister
// (Dependencies.NotificationsRepo) — Update round-trips a refreshed
// OAuth token back to the row. A no-op suffices for construction.
type coverageStubPersister struct{}

func (coverageStubPersister) Update(_ context.Context, _ *notifications.Notification) error {
	return nil
}

// prodShapeDeps mirrors the exact Dependencies bundle cmd/sonarr's
// serve() builds for the dispatch service (main.go, J459 + J460): a
// std *http.Client doer, an InstanceName()-bearing ConfigProvider, and
// a NotificationsRepo persister for Trakt's OAuth refresh.
func prodShapeDeps() notifications.Dependencies {
	return notifications.Dependencies{
		HTTPClient:        &http.Client{},
		ConfigProvider:    coverageStubConfig{name: "CoverageInstance"},
		NotificationsRepo: coverageStubPersister{},
	}
}

// TestProviderCatalog_Complete asserts the registry exposes exactly the
// expected 25-provider catalog. A drift here means a provider was added
// without updating cmd/sonarr/provider_imports.go (so the dispatch
// service would never fan out to it) or removed.
func TestProviderCatalog_Complete(t *testing.T) {
	t.Parallel()
	names := notifications.Default().Names()
	got := make(map[string]string, len(names))
	for _, k := range names {
		got[k.Implementation()] = k.ConfigContract()
	}
	if len(got) != len(expectedCatalog) {
		t.Fatalf("catalog size: got %d providers, want %d\ngot: %v", len(got), len(expectedCatalog), got)
	}
	for impl, contract := range expectedCatalog {
		gc, ok := got[impl]
		if !ok {
			t.Errorf("provider %q not registered in Default()", impl)
			continue
		}
		if gc != contract {
			t.Errorf("provider %q: ConfigContract got %q want %q", impl, gc, contract)
		}
	}
}

// TestProviderCatalog_AllConstructCleanly sweeps every registered
// provider and constructs it through the REAL Default() registry with
// the production Dependencies shape — the same path the J459 dispatch
// Factory.enabled() takes per event. A constructor that errors (or a
// nil provider) would silently drop that provider from every fan-out;
// this catches that class at test time rather than in production logs.
func TestProviderCatalog_AllConstructCleanly(t *testing.T) {
	t.Parallel()
	deps := prodShapeDeps()
	for _, k := range notifications.Default().Names() {
		k := k
		t.Run(k.Implementation(), func(t *testing.T) {
			t.Parallel()
			contract := k.ConfigContract()
			row := &notifications.Notification{
				ID:             1,
				Name:           k.Implementation() + "-row",
				Implementation: k.Implementation(),
				ConfigContract: &contract,
				// Settings left empty: every provider's New() applies
				// its own defaults when Settings == "" rather than
				// requiring a populated config to construct.
			}
			p, err := notifications.Default().Construct(row, deps)
			if err != nil {
				t.Fatalf("Construct(%s): %v", k, err)
			}
			if p == nil {
				t.Fatalf("Construct(%s): nil provider, no error", k)
			}
			if p.Name() == "" {
				t.Errorf("Construct(%s): empty Name()", k)
			}
		})
	}
}

// TestDiscord_InstanceNameFromConfigProvider is the behavioral pin for
// the J460 fix: the instance name supplied via
// Dependencies.ConfigProvider must surface as the Discord embed author
// (C# Discord.cs:43 — `Settings.Author ?? _configFileProvider.
// InstanceName`). Before J460 the binary wired the app-config
// config.Provider (no InstanceName accessor) here, so the author always
// fell back to the literal "Sonarr". Drives a real OnSeriesAdd send to
// an httptest server and asserts the author bar carries the configured
// instance name.
func TestDiscord_InstanceNameFromConfigProvider(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	settings, err := json.Marshal(discord.Settings{WebHookURL: srv.URL})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       string(settings),
	}
	p, err := discord.New(def, notifications.Dependencies{
		HTTPClient:     srv.Client(),
		ConfigProvider: coverageStubConfig{name: "MyInstance"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.OnSeriesAdd(context.Background(), notifications.SeriesAddMessage{
		Series: &series.Series{Title: "Show", TvdbID: 1},
	}); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}

	var payload discord.Payload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Embeds) != 1 || payload.Embeds[0].Author == nil {
		t.Fatalf("expected 1 embed with an author bar, got %s", gotBody)
	}
	if got := payload.Embeds[0].Author.Name; got != "MyInstance" {
		t.Errorf("embed author name: got %q, want %q (instance name from ConfigProvider not wired)", got, "MyInstance")
	}
}
