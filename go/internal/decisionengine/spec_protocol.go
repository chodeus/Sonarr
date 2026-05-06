package decisionengine

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/indexers"
)

// ProtocolSpecificationName is the canonical Spec.Name() identifier.
const ProtocolSpecificationName = "Protocol"

// DelayProfileResolver is the minimal dependency
// ProtocolSpecification needs — the "best delay profile for these
// series tags" lookup.
//
// Returns the highest-priority `delayprofiles.Profile` whose Tags
// intersect `tagIDs` (or has no tags — the default catch-all),
// `ok=false` when none match, or `(Profile{}, false, err)` on a
// lookup failure. Wraps the existing
// `delayprofiles.BestForTags` plus the underlying repo. Tests inject
// a stub.
type DelayProfileResolver interface {
	BestForTags(ctx context.Context, tagIDs []int) (delayprofiles.Profile, bool, error)
}

// ProtocolSpecification rejects releases whose download protocol is
// disabled by the matching DelayProfile. Permanent rejection — the
// user explicitly turned off Usenet (or Torrent) for this series'
// tag set; retrying won't change that.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/ProtocolSpecification.cs:1-43`.
type ProtocolSpecification struct {
	resolver DelayProfileResolver
	log      *slog.Logger
}

// NewProtocolSpecification builds the spec. log defaults to slog.Default
// when nil. resolver MUST be non-nil — there's no useful default for
// the delay-profile lookup.
func NewProtocolSpecification(resolver DelayProfileResolver, log *slog.Logger) (*ProtocolSpecification, error) {
	if resolver == nil {
		return nil, fmt.Errorf("decisionengine: ProtocolSpecification requires a non-nil DelayProfileResolver")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProtocolSpecification{resolver: resolver, log: log}, nil
}

// Name returns the canonical spec name.
func (*ProtocolSpecification) Name() string { return ProtocolSpecificationName }

// Kind reports Permanent.
func (*ProtocolSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — pure config + protocol compare, no I/O
// beyond the delay-profile lookup.
func (*ProtocolSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:23-40`. A resolver error
// or no-match falls back to Accept (defensive — a missing
// DelayProfile shouldn't fail every release at evaluation time; the
// C# tree throws `.First()` but the Go port surfaces ok=false).
func (s *ProtocolSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil || subject == nil || subject.Series == nil {
		return Accept()
	}

	profile, ok, err := s.resolver.BestForTags(ctx, subject.Series.Tags)
	if err != nil {
		s.log.Debug("protocol: delay-profile lookup failed; accepting (defensive)",
			"err", err, "title", rel.Title)
		return Accept()
	}
	if !ok {
		s.log.Debug("protocol: no matching delay profile; accepting", "title", rel.Title)
		return Accept()
	}

	switch rel.DownloadProtocol {
	case indexers.DownloadProtocolUsenet:
		if !profile.EnableUsenet {
			s.log.Debug("protocol: usenet disabled for series", "title", rel.Title)
			return Reject(ReasonProtocolDisabled, "Usenet is not enabled for this series")
		}
	case indexers.DownloadProtocolTorrent:
		if !profile.EnableTorrent {
			s.log.Debug("protocol: torrent disabled for series", "title", rel.Title)
			return Reject(ReasonProtocolDisabled, "Torrent is not enabled for this series")
		}
	}
	return Accept()
}
