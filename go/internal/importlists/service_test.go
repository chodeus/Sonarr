package importlists_test

import (
	"sort"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// Compile-time assertion: *importlists.Service satisfies the
// qualityprofiles.ImportListProfileChecker bridge.
var _ qualityprofiles.ImportListProfileChecker = (*importlists.Service)(nil)

func newTestService(t *testing.T) (*importlists.Service, *importlists.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := importlists.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := importlists.NewService(repo)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo
}

func sampleImportList(name string, qualityProfileID int) *importlists.ImportList {
	return &importlists.ImportList{
		Name:                     name,
		Implementation:           "PlexImport",
		RootFolderPath:           "/data/tv",
		ShouldMonitor:            1,
		QualityProfileID:         qualityProfileID,
		SeriesType:               0,
		SeasonFolder:             true,
		SearchForMissingEpisodes: true,
		MonitorNewItems:          0,
	}
}

func TestService_AllQualityProfileIDs_Empty(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	got, err := svc.AllQualityProfileIDs(t.Context())
	if err != nil {
		t.Fatalf("AllQualityProfileIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestService_AllQualityProfileIDs_ReturnsEachRow(t *testing.T) {
	t.Parallel()
	svc, repo := newTestService(t)
	for _, qp := range []int{1, 2, 1, 3} {
		if _, err := repo.Insert(t.Context(), sampleImportList(uniqueName(qp), qp)); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	got, err := svc.AllQualityProfileIDs(t.Context())
	if err != nil {
		t.Fatalf("AllQualityProfileIDs: %v", err)
	}
	sort.Ints(got)
	want := []int{1, 1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d (%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("[%d]=%d want %d (full=%v)", i, got[i], v, got)
		}
	}
}

// Counter to give each fixture a unique Name (UNIQUE column).
var nameCounter int

func uniqueName(qp int) string {
	nameCounter++
	return "list-" + uniqueSuffix(nameCounter, qp)
}

func uniqueSuffix(i, qp int) string {
	return string(rune('a'+i)) + "-qp" + string(rune('0'+qp))
}

func TestService_All_RoundTripsRows(t *testing.T) {
	t.Parallel()
	svc, repo := newTestService(t)
	if _, err := repo.Insert(t.Context(), sampleImportList("solo", 7)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len=%d want 1", len(all))
	}
	if all[0].Name != "solo" || all[0].QualityProfileID != 7 {
		t.Errorf("round-trip: %+v", all[0])
	}
}
