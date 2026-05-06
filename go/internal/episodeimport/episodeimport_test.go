package episodeimport_test

import (
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
)

// TestImportRejectionReason_NumericOrder pins the wire-stable enum
// ordinals to their C# numeric IDs. The v3 React frontend reads
// these as ints from SignalR — a reorder breaks user-saved per-int
// reason filters.
func TestImportRejectionReason_NumericOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		got  episodeimport.ImportRejectionReason
		want int
	}{
		{episodeimport.ImportRejectionUnknown, 0},
		{episodeimport.ImportRejectionFileLocked, 1},
		{episodeimport.ImportRejectionUnknownSeries, 2},
		{episodeimport.ImportRejectionEpisodeAlreadyImported, 18},
		{episodeimport.ImportRejectionMinimumFreeSpace, 21},
		{episodeimport.ImportRejectionUnverifiedSceneMapping, 31},
		{episodeimport.ImportRejectionMultiSeason, 36},
	}
	for _, c := range cases {
		if int(c.got) != c.want {
			t.Errorf("%v = %d, want %d", c.got, int(c.got), c.want)
		}
	}
}

// TestImportRejectionReason_String pins each ordinal's Stringer
// output to the C# enum-identifier name. Out-of-range values fall
// through to a typed token.
func TestImportRejectionReason_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   episodeimport.ImportRejectionReason
		want string
	}{
		{episodeimport.ImportRejectionUnknown, "Unknown"},
		{episodeimport.ImportRejectionEpisodeAlreadyImported, "EpisodeAlreadyImported"},
		{episodeimport.ImportRejectionNotCustomFormatUpgradeAfterRename, "NotCustomFormatUpgradeAfterRename"},
		{episodeimport.ImportRejectionMultiSeason, "MultiSeason"},
		{episodeimport.ImportRejectionReason(99), "ImportRejectionReason(99)"},
		{episodeimport.ImportRejectionReason(-1), "ImportRejectionReason(-1)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", int(c.in), got, c.want)
		}
	}
}

