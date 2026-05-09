package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
)

// CustomFormatAllowedByProfileSpecificationName is the canonical
// Spec.Name() identifier.
const CustomFormatAllowedByProfileSpecificationName = "CustomFormatAllowedByProfile"

// CustomFormatAllowedByProfileProfileReader is the narrow
// profile-resolver dependency for
// [CustomFormatAllowedByProfileSpecification]. Same shape as the
// slice-3.65 [AcceptableSizeProfileReader] / [QualityAllowedByProfileProfileReader]
// — defined separately so the compile-time interface satisfaction
// stays scoped to one spec (Go's structural typing lets the same
// backing implementation satisfy all three).
//
// Returns the parsed [*ProfileView] for a given `qualityProfileID`,
// `ok=false` when the profile doesn't exist, or `(nil, false, err)`
// when the lookup fails outright.
type CustomFormatAllowedByProfileProfileReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// CustomFormatAllowedByProfileSpecification rejects releases whose
// per-Subject CustomFormatScore is below the series' QualityProfile
// MinFormatScore floor.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/CustomFormatAllowedByProfileSpecification.cs:1-34`.
// The C# class spelling has a lowercase 'b' in the middle
// (`CustomFormatAllowedbyProfileSpecification`); the Go port
// corrects to `ByProfile` for grep-readability — the canonical
// Spec.Name() carries the corrected form.
//
// # Algorithm (mirrors C# `:19-31`)
//
//  1. Resolve the series' QualityProfile via the narrow reader.
//  2. Reject when Subject.CustomFormatScore < MinFormatScore.
//  3. Otherwise accept.
//
// # Defensive Accept arms
//
// Profile-resolver errors / nil-Series / nil-Series-QualityProfileID
// / profile-not-found all Accept defensively. Same posture as the
// surrounding decisionengine specs — a quality-profile DB hiccup
// shouldn't reject every candidate. The C# tree dereferences
// `subject.Series.QualityProfile.Value` directly and would NPE on a
// missing profile; the Go port chooses the safer fallthrough.
//
// # Why Default priority + Permanent kind
//
// Mirrors C# `:16-17`. The score is a precomputed integer on the
// Subject (no DB hit on every evaluation) so Default priority is
// correct. Once below the floor, no future re-evaluation will lift
// the score — Permanent.
type CustomFormatAllowedByProfileSpecification struct {
	profileLookup CustomFormatAllowedByProfileProfileReader
	log           *slog.Logger
}

// NewCustomFormatAllowedByProfileSpecification builds the spec.
// log defaults to slog.Default when nil. profileLookup MUST be
// non-nil.
func NewCustomFormatAllowedByProfileSpecification(profileLookup CustomFormatAllowedByProfileProfileReader, log *slog.Logger) (*CustomFormatAllowedByProfileSpecification, error) {
	if profileLookup == nil {
		return nil, fmt.Errorf("decisionengine: CustomFormatAllowedByProfileSpecification requires a non-nil profileLookup")
	}
	if log == nil {
		log = slog.Default()
	}
	return &CustomFormatAllowedByProfileSpecification{profileLookup: profileLookup, log: log}, nil
}

// Name returns the canonical spec name.
func (*CustomFormatAllowedByProfileSpecification) Name() string {
	return CustomFormatAllowedByProfileSpecificationName
}

// Kind reports Permanent — once below the MinFormatScore floor,
// the score doesn't change on retry.
func (*CustomFormatAllowedByProfileSpecification) Kind() RejectionKind {
	return RejectionPermanent
}

// Priority returns Default — the score is a precomputed integer on
// the Subject, no DB hit per evaluation.
func (*CustomFormatAllowedByProfileSpecification) Priority() SpecPriority {
	return SpecPriorityDefault
}

// IsSatisfiedBy mirrors the C# method at `:19-31`.
func (s *CustomFormatAllowedByProfileSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}
	if subject.Series.QualityProfileID == nil {
		return Accept()
	}
	view, ok, err := s.profileLookup.LookupProfile(ctx, *subject.Series.QualityProfileID)
	if err != nil {
		s.log.Debug("customFormatAllowed: profile lookup failed; accepting (defensive)",
			"err", err, "seriesID", subject.Series.ID)
		return Accept()
	}
	if !ok || view == nil || view.Profile == nil {
		return Accept()
	}
	minScore := view.Profile.MinFormatScore
	if subject.CustomFormatScore < minScore {
		s.log.Debug("customFormatAllowed: score below minimum; rejecting",
			"score", subject.CustomFormatScore, "min", minScore,
			"seriesID", subject.Series.ID)
		return Reject(ReasonCustomFormatMinimumScore,
			fmt.Sprintf("Custom Formats have score %d below Series profile minimum %d",
				subject.CustomFormatScore, minScore))
	}
	return Accept()
}
