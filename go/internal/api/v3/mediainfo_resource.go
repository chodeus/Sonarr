package v3

import (
	"fmt"
	"math"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/mediainfo"
)

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
// J330 — wire-shape pin. J374 lands the population: ToMediaInfoResource
// (below) projects the persisted typed mediainfo.Model
// (internal/mediainfo/mediainfo.go — the full rich blob, already decoded
// on the File model) into both surfaces' fields, and ToEpisodeFileResource
// assigns it. A nil model leaves the pointer nil so omitempty drops the
// key (zero-row contract preserved).
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

// ToMediaInfoResource projects a persisted [mediainfo.Model] into the
// UNION wire shape — the v3-flat audio/subtitle summary fields + the
// shared video/runtime fields + the v5-typed AudioStreams/SubtitleStreams
// lists. The apiv3 type deliberately carries both surfaces' fields (the
// J330 additive-divergence design), so this mapper populates BOTH the C#
// v3 `EpisodeFiles/MediaInfoResource.ToResource` flat projection AND the
// C# v5 `EpisodeFiles/MediaInfoResource.ToResource` stream-list
// projection; each surface ignores the other's extra keys.
//
// sceneName feeds the codec formatters (C# passes `model.SceneName`).
// A nil model returns nil so the caller's omitempty pointer drops the
// mediaInfo key (mirrors the C# null guard).
//
// J374.
func ToMediaInfoResource(m *mediainfo.Model, sceneName string) *MediaInfoResource {
	if m == nil {
		return nil
	}
	primary := m.PrimaryAudioStream()
	var audioBitrate int64
	if primary != nil {
		audioBitrate = primary.Bitrate
	}

	audioLangs := make([]string, len(m.AudioStreams))
	for i := range m.AudioStreams {
		audioLangs[i] = m.AudioStreams[i].Language
	}
	subLangs := make([]string, len(m.SubtitleStreams))
	for i := range m.SubtitleStreams {
		subLangs[i] = m.SubtitleStreams[i].Language
	}

	res := &MediaInfoResource{
		// v3-flat audio summary (FormatAudioChannels/FormatAudioCodec on
		// the primary stream; languages slash-joined like C#
		// ConcatToString("/")).
		AudioBitrate:     audioBitrate,
		AudioChannels:    mediainfo.FormatAudioChannels(primary),
		AudioCodec:       mediainfo.FormatAudioCodec(primary, sceneName),
		AudioLanguages:   strings.Join(audioLangs, "/"),
		AudioStreamCount: len(m.AudioStreams),
		// shared video / runtime.
		VideoBitDepth:         m.VideoBitDepth,
		VideoBitrate:          m.VideoBitrate,
		VideoCodec:            mediainfo.FormatVideoCodec(m, sceneName),
		VideoFps:              math.Round(m.VideoFps*1000) / 1000,
		VideoDynamicRange:     mediainfo.FormatVideoDynamicRange(m),
		VideoDynamicRangeType: mediainfo.FormatVideoDynamicRangeType(m),
		Resolution:            fmt.Sprintf("%dx%d", m.Width, m.Height),
		RunTime:               formatMediaInfoRuntime(m.RunTime.Duration()),
		ScanType:              m.ScanType,
		// v3-flat subtitle summary.
		Subtitles: strings.Join(subLangs, "/"),
	}

	// v5-typed stream lists (J309 pointer-to-slice 3-state: a non-nil
	// model slice → non-nil resource slice so it emits `[]`/list; a nil
	// model slice leaves the pointer nil → key absent).
	if m.AudioStreams != nil {
		streams := make([]MediaInfoAudioStreamResource, 0, len(m.AudioStreams))
		for i := range m.AudioStreams {
			s := &m.AudioStreams[i]
			streams = append(streams, MediaInfoAudioStreamResource{
				Language:         s.Language,
				Codec:            mediainfo.FormatAudioCodec(s, sceneName),
				CodecID:          s.CodecID,
				Profile:          s.Profile,
				Bitrate:          s.Bitrate,
				Channels:         mediainfo.FormatAudioChannels(s),
				ChannelPositions: s.ChannelPositions,
				Title:            s.Title,
			})
		}
		res.AudioStreams = &streams
	}
	if m.SubtitleStreams != nil {
		streams := make([]MediaInfoSubtitleStreamResource, 0, len(m.SubtitleStreams))
		for i := range m.SubtitleStreams {
			s := &m.SubtitleStreams[i]
			streams = append(streams, MediaInfoSubtitleStreamResource{
				Language:        s.Language,
				Format:          s.Format,
				Title:           s.Title,
				Forced:          s.Forced,
				HearingImpaired: s.HearingImpaired,
			})
		}
		res.SubtitleStreams = &streams
	}
	return res
}

// formatMediaInfoRuntime mirrors the C# `MediaInfoResourceMapper
// .FormatRuntime` — `H:MM:SS` when there's an hours component, else
// `M:SS`.
func formatMediaInfoRuntime(d time.Duration) string {
	totalSec := int(d.Seconds())
	if totalSec < 0 {
		totalSec = 0
	}
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
