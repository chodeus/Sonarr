package scenemappings_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/scenemappings"
)

// fakeReader / fakeWriter satisfy MappingsReader + MappingsWriter
// for in-memory testing of UpdateMappings without a real DB.
type fakeStore struct {
	mu       sync.Mutex
	rows     []scenemappings.Mapping
	nextID   int
	getCalls int

	insertCalls []scenemappings.Mapping
	updateCalls []scenemappings.Mapping
	deleteCalls []int

	getErr    error
	insertErr error
	updateErr error
	deleteErr error
}

func newFakeStore(seed ...scenemappings.Mapping) *fakeStore {
	f := &fakeStore{}
	for _, s := range seed {
		f.nextID++
		s.ID = f.nextID
		f.rows = append(f.rows, s)
	}
	return f
}

func (f *fakeStore) All(_ context.Context) ([]scenemappings.Mapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]scenemappings.Mapping, len(f.rows))
	copy(cp, f.rows)
	return cp, nil
}

func (f *fakeStore) GetAllByType(_ context.Context, typ string) ([]scenemappings.Mapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make([]scenemappings.Mapping, 0)
	for _, r := range f.rows {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) InsertMany(_ context.Context, mappings []scenemappings.Mapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return f.insertErr
	}
	f.insertCalls = append(f.insertCalls, mappings...)
	for i := range mappings {
		f.nextID++
		mappings[i].ID = f.nextID
		f.rows = append(f.rows, mappings[i])
	}
	return nil
}

func (f *fakeStore) UpdateMany(_ context.Context, mappings []scenemappings.Mapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updateCalls = append(f.updateCalls, mappings...)
	for _, m := range mappings {
		for i := range f.rows {
			if f.rows[i].ID == m.ID {
				f.rows[i] = m
				break
			}
		}
	}
	return nil
}

func (f *fakeStore) DeleteByIDs(_ context.Context, ids []int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, ids...)
	keep := f.rows[:0]
	for _, r := range f.rows {
		drop := false
		for _, id := range ids {
			if r.ID == id {
				drop = true
				break
			}
		}
		if !drop {
			keep = append(keep, r)
		}
	}
	f.rows = keep
	return nil
}

// stubProvider returns the configured mappings + error on each call.
type stubProvider struct {
	mappings []scenemappings.Mapping
	err      error
	calls    int
}

func (s *stubProvider) GetSceneMappings(_ context.Context) ([]scenemappings.Mapping, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	cp := make([]scenemappings.Mapping, len(s.mappings))
	copy(cp, s.mappings)
	return cp, nil
}

func makeSvc(t *testing.T, store *fakeStore, providers []scenemappings.Provider, bus *messaging.Bus) *scenemappings.Service {
	t.Helper()
	svc := scenemappings.NewService(store, slog.Default()).
		WithWriter(store).
		WithProviders(providers...).
		WithBus(bus)
	if svc == nil {
		t.Fatalf("NewService returned nil")
	}
	return svc
}

// TestUpdateMappings_NoWriter_ReturnsError covers the
// pre-WithWriter guard.
func TestUpdateMappings_NoWriter_ReturnsError(t *testing.T) {
	store := newFakeStore()
	svc := scenemappings.NewService(store, slog.Default()).
		WithProviders(&stubProvider{}) // no WithWriter
	if err := svc.UpdateMappings(t.Context()); err == nil {
		t.Errorf("err = nil, want writer-not-wired error")
	}
}

// TestUpdateMappings_NoProviders_NoOpReturnsNil covers the empty-
// providers degenerate path. Not an error — production wiring may
// legitimately skip providers.
func TestUpdateMappings_NoProviders_NoOpReturnsNil(t *testing.T) {
	store := newFakeStore()
	svc := makeSvc(t, store, nil, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(store.insertCalls) != 0 || len(store.updateCalls) != 0 || len(store.deleteCalls) != 0 {
		t.Errorf("store mutated despite no providers: ins=%d upd=%d del=%d",
			len(store.insertCalls), len(store.updateCalls), len(store.deleteCalls))
	}
}

// TestUpdateMappings_HappyPath_InsertsNewMappings exercises the
// fresh-install flow: empty store + one provider returning N rows →
// all N rows inserted.
func TestUpdateMappings_HappyPath_InsertsNewMappings(t *testing.T) {
	store := newFakeStore()
	prov := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "a", Title: "Alpha", SearchTerm: "alpha"},
			{MappingID: "b", Title: "Beta", SearchTerm: "beta"},
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{prov}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	if len(store.insertCalls) != 2 {
		t.Errorf("inserts = %d, want 2", len(store.insertCalls))
	}
	if len(store.updateCalls) != 0 || len(store.deleteCalls) != 0 {
		t.Errorf("expected only inserts; got upd=%d del=%d",
			len(store.updateCalls), len(store.deleteCalls))
	}
	// Verify ParseTerm + Type were stamped.
	for _, m := range store.insertCalls {
		if m.ParseTerm == "" {
			t.Errorf("ParseTerm not stamped on %q", m.Title)
		}
		if m.Type != "stubProvider" {
			t.Errorf("Type = %q, want stubProvider (from providerTypeName)", m.Type)
		}
	}
}

