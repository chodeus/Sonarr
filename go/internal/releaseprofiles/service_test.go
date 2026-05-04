package releaseprofiles_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/releaseprofiles"
)

func newService(t *testing.T) *releaseprofiles.Service {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := releaseprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return releaseprofiles.NewService(r)
}

func TestService_AddRoundTrip(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	p := &releaseprofiles.Profile{
		Name:     "anime",
		Enabled:  true,
		Required: []string{"sub"},
		Ignored:  []string{"raw"},
		Tags:     []int{1, 2},
	}
	out, err := svc.Add(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == 0 {
		t.Errorf("Add: ID not populated")
	}

	got, err := svc.Get(t.Context(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "anime" || !got.Enabled || len(got.Required) != 1 {
		t.Errorf("post-Add round-trip: %+v", got)
	}
}

func TestService_AllForTag(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	mustAdd := func(p *releaseprofiles.Profile) {
		t.Helper()
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd(&releaseprofiles.Profile{Name: "a", Tags: []int{1, 2}})
	mustAdd(&releaseprofiles.Profile{Name: "b", Tags: []int{2, 3}})
	mustAdd(&releaseprofiles.Profile{Name: "c", Tags: []int{4}})

	got, err := svc.AllForTag(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("AllForTag(2): got %d, want 2", len(got))
	}

	got, err = svc.AllForTag(t.Context(), 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("AllForTag(unknown): got %d, want 0", len(got))
	}
}

func TestService_AllExcludedForTag(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	mustAdd := func(p *releaseprofiles.Profile) {
		t.Helper()
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd(&releaseprofiles.Profile{Name: "with-1", ExcludedTags: []int{1, 2}})
	mustAdd(&releaseprofiles.Profile{Name: "with-3", ExcludedTags: []int{3}})

	got, err := svc.AllExcludedForTag(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "with-1" {
		t.Errorf("AllExcludedForTag(2): %+v", got)
	}
}

func TestService_AllForTags_IntersectAndEmptyCatchAll(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	mustAdd := func(p *releaseprofiles.Profile) {
		t.Helper()
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	// Profile A: Tags intersect [42] → included (regardless of exclusion).
	mustAdd(&releaseprofiles.Profile{Name: "tagged-42", Tags: []int{42}})
	// Profile B: Empty Tags + non-intersecting ExcludedTags → included.
	mustAdd(&releaseprofiles.Profile{Name: "catch-all-clean", Tags: []int{}, ExcludedTags: []int{99}})
	// Profile C: Empty Tags + intersecting ExcludedTags → excluded.
	mustAdd(&releaseprofiles.Profile{Name: "catch-all-excluded", Tags: []int{}, ExcludedTags: []int{42}})
	// Profile D: Tags don't intersect [42] AND not empty → excluded.
	mustAdd(&releaseprofiles.Profile{Name: "tagged-99", Tags: []int{99}})

	got, err := svc.AllForTags(t.Context(), []int{42})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range got {
		names[p.Name] = true
	}
	if !names["tagged-42"] {
		t.Error("expected tagged-42 in AllForTags([42])")
	}
	if !names["catch-all-clean"] {
		t.Error("expected catch-all-clean in AllForTags([42])")
	}
	if names["catch-all-excluded"] {
		t.Error("catch-all-excluded should be filtered (its ExcludedTags intersects)")
	}
	if names["tagged-99"] {
		t.Error("tagged-99 should be filtered (Tags don't intersect, not empty)")
	}
}

func TestService_AllForTags_PreservesC_OperatorPrecedenceQuirk(t *testing.T) {
	t.Parallel()
	// The C# expression's operator precedence parses as
	//   A || (B && C)
	// where A = Tags intersect, B = Tags empty, C = ExcludedTags doesn't intersect.
	// So a profile whose Tags INTERSECTS but whose ExcludedTags ALSO intersects
	// is INCLUDED. This is likely a latent C# bug; the Go port preserves it.
	svc := newService(t)

	if _, err := svc.Add(t.Context(), &releaseprofiles.Profile{
		Name:         "intersect-and-excluded",
		Tags:         []int{42},
		ExcludedTags: []int{42},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.AllForTags(t.Context(), []int{42})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "intersect-and-excluded" {
		t.Errorf("operator-precedence quirk: got %+v, want intersect-and-excluded included", got)
	}
}

func TestService_EnabledForTags(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	mustAdd := func(p *releaseprofiles.Profile) {
		t.Helper()
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	// Enabled, indexer 5: matches.
	mustAdd(&releaseprofiles.Profile{Name: "enabled-i5", Enabled: true, Tags: []int{42}, IndexerIDs: []int{5}})
	// Disabled: filtered.
	mustAdd(&releaseprofiles.Profile{Name: "disabled", Enabled: false, Tags: []int{42}, IndexerIDs: []int{5}})
	// Enabled, IndexerIDs empty (catch-all): matches any indexer.
	mustAdd(&releaseprofiles.Profile{Name: "enabled-all-indexers", Enabled: true, Tags: []int{42}, IndexerIDs: []int{}})
	// Enabled, indexer 9: doesn't match indexer 5.
	mustAdd(&releaseprofiles.Profile{Name: "enabled-i9", Enabled: true, Tags: []int{42}, IndexerIDs: []int{9}})

	got, err := svc.EnabledForTags(t.Context(), []int{42}, 5)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range got {
		names[p.Name] = true
	}
	if !names["enabled-i5"] {
		t.Error("expected enabled-i5")
	}
	if !names["enabled-all-indexers"] {
		t.Error("expected enabled-all-indexers (empty IndexerIDs is catch-all)")
	}
	if names["disabled"] {
		t.Error("disabled should be filtered")
	}
	if names["enabled-i9"] {
		t.Error("enabled-i9 should be filtered (different indexer)")
	}
}

func TestService_UpdateAndDelete(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	p, err := svc.Add(t.Context(), &releaseprofiles.Profile{Name: "old"})
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "new"
	if err := svc.Update(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(t.Context(), p.ID)
	if got.Name != "new" {
		t.Errorf("Update: Name = %q, want new", got.Name)
	}

	if err := svc.Delete(t.Context(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(t.Context(), p.ID); !errors.Is(err, releaseprofiles.ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
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
