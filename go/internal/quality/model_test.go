package quality_test

import (
	"encoding/json"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

func TestRevision_DefaultIsV1(t *testing.T) {
	t.Parallel()
	r := quality.NewRevision()
	if r.Version != 1 || r.Real != 0 || r.IsRepack {
		t.Errorf("default = %+v, want {1 0 false}", r)
	}
}

func TestRevision_Compare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b quality.Revision
		want int
	}{
		// Real dominates Version.
		{quality.Revision{Version: 1, Real: 1}, quality.Revision{Version: 99, Real: 0}, 1},
		{quality.Revision{Version: 99, Real: 0}, quality.Revision{Version: 1, Real: 1}, -1},
		// Equal Real falls through to Version.
		{quality.Revision{Version: 2}, quality.Revision{Version: 1}, 1},
		{quality.Revision{Version: 1}, quality.Revision{Version: 2}, -1},
		// Equal both → 0.
		{quality.Revision{Version: 1, Real: 0}, quality.Revision{Version: 1, Real: 0}, 0},
		{quality.Revision{Version: 5, Real: 2}, quality.Revision{Version: 5, Real: 2}, 0},
		// IsRepack is not part of ordering.
		{quality.Revision{Version: 1, IsRepack: true}, quality.Revision{Version: 1}, 0},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("(%+v).Compare(%+v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRevision_EqualIgnoresIsRepack(t *testing.T) {
	t.Parallel()
	a := quality.Revision{Version: 2, Real: 1, IsRepack: true}
	b := quality.Revision{Version: 2, Real: 1, IsRepack: false}
	if !a.Equal(b) {
		t.Errorf("Equal(repack vs non-repack): false, want true (IsRepack must not affect equality)")
	}
}

func TestRevision_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r    quality.Revision
		want string
	}{
		{quality.Revision{Version: 1}, "v1"},
		{quality.Revision{Version: 2}, "v2"},
		{quality.Revision{Version: 3, Real: 1}, "v3 Real:1"},
		{quality.Revision{Version: 1, Real: 0}, "v1"}, // Real=0 omitted
	}
	for _, c := range cases {
		if got := c.r.String(); got != c.want {
			t.Errorf("(%+v).String() = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestModel_Default(t *testing.T) {
	t.Parallel()
	m := quality.NewModel(quality.WEBDL1080p)
	if m.Quality != quality.WEBDL1080p {
		t.Errorf("Quality = %+v", m.Quality)
	}
	if m.Revision != quality.NewRevision() {
		t.Errorf("Revision = %+v, want default v1", m.Revision)
	}
	if m.SourceDetectionSource != quality.DetectionUnknown {
		t.Errorf("SourceDetectionSource = %d, want Unknown", m.SourceDetectionSource)
	}
}

func TestModel_String(t *testing.T) {
	t.Parallel()
	m := quality.NewModelWithRevision(quality.Bluray2160p, quality.Revision{Version: 2, Real: 1})
	want := "Bluray-2160p v2 Real:1"
	if got := m.String(); got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

// TestModel_Compare exercises the compare semantics from the C#
// QualityModel.cs:50-87 — definitionWeight first, then Revision.
func TestModel_Compare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b quality.Model
		want int
	}{
		// Higher Weight wins.
		{quality.NewModel(quality.WEBDL1080p), quality.NewModel(quality.HDTV720p), 1},
		{quality.NewModel(quality.HDTV720p), quality.NewModel(quality.WEBDL1080p), -1},
		// Tied Weight (WEBRip720p and WEBDL720p both Weight=10) → revision decides.
		{
			quality.NewModelWithRevision(quality.WEBRip720p, quality.Revision{Version: 2}),
			quality.NewModelWithRevision(quality.WEBDL720p, quality.Revision{Version: 1}),
			1,
		},
		{
			quality.NewModelWithRevision(quality.WEBRip720p, quality.Revision{Version: 1}),
			quality.NewModelWithRevision(quality.WEBDL720p, quality.Revision{Version: 1}),
			0,
		},
		// Equal everything.
		{quality.NewModel(quality.WEBDL1080p), quality.NewModel(quality.WEBDL1080p), 0},
		// Real dominates within revision.
		{
			quality.NewModelWithRevision(quality.WEBDL1080p, quality.Revision{Version: 1, Real: 1}),
			quality.NewModelWithRevision(quality.WEBDL1080p, quality.Revision{Version: 99, Real: 0}),
			1,
		},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("(%v).Compare(%v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestModel_EqualIgnoresDetectionSource(t *testing.T) {
	t.Parallel()
	a := quality.NewModel(quality.HDTV720p)
	a.SourceDetectionSource = quality.DetectionMediaInfo
	b := quality.NewModel(quality.HDTV720p)
	b.SourceDetectionSource = quality.DetectionName
	if !a.Equal(b) {
		t.Errorf("Equal must ignore DetectionSource")
	}
}

// TestModel_JSON_PersistedShape confirms the wire format defined
// in analysis/embedded-docs/catalogue.md § QualityModel:
//
//	{"quality": <id>, "revision": {"version":..,"real":..,"isRepack":..}}
//
// Quality is the bare Id, not the full object. DetectionSource
// fields are hidden.
func TestModel_JSON_PersistedShape(t *testing.T) {
	t.Parallel()
	m := quality.NewModelWithRevision(quality.Bluray1080p, quality.Revision{Version: 2, Real: 1, IsRepack: false})
	m.SourceDetectionSource = quality.DetectionMediaInfo
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"quality":7,"revision":{"version":2,"real":1,"isRepack":false}}`
	if string(out) != want {
		t.Errorf("Marshal:\n got %s\nwant %s", out, want)
	}
}

// TestModel_JSON_RoundTrip exercises Marshal → Unmarshal parity.
// Detection-source fields are runtime state and reset to Unknown
// on the round-trip (matches the C# JsonIgnore behaviour).
func TestModel_JSON_RoundTrip(t *testing.T) {
	t.Parallel()
	original := quality.NewModelWithRevision(quality.WEBDL2160p, quality.Revision{Version: 3, Real: 2, IsRepack: true})
	original.SourceDetectionSource = quality.DetectionMediaInfo
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded quality.Model
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Quality != original.Quality {
		t.Errorf("Quality round-trip: got %v, want %v", decoded.Quality, original.Quality)
	}
	if decoded.Revision != original.Revision {
		t.Errorf("Revision round-trip: got %+v, want %+v", decoded.Revision, original.Revision)
	}
	// Detection-source fields drop on round-trip.
	if decoded.SourceDetectionSource != quality.DetectionUnknown {
		t.Errorf("SourceDetectionSource leaked through JSON: %v", decoded.SourceDetectionSource)
	}
}

// TestModel_JSON_UnknownIdFallsBackToUnknown confirms the
// FindByID(0)→Unknown semantics: an unknown Quality id (e.g. 11,
// the retired HDTV480p) does NOT error but resolves to
// quality.Unknown so legacy persisted rows remain readable.
func TestModel_JSON_UnknownIdFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	const legacy = `{"quality":11,"revision":{"version":1,"real":0,"isRepack":false}}`
	var m quality.Model
	if err := json.Unmarshal([]byte(legacy), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Quality != quality.Unknown {
		t.Errorf("Unknown legacy id resolved to %+v, want Unknown", m.Quality)
	}
}

func TestQualityWeight(t *testing.T) {
	t.Parallel()
	// Pin a few weights from the C# default table.
	cases := []struct {
		q    quality.Quality
		want int
	}{
		{quality.Unknown, 1},
		{quality.SDTV, 2},
		{quality.HDTV720p, 7},
		{quality.WEBRip720p, 10}, // ties WEBDL720p
		{quality.WEBDL720p, 10},
		{quality.Bluray2160pRemux, 18},
	}
	for _, c := range cases {
		if got := quality.QualityWeight(c.q); got != c.want {
			t.Errorf("QualityWeight(%s) = %d, want %d", c.q.Name, got, c.want)
		}
	}
}
