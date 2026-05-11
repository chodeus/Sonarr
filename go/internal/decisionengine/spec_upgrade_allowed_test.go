package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// === UpgradeAllowedSpecification ===

// stubProfileLookup feeds a fixed (*ProfileView, ok, err) tuple. The
// view's Profile is constructed from a 0-value qualityprofiles.Profile
// so callers can mutate fields like UpgradeAllowed before the lookup.
type stubProfileLookup struct {
	view *decisionengine.ProfileView
	ok   bool
	err  error
}

func (s stubProfileLookup) LookupProfile(_ context.Context, _ int) (*decisionengine.ProfileView, bool, error) {
	return s.view, s.ok, s.err
}

// stubUpgradeAllowedChecker returns a fixed allow value. Captures the
// last invocation args so per-test asserts can pin them.
type stubUpgradeAllowedChecker struct {
	allow         bool
	gotCurrent    quality.Model
	gotCurrentCFs []customformats.CustomFormat
	gotNew        quality.Model
	gotNewCFs     []customformats.CustomFormat
	calls         int
}

func (s *stubUpgradeAllowedChecker) IsUpgradeAllowed(
	_ *decisionengine.ProfileView,
	currentQuality quality.Model,
	currentCustomFormats []customformats.CustomFormat,
	newQuality quality.Model,
	newCustomFormats []customformats.CustomFormat,
) bool {
	s.calls++
	s.gotCurrent = currentQuality
	s.gotCurrentCFs = currentCustomFormats
	s.gotNew = newQuality
	s.gotNewCFs = newCustomFormats
	return s.allow
}

// stubFormatCalc returns a fixed format slice + optional error. Records
// the last invocation's file pointer so callers can pin the
// per-EpisodeFile dispatch.
type stubFormatCalc struct {
	formats []customformats.CustomFormat
	err     error
	gotFile *episodefiles.File
}

func (s *stubFormatCalc) ParseCustomFormat(_ context.Context, file *episodefiles.File, _ *series.Series) ([]customformats.CustomFormat, error) {
	s.gotFile = file
	if s.err != nil {
		return nil, s.err
	}
	return s.formats, nil
}

func upgradeAllowedView(t *testing.T) *decisionengine.ProfileView {
	t.Helper()
	view, err := decisionengine.NewProfileView(&qualityprofiles.Profile{
		ID:                7,
		Name:              "HD",
		UpgradeAllowed:    true,
		Items:             "[]",
		FormatItems:       "[]",
		MinFormatScore:    0,
		CutoffFormatScore: 0,
	})
	if err != nil {
		t.Fatalf("NewProfileView: %v", err)
	}
	return view
}

func upgradeAllowedSubject(episodeFileIDs []int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeFileID: efp}
	}
	pid := 7
	return &decisionengine.Subject{
		Series:   &series.Series{ID: 7, QualityProfileID: pid},
		Episodes: eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: quality.HDTV720p, Revision: quality.Revision{Version: 1}},
		},
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
}

func newUpgradeAllowedSpec(
	t *testing.T,
	allow bool,
	view *decisionengine.ProfileView,
	files map[int]*episodefiles.File,
	formats []customformats.CustomFormat,
) (*decisionengine.UpgradeAllowedSpecification, *stubUpgradeAllowedChecker, *stubFormatCalc) {
	t.Helper()
	checker := &stubUpgradeAllowedChecker{allow: allow}
	calc := &stubFormatCalc{formats: formats}
	spec, err := decisionengine.NewUpgradeAllowedSpecification(
		checker,
		stubProfileLookup{view: view, ok: view != nil},
		stubFileResolver{files: files},
		calc,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeAllowedSpecification: %v", err)
	}
	return spec, checker, calc
}

func TestUpgradeAllowed_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	checker := &stubUpgradeAllowedChecker{}
	view := upgradeAllowedView(t)
	if _, err := decisionengine.NewUpgradeAllowedSpecification(nil, stubProfileLookup{view: view, ok: true}, stubFileResolver{}, &stubFormatCalc{}, nil); err == nil {
		t.Errorf("expected error on nil checker")
	}
	if _, err := decisionengine.NewUpgradeAllowedSpecification(checker, nil, stubFileResolver{}, &stubFormatCalc{}, nil); err == nil {
		t.Errorf("expected error on nil profile lookup")
	}
	if _, err := decisionengine.NewUpgradeAllowedSpecification(checker, stubProfileLookup{view: view, ok: true}, nil, &stubFormatCalc{}, nil); err == nil {
		t.Errorf("expected error on nil file resolver")
	}
	if _, err := decisionengine.NewUpgradeAllowedSpecification(checker, stubProfileLookup{view: view, ok: true}, stubFileResolver{}, nil, nil); err == nil {
		t.Errorf("expected error on nil format calculator")
	}
}