// TestUpdateMappings_FiltersInvalidRows covers the empty-Title /
// empty-SearchTerm filter (C# :148-158).
func TestUpdateMappings_FiltersInvalidRows(t *testing.T) {
	store := newFakeStore()
	prov := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "a", Title: "Alpha", SearchTerm: "alpha"}, // valid
			{MappingID: "b", Title: "", SearchTerm: "beta"},       // invalid: no Title
			{MappingID: "c", Title: "Charlie", SearchTerm: " "},   // invalid: blank SearchTerm
			{MappingID: "d", Title: "Delta", SearchTerm: "delta"}, // valid
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{prov}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	if len(store.insertCalls) != 2 {
		t.Errorf("inserts = %d, want 2 (Alpha + Delta); invalid rows should be filtered", len(store.insertCalls))
	}
}

// TestUpdateMappings_DiffByMappingID covers the C# :167-197 partition
// (insert / update / delete). Existing has [a, b, c]; provider
// returns [a (updated), b (same), d (new)] — should update a + b,
// insert d, delete c.
func TestUpdateMappings_DiffByMappingID(t *testing.T) {
	store := newFakeStore(
		scenemappings.Mapping{MappingID: "a", Title: "Alpha-OLD", SearchTerm: "alpha-old", Type: "stubProvider"},
		scenemappings.Mapping{MappingID: "b", Title: "Beta", SearchTerm: "beta", Type: "stubProvider"},
		scenemappings.Mapping{MappingID: "c", Title: "Charlie", SearchTerm: "charlie", Type: "stubProvider"},
	)
	prov := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "a", Title: "Alpha-NEW", SearchTerm: "alpha-new"},
			{MappingID: "b", Title: "Beta", SearchTerm: "beta"},
			{MappingID: "d", Title: "Delta", SearchTerm: "delta"},
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{prov}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	if len(store.insertCalls) != 1 || store.insertCalls[0].MappingID != "d" {
		t.Errorf("inserts = %+v, want [Delta]", store.insertCalls)
	}
	if len(store.updateCalls) != 2 {
		t.Errorf("updates = %d, want 2 (Alpha + Beta)", len(store.updateCalls))
	}
	if len(store.deleteCalls) != 1 {
		t.Errorf("deletes = %v, want 1 (Charlie's Id)", store.deleteCalls)
	}
}

// TestUpdateMappings_SkipsRowsWithEmptyMappingID covers C# :179-183.
func TestUpdateMappings_SkipsRowsWithEmptyMappingID(t *testing.T) {
	store := newFakeStore()
	prov := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "a", Title: "Alpha", SearchTerm: "alpha"},
			{MappingID: "", Title: "NoMappingID", SearchTerm: "x"},
			{MappingID: "  ", Title: "WhitespaceMappingID", SearchTerm: "y"},
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{prov}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	if len(store.insertCalls) != 1 || store.insertCalls[0].MappingID != "a" {
		t.Errorf("inserts = %+v, want only [Alpha]", store.insertCalls)
	}
}

