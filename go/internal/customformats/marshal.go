package customformats

import (
	"encoding/json"
	"fmt"
)

// Persisted-spec marshal/unmarshal. Ports
// `src/NzbDrone.Core/Datastore/Converters/CustomFormatSpecificationConverter.cs`,
// the System.Text.Json `JsonConverter<List<ICustomFormatSpecification>>`
// that owns the wire format for the `CustomFormat.Specifications` text
// column.
//
// Wire shape (envelope per spec):
//
//	[
//	  {"type": "<ClassName>", "body": { ...spec fields... }},
//	  {"type": "<ClassName>", "body": { ...spec fields... }}
//	]
//
// `<ClassName>` is the C# class name (e.g. `ReleaseTitleSpecification`,
// NOT `ReleaseTitleSpec`). The catalogue lives at
// `internal/provider.customFormatSpecTypes` and pins HEAD's 8 concrete
// types. Migration 205 renamed `SeasonPackSpecification` →
// `ReleaseTypeSpecification`; legacy rows that still carry the old
// name should have been rewritten by the v205 migration body, so the
// Go port doesn't need to alias the old name.
//
// `body` is the spec's fields encoded as a flat object — for
// ReleaseTitle/Group: `{"name": "...", "negate": false, "required":
// false, "value": "<regex>"}`. The `value` field for regex specs is
// the raw pattern string (the C# `Value` setter compiles internally
// — the JSON shape only carries the raw string).

// SpecEnvelope is the wire shape per element of the Specifications
// JSON array.
type specEnvelope struct {
	Type string          `json:"type"`
	Body json.RawMessage `json:"body"`
}

// Per-spec body shapes. JSON tags mirror the C# property casing
// (`System.Text.Json` uses camelCase by default with
// `JsonNamingPolicy.CamelCase`, which Sonarr applies globally).

type baseBody struct {
	Name     string `json:"name"`
	Negate   bool   `json:"negate"`
	Required bool   `json:"required"`
}

type regexSpecBody struct {
	baseBody
	Value string `json:"value"`
}

type intValueSpecBody struct {
	baseBody
	Value int `json:"value"`
}

type languageSpecBody struct {
	baseBody
	Value          int  `json:"value"`
	ExceptLanguage bool `json:"exceptLanguage"`
}

type sizeSpecBody struct {
	baseBody
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// UnmarshalSpecs parses a JSON-encoded Specifications array into a
// typed `[]Specification` slice. Mirrors the C# `Read` half of
// `CustomFormatSpecificationListConverter`. Returns an error if the
// input isn't a JSON array, contains an unknown discriminator, or
// any body is malformed.
//
// Caller use:
//
//	specs, err := customformats.UnmarshalSpecs([]byte(row.Specifications))
//	if err != nil { ... }
//	format := &Format{Name: row.Name, Specifications: specs}
//
// Empty / null inputs return (nil, nil) so callers can treat
// "no specs persisted" as the zero value.
func UnmarshalSpecs(data []byte) ([]Specification, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var envelopes []specEnvelope
	if err := json.Unmarshal(data, &envelopes); err != nil {
		return nil, fmt.Errorf("customformats: unmarshal envelope: %w", err)
	}
	specs := make([]Specification, 0, len(envelopes))
	for i, env := range envelopes {
		spec, err := decodeSpec(env)
		if err != nil {
			return nil, fmt.Errorf("customformats: spec %d (%q): %w", i, env.Type, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// MarshalSpecs encodes a `[]Specification` slice into the persisted
// JSON envelope shape. Mirrors the C# `Write` half of
// `CustomFormatSpecificationListConverter`. Returns the canonical
// `[{"type": "...", "body": {...}}, ...]` form.
//
// Round-trip: `MarshalSpecs(UnmarshalSpecs(b))` produces JSON
// byte-equivalent to `b` modulo whitespace + key-order
// (Go's encoding/json sorts map keys but emits struct fields in
// declaration order — same shape as System.Text.Json's default).
func MarshalSpecs(specs []Specification) ([]byte, error) {
	envelopes := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		typename, body, err := encodeSpec(spec)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, map[string]any{
			"type": typename,
			"body": body,
		})
	}
	return json.Marshal(envelopes)
}

// decodeSpec dispatches on the envelope's type discriminator and
// constructs the matching Go spec struct. Mirrors the C# pipeline at
// `CustomFormatSpecificationConverter.Read:33-52` (the
// `Type.GetType($"NzbDrone.Core.CustomFormats.{typename}, ...")`
// reflection-based dispatch).
func decodeSpec(env specEnvelope) (Specification, error) {
	switch env.Type {
	case "ReleaseTitleSpecification":
		return decodeReleaseTitleSpec(env.Body)
	case "ReleaseGroupSpecification":
		return decodeReleaseGroupSpec(env.Body)
	case "ResolutionSpecification":
		return decodeIntValueSpec(env.Body, func(b intValueSpecBody) Specification {
			return &ResolutionSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}
		})
	case "SourceSpecification":
		return decodeIntValueSpec(env.Body, func(b intValueSpecBody) Specification {
			return &SourceSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}
		})
	case "ReleaseTypeSpecification":
		return decodeIntValueSpec(env.Body, func(b intValueSpecBody) Specification {
			return &ReleaseTypeSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}
		})
	case "IndexerFlagSpecification":
		return decodeIntValueSpec(env.Body, func(b intValueSpecBody) Specification {
			return &IndexerFlagSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}
		})
	case "SizeSpecification":
		var b sizeSpecBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &SizeSpec{BaseSpecification: bodyToBase(b.baseBody), Min: b.Min, Max: b.Max}, nil
	case "LanguageSpecification":
		var b languageSpecBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &LanguageSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value, ExceptLanguage: b.ExceptLanguage}, nil
	default:
		return nil, fmt.Errorf("unknown discriminator")
	}
}

