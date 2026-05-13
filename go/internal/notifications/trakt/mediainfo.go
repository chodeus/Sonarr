package trakt

import (
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/quality"
)

// interlacedScanTypes is the Trakt-side set of MediaInfo scan-type
// strings that count as "interlaced" for the resolution string suffix.
// Mirrors C# `TraktInterlacedTypes.InterlacedTypes` —
// case-insensitive HashSet of {"Interlaced", "MBAFF", "PAFF"}.
var interlacedScanTypes = map[string]struct{}{
	"interlaced": {},
	"mbaff":      {},
	"paff":       {},
}

// isInterlaced returns true when scanType matches a known interlaced
// marker (case-insensitive). Empty / unknown values are treated as
// progressive (matches C# default branch).
func isInterlaced(scanType string) bool {
	_, ok := interlacedScanTypes[strings.ToLower(strings.TrimSpace(scanType))]
	return ok
}

// MapMediaType converts a [quality.QualitySource] to the Trakt
// `media_type` field value. Mirrors C# `Trakt.MapMediaType` at
// `:297-312`. Returns empty string for unrecognised sources (matches
// C# `_ => string.Empty`).
func MapMediaType(source quality.QualitySource) string {
	switch source {
	case quality.SourceWeb, quality.SourceWebRip:
		return "digital"
	case quality.SourceBluray, quality.SourceBlurayRaw:
		return "bluray"
	case quality.SourceTelevision, quality.SourceTelevisionRaw:
		return "vhs"
	case quality.SourceDVD:
		return "dvd"
	default:
		return ""
	}
}

// MapResolution converts a quality resolution int + scan-type string
// to the Trakt `resolution` field value. Mirrors C# `MapResolution`
// at `:314-329`. Format strings use `i` for interlaced + `p` for
// progressive at 1080/576/480; 720p is always `p`; 2160 is the
// hardcoded "uhd_4k". Unknown resolutions return empty (matches C#).
func MapResolution(resolution int, scanType string) string {
	scanID := "p"
	if isInterlaced(scanType) {
		scanID = "i"
	}
	switch resolution {
	case 2160:
		return "uhd_4k"
	case 1080:
		return "hd_1080" + scanID
	case 720:
		return "hd_720p"
	case 576:
		return "sd_576" + scanID
	case 480:
		return "sd_480" + scanID
	default:
		return ""
	}
}

// MapHdr extracts the HDR format from a file's MediaInfo and converts
// it to the Trakt `hdr` field value. Mirrors C# `MapHdr` at
// `:331-343`. Returns empty when MediaInfo is nil OR when the HDR
// format isn't in the known set (matches C# `_ => null` → omitempty
// will drop the JSON key).
func MapHdr(f *episodefiles.File) string {
	if f == nil || f.MediaInfo == nil {
		return ""
	}
	switch f.MediaInfo.VideoHdrFormat {
	case mediainfo.HdrDolbyVision, mediainfo.HdrDolbyVisionSdr:
		return "dolby_vision"
	case mediainfo.Hdr10, mediainfo.HdrDolbyVisionHdr10:
		return "hdr10"
	case mediainfo.Hdr10Plus, mediainfo.HdrDolbyVisionHdr10Plus:
		return "hdr10_plus"
	case mediainfo.HdrHlg10, mediainfo.HdrDolbyVisionHlg:
		return "hlg"
	default:
		return ""
	}
}

// MapAudio resolves the primary audio stream + scene name to a
// Trakt `audio` field value via [mediainfo.FormatAudioCodec], then
// maps the resulting codec name to the Trakt-specific format string.
// Mirrors C# `MapAudio` at `:345-373`. Returns empty for missing
// MediaInfo or unrecognised codec names (matches C# `_ => string.
// Empty`).
func MapAudio(f *episodefiles.File) string {
	if f == nil || f.MediaInfo == nil {
		return ""
	}
	stream := f.MediaInfo.PrimaryAudioStream()
	if stream == nil {
		return ""
	}
	codec := mediainfo.FormatAudioCodec(stream, f.SceneName)
	switch codec {
	case "AC3":
		return "dolby_digital"
	case "EAC3":
		return "dolby_digital_plus"
	case "TrueHD":
		return "dolby_truehd"
	case "EAC3 Atmos":
		return "dolby_digital_plus_atmos"
	case "TrueHD Atmos":
		return "dolby_atmos"
	case "DTS", "DTS-ES":
		return "dts"
	case "DTS-HD MA":
		return "dts_ma"
	case "DTS-HD HRA":
		return "dts_hr"
	case "DTS-X":
		return "dts_x"
	case "MP3":
		return "mp3"
	case "MP2":
		return "mp2"
	case "Vorbis":
		return "ogg"
	case "WMA":
		return "wma"
	case "AAC":
		return "aac"
	case "PCM":
		return "lpcm"
	case "FLAC":
		return "flac"
	case "Opus":
		return "ogg_opus"
	default:
		return ""
	}
}

// MapAudioChannels formats the primary audio stream's channel count
// as a `"%.1f"` string (e.g. "5.1"). Mirrors C# `MapAudioChannels`
// at `:375-380`. Returns empty for missing MediaInfo. The
// audioFormat parameter is ignored — C# accepts it but the helper
// body doesn't reference it; preserved for signature parity.
func MapAudioChannels(f *episodefiles.File, _ string) string {
	if f == nil || f.MediaInfo == nil {
		return ""
	}
	stream := f.MediaInfo.PrimaryAudioStream()
	if stream == nil {
		return ""
	}
	ch := mediainfo.FormatAudioChannels(stream)
	// C# uses ToString("0.0") — one fractional digit, always shown.
	return fmt.Sprintf("%.1f", ch)
}
