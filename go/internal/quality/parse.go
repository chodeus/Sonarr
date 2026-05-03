package quality

import "strings"

// ParseQuality is the top-level entry point that mirrors
// `QualityParser.ParseQuality` at
// `src/NzbDrone.Core/Parser/QualityParser.cs:68-98`.
//
// Algorithm:
//
//  1. Empty / whitespace name → Model{Quality: Unknown}.
//  2. Trim name, run ParseQualityName.
//  3. If the result is still Unknown AND the name has no invalid path
//     chars, look up the file extension in MediaFileExtensions and
//     adopt that Quality (with both detection sources marked Extension).
//
// The C# code wraps the extension-lookup in a try/catch on
// `ArgumentException` (which `Path.GetExtension` can throw on names
// with invalid path chars). The Go port pre-checks via
// containsInvalidPathChars and skips the lookup when the check
// fails, achieving the same observable behaviour without the
// exception dance.
func ParseQuality(name string) Model {
	if strings.TrimSpace(name) == "" {
		return Model{Quality: Unknown}
	}
	name = strings.TrimSpace(name)

	result := ParseQualityName(name)

	if result.Quality.ID == Unknown.ID && !containsInvalidPathChars(name) {
		ext := getPathExtension(name)
		if q := GetQualityForExtension(ext); q.ID != Unknown.ID {
			result.Quality = q
			result.SourceDetectionSource = DetectionExtension
			result.ResolutionDetectionSource = DetectionExtension
		}
	}

	return result
}

