// Package indexersearch holds the typed SearchCriteria sub-types
// every indexer's RequestGenerator consumes plus the
// search-orchestration body. C# cite:
// `src/NzbDrone.Core/IndexerSearch/Definitions/*.cs`.
//
// Slice 4.BW (C-cleanup [C14]) consolidated the shared
// SearchCriteriaBase fields + the SearchMode enum + the
// GetCleanSceneTitle helper into the decisionengine package so they
// could be carried on `*decisionengine.SearchCriteria` without an
// import cycle. This file re-exports the names callers spell
// `indexersearch.{CriteriaBase, SearchMode, SearchModeRSS, ...,
// GetCleanSceneTitle}` via type aliases + constants. The 7 typed
// sub-criteria (SingleEpisodeCriteria, etc.) remain here and embed
// CriteriaBase — embedding an aliased type works identically to
// embedding the original, so the SceneTitles + SearchMode +
// MonitoredEpisodesOnly + UserInvokedSearch + InteractiveSearch
// field reads continue to compile without changes.
package indexersearch

import (
	"fmt"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/series"
)

// CriteriaBase is the shared search-criteria shape every typed
// sub-criteria embeds. Slice 4.BW consolidated the type into the
// decisionengine package — this alias preserves the
// `indexersearch.CriteriaBase` spelling at every existing call site
// without duplicating the field set. The underlying type carries:
// Series, SceneTitles, Episodes, SearchMode, MonitoredEpisodesOnly,
// UserInvokedSearch, InteractiveSearch. See
// [decisionengine.SearchCriteria] for the canonical doc-comment.
type CriteriaBase = decisionengine.SearchCriteria

// SearchMode mirrors `src/NzbDrone.Core/Indexers/SearchMode.cs`. The
// type + constants moved to decisionengine in slice 4.BW; this alias
// keeps the `indexersearch.SearchMode*` spelling working.
type SearchMode = decisionengine.SearchMode

// SearchMode constants re-exported from decisionengine. Slice 4.BW.
const (
	SearchModeDefault     = decisionengine.SearchModeDefault
	SearchModeRSS         = decisionengine.SearchModeRSS
	SearchModeSearchID    = decisionengine.SearchModeSearchID
	SearchModeSearchTitle = decisionengine.SearchModeSearchTitle
)

// GetCleanSceneTitle is the re-export of
// [decisionengine.GetCleanSceneTitle] for callers that spell the
// helper `indexersearch.GetCleanSceneTitle`. Slice 4.BW relocation.
func GetCleanSceneTitle(title string) string {
	return decisionengine.GetCleanSceneTitle(title)
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
