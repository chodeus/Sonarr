package customformats

import (
	"encoding/json"
	"testing"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

func TestUnmarshalSpecs_EmptyInput(t *testing.T) {
	specs, err := UnmarshalSpecs(nil)
	if err != nil || specs != nil {
		t.Errorf("nil input should return (nil, nil); got (%v, %v)", specs, err)
	}
	specs, err = UnmarshalSpecs([]byte(""))
	if err != nil || specs != nil {
		t.Errorf("empty bytes should return (nil, nil); got (%v, %v)", specs, err)
	}
}

func TestUnmarshalSpecs_EmptyArray(t *testing.T) {
	specs, err := UnmarshalSpecs([]byte("[]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 {
		t.Errorf("empty array should produce empty specs slice; got %v", specs)
	}
}

func TestUnmarshalSpecs_ReleaseTitleSpec(t *testing.T) {
	in := `[{"type":"ReleaseTitleSpecification","body":{"name":"HDTV","negate":false,"required":false,"value":"HDTV"}}]`
	specs, err := UnmarshalSpecs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	rt, ok := specs[0].(*ReleaseTitleSpec)
	if !ok {
		t.Fatalf("expected *ReleaseTitleSpec, got %T", specs[0])
	}
	if rt.Name != "HDTV" {
		t.Errorf("Name = %q, want HDTV", rt.Name)
	}
	if rt.Pattern() != "HDTV" {
		t.Errorf("Pattern = %q, want HDTV", rt.Pattern())
	}
}

func TestUnmarshalSpecs_AllEightTypes(t *testing.T) {
	// Synthesise one envelope per spec type — verify each
	// dispatches to the right typed Go struct with its fields
	// populated.
	in := `[
		{"type":"ReleaseTitleSpecification","body":{"name":"t1","negate":false,"required":false,"value":"WEB-DL"}},
		{"type":"ReleaseGroupSpecification","body":{"name":"g1","negate":true,"required":false,"value":"LOL"}},
		{"type":"ResolutionSpecification","body":{"name":"r1","negate":false,"required":true,"value":1080}},
		{"type":"SourceSpecification","body":{"name":"s1","negate":false,"required":false,"value":6}},
		{"type":"ReleaseTypeSpecification","body":{"name":"rt1","negate":false,"required":false,"value":2}},
		{"type":"IndexerFlagSpecification","body":{"name":"if1","negate":false,"required":false,"value":1}},
		{"type":"SizeSpecification","body":{"name":"sz1","negate":false,"required":false,"min":1.5,"max":10.0}},
		{"type":"LanguageSpecification","body":{"name":"l1","negate":false,"required":false,"value":1,"exceptLanguage":true}}
	]`
	specs, err := UnmarshalSpecs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 8 {
		t.Fatalf("expected 8 specs, got %d", len(specs))
	}

	// Verify each spec's type + a field or two.
	if rt, ok := specs[0].(*ReleaseTitleSpec); !ok || rt.Name != "t1" || rt.Pattern() != "WEB-DL" {
		t.Errorf("specs[0] mismatch: %+v", specs[0])
	}
	if rg, ok := specs[1].(*ReleaseGroupSpec); !ok || rg.Name != "g1" || !rg.Negate || rg.Pattern() != "LOL" {
		t.Errorf("specs[1] mismatch: %+v", specs[1])
	}
	if rs, ok := specs[2].(*ResolutionSpec); !ok || rs.Name != "r1" || !rs.Required || rs.Value != 1080 {
		t.Errorf("specs[2] mismatch: %+v", specs[2])
	}
	if ss, ok := specs[3].(*SourceSpec); !ok || ss.Value != int(quality.SourceBluray) {
		t.Errorf("specs[3] mismatch: %+v", specs[3])
	}
	if rts, ok := specs[4].(*ReleaseTypeSpec); !ok || rts.Value != int(parser.ReleaseTypeMultiEpisode) {
		t.Errorf("specs[4] mismatch: %+v", specs[4])
	}
	if ifs, ok := specs[5].(*IndexerFlagSpec); !ok || ifs.Value != int(parser.IndexerFlagFreeleech) {
		t.Errorf("specs[5] mismatch: %+v", specs[5])
	}
	if sz, ok := specs[6].(*SizeSpec); !ok || sz.Min != 1.5 || sz.Max != 10.0 {
		t.Errorf("specs[6] mismatch: %+v", specs[6])
	}
	if ls, ok := specs[7].(*LanguageSpec); !ok || ls.Value != language.English.ID || !ls.ExceptLanguage {
		t.Errorf("specs[7] mismatch: %+v", specs[7])
	}
}

func TestUnmarshalSpecs_UnknownDiscriminator(t *testing.T) {
	in := `[{"type":"FuturePotentialSpecification","body":{"name":"x"}}]`
	_, err := UnmarshalSpecs([]byte(in))
	if err == nil {
		t.Error("expected error for unknown discriminator")
	}
}

func TestUnmarshalSpecs_InvalidJSON(t *testing.T) {
	_, err := UnmarshalSpecs([]byte(`{not an array`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestUnmarshalSpecs_BadRegex(t *testing.T) {
	in := `[{"type":"ReleaseTitleSpecification","body":{"name":"bad","value":"(unclosed"}}]`
	_, err := UnmarshalSpecs([]byte(in))
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}

func TestMarshalSpecs_RoundTrip(t *testing.T) {
	rt := &ReleaseTitleSpec{
		BaseSpecification: BaseSpecification{Name: "HDTV-CF", Negate: false, Required: true},
	}
	if err := rt.SetPattern("HDTV"); err != nil {
		t.Fatal(err)
	}
	res := &ResolutionSpec{
		BaseSpecification: BaseSpecification{Name: "1080p-CF"},
		Value:             1080,
	}
	sz := &SizeSpec{
		BaseSpecification: BaseSpecification{Name: "size-CF"},
		Min:               1.0,
		Max:               5.0,
	}
	ls := &LanguageSpec{
		BaseSpecification: BaseSpecification{Name: "lang-CF"},
		Value:             language.English.ID,
		ExceptLanguage:    true,
	}

	original := []Specification{rt, res, sz, ls}
	encoded, err := MarshalSpecs(original)
	if err != nil {
		t.Fatal(err)
	}

	// Sanity-check the wire shape: must be a JSON array of
	// `{type, body}` envelopes.
	var raw []map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Fatalf("expected 4 envelopes, got %d", len(raw))
	}
	if raw[0]["type"] != "ReleaseTitleSpecification" {
		t.Errorf("envelope[0].type = %v, want ReleaseTitleSpecification", raw[0]["type"])
	}

	// Round-trip back through Unmarshal.
	roundTripped, err := UnmarshalSpecs(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTripped) != 4 {
		t.Fatalf("expected 4 specs after round-trip, got %d", len(roundTripped))
	}

	// Verify the round-tripped types + Name fields.
	if rt2, ok := roundTripped[0].(*ReleaseTitleSpec); !ok || rt2.Name != "HDTV-CF" || rt2.Pattern() != "HDTV" || !rt2.Required {
		t.Errorf("round-trip rt mismatch: %+v", roundTripped[0])
	}
	if r2, ok := roundTripped[1].(*ResolutionSpec); !ok || r2.Name != "1080p-CF" || r2.Value != 1080 {
		t.Errorf("round-trip res mismatch: %+v", roundTripped[1])
	}
	if sz2, ok := roundTripped[2].(*SizeSpec); !ok || sz2.Name != "size-CF" || sz2.Min != 1.0 || sz2.Max != 5.0 {
		t.Errorf("round-trip sz mismatch: %+v", roundTripped[2])
	}
	if ls2, ok := roundTripped[3].(*LanguageSpec); !ok || ls2.Name != "lang-CF" || !ls2.ExceptLanguage {
		t.Errorf("round-trip ls mismatch: %+v", roundTripped[3])
	}
}

func TestMarshalSpecs_UnknownType(t *testing.T) {
	// fakeSpec satisfies Specification but isn't one of the 8
	// catalogued types — exercises the encodeSpec default-case
	// error path.
	_, _, err := encodeSpec(&fakeSpec{})
	if err == nil {
		t.Error("expected error for unknown spec type")
	}
}

// fakeSpec satisfies Specification but isn't one of the 8 catalogued
// types. Used by TestMarshalSpecs_UnknownType to exercise the
// default-case error path.
type fakeSpec struct{ BaseSpecification }

func (s *fakeSpec) Order() int                             { return 99 }
func (s *fakeSpec) ImplementationName() string             { return "Fake" }
func (s *fakeSpec) IsSatisfiedBy(_ CustomFormatInput) bool { return false }
