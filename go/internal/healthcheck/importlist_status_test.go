package healthcheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/importliststatus"
)

type stubEnabledImportListLister struct {
	enabled []healthcheck.EnabledImportList
	err     error
}

func (s stubEnabledImportListLister) EnabledImportLists(_ context.Context) ([]healthcheck.EnabledImportList, error) {
	return s.enabled, s.err
}

type stubImportListStatusBlockedLister struct {
	blocked []importliststatus.Status
	err     error
}

func (s stubImportListStatusBlockedLister) GetBlockedProviders(_ context.Context) ([]importliststatus.Status, error) {
	return s.blocked, s.err
}

func makeImportListStatusCheck(t *testing.T, enabled []healthcheck.EnabledImportList, blocked []importliststatus.Status) *healthcheck.ImportListStatusCheck {
	t.Helper()
	c, err := healthcheck.NewImportListStatusCheck(
		stubEnabledImportListLister{enabled: enabled},
		stubImportListStatusBlockedLister{blocked: blocked},
	)
	if err != nil {
		t.Fatalf("NewImportListStatusCheck: %v", err)
	}
	return c
}

func TestImportListStatusCheck_NewRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewImportListStatusCheck(nil, stubImportListStatusBlockedLister{}); err == nil {
		t.Errorf("nil EnabledImportListLister: expected error")
	}
	if _, err := healthcheck.NewImportListStatusCheck(stubEnabledImportListLister{}, nil); err == nil {
		t.Errorf("nil ImportListStatusBlockedLister: expected error")
	}
}

func TestImportListStatusCheck_NoEnabledImportLists_OK(t *testing.T) {
	t.Parallel()
	c := makeImportListStatusCheck(t, nil, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on no enabled import lists, got %v: %s", got.Type, got.Message)
	}
}

func TestImportListStatusCheck_AllHealthy_OK(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Sonarr Peer"},
		{ID: 2, Name: "Plex Watchlist"},
	}
	c := makeImportListStatusCheck(t, enabled, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on all-healthy, got %v: %s", got.Type, got.Message)
	}
}

// TestImportListStatusCheck_SomeBlocked_Warning verifies the
// some-blocked → Warning + comma-separated names path. Mirrors the
// C# `:48-55` arm.
func TestImportListStatusCheck_SomeBlocked_Warning(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Bravo"},
		{ID: 3, Name: "Charlie"},
	}
	blocked := []importliststatus.Status{
		{ProviderID: 1},
		{ProviderID: 3},
	}
	c := makeImportListStatusCheck(t, enabled, blocked)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("expected Warning, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonImportListStatusUnavailable {
		t.Errorf("Reason: got %v want ReasonImportListStatusUnavailable", got.Reason)
	}
	if !strings.Contains(got.Message, "Alpha") || !strings.Contains(got.Message, "Charlie") {
		t.Errorf("Message should contain Alpha + Charlie names: %q", got.Message)
	}
	if strings.Contains(got.Message, "Bravo") {
		t.Errorf("Bravo is healthy, should not appear in message: %q", got.Message)
	}
}

// TestImportListStatusCheck_AllBlocked_Error verifies the
// every-row-down → Error path. Mirrors the C# `:41-46` arm.
func TestImportListStatusCheck_AllBlocked_Error(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Bravo"},
	}
	blocked := []importliststatus.Status{
		{ProviderID: 1},
		{ProviderID: 2},
	}
	c := makeImportListStatusCheck(t, enabled, blocked)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("expected Error on all-blocked, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonImportListStatusAllUnavailable {
		t.Errorf("Reason: got %v want ReasonImportListStatusAllUnavailable", got.Reason)
	}
}

// TestImportListStatusCheck_NamesSorted verifies the names are
// rendered in case-insensitive sorted order.
func TestImportListStatusCheck_NamesSorted(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledImportList{
		{ID: 1, Name: "zebra"},
		{ID: 2, Name: "Alpha"},
		{ID: 3, Name: "mongoose"},
		{ID: 4, Name: "Healthy"}, // not blocked
	}
	blocked := []importliststatus.Status{
		{ProviderID: 1}, {ProviderID: 2}, {ProviderID: 3},
	}
	c := makeImportListStatusCheck(t, enabled, blocked)
	got := c.Run(t.Context())
	idx := strings.Index(got.Message, ": ")
	if idx < 0 {
		t.Fatalf("Message format unexpected: %q", got.Message)
	}
	names := got.Message[idx+2:]
	want := "Alpha, mongoose, zebra"
	if names != want {
		t.Errorf("names: got %q want %q", names, want)
	}
}

// TestImportListStatusCheck_ListListerError surfaces a lookup error
// as Warning rather than panic.
func TestImportListStatusCheck_ListListerError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewImportListStatusCheck(
		stubEnabledImportListLister{err: errors.New("db down")},
		stubImportListStatusBlockedLister{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("list lister error: want Warning, got %v: %s", got.Type, got.Message)
	}
}

// TestImportListStatusCheck_StatusListerError surfaces a status-
// lookup error as Warning.
func TestImportListStatusCheck_StatusListerError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewImportListStatusCheck(
		stubEnabledImportListLister{enabled: []healthcheck.EnabledImportList{{ID: 1, Name: "X"}}},
		stubImportListStatusBlockedLister{err: errors.New("status broken")},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("status lister error: want Warning, got %v: %s", got.Type, got.Message)
	}
}

func TestImportListStatusCheck_Metadata(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewImportListStatusCheck(stubEnabledImportListLister{}, stubImportListStatusBlockedLister{})
	if c.Name() != healthcheck.ImportListStatusCheckName {
		t.Errorf("Name: got %q", c.Name())
	}
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup: want true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule: want true")
	}
	if c.EventTriggers() != nil {
		t.Errorf("EventTriggers: want nil (event-driven bindings deferred), got %v", c.EventTriggers())
	}
}
