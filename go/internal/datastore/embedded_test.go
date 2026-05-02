package datastore

import (
	"testing"
	"time"
)

func TestSTTime_MarshalJSON_FormatExact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"utc midnight", time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), `"2024-01-15T00:00:00Z"`},
		{"utc with time", time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC), `"2024-01-15T10:30:45Z"`},
		{"non-utc converted to utc", time.Date(2024, 1, 15, 2, 0, 0, 0, time.FixedZone("EST", -5*3600)), `"2024-01-15T07:00:00Z"`},
		{"sub-second truncated", time.Date(2024, 1, 15, 0, 0, 0, 999_999_999, time.UTC), `"2024-01-15T00:00:00Z"`},
		{"zero", time.Time{}, `"0001-01-01T00:00:00Z"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := STTime(c.in).MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestSTTime_UnmarshalJSON_AcceptsFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"canonical", `"2024-01-15T10:30:45Z"`, time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)},
		{"rfc3339 with offset", `"2024-01-15T10:30:45+02:00"`, time.Date(2024, 1, 15, 8, 30, 45, 0, time.UTC)},
		{"rfc3339 nano", `"2024-01-15T10:30:45.123456789Z"`, time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)},
		{"null", `null`, time.Time{}},
		{"empty string", `""`, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got STTime
			if err := got.UnmarshalJSON([]byte(c.in)); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if !got.Time().Equal(c.want) {
				t.Errorf("got %v, want %v", got.Time(), c.want)
			}
		})
	}
}

func TestSTTime_RoundTrip(t *testing.T) {
	t.Parallel()
	original := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	encoded, err := STTime(original).MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var decoded STTime
	if err := decoded.UnmarshalJSON(encoded); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if !decoded.Time().Equal(original) {
		t.Errorf("round-trip drift: %v vs %v", decoded.Time(), original)
	}
}

func TestSTDuration_MarshalJSON_FormatExact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"zero", 0, `"00:00:00"`},
		{"one second", time.Second, `"00:00:01"`},
		{"one minute", time.Minute, `"00:01:00"`},
		{"one hour", time.Hour, `"01:00:00"`},
		{"complex", 3*time.Hour + 5*time.Minute + 7*time.Second, `"03:05:07"`},
		{"23h59m59s", 23*time.Hour + 59*time.Minute + 59*time.Second, `"23:59:59"`},
		{"one day", 24 * time.Hour, `"1.00:00:00"`},
		{"day plus", 25*time.Hour + 30*time.Minute, `"1.01:30:00"`},
		{"negative", -1 * time.Hour, `"-01:00:00"`},
		{"negative with days", -(25 * time.Hour), `"-1.01:00:00"`},
		{"with fractional millisec", 1500 * time.Millisecond, `"00:00:01.5000000"`},
		{"sub-millisec ticks", 1*time.Second + 100*time.Nanosecond, `"00:00:01.0000001"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := STDuration(c.in).MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestSTDuration_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"zero", `"00:00:00"`, 0},
		{"hh:mm:ss", `"01:23:45"`, time.Hour + 23*time.Minute + 45*time.Second},
		{"with days", `"1.02:00:00"`, 26 * time.Hour},
		{"negative", `"-00:30:00"`, -30 * time.Minute},
		{"fractional", `"00:00:01.5"`, 1500 * time.Millisecond},
		{"7-digit fractional", `"00:00:01.5000000"`, 1500 * time.Millisecond},
		{"null", `null`, 0},
		{"empty", `""`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got STDuration
			if err := got.UnmarshalJSON([]byte(c.in)); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if got.Duration() != c.want {
				t.Errorf("got %v, want %v", got.Duration(), c.want)
			}
		})
	}
}

