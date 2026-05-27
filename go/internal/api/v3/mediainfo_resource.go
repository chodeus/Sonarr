package v3

// MediaInfoResource is the wire-shape DTO that EpisodeFileResource
// carries on its `mediaInfo` field. v3 and v5 SHAPE-DIVERGE on this
// resource:
//
//   - v3 (`src/Sonarr.Api.V3/EpisodeFiles/MediaInfoResource.cs:9-23`)
//     carries a FLATTENED projection of the underlying MediaInfoModel:
//     5 audio-summary fields (AudioBitrate / AudioChannels / AudioCodec
//     / AudioLanguages / AudioStreamCount) + the slash-joined
//     Subtitles language string + 9 shared video/runtime fields.
//   - v5 (`src/Sonarr.Api.V5/EpisodeFiles/MediaInfoResource.cs:6-19`)
//     DROPS the v3 flat audio + Subtitles fields and ADDS 2 typed
//     LIST subresources (AudioStreams + SubtitleStreams) carrying the
//     full per-stream detail.
//
// The Go-port emits the UNION of both shapes on the shared apiv3
// type. Every field uses omitempty so the v3-only and v5-only
// subsets drop their keys when the populated-mapper allocates only
// the relevant fields for each surface. Deliberate additive
// divergence on each surface — v3 clients see extra audioStreams /
// subtitleStreams keys when populated; v5 clients see extra
// audioBitrate / etc. keys when populated. Both v3 and v5 ignore
// unknown JSON keys so this is harmless. The pragmatic alternative
// (breaking the EpisodeFileResource alias on v5) costs more LOC
// without semantic gain.
//
// J330 — wire-shape pin only. Population is gated on the populated-
// row episodefile mapper, which needs to decode the persisted
// mediainfo.Model (internal/mediainfo/mediainfo.go:187 — the full
// 16-field rich blob) into both projections at projection time.
// Today the EpisodeFileResource.MediaInfo field stays nil and
// omitempty drops the key.
type MediaInfoResource struct {
	// v3-flat audio summary fields (additive divergence on v5).
	AudioBitrate     int64   `json:"audioBitrate,omitempty"`
	AudioChannels    float64 `json:"audioChannels,omitempty"`
	AudioCodec       string  `json:"audioCodec,omitempty"`
	AudioLanguages   string  `json:"audioLanguages,omitempty"`
	AudioStreamCount int     `json:"audioStreamCount,omitempty"`

	// Shared video / runtime fields (v3+v5).
	VideoBitDepth         int     `json:"videoBitDepth,omitempty"`
	VideoBitrate          int64   `json:"videoBitrate,omitempty"`
	VideoCodec            string  `json:"videoCodec,omitempty"`
	VideoFps              float64 `json:"videoFps,omitempty"`
	VideoDynamicRange     string  `json:"videoDynamicRange,omitempty"`
	VideoDynamicRangeType string  `json:"videoDynamicRangeType,omitempty"`
	Resolution            string  `json:"resolution,omitempty"`
	RunTime               string  `json:"runTime,omitempty"`
	ScanType              string  `json:"scanType,omitempty"`

	// v3-flat subtitle summary (additive divergence on v5).
	Subtitles string `json:"subtitles,omitempty"`

	// v5-typed stream lists (additive divergence on v3). Pointer-to-
	// slice 3-state per J309: nil → key absent; non-nil empty → `[]`;
	// populated → typed list with full per-stream fields.
	AudioStreams    *[]MediaInfoAudioStreamResource    `json:"audioStreams,omitempty"`
	SubtitleStreams *[]MediaInfoSubtitleStreamResource `json:"subtitleStreams,omitempty"`
}

// MediaInfoAudioStreamResource mirrors C# v5
// `MediaInfoAudioStreamResource` (`src/Sonarr.Api.V5/EpisodeFiles/
// MediaInfoStreamResource.cs:10-19`). 8-field per-stream DTO carried
// inside MediaInfoResource.AudioStreams.
//
// All fields nullable on C# (string?/long/decimal/string?). The
// Go-port emits Channels as float64 (C# `decimal Channels`) since
// C# `MediaInfoFormatter.FormatAudioChannels` returns fractional
// values for surround-sound layouts (e.g. 5.1).
//
// J330 wire-shape pin.
type MediaInfoAudioStreamResource struct {
	Language         string  `json:"language,omitempty"`
	Codec            string  `json:"codec,omitempty"`
	CodecID          string  `json:"codecId,omitempty"`
	Profile          string  `json:"profile,omitempty"`
	Bitrate          int64   `json:"bitrate,omitempty"`
	Channels         float64 `json:"channels,omitempty"`
	ChannelPositions string  `json:"channelPositions,omitempty"`
	Title            string  `json:"title,omitempty"`
}

// MediaInfoSubtitleStreamResource mirrors C# v5
// `MediaInfoSubtitleStreamResource` (`src/Sonarr.Api.V5/EpisodeFiles/
// MediaInfoStreamResource.cs:21-26`). 5-field per-stream DTO
// carried inside MediaInfoResource.SubtitleStreams.
//
// Forced + HearingImpaired are pointer-to-bool (matches C# `bool?`
// under WhenWritingNull — nil drops the key; non-nil emits
// true/false).
//
// J330 wire-shape pin.
type MediaInfoSubtitleStreamResource struct {
	Language        string `json:"language,omitempty"`
	Format          string `json:"format,omitempty"`
	Title           string `json:"title,omitempty"`
	Forced          *bool  `json:"forced,omitempty"`
	HearingImpaired *bool  `json:"hearingImpaired,omitempty"`
}
