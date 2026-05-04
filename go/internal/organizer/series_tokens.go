package organizer

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/series"
)

// reservedDeviceNamesRegex matches Windows reserved device names at the
// start of a filename (`AUX`, `COM1-9`, `CON`, `LPT1-9`, `NUL`, `PRN`)
// followed by a literal dot. Mirrors the C# regex at
// `FileNameBuilder.cs:86`.
//
// `ReplaceReservedDeviceNames` re-emits these by substituting the
// trailing dot with an underscore so the resulting filename is safe
// across all OSes.
var reservedDeviceNamesRegex = regexp.MustCompile(`(?i)^(?:aux|com[1-9]|con|lpt[1-9]|nul|prn)\.`)

// ReplaceReservedDeviceNames ports
// `FileNameBuilder.ReplaceReservedDeviceNames` at `:1153-1157`.
// Substitutes the trailing dot in a Windows reserved device name
// prefix with `_`. The C# version uses `match.Value.Replace(".", "_")`;
// the Go port mirrors this exactly.
func ReplaceReservedDeviceNames(input string) string {
	return reservedDeviceNamesRegex.ReplaceAllStringFunc(input, func(match string) string {
		return strings.ReplaceAll(match, ".", "_")
	})
}

// RegisterSeriesTokens populates `handlers` with the 14 series-level
// tokens the C# `AddSeriesTokens` registers at
// `FileNameBuilder.cs:451-467`. Each handler returns a string derived
// from `s` — Title, Year, ImdbID, etc. — possibly run through one of
// the title-cleaning helpers from 2.55.
//
// Token keys are stored under their normalized form so user-written
// patterns with any combination of separators (`{Series Title}`,
// `{Series.Title}`, `{Series_Title}`, `{series-title}`) all match.
//
// The `{Series Year}` token returns the empty string when the series
// has no Year set (the C# code calls `series.Year.ToString()` which
// would emit `0`; the Go port treats `Year == nil` as "unknown" and
// emits empty — preferable for filename rendering).
//
// Truncation: the C# version applies a private `Truncate(input, format)`
// to most series-title tokens — that handles the `{Series Title:30}`
// "limit to 30 chars" form. Truncation lands in the next slice so the
// CustomFormat is currently passed through unused on those tokens.
func RegisterSeriesTokens(handlers map[string]TokenHandler, s *series.Series) {
	if s == nil {
		return
	}
	year := 0
	if s.Year != nil {
		year = *s.Year
	}

	put := func(name string, fn TokenHandler) {
		handlers[NormalizeTokenKey(name)] = fn
	}

	put("Series Title", func(_ TokenMatch) string { return s.Title })
	put("Series CleanTitle", func(_ TokenMatch) string { return CleanTitle(s.Title) })
	put("Series TitleYear", func(_ TokenMatch) string { return TitleYear(s.Title, year) })
	put("Series CleanTitleYear", func(_ TokenMatch) string { return CleanTitle(TitleYear(s.Title, year)) })
	put("Series TitleWithoutYear", func(_ TokenMatch) string { return TitleWithoutYear(s.Title) })
	put("Series CleanTitleWithoutYear", func(_ TokenMatch) string { return CleanTitle(TitleWithoutYear(s.Title)) })
	put("Series TitleThe", func(_ TokenMatch) string { return TitleThe(s.Title) })
	put("Series CleanTitleThe", func(_ TokenMatch) string { return CleanTitleThe(s.Title) })
	put("Series TitleTheYear", func(_ TokenMatch) string { return TitleYear(TitleThe(s.Title), year) })
	put("Series CleanTitleTheYear", func(_ TokenMatch) string { return CleanTitleTheYear(s.Title, year) })
	put("Series TitleTheWithoutYear", func(_ TokenMatch) string { return TitleWithoutYear(TitleThe(s.Title)) })
	put("Series CleanTitleTheWithoutYear", func(_ TokenMatch) string { return CleanTitleThe(TitleWithoutYear(s.Title)) })
	put("Series TitleFirstCharacter", func(_ TokenMatch) string { return TitleFirstCharacter(TitleThe(s.Title)) })
	put("Series Year", func(_ TokenMatch) string {
		if year == 0 {
			return ""
		}
		return strconv.Itoa(year)
	})
}

