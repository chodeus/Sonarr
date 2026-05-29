package notifications_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/webhook"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// TestDispatch_RealRegistry_WebhookOnDownload_J459 is the end-to-end
// verification of the J459 dispatch wiring: it exercises the SAME path
// main.go wires — the real notifications.Default() provider registry →
// Factory → Service → bus subscription → a concrete provider's real HTTP
// send. A published EpisodeImportedEvent must reach an enabled Webhook
// notification and POST to its configured URL. If the dispatch Service
// weren't subscribed (the pre-J459 bug), no POST would arrive.
func TestDispatch_RealRegistry_WebhookOnDownload_J459(t *testing.T) {
	t.Parallel()
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case got <- string(b):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	settings, err := json.Marshal(webhook.Settings{URL: srv.URL, Method: int(webhook.MethodPOST)})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	contract := webhook.ConfigContract
	rows := []notifications.Notification{{
		ID:             1,
		Name:           "wh",
		Implementation: webhook.Implementation,
		ConfigContract: &contract,
		OnDownload:     true,
		Settings:       string(settings),
	}}

	// Build the dispatch service exactly as main.go does — over the REAL
	// Default() registry (populated by the blank import below) + a real
	// net/http client (the httptest server's).
	factory, err := notifications.NewFactory(&stubRepo{rows: rows}, notifications.Default(), nil, notifications.Dependencies{HTTPClient: srv.Client()}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	svc, err := notifications.NewService(factory, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	bus := messaging.New(slog.New(slog.DiscardHandler))
	svc.Subscribe(bus)

	ev := episodeimport.EpisodeImportedEvent{
		NewDownload: true,
		LocalEpisode: &episodeimport.LocalEpisode{
			Series:   &series.Series{Title: "Show"},
			Episodes: []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}},
			Quality:  quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()},
		},
		EpisodeFile: &episodefiles.File{},
	}
	if err := messaging.Publish(context.Background(), bus, ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case body := <-got:
		if body == "" {
			t.Error("webhook POST had an empty body")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was never POSTed — the notification dispatch wiring is broken")
	}
}
