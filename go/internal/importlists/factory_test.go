package importlists_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importliststatus"
)

// stubFactoryRepo implements [importlists.factoryRepo] structurally
// (we can't reference the unexported interface from the _test
// package, but the concrete signature is reachable through the
// constructor's parameter list). It carries a fixed slice for
// deterministic ordering.
type stubFactoryRepo struct {
	rows []importlists.ImportList
	err  error
}

func (s *stubFactoryRepo) All(_ context.Context) ([]importlists.ImportList, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

// stubFactoryStatus implements [importlists.factoryStatus]. Carries
// a fixed blocked-list + a per-row record-counter so tests can
// assert what the Factory invoked.
type stubFactoryStatus struct {
	blocked       []importliststatus.Status
	blockedErr    error
	recordSuccess map[int]int
	recordFailure map[int]int
	recordErr     error
}

func newStubFactoryStatus() *stubFactoryStatus {
	return &stubFactoryStatus{
		recordSuccess: map[int]int{},
		recordFailure: map[int]int{},
	}
}

func (s *stubFactoryStatus) GetBlockedProviders(_ context.Context) ([]importliststatus.Status, error) {
	return s.blocked, s.blockedErr
}

func (s *stubFactoryStatus) RecordSuccess(_ context.Context, id int) error {
	s.recordSuccess[id]++
	return s.recordErr
}

func (s *stubFactoryStatus) RecordFailure(_ context.Context, id int, _ time.Duration) error {
	s.recordFailure[id]++
	return s.recordErr
}

// stubProvider implements [importlists.Provider] with controllable
// Test behaviour. Used so Factory.Test can be exercised without
// pulling in a concrete provider package.
type factoryStubProvider struct {
	def         *importlists.ImportList
	testReturns []importlists.TestFailure
	testErr     error
}

func (p *factoryStubProvider) Name() string                        { return "Stub" }
func (p *factoryStubProvider) Definition() *importlists.ImportList { return p.def }
func (p *factoryStubProvider) ListType() importlists.ListType      { return importlists.ListTypeOther }
func (p *factoryStubProvider) MinRefreshInterval() time.Duration   { return 6 * time.Hour }
func (p *factoryStubProvider) Fetch(context.Context) (importlists.FetchResult, error) {
	return importlists.FetchResult{}, nil
}

func (p *factoryStubProvider) Test(context.Context) ([]importlists.TestFailure, error) {
	return p.testReturns, p.testErr
}

func (p *factoryStubProvider) RequestAction(context.Context, string, map[string]string) (any, error) {
	return nil, nil
}

func newStubFactoryRegistry(t *testing.T, testByID map[int][]importlists.TestFailure) *importlists.Registry {
	t.Helper()
	r := importlists.NewRegistry()
	contract := "StubSettings"
	if err := r.Register("Stub", contract, func(def *importlists.ImportList, _ importlists.Dependencies) (importlists.Provider, error) {
		failures := testByID[def.ID]
		return &factoryStubProvider{def: def, testReturns: failures}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

// helpers for creating ImportList rows
func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }
func makeRow(id int, name string, enabled *bool) importlists.ImportList {
	contract := "StubSettings"
	return importlists.ImportList{
		ID:                 id,
		Name:               name,
		Implementation:     "Stub",
		ConfigContract:     &contract,
		EnableAutomaticAdd: enabled,
	}
}

// Helper: build a Factory wired to fresh stub repo/status/registry.
func newFactory(t *testing.T, rows []importlists.ImportList) (*importlists.Factory, *stubFactoryStatus) {
	t.Helper()
	repo := &stubFactoryRepo{rows: rows}
	status := newStubFactoryStatus()
	registry := newStubFactoryRegistry(t, nil)
	f, err := importlists.NewFactory(repo, registry, status, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	return f, status
}

func TestNewFactory_RejectsNilRepoOrRegistry(t *testing.T) {
	t.Parallel()
	if _, err := importlists.NewFactory(nil, importlists.NewRegistry(), nil, importlists.Dependencies{}, nil); err == nil {
		t.Errorf("nil repo: expected error")
	}
	if _, err := importlists.NewFactory(&stubFactoryRepo{}, nil, nil, importlists.Dependencies{}, nil); err == nil {
		t.Errorf("nil registry: expected error")
	}
}

func TestFactory_AutomaticAddEnabled_FiltersByEnabled(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "On", boolPtr(true)),
		makeRow(2, "Off", boolPtr(false)),
		makeRow(3, "NilLegacy", nil),
		makeRow(4, "AlsoOn", boolPtr(true)),
	}
	f, _ := newFactory(t, rows)
	got, err := f.AutomaticAddEnabled(context.Background(), false)
	if err != nil {
		t.Fatalf("AutomaticAddEnabled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d providers want 2", len(got))
	}
	if got[0].Definition().ID != 1 || got[1].Definition().ID != 4 {
		t.Errorf("kept ids: [%d, %d]", got[0].Definition().ID, got[1].Definition().ID)
	}
}

func TestFactory_AutomaticAddEnabled_BlockedFilter(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "Healthy", boolPtr(true)),
		makeRow(2, "Blocked", boolPtr(true)),
	}
	f, status := newFactory(t, rows)
	disabledTill := time.Now().Add(1 * time.Hour)
	status.blocked = []importliststatus.Status{
		{ProviderID: 2, DisabledTill: &disabledTill},
	}

	// filterBlocked=false → both pass.
	got, _ := f.AutomaticAddEnabled(context.Background(), false)
	if len(got) != 2 {
		t.Errorf("filterBlocked=false: got %d want 2", len(got))
	}

	// filterBlocked=true → only healthy.
	got, _ = f.AutomaticAddEnabled(context.Background(), true)
	if len(got) != 1 || got[0].Definition().ID != 1 {
		t.Errorf("filterBlocked=true: got %+v", got)
	}
}

