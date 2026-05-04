package organizer

// RegisterMediaInfoStubs ports the surface of
// `FileNameBuilder.AddMediaInfoTokens` at `:651-694` BUT registers
// every token to return the empty string. The full media-info
// formatter port (`MediaInfoFormatter.cs` — codec / channel / dynamic-
// range derivation, language fallback rules) lands later — it's
// substantial (~300 LOC) and orthogonal to the rest of the
// FileNameBuilder pipeline.
//
// Registering stubs ensures user-written patterns containing
// `{MediaInfo Video}` etc. don't fail parsing; they just render to
// empty until the full formatter ships. Existing test fixtures that
// don't reference these tokens are unaffected.
//
// Tokens registered (15 total, lower-cased keys via NormalizeTokenKey):
// MediaInfo Video, MediaInfo VideoCodec, MediaInfo VideoBitDepth,
// MediaInfo VideoDynamicRange, MediaInfo VideoDynamicRangeType,
// MediaInfo Audio, MediaInfo AudioCodec, MediaInfo AudioChannels,
// MediaInfo AudioLanguages, MediaInfo AudioLanguagesAll,
// MediaInfo SubtitleLanguages, MediaInfo SubtitleLanguagesAll,
// MediaInfo Simple, MediaInfo Full.
func RegisterMediaInfoStubs(handlers map[string]TokenHandler) {
	stub := func(TokenMatch) string { return "" }
	for _, name := range []string{
		"MediaInfo Video",
		"MediaInfo VideoCodec",
		"MediaInfo VideoBitDepth",
		"MediaInfo VideoDynamicRange",
		"MediaInfo VideoDynamicRangeType",
		"MediaInfo Audio",
		"MediaInfo AudioCodec",
		"MediaInfo AudioChannels",
		"MediaInfo AudioLanguages",
		"MediaInfo AudioLanguagesAll",
		"MediaInfo SubtitleLanguages",
		"MediaInfo SubtitleLanguagesAll",
		"MediaInfo Simple",
		"MediaInfo Full",
	} {
		handlers[NormalizeTokenKey(name)] = stub
	}
}
