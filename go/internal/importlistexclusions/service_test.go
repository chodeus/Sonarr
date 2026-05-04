package importlistexclusions_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importlistexclusions"
)

func newService(t *testing.T) *importlistexclusions.Service {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := importlistexclusions.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return importlistexclusions.NewService(r)
}

func TestService_AddAndFindByTvdbID(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	out, err := svc.Add(t.Context(), &importlistexclusions.Exclusion{
		TvdbID: 12345, Title: "Old Show",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == 0 {
		t.Errorf("Add: ID not populated")
	}

	got, err := svc.FindByTvdbID(t.Context(), 12345)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != out.ID || got.Title != "Old Show" {
		t.Errorf("FindByTvdbID round-trip: %+v", got)
	}

	if _, err := svc.FindByTvdbID(t.Context(), 99999); !errors.Is(err, importlistexclusions.ErrNotFound) {
		t.Errorf("FindByTvdbID(unknown) = %v, want ErrNotFound", err)
	}
}

func TestService_All_OrderedByTitle(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	for i, title := range []string{"Charlie", "Alpha", "Bravo"} {
		if _, err := svc.Add(t.Context(), &importlistexclusions.Exclusion{
			TvdbID: 100 + i, Title: title,
		}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("All: got %d, want 3", len(all))
	}
	want := []string{"Alpha", "Bravo", "Charlie"}
	for i, e := range all {
		if e.Title != want[i] {
			t.Errorf("All[%d].Title = %q, want %q (mapper.OrderBy=Title)", i, e.Title, want[i])
		}
	}
}

func TestService_Update(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	e, err := svc.Add(t.Context(), &importlistexclusions.Exclusion{TvdbID: 1, Title: "old"})
	if err != nil {
		t.Fatal(err)
	}
	e.Title = "new"
	if err := svc.Update(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(t.Context(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new" {
		t.Errorf("Update: Title = %q, want new", got.Title)
	}
}

func TestService_DeleteMany_TolerantToMissing(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	// Add two; delete-many across a real id, a missing id, and the other real id.
	a, _ := svc.Add(t.Context(), &importlistexclusions.Exclusion{TvdbID: 1, Title: "A"})
	b, _ := svc.Add(t.Context(), &importlistexclusions.Exclusion{TvdbID: 2, Title: "B"})
	if err := svc.DeleteMany(t.Context(), []int{a.ID, 99999, b.ID}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("post-DeleteMany: got %d rows, want 0", len(all))
	}
}

func TestService_NilInputErrors(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if _, err := svc.Add(t.Context(), nil); err == nil {
		t.Error("Add(nil) should error")
	}
	if err := svc.Update(t.Context(), nil); err == nil {
		t.Error("Update(nil) should error")
	}
}

func TestService_Get_NotFound(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if _, err := svc.Get(t.Context(), 9999); !errors.Is(err, importlistexclusions.ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}
