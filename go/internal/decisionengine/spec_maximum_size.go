package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
)

// MaximumSizeSpecificationName is the canonical Spec.Name() identifier.
// Mirrors the C# class name minus the `Specification` suffix.
const MaximumSizeSpecificationName = "MaximumSize"

// MaximumSizeReader is the minimal config dependency
// MaximumSizeSpecification needs — exposes the user's global
// "maximum size" floor in megabytes.
//
// Per the convention validated across Phase 3, each spec gets a
// small focused interface with one method, NOT a god-interface that
// grows as more specs land. Production wires a single ConfigService
// adapter that satisfies every per-spec reader; tests inject a tiny
// stub.
//
// A return value of 0 disables the check (the C# semantics).
type MaximumSizeReader interface {
	// MaximumSize returns the user-configured maximum release size
	// in MEGABYTES. Mirrors `IConfigService.MaximumSize` at
	// `src/NzbDrone.Core/Configuration/IConfigService.cs:58` and the
	// concrete getter at `ConfigService.cs:119-123` which defaults to
	// 0 when unset.
	MaximumSize() int
}

// MaximumSizeSpecification rejects releases whose size exceeds the
// user's configured global maximum. Permanent rejection — the size
// won't shrink on retry.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/MaximumSizeSpecification.cs:1-52`.
//
// Three pass-through cases:
//
//  1. MaximumSize() == 0 → user disabled the check; accept everything.
//  2. Release.Size == 0  → unknown size; accept (the C# tree treats
//     unknown as "out of scope" rather than as a failed check).
//  3. Release.Size <= MaximumSize → accept.
//
// Otherwise reject with [ReasonMaximumSizeExceeded] and a message
// formatted like the C# version ("X MB is too big, maximum size is
// Y MB (Settings->Indexers->Maximum Size)").
type MaximumSizeSpecification struct {
	cfg MaximumSizeReader
	log *slog.Logger
}

// NewMaximumSizeSpecification builds the spec. log defaults to
// slog.Default when nil. cfg MUST be non-nil — there's no useful
// default for the user's max-size config.
func NewMaximumSizeSpecification(cfg MaximumSizeReader, log *slog.Logger) (*MaximumSizeSpecification, error) {
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: MaximumSizeSpecification requires a non-nil MaximumSizeReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &MaximumSizeSpecification{cfg: cfg, log: log}, nil
}

// Name returns the canonical spec name.
func (*MaximumSizeSpecification) Name() string { return MaximumSizeSpecificationName }

// Kind reports Permanent — once-too-big stays too-big on retry.
func (*MaximumSizeSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — pure config + size compare, no I/O.
func (*MaximumSizeSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:22-50`. See the type
// doc-comment for the three pass-through cases.
func (s *MaximumSizeSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	maxMB := s.cfg.MaximumSize()
	if maxMB <= 0 {
		s.log.Debug("maximumSize: not configured; accepting", "title", rel.Title)
		return Accept()
	}

	if rel.Size == 0 {
		s.log.Debug("maximumSize: release size unknown; accepting", "title", rel.Title)
		return Accept()
	}

	maxBytes := int64(maxMB) * 1024 * 1024
	if rel.Size > maxBytes {
		msg := fmt.Sprintf("%s is too big, maximum size is %s (Settings->Indexers->Maximum Size)",
			formatSize(rel.Size), formatSize(maxBytes))
		s.log.Debug("maximumSize: rejecting oversize release",
			"title", rel.Title, "size", rel.Size, "max", maxBytes)
		return Reject(ReasonMaximumSizeExceeded, msg)
	}

	return Accept()
}

// formatSize renders a byte count in the C# `SizeSuffix()`-equivalent
// shape used in user-facing rejection messages. Returns the largest
// applicable IEC unit (B / KB / MB / GB / TB) with one fractional
// digit. Negative inputs treated as zero.
//
// Mirrors `Common/Extensions/Int64Extensions.SizeSuffix` semantics
// closely enough that the message text humans see in the React UI is
// the same as the C# build's; not a verbatim port (the C# version
// has a digits-of-precision parameter the spec doesn't pass).
func formatSize(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	suffixes := []string{"KB", "MB", "GB", "TB", "PB"}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit && exp < len(suffixes)-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), suffixes[exp])
}
