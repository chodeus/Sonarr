// Package mediainfo is the Go port of
// `NzbDrone.Core.MediaFiles.MediaInfo.MediaInfoModel` and the colocated
// audio/subtitle stream + HdrFormat types. The struct is the embedded
// JSON shape stored on `EpisodeFiles.MediaInfo` (rows 11/12/13 of the
// embedded-doc catalogue).
//
// Ports `src/NzbDrone.Core/MediaFiles/MediaInfo/MediaInfoModel.cs`,
// `MediaInfoAudioStreamModel.cs`, `MediaInfoSubtitleStreamModel.cs`,
// and `HdrFormat.cs`.
//
// Wire-format constraints worth pinning:
//
//   - The C# `VideoCodecID` property is named that way; STJ camelCase
//     produces `videoCodecID` (only the first character is lowercased).
//     The Go tag MUST be the literal `videoCodecID` — `videoCodecId`
//     would not deserialise legacy rows. Catalogued as EDOC-Q16.
//   - `RunTime` is a TimeSpan, serialised via `datastore.STDuration`
//     to round-trip C#'s `[d.]hh:mm:ss[.fffffff]` shape.
//   - `VideoColourPrimaries` keeps the British spelling.
//   - The C# `PrimaryAudioStream` is a getter-only computed property
//     that STJ skips on serialise. The Go port mirrors this with a
//     method (not a field) so the zero value isn't emitted.
//   - `Title` is `[JsonIgnore]` in C# and never persisted. The Go
//     port carries the field with `json:"-"` so live MediaInfo
//     extraction (see [internal/episodeimport.AugmentQualityFromMediaInfo]
//     which reads it to recover a release-tag source from the
//     MediaInfo title string) has access to it without bloating the
//     persisted EpisodeFile.MediaInfo column.
//   - `AudioStreams` and `SubtitleStreams` were added in migration 225
//     (`225_mediainfo_multiple_streams.cs`); pre-225 rows lacked them.
//     Both fields use `omitempty` so empty/nil slices don't bloat
//     freshly-marshalled rows.
package mediainfo

import (
	"sonarr.tv/sonarr/internal/datastore"
)

// HdrFormat ports the `HdrFormat` enum at
// `MediaFiles/MediaInfo/HdrFormat.cs`. JSON serialisation is camelCase
// strings (NOT integer values) — STJ is configured with
// `JsonStringEnumConverter(camelCase, allowIntegerValues=true)` for
// every enum on the wire. The Go port mirrors that via the
// MarshalJSON / UnmarshalJSON methods below.
//
// Numeric values mirror the C# enum's declaration order; pre-existing
// rows with integer-form values (allowIntegerValues=true) round-trip
// through the unmarshaler.
type HdrFormat int

// Hdr* constants enumerate every value of the C# HdrFormat enum, in
// declaration order. New values must be appended to preserve the wire
// numbering.
const (
	HdrNone                 HdrFormat = iota // "none"
	HdrUnknown                               // "unknownHdr"
	HdrPq10                                  // "pq10"
	Hdr10                                    // "hdr10"
	Hdr10Plus                                // "hdr10Plus"
	HdrHlg10                                 // "hlg10"
	HdrDolbyVision                           // "dolbyVision"
	HdrDolbyVisionHdr10                      // "dolbyVisionHdr10"
	HdrDolbyVisionSdr                        // "dolbyVisionSdr"
	HdrDolbyVisionHlg                        // "dolbyVisionHlg"
	HdrDolbyVisionHdr10Plus                  // "dolbyVisionHdr10Plus"
)

// String returns the canonical camelCase wire name for the format.
func (h HdrFormat) String() string {
	switch h {
	case HdrNone:
		return "none"
	case HdrUnknown:
		return "unknownHdr"
	case HdrPq10:
		return "pq10"
	case Hdr10:
		return "hdr10"
	case Hdr10Plus:
		return "hdr10Plus"
	case HdrHlg10:
		return "hlg10"
	case HdrDolbyVision:
		return "dolbyVision"
	case HdrDolbyVisionHdr10:
		return "dolbyVisionHdr10"
	case HdrDolbyVisionSdr:
		return "dolbyVisionSdr"
	case HdrDolbyVisionHlg:
		return "dolbyVisionHlg"
	case HdrDolbyVisionHdr10Plus:
		return "dolbyVisionHdr10Plus"
	default:
		return "none"
	}
}

// MarshalJSON emits the canonical camelCase string form. Mirrors STJ
// `JsonStringEnumConverter(camelCase)`. Defaults to "none" on unknown
// values.
func (h HdrFormat) MarshalJSON() ([]byte, error) {
	return []byte(`"` + h.String() + `"`), nil
}

