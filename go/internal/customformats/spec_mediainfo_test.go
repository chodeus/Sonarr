package customformats

import (
	"testing"

	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
)

func makeAudioCodecSpec(t *testing.T, pattern string) *AudioCodecSpec {
	t.Helper()
	s := &AudioCodecSpec{}
	if err := s.SetPattern(pattern); err != nil {
		t.Fatalf("SetPattern(%q): %v", pattern, err)
	}
	return s
}

// HdrFormatSpec scores from the probed VideoHdrFormat, never the title.
func TestHdrFormatSpec_MatchesProbedFormat(t *testing.T) {
	f := makeFormat("HDR10", &HdrFormatSpec{Value: int(mediainfo.Hdr10)})

	// Probed HDR10 → matches.
	in := CustomFormatInput{MediaInfo: &mediainfo.Model{VideoHdrFormat: mediainfo.Hdr10}}
	if got := Calculate(in, []*Format{f}); len(got) != 1 {
		t.Fatalf("HDR10 file: expected match, got %d", len(got))
	}
	// Probed Dolby Vision → does NOT match an HDR10 spec.
	in = CustomFormatInput{MediaInfo: &mediainfo.Model{VideoHdrFormat: mediainfo.HdrDolbyVision}}
	if got := Calculate(in, []*Format{f}); len(got) != 0 {
		t.Fatalf("DV file: expected no match for HDR10 spec, got %d", len(got))
	}
}

// Pre-download (MediaInfo nil) a format carrying a MediaInfo condition is
// SKIPPED entirely — it neither matches nor inflates the grab score.
func TestMediaInfoSpec_InapplicablePreDownload(t *testing.T) {
	f := makeFormat("HDR10", &HdrFormatSpec{Value: int(mediainfo.Hdr10)})
	in := CustomFormatInput{MediaInfo: nil} // grab time
	if got := Calculate(in, []*Format{f}); len(got) != 0 {
		t.Fatalf("pre-download MediaInfo format must not match, got %d", len(got))
	}
}

// The operator's over-promise scenario: a "DV" format requires the title
// to mention DV AND the probe to be Dolby Vision. A release whose title
// claims DV but whose file is really HDR10 must NOT score the DV format —
// not pre-download (skipped), and not post-download (DV spec fails).
func TestMediaInfoSpec_OverPromiseScenario(t *testing.T) {
	dvFormat := makeFormat("DV REMUX",
		makeReleaseTitleSpec(t, `\bDV\b`),
		&HdrFormatSpec{Value: int(mediainfo.HdrDolbyVision)},
	)
	title := &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E01.2160p.DV.REMUX-GRP"}

	// Pre-download: title says DV, but MediaInfo unknown → format skipped
	// (no over-promise on the grab).
	pre := CustomFormatInput{EpisodeInfo: title, MediaInfo: nil}
	if got := Calculate(pre, []*Format{dvFormat}); len(got) != 0 {
		t.Fatalf("pre-download: DV format must not match on the lying title, got %d", len(got))
	}

	// Post-download, file is actually HDR10 (the title lied) → DV spec
	// fails → format does not match → correct low score.
	postHdr10 := CustomFormatInput{EpisodeInfo: title, MediaInfo: &mediainfo.Model{VideoHdrFormat: mediainfo.Hdr10}}
	if got := Calculate(postHdr10, []*Format{dvFormat}); len(got) != 0 {
		t.Fatalf("post-download HDR10: DV format must not match, got %d", len(got))
	}

	// Post-download, file genuinely IS Dolby Vision → matches.
	postDV := CustomFormatInput{EpisodeInfo: title, MediaInfo: &mediainfo.Model{VideoHdrFormat: mediainfo.HdrDolbyVision}}
	if got := Calculate(postDV, []*Format{dvFormat}); len(got) != 1 {
		t.Fatalf("post-download DV: format must match, got %d", len(got))
	}
}

// A MediaInfo-carrying format being skipped pre-download must not affect
// OTHER (title-only) formats evaluated in the same pass.
func TestMediaInfoSpec_DoesNotAffectOtherFormatsPreDownload(t *testing.T) {
	titleOnly := makeFormat("REMUX", makeReleaseTitleSpec(t, "REMUX"))
	mediaInfoFmt := makeFormat("DV", &HdrFormatSpec{Value: int(mediainfo.HdrDolbyVision)})
	in := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E01.2160p.DV.REMUX-GRP"},
		MediaInfo:   nil,
	}
	got := Calculate(in, []*Format{titleOnly, mediaInfoFmt})
	if len(got) != 1 || got[0].Name != "REMUX" {
		t.Fatalf("only the title-only format should match pre-download; got %+v", got)
	}
}

