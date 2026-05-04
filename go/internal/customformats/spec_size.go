package customformats

// bytesPerGB matches the C# `Min.Gigabytes()` extension at
// `NzbDrone.Common.Extensions`. The Sonarr codebase uses 1024-based
// (binary / IEC) gigabytes throughout, NOT 1000-based (SI/decimal).
// Verified by inspection of the C# extension source.
const bytesPerGB int64 = 1024 * 1024 * 1024

// SizeSpec ports `Specifications/SizeSpecification.cs`.
// IsSatisfiedBy reports whether the input's `Size` (in bytes) is
// strictly greater than `Min` GB and less-than-or-equal to `Max` GB.
//
// Note the asymmetric `>` vs `<=` bounds — mirrors the C# expression
// `size > Min.Gigabytes() && size <= Max.Gigabytes()` at `:25`. A
// size of exactly `Min` GB does NOT match; a size of exactly `Max`
// GB DOES match.
type SizeSpec struct {
	BaseSpecification
	// Min and Max in gigabytes (float, matching C# `double`).
	Min float64
	Max float64
}

// Order returns 8.
func (s *SizeSpec) Order() int { return 8 }

// ImplementationName returns "Size".
func (s *SizeSpec) ImplementationName() string { return "Size" }

// IsSatisfiedBy mirrors `SizeSpecification.IsSatisfiedByWithoutNegate`
// at `:23-26`.
func (s *SizeSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	minBytes := int64(s.Min * float64(bytesPerGB))
	maxBytes := int64(s.Max * float64(bytesPerGB))
	match := input.Size > minBytes && input.Size <= maxBytes
	return s.applyNegate(match)
}
