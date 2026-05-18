package downloadclients_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// fakeProvider satisfies [downloadclients.Provider] for registry tests.
// All methods return zero values or the per-field stub state.
type fakeProvider struct {
	name string
	def  *downloadclients.DownloadClient
}

func (p *fakeProvider) Name() string                                { return p.name }
func (p *fakeProvider) Protocol() indexers.DownloadProtocol         { return indexers.DownloadProtocolUsenet }
func (p *fakeProvider) Definition() *downloadclients.DownloadClient { return p.def }
func (p *fakeProvider) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", nil
}

func (p *fakeProvider) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, nil
}

func (p *fakeProvider) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

func (p *fakeProvider) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return nil
}

func (p *fakeProvider) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{}, nil
}

func (p *fakeProvider) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

func (p *fakeProvider) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	return nil, nil
}

func (p *fakeProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// fakeUsenetSettings + fakeTorrentSettings satisfy the marker
// interfaces. Used by interface-satisfaction smoke tests.
type fakeUsenetSettings struct {
	downloadclients.BaseUsenetSettings
}

func (fakeUsenetSettings) Validate() []downloadclients.TestFailure { return nil }

type fakeTorrentSettings struct {
	downloadclients.BaseTorrentSettings
}

func (fakeTorrentSettings) Validate() []downloadclients.TestFailure { return nil }

// Compile-time interface satisfaction guards. The actual assertions
// happen here; if these break the build, the surface has drifted.
var (
	_ downloadclients.Provider        = (*fakeProvider)(nil)
	_ downloadclients.Settings        = fakeUsenetSettings{}
	_ downloadclients.UsenetSettings  = fakeUsenetSettings{}
	_ downloadclients.Settings        = fakeTorrentSettings{}
	_ downloadclients.TorrentSettings = fakeTorrentSettings{}
)

// Registry tests cover the surface end-to-end: Register + duplicate
// rejection, Replace + ErrNotRegistered, Lookup, Has, Names, and the
// Construct shortcut.

func TestRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	called := 0
	factory := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		called++
		return &fakeProvider{name: "fake"}, nil
	}
	if err := r.Register("Sabnzbd", "SabnzbdSettings", factory); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Lookup("Sabnzbd", "SabnzbdSettings")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got == nil {
		t.Fatal("Lookup returned nil factory")
	}
	prov, err := got(nil, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("factory invoke: %v", err)
	}
	if prov.Name() != "fake" {
		t.Errorf("Name = %q, want fake", prov.Name())
	}
	if called != 1 {
		t.Errorf("factory called %d times, want 1", called)
	}
}

func TestRegistry_RegisterRejectsEmptyKeys(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	factory := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		return nil, nil
	}
	if err := r.Register("", "SabnzbdSettings", factory); err == nil {
		t.Error("empty implementation accepted; want error")
	}
	if err := r.Register("Sabnzbd", "", factory); err == nil {
		t.Error("empty configContract accepted; want error")
	}
	if err := r.Register("Sabnzbd", "SabnzbdSettings", nil); err == nil {
		t.Error("nil factory accepted; want error")
	}
}

