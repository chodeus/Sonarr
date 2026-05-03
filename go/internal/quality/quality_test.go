package quality_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

func TestFindByID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   int
		want quality.Quality
		ok   bool
	}{
		{0, quality.Unknown, true},
		{1, quality.SDTV, true},
		{3, quality.WEBDL1080p, true},
		{10, quality.RawHD, true},
		{20, quality.Bluray1080pRemux, true},
		{22, quality.Bluray576p, true},
		// Id 11 was retired (HDTV480p); should not resolve.
		{11, quality.Unknown, false},
		{99, quality.Unknown, false},
		{-1, quality.Unknown, false},
	}
	for _, c := range cases {
		got, ok := quality.FindByID(c.id)
		if got != c.want || ok != c.ok {
			t.Errorf("FindByID(%d) = (%+v, %v), want (%+v, %v)", c.id, got, ok, c.want, c.ok)
		}
	}
}

func TestAll_Count(t *testing.T) {
	t.Parallel()
	// 22 qualities: 21 active IDs (1-10, 12-22) plus Unknown (0).
	if got := len(quality.All()); got != 22 {
		t.Errorf("All() count = %d, want 22", got)
	}
}

// TestAll_Order pins the registration order. The C# tree's static
// ctor at Quality.cs:128-152 establishes this order, which is what
// the UI reads. Reordering would change quality-profile defaults.
func TestAll_Order(t *testing.T) {
	t.Parallel()
	want := []quality.Quality{
		quality.Unknown,
		quality.SDTV,
		quality.DVD,
		quality.WEBRip480p,
		quality.WEBDL480p,
		quality.Bluray480p,
		quality.Bluray576p,
		quality.HDTV720p,
		quality.WEBRip720p,
		quality.WEBDL720p,
		quality.Bluray720p,
		quality.Bluray1080p,
		quality.HDTV1080p,
		quality.WEBRip1080p,
		quality.WEBDL1080p,
		quality.RawHD,
		quality.HDTV2160p,
		quality.WEBRip2160p,
		quality.WEBDL2160p,
		quality.Bluray2160p,
		quality.Bluray1080pRemux,
		quality.Bluray2160pRemux,
	}
	got := quality.All()
	if len(got) != len(want) {
		t.Fatalf("All() length %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("All()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestQuality_FrozenIDs pins a representative set of (Id, Name)
// pairs. The persisted-Id contract means renumbering any of these
// would corrupt user databases.
func TestQuality_FrozenIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   int
		name string
	}{
		{0, "Unknown"},
		{1, "SDTV"},
		{2, "DVD"},
		{3, "WEBDL-1080p"},
		{10, "Raw-HD"},
		{20, "Bluray-1080p Remux"},
		{22, "Bluray-576p"},
	}
	for _, c := range cases {
		got, ok := quality.FindByID(c.id)
		if !ok {
			t.Errorf("FindByID(%d) missing", c.id)
			continue
		}
		if got.Name != c.name {
			t.Errorf("Id %d Name = %q, want %q", c.id, got.Name, c.name)
		}
	}
}

func TestQuality_SourceClassification(t *testing.T) {
	t.Parallel()
	// Sanity-check a few rows' Source / Resolution values to confirm
	// the table is consistent with the C# Quality.cs:77-124 declarations.
	cases := []struct {
		q              quality.Quality
		wantSource     quality.QualitySource
		wantResolution int
	}{
		{quality.SDTV, quality.SourceTelevision, 480},
		{quality.HDTV720p, quality.SourceTelevision, 720},
		{quality.WEBDL2160p, quality.SourceWeb, 2160},
		{quality.WEBRip1080p, quality.SourceWebRip, 1080},
		{quality.Bluray2160pRemux, quality.SourceBlurayRaw, 2160},
		{quality.RawHD, quality.SourceTelevisionRaw, 1080},
		{quality.DVD, quality.SourceDVD, 480},
	}
	for _, c := range cases {
		if c.q.Source != c.wantSource {
			t.Errorf("%s.Source = %d, want %d", c.q.Name, c.q.Source, c.wantSource)
		}
		if c.q.Resolution != c.wantResolution {
			t.Errorf("%s.Resolution = %d, want %d", c.q.Name, c.q.Resolution, c.wantResolution)
		}
	}
}
