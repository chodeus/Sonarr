package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"sonarr.tv/sonarr/internal/releaseprofiles"
)

// AirDateSpecificationName is the canonical Spec.Name() identifier.
const AirDateSpecificationName = "AirDate"

// AirDateSpecification rejects releases published before the
// adjusted air date (per-episode AirDateUtc + the per-profile
// AirDateGracePeriod) when an enabled release profile flags
// AirDateRestriction=true.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/AirDateSpecification.cs:1-79`.
//
// # Algorithm
//
//  1. Lookup release profiles enabled for the series' tags +
//     release's indexer.
//  2. No profiles → accept.
//  3. Pick the "best" profile via the C# tie-break: prefer
//     AirDateRestriction=true; among those, prefer the LARGER
//     AirDateGracePeriod (so a 7-day grace beats a 1-day grace).
//  4. Best profile's AirDateRestriction=false → accept.
//  5. For each episode: AirDateUtc nil → reject [ReasonBeforeAirDate]
//     ("No air date available"). adjustedAirDate = AirDateUtc +
//     gracePeriod days; release.PublishDate < adjustedAirDate →
//     reject with the "before adjusted air date" message.
//  6. Otherwise accept.
//
// Permanent rejection — the per-episode AirDate / per-profile
// grace are stable values; doesn't flip without a profile or
// metadata edit which produces a fresh Subject.
//
// Defensive Accept on profile-finder error / nil-Series / nil-Release.
type AirDateSpecification struct {
	profiles ReleaseProfileFinder
	log      *slog.Logger
}

// NewAirDateSpecification builds the spec. log defaults to
// [slog.Default] when nil. profiles MUST be non-nil.
func NewAirDateSpecification(profiles ReleaseProfileFinder, log *slog.Logger) (*AirDateSpecification, error) {
	if profiles == nil {
		return nil, fmt.Errorf("decisionengine: AirDateSpecification requires a non-nil ReleaseProfileFinder")
	}
	if log == nil {
		log = slog.Default()
	}
	return &AirDateSpecification{profiles: profiles, log: log}, nil
}

// Name returns the canonical spec name.
func (*AirDateSpecification) Name() string { return AirDateSpecificationName }

// Kind reports Permanent.
func (*AirDateSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — needs the per-Series profile lookup.
func (*AirDateSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:28-69`.
func (s *AirDateSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	profiles, err := s.profiles.EnabledForTags(ctx, subject.Series.Tags, rel.IndexerID)
	if err != nil {
		s.log.Warn("airDate: profile lookup failed; accepting defensively",
			"seriesId", subject.Series.ID, "indexerId", rel.IndexerID, "err", err)
		return Accept()
	}
	if len(profiles) == 0 {
		s.log.Debug("airDate: no release profile, accepting")
		return Accept()
	}

	best := bestAirDateProfile(profiles)
	if !best.AirDateRestriction {
		s.log.Debug("airDate: profile does not restrict before-airdate, accepting")
		return Accept()
	}

	releaseDate := rel.PublishDate
	gracePeriod := best.AirDateGracePeriod

	for _, ep := range subject.Episodes {
		if ep.AirDateUtc == nil {
			s.log.Debug("airDate: no air date available, rejecting",
				"episodeId", ep.ID)
			return Reject(ReasonBeforeAirDate, "No air date available")
		}
		adjustedAirDate := ep.AirDateUtc.AddDate(0, 0, gracePeriod)
		if releaseDate.Before(adjustedAirDate) {
			msg := fmt.Sprintf("Release date %s is before adjusted air date of %s (Air Date: %s. Grace period %d days)",
				releaseDate.Format("2006-01-02"),
				adjustedAirDate.Format("2006-01-02"),
				ep.AirDateUtc.Format("2006-01-02"),
				gracePeriod)
			s.log.Debug("airDate: release before adjusted air date",
				"episodeId", ep.ID, "releaseDate", releaseDate, "adjusted", adjustedAirDate)
			return Reject(ReasonBeforeAirDate, msg)
		}
	}

	return Accept()
}

// bestAirDateProfile mirrors the C# tie-break at `:38-41`:
// `OrderByDescending(p => p.AirDateRestriction ? 1 : 0)
//
//	.ThenByDescending(p => p.AirDateGracePeriod)
//	.First()`.
//
// I.e. profiles with AirDateRestriction=true sort ahead of those
// without; among the AirDateRestriction set, the LARGER grace
// period wins.
func bestAirDateProfile(profiles []releaseprofiles.Profile) releaseprofiles.Profile {
	sorted := append([]releaseprofiles.Profile(nil), profiles...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].AirDateRestriction != sorted[j].AirDateRestriction {
			return sorted[i].AirDateRestriction
		}
		return sorted[i].AirDateGracePeriod > sorted[j].AirDateGracePeriod
	})
	return sorted[0]
}

// Compile-time interface satisfaction.
var _ Specification = (*AirDateSpecification)(nil)
