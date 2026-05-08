package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// === AnimeVersionUpgradeSpecification ===

func newAnimeVersionUpgradeSpec(t *testing.T, isUpgrade bool, propers quality.ProperDownloadType, files map[int]*episodefiles.File) *decisionengine.AnimeVersionUpgradeSpecification {
	t.Helper()
	spec, err := decisionengine.NewAnimeVersionUpgradeSpecification(
		stubUpgradable{isUpgrade: isUpgrade},
		stubPropersCfg{kind: propers},
		stubFileResolver{files: files},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewAnimeVersionUpgradeSpecification: %v", err)
	}
	return spec
}

func animeSubject(seriesType int, releaseGroup string, episodeFileIDs []int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeFileID: efp}
	}
	return &decisionengine.Subject{
		Series:   &series.Series{ID: 7, SeriesType: seriesType},
		Episodes: eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseGroup: releaseGroup,
			Quality: quality.Model{
				Quality:  quality.HDTV720p,
				Revision: quality.Revision{Version: 2},
			},
		},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
}

func TestAnimeVersionUpgrade_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewAnimeVersionUpgradeSpecification(nil, stubPropersCfg{}, stubFileResolver{}, nil); err == nil {
		t.Errorf("expected error on nil upgradable")
	}
}

func TestAnimeVersionUpgrade_AcceptsNonAnime(t *testing.T) {
	t.Parallel()
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, nil)
	got := spec.IsSatisfiedBy(t.Context(), animeSubject(series.SeriesTypeStandard, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on non-anime, got %+v", got)
	}
}

func TestAnimeVersionUpgrade_AcceptsWhenDoNotPrefer(t *testing.T) {
	t.Parallel()
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperDoNotPrefer, nil)
	got := spec.IsSatisfiedBy(t.Context(), animeSubject(series.SeriesTypeAnime, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when DoNotPrefer, got %+v", got)
	}
}

func TestAnimeVersionUpgrade_AcceptsMatchingReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "HorribleSubs", Quality: "{}"},
	}
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "HorribleSubs", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on matching release-group, got %+v", got)
	}
}

func TestAnimeVersionUpgrade_RejectsCaseDifference(t *testing.T) {
	t.Parallel()
	// C# uses case-SENSITIVE comparison for anime. HorribleSubs ≠
	// horriblesubs.
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "HorribleSubs", Quality: "{}"},
	}
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "horriblesubs", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on case-different release-group (anime case-sensitive), got Accept")
	}
	if got.Reason != decisionengine.ReasonReleaseGroupDoesNotMatch {
		t.Errorf("Reason = %v, want ReasonReleaseGroupDoesNotMatch", got.Reason)
	}
}

func TestAnimeVersionUpgrade_RejectsMismatchedReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "OtherGroup", Quality: "{}"},
	}
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "HorribleSubs", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on mismatched release-group, got Accept")
	}
	if got.Reason != decisionengine.ReasonReleaseGroupDoesNotMatch {
		t.Errorf("Reason = %v, want ReasonReleaseGroupDoesNotMatch", got.Reason)
	}
}

func TestAnimeVersionUpgrade_RejectsEmptyFileReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "", Quality: "{}"},
	}
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "HorribleSubs", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on empty file release-group, got Accept")
	}
	if got.Reason != decisionengine.ReasonUnknownReleaseGroup {
		t.Errorf("Reason = %v, want ReasonUnknownReleaseGroup", got.Reason)
	}
}

func TestAnimeVersionUpgrade_RejectsEmptyReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "HorribleSubs", Quality: "{}"},
	}
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on empty release release-group, got Accept")
	}
	if got.Reason != decisionengine.ReasonUnknownReleaseGroup {
		t.Errorf("Reason = %v, want ReasonUnknownReleaseGroup", got.Reason)
	}
}

func TestAnimeVersionUpgrade_AcceptsNotRevisionUpgrade(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "OtherGroup", Quality: "{}"},
	}
	spec := newAnimeVersionUpgradeSpec(t, false, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "HorribleSubs", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when not a revision upgrade, got %+v", got)
	}
}

func TestAnimeVersionUpgrade_NotFoundSkipsEpisode(t *testing.T) {
	t.Parallel()
	spec := newAnimeVersionUpgradeSpec(t, true, quality.ProperPreferAndUpgrade, map[int]*episodefiles.File{})
	got := spec.IsSatisfiedBy(t.Context(),
		animeSubject(series.SeriesTypeAnime, "HorribleSubs", []int{99}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on missing EpisodeFile, got %+v", got)
	}
}

func TestAnimeVersionUpgrade_Metadata(t *testing.T) {
	t.Parallel()
	spec := newAnimeVersionUpgradeSpec(t, false, quality.ProperPreferAndUpgrade, nil)
	if spec.Name() != "AnimeVersionUpgrade" {
		t.Errorf("Name = %q, want AnimeVersionUpgrade", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
}