// RegisterIDTokens populates `handlers` with the 4 series-id tokens the
// C# `AddIdTokens` registers at `:716-722`.
//
// `{TvMazeId}` and `{TmdbId}` emit empty when the value is ≤ 0 (matches
// the C# `> 0 ? .ToString() : string.Empty` guard). `{TvdbId}` emits
// the int as decimal regardless of value (matches `.ToString()`).
// `{ImdbId}` emits the empty string when nil/empty.
func RegisterIDTokens(handlers map[string]TokenHandler, s *series.Series) {
	if s == nil {
		return
	}
	put := func(name string, fn TokenHandler) {
		handlers[NormalizeTokenKey(name)] = fn
	}
	put("ImdbId", func(_ TokenMatch) string { return s.ImdbID })
	put("TvdbId", func(_ TokenMatch) string { return strconv.Itoa(s.TvdbID) })
	put("TvMazeId", func(_ TokenMatch) string {
		if s.TvMazeID <= 0 {
			return ""
		}
		return strconv.Itoa(s.TvMazeID)
	})
	put("TmdbId", func(_ TokenMatch) string {
		if s.TmdbID <= 0 {
			return ""
		}
		return strconv.Itoa(s.TmdbID)
	})
}

// RegisterSeasonTokens populates `handlers` with the `{Season}` token
// the C# `AddSeasonTokens` registers at `:595-598`. The handler honours
// the user-supplied `:00`-style zero-pad format via [FormatNumber].
func RegisterSeasonTokens(handlers map[string]TokenHandler, seasonNumber int) {
	handlers[NormalizeTokenKey("Season")] = func(tm TokenMatch) string {
		return FormatNumber(seasonNumber, tm.CustomFormat)
	}
}

// FolderOptions encapsulates the subset of `NamingConfig` needed for
// the [GetSeriesFolder] / [GetSeasonFolder] paths. Pulled out so
// callers don't have to drag the full namingconfig.Config into the
// organizer surface.
//
// `SeriesFolderFormat`, `SeasonFolderFormat`, `SpecialsFolderFormat`
// are the user-configured pattern strings. `Clean` carries the
// CleanFileName options that the per-token replacement applies.
type FolderOptions struct {
	SeriesFolderFormat   string
	SeasonFolderFormat   string
	SpecialsFolderFormat string
	Clean                CleanNameOptions
}

// GetSeriesFolder ports `FileNameBuilder.GetSeriesFolder(series, namingConfig)`
// at `FileNameBuilder.cs:258-277`. Builds the per-series folder name
// from `opts.SeriesFolderFormat` by registering the series + ID tokens,
// running the replacement, then applying:
//
//  1. `CleanFolderName` — collapses runs of separator chars and
//     trims edges.
//  2. `ReplaceReservedDeviceNames` — Windows reserved-name guard.
//  3. `{ellipsis}` → `...` literal substitution (for the truncate
//     token output landing in 2.58).
func GetSeriesFolder(s *series.Series, opts FolderOptions) string {
	handlers := map[string]TokenHandler{}
	RegisterSeriesTokens(handlers, s)
	RegisterIDTokens(handlers, s)

	folder := ReplaceTokens(opts.SeriesFolderFormat, handlers, opts.Clean, false)
	folder = CleanFolderName(folder)
	folder = ReplaceReservedDeviceNames(folder)
	folder = strings.ReplaceAll(folder, "{ellipsis}", "...")
	return folder
}

// GetSeasonFolder ports `FileNameBuilder.GetSeasonFolder(series, seasonNumber, namingConfig)`
// at `:279-300`. Builds the per-season subfolder name. Registers
// series + ID + Season tokens; uses `SpecialsFolderFormat` when
// `seasonNumber == 0`, else `SeasonFolderFormat`.
func GetSeasonFolder(s *series.Series, seasonNumber int, opts FolderOptions) string {
	handlers := map[string]TokenHandler{}
	RegisterSeriesTokens(handlers, s)
	RegisterIDTokens(handlers, s)
	RegisterSeasonTokens(handlers, seasonNumber)

	format := opts.SeasonFolderFormat
	if seasonNumber == 0 {
		format = opts.SpecialsFolderFormat
	}
	folder := ReplaceTokens(format, handlers, opts.Clean, false)
	folder = CleanFolderName(folder)
	folder = ReplaceReservedDeviceNames(folder)
	folder = strings.ReplaceAll(folder, "{ellipsis}", "...")
	return folder
}

// BuildSeasonPath ports `FileNameBuilder.BuildSeasonPath(series, seasonNumber)`
// at `:242-256`. Joins `series.Path` with the season folder when the
// series uses season folders; otherwise returns the series root path.
//
// The C# `Path.Combine` is mirrored via Go's `filepath.Join`. The
// season-folder name is run through `CleanFileName` (default
// NamingConfig options) before the join — matches the C# `:250` step.
func BuildSeasonPath(s *series.Series, seasonNumber int, opts FolderOptions) string {
	if s == nil {
		return ""
	}
	if !s.SeasonFolder {
		return s.Path
	}
	folder := GetSeasonFolder(s, seasonNumber, opts)
	folder = CleanFileNameDefault(folder)
	return filepath.Join(s.Path, folder)
}
