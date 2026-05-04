package mediainfo

import (
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/parser"
)

// FormatAudioCodec ports `MediaInfoFormatter.FormatAudioCodec` at
// `src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoFormatter.cs:32-154`.
//
// Returns the human-friendly codec name from a stream's Format /
// CodecID / Profile triple, with sceneName as a tie-breaker for
// formats that map differently based on the release naming.
//
// Empty / nil stream returns "" (the C# version returns null on a nil
// stream and "" on an empty Format; the Go port collapses both to ""
// since the only callers downstream concatenate or check IsEmpty).
//
// Returns the original Format verbatim when no rule matches —
// matches the C# `return audioStream.Format` fallback at `:153`.
// The C# Sentry warning is dropped (no Sentry plumbing yet); the
// fallback path is exercised by the regression test pinning unknown
// formats pass through.
func FormatAudioCodec(stream *AudioStream, sceneName string) string {
	if stream == nil || stream.Format == "" {
		return ""
	}
	audioFormat := stream.Format
	codecID := stream.CodecID
	profile := stream.Profile

	// CodecID specials trump format-specific rules.
	if codecID == "thd+" {
		return "TrueHD Atmos"
	}
	if codecID == "ec+3" {
		return "EAC3 Atmos"
	}

	switch audioFormat {
	case "truehd":
		if profile == "Dolby TrueHD + Dolby Atmos" {
			return "TrueHD Atmos"
		}
		return "TrueHD"
	case "flac":
		return "FLAC"
	case "dts":
		switch profile {
		case "DTS:X", "DTS-HD MA + DTS:X IMAX":
			return "DTS-X"
		case "DTS-HD MA":
			return "DTS-HD MA"
		case "DTS-ES":
			return "DTS-ES"
		case "DTS-HD HRA":
			return "DTS-HD HRA"
		case "DTS Express":
			return "DTS Express"
		case "DTS 96/24":
			return "DTS 96/24"
		default:
			return "DTS"
		}
	case "eac3":
		if profile == "Dolby Digital Plus + Dolby Atmos" {
			return "EAC3 Atmos"
		}
		return "EAC3"
	case "ac3":
		return "AC3"
	case "aac":
		if codecID == "A_AAC/MPEG4/LC/SBR" {
			return "HE-AAC"
		}
		switch profile {
		case "HE-AAC":
			return "HE-AAC"
		case "xHE-AAC":
			return "xHE-AAC"
		default:
			return "AAC"
		}
	case "mp3":
		return "MP3"
	case "mp2":
		return "MP2"
	case "opus":
		return "Opus"
	case "vorbis":
		return "Vorbis"
	case "wmav1", "wmav2", "wmapro":
		return "WMA"
	}

	if strings.HasPrefix(audioFormat, "pcm_") || strings.HasPrefix(audioFormat, "adpcm_") {
		return "PCM"
	}

	// Unknown — return verbatim. The C# version logs a Sentry warning
	// here ("UnknownAudioFormatFFProbe"); not plumbed yet.
	return audioFormat
}

// FormatVideoCodec ports `MediaInfoFormatter.FormatVideoCodec` at
// `:156-269`.
//
// Returns the human-friendly video codec, possibly preferring the
// scene name when the format is generic (h264 / hevc — those split
// into AVC/x264/h264 vs HEVC/x265/h265 based on the release naming).
//
// Nil model or empty VideoFormat returns "" (the C# version returns
// null on the nil/missing path; collapsed for Go-port simplicity).
func FormatVideoCodec(m *Model, sceneName string) string {
	if m == nil || m.VideoFormat == "" {
		return ""
	}
	videoFormat := m.VideoFormat
	codecID := m.VideoCodecID

	if codecID == "x264" {
		return "x264"
	}
	if videoFormat == "h264" {
		return getSceneNameMatch(sceneName, "AVC", "x264", "h264")
	}
	if codecID == "x265" {
		return "x265"
	}
	if videoFormat == "hevc" {
		return getSceneNameMatch(sceneName, "HEVC", "x265", "h265")
	}

	switch videoFormat {
	case "mpeg2video":
		return "MPEG2"
	case "mpeg1video":
		return "MPEG"
	case "vc1":
		return "VC1"
	case "av1":
		return "AV1"
	case "vp7", "vp8", "vp9":
		return strings.ToUpper(videoFormat)
	case "wmv1", "wmv2", "wmv3":
		return "WMV"
	case "qtrle", "rpza", "rv10", "rv20", "rv30", "rv40",
		"cinepak", "rawvideo", "msvideo1":
		return ""
	}

	// MPEG-4 family with codecID-driven branding (XviD vs DivX).
	if videoFormat == "mpeg4" || strings.Contains(videoFormat, "msmpeg4") {
		switch strings.ToUpper(codecID) {
		case "XVID":
			return "XviD"
		case "DIVX":
			return "DivX"
		}
		switch codecID {
		case "DIV3", "DX50":
			return "DivX"
		}
		return ""
	}

	if strings.Contains(videoFormat, "vp6") {
		return "VP6"
	}

	// Unknown — return original (trimmed) format. The C# version logs
	// a Sentry warning here ("UnknownVideoFormatFFProbe"); not
	// plumbed yet.
	return strings.TrimSpace(videoFormat)
}

