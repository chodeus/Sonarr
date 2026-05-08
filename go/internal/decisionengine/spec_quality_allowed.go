package decisionengine

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// QualityAllowedByProfileSpecificationName is the canonical Spec.Name() identifier.
const QualityAllowedByProfileSpecificationName = "QualityAllowedByProfile"

// QualityAllowedByProfileProfileReader is the narrow profile-resolver
// dependency for [QualityAllowedByProfileSpecification]. Same shape as
// the slice-3.65 [AcceptableSizeProfileReader] but defined separately
// so the compile-time interface satisfaction stays scoped to one spec
// (Go's structural typing lets the same backing implementation satisfy
// both).
//
// Returns the parsed [*ProfileView] for a given `qualityProfileID`,
// `ok=false` when the profile doesn't exist, or `(nil, false, err)`
// when the lookup fails outright.
type QualityAllowedByProfileProfileReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// QualityAllowedByProfileSpecification rejects releases whose parsed
// quality is not flagged Allowed in the series' QualityProfile.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/QualityAllowedByProfileSpecification.cs:1-31`.
//
// # Algorithm
//
//  1. Resolve the series' QualityProfile.
//  2. Resolve the QualityItem (or grouped QualityItem) at the profile
//     index for the parsed quality.
//  3. Reject when `Item.Allowed == false`.
//
// # C# quirk preserved
//
// The C# `QualityProfile.GetIndex(quality)` returns a zero
// [qualityprofiles.QualityIndex] (Index=0) when the requested quality
// isn't in the profile — i.e. unknown qualities collapse to the first
// item's Allowed bit rather than getting a clean miss signal. The Go
// port preserves the quirk through the `if !ok { idx = QualityIndex{} }`
// coercion below — same observable behaviour as the C# tree, including
// the "first item Allowed=true → accept an unknown quality" edge case.
// Documented here so a future reviewer can retire the quirk if Sonarr
// ever fixes it C#-side.
//
// # Defensive Accept arms
//
// Profile-resolver errors / nil-Series / nil-ParsedEpisodeInfo /
// no-QualityProfileID-on-Series / empty-Items profile all Accept
// defensively. Same posture as
// [internal/episodeimport/specs.go:1086-1090] (UpgradeSpecification's
// "transient DB outage" tolerance) — a quality-profile DB hiccup
// shouldn't reject every candidate in a batch when the right answer is
// "we don't know yet".
//
// Permanent rejection when an actual disallowed quality lands.
type QualityAllowedByProfileSpecification struct {
	profiles QualityAllowedByProfileProfileReader
	log      *slog.Logger
}

// NewQualityAllowedByProfileSpecification builds the spec. log
// defaults to [slog.Default] when nil. profiles MUST be non-nil —
// there's no useful default for the quality-profile lookup.
func NewQualityAllowedByProfileSpecification(profiles QualityAllowedByProfileProfileReader, log *slog.Logger) (*QualityAllowedByProfileSpecification, error) {
	if profiles == nil {
		return nil, fmt.Errorf("decisionengine: QualityAllowedByProfileSpecification requires a non-nil QualityAllowedByProfileProfileReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &QualityAllowedByProfileSpecification{profiles: profiles, log: log}, nil
}

// Name returns the canonical spec name.
func (*QualityAllowedByProfileSpecification) Name() string {
	return QualityAllowedByProfileSpecificationName
}

// Kind reports Permanent — a quality the profile rejects today won't
// flip to Allowed without a profile edit, which produces a fresh
// Subject anyway.
func (*QualityAllowedByProfileSpecification) Kind() RejectionKind {
	return RejectionPermanent
}

// Priority returns Default.
func (*QualityAllowedByProfileSpecification) Priority() SpecPriority {
	return SpecPriorityDefault
}

// IsSatisfiedBy mirrors the C# method at
// `QualityAllowedByProfileSpecification.cs:18-30`.
func (s *QualityAllowedByProfileSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if subject.Series.QualityProfileID == nil {
		s.log.Debug("qualityAllowed: series has no QualityProfileID; accepting defensively",
			"seriesId", subject.Series.ID)
		return Accept()
	}

	view, ok, err := s.profiles.LookupProfile(ctx, *subject.Series.QualityProfileID)
	if err != nil {
		s.log.Warn("qualityAllowed: profile lookup failed; accepting defensively",
			"seriesId", subject.Series.ID, "profileId", *subject.Series.QualityProfileID, "err", err)
		return Accept()
	}
	if !ok || view == nil || len(view.Items) == 0 {
		s.log.Debug("qualityAllowed: profile missing or empty; accepting defensively",
			"seriesId", subject.Series.ID, "profileId", *subject.Series.QualityProfileID)
		return Accept()
	}

	q := subject.ParsedEpisodeInfo.Quality.Quality
	// C# quirk: GetIndex(quality) returns a zero QualityIndex (Index=0)
	// when the quality isn't in the profile. Preserve verbatim.
	idx, found := qualityprofiles.LookupIndex(view.Items, q, false)
	if !found {
		idx = qualityprofiles.QualityIndex{}
	}
	if idx.Index < 0 || idx.Index >= len(view.Items) {
		// Defensive — LookupIndex shouldn't return an out-of-range
		// Index, but the bounds check keeps a future contract change
		// from panicking here.
		s.log.Warn("qualityAllowed: index out of range; accepting defensively",
			"seriesId", subject.Series.ID, "index", idx.Index, "items", len(view.Items))
		return Accept()
	}

	item := view.Items[idx.Index]
	if !item.Allowed {
		s.log.Debug("qualityAllowed: quality rejected by profile",
			"seriesId", subject.Series.ID, "quality", q.Name, "profileId", *subject.Series.QualityProfileID)
		return Reject(ReasonQualityNotWanted,
			fmt.Sprintf("%s is not wanted in profile", q.Name))
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*QualityAllowedByProfileSpecification)(nil)
