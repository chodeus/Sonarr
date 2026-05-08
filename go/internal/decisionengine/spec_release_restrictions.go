package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/releaseprofiles"
)

// ReleaseRestrictionsSpecificationName is the canonical Spec.Name() identifier.
const ReleaseRestrictionsSpecificationName = "ReleaseRestrictions"

// ReleaseProfileFinder narrows
// [*releaseprofiles.Service.EnabledForTags] (one method) — the
// per-Series + per-Indexer release-profile lookup. Production
// binding: [*releaseprofiles.Service.EnabledForTags] satisfies via
// Go's structural typing.
//
// Defined here (rather than in releaseprofiles) so the spec stays
// stub-able in tests without constructing the full Service.
type ReleaseProfileFinder interface {
	EnabledForTags(ctx context.Context, tagIDs []int, indexerID int) ([]releaseprofiles.Profile, error)
}

// TermMatcher narrows [*releaseprofiles.MatcherService.IsMatch] (one
// method) — the cached Required/Ignored term-vs-title matcher.
// Production binding: [*releaseprofiles.MatcherService.IsMatch]
// satisfies via Go's structural typing.
//
// Used by both ReleaseRestrictionsSpec and any future spec that
// matches release-profile terms against a release title (e.g.
// some flavours of CustomFormat). Shared interface lives next to the
// first spec that uses it.
type TermMatcher interface {
	IsMatch(term, value string) bool
}

// ReleaseRestrictionsSpecification rejects releases that fail the
// per-tag Required (must-contain) and Ignored (must-not-contain)
// term-list checks defined by enabled release profiles.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/ReleaseRestrictionsSpecification.cs:1-66`.
//
// # Algorithm
//
//  1. Lookup all release profiles enabled for the series' tags +
//     the release's indexer.
//  2. For every profile with Required terms: if NONE of them match
//     the release title, reject [ReasonMustContainMissing] with the
//     full required list in the message.
//  3. For every profile with Ignored terms: if ANY of them match
//     the release title, reject [ReasonMustNotContainPresent] with
//     the matched-terms list.
//  4. Otherwise accept.
//
// Permanent rejection — release-profile term lists don't flip
// without a profile edit which produces a fresh Subject.
//
// # Defensive Accept arms
//
// Profile-finder errors / nil-Series / nil-Release Accept
// defensively. A transient releaseprofiles DB outage shouldn't
// reject every candidate in a batch (matches the surrounding
// "transient DB outage" tolerance from slice 3.A4's RepackSpec).
type ReleaseRestrictionsSpecification struct {
	profiles ReleaseProfileFinder
	matcher  TermMatcher
	log      *slog.Logger
}

// NewReleaseRestrictionsSpecification builds the spec. log defaults
// to [slog.Default] when nil. profiles / matcher MUST be non-nil.
func NewReleaseRestrictionsSpecification(profiles ReleaseProfileFinder, matcher TermMatcher, log *slog.Logger) (*ReleaseRestrictionsSpecification, error) {
	if profiles == nil {
		return nil, fmt.Errorf("decisionengine: ReleaseRestrictionsSpecification requires a non-nil ReleaseProfileFinder")
	}
	if matcher == nil {
		return nil, fmt.Errorf("decisionengine: ReleaseRestrictionsSpecification requires a non-nil TermMatcher")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ReleaseRestrictionsSpecification{profiles: profiles, matcher: matcher, log: log}, nil
}

// Name returns the canonical spec name.
func (*ReleaseRestrictionsSpecification) Name() string { return ReleaseRestrictionsSpecificationName }

// Kind reports Permanent.
func (*ReleaseRestrictionsSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default.
func (*ReleaseRestrictionsSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:27-65`.
func (s *ReleaseRestrictionsSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	profiles, err := s.profiles.EnabledForTags(ctx, subject.Series.Tags, rel.IndexerID)
	if err != nil {
		s.log.Warn("releaseRestrictions: profile lookup failed; accepting defensively",
			"seriesId", subject.Series.ID, "indexerId", rel.IndexerID, "err", err)
		return Accept()
	}

	title := rel.Title

	// Required pass: every profile with non-empty Required must
	// match at least one of its required terms against the title.
	for _, p := range profiles {
		if len(p.Required) == 0 {
			continue
		}
		found := s.containsAny(p.Required, title)
		if len(found) == 0 {
			terms := strings.Join(p.Required, ", ")
			s.log.Debug("releaseRestrictions: missing required terms",
				"title", title, "terms", terms)
			return Reject(ReasonMustContainMissing,
				fmt.Sprintf("Does not contain one of the required terms: %s", terms))
		}
	}

	// Ignored pass: every profile's Ignored term-list must produce
	// no matches against the title.
	for _, p := range profiles {
		if len(p.Ignored) == 0 {
			continue
		}
		found := s.containsAny(p.Ignored, title)
		if len(found) > 0 {
			terms := strings.Join(found, ", ")
			s.log.Debug("releaseRestrictions: contains ignored terms",
				"title", title, "terms", terms)
			return Reject(ReasonMustNotContainPresent,
				fmt.Sprintf("Contains these ignored terms: %s", terms))
		}
	}

	return Accept()
}

// containsAny returns the subset of `terms` that match `title` per
// the configured TermMatcher (which handles plain case-insensitive
// AND Perl-regex syntax — see [releaseprofiles.MatcherFor]).
// Mirrors the C# private helper at `:60-65`.
func (s *ReleaseRestrictionsSpecification) containsAny(terms []string, title string) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if s.matcher.IsMatch(t, title) {
			out = append(out, t)
		}
	}
	return out
}

// Compile-time interface satisfaction.
var _ Specification = (*ReleaseRestrictionsSpecification)(nil)
