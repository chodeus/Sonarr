package notifications_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notificationstatus"
)

// stubRepo is the in-memory factoryRepo for factory tests — keeps
// the tests independent of the live DB.
type stubRepo struct {
	rows []notifications.Notification
	err  error
}

func (s *stubRepo) All(_ context.Context) ([]notifications.Notification, error) {
	return s.rows, s.err
}

// stubStatus is the in-memory factoryStatus for the blocked-filter
// arm.
type stubStatus struct {
	blocked []notificationstatus.Status
	err     error
}

func (s *stubStatus) GetBlockedProviders(_ context.Context) ([]notificationstatus.Status, error) {
	return s.blocked, s.err
}

// freshRegistry returns a notifications.Registry pre-populated
// with a single stub constructor under (Stub, StubSettings).
func freshRegistry(t *testing.T) *notifications.Registry {
	t.Helper()
	r := notifications.NewRegistry()
	if err := r.Register("Stub", "StubSettings", newStubFactory()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

// stubContract returns a *string for the ConfigContract column.
func stubContract() *string {
	c := "StubSettings"
	return &c
}

func TestFactory_NewFactory_RejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := notifications.NewFactory(nil, notifications.NewRegistry(), nil, notifications.Dependencies{}, nil); err == nil {
		t.Errorf("nil repo: expected error")
	}
	if _, err := notifications.NewFactory(&stubRepo{}, nil, nil, notifications.Dependencies{}, nil); err == nil {
		t.Errorf("nil registry: expected error")
	}
}

// TestFactory_OnGrabEnabled_FiltersByFlag verifies that only rows
// with OnGrab=true are returned. Other On* events MUST NOT trigger
// inclusion.
func TestFactory_OnGrabEnabled_FiltersByFlag(t *testing.T) {
	t.Parallel()
	repo := &stubRepo{
		rows: []notifications.Notification{
			{ID: 1, Name: "Grabby", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
			{ID: 2, Name: "Downloady", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true},
			{ID: 3, Name: "Both", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true, OnDownload: true},
		},
	}
	f, err := notifications.NewFactory(repo, freshRegistry(t), nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	got, err := f.OnGrabEnabled(t.Context(), false)
	if err != nil {
		t.Fatalf("OnGrabEnabled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (Grabby + Both); names=%v", len(got), nameList(got))
	}
	names := nameList(got)
	if names[0] != "Grabby" || names[1] != "Both" {
		// repo.All returns insertion order — test asserts that order.
		t.Errorf("names: got %v want [Grabby Both]", names)
	}
}

// TestFactory_PerEventAccessors covers every OnXEnabled accessor
// in a single table-driven test — verifies each filters on the
// correct field of the Notification struct.
func TestFactory_PerEventAccessors(t *testing.T) {
	t.Parallel()

	truePtr := func(b bool) *bool { return &b }

	rows := []notifications.Notification{
		{ID: 1, Name: "grab", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		{ID: 2, Name: "dl", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true},
		{ID: 3, Name: "upgrade", Implementation: "Stub", ConfigContract: stubContract(), OnUpgrade: truePtr(true)},
		{ID: 4, Name: "imp", Implementation: "Stub", ConfigContract: stubContract(), OnImportComplete: true},
		{ID: 5, Name: "rename", Implementation: "Stub", ConfigContract: stubContract(), OnRename: true},
		{ID: 6, Name: "seriesAdd", Implementation: "Stub", ConfigContract: stubContract(), OnSeriesAdd: true},
		{ID: 7, Name: "seriesDel", Implementation: "Stub", ConfigContract: stubContract(), OnSeriesDelete: true},
		{ID: 8, Name: "epDel", Implementation: "Stub", ConfigContract: stubContract(), OnEpisodeFileDelete: true},
		{ID: 9, Name: "epDelUpg", Implementation: "Stub", ConfigContract: stubContract(), OnEpisodeFileDeleteForUpgrade: true},
		{ID: 10, Name: "health", Implementation: "Stub", ConfigContract: stubContract(), OnHealthIssue: true},
		{ID: 11, Name: "healthRest", Implementation: "Stub", ConfigContract: stubContract(), OnHealthRestored: true},
		{ID: 12, Name: "appUpd", Implementation: "Stub", ConfigContract: stubContract(), OnApplicationUpdate: true},
		{ID: 13, Name: "manual", Implementation: "Stub", ConfigContract: stubContract(), OnManualInteractionRequired: true},
	}
	f, _ := notifications.NewFactory(&stubRepo{rows: rows}, freshRegistry(t), nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))

	cases := []struct {
		name   string
		fn     func(context.Context, bool) ([]notifications.Provider, error)
		wantID int
	}{
		{"OnGrabEnabled", f.OnGrabEnabled, 1},
		{"OnDownloadEnabled", f.OnDownloadEnabled, 2},
		{"OnUpgradeEnabled", f.OnUpgradeEnabled, 3},
		{"OnImportCompleteEnabled", f.OnImportCompleteEnabled, 4},
		{"OnRenameEnabled", f.OnRenameEnabled, 5},
		{"OnSeriesAddEnabled", f.OnSeriesAddEnabled, 6},
		{"OnSeriesDeleteEnabled", f.OnSeriesDeleteEnabled, 7},
		{"OnEpisodeFileDeleteEnabled", f.OnEpisodeFileDeleteEnabled, 8},
		{"OnEpisodeFileDeleteForUpgradeEnabled", f.OnEpisodeFileDeleteForUpgradeEnabled, 9},
		{"OnHealthIssueEnabled", f.OnHealthIssueEnabled, 10},
		{"OnHealthRestoredEnabled", f.OnHealthRestoredEnabled, 11},
		{"OnApplicationUpdateEnabled", f.OnApplicationUpdateEnabled, 12},
		{"OnManualInteractionEnabled", f.OnManualInteractionEnabled, 13},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := c.fn(t.Context(), false)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(got) != 1 {
				t.Fatalf("%s: len=%d want 1", c.name, len(got))
			}
			if got[0].Definition().ID != c.wantID {
				t.Errorf("%s: id=%d want %d", c.name, got[0].Definition().ID, c.wantID)
			}
		})
	}
}

// TestFactory_OnUpgrade_NullableFalse verifies that an OnUpgrade=nil
// (legacy NULL in DB) is treated as false. C# parity: nullable bool
// JSON-deserialises to bool? which is null → false in the
// Where predicate.
func TestFactory_OnUpgrade_NullableFalse(t *testing.T) {
	t.Parallel()
	falsePtr := func() *bool { b := false; return &b }
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnUpgrade: nil},
		{ID: 2, Implementation: "Stub", ConfigContract: stubContract(), OnUpgrade: falsePtr()},
	}
	f, _ := notifications.NewFactory(&stubRepo{rows: rows}, freshRegistry(t), nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	got, _ := f.OnUpgradeEnabled(t.Context(), false)
	if len(got) != 0 {
		t.Errorf("len=%d want 0 (nil and *false both exclude)", len(got))
	}
}

