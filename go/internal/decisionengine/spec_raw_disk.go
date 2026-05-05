package decisionengine

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/parser"
)

// RawDiskSpecificationName is the canonical Spec.Name() identifier.
// Mirrors the C# class name minus the `Specification` suffix.
const RawDiskSpecificationName = "RawDisk"

// rawDiskRegex is the C# `DiscRegex` array at
// `RawDiskSpecification.cs:13-19`. Three patterns:
//
//  1. Disc-numbered Bluray release name (e.g. `Disc.01.1080p.BluRay`).
//  2. Full-Bluray release name (`1080p.Full.Bluray`).
//  3. DVD-Video token (`DVD-R`, `DVD9`, `DVD5`, `xMDVD`).
//
// All three are RE2-compatible verbatim from the C#
// `RegexOptions.IgnoreCase | RegexOptions.Compiled`. Compiled lazily at
// package init via the package-var below.
var rawDiskRegex = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:dis[ck])(?:[-_. ]\d+[-_. ])(?:(?:(?:480|720|1080|2160)[ip]|)[-_. ])?(?:Blu-?ray)`),
	regexp.MustCompile(`(?i)(?:(?:480|720|1080|2160)[ip]|)[-_. ](?:full)[-_. ](?:Blu-?ray)`),
	regexp.MustCompile(`(?i)(?:\d?x?M?DVD-?[R59])(?:[ ._]|$)`),
}

// rawDvdContainers + rawBlurayContainers mirror the C# arrays at
// `RawDiskSpecification.cs:21-22`. Container is matched lower-cased
// against these.
var (
	rawDvdContainers     = map[string]struct{}{"vob": {}, "iso": {}}
	rawBlurayContainers  = map[string]struct{}{"m2ts": {}}
	rawBlurayMessage     = "Raw Bluray release"
	rawBlurayDiscMessage = "Raw Bluray/DVD release"
	rawDVDMessage        = "Raw DVD release"
)

// RawDiskSpecification rejects "raw" releases (BD-ISO, DVD VOB, full
// Bluray rips that contain the disc structure). Permanent rejection;
// no config — the C# tree never made this user-toggleable because raw
// disc releases require post-processing Sonarr can't do.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RawDiskSpecification.cs:1-71`.
type RawDiskSpecification struct {
	log *slog.Logger
}

// NewRawDiskSpecification builds the spec. log defaults to slog.Default
// when nil.
func NewRawDiskSpecification(log *slog.Logger) *RawDiskSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &RawDiskSpecification{log: log}
}

// Name returns the canonical spec name.
func (*RawDiskSpecification) Name() string { return RawDiskSpecificationName }

// Kind reports Permanent — raw-disc rejections are not retry-able.
func (*RawDiskSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — cheap evaluation, no disk reads.
func (*RawDiskSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:33-67`. Returns Accept when
// the release passes; Reject(ReasonRaw, ...) when it doesn't.
//
// Three rejection paths:
//
//  1. Title matches one of the rawDiskRegex patterns → "Raw Bluray/DVD release"
//  2. Container is one of {vob, iso} → "Raw DVD release"
//  3. Container is m2ts → "Raw Bluray release"
//
// Releases with nil Release (the C# `subject.Release == null` early-exit
// at `:35-38`) pass — the C# tree treats no-release as a wiring
// pre-condition, not a rejection.
func (s *RawDiskSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	for _, re := range rawDiskRegex {
		if re.MatchString(rel.Title) {
			s.log.Debug("rawDisk: title matches raw disc pattern", "title", rel.Title)
			return Reject(ReasonRaw, rawBlurayDiscMessage)
		}
	}

	container := strings.TrimSpace(strings.ToLower(rel.Container))
	if container == "" {
		return Accept()
	}

	if _, ok := rawDvdContainers[container]; ok {
		s.log.Debug("rawDisk: container is raw DVD", "container", container)
		return Reject(ReasonRaw, rawDVDMessage)
	}
	if _, ok := rawBlurayContainers[container]; ok {
		s.log.Debug("rawDisk: container is raw Bluray", "container", container)
		return Reject(ReasonRaw, rawBlurayMessage)
	}

	return Accept()
}

// releaseFromSubject extracts the embedded *parser.ReleaseInfo from a
// Subject's Release field. Returns nil if Release is nil or has an
// unexpected type. Both *ReleaseInfo and *TorrentInfo flow through —
// TorrentInfo embeds ReleaseInfo so the address-of-embed is the same
// shape.
//
// The Subject.Release field is `any` to allow either concrete type
// without an interface cast; this helper centralises the pattern so
// every spec doesn't have to repeat the type-switch.
func releaseFromSubject(s *Subject) *parser.ReleaseInfo {
	if s == nil {
		return nil
	}
	switch r := s.Release.(type) {
	case *parser.ReleaseInfo:
		return r
	case *parser.TorrentInfo:
		if r == nil {
			return nil
		}
		return &r.ReleaseInfo
	}
	return nil
}
