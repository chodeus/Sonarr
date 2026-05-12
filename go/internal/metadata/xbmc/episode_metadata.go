package xbmc

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/series"
)

// EpisodeMetadata builds the per-episode `<filename>.nfo` body for
// the episodes linked to the given EpisodeFile.
// Ports C# `XbmcMetadata.EpisodeMetadata` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/XbmcMetadata.cs:278-423`.
//
// Emission gates:
//
//   - [Settings.EpisodeMetadata]: when false, returns zero-value.
//   - Episode-lookup empty: zero-value (matches C# null return when
//     the file has no associated episodes — multi-episode files are
//     rare but supported).
//   - Episode lookup errors: returned to caller (the consumer-facing
//     service can decide to swallow / log / abort).
//
// The body is a sequence of `<episodedetails>` root elements — one per
// linked episode. C# uses `ConformanceLevel.Fragment` on its
// XmlWriterSettings to allow this multi-root output. The Go port
// builds the body by iterating the episodes and encoding each
// `episodeDetailsXML` struct in turn. Kodi / Emby / Jellyfin parse
// the multi-root file via their own NFO scanners — XML purists
// frown, but this is the long-standing Kodi convention.
//
// Filename = stem of episodeFile.RelativePath + ".nfo" via
// [getEpisodeMetadataFilename] (re-used from sub-arc W2a).
//
// Deferred from this slice (4.BE / sub-arc W2c-ii):
//   - **Watched-status disk sniff** (C# `:519-531`
//     GetExistingWatchedStatus reads any existing .nfo on disk and
//     regex-matches `<watched>true</watched>`). The Go port returns
//     false unconditionally for now. The C# behaviour preserves the
//     user's local watched state across regenerations; the Go port
//     loses that on first regen until the disk-sniff lands.
//     TODO (queue [W6] orchestrator slice): thread a disk-reader
//     dep through Deps and re-introduce the sniff.
//   - **MediaInfoFormatter.FormatVideoCodec / FormatAudioCodec**
//     (C# `:357,392`). The Go port emits `mediainfo.Model.VideoFormat`
//     verbatim and `audioStream.Format` verbatim. The C# formatter
//     applies codec normalisation (h264 / AVC / DivX X.Y inference
//     from sceneName + profile) — a complex ~150 LOC helper out of
//     scope here. Document as deferred wire divergence.
func (m *XbmcMetadata) EpisodeMetadata(ctx context.Context, s *series.Series, episodeFile *episodefiles.File) (metadata.MetadataFileResult, error) {
	if m == nil || s == nil || episodeFile == nil || !m.settings.EpisodeMetadata {
		return metadata.MetadataFileResult{}, nil
	}
	if m.episodes == nil {
		return metadata.MetadataFileResult{}, nil
	}

	linked, err := m.episodes.AllByFileID(ctx, episodeFile.ID)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("xbmc: EpisodeMetadata: lookup episodes for fileID=%d: %w", episodeFile.ID, err)
	}
	if len(linked) == 0 {
		return metadata.MetadataFileResult{}, nil
	}

	// TODO (queue [W6]): real watched-status sniff via disk-reader
	// dep. Returns false unconditionally for now.
	watched := false

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")

	for _, ep := range linked {
		details, buildErr := buildEpisodeDetailsXML(ep, episodeFile, watched, m.settings)
		if buildErr != nil {
			return metadata.MetadataFileResult{}, buildErr
		}
		if encErr := enc.Encode(&details); encErr != nil {
			return metadata.MetadataFileResult{}, fmt.Errorf("xbmc: EpisodeMetadata: xml encode episode %d: %w", ep.ID, encErr)
		}
	}
	if err := enc.Flush(); err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("xbmc: EpisodeMetadata: xml flush: %w", err)
	}

	// C# `:422` trims trailing newline chars from the body. Match.
	body := strings.TrimRight(buf.String(), "\r\n")
	filename := getEpisodeMetadataFilename(episodeFile.RelativePath)
	return metadata.NewMetadataFileResult(filename, body), nil
}

