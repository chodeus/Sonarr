package autotagging

import (
	"encoding/json"
	"fmt"
)

// Persisted-spec marshal/unmarshal. Ports
// `src/NzbDrone.Core/Datastore/Converters/AutoTaggingSpecificationConverter.cs`.
// Wire shape is identical to CustomFormat:
// `[{"type": "<ClassName>", "body": {...}}]`.
//
// Discriminator catalogue at
// `internal/provider.autoTaggingSpecTypes` — 11 concrete types at
// HEAD, all 11 ported in the spec.go file alongside this slice.

type specEnvelope struct {
	Type string          `json:"type"`
	Body json.RawMessage `json:"body"`
}

type baseBody struct {
	Name     string `json:"name"`
	Negate   bool   `json:"negate"`
	Required bool   `json:"required"`
}

type intValueBody struct {
	baseBody
	Value int `json:"value"`
}

type stringValueBody struct {
	baseBody
	Value string `json:"value"`
}

type stringListBody struct {
	baseBody
	Value []string `json:"value"`
}

type yearBody struct {
	baseBody
	Min int `json:"min"`
	Max int `json:"max"`
}

type statusBody struct {
	baseBody
	Status int `json:"status"` // C# uses `Status`, not `Value`.
}

func bodyToBase(b baseBody) BaseSpecification { return BaseSpecification(b) }

// UnmarshalSpecs parses a JSON-encoded Specifications array into a
// typed `[]Specification` slice. Empty / nil input → (nil, nil).
func UnmarshalSpecs(data []byte) ([]Specification, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var envelopes []specEnvelope
	if err := json.Unmarshal(data, &envelopes); err != nil {
		return nil, fmt.Errorf("autotagging: unmarshal envelope: %w", err)
	}
	specs := make([]Specification, 0, len(envelopes))
	for i, env := range envelopes {
		spec, err := decodeSpec(env)
		if err != nil {
			return nil, fmt.Errorf("autotagging: spec %d (%q): %w", i, env.Type, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// MarshalSpecs encodes a `[]Specification` slice into the
// canonical wire shape. Round-trip safe.
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

func decodeSpec(env specEnvelope) (Specification, error) {
	switch env.Type {
	case "MonitoredSpecification":
		var b baseBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &MonitoredSpec{BaseSpecification: bodyToBase(b)}, nil
	case "QualityProfileSpecification":
		var b intValueBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &QualityProfileSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "SeriesTypeSpecification":
		var b intValueBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &SeriesTypeSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "StatusSpecification":
		var b statusBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &StatusSpec{BaseSpecification: bodyToBase(b.baseBody), Status: b.Status}, nil
	case "TagSpecification":
		var b intValueBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &TagSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "YearSpecification":
		var b yearBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &YearSpec{BaseSpecification: bodyToBase(b.baseBody), Min: b.Min, Max: b.Max}, nil
	case "GenreSpecification":
		var b stringListBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &GenreSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "NetworkSpecification":
		var b stringListBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &NetworkSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "OriginalCountrySpecification":
		var b stringListBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &OriginalCountrySpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "OriginalLanguageSpecification":
		var b intValueBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &OriginalLanguageSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	case "RootFolderSpecification":
		var b stringValueBody
		if err := json.Unmarshal(env.Body, &b); err != nil {
			return nil, err
		}
		return &RootFolderSpec{BaseSpecification: bodyToBase(b.baseBody), Value: b.Value}, nil
	default:
		return nil, fmt.Errorf("unknown discriminator")
	}
}

func encodeSpec(spec Specification) (string, map[string]any, error) {
	switch s := spec.(type) {
	case *MonitoredSpec:
		return "MonitoredSpecification", baseToMap(s.BaseSpecification, nil), nil
	case *QualityProfileSpec:
		return "QualityProfileSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *SeriesTypeSpec:
		return "SeriesTypeSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *StatusSpec:
		return "StatusSpecification", baseToMap(s.BaseSpecification, map[string]any{"status": s.Status}), nil
	case *TagSpec:
		return "TagSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *YearSpec:
		return "YearSpecification", baseToMap(s.BaseSpecification, map[string]any{
			"min": s.Min,
			"max": s.Max,
		}), nil
	case *GenreSpec:
		return "GenreSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *NetworkSpec:
		return "NetworkSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *OriginalCountrySpec:
		return "OriginalCountrySpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *OriginalLanguageSpec:
		return "OriginalLanguageSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	case *RootFolderSpec:
		return "RootFolderSpecification", baseToMap(s.BaseSpecification, map[string]any{"value": s.Value}), nil
	default:
		return "", nil, fmt.Errorf("autotagging: unknown spec type %T", spec)
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
