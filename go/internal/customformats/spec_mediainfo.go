package customformats

// MediaInfo-based custom-format specifications. These are a NET-NEW
// condition family with no current C# equivalent (current Sonarr/Radarr
// custom formats only match release-title/filename text, parsed quality,
// language, indexer-flags, size, release-group and release-type — none
// read the probed MediaInfo). They exist to make custom-format scoring
// HONEST about MediaInfo-derived attributes (HDR/DV, audio codec, audio
// channels): a release TITLE can claim "DV" that the file doesn't
// deliver, but these specs score from the actual ffprobe MediaInfo.
//
// Upstreamability: the intended C# shape is a spec per type deriving
// from `CustomFormatSpecificationBase` plus an
// `ICustomFormatSpecification.IsApplicable(CustomFormatInput) => true`
// default hook that MediaInfo specs override to
// `input.MediaInfo != null`. The Go `evaluable` interface below maps to
// that hook. Tracks Sonarr/Sonarr#5598 (maintainers explicitly want
// MediaInfo-based conditions).
//
// Pre/post behaviour (the whole point): MediaInfo is only known AFTER
// download (ffprobe). Pre-download `CustomFormatInput.MediaInfo` is nil,
// so these specs report `applicable == false`. [Calculate] then SKIPS
// the entire format pre-download (see formatApplicable) — a format that
// carries an un-knowable MediaInfo condition must not match on its
// title-based specs alone, or a release title could over-promise an
// attribute it doesn't have. Post-download the MediaInfo is present and
// these specs are authoritative.

// evaluable is the opt-in extension for specs whose evaluation depends
// on data that may be absent for a given input (here: MediaInfo, which
// only exists post-download). Evaluate reports BOTH whether the spec
// matched (before Negate) AND whether it was APPLICABLE — i.e. whether
// the input carried the data the spec needs.
//
// [Calculate] uses applicability to skip a whole format when any of its
// specs is inapplicable (see formatApplicable). Specs that do NOT
// implement evaluable are always treated as applicable, so the existing
// title/quality/size/etc. specs take a byte-identical path.
type evaluable interface {
	// Evaluate returns (matched-before-Negate, applicable).
	Evaluate(input CustomFormatInput) (matched bool, applicable bool)
}

// HdrFormatSpec matches the probed `MediaInfo.VideoHdrFormat` against a
// specific [mediainfo.HdrFormat] value (e.g. Dolby Vision, HDR10). The
// Value is the int enum form so it round-trips through the same
// int-value envelope the Resolution/Source/ReleaseType specs use.
type HdrFormatSpec struct {
	BaseSpecification
	// Value is the mediainfo.HdrFormat to match (int enum).
	Value int
}

// Order returns 11 — after the existing HEAD specs (ReleaseType=10).
func (s *HdrFormatSpec) Order() int { return 11 }

// ImplementationName returns "HDR Format".
func (s *HdrFormatSpec) ImplementationName() string { return "HDR Format" }

// Evaluate matches MediaInfo.VideoHdrFormat == Value; inapplicable when
// no MediaInfo is present (pre-download).
func (s *HdrFormatSpec) Evaluate(input CustomFormatInput) (bool, bool) {
	if input.MediaInfo == nil {
		return false, false
	}
	return int(input.MediaInfo.VideoHdrFormat) == s.Value, true
}

// IsSatisfiedBy applies the Negate-aware wrapper. When MediaInfo is
// absent Evaluate reports no match; the format is skipped upstream by
// formatApplicable so this path only runs post-download in practice.
func (s *HdrFormatSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	matched, _ := s.Evaluate(input)
	return s.applyNegate(matched)
}

// AudioCodecSpec matches the regex against ANY audio stream's Format
// (e.g. `DTS`, `TrueHD`, `AAC`). Reuses [RegexSpec] for the pattern.
type AudioCodecSpec struct {
	BaseSpecification
	RegexSpec
}

// Order returns 12.
func (s *AudioCodecSpec) Order() int { return 12 }

// ImplementationName returns "Audio Codec".
func (s *AudioCodecSpec) ImplementationName() string { return "Audio Codec" }

// Evaluate matches when any audio stream's Format matches the pattern;
// inapplicable when no MediaInfo is present (pre-download). MediaInfo
// present but with no audio streams is applicable (we can evaluate) and
// simply doesn't match.
func (s *AudioCodecSpec) Evaluate(input CustomFormatInput) (bool, bool) {
	if input.MediaInfo == nil {
		return false, false
	}
	for i := range input.MediaInfo.AudioStreams {
		if s.matchString(input.MediaInfo.AudioStreams[i].Format) {
			return true, true
		}
	}
	return false, true
}

// IsSatisfiedBy applies the Negate-aware wrapper.
func (s *AudioCodecSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	matched, _ := s.Evaluate(input)
	return s.applyNegate(matched)
}

// AudioChannelsSpec matches when ANY audio stream's channel COUNT is in
// the inclusive range [Min, Max]. Channel count is the raw stream count
// (6 = 5.1, 8 = 7.1, 2 = stereo) — the value [mediainfo.AudioStream]
// carries. Inclusive bounds (unlike SizeSpec's `>Min, <=Max`) because
// channel counts are small discrete integers.
type AudioChannelsSpec struct {
	BaseSpecification
	Min int
	Max int
}

// Order returns 13.
func (s *AudioChannelsSpec) Order() int { return 13 }

// ImplementationName returns "Audio Channels".
func (s *AudioChannelsSpec) ImplementationName() string { return "Audio Channels" }

// Evaluate matches when any audio stream's Channels is within [Min,Max];
// inapplicable when no MediaInfo is present (pre-download).
func (s *AudioChannelsSpec) Evaluate(input CustomFormatInput) (bool, bool) {
	if input.MediaInfo == nil {
		return false, false
	}
	for i := range input.MediaInfo.AudioStreams {
		c := input.MediaInfo.AudioStreams[i].Channels
		if c >= s.Min && c <= s.Max {
			return true, true
		}
	}
	return false, true
}

// IsSatisfiedBy applies the Negate-aware wrapper.
func (s *AudioChannelsSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	matched, _ := s.Evaluate(input)
	return s.applyNegate(matched)
}

// compile-time assertions that the MediaInfo specs implement evaluable.
var (
	_ evaluable = (*HdrFormatSpec)(nil)
	_ evaluable = (*AudioCodecSpec)(nil)
	_ evaluable = (*AudioChannelsSpec)(nil)
)
