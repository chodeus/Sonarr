package v3

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFieldDef_Marshal_OmitsValueByDefault(t *testing.T) {
	t.Parallel()
	// Bare struct literal — ValueSet=false → "value" key absent.
	f := FieldDef{Order: 0, Name: "x", Label: "X", Type: "textbox", Privacy: "normal"}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"value":`) {
		t.Errorf("default field should omit value: %s", b)
	}
}

func TestFieldDef_Marshal_EmitsValueWhenSet(t *testing.T) {
	t.Parallel()
	// WithValue(0) → ValueSet=true → "value":0 emits even though the
	// underlying int is the zero value. Mirrors C#
	// JsonIgnoreCondition.WhenWritingNull which doesn't fire for
	// non-null primitives.
	f := FieldDef{Order: 0, Name: "value", Label: "X", Type: "select", Privacy: "normal"}.WithValue(0)
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"value":0`) {
		t.Errorf(`should emit "value":0: %s`, b)
	}
}

func TestFieldDef_Marshal_EmitsValueFalse(t *testing.T) {
	t.Parallel()
	f := FieldDef{Name: "x", Label: "X", Type: "checkbox", Privacy: "normal"}.WithValue(false)
	b, _ := json.Marshal(f)
	if !strings.Contains(string(b), `"value":false`) {
		t.Errorf(`should emit "value":false: %s`, b)
	}
}

func TestCustomFormatSchema_TopLevelEmitsEmptyPresets(t *testing.T) {
	t.Parallel()
	// Every entry in customFormatSchemas must emit `presets` on the
	// wire (the C# `.ToList()` yields a non-null empty list). The
	// nil-Presets coercion in the init block guarantees this.
	for _, s := range customFormatSchemas {
		b, _ := json.Marshal(s)
		if !strings.Contains(string(b), `"presets":`) {
			t.Errorf("%s: top-level should emit presets key: %s", s.Implementation, b)
		}
	}
}

func TestCustomFormatSchema_PresetItemOmitsPresets(t *testing.T) {
	t.Parallel()
	// Inner preset entries (the ReleaseTitleSpec.Presets slice) must
	// omit `presets` entirely — Go's *[]Foo with omitempty drops nil
	// pointer.
	for _, s := range customFormatSchemas {
		if s.Presets == nil {
			continue
		}
		for _, p := range *s.Presets {
			if p.Presets != nil {
				t.Errorf("inner preset must have nil Presets: %s/%s", s.Implementation, p.Name)
			}
			b, _ := json.Marshal(p)
			if strings.Contains(string(b), `"presets":`) {
				t.Errorf("inner preset must omit presets key: %s/%s body=%s", s.Implementation, p.Name, b)
			}
		}
	}
}

func TestCustomFormatSchema_PinnedCount(t *testing.T) {
	t.Parallel()
	// 8 ported HEAD specs + 3 MediaInfo-based conditions (HDR Format,
	// Audio Codec, Audio Channels — Slice A forward-divergence).
	if got := len(customFormatSchemas); got != 11 {
		t.Errorf("customFormatSchemas count: got %d want 11", got)
	}
}

func TestCustomFormatSchema_MediaInfoSpecsPresent(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"HdrFormatSpecification":     false,
		"AudioCodecSpecification":    false,
		"AudioChannelsSpecification": false,
	}
	for _, s := range customFormatSchemas {
		if _, ok := want[s.Implementation]; ok {
			want[s.Implementation] = true
		}
	}
	for impl, found := range want {
		if !found {
			t.Errorf("customFormatSchemas missing MediaInfo spec %q", impl)
		}
	}
}
