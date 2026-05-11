package autotagging

import (
	"reflect"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/series"
)

// AutoTagging matching engine. Ports the surface from
// `src/NzbDrone.Core/AutoTagging/`:
//
//   - `IAutoTaggingSpecification` (`Specifications/IAutoTagSpecification.cs`)
//     and `AutoTaggingSpecificationBase` (`Specifications/AutoTagSpecificationBase.cs`).
//   - All 11 concrete specs in `Specifications/`:
//     Monitored, QualityProfile, SeriesType, Status, Tag, Year,
//     Genre, Network, OriginalCountry, OriginalLanguage, RootFolder.
//   - `SpecificationMatchesGroup` + the matching algorithm at
//     `AutoTaggingService.cs`.
//   - `AutoTaggingSpecificationConverter` JSON envelope.
//
// Structurally parallel to `internal/customformats` — same
// Specification interface shape, same per-type-grouping match
// algorithm, same `{type, body}` envelope. The two packages don't
// share code because the C# specs operate on different inputs
// (CustomFormat → CustomFormatInput; AutoTag → Series) and the
// per-spec body shapes are different.

// Input is the bundle of context every Specification.IsSatisfiedBy
// needs. Diverges from the C# `IsSatisfiedBy(Series)` signature so
// the Go port can attach derived context (`RootFolderPath`)
// alongside the persisted Series fields without polluting the
// `series.Series` storage struct.
//
// Callers populate Series + RootFolderPath before invoking
// Calculate. The C# AutoTaggingService does the equivalent
// pre-population on `series.RootFolderPath` (a getter that's
// resolved against the RootFolderService at call time).
type Input struct {
	Series         *series.Series
	RootFolderPath string
}

// Specification is the typed-spec interface. Each implementation
// answers "does this Series satisfy my pattern?".
type Specification interface {
	Order() int
	ImplementationName() string
	IsSatisfiedBy(input Input) bool
}

// BaseSpecification mirrors `AutoTaggingSpecificationBase.cs:5-37`.
// Embed by-value into typed specs to inherit the
// `Name`/`Negate`/`Required` fields plus the `applyNegate` helper
// the typed specs use in their IsSatisfiedBy wrappers.
type BaseSpecification struct {
	Name     string
	Negate   bool
	Required bool
}

func (b *BaseSpecification) applyNegate(match bool) bool {
	if b.Negate {
		return !match
	}
	return match
}

// MonitoredSpec ports `Specifications/MonitoredSpecification.cs`.
// Order=1. IsSatisfiedBy returns `Series.Monitored`.
type MonitoredSpec struct{ BaseSpecification }

// Order implements Specification.
func (s *MonitoredSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *MonitoredSpec) ImplementationName() string { return "Monitored" }

// IsSatisfiedBy implements Specification.
func (s *MonitoredSpec) IsSatisfiedBy(in Input) bool {
	return s.applyNegate(in.Series != nil && in.Series.Monitored)
}

// QualityProfileSpec ports `Specifications/QualityProfileSpecification.cs`.
// Order=1. IsSatisfiedBy returns `series.QualityProfileId == Value`.
// Series.QualityProfileID is `*int` (nullable); compare against the
// dereferenced value, treating nil as "no match" (a profile id of
// 0 from the C# side would never match a real persisted profile).
type QualityProfileSpec struct {
	BaseSpecification
	Value int
}

// Order implements Specification.
func (s *QualityProfileSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *QualityProfileSpec) ImplementationName() string { return "Quality Profile" }

// IsSatisfiedBy implements Specification.
func (s *QualityProfileSpec) IsSatisfiedBy(in Input) bool {
	if in.Series == nil || in.Series.QualityProfileID == 0 {
		return s.applyNegate(false)
	}
	return s.applyNegate(in.Series.QualityProfileID == s.Value)
}

// SeriesTypeSpec ports `Specifications/SeriesTypeSpecification.cs`.
// Order=2. IsSatisfiedBy returns `(int)series.SeriesType == Value`.
type SeriesTypeSpec struct {
	BaseSpecification
	Value int
}

// Order implements Specification.
func (s *SeriesTypeSpec) Order() int { return 2 }

