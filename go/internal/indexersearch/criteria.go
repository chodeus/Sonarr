// Package indexersearch holds the typed SearchCriteria contracts that
// every indexer's RequestGenerator consumes. C# cite:
// src/NzbDrone.Core/IndexerSearch/Definitions/*.cs.
//
// The package intentionally has no fan-out / orchestration logic — that
// will land with queue [7] (indexersearch.Service body). Right now it's
// just the seven typed criteria + the shared base + the GetCleanSceneTitle
// helper that the criteria carry as a derived property.
//
// Note: there's a partial decisionengine.SearchCriteria type from Phase 3
// (used by SeasonPackOnlySpecification). That type is a deliberate subset
// — episodes + invocation flags only — and its docstring flags it as
// transitional. The full criteria types live HERE; the spec will migrate
// to consume them as a follow-up.
package indexersearch

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SearchMode is a bit-flag enum mirroring src/NzbDrone.Core/Indexers/SearchMode.cs.
// SearchID and SearchTitle are independent flags; Default selects both
// behaviours via fallthrough in the request generators.
type SearchMode int

// SearchMode flag values.
const (
	SearchModeDefault     SearchMode = 0
	SearchModeRSS         SearchMode = 1 << 0 // 1
	SearchModeSearchID    SearchMode = 1 << 1 // 2
	SearchModeSearchTitle SearchMode = 1 << 2 // 4
)

// HasFlag reports whether m contains f. C# `SearchMode.HasFlag(other)`
// equivalent for enum bit-test semantics.
func (m SearchMode) HasFlag(f SearchMode) bool { return m&f == f }

// CriteriaBase is the shared fields every typed search criteria carries.
// C# cite: src/NzbDrone.Core/IndexerSearch/Definitions/SearchCriteriaBase.cs:11-43.
//
// Designed for embedding (Go composition replaces C# inheritance):
//
//	type SingleEpisodeCriteria struct {
//	    CriteriaBase
//	    EpisodeNumber int
//	    SeasonNumber  int
//	}
type CriteriaBase struct {
	Series                *series.Series
	SceneTitles           []string
	Episodes              []episodes.Episode
	SearchMode            SearchMode
	MonitoredEpisodesOnly bool
	UserInvokedSearch     bool
	InteractiveSearch     bool
}

