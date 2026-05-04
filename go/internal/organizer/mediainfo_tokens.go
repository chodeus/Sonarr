package organizer

import (
	"fmt"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
)

// RegisterMediaInfoTokens ports the surface of
// `FileNameBuilder.AddMediaInfoTokens` at
// `src/NzbDrone.Core/Organizer/FileNameBuilder.cs:651-694`.
//
// When `m` is nil (no MediaInfo recorded yet), all 14 tokens register
// to empty — preserves the C# `_logger.Trace + return` short-circuit
// at `:653-657`. Existing user patterns containing `{MediaInfo ...}`
// still parse and render to empty.
//
// When `m` is non-nil, the codec / bit-depth / channel / Simple /
// dynamic-range tokens are wired to real handlers via the
// `mediainfo.Format*` helpers. The four language tokens
// (AudioLanguages / AudioLanguagesAll / SubtitleLanguages /
// SubtitleLanguagesAll) remain stubbed to empty in this slice; their
// full port requires the C# `Iso639BTMap` + `CultureInfo.TwoLetter
// ISOLanguageName` lookups (`:747-814`). Adding them is a follow-up
// slice (~80 LOC); the stubbing here keeps the existing render
// behaviour for those tokens unchanged.
//
// The `Full` token's behaviour matches C# `:688`: it concatenates
// videoCodec + audioCodec + audioLangs + subtitleLangs. With the
// language tokens currently empty, `{MediaInfo Full}` renders as
// `{videoCodec} {audioCodec}` followed by two trailing spaces — the
// SAME shape `{MediaInfo Simple}` produces, modulo whitespace.
// Pinned by test.
//
// `sceneName` should be the scene-or-file name of the episode file
// (derived via [SceneOrFileName] or its host equivalent). It's
// used by the codec resolvers as a tie-breaker — h264 → x264 / AVC
// / h264 splits based on what's in the release name.
//
// Tokens registered (15 total — keys normalised via NormalizeTokenKey):
//   - `MediaInfo Video` / `MediaInfo VideoCodec` / `MediaInfo VideoBitDepth`
//   - `MediaInfo Audio` / `MediaInfo AudioCodec` / `MediaInfo AudioChannels`
//   - `MediaInfo AudioLanguages` / `MediaInfo AudioLanguagesAll`
//     (stubbed to empty — pending Iso639 culture-mapper port)
//   - `MediaInfo SubtitleLanguages` / `MediaInfo SubtitleLanguagesAll`
//     (stubbed)
//   - `MediaInfo Simple` / `MediaInfo Full`
//   - `MediaInfo VideoDynamicRange` / `MediaInfo VideoDynamicRangeType`
func RegisterMediaInfoTokens(handlers map[string]TokenHandler, m *mediainfo.Model, sceneName string) {
	if m == nil {
		registerMediaInfoStubs(handlers)
		return
	}

	videoCodec := mediainfo.FormatVideoCodec(m, sceneName)
	audioCodec := mediainfo.FormatAudioCodec(m.PrimaryAudioStream(), sceneName)
	audioChannels := mediainfo.FormatAudioChannels(m.PrimaryAudioStream())

	// VideoBitDepth: defaults to "8" when 0 — matches C# `:668`.
	videoBitDepth := "8"
	if m.VideoBitDepth > 0 {
		videoBitDepth = fmt.Sprintf("%d", m.VideoBitDepth)
	}

	// AudioChannels: rendered as "5.1" / "7.1" / "2.0" via F1
	// formatting; empty when 0. Matches C# `:669-671`.
	audioChannelsFormatted := ""
	if audioChannels > 0 {
		audioChannelsFormatted = strconvFloat1(audioChannels)
	}

	// Codec / bit-depth / channel tokens.
	handlers[NormalizeTokenKey("MediaInfo Video")] = staticToken(videoCodec)
	handlers[NormalizeTokenKey("MediaInfo VideoCodec")] = staticToken(videoCodec)
	handlers[NormalizeTokenKey("MediaInfo VideoBitDepth")] = staticToken(videoBitDepth)

	handlers[NormalizeTokenKey("MediaInfo Audio")] = staticToken(audioCodec)
	handlers[NormalizeTokenKey("MediaInfo AudioCodec")] = staticToken(audioCodec)
	handlers[NormalizeTokenKey("MediaInfo AudioChannels")] = staticToken(audioChannelsFormatted)

	// Composite tokens. C# `:686` does NOT trim — emits `" "` when
	// both codecs are empty. Preserve that for exact parity.
	simple := videoCodec + " " + audioCodec
	handlers[NormalizeTokenKey("MediaInfo Simple")] = staticToken(simple)
	// Full = `{video} {audio}{audioLangs} {subLangs}` — matches the
	// C# concat at `:688` with the language tokens currently rendering
	// to empty. Trailing whitespace is intentional and matches C#.
	full := videoCodec + " " + audioCodec + " "
	handlers[NormalizeTokenKey("MediaInfo Full")] = staticToken(full)

	// Dynamic-range tokens.
	handlers[NormalizeTokenKey("MediaInfo VideoDynamicRange")] =
		staticToken(mediainfo.FormatVideoDynamicRange(m))
	handlers[NormalizeTokenKey("MediaInfo VideoDynamicRangeType")] =
		staticToken(mediainfo.FormatVideoDynamicRangeType(m))

	// Language tokens stubbed (deferred — see top-of-file comment).
	stubLanguageTokens(handlers)
}

