package healthcheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/indexerstatus"
)

// stubEnabledIndexerLister returns a fixed slice / error.
type stubEnabledIndexerLister struct {
	enabled []healthcheck.EnabledIndexer
	err     error
}

func (s stubEnabledIndexerLister) EnabledIndexers(_ context.Context) ([]healthcheck.EnabledIndexer, error) {
	return s.enabled, s.err
}

// stubIndexerStatusBlockedLister returns a fixed slice / error.
type stubIndexerStatusBlockedLister struct {
	blocked []indexerstatus.Status
	err     error
}

func (s stubIndexerStatusBlockedLister) GetBlockedProviders(_ context.Context) ([]indexerstatus.Status, error) {
	return s.blocked, s.err
}

func tp(t time.Time) *time.Time { return &t }

func makeIndexerStatusCheck(t *testing.T, enabled []healthcheck.EnabledIndexer, blocked []indexerstatus.Status, now time.Time) *healthcheck.IndexerStatusCheck {
	t.Helper()
	c, err := healthcheck.NewIndexerStatusCheck(
		stubEnabledIndexerLister{enabled: enabled},
		stubIndexerStatusBlockedLister{blocked: blocked},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewIndexerStatusCheck: %v", err)
	}
	return c
}

func TestIndexerStatusCheck_NewRejectsNilIndexerLister(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewIndexerStatusCheck(nil, stubIndexerStatusBlockedLister{}, nil); err == nil {
		t.Errorf("expected error on nil EnabledIndexerLister")
	}
}

func TestIndexerStatusCheck_NewRejectsNilStatusLister(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewIndexerStatusCheck(stubEnabledIndexerLister{}, nil, nil); err == nil {
		t.Errorf("expected error on nil IndexerStatusBlockedLister")
	}
}

func TestIndexerStatusCheck_NoEnabledIndexersReturnsOK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	c := makeIndexerStatusCheck(t, nil, nil, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on no enabled indexers, got %v: %s", got.Type, got.Message)
	}
}

func TestIndexerStatusCheck_AllHealthyReturnsOK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Beta"},
	}
	c := makeIndexerStatusCheck(t, enabled, nil, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK when no blocked statuses, got %v: %s", got.Type, got.Message)
	}
}

func TestIndexerStatusCheck_BlockedButNotEnabledReturnsOK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "Alpha"},
	}
	// Status row exists for ProviderID=999 (not in enabled list) — must
	// be ignored. Mirrors the C# inner-join semantics at `:29-32`.
	blocked := []indexerstatus.Status{
		{ProviderID: 999, InitialFailure: tp(now.Add(-1 * time.Hour))},
	}
	c := makeIndexerStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK when blocked status doesn't match enabled set, got %v", got.Type)
	}
}

func TestIndexerStatusCheck_BlockedWithNilInitialFailureReturnsOK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{{ID: 1, Name: "Alpha"}}
	// Mirrors the C# `s.Status.InitialFailure.HasValue` predicate at
	// `:33` — nil InitialFailure must NOT contribute to the down set.
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: nil},
	}
	c := makeIndexerStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK when InitialFailure is nil, got %v", got.Type)
	}
}

func TestIndexerStatusCheck_OldFailureOutsideWindowReturnsOK(t *testing.T) {
	t.Parallel()
	// Failure 7h ago — outside the 6h window, so SHORT-TERM check
	// passes. (Long-term variant in a follow-up slice will fire here.)
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{{ID: 1, Name: "Alpha"}}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(now.Add(-7 * time.Hour))},
	}
	c := makeIndexerStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on old failure outside short-term window, got %v", got.Type)
	}
}