// AllSceneTitles returns the union of SceneTitles + CleanSceneTitles, deduped,
// in iteration order. Mirrors the C# property at SearchCriteriaBase.cs:25.
func (c CriteriaBase) AllSceneTitles() []string {
	seen := make(map[string]struct{}, len(c.SceneTitles)*2)
	out := make([]string, 0, len(c.SceneTitles)*2)
	for _, t := range c.SceneTitles {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	for _, t := range c.CleanSceneTitles() {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// CleanSceneTitles returns the SceneTitles each cleaned via GetCleanSceneTitle,
// deduped. Mirrors SearchCriteriaBase.cs:26.
func (c CriteriaBase) CleanSceneTitles() []string {
	seen := make(map[string]struct{}, len(c.SceneTitles))
	out := make([]string, 0, len(c.SceneTitles))
	for _, t := range c.SceneTitles {
		clean := GetCleanSceneTitle(t)
		if _, ok := seen[clean]; !ok {
			seen[clean] = struct{}{}
			out = append(out, clean)
		}
	}
	return out
}

var (
	specialCharRegex = regexp.MustCompile(`['` + "`" + `´‘’.]`)
	// Use unicode-aware classes — Go's `\w` is ASCII-only by default
	// while .NET's is unicode-aware. Without this, `é` in "Café" gets
	// replaced with `+` before RemoveDiacritics has a chance to convert
	// it to `e`. Behaviour now matches the C# regex semantics.
	nonWordRegex      = regexp.MustCompile(`[^\p{L}\p{N}_]`)
	beginningTheRegex = regexp.MustCompile(`(?i)^the\s`)
	repeatedPlusRegex = regexp.MustCompile(`\+{2,}`)
)

// GetCleanSceneTitle ports SearchCriteriaBase.cs:30-43.
//
// Algorithm:
//  1. Strip leading "the " (case-insensitive).
//  2. Replace "&" with "and".
//  3. Strip apostrophes / backticks / unicode quotes / dots.
//  4. Replace any remaining non-word character with "+".
//  5. Collapse runs of "+" into a single "+".
//  6. Strip diacritics.
//  7. Trim leading/trailing "+" or whitespace.
func GetCleanSceneTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}
	cleanTitle := beginningTheRegex.ReplaceAllString(title, "")
	cleanTitle = strings.ReplaceAll(cleanTitle, "&", "and")
	cleanTitle = specialCharRegex.ReplaceAllString(cleanTitle, "")
	cleanTitle = nonWordRegex.ReplaceAllString(cleanTitle, "+")
	cleanTitle = repeatedPlusRegex.ReplaceAllString(cleanTitle, "+")
	cleanTitle = removeDiacritics(cleanTitle)
	return strings.Trim(cleanTitle, "+ ")
}

// removeDiacritics ports the C# RemoveDiacritics extension at
// NzbDrone.Common.Extensions.StringExtensions. NFD-decomposes first so
// pre-composed Latin characters (e.g. é = U+00E9) split into base letter
// + combining mark, then drops the combining marks. Without the NFD
// step, `é` would survive as a single non-Mn rune and never get stripped.
func removeDiacritics(s string) string {
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// --- Typed criteria, in the order they appear in the C# Definitions/ folder. ---

// SingleEpisodeCriteria — `SeriesType=Standard, single episode`.
// C# cite: SingleEpisodeSearchCriteria.cs:1-13.
type SingleEpisodeCriteria struct {
	CriteriaBase
	EpisodeNumber int
	SeasonNumber  int
}

// String mirrors the C# ToString format: `[<series> : S<ss>E<ee>]`.
func (c SingleEpisodeCriteria) String() string {
	title := ""
	if c.Series != nil {
		title = c.Series.Title
	}
	return fmt.Sprintf("[%s : S%02dE%02d]", title, c.SeasonNumber, c.EpisodeNumber)
}

// SeasonCriteria — `SeriesType=Standard, full season`. C# cite:
// SeasonSearchCriteria.cs:1-12.
type SeasonCriteria struct {
	CriteriaBase
	SeasonNumber int
}

// String mirrors `[<series> : S<ss>]`.
func (c SeasonCriteria) String() string {
	title := ""
	if c.Series != nil {
		title = c.Series.Title
	}
	return fmt.Sprintf("[%s : S%02d]", title, c.SeasonNumber)
}

// DailyEpisodeCriteria — daily series, single airdate. C# cite:
// DailyEpisodeSearchCriteria.cs:1-14.
type DailyEpisodeCriteria struct {
	CriteriaBase
	AirDate time.Time
}

// String mirrors `[<series> : <yyyy-MM-dd>]`.
func (c DailyEpisodeCriteria) String() string {
	title := ""
	if c.Series != nil {
		title = c.Series.Title
	}
	return fmt.Sprintf("[%s : %s]", title, c.AirDate.Format("2006-01-02"))
}

// DailySeasonCriteria — daily series, year-as-season. C# cite:
// DailySeasonSearchCriteria.cs:1-12.
type DailySeasonCriteria struct {
	CriteriaBase
	Year int
}

// String mirrors `[<series> : <yyyy>]`.
func (c DailySeasonCriteria) String() string {
	title := ""
	if c.Series != nil {
		title = c.Series.Title
	}
	return fmt.Sprintf("[%s : %d]", title, c.Year)
}

// AnimeEpisodeCriteria — anime series, single absolute episode. C# cite:
// AnimeEpisodeSearchCriteria.cs:1-15.
type AnimeEpisodeCriteria struct {
	CriteriaBase
	AbsoluteEpisodeNumber int
	EpisodeNumber         int
	SeasonNumber          int
	IsSeasonSearch        bool
}

// String mirrors `[<series> : S<ss>E<ee> (<aaa>)]`.
func (c AnimeEpisodeCriteria) String() string {
	title := ""
	if c.Series != nil {
		title = c.Series.Title
	}
	return fmt.Sprintf("[%s : S%02dE%02d (%02d)]", title, c.SeasonNumber, c.EpisodeNumber, c.AbsoluteEpisodeNumber)
}

// AnimeSeasonCriteria — anime series, full season. C# cite:
// AnimeSeasonSearchCriteria.cs:1-12.
type AnimeSeasonCriteria struct {
	CriteriaBase
	SeasonNumber int
}

// String mirrors `[<series> : S<ss>]`.
func (c AnimeSeasonCriteria) String() string {
	title := ""
	if c.Series != nil {
		title = c.Series.Title
	}
	return fmt.Sprintf("[%s : S%02d]", title, c.SeasonNumber)
}

// SpecialEpisodeCriteria — specials. C# cite: SpecialEpisodeSearchCriteria.cs:1-21.
type SpecialEpisodeCriteria struct {
	CriteriaBase
	EpisodeQueryTitles []string
}

// String mirrors the C# format. Note: the C# code has a logic bug — when
// EpisodeQueryTitles is non-empty the format is the "Specials" label, and
// when empty it joins the (empty) list. We mirror it verbatim.
func (c SpecialEpisodeCriteria) String() string {
	title := ""
	seriesType := ""
	if c.Series != nil {
		title = c.Series.Title
		seriesType = seriesTypeName(c.Series.SeriesType)
	}
	if len(c.EpisodeQueryTitles) > 0 {
		return fmt.Sprintf("[%s (%s)] Specials", title, seriesType)
	}
	return fmt.Sprintf("[%s (%s): %s]", title, seriesType, strings.Join(c.EpisodeQueryTitles, ","))
}

// seriesTypeName mirrors C# Tv.SeriesTypes.ToString — int → name. The Go
// enum is `int` (matching the persisted column shape); this helper gives
// the printable name for the C# ToString contract.
func seriesTypeName(t int) string {
	switch t {
	case series.SeriesTypeStandard:
		return "Standard"
	case series.SeriesTypeDaily:
		return "Daily"
	case series.SeriesTypeAnime:
		return "Anime"
	default:
		return fmt.Sprintf("SeriesType(%d)", t)
	}
}