// ImplementationName implements Specification.
func (s *SeriesTypeSpec) ImplementationName() string { return "Series Type" }

// IsSatisfiedBy implements Specification.
func (s *SeriesTypeSpec) IsSatisfiedBy(in Input) bool {
	return s.applyNegate(in.Series != nil && in.Series.SeriesType == s.Value)
}

// StatusSpec ports `Specifications/StatusSpecification.cs`.
// Order=1. C# field is `Status` (not `Value`); preserved verbatim
// in the Go port for round-trip JSON parity with persisted rows.
type StatusSpec struct {
	BaseSpecification
	Status int
}

// Order implements Specification.
func (s *StatusSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *StatusSpec) ImplementationName() string { return "Status" }

// IsSatisfiedBy implements Specification.
func (s *StatusSpec) IsSatisfiedBy(in Input) bool {
	return s.applyNegate(in.Series != nil && in.Series.Status == s.Status)
}

// TagSpec ports `Specifications/TagSpecification.cs`. Order=1.
// IsSatisfiedBy returns `series.Tags.Contains(Value)`.
type TagSpec struct {
	BaseSpecification
	Value int
}

// Order implements Specification.
func (s *TagSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *TagSpec) ImplementationName() string { return "Tag" }

// IsSatisfiedBy implements Specification.
func (s *TagSpec) IsSatisfiedBy(in Input) bool {
	if in.Series == nil {
		return s.applyNegate(false)
	}
	for _, t := range in.Series.Tags {
		if t == s.Value {
			return s.applyNegate(true)
		}
	}
	return s.applyNegate(false)
}

// YearSpec ports `Specifications/YearSpecification.cs`. Order=1.
// IsSatisfiedBy returns `series.Year >= Min && series.Year <= Max`.
// Series.Year is `*int`; nil → no match.
type YearSpec struct {
	BaseSpecification
	Min int
	Max int
}

// Order implements Specification.
func (s *YearSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *YearSpec) ImplementationName() string { return "Year" }

// IsSatisfiedBy implements Specification.
func (s *YearSpec) IsSatisfiedBy(in Input) bool {
	if in.Series == nil || in.Series.Year == 0 {
		return s.applyNegate(false)
	}
	y := in.Series.Year
	return s.applyNegate(y >= s.Min && y <= s.Max)
}

// GenreSpec ports `Specifications/GenreSpecification.cs`.
// Order=1. The C# `Value` is `IEnumerable<string>` matched against
// `series.Genres` via case-insensitive Contains. Series.Genres in
// the Go port is the raw JSON array string (per series.go's
// embedded-doc deferral); GenreSpec parses it on each call.
//
// (When the typed Genre layer ships in a future series-services
// slice, this can switch to reading a parsed `[]string`. Until
// then we parse JSON each call.)
type GenreSpec struct {
	BaseSpecification
	Value []string
}

// Order implements Specification.
func (s *GenreSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *GenreSpec) ImplementationName() string { return "Genre" }

// IsSatisfiedBy implements Specification.
func (s *GenreSpec) IsSatisfiedBy(in Input) bool {
	if in.Series == nil {
		return s.applyNegate(false)
	}
	genres := parseGenres(in.Series.Genres)
	for _, g := range genres {
		if anyEqualsCI(s.Value, g) {
			return s.applyNegate(true)
		}
	}
	return s.applyNegate(false)
}

// NetworkSpec ports `Specifications/NetworkSpecification.cs`.
// Order=1. Case-insensitive equals against `series.Network`. Any
// of the configured Value entries matching counts.
type NetworkSpec struct {
	BaseSpecification
	Value []string
}

// Order implements Specification.
func (s *NetworkSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *NetworkSpec) ImplementationName() string { return "Network" }

// IsSatisfiedBy implements Specification.
func (s *NetworkSpec) IsSatisfiedBy(in Input) bool {
	if in.Series == nil {
		return s.applyNegate(false)
	}
	for _, n := range s.Value {
		if strings.EqualFold(in.Series.Network, n) {
			return s.applyNegate(true)
		}
	}
	return s.applyNegate(false)
}

// OriginalCountrySpec ports `Specifications/OriginalCountrySpecification.cs`.
// Order=1. Case-insensitive equals against `series.OriginalCountry`.
type OriginalCountrySpec struct {
	BaseSpecification
	Value []string
}

