package parser

import (
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
)

// First slice of the ParseTitle port. Mirrors the data models at
// `NzbDrone.Core.Parser.Model.ParsedEpisodeInfo` and
// `NzbDrone.Core.Parser.Model.SeriesTitleInfo` plus the
// `NzbDrone.Core.Parser.Model.ReleaseType` enum.
//
// The 122-element ReportTitleRegex array + ParseTitle/ParseMatchCollection
// pipeline land in subsequent slices; this slice ships the foundation
// data shapes plus the small helpers that are used outside the regex
// match-walking machinery.

// ReleaseType mirrors `NzbDrone.Core.Parser.Model.ReleaseType`.
// Persisted via int value in CustomFormat / ReleaseTypeSpecification
// JSON, so the integer order is frozen.
type ReleaseType int

const (
	// ReleaseTypeUnknown is the default (no match / unclassified).
	ReleaseTypeUnknown ReleaseType = 0
	// ReleaseTypeSingleEpisode covers exactly one episode token.
	ReleaseTypeSingleEpisode ReleaseType = 1
	// ReleaseTypeMultiEpisode covers two or more episode tokens.
	ReleaseTypeMultiEpisode ReleaseType = 2
	// ReleaseTypeSeasonPack covers `FullSeason=true` releases.
	ReleaseTypeSeasonPack ReleaseType = 3
)

// String mirrors C# `Enum.ToString()` for [ReleaseType] — emits the
// PascalCase identifier for named values, falls through to the
// numeric string for unnamed values (the exact C# behaviour for
// out-of-range enum casts). Wire-shape parity with C#
// `HistoryService.cs:180,233,297,330,360` which use
// `ReleaseType.ToString()` when packing history.Data payloads.
func (r ReleaseType) String() string {
	switch r {
	case ReleaseTypeUnknown:
		return "Unknown"
	case ReleaseTypeSingleEpisode:
		return "SingleEpisode"
	case ReleaseTypeMultiEpisode:
		return "MultiEpisode"
	case ReleaseTypeSeasonPack:
		return "SeasonPack"
	}
	return strconv.Itoa(int(r))
}

// SeriesTitleInfo mirrors `NzbDrone.Core.Parser.Model.SeriesTitleInfo`.
// The Title / TitleWithoutYear / Year split is populated from the
// YearInTitleRegex match in `Parser.GetSeriesTitleInfo`; AllTitles is
// populated when TitleComponentsRegex matches (the ` (...)` /
// ` | ` / ` AKA ` patterns) OR when the regex's `<title>` group
// matched multiple times.
type SeriesTitleInfo struct {
	Title            string
	TitleWithoutYear string
	Year             int
	AllTitles        []string
}

// ParsedEpisodeInfo mirrors `NzbDrone.Core.Parser.Model.ParsedEpisodeInfo`.
// Returned by ParseTitle; populated by ParseMatchCollection from the
// surviving ReportTitleRegex match.
type ParsedEpisodeInfo struct {
	ReleaseTitle                  string
	SeriesTitle                   string
	SeriesTitleInfo               *SeriesTitleInfo
	Quality                       quality.Model
	SeasonNumber                  int
	EpisodeNumbers                []int
	AbsoluteEpisodeNumbers        []int
	SpecialAbsoluteEpisodeNumbers []float64 // C# decimal[]; Go float64 — NumberStyles.Float parse
	AirDate                       string
	Languages                     []language.Language
	FullSeason                    bool
	IsPartialSeason               bool
	IsMultiSeason                 bool
	IsSeasonExtra                 bool
	IsSplitEpisode                bool
	IsMiniSeries                  bool
	Special                       bool
	ReleaseGroup                  string
	ReleaseHash                   string
	SeasonPart                    int
	ReleaseTokens                 string
	DailyPart                     *int // C# int? nullable
}

// IsDaily mirrors the C# `ParsedEpisodeInfo.IsDaily` getter at
// `ParsedEpisodeInfo.cs:43-53`. True iff AirDate is non-empty.
func (p *ParsedEpisodeInfo) IsDaily() bool {
	return strings.TrimSpace(p.AirDate) != ""
}

// IsAbsoluteNumbering mirrors the C# getter at `ParsedEpisodeInfo.cs:55-65`.
// True iff AbsoluteEpisodeNumbers has at least one entry.
func (p *ParsedEpisodeInfo) IsAbsoluteNumbering() bool {
	return len(p.AbsoluteEpisodeNumbers) > 0
}

