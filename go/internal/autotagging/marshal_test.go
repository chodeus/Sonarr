package autotagging

import (
	"testing"
)

func TestUnmarshalSpecs_AllElevenTypes(t *testing.T) {
	in := `[
		{"type":"MonitoredSpecification","body":{"name":"m","negate":false,"required":false}},
		{"type":"QualityProfileSpecification","body":{"name":"qp","value":7}},
		{"type":"SeriesTypeSpecification","body":{"name":"st","value":2}},
		{"type":"StatusSpecification","body":{"name":"s","status":1}},
		{"type":"TagSpecification","body":{"name":"t","value":3}},
		{"type":"YearSpecification","body":{"name":"y","min":2010,"max":2020}},
		{"type":"GenreSpecification","body":{"name":"g","value":["Comedy","Drama"]}},
		{"type":"NetworkSpecification","body":{"name":"n","value":["NBC"]}},
		{"type":"OriginalCountrySpecification","body":{"name":"oc","value":["USA"]}},
		{"type":"OriginalLanguageSpecification","body":{"name":"ol","value":1}},
		{"type":"RootFolderSpecification","body":{"name":"rf","value":"/tv"}}
	]`
	specs, err := UnmarshalSpecs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 11 {
		t.Fatalf("expected 11 specs, got %d", len(specs))
	}

	// Spot-check each type + a key field.
	if _, ok := specs[0].(*MonitoredSpec); !ok {
		t.Errorf("specs[0] = %T, want *MonitoredSpec", specs[0])
	}
	if qp, ok := specs[1].(*QualityProfileSpec); !ok || qp.Value != 7 {
		t.Errorf("specs[1] mismatch: %+v", specs[1])
	}
	if st, ok := specs[2].(*SeriesTypeSpec); !ok || st.Value != 2 {
		t.Errorf("specs[2] mismatch: %+v", specs[2])
	}
	if s, ok := specs[3].(*StatusSpec); !ok || s.Status != 1 {
		t.Errorf("specs[3] mismatch: %+v", specs[3])
	}
	if tg, ok := specs[4].(*TagSpec); !ok || tg.Value != 3 {
		t.Errorf("specs[4] mismatch: %+v", specs[4])
	}
	if y, ok := specs[5].(*YearSpec); !ok || y.Min != 2010 || y.Max != 2020 {
		t.Errorf("specs[5] mismatch: %+v", specs[5])
	}
	if g, ok := specs[6].(*GenreSpec); !ok || len(g.Value) != 2 {
		t.Errorf("specs[6] mismatch: %+v", specs[6])
	}
	if n, ok := specs[7].(*NetworkSpec); !ok || n.Value[0] != "NBC" {
		t.Errorf("specs[7] mismatch: %+v", specs[7])
	}
	if oc, ok := specs[8].(*OriginalCountrySpec); !ok || oc.Value[0] != "USA" {
		t.Errorf("specs[8] mismatch: %+v", specs[8])
	}
	if ol, ok := specs[9].(*OriginalLanguageSpec); !ok || ol.Value != 1 {
		t.Errorf("specs[9] mismatch: %+v", specs[9])
	}
	if rf, ok := specs[10].(*RootFolderSpec); !ok || rf.Value != "/tv" {
		t.Errorf("specs[10] mismatch: %+v", specs[10])
	}
}

func TestUnmarshalSpecs_EmptyInput(t *testing.T) {
	specs, err := UnmarshalSpecs(nil)
	if err != nil || specs != nil {
		t.Errorf("nil → (nil, nil)")
	}
}

func TestUnmarshalSpecs_UnknownDiscriminator(t *testing.T) {
	_, err := UnmarshalSpecs([]byte(`[{"type":"FuturePotentialSpecification","body":{"name":"x"}}]`))
	if err == nil {
		t.Error("expected error for unknown discriminator")
	}
}

func TestMarshalSpecs_RoundTrip(t *testing.T) {
	original := []Specification{
		&MonitoredSpec{BaseSpecification: BaseSpecification{Name: "m1"}},
		&YearSpec{BaseSpecification: BaseSpecification{Name: "y1"}, Min: 2010, Max: 2020},
		&GenreSpec{BaseSpecification: BaseSpecification{Name: "g1", Negate: true}, Value: []string{"Drama"}},
		&StatusSpec{BaseSpecification: BaseSpecification{Name: "s1", Required: true}, Status: 1},
	}
	encoded, err := MarshalSpecs(original)
	if err != nil {
		t.Fatal(err)
	}

	roundTripped, err := UnmarshalSpecs(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTripped) != 4 {
		t.Fatalf("expected 4 specs after round-trip, got %d", len(roundTripped))
	}

	if m, ok := roundTripped[0].(*MonitoredSpec); !ok || m.Name != "m1" {
		t.Errorf("round-trip [0]: %+v", roundTripped[0])
	}
	if y, ok := roundTripped[1].(*YearSpec); !ok || y.Min != 2010 || y.Max != 2020 {
		t.Errorf("round-trip [1]: %+v", roundTripped[1])
	}
	if g, ok := roundTripped[2].(*GenreSpec); !ok || !g.Negate || len(g.Value) != 1 {
		t.Errorf("round-trip [2]: %+v", roundTripped[2])
	}
	if s, ok := roundTripped[3].(*StatusSpec); !ok || !s.Required || s.Status != 1 {
		t.Errorf("round-trip [3]: %+v", roundTripped[3])
	}
}