func decodeReleaseTitleSpec(body []byte) (Specification, error) {
	var b regexSpecBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, err
	}
	spec := &ReleaseTitleSpec{BaseSpecification: bodyToBase(b.baseBody)}
	if err := spec.SetPattern(b.Value); err != nil {
		return nil, err
	}
	return spec, nil
}

func decodeReleaseGroupSpec(body []byte) (Specification, error) {
	var b regexSpecBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, err
	}
	spec := &ReleaseGroupSpec{BaseSpecification: bodyToBase(b.baseBody)}
	if err := spec.SetPattern(b.Value); err != nil {
		return nil, err
	}
	return spec, nil
}

func decodeIntValueSpec(body []byte, build func(intValueSpecBody) Specification) (Specification, error) {
	var b intValueSpecBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, err
	}
	return build(b), nil
}

func bodyToBase(b baseBody) BaseSpecification {
	return BaseSpecification(b)
}

// encodeSpec dispatches a Go spec struct to its discriminator string +
// body map. Mirrors the C# `Write:11-19` `SpecificationWrapper` shape.
func encodeSpec(spec Specification) (string, map[string]any, error) {
	switch s := spec.(type) {
	case *ReleaseTitleSpec:
		return "ReleaseTitleSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value": s.Pattern(),
		}), nil
	case *ReleaseGroupSpec:
		return "ReleaseGroupSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value": s.Pattern(),
		}), nil
	case *ResolutionSpec:
		return "ResolutionSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value": s.Value,
		}), nil
	case *SourceSpec:
		return "SourceSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value": s.Value,
		}), nil
	case *ReleaseTypeSpec:
		return "ReleaseTypeSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value": s.Value,
		}), nil
	case *IndexerFlagSpec:
		return "IndexerFlagSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value": s.Value,
		}), nil
	case *SizeSpec:
		return "SizeSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"min": s.Min,
			"max": s.Max,
		}), nil
	case *LanguageSpec:
		return "LanguageSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"value":          s.Value,
			"exceptLanguage": s.ExceptLanguage,
		}), nil
	default:
		return "", nil, fmt.Errorf("customformats: unknown spec type %T", spec)
	}
}

func baseToMap(b BaseSpecification, extra map[string]any) map[string]any {
	out := map[string]any{
		"name":     b.Name,
		"negate":   b.Negate,
		"required": b.Required,
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
