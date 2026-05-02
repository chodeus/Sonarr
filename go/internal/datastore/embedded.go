package datastore

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file owns the wire format for embedded-document JSON columns.
// Sonarr's C# side persists these via System.Text.Json (NOT Newtonsoft —
// see analysis/embedded-docs/catalogue.md for the correction history)
// with this exact configuration:
//
//   AllowTrailingCommas = true
//   DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
//   PropertyNameCaseInsensitive = true
//   DictionaryKeyPolicy = JsonNamingPolicy.CamelCase
//   PropertyNamingPolicy = JsonNamingPolicy.CamelCase
//   WriteIndented = true
//   Converters: enum (camelCase, allowIntegerValues=true), Version,
//               HttpUri, TimeSpan, UTC DateTime
//
// Go's encoding/json gives us:
//
//   - camelCase keys via per-field `json:"camelName"` tags. Callers
//     MUST tag every persisted field; we don't synthesize names.
//   - Indented output via json.MarshalIndent("", "  ").
//   - WhenWritingNull is approximated by `omitempty` ONLY on pointer,
//     slice, map, interface, and string fields. Numeric zero / boolean
//     false get written explicitly because C# WhenWritingNull does
//     not omit value-type defaults.
//   - DateTime and TimeSpan need bespoke types — STTime and STDuration
//     below — because Go's default time.Time JSON output uses RFC3339
//     with fractional seconds and a numeric offset, not the C# UTC-Z
//     literal format.
//
// Enum-as-string + Version + HttpUri converters are not yet ported.
// They land when the first model with such a field needs them. This
// file's contract: the JSON bytes we produce for plain structs of
// primitive + slice + STTime + STDuration fields match the C# bytes
// for the equivalent class shape.

// MarshalEmbedded serializes v as JSON for storage in an embedded-doc
// TEXT column. Output is camelCase (driven by struct tags) and indented
// with two spaces, matching the C# STJson WriteIndented default.
//
// Pass nil to get the empty-array / empty-object literal "null". Callers
// that want "[]" for an unset slice should marshal an empty slice
// explicitly, not nil.
func MarshalEmbedded(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("datastore: marshal embedded: %w", err)
	}
	return string(b), nil
}

// UnmarshalEmbedded parses JSON text from a TEXT column into v. Empty or
// whitespace-only input is treated as "no value" — v is left at its zero
// value with no error. This matches how the C# bridge reads NULL or empty
// columns: the deserializer hands back default(T).
func UnmarshalEmbedded(s string, v any) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(trimmed), v); err != nil {
		return fmt.Errorf("datastore: unmarshal embedded: %w", err)
	}
	return nil
}

// STTime is the wire-format-stable wrapper for DateTime fields. Encodes
// as `"yyyy-MM-ddTHH:mm:ssZ"` UTC, no fractional seconds, no offset —
// matching STJUtcConverter in the C# tree. Decoding accepts that exact
// format plus standard RFC3339 variants for forward-compat.
//
// The zero value marshals as a JSON null when its parent field uses
// `omitempty` on a *STTime, or as `"0001-01-01T00:00:00Z"` if it's a
// non-pointer field. Callers should match the C# nullability of each
// field with a Go pointer when the column is nullable.
type STTime time.Time

// stTimeFormat is the exact STJUtcConverter output format. Go's reference
// time `Mon Jan 2 15:04:05 MST 2006` makes the string-template form below
// equivalent to C#'s `yyyy'-'MM'-'dd'T'HH':'mm':'ssZ`.
const stTimeFormat = `2006-01-02T15:04:05Z`

// MarshalJSON implements json.Marshaler.
func (t STTime) MarshalJSON() ([]byte, error) {
	tm := time.Time(t).UTC()
	// Quote the formatted string. encoding/json expects valid JSON.
	return []byte(`"` + tm.Format(stTimeFormat) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler. Accepts the canonical
// STJUtcConverter output plus full RFC3339 (with fractional seconds and
// numeric offsets) since older C# rows or hand-edited JSON may use it.
func (t *STTime) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*t = STTime(time.Time{})
		return nil
	}
	s := strings.Trim(string(b), `"`)
	if s == "" {
		*t = STTime(time.Time{})
		return nil
	}
	for _, layout := range []string{stTimeFormat, time.RFC3339, time.RFC3339Nano} {
		if parsed, err := time.Parse(layout, s); err == nil {
			*t = STTime(parsed.UTC())
			return nil
		}
	}
	return fmt.Errorf("datastore: STTime: cannot parse %q", s)
}

