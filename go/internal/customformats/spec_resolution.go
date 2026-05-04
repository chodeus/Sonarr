package customformats

import "sonarr.tv/sonarr/internal/quality"

// ResolutionSpec ports `Specifications/ResolutionSpecification.cs`.
// IsSatisfiedBy reports whether the parsed quality's Resolution
// matches `Value` exactly.
type ResolutionSpec struct {
	BaseSpecification
	// Value holds the int Resolution to match (e.g. 720, 1080,
	// 2160). Mirrors the C# `Value` property typed as
	// `Resolution` enum.
	Value int
}

// Order returns 6.
func (s *ResolutionSpec) Order() int { return 6 }

// ImplementationName returns "Resolution".
func (s *ResolutionSpec) ImplementationName() string { return "Resolution" }

// IsSatisfiedBy mirrors `ResolutionSpecification.IsSatisfiedByWithoutNegate`
// at `:27`. When EpisodeInfo is nil OR the parsed Quality is
// Unknown, the comparison source defaults to the Unknown
// resolution sentinel (mirrors the C# `?? (int)Resolution.Unknown`
// null-coalescing).
func (s *ResolutionSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	actual := int(quality.ResolutionUnknown)
	if input.EpisodeInfo != nil {
		actual = input.EpisodeInfo.Quality.Quality.Resolution
	}
	return s.applyNegate(actual == s.Value)
}