// RegisterMediaInfoStubs registers all 14 MediaInfo tokens to empty.
// Retained for callers that want the no-op behaviour without
// constructing a `mediainfo.Model`. New code should prefer
// [RegisterMediaInfoTokens] with a nil `*Model` — same effect.
//
// Deprecated: prefer RegisterMediaInfoTokens(handlers, nil, "").
func RegisterMediaInfoStubs(handlers map[string]TokenHandler) {
	registerMediaInfoStubs(handlers)
}

// registerMediaInfoStubs is the private no-op implementation shared
// between the deprecated public stub and the nil-Model branch of
// RegisterMediaInfoTokens.
func registerMediaInfoStubs(handlers map[string]TokenHandler) {
	stub := staticToken("")
	for _, name := range mediaInfoTokenNames {
		handlers[NormalizeTokenKey(name)] = stub
	}
}

// stubLanguageTokens registers ONLY the four language tokens to empty.
// Used by RegisterMediaInfoTokens after wiring the codec tokens — the
// language tokens still need the Iso639 culture-mapping port.
func stubLanguageTokens(handlers map[string]TokenHandler) {
	stub := staticToken("")
	for _, name := range []string{
		"MediaInfo AudioLanguages",
		"MediaInfo AudioLanguagesAll",
		"MediaInfo SubtitleLanguages",
		"MediaInfo SubtitleLanguagesAll",
	} {
		handlers[NormalizeTokenKey(name)] = stub
	}
}

// staticToken returns a TokenHandler that ignores the TokenMatch and
// always returns `s`. Used by handlers whose value is fully resolved
// at registration time.
func staticToken(s string) TokenHandler {
	return func(TokenMatch) string { return s }
}

// strconvFloat1 formats a float64 with exactly one digit after the
// decimal point — mirrors C# `audioChannels.ToString("F1",
// CultureInfo.InvariantCulture)` at `:670`. Always emits a `.` (not a
// locale-specific decimal mark).
func strconvFloat1(v float64) string {
	return fmt.Sprintf("%.1f", v)
}

// SceneOrFileName mirrors `EpisodeFile.GetSceneOrFileName` at
// `src/NzbDrone.Core/MediaFiles/EpisodeFile.cs:35-55`. Used by the
// host wiring to derive the sceneName the MediaInfoFormatter codec
// rules use as tie-breaker.
//
// Priority:
//
//  1. SceneName, if non-empty.
//  2. Filename (without extension) of RelativePath, if non-empty.
//  3. Filename (without extension) of OriginalFilePath, if non-empty.
//  4. Empty string.
//
// (The C# version's third arm uses `Path` — the absolute file path —
// which the Go port doesn't persist on the File struct. The closest
// approximation is OriginalFilePath, which holds the absolute path
// of the file at import time.)
func SceneOrFileName(file *episodefiles.File) string {
	if file == nil {
		return ""
	}
	if strings.TrimSpace(file.SceneName) != "" {
		return file.SceneName
	}
	if rel := strings.TrimSpace(file.RelativePath); rel != "" {
		return trimExtension(filepath.Base(rel))
	}
	if orig := strings.TrimSpace(file.OriginalFilePath); orig != "" {
		return trimExtension(filepath.Base(orig))
	}
	return ""
}

func trimExtension(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return strings.TrimSuffix(name, ext)
}

// mediaInfoTokenNames is the canonical list of every {MediaInfo *}
// token the FileNameBuilder recognises. Ordering matches the
// declaration order at `:673-693`. Used by the no-op stubs registrar.
var mediaInfoTokenNames = []string{
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
}
