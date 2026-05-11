package series_test

import (
	"errors"
	"sort"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/series"
)

// seedTwo inserts two canonical fixture series for the query tests.
// "The Mandalorian" (TvdbId 361753, year 2019, imdb tt8111088,
// /data/tv/The Mandalorian, tags [1,2,3], qualityProfile 1) and
// "Foundation" (TvdbId 99999, year 2021, no imdb, /data/tv/Foundation,
// no tags, no quality profile).
func seedTwo(t *testing.T, db *datastore.DB) (*series.Repo, [2]int) {
	t.Helper()
	repo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	mando := &series.Series{
		TvdbID:           361753,
		TvRageID:         55555,
		ImdbID:           "tt8111088",
		Title:            "The Mandalorian",
		CleanTitle:       "themandalorian",
		Status:           1,
		Images:           "[]",
		Path:             "/data/tv/The Mandalorian",
		Year:             2019,
		Tags:             []int{1, 2, 3},
		QualityProfileID: 1,
	}
	foundation := &series.Series{
		TvdbID:     99999,
		TvRageID:   0,
		Title:      "Foundation",
		CleanTitle: "foundation",
		Status:     1,
		Images:     "[]",
		Path:       "/data/tv/Foundation",
		Year:       2021,
	}
	if _, err := repo.Insert(t.Context(), mando); err != nil {
		t.Fatalf("Insert mando: %v", err)
	}
	if _, err := repo.Insert(t.Context(), foundation); err != nil {
		t.Fatalf("Insert foundation: %v", err)
	}
	return repo, [2]int{mando.ID, foundation.ID}
}

func TestRepo_FindByTvRageID(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.FindByTvRageID(t.Context(), 55555)
	if err != nil {
		t.Fatalf("FindByTvRageID: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("FindByTvRageID: id=%d want %d", got.ID, ids[0])
	}
	if _, err := repo.FindByTvRageID(t.Context(), 12345678); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("FindByTvRageID(missing): %v", err)
	}
}

func TestRepo_FindByImdbID(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.FindByImdbID(t.Context(), "tt8111088")
	if err != nil {
		t.Fatalf("FindByImdbID: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("FindByImdbID: id=%d want %d", got.ID, ids[0])
	}
	if _, err := repo.FindByImdbID(t.Context(), "tt0000000"); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("FindByImdbID(missing): %v", err)
	}
}

func TestRepo_FindByPath(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.FindByPath(t.Context(), "/data/tv/Foundation")
	if err != nil {
		t.Fatalf("FindByPath: %v", err)
	}
	if got.ID != ids[1] {
		t.Errorf("FindByPath: id=%d want %d", got.ID, ids[1])
	}
}