// FormatVideoDynamicRange ports `:303-306`. Returns "HDR" when any
// HDR format is set, "" otherwise. Used by the organizer's
// `{MediaInfo VideoDynamicRange}` token.
func FormatVideoDynamicRange(m *Model) string {
	if m == nil || m.VideoHdrFormat == HdrNone {
		return ""
	}
	return "HDR"
}

// FormatVideoDynamicRangeType ports `:308-324`. Returns the specific
// HDR/DV variant ("DV", "DV HDR10", "HDR10Plus", etc.) — used by the
// `{MediaInfo VideoDynamicRangeType}` token.
func FormatVideoDynamicRangeType(m *Model) string {
	if m == nil {
		return ""
	}
	switch m.VideoHdrFormat {
	case HdrDolbyVision:
		return "DV"
	case HdrDolbyVisionHdr10:
		return "DV HDR10"
	case HdrDolbyVisionHdr10Plus:
		return "DV HDR10Plus"
	case HdrDolbyVisionHlg:
		return "DV HLG"
	case HdrDolbyVisionSdr:
		return "DV SDR"
	case Hdr10:
		return "HDR10"
	case Hdr10Plus:
		return "HDR10Plus"
	case HdrHlg10:
		return "HLG"
	case HdrPq10:
		return "PQ"
	case HdrUnknown:
		return "HDR"
	}
	return ""
}

// FormatAudioChannels ports `:20-30` plus the position-regex helper
// at `:271-285`.
//
// Returns the channel count as a decimal-style number, e.g. 5.1 / 7.1
// / 2.0. The C# version returns `decimal`; the Go port returns
// float64 since none of the downstream tokens need the precise
// decimal type. The two-step derivation:
//
//  1. If ChannelPositions starts with `\d.\d`, parse that prefix
//     (e.g. "5.1 / Side: L R" → 5.1).
//  2. Otherwise fall back to the integer Channels field.
//
// Nil stream returns 0. Matches the C# return value.
func FormatAudioChannels(stream *AudioStream) float64 {
	if stream == nil {
		return 0
	}
	if v := formatAudioChannelsFromPositions(stream.ChannelPositions); v != 0 {
		return v
	}
	return float64(stream.Channels)
}

// positionRegex captures a leading `\d.\d` prefix in the channel-
// positions string. Mirrors `MediaInfoFormatter.PositionRegex` at
// `:15-16`. RE2 (used by Go's regexp) doesn't support named groups
// the same way as .NET; the named group is rewritten as a
// positional capture since we only need group 1.
var positionRegex = regexp.MustCompile(`^(\d\.\d)`)

func formatAudioChannelsFromPositions(positions string) float64 {
	if positions == "" {
		return 0
	}
	m := positionRegex.FindStringSubmatch(positions)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return v
}

// getSceneNameMatch ports `:287-301`. Strips the file extension off
// the sceneName, then walks `tokens` and returns the first one whose
// (case-insensitive) substring is in the cleaned scene name.
// Falls back to the LAST token in the list (matches C# `tokens.Last()`).
func getSceneNameMatch(sceneName string, tokens ...string) string {
	if sceneName != "" {
		sceneName = parser.RemoveFileExtension(sceneName)
	}
	scene := strings.ToLower(sceneName)
	for _, t := range tokens {
		if scene != "" && strings.Contains(scene, strings.ToLower(t)) {
			return t
		}
	}
	if len(tokens) == 0 {
		return ""
	}
	return tokens[len(tokens)-1]
}
