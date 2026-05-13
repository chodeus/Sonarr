package notifications_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
)

// stubProvider implements [notifications.Provider] with no-op
// bodies — used for Registry round-trip tests and as a compile-
// time guard that the Provider interface is implementable from
// the test package (catches signature drift on hooks the
// shipping concrete providers will need to match).
type stubProvider struct {
	def *notifications.Notification
}

func (p *stubProvider) Name() string                            { return "Stub" }
func (p *stubProvider) Link() string                            { return "" }
func (p *stubProvider) Definition() *notifications.Notification { return p.def }
func (p *stubProvider) OnGrab(context.Context, notifications.GrabMessage) error {
	return nil
}
func (p *stubProvider) OnDownload(context.Context, notifications.DownloadMessage) error {
	return nil
}
func (p *stubProvider) OnRename(context.Context, notifications.RenameMessage) error {
	return nil
}
func (p *stubProvider) OnImportComplete(context.Context, notifications.ImportCompleteMessage) error {
	return nil
}
func (p *stubProvider) OnEpisodeFileDelete(context.Context, notifications.EpisodeDeleteMessage) error {
	return nil
}
func (p *stubProvider) OnSeriesAdd(context.Context, notifications.SeriesAddMessage) error {
	return nil
}
func (p *stubProvider) OnSeriesDelete(context.Context, notifications.SeriesDeleteMessage) error {
	return nil
}
func (p *stubProvider) OnHealthIssue(context.Context, notifications.HealthCheckMessage) error {
	return nil
}
func (p *stubProvider) OnHealthRestored(context.Context, notifications.HealthCheckMessage) error {
	return nil
}
func (p *stubProvider) OnApplicationUpdate(context.Context, notifications.ApplicationUpdateMessage) error {
	return nil
}
func (p *stubProvider) OnManualInteractionRequired(context.Context, notifications.ManualInteractionRequiredMessage) error {
	return nil
}
func (p *stubProvider) ProcessQueue(context.Context) error                        { return nil }
func (p *stubProvider) Test(context.Context) ([]notifications.TestFailure, error) { return nil, nil }
func (p *stubProvider) SupportsOnGrab() bool                                      { return true }
func (p *stubProvider) SupportsOnDownload() bool                                  { return true }
func (p *stubProvider) SupportsOnUpgrade() bool                                   { return true }
func (p *stubProvider) SupportsOnImportComplete() bool                            { return true }
func (p *stubProvider) SupportsOnRename() bool                                    { return false }
func (p *stubProvider) SupportsOnSeriesAdd() bool                                 { return false }
func (p *stubProvider) SupportsOnSeriesDelete() bool                              { return false }
func (p *stubProvider) SupportsOnEpisodeFileDelete() bool                         { return false }
func (p *stubProvider) SupportsOnEpisodeFileDeleteForUpgrade() bool               { return false }
func (p *stubProvider) SupportsOnHealthIssue() bool                               { return true }
func (p *stubProvider) SupportsOnHealthRestored() bool                            { return true }
func (p *stubProvider) SupportsOnApplicationUpdate() bool                         { return true }
func (p *stubProvider) SupportsOnManualInteractionRequired() bool                 { return true }
func (p *stubProvider) RequestAction(context.Context, string, map[string]string) (any, error) {
	return struct{}{}, nil
}

// Compile-time assertion: stubProvider satisfies notifications.Provider.
var _ notifications.Provider = (*stubProvider)(nil)

func newStubFactory() notifications.Constructor {
	return func(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
		return &stubProvider{def: def}, nil
	}
}