func TestRepo_FindByTitle(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.FindByTitle(t.Context(), "themandalorian")
	if err != nil {
		t.Fatalf("FindByTitle: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("FindByTitle: id=%d want %d", got.ID, ids[0])
	}
	// Caller passes already-cleaned title. We still lower-case
	// internally for parity with C#'s `ToLowerInvariant`.
	got2, err := repo.FindByTitle(t.Context(), "ThemAndalorian")
	if err != nil || got2 == nil || got2.ID != ids[0] {
		t.Errorf("FindByTitle case-insensitive: %v %v", got2, err)
	}
}

func TestRepo_FindByTitle_MultipleMatches(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := seedTwo(t, db)
	// Insert a second row with the same CleanTitle.
	if _, err := repo.Insert(t.Context(), &series.Series{
		TvdbID:     12345,
		Title:      "The Mandalorian (UK)",
		CleanTitle: "themandalorian",
		Status:     1,
		Images:     "[]",
		Path:       "/data/tv/Mando UK",
	}); err != nil {
		t.Fatalf("Insert dup-title: %v", err)
	}
	_, err := repo.FindByTitle(t.Context(), "themandalorian")
	if !errors.Is(err, series.ErrMultipleSeriesFound) {
		t.Errorf("FindByTitle(multi): got %v want ErrMultipleSeriesFound", err)
	}
}

func TestRepo_FindByTitleAndYear(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.FindByTitleAndYear(t.Context(), "themandalorian", 2019)
	if err != nil {
		t.Fatalf("FindByTitleAndYear: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("FindByTitleAndYear: id=%d want %d", got.ID, ids[0])
	}
	if _, err := repo.FindByTitleAndYear(t.Context(), "themandalorian", 2020); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("FindByTitleAndYear(wrong year): %v want ErrNotFound", err)
	}
}

func TestRepo_FindByTitleInexact(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	// "themandalorian.1080p.web-dl" contains "themandalorian" → mando matches.
	rows, err := repo.FindByTitleInexact(t.Context(), "themandalorian.1080p.web-dl")
	if err != nil {
		t.Fatalf("FindByTitleInexact: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != ids[0] {
		t.Errorf("FindByTitleInexact: got %d rows %+v", len(rows), rows)
	}
	// Non-substring → no match.
	rows2, err := repo.FindByTitleInexact(t.Context(), "supernatural")
	if err != nil {
		t.Fatalf("FindByTitleInexact(no match): %v", err)
	}
	if len(rows2) != 0 {
		t.Errorf("FindByTitleInexact(no match): %d rows", len(rows2))
	}
}

func TestRepo_SeriesPathExists(t *testing.T) {
	t.Parallel()
	repo, _ := seedTwo(t, datastoretest.OpenSQLite(t))
	ok, err := repo.SeriesPathExists(t.Context(), "/data/tv/Foundation")
	if err != nil || !ok {
		t.Errorf("SeriesPathExists(present): ok=%v err=%v", ok, err)
	}
	ok2, err := repo.SeriesPathExists(t.Context(), "/data/tv/Nope")
	if err != nil || ok2 {
		t.Errorf("SeriesPathExists(missing): ok=%v err=%v", ok2, err)
	}
}

func TestRepo_AllSeriesPaths(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.AllSeriesPaths(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesPaths: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[ids[0]] != "/data/tv/The Mandalorian" || got[ids[1]] != "/data/tv/Foundation" {
		t.Errorf("AllSeriesPaths: %+v", got)
	}
}

func TestRepo_AllSeriesTvdbIDs(t *testing.T) {
	t.Parallel()
	repo, _ := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.AllSeriesTvdbIDs(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesTvdbIDs: %v", err)
	}
	sort.Ints(got)
	want := []int{99999, 361753}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("AllSeriesTvdbIDs: %v want %v", got, want)
	}
}

func TestRepo_AllSeriesTags(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.AllSeriesTags(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesTags: %v", err)
	}
	// mando has [1,2,3]; foundation persists "[]" (empty list, not NULL)
	// because the Bind path emits "[]" rather than NULL when Tags is nil.
	if got[ids[0]] == nil || len(got[ids[0]]) != 3 {
		t.Errorf("mando tags: %v", got[ids[0]])
	}
	// Foundation row should be present (Tags = "[]", not NULL).
	if _, ok := got[ids[1]]; !ok {
		t.Errorf("foundation row missing from AllSeriesTags")
	}
	if len(got[ids[1]]) != 0 {
		t.Errorf("foundation tags should be empty, got %v", got[ids[1]])
	}
}

func TestRepo_AllSeriesQualityProfiles(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	got, err := repo.AllSeriesQualityProfiles(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesQualityProfiles: %v", err)
	}
	if got[ids[0]] != 1 {
		t.Errorf("mando QP=%d want 1", got[ids[0]])
	}
	// foundation has nil QualityProfileID → coerces to 0
	if got[ids[1]] != 0 {
		t.Errorf("foundation QP=%d want 0 (NULL coerce)", got[ids[1]])
	}
}

// Postgres is exercised by the full repo suite; query helpers are
// engine-independent except for FindByTitleInexact (instr vs strpos).
func TestRepo_FindByTitleInexact_Postgres(t *testing.T) {
	repo, ids := seedTwo(t, datastoretest.OpenPostgres(t))
	rows, err := repo.FindByTitleInexact(t.Context(), "themandalorian.1080p")
	if err != nil {
		t.Fatalf("FindByTitleInexact (PG): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != ids[0] {
		t.Errorf("FindByTitleInexact (PG): got %d rows %+v", len(rows), rows)
	}
}
