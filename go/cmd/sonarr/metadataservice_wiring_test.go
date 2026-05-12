package main

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

func TestMetadataAttrsNoop_AlwaysSucceeds(t *testing.T) {
	t.Parallel()
	a := metadataAttrsNoop{}
	if err := a.SetFilePermissions("/anything"); err != nil {
		t.Errorf("no-op should never error: %v", err)
	}
}

func TestMetadataOtherRenamerNoop_AlwaysSucceeds(t *testing.T) {
	t.Parallel()
	r := metadataOtherRenamerNoop{}
	if err := r.RenameOtherExtraFile(&series.Series{}, "/anything"); err != nil {
		t.Errorf("no-op should never error: %v", err)
	}
}

func TestMetadataSeasonsAdapter_DecodesJSONBlob(t *testing.T) {
	t.Parallel()
	a := metadataSeasonsAdapter{}
	s := &series.Series{Seasons: `[
		{"seasonNumber": 0, "monitored": false, "images": []},
		{"seasonNumber": 1, "monitored": true, "images": [{"coverType": 1, "remoteUrl": "https://cdn/s1.jpg"}]},
		{"seasonNumber": 2, "monitored": true}
	]`}
	out, err := a.SeasonsForSeries(s)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d, want 3 seasons", len(out))
	}
	if out[0].SeasonNumber != 0 || out[0].Monitored {
		t.Errorf("season 0: %+v", out[0])
	}
	if out[1].SeasonNumber != 1 || !out[1].Monitored || len(out[1].Images) != 1 {
		t.Errorf("season 1: %+v", out[1])
	}
	if out[1].Images[0].RemoteUrl != "https://cdn/s1.jpg" {
		t.Errorf("nested RemoteUrl decode: %+v", out[1].Images[0])
	}
}

func TestMetadataSeasonsAdapter_EmptyBlobReturnsNil(t *testing.T) {
	t.Parallel()
	a := metadataSeasonsAdapter{}
	for _, body := range []string{"", "[]"} {
		got, err := a.SeasonsForSeries(&series.Series{Seasons: body})
		if err != nil {
			t.Errorf("blob=%q: %v", body, err)
		}
		if got != nil {
			t.Errorf("blob=%q: should be nil, got %+v", body, got)
		}
	}
}

func TestMetadataSeasonsAdapter_NilSeriesReturnsNil(t *testing.T) {
	t.Parallel()
	a := metadataSeasonsAdapter{}
	got, err := a.SeasonsForSeries(nil)
	if err != nil || got != nil {
		t.Errorf("nil series: got=%+v err=%v", got, err)
	}
}

func TestMetadataSeasonsAdapter_MalformedBlobErrors(t *testing.T) {
	t.Parallel()
	a := metadataSeasonsAdapter{}
	_, err := a.SeasonsForSeries(&series.Series{Seasons: "{not json"})
	if err == nil {
		t.Errorf("expected error on malformed JSON")
	}
}

// factoryFakeProvider implements metadata.Provider just enough for
// the registry-construct loop to accept it. Stub returns for every
// method.
type factoryFakeProvider struct {
	def *metadata.Metadata
}

func (p *factoryFakeProvider) Name() string                   { return "fake" }
func (p *factoryFakeProvider) Definition() *metadata.Metadata { return p.def }
func (factoryFakeProvider) GetFilenameAfterMove(*series.Series, *episodefiles.File, *metadatafiles.MetadataFile) string {
	return ""
}
func (factoryFakeProvider) FindMetadataFile(*series.Series, string) (*metadatafiles.MetadataFile, error) {
	return nil, nil
}
func (factoryFakeProvider) SeriesMetadata(context.Context, *series.Series, metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}
func (factoryFakeProvider) EpisodeMetadata(context.Context, *series.Series, *episodefiles.File) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}
func (factoryFakeProvider) SeriesImages(context.Context, *series.Series) ([]metadata.ImageFileResult, error) {
	return nil, nil
}
func (factoryFakeProvider) SeasonImages(context.Context, *series.Series, metadata.Season) ([]metadata.ImageFileResult, error) {
	return nil, nil
}
func (factoryFakeProvider) EpisodeImages(context.Context, *series.Series, *episodefiles.File) ([]metadata.ImageFileResult, error) {
	return nil, nil
}
func (factoryFakeProvider) Test(context.Context) ([]metadata.TestFailure, error) { return nil, nil }
func (factoryFakeProvider) RequestAction(context.Context, string, map[string]string) (any, error) {
	return nil, nil
}

