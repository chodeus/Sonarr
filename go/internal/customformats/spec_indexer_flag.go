package customformats

import "sonarr.tv/sonarr/internal/parser"

// IndexerFlagSpec ports `Specifications/IndexerFlagSpecification.cs`.
// IsSatisfiedBy reports whether the input's `IndexerFlags` includes
// the `Value` bit (or all bits in `Value` when `Value` is a
// composite). Uses Go's `IndexerFlags.HasFlag` which mirrors C#'s
// `Enum.HasFlag` semantic.
type IndexerFlagSpec struct {
	BaseSpecification
	// Value holds the int bitmask to match. Mirrors the C# `Value`
	// property typed as `IndexerFlags` enum.
	Value int
}

// Order returns 4.
func (s *IndexerFlagSpec) Order() int { return 4 }

// ImplementationName returns "Indexer Flag".
func (s *IndexerFlagSpec) ImplementationName() string { return "Indexer Flag" }

// IsSatisfiedBy mirrors `IndexerFlagSpecification.IsSatisfiedByWithoutNegate`
// at `:30`.
func (s *IndexerFlagSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	return s.applyNegate(input.IndexerFlags.HasFlag(parser.IndexerFlags(s.Value)))
}
