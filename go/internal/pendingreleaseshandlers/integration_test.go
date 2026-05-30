package pendingreleaseshandlers_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/pendingreleaseshandlers"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// These integration tests exercise the FULL J485 wiring path:
// a real messaging.Bus + the production Subscribe closures from
// cmd/sonarr/main.go + the real pendingreleases.Service (not a stub
// remover) + a real SQLite-backed Repo. They prove that publishing the
// upstream event actually removes the matching pending row — the
// behaviour the previously-unsubscribed handlers left dormant.

func seedPendingRow(t *testing.T, svc *pendingreleases.Service, repo *pendingreleases.Repo, seriesID, season int, eps []int, q quality.Model, title string) {
	t.Helper()
	parsedJSON, _ := json.Marshal(map[string]any{
		"seasonNumber":   season,
		"episodeNumbers": eps,
		"quality":        q,
	})
	relJSON, _ := json.Marshal(map[string]any{
		"title":       title,
		"publishDate": time.Now().UTC(),
		"indexer":     "nzb",
	})
	row := &pendingreleases.Release{
		SeriesID:          seriesID,
		Title:             title,
		Added:             time.Now().UTC(),
		ParsedEpisodeInfo: string(parsedJSON),
		ReleaseInfo:       string(relJSON),
		Reason:            int(pendingreleases.ReasonDelay),
	}
	if _, err := repo.Insert(t.Context(), row); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
}

func mkService(t *testing.T, db *datastore.DB, bus *messaging.Bus) (*pendingreleases.Service, *pendingreleases.Repo) {
	t.Helper()
	repo, err := pendingreleases.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := pendingreleases.NewService(repo, bus, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo
}

// TestEpisodeGrabbedEvent_RemovesPendingRow — a published
// EpisodeGrabbedEvent, routed through the production Subscribe closure,
// removes the matching pending row via the real Service.RemoveGrabbed.
func TestEpisodeGrabbedEvent_RemovesPendingRow(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(slog.Default())
	svc, repo := mkService(t, db, bus)

	q := quality.Model{}
	seedPendingRow(t, svc, repo, 1, 2, []int{5}, q, "show.s02e05")

	// Mirror the exact main.go J485 subscription.
	messaging.Subscribe(bus, "pendingreleases.HandleEpisodeGrabbed",
		func(ctx context.Context, ev decisionengine.EpisodeGrabbedEvent) error {
			return pendingreleaseshandlers.HandleEpisodeGrabbed(ctx, svc, &ev)
		})

	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 1}
	sub.MappedSeasonNumber = 2
	sub.Episodes = []episodes.Episode{{EpisodeNumber: 5}}
	sub.ParsedEpisodeInfo = &parser.ParsedEpisodeInfo{Quality: q}

	if err := messaging.Publish(t.Context(), bus, decisionengine.EpisodeGrabbedEvent{Episode: sub}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rows, err := repo.AllBySeriesID(t.Context(), 1)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("pending rows after grab = %d, want 0 (RemoveGrabbed should have cleared it)", len(rows))
	}
}

// TestSeriesDeletedEvent_PurgesPendingRows — a published
// SeriesDeletedEvent purges that series' pending rows via the real
// Service.DeleteBySeriesIDs.
func TestSeriesDeletedEvent_PurgesPendingRows(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(slog.Default())
	svc, repo := mkService(t, db, bus)

	seedPendingRow(t, svc, repo, 7, 1, []int{1}, quality.Model{}, "other.s01e01")

	messaging.Subscribe(bus, "pendingreleases.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			return pendingreleaseshandlers.HandleSeriesDeleted(ctx, svc, &ev)
		})

	ev := series.SeriesDeletedEvent{Series: []series.Series{{ID: 7}}}
	if err := messaging.Publish(t.Context(), bus, ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rows, err := repo.AllBySeriesID(t.Context(), 7)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("pending rows after series delete = %d, want 0", len(rows))
	}
}
