package trakt_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/notifications/trakt"
	"sonarr.tv/sonarr/internal/quality"
)

func TestMapMediaType(t *testing.T) {
	t.Parallel()
	cases := map[quality.QualitySource]string{
		quality.SourceWeb:           "digital",
		quality.SourceWebRip:        "digital",
		quality.SourceBluray:        "bluray",
		quality.SourceBlurayRaw:     "bluray",
		quality.SourceTelevision:    "vhs",
		quality.SourceTelevisionRaw: "vhs",
		quality.SourceDVD:           "dvd",
		quality.SourceUnknown:       "",
	}
	for src, want := range cases {
		if got := trakt.MapMediaType(src); got != want {
			t.Errorf("MapMediaType(%v)=%q want %q", src, got, want)
		}
	}
}

func TestMapResolution_ProgressiveAndInterlaced(t *testing.T) {
	t.Parallel()
	cases := []struct {
		res      int
		scanType string
		want     string
	}{
		{2160, "Progressive", "uhd_4k"},
		{2160, "Interlaced", "uhd_4k"}, // 2160 is always uhd_4k regardless of scan
		{1080, "", "hd_1080p"},
		{1080, "Progressive", "hd_1080p"},
		{1080, "Interlaced", "hd_1080i"},
		{1080, "MBAFF", "hd_1080i"},
		{1080, "PAFF", "hd_1080i"},
		{1080, "INTERLACED", "hd_1080i"}, // case-insensitive
		{720, "Interlaced", "hd_720p"},   // 720 is always p
		{576, "Progressive", "sd_576p"},
		{576, "Interlaced", "sd_576i"},
		{480, "MBAFF", "sd_480i"},
		{480, "", "sd_480p"},
		{360, "Progressive", ""}, // 360 not in the C# switch
		{0, "Progressive", ""},
	}
	for _, c := range cases {
		got := trakt.MapResolution(c.res, c.scanType)
		if got != c.want {
			t.Errorf("MapResolution(%d, %q)=%q want %q", c.res, c.scanType, got, c.want)
		}
	}
}

func TestMapHdr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hdr  mediainfo.HdrFormat
		want string
	}{
		{mediainfo.HdrDolbyVision, "dolby_vision"},
		{mediainfo.HdrDolbyVisionSdr, "dolby_vision"},
		{mediainfo.Hdr10, "hdr10"},
		{mediainfo.HdrDolbyVisionHdr10, "hdr10"},
		{mediainfo.Hdr10Plus, "hdr10_plus"},
		{mediainfo.HdrDolbyVisionHdr10Plus, "hdr10_plus"},
		{mediainfo.HdrHlg10, "hlg"},
		{mediainfo.HdrDolbyVisionHlg, "hlg"},
		{mediainfo.HdrNone, ""},
		{mediainfo.HdrUnknown, ""},
		{mediainfo.HdrPq10, ""},
	}
	for _, c := range cases {
		f := &episodefiles.File{MediaInfo: &mediainfo.Model{VideoHdrFormat: c.hdr}}
		if got := trakt.MapHdr(f); got != c.want {
			t.Errorf("MapHdr(%v)=%q want %q", c.hdr, got, c.want)
		}
	}
}

func TestMapHdr_NilSafety(t *testing.T) {
	t.Parallel()
	if got := trakt.MapHdr(nil); got != "" {
		t.Errorf("nil file: %q want empty", got)
	}
	if got := trakt.MapHdr(&episodefiles.File{}); got != "" {
		t.Errorf("nil MediaInfo: %q want empty", got)
	}
}

func TestMapAudio_FormatMapping(t *testing.T) {
	t.Parallel()
	// FormatAudioCodec consumes lowercase ffprobe-style format strings
	// and returns the human-friendly codec name; MapAudio then maps
	// that to the Trakt-specific value. Cover the round-trip from
	// ffprobe format → MapAudio output.
	cases := []struct {
		format  string
		codecID string
		profile string
		want    string
	}{
		{"ac3", "", "", "dolby_digital"},
		{"eac3", "", "", "dolby_digital_plus"},
		{"eac3", "ec+3", "", "dolby_digital_plus_atmos"},
		{"truehd", "", "", "dolby_truehd"},
		{"truehd", "thd+", "", "dolby_atmos"},
		{"dts", "", "", "dts"},
		{"dts", "", "DTS-ES", "dts"},
		{"dts", "", "DTS-HD MA", "dts_ma"},
		{"dts", "", "DTS-HD HRA", "dts_hr"},
		{"dts", "", "DTS:X", "dts_x"},
		{"mp3", "", "", "mp3"},
		{"mp2", "", "", "mp2"},
		{"vorbis", "", "", "ogg"},
		{"wmav2", "", "", "wma"},
		{"aac", "", "", "aac"},
		{"pcm_s16le", "", "", "lpcm"},
		{"flac", "", "", "flac"},
		{"opus", "", "", "ogg_opus"},
		{"unknown_codec_xyz", "", "", ""},
	}
	for _, c := range cases {
		f := &episodefiles.File{
			MediaInfo: &mediainfo.Model{
				AudioStreams: []mediainfo.AudioStream{{Format: c.format, CodecID: c.codecID, Profile: c.profile}},
			},
		}
		if got := trakt.MapAudio(f); got != c.want {
			t.Errorf("MapAudio(format=%q codecID=%q profile=%q)=%q want %q", c.format, c.codecID, c.profile, got, c.want)
		}
	}
}

