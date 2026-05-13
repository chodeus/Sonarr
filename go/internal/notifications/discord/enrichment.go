package discord

import (
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/series"
)

// fieldsContain returns true when the persisted GrabFields /
// ImportFields slice contains the given field id. Used by the
// embed-image wiring + by the Codecs/Languages/Subtitles arms — all
// three are conditionally enabled per the user's Settings.
func fieldsContain(fields []int, id int) bool {
	for _, f := range fields {
		if f == id {
			return true
		}
	}
	return false
}

// applySeriesCoverImages decodes the series.Images blob once and
// stamps the embed's Thumbnail (Poster) + Image (Fanart) slots when
// the corresponding settings field is enabled. Mirrors C#
// `Discord.cs:54-69` (OnGrab) + `:157-172` (OnDownload) + similar
// blocks in OnImportComplete. Decode failures swallow to no-image
// — notification dispatch shouldn't fail on a bad Images blob.
func applySeriesCoverImages(embed *Embed, ser *series.Series, includePoster, includeFanart bool) {
	if embed == nil || ser == nil || (!includePoster && !includeFanart) {
		return
	}
	covers, _ := mediacover.DecodeSeriesImages(ser)
	if includePoster {
		if c := mediacover.FindCover(covers, mediacover.CoverTypePoster); c != nil {
			if u := preferRemote(c); u != "" {
				embed.Thumbnail = &Image{URL: u}
			}
		}
	}
	if includeFanart {
		if c := mediacover.FindCover(covers, mediacover.CoverTypeFanart); c != nil {
			if u := preferRemote(c); u != "" {
				embed.Image = &Image{URL: u}
			}
		}
	}
}

// preferRemote returns the publicly-reachable cover URL — RemoteUrl
// (upstream metadata source) if set, else Url (Sonarr-API-served,
// not externally reachable but better than nothing for self-hosted
// Discord bots running inside the same network).
func preferRemote(c *mediacover.Cover) string {
	if c.RemoteUrl != "" {
		return c.RemoteUrl
	}
	return c.Url
}

// mediaInfoCodecsString composes the "video / audio channels" string
// the C# emitter at `Discord.cs:200-203` produces:
//
//	{FormatVideoCodec} / {FormatAudioCodec} {FormatAudioChannels}
//
// Empty when MediaInfo has no usable video/audio data. Used by the
// ImportFieldCodecs case in OnDownload.
func mediaInfoCodecsString(f *episodefiles.File) string {
	if f == nil || f.MediaInfo == nil {
		return ""
	}
	video := mediainfo.FormatVideoCodec(f.MediaInfo, f.SceneName)
	stream := f.MediaInfo.PrimaryAudioStream()
	if stream == nil && video == "" {
		return ""
	}
	audio := ""
	channels := ""
	if stream != nil {
		audio = mediainfo.FormatAudioCodec(stream, f.SceneName)
		channels = fmt.Sprintf("%.1f", mediainfo.FormatAudioChannels(stream))
	}
	return fmt.Sprintf("%s / %s %s", video, audio, channels)
}

// audioLanguage / subtitleLanguage extract the Language field from
// the typed AudioStream / SubtitleStream slices for
// streamLanguagesString. Closures-over-the-slice avoids two
// near-identical loop bodies.
func audioLanguage(s mediainfo.AudioStream) string       { return s.Language }
func subtitleLanguage(s mediainfo.SubtitleStream) string { return s.Language }

// streamLanguagesString joins per-stream Language values with "/"
// matching C# `ConcatToString("/")` at `Discord.cs:216` + `:220`.
// Empty Language values are preserved (C# emits them too —
// preserves wire-format parity even though it looks like a bug).
// Empty slice returns empty string.
func streamLanguagesString[T any](streams []T, get func(T) string) string {
	if len(streams) == 0 {
		return ""
	}
	parts := make([]string, len(streams))
	for i := range streams {
		parts[i] = get(streams[i])
	}
	return strings.Join(parts, "/")
}
