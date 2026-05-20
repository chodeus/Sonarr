package delayprofiles_test

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/messaging"
)

func newServiceSQLite(t *testing.T) (*delayprofiles.Service, *delayprofiles.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := delayprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return delayprofiles.NewService(r, nil), r
}

func TestService_Add_AssignsOrderAndPersists(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	// Migration 070 already inserted the default profile (Id=1,
	// Order=INT_MAX). Count is 1 at start.
	p1 := &delayprofiles.Profile{
		EnableUsenet:  true,
		EnableTorrent: true,
		Tags:          []int{},
	}
	if _, err := svc.Add(t.Context(), p1); err != nil {
		t.Fatalf("Add p1: %v", err)
	}
	if p1.Order != 1 {
		t.Errorf("first Add: Order = %d, want 1 (Count was 1)", p1.Order)
	}

	p2 := &delayprofiles.Profile{
		EnableUsenet:  true,
		EnableTorrent: false,
		Tags:          []int{},
	}
	if _, err := svc.Add(t.Context(), p2); err != nil {
		t.Fatalf("Add p2: %v", err)
	}
	if p2.Order != 2 {
		t.Errorf("second Add: Order = %d, want 2 (Count was 2)", p2.Order)
	}

	// Round-trip via Repo.
	got, err := repo.Get(t.Context(), p1.ID)
	if err != nil {
		t.Fatalf("Get p1: %v", err)
	}
	if got.Order != 1 || !got.EnableUsenet {
		t.Errorf("post-Add round-trip: %+v", got)
	}
}

func TestService_Add_NilErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)
	if _, err := svc.Add(t.Context(), nil); err == nil {
		t.Error("Add(nil) should error")
	}
}

func TestService_Update_NilErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)
	if err := svc.Update(t.Context(), nil); err == nil {
		t.Error("Update(nil) should error")
	}
}

