package downloadhistory_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadhistory"
)

func ptrInt(i int) *int { return &i }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := downloadhistory.NewRepo(db)
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

	grab := &downloadhistory.Entry{
		EventType:        1, // DownloadGrabbed
		SeriesID:         50,
		DownloadID:       "abc123",
		SourceTitle:      "Foo.S01E01",
		Date:             earlier,
		Protocol:         1,
		IndexerID:        10,
		DownloadClientID: 2,
		Release:          `{"title":"Foo.S01E01","size":1234}`,
		Data:             `{"releaseGroup":"RARBG"}`,
	}
	if _, err := repo.Insert(t.Context(), grab); err != nil {
		t.Fatalf("Insert grab: %v", err)
	}

	imported := &downloadhistory.Entry{
		EventType:   2, // DownloadImported
		SeriesID:    50,
		DownloadID:  "abc123",
		SourceTitle: "Foo.S01E01",
		Date:        now,
	}
	if _, err := repo.Insert(t.Context(), imported); err != nil {
		t.Fatalf("Insert imported: %v", err)
	}

	got, err := repo.Get(t.Context(), grab.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Protocol == 0 || got.Protocol != 1 {
		t.Errorf("Protocol: %v", got.Protocol)
	}
	if got.Release != grab.Release {
		t.Errorf("Release: %q", got.Release)
	}

	gotImp, err := repo.Get(t.Context(), imported.ID)
	if err != nil {
		t.Fatalf("Get imported: %v", err)
	}
	if gotImp.Protocol != 0 || gotImp.Release != "" || gotImp.Data != "" {
		t.Errorf("imported sparse: %+v", gotImp)
	}

	byDownload, err := repo.AllByDownloadID(t.Context(), "abc123")
	if err != nil {
		t.Fatalf("AllByDownloadID: %v", err)
	}
	if len(byDownload) != 2 {
		t.Errorf("byDownload: got %d, want 2", len(byDownload))
	}
	// Date DESC order — imported (now) should come before grab (earlier).
	if byDownload[0].EventType != 2 || byDownload[1].EventType != 1 {
		t.Errorf("Date DESC ordering: %+v", byDownload)
	}

	bySeries, err := repo.AllBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(bySeries) != 2 {
		t.Errorf("bySeries: got %d, want 2", len(bySeries))
	}

	if err := repo.Update(t.Context(), &downloadhistory.Entry{ID: 99999, DownloadID: "x", SourceTitle: "x", Date: now}); !errors.Is(err, downloadhistory.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), grab.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), grab.ID); !errors.Is(err, downloadhistory.ErrNotFound) {
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
