package v3

import (
	"encoding/json"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

// J325 wire-shape pin for the typed QualityValue + RevisionValue +
// QualityModelResource wrapping. Verifies the wire shape matches
// C# QualityModel under STJson (`{quality:{id,name,source,resolution},
// revision:{version,real,isRepack}}`). Tests both the type marshal
// and the ToQualityModelResource projection from persisted
// [quality.Model].
func TestQualityModelResource_WireShape_J325(t *testing.T) {
	t.Parallel()
	// Hand-construct a populated QualityModelResource.
	res := QualityModelResource{
		Quality:  QualityValue{ID: 4, Name: "HDTV-720p", Source: "television", Resolution: 720},
		Revision: RevisionValue{Version: 1, Real: 0, IsRepack: false},
	}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"quality":{`,
		`"id":4`,
		`"name":"HDTV-720p"`,
		`"source":"television"`,
		`"resolution":720`,
		`"revision":{`,
		`"version":1`,
		`"real":0`,
		`"isRepack":false`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// All fields no-omitempty — zero values still emit.
	zero, err := json.Marshal(QualityModelResource{})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	zeroGot := string(zero)
	zeroMustContain := []string{
		`"quality":{"id":0,"name":"","source":"","resolution":0}`,
		`"revision":{"version":0,"real":0,"isRepack":false}`,
	}
	for _, want := range zeroMustContain {
		if !strings.Contains(zeroGot, want) {
			t.Errorf("zero-value missing %q in %s", want, zeroGot)
		}
	}
}

// J325 helper pin: ToQualityModelResource projects a persisted
// quality.Model into the expanded wire form. Verifies the
// QualitySource camelCase conversion via QualitySourceName + the
// Revision pass-through.
func TestToQualityModelResource_PopulatedAndUnknown_J325(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          quality.Model
		wantQuality QualityValue
		wantRev     RevisionValue
	}{
		{
			name:        "HDTV-720p with default revision",
			in:          quality.Model{Quality: quality.HDTV720p, Revision: quality.Revision{Version: 1, Real: 0, IsRepack: false}},
			wantQuality: QualityValue{ID: 4, Name: "HDTV-720p", Source: "television", Resolution: 720},
			wantRev:     RevisionValue{Version: 1, Real: 0, IsRepack: false},
		},
		{
			name:        "WEBDL-1080p with proper revision",
			in:          quality.Model{Quality: quality.WEBDL1080p, Revision: quality.Revision{Version: 2, Real: 1, IsRepack: true}},
			wantQuality: QualityValue{ID: 3, Name: "WEBDL-1080p", Source: "web", Resolution: 1080},
			wantRev:     RevisionValue{Version: 2, Real: 1, IsRepack: true},
		},
		{
			name:        "Unknown quality (zero-value)",
			in:          quality.Model{Quality: quality.Unknown, Revision: quality.Revision{Version: 1}},
			wantQuality: QualityValue{ID: 0, Name: "Unknown", Source: "unknown", Resolution: 0},
			wantRev:     RevisionValue{Version: 1, Real: 0, IsRepack: false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToQualityModelResource(tc.in)
			if got == nil {
				t.Fatalf("ToQualityModelResource returned nil")
			}
			if got.Quality != tc.wantQuality {
				t.Errorf("Quality: got %+v want %+v", got.Quality, tc.wantQuality)
			}
			if got.Revision != tc.wantRev {
				t.Errorf("Revision: got %+v want %+v", got.Revision, tc.wantRev)
			}
		})
	}
}

// J325 wire-shape pin for the Quality *QualityModelResource field
// widening across the 3 carrier resources (History/Blocklist/Queue).
// All three use omitempty pointer: nil drops the key (matches C#
// `QualityModel?` null under WhenWritingNull); non-nil emits the
// expanded `{quality:{...},revision:{...}}` envelope.
func TestCarrierResources_QualityField_J325(t *testing.T) {
	t.Parallel()
	qmr := ToQualityModelResource(quality.Model{Quality: quality.HDTV720p, Revision: quality.Revision{Version: 1}})

	t.Run("HistoryResource nil omits + populated emits", func(t *testing.T) {
		nilCase := HistoryResource{ID: 1, SourceTitle: "x", CustomFormats: []CustomFormatResource{}}
		buf, _ := json.Marshal(nilCase)
		if strings.Contains(string(buf), `"quality":`) {
			t.Errorf("nil Quality must omit; got %s", buf)
		}
		populated := HistoryResource{ID: 1, SourceTitle: "x", Quality: qmr, CustomFormats: []CustomFormatResource{}}
		buf2, _ := json.Marshal(populated)
		if !strings.Contains(string(buf2), `"quality":{"quality":{"id":4`) {
			t.Errorf("populated Quality envelope missing; got %s", buf2)
		}
	})
	t.Run("BlocklistResource nil omits + populated emits", func(t *testing.T) {
		nilCase := BlocklistResource{ID: 1, SourceTitle: "x", EpisodeIDs: []int{}, CustomFormats: []CustomFormatResource{}}
		buf, _ := json.Marshal(nilCase)
		if strings.Contains(string(buf), `"quality":`) {
			t.Errorf("nil Quality must omit; got %s", buf)
		}
		populated := BlocklistResource{ID: 1, SourceTitle: "x", EpisodeIDs: []int{}, Quality: qmr, CustomFormats: []CustomFormatResource{}}
		buf2, _ := json.Marshal(populated)
		if !strings.Contains(string(buf2), `"quality":{"quality":{"id":4`) {
			t.Errorf("populated Quality envelope missing; got %s", buf2)
		}
	})
	t.Run("QueueResource nil omits + populated emits", func(t *testing.T) {
		nilCase := QueueResource{ID: 1, Title: "x"}
		buf, _ := json.Marshal(nilCase)
		if strings.Contains(string(buf), `"quality":`) {
			t.Errorf("nil Quality must omit; got %s", buf)
		}
		populated := QueueResource{ID: 1, Title: "x", Quality: qmr}
		buf2, _ := json.Marshal(populated)
		if !strings.Contains(string(buf2), `"quality":{"quality":{"id":4`) {
			t.Errorf("populated Quality envelope missing; got %s", buf2)
		}
	})
}
