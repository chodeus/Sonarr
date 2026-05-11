package importlists_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// stubProvider implements [importlists.Provider] with no-op
// bodies — used for Registry round-trip tests and as a compile-
// time guard that the Provider interface is implementable from
// the test package (catches signature drift on hooks the
// shipping concrete providers will need to match).
type stubProvider struct {
	def *importlists.ImportList
}

func (p *stubProvider) Name() string                        { return "Stub" }
func (p *stubProvider) Definition() *importlists.ImportList { return p.def }
func (p *stubProvider) ListType() importlists.ListType      { return importlists.ListTypeOther }
func (p *stubProvider) MinRefreshInterval() time.Duration   { return 6 * time.Hour }
func (p *stubProvider) Fetch(context.Context) (importlists.FetchResult, error) {
	return importlists.FetchResult{}, nil
}
func (p *stubProvider) Test(context.Context) ([]importlists.TestFailure, error) {
	return nil, nil
}
func (p *stubProvider) RequestAction(context.Context, string, map[string]string) (any, error) {
	return nil, nil
}

// Compile-time assertion: stubProvider satisfies importlists.Provider.
var _ importlists.Provider = (*stubProvider)(nil)

func newStubFactory() importlists.Constructor {
	return func(def *importlists.ImportList, _ importlists.Dependencies) (importlists.Provider, error) {
		return &stubProvider{def: def}, nil
	}
}

func TestRegistry_RegisterLookup(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	if err := r.Register("SonarrImport", "SonarrSettings", newStubFactory()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Lookup("SonarrImport", "SonarrSettings")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got == nil {
		t.Errorf("Lookup returned nil factory")
	}
	if !r.Has("SonarrImport", "SonarrSettings") {
		t.Errorf("Has=false after Register")
	}
}

func TestRegistry_Register_RejectsEmpty(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
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
	r := importlists.NewRegistry()
	_ = r.Register("SonarrImport", "SonarrSettings", newStubFactory())
	err := r.Register("SonarrImport", "SonarrSettings", newStubFactory())
	if !errors.Is(err, importlists.ErrAlreadyRegistered) {
		t.Errorf("duplicate register: got %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistry_Lookup_Missing(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	_, err := r.Lookup("SonarrImport", "SonarrSettings")
	if !errors.Is(err, importlists.ErrNotRegistered) {
		t.Errorf("missing lookup: got %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Replace(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	_ = r.Register("SonarrImport", "SonarrSettings", newStubFactory())

	called := false
	replacement := func(def *importlists.ImportList, _ importlists.Dependencies) (importlists.Provider, error) {
		called = true
		return &stubProvider{def: def}, nil
	}
	if err := r.Replace("SonarrImport", "SonarrSettings", replacement); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	f, _ := r.Lookup("SonarrImport", "SonarrSettings")
	if _, err := f(&importlists.ImportList{}, importlists.Dependencies{}); err != nil {
		t.Fatalf("constructed: %v", err)
	}
	if !called {
		t.Errorf("replacement factory not invoked")
	}
}

func TestRegistry_Replace_Missing(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	err := r.Replace("SonarrImport", "SonarrSettings", newStubFactory())
	if !errors.Is(err, importlists.ErrNotRegistered) {
		t.Errorf("replace missing: got %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Names_Sorted(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	_ = r.Register("SonarrImport", "SonarrSettings", newStubFactory())
	_ = r.Register("PlexImport", "PlexListSettings", newStubFactory())
	_ = r.Register("TraktUserImport", "TraktUserSettings", newStubFactory())

	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("len(Names) = %d, want 3", len(names))
	}
	if names[0].Implementation() != "PlexImport" ||
		names[1].Implementation() != "SonarrImport" ||
		names[2].Implementation() != "TraktUserImport" {
		t.Errorf("Names not sorted: %v", names)
	}
	if names[0].String() != "PlexImport/PlexListSettings" {
		t.Errorf("String(): %q, want PlexImport/PlexListSettings", names[0].String())
	}
}

func TestRegistry_Construct_FromRow(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	contract := "SonarrSettings"
	_ = r.Register("SonarrImport", contract, newStubFactory())
	row := &importlists.ImportList{
		ID:             7,
		Name:           "Peer Sonarr",
		Implementation: "SonarrImport",
		ConfigContract: &contract,
	}
	p, err := r.Construct(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Definition() != row {
		t.Errorf("Definition pointer not preserved")
	}
}

func TestRegistry_Construct_NilRow(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	if _, err := r.Construct(nil, importlists.Dependencies{}); err == nil {
		t.Errorf("nil row: expected error")
	}
}

func TestRegistry_Construct_NilConfigContract(t *testing.T) {
	t.Parallel()
	r := importlists.NewRegistry()
	row := &importlists.ImportList{
		Implementation: "SonarrImport",
		ConfigContract: nil, // legacy v1-migration null
	}
	// With nil ConfigContract, lookup keys on ("SonarrImport", "") —
	// since Register rejects empty contract, this lookup misses.
	if _, err := r.Construct(row, importlists.Dependencies{}); !errors.Is(err, importlists.ErrNotRegistered) {
		t.Errorf("nil contract: got %v, want ErrNotRegistered", err)
	}
}

// TestDefault verifies the package-global registry is reachable and
// distinct from a fresh NewRegistry instance.
func TestDefault(t *testing.T) {
	t.Parallel()
	if importlists.Default() == nil {
		t.Errorf("Default() returned nil")
	}
	fresh := importlists.NewRegistry()
	if importlists.Default() == fresh {
		t.Errorf("Default() returned the same pointer as NewRegistry()")
	}
}