// IsPossibleSpecialEpisode mirrors the C# getter at `ParsedEpisodeInfo.cs:67-80`.
// Multi-condition: the parse looks like a special if it lacks
// air-date AND series title AND has zero episodes (or season 0), OR
// has a series title with Special=true, OR is the single E0 sentinel.
func (p *ParsedEpisodeInfo) IsPossibleSpecialEpisode() bool {
	noAirDate := strings.TrimSpace(p.AirDate) == ""
	noTitle := strings.TrimSpace(p.SeriesTitle) == ""
	noEpisodesOrSeasonZero := len(p.EpisodeNumbers) == 0 || p.SeasonNumber == 0
	if (noAirDate && noTitle && noEpisodesOrSeasonZero) || (!noTitle && p.Special) {
		return true
	}
	return len(p.EpisodeNumbers) == 1 && p.EpisodeNumbers[0] == 0
}

// IsPossibleSceneSeasonSpecial mirrors the C# getter at
// `ParsedEpisodeInfo.cs:82-92`. True iff seasonNumber != 0 AND there's
// exactly one episode number AND that episode number is 0.
func (p *ParsedEpisodeInfo) IsPossibleSceneSeasonSpecial() bool {
	return p.SeasonNumber != 0 && len(p.EpisodeNumbers) == 1 && p.EpisodeNumbers[0] == 0
}

// ReleaseType mirrors the C# getter at `ParsedEpisodeInfo.cs:94-115`.
// Multi → 2+ episodes; Single → 1 episode; SeasonPack → FullSeason
// flag set; otherwise Unknown.
func (p *ParsedEpisodeInfo) ReleaseType() ReleaseType {
	if len(p.EpisodeNumbers) > 1 || len(p.AbsoluteEpisodeNumbers) > 1 {
		return ReleaseTypeMultiEpisode
	}
	if len(p.EpisodeNumbers) == 1 || len(p.AbsoluteEpisodeNumbers) == 1 {
		return ReleaseTypeSingleEpisode
	}
	if p.FullSeason {
		return ReleaseTypeSeasonPack
	}
	return ReleaseTypeUnknown
}

// String mirrors `ParsedEpisodeInfo.ToString()` at
// `ParsedEpisodeInfo.cs:117-150`. Format: `<title> - <episode-string> <quality>`.
// The episode-string varies by detection:
//
//   - Daily-with-no-episode-numbers → `<airdate>`.
//   - FullSeason → `Season <NN>`.
//   - EpisodeNumbers populated → `S<NN>E<NN>(-<NN>)*`.
//   - AbsoluteEpisodeNumbers populated → `<NNN>(-<NNN>)*`.
//   - Special with SeasonNumber != 0 → `[Unknown Season <NN> Special]`.
//   - Special with SeasonNumber == 0 → `[Unknown Special]`.
//   - Otherwise → `[Unknown Episode]`.
func (p *ParsedEpisodeInfo) String() string {
	episodeString := "[Unknown Episode]"

	switch {
	case p.IsDaily() && len(p.EpisodeNumbers) == 0:
		episodeString = p.AirDate
	case p.FullSeason:
		episodeString = fmt.Sprintf("Season %02d", p.SeasonNumber)
	case len(p.EpisodeNumbers) > 0:
		parts := make([]string, len(p.EpisodeNumbers))
		for i, n := range p.EpisodeNumbers {
			parts[i] = fmt.Sprintf("%02d", n)
		}
		episodeString = fmt.Sprintf("S%02dE%s", p.SeasonNumber, strings.Join(parts, "-"))
	case len(p.AbsoluteEpisodeNumbers) > 0:
		parts := make([]string, len(p.AbsoluteEpisodeNumbers))
		for i, n := range p.AbsoluteEpisodeNumbers {
			parts[i] = fmt.Sprintf("%03d", n)
		}
		episodeString = strings.Join(parts, "-")
	case p.Special:
		if p.SeasonNumber != 0 {
			episodeString = fmt.Sprintf("[Unknown Season %02d Special]", p.SeasonNumber)
		} else {
			episodeString = "[Unknown Special]"
		}
	}

	return fmt.Sprintf("%s - %s %s", p.SeriesTitle, episodeString, p.Quality)
}

// NewParsedEpisodeInfo constructs an empty ParsedEpisodeInfo with the
// slice fields initialised to non-nil empty slices. Mirrors the C#
// default-constructor at `ParsedEpisodeInfo.cs:35-41`.
func NewParsedEpisodeInfo() *ParsedEpisodeInfo {
	return &ParsedEpisodeInfo{
		EpisodeNumbers:                []int{},
		AbsoluteEpisodeNumbers:        []int{},
		SpecialAbsoluteEpisodeNumbers: []float64{},
		Languages:                     []language.Language{},
	}
}
