package customformats

import (
	"reflect"
	"sort"
)

// Core matching algorithm. Ports the per-type-grouping logic at
// `src/NzbDrone.Core/CustomFormats/CustomFormatCalculationService.cs:147-168`
// plus `SpecificationMatchesGroup.cs`.
//
// The seven typed entry points the C# side exposes
// (`ParseCustomFormat(RemoteEpisode, ...)`, `ParseCustomFormat(EpisodeFile, ...)`,
// etc.) are deferred — each builds a CustomFormatInput from a
// domain type the Go port hasn't reached yet (RemoteEpisode,
// EpisodeFile, LocalEpisode, Blocklist, EpisodeHistory). They land
// alongside their respective domain types. The core algorithm
// shipped here is what every adapter delegates to.

// Format is the in-memory typed sibling of the persisted
// `CustomFormat` struct (defined in customformats.go for storage).
// It carries the same name + IncludeWhenRenaming bit but with a
// typed `[]Specification` slice instead of the opaque JSON string.
//
// Persisted-spec deserialisation (the JSON-discriminator decode
// that turns CustomFormat.Specifications text into a
// []Specification slice) is a separate slice — when it lands, it
// produces *Format values from CustomFormat rows.
type Format struct {
	ID                              int
	Name                            string
	Specifications                  []Specification
	IncludeCustomFormatWhenRenaming bool
}

// SpecificationGroup ports `SpecificationMatchesGroup.cs:5-13`.
// Holds the per-type group of `(spec, satisfied)` pairs. Used
// internally by Calculate; exported because the algorithm
// surfaces individual group DidMatch decisions for callers that
// want to debug "why didn't this format match?".
type SpecificationGroup struct {
	// Matches preserves the order specs were inserted (within the
	// group) — Go's map iteration is randomised, so the slice form
	// gives stable diagnostic output.
	Matches []SpecificationMatch
}

// SpecificationMatch pairs a Specification with whether it was
// satisfied by the current input. Mirrors the C#
// `Dictionary<ICustomFormatSpecification, bool>.Entry`.
type SpecificationMatch struct {
	Spec      Specification
	Satisfied bool
}

// DidMatch ports the C# `SpecificationMatchesGroup.DidMatch` getter
// at `:10-11`:
//
//	!(Matches.Any(m => m.Key.Required && m.Value == false) ||
//	  Matches.All(m => m.Value == false))
//
// Decoded: the group matches iff
//
//   - NO required spec is unsatisfied, AND
//   - at least one spec IS satisfied.
//
// An empty Matches slice (no specs in this type-group) returns
// false because `All(false)` over an empty enumerable in C# is
// vacuously true, so the second clause makes the whole expression
// false. The Go port preserves this.
func (g SpecificationGroup) DidMatch() bool {
	if len(g.Matches) == 0 {
		return false
	}
	anyRequiredFailed := false
	allFailed := true
	for _, m := range g.Matches {
		if !m.Satisfied {
			if r, ok := requiredFlag(m.Spec); ok && r {
				anyRequiredFailed = true
			}
		} else {
			allFailed = false
		}
	}
	return !anyRequiredFailed && !allFailed
}

// requiredFlag extracts the Required bit from a Specification's
// embedded BaseSpecification. The Specification interface doesn't
// expose Required directly (the C# `ICustomFormatSpecification`
// does); we use reflection here so callers don't have to widen
// the interface or expose internal helpers.
//
// Returns (false, false) if the spec doesn't embed
// BaseSpecification — defensive against future spec types that
// might implement Specification without using the embedded base.
func requiredFlag(spec Specification) (bool, bool) {
	v := reflect.Indirect(reflect.ValueOf(spec))
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return false, false
	}
	field := v.FieldByName("BaseSpecification")
	if !field.IsValid() || field.Kind() != reflect.Struct {
		return false, false
	}
	required := field.FieldByName("Required")
	if !required.IsValid() || required.Kind() != reflect.Bool {
		return false, false
	}
	return required.Bool(), true
}

// Calculate ports the core matching loop at
// `CustomFormatCalculationService.cs:147-168`. Walks every format,
// groups its specs by Go type, computes each group's DidMatch,
// and keeps the format if every group matches. Returns the
// surviving formats sorted by Name (mirrors the C#
// `OrderBy(x => x.Name)`).
//
// The grouping by `reflect.TypeOf(spec)` mirrors the C#
// `GroupBy(t => t.GetType())` semantic: two `ResolutionSpec`
// instances with different `Value` go in the SAME group, while a
// `ResolutionSpec` and a `SourceSpec` go in DIFFERENT groups.
// Inside a group, the C# `DidMatch` lets just one match satisfy
// the group (so a format with two ResolutionSpecs of different
// Value matches when EITHER is satisfied — provided no Required
// spec failed).
func Calculate(input CustomFormatInput, formats []*Format) []*Format {
	var matched []*Format
	for _, f := range formats {
		if f == nil {
			continue
		}
		groups := groupSpecsByType(f.Specifications, input)
		if allGroupsMatched(groups) {
			matched = append(matched, f)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Name < matched[j].Name
	})
	return matched
}

func groupSpecsByType(specs []Specification, input CustomFormatInput) []SpecificationGroup {
	type key struct{ t reflect.Type }
	idx := map[key]int{}
	var groups []SpecificationGroup
	for _, spec := range specs {
		k := key{t: reflect.TypeOf(spec)}
		i, ok := idx[k]
		if !ok {
			i = len(groups)
			groups = append(groups, SpecificationGroup{})
			idx[k] = i
		}
		groups[i].Matches = append(groups[i].Matches, SpecificationMatch{
			Spec:      spec,
			Satisfied: spec.IsSatisfiedBy(input),
		})
	}
	return groups
}

func allGroupsMatched(groups []SpecificationGroup) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if !g.DidMatch() {
			return false
		}
	}
	return true
}