func TestFactory_AutomaticAddEnabled_NilStatusSkipsBlockFilter(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "X", boolPtr(true))}
	repo := &stubFactoryRepo{rows: rows}
	registry := newStubFactoryRegistry(t, nil)
	f, err := importlists.NewFactory(repo, registry, nil, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	got, err := f.AutomaticAddEnabled(context.Background(), true)
	if err != nil {
		t.Fatalf("AutomaticAddEnabled: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 provider, got %d", len(got))
	}
}

func TestFactory_AutomaticAddEnabled_BlockedErrorPropagates(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "X", boolPtr(true))}
	f, status := newFactory(t, rows)
	status.blockedErr = errors.New("boom")
	if _, err := f.AutomaticAddEnabled(context.Background(), true); err == nil {
		t.Errorf("expected error")
	}
}

func TestFactory_All_IncludesDisabledRows(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "On", boolPtr(true)),
		makeRow(2, "Off", boolPtr(false)),
		makeRow(3, "NilLegacy", nil),
	}
	f, _ := newFactory(t, rows)
	got, err := f.All(context.Background())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("All: got %d want 3", len(got))
	}
}

func TestFactory_All_RepoErrorPropagates(t *testing.T) {
	t.Parallel()
	repo := &stubFactoryRepo{err: errors.New("db down")}
	registry := newStubFactoryRegistry(t, nil)
	f, err := importlists.NewFactory(repo, registry, nil, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	if _, err := f.All(context.Background()); err == nil {
		t.Errorf("expected error")
	}
}

func TestFactory_All_SkipsConstructErrors(t *testing.T) {
	t.Parallel()
	registry := importlists.NewRegistry()
	contract := "StubSettings"
	_ = registry.Register("Stub", contract, func(def *importlists.ImportList, _ importlists.Dependencies) (importlists.Provider, error) {
		if def.ID == 2 {
			return nil, errors.New("broken impl")
		}
		return &factoryStubProvider{def: def}, nil
	})
	rows := []importlists.ImportList{
		{ID: 1, Name: "ok", Implementation: "Stub", ConfigContract: &contract, EnableAutomaticAdd: boolPtr(true)},
		{ID: 2, Name: "bad", Implementation: "Stub", ConfigContract: &contract, EnableAutomaticAdd: boolPtr(true)},
		{ID: 3, Name: "alsoOk", Implementation: "Stub", ConfigContract: &contract, EnableAutomaticAdd: boolPtr(true)},
	}
	repo := &stubFactoryRepo{rows: rows}
	f, err := importlists.NewFactory(repo, registry, nil, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	got, _ := f.All(context.Background())
	if len(got) != 2 {
		t.Errorf("kept count: got %d want 2 (broken impl skipped)", len(got))
	}
	if got[0].Definition().ID != 1 || got[1].Definition().ID != 3 {
		t.Errorf("kept ids: [%d, %d]", got[0].Definition().ID, got[1].Definition().ID)
	}
}

// Test() success path: failures empty, ID != 0 → RecordSuccess.
func TestFactory_Test_RecordsSuccess(t *testing.T) {
	t.Parallel()
	registry := newStubFactoryRegistry(t, nil) // no failures
	repo := &stubFactoryRepo{}
	status := newStubFactoryStatus()
	f, _ := importlists.NewFactory(repo, registry, status, importlists.Dependencies{}, slog.New(slog.DiscardHandler))

	row := makeRow(7, "Stub-1", boolPtr(true))
	failures, err := f.Test(context.Background(), &row)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures: %+v want empty", failures)
	}
	if status.recordSuccess[7] != 1 {
		t.Errorf("RecordSuccess: %d want 1", status.recordSuccess[7])
	}
	if status.recordFailure[7] != 0 {
		t.Errorf("RecordFailure: %d want 0", status.recordFailure[7])
	}
}

// Test() failure path: failures non-empty, ID != 0 → RecordFailure.
func TestFactory_Test_RecordsFailure(t *testing.T) {
	t.Parallel()
	registry := newStubFactoryRegistry(t, map[int][]importlists.TestFailure{
		7: {{PropertyName: "ApiKey", ErrorMessage: "Invalid"}},
	})
	repo := &stubFactoryRepo{}
	status := newStubFactoryStatus()
	f, _ := importlists.NewFactory(repo, registry, status, importlists.Dependencies{}, slog.New(slog.DiscardHandler))

	row := makeRow(7, "Stub-1", boolPtr(true))
	failures, err := f.Test(context.Background(), &row)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 1 {
		t.Errorf("failures: %+v want 1", failures)
	}
	if status.recordFailure[7] != 1 {
		t.Errorf("RecordFailure: %d want 1", status.recordFailure[7])
	}
	if status.recordSuccess[7] != 0 {
		t.Errorf("RecordSuccess: %d want 0", status.recordSuccess[7])
	}
}

// Test() with ID=0 (new row not yet persisted) → does NOT record.
// Mirrors C# `:78-81`.
func TestFactory_Test_IdZeroSkipsRecord(t *testing.T) {
	t.Parallel()
	registry := newStubFactoryRegistry(t, map[int][]importlists.TestFailure{
		0: {{PropertyName: "ApiKey", ErrorMessage: "Invalid"}},
	})
	repo := &stubFactoryRepo{}
	status := newStubFactoryStatus()
	f, _ := importlists.NewFactory(repo, registry, status, importlists.Dependencies{}, slog.New(slog.DiscardHandler))

	row := makeRow(0, "NewUnsaved", boolPtr(true))
	failures, _ := f.Test(context.Background(), &row)
	if len(failures) != 1 {
		t.Errorf("failures: %+v want 1", failures)
	}
	if status.recordSuccess[0] != 0 || status.recordFailure[0] != 0 {
		t.Errorf("recorded with Id=0: success=%d failure=%d (should both be 0)",
			status.recordSuccess[0], status.recordFailure[0])
	}
}

// Test() with nil status silently skips the record step.
func TestFactory_Test_NilStatusTolerated(t *testing.T) {
	t.Parallel()
	registry := newStubFactoryRegistry(t, nil)
	repo := &stubFactoryRepo{}
	f, _ := importlists.NewFactory(repo, registry, nil, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	row := makeRow(7, "Stub", boolPtr(true))
	if _, err := f.Test(context.Background(), &row); err != nil {
		t.Errorf("Test: %v", err)
	}
}

// Test() with nil row returns an error rather than panicking.
func TestFactory_Test_NilRow(t *testing.T) {
	t.Parallel()
	registry := newStubFactoryRegistry(t, nil)
	repo := &stubFactoryRepo{}
	f, _ := importlists.NewFactory(repo, registry, nil, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	if _, err := f.Test(context.Background(), nil); err == nil {
		t.Errorf("expected error")
	}
}

// Test() with Construct error propagates.
func TestFactory_Test_ConstructError(t *testing.T) {
	t.Parallel()
	registry := importlists.NewRegistry()
	_ = registry.Register("Stub", "StubSettings", func(*importlists.ImportList, importlists.Dependencies) (importlists.Provider, error) {
		return nil, errors.New("broken")
	})
	repo := &stubFactoryRepo{}
	f, _ := importlists.NewFactory(repo, registry, nil, importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	row := makeRow(7, "Stub", boolPtr(true))
	if _, err := f.Test(context.Background(), &row); err == nil {
		t.Errorf("expected error")
	}
}

// ShouldHandleByTags ports the C# helper at NotificationService.cs:97-113
// with the same semantics. Pin its behaviour with a small table.
func TestShouldHandleByTags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		listTags, srsTags []int
		want              bool
	}{
		{"both_empty_matches", nil, nil, true},
		{"list_empty_matches_anything", nil, []int{1, 2}, true},
		{"intersect", []int{1, 2}, []int{2, 3}, true},
		{"disjoint", []int{1, 2}, []int{3, 4}, false},
		{"list_set_series_empty", []int{1}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := importlists.ShouldHandleByTags(tc.listTags, tc.srsTags); got != tc.want {
				t.Errorf("ShouldHandleByTags(%v, %v) = %v, want %v", tc.listTags, tc.srsTags, got, tc.want)
			}
		})
	}
}

// Defensive: unused helper preserves a reference to strPtr to keep
// the import surface stable across edits.
var _ = strPtr
