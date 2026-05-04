package mediainfo_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/mediainfo"
)

func TestHdrFormat_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    mediainfo.HdrFormat
		want string
	}{
		{mediainfo.HdrNone, "none"},
		{mediainfo.HdrUnknown, "unknownHdr"},
		{mediainfo.HdrPq10, "pq10"},
		{mediainfo.Hdr10, "hdr10"},
		{mediainfo.Hdr10Plus, "hdr10Plus"},
		{mediainfo.HdrHlg10, "hlg10"},
		{mediainfo.HdrDolbyVision, "dolbyVision"},
		{mediainfo.HdrDolbyVisionHdr10, "dolbyVisionHdr10"},
		{mediainfo.HdrDolbyVisionSdr, "dolbyVisionSdr"},
		{mediainfo.HdrDolbyVisionHlg, "dolbyVisionHlg"},
		{mediainfo.HdrDolbyVisionHdr10Plus, "dolbyVisionHdr10Plus"},
		{mediainfo.HdrFormat(999), "none"},
	}
	for _, c := range cases {
		if got := c.v.String(); got != c.want {
			t.Errorf("HdrFormat(%d).String() = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestHdrFormat_FrozenValues(t *testing.T) {
	t.Parallel()
	// Wire numbering must match the C# enum declaration order. If a
	// new value lands, append it — do NOT renumber.
	pins := map[mediainfo.HdrFormat]int{
		mediainfo.HdrNone:                 0,
		mediainfo.HdrUnknown:              1,
		mediainfo.HdrPq10:                 2,
		mediainfo.Hdr10:                   3,
		mediainfo.Hdr10Plus:               4,
		mediainfo.HdrHlg10:                5,
		mediainfo.HdrDolbyVision:          6,
		mediainfo.HdrDolbyVisionHdr10:     7,
		mediainfo.HdrDolbyVisionSdr:       8,
		mediainfo.HdrDolbyVisionHlg:       9,
		mediainfo.HdrDolbyVisionHdr10Plus: 10,
	}
	for v, want := range pins {
		if int(v) != want {
			t.Errorf("HdrFormat %s: got %d, want %d", v, int(v), want)
		}
	}
}

func TestHdrFormat_MarshalJSON(t *testing.T) {
	t.Parallel()
	out, err := json.Marshal(mediainfo.Hdr10Plus)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != `"hdr10Plus"` {
		t.Errorf("Marshal: got %s, want %q", out, `"hdr10Plus"`)
	}
}

func TestHdrFormat_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	cases := map[string]mediainfo.HdrFormat{
		`"none"`:                 mediainfo.HdrNone,
		`"hdr10"`:                mediainfo.Hdr10,
		`"hdr10Plus"`:            mediainfo.Hdr10Plus,
		`"dolbyVisionHdr10Plus"`: mediainfo.HdrDolbyVisionHdr10Plus,
		`"unknownEnumValue"`:     mediainfo.HdrNone, // unknown → none
		`null`:                   mediainfo.HdrNone,
		// Integer form (allowIntegerValues=true).
		`3`:  mediainfo.Hdr10,
		`10`: mediainfo.HdrDolbyVisionHdr10Plus,
	}
	for in, want := range cases {
		var got mediainfo.HdrFormat
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Errorf("Unmarshal(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Unmarshal(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestModel_RoundTrip_FullShape(t *testing.T) {
	t.Parallel()
	in := mediainfo.Model{
		RawStreamData:                `{"streams":[]}`,
		SchemaRevision:               11,
		ContainerFormat:              "Matroska",
		VideoFormat:                  "HEVC",
		VideoCodecID:                 "V_MPEGH/ISO/HEVC",
		VideoProfile:                 "Main 10@L5.1@High",
		VideoBitrate:                 12345678,
		VideoBitDepth:                10,
		VideoColourPrimaries:         "BT.2020",
		VideoTransferCharacteristics: "PQ",
		VideoHdrFormat:               mediainfo.Hdr10,
		Height:                       2160,
		Width:                        3840,
		RunTime:                      datastore.STDuration(42*time.Minute + 15*time.Second),
		VideoFps:                     23.976,
		AudioStreams: []mediainfo.AudioStream{
			{Language: "eng", Format: "DTS-HD MA", CodecID: "A_DTS", Channels: 6, Bitrate: 1500000, Title: "English"},
			{Language: "jpn", Format: "AC-3", Channels: 2},
		},
		SubtitleStreams: []mediainfo.SubtitleStream{
			{Language: "eng", Format: "PGS"},
		},
		ScanType: "Progressive",
	}
	raw, err := json.Marshal(&in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// VideoCodecID must serialise with the literal `videoCodecID`
	// key (not the more obvious `videoCodecId` — STJ camelCase only
	// lowercases the first character). EDOC-Q16.
	if !strings.Contains(string(raw), `"videoCodecID":"V_MPEGH/ISO/HEVC"`) {
		t.Errorf("Marshal: missing literal videoCodecID key in: %s", raw)
	}
	if !strings.Contains(string(raw), `"videoColourPrimaries":"BT.2020"`) {
		t.Errorf("Marshal: missing British-spelling videoColourPrimaries: %s", raw)
	}
	if !strings.Contains(string(raw), `"videoHdrFormat":"hdr10"`) {
		t.Errorf("Marshal: HDR form: %s", raw)
	}
	if !strings.Contains(string(raw), `"runTime":"00:42:15"`) {
		t.Errorf("Marshal: runTime form: %s", raw)
	}

	var out mediainfo.Model
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.VideoCodecID != in.VideoCodecID {
		t.Errorf("RoundTrip VideoCodecID: got %q, want %q", out.VideoCodecID, in.VideoCodecID)
	}
	if out.VideoHdrFormat != in.VideoHdrFormat {
		t.Errorf("RoundTrip VideoHdrFormat: got %v, want %v", out.VideoHdrFormat, in.VideoHdrFormat)
	}
	if out.RunTime != in.RunTime {
		t.Errorf("RoundTrip RunTime: got %v, want %v", out.RunTime, in.RunTime)
	}
	if len(out.AudioStreams) != 2 || out.AudioStreams[0].Channels != 6 {
		t.Errorf("RoundTrip AudioStreams: %+v", out.AudioStreams)
	}
	if len(out.SubtitleStreams) != 1 || out.SubtitleStreams[0].Language != "eng" {
		t.Errorf("RoundTrip SubtitleStreams: %+v", out.SubtitleStreams)
	}
}

func TestModel_RoundTrip_VideoCodecIDCasing(t *testing.T) {
	t.Parallel()
	// EDOC-Q16: WRITE must emit the literal `videoCodecID` key (only
	// the first character lowercased). READ is case-insensitive on
	// both sides — STJ's PropertyNameCaseInsensitive=true matches
	// Go's encoding/json default — so legacy/typo'd `videoCodecId`
	// payloads bind correctly. The output is what's pinned.
	in := mediainfo.Model{VideoCodecID: "X_OUT"}
	raw, _ := json.Marshal(&in)
	if !strings.Contains(string(raw), `"videoCodecID":"X_OUT"`) {
		t.Errorf("write: missing literal videoCodecID key in %s", raw)
	}
	if strings.Contains(string(raw), `"videoCodecId":`) {
		t.Errorf("write: must not emit lowercase videoCodecId in %s", raw)
	}
	// Read both casings (case-insensitive Go json + STJ parity).
	for _, payload := range []string{
		`{"videoCodecID":"A"}`,
		`{"videoCodecId":"B"}`, // typo'd / legacy lowercase
	} {
		var out mediainfo.Model
		if err := json.Unmarshal([]byte(payload), &out); err != nil {
			t.Errorf("Unmarshal(%q): %v", payload, err)
			continue
		}
		if out.VideoCodecID == "" {
			t.Errorf("Unmarshal(%q): VideoCodecID empty", payload)
		}
	}
}

func TestModel_PrimaryAudioStream(t *testing.T) {
	t.Parallel()
	// Empty / nil model.
	var nilModel *mediainfo.Model
	if got := nilModel.PrimaryAudioStream(); got != nil {
		t.Errorf("nil model: got %+v, want nil", got)
	}
	empty := &mediainfo.Model{}
	if got := empty.PrimaryAudioStream(); got != nil {
		t.Errorf("empty: got %+v, want nil", got)
	}

	// Single + multiple.
	single := &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{{Language: "eng"}}}
	if got := single.PrimaryAudioStream(); got == nil || got.Language != "eng" {
		t.Errorf("single: %+v", got)
	}

	multi := &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{
		{Language: "eng"},
		{Language: "jpn"},
	}}
	if got := multi.PrimaryAudioStream(); got == nil || got.Language != "eng" {
		t.Errorf("multi: got %+v, want eng", got)
	}

	// Returned value is a copy — mutating it doesn't affect the slice.
	if got := single.PrimaryAudioStream(); got != nil {
		got.Language = "MUTATED"
		if single.AudioStreams[0].Language != "eng" {
			t.Errorf("PrimaryAudioStream returned a reference to the underlying slice")
		}
	}
}

func TestSubtitleStream_NullableForced(t *testing.T) {
	t.Parallel()
	tru := true
	in := mediainfo.SubtitleStream{Language: "eng", Forced: &tru}
	raw, _ := json.Marshal(&in)
	if !strings.Contains(string(raw), `"forced":true`) {
		t.Errorf("forced=true: %s", raw)
	}
	// Round-trip nil → omitted.
	in2 := mediainfo.SubtitleStream{Language: "eng"}
	raw, _ = json.Marshal(&in2)
	if strings.Contains(string(raw), "forced") {
		t.Errorf("forced=nil should be omitted: %s", raw)
	}
}

func TestModel_OmitemptyTrimsZeros(t *testing.T) {
	t.Parallel()
	zero := mediainfo.Model{}
	raw, err := json.Marshal(&zero)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// All-zero struct should serialise to `{}` modulo any default-non-empty
	// fields. Currently every field has omitempty, so the empty struct
	// must produce an empty object.
	if string(raw) != "{}" {
		t.Errorf("zero Model: got %s, want {}", raw)
	}
}
