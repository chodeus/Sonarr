package quality

// FindBySourceAndResolution maps a (QualitySource, resolution-int)
// pair to the canonical Quality value. Mirrors the C#
// `QualityFinder.FindBySourceAndResolution` at
// `src/NzbDrone.Core/Qualities/QualityFinder.cs:11-52`.
//
// Decision logic, in order:
//
//  1. Exact match: scan `All()` for the unique Quality whose Source
//     and Resolution both match. Return it.
//
//  2. Sub-720 special case: if no exact match AND resolution < 720,
//     return SDTV / WEBDL480p / WEBRip480p for Television / Web /
//     WebRip respectively. Prevents 576p Television/Web releases
//     from being rolled up to Bluray576p.
//
//  3. Otherwise pick the nearest-by-source Quality at the requested
//     resolution: enumerate qualities matching `resolution`, sorted
//     by `QualitySource` ascending, return the first whose Source
//     is >= the requested source. Falls through to `Unknown` if no
//     candidate clears the threshold.
//
// The C# code logs a warning when the fallback path triggers; the
// Go port omits the log because logging in this package would
// require wiring NLog-equivalent infrastructure that doesn't exist
// yet in the foundation. Caller-side logging will be added when the
// service layer wires it up.
func FindBySourceAndResolution(source QualitySource, resolution int) Quality {
	// Step 1: exact match.
	for _, q := range All() {
		if q.Source == source && q.Resolution == resolution {
			return q
		}
	}

	// Step 2: sub-720 source-specific fallback.
	if resolution < 720 {
		switch source {
		case SourceTelevision:
			return SDTV
		case SourceWeb:
			return WEBDL480p
		case SourceWebRip:
			return WEBRip480p
		}
	}

	// Step 3: nearest-source-at-resolution scan, sorted by Source
	// ascending so the smallest Source >= the requested one wins.
	var candidates []Quality
	for _, q := range All() {
		if q.Resolution == resolution {
			candidates = append(candidates, q)
		}
	}
	// Stable sort by Source ascending. Use insertion sort to avoid an
	// extra import; candidate lists are tiny (<10 elements).
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j-1].Source > candidates[j].Source; j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}
	for _, q := range candidates {
		if q.Source >= source {
			return q
		}
	}

	return Unknown
}
