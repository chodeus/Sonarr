// Package timefmt provides time-formatting helpers that match the
// C# Sonarr wire shape — primarily second-precision ISO 8601 in
// UTC. C# `DateTime.ToString("o")` (round-trip format) emits
// nanosecond-level precision when the input has subseconds, but
// Sonarr's actual emitted timestamps are second-precision because
// they originate from a SQLite-stored DATETIME column that drops
// subseconds. Go's `time.Time.MarshalJSON` uses RFC3339Nano which
// WOULD emit subseconds if a Go-port code path constructs a
// `time.Time` from `time.Now()` and serializes it directly. The
// wrapper here keeps the wire shape stable regardless.
package timefmt

import (
	"strconv"
	"time"
)

// ISO8601 is a `time.Time` wrapper whose JSON encoding always
// truncates to second precision and uses the UTC zone, matching the
// recorded contract goldens (e.g. `"2026-05-02T06:56:55Z"`). Used
// on wire-emission paths whose source time is `time.Now()` or
// otherwise carries non-zero subseconds.
//
// Compatible with `encoding/json` via Marshaler/Unmarshaler. The
// underlying value is reachable via `(ISO8601)(t).Time()` for code
// that needs the original time.Time.
//
// Audit fix C.2.
type ISO8601 time.Time

// Time returns the underlying time.Time.
func (t ISO8601) Time() time.Time { return time.Time(t) }

// IsZero reports whether the underlying time is the zero value.
func (t ISO8601) IsZero() bool { return time.Time(t).IsZero() }

// MarshalJSON emits the time as `"YYYY-MM-DDTHH:MM:SSZ"` (second
// precision, UTC). Zero value emits `"0001-01-01T00:00:00Z"` —
// matches the C# `DateTime.MinValue.ToString("yyyy-MM-ddTHH:mm:ssZ")`
// output that surfaces in some recorded goldens.
func (t ISO8601) MarshalJSON() ([]byte, error) {
	tt := time.Time(t).UTC().Truncate(time.Second)
	out := make([]byte, 0, 22)
	out = append(out, '"')
	out = tt.AppendFormat(out, "2006-01-02T15:04:05Z")
	out = append(out, '"')
	return out, nil
}

// UnmarshalJSON accepts both RFC3339Nano (subsecond) and the
// second-precision form, truncating on read so internal arithmetic
// stays at second resolution. `null` decodes to the zero value.
func (t *ISO8601) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		// Not a quoted string — could be `null`.
		if string(b) == "null" {
			*t = ISO8601(time.Time{})
			return nil
		}
		return err
	}
	if s == "" {
		*t = ISO8601(time.Time{})
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fall back to the second-precision shape.
		parsed, err = time.Parse("2006-01-02T15:04:05Z", s)
		if err != nil {
			return err
		}
	}
	*t = ISO8601(parsed.UTC().Truncate(time.Second))
	return nil
}

// Now returns the current UTC time truncated to second precision.
// Use this in place of `time.Now()` at every site whose emitted
// time hits a wire-shape contract.
func Now() ISO8601 {
	return ISO8601(time.Now().UTC().Truncate(time.Second))
}

// FromTime wraps a time.Time as ISO8601 (truncating to second
// precision + UTC at construction so subsequent arithmetic on the
// wrapper stays consistent).
func FromTime(t time.Time) ISO8601 {
	return ISO8601(t.UTC().Truncate(time.Second))
}