// Non-MediaInfo formats are unchanged by a nil MediaInfo (byte-identical
// path — they don't implement evaluable, so formatApplicable returns true).
func TestMediaInfoSpec_NonMediaInfoUnaffected(t *testing.T) {
	f := makeFormat("HDTV", makeReleaseTitleSpec(t, "HDTV"))
	in := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Series.S01E01.HDTV.x264-LOL"},
		MediaInfo:   nil,
	}
	if got := Calculate(in, []*Format{f}); len(got) != 1 {
		t.Fatalf("title-only format must still match with nil MediaInfo, got %d", len(got))
	}
}

func TestAudioCodecSpec_MatchesAnyStream(t *testing.T) {
	f := makeFormat("DTS", makeAudioCodecSpec(t, `DTS`))
	in := CustomFormatInput{MediaInfo: &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{
		{Format: "AAC"}, {Format: "DTS-HD MA"},
	}}}
	if got := Calculate(in, []*Format{f}); len(got) != 1 {
		t.Fatalf("expected DTS match across streams, got %d", len(got))
	}
	// No DTS stream → no match (but applicable, so not skipped — proven by
	// it being evaluated to false rather than skipped).
	in = CustomFormatInput{MediaInfo: &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{{Format: "AAC"}}}}
	if got := Calculate(in, []*Format{f}); len(got) != 0 {
		t.Fatalf("expected no DTS match, got %d", len(got))
	}
}

func TestAudioChannelsSpec_RangeInclusive(t *testing.T) {
	f := makeFormat("Surround", &AudioChannelsSpec{Min: 6, Max: 8})
	// 7.1 (8 channels) → in range.
	in := CustomFormatInput{MediaInfo: &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{{Channels: 8}}}}
	if got := Calculate(in, []*Format{f}); len(got) != 1 {
		t.Fatalf("8 channels should match [6,8], got %d", len(got))
	}
	// Stereo (2 channels) → out of range.
	in = CustomFormatInput{MediaInfo: &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{{Channels: 2}}}}
	if got := Calculate(in, []*Format{f}); len(got) != 0 {
		t.Fatalf("2 channels should not match [6,8], got %d", len(got))
	}
}

func TestMediaInfoSpec_NegateMatchesAbsence(t *testing.T) {
	// "NOT Dolby Vision" — post-download HDR10 file → the negated spec is
	// satisfied (absence of DV), so the format matches.
	f := makeFormat("notDV", &HdrFormatSpec{BaseSpecification: BaseSpecification{Negate: true}, Value: int(mediainfo.HdrDolbyVision)})
	in := CustomFormatInput{MediaInfo: &mediainfo.Model{VideoHdrFormat: mediainfo.Hdr10}}
	if got := Calculate(in, []*Format{f}); len(got) != 1 {
		t.Fatalf("negated DV spec should match a non-DV file, got %d", len(got))
	}
	// Pre-download it is still inapplicable (skipped) regardless of Negate.
	in = CustomFormatInput{MediaInfo: nil}
	if got := Calculate(in, []*Format{f}); len(got) != 0 {
		t.Fatalf("negated MediaInfo spec still skipped pre-download, got %d", len(got))
	}
}

// Serialization round-trip for the three new spec types.
func TestMediaInfoSpecs_MarshalRoundTrip(t *testing.T) {
	codec := &AudioCodecSpec{BaseSpecification: BaseSpecification{Name: "dts"}}
	if err := codec.SetPattern("DTS"); err != nil {
		t.Fatal(err)
	}
	specs := []Specification{
		&HdrFormatSpec{BaseSpecification: BaseSpecification{Name: "dv", Required: true}, Value: int(mediainfo.HdrDolbyVision)},
		codec,
		&AudioChannelsSpec{BaseSpecification: BaseSpecification{Name: "surround", Negate: true}, Min: 6, Max: 8},
	}
	data, err := MarshalSpecs(specs)
	if err != nil {
		t.Fatalf("MarshalSpecs: %v", err)
	}
	back, err := UnmarshalSpecs(data)
	if err != nil {
		t.Fatalf("UnmarshalSpecs: %v", err)
	}
	if len(back) != 3 {
		t.Fatalf("round-trip count: got %d want 3", len(back))
	}
	hdr, ok := back[0].(*HdrFormatSpec)
	if !ok || hdr.Value != int(mediainfo.HdrDolbyVision) || !hdr.Required {
		t.Errorf("hdr round-trip: %+v ok=%v", back[0], ok)
	}
	ac, ok := back[1].(*AudioCodecSpec)
	if !ok || ac.Pattern() != "DTS" {
		t.Errorf("audio-codec round-trip: %+v ok=%v", back[1], ok)
	}
	ch, ok := back[2].(*AudioChannelsSpec)
	if !ok || ch.Min != 6 || ch.Max != 8 || !ch.Negate {
		t.Errorf("audio-channels round-trip: %+v ok=%v", back[2], ok)
	}
}
