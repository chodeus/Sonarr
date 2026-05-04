package customformats

import "sonarr.tv/sonarr/internal/quality"

// SourceSpec ports `Specifications/SourceSpecification.cs`.
// IsSatisfiedBy reports whether the parsed quality's Source
// (from the C# `QualitySource` enum) matches `Value` exactly.
type SourceSpec struct {
	BaseSpecification
	// Value holds the int QualitySource to match. Mirrors the
	// C# `Value` property typed as `QualitySource` enum.
	Value int
}

// Order returns 5.
func (s *SourceSpec) Order() int { return 5 }

// ImplementationName returns "Source".
func (s *SourceSpec) ImplementationName() string { return "Source" }

// IsSatisfiedBy mirrors `SourceSpecification.IsSatisfiedByWithoutNegate`
// at `:27`. Defaults to `QualitySource.Unknown` when EpisodeInfo is
// nil (matches the C# `?? (int)QualitySource.Unknown`).
func (s *SourceSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	actual := quality.SourceUnknown
	if input.EpisodeInfo != nil {
		actual = input.EpisodeInfo.Quality.Quality.Source
	}
	return s.applyNegate(actual == quality.QualitySource(s.Value))
}
