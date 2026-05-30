package decisionengine

import (
	"context"
	"log/slog"
	"strings"
)

// SceneMappingSpecificationName is the canonical Spec.Name() identifier.
const SceneMappingSpecificationName = "SceneMapping"

// SceneMappingSpecification TEMPORARILY rejects a release whose matched
// scene mapping declares an explicitly "mixed" scene origin — i.e. the
// release was numbered with multiple unidentifiable schemes, so Sonarr
// can't trust the parsed episode numbers. Temporary because a future
// TheXEM mapping update may disambiguate it.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/SceneMappingSpecification.cs:1-78`.
// The C# spec takes ONLY a logger — it reads `remoteEpisode.SceneMapping`
// (the Subject's already-resolved mapping, populated by the parsing
// service), NOT the scenemappings.Service. (The long-standing
// decisionengine_wiring "BLOCKED on scenemappings.Service signature"
// note was stale — this spec has no service dependency.)
//
// # Algorithm (C# `:21-76`)
//
//  1. No scene mapping → Accept.
//  2. Empty/blank SceneOrigin → Accept.
//  3. SceneOrigin split on ':'; first segment == "mixed" → Reject
//     TEMPORARY [ReasonAmbiguousNumbering]. With a non-blank Comment the
//     C# message is the (latently un-formatted) "{0} has ambiguous
//     numbering"; otherwise "Ambiguous numbering".
//  4. "unknown" origin → log + Accept; any other origin → Accept.
type SceneMappingSpecification struct {
	log *slog.Logger
}

// NewSceneMappingSpecification builds the spec. Logger-only (no other
// deps), matching the C# constructor; log defaults to slog.Default.
// Returns the concrete pointer (no error) — same shape as the other
// dependency-free specs (MonitoredEpisode / MultiSeason / NotSample).
func NewSceneMappingSpecification(log *slog.Logger) *SceneMappingSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &SceneMappingSpecification{log: log}
}

// Name returns the canonical spec name.
func (*SceneMappingSpecification) Name() string { return SceneMappingSpecificationName }

// Kind reports Temporary — "till there's a mapping" (C# `:17`).
func (*SceneMappingSpecification) Kind() RejectionKind { return RejectionTemporary }

// Priority returns Default — pure in-memory read of the Subject's
// already-resolved scene mapping (C# `:16`).
func (*SceneMappingSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:21-76`.
func (s *SceneMappingSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.SceneMapping == nil {
		s.log.Debug("sceneMapping: no applicable scene mapping, skipping")
		return Accept()
	}
	if strings.TrimSpace(subject.SceneMapping.SceneOrigin) == "" {
		s.log.Debug("sceneMapping: no explicit scene origin in scene mapping")
		return Accept()
	}

	// C# splits the RAW (untrimmed) SceneOrigin on ':'; the blank-check
	// above already guarantees a non-empty first segment.
	split := strings.Split(subject.SceneMapping.SceneOrigin, ":")
	switch split[0] {
	case "mixed":
		s.log.Debug("sceneMapping: origin explicitly mixed (multiple unidentifiable numbering schemes)")
		if strings.TrimSpace(subject.SceneMapping.Comment) != "" {
			// Faithful to C# `:55`: the 2-arg `DownloadSpecDecision.Reject`
			// overload does NOT run string.Format, so the literal "{0}" is
			// emitted (a latent upstream bug — the series title was meant
			// to be interpolated). Reproduced verbatim for parity.
			return RejectTemporary(ReasonAmbiguousNumbering, "{0} has ambiguous numbering")
		}
		return RejectTemporary(ReasonAmbiguousNumbering, "Ambiguous numbering")
	case "unknown":
		// C# `:64-68`: log only; the numbering scheme is unknown but
		// "scene" (or the second segment) is assumed. Still Accept.
		assumed := "scene"
		if len(split) >= 2 {
			assumed = split[1]
		}
		s.log.Debug("sceneMapping: origin explicitly unknown; assuming numbering", "assumed", assumed)
	}

	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*SceneMappingSpecification)(nil)
