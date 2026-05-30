package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

func makeEpisodeRequestedSpec() *decisionengine.EpisodeRequestedSpecification {
	return decisionengine.NewEpisodeRequestedSpecification(slog.New(slog.DiscardHandler))
}

func episodeRequestedSubject(eps ...episodes.Episode) *decisionengine.Subject {
	return &decisionengine.Subject{Series: &series.Series{ID: 1}, Episodes: eps}
}

func searchInfoWithEpisodes(eps ...episodes.Episode) decisionengine.ReleaseDecisionInformation {
	return decisionengine.ReleaseDecisionInformation{
		SearchCriteria: &decisionengine.SearchCriteria{Episodes: eps},
	}
}

func TestEpisodeRequestedSpec_NoSearchCriteria_Accept(t *testing.T) {
	t.Parallel()
	got := makeEpisodeRequestedSpec().IsSatisfiedBy(t.Context(),
		episodeRequestedSubject(episodes.Episode{ID: 5}),
		decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Error("RSS (nil SearchCriteria) must Accept")
	}
}

func TestEpisodeRequestedSpec_Intersects_Accept(t *testing.T) {
	t.Parallel()
	info := searchInfoWithEpisodes(episodes.Episode{ID: 5}, episodes.Episode{ID: 6})
	got := makeEpisodeRequestedSpec().IsSatisfiedBy(t.Context(),
		episodeRequestedSubject(episodes.Episode{ID: 6, SeasonNumber: 1, EpisodeNumber: 2}), info)
	if !got.Accepted {
		t.Errorf("release sharing a requested episode should Accept, got: %s", got.Message)
	}
}

func TestEpisodeRequestedSpec_NoIntersection_SingleEpisode_Reject(t *testing.T) {
	t.Parallel()
	info := searchInfoWithEpisodes(episodes.Episode{ID: 5})
	got := makeEpisodeRequestedSpec().IsSatisfiedBy(t.Context(),
		episodeRequestedSubject(episodes.Episode{ID: 99, SeasonNumber: 3, EpisodeNumber: 7}), info)
	if got.Accepted || got.Reason != decisionengine.ReasonWrongEpisode {
		t.Fatalf("non-requested episode should reject WrongEpisode, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	if got.Message != "Episode wasn't requested: 3x7" {
		t.Errorf("message = %q, want 'Episode wasn't requested: 3x7'", got.Message)
	}
}

func TestEpisodeRequestedSpec_NoIntersection_MultiEpisode_RangeMessage(t *testing.T) {
	t.Parallel()
	info := searchInfoWithEpisodes(episodes.Episode{ID: 5})
	got := makeEpisodeRequestedSpec().IsSatisfiedBy(t.Context(),
		episodeRequestedSubject(
			episodes.Episode{ID: 99, SeasonNumber: 2, EpisodeNumber: 8},
			episodes.Episode{ID: 98, SeasonNumber: 2, EpisodeNumber: 5},
		), info)
	if got.Accepted {
		t.Fatal("non-requested multi-episode should reject")
	}
	if got.Message != "Episode wasn't requested: 2x5-8" {
		t.Errorf("message = %q, want 'Episode wasn't requested: 2x5-8' (sorted span)", got.Message)
	}
}

func TestEpisodeRequestedSpec_NoMatchedEpisodes_BareMessage(t *testing.T) {
	t.Parallel()
	info := searchInfoWithEpisodes(episodes.Episode{ID: 5})
	got := makeEpisodeRequestedSpec().IsSatisfiedBy(t.Context(), episodeRequestedSubject(), info)
	if got.Accepted || got.Message != "Episode wasn't requested" {
		t.Errorf("no-episodes release: accepted=%v message=%q", got.Accepted, got.Message)
	}
}

func TestEpisodeRequestedSpec_Metadata(t *testing.T) {
	t.Parallel()
	s := makeEpisodeRequestedSpec()
	if s.Name() != decisionengine.EpisodeRequestedSpecificationName || s.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("metadata: name=%q kind=%v", s.Name(), s.Kind())
	}
}
