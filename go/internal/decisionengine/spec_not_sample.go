package decisionengine

import (
	"context"
	"log/slog"
	"strings"
)

// NotSampleSpecificationName is the canonical Spec.Name() identifier.
const NotSampleSpecificationName = "NotSample"

// NotSampleMaxBytes is the size threshold below which a release whose
// title contains "sample" is rejected. Mirrors the C# `70.Megabytes()`
// at `NotSampleSpecification.cs:24` (70 * 1024 * 1024 = 73 400 320).
const NotSampleMaxBytes int64 = 70 * 1024 * 1024

// NotSampleSpecification rejects sample releases — title containing
// "sample" AND size below 70MB. Permanent rejection.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/NotSampleSpecification.cs:1-31`.
//
// The narrowness of the rule (title-contains AND size-below) avoids
// false positives on large releases that legitimately have "sample"
// in the name (e.g. a S01E01 episode named ".SAMPLE.1080p.HDTV"
// where the user mis-tagged a real release).
type NotSampleSpecification struct {
	log *slog.Logger
}

// NewNotSampleSpecification builds the spec. log defaults to slog.Default
// when nil.
func NewNotSampleSpecification(log *slog.Logger) *NotSampleSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &NotSampleSpecification{log: log}
}

// Name returns the canonical spec name.
func (*NotSampleSpecification) Name() string { return NotSampleSpecificationName }

// Kind reports Permanent.
func (*NotSampleSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default.
func (*NotSampleSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:22-30`. Returns Reject only
// when title contains "sample" AND size < 70MB; everything else passes.
//
// Release is read via [releaseFromSubject] so both *ReleaseInfo and
// *TorrentInfo flow through. Nil Release passes — the C# tree's early-
// exit pattern.
func (s *NotSampleSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}
	if strings.Contains(strings.ToLower(rel.Title), "sample") && rel.Size < NotSampleMaxBytes {
		s.log.Debug("notSample: small release with 'sample' token", "title", rel.Title, "size", rel.Size)
		return Reject(ReasonSample, "Sample")
	}
	return Accept()
}