func TestMapAudio_NilSafety(t *testing.T) {
	t.Parallel()
	if got := trakt.MapAudio(nil); got != "" {
		t.Errorf("nil file: %q want empty", got)
	}
	if got := trakt.MapAudio(&episodefiles.File{}); got != "" {
		t.Errorf("nil MediaInfo: %q want empty", got)
	}
	if got := trakt.MapAudio(&episodefiles.File{MediaInfo: &mediainfo.Model{}}); got != "" {
		t.Errorf("empty AudioStreams: %q want empty", got)
	}
}

func TestMapAudioChannels(t *testing.T) {
	t.Parallel()
	// FormatAudioChannels prefers the parsed ChannelPositions prefix
	// (e.g. "5.1 / 5.1 / ...") over the raw int Channels count. When
	// ChannelPositions is empty, falls back to float(Channels).
	cases := []struct {
		positions string
		channels  int
		want      string
	}{
		{"5.1 / 5.1", 6, "5.1"}, // ChannelPositions wins
		{"7.1 / 7.1", 8, "7.1"},
		{"", 2, "2.0"}, // fallback to Channels float
		{"", 6, "6.0"}, // fallback — no positions
		{"", 1, "1.0"},
	}
	for _, c := range cases {
		f := &episodefiles.File{
			MediaInfo: &mediainfo.Model{
				AudioStreams: []mediainfo.AudioStream{{Channels: c.channels, ChannelPositions: c.positions}},
			},
		}
		if got := trakt.MapAudioChannels(f, ""); got != c.want {
			t.Errorf("MapAudioChannels(positions=%q ch=%d)=%q want %q", c.positions, c.channels, got, c.want)
		}
	}
}

func TestMapAudioChannels_NilSafety(t *testing.T) {
	t.Parallel()
	if got := trakt.MapAudioChannels(nil, ""); got != "" {
		t.Errorf("nil file: %q want empty", got)
	}
	if got := trakt.MapAudioChannels(&episodefiles.File{}, ""); got != "" {
		t.Errorf("nil MediaInfo: %q want empty", got)
	}
}

func TestBuildEpisodeResource_PopulatesAllFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	f := &episodefiles.File{
		// Quality id 7 = Bluray1080p (Source=Bluray, Resolution=1080).
		Quality: `{"quality":7,"revision":{"version":1,"real":0,"isRepack":false}}`,
		MediaInfo: &mediainfo.Model{
			ScanType:       "Progressive",
			VideoHdrFormat: mediainfo.Hdr10,
			AudioStreams: []mediainfo.AudioStream{
				{Format: "ac3", Channels: 6, ChannelPositions: "5.1 / 5.1"},
			},
		},
	}
	er := trakt.BuildEpisodeResource(f, 7, true, now)
	if er.Number != 7 {
		t.Errorf("Number=%d", er.Number)
	}
	if er.CollectedAt == nil || !er.CollectedAt.Equal(now) {
		t.Errorf("CollectedAt=%v want %v", er.CollectedAt, now)
	}
	if er.Resolution != "hd_1080p" {
		t.Errorf("Resolution=%q", er.Resolution)
	}
	if er.MediaType != "bluray" {
		t.Errorf("MediaType=%q", er.MediaType)
	}
	if er.Hdr != "hdr10" {
		t.Errorf("Hdr=%q", er.Hdr)
	}
	if er.Audio != "dolby_digital" {
		t.Errorf("Audio=%q", er.Audio)
	}
	if er.AudioChannels != "5.1" {
		t.Errorf("AudioChannels=%q", er.AudioChannels)
	}
}

func TestBuildEpisodeResource_WithoutCollectedAt(t *testing.T) {
	t.Parallel()
	f := &episodefiles.File{
		Quality:   `{"quality":1}`, // SDTV (Source=Television, Resolution=480)
		MediaInfo: &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{{Format: "aac", Channels: 2}}},
	}
	er := trakt.BuildEpisodeResource(f, 3, false, time.Now())
	if er.CollectedAt != nil {
		t.Errorf("CollectedAt should be nil for remove path, got %v", er.CollectedAt)
	}
	if er.Number != 3 {
		t.Errorf("Number=%d", er.Number)
	}
	if er.Resolution != "sd_480p" {
		t.Errorf("Resolution=%q want sd_480p", er.Resolution)
	}
	if er.MediaType != "vhs" {
		t.Errorf("MediaType=%q want vhs (SourceTelevision)", er.MediaType)
	}
	if er.Audio != "aac" {
		t.Errorf("Audio=%q", er.Audio)
	}
}

func TestBuildEpisodeResource_NilFile(t *testing.T) {
	t.Parallel()
	er := trakt.BuildEpisodeResource(nil, 1, true, time.Now())
	if er.Number != 1 || er.CollectedAt == nil {
		t.Errorf("nil file should still yield Number + CollectedAt, got %+v", er)
	}
	if er.Resolution != "" || er.MediaType != "" || er.Hdr != "" || er.Audio != "" || er.AudioChannels != "" {
		t.Errorf("nil file should produce all-empty MediaInfo fields, got %+v", er)
	}
}

func TestBuildEpisodeResource_EmptyOrInvalidQuality(t *testing.T) {
	t.Parallel()
	cases := []string{"", "{}", "{not valid json"}
	for i, blob := range cases {
		f := &episodefiles.File{Quality: blob}
		er := trakt.BuildEpisodeResource(f, 1, false, time.Now())
		if er.Resolution != "" || er.MediaType != "" {
			t.Errorf("case %d (%q): expected empty Resolution/MediaType, got %+v", i, blob, er)
		}
	}
}