// ParseQualityName is the heavy decision matrix. Mirrors
// `QualityParser.ParseQualityName` at `QualityParser.cs:100-598`.
//
// The control flow follows the C# source line-for-line so a future
// sync against the C# tree is direct: each labelled section here
// corresponds to a numbered branch of the C# function. Comments cite
// the source line ranges.
//
// the C# decision matrix; flattening it would obscure the C# parity
// the rest of the package relies on for verification.
//
//nolint:gocyclo,funlen,gocognit // Mirrors the line-for-line shape of
func ParseQualityName(name string) Model {
	normalized := strings.TrimSpace(strings.ReplaceAll(name, "_", " "))

	result := Model{
		Quality:  Unknown,
		Revision: ParseRevision(name),
	}

	// `:105-112`: RawHD short-circuit. Highest-priority classification.
	if IsRawHD(normalized) {
		result.SourceDetectionSource = DetectionName
		result.ResolutionDetectionSource = DetectionName
		result.Quality = RawHD
		return result
	}

	source := ParseSource(normalized)
	resolution := ParseResolution(normalized)
	codec := ParseCodec(normalized)
	remux := IsRemux(normalized)

	// `:120-123`: tag the resolution detection source if we found one.
	if resolution != ResolutionUnknown {
		result.ResolutionDetectionSource = DetectionName
	}

	// `:125-318`: source-driven dispatch.
	if source != SourceTagUnknown {
		result.SourceDetectionSource = DetectionName

		switch source {
		case SourceTagBluray:
			// `:129-173`: Bluray decision tree.
			if codec == CodecXvid || codec == CodecDivX {
				result.Quality = Bluray480p
				return result
			}
			switch resolution {
			case Resolution2160p:
				if remux {
					result.Quality = Bluray2160pRemux
				} else {
					result.Quality = Bluray2160p
				}
				return result
			case Resolution1080p:
				if remux {
					result.Quality = Bluray1080pRemux
				} else {
					result.Quality = Bluray1080p
				}
				return result
			case Resolution576p:
				result.Quality = Bluray576p
				return result
			case Resolution360p, Resolution480p, Resolution540p:
				result.Quality = Bluray480p
				return result
			}
			// `:163-169`: remux without source treated as 1080p,
			// not 720p. 720p remux falls back to 720p Bluray.
			if remux && resolution != Resolution720p {
				result.Quality = Bluray1080pRemux
				return result
			}
			result.Quality = Bluray720p
			return result

		case SourceTagWebDL:
			// `:175-203`.
			switch resolution {
			case Resolution2160p:
				result.Quality = WEBDL2160p
				return result
			case Resolution1080p:
				result.Quality = WEBDL1080p
				return result
			case Resolution720p:
				result.Quality = WEBDL720p
				return result
			}
			if strings.Contains(name, "[WEBDL]") {
				result.Quality = WEBDL720p
				return result
			}
			result.Quality = WEBDL480p
			return result

		case SourceTagWebRip:
			// `:205-227`.
			switch resolution {
			case Resolution2160p:
				result.Quality = WEBRip2160p
				return result
			case Resolution1080p:
				result.Quality = WEBRip1080p
				return result
			case Resolution720p:
				result.Quality = WEBRip720p
				return result
			}
			result.Quality = WEBRip480p
			return result

		case SourceTagHDTV:
			// `:229-263`.
			if IsMPEG2(normalized) {
				result.Quality = RawHD
				return result
			}
			switch resolution {
			case Resolution2160p:
				result.Quality = HDTV2160p
				return result
			case Resolution1080p:
				result.Quality = HDTV1080p
				return result
			case Resolution720p:
				result.Quality = HDTV720p
				return result
			}
			if strings.Contains(name, "[HDTV]") {
				result.Quality = HDTV720p
				return result
			}
			result.Quality = SDTV
			return result

		case SourceTagBDRip, SourceTagBRRip:
			// `:265-283`.
			switch resolution {
			case Resolution720p:
				result.Quality = Bluray720p
			case Resolution1080p:
				result.Quality = Bluray1080p
			case Resolution2160p:
				result.Quality = Bluray2160p
			default:
				result.Quality = Bluray480p
			}
			return result

		case SourceTagDVD:
			// `:285-289`.
			result.Quality = DVD
			return result

		case SourceTagDSR, SourceTagPDTV, SourceTagSDTV, SourceTagTVRip:
			// `:291-317`.
			if resolution == Resolution1080p || containsCI(normalized, "1080p") {
				result.Quality = HDTV1080p
				return result
			}
			if resolution == Resolution720p || containsCI(normalized, "720p") {
				result.Quality = HDTV720p
				return result
			}
			if IsHighDefPdtv(normalized) {
				result.ResolutionDetectionSource = DetectionName
				result.Quality = HDTV720p
				return result
			}
			result.Quality = SDTV
			return result
		}
	}

	// `:320-347`: source-less remux fallback (resolution determines
	// Bluray sub-quality).
	if source == SourceTagUnknown && remux && resolution != ResolutionUnknown {
		result.SourceDetectionSource = DetectionUnknown
		switch resolution {
		case Resolution480p:
			result.Quality = Bluray480p
			return result
		case Resolution720p:
			result.Quality = Bluray720p
			return result
		case Resolution2160p:
			result.Quality = Bluray2160pRemux
			return result
		case Resolution1080p:
			result.Quality = Bluray1080pRemux
			return result
		}
	}

	// `:350-390`: anime Bluray fallback.
	if IsAnimeBluray(normalized) {
		result.SourceDetectionSource = DetectionName

		if resolution == Resolution360p || resolution == Resolution480p ||
			resolution == Resolution540p || resolution == Resolution576p ||
			containsCI(normalized, "480p") {
			result.ResolutionDetectionSource = DetectionName
			result.Quality = DVD
			return result
		}
		if resolution == Resolution1080p || containsCI(normalized, "1080p") {
			result.ResolutionDetectionSource = DetectionName
			if remux {
				result.Quality = Bluray1080pRemux
			} else {
				result.Quality = Bluray1080p
			}
			return result
		}
		if resolution == Resolution2160p || containsCI(normalized, "2160p") {
			result.ResolutionDetectionSource = DetectionName
			if remux {
				result.Quality = Bluray2160pRemux
			} else {
				result.Quality = Bluray2160p
			}
			return result
		}
		// 720p remux→720p; non-720p remux→1080p remux fallback.
		if remux && resolution != Resolution720p {
			result.Quality = Bluray1080pRemux
			return result
		}
		result.Quality = Bluray720p
		return result
	}

	// `:392-424`: anime WebDL fallback.
	if IsAnimeWebDL(normalized) {
		result.SourceDetectionSource = DetectionName

		if resolution == Resolution360p || resolution == Resolution480p ||
			resolution == Resolution540p || resolution == Resolution576p ||
			containsCI(normalized, "480p") {
			result.ResolutionDetectionSource = DetectionName
			result.Quality = WEBDL480p
			return result
		}
		if resolution == Resolution1080p || containsCI(normalized, "1080p") {
			result.ResolutionDetectionSource = DetectionName
			result.Quality = WEBDL1080p
			return result
		}
		if resolution == Resolution2160p || containsCI(normalized, "2160p") {
			result.ResolutionDetectionSource = DetectionName
			result.Quality = WEBDL2160p
			return result
		}
		result.Quality = WEBDL720p
		return result
	}

	// `:426-497`: resolution-only fallback (no source matched).
	// Maps the resolution + extension/remux source guess to a Quality
	// via QualityFinder.FindBySourceAndResolution.
	if resolution != ResolutionUnknown {
		extSource := SourceUnknown
		if remux {
			result.SourceDetectionSource = DetectionName
			extSource = SourceBlurayRaw
		} else if !containsInvalidPathChars(name) {
			ext := getPathExtension(name)
			if q := GetQualityForExtension(ext); q.ID != Unknown.ID {
				result.SourceDetectionSource = DetectionExtension
				extSource = q.Source
			}
		}

		switch resolution {
		case Resolution2160p:
			result.ResolutionDetectionSource = DetectionName
			if extSource == SourceUnknown {
				result.Quality = HDTV2160p
			} else {
				result.Quality = FindBySourceAndResolution(extSource, 2160)
			}
			return result
		case Resolution1080p:
			result.ResolutionDetectionSource = DetectionName
			if extSource == SourceUnknown {
				result.Quality = HDTV1080p
			} else {
				result.Quality = FindBySourceAndResolution(extSource, 1080)
			}
			return result
		case Resolution720p:
			result.ResolutionDetectionSource = DetectionName
			if extSource == SourceUnknown {
				result.Quality = HDTV720p
			} else {
				result.Quality = FindBySourceAndResolution(extSource, 720)
			}
			return result
		case Resolution360p, Resolution480p, Resolution540p, Resolution576p:
			result.ResolutionDetectionSource = DetectionName
			if extSource == SourceUnknown {
				result.Quality = SDTV
			} else {
				result.Quality = FindBySourceAndResolution(extSource, 480)
			}
			return result
		}
	}

	// `:499-504`: bare x264 → SDTV.
	if codec == CodecX264 {
		result.Quality = SDTV
		return result
	}

	// `:506-526`: 848x480 fallback (DVD vs Bluray vs SDTV based on
	// surrounding tokens).
	if strings.Contains(normalized, "848x480") {
		result.ResolutionDetectionSource = DetectionName
		switch {
		case strings.Contains(normalized, "dvd"):
			result.SourceDetectionSource = DetectionName
			result.Quality = DVD
		case containsCI(normalized, "bluray"):
			result.SourceDetectionSource = DetectionName
			result.Quality = Bluray480p
		default:
			result.Quality = SDTV
		}
		return result
	}

	// `:528-543`: 1280x720 fallback (HDTV vs Bluray).
	if containsCI(normalized, "1280x720") {
		result.ResolutionDetectionSource = DetectionName
		if containsCI(normalized, "bluray") {
			result.SourceDetectionSource = DetectionName
			result.Quality = Bluray720p
		} else {
			result.Quality = HDTV720p
		}
		return result
	}

	// `:545-560`: 1920x1080 fallback (HDTV vs Bluray).
	if containsCI(normalized, "1920x1080") {
		result.ResolutionDetectionSource = DetectionName
		if containsCI(normalized, "bluray") {
			result.SourceDetectionSource = DetectionName
			result.Quality = Bluray1080p
		} else {
			result.Quality = HDTV1080p
		}
		return result
	}

	// `:562-587`: bluray720p / bluray1080p / bluray2160p concatenated
	// token shortcuts (the "bluray" prefix without separator).
	if containsCI(normalized, "bluray720p") {
		result.SourceDetectionSource = DetectionName
		result.ResolutionDetectionSource = DetectionName
		result.Quality = Bluray720p
		return result
	}
	if containsCI(normalized, "bluray1080p") {
		result.SourceDetectionSource = DetectionName
		result.ResolutionDetectionSource = DetectionName
		result.Quality = Bluray1080p
		return result
	}
	if containsCI(normalized, "bluray2160p") {
		result.SourceDetectionSource = DetectionName
		result.ResolutionDetectionSource = DetectionName
		result.Quality = Bluray2160p
		return result
	}

	// `:589-595`: final HD-TV / SD-TV fallback via OtherSourceRegex.
	if other := ClassifyOtherSource(normalized); other != SourceTagUnknown {
		result.SourceDetectionSource = DetectionName
		switch other {
		case SourceTagHDTV:
			result.Quality = HDTV720p
		case SourceTagSDTV:
			result.Quality = SDTV
		}
		return result
	}

	return result
}

