package decisionengine_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// properNow is a fixed clock so the 7-day cutoff is deterministic.
var properNow = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

func newProperSpec(t *testing.T, isUpgrade bool, propers quality.ProperDownloadType, files map[int]*episodefiles.File) *decisionengine.ProperSpecification {
	t.Helper()
	spec, err := decisionengine.NewProperSpecification(
		stubUpgradable{isUpgrade: isUpgrade},
		stubPropersCfg{kind: propers},
		stubFileResolver{files: files},
		func() time.Time { return properNow },
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewProperSpecification: %v", err)
	}
	return spec
}

func properSubject(episodeFileIDs ...int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeFileID: efp}
	}
	return &decisionengine.Subject{
		Episodes: eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: quality.HDTV720p, Revision: quality.Revision{Version: 2}},
		},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
}

func fileAddedDaysAgo(id, days int) *episodefiles.File {
	return &episodefiles.File{ID: id, DateAdded: properNow.AddDate(0, 0, -days)}
}

func TestProper_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewProperSpecification(nil, stubPropersCfg{}, stubFileResolver{}, nil, nil); err == nil {
		t.Error("expected error on nil upgradable")
	}
	if _, err := decisionengine.NewProperSpecification(stubUpgradable{}, nil, stubFileResolver{}, nil, nil); err == nil {
		t.Error("expected error on nil cfg")
	}
	if _, err := decisionengine.NewProperSpecification(stubUpgradable{}, stubPropersCfg{}, nil, nil, nil); err == nil {
		t.Error("expected error on nil files")
	}
}

func TestProper_SearchCriteria_Accept(t *testing.T) {
	t.Parallel()
	// Would otherwise reject (DoNotUpgrade + revision upgrade), but a
	// search bypasses the gate.
	files := map[int]*episodefiles.File{10: fileAddedDaysAgo(10, 1)}
	spec := newProperSpec(t, true, quality.ProperDoNotUpgrade, files)
	info := decisionengine.ReleaseDecisionInformation{SearchCriteria: &decisionengine.SearchCriteria{}}
	if !spec.IsSatisfiedBy(t.Context(), properSubject(10), info).Accepted {
		t.Error("search should bypass the proper gate")
	}
}

func TestProper_DoNotPrefer_Accept(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: fileAddedDaysAgo(10, 100)}
	spec := newProperSpec(t, true, quality.ProperDoNotPrefer, files)
	if !spec.IsSatisfiedBy(t.Context(), properSubject(10), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("DoNotPrefer should Accept")
	}
}

func TestProper_NotRevisionUpgrade_Accept(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: fileAddedDaysAgo(10, 100)}
	spec := newProperSpec(t, false /* not an upgrade */, quality.ProperPreferAndUpgrade, files)
	if !spec.IsSatisfiedBy(t.Context(), properSubject(10), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("non-revision-upgrade should Accept")
	}
}

func TestProper_DoNotUpgrade_Reject(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: fileAddedDaysAgo(10, 1)}
	spec := newProperSpec(t, true, quality.ProperDoNotUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), properSubject(10), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonPropersDisabled {
		t.Fatalf("DoNotUpgrade+upgrade should reject PropersDisabled, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestProper_RecentFile_Accept(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: fileAddedDaysAgo(10, 3)} // within 7 days
	spec := newProperSpec(t, true, quality.ProperPreferAndUpgrade, files)
	if !spec.IsSatisfiedBy(t.Context(), properSubject(10), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("recent file proper should Accept")
	}
}

func TestProper_OldFile_Reject(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: fileAddedDaysAgo(10, 10)} // older than 7 days
	spec := newProperSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), properSubject(10), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonProperForOldFile {
		t.Fatalf("old-file proper should reject ProperForOldFile, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestProper_NoEpisodeFiles_Accept(t *testing.T) {
	t.Parallel()
	spec := newProperSpec(t, true, quality.ProperPreferAndUpgrade, nil)
	if !spec.IsSatisfiedBy(t.Context(), properSubject(0), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("no existing files → Accept")
	}
}