func TestUpgradeAllowed_AcceptsNilSubject(t *testing.T) {
	t.Parallel()
	spec, checker, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), nil, nil)
	if got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on nil-Subject, got %+v", got)
	}
	if checker.calls != 0 {
		t.Errorf("checker should not be called on nil-Subject; calls=%d", checker.calls)
	}
}

func TestUpgradeAllowed_AcceptsNilSeries(t *testing.T) {
	t.Parallel()
	spec, _, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), nil, nil)
	subj := &decisionengine.Subject{ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{}}
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on nil-Series, got %+v", got)
	}
}

func TestUpgradeAllowed_AcceptsNilParsedEpisodeInfo(t *testing.T) {
	t.Parallel()
	spec, _, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), nil, nil)
	subj := &decisionengine.Subject{Series: &series.Series{ID: 1}}
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on nil-ParsedEpisodeInfo, got %+v", got)
	}
}

func TestUpgradeAllowed_AcceptsNilQualityProfileID(t *testing.T) {
	t.Parallel()
	spec, _, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), nil, nil)
	subj := upgradeAllowedSubject([]int{1})
	subj.Series.QualityProfileID = 0
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on nil-QualityProfileID, got %+v", got)
	}
}

func TestUpgradeAllowed_AcceptsOnProfileLookupError(t *testing.T) {
	t.Parallel()
	checker := &stubUpgradeAllowedChecker{allow: false}
	calc := &stubFormatCalc{}
	spec, err := decisionengine.NewUpgradeAllowedSpecification(
		checker,
		stubProfileLookup{err: errors.New("boom")},
		stubFileResolver{},
		calc,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeAllowedSpecification: %v", err)
	}
	subj := upgradeAllowedSubject([]int{1})
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on profile-lookup error (defensive), got %+v", got)
	}
	if checker.calls != 0 {
		t.Errorf("checker should not be called when profile lookup errors; calls=%d", checker.calls)
	}
}

func TestUpgradeAllowed_AcceptsOnProfileNotFound(t *testing.T) {
	t.Parallel()
	checker := &stubUpgradeAllowedChecker{allow: false}
	calc := &stubFormatCalc{}
	spec, err := decisionengine.NewUpgradeAllowedSpecification(
		checker,
		stubProfileLookup{ok: false},
		stubFileResolver{},
		calc,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeAllowedSpecification: %v", err)
	}
	subj := upgradeAllowedSubject([]int{1})
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on profile-not-found (defensive), got %+v", got)
	}
}

func TestUpgradeAllowed_SkipsEpisodesWithNoFile(t *testing.T) {
	t.Parallel()
	// Episodes with EpisodeFileID nil/0 → skipped per C# `:30`
	// `Where(c => c.EpisodeFileId != 0)`.
	spec, checker, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), nil, nil)
	subj := upgradeAllowedSubject([]int{0, 0})
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept when no episodes have files, got %+v", got)
	}
	if checker.calls != 0 {
		t.Errorf("checker should not be called when all episodes have no file; calls=%d", checker.calls)
	}
}

func TestUpgradeAllowed_SkipsMissingFile(t *testing.T) {
	t.Parallel()
	// EpisodeFileResolver returns ErrNotFound → skipped per C# `:32-36`
	// (file is no longer available).
	spec, checker, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), map[int]*episodefiles.File{}, nil)
	subj := upgradeAllowedSubject([]int{42})
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept when file lookup misses, got %+v", got)
	}
	if checker.calls != 0 {
		t.Errorf("checker should not be called when file resolver misses; calls=%d", checker.calls)
	}
}

func TestUpgradeAllowed_AcceptsWhenUpgradePermitted(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		42: {ID: 42, Quality: "{}"},
	}
	spec, checker, calc := newUpgradeAllowedSpec(t, true, upgradeAllowedView(t), files, nil)
	subj := upgradeAllowedSubject([]int{42})
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept when checker allows, got %+v", got)
	}
	if checker.calls != 1 {
		t.Errorf("expected 1 checker call, got %d", checker.calls)
	}
	if calc.gotFile == nil || calc.gotFile.ID != 42 {
		t.Errorf("expected format calc invoked with file=42, got %v", calc.gotFile)
	}
}

