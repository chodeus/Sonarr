package decisionengine

import (
	"context"
	"log/slog"
)

// PendingSpecificationName is the canonical Spec.Name() identifier.
const PendingSpecificationName = "Pending"

// PendingSpecification TEMPORARILY rejects a PUSHED (or non-user
// search) release when a release for at least one of the same episodes
// is already sitting in the pending-release queue — so a manually
// pushed release doesn't jump ahead of one already waiting out a delay.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/PendingSpecification.cs:8-53`.
//
// # Algorithm (C# `:25-52`)
//
//  1. ReleaseSource == Rss → Accept (RSS sync never blocks on pending;
//     the DelaySpecification already governs RSS waits).
//  2. User-invoked search → Accept.
//  3. Any pending release for this series whose episodes intersect the
//     subject's → Reject TEMPORARY [ReasonMinimumAgeDelayPushed].
//
// Reuses [PendingReleaseAgeReader]: its `found` return is exactly "is
// there a matching pending release for these episodes" (the age value
// is unused here — only DelaySpecification reads it). The Go match keys
// on (season, episode-numbers) rather than the C# episode-ids, the same
// documented divergence as pendingreleases.Service.OldestPending.
type PendingSpecification struct {
	pending PendingReleaseAgeReader
	log     *slog.Logger
}

// NewPendingSpecification builds the spec. pending MAY be nil — when
// nil the spec is inert (always Accepts), matching the
// optional-dependency posture of the rest of the pending subsystem (a
// queue/status-only boot has no full pending service). Production wires
// the real service. log defaults to slog.Default.
func NewPendingSpecification(pending PendingReleaseAgeReader, log *slog.Logger) (*PendingSpecification, error) {
	if log == nil {
		log = slog.Default()
	}
	return &PendingSpecification{pending: pending, log: log}, nil
}

// Name returns the canonical spec name.
func (*PendingSpecification) Name() string { return PendingSpecificationName }

// Kind reports Temporary — the pushed release becomes grab-eligible
// once the pending release clears (C# `Type => RejectionType.Temporary`,
// `:21`).
func (*PendingSpecification) Kind() RejectionKind { return RejectionTemporary }

// Priority returns Database — reads the pending-release queue (C#
// `Priority => SpecificationPriority.Database`, `:20`).
func (*PendingSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:25-52`.
func (s *PendingSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}

	// C# `:27-30` — RSS sync never blocks on pending.
	if subject.ReleaseSource == ReleaseSourceRSS {
		return Accept()
	}

	// C# `:32-36` — user-invoked search ignores the pending guard.
	if c, ok := info.SearchCriteria.(*SearchCriteria); ok && c != nil && c.UserInvokedSearch {
		return Accept()
	}

	if s.pending == nil {
		return Accept()
	}

	epNums := episodeNumbersForDelay(subject)
	if len(epNums) == 0 {
		return Accept()
	}

	_, found, err := s.pending.OldestPendingAgeMinutes(ctx, subject.Series.ID, subject.MappedSeasonNumber, epNums)
	if err != nil {
		// Don't block a grab on a pending-queue read failure — degrade open.
		s.log.Warn("pending: pending-queue lookup failed; accepting", "err", err)
		return Accept()
	}
	if found {
		s.log.Debug("pending: a matching release is already pending; delaying pushed release")
		return RejectTemporary(ReasonMinimumAgeDelayPushed,
			"Release containing at least one matching episode is already pending, delaying pushed release")
	}

	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*PendingSpecification)(nil)
