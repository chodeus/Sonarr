package series_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/series"
)

func ptrInt(i int) *int              { return &i }
func ptrTime(t time.Time) *time.Time { return &t }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := series.NewRepo(db)
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

	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	full := &series.Series{
		TvdbID:            361753,
		TvRageID:          0,
		ImdbID:            "tt8111088",
		Title:             "The Mandalorian",
		TitleSlug:         "the-mandalorian",
		CleanTitle:        "themandalorian",
		Status:            1,
		Overview:          "A lone bounty hunter.",
		AirTime:           "21:00",
		Images:            `[{"coverType":"poster","url":"http://example.com/p.jpg"}]`,
		Path:              "/data/tv/The Mandalorian",
		Monitored:         true,
		SeasonFolder:      true,
		LastInfoSync:      ptrTime(now),
		LastDiskSync:      ptrTime(now),
		Runtime:           45,
		SeriesType:        0,
		Network:           "Disney+",
		UseSceneNumbering: false,
		FirstAired:        ptrTime(now),
		NextAiring:        ptrTime(now),
		Year:              ptrInt(2019),
		Seasons:           `[{"seasonNumber":1,"monitored":true}]`,
		Actors:            `[]`,
		Ratings:           `{"votes":1000,"value":8.5}`,
		Genres:            `["Sci-Fi","Drama"]`,
		Certification:     "TV-PG",
		SortTitle:         "mandalorian, the",
		QualityProfileID:  ptrInt(1),
		Tags:              []int{1, 2, 3},
		Added:             ptrTime(now),
		AddOptions:        `{"searchForMissingEpisodes":false}`,
		TvMazeID:          12345,
		OriginalLanguage:  1,
		LastAired:         ptrTime(now),
		MonitorNewItems:   0,
		TmdbID:            82856,
		MalIDs:            []int{0},
		AniListIDs:        []int{0},
		OriginalCountry:   "US",
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	sparse := &series.Series{
		TvdbID:       99999,
		TvRageID:     0,
		Title:        "Foundation",
		CleanTitle:   "foundation",
		Status:       1,
		Path:         "/data/tv/Foundation",
		Monitored:    false,
		SeasonFolder: true,
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "The Mandalorian" || got.Network != "Disney+" {
		t.Errorf("scalars: %+v", got)
	}
	if got.Year == nil || *got.Year != 2019 {
		t.Errorf("Year: %v", got.Year)
	}
	if !reflect.DeepEqual(got.Tags, []int{1, 2, 3}) {
		t.Errorf("Tags: %v", got.Tags)
	}
	if got.QualityProfileID == nil || *got.QualityProfileID != 1 {
		t.Errorf("QualityProfileID: %v", got.QualityProfileID)
	}
	if got.LastInfoSync == nil || !got.LastInfoSync.Equal(now) {
		t.Errorf("LastInfoSync: %v", got.LastInfoSync)
	}
	if !got.Monitored || !got.SeasonFolder {
		t.Errorf("bools: %+v", got)
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.LastInfoSync != nil || gotSparse.FirstAired != nil ||
		gotSparse.LastAired != nil || gotSparse.Added != nil {
		t.Errorf("sparse pointer nullables: %+v", gotSparse)
	}
	if gotSparse.Year != nil || gotSparse.QualityProfileID != nil {
		t.Errorf("sparse int pointers: %+v", gotSparse)
	}
	if gotSparse.Network != "" || gotSparse.Genres != "" {
		t.Errorf("sparse strings: %+v", gotSparse)
	}
	if gotSparse.MalIDs == nil || len(gotSparse.MalIDs) != 0 {
		t.Errorf("sparse MalIDs: %v", gotSparse.MalIDs)
	}

	// GetByTvdbID round-trip.
	byTvdb, err := repo.GetByTvdbID(t.Context(), 361753)
	if err != nil {
		t.Fatalf("GetByTvdbID: %v", err)
	}
	if byTvdb.ID != full.ID {
		t.Errorf("GetByTvdbID: %d, want %d", byTvdb.ID, full.ID)
	}
	if _, err := repo.GetByTvdbID(t.Context(), 0); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("GetByTvdbID(missing): %v", err)
	}

	// UNIQUE on TvdbId.
	if _, err := repo.Insert(t.Context(), &series.Series{TvdbID: 361753, Title: "Dup", CleanTitle: "dup", Status: 1, Path: "/x", Images: "[]"}); err == nil {
		t.Errorf("Insert duplicate TvdbId: expected error")
	}

	if err := repo.Update(t.Context(), &series.Series{ID: 99999, Title: "x", CleanTitle: "x", Path: "/x", Images: "[]", TvdbID: 99999, Status: 1}); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