// UnmarshalJSON accepts either the camelCase string form (preferred)
// or a bare integer. Matches STJ's `allowIntegerValues=true`. Unknown
// strings/values resolve to HdrNone (matches C# default behavior on
// unrecognised enum input).
func (h *HdrFormat) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*h = HdrNone
		return nil
	}
	// String form ("hdr10").
	if b[0] == '"' && b[len(b)-1] == '"' {
		switch string(b[1 : len(b)-1]) {
		case "none":
			*h = HdrNone
		case "unknownHdr":
			*h = HdrUnknown
		case "pq10":
			*h = HdrPq10
		case "hdr10":
			*h = Hdr10
		case "hdr10Plus":
			*h = Hdr10Plus
		case "hlg10":
			*h = HdrHlg10
		case "dolbyVision":
			*h = HdrDolbyVision
		case "dolbyVisionHdr10":
			*h = HdrDolbyVisionHdr10
		case "dolbyVisionSdr":
			*h = HdrDolbyVisionSdr
		case "dolbyVisionHlg":
			*h = HdrDolbyVisionHlg
		case "dolbyVisionHdr10Plus":
			*h = HdrDolbyVisionHdr10Plus
		default:
			*h = HdrNone
		}
		return nil
	}
	// Integer form (legacy / allowIntegerValues=true).
	var n int
	for _, c := range b {
		if c < '0' || c > '9' {
			*h = HdrNone
			return nil
		}
		n = n*10 + int(c-'0')
	}
	*h = HdrFormat(n)
	return nil
}

// AudioStream ports `MediaInfoAudioStreamModel` at
// `MediaInfoAudioStreamModel.cs`. Added in migration 225.
type AudioStream struct {
	Language         string `json:"language"`
	Format           string `json:"format"`
	CodecID          string `json:"codecId"`
	Profile          string `json:"profile"`
	Bitrate          int64  `json:"bitrate"`
	Channels         int    `json:"channels"`
	ChannelPositions string `json:"channelPositions"`
	Title            string `json:"title"`
}

// SubtitleStream ports `MediaInfoSubtitleStreamModel` at
// `MediaInfoSubtitleStreamModel.cs`. Added in migration 225.
//
// `Forced` and `HearingImpaired` are nullable booleans in C# (bool?)
// — Go ports them as `*bool` so the unset case round-trips as
// missing JSON keys (under `omitempty`).
type SubtitleStream struct {
	Language        string `json:"language"`
	Format          string `json:"format"`
	Title           string `json:"title"`
	Forced          *bool  `json:"forced"`
	HearingImpaired *bool  `json:"hearingImpaired"`
}

// Model ports `MediaInfoModel`. Stored on EpisodeFiles.MediaInfo as
// embedded JSON. Field order in the struct mirrors the C# property
// declaration order to keep drift visible.
type Model struct {
	RawStreamData                string               `json:"rawStreamData"`
	SchemaRevision               int                  `json:"schemaRevision"`
	ContainerFormat              string               `json:"containerFormat"`
	VideoFormat                  string               `json:"videoFormat"`
	VideoCodecID                 string               `json:"videoCodecID"` // EDOC-Q16: literal tag, NOT videoCodecId
	VideoProfile                 string               `json:"videoProfile"`
	VideoBitrate                 int64                `json:"videoBitrate"`
	VideoBitDepth                int                  `json:"videoBitDepth"`
	VideoColourPrimaries         string               `json:"videoColourPrimaries"` // British spelling preserved
	VideoTransferCharacteristics string               `json:"videoTransferCharacteristics"`
	VideoHdrFormat               HdrFormat            `json:"videoHdrFormat"`
	Height                       int                  `json:"height"`
	Width                        int                  `json:"width"`
	RunTime                      datastore.STDuration `json:"runTime"`
	VideoFps                     float64              `json:"videoFps"` // C# decimal → Go float64; precision is fine for fps
	AudioStreams                 []AudioStream        `json:"audioStreams"`
	SubtitleStreams              []SubtitleStream     `json:"subtitleStreams"`
	ScanType                     string               `json:"scanType"`
	Title                        string               `json:"-"`
}

// PrimaryAudioStream returns the first audio stream, or nil when
// AudioStreams is empty. Mirrors the C# `PrimaryAudioStream` getter
// (`MediaInfoModel.cs:35`) which uses `AudioStreams?.FirstOrDefault()`.
//
// Returns a copy so callers can safely mutate without affecting the
// underlying slice — matches the C# value-semantic of accessing a
// reference-type list element.
func (m *Model) PrimaryAudioStream() *AudioStream {
	if m == nil || len(m.AudioStreams) == 0 {
		return nil
	}
	stream := m.AudioStreams[0]
	return &stream
}
