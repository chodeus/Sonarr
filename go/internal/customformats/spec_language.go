package customformats

import "sonarr.tv/sonarr/internal/language"

// LanguageSpec ports `Specifications/LanguageSpecification.cs`.
// Reports whether the input's `Languages` list satisfies a
// language-membership condition with two configurable axes:
//
//   - ExceptLanguage: when true, the test flips from "languages
//     contains Value" to "languages contains anything OTHER than
//     Value".
//   - Original-language fallback: when Value == Language.Original.Id
//     AND the input's Series.OriginalLanguage is not Unknown, the
//     comparison swaps to that series's original language. Lets a
//     custom format target "non-English" releases without hard-
//     coding an English Id.
//
// LanguageSpec OVERRIDES the BaseSpecification.applyNegate flow
// because the C# Negate path has subtly different
// nil-fallback semantics from the without-Negate path. See
// `IsSatisfiedBy` for details.
type LanguageSpec struct {
	BaseSpecification
	// Value holds the int Language.ID to match. Mirrors the C# `Value`
	// property typed as `Language` enum (which is itself int-backed).
	Value int
	// ExceptLanguage flips the comparison from "contains" to
	// "contains anything other than".
	ExceptLanguage bool
}

// Order returns 3.
func (s *LanguageSpec) Order() int { return 3 }

// ImplementationName returns "Language".
func (s *LanguageSpec) ImplementationName() string { return "Language" }

// IsSatisfiedBy ports the C# override at `LanguageSpecification.cs:42-50`.
// Two behavioural quirks preserved verbatim from C#:
//
//  1. The Negate path has its OWN body (`IsSatisfiedByWithNegate`)
//     rather than wrapping the without-Negate result. It exists
//     because the C# null-coalescing `?? false` interacts oddly
//     with the `!` operator: when `input.Languages` is null, BOTH
//     paths return false (NOT one returning true). The Go port
//     mirrors this — a nil Languages slice always returns false
//     regardless of Negate.
//  2. The Original-language fallback only fires when EpisodeInfo
//     AND Series are both non-nil — mirrors the C# truthy-chain.
func (s *LanguageSpec) IsSatisfiedBy(input CustomFormatInput) bool {
	if input.Languages == nil {
		return false
	}
	cmp := s.compareLanguage(input)
	if s.Negate {
		if s.ExceptLanguage {
			return !anyLanguageNotEqual(input.Languages, cmp)
		}
		return !containsLanguage(input.Languages, cmp)
	}
	if s.ExceptLanguage {
		return anyLanguageNotEqual(input.Languages, cmp)
	}
	return containsLanguage(input.Languages, cmp)
}

// compareLanguage applies the C# Original-language fallback at
// `LanguageSpecification.cs:51-54`.
func (s *LanguageSpec) compareLanguage(input CustomFormatInput) language.Language {
	if input.EpisodeInfo != nil && input.Series != nil &&
		s.Value == language.Original.ID &&
		input.Series.OriginalLanguage != language.Unknown.ID {
		if l, ok := language.FindByID(input.Series.OriginalLanguage); ok {
			return l
		}
	}
	if l, ok := language.FindByID(s.Value); ok {
		return l
	}
	return language.Language{}
}

func containsLanguage(xs []language.Language, target language.Language) bool {
	for _, l := range xs {
		if l == target {
			return true
		}
	}
	return false
}

func anyLanguageNotEqual(xs []language.Language, target language.Language) bool {
	for _, l := range xs {
		if l != target {
			return true
		}
	}
	return false
}