// TestUpdateMappings_EmptyProviderResult_DoesNotDelete covers C#
// :204-206 — an empty fetch is transient; existing rows must NOT be
// deleted.
func TestUpdateMappings_EmptyProviderResult_DoesNotDelete(t *testing.T) {
	store := newFakeStore(
		scenemappings.Mapping{MappingID: "a", Title: "Alpha", SearchTerm: "alpha", Type: "stubProvider"},
	)
	prov := &stubProvider{mappings: nil} // empty fetch
	svc := makeSvc(t, store, []scenemappings.Provider{prov}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	if len(store.deleteCalls) != 0 || len(store.insertCalls) != 0 || len(store.updateCalls) != 0 {
		t.Errorf("expected no mutations on empty fetch; got ins=%d upd=%d del=%v",
			len(store.insertCalls), len(store.updateCalls), store.deleteCalls)
	}
}

// TestUpdateMappings_ProviderError_DoesNotFailLoop covers C#
// :208-211 — a per-provider error is logged + skipped; other
// providers continue.
func TestUpdateMappings_ProviderError_DoesNotFailLoop(t *testing.T) {
	store := newFakeStore()
	badProv := &stubProvider{err: errors.New("upstream 500")}
	goodProv := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "x", Title: "X", SearchTerm: "x"},
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{badProv, goodProv}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Errorf("err = %v, want nil (provider error should be swallowed)", err)
	}
	if len(store.insertCalls) != 1 || store.insertCalls[0].MappingID != "x" {
		t.Errorf("inserts = %+v, want [x] from the surviving provider", store.insertCalls)
	}
	if badProv.calls != 1 || goodProv.calls != 1 {
		t.Errorf("each provider should be called once: bad=%d good=%d",
			badProv.calls, goodProv.calls)
	}
}

// TestUpdateMappings_PublishesEvent covers the C# :216 event publish.
func TestUpdateMappings_PublishesEvent(t *testing.T) {
	store := newFakeStore()
	prov := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "a", Title: "Alpha", SearchTerm: "alpha"},
		},
	}
	bus := messaging.New(slog.Default())
	var fired int32
	var mu sync.Mutex
	messaging.Subscribe(bus, "test-event-receiver", func(_ context.Context, _ scenemappings.UpdatedEvent) error {
		mu.Lock()
		fired++
		mu.Unlock()
		return nil
	})

	svc := makeSvc(t, store, []scenemappings.Provider{prov}, bus)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	mu.Lock()
	got := fired
	mu.Unlock()
	if got != 1 {
		t.Errorf("UpdatedEvent fired %d times, want 1", got)
	}
}

// TestUpdateMappings_PartitionsByProviderType verifies that two
// providers with different type names operate independently — each
// reads GetAllByType for its own provider, and a mapping deleted by
// provider A doesn't leak across to provider B.
func TestUpdateMappings_PartitionsByProviderType(t *testing.T) {
	store := newFakeStore(
		scenemappings.Mapping{MappingID: "a", Title: "Alpha", SearchTerm: "alpha", Type: "stubProvider"},
		scenemappings.Mapping{MappingID: "z", Title: "Zeta", SearchTerm: "zeta", Type: "otherProvider"},
	)
	provA := &stubProvider{
		mappings: []scenemappings.Mapping{
			// New row, no overlap with the existing "z" from the other type.
			{MappingID: "a2", Title: "Alpha2", SearchTerm: "alpha2"},
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{provA}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	// Existing "a" (stubProvider type) should be deleted (not in fetch).
	// Existing "z" (otherProvider type) should be untouched.
	if len(store.deleteCalls) != 1 {
		t.Errorf("deletes = %v, want 1 (the stubProvider 'a' row)", store.deleteCalls)
	}
	// Verify "z" still in store.
	all, _ := store.All(context.Background())
	foundZ := false
	for _, m := range all {
		if m.MappingID == "z" {
			foundZ = true
			break
		}
	}
	if !foundZ {
		t.Errorf("otherProvider row 'z' was deleted; expected provider-type partitioning to protect it")
	}
}

// TestUpdateMappings_RefreshesCache_AfterMutation covers the C# :214
// RefreshCache call — verifies the in-memory cache reflects post-
// update state via a subsequent FindByTvdbID lookup.
func TestUpdateMappings_RefreshesCache_AfterMutation(t *testing.T) {
	store := newFakeStore()
	prov := &stubProvider{
		mappings: []scenemappings.Mapping{
			{MappingID: "a", Title: "Alpha", SearchTerm: "alpha", TvdbID: 81189},
		},
	}
	svc := makeSvc(t, store, []scenemappings.Provider{prov}, nil)
	if err := svc.UpdateMappings(t.Context()); err != nil {
		t.Fatalf("UpdateMappings: %v", err)
	}
	got, err := svc.FindByTvdbID(t.Context(), 81189)
	if err != nil {
		t.Fatalf("FindByTvdbID: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Alpha" {
		t.Errorf("post-refresh cache lookup = %+v, want [Alpha]", got)
	}
}