// episodeDetailsXML is the per-episode XML root. Field ordering
// mirrors the C# `XElement.Add` sequence at `XbmcMetadata.cs:305-340`
// for git-diff readability of generated fixtures.
type episodeDetailsXML struct {
	XMLName            xml.Name      `xml:"episodedetails"`
	Title              string        `xml:"title"`
	Season             int           `xml:"season"`
	Episode            int           `xml:"episode"`
	Aired              string        `xml:"aired"`
	Plot               string        `xml:"plot"`
	DisplayAfterSeason *int          `xml:"displayafterseason,omitempty"`
	DisplaySeason      *int          `xml:"displayseason,omitempty"`
	DisplayEpisode     *int          `xml:"displayepisode,omitempty"`
	UniqueIDs          []uniqueIDXML `xml:"uniqueid"`
	Thumb              *string       `xml:"thumb"`
	Watched            string        `xml:"watched"`
	Rating             *string       `xml:"rating,omitempty"`
	FileInfo           *fileInfoXML  `xml:"fileinfo,omitempty"`
}

// uniqueIDXML is the shared `<uniqueid type="..." default="...">value</uniqueid>`
// element shape used by both [series_metadata.go] and this file.
// `default` is omitted (no attr) when empty so non-default uniqueid
// elements render without the attribute.
type uniqueIDXML struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

// actorXML is the shared `<actor>` element shape used by
// [series_metadata.go]. Declared at package scope so both files can
// reference the same type.
type actorXML struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Thumb string `xml:"thumb,omitempty"`
}

// fileInfoXML wraps the MediaInfo streamdetails sub-document.
type fileInfoXML struct {
	StreamDetails streamDetailsXML `xml:"streamdetails"`
}

type streamDetailsXML struct {
	Video     videoStreamXML      `xml:"video"`
	Audios    []audioStreamXML    `xml:"audio"`
	Subtitles []subtitleStreamXML `xml:"subtitle"`
}

type videoStreamXML struct {
	Aspect            string  `xml:"aspect"`
	Bitrate           int64   `xml:"bitrate"`
	Codec             string  `xml:"codec"`
	Framerate         string  `xml:"framerate"`
	Height            int     `xml:"height"`
	ScanType          string  `xml:"scantype"`
	Width             int     `xml:"width"`
	Duration          string  `xml:"duration"`
	DurationInSeconds int64   `xml:"durationinseconds"`
	HdrType           *string `xml:"hdrtype"`
}

type audioStreamXML struct {
	Bitrate  int64  `xml:"bitrate"`
	Channels int    `xml:"channels"`
	Codec    string `xml:"codec"`
	Language string `xml:"language"`
}

type subtitleStreamXML struct {
	Language string `xml:"language"`
}

