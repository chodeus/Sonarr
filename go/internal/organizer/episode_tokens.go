package organizer

import (
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesType enumerates the C# `SeriesTypes` enum values. Anime is the
// only one that uses the absolute-episode-number tokens; standard +
// daily fall through to the season+episode numbering.
type SeriesType int

// SeriesType constants — match the C# enum integer Ids for stable
// persistence.
const (
	SeriesTypeStandard SeriesType = 0
	SeriesTypeDaily    SeriesType = 1
	SeriesTypeAnime    SeriesType = 2
)

// episodeRegex matches `{episode(:0+)?}` tokens (case-insensitive).
// Mirrors the C# `EpisodeRegex` at `FileNameBuilder.cs:51-52`.
var episodeRegex = regexp.MustCompile(`(?i)(?P<episode>\{episode(?::0+)?\})`)

// seasonRegex matches `{season(:0+)?}` tokens. Mirrors C# `:54-55`.
var seasonRegex = regexp.MustCompile(`(?i)(?P<season>\{season(?::0+)?\})`)

// absoluteEpisodeRegex matches `{absolute(:0+)?}`. Mirrors C# `:57-58`.
var absoluteEpisodeRegex = regexp.MustCompile(`(?i)(?P<absolute>\{absolute(?::0+)?\})`)

// seasonEpisodePatternRegex matches the full season-episode token
// shape `s{season}e{episode}` (with optional separator between and
// flanking separators). Mirrors C# `SeasonEpisodePatternRegex` at
// `:60-61`. Used to discover the user's preferred separator/spelling
// before we expand the multi-episode format.
//
// Group meaning (matches the C# capture-group names):
//
//   - separator: optional flanking separator chars
//   - seasonEpisode: the whole `s{season}e{episode}` chunk
//   - episodeSeparator: the chars between season and episode tokens
//     (e.g. `e` in `S01E01`, ` x ` in `1 x 01`).
//   - episode: the inner `{episode}` token (with format).
var seasonEpisodePatternRegex = regexp.MustCompile(
	`(?i)(?P<separatorL>[- ._]+?)?(?P<seasonEpisode>s?\{season(?::0+)?\}(?P<episodeSeparator>[- ._]?[ex])(?P<episode>\{episode(?::0+)?\}))(?P<separatorR>[- ._]+?)?`,
)

// absoluteEpisodePatternRegex matches the absolute-episode token
// shape `{absolute}` with flanking separators. Mirrors C# `:63-64`.
var absoluteEpisodePatternRegex = regexp.MustCompile(
	`(?i)(?P<separatorL>[- ._]+?)?(?P<absolute>\{absolute(?::0+)?\})(?P<separatorR>[- ._]+?)?`,
)

// EpisodeFormat is the parsed shape of one season-episode token.
// Mirrors `EpisodeFormat.cs`.
type EpisodeFormat struct {
	Separator            string
	EpisodePattern       string
	EpisodeSeparator     string
	SeasonEpisodePattern string
}

// AbsoluteEpisodeFormat is the parsed shape of one absolute-episode
// token. Mirrors `AbsoluteEpisodeFormat.cs`.
type AbsoluteEpisodeFormat struct {
	Separator              string
	AbsoluteEpisodePattern string
}

// ExtractEpisodeFormats walks `pattern` and returns one [EpisodeFormat]
// per distinct season-episode shape. De-duplicates by
// SeasonEpisodePattern (matches the C# `DistinctBy` at `:471`).
func ExtractEpisodeFormats(pattern string) []EpisodeFormat {
	matches := seasonEpisodePatternRegex.FindAllStringSubmatch(pattern, -1)
	names := seasonEpisodePatternRegex.SubexpNames()
	seen := make(map[string]bool)
	out := make([]EpisodeFormat, 0)
	get := func(groups []string, name string) string {
		for i, n := range names {
			if n == name {
				return groups[i]
			}
		}
		return ""
	}
	for _, m := range matches {
		sep := get(m, "separatorL")
		if sep == "" {
			sep = get(m, "separatorR")
		}
		ef := EpisodeFormat{
			Separator:            sep,
			EpisodePattern:       get(m, "episode"),
			EpisodeSeparator:     get(m, "episodeSeparator"),
			SeasonEpisodePattern: get(m, "seasonEpisode"),
		}
		if seen[ef.SeasonEpisodePattern] {
			continue
		}
		seen[ef.SeasonEpisodePattern] = true
		out = append(out, ef)
	}
	return out
}

// ExtractAbsoluteEpisodeFormats walks `pattern` and returns one
// [AbsoluteEpisodeFormat] per distinct absolute-episode shape.
// De-duplicates by AbsoluteEpisodePattern (matches the C# `DistinctBy`
// at `:534`). Default separator is `-` when none captured (matches
// the C# `IsNotNullOrWhiteSpace ? ... : "-"` ternary at `:988`).
func ExtractAbsoluteEpisodeFormats(pattern string) []AbsoluteEpisodeFormat {
	matches := absoluteEpisodePatternRegex.FindAllStringSubmatch(pattern, -1)
	names := absoluteEpisodePatternRegex.SubexpNames()
	seen := make(map[string]bool)
	out := make([]AbsoluteEpisodeFormat, 0)
	get := func(groups []string, name string) string {
		for i, n := range names {
			if n == name {
				return groups[i]
			}
		}
		return ""
	}
	for _, m := range matches {
		sep := get(m, "separatorL")
		if sep == "" {
			sep = get(m, "separatorR")
		}
		if strings.TrimSpace(sep) == "" {
			sep = "-"
		}
		af := AbsoluteEpisodeFormat{
			Separator:              sep,
			AbsoluteEpisodePattern: get(m, "absolute"),
		}
		if seen[af.AbsoluteEpisodePattern] {
			continue
		}
		seen[af.AbsoluteEpisodePattern] = true
		out = append(out, af)
	}
	return out
}

// ReplaceNumberToken ports `FileNameBuilder.ReplaceNumberToken` at
// `FileNameBuilder.cs:960-969`. Strips the brace wrapping and
// optional `:format` from `token`, then formats `value` per the
// captured format string.
func ReplaceNumberToken(token string, value int) string {
	inner := strings.Trim(token, "{}")
	parts := strings.SplitN(inner, ":", 2)
	if len(parts) == 1 {
		return strconv.Itoa(value)
	}
	return FormatNumber(value, parts[1])
}

// ReplaceSeasonTokens ports `FileNameBuilder.ReplaceSeasonTokens` at
// `:955-958`. Replaces every `{season}`-shape token in `pattern` with
// the formatted seasonNumber.
func ReplaceSeasonTokens(pattern string, seasonNumber int) string {
	return seasonRegex.ReplaceAllStringFunc(pattern, func(match string) string {
		return ReplaceNumberToken(match, seasonNumber)
	})
}

// FormatNumberTokens ports `FileNameBuilder.FormatNumberTokens` at
// `:915-927`. Concatenates one render per episode, using `basePattern`
// for the first and `formatPattern` for the rest. Then applies
// [ReplaceSeasonTokens] using the first episode's SeasonNumber.
func FormatNumberTokens(basePattern, formatPattern string, eps []episodes.Episode) string {
	if len(eps) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, e := range eps {
		ptn := formatPattern
		if i == 0 {
			ptn = basePattern
		}
		ptn = episodeRegex.ReplaceAllStringFunc(ptn, func(match string) string {
			return ReplaceNumberToken(match, e.EpisodeNumber)
		})
		sb.WriteString(ptn)
	}
	return ReplaceSeasonTokens(sb.String(), eps[0].SeasonNumber)
}

// FormatAbsoluteNumberTokens ports `:929-941`. Like [FormatNumberTokens]
// but for absolute episode numbers.
func FormatAbsoluteNumberTokens(basePattern, formatPattern string, eps []episodes.Episode) string {
	if len(eps) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, e := range eps {
		ptn := formatPattern
		if i == 0 {
			ptn = basePattern
		}
		abs := 0
		if e.AbsoluteEpisodeNumber != nil {
			abs = *e.AbsoluteEpisodeNumber
		}
		ptn = absoluteEpisodeRegex.ReplaceAllStringFunc(ptn, func(match string) string {
			return ReplaceNumberToken(match, abs)
		})
		sb.WriteString(ptn)
	}
	return ReplaceSeasonTokens(sb.String(), eps[0].SeasonNumber)
}

// FormatRangeNumberTokens ports `:943-953`. Folds the episode list to
// `[first]` (single-ep) or `[first, last]` (multi-ep) before delegating
// to [FormatNumberTokens]. Used by the Range / PrefixedRange
// MultiEpisodeStyle paths.
func FormatRangeNumberTokens(seasonEpisodePattern, formatPattern string, eps []episodes.Episode) string {
	if len(eps) == 0 {
		return ""
	}
	if len(eps) == 1 {
		return FormatNumberTokens(seasonEpisodePattern, formatPattern, eps)
	}
	return FormatNumberTokens(seasonEpisodePattern, formatPattern, []episodes.Episode{eps[0], eps[len(eps)-1]})
}

// AddSeasonEpisodeNumberingTokens ports
// `FileNameBuilder.AddSeasonEpisodeNumberingTokens` at `:469-530`.
// Mutates `pattern` by replacing each season-episode shape with a
// `{Season EpisodeN}` placeholder; registers a handler returning the
// fully-rendered episode-pattern under that placeholder key. Honours
// the user's MultiEpisodeStyle for multi-episode formats.
//
// Returns the rewritten pattern (with placeholders); the handlers map
// is mutated in place.
//
// Also registers `{Season}` and `{Episode}` tokens (the latter only when
// there are multiple episodes — for single episodes the `{episode}`
// regex pass already handled it inline).
func AddSeasonEpisodeNumberingTokens(pattern string, handlers map[string]TokenHandler, eps []episodes.Episode, multiEpisode MultiEpisodeStyle) string {
	if len(eps) == 0 {
		return pattern
	}
	formats := ExtractEpisodeFormats(pattern)
	for index, ef := range formats {
		seasonEpisodePattern := ef.SeasonEpisodePattern
		var formatPattern string

		switch multiEpisode {
		case MultiEpisodeDuplicate:
			formatPattern = ef.Separator + ef.SeasonEpisodePattern
			seasonEpisodePattern = FormatNumberTokens(seasonEpisodePattern, formatPattern, eps)
		case MultiEpisodeRepeat:
			formatPattern = ef.EpisodeSeparator + ef.EpisodePattern
			seasonEpisodePattern = FormatNumberTokens(seasonEpisodePattern, formatPattern, eps)
		case MultiEpisodeScene:
			formatPattern = "-" + ef.EpisodeSeparator + ef.EpisodePattern
			seasonEpisodePattern = FormatNumberTokens(seasonEpisodePattern, formatPattern, eps)
		case MultiEpisodeRange:
			formatPattern = "-" + ef.EpisodePattern
			seasonEpisodePattern = FormatRangeNumberTokens(seasonEpisodePattern, formatPattern, eps)
		case MultiEpisodePrefixedRange:
			formatPattern = "-" + ef.EpisodeSeparator + ef.EpisodePattern
			seasonEpisodePattern = FormatRangeNumberTokens(seasonEpisodePattern, formatPattern, eps)
		default: // MultiEpisodeExtend
			formatPattern = "-" + ef.EpisodePattern
			seasonEpisodePattern = FormatNumberTokens(seasonEpisodePattern, formatPattern, eps)
		}

		token := "{Season Episode" + strconv.Itoa(index+1) + "}"
		pattern = strings.ReplaceAll(pattern, ef.SeasonEpisodePattern, token)
		captured := seasonEpisodePattern
		handlers[NormalizeTokenKey(token)] = func(_ TokenMatch) string { return captured }
	}

	RegisterSeasonTokens(handlers, eps[0].SeasonNumber)

	if len(eps) > 1 {
		first := eps[0].EpisodeNumber
		last := eps[len(eps)-1].EpisodeNumber
		handlers[NormalizeTokenKey("Episode")] = func(tm TokenMatch) string {
			return FormatNumber(first, tm.CustomFormat) + "-" + FormatNumber(last, tm.CustomFormat)
		}
	} else {
		ep := eps[0].EpisodeNumber
		handlers[NormalizeTokenKey("Episode")] = func(tm TokenMatch) string {
			return FormatNumber(ep, tm.CustomFormat)
		}
	}
	return pattern
}

// AddAbsoluteNumberingTokens ports `:532-593`. The anime-only
// counterpart of [AddSeasonEpisodeNumberingTokens]. For non-anime
// series or any episode missing AbsoluteEpisodeNumber, the absolute
// pattern is stripped from `pattern` (matches `:539-543`).
func AddAbsoluteNumberingTokens(pattern string, handlers map[string]TokenHandler, s *series.Series, eps []episodes.Episode, multiEpisode MultiEpisodeStyle, seriesType SeriesType) string {
	if len(eps) == 0 {
		return pattern
	}
	formats := ExtractAbsoluteEpisodeFormats(pattern)

	allHaveAbsolute := func() bool {
		for _, e := range eps {
			if e.AbsoluteEpisodeNumber == nil {
				return false
			}
		}
		return true
	}

	for index, af := range formats {
		if seriesType != SeriesTypeAnime || !allHaveAbsolute() {
			pattern = strings.ReplaceAll(pattern, af.AbsoluteEpisodePattern, "")
			continue
		}

		absolutePattern := af.AbsoluteEpisodePattern
		var formatPattern string

		switch multiEpisode {
		case MultiEpisodeDuplicate:
			formatPattern = af.Separator + af.AbsoluteEpisodePattern
			absolutePattern = FormatAbsoluteNumberTokens(absolutePattern, formatPattern, eps)
		case MultiEpisodeRepeat:
			repeatSep := strings.TrimSpace(af.Separator)
			if repeatSep == "" {
				repeatSep = " "
			}
			formatPattern = repeatSep + af.AbsoluteEpisodePattern
			absolutePattern = FormatAbsoluteNumberTokens(absolutePattern, formatPattern, eps)
		case MultiEpisodeScene:
			formatPattern = "-" + af.AbsoluteEpisodePattern
			absolutePattern = FormatAbsoluteNumberTokens(absolutePattern, formatPattern, eps)
		case MultiEpisodeRange, MultiEpisodePrefixedRange:
			formatPattern = "-" + af.AbsoluteEpisodePattern
			folded := []episodes.Episode{eps[0]}
			if len(eps) > 1 {
				folded = append(folded, eps[len(eps)-1])
			}
			absolutePattern = FormatAbsoluteNumberTokens(absolutePattern, formatPattern, folded)
		default: // Extend
			formatPattern = "-" + af.AbsoluteEpisodePattern
			absolutePattern = FormatAbsoluteNumberTokens(absolutePattern, formatPattern, eps)
		}

		token := "{Absolute Pattern" + strconv.Itoa(index+1) + "}"
		pattern = strings.ReplaceAll(pattern, af.AbsoluteEpisodePattern, token)
		captured := absolutePattern
		handlers[NormalizeTokenKey(token)] = func(_ TokenMatch) string { return captured }
	}
	_ = s // currently only seriesType is read; kept for future series-aware filters
	return pattern
}

// Truncate ports `FileNameBuilder.Truncate(input, formatter)` at
// `FileNameBuilder.cs:1207-1227`. Trims `input` to a max length
// derived from the integer-parsed `formatter`:
//
//   - Empty/whitespace input → empty result.
//   - formatter empty / not an int / `0` → input unchanged.
//   - formatter > 0 → keep the first (formatter-3) chars, append
//     `{ellipsis}`. The placeholder is substituted to `...` by
//     CleanFolderName/the BuildFileName final pass.
//   - formatter < 0 → keep the LAST (|formatter|-3) chars, prepend
//     `{ellipsis}`.
//
// Used by tokens that accept a max-length specifier (e.g.
// `{Series Title:30}` truncates to 30 chars).
func Truncate(input, formatter string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	maxLength, err := strconv.Atoi(formatter)
	if err != nil {
		return input
	}
	if maxLength == 0 {
		return input
	}
	abs := maxLength
	if abs < 0 {
		abs = -abs
	}
	if len([]rune(input)) <= abs {
		return input
	}
	if maxLength < 0 {
		runes := []rune(input)
		take := abs - 3
		if take < 0 {
			take = 0
		}
		tail := string(runes[len(runes)-take:])
		tail = strings.TrimLeft(tail, " .")
		return "{ellipsis}" + tail
	}
	runes := []rune(input)
	take := maxLength - 3
	if take < 0 {
		take = 0
	}
	head := string(runes[:take])
	head = strings.TrimRight(head, " .")
	return head + "{ellipsis}"
}

// AddEpisodeTokens ports `AddEpisodeTokens` at `:600-610`. Registers
// the `{Air Date}` token. Empty / missing AirDate emits `Unknown`
// (matches the C# else branch at `:608`).
func AddEpisodeTokens(handlers map[string]TokenHandler, eps []episodes.Episode) {
	handlers[NormalizeTokenKey("Air Date")] = func(_ TokenMatch) string {
		if len(eps) == 0 || strings.TrimSpace(eps[0].AirDate) == "" {
			return "Unknown"
		}
		return strings.ReplaceAll(eps[0].AirDate, "-", " ")
	}
}
