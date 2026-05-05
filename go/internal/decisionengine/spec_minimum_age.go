package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"sonarr.tv/sonarr/internal/indexers"
)

// MinimumAgeSpecificationName is the canonical Spec.Name() identifier.
// Mirrors the C# class name minus the `Specification` suffix.
const MinimumAgeSpecificationName = "MinimumAge"

// MinimumAgeReader is the minimal config dependency
// MinimumAgeSpecification needs — exposes the user's global "wait at
// least N minutes after publish before grabbing" floor.
//
// Like [MaximumSizeReader], the interface is deliberately tiny so
// each spec stays orthogonal. Production wires the single
// ConfigService adapter; tests inject a stub.
//
// A return value of 0 disables the check (the C# semantics).
type MinimumAgeReader interface {
	// MinimumAge returns the user-configured minimum release age in
	// MINUTES. Mirrors `IConfigService.MinimumAge` at
	// `src/NzbDrone.Core/Configuration/IConfigService.cs` and the
	// concrete getter in ConfigService.cs which defaults to 0 when
	// unset.
	MinimumAge() int
}

// MinimumAgeSpecification rejects Usenet releases younger than the
// user-configured minimum age. Temporary rejection — the release will
// be re-evaluated when its age crosses the threshold.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/MinimumAgeSpecification.cs:1-53`.
//
// Three pass-through cases:
//
//  1. Release.DownloadProtocol != Usenet → accept (the floor is a
//     usenet-retention heuristic; torrents are gated by seeders/peers
//     instead).
//  2. MinimumAge() == 0 → user disabled the check; accept.
//  3. ageMinutes >= MinimumAge() → accept.
//
// Otherwise reject with [ReasonMinimumAge] and a message text-matching
// the C# version ("Only X.X minutes old, minimum age is Y minutes").
type MinimumAgeSpecification struct {
	cfg MinimumAgeReader
	log *slog.Logger
}

// NewMinimumAgeSpecification builds the spec. log defaults to
// slog.Default when nil. cfg MUST be non-nil — there's no useful
// default for the user's minimum-age config.
func NewMinimumAgeSpecification(cfg MinimumAgeReader, log *slog.Logger) (*MinimumAgeSpecification, error) {
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: MinimumAgeSpecification requires a non-nil MinimumAgeReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &MinimumAgeSpecification{cfg: cfg, log: log}, nil
}

// Name returns the canonical spec name.
func (*MinimumAgeSpecification) Name() string { return MinimumAgeSpecificationName }

// Kind reports Temporary — a too-young release becomes acceptable
// once the clock advances past the threshold.
func (*MinimumAgeSpecification) Kind() RejectionKind { return RejectionTemporary }

// Priority returns Default — pure config + clock compare, no I/O.
func (*MinimumAgeSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:22-51`. See the type
// doc-comment for the three pass-through cases.
func (s *MinimumAgeSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	if rel.DownloadProtocol != indexers.DownloadProtocolUsenet {
		s.log.Debug("minimumAge: skipping non-usenet release", "title", rel.Title)
		return Accept()
	}

	minMinutes := s.cfg.MinimumAge()
	if minMinutes <= 0 {
		s.log.Debug("minimumAge: not configured; accepting", "title", rel.Title)
		return Accept()
	}

	age := rel.AgeMinutes()
	ageRounded := math.Round(age*10) / 10
	if age < float64(minMinutes) {
		msg := fmt.Sprintf("Only %s minutes old, minimum age is %d minutes",
			formatRounded(ageRounded), minMinutes)
		s.log.Debug("minimumAge: rejecting too-young release",
			"title", rel.Title, "ageMinutes", ageRounded, "minMinutes", minMinutes)
		return RejectTemporary(ReasonMinimumAge, msg)
	}

	return Accept()
}

// formatRounded renders a float that has already been rounded to one
// decimal place. Strips a trailing ".0" so whole-minute values render
// as plain integers in user-facing messages, matching the C#
// `Math.Round(age, 1)` formatted-via-string-interpolation shape.
func formatRounded(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}
