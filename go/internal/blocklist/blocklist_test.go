package blocklist_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt64(i int64) *int64        { return &i }
func ptrInt(i int) *int              { return &i }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := blocklist.NewRepo(db)
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
	yesterday := now.Add(-24 * time.Hour)

	full := &blocklist.Entry{
		SeriesID:        100,
		EpisodeIDs:      []int{1, 2, 3},
		SourceTitle:     "Foo.S01E01.PROPER.720p-RARBG",
		Quality:         `{"quality":{"id":4,"name":"HDTV-720p"},"revision":{"version":2,"real":0,"isRepack":true}}`,
		Date:            now,
		PublishedDate:   ptrTime(yesterday),
		Size:            ptrInt64(2_500_000_000),
		Protocol:        ptrInt(1),
		Indexer:         "RARBG",
		Message:         "User-blocked: bad encode",
		TorrentInfoHash: "deadbeefcafebabe",
		Languages:       []int{1, 2}, // English + French
		IndexerFlags:    1,
		ReleaseType:     0,
		Source:          "Manual",
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	// Sparse — only NOT-NULL columns set, exercising NULL persistence on
	// every nullable column.
	sparse := &blocklist.Entry{
		SeriesID:    100,
		EpisodeIDs:  []int{},
		SourceTitle: "Foo.S01E02.WEBDL",
		Date:        now,
		Languages:   []int{},
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	// Different series.
	other := &blocklist.Entry{
		SeriesID:    200,
		EpisodeIDs:  []int{},
		SourceTitle: "Bar.S02E05",
		Date:        now,
		Languages:   []int{},
	}
	if _, err := repo.Insert(t.Context(), other); err != nil {
		t.Fatalf("Insert other-series: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get full: %v", err)
	}
	if !reflect.DeepEqual(got.EpisodeIDs, []int{1, 2, 3}) {
		t.Errorf("EpisodeIDs round-trip: %v", got.EpisodeIDs)
	}
	if !reflect.DeepEqual(got.Languages, []int{1, 2}) {
		t.Errorf("Languages round-trip: %v", got.Languages)
	}
	if got.Quality != full.Quality {
		t.Errorf("Quality round-trip:\n got: %s\nwant: %s", got.Quality, full.Quality)
	}
	if got.PublishedDate == nil || !got.PublishedDate.Equal(yesterday) {
		t.Errorf("PublishedDate: %v", got.PublishedDate)
	}
	if got.Size == nil || *got.Size != 2_500_000_000 {
		t.Errorf("Size: %v", got.Size)
	}
	if got.Protocol == nil || *got.Protocol != 1 {
		t.Errorf("Protocol: %v", got.Protocol)
	}
	if got.Date.Location() != time.UTC {
		t.Errorf("Date timezone: %v", got.Date.Location())
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.PublishedDate != nil || gotSparse.Size != nil || gotSparse.Protocol != nil {
		t.Errorf("sparse pointer nullables not nil: %+v", gotSparse)
	}
	if gotSparse.Indexer != "" || gotSparse.Message != "" || gotSparse.TorrentInfoHash != "" || gotSparse.Source != "" {
		t.Errorf("sparse string nullables not empty: %+v", gotSparse)
	}
	if gotSparse.Quality != "{}" {
		t.Errorf("sparse Quality: %q, want default '{}'", gotSparse.Quality)
	}

	// AllBySeriesID — series 100 has 2 rows, series 200 has 1.
	bySeries100, err := repo.AllBySeriesID(t.Context(), 100)
	if err != nil {
		t.Fatalf("AllBySeriesID(100): %v", err)
	}
	if len(bySeries100) != 2 {
		t.Errorf("series100: got %d, want 2", len(bySeries100))
	}
	bySeries200, err := repo.AllBySeriesID(t.Context(), 200)
	if err != nil {
		t.Fatalf("AllBySeriesID(200): %v", err)
	}
	if len(bySeries200) != 1 {
		t.Errorf("series200: got %d, want 1", len(bySeries200))
	}

	if err := repo.Update(t.Context(), &blocklist.Entry{ID: 99999, SeriesID: 1, EpisodeIDs: []int{}, SourceTitle: "x", Date: now, Languages: []int{}}); !errors.Is(err, blocklist.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, blocklist.ErrNotFound) {
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