func TestUpgradeAllowed_RejectsWhenCheckerDenies(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		42: {ID: 42, Quality: "{}"},
	}
	spec, _, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), files, nil)
	subj := upgradeAllowedSubject([]int{42})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when checker denies, got Accept")
	}
	if got.Reason != decisionengine.ReasonQualityUpgradesDisabled {
		t.Errorf("Reason = %v, want ReasonQualityUpgradesDisabled", got.Reason)
	}
}

func TestUpgradeAllowed_PassesFileQualityAndFormats(t *testing.T) {
	t.Parallel()
	// Pin the per-file dispatch — file.Quality is decoded and the
	// per-file CFs from the calculator flow into IsUpgradeAllowed,
	// while Subject.CustomFormats flows in as the candidate-side CFs.
	candidateCF := customformats.CustomFormat{ID: 9, Name: "Candidate"}
	fileCF := customformats.CustomFormat{ID: 11, Name: "OnDisk"}
	files := map[int]*episodefiles.File{
		42: {ID: 42, Quality: `{"quality":1,"revision":{"version":1,"real":0,"isRepack":false}}`},
	}
	spec, checker, calc := newUpgradeAllowedSpec(t, true, upgradeAllowedView(t), files, []customformats.CustomFormat{fileCF})
	subj := upgradeAllowedSubject([]int{42})
	subj.CustomFormats = []customformats.CustomFormat{candidateCF}
	subj.ParsedEpisodeInfo.Quality = quality.Model{Quality: quality.Bluray1080p, Revision: quality.Revision{Version: 2}}
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept (checker=true), got %+v", got)
	}
	if checker.gotCurrent.Quality.ID != 1 {
		t.Errorf("currentQuality.ID = %d, want 1 (decoded from file)", checker.gotCurrent.Quality.ID)
	}
	if checker.gotNew.Quality.ID != quality.Bluray1080p.ID {
		t.Errorf("newQuality.ID = %d, want %d", checker.gotNew.Quality.ID, quality.Bluray1080p.ID)
	}
	if len(checker.gotCurrentCFs) != 1 || checker.gotCurrentCFs[0].ID != fileCF.ID {
		t.Errorf("currentCFs = %+v, want [fileCF]", checker.gotCurrentCFs)
	}
	if len(checker.gotNewCFs) != 1 || checker.gotNewCFs[0].ID != candidateCF.ID {
		t.Errorf("newCFs = %+v, want [candidateCF]", checker.gotNewCFs)
	}
	if calc.gotFile == nil || calc.gotFile.ID != 42 {
		t.Errorf("calc.gotFile = %v, want id=42", calc.gotFile)
	}
}

func TestUpgradeAllowed_RejectsAtFirstFailingFile(t *testing.T) {
	t.Parallel()
	// Two episodes both with files: the spec walks per-file and
	// short-circuits on the first IsUpgradeAllowed=false. Mirrors C#
	// `:30-52` early `return Reject`.
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
	}
	spec, checker, _ := newUpgradeAllowedSpec(t, false, upgradeAllowedView(t), files, nil)
	subj := upgradeAllowedSubject([]int{1, 2})
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on first failing file, got Accept")
	}
	if checker.calls != 1 {
		t.Errorf("expected 1 checker call (short-circuit), got %d", checker.calls)
	}
}

func TestUpgradeAllowed_SkipsOnFormatCalcError(t *testing.T) {
	t.Parallel()
	// Format calculator error → continue to next file (defensive).
	// Single episode + calc error → no IsUpgradeAllowed call → Accept.
	files := map[int]*episodefiles.File{
		42: {ID: 42, Quality: "{}"},
	}
	checker := &stubUpgradeAllowedChecker{allow: false}
	calc := &stubFormatCalc{err: errors.New("transient")}
	spec, err := decisionengine.NewUpgradeAllowedSpecification(
		checker,
		stubProfileLookup{view: upgradeAllowedView(t), ok: true},
		stubFileResolver{files: files},
		calc,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeAllowedSpecification: %v", err)
	}
	subj := upgradeAllowedSubject([]int{42})
	if got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on calc error (defensive), got %+v", got)
	}
	if checker.calls != 0 {
		t.Errorf("checker should not be called when calc errors; calls=%d", checker.calls)
	}
}

func TestUpgradeAllowed_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, _, _ := newUpgradeAllowedSpec(t, true, upgradeAllowedView(t), nil, nil)
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.UpgradeAllowedSpecificationName {
		t.Errorf("Name = %q, want %q", iface.Name(), decisionengine.UpgradeAllowedSpecificationName)
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v, want Default", iface.Priority())
	}
}