func TestRegistry_RegisterLookup(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	if err := r.Register("Webhook", "WebhookSettings", newStubFactory()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Lookup("Webhook", "WebhookSettings")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got == nil {
		t.Errorf("Lookup returned nil factory")
	}
	if !r.Has("Webhook", "WebhookSettings") {
		t.Errorf("Has=false after Register")
	}
}

func TestRegistry_Register_RejectsEmpty(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	if err := r.Register("", "Foo", newStubFactory()); err == nil {
		t.Errorf("empty implementation: expected error")
	}
	if err := r.Register("Foo", "", newStubFactory()); err == nil {
		t.Errorf("empty configContract: expected error")
	}
	if err := r.Register("Foo", "Bar", nil); err == nil {
		t.Errorf("nil factory: expected error")
	}
}

func TestRegistry_Register_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	_ = r.Register("Webhook", "WebhookSettings", newStubFactory())
	err := r.Register("Webhook", "WebhookSettings", newStubFactory())
	if !errors.Is(err, notifications.ErrAlreadyRegistered) {
		t.Errorf("duplicate register: got %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistry_Lookup_Missing(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	_, err := r.Lookup("Webhook", "WebhookSettings")
	if !errors.Is(err, notifications.ErrNotRegistered) {
		t.Errorf("missing lookup: got %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Replace(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	_ = r.Register("Webhook", "WebhookSettings", newStubFactory())

	called := false
	replacement := func(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
		called = true
		return &stubProvider{def: def}, nil
	}
	if err := r.Replace("Webhook", "WebhookSettings", replacement); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	f, _ := r.Lookup("Webhook", "WebhookSettings")
	if _, err := f(&notifications.Notification{}, notifications.Dependencies{}); err != nil {
		t.Fatalf("constructed: %v", err)
	}
	if !called {
		t.Errorf("replacement factory not invoked")
	}
}

func TestRegistry_Replace_Missing(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	err := r.Replace("Webhook", "WebhookSettings", newStubFactory())
	if !errors.Is(err, notifications.ErrNotRegistered) {
		t.Errorf("replace missing: got %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Names_Sorted(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	_ = r.Register("Webhook", "WebhookSettings", newStubFactory())
	_ = r.Register("Discord", "DiscordSettings", newStubFactory())
	_ = r.Register("Slack", "SlackSettings", newStubFactory())

	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("len(Names) = %d, want 3", len(names))
	}
	if names[0].Implementation() != "Discord" || names[1].Implementation() != "Slack" || names[2].Implementation() != "Webhook" {
		t.Errorf("Names not sorted: %v", names)
	}
	if names[0].String() != "Discord/DiscordSettings" {
		t.Errorf("String(): %q, want Discord/DiscordSettings", names[0].String())
	}
}

func TestRegistry_Construct_FromRow(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	contract := "WebhookSettings"
	_ = r.Register("Webhook", contract, newStubFactory())
	row := &notifications.Notification{
		ID:             7,
		Name:           "My Hook",
		Implementation: "Webhook",
		ConfigContract: &contract,
	}
	p, err := r.Construct(row, notifications.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Definition() != row {
		t.Errorf("Definition pointer not preserved")
	}
}

func TestRegistry_Construct_NilRow(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	if _, err := r.Construct(nil, notifications.Dependencies{}); err == nil {
		t.Errorf("nil row: expected error")
	}
}

func TestRegistry_Construct_NilConfigContract(t *testing.T) {
	t.Parallel()
	r := notifications.NewRegistry()
	_ = r.Register("Webhook", "", newStubFactory()) // empty contract is rejected
	row := &notifications.Notification{
		Implementation: "Webhook",
		ConfigContract: nil, // legacy v1-migration null
	}
	// With nil ConfigContract, lookup keys on ("Webhook", "") — and
	// since Register rejects empty contract, this lookup misses.
	if _, err := r.Construct(row, notifications.Dependencies{}); !errors.Is(err, notifications.ErrNotRegistered) {
		t.Errorf("nil contract: got %v, want ErrNotRegistered", err)
	}
}

// TestDefault verifies the package-global registry is reachable and
// distinct from a fresh NewRegistry instance.
func TestDefault(t *testing.T) {
	t.Parallel()
	if notifications.Default() == nil {
		t.Errorf("Default() returned nil")
	}
	fresh := notifications.NewRegistry()
	if notifications.Default() == fresh {
		t.Errorf("Default() returned the same pointer as NewRegistry()")
	}
}