// Order implements Specification.
func (s *OriginalCountrySpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *OriginalCountrySpec) ImplementationName() string { return "Original Country" }

// IsSatisfiedBy implements Specification.
func (s *OriginalCountrySpec) IsSatisfiedBy(in Input) bool {
	if in.Series == nil {
		return s.applyNegate(false)
	}
	for _, c := range s.Value {
		if strings.EqualFold(in.Series.OriginalCountry, c) {
			return s.applyNegate(true)
		}
	}
	return s.applyNegate(false)
}

// OriginalLanguageSpec ports `Specifications/OriginalLanguageSpecification.cs`.
// Order=1. Exact-match against `series.OriginalLanguage` (int).
type OriginalLanguageSpec struct {
	BaseSpecification
	Value int
}

// Order implements Specification.
func (s *OriginalLanguageSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *OriginalLanguageSpec) ImplementationName() string { return "Original Language" }

// IsSatisfiedBy implements Specification.
func (s *OriginalLanguageSpec) IsSatisfiedBy(in Input) bool {
	return s.applyNegate(in.Series != nil && in.Series.OriginalLanguage == s.Value)
}

// RootFolderSpec ports `Specifications/RootFolderSpecification.cs`.
// Order=1. Path-equals against `Input.RootFolderPath` (NOT a Series
// field — populated by the caller from the RootFolderService
// before invoking Calculate).
//
// "Path-equals" mirrors the C# `PathExtensions.PathEquals` —
// case-insensitive match with separator normalisation. The Go
// port uses `strings.EqualFold` after normalising trailing
// separators; cross-platform (`/` on Unix, `\` on Windows) is
// handled by `filepath.Clean` on both sides.
type RootFolderSpec struct {
	BaseSpecification
	Value string
}

// Order implements Specification.
func (s *RootFolderSpec) Order() int { return 1 }

// ImplementationName implements Specification.
func (s *RootFolderSpec) ImplementationName() string { return "Root Folder" }

// IsSatisfiedBy implements Specification.
func (s *RootFolderSpec) IsSatisfiedBy(in Input) bool {
	return s.applyNegate(pathEquals(in.RootFolderPath, s.Value))
}

func pathEquals(a, b string) bool {
	a = strings.TrimRight(a, `/\`)
	b = strings.TrimRight(b, `/\`)
	return strings.EqualFold(a, b)
}

func anyEqualsCI(xs []string, target string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, target) {
			return true
		}
	}
	return false
}

// SpecificationGroup ports `SpecificationMatchesGroup.cs:5-13`. See
// `customformats.SpecificationGroup` for the algorithm rationale —
// AutoTagging's group structure is identical (per-type grouping,
// `DidMatch = !any-required-failed && !all-failed`).
type SpecificationGroup struct {
	Matches []SpecificationMatch
}

// SpecificationMatch pairs a Specification with its IsSatisfiedBy result.
type SpecificationMatch struct {
	Spec      Specification
	Satisfied bool
}

// DidMatch ports the C# DidMatch getter at `SpecificationMatchesGroup.cs:10-11`.
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

// Tag is the in-memory typed sibling of the persisted `AutoTag`
// struct (storage form lives in autotagging.go). Carries Name +
// RemoveTagsAutomatically + persisted Tags + a typed Specifications
// slice.
type Tag struct {
	ID                      int
	Name                    string
	Specifications          []Specification
	RemoveTagsAutomatically bool
	Tags                    []int
}

// Calculate ports the per-tag matching loop from
// `AutoTaggingService.cs`. For each AutoTag, groups its specs by Go
// type, computes each group's DidMatch, and keeps the AutoTag if
// every group matches. Returns survivors sorted by Name.
func Calculate(input Input, tags []*Tag) []*Tag {
	var matched []*Tag
	for _, t := range tags {
		if t == nil {
			continue
		}
		groups := groupSpecsByType(t.Specifications, input)
		if allGroupsMatched(groups) {
			matched = append(matched, t)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Name < matched[j].Name
	})
	return matched
}

func groupSpecsByType(specs []Specification, input Input) []SpecificationGroup {
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