// TestFactory_FilterBlocked verifies that filterBlocked=true skips
// rows whose ID appears in notificationstatus.Service.GetBlockedProviders.
func TestFactory_FilterBlocked(t *testing.T) {
	t.Parallel()
	repo := &stubRepo{
		rows: []notifications.Notification{
			{ID: 1, Name: "alive", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
			{ID: 2, Name: "dead", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		},
	}
	status := &stubStatus{blocked: []notificationstatus.Status{{ProviderID: 2}}}
	f, _ := notifications.NewFactory(repo, freshRegistry(t), status, notifications.Dependencies{}, slog.New(slog.DiscardHandler))

	gotWithFilter, _ := f.OnGrabEnabled(t.Context(), true)
	if len(gotWithFilter) != 1 || gotWithFilter[0].Definition().ID != 1 {
		t.Errorf("filterBlocked=true: got %v want [1]", idList(gotWithFilter))
	}
	gotNoFilter, _ := f.OnGrabEnabled(t.Context(), false)
	if len(gotNoFilter) != 2 {
		t.Errorf("filterBlocked=false: got %v want both", idList(gotNoFilter))
	}
}

// TestFactory_FilterBlocked_NilStatus verifies that when the
// Factory was constructed with a nil status (test/migration mode),
// filterBlocked=true is a no-op and every enabled provider is
// returned.
func TestFactory_FilterBlocked_NilStatus(t *testing.T) {
	t.Parallel()
	repo := &stubRepo{
		rows: []notifications.Notification{
			{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		},
	}
	f, _ := notifications.NewFactory(repo, freshRegistry(t), nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	got, _ := f.OnGrabEnabled(t.Context(), true)
	if len(got) != 1 {
		t.Errorf("nil-status + filterBlocked=true: got %d want 1", len(got))
	}
}

// TestFactory_ConstructorErrorsAreSkipped verifies that a row whose
// Constructor errors is logged + skipped, not propagated. The C#
// behaviour at fan-out time wraps the resolution failure as a
// per-provider log Warn — the Go port mirrors via the registry
// Construct fall-through.
func TestFactory_ConstructorErrorsAreSkipped(t *testing.T) {
	t.Parallel()
	registry := notifications.NewRegistry()
	_ = registry.Register("Good", "GoodSettings", newStubFactory())
	_ = registry.Register("Bad", "BadSettings", func(*notifications.Notification, notifications.Dependencies) (notifications.Provider, error) {
		return nil, errors.New("boom")
	})
	badContract := "BadSettings"
	goodContract := "GoodSettings"
	repo := &stubRepo{
		rows: []notifications.Notification{
			{ID: 1, Name: "good", Implementation: "Good", ConfigContract: &goodContract, OnGrab: true},
			{ID: 2, Name: "bad", Implementation: "Bad", ConfigContract: &badContract, OnGrab: true},
		},
	}
	f, _ := notifications.NewFactory(repo, registry, nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	got, err := f.OnGrabEnabled(t.Context(), false)
	if err != nil {
		t.Fatalf("OnGrabEnabled: %v", err)
	}
	if len(got) != 1 || got[0].Definition().ID != 1 {
		t.Errorf("got %v want [1]", idList(got))
	}
}

// TestFactory_RepoErrorPropagates verifies that a non-nil error
// from repo.All propagates as a wrapped factory error.
func TestFactory_RepoErrorPropagates(t *testing.T) {
	t.Parallel()
	repo := &stubRepo{err: errors.New("db down")}
	f, _ := notifications.NewFactory(repo, freshRegistry(t), nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	_, err := f.OnGrabEnabled(t.Context(), false)
	if err == nil {
		t.Errorf("expected error propagation")
	}
}

// TestShouldHandleSeries covers the three branches of the C#
// helper at NotificationService.cs:97-113.
func TestShouldHandleSeries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		notifTags  []int
		seriesTags []int
		want       bool
	}{
		{"empty notification tags → match anything", nil, []int{1, 2}, true},
		{"empty notif tags, empty series tags → match", nil, nil, true},
		{"intersecting tags → match", []int{1, 2}, []int{2, 3}, true},
		{"no intersection → no match", []int{1, 2}, []int{3, 4}, false},
		{"notif tags, empty series tags → no match", []int{1}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := notifications.ShouldHandleSeries(c.notifTags, c.seriesTags); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

// TestShouldHandleHealthFailure covers the C# helper at
// NotificationService.cs:115-128. Error always fires; Warning only
// when IncludeHealthWarnings is true; Notice/Ok never fire.
func TestShouldHandleHealthFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		level   healthcheck.Result
		incWarn bool
		want    bool
	}{
		{"Error always fires", healthcheck.ResultError, false, true},
		{"Warning + IncludeHealthWarnings=true fires", healthcheck.ResultWarning, true, true},
		{"Warning + IncludeHealthWarnings=false skips", healthcheck.ResultWarning, false, false},
		{"Notice never fires", healthcheck.ResultNotice, true, false},
		{"OK never fires", healthcheck.ResultOK, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := notifications.ShouldHandleHealthFailure(c.level, c.incWarn); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

// Helpers.

func nameList(ps []notifications.Provider) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if d := p.Definition(); d != nil {
			out = append(out, d.Name)
		}
	}
	return out
}

func idList(ps []notifications.Provider) []int {
	out := make([]int, 0, len(ps))
	for _, p := range ps {
		if d := p.Definition(); d != nil {
			out = append(out, d.ID)
		}
	}
	return out
}
