package decisionengine

import (
	"context"
	"log/slog"
	"time"
)

// FullSeasonSpecificationName is the canonical Spec.Name() identifier.
const FullSeasonSpecificationName = "FullSeason"

// fullSeasonAirGracePeriod is the look-ahead window after the latest
// episode's air-date during which a "full season" release is still
// considered too early. Mirrors the C# `DateTime.UtcNow.AddHours(24)`
// at `FullSeasonSpecification.cs:27` — episodes airing in the next
// 24 hours haven't aired yet.
const fullSeasonAirGracePeriod = 24 * time.Hour

// FullSeasonSpecification rejects FullSeason releases whose member
// episodes haven't all aired (or air within the next 24h). Permanent
// rejection — once a future episode lands, the season pack will be
// re-evaluated under a fresh Subject.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/FullSeasonSpecification.cs:1-37`.
//
// The C# tree marks this as Permanent because the Subject is created
// fresh per release-evaluation — a future episode landing produces a
// new Subject.Episodes list with updated AirDateUtc values; there's
// no in-place re-test of the same Subject.
type FullSeasonSpecification struct {
	now func() time.Time
	log *slog.Logger
}

// NewFullSeasonSpecification builds the spec. log defaults to slog.Default
// when nil. The clock parameter is optional — pass nil to default to
// time.Now (production); tests pass a fixed clock.
func NewFullSeasonSpecification(now func() time.Time, log *slog.Logger) *FullSeasonSpecification {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &FullSeasonSpecification{now: now, log: log}
}

// Name returns the canonical spec name.
func (*FullSeasonSpecification) Name() string { return FullSeasonSpecificationName }

// Kind reports Permanent.
func (*FullSeasonSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default.
func (*FullSeasonSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:21-35`. No-op when the
// release isn't a full-season pack; rejects when any member episode
// has no AirDateUtc OR its AirDateUtc is more than 24h in the future.
func (s *FullSeasonSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if !subject.ParsedEpisodeInfo.FullSeason {
		return Accept()
	}

	cutoff := s.now().UTC().Add(fullSeasonAirGracePeriod)
	for _, e := range subject.Episodes {
		if e.AirDateUtc == nil || e.AirDateUtc.After(cutoff) {
			s.log.Debug("fullSeason: rejecting unaired full-season pack",
				"epAirDate", e.AirDateUtc, "cutoff", cutoff)
			return Reject(ReasonFullSeasonNotAired,
				"Full season release rejected. All episodes haven't aired yet.")
		}
	}
	return Accept()
}