func TestService_Get_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)
	if _, err := svc.Get(t.Context(), 9999); !errors.Is(err, delayprofiles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_RenumbersSurvivors(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	p1 := &delayprofiles.Profile{Tags: []int{1}}
	p2 := &delayprofiles.Profile{Tags: []int{2}}
	p3 := &delayprofiles.Profile{Tags: []int{3}}
	for _, p := range []*delayprofiles.Profile{p1, p2, p3} {
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	// p1.Order=1, p2.Order=2, p3.Order=3, default(Id=1).Order=INT_MAX.

	// Delete the middle one. Survivors should be renumbered 1..N
	// (skipping the default Id=1, which keeps INT_MAX).
	if err := svc.Delete(t.Context(), p2.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("post-Delete row count = %d, want 3 (default + 2 survivors)", len(all))
	}

	byID := make(map[int]delayprofiles.Profile, len(all))
	for _, p := range all {
		byID[p.ID] = p
	}
	// Default profile keeps INT_MAX.
	if def, ok := byID[1]; !ok || def.Order != 2147483647 {
		t.Errorf("default profile after delete: %+v (want Order=INT_MAX)", def)
	}
	// Survivors are renumbered. The exact value depends on their pre-
	// delete Order rank, but both should be in {1, 2, 3}.
	for _, id := range []int{p1.ID, p3.ID} {
		got, ok := byID[id]
		if !ok {
			t.Errorf("survivor id=%d not found", id)
			continue
		}
		if got.Order < 1 || got.Order > 3 {
			t.Errorf("survivor id=%d: Order = %d, want 1..3", id, got.Order)
		}
	}
}

func TestService_AllForTag(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	a := &delayprofiles.Profile{Tags: []int{1, 2}}
	b := &delayprofiles.Profile{Tags: []int{2, 3}}
	c := &delayprofiles.Profile{Tags: []int{4}}
	for _, p := range []*delayprofiles.Profile{a, b, c} {
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

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

func TestService_AllForTags_IncludesEmptyTags(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	tagged := &delayprofiles.Profile{Tags: []int{42}}
	untagged := &delayprofiles.Profile{Tags: []int{}}
	other := &delayprofiles.Profile{Tags: []int{99}}
	for _, p := range []*delayprofiles.Profile{tagged, untagged, other} {
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	// Default profile (migration-seeded, Id=1) has Tags=[]; it also
	// matches the empty-tags catch-all clause.
	got, err := svc.AllForTags(t.Context(), []int{42})
	if err != nil {
		t.Fatal(err)
	}
	// Expect: tagged (intersect 42), untagged (empty), default (empty).
	if len(got) != 3 {
		t.Errorf("AllForTags([42]): got %d, want 3 (intersect + 2 empty-tag catch-alls)", len(got))
	}
}

func TestService_BestForTags_PicksLowestOrderMatch(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	// Add two profiles tagged with 7. Add returns Order=Count, so
	// p1.Order < p2.Order — p1 should win.
	p1 := &delayprofiles.Profile{Tags: []int{7}, UsenetDelay: 10}
	p2 := &delayprofiles.Profile{Tags: []int{7}, UsenetDelay: 20}
	if _, err := svc.Add(t.Context(), p1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(t.Context(), p2); err != nil {
		t.Fatal(err)
	}

	best, err := svc.BestForTags(t.Context(), []int{7})
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != p1.ID {
		t.Errorf("BestForTags: got id=%d (Order=%d), want id=%d (Order=%d)",
			best.ID, best.Order, p1.ID, p1.Order)
	}
}

func TestService_BestForTags_FallsBackToDefaultProfile(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	// No profiles tagged 99 — only the migration-seeded default
	// profile (Id=1, Tags=[], Order=INT_MAX) matches the empty-tags
	// catch-all.
	best, err := svc.BestForTags(t.Context(), []int{99})
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != 1 {
		t.Errorf("BestForTags: got id=%d, want id=1 (the default)", best.ID)
	}
}

func TestService_BestForTags_CachesAndInvalidates(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	first, err := svc.BestForTags(t.Context(), []int{42})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 1 {
		t.Errorf("initial Best: got id=%d, want default Id=1", first.ID)
	}

	// Insert a tagged profile DIRECTLY via the Repo (bypasses
	// Service mutation hooks → bestByTags should NOT be cleared).
	tagged := &delayprofiles.Profile{Tags: []int{42}, Order: 1}
	if _, err := repo.Insert(t.Context(), tagged); err != nil {
		t.Fatal(err)
	}

	// Second call hits the memo and still returns the default.
	cached, err := svc.BestForTags(t.Context(), []int{42})
	if err != nil {
		t.Fatal(err)
	}
	if cached.ID != 1 {
		t.Errorf("after direct Repo insert, BestForTags should still serve cache (id=1), got id=%d", cached.ID)
	}

	// Now mutate via Service → cache clears.
	if err := svc.Update(t.Context(), tagged); err != nil {
		t.Fatal(err)
	}
	postInvalidation, err := svc.BestForTags(t.Context(), []int{42})
	if err != nil {
		t.Fatal(err)
	}
	if postInvalidation.ID != tagged.ID {
		t.Errorf("after Service Update, expected refreshed Best id=%d, got id=%d", tagged.ID, postInvalidation.ID)
	}
}

func TestService_BestForTags_NoMatch_NotFound(t *testing.T) {
	t.Parallel()
	// Pathological: delete the seed default so no empty-tag profile
	// exists, then query a tag set that nothing matches.
	svc, repo := newServiceSQLite(t)
	if err := repo.Delete(t.Context(), 1); err != nil {
		t.Fatalf("delete default: %v", err)
	}
	if _, err := svc.BestForTags(t.Context(), []int{99}); !errors.Is(err, delayprofiles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Reorder_MovesAndRenumbers(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	p1 := &delayprofiles.Profile{Tags: []int{1}}
	p2 := &delayprofiles.Profile{Tags: []int{2}}
	p3 := &delayprofiles.Profile{Tags: []int{3}}
	for _, p := range []*delayprofiles.Profile{p1, p2, p3} {
		if _, err := svc.Add(t.Context(), p); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	// Initial Order: p1=1, p2=2, p3=3, default(Id=1)=INT_MAX.

	// Move p1 to after p3.
	afterID := p3.ID
	out, err := svc.Reorder(t.Context(), p1.ID, &afterID)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("Reorder return: %d rows, want 4", len(out))
	}

	// Find the survivors and verify p1 is now after p3.
	var orderByID = map[int]int{}
	for _, p := range out {
		orderByID[p.ID] = p.Order
	}
	if orderByID[p1.ID] <= orderByID[p3.ID] {
		t.Errorf("after Reorder(p1 after p3): p1.Order=%d, p3.Order=%d (want p1 > p3)",
			orderByID[p1.ID], orderByID[p3.ID])
	}
	// Default (Id=1) keeps INT_MAX — never reshuffled.
	if orderByID[1] != 2147483647 {
		t.Errorf("default profile Order changed under Reorder: %d", orderByID[1])
	}

	// Reorder return is sorted ascending by Order.
	for i := 1; i < len(out); i++ {
		if out[i].Order < out[i-1].Order {
			t.Errorf("Reorder return not sorted: %+v", out)
			break
		}
	}
}

func TestService_Reorder_UnknownIDIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	p := &delayprofiles.Profile{Tags: []int{1}}
	if _, err := svc.Add(t.Context(), p); err != nil {
		t.Fatal(err)
	}

	pre, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	preOrders := make([]int, len(pre))
	for i, x := range pre {
		preOrders[i] = x.Order
	}

	// Reorder an ID that doesn't exist — should leave Orders unchanged.
	if _, err := svc.Reorder(t.Context(), 9999, nil); err != nil {
		t.Fatalf("Reorder unknown: %v", err)
	}

	post, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	postOrders := make([]int, len(post))
	for i, x := range post {
		postOrders[i] = x.Order
	}
	sort.Ints(preOrders)
	sort.Ints(postOrders)
	if len(preOrders) != len(postOrders) {
		t.Fatalf("row count changed: pre=%d post=%d", len(preOrders), len(postOrders))
	}
	for i := range preOrders {
		if preOrders[i] != postOrders[i] {
			t.Errorf("Order changed under no-op Reorder at i=%d: pre=%d post=%d",
				i, preOrders[i], postOrders[i])
		}
	}
}

// Slice 5.J29 (audit B.2 second wave). All four CUD paths publish
// DelayProfileUpdatedEvent so the SignalR `delayprofile` channel can
// emit Sync. Go-port additive (C# class doesn't emit).
func TestService_PublishesUpdatedEventOnCUD(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := delayprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc := delayprofiles.NewService(r, bus)

	var got atomic.Int32
	messaging.Subscribe(bus, "test.delayprofile.updated",
		func(_ context.Context, _ delayprofiles.DelayProfileUpdatedEvent) error {
			got.Add(1)
			return nil
		})

	// Add.
	p := &delayprofiles.Profile{Tags: []int{1}}
	if _, err := svc.Add(context.Background(), p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Update.
	p.EnableTorrent = true
	if err := svc.Update(context.Background(), p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Reorder (no-op afterID for an existing id).
	if _, err := svc.Reorder(context.Background(), p.ID, nil); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	// Delete.
	if err := svc.Delete(context.Background(), p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got.Load() != 4 {
		t.Errorf("DelayProfileUpdatedEvent count: got %d, want 4 (Add+Update+Reorder+Delete)", got.Load())
	}
}
