package tagdetail

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func staticEntities(es ...Entity) EntityLoader {
	return func(context.Context) ([]Entity, error) { return es, nil }
}

func buildService() *Service {
	return NewService(Loaders{
		Tags: func(context.Context) ([]Tag, error) {
			// Already label-sorted by the caller (tagsSvc.All).
			return []Tag{{ID: 1, Label: "favorites"}, {ID: 2, Label: "hd"}, {ID: 3, Label: "unused"}}, nil
		},
		DelayProfiles:   staticEntities(Entity{ID: 10, Tags: []int{1}}, Entity{ID: 11, Tags: []int{2}}),
		ImportLists:     staticEntities(Entity{ID: 20, Tags: []int{1, 2}}),
		Notifications:   staticEntities(Entity{ID: 30, Tags: []int{2}}),
		ReleaseProfiles: staticEntities(Entity{ID: 40, Tags: []int{1}}),
		// Excluded profiles read ExcludedTags (mapped to Entity.Tags by caller).
		ExcludedReleaseProfiles: staticEntities(Entity{ID: 40, Tags: []int{2}}),
		Series:                  staticEntities(Entity{ID: 50, Tags: []int{1}}, Entity{ID: 51, Tags: []int{1}}),
		Indexers:                staticEntities(Entity{ID: 60, Tags: []int{2}}),
		// AutoTag 70 references tag 1 via a folded TagSpec value (caller folds).
		AutoTags:        staticEntities(Entity{ID: 70, Tags: []int{1}}),
		DownloadClients: staticEntities(Entity{ID: 80, Tags: []int{2}}),
	})
}

func TestAllTagDetails_JoinsAndOrders(t *testing.T) {
	t.Parallel()
	got, err := buildService().AllTagDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d details, want 3", len(got))
	}
	// Order matches the tags loader (label-sorted): favorites, hd, unused.
	if got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("order: %d,%d,%d", got[0].ID, got[1].ID, got[2].ID)
	}

	fav := got[0]
	if !reflect.DeepEqual(fav.DelayProfileIDs, []int{10}) {
		t.Errorf("fav delayProfiles = %v want [10]", fav.DelayProfileIDs)
	}
	if !reflect.DeepEqual(fav.ImportListIDs, []int{20}) {
		t.Errorf("fav importLists = %v want [20]", fav.ImportListIDs)
	}
	if !reflect.DeepEqual(fav.RestrictionIDs, []int{40}) {
		t.Errorf("fav restriction = %v want [40]", fav.RestrictionIDs)
	}
	if !reflect.DeepEqual(fav.SeriesIDs, []int{50, 51}) {
		t.Errorf("fav series = %v want [50 51]", fav.SeriesIDs)
	}
	if !reflect.DeepEqual(fav.AutoTagIDs, []int{70}) {
		t.Errorf("fav autoTags = %v want [70]", fav.AutoTagIDs)
	}
	// favorites is NOT excluded anywhere.
	if len(fav.ExcludedReleaseProfileIDs) != 0 {
		t.Errorf("fav excluded = %v want []", fav.ExcludedReleaseProfileIDs)
	}

	hd := got[1]
	if !reflect.DeepEqual(hd.ExcludedReleaseProfileIDs, []int{40}) {
		t.Errorf("hd excluded = %v want [40]", hd.ExcludedReleaseProfileIDs)
	}
	if !reflect.DeepEqual(hd.NotificationIDs, []int{30}) {
		t.Errorf("hd notifications = %v want [30]", hd.NotificationIDs)
	}
	if !reflect.DeepEqual(hd.IndexerIDs, []int{60}) {
		t.Errorf("hd indexers = %v want [60]", hd.IndexerIDs)
	}
	if !reflect.DeepEqual(hd.DownloadClientIDs, []int{80}) {
		t.Errorf("hd downloadClients = %v want [80]", hd.DownloadClientIDs)
	}

	// Unused tag: every slice empty + non-nil (emits []).
	unused := got[2]
	for name, s := range map[string][]int{
		"delay": unused.DelayProfileIDs, "import": unused.ImportListIDs,
		"notif": unused.NotificationIDs, "restrict": unused.RestrictionIDs,
		"excluded": unused.ExcludedReleaseProfileIDs, "indexer": unused.IndexerIDs,
		"dlclient": unused.DownloadClientIDs, "autotag": unused.AutoTagIDs,
		"series": unused.SeriesIDs,
	} {
		if s == nil {
			t.Errorf("unused.%s is nil, want non-nil empty", name)
		}
		if len(s) != 0 {
			t.Errorf("unused.%s = %v want []", name, s)
		}
	}
}

func TestTagDetail_ByID(t *testing.T) {
	t.Parallel()
	d, ok, err := buildService().TagDetail(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("tag 2 not found")
	}
	if d.Label != "hd" {
		t.Errorf("label = %q want hd", d.Label)
	}
	if !reflect.DeepEqual(d.IndexerIDs, []int{60}) {
		t.Errorf("indexers = %v want [60]", d.IndexerIDs)
	}
}

func TestTagDetail_NotFound(t *testing.T) {
	t.Parallel()
	_, ok, err := buildService().TagDetail(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected not found for id 999")
	}
}

func TestAllTagDetails_EmptyDB(t *testing.T) {
	t.Parallel()
	svc := NewService(Loaders{
		Tags: func(context.Context) ([]Tag, error) { return nil, nil },
	})
	got, err := svc.AllTagDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d, want 0 (empty DB)", len(got))
	}
}

func TestAllTagDetails_LoaderErrorPropagates(t *testing.T) {
	t.Parallel()
	svc := NewService(Loaders{
		Tags:          func(context.Context) ([]Tag, error) { return []Tag{{ID: 1}}, nil },
		DelayProfiles: func(context.Context) ([]Entity, error) { return nil, errors.New("db down") },
	})
	if _, err := svc.AllTagDetails(context.Background()); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestNilLoaders_ContributeNothing(t *testing.T) {
	t.Parallel()
	// Only Tags set; all consumer loaders nil.
	svc := NewService(Loaders{
		Tags: func(context.Context) ([]Tag, error) { return []Tag{{ID: 1, Label: "x"}}, nil },
	})
	got, err := svc.AllTagDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].SeriesIDs) != 0 {
		t.Fatalf("unexpected: %+v", got)
	}
}