func TestImportResultType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   episodeimport.ImportResultType
		want string
	}{
		{episodeimport.ImportResultImported, "Imported"},
		{episodeimport.ImportResultRejected, "Rejected"},
		{episodeimport.ImportResultSkipped, "Skipped"},
		{episodeimport.ImportResultType(7), "ImportResultType(7)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestImportMode_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   episodeimport.ImportMode
		want string
	}{
		{episodeimport.ImportModeAuto, "Auto"},
		{episodeimport.ImportModeMove, "Move"},
		{episodeimport.ImportModeCopy, "Copy"},
		{episodeimport.ImportMode(7), "ImportMode(7)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestImportRejection_DefaultPermanent(t *testing.T) {
	t.Parallel()
	r := episodeimport.NewImportRejection(episodeimport.ImportRejectionFullSeason, "season pack")
	if r.Reason != episodeimport.ImportRejectionFullSeason {
		t.Errorf("Reason = %v, want FullSeason", r.Reason)
	}
	if r.Message != "season pack" {
		t.Errorf("Message = %q, want %q", r.Message, "season pack")
	}
	if r.Kind != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", r.Kind)
	}
}

func TestImportRejection_WithKind(t *testing.T) {
	t.Parallel()
	r := episodeimport.NewImportRejectionWithKind(
		episodeimport.ImportRejectionMinimumFreeSpace, "low free space", decisionengine.RejectionTemporary)
	if r.Kind != decisionengine.RejectionTemporary {
		t.Errorf("Kind = %v, want Temporary", r.Kind)
	}
	if got := r.String(); !strings.Contains(strings.ToLower(got), "temporary") || !strings.Contains(got, "low free space") {
		t.Errorf("String() = %q, want contains temporary + message", got)
	}
}

func TestImportSpecDecision_AcceptIsSingleton(t *testing.T) {
	t.Parallel()
	a := episodeimport.AcceptImportSpec()
	b := episodeimport.AcceptImportSpec()
	if a != b {
		t.Errorf("AcceptImportSpec() returned different pointers; want shared singleton")
	}
	if !a.Accepted() {
		t.Errorf("singleton must report Accepted=true")
	}
	if a.Reason() != episodeimport.ImportRejectionUnknown {
		t.Errorf("accept Reason = %v, want Unknown", a.Reason())
	}
	if a.Message() != "" {
		t.Errorf("accept Message = %q, want empty", a.Message())
	}
}

func TestImportSpecDecision_Reject(t *testing.T) {
	t.Parallel()
	d := episodeimport.RejectImportSpec(episodeimport.ImportRejectionSample, "looks like a sample")
	if d.Accepted() {
		t.Errorf("Accepted = true, want false on reject")
	}
	if d.Reason() != episodeimport.ImportRejectionSample {
		t.Errorf("Reason = %v, want Sample", d.Reason())
	}
	if d.Message() != "looks like a sample" {
		t.Errorf("Message = %q, want exact", d.Message())
	}
}

func TestImportSpecDecision_RejectFormat(t *testing.T) {
	t.Parallel()
	d := episodeimport.RejectImportSpec(episodeimport.ImportRejectionMinimumFreeSpace,
		"need %d MB, have %d MB", 100, 25)
	if !strings.Contains(d.Message(), "need 100 MB, have 25 MB") {
		t.Errorf("printf interpolation lost: %q", d.Message())
	}
}

func TestImportSpecDecision_NilSafe(t *testing.T) {
	t.Parallel()
	var d *episodeimport.ImportSpecDecision
	if d.Accepted() {
		t.Errorf("nil.Accepted() = true, want false")
	}
	if d.Reason() != episodeimport.ImportRejectionUnknown {
		t.Errorf("nil.Reason() = %v, want Unknown", d.Reason())
	}
	if d.Message() != "" {
		t.Errorf("nil.Message() = %q, want empty", d.Message())
	}
}

func TestImportDecision_ApprovedZero(t *testing.T) {
	t.Parallel()
	d := episodeimport.NewImportDecision(episodeimport.NewLocalEpisode())
	if !d.Approved() {
		t.Errorf("zero rejections must be Approved")
	}
}

func TestImportDecision_RejectedNonEmpty(t *testing.T) {
	t.Parallel()
	d := episodeimport.NewImportDecision(
		episodeimport.NewLocalEpisode(),
		episodeimport.NewImportRejection(episodeimport.ImportRejectionSample, "sample"),
	)
	if d.Approved() {
		t.Errorf("with rejection, Approved must be false")
	}
	if len(d.Rejections) != 1 {
		t.Errorf("Rejections len = %d, want 1", len(d.Rejections))
	}
}

func TestImportDecision_RejectionDefensiveCopy(t *testing.T) {
	t.Parallel()
	rejs := []episodeimport.ImportRejection{
		episodeimport.NewImportRejection(episodeimport.ImportRejectionSample, "sample"),
	}
	d := episodeimport.NewImportDecision(episodeimport.NewLocalEpisode(), rejs...)
	// Mutate the input slice; the decision's copy must be untouched.
	rejs[0] = episodeimport.NewImportRejection(episodeimport.ImportRejectionFullSeason, "tampered")
	if d.Rejections[0].Reason != episodeimport.ImportRejectionSample {
		t.Errorf("input mutation leaked into stored decision: %v", d.Rejections[0].Reason)
	}
}

func TestImportDecision_NilApproved(t *testing.T) {
	t.Parallel()
	var d *episodeimport.ImportDecision
	if d.Approved() {
		t.Errorf("nil decision must report !Approved")
	}
}

func TestImportResult_TypeDerivation(t *testing.T) {
	t.Parallel()
	dec := episodeimport.NewImportDecision(episodeimport.NewLocalEpisode())
	rej := episodeimport.NewImportDecision(
		episodeimport.NewLocalEpisode(),
		episodeimport.NewImportRejection(episodeimport.ImportRejectionSample, "sample"),
	)

	// errors empty → Imported regardless of approval state.
	imported := episodeimport.NewImportedImportResult(dec, &episodefiles.File{})
	if got := imported.ResultType(); got != episodeimport.ImportResultImported {
		t.Errorf("imported result type = %v, want Imported", got)
	}

	// approved + errors → Skipped.
	skipped := episodeimport.NewSkippedImportResult(dec, "root folder missing")
	if got := skipped.ResultType(); got != episodeimport.ImportResultSkipped {
		t.Errorf("approved+errors result type = %v, want Skipped", got)
	}

	// rejected + errors → Rejected.
	rejected := episodeimport.NewSkippedImportResult(rej, "rejected with note")
	if got := rejected.ResultType(); got != episodeimport.ImportResultRejected {
		t.Errorf("rejected+errors result type = %v, want Rejected", got)
	}
}

func TestImportResult_NilDecisionPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil decision")
		}
	}()
	episodeimport.NewSkippedImportResult(nil, "boom")
}

