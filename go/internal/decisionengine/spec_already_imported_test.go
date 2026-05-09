package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubCDH is the minimal AlreadyImportedCDHReader test double.
type stubCDH bool

func (b stubCDH) EnableCompletedDownloadHandling() bool { return bool(b) }

// stubHistoryByEpisode is the minimal HistoryByEpisodeFinder.
type stubHistoryByEpisode struct {
	rows []history.Entry
	err  error
}

func (s stubHistoryByEpisode) FindByEpisodeId(_ context.Context, _ int) ([]history.Entry, error) {
	return s.rows, s.err
}

func aiSpec(t *testing.T, cdh bool, rows []history.Entry, err error) *decisionengine.AlreadyImportedSpecification {
	t.Helper()
	spec, sErr := decisionengine.NewAlreadyImportedSpecification(stubCDH(cdh), stubHistoryByEpisode{rows: rows, err: err}, slog.New(slog.DiscardHandler))
	if sErr != nil {
		t.Fatalf("constructor: %v", sErr)
	}
	return spec
}

// titleHDTV720p / titleHDTV1080p — fixed Quality JSON strings the C#
// fixture writes via QualityModel(Quality, Revision(version:1)). Id
// 4=HDTV-720p, 9=HDTV-1080p; revision is the v1 default.
const (
	titleS01E01     = "Series.Title.S01E01.720p.HDTV.x264-Sonarr"
	qualityHDTV720  = `{"quality":4,"revision":{"version":1,"real":0,"isRepack":false}}`
	qualityHDTV1080 = `{"quality":9,"revision":{"version":1,"real":0,"isRepack":false}}`
)

// historySingleEpisode builds a Subject carrying one Episode with a
// non-zero EpisodeFileID (i.e. HasFile=true) plus the supplied
// Release. Mirrors the C# fixture's `singleEpisodeList` setup.
func subjectWithFile(release any) *decisionengine.Subject {
	fileID := 1
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7},
		Episodes: []episodes.Episode{
			{ID: 1, EpisodeFileID: &fileID},
		},
		Release: release,
	}
}

// grabbedRow / importedRow build canonical-title history rows for
// the per-test fixtures. titleS01E01 is the C# fixture's TITLE
// constant reused for every row — kept inline (rather than a
// parameter) since no test needs a different SourceTitle on the
// history side; the release-side title varies in
// `subj.Release.Title` instead.
//
// grabbedRow always pins Quality=HDTV-720p — the C# fixture's grab
// row always uses _hdtv720p; the upgrade case varies the IMPORTED
// row's quality, not the grabbed one.
func grabbedRow(downloadID string) history.Entry {
	t := int(history.EventTypeGrabbed)
	return history.Entry{DownloadID: downloadID, SourceTitle: titleS01E01, Quality: qualityHDTV720, EventType: &t}
}

func importedRow(downloadID, q string) history.Entry {
	t := int(history.EventTypeDownloadFolderImported)
	return history.Entry{DownloadID: downloadID, SourceTitle: titleS01E01, Quality: q, EventType: &t}
}

// --- constructor guards --------------------------------------------

func TestAlreadyImported_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewAlreadyImportedSpecification(nil, stubHistoryByEpisode{}, nil); err == nil {
		t.Errorf("expected error on nil CDH reader, got nil")
	}
	if _, err := decisionengine.NewAlreadyImportedSpecification(stubCDH(true), nil, nil); err == nil {
		t.Errorf("expected error on nil history finder, got nil")
	}
}

// --- C# AlreadyImportedSpecificationFixture ports -------------------

func TestAlreadyImported_AcceptsWhenCDHDisabled(t *testing.T) {
	t.Parallel()
	spec := aiSpec(t, false, nil, nil)
	subj := subjectWithFile(&parser.ReleaseInfo{Title: titleS01E01})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("CDH-disabled must accept, got %+v", got)
	}
}

func TestAlreadyImported_AcceptsWhenEpisodeHasNoFile(t *testing.T) {
	t.Parallel()
	spec := aiSpec(t, true, nil, nil)
	zero := 0
	subj := &decisionengine.Subject{
		Series:   &series.Series{ID: 7},
		Episodes: []episodes.Episode{{ID: 1, EpisodeFileID: &zero}},
		Release:  &parser.ReleaseInfo{Title: titleS01E01},
	}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when episode has no file, got %+v", got)
	}
}

func TestAlreadyImported_AcceptsWhenNoGrabbedEvent(t *testing.T) {
	t.Parallel()
	spec := aiSpec(t, true, nil, nil)
	subj := subjectWithFile(&parser.ReleaseInfo{Title: titleS01E01})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when no Grabbed history, got %+v", got)
	}
}

func TestAlreadyImported_AcceptsWhenNoImportedEvent(t *testing.T) {
	t.Parallel()
	rows := []history.Entry{
		grabbedRow("ABC123"),
	}
	spec := aiSpec(t, true, rows, nil)
	subj := subjectWithFile(&parser.ReleaseInfo{Title: titleS01E01})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when no DownloadFolderImported row, got %+v", got)
	}
}

