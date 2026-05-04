package customfilters_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func newService(t *testing.T) (*customfilters.Service, *customfilters.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := customfilters.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return customfilters.NewService(r), r
}

func TestService_AddRoundTrip(t *testing.T) {
	t.Parallel()
	svc, repo := newService(t)

	f := &customfilters.Filter{
		Type:    "series",
		Label:   "Continuing",
		Filters: `[{"key":"status","value":"continuing"}]`,
	}
	out, err := svc.Add(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == 0 {
		t.Errorf("Add: ID not populated")
	}

	got, err := repo.Get(t.Context(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "series" || got.Label != "Continuing" {
		t.Errorf("post-Add round-trip: %+v", got)
	}
}

func TestService_All(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	for _, label := range []string{"a", "b", "c"} {
		if _, err := svc.Add(t.Context(), &customfilters.Filter{
			Type: "series", Label: label, Filters: `[]`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("All: got %d, want 3", len(all))
	}
}

func TestService_UpdateAndDelete(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)

	f, err := svc.Add(t.Context(), &customfilters.Filter{
		Type: "series", Label: "old", Filters: `[]`,
	})
	if err != nil {
		t.Fatal(err)
	}

	f.Label = "renamed"
	if err := svc.Update(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(t.Context(), f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "renamed" {
		t.Errorf("Update: Label = %q, want renamed", got.Label)
	}

	if err := svc.Delete(t.Context(), f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(t.Context(), f.ID); !errors.Is(err, customfilters.ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
}

func TestService_NilInputErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if _, err := svc.Add(t.Context(), nil); err == nil {
		t.Error("Add(nil) should error")
	}
	if err := svc.Update(t.Context(), nil); err == nil {
		t.Error("Update(nil) should error")
	}
}

func TestService_GetNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if _, err := svc.Get(t.Context(), 9999); !errors.Is(err, customfilters.ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}
