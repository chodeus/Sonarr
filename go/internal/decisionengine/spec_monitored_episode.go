package decisionengine

import (
	"context"
	"log/slog"
)

// MonitoredEpisodeSpecificationName is the canonical Spec.Name() identifier.
const MonitoredEpisodeSpecificationName = "MonitoredEpisode"

// MonitoredEpisodeSpecification rejects releases for a series or
// episodes the user is not monitoring. It is the core "only download
// what I'm monitoring" gate — on RSS sync it stops unmonitored series /
// episodes from being grabbed.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/MonitoredEpisodeSpecification.cs:1-66`.
//
// # Algorithm (C# `:21-64`)
//
//  1. SearchCriteria present AND MonitoredEpisodesOnly == false → Accept
//     (an interactive/forced search that explicitly includes unmonitored
//     episodes skips this gate). On RSS sync SearchCriteria is nil, so
//     the gate always applies.
//  2. Series not monitored → Reject [ReasonSeriesNotMonitored].
//  3. All matched episodes monitored → Accept.
//  4. Single-episode release, that episode unmonitored → Reject
//     [ReasonEpisodeNotMonitored] ("Episode is not monitored").
//  5. Multi-episode release with at least one unmonitored episode →
//     Reject [ReasonEpisodeNotMonitored] ("One or more episodes is not
//     monitored").
//
// Permanent rejection — Series/Episode monitored flags are stable; they
// don't flip without a user edit which produces a fresh Subject.
//
// Defensive Accept on nil Subject / nil Series (a release with no
// matched series is dropped earlier by the decision maker's
// unknown-series handling, not here).
type MonitoredEpisodeSpecification struct {
	log *slog.Logger
}

// NewMonitoredEpisodeSpecification builds the spec. log defaults to
// [slog.Default] when nil.
func NewMonitoredEpisodeSpecification(log *slog.Logger) *MonitoredEpisodeSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &MonitoredEpisodeSpecification{log: log}
}

// Name returns the canonical spec name.
func (*MonitoredEpisodeSpecification) Name() string { return MonitoredEpisodeSpecificationName }

// Kind reports Permanent.
func (*MonitoredEpisodeSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — pure in-memory flag checks, no I/O.
func (*MonitoredEpisodeSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:21-64`.
func (s *MonitoredEpisodeSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}

	// C# `information.SearchCriteria is { MonitoredEpisodesOnly: false }`
	// — matches only when SearchCriteria is non-nil AND
	// MonitoredEpisodesOnly is false. On RSS sync SearchCriteria is nil,
	// so this does NOT match and the monitored gate applies.
	if criteria, ok := info.SearchCriteria.(*SearchCriteria); ok && criteria != nil && !criteria.MonitoredEpisodesOnly {
		s.log.Debug("monitoredEpisode: skipping monitored check during search")
		return Accept()
	}

	if !subject.Series.Monitored {
		s.log.Debug("monitoredEpisode: series not monitored, rejecting",
			"seriesId", subject.Series.ID, "series", subject.Series.Title)
		return Reject(ReasonSeriesNotMonitored, "Series is not monitored")
	}

	monitoredCount := 0
	for _, ep := range subject.Episodes {
		if ep.Monitored {
			monitoredCount++
		}
	}
	if monitoredCount == len(subject.Episodes) {
		return Accept()
	}

	if len(subject.Episodes) == 1 {
		s.log.Debug("monitoredEpisode: episode not monitored, rejecting",
			"seriesId", subject.Series.ID)
		return Reject(ReasonEpisodeNotMonitored, "Episode is not monitored")
	}

	s.log.Debug("monitoredEpisode: one or more episodes not monitored, rejecting",
		"seriesId", subject.Series.ID,
		"monitored", monitoredCount,
		"total", len(subject.Episodes))
	return Reject(ReasonEpisodeNotMonitored, "One or more episodes is not monitored")
}

// Compile-time interface satisfaction.
var _ Specification = (*MonitoredEpisodeSpecification)(nil)