func TestSTDuration_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{
		0, time.Second, 90 * time.Minute, 25 * time.Hour, -3 * time.Hour, 1500 * time.Millisecond,
	} {
		encoded, err := STDuration(d).MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON %v: %v", d, err)
		}
		var decoded STDuration
		if err := decoded.UnmarshalJSON(encoded); err != nil {
			t.Fatalf("UnmarshalJSON %v: %v", d, err)
		}
		if decoded.Duration() != d {
			t.Errorf("round-trip drift: %v -> %s -> %v", d, encoded, decoded.Duration())
		}
	}
}

// TestMarshalEmbedded_CamelCaseAndIndent locks in the byte-level wire
// format we produce for a representative embedded-doc payload. The shape
// mirrors what Series.Seasons looks like ([{seasonNumber, monitored}])
// — the simplest concrete example from the analysis.
func TestMarshalEmbedded_CamelCaseAndIndent(t *testing.T) {
	t.Parallel()
	type Season struct {
		SeasonNumber int  `json:"seasonNumber"`
		Monitored    bool `json:"monitored"`
	}
	in := []Season{{0, false}, {1, true}, {2, false}}
	got, err := MarshalEmbedded(in)
	if err != nil {
		t.Fatalf("MarshalEmbedded: %v", err)
	}
	want := `[
  {
    "seasonNumber": 0,
    "monitored": false
  },
  {
    "seasonNumber": 1,
    "monitored": true
  },
  {
    "seasonNumber": 2,
    "monitored": false
  }
]`
	if got != want {
		t.Errorf("MarshalEmbedded mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestMarshalEmbedded_OmitNilNotZero exercises the WhenWritingNull
// approximation: pointer/slice/map fields with nil values are omitted,
// but value-type zeros are NOT omitted (matching C# semantics where
// `int Foo = 0` and `bool Bar = false` are written explicitly).
func TestMarshalEmbedded_OmitNilNotZero(t *testing.T) {
	t.Parallel()
	type Inner struct{ X int }
	type Sample struct {
		// Value-type zeros — must be written.
		Count   int  `json:"count"`
		Enabled bool `json:"enabled"`
		// Nullable pointers — omitted when nil.
		Optional *Inner `json:"optional,omitempty"`
		// Slice — omitted when nil.
		Names []string `json:"names,omitempty"`
	}
	got, err := MarshalEmbedded(Sample{})
	if err != nil {
		t.Fatalf("MarshalEmbedded: %v", err)
	}
	want := `{
  "count": 0,
  "enabled": false
}`
	if got != want {
		t.Errorf("zero-value Sample\n got: %s\nwant: %s", got, want)
	}
}

func TestUnmarshalEmbedded_EmptyInputZeroValue(t *testing.T) {
	t.Parallel()
	var s []int
	for _, in := range []string{"", "  ", "null", "\n\t"} {
		s = []int{1, 2, 3}
		if err := UnmarshalEmbedded(in, &s); err != nil {
			t.Errorf("UnmarshalEmbedded(%q): %v", in, err)
		}
		// We left s alone; it should remain whatever was passed in.
		// (The function does NOT zero v on empty input — that's the
		// Go convention. The caller's variable starts at its zero
		// value or whatever the caller put there.)
		if len(s) != 3 {
			t.Errorf("UnmarshalEmbedded(%q) mutated s: got %v", in, s)
		}
	}
}

func TestUnmarshalEmbedded_RoundTripStruct(t *testing.T) {
	t.Parallel()
	type Inner struct {
		Name string `json:"name"`
		Tags []int  `json:"tags"`
	}
	in := Inner{Name: "drama", Tags: []int{1, 2, 3}}
	encoded, err := MarshalEmbedded(in)
	if err != nil {
		t.Fatalf("MarshalEmbedded: %v", err)
	}
	var out Inner
	if err := UnmarshalEmbedded(encoded, &out); err != nil {
		t.Fatalf("UnmarshalEmbedded: %v", err)
	}
	if out.Name != "drama" || len(out.Tags) != 3 || out.Tags[0] != 1 {
		t.Errorf("round-trip drift: %+v", out)
	}
}