// buildEpisodeDetailsXML constructs the `<episodedetails>` body for
// a single episode + parent episodeFile. Ports the per-episode loop
// of C# `XbmcMetadata.EpisodeMetadata` at `:301-417`.
//
// Decode failures on episode.Images / episode.Ratings produce wrapped
// errors so the caller can surface them (`xbmc: EpisodeMetadata:
// episode N: ...`).
func buildEpisodeDetailsXML(ep episodes.Episode, episodeFile *episodefiles.File, watched bool, settings Settings) (episodeDetailsXML, error) {
	details := episodeDetailsXML{
		Title:   ep.Title,
		Season:  ep.SeasonNumber,
		Episode: ep.EpisodeNumber,
		Aired:   ep.AirDate,
		Plot:    ep.Overview,
		Watched: boolPascal(watched),
	}

	// Special-episode display branches. C# `:312-320`:
	//   season=0 && AiredAfterSeasonNumber → only displayafterseason;
	//   season=0 && AiredBeforeSeasonNumber → displayseason + displayepisode (AiredBefore ?? -1).
	if ep.SeasonNumber == 0 && ep.AiredAfterSeasonNumber != nil {
		v := *ep.AiredAfterSeasonNumber
		details.DisplayAfterSeason = &v
	} else if ep.SeasonNumber == 0 && ep.AiredBeforeSeasonNumber != nil {
		v := *ep.AiredBeforeSeasonNumber
		details.DisplaySeason = &v
		// AiredBeforeEpisodeNumber ?? -1.
		ne := -1
		if ep.AiredBeforeEpisodeNumber != nil {
			ne = *ep.AiredBeforeEpisodeNumber
		}
		details.DisplayEpisode = &ne
	}

	// `<uniqueid type="tvdb" default="true">` then `<uniqueid type="sonarr">`.
	// C# `:322-328`. The TVDB id can be 0 on episodes that haven't
	// hydrated SkyHook yet; C# still emits "0" — match.
	tvdbVal := 0
	if ep.TvdbID != nil {
		tvdbVal = *ep.TvdbID
	}
	details.UniqueIDs = []uniqueIDXML{
		{Type: "tvdb", Default: "true", Value: strconv.Itoa(tvdbVal)},
		{Type: "sonarr", Value: strconv.Itoa(ep.ID)},
	}

	// Thumb 3-state branch — see C# `:331-338`:
	//   image == nil → emit empty <thumb></thumb>;
	//   image != nil && EpisodeImageThumb → emit <thumb>URL</thumb>;
	//   image != nil && !EpisodeImageThumb → DO NOT emit thumb.
	imageURL, imageFound, decodeErr := findScreenshotURL(ep.Images)
	if decodeErr != nil {
		return episodeDetailsXML{}, fmt.Errorf("xbmc: EpisodeMetadata: episode %d: decode Images: %w", ep.ID, decodeErr)
	}
	switch {
	case !imageFound:
		empty := ""
		details.Thumb = &empty
	case settings.EpisodeImageThumb:
		url := imageURL
		details.Thumb = &url
	}
	// else: leave details.Thumb == nil → omitted from XML.

	// Rating gate — C# `:342-345`. Votes > 0.
	ratings, decodeErr := decodeRatings(ep.Ratings)
	if decodeErr != nil {
		return episodeDetailsXML{}, fmt.Errorf("xbmc: EpisodeMetadata: episode %d: decode Ratings: %w", ep.ID, decodeErr)
	}
	if ratings.Votes > 0 {
		formatted := strconv.FormatFloat(ratings.Value, 'f', -1, 64)
		details.Rating = &formatted
	}

	// MediaInfo streamdetails sub-document — C# `:347-410`.
	if episodeFile != nil && episodeFile.MediaInfo != nil {
		fi := buildFileInfoXML(episodeFile.MediaInfo, episodeFile)
		details.FileInfo = &fi
	}

	return details, nil
}

// buildFileInfoXML ports the C# MediaInfo block at `:347-410`. The
// aspect ratio is `(float)Width / (float)Height` matching the C# cast
// at `:355`; division by zero is guarded (Go would panic on int/0,
// the C# float coercion yields Infinity — the Go port returns "0" to
// avoid emitting "+Inf" which Kodi rejects). Documented divergence.
//
// AudioStreams + SubtitleStreams iterate with C# `:387-406` ordering.
// `audio.Codec` falls back to AudioStream.Format (deferred:
// MediaInfoFormatter.FormatAudioCodec — see top-of-file note).
func buildFileInfoXML(mi *mediainfo.Model, episodeFile *episodefiles.File) fileInfoXML {
	sceneOrFile := getSceneOrFileName(episodeFile)

	aspect := "0"
	if mi.Height > 0 {
		aspect = strconv.FormatFloat(float64(mi.Width)/float64(mi.Height), 'f', -1, 64)
	}

	rt := mi.RunTime.Duration()
	durationMinutes := rt.Minutes()
	durationSeconds := math.Round(rt.Seconds())

	video := videoStreamXML{
		Aspect:            aspect,
		Bitrate:           mi.VideoBitrate,
		Codec:             formatVideoCodec(mi, sceneOrFile),
		Framerate:         strconv.FormatFloat(mi.VideoFps, 'f', -1, 64),
		Height:            mi.Height,
		ScanType:          mi.ScanType,
		Width:             mi.Width,
		Duration:          strconv.FormatFloat(durationMinutes, 'f', -1, 64),
		DurationInSeconds: int64(durationSeconds),
	}
	// HDR type mapping — C# `:367-381`. The C# tree emits hdrtype
	// for: every DolbyVision variant → "dolbyvision"; Hdr10/Hdr10Plus
	// /Pq10 → "hdr10"; Hlg10 → "hlg"; None → "" (empty element).
	// HdrUnknown is NOT mapped — no hdrtype element.
	if hdr, ok := hdrTypeName(mi.VideoHdrFormat); ok {
		video.HdrType = &hdr
	}

	var audios []audioStreamXML
	for _, a := range mi.AudioStreams {
		audios = append(audios, audioStreamXML{
			Bitrate:  a.Bitrate,
			Channels: a.Channels,
			Codec:    formatAudioCodec(a, sceneOrFile),
			Language: a.Language,
		})
	}

	var subs []subtitleStreamXML
	for _, sub := range mi.SubtitleStreams {
		subs = append(subs, subtitleStreamXML{Language: sub.Language})
	}

	return fileInfoXML{StreamDetails: streamDetailsXML{
		Video:     video,
		Audios:    audios,
		Subtitles: subs,
	}}
}

