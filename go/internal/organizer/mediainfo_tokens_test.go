package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/organizer"
)

func resolveToken(t *testing.T, handlers map[string]organizer.TokenHandler, name string) string {
	t.Helper()
	h, ok := handlers[organizer.NormalizeTokenKey(name)]
	if !ok {
		t.Fatalf("%s: not registered", name)
	}
	return h(organizer.TokenMatch{})
}

func TestRegisterMediaInfoTokens_NilModelMatchesStubs(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, nil, "")
	// Every token must register and return empty.
	for _, name := range []string{
		"MediaInfo Video", "MediaInfo VideoCodec", "MediaInfo VideoBitDepth",
		"MediaInfo VideoDynamicRange", "MediaInfo VideoDynamicRangeType",
		"MediaInfo Audio", "MediaInfo AudioCodec", "MediaInfo AudioChannels",
		"MediaInfo AudioLanguages", "MediaInfo AudioLanguagesAll",
		"MediaInfo SubtitleLanguages", "MediaInfo SubtitleLanguagesAll",
		"MediaInfo Simple", "MediaInfo Full",
	} {
		if got := resolveToken(t, handlers, name); got != "" {
			t.Errorf("%s: got %q want empty", name, got)
		}
	}
}

func TestRegisterMediaInfoTokens_RealModel(t *testing.T) {
	t.Parallel()
	model := &mediainfo.Model{
		VideoFormat:    "hevc",
		VideoCodecID:   "V_MPEGH/ISO/HEVC",
		VideoBitDepth:  10,
		VideoHdrFormat: mediainfo.Hdr10Plus,
		AudioStreams: []mediainfo.AudioStream{
			{
				Format:           "eac3",
				CodecID:          "ec+3",
				ChannelPositions: "5.1 / Side: L R",
				Channels:         6,
				Language:         "eng",
			},
		},
	}
	scene := "Show.S01E01.2160p.HEVC.10bit.HDR10Plus.EAC3.Atmos-Group"

	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, scene)

	cases := map[string]string{
		"MediaInfo Video":                 "HEVC", // sceneName drives HEVC vs h265 vs x265
		"MediaInfo VideoCodec":            "HEVC",
		"MediaInfo VideoBitDepth":         "10",
		"MediaInfo Audio":                 "EAC3 Atmos",
		"MediaInfo AudioCodec":            "EAC3 Atmos",
		"MediaInfo AudioChannels":         "5.1",
		"MediaInfo VideoDynamicRange":     "HDR",
		"MediaInfo VideoDynamicRangeType": "HDR10Plus",
		"MediaInfo Simple":                "HEVC EAC3 Atmos",
		// Language tokens shipped in 3.7. Single English-only audio
		// stream → AudioLanguages skipped, AudioLanguagesAll = [EN].
		"MediaInfo AudioLanguages":       "",
		"MediaInfo AudioLanguagesAll":    "[EN]",
		"MediaInfo SubtitleLanguages":    "",
		"MediaInfo SubtitleLanguagesAll": "",
		// Full token: video + " " + audio + audioLangs + " " + subLangs.
		// AudioLanguages is empty (English-only skip) so the audioLangs
		// portion is empty; subLangs is empty (no streams).
		"MediaInfo Full": "HEVC EAC3 Atmos ",
	}
	for name, want := range cases {
		if got := resolveToken(t, handlers, name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestRegisterMediaInfoTokens_DefaultBitDepth(t *testing.T) {
	t.Parallel()
	// VideoBitDepth=0 → "8" default per FileNameBuilder.cs:668.
	model := &mediainfo.Model{VideoFormat: "h264", VideoCodecID: "x264"}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "")
	if got := resolveToken(t, handlers, "MediaInfo VideoBitDepth"); got != "8" {
		t.Errorf("MediaInfo VideoBitDepth (default): got %q, want 8", got)
	}
}

func TestRegisterMediaInfoTokens_EmptyAudioChannels(t *testing.T) {
	t.Parallel()
	// audioChannels resolves to 0 → empty string (per :669-671).
	model := &mediainfo.Model{
		VideoFormat: "h264",
		AudioStreams: []mediainfo.AudioStream{
			{Format: "aac"},
		},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "")
	if got := resolveToken(t, handlers, "MediaInfo AudioChannels"); got != "" {
		t.Errorf("MediaInfo AudioChannels: got %q, want empty (no positions, no Channels)", got)
	}
}

func TestRegisterMediaInfoTokens_NoAudioStreams(t *testing.T) {
	t.Parallel()
	model := &mediainfo.Model{VideoFormat: "h264", VideoCodecID: "x264"}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "")
	// Audio codec is empty (no stream).
	if got := resolveToken(t, handlers, "MediaInfo AudioCodec"); got != "" {
		t.Errorf("MediaInfo AudioCodec: got %q, want empty", got)
	}
	if got := resolveToken(t, handlers, "MediaInfo AudioChannels"); got != "" {
		t.Errorf("MediaInfo AudioChannels: got %q, want empty", got)
	}
	// Simple = `{video} ` (trailing space because audio is empty).
	if got := resolveToken(t, handlers, "MediaInfo Simple"); got != "x264 " {
		t.Errorf("MediaInfo Simple: got %q, want %q", got, "x264 ")
	}
}

func TestSceneOrFileName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		file *episodefiles.File
		want string
	}{
		{"nil", nil, ""},
		{"scene only", &episodefiles.File{SceneName: "Show.S01E01-Group"}, "Show.S01E01-Group"},
		{"relative falls through", &episodefiles.File{RelativePath: "Season 1/Show.S01E01.mkv"}, "Show.S01E01"},
		{"original falls through", &episodefiles.File{OriginalFilePath: "/tmp/Show.S01E01.mkv"}, "Show.S01E01"},
		{"scene wins over relative", &episodefiles.File{SceneName: "scene", RelativePath: "rel.mkv"}, "scene"},
		{"all empty", &episodefiles.File{}, ""},
		// filepath.Ext (and C# Path.GetFileNameWithoutExtension) treat
		// everything after the LAST dot as an extension. "Show.S01E01"
		// strips to "Show" — matches C# behaviour.
		{"trailing dot fragment treated as ext", &episodefiles.File{RelativePath: "Show.S01E01"}, "Show"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := organizer.SceneOrFileName(c.file); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRegisterMediaInfoStubs_BackwardsCompat(t *testing.T) {
	t.Parallel()
	// The deprecated-but-public RegisterMediaInfoStubs entry point
	// must still register every token to empty so any external
	// caller that depends on it (none in the tree, but the API is
	// public) continues to work.
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoStubs(handlers)
	if got := resolveToken(t, handlers, "MediaInfo VideoCodec"); got != "" {
		t.Errorf("RegisterMediaInfoStubs: got %q, want empty", got)
	}
}
