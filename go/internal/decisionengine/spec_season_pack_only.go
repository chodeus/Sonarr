package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SeasonPackOnlySpecificationName is the canonical Spec.Name() identifier.
const SeasonPackOnlySpecificationName = "SeasonPackOnly"

// SearchCriteria is the typed shape every search-related component
// shares. Ports C# `IndexerSearch.SearchCriteriaBase` at
// `src/NzbDrone.Core/IndexerSearch/Definitions/SearchCriteriaBase.cs`
// in full.
//
// History:
//   - Slice 4.E: introduced with three fields (Episodes /
//     UserInvokedSearch / InteractiveSearch) for the per-spec
//     evaluation path.
//   - Slice 4.S: added Series for parsingservice.Service.Map
//     series-resolution short-circuits.
//   - Slice 4.BW (C-cleanup [C14]): consolidated with
//     indexersearch.CriteriaBase — added SceneTitles, SearchMode,
//     MonitoredEpisodesOnly + the AllSceneTitles/CleanSceneTitles
//     helpers (defined in criteria.go). The indexersearch package
//     now re-exports the name `CriteriaBase` as a type alias of this
//     struct, so the 7 typed sub-criteria (SingleEpisodeCriteria,
//     etc.) continue to embed by the indexersearch name without
//     duplicating the field set. The old "decisionengine.SearchCriteria
//     is a partial subset" doc-comment retires here.
//
// Callers attach the typed value to ReleaseDecisionInformation.SearchCriteria
// (which remains `any` at that field for legacy reasons); specs
// type-assert to *SearchCriteria.
type SearchCriteria struct {
	// Series is the series the search was issued against — mirrors C#
	// `SearchCriteriaBase.Series`. Read by parsingservice.findSeries to
	// short-circuit ID + CleanTitle matches without a series lookup. May
	// be nil in test setups that don't pin a series; production callers
	// always populate it.
	Series *series.Series
	// SceneTitles is the set of indexer-search query titles (deduped
	// scene names + the canonical series title). Mirrors C#
	// `SearchCriteriaBase.SceneTitles`. Slice 4.BW added — the
	// indexersearch.CriteriaBase carried this field pre-merge.
	SceneTitles []string
	// Episodes is the list of episodes the search asked for. May span
	// multiple seasons.
	Episodes []episodes.Episode
	// SearchMode is the per-criteria flag bitfield controlling which
	// of (SearchID, SearchTitle) the indexer's RequestGenerator
	// should consider. Mirrors C# `SearchCriteriaBase.SearchMode`.
	// Slice 4.BW added; the SearchMode type itself lives in
	// criteria.go.
	SearchMode SearchMode
	// MonitoredEpisodesOnly is true when unmonitored episodes should
	// be excluded from the search target list. Mirrors C#
	// `SearchCriteriaBase.MonitoredEpisodesOnly`. Slice 4.BW added.
	MonitoredEpisodesOnly bool
	// UserInvokedSearch is true when the user pressed "Search all
	// monitored episodes" / "Search this season" — the orchestrator
	// uses it to derive `ReleaseSourceUserInvokedSearch`. Mirrors C#
	// `SearchCriteriaBase.UserInvokedSearch`.
	UserInvokedSearch bool
	// InteractiveSearch is true when the user opened the per-episode
	// release list manually — the orchestrator routes this to
	// `ReleaseSourceInteractiveSearch`. Mirrors C#
	// `SearchCriteriaBase.InteractiveSearch`.
	InteractiveSearch bool

	// Kind discriminates which C# SearchCriteriaBase subtype this
	// criteria represents. The C# tree models each search shape as a
	// distinct subclass (SingleEpisodeSearchCriteria, SeasonSearchCriteria,
	// AnimeEpisodeSearchCriteria, …); the Go port unifies them into this
	// one struct, so Kind + the SeasonNumber/EpisodeNumber/
	// AnimeIsSeasonSearch fields below carry the per-subtype data the
	// search-match specs (SeasonMatch / SingleEpisodeSearchMatch) need to
	// discriminate. Populated by the typed `indexersearch.*Criteria.Base()`
	// methods; defaults to [SearchKindOther] (those specs Accept).
	Kind SearchKind
	// SeasonNumber is the searched season — meaningful only when Kind is
	// SearchKindSeason / SearchKindSingleEpisode / SearchKindAnimeEpisode.
	SeasonNumber int
	// EpisodeNumber is the searched episode — meaningful only when Kind
	// is SearchKindSingleEpisode / SearchKindAnimeEpisode.
	EpisodeNumber int
	// AnimeIsSeasonSearch mirrors C# `AnimeEpisodeSearchCriteria.IsSeasonSearch`
	// — meaningful only when Kind is SearchKindAnimeEpisode.
	AnimeIsSeasonSearch bool
}

