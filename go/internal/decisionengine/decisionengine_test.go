package decisionengine_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
)

// TestRejectionKind_OrdinalsAndStrings pins the C# wire values at
// `RejectionType.cs:5-7` and the lower-case String render.
func TestRejectionKind_OrdinalsAndStrings(t *testing.T) {
	t.Parallel()
	if int(decisionengine.RejectionPermanent) != 0 {
		t.Errorf("RejectionPermanent = %d, want 0", decisionengine.RejectionPermanent)
	}
	if int(decisionengine.RejectionTemporary) != 1 {
		t.Errorf("RejectionTemporary = %d, want 1", decisionengine.RejectionTemporary)
	}
	if got := decisionengine.RejectionPermanent.String(); got != "permanent" {
		t.Errorf("RejectionPermanent.String() = %q, want %q", got, "permanent")
	}
	if got := decisionengine.RejectionTemporary.String(); got != "temporary" {
		t.Errorf("RejectionTemporary.String() = %q, want %q", got, "temporary")
	}
	if got := decisionengine.RejectionKind(99).String(); got != "unknown" {
		t.Errorf("RejectionKind(99).String() = %q, want unknown", got)
	}
}

// TestSpecPriority_OrdinalsAndAliases pins the C# `SpecificationPriority.cs`
// values + the three alias ordinals collapsing onto 0.
func TestSpecPriority_OrdinalsAndAliases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  decisionengine.SpecPriority
		want int
	}{
		{"Default", decisionengine.SpecPriorityDefault, 0},
		{"Parsing", decisionengine.SpecPriorityParsing, 0},
		{"Database", decisionengine.SpecPriorityDatabase, 0},
		{"Disk", decisionengine.SpecPriorityDisk, 1},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestUpgradeableRejectReason_Ordinals pins the 8 entries against
// `UpgradeableRejectReason.cs:5-12`. Wire-stable order.
func TestUpgradeableRejectReason_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  decisionengine.UpgradeableRejectReason
		want int
	}{
		{"None", decisionengine.UpgradeableNone, 0},
		{"BetterQuality", decisionengine.UpgradeableBetterQuality, 1},
		{"BetterRevision", decisionengine.UpgradeableBetterRevision, 2},
		{"QualityCutoff", decisionengine.UpgradeableQualityCutoff, 3},
		{"CustomFormatScore", decisionengine.UpgradeableCustomFormatScore, 4},
		{"CustomFormatCutoff", decisionengine.UpgradeableCustomFormatCutoff, 5},
		{"MinCustomFormatScore", decisionengine.UpgradeableMinCustomFormatScore, 6},
		{"UpgradesNotAllowed", decisionengine.UpgradeableUpgradesNotAllowed, 7},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("Upgradeable%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestReleaseSourceType_Ordinals pins the 6-entry enum at
// `RemoteEpisode.cs:49-54`.
func TestReleaseSourceType_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  decisionengine.ReleaseSourceType
		want int
	}{
		{"Unknown", decisionengine.ReleaseSourceUnknown, 0},
		{"RSS", decisionengine.ReleaseSourceRSS, 1},
		{"Search", decisionengine.ReleaseSourceSearch, 2},
		{"UserInvokedSearch", decisionengine.ReleaseSourceUserInvokedSearch, 3},
		{"InteractiveSearch", decisionengine.ReleaseSourceInteractiveSearch, 4},
		{"ReleasePush", decisionengine.ReleaseSourceReleasePush, 5},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("ReleaseSource%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestRejectionReason_OrdinalPins guards against accidental re-ordering
// of the 75-entry RejectionReason enum. Spot-checks:
//
//   - Reason 0 is Unknown.
//   - Reason 4 is UnableToParse — the canonical "early-rejection".
//   - Reason 26 is FullSeason — middle of the enum.
//   - Reason 74 is BeforeAirDate — the last (most-recently-added) entry.
//
// If any of these shift, the v3 wire payload broke and a downstream
// frontend must be updated in lockstep — flag for review BEFORE landing.
func TestRejectionReason_OrdinalPins(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  decisionengine.RejectionReason
		want int
	}{
		{"Unknown", decisionengine.ReasonUnknown, 0},
		{"UnableToParse", decisionengine.ReasonUnableToParse, 4},
		{"FullSeason", decisionengine.ReasonFullSeason, 26},
		{"MaximumSizeExceeded", decisionengine.ReasonMaximumSizeExceeded, 39},
		{"QualityNotWanted", decisionengine.ReasonQualityNotWanted, 45},
		{"BeforeAirDate", decisionengine.ReasonBeforeAirDate, 74},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("Reason%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestRejection_StringFormat pins the C# `Rejection.ToString` output
// at `Rejection.cs:23-26`: `[<kind>] <message>`.
func TestRejection_StringFormat(t *testing.T) {
	t.Parallel()
	r := decisionengine.NewRejection(decisionengine.ReasonQualityNotWanted, "quality 'WEBDL-720p' not in profile")
	want := "[permanent] quality 'WEBDL-720p' not in profile"
	if got := r.String(); got != want {
		t.Errorf("Rejection.String() = %q, want %q", got, want)
	}
	temp := decisionengine.NewTemporaryRejection(decisionengine.ReasonMinimumAge, "release too new")
	wantTemp := "[temporary] release too new"
	if got := temp.String(); got != wantTemp {
		t.Errorf("Temporary.String() = %q, want %q", got, wantTemp)
	}
}

// TestSpecDecision_AcceptIsSingleton pins the C# pattern at
// `DownloadSpecDecision.cs:7,13-15`: every Accept() returns the SAME
// underlying value. (Go won't observe pointer identity for value
// types, but the package keeps a single source-of-truth so a future
// shift to a pointer-returning shape doesn't change semantics.)
func TestSpecDecision_AcceptIsSingleton(t *testing.T) {
	t.Parallel()
	a := decisionengine.Accept()
	b := decisionengine.Accept()
	if !a.Accepted || !b.Accepted {
		t.Errorf("Accept() should produce Accepted=true")
	}
	if a != b {
		t.Errorf("Accept() should produce equal values: a=%+v b=%+v", a, b)
	}
}

// TestSpecDecision_RejectCarriesReasonAndMessage pins the rejection
// factory at `:22-29`.
func TestSpecDecision_RejectCarriesReasonAndMessage(t *testing.T) {
	t.Parallel()
	d := decisionengine.Reject(decisionengine.ReasonSample, "looks like a sample")
	if d.Accepted {
		t.Errorf("Reject() Accepted = true, want false")
	}
	if d.Reason != decisionengine.ReasonSample {
		t.Errorf("Reject() Reason = %v, want ReasonSample", d.Reason)
	}
	if d.Message != "looks like a sample" {
		t.Errorf("Reject() Message = %q, want %q", d.Message, "looks like a sample")
	}
}

// TestDecision_PredicatesEmptyApproved pins the C# `Approved` semantics
// at `DownloadDecision.cs:11`.
func TestDecision_PredicatesEmptyApproved(t *testing.T) {
	t.Parallel()
	d := decisionengine.NewDecision("subject")
	if !d.Approved() {
		t.Errorf("NewDecision: Approved() = false, want true")
	}
	if d.TemporarilyRejected() {
		t.Errorf("NewDecision: TemporarilyRejected() = true, want false")
	}
	if d.Rejected() {
		t.Errorf("NewDecision: Rejected() = true, want false")
	}
}

// TestDecision_PermanentRejectionFlagsRejected pins
// `DownloadDecision.Rejected` at `:21-27`.
func TestDecision_PermanentRejectionFlagsRejected(t *testing.T) {
	t.Parallel()
	d := decisionengine.NewRejectedDecision("subject",
		decisionengine.NewRejection(decisionengine.ReasonBlocklisted, "blocklisted"))
	if d.Approved() {
		t.Errorf("Permanent rejection: Approved() = true, want false")
	}
	if !d.Rejected() {
		t.Errorf("Permanent rejection: Rejected() = false, want true")
	}
	if d.TemporarilyRejected() {
		t.Errorf("Permanent rejection: TemporarilyRejected() = true, want false")
	}
}

// TestDecision_OnlyTemporaryFlagsTemporarilyRejected pins
// `DownloadDecision.TemporarilyRejected` at `:13-19`. Decision has
// rejections but they're all Temporary — must NOT be flagged Rejected.
func TestDecision_OnlyTemporaryFlagsTemporarilyRejected(t *testing.T) {
	t.Parallel()
	d := decisionengine.NewRejectedDecision("subject",
		decisionengine.NewTemporaryRejection(decisionengine.ReasonMinimumAge, "too new"),
		decisionengine.NewTemporaryRejection(decisionengine.ReasonMinimumAgeDelay, "delay profile"))
	if d.Approved() {
		t.Errorf("Temporary-only: Approved() = true, want false")
	}
	if d.Rejected() {
		t.Errorf("Temporary-only: Rejected() = true, want false")
	}
	if !d.TemporarilyRejected() {
		t.Errorf("Temporary-only: TemporarilyRejected() = false, want true")
	}
}

// TestDecision_MixedRejectionsFlagsRejected — the documented gotcha:
// one Permanent among Temporary rejections demotes the whole decision
// from TemporarilyRejected to Rejected. Mirrors the C# `Any/All` split
// at `:15-26`.
func TestDecision_MixedRejectionsFlagsRejected(t *testing.T) {
	t.Parallel()
	d := decisionengine.NewRejectedDecision("subject",
		decisionengine.NewTemporaryRejection(decisionengine.ReasonMinimumAge, "too new"),
		decisionengine.NewRejection(decisionengine.ReasonBlocklisted, "blocklisted"))
	if !d.Rejected() {
		t.Errorf("Mixed: Rejected() = false, want true (one Permanent demotes)")
	}
	if d.TemporarilyRejected() {
		t.Errorf("Mixed: TemporarilyRejected() = true, want false")
	}
}

// TestNewRejectedDecision_CopiesRejectionsSlice — the constructor must
// copy the rejections slice so callers can mutate their input without
// affecting the Decision.
func TestNewRejectedDecision_CopiesRejectionsSlice(t *testing.T) {
	t.Parallel()
	rejections := []decisionengine.Rejection{
		decisionengine.NewRejection(decisionengine.ReasonSample, "sample"),
	}
	d := decisionengine.NewRejectedDecision("subject", rejections...)
	rejections[0] = decisionengine.NewRejection(decisionengine.ReasonBlocklisted, "mutated")

	// The Decision should still reflect the original Sample rejection.
	if len(d.Rejections) != 1 || d.Rejections[0].Reason != decisionengine.ReasonSample {
		t.Errorf("Decision rejections were mutated: got %+v", d.Rejections)
	}
}

// TestNewRejectedDecision_NoRejectionsIsApproved — passing zero
// rejections to NewRejectedDecision should produce an Approved
// Decision (defensive: callers might splat an empty slice).
func TestNewRejectedDecision_NoRejectionsIsApproved(t *testing.T) {
	t.Parallel()
	d := decisionengine.NewRejectedDecision("subject")
	if !d.Approved() {
		t.Errorf("NewRejectedDecision with no rejections: Approved() = false, want true")
	}
}
