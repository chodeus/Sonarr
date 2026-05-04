package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/organizer"
)

func TestFormatLanguagesToken_Basic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		codes           []string
		filter          string
		skipEnglishOnly bool
		quoted          bool
		want            string
	}{
		{
			name:   "single English not skipped when skipEnglishOnly=false",
			codes:  []string{"eng"},
			quoted: true,
			want:   "[EN]",
		},
		{
			name:            "single English skipped when skipEnglishOnly=true",
			codes:           []string{"eng"},
			skipEnglishOnly: true,
			quoted:          true,
			want:            "",
		},
		{
			name:            "English-and-other not skipped",
			codes:           []string{"eng", "jpn"},
			skipEnglishOnly: true,
			quoted:          true,
			want:            "[EN+JA]",
		},
		{
			name:   "und filtered out",
			codes:  []string{"und", "jpn"},
			quoted: true,
			want:   "[JA]",
		},
		{
			name:   "duplicates collapsed in order",
			codes:  []string{"jpn", "eng", "jpn"},
			quoted: true,
			want:   "[JA+EN]",
		},
		{
			name:   "unknown code dropped",
			codes:  []string{"zzz", "eng"},
			quoted: true,
			want:   "[EN]",
		},
		{
			name:   "BT-mapped 3-letter (ger -> de -> DE)",
			codes:  []string{"ger"},
			quoted: true,
			want:   "[DE]",
		},
		{
			name:   "unquoted",
			codes:  []string{"eng", "jpn"},
			quoted: false,
			want:   "EN+JA",
		},
		{
			name:   "empty input",
			codes:  nil,
			quoted: true,
			want:   "",
		},
		{
			name:   "all 'und'",
			codes:  []string{"und", "und"},
			quoted: true,
			want:   "",
		},
		{
			name:   "whitespace trimmed",
			codes:  []string{"  eng  ", "\tjpn\n"},
			quoted: true,
			want:   "[EN+JA]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := organizer.FormatLanguagesToken(c.codes, c.filter, c.skipEnglishOnly, c.quoted)
			if got != c.want {
				t.Errorf("FormatLanguagesToken(%v, %q, %v, %v) = %q, want %q",
					c.codes, c.filter, c.skipEnglishOnly, c.quoted, got, c.want)
			}
		})
	}
}

func TestFormatLanguagesToken_Filter(t *testing.T) {
	t.Parallel()
	codes := []string{"eng", "jpn", "ger", "fra"} // EN, JA, DE, FR

	cases := []struct {
		name   string
		filter string
		want   string
	}{
		{"intersect EN+JA", "EN+JA", "[EN+JA]"},
		{"intersect single FR", "FR", "[FR]"},
		{"intersect with one absent code", "EN+ZZ", "[EN]"},
		{"intersect all-absent", "ZZ+YY", ""},
		{"exclude EN", "-EN", "[JA+DE+FR]"},
		{"exclude EN-JA", "-EN-JA", "[DE+FR]"},
		{"trailing + adds wildcard when filtered out anything",
			"EN+", "[EN+--]"},
		{"trailing + + intersect with no extras adds wildcard",
			"EN+JA+", "[EN+JA+--]"},
		{"empty filter", "", "[EN+JA+DE+FR]"},
		{"whitespace filter ignored", "   ", "[EN+JA+DE+FR]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := organizer.FormatLanguagesToken(codes, c.filter, false, true)
			if got != c.want {
				t.Errorf("filter=%q: got %q, want %q", c.filter, got, c.want)
			}
		})
	}
}

func TestFormatLanguagesToken_TrailingPlusOnlyWhenSomethingExcluded(t *testing.T) {
	t.Parallel()
	// `EN+JA+` with codes [EN, JA] keeps everything → no wildcard.
	codes := []string{"eng", "jpn"}
	got := organizer.FormatLanguagesToken(codes, "EN+JA+", false, true)
	if got != "[EN+JA]" {
		t.Errorf("trailing-+ no exclusion: got %q, want [EN+JA]", got)
	}
}

