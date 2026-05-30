package xbmc_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/xbmc"
	"sonarr.tv/sonarr/internal/series"
)

// stubNfoFS satisfies xbmc.NfoFilesystem (the DiskProvider requirement of
// the bundle constructor).
type stubNfoFS struct{}

func (stubNfoFS) GetFileSize(string) (int64, error)  { return 0, nil }
func (stubNfoFS) ReadAllText(string) (string, error) { return "", nil }

// TestXbmc_Construct_MapCoversToLocalFlows_J472 pins the J472 wiring fix at
// the production construction seam: metadata.Default().Construct (→
// constructFromDependencies) casts deps.MapCoversToLocal to a
// CoverPathLookup. With the dep wired, SeriesImages emits the series cover
// images; with it nil (the pre-J472 main.go state), SeriesImages silently
// returns empty. The cast is non-panicking, so a missing dep fails
// SILENTLY — exactly what this guards.
func TestXbmc_Construct_MapCoversToLocalFlows_J472(t *testing.T) {
	t.Parallel()
	row := &metadata.Metadata{
		ID:             1,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
		// empty Settings → DefaultSettings (SeriesImages enabled).
	}
	covers := []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
		{CoverType: mediacover.CoverTypeFanart, RemoteUrl: "https://cdn/fanart.jpg"},
	}
	s := &series.Series{ID: 42, Images: mustMarshalCovers(t, covers)}

	// (1) MapCoversToLocal wired → series images present.
	p, err := metadata.Default().Construct(row, metadata.Dependencies{
		DiskProvider:     stubNfoFS{},
		MapCoversToLocal: fakeCoverPath{},
	})
	if err != nil {
		t.Fatalf("Construct (wired): %v", err)
	}
	imgs, err := p.SeriesImages(t.Context(), s)
	if err != nil {
		t.Fatalf("SeriesImages (wired): %v", err)
	}
	if len(imgs) == 0 {
		t.Fatal("MapCoversToLocal wired but SeriesImages empty — the cast/flow is broken")
	}

	// (2) MapCoversToLocal nil (pre-J472) → series images silently empty.
	p2, err := metadata.Default().Construct(row, metadata.Dependencies{DiskProvider: stubNfoFS{}})
	if err != nil {
		t.Fatalf("Construct (unwired): %v", err)
	}
	imgs2, err := p2.SeriesImages(t.Context(), s)
	if err != nil {
		t.Fatalf("SeriesImages (unwired): %v", err)
	}
	if len(imgs2) != 0 {
		t.Errorf("nil MapCoversToLocal should yield no series images, got %d", len(imgs2))
	}
}
