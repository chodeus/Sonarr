package decisionengine

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/indexers"
)

// RetentionSpecificationName is the canonical Spec.Name() identifier.
const RetentionSpecificationName = "Retention"

// RetentionConfigReader is the narrow config dependency for
// [RetentionSpecification]. The single getter ports
// `IConfigService.Retention` (default 0 — feature disabled).
//
// Production binding: [*config.Provider].Retention satisfies this
// interface via Go's structural typing.
type RetentionConfigReader interface {
	Retention() int
}

// RetentionSpecification rejects usenet releases older than the
// user-configured retention window.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RetentionSpecification.cs:1-43`.
//
// # Algorithm
//
//  1. Release.DownloadProtocol != Usenet → accept (the floor is a
//     usenet-only retention heuristic; torrents are gated by
//     seeders/peers in [TorrentSeedingSpecification]).
//  2. Retention() == 0 → user disabled the check; accept.
//  3. Release.Age <= Retention → accept.
//  4. Otherwise reject with [ReasonMaximumAge].
//
// Permanent rejection — once a usenet release crosses the retention
// floor it isn't going to come back. (The C# tree marks this
// Permanent at `:21`; the Go port preserves the type.)
type RetentionSpecification struct {
	cfg RetentionConfigReader
	log *slog.Logger
}

// NewRetentionSpecification builds the spec. log defaults to
// [slog.Default] when nil. cfg MUST be non-nil — there's no useful
// default for the user's retention config.
func NewRetentionSpecification(cfg RetentionConfigReader, log *slog.Logger) (*RetentionSpecification, error) {
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: RetentionSpecification requires a non-nil RetentionConfigReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RetentionSpecification{cfg: cfg, log: log}, nil
}

// Name returns the canonical spec name.
func (*RetentionSpecification) Name() string { return RetentionSpecificationName }

// Kind reports Permanent.
func (*RetentionSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — config + protocol compare, no I/O.
func (*RetentionSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:23-41`.
func (s *RetentionSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	if rel.DownloadProtocol != indexers.DownloadProtocolUsenet {
		s.log.Debug("retention: skipping non-usenet release", "title", rel.Title)
		return Accept()
	}

	retention := s.cfg.Retention()
	if retention <= 0 {
		s.log.Debug("retention: not configured; accepting", "title", rel.Title)
		return Accept()
	}

	age := rel.Age()
	if age > retention {
		s.log.Debug("retention: rejecting too-old usenet release",
			"title", rel.Title, "ageDays", age, "retentionDays", retention)
		return Reject(ReasonMaximumAge, "Older than configured retention")
	}

	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*RetentionSpecification)(nil)
