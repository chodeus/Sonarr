package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
)

// === SameEpisodesGrabSpecification ===

// stubSameEpsFinder maps fileID → []episodes.Episode. err is returned
// for any lookup unless ignored.
type stubSameEpsFinder struct {
	files map[int][]episodes.Episode
	err   error
}

func (s stubSameEpsFinder) GetEpisodesByFileID(_ context.Context, id int) ([]episodes.Episode, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.files[id], nil
}

func newSameEpisodesGrabSpec(t *testing.T, files map[int][]episodes.Episode, lookupErr error) *decisionengine.SameEpisodesGrabSpecification {
	t.Helper()
	spec, err := decisionengine.NewSameEpisodesGrabSpecification(
		stubSameEpsFinder{files: files, err: lookupErr},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewSameEpisodesGrabSpecification: %v", err)
	}
	return spec
}

func sameEpisodesGrabSubject(eps ...episodes.Episode) *decisionengine.Subject {
	return &decisionengine.Subject{Episodes: eps}
}

func TestSameEpisodesGrab_NewRejectsNilFinder(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewSameEpisodesGrabSpecification(nil, nil); err == nil {
		t.Errorf("expected error on nil finder")
	}
}

func TestSameEpisodesGrab_AcceptsEmpty(t *testing.T) {
	t.Parallel()
	spec := newSameEpisodesGrabSpec(t, nil, nil)
	got := spec.IsSatisfiedBy(t.Context(), sameEpisodesGrabSubject(), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on empty episode list, got %+v", got)
	}
}

func TestSameEpisodesGrab_AcceptsNoExistingFile(t *testing.T) {
	t.Parallel()
	spec := newSameEpisodesGrabSpec(t, nil, nil)
	// Episode has no EpisodeFileID — nothing to compare against.
	got := spec.IsSatisfiedBy(t.Context(),
		sameEpisodesGrabSubject(episodes.Episode{ID: 1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when no existing files, got %+v", got)
	}
}

func TestSameEpisodesGrab_AcceptsExactMatch(t *testing.T) {
	t.Parallel()
	// Episode 1 is linked to file 100; file 100 covers ONLY episode 1.
	files := map[int][]episodes.Episode{
		100: {{ID: 1}},
	}
	spec := newSameEpisodesGrabSpec(t, files, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		sameEpisodesGrabSubject(episodes.Episode{ID: 1, EpisodeFileID: ptrInt(100)}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on exact match, got %+v", got)
	}
}

func TestSameEpisodesGrab_AcceptsSupersetCandidate(t *testing.T) {
	t.Parallel()
	// File 100 covers episodes 1 + 2; candidate has episodes 1 + 2 + 3.
	// → no existing-file episode is missing from the candidate set.
	files := map[int][]episodes.Episode{
		100: {{ID: 1}, {ID: 2}},
	}
	spec := newSameEpisodesGrabSpec(t, files, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		sameEpisodesGrabSubject(
			episodes.Episode{ID: 1, EpisodeFileID: ptrInt(100)},
			episodes.Episode{ID: 2, EpisodeFileID: ptrInt(100)},
			episodes.Episode{ID: 3},
		),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on superset candidate, got %+v", got)
	}
}

func TestSameEpisodesGrab_RejectsExistingHasExtraEpisodes(t *testing.T) {
	t.Parallel()
	// File 100 covers episodes 1 + 2; candidate has only episode 1.
	// → grabbing this release would orphan episode 2.
	files := map[int][]episodes.Episode{
		100: {{ID: 1}, {ID: 2}},
	}
	spec := newSameEpisodesGrabSpec(t, files, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		sameEpisodesGrabSubject(episodes.Episode{ID: 1, EpisodeFileID: ptrInt(100)}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when existing file covers more episodes, got Accept")
	}
	if got.Reason != decisionengine.ReasonExistingFileHasMoreEpisodes {
		t.Errorf("Reason = %v, want ReasonExistingFileHasMoreEpisodes", got.Reason)
	}
}

func TestSameEpisodesGrab_LookupErrorAcceptsDefensively(t *testing.T) {
	t.Parallel()
	spec := newSameEpisodesGrabSpec(t, nil, errors.New("db down"))
	got := spec.IsSatisfiedBy(t.Context(),
		sameEpisodesGrabSubject(episodes.Episode{ID: 1, EpisodeFileID: ptrInt(100)}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected defensive Accept on lookup error, got %+v", got)
	}
}

func TestSameEpisodesGrab_NilSubjectAccepts(t *testing.T) {
	t.Parallel()
	spec := newSameEpisodesGrabSpec(t, nil, nil)
	got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Subject, got %+v", got)
	}
}

func TestSameEpisodesGrab_Metadata(t *testing.T) {
	t.Parallel()
	spec := newSameEpisodesGrabSpec(t, nil, nil)
	if spec.Name() != "SameEpisodesGrab" {
		t.Errorf("Name = %q, want SameEpisodesGrab", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
}
