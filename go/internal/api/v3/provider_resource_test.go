package v3

import (
	"encoding/json"
	"testing"
)

// J425 (G4): fieldsFromSettingsJSON decodes a Settings JSON object into a
// name-sorted fields[] array, and is the inverse of settingsJSONFromFields.
func TestFieldsFromSettingsJSON_J425(t *testing.T) {
	t.Parallel()

	got := fieldsFromSettingsJSON(`{"port":8080,"host":"box"}`)
	if len(got) != 2 {
		t.Fatalf("got %d fields, want 2", len(got))
	}
	// Sorted by name: host before port.
	if got[0].Name != "host" || got[1].Name != "port" {
		t.Errorf("not name-sorted: %+v", got)
	}
	if got[0].Value != "box" {
		t.Errorf("host value = %v, want box", got[0].Value)
	}

	// Empty / "{}" / invalid → nil (→ omitempty drops the key).
	for _, in := range []string{"", "  ", "{}", "not json", "[]"} {
		if f := fieldsFromSettingsJSON(in); f != nil {
			t.Errorf("fieldsFromSettingsJSON(%q) = %v, want nil", in, f)
		}
	}
}

// Round-trip: fields → settings JSON → fields preserves the name/value set.
func TestProviderFieldsRoundTrip_J425(t *testing.T) {
	t.Parallel()
	in := []ProviderField{{Name: "apiKey", Value: "abc"}, {Name: "baseUrl", Value: "http://x"}}
	j, err := settingsJSONFromFields(in)
	if err != nil {
		t.Fatal(err)
	}
	back := fieldsFromSettingsJSON(j)
	// Compare as maps (order differs: input order vs sorted).
	want := map[string]any{"apiKey": "abc", "baseUrl": "http://x"}
	gotM := map[string]any{}
	for _, f := range back {
		gotM[f.Name] = f.Value
	}
	wb, _ := json.Marshal(want)
	gb, _ := json.Marshal(gotM)
	if string(wb) != string(gb) {
		t.Errorf("round-trip mismatch: got %s want %s", gb, wb)
	}
}

func TestDerefSettings_J425(t *testing.T) {
	t.Parallel()
	if derefSettings(nil) != "" {
		t.Error("nil → empty string")
	}
	s := "x"
	if derefSettings(&s) != "x" {
		t.Error("*string deref")
	}
}