// SearchKind discriminates the C# SearchCriteriaBase subtype carried in
// a unified [SearchCriteria]. Only the values the search-match specs
// branch on are modelled; every other C# subtype (daily, anime-season,
// special, daily-season) maps to [SearchKindOther], for which both
// search-match specs Accept.
type SearchKind int

const (
	// SearchKindOther is the default — not a subtype the search-match
	// specs act on (RSS, daily, anime-season, special, …).
	SearchKindOther SearchKind = iota
	// SearchKindSingleEpisode is the C# SingleEpisodeSearchCriteria.
	SearchKindSingleEpisode
	// SearchKindSeason is the C# SeasonSearchCriteria (standard full
	// season — NOT AnimeSeasonSearchCriteria).
	SearchKindSeason
	// SearchKindAnimeEpisode is the C# AnimeEpisodeSearchCriteria.
	SearchKindAnimeEpisode
)

// SeasonPackOnlySpecification rejects single-episode releases for a
// season whose latest episode aired more than
// `SeasonSearchMaximumSingleEpisodeAge` days ago — at that point the
// indexer is signalling "stop looking for individual episodes; only
// season packs are reasonable".
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/SeasonPackOnlySpecification.cs:1-50`.
//
// Permanent rejection — once a season ages past the threshold, single
// episodes from that season won't become acceptable on retry.
//
// The spec is a no-op when:
//
//   - SearchCriteria is nil (RSS sweep, manual push, no search context).
//   - The search has only one Episode in scope (single-episode search,
//     the user is explicitly asking for one).
//   - SeasonSearchMaximumSingleEpisodeAge is 0 or negative (indexer
//     opt-out).
//   - Series.SeriesType is not Standard (Daily / Anime have their own
//     pack semantics).
//   - The release is itself a FullSeason pack.
//   - Subject has no episodes (defensive).
type SeasonPackOnlySpecification struct {
	now func() time.Time
	log *slog.Logger
}

// NewSeasonPackOnlySpecification builds the spec. log defaults to
// slog.Default when nil. The clock parameter is optional — pass nil
// to default to time.Now (production); tests pass a fixed clock.
func NewSeasonPackOnlySpecification(now func() time.Time, log *slog.Logger) *SeasonPackOnlySpecification {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &SeasonPackOnlySpecification{now: now, log: log}
}

// Name returns the canonical spec name.
func (*SeasonPackOnlySpecification) Name() string { return SeasonPackOnlySpecificationName }

// Kind reports Permanent.
func (*SeasonPackOnlySpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — pure in-memory predicate.
func (*SeasonPackOnlySpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:22-47`. See the type
// doc-comment for the no-op cases.
func (s *SeasonPackOnlySpecification) IsSatisfiedBy(_ context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil || subject == nil {
		return Accept()
	}

	criteria, ok := info.SearchCriteria.(*SearchCriteria)
	if !ok || criteria == nil {
		return Accept()
	}
	if len(criteria.Episodes) <= 1 {
		return Accept()
	}

	maxAgeDays := rel.SeasonSearchMaximumSingleEpisodeAge
	if maxAgeDays <= 0 {
		return Accept()
	}

	if subject.Series == nil || subject.Series.SeriesType != series.SeriesTypeStandard {
		return Accept()
	}
	if subject.ParsedEpisodeInfo == nil || subject.ParsedEpisodeInfo.FullSeason {
		return Accept()
	}
	if len(subject.Episodes) == 0 {
		return Accept()
	}

	candidateSeason := subject.Episodes[0].SeasonNumber
	cutoff := s.now().UTC().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)

	var maxAir time.Time
	any := false
	for _, e := range criteria.Episodes {
		if e.AirDateUtc == nil {
			continue
		}
		if e.SeasonNumber != candidateSeason {
			continue
		}
		air := e.AirDateUtc.UTC()
		if !any || air.After(maxAir) {
			maxAir = air
			any = true
		}
	}
	if !any {
		return Accept()
	}

	if maxAir.Before(cutoff) {
		msg := fmt.Sprintf("Last episode in this season aired more than %d days ago, season pack required.", maxAgeDays)
		s.log.Debug("seasonPackOnly: rejecting too-old single-episode release",
			"title", rel.Title, "season", candidateSeason, "maxAir", maxAir, "cutoff", cutoff)
		return Reject(ReasonNotSeasonPack, msg)
	}
	return Accept()
}
