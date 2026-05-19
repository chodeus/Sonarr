package healthcheck_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/indexerstatus"
)

func makeIndexerLongTermStatusCheck(t *testing.T, enabled []healthcheck.EnabledIndexer, blocked []indexerstatus.Status, now time.Time) *healthcheck.IndexerLongTermStatusCheck {
	t.Helper()
	c, err := healthcheck.NewIndexerLongTermStatusCheck(
		stubEnabledIndexerLister{enabled: enabled},
		stubIndexerStatusBlockedLister{blocked: blocked},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewIndexerLongTermStatusCheck: %v", err)
	}
	return c
}

func TestIndexerLongTermStatusCheck_NewRejectsNils(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewIndexerLongTermStatusCheck(nil, stubIndexerStatusBlockedLister{}, nil); err == nil {
		t.Errorf("expected error on nil indexer lister")
	}
	if _, err := healthcheck.NewIndexerLongTermStatusCheck(stubEnabledIndexerLister{}, nil, nil); err == nil {
		t.Errorf("expected error on nil status lister")
	}
}

func TestIndexerLongTermStatusCheck_OKNoEnabled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	c := makeIndexerLongTermStatusCheck(t, nil, nil, now)
	if got := c.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestIndexerLongTermStatusCheck_OKRecentFailureOnly(t *testing.T) {
	t.Parallel()
	// Failure 1h ago should be picked up by IndexerStatusCheck, NOT
	// by IndexerLongTermStatusCheck.
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{{ID: 1, Name: "Indy"}}
	blocked := []indexerstatus.Status{{ProviderID: 1, InitialFailure: tp(now.Add(-1 * time.Hour))}}
	c := makeIndexerLongTermStatusCheck(t, enabled, blocked, now)
	if got := c.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK (recent failure shouldn't trip long-term check)", got.Type)
	}
}

func TestIndexerLongTermStatusCheck_WarnsOnLongFailureSubset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "Long"},
		{ID: 2, Name: "Healthy"},
	}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(now.Add(-24 * time.Hour))},
	}
	c := makeIndexerLongTermStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != healthcheck.ReasonIndexerLongTermStatusUnavailable {
		t.Errorf("reason = %v", got.Reason)
	}
	if !strings.Contains(got.Message, "Long") {
		t.Errorf("message missing indexer name: %q", got.Message)
	}
}

func TestIndexerLongTermStatusCheck_ErrorWhenAllLongFailed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "A"},
		{ID: 2, Name: "B"},
	}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(now.Add(-12 * time.Hour))},
		{ProviderID: 2, InitialFailure: tp(now.Add(-7 * time.Hour))},
	}
	c := makeIndexerLongTermStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("type = %v, want Error", got.Type)
	}
	if got.Reason != healthcheck.ReasonIndexerLongTermStatusAllUnavailable {
		t.Errorf("reason = %v", got.Reason)
	}
}

func TestIndexerLongTermStatusCheck_NilInitialFailureIgnored(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{{ID: 1, Name: "Indy"}}
	blocked := []indexerstatus.Status{{ProviderID: 1, InitialFailure: nil}}
	c := makeIndexerLongTermStatusCheck(t, enabled, blocked, now)
	if got := c.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK (nil failure)", got.Type)
	}
}

func TestIndexerLongTermStatusCheck_SortedMessage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	enabled := []healthcheck.EnabledIndexer{
		{ID: 1, Name: "Zeta"},
		{ID: 2, Name: "Alpha"},
		{ID: 3, Name: "Mid"},
		{ID: 4, Name: "Healthy"},
	}
	blocked := []indexerstatus.Status{
		{ProviderID: 1, InitialFailure: tp(now.Add(-12 * time.Hour))},
		{ProviderID: 2, InitialFailure: tp(now.Add(-12 * time.Hour))},
		{ProviderID: 3, InitialFailure: tp(now.Add(-12 * time.Hour))},
	}
	c := makeIndexerLongTermStatusCheck(t, enabled, blocked, now)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v", got.Type)
	}
	if !strings.Contains(got.Message, "Alpha, Mid, Zeta") {
		t.Errorf("expected alphabetic order; got %q", got.Message)
	}
}

func TestIndexerLongTermStatusCheck_IndexerErrorPropagates(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewIndexerLongTermStatusCheck(
		stubEnabledIndexerLister{err: errors.New("ix boom")},
		stubIndexerStatusBlockedLister{},
		nil,
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v", got.Type)
	}
	if !strings.Contains(got.Message, "ix boom") {
		t.Errorf("message: %q", got.Message)
	}
}

func TestIndexerLongTermStatusCheck_StatusErrorPropagates(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewIndexerLongTermStatusCheck(
		stubEnabledIndexerLister{enabled: []healthcheck.EnabledIndexer{{ID: 1, Name: "X"}}},
		stubIndexerStatusBlockedLister{err: errors.New("status boom")},
		nil,
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v", got.Type)
	}
	if !strings.Contains(got.Message, "status boom") {
		t.Errorf("message: %q", got.Message)
	}
}
