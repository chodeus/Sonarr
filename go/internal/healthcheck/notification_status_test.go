package healthcheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notificationstatus"
)

type stubEnabledNotificationLister struct {
	enabled []healthcheck.EnabledNotification
	err     error
}

func (s stubEnabledNotificationLister) EnabledNotifications(_ context.Context) ([]healthcheck.EnabledNotification, error) {
	return s.enabled, s.err
}

type stubNotificationStatusBlockedLister struct {
	blocked []notificationstatus.Status
	err     error
}

func (s stubNotificationStatusBlockedLister) GetBlockedProviders(_ context.Context) ([]notificationstatus.Status, error) {
	return s.blocked, s.err
}

func makeNotificationStatusCheck(t *testing.T, enabled []healthcheck.EnabledNotification, blocked []notificationstatus.Status) *healthcheck.NotificationStatusCheck {
	t.Helper()
	c, err := healthcheck.NewNotificationStatusCheck(
		stubEnabledNotificationLister{enabled: enabled},
		stubNotificationStatusBlockedLister{blocked: blocked},
	)
	if err != nil {
		t.Fatalf("NewNotificationStatusCheck: %v", err)
	}
	return c
}

func TestNotificationStatusCheck_NewRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewNotificationStatusCheck(nil, stubNotificationStatusBlockedLister{}); err == nil {
		t.Errorf("nil EnabledNotificationLister: expected error")
	}
	if _, err := healthcheck.NewNotificationStatusCheck(stubEnabledNotificationLister{}, nil); err == nil {
		t.Errorf("nil NotificationStatusBlockedLister: expected error")
	}
}

func TestNotificationStatusCheck_NoEnabledNotifications_OK(t *testing.T) {
	t.Parallel()
	c := makeNotificationStatusCheck(t, nil, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on no enabled notifications, got %v: %s", got.Type, got.Message)
	}
}

func TestNotificationStatusCheck_AllHealthy_OK(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledNotification{
		{ID: 1, Name: "Discord"},
		{ID: 2, Name: "Webhook"},
	}
	c := makeNotificationStatusCheck(t, enabled, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on all-healthy, got %v: %s", got.Type, got.Message)
	}
}

// TestNotificationStatusCheck_SomeBlocked_Warning verifies the
// some-blocked → Warning + comma-separated names path. Mirrors the
// C# `:48-55` arm.
func TestNotificationStatusCheck_SomeBlocked_Warning(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledNotification{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Bravo"},
		{ID: 3, Name: "Charlie"},
	}
	blocked := []notificationstatus.Status{
		{ProviderID: 1},
		{ProviderID: 3},
	}
	c := makeNotificationStatusCheck(t, enabled, blocked)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("expected Warning, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonNotificationStatusSingle {
		t.Errorf("Reason: got %v want ReasonNotificationStatusSingle", got.Reason)
	}
	if !strings.Contains(got.Message, "Alpha") || !strings.Contains(got.Message, "Charlie") {
		t.Errorf("Message should contain Alpha + Charlie names: %q", got.Message)
	}
	if strings.Contains(got.Message, "Bravo") {
		t.Errorf("Bravo is healthy, should not appear in message: %q", got.Message)
	}
}

// TestNotificationStatusCheck_AllBlocked_Error verifies the
// every-row-down → Error path. Mirrors the C# `:41-46` arm.
func TestNotificationStatusCheck_AllBlocked_Error(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledNotification{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Bravo"},
	}
	blocked := []notificationstatus.Status{
		{ProviderID: 1},
		{ProviderID: 2},
	}
	c := makeNotificationStatusCheck(t, enabled, blocked)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("expected Error on all-blocked, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonNotificationStatusAll {
		t.Errorf("Reason: got %v want ReasonNotificationStatusAll", got.Reason)
	}
}

// TestNotificationStatusCheck_NamesSorted verifies the names are
// rendered in case-insensitive sorted order, matching the
// IndexerStatusCheck precedent.
func TestNotificationStatusCheck_NamesSorted(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledNotification{
		{ID: 1, Name: "zebra"},
		{ID: 2, Name: "Alpha"},
		{ID: 3, Name: "mongoose"},
		{ID: 4, Name: "Healthy"}, // not blocked
	}
	blocked := []notificationstatus.Status{
		{ProviderID: 1}, {ProviderID: 2}, {ProviderID: 3},
	}
	c := makeNotificationStatusCheck(t, enabled, blocked)
	got := c.Run(t.Context())
	// Look at the substring after the colon-space.
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

// TestNotificationStatusCheck_NotificationListerError surfaces a
// lookup error as Warning rather than panic. Mirrors the
// IndexerStatusCheck precedent.
func TestNotificationStatusCheck_NotificationListerError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewNotificationStatusCheck(
		stubEnabledNotificationLister{err: errors.New("db down")},
		stubNotificationStatusBlockedLister{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("notification lister error: want Warning, got %v: %s", got.Type, got.Message)
	}
}

// TestNotificationStatusCheck_StatusListerError surfaces a status-
// lookup error as Warning.
func TestNotificationStatusCheck_StatusListerError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewNotificationStatusCheck(
		stubEnabledNotificationLister{enabled: []healthcheck.EnabledNotification{{ID: 1, Name: "X"}}},
		stubNotificationStatusBlockedLister{err: errors.New("status broken")},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("status lister error: want Warning, got %v: %s", got.Type, got.Message)
	}
}

func TestNotificationStatusCheck_Metadata(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewNotificationStatusCheck(stubEnabledNotificationLister{}, stubNotificationStatusBlockedLister{})
	if c.Name() != healthcheck.NotificationStatusCheckName {
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
