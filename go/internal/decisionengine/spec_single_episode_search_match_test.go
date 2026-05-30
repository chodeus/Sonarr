package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func makeSingleEpSpec() *decisionengine.SingleEpisodeSearchMatchSpecification {
	return decisionengine.NewSingleEpisodeSearchMatchSpecification(slog.New(slog.DiscardHandler))
}

func singleEpSubject(season int, epNums []int, fullSeason bool) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{ID: 1},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			SeasonNumber:   season,
			EpisodeNumbers: epNums,
			FullSeason:     fullSeason,
		},
	}
}

func singleEpInfo(kind decisionengine.SearchKind, season, episode int, animeIsSeasonSearch bool) decisionengine.ReleaseDecisionInformation {
	return decisionengine.ReleaseDecisionInformation{
		SearchCriteria: &decisionengine.SearchCriteria{
			Kind:                kind,
			SeasonNumber:        season,
			EpisodeNumber:       episode,
			AnimeIsSeasonSearch: animeIsSeasonSearch,
		},
	}
}

func TestSingleEpMatch_NoSearchCriteria_Accept(t *testing.T) {
	t.Parallel()
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(1, []int{1}, false),
		decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Error("RSS (nil SearchCriteria) must Accept")
	}
}

func TestSingleEpMatch_OtherKind_Accept(t *testing.T) {
	t.Parallel()
	// A season search (SearchKindSeason) is not this spec's concern.
	info := singleEpInfo(decisionengine.SearchKindSeason, 9, 0, false)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(2, []int{1}, false), info)
	if !got.Accepted {
		t.Error("non-single/anime-episode criteria must Accept")
	}
}

func TestSingleEpMatch_WrongSeason_Reject(t *testing.T) {
	t.Parallel()
	info := singleEpInfo(decisionengine.SearchKindSingleEpisode, 9, 5, false)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(2, []int{5}, false), info)
	if got.Accepted || got.Reason != decisionengine.ReasonWrongSeason || got.Message != "Wrong season" {
		t.Fatalf("wrong season: accepted=%v reason=%v msg=%q", got.Accepted, got.Reason, got.Message)
	}
}

func TestSingleEpMatch_FullSeasonPack_Reject(t *testing.T) {
	t.Parallel()
	// Right season, but the release has no episode numbers (a full-season
	// pack) during a single-episode search → reject FullSeason.
	info := singleEpInfo(decisionengine.SearchKindSingleEpisode, 2, 5, false)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(2, nil, true), info)
	if got.Accepted || got.Reason != decisionengine.ReasonFullSeason || got.Message != "Full season pack" {
		t.Fatalf("full season pack: accepted=%v reason=%v msg=%q", got.Accepted, got.Reason, got.Message)
	}
}

func TestSingleEpMatch_WrongEpisode_Reject(t *testing.T) {
	t.Parallel()
	// Right season, has episodes, but not the searched one → WrongEpisode.
	info := singleEpInfo(decisionengine.SearchKindSingleEpisode, 2, 5, false)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(2, []int{7, 8}, false), info)
	if got.Accepted || got.Reason != decisionengine.ReasonWrongEpisode || got.Message != "Wrong episode" {
		t.Fatalf("wrong episode: accepted=%v reason=%v msg=%q", got.Accepted, got.Reason, got.Message)
	}
}

func TestSingleEpMatch_RightEpisode_Accept(t *testing.T) {
	t.Parallel()
	info := singleEpInfo(decisionengine.SearchKindSingleEpisode, 2, 5, false)
	// A multi-episode pack that includes the searched episode passes.
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(2, []int{4, 5, 6}, false), info)
	if !got.Accepted {
		t.Errorf("release containing the searched episode should Accept, got: %s", got.Message)
	}
}

func TestSingleEpMatch_Anime_FullSeasonNotSeasonSearch_Reject(t *testing.T) {
	t.Parallel()
	// Anime episode search; release is a full season; the search was not
	// a season search → reject FullSeason.
	info := singleEpInfo(decisionengine.SearchKindAnimeEpisode, 1, 12, false)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(1, nil, true), info)
	if got.Accepted || got.Reason != decisionengine.ReasonFullSeason {
		t.Fatalf("anime full season: accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestSingleEpMatch_Anime_FullSeasonDuringSeasonSearch_Accept(t *testing.T) {
	t.Parallel()
	// Same full-season release, but the anime search IS a season search →
	// Accept (the user wanted the whole season).
	info := singleEpInfo(decisionengine.SearchKindAnimeEpisode, 1, 12, true)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(1, nil, true), info)
	if !got.Accepted {
		t.Errorf("anime full-season during a season search should Accept, got: %s", got.Message)
	}
}

func TestSingleEpMatch_Anime_NotFullSeason_Accept(t *testing.T) {
	t.Parallel()
	info := singleEpInfo(decisionengine.SearchKindAnimeEpisode, 1, 12, false)
	got := makeSingleEpSpec().IsSatisfiedBy(t.Context(), singleEpSubject(1, []int{12}, false), info)
	if !got.Accepted {
		t.Errorf("anime single-episode result should Accept, got: %s", got.Message)
	}
}

func TestSingleEpMatch_Metadata(t *testing.T) {
	t.Parallel()
	s := makeSingleEpSpec()
	if s.Name() != decisionengine.SingleEpisodeSearchMatchSpecificationName || s.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("metadata: name=%q kind=%v", s.Name(), s.Kind())
	}
}
