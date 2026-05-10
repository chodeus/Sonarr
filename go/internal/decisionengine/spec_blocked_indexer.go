package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/providerstatus"
)

// BlockedIndexerSpecificationName is the canonical Spec.Name() identifier.
const BlockedIndexerSpecificationName = "BlockedIndexer"

// blockedIndexerCacheTTL mirrors the C# 15-second cache TTL at
// `BlockedIndexerSpecification.cs:23` —
// `cacheManager.GetCacheDictionary(..., TimeSpan.FromSeconds(15))`.
//
// 15 seconds is short enough that a recently-recovered indexer
// re-enters the eligibility set within one decision-engine sweep,
// while still amortising the BlockedView read across the typical
// per-sweep volume of release candidates (dozens to thousands).
const blockedIndexerCacheTTL = 15 * time.Second

// BlockedIndexerSpecification rejects releases produced by an indexer
// currently inside its blocked-till window. C# cite:
// src/NzbDrone.Core/DecisionEngine/Specifications/BlockedIndexerSpecification.cs:11-44.
//
// Rejection is Temporary — the indexer's cooldown is time-bounded and
// the candidate may re-emerge from the pending-releases retry loop
// once the cooldown expires.
//
// Priority is Database since the underlying read hits the
// IndexerStatus persistence layer; the 15-second cache (mirrors the C#
// ICachedDictionary at :23) absorbs hot-loop pressure during a single
// decision-engine sweep.
type BlockedIndexerSpecification struct {
	blocked providerstatus.BlockedView
	log     *slog.Logger

	// cache state — guarded by mu.
	mu        sync.Mutex
	cachedSet map[int]time.Time // providerID → DisabledTill (zero when nil in source)
	cachedAt  time.Time
	clock     func() time.Time
}

// NewBlockedIndexerSpecification builds the spec. blocked MUST be
// non-nil — there's no useful default for the blocked-indexer lookup.
// log defaults to slog.Default when nil.
//
// The clock is fixed to time.Now in production; the unexported
// `withClock` test helper swaps it for deterministic TTL tests.
func NewBlockedIndexerSpecification(blocked providerstatus.BlockedView, log *slog.Logger) (*BlockedIndexerSpecification, error) {
	if blocked == nil {
		return nil, fmt.Errorf("decisionengine: BlockedIndexerSpecification requires a non-nil BlockedView")
	}
	if log == nil {
		log = slog.Default()
	}
	return &BlockedIndexerSpecification{
		blocked: blocked,
		log:     log,
		clock:   time.Now,
	}, nil
}

// Name returns the canonical spec name.
func (*BlockedIndexerSpecification) Name() string { return BlockedIndexerSpecificationName }

// Kind reports Temporary — the indexer's cooldown will eventually
// expire and the candidate's pending-release retry will re-evaluate.
func (*BlockedIndexerSpecification) Kind() RejectionKind { return RejectionTemporary }

// Priority returns Database — the underlying check hits persistence
// (cache mitigates the per-call cost).
func (*BlockedIndexerSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:29-38`. Looks up the
// candidate's IndexerId in the cached blocked-indexer dict; rejects
// (Temporary) when present, accepts otherwise.
//
// Defensive paths:
//   - nil/empty subject → accept (orchestrator pre-filters but defensive).
//   - Release with no resolvable IndexerId (zero) → accept (the C# tree
//     stringifies the zero ID and looks it up; the dict won't contain
//     it because Recorder rejects ProviderID=0 writes — same outcome).
//   - BlockedView read error → log + accept (defensive: a transient
//     DB blip shouldn't block every release at evaluation time; the
//     orchestrator's next sweep retries the lookup).
func (s *BlockedIndexerSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Release == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil || rel.IndexerID == 0 {
		return Accept()
	}

	set, err := s.blockedSet(ctx)
	if err != nil {
		s.log.Debug("blockedindexer: lookup failed; accepting (defensive)",
			"err", err, "indexerId", rel.IndexerID, "title", rel.Title)
		return Accept()
	}

	till, blocked := set[rel.IndexerID]
	if !blocked {
		return Accept()
	}

	tillStr := "unknown"
	if !till.IsZero() {
		tillStr = till.Format(time.RFC3339)
	}
	msg := fmt.Sprintf("Indexer %s is blocked till %s due to failures, cannot grab release.",
		rel.Indexer, tillStr)
	return RejectTemporary(ReasonIndexerDisabled, msg)
}

// blockedSet returns the cached blocked-indexer set, refreshing from
// BlockedView when the cache is stale. Mirrors the C#
// ICachedDictionary's lazy-refresh semantics — a get under expired
// TTL re-fetches before serving.
func (s *BlockedIndexerSpecification) blockedSet(ctx context.Context) (map[int]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if !s.cachedAt.IsZero() && now.Sub(s.cachedAt) < blockedIndexerCacheTTL {
		return s.cachedSet, nil
	}
	rows, err := s.blocked.GetBlocked(ctx)
	if err != nil {
		// Surface the error but keep the prior cached set in place —
		// stale data beats no data while the source recovers.
		return nil, err
	}
	set := make(map[int]time.Time, len(rows))
	for _, r := range rows {
		var till time.Time
		if r.DisabledTill != nil {
			till = *r.DisabledTill
		}
		set[r.ProviderID] = till
	}
	s.cachedSet = set
	s.cachedAt = now
	return set, nil
}

// withClock is a test-only hook to pin the spec's clock so TTL
// behaviour can be exercised deterministically.
//
//nolint:unused // exercised via spec_blocked_indexer_test.go
func (s *BlockedIndexerSpecification) withClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = clock
}

// Compile-time guard.
var _ Specification = (*BlockedIndexerSpecification)(nil)
