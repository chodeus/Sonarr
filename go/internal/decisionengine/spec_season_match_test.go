package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func makeSeasonMatchSpec() *decisionengine.SeasonMatchSpecification {
	return decisionengine.NewSeasonMatchSpecification(slog.New(slog.DiscardHandler))
}

func seasonMatchSubject(parsedSeason int) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series:            &series.Series{ID: 1},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{SeasonNumber: parsedSeason},
	}
}

func seasonSearchInfo(kind decisionengine.SearchKind, season int) decisionengine.ReleaseDecisionInformation {
	return decisionengine.ReleaseDecisionInformation{
		SearchCriteria: &decisionengine.SearchCriteria{Kind: kind, SeasonNumber: season},
	}
}

func TestSeasonMatch_NoSearchCriteria_Accept(t *testing.T) {
	t.Parallel()
	got := makeSeasonMatchSpec().IsSatisfiedBy(t.Context(), seasonMatchSubject(2),
		decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Error("RSS (nil SearchCriteria) must Accept")
	}
}

func TestSeasonMatch_NotSeasonSearch_Accept(t *testing.T) {
	t.Parallel()
	// A single-episode search must NOT be acted on by SeasonMatch (it's
	// SeasonSearchCriteria-only), even with a season mismatch.
	info := seasonSearchInfo(decisionengine.SearchKindSingleEpisode, 9)
	got := makeSeasonMatchSpec().IsSatisfiedBy(t.Context(), seasonMatchSubject(2), info)
	if !got.Accepted {
		t.Error("non-season-search criteria must Accept")
	}
}

func TestSeasonMatch_AnimeSeason_NotActedOn_Accept(t *testing.T) {
	t.Parallel()
	// AnimeSeasonSearchCriteria maps to SearchKindOther — SeasonMatch
	// must NOT reject it even on a season mismatch (faithful to the C#
	// `as SeasonSearchCriteria` which excludes the anime-season subtype).
	info := seasonSearchInfo(decisionengine.SearchKindOther, 9)
	got := makeSeasonMatchSpec().IsSatisfiedBy(t.Context(), seasonMatchSubject(2), info)
	if !got.Accepted {
		t.Error("anime-season (SearchKindOther) must Accept")
	}
}

func TestSeasonMatch_WrongSeason_Reject(t *testing.T) {
	t.Parallel()
	info := seasonSearchInfo(decisionengine.SearchKindSeason, 9)
	got := makeSeasonMatchSpec().IsSatisfiedBy(t.Context(), seasonMatchSubject(2), info)
	if got.Accepted || got.Reason != decisionengine.ReasonWrongSeason {
		t.Fatalf("wrong season should reject WrongSeason, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	if got.Message != "Wrong season" {
		t.Errorf("message = %q, want 'Wrong season'", got.Message)
	}
}

func TestSeasonMatch_RightSeason_Accept(t *testing.T) {
	t.Parallel()
	info := seasonSearchInfo(decisionengine.SearchKindSeason, 2)
	got := makeSeasonMatchSpec().IsSatisfiedBy(t.Context(), seasonMatchSubject(2), info)
	if !got.Accepted {
		t.Errorf("matching season should Accept, got: %s", got.Message)
	}
}

func TestSeasonMatch_Metadata(t *testing.T) {
	t.Parallel()
	s := makeSeasonMatchSpec()
	if s.Name() != decisionengine.SeasonMatchSpecificationName || s.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("metadata: name=%q kind=%v", s.Name(), s.Kind())
	}
}