func TestIndexerStatusCheck_SomeRecentFailuresReturnsWarning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "Zebra"},
		{ID: 2, Name: "Beta"},
		{ID: 3, Name: "Alpha"},
	}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(now.Add(-1 * time.Hour))},
		{ProviderID: 3, InitialFailure: tp(now.Add(-30 * time.Minute))},
	}
	c := makeIndexerStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning on partial outage, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonIndexerStatusUnavailable {
		t.Errorf("Reason = %v, want ReasonIndexerStatusUnavailable", got.Reason)
	}
	// Names sort case-insensitive: "Alpha", "Zebra".
	if !strings.Contains(got.Message, "Alpha") || !strings.Contains(got.Message, "Zebra") {
		t.Errorf("Message %q should mention down indexer names", got.Message)
	}
	alphaIdx := strings.Index(got.Message, "Alpha")
	zebraIdx := strings.Index(got.Message, "Zebra")
	if alphaIdx >= zebraIdx {
		t.Errorf("Alpha should sort before Zebra in %q", got.Message)
	}
	if strings.Contains(got.Message, "Beta") {
		t.Errorf("Healthy indexer Beta should not appear: %q", got.Message)
	}
}

func TestIndexerStatusCheck_AllRecentFailuresReturnsError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Beta"},
	}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(now.Add(-1 * time.Hour))},
		{ProviderID: 2, InitialFailure: tp(now.Add(-2 * time.Hour))},
	}
	c := makeIndexerStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error on full outage, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonIndexerStatusAllUnavailable {
		t.Errorf("Reason = %v, want ReasonIndexerStatusAllUnavailable", got.Reason)
	}
}

func TestIndexerStatusCheck_BoundaryFailureExactlyAtCutoffIsExcluded(t *testing.T) {
	t.Parallel()
	// Mirrors the C# `After(now-6h)` STRICT-greater predicate —
	// failures at exactly the cutoff are NOT counted as recent.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-healthcheck.IndexerRecentFailureWindow)
	enabled := []healthcheck.EnabledIndexer{{ID: 1, Name: "Alpha"}}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(cutoff)},
	}
	c := makeIndexerStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK at exactly-cutoff failure, got %v", got.Type)
	}
}

func TestIndexerStatusCheck_IndexerListerErrorReturnsWarning(t *testing.T) {
	t.Parallel()
	c, err := healthcheck.NewIndexerStatusCheck(
		stubEnabledIndexerLister{err: errors.New("db down")},
		stubIndexerStatusBlockedLister{},
		nil,
	)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning on indexer-lister error, got %v", got.Type)
	}
	if !strings.Contains(got.Message, "db down") {
		t.Errorf("Message %q should mention error", got.Message)
	}
}

func TestIndexerStatusCheck_StatusListerErrorReturnsWarning(t *testing.T) {
	t.Parallel()
	c, err := healthcheck.NewIndexerStatusCheck(
		stubEnabledIndexerLister{enabled: []healthcheck.EnabledIndexer{{ID: 1, Name: "X"}}},
		stubIndexerStatusBlockedLister{err: errors.New("db down")},
		nil,
	)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning on status-lister error, got %v", got.Type)
	}
	if !strings.Contains(got.Message, "db down") {
		t.Errorf("Message %q should mention error", got.Message)
	}
}

func TestIndexerStatusCheck_CheckOnStartupAndScheduleDefaults(t *testing.T) {
	t.Parallel()
	c := makeIndexerStatusCheck(t, nil, nil, time.Now())
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
}

func TestIndexerStatusCheck_EventTriggersEmpty(t *testing.T) {
	t.Parallel()
	// ProviderXxxEvent<IIndexer> bindings deferred — Phase 4 territory.
	c := makeIndexerStatusCheck(t, nil, nil, time.Now())
	if got := c.EventTriggers(); len(got) != 0 {
		t.Errorf("expected no triggers until indexer-provider events port, got %v", got)
	}
}

func TestIndexerStatusCheck_NameAndInterfaceShape(t *testing.T) {
	t.Parallel()
	c := makeIndexerStatusCheck(t, nil, nil, time.Now())
	if c.Name() != healthcheck.IndexerStatusCheckName {
		t.Errorf("Name = %q", c.Name())
	}
	var iface healthcheck.Check = c
	_ = iface
	var iface2 healthcheck.EventDrivenCheck = c
	_ = iface2
}
