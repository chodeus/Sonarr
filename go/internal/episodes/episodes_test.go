package episodes_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodes"
)

func ptrInt(i int) *int              { return &i }
func ptrTime(t time.Time) *time.Time { return &t }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := episodes.NewRepo(db)
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

	airDate := time.Date(2026, 5, 1, 4, 0, 0, 0, time.UTC)

	full := &episodes.Episode{
		SeriesID:                   50,
		SeasonNumber:               1,
		EpisodeNumber:              1,
		Title:                      "Pilot",
		Overview:                   "The first episode.",
		EpisodeFileID:              ptrInt(100),
		AbsoluteEpisodeNumber:      ptrInt(1),
		SceneAbsoluteEpisodeNumber: ptrInt(1),
		SceneSeasonNumber:          ptrInt(1),
		SceneEpisodeNumber:         ptrInt(1),
		Monitored:                  true,
		AirDateUtc:                 ptrTime(airDate),
		AirDate:                    "2026-05-01",
		Ratings:                    `{"votes":100,"value":8.5}`,
		Images:                     `[{"coverType":"screenshot","url":"http://example.com/foo.jpg"}]`,
		UnverifiedSceneNumbering:   false,
		LastSearchTime:             ptrTime(airDate),
		TvdbID:                     ptrInt(99999),
		Runtime:                    45,
		FinaleType:                 "season",
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	sparse := &episodes.Episode{
		SeriesID:      50,
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	// Different season.
	s2e1 := &episodes.Episode{
		SeriesID:      50,
		SeasonNumber:  2,
		EpisodeNumber: 1,
	}
	if _, err := repo.Insert(t.Context(), s2e1); err != nil {
		t.Fatalf("Insert s2e1: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Pilot" || got.Runtime != 45 || got.FinaleType != "season" {
		t.Errorf("scalar columns: %+v", got)
	}
	if got.EpisodeFileID == nil || *got.EpisodeFileID != 100 {
		t.Errorf("EpisodeFileID: %v", got.EpisodeFileID)
	}
	if got.AirDateUtc == nil || !got.AirDateUtc.Equal(airDate) {
		t.Errorf("AirDateUtc: %v", got.AirDateUtc)
	}
	if !got.Monitored {
		t.Errorf("Monitored: false, want true")
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.Title != "" || gotSparse.AirDate != "" || gotSparse.Ratings != "" {
		t.Errorf("sparse string nullables: %+v", gotSparse)
	}
	if gotSparse.EpisodeFileID != nil || gotSparse.AirDateUtc != nil ||
		gotSparse.LastSearchTime != nil || gotSparse.TvdbID != nil {
		t.Errorf("sparse pointer nullables not nil: %+v", gotSparse)
	}
	if gotSparse.Monitored {
		t.Errorf("default Monitored: true, want false")
	}
	if gotSparse.Runtime != 0 {
		t.Errorf("default Runtime: %d, want 0", gotSparse.Runtime)
	}

	// AllBySeriesID — series 50 has 3.
	bySeries, err := repo.AllBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(bySeries) != 3 {
		t.Errorf("series 50: got %d, want 3", len(bySeries))
	}
	// Order is (S,E) ascending: S1E1, S1E2, S2E1.
	if bySeries[0].SeasonNumber != 1 || bySeries[0].EpisodeNumber != 1 {
		t.Errorf("first row: %+v", bySeries[0])
	}
	if bySeries[2].SeasonNumber != 2 || bySeries[2].EpisodeNumber != 1 {
		t.Errorf("third row: %+v", bySeries[2])
	}

	// AllBySeriesAndSeason(50, 1) — 2 episodes.
	bySeason, err := repo.AllBySeriesAndSeason(t.Context(), 50, 1)
	if err != nil {
		t.Fatalf("AllBySeriesAndSeason: %v", err)
	}
	if len(bySeason) != 2 {
		t.Errorf("season 1: got %d, want 2", len(bySeason))
	}

	if err := repo.Update(t.Context(), &episodes.Episode{ID: 99999}); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// 5.D3 port pre-work: DeleteBySeriesID bulk-cascade. Use series
	// 200 (untouched by the earlier seed block) so this test doesn't
	// interfere with the per-row Delete above.
	for _, ep := range []episodes.Episode{
		{SeriesID: 200, SeasonNumber: 1, EpisodeNumber: 1},
		{SeriesID: 200, SeasonNumber: 1, EpisodeNumber: 2},
		{SeriesID: 200, SeasonNumber: 2, EpisodeNumber: 1},
	} {
		ep := ep
		if _, err := repo.Insert(t.Context(), &ep); err != nil {
			t.Fatalf("Insert series 200: %v", err)
		}
	}
	deleted, err := repo.DeleteBySeriesID(t.Context(), 200)
	if err != nil {
		t.Fatalf("DeleteBySeriesID: %v", err)
	}
	if deleted != 3 {
		t.Errorf("DeleteBySeriesID rows: got %d, want 3", deleted)
	}
	remaining, err := repo.AllBySeriesID(t.Context(), 200)
	if err != nil {
		t.Fatalf("AllBySeriesID after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("series 200 should have 0 episodes after delete; got %d", len(remaining))
	}
	// Idempotent on already-empty series: returns 0, no error.
	deleted2, err := repo.DeleteBySeriesID(t.Context(), 99999)
	if err != nil {
		t.Errorf("DeleteBySeriesID(missing): %v", err)
	}
	if deleted2 != 0 {
		t.Errorf("DeleteBySeriesID(missing) rows: got %d, want 0", deleted2)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