func TestRegistry_RegisterRejectsDuplicates(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	factory := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		return nil, nil
	}
	if err := r.Register("Sabnzbd", "SabnzbdSettings", factory); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register("Sabnzbd", "SabnzbdSettings", factory)
	if !errors.Is(err, downloadclients.ErrAlreadyRegistered) {
		t.Errorf("second Register err = %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistry_LookupNotRegistered(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	_, err := r.Lookup("Sabnzbd", "SabnzbdSettings")
	if !errors.Is(err, downloadclients.ErrNotRegistered) {
		t.Errorf("Lookup err = %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Replace(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	originalCalled := 0
	original := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		originalCalled++
		return &fakeProvider{name: "original"}, nil
	}
	if err := r.Register("Sabnzbd", "SabnzbdSettings", original); err != nil {
		t.Fatalf("Register: %v", err)
	}

	replacementCalled := 0
	replacement := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		replacementCalled++
		return &fakeProvider{name: "replacement"}, nil
	}
	if err := r.Replace("Sabnzbd", "SabnzbdSettings", replacement); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, _ := r.Lookup("Sabnzbd", "SabnzbdSettings")
	prov, _ := got(nil, downloadclients.Dependencies{})
	if prov.Name() != "replacement" {
		t.Errorf("Name = %q, want replacement", prov.Name())
	}
	if originalCalled != 0 {
		t.Errorf("original called %d times after Replace, want 0", originalCalled)
	}
	if replacementCalled != 1 {
		t.Errorf("replacement called %d times, want 1", replacementCalled)
	}
}

func TestRegistry_ReplaceNotRegistered(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	factory := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		return nil, nil
	}
	err := r.Replace("Sabnzbd", "SabnzbdSettings", factory)
	if !errors.Is(err, downloadclients.ErrNotRegistered) {
		t.Errorf("Replace err = %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_HasAndNames(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	factory := func(*downloadclients.DownloadClient, downloadclients.Dependencies) (downloadclients.Provider, error) {
		return nil, nil
	}
	if r.Has("Sabnzbd", "SabnzbdSettings") {
		t.Error("Has returned true for unregistered key")
	}
	_ = r.Register("Sabnzbd", "SabnzbdSettings", factory)
	_ = r.Register("Transmission", "TransmissionSettings", factory)
	_ = r.Register("Vuze", "TransmissionSettings", factory) // shared contract

	if !r.Has("Sabnzbd", "SabnzbdSettings") {
		t.Error("Has = false after Register")
	}
	if !r.Has("Vuze", "TransmissionSettings") {
		t.Error("shared-contract pair not registered")
	}

	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("Names: got %d entries, want 3", len(names))
	}
	want := []string{
		"Sabnzbd/SabnzbdSettings",
		"Transmission/TransmissionSettings",
		"Vuze/TransmissionSettings",
	}
	for i, k := range names {
		if k.String() != want[i] {
			t.Errorf("Names[%d] = %s, want %s", i, k.String(), want[i])
		}
		if k.Implementation() == "" || k.ConfigContract() == "" {
			t.Errorf("Names[%d] has empty field: %+v", i, k)
		}
	}
}

func TestRegistry_Construct(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	factory := func(d *downloadclients.DownloadClient, _ downloadclients.Dependencies) (downloadclients.Provider, error) {
		return &fakeProvider{name: d.Name, def: d}, nil
	}
	_ = r.Register("Sabnzbd", "SabnzbdSettings", factory)

	row := &downloadclients.DownloadClient{
		ID:             7,
		Name:           "my-sab",
		Implementation: "Sabnzbd",
		ConfigContract: "SabnzbdSettings",
	}
	prov, err := r.Construct(row, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if prov.Name() != "my-sab" {
		t.Errorf("Name = %q, want my-sab", prov.Name())
	}
	if prov.Definition() != row {
		t.Error("Definition not propagated to provider")
	}
}

func TestRegistry_ConstructNilRow(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	_, err := r.Construct(nil, downloadclients.Dependencies{})
	if err == nil {
		t.Error("Construct(nil) accepted; want error")
	}
}

func TestRegistry_ConstructUnregistered(t *testing.T) {
	t.Parallel()
	r := downloadclients.NewRegistry()
	row := &downloadclients.DownloadClient{
		Implementation: "Sabnzbd",
		ConfigContract: "SabnzbdSettings",
	}
	_, err := r.Construct(row, downloadclients.Dependencies{})
	if !errors.Is(err, downloadclients.ErrNotRegistered) {
		t.Errorf("err = %v, want ErrNotRegistered", err)
	}
}

func TestDefaultGlobalRegistry(t *testing.T) {
	t.Parallel()
	// Default() returns a real registry; Names() returns a sorted
	// slice (may be empty before any client package registers).
	r := downloadclients.Default()
	if r == nil {
		t.Fatal("Default() returned nil")
	}
	// Smoke check Names doesn't panic on empty.
	_ = r.Names()
}

// MarkItemAsImported's typed-error contract.
func TestErrMarkAsImportedUnsupported(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "f"}
	err := p.MarkItemAsImported(context.Background(), nil)
	if !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}
