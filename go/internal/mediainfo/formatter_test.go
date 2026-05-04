package mediainfo_test

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/mediainfo"
)

func TestFormatAudioCodec(t *testing.T) {
	t.Parallel()
	const sceneDefault = "My.Series.S01E01-Sonarr"
	cases := []struct {
		format, codecID, profile string
		sceneName                string
		want                     string
	}{
		// Direct ports of FormatAudioCodecFixture.cs:14-45.
		{"mp2", "", "", "droned.s01e03.swedish.720p.hdtv.x264-prince", "MP2"},
		{"vorbis", "", "", "DB Super HDTV", "Vorbis"},
		{"pcm_s16le", "", "", "DW DVDRip XviD-idTV, ", "PCM"},
		{"truehd", "", "", "", "TrueHD"},
		{"truehd", "", "", "TrueHD", "TrueHD"},
		{"truehd", "thd+", "", "Atmos", "TrueHD Atmos"},
		{"truehd", "thd+", "", "TrueHD.Atmos.7.1", "TrueHD Atmos"},
		{"truehd", "thd+", "", "", "TrueHD Atmos"},
		{"truehd", "", "Dolby TrueHD + Dolby Atmos", "Atmos", "TrueHD Atmos"},
		{"truehd", "", "Dolby TrueHD + Dolby Atmos", "TrueHD.Atmos.7.1", "TrueHD Atmos"},
		{"truehd", "", "Dolby TrueHD + Dolby Atmos", "", "TrueHD Atmos"},
		{"wmav1", "", "", "Droned.wmv", "WMA"},
		{"wmav2", "", "", "B.N.S04E18.720p.WEB-DL", "WMA"},
		{"opus", "", "", "Roadkill Ep3x11 - YouTube.webm", "Opus"},
		{"mp3", "", "", "climbing.mp4", "MP3"},
		{"dts", "", "DTS-HD MA", "DTS-HD.MA", "DTS-HD MA"},
		{"dts", "", "DTS:X", "DTS-X", "DTS-X"},
		{"dts", "", "DTS-HD MA + DTS:X IMAX", "DTS-X", "DTS-X"},
		{"dts", "", "DTS-ES", "DTS-ES", "DTS-ES"},
		{"dts", "", "DTS-ES", "DTS", "DTS-ES"},
		{"dts", "", "DTS-HD HRA", "DTSHD-HRA", "DTS-HD HRA"},
		{"dts", "", "DTS", "DTS", "DTS"},
		{"eac3", "ec+3", "", "EAC3.Atmos", "EAC3 Atmos"},
		{"eac3", "", "Dolby Digital Plus + Dolby Atmos", "EAC3.Atmos", "EAC3 Atmos"},
		{"eac3", "", "", "DDP5.1", "EAC3"},
		{"ac3", "", "", "DD5.1", "AC3"},
		{"aac", "", "", "AAC2.0", "AAC"},
		{"aac", "", "HE-AAC", "AAC2.0", "HE-AAC"},
		{"aac", "", "xHE-AAC", "AAC2.0", "xHE-AAC"},
		{"adpcm_ima_qt", "", "", "Custom?", "PCM"},
		{"adpcm_ms", "", "", "Custom", "PCM"},
		// Default-fallback regression test:
		{"Other Audio Format", "Other Audio Codec", "", sceneDefault, "Other Audio Format"},
	}
	for _, c := range cases {
		stream := &mediainfo.AudioStream{
			Format:  c.format,
			CodecID: c.codecID,
			Profile: c.profile,
		}
		if got := mediainfo.FormatAudioCodec(stream, c.sceneName); got != c.want {
			t.Errorf("FormatAudioCodec({%q,%q,%q}, %q) = %q, want %q",
				c.format, c.codecID, c.profile, c.sceneName, got, c.want)
		}
	}

	// Empty / nil paths.
	if got := mediainfo.FormatAudioCodec(nil, ""); got != "" {
		t.Errorf("FormatAudioCodec(nil): got %q, want empty", got)
	}
	if got := mediainfo.FormatAudioCodec(&mediainfo.AudioStream{}, ""); got != "" {
		t.Errorf("FormatAudioCodec({}): got %q, want empty", got)
	}
}

