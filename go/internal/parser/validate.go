package parser

import "errors"

// ValidateForSeriesType ports
// `ValidateParsedEpisodeInfo.ValidateForSeriesType` at
// `src/NzbDrone.Core/Parser/ValidateParsedEpisodeInfo.cs:12-31`.
//
// Reports whether `parsed` is consistent with the given series
// type. Currently the only check: a daily-style parse against a
// Standard series is rejected (returns false). All other
// combinations return true.
//
// The Go port drops the C# `warnIfInvalid` parameter — the C# version
// uses it to switch the log level (Warn vs Debug) when the validation
// fails. The logger isn't wired through this package yet; callers
// can log the reason themselves based on the boolean return. When
// the logger plumbing lands, the parameter can be re-added without a
// compatibility break.
//
// Pass the series's SeriesType (`internal/series.SeriesTypeStandard`,
// `SeriesTypeDaily`, or `SeriesTypeAnime`) as the second argument —
// the parser package can't import series without an import cycle, so
// the typed parameter is `int`.
func ValidateForSeriesType(parsed *ParsedEpisodeInfo, seriesType int) bool {
	if parsed == nil {
		return false
	}
	const seriesTypeStandard = 0
	if parsed.IsDaily() && seriesType == seriesTypeStandard {
		return false
	}
	return true
}

// ErrInvalidDate ports `Parser.InvalidDateException` at
// `src/NzbDrone.Core/Parser/InvalidDateException.cs:1-16`. The C#
// version is a `NzbDroneException` subclass; in Go it's a sentinel
// error. Use `errors.Is(err, ErrInvalidDate)` to detect.
//
// The C# code throws this from `ParseMatchCollection` at
// `Parser.cs:1142` when an airdate falls before 1970-01-01. The Go
// port doesn't yet build a daily-aware ParseMatchCollection that
// validates airdates, so this sentinel is currently unused —
// reserved for the slice that wires daily-airdate validation
// (mirrors the C# `try { ... } catch (InvalidDateException) { break; }`
// guard at `:780-784`).
var ErrInvalidDate = errors.New("parser: invalid date in parsed episode info")

// ErrInvalidSeason ports `Parser.InvalidSeasonException` at
// `src/NzbDrone.Core/Parser/InvalidSeasonException.cs:1-16`.
// Reserved for season-validation paths the Go port hasn't reached.
var ErrInvalidSeason = errors.New("parser: invalid season in parsed episode info")
