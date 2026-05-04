package customformats

import (
	"regexp"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// First slice of the CustomFormat matching engine. Ports the
// surface from `src/NzbDrone.Core/CustomFormats/`:
//
//   - CustomFormatInput (`CustomFormatInput.cs`) — the bundle of
//     parsed-release context that every Specification.IsSatisfiedBy
//     consumes.
//   - ICustomFormatSpecification (`Specifications/ICustomFormatSpecification.cs`)
//     and CustomFormatSpecificationBase (`Specifications/CustomFormatSpecificationBase.cs`)
//     — the spec interface plus the Negate-toggle wrapper.
//   - RegexSpecificationBase (`Specifications/RegexSpecificationBase.cs`)
//     — shared regex-match helper for ReleaseTitle / ReleaseGroup specs.
//   - ReleaseTitleSpecification (`Specifications/ReleaseTitleSpecification.cs`)
//   - ReleaseGroupSpecification (`Specifications/ReleaseGroupSpecification.cs`)
//
// Deferred to subsequent slices: Resolution / Source / Language /
// ReleaseType / Size / IndexerFlag specs (each a small port — defer
// only because they each pull additional dependencies).
//
// Persisted-spec deserialisation (the JSON-discriminator decode that
// turns a `Specifications` text column into a typed []Specification
// slice) lands when CustomFormatService is ported — Phase 2 closer
// to the persistence layer.

// CustomFormatInput ports `CustomFormatInput.cs:8-46`. Bundles the
// parsed-release context every Specification.IsSatisfiedBy needs.
//
// EpisodeInfo and Series are pointer types because the C# code
// allows null in both slots. The other fields are zero-valuable.
type CustomFormatInput struct {
	EpisodeInfo  *parser.ParsedEpisodeInfo
	Series       *series.Series
	Size         int64
	IndexerFlags parser.IndexerFlags
	Languages    []language.Language
	Filename     string
	ReleaseType  parser.ReleaseType
}

// Specification ports `ICustomFormatSpecification.cs:5-19`. Each
// implementation answers "does this CustomFormatInput satisfy
// my pattern?".
//
// Implementations are expected to embed *BaseSpecification so the
// Negate / Required / Name fields are inherited along with the
// IsSatisfiedBy wrapper that applies Negate.
type Specification interface {
	// Order is the C# `Order` accessor — fixed-position rank used
	// by the UI to display specs in a stable visual order. Mirrors
	// `ICustomFormatSpecification.Order`.
	Order() int
	// ImplementationName is the C# `ImplementationName` — the
	// human-readable name for this spec type (e.g. "Release Title").
	ImplementationName() string
	// IsSatisfiedBy reports whether the input matches this spec.
	// Implementations should respect the Negate flag (the
	// BaseSpecification helper does this automatically).
	IsSatisfiedBy(input CustomFormatInput) bool
}

// BaseSpecification ports `CustomFormatSpecificationBase.cs:5-37`.
// Embed by-value into a typed spec struct to inherit the
// Negate/Required/Name fields plus the Negate-aware IsSatisfiedBy
// wrapper. Each typed spec then implements
// `IsSatisfiedByWithoutNegate(input)`.
//
// Use:
//
//	type ReleaseTitleSpec struct {
//	    BaseSpecification
//	    RegexSpec
//	}
//	func (s *ReleaseTitleSpec) IsSatisfiedBy(input CustomFormatInput) bool {
//	    return s.applyNegate(s.matchTitle(input))
//	}
type BaseSpecification struct {
	Name     string
	Negate   bool
	Required bool
}

// applyNegate is the in-package helper consumed by typed specs
// in their IsSatisfiedBy wrappers. C# does this in the abstract
// base class's IsSatisfiedBy method; the Go port factors it out
// because Go has no inherited virtual-method dispatch.
func (b *BaseSpecification) applyNegate(match bool) bool {
	if b.Negate {
		return !match
	}
	return match
}

// RegexSpec ports `RegexSpecificationBase.cs:17-53`. Holds the
// raw pattern string + a compiled *regexp.Regexp. SetPattern is the
// idiomatic-Go equivalent of the C# `Value` setter.
type RegexSpec struct {
	pattern string
	re      *regexp.Regexp
}

// SetPattern compiles `pattern` as a case-insensitive regex.
// Mirrors `RegexSpecificationBase.Value`'s setter at
// `RegexSpecificationBase.cs:25-37` which uses
// `RegexOptions.Compiled | RegexOptions.IgnoreCase`. Empty /
// whitespace-only patterns clear the compiled regex (the spec
// then matches nothing, mirroring the C# `MatchString` early-
// return when `_regex == null`).
//
// Returns an error when the pattern is non-empty but doesn't
// compile. Callers that store user-provided patterns should
// surface this error to the user; in-memory test fixtures with
// known-good patterns can ignore it.
func (r *RegexSpec) SetPattern(pattern string) error {
	r.pattern = pattern
	r.re = nil
	if pattern == "" {
		return nil
	}
	compiled, err := regexp.Compile(`(?i)` + pattern)
	if err != nil {
		return err
	}
	r.re = compiled
	return nil
}

// Pattern returns the raw pattern string (the C# `Value` getter).
func (r *RegexSpec) Pattern() string { return r.pattern }

// matchString ports `RegexSpecificationBase.MatchString` at
// `:39-47`. Returns false when either the input or the compiled
// regex is empty/nil.
func (r *RegexSpec) matchString(s string) bool {
	if s == "" || r.re == nil {
		return false
	}
	return r.re.MatchString(s)
}

// ReleaseTitleSpec ports `Specifications/ReleaseTitleSpecification.cs`.
// IsSatisfiedBy matches the regex against the parsed episode
// info's ReleaseTitle OR the input filename — either-side match
// counts. Mirrors the C# `MatchString(input.EpisodeInfo?.ReleaseTitle)
// || MatchString(input.Filename)` short-circuit.
type ReleaseTitleSpec struct {
	BaseSpecification
	RegexSpec
}

// Order returns 1 — mirrors C# `ReleaseTitleSpecification.Order`.
func (s *ReleaseTitleSpec) Order() int { return 1 }

// ImplementationName returns "Release Title".
func (s *ReleaseTitleSpec) ImplementationName() string { return "Release Title" }

// IsSatisfiedBy applies the Negate-aware wrapper around the
// inner ReleaseTitle / Filename match.
func (s *ReleaseTitleSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	match := false
	if input.EpisodeInfo != nil {
		match = s.matchString(input.EpisodeInfo.ReleaseTitle)
	}
	if !match {
		match = s.matchString(input.Filename)
	}
	return s.applyNegate(match)
}

// ReleaseGroupSpec ports `Specifications/ReleaseGroupSpecification.cs`.
// Matches the regex against the parsed episode info's ReleaseGroup.
type ReleaseGroupSpec struct {
	BaseSpecification
	RegexSpec
}

// Order returns 9 — mirrors C# `ReleaseGroupSpecification.Order`.
func (s *ReleaseGroupSpec) Order() int { return 9 }

// ImplementationName returns "Release Group".
func (s *ReleaseGroupSpec) ImplementationName() string { return "Release Group" }

// IsSatisfiedBy applies the Negate-aware wrapper around the
// inner ReleaseGroup match.
func (s *ReleaseGroupSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	match := false
	if input.EpisodeInfo != nil {
		match = s.matchString(input.EpisodeInfo.ReleaseGroup)
	}
	return s.applyNegate(match)
}