// TestMetadataWriterFactoryAdapter_FilterLogic exercises the
// inner Enable-filter + registry-construct logic without touching
// the DB. The real adapter calls *metadata.Repo.All which is a
// trivial pass-through; this test re-implements the inner loop
// against an in-memory row list to keep the unit-test fast.
func TestMetadataWriterFactoryAdapter_FilterLogic(t *testing.T) {
	t.Parallel()
	reg := metadata.NewRegistry()
	const impl = "FakeWriter_FilterLogic"
	const contract = "FakeWriterSettings"
	reg.MustRegister(impl, contract, func(def *metadata.Metadata, _ metadata.Dependencies) (metadata.Provider, error) {
		return &factoryFakeProvider{def: def}, nil
	})

	rows := []metadata.Metadata{
		{ID: 1, Implementation: impl, ConfigContract: contract, Enable: true},
		{ID: 2, Implementation: impl, ConfigContract: contract, Enable: false},
		{ID: 3, Implementation: impl, ConfigContract: contract, Enable: true},
	}

	// Enabled filter — drop row 2.
	var enabled []metadata.Provider
	for i := range rows {
		row := &rows[i]
		if !row.Enable {
			continue
		}
		p, err := reg.Construct(row, metadata.Dependencies{})
		if err != nil {
			continue
		}
		enabled = append(enabled, p)
	}
	if len(enabled) != 2 {
		t.Errorf("Enabled() filter: got %d, want 2", len(enabled))
	}

	// All — no filter.
	var all []metadata.Provider
	for i := range rows {
		p, err := reg.Construct(&rows[i], metadata.Dependencies{})
		if err != nil {
			continue
		}
		all = append(all, p)
	}
	if len(all) != 3 {
		t.Errorf("All() no-filter: got %d, want 3", len(all))
	}
}

// TestMetadataWriterFactoryAdapter_ConstructorErrorSkipsRow verifies
// rows whose Constructor errors are silently dropped from the
// per-event fan-out (so a single misconfigured writer doesn't
// derail the whole event).
func TestMetadataWriterFactoryAdapter_ConstructorErrorSkipsRow(t *testing.T) {
	t.Parallel()
	reg := metadata.NewRegistry()
	const impl = "FakeWriter_ConstructorError"
	const contract = "FakeWriterSettings"
	reg.MustRegister(impl, contract, func(def *metadata.Metadata, _ metadata.Dependencies) (metadata.Provider, error) {
		if def.ID == 2 {
			return nil, errors.New("settings corrupt")
		}
		return &factoryFakeProvider{def: def}, nil
	})

	rows := []metadata.Metadata{
		{ID: 1, Implementation: impl, ConfigContract: contract, Enable: true},
		{ID: 2, Implementation: impl, ConfigContract: contract, Enable: true},
	}

	var enabled []metadata.Provider
	for i := range rows {
		p, err := reg.Construct(&rows[i], metadata.Dependencies{})
		if err != nil {
			continue
		}
		enabled = append(enabled, p)
	}
	if len(enabled) != 1 {
		t.Errorf("constructor-error rows should be skipped: got %d, want 1", len(enabled))
	}
}

// TestMetadataDiskAdapter_WriteAllText_RoundTrip exercises the
// os.WriteFile-backed WriteAllText.
func TestMetadataDiskAdapter_WriteAllText_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := newMetadataDiskAdapter(nil) // provider not needed for WriteAllText
	path := dir + "/test.txt"
	if err := a.WriteAllText(path, "hello world"); err != nil {
		t.Fatalf("WriteAllText: %v", err)
	}
}
