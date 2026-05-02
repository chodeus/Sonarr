package history_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/history"
)

func ptrInt(i int) *int { return &i }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := history.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d, want 0", len(all))
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	earlier := now.Add(-time.Hour)

	grab := &history.Entry{
		EpisodeID:   100,
		SeriesID:    50,
		SourceTitle: "Foo.S01E01.1080p.WEB-DL-RARBG",
		Date:        earlier,
		Quality:     `{"quality":{"id":7,"name":"WEBDL-1080p"},"revision":{"version":1}}`,
		Data: map[string]string{
			"indexer":        "RARBG",
			"releaseGroup":   "RARBG",
			"downloadClient": "qBittorrent",
		},
		EventType:  ptrInt(1),
		DownloadID: "abc123",
		Languages:  []int{1},
	}
	if _, err := repo.Insert(t.Context(), grab); err != nil {
		t.Fatalf("Insert grab: %v", err)
	}

	imported := &history.Entry{
		EpisodeID:   100,
		SeriesID:    50,
		SourceTitle: "Foo.S01E01.1080p.WEB-DL-RARBG",
		Date:        now,
		Quality:     `{"quality":{"id":7,"name":"WEBDL-1080p"},"revision":{"version":1}}`,
		Data:        map[string]string{},
		EventType:   ptrInt(3),
		DownloadID:  "abc123",
		Languages:   []int{1},
	}
	if _, err := repo.Insert(t.Context(), imported); err != nil {
		t.Fatalf("Insert imported: %v", err)
	}

	// Different series, different download.
	otherSeries := &history.Entry{
		EpisodeID:   200,
		SeriesID:    99,
		SourceTitle: "Bar.S02E03",
		Date:        now,
		Quality:     `{}`,
		Data:        map[string]string{},
		Languages:   []int{},
	}
	if _, err := repo.Insert(t.Context(), otherSeries); err != nil {
		t.Fatalf("Insert otherSeries: %v", err)
	}

	got, err := repo.Get(t.Context(), grab.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EventType == nil || *got.EventType != 1 {
		t.Errorf("EventType: %v", got.EventType)
	}
	if !reflect.DeepEqual(got.Data, grab.Data) {
		t.Errorf("Data round-trip:\n got: %v\nwant: %v", got.Data, grab.Data)
	}
	if got.Date.Location() != time.UTC {
		t.Errorf("Date timezone: %v", got.Date.Location())
	}

	// EventType nil round-trip on otherSeries.
	gotOther, err := repo.Get(t.Context(), otherSeries.ID)
	if err != nil {
		t.Fatalf("Get other: %v", err)
	}
	if gotOther.EventType != nil {
		t.Errorf("nil EventType: got %v", *gotOther.EventType)
	}
	if gotOther.DownloadID != "" {
		t.Errorf("empty DownloadID: %q", gotOther.DownloadID)
	}

	// AllBySeriesID — series 50 has 2 rows.
	bySeries50, err := repo.AllBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("AllBySeriesID(50): %v", err)
	}
	if len(bySeries50) != 2 {
		t.Errorf("series 50: got %d, want 2", len(bySeries50))
	}
	// Date DESC order — 'now' (imported) should come before 'earlier' (grab).
	if !bySeries50[0].Date.After(bySeries50[1].Date) {
		t.Errorf("Date DESC ordering: %+v", bySeries50)
	}

	// AllByEpisodeID(100) — same 2 rows.
	byEpisode, err := repo.AllByEpisodeID(t.Context(), 100)
	if err != nil {
		t.Fatalf("AllByEpisodeID: %v", err)
	}
	if len(byEpisode) != 2 {
		t.Errorf("episode 100: got %d, want 2", len(byEpisode))
	}

	// AllByDownloadID("abc123") — same 2 rows.
	byDownload, err := repo.AllByDownloadID(t.Context(), "abc123")
	if err != nil {
		t.Fatalf("AllByDownloadID: %v", err)
	}
	if len(byDownload) != 2 {
		t.Errorf("download abc123: got %d, want 2", len(byDownload))
	}
	if missing, err := repo.AllByDownloadID(t.Context(), "nope"); err != nil || len(missing) != 0 {
		t.Errorf("AllByDownloadID(missing): %v / %d", err, len(missing))
	}

	if err := repo.Update(t.Context(), &history.Entry{ID: 99999, Data: map[string]string{}, Languages: []int{}, Date: now}); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), grab.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), grab.ID); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
