package v3

import (
	"encoding/json"
	"strings"
	"testing"
)

// J330 wire-shape pin for the MediaInfo typed-DTO cluster
// (MediaInfoResource + MediaInfoAudioStreamResource +
// MediaInfoSubtitleStreamResource). Tests verify the union-shape
// (v3 flat audio + v5 typed streams co-exist on the shared DTO),
// the per-field omitempty contracts, and the pointer-to-slice
// 3-state distinction on AudioStreams + SubtitleStreams.
func TestMediaInfoResource_UnionWireShape_J330(t *testing.T) {
	t.Parallel()
	// Branch 1: zero-value MediaInfoResource — all fields omitempty,
	// all drop → wire emits "{}".
	zero := MediaInfoResource{}
	buf, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(buf) != `{}` {
		t.Errorf("zero MediaInfoResource must marshal to `{}`; got %s", buf)
	}
	// Branch 2: v3-shape populated (flat audio fields + flat subtitles).
	v3shape := MediaInfoResource{
		AudioBitrate:     192000,
		AudioChannels:    5.1,
		AudioCodec:       "AAC",
		AudioLanguages:   "English/Japanese",
		AudioStreamCount: 2,
		VideoBitDepth:    8,
		VideoBitrate:     5_000_000,
		VideoCodec:       "x264",
		VideoFps:         23.976,
		Resolution:       "1920x1080",
		RunTime:          "23:45",
		ScanType:         "Progressive",
		Subtitles:        "English/Spanish",
	}
	buf2, err := json.Marshal(v3shape)
	if err != nil {
		t.Fatalf("marshal v3 shape: %v", err)
	}
	got2 := string(buf2)
	mustContain := []string{
		`"audioBitrate":192000`,
		`"audioChannels":5.1`,
		`"audioCodec":"AAC"`,
		`"audioLanguages":"English/Japanese"`,
		`"audioStreamCount":2`,
		`"videoBitDepth":8`,
		`"videoBitrate":5000000`,
		`"videoCodec":"x264"`,
		`"videoFps":23.976`,
		`"resolution":"1920x1080"`,
		`"runTime":"23:45"`,
		`"scanType":"Progressive"`,
		`"subtitles":"English/Spanish"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got2, want) {
			t.Errorf("v3 shape missing %q in %s", want, got2)
		}
	}
	// Branch 3: v5-shape populated (typed stream lists, no flat fields).
	audio := []MediaInfoAudioStreamResource{
		{Language: "eng", Codec: "AAC", CodecID: "mp4a-40-2", Profile: "LC", Bitrate: 192000, Channels: 2.0, ChannelPositions: "L R", Title: "English Stereo"},
		{Language: "jpn", Codec: "DTS-HD MA", Bitrate: 3000000, Channels: 5.1},
	}
	forced := true
	subs := []MediaInfoSubtitleStreamResource{
		{Language: "eng", Format: "S_TEXT/UTF8", Title: "Eng SDH", Forced: nil, HearingImpaired: &forced},
		{Language: "spa", Format: "S_TEXT/UTF8"},
	}
	v5shape := MediaInfoResource{
		VideoBitDepth:   10,
		VideoCodec:      "x265",
		Resolution:      "3840x2160",
		AudioStreams:    &audio,
		SubtitleStreams: &subs,
	}
	buf3, err := json.Marshal(v5shape)
	if err != nil {
		t.Fatalf("marshal v5 shape: %v", err)
	}
	got3 := string(buf3)
	v5MustContain := []string{
		`"audioStreams":[`,
		`"codec":"AAC"`,
		`"codecId":"mp4a-40-2"`,
		`"channelPositions":"L R"`,
		`"title":"English Stereo"`,
		`"subtitleStreams":[`,
		`"format":"S_TEXT/UTF8"`,
		`"hearingImpaired":true`,
	}
	for _, want := range v5MustContain {
		if !strings.Contains(got3, want) {
			t.Errorf("v5 shape missing %q in %s", want, got3)
		}
	}
	// Defensive: the v3-only flat fields should NOT appear (all
	// zero-valued in the v5 shape).
	for _, key := range []string{`"audioBitrate":`, `"audioCodec":`, `"audioLanguages":`, `"subtitles":`} {
		if strings.Contains(got3, key) {
			t.Errorf("v5 shape leaked v3 flat field %q; got %s", key, got3)
		}
	}
	// Defensive: in subs entry 1 the Forced pointer is nil → "forced" key absent;
	// HearingImpaired pointer is non-nil-true → "hearingImpaired":true present.
	if strings.Contains(got3, `"forced":`) {
		t.Errorf("nil Forced must omit `\"forced\":`; got %s", got3)
	}
}

// J330 pointer-to-slice 3-state pin on AudioStreams + SubtitleStreams:
// nil → key absent; non-nil empty → emits `[]`; populated → typed
// list (covered in the union test).
func TestMediaInfoResource_AudioStreamsEmptySlice_J330(t *testing.T) {
	t.Parallel()
	empty := []MediaInfoAudioStreamResource{}
	emptySubs := []MediaInfoSubtitleStreamResource{}
	res := MediaInfoResource{AudioStreams: &empty, SubtitleStreams: &emptySubs}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	if !strings.Contains(got, `"audioStreams":[]`) {
		t.Errorf("empty non-nil AudioStreams must emit `\"audioStreams\":[]`; got %s", got)
	}
	if !strings.Contains(got, `"subtitleStreams":[]`) {
		t.Errorf("empty non-nil SubtitleStreams must emit `\"subtitleStreams\":[]`; got %s", got)
	}
}

// J330 wire-shape pin for the EpisodeFileResource.MediaInfo
// pointer-to-struct widening. Nil → key absent (production stub);
// populated → emits the full envelope.
func TestEpisodeFileResource_MediaInfoField_J330(t *testing.T) {
	t.Parallel()
	// Branch 1: nil MediaInfo (today's stub default) → omitempty drops.
	nilCase := EpisodeFileResource{ID: 1, SeriesID: 7, CustomFormats: []CustomFormatResource{}, Languages: []LanguageRef{}}
	buf, err := json.Marshal(nilCase)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if strings.Contains(string(buf), `"mediaInfo":`) {
		t.Errorf("nil MediaInfo must omit the key; got %s", buf)
	}
	// Branch 2: populated MediaInfo → emits nested envelope.
	populated := EpisodeFileResource{
		ID: 2, SeriesID: 7, CustomFormats: []CustomFormatResource{}, Languages: []LanguageRef{},
		MediaInfo: &MediaInfoResource{VideoCodec: "x264", Resolution: "1920x1080", AudioCodec: "AAC", AudioChannels: 2.0},
	}
	buf2, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf2)
	mustContain := []string{
		`"mediaInfo":{`,
		`"videoCodec":"x264"`,
		`"resolution":"1920x1080"`,
		`"audioCodec":"AAC"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated EpisodeFileResource.MediaInfo missing %q in %s", want, got)
		}
	}
}
