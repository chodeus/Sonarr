package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// === RepackSpecification & AnimeVersionUpgradeSpecification ===

// stubFileResolver maps fileID → *episodefiles.File. err is returned
// for any lookup unless missID is set, in which case missID returns
// ErrNotFound.
type stubFileResolver struct {
	files map[int]*episodefiles.File
	err   error
}

func (s stubFileResolver) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if s.err != nil {
		return nil, s.err
	}
	f, ok := s.files[id]
	if !ok {
		return nil, episodefiles.ErrNotFound
	}
	return f, nil
}

// stubUpgradable always returns the configured isUpgrade bool.
type stubUpgradable struct{ isUpgrade bool }

func (s stubUpgradable) IsRevisionUpgrade(_ quality.Model, _ quality.Model) bool {
	return s.isUpgrade
}

// stubPropersCfg holds the configured ProperDownloadType.
type stubPropersCfg struct{ kind quality.ProperDownloadType }

func (s stubPropersCfg) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return s.kind
}

func ptrInt(v int) *int { return &v }

func newRepackSpec(t *testing.T, isUpgrade bool, propers quality.ProperDownloadType, files map[int]*episodefiles.File) *decisionengine.RepackSpecification {
	t.Helper()
	spec, err := decisionengine.NewRepackSpecification(
		stubUpgradable{isUpgrade: isUpgrade},
		stubPropersCfg{kind: propers},
		stubFileResolver{files: files},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewRepackSpecification: %v", err)
	}
	return spec
}

func repackSubject(isRepack bool, releaseGroup string, episodeFileIDs []int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeFileID: efp}
	}
	return &decisionengine.Subject{
		Series:   nil,
		Episodes: eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseGroup: releaseGroup,
			Quality: quality.Model{
				Quality:  quality.HDTV720p,
				Revision: quality.Revision{Version: 2, IsRepack: isRepack},
			},
		},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
}

func TestRepack_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewRepackSpecification(nil, stubPropersCfg{}, stubFileResolver{}, nil); err == nil {
		t.Errorf("expected error on nil upgradable")
	}
	if _, err := decisionengine.NewRepackSpecification(stubUpgradable{}, nil, stubFileResolver{}, nil); err == nil {
		t.Errorf("expected error on nil cfg")
	}
	if _, err := decisionengine.NewRepackSpecification(stubUpgradable{}, stubPropersCfg{}, nil, nil); err == nil {
		t.Errorf("expected error on nil files")
	}
}

func TestRepack_AcceptsNonRepack(t *testing.T) {
	t.Parallel()
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, nil)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(false, "GROUP", []int{0}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on non-repack release, got %+v", got)
	}
}

func TestRepack_AcceptsWhenDoNotPrefer(t *testing.T) {
	t.Parallel()
	spec := newRepackSpec(t, true, quality.ProperDoNotPrefer, nil)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{0}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when DoNotPrefer, got %+v", got)
	}
}

func TestRepack_AcceptsNoExistingFiles(t *testing.T) {
	t.Parallel()
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, nil)
	// All EpisodeFileIDs are 0 → nothing to compare against.
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{0, 0}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when no existing files, got %+v", got)
	}
}

func TestRepack_RejectsWhenDoNotUpgrade(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "GROUP", Quality: "{}"},
	}
	spec := newRepackSpec(t, true, quality.ProperDoNotUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when DoNotUpgrade, got Accept")
	}
	if got.Reason != decisionengine.ReasonRepackDisabled {
		t.Errorf("Reason = %v, want ReasonRepackDisabled", got.Reason)
	}
}

func TestRepack_AcceptsMatchingReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "GROUP", Quality: "{}"},
	}
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on matching release-group, got %+v", got)
	}
}

func TestRepack_AcceptsMatchingReleaseGroupCaseInsensitive(t *testing.T) {
	t.Parallel()
	// C# uses `EqualsIgnoreCase` for repack — case-insensitive.
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "Group", Quality: "{}"},
	}
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on case-insensitive match, got %+v", got)
	}
}

func TestRepack_RejectsMismatchedReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "OTHER", Quality: "{}"},
	}
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on mismatched release-group, got Accept")
	}
	if got.Reason != decisionengine.ReasonRepackReleaseGroupDoesNotMatch {
		t.Errorf("Reason = %v, want ReasonRepackReleaseGroupDoesNotMatch", got.Reason)
	}
}

func TestRepack_RejectsEmptyFileReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "", Quality: "{}"},
	}
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on empty file releaseGroup, got Accept")
	}
	if got.Reason != decisionengine.ReasonRepackUnknownReleaseGroup {
		t.Errorf("Reason = %v, want ReasonRepackUnknownReleaseGroup", got.Reason)
	}
}

func TestRepack_RejectsEmptyReleaseReleaseGroup(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "GROUP", Quality: "{}"},
	}
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on empty release releaseGroup, got Accept")
	}
	if got.Reason != decisionengine.ReasonRepackUnknownReleaseGroup {
		t.Errorf("Reason = %v, want ReasonRepackUnknownReleaseGroup", got.Reason)
	}
}

func TestRepack_AcceptsWhenNotRevisionUpgrade(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, ReleaseGroup: "OTHER", Quality: "{}"},
	}
	// stubUpgradable returns false → revision check skips.
	spec := newRepackSpec(t, false, quality.ProperPreferAndUpgrade, files)
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when not a revision upgrade, got %+v", got)
	}
}

func TestRepack_LookupErrorSkipsEpisode(t *testing.T) {
	t.Parallel()
	// Non-NotFound error → skip. Spec accepts since no other comparisons fire.
	spec, err := decisionengine.NewRepackSpecification(
		stubUpgradable{isUpgrade: true},
		stubPropersCfg{kind: quality.ProperPreferAndUpgrade},
		stubFileResolver{err: errors.New("db down")},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewRepackSpecification: %v", err)
	}
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{1}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on lookup error (skip episode), got %+v", got)
	}
}

func TestRepack_NotFoundSkipsEpisode(t *testing.T) {
	t.Parallel()
	// EpisodeFileID points to a missing file → skip.
	spec := newRepackSpec(t, true, quality.ProperPreferAndUpgrade, map[int]*episodefiles.File{})
	got := spec.IsSatisfiedBy(t.Context(), repackSubject(true, "GROUP", []int{99}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when EpisodeFile missing, got %+v", got)
	}
}

func TestRepack_Metadata(t *testing.T) {
	t.Parallel()
	spec := newRepackSpec(t, false, quality.ProperPreferAndUpgrade, nil)
	if spec.Name() != "Repack" {
		t.Errorf("Name = %q, want Repack", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDatabase {
		t.Errorf("Priority = %v, want Database", spec.Priority())
	}
}