// Time returns the underlying time.Time in UTC.
func (t STTime) Time() time.Time { return time.Time(t).UTC() }

// STDuration is the wire-format-stable wrapper for TimeSpan fields. The
// canonical C# TimeSpan.ToString() output is `[-][d'.']hh':'mm':'ss['.'fffffff]`
// where days and fractional seconds are present only when non-zero.
// We emit the canonical form on write; on read we accept the full grammar
// including a leading negative sign, optional days prefix, and optional
// fractional seconds (1-7 digits matching .NET ticks resolution).
type STDuration time.Duration

// MarshalJSON implements json.Marshaler. Output mirrors C# TimeSpan.
func (d STDuration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + formatTimeSpan(time.Duration(d)) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *STDuration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*d = 0
		return nil
	}
	s := strings.Trim(string(b), `"`)
	if s == "" {
		*d = 0
		return nil
	}
	parsed, err := parseTimeSpan(s)
	if err != nil {
		return fmt.Errorf("datastore: STDuration: cannot parse %q: %w", s, err)
	}
	*d = STDuration(parsed)
	return nil
}

// Duration returns the underlying time.Duration.
func (d STDuration) Duration() time.Duration { return time.Duration(d) }

// FormatDuration emits the canonical TimeSpan string the C# side stores
// in scalar text columns like Commands.Duration. Use this for SQL writes
// of TimeSpan-typed columns; STDuration handles the same format inside
// JSON embedded documents.
func FormatDuration(d time.Duration) string { return formatTimeSpan(d) }

// ParseDuration parses the same TimeSpan grammar FormatDuration emits.
// Use this for SQL reads of TimeSpan-typed columns.
func ParseDuration(s string) (time.Duration, error) { return parseTimeSpan(s) }

// formatTimeSpan emits the canonical `[-][d'.']hh:mm:ss['.'fffffff]` form.
// Sub-second nanoseconds are encoded as 7 digits matching the .NET ticks
// granularity (100ns); higher-precision Go nanoseconds are truncated.
func formatTimeSpan(d time.Duration) string {
	negative := d < 0
	if negative {
		d = -d
	}
	const day = 24 * time.Hour
	days := int64(d / day)
	d -= time.Duration(days) * day
	hours := int64(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int64(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int64(d / time.Second)
	d -= time.Duration(secs) * time.Second
	// .NET ticks: 1 tick = 100 ns; 7 fractional digits represent ticks-of-second.
	ticks := int64(d / 100)

	var sb strings.Builder
	if negative {
		sb.WriteByte('-')
	}
	if days > 0 {
		sb.WriteString(strconv.FormatInt(days, 10))
		sb.WriteByte('.')
	}
	fmt.Fprintf(&sb, "%02d:%02d:%02d", hours, mins, secs)
	if ticks > 0 {
		// Trim trailing zeros for tighter output? .NET keeps all 7 digits
		// when fractional seconds are present; we match that.
		fmt.Fprintf(&sb, ".%07d", ticks)
	}
	return sb.String()
}

// parseTimeSpan parses the same grammar the formatter emits.
func parseTimeSpan(s string) (time.Duration, error) {
	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	}
	// Optional "{d}." prefix.
	var days int64
	if dot := strings.Index(s, "."); dot >= 0 {
		// "." can be either days separator or fractional-seconds separator.
		// If the part before the first dot has no colons, it's days; else
		// the dot is fractional.
		head := s[:dot]
		if !strings.Contains(head, ":") {
			d, err := strconv.ParseInt(head, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("days: %w", err)
			}
			days = d
			s = s[dot+1:]
		}
	}
	// Now s is "hh:mm:ss" or "hh:mm:ss.fffffff".
	var fraction string
	if dot := strings.Index(s, "."); dot >= 0 {
		fraction = s[dot+1:]
		s = s[:dot]
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("expected hh:mm:ss, got %q", s)
	}
	hours, err1 := strconv.ParseInt(parts[0], 10, 64)
	mins, err2 := strconv.ParseInt(parts[1], 10, 64)
	secs, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("invalid hh/mm/ss in %q", s)
	}
	out := time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(mins)*time.Minute +
		time.Duration(secs)*time.Second
	if fraction != "" {
		// Pad/truncate to 9 digits (Go nanoseconds), then parse.
		const want = 9
		switch {
		case len(fraction) > want:
			fraction = fraction[:want]
		case len(fraction) < want:
			fraction += strings.Repeat("0", want-len(fraction))
		}
		nanos, err := strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("fractional: %w", err)
		}
		out += time.Duration(nanos)
	}
	if negative {
		out = -out
	}
	return out, nil
}
