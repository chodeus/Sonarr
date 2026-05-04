package customformats

import "sonarr.tv/sonarr/internal/parser"

// ReleaseTypeSpec ports `Specifications/ReleaseTypeSpecification.cs`
// (formerly `SeasonPackSpecification` — renamed in migration 205).
// IsSatisfiedBy reports whether the input's `ReleaseType` matches
// `Value` exactly.
type ReleaseTypeSpec struct {
	BaseSpecification
	// Value holds the int ReleaseType to match. Mirrors the C#
	// `Value` property typed as `ReleaseType` enum.
	Value int
}

// Order returns 10.
func (s *ReleaseTypeSpec) Order() int { return 10 }

// ImplementationName returns "Release Type".
func (s *ReleaseTypeSpec) ImplementationName() string { return "Release Type" }

// IsSatisfiedBy mirrors `ReleaseTypeSpecification.IsSatisfiedByWithoutNegate`
// at `:30`.
func (s *ReleaseTypeSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	return s.applyNegate(input.ReleaseType == parser.ReleaseType(s.Value))
}
