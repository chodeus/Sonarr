package decisionengine_test

import (
	"log/slog"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

type stubAutoUnmonitor struct{ on bool }

func (s stubAutoUnmonitor) AutoUnmonitorPreviouslyDownloadedEpisodes() bool { return s.on }

// stubFileExistence reports a path present unless it's in the missing set.
type stubFileExistence struct{ missing map[string]bool }

func (s stubFileExistence) FileExists(path string) bool { return !s.missing[path] }

func newDeletedEpisodeFileSpec(t *testing.T, autoUnmonitor bool, files map[int]*episodefiles.File, missing map[string]bool) *decisionengine.DeletedEpisodeFileSpecification {
	t.Helper()
	spec, err := decisionengine.NewDeletedEpisodeFileSpecification(
		stubAutoUnmonitor{on: autoUnmonitor},
		stubFileExistence{missing: missing},
		stubFileResolver{files: files},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewDeletedEpisodeFileSpecification: %v", err)
	}
	return spec
}

func deletedEpisodeFileSubject(seriesPath string, episodeFileIDs ...int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeFileID: efp}
	}
	return &decisionengine.Subject{
		Series:            &series.Series{ID: 1, Path: seriesPath},
		Episodes:          eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{},
	}
}

func TestDeletedEpisodeFile_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewDeletedEpisodeFileSpecification(nil, stubFileExistence{}, stubFileResolver{}, nil); err == nil {
		t.Error("expected error on nil cfg")
	}
	if _, err := decisionengine.NewDeletedEpisodeFileSpecification(stubAutoUnmonitor{}, nil, stubFileResolver{}, nil); err == nil {
		t.Error("expected error on nil disk")
	}
	if _, err := decisionengine.NewDeletedEpisodeFileSpecification(stubAutoUnmonitor{}, stubFileExistence{}, nil, nil); err == nil {
		t.Error("expected error on nil files")
	}
}

func TestDeletedEpisodeFile_AutoUnmonitorOff_Accept(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: {ID: 10, RelativePath: "S01/ep.mkv"}}
	spec := newDeletedEpisodeFileSpec(t, false, files, map[string]bool{filepath.Join("/tv/Show", "S01/ep.mkv"): true})
	if !spec.IsSatisfiedBy(t.Context(), deletedEpisodeFileSubject("/tv/Show", 10), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("AutoUnmonitor off → Accept even if file missing")
	}
}

func TestDeletedEpisodeFile_Search_Accept(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: {ID: 10, RelativePath: "S01/ep.mkv"}}
	spec := newDeletedEpisodeFileSpec(t, true, files, map[string]bool{filepath.Join("/tv/Show", "S01/ep.mkv"): true})
	info := decisionengine.ReleaseDecisionInformation{SearchCriteria: &decisionengine.SearchCriteria{}}
	if !spec.IsSatisfiedBy(t.Context(), deletedEpisodeFileSubject("/tv/Show", 10), info).Accepted {
		t.Error("search → Accept (skip check)")
	}
}

func TestDeletedEpisodeFile_FilePresent_Accept(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: {ID: 10, RelativePath: "S01/ep.mkv"}}
	spec := newDeletedEpisodeFileSpec(t, true, files, nil /* nothing missing */)
	if !spec.IsSatisfiedBy(t.Context(), deletedEpisodeFileSubject("/tv/Show", 10), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("file present on disk → Accept")
	}
}

func TestDeletedEpisodeFile_FileMissing_Reject(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{10: {ID: 10, RelativePath: "S01/ep.mkv"}}
	spec := newDeletedEpisodeFileSpec(t, true, files, map[string]bool{filepath.Join("/tv/Show", "S01/ep.mkv"): true})
	got := spec.IsSatisfiedBy(t.Context(), deletedEpisodeFileSubject("/tv/Show", 10), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonEpisodeNotMonitored {
		t.Fatalf("missing file should reject EpisodeNotMonitored, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	if spec.Kind() != decisionengine.RejectionTemporary {
		t.Errorf("Kind() = %v, want Temporary", spec.Kind())
	}
}

func TestDeletedEpisodeFile_NoFiles_Accept(t *testing.T) {
	t.Parallel()
	spec := newDeletedEpisodeFileSpec(t, true, nil, nil)
	if !spec.IsSatisfiedBy(t.Context(), deletedEpisodeFileSubject("/tv/Show", 0), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("no episode files → Accept")
	}
}