func TestFormatVideoCodec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		format, codecID string
		sceneName       string
		want            string
	}{
		// Direct ports of FormatVideoCodecFixture.cs:11-39.
		{"mpeg2video", "", "Droned.S01E02.1080i.HDTV.DD5.1.MPEG2-NTb", "MPEG2"},
		{"mpeg2video", "", "", "MPEG2"},
		{"mpeg1video", "", "The.Simpsons.S13E04.INTERNAL-ANiVCD.mpg", "MPEG"},
		{"vc1", "WVC1", "B.N.S04E18.720p.WEB-DL", "VC1"},
		{"vc1", "V_MS/VFW/FOURCC/WVC1", "", "VC1"},
		{"vc1", "WMV3", "It's Always Sunny S07E13 The Gang's RevengeHDTV.XviD-2HD.avi", "VC1"},
		{"h264", "V.MPEG4/ISO/AVC", "pd.2015.S03E08.720p.iP.WEBRip.AAC2.0.H264-BTW", "h264"},
		{"h264", "V_MPEG4/ISO/AVC", "Resistance.2019.S01E03.1080p.RTE.WEB-DL.AAC2.0.x264-RTN", "x264"},
		{"wmv1", "WMV1", "Droned.wmv", "WMV"},
		{"wmv2", "WMV2", "Droned.wmv", "WMV"},
		{"mpeg4", "XVID", "", "XviD"},
		{"mpeg4", "DIVX", "", "DivX"},
		{"mpeg4", "divx", "", "DivX"},
		{"mpeg4", "DIV3", "spsm.dvdrip.divx.avi'.", "DivX"},
		{"msmpeg4", "DIV3", "Exit the Dragon, Enter the Tiger (1976) 360p MPEG Audio.avi", "DivX"},
		{"msmpeg4v2", "DIV3", "Exit the Dragon, Enter the Tiger (1976) 360p MPEG Audio.avi", "DivX"},
		{"msmpeg4v3", "DIV3", "Exit the Dragon, Enter the Tiger (1976) 360p MPEG Audio.avi", "DivX"},
		{"vp6f", "4", "Top Gear - S12E01 - Lorries - SD TV.flv", "VP6"},
		{"vp6", "4", "Top Gear - S12E01 - Lorries - SD TV.flv", "VP6"},
		{"vp7", "VP70", "Sweet Seymour.avi", "VP7"},
		{"vp8", "V_VP8", "Dick.mkv", "VP8"},
		{"vp9", "V_VP9", "Roadkill Ep3x11 - YouTube.webm", "VP9"},
		{"h264", "x264", "Ghost Advent - S04E05 - Stanley Hotel SDTV.avi", "x264"},
		{"hevc", "V_MPEGH/ISO/HEVC", "The BBT S11E12 The Matrimonial Metric 1080p 10bit AMZN WEB-DL", "h265"},
		{"mpeg4", "mp4v-20", "", ""},
		{"mpeg4", "XVID", "American.Chopper.S06E07.Mountain.Creek.Bike.DSR.XviD-KRS", "XviD"},
		{"rpza", "V_QUICKTIME", "Custom", ""},
		{"mpeg4", "FMP4", "", ""},
		{"mpeg4", "MP42", "", ""},
		{"mpeg4", "mp43", "Bubble.Guppies.S01E13.480p.WEB-DL.H.264-BTN-Custom", ""},

		// Fallback variants — fixture FormatVideoCodecFixture.cs:53-58.
		{"h264", "x264", "Some.Video.S01E01.h264", "x264"},
		{"hevc", "x265", "Some.Video.S01E01.h265", "x265"},
		{"h264", "", "Some.Video.S01E01.x264", "x264"},
		{"hevc", "", "Some.Video.S01E01.x265", "x265"},
		{"h264", "", "Some.Video.S01E01", "h264"},
		{"hevc", "", "Some.Video.S01E01", "h265"},
	}
	for _, c := range cases {
		m := &mediainfo.Model{VideoFormat: c.format, VideoCodecID: c.codecID}
		if got := mediainfo.FormatVideoCodec(m, c.sceneName); got != c.want {
			t.Errorf("FormatVideoCodec({%q,%q}, %q) = %q, want %q",
				c.format, c.codecID, c.sceneName, got, c.want)
		}
	}

	// Default-fallback (`should_return_VideoFormat_by_default`).
	if got := mediainfo.FormatVideoCodec(&mediainfo.Model{VideoFormat: "VideoCodec"}, ""); got != "VideoCodec" {
		t.Errorf("FormatVideoCodec default: got %q, want %q", got, "VideoCodec")
	}
	// Nil path.
	if got := mediainfo.FormatVideoCodec(nil, ""); got != "" {
		t.Errorf("FormatVideoCodec(nil): got %q, want empty", got)
	}
}

func TestFormatVideoDynamicRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hdr  mediainfo.HdrFormat
		want string
	}{
		{mediainfo.HdrNone, ""},
		{mediainfo.HdrHlg10, "HDR"},
		{mediainfo.HdrPq10, "HDR"},
		{mediainfo.Hdr10, "HDR"},
		{mediainfo.Hdr10Plus, "HDR"},
		{mediainfo.HdrDolbyVision, "HDR"},
		{mediainfo.HdrUnknown, "HDR"},
		{mediainfo.HdrDolbyVisionHdr10Plus, "HDR"},
	}
	for _, c := range cases {
		m := &mediainfo.Model{VideoHdrFormat: c.hdr, SchemaRevision: 8}
		if got := mediainfo.FormatVideoDynamicRange(m); got != c.want {
			t.Errorf("FormatVideoDynamicRange(%s) = %q, want %q", c.hdr, got, c.want)
		}
	}
	// Nil model returns empty.
	if got := mediainfo.FormatVideoDynamicRange(nil); got != "" {
		t.Errorf("FormatVideoDynamicRange(nil) = %q, want empty", got)
	}
}

func TestFormatVideoDynamicRangeType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hdr  mediainfo.HdrFormat
		want string
	}{
		{mediainfo.HdrNone, ""},
		{mediainfo.HdrHlg10, "HLG"},
		{mediainfo.HdrPq10, "PQ"},
		{mediainfo.Hdr10, "HDR10"},
		{mediainfo.Hdr10Plus, "HDR10Plus"},
		{mediainfo.HdrDolbyVision, "DV"},
		{mediainfo.HdrDolbyVisionHdr10, "DV HDR10"},
		{mediainfo.HdrDolbyVisionHdr10Plus, "DV HDR10Plus"},
		{mediainfo.HdrDolbyVisionHlg, "DV HLG"},
		{mediainfo.HdrDolbyVisionSdr, "DV SDR"},
		{mediainfo.HdrUnknown, "HDR"},
	}
	for _, c := range cases {
		m := &mediainfo.Model{VideoHdrFormat: c.hdr, SchemaRevision: 9}
		if got := mediainfo.FormatVideoDynamicRangeType(m); got != c.want {
			t.Errorf("FormatVideoDynamicRangeType(%s) = %q, want %q", c.hdr, got, c.want)
		}
	}
	if got := mediainfo.FormatVideoDynamicRangeType(nil); got != "" {
		t.Errorf("FormatVideoDynamicRangeType(nil) = %q, want empty", got)
	}
}

func TestFormatAudioChannels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		positions string
		channels  int
		want      float64
	}{
		// Position-prefix takes precedence.
		{"5.1 / Side: L R", 6, 5.1},
		{"7.1", 8, 7.1},
		{"2.0", 2, 2.0},
		// No position-prefix → fall back to integer Channels field.
		{"", 6, 6},
		{"Front: L C R", 5, 5},
		// Trailing-only digit (not the prefix shape) — also falls back.
		{"channels=5.1", 7, 7},
	}
	for _, c := range cases {
		stream := &mediainfo.AudioStream{ChannelPositions: c.positions, Channels: c.channels}
		if got := mediainfo.FormatAudioChannels(stream); got != c.want {
			t.Errorf("FormatAudioChannels({%q, %d}) = %v, want %v",
				c.positions, c.channels, got, c.want)
		}
	}
	if got := mediainfo.FormatAudioChannels(nil); got != 0 {
		t.Errorf("FormatAudioChannels(nil) = %v, want 0", got)
	}
}

// TestFormatAudioCodec_NilProfileAndCodecID verifies that the C# nil-
// safe handling of `audioCodecId ?? string.Empty` and
// `audioProfile ?? string.Empty` is preserved — the Go port uses
// empty strings throughout but the test catches any future regression
// where someone introduces nil-pointer access.
func TestFormatAudioCodec_NilProfileAndCodecID(t *testing.T) {
	t.Parallel()
	stream := &mediainfo.AudioStream{Format: "dts"} // CodecID + Profile zero values
	if got := mediainfo.FormatAudioCodec(stream, ""); got != "DTS" {
		t.Errorf("dts default profile: got %q, want %q", got, "DTS")
	}
}

// Sanity check — getSceneNameMatch tokens last-default behavior is
// observable through FormatVideoCodec's hevc + empty-scene path.
func TestFormatVideoCodec_HevcEmptySceneFallsToLastToken(t *testing.T) {
	t.Parallel()
	got := mediainfo.FormatVideoCodec(&mediainfo.Model{VideoFormat: "hevc"}, "")
	if !strings.EqualFold(got, "h265") {
		t.Errorf("hevc/empty: got %q, want h265", got)
	}
}