func TestRegisterMediaInfoTokens_LanguageTokens(t *testing.T) {
	t.Parallel()
	model := &mediainfo.Model{
		VideoFormat: "h264",
		AudioStreams: []mediainfo.AudioStream{
			{Format: "ac3", Language: "eng", Channels: 2},
			{Language: "jpn"},
			{Language: "und"},
		},
		SubtitleStreams: []mediainfo.SubtitleStream{
			{Language: "eng"},
			{Language: "fra"},
		},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "")

	cases := map[string]string{
		// AudioLanguages: skipEnglishOnly=true. EN+JA both present → no skip.
		"MediaInfo AudioLanguages":    "[EN+JA]",
		"MediaInfo AudioLanguagesAll": "[EN+JA]",
		"MediaInfo SubtitleLanguages": "[EN+FR]",
		// SubtitleLanguagesAll uses the same args as SubtitleLanguages
		// in C# (`:684` is a duplicate of `:683`). Pin the dup.
		"MediaInfo SubtitleLanguagesAll": "[EN+FR]",
	}
	for name, want := range cases {
		got := resolveToken(t, handlers, name)
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestRegisterMediaInfoTokens_LanguageTokens_SkipsEnglishOnly(t *testing.T) {
	t.Parallel()
	// Only English audio → AudioLanguages renders empty (skipEnglishOnly).
	// AudioLanguagesAll still renders [EN].
	model := &mediainfo.Model{
		VideoFormat: "h264",
		AudioStreams: []mediainfo.AudioStream{
			{Format: "ac3", Language: "eng"},
		},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "")

	if got := resolveToken(t, handlers, "MediaInfo AudioLanguages"); got != "" {
		t.Errorf("AudioLanguages (English-only): got %q, want empty", got)
	}
	if got := resolveToken(t, handlers, "MediaInfo AudioLanguagesAll"); got != "[EN]" {
		t.Errorf("AudioLanguagesAll (English-only): got %q, want [EN]", got)
	}
}

func TestRegisterMediaInfoTokens_FullToken(t *testing.T) {
	t.Parallel()
	// Full token concatenates: video + " " + audio + audioLangs + " " + subLangs
	// (matches C# :688 exactly). Render against the same fixture as 3.6.
	model := &mediainfo.Model{
		VideoFormat:  "hevc",
		VideoCodecID: "V_MPEGH/ISO/HEVC",
		AudioStreams: []mediainfo.AudioStream{
			{Format: "eac3", CodecID: "ec+3", Language: "eng", Channels: 6},
			{Format: "aac", Language: "jpn"},
		},
		SubtitleStreams: []mediainfo.SubtitleStream{
			{Language: "eng"},
		},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "Show.S01E01.HEVC.EAC3.Atmos-Group")

	// Expected:
	//   video = "HEVC"
	//   audio (primary, eac3+ec+3) = "EAC3 Atmos"
	//   audioLangs (skipEnglishOnly=true, EN+JA) = "[EN+JA]"
	//   subLangs (skipEnglish=false, EN) = "[EN]"
	//   = "HEVC EAC3 Atmos[EN+JA] [EN]"
	got := resolveToken(t, handlers, "MediaInfo Full")
	want := "HEVC EAC3 Atmos[EN+JA] [EN]"
	if got != want {
		t.Errorf("MediaInfo Full: got %q, want %q", got, want)
	}
}

func TestRegisterMediaInfoTokens_FullToken_RespectsCustomFormat(t *testing.T) {
	t.Parallel()
	// CustomFormat on the TokenMatch should drive the language filter
	// for Full / Audio* / Subtitle* tokens. Unique to slice 3.7.
	model := &mediainfo.Model{
		VideoFormat: "h264",
		AudioStreams: []mediainfo.AudioStream{
			{Format: "ac3", Language: "eng"},
			{Format: "ac3", Language: "jpn"},
			{Format: "ac3", Language: "fra"},
		},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoTokens(handlers, model, "")

	// CustomFormat="-FR" on AudioLanguagesAll excludes FR.
	h := handlers[organizer.NormalizeTokenKey("MediaInfo AudioLanguagesAll")]
	if got := h(organizer.TokenMatch{CustomFormat: "-FR"}); got != "[EN+JA]" {
		t.Errorf("AudioLanguagesAll(-FR): got %q, want [EN+JA]", got)
	}
	// CustomFormat="EN+JA" intersects.
	if got := h(organizer.TokenMatch{CustomFormat: "EN+JA"}); got != "[EN+JA]" {
		t.Errorf("AudioLanguagesAll(EN+JA): got %q, want [EN+JA]", got)
	}
}
