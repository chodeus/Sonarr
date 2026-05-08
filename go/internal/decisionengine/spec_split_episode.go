package decisionengine

import (
	"context"
	"log/slog"
)

// SplitEpisodeSpecificationName is the canonical Spec.Name() identifier.
const SplitEpisodeSpecificationName = "SplitEpisode"

// SplitEpisodeSpecification rejects "split-episode" releases — the
// rare case where a single broadcast is split across two release
// numbers (e.g. `S04E01.5`/`S04E02` style — most common in anime
// and certain reality-TV cuts). Sonarr can't handle these because
// the persisted episode rows assume a 1:1 release-to-episode-number
// mapping; an E01.5 file would either overwrite E02 or get filed
// under a synthetic episode number that the renamer can't represent.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/SplitEpisodeSpecification.cs:1-25`.
// Zero deps beyond the log; the C# IoC graph injects only the
// logger.
//
// Permanent rejection — the parser's IsSplitEpisode bit is a
// structural property of the release name, not a lookup that might
// flip on a re-evaluation.
type SplitEpisodeSpecification struct {
	log *slog.Logger
}

// NewSplitEpisodeSpecification builds the spec. log defaults to
// [slog.Default] when nil — matches the existing zero-dep spec
// constructor convention from slice 3.86.
func NewSplitEpisodeSpecification(log *slog.Logger) *SplitEpisodeSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &SplitEpisodeSpecification{log: log}
}

// Name returns the canonical spec name.
func (*SplitEpisodeSpecification) Name() string { return SplitEpisodeSpecificationName }

// Kind reports Permanent.
func (*SplitEpisodeSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — cheap evaluation, no DB or disk reads.
func (*SplitEpisodeSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors `SplitEpisodeSpecification.cs:18-23`. Rejects
// when the parser flagged the release as a split episode; otherwise
// Accept.
//
// `ParsedEpisodeInfo.IsSplitEpisode` is set by [parser.ParseTitle]
// when the title matches the C# `SplitEpisodeRegex` (e.g.
// `S04E01.5` / `S04E01-02` boundary cases). The Go parser ports
// the flag verbatim from the C# regex match.
//
// nil-safe: a nil Subject or nil ParsedEpisodeInfo Accept defensively
// (matches the surrounding zero-dep spec posture; the C# reference
// would NRE because RemoteEpisode + ParsedEpisodeInfo are
// constructor-injected non-null).
func (s *SplitEpisodeSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if subject.ParsedEpisodeInfo.IsSplitEpisode {
		s.log.Debug("splitEpisode: rejecting split-episode release",
			"title", subject.ParsedEpisodeInfo.ReleaseTitle)
		return Reject(ReasonSplitEpisode,
			"Split episode releases are not supported")
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*SplitEpisodeSpecification)(nil)
