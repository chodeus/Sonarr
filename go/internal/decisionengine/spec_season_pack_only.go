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

// SearchCriteria is the typed shape that future specs (FullSeason,
// MultiSeason, WrongEpisode, etc.) will share. Carries the episodes
// the search was issued against so per-release decisions can verify
// the candidate matches the user's intent.
//
// This is a partial port of the C# `IndexerSearch.SearchCriteriaBase`
// — only the field SeasonPackOnlySpecification needs lives here. The
// full type (SeriesId, Series, MonitoredEpisodesOnly, etc.) lands in
// its own slice once IndexerSearch is on the Phase 4 critical path.
//
// Callers attach the typed value to ReleaseDecisionInformation.SearchCriteria
// (which remains `any` for now); specs type-assert to `*SearchCriteria`.
type SearchCriteria struct {
	// Episodes is the list of episodes the search asked for. May span
	// multiple seasons.
	Episodes []episodes.Episode
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
}

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
