package series_test

import (
	"errors"
	"sort"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/series"
)

// Compile-time assertion: *series.Service satisfies the
// rootfolders.SeriesPaths interface. This is the bridge point that
// lets host wiring replace the RootFolderService stub.
var _ rootfolders.SeriesPaths = (*series.Service)(nil)

func newServiceWithFixtures(t *testing.T, db *datastore.DB) (*series.Service, [2]int) {
	t.Helper()
	repo, ids := seedTwo(t, db)
	svc, err := series.NewService(repo, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, ids
}

func TestService_GetSeries(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.GetSeries(t.Context(), ids[0])
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.TvdbID != 361753 {
		t.Errorf("GetSeries: TvdbID=%d", got.TvdbID)
	}
	if _, err := svc.GetSeries(t.Context(), 999999); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("GetSeries(missing): %v", err)
	}
}

func TestService_GetMany_DropsMissingIDs(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.GetMany(t.Context(), []int{ids[0], 99999, ids[1]})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("GetMany: len=%d want 2", len(got))
	}
}

func TestService_GetAllSeries(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.GetAllSeries(t.Context())
	if err != nil {
		t.Fatalf("GetAllSeries: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("GetAllSeries: len=%d want 2", len(got))
	}
}

// FindByTitle applies parser.CleanSeriesTitle to the input — passing the
// release-style "The Mandalorian" surfaces the persisted "themandalorian".
func TestService_FindByTitle_AppliesCleanSeriesTitle(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.FindByTitle(t.Context(), "The Mandalorian")
	if err != nil {
		t.Fatalf("FindByTitle: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("FindByTitle: id=%d want %d", got.ID, ids[0])
	}
}

func TestService_FindByTitleAndYear(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.FindByTitleAndYear(t.Context(), "The Mandalorian", 2019)
	if err != nil {
		t.Fatalf("FindByTitleAndYear: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("id=%d want %d", got.ID, ids[0])
	}
	if _, err := svc.FindByTitleAndYear(t.Context(), "The Mandalorian", 2020); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("FindByTitleAndYear(wrong year): %v", err)
	}
}

func TestService_FindByPath(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.FindByPath(t.Context(), "/data/tv/Foundation")
	if err != nil {
		t.Fatalf("FindByPath: %v", err)
	}
	if got.ID != ids[1] {
		t.Errorf("id=%d want %d", got.ID, ids[1])
	}
}

func TestService_FindByImdbID(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.FindByImdbID(t.Context(), "tt8111088")
	if err != nil {
		t.Fatalf("FindByImdbID: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("id=%d want %d", got.ID, ids[0])
	}
}

// FindByTitleInexact: 0 candidates → ErrNotFound; 1 candidate → it;
// many candidates → leftmost-position-then-longest wins.
func TestService_FindByTitleInexact_NoMatch(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	if _, err := svc.FindByTitleInexact(t.Context(), "Supernatural S15E20 1080p"); !errors.Is(err, series.ErrNotFound) {
		t.Errorf("FindByTitleInexact(no match): %v want ErrNotFound", err)
	}
}

func TestService_FindByTitleInexact_SingleCandidate(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.FindByTitleInexact(t.Context(), "The Mandalorian S01E01 1080p WEB-DL")
	if err != nil {
		t.Fatalf("FindByTitleInexact: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("FindByTitleInexact: id=%d want %d", got.ID, ids[0])
	}
}

// Multiple candidates: insert two series whose CleanTitles are both
// substrings of the input. The longer one (`themandalorianbookofboba`)
// is positioned at index 0, so it wins outright. If we use an input
// where both start at position 0 (i.e. one is a prefix of the other)
// the "longer wins" tie-break activates.
func TestService_FindByTitleInexact_LongestLeftmostWins(t *testing.T) {
	t.Parallel()
	repo, ids := seedTwo(t, datastoretest.OpenSQLite(t))
	// "the" CleanTitle is a substring of "themandalorian"; both match
	// the input at position 0. Longest should win.
	if _, err := repo.Insert(t.Context(), &series.Series{
		TvdbID:     2,
		Title:      "The",
		CleanTitle: "the",
		Status:     1,
		Images:     "[]",
		Path:       "/data/tv/The",
	}); err != nil {
		t.Fatalf("Insert short: %v", err)
	}
	svc, err := series.NewService(repo, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.FindByTitleInexact(t.Context(), "themandalorian.season.1.1080p")
	if err != nil {
		t.Fatalf("FindByTitleInexact: %v", err)
	}
	if got.ID != ids[0] {
		t.Errorf("longest leftmost: got id=%d (%q) want id=%d (themandalorian)", got.ID, got.CleanTitle, ids[0])
	}
}

func TestService_AllSeriesPaths_MatchesRootFolderInterface(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	paths, err := svc.AllSeriesPaths(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesPaths: %v", err)
	}
	if paths[ids[0]] != "/data/tv/The Mandalorian" || paths[ids[1]] != "/data/tv/Foundation" {
		t.Errorf("AllSeriesPaths: %+v", paths)
	}
}

func TestService_AllSeriesTvdbIDs(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.AllSeriesTvdbIDs(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesTvdbIDs: %v", err)
	}
	sort.Ints(got)
	if len(got) != 2 || got[0] != 99999 || got[1] != 361753 {
		t.Errorf("AllSeriesTvdbIDs: %v", got)
	}
}

func TestService_AllSeriesQualityProfiles(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.AllSeriesQualityProfiles(t.Context())
	if err != nil {
		t.Fatalf("AllSeriesQualityProfiles: %v", err)
	}
	if got[ids[0]] != 1 || got[ids[1]] != 0 {
		t.Errorf("AllSeriesQualityProfiles: %+v", got)
	}
}

func TestService_AllForTag(t *testing.T) {
	t.Parallel()
	svc, ids := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	got, err := svc.AllForTag(t.Context(), 2)
	if err != nil {
		t.Fatalf("AllForTag: %v", err)
	}
	if len(got) != 1 || got[0].ID != ids[0] {
		t.Errorf("AllForTag(2): %v want [mando]", got)
	}
	got2, err := svc.AllForTag(t.Context(), 999)
	if err != nil {
		t.Fatalf("AllForTag(999): %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("AllForTag(999): %v want empty", got2)
	}
}

func TestService_SeriesPathExists(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	ok, err := svc.SeriesPathExists(t.Context(), "/data/tv/Foundation")
	if err != nil || !ok {
		t.Errorf("SeriesPathExists(present): ok=%v err=%v", ok, err)
	}
	ok2, err := svc.SeriesPathExists(t.Context(), "/data/tv/Nope")
	if err != nil || ok2 {
		t.Errorf("SeriesPathExists(missing): ok=%v err=%v", ok2, err)
	}
}