// containsCI is a case-insensitive substring check, mirroring the C#
// `ContainsIgnoreCase` extension method used throughout
// `ParseQualityName`. Equivalent to `strings.Contains(strings.ToLower(s),
// strings.ToLower(substr))` but avoids two allocations per call.
func containsCI(s, substr string) bool {
	if substr == "" {
		return true
	}
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a := s[i+j]
			b := substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// getPathExtension mirrors the C# `string.GetPathExtension()` helper
// at `src/NzbDrone.Common/Extensions/PathExtensions.cs:74-83`.
//
// Returns the substring beginning at the last `.` in `path`, or
// empty string if there's no dot OR the dot is the last character
// (i.e. trailing dot, no extension to read). Includes the leading
// dot in the result. NOT equivalent to Go's `filepath.Ext` because
// `filepath.Ext` strips characters after the last `\` or `/` first
// and treats things differently — the Sonarr helper operates on the
// full string regardless of separators.
func getPathExtension(path string) string {
	idx := strings.LastIndex(path, ".")
	if idx == -1 || idx == len(path)-1 {
		return ""
	}
	return path[idx:]
}

// containsInvalidPathChars mirrors `string.ContainsInvalidPathChars()`
// at `src/NzbDrone.Common/Extensions/PathExtensions.cs:184-192`,
// which delegates to .NET `Path.GetInvalidPathChars()`. The cross-
// platform set returned by .NET is:
//
//   - control chars 0x00–0x1F
//   - the vertical bar `|`
//
// (On Windows .NET adds more, but the C# usage in QualityParser
// pre-dates the Windows-specific set and has always operated on the
// cross-platform list.)
//
// Used in ParseQuality + the resolution-only fallback to skip the
// extension-lookup branch when the name contains chars that
// `Path.GetExtension` would refuse — preserving the C# try/catch
// semantic without actually throwing.
func containsInvalidPathChars(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '|' {
			return true
		}
	}
	return false
}
