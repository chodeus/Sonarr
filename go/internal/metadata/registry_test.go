package metadata_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

func stubConstructor(name string) metadata.Constructor {
	return func(def *metadata.Metadata, _ metadata.Dependencies) (metadata.Provider, error) {
		return &stubProvider{name: name, definition: def}, nil
	}
}

type stubProvider struct {
	name       string
	definition *metadata.Metadata
}

func (p *stubProvider) Name() string                   { return p.name }
func (p *stubProvider) Definition() *metadata.Metadata { return p.definition }
func (p *stubProvider) GetFilenameAfterMove(_ *series.Series, _ *episodefiles.File, _ *metadatafiles.MetadataFile) string {
	return ""
}

func (p *stubProvider) FindMetadataFile(_ *series.Series, _ string) (*metadatafiles.MetadataFile, error) {
	return nil, nil
}

func (p *stubProvider) SeriesMetadata(_ context.Context, _ *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

func (p *stubProvider) EpisodeMetadata(_ context.Context, _ *series.Series, _ *episodefiles.File) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

func (p *stubProvider) SeriesImages(_ context.Context, _ *series.Series) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

func (p *stubProvider) SeasonImages(_ context.Context, _ *series.Series, _ metadata.Season) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

func (p *stubProvider) EpisodeImages(_ context.Context, _ *series.Series, _ *episodefiles.File) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

func (p *stubProvider) Test(_ context.Context) ([]metadata.TestFailure, error) {
	return nil, nil
}

func (p *stubProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	if err := r.Register("XbmcMetadata", "XbmcMetadataSettings", stubConstructor("Kodi")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Has("XbmcMetadata", "XbmcMetadataSettings") {
		t.Errorf("Has reported false after Register")
	}
	f, err := r.Lookup("XbmcMetadata", "XbmcMetadataSettings")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	p, err := f(&metadata.Metadata{ID: 1, Implementation: "XbmcMetadata", ConfigContract: "XbmcMetadataSettings"}, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if p.Name() != "Kodi" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestRegistry_RegisterRejectsEmpty(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	if err := r.Register("", "X", stubConstructor("x")); err == nil {
		t.Errorf("empty impl: expected error")
	}
	if err := r.Register("X", "", stubConstructor("x")); err == nil {
		t.Errorf("empty contract: expected error")
	}
	if err := r.Register("X", "Y", nil); err == nil {
		t.Errorf("nil factory: expected error")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	if err := r.Register("X", "Y", stubConstructor("first")); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := r.Register("X", "Y", stubConstructor("second"))
	if !errors.Is(err, metadata.ErrAlreadyRegistered) {
		t.Errorf("second: %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistry_MustRegister_PanicsOnDup(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	r.MustRegister("X", "Y", stubConstructor("first"))
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic on duplicate MustRegister")
		}
	}()
	r.MustRegister("X", "Y", stubConstructor("second"))
}

func TestRegistry_LookupMissing(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	_, err := r.Lookup("Missing", "Settings")
	if !errors.Is(err, metadata.ErrNotRegistered) {
		t.Errorf("Lookup missing: %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Replace(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	if err := r.Replace("X", "Y", stubConstructor("none")); !errors.Is(err, metadata.ErrNotRegistered) {
		t.Errorf("Replace before Register: %v, want ErrNotRegistered", err)
	}
	if err := r.Register("X", "Y", stubConstructor("first")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Replace("X", "Y", stubConstructor("second")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	f, _ := r.Lookup("X", "Y")
	p, _ := f(&metadata.Metadata{}, metadata.Dependencies{})
	if p.Name() != "second" {
		t.Errorf("after Replace Name() = %q, want %q", p.Name(), "second")
	}
	if err := r.Replace("X", "Y", nil); err == nil {
		t.Errorf("Replace with nil factory: expected error")
	}
}

func TestRegistry_Names(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	r.MustRegister("XbmcMetadata", "XbmcMetadataSettings", stubConstructor("Kodi"))
	r.MustRegister("PlexMetadata", "PlexMetadataSettings", stubConstructor("Plex"))
	r.MustRegister("RoksboxMetadata", "RoksboxMetadataSettings", stubConstructor("Roksbox"))
	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("Names() len = %d, want 3", len(names))
	}
	// Sorted by Implementation; assert pinned order.
	want := []string{"PlexMetadata", "RoksboxMetadata", "XbmcMetadata"}
	for i, n := range names {
		if n.Implementation != want[i] {
			t.Errorf("Names()[%d].Implementation = %q, want %q", i, n.Implementation, want[i])
		}
	}
	if got := names[0].String(); got != "PlexMetadata/PlexMetadataSettings" {
		t.Errorf("String() = %q", got)
	}
}

func TestRegistry_Construct(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	r.MustRegister("XbmcMetadata", "XbmcMetadataSettings", stubConstructor("Kodi"))

	row := &metadata.Metadata{ID: 7, Implementation: "XbmcMetadata", ConfigContract: "XbmcMetadataSettings"}
	p, err := r.Construct(row, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Name() != "Kodi" {
		t.Errorf("Name() = %q", p.Name())
	}
	if d := p.Definition(); d == nil || d.ID != 7 {
		t.Errorf("Definition wrong: %+v", d)
	}

	if _, err := r.Construct(nil, metadata.Dependencies{}); err == nil {
		t.Errorf("Construct(nil): expected error")
	}
	if _, err := r.Construct(&metadata.Metadata{Implementation: "X", ConfigContract: "Y"}, metadata.Dependencies{}); !errors.Is(err, metadata.ErrNotRegistered) {
		t.Errorf("Construct unknown: %v, want ErrNotRegistered", err)
	}
}

func TestRegistry_Default(t *testing.T) {
	t.Parallel()
	d1 := metadata.Default()
	d2 := metadata.Default()
	if d1 != d2 {
		t.Errorf("Default() returned different instances")
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	r := metadata.NewRegistry()
	const N = 64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Has("X", "Y")
		}()
	}
	r.MustRegister("X", "Y", stubConstructor("ok"))
	wg.Wait()
	if !r.Has("X", "Y") {
		t.Errorf("Has reported false after Register")
	}
}