// hdrTypeName maps the [mediainfo.HdrFormat] to the Kodi hdrtype
// string. ok==false signals "no hdrtype element". Mirrors the C#
// branch ladder at `XbmcMetadata.cs:367-381`.
func hdrTypeName(format mediainfo.HdrFormat) (string, bool) {
	switch format {
	case mediainfo.HdrDolbyVision, mediainfo.HdrDolbyVisionHdr10, mediainfo.HdrDolbyVisionHdr10Plus, mediainfo.HdrDolbyVisionHlg, mediainfo.HdrDolbyVisionSdr:
		return "dolbyvision", true
	case mediainfo.Hdr10, mediainfo.Hdr10Plus, mediainfo.HdrPq10:
		return "hdr10", true
	case mediainfo.HdrHlg10:
		return "hlg", true
	case mediainfo.HdrNone:
		// C# `:378` emits an empty <hdrtype></hdrtype> for the None
		// case. Use empty string to match.
		return "", true
	default:
		// HdrUnknown + any future enum value: no hdrtype element.
		return "", false
	}
}

// formatVideoCodec is the deferred Go-port shim for C#
// `MediaInfoFormatter.FormatVideoCodec`. Returns the raw
// VideoFormat verbatim. TODO when that helper ports (likely as a
// follow-up cleanup or queue [W6] add-on): apply scene-name +
// profile-aware codec normalisation.
//
// _sceneOrFile is the reserved arg the future formatter consumes;
// kept on the signature so the call sites don't need rewriting.
func formatVideoCodec(mi *mediainfo.Model, _ string) string {
	if mi == nil {
		return ""
	}
	return mi.VideoFormat
}

// formatAudioCodec is the deferred Go-port shim for C#
// `MediaInfoFormatter.FormatAudioCodec`. Returns the raw audio Format
// verbatim. TODO see [formatVideoCodec].
func formatAudioCodec(a mediainfo.AudioStream, _ string) string {
	return a.Format
}

// findScreenshotURL decodes the episode.Images JSON-blob and
// returns the RemoteUrl of the [mediacover.CoverTypeScreenshot]
// entry (if any). `found` reports whether a screenshot was located;
// callers use it for the 3-state thumb branch.
func findScreenshotURL(blob string) (url string, found bool, err error) {
	covers, err := decodeCovers(blob)
	if err != nil {
		return "", false, err
	}
	for _, c := range covers {
		if c.CoverType == mediacover.CoverTypeScreenshot {
			return c.RemoteUrl, true, nil
		}
	}
	return "", false, nil
}

// getSceneOrFileName ports the C# `EpisodeFileExtensions.GetSceneOrFileName`
// at `src/NzbDrone.Core/MediaFiles/EpisodeFileExtensions.cs`. Returns
// SceneName when non-empty, else the stem of RelativePath.
//
// Documented divergence: the C# helper also handles ParsedEpisodeInfo
// title round-tripping in edge cases; the Go port keeps the simple
// 2-arm form which covers every call site we've audited so far.
func getSceneOrFileName(episodeFile *episodefiles.File) string {
	if episodeFile == nil {
		return ""
	}
	if strings.TrimSpace(episodeFile.SceneName) != "" {
		return episodeFile.SceneName
	}
	stem := filepath.Base(episodeFile.RelativePath)
	if ext := filepath.Ext(stem); ext != "" {
		stem = stem[:len(stem)-len(ext)]
	}
	return stem
}

// boolPascal converts a Go bool to .NET's `Boolean.ToString()` form —
// "True" or "False". The C# tree emits these capital-cased forms in
// the XElement body when adding a bool. Kodi parses both cases, but
// the Go port matches C# for visual continuity with existing
// Kodi-userbase .nfo files.
func boolPascal(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