func TestAlreadyImported_AcceptsWhenGrabbedAndImportedQualityIsTheSame(t *testing.T) {
	t.Parallel()
	rows := []history.Entry{
		importedRow("ABC123", qualityHDTV720),
		grabbedRow("ABC123"),
	}
	spec := aiSpec(t, true, rows, nil)
	subj := subjectWithFile(&parser.ReleaseInfo{Title: titleS01E01})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("same-quality grabbed+imported must Accept, got %+v", got)
	}
}

func TestAlreadyImported_AcceptsWhenGrabbedDownloadIdAndReleaseHashUnknown(t *testing.T) {
	t.Parallel()
	rows := []history.Entry{
		importedRow("ABC123", qualityHDTV1080),
		grabbedRow("ABC123"),
	}
	spec := aiSpec(t, true, rows, nil)
	// Different release Title (so the title-match arm doesn't reject)
	// + Torrent protocol with empty InfoHash → both reject paths skip.
	subj := subjectWithFile(&parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Different.Title",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("torrent-with-no-hash + different-title must Accept, got %+v", got)
	}
}

func TestAlreadyImported_AcceptsWhenGrabbedDownloadHasNoId(t *testing.T) {
	t.Parallel()
	// Grabbed row with empty DownloadID + an Imported row with a real
	// DownloadID — they don't pair (firstImportedForGrab returns
	// none) so the spec accepts even though the release hash matches
	// the imported row's DownloadId.
	grab := grabbedRow("")
	imp := importedRow("XYZ789", qualityHDTV1080)
	spec := aiSpec(t, true, []history.Entry{imp, grab}, nil)
	subj := subjectWithFile(&parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            titleS01E01,
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		InfoHash: "XYZ789",
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("grab without DownloadID must Accept, got %+v", got)
	}
}

func TestAlreadyImported_RejectsWhenTorrentHashMatchesGrabbedDownloadId(t *testing.T) {
	t.Parallel()
	rows := []history.Entry{
		importedRow("ABC123", qualityHDTV1080),
		grabbedRow("ABC123"),
	}
	spec := aiSpec(t, true, rows, nil)
	subj := subjectWithFile(&parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Different.Title", // title-match arm skipped
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		InfoHash: "ABC123",
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected Reject (same hash), got Accept")
	}
	if got.Reason != decisionengine.ReasonAlreadyImportedSameHash {
		t.Errorf("Reason = %v, want ReasonAlreadyImportedSameHash", got.Reason)
	}
	if got.Message != "Has same torrent hash as a grabbed and imported release" {
		t.Errorf("Message = %q, want exact C# string", got.Message)
	}
}

func TestAlreadyImported_RejectsWhenReleaseTitleMatchesGrabbedSourceTitle(t *testing.T) {
	t.Parallel()
	rows := []history.Entry{
		importedRow("ABC123", qualityHDTV1080),
		grabbedRow("ABC123"),
	}
	spec := aiSpec(t, true, rows, nil)
	// Usenet release (no torrent hash check) with matching title.
	subj := subjectWithFile(&parser.ReleaseInfo{
		Title:            titleS01E01,
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected Reject (same title), got Accept")
	}
	if got.Reason != decisionengine.ReasonAlreadyImportedSameName {
		t.Errorf("Reason = %v, want ReasonAlreadyImportedSameName", got.Reason)
	}
	if got.Message != "Has same release name as a grabbed and imported release" {
		t.Errorf("Message = %q, want exact C# string", got.Message)
	}
}

func TestAlreadyImported_AcceptsOnHistoryError(t *testing.T) {
	t.Parallel()
	// Defensive: a transient repo error degrades to accept rather
	// than fail the candidate.
	spec := aiSpec(t, true, nil, errors.New("boom"))
	subj := subjectWithFile(&parser.ReleaseInfo{Title: titleS01E01})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("history error must degrade to Accept, got %+v", got)
	}
}

func TestAlreadyImported_TitleMatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	rows := []history.Entry{
		importedRow("ABC123", qualityHDTV1080),
		grabbedRow("ABC123"),
	}
	spec := aiSpec(t, true, rows, nil)
	subj := subjectWithFile(&parser.ReleaseInfo{
		Title:            "SERIES.TITLE.S01E01.720P.HDTV.X264-SONARR",
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("case-insensitive title match must Reject, got Accept")
	}
}

func TestAlreadyImported_NameAndKindAndPriority(t *testing.T) {
	t.Parallel()
	spec := aiSpec(t, true, nil, nil)
	if got := spec.Name(); got != decisionengine.AlreadyImportedSpecificationName {
		t.Errorf("Name = %q, want %q", got, decisionengine.AlreadyImportedSpecificationName)
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDatabase {
		t.Errorf("Priority = %v, want Database", spec.Priority())
	}
}