func TestImportResult_NilSafeResultType(t *testing.T) {
	t.Parallel()
	var r *episodeimport.ImportResult
	if got := r.ResultType(); got != episodeimport.ImportResultImported {
		t.Errorf("nil ResultType = %v, want default Imported", got)
	}
}

// --- LocalEpisode ---------------------------------------------------

func TestNewLocalEpisode_NonNilSliceFields(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	if le.Episodes == nil || le.Languages == nil ||
		le.CustomFormats == nil || le.OriginalFileNameCustomFormats == nil {
		t.Errorf("NewLocalEpisode must initialise slice fields non-nil: %+v", le)
	}
}

func TestLocalEpisode_SeasonNumber_NoEpisodes(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	_, err := le.SeasonNumber()
	if !errors.Is(err, episodeimport.ErrInvalidSeason) {
		t.Errorf("err = %v, want wrapping ErrInvalidSeason", err)
	}
}

func TestLocalEpisode_SeasonNumber_Single(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Episodes = []episodes.Episode{
		{ID: 1, SeasonNumber: 5},
		{ID: 2, SeasonNumber: 5},
	}
	got, err := le.SeasonNumber()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 5 {
		t.Errorf("SeasonNumber = %d, want 5", got)
	}
}

func TestLocalEpisode_SeasonNumber_Multiple(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Episodes = []episodes.Episode{
		{ID: 1, SeasonNumber: 1},
		{ID: 2, SeasonNumber: 2},
	}
	_, err := le.SeasonNumber()
	if !errors.Is(err, episodeimport.ErrInvalidSeason) {
		t.Errorf("multi-season err = %v, want wrapping ErrInvalidSeason", err)
	}
}

func TestLocalEpisode_IsSpecial(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	if le.IsSpecial() {
		t.Errorf("zero-episode IsSpecial = true, want false (defensive)")
	}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 0}}
	if !le.IsSpecial() {
		t.Errorf("season-0 IsSpecial = false, want true")
	}
	le.Episodes = []episodes.Episode{{ID: 1, SeasonNumber: 1}}
	if le.IsSpecial() {
		t.Errorf("season-1 IsSpecial = true, want false")
	}
}

func TestLocalEpisode_String(t *testing.T) {
	t.Parallel()
	var nilLE *episodeimport.LocalEpisode
	if got := nilLE.String(); got != "" {
		t.Errorf("nil.String() = %q, want empty", got)
	}
	le := &episodeimport.LocalEpisode{Path: "/tv/Foo/foo.mkv"}
	if got := le.String(); got != "/tv/Foo/foo.mkv" {
		t.Errorf("String() = %q, want path verbatim", got)
	}
}

func TestLocalEpisode_GetSceneOrFileName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		sceneName string
		path      string
		want      string
	}{
		{"scene name set", "Series.S01E01.PROPER.GROUP", "/tv/Foo/foo.mkv", "Series.S01E01.PROPER.GROUP"},
		{"scene name whitespace", "   ", "/tv/Foo/foo.mkv", "foo"},
		{"unix path basename", "", "/tv/Foo/Bar baz.mkv", "Bar baz"},
		{"windows path basename", "", `C:\TV\Foo\bar.mkv`, "bar"},
		{"no extension", "", "/tv/Foo/bar", "bar"},
		{"leading dot kept", "", "/tv/Foo/.hidden.mkv", ".hidden"},
		{"empty path", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			le := &episodeimport.LocalEpisode{Path: c.path, SceneName: c.sceneName}
			if got := le.GetSceneOrFileName(); got != c.want {
				t.Errorf("GetSceneOrFileName() = %q, want %q", got, c.want)
			}
		})
	}
	var nilLE *episodeimport.LocalEpisode
	if got := nilLE.GetSceneOrFileName(); got != "" {
		t.Errorf("nil.GetSceneOrFileName() = %q, want empty", got)
	}
}

// TestSpecification_Implementable verifies the package's
// Specification interface is shaped correctly: a tiny inline impl
// must compile and dispatch via the interface. Catches accidental
// changes to the interface signature.
func TestSpecification_Implementable(t *testing.T) {
	t.Parallel()
	var _ episodeimport.Specification = stubSpec{}
}

type stubSpec struct{}

func (stubSpec) IsSatisfiedBy(_ *episodeimport.LocalEpisode, _ *downloadclients.Item) *episodeimport.ImportSpecDecision {
	return episodeimport.AcceptImportSpec()
}
func (stubSpec) Name() string { return "stub" }
