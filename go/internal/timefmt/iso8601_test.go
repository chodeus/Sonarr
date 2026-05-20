package timefmt_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/timefmt"
)

func TestISO8601_MarshalSecondPrecision(t *testing.T) {
	t.Parallel()
	// 2026-05-02T06:56:55.123456789Z → emits without subseconds.
	in := time.Date(2026, 5, 2, 6, 56, 55, 123456789, time.UTC)
	b, err := json.Marshal(timefmt.FromTime(in))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `"2026-05-02T06:56:55Z"`
	if string(b) != want {
		t.Errorf("Marshal: got %s, want %s", b, want)
	}
}

func TestISO8601_MarshalUTCNormalises(t *testing.T) {
	t.Parallel()
	// Local-zone time should serialise in UTC.
	utc := time.Date(2026, 5, 2, 18, 0, 0, 0, time.UTC)
	local := utc.Local()
	b, err := json.Marshal(timefmt.FromTime(local))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasSuffix(string(b), `Z"`) {
		t.Errorf("expected Z suffix; got %s", b)
	}
}

func TestISO8601_MarshalZero(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(timefmt.ISO8601(time.Time{}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `"0001-01-01T00:00:00Z"`
	if string(b) != want {
		t.Errorf("Marshal zero: got %s, want %s", b, want)
	}
}

func TestISO8601_UnmarshalRFC3339Nano(t *testing.T) {
	t.Parallel()
	var got timefmt.ISO8601
	if err := json.Unmarshal([]byte(`"2026-05-02T06:56:55.123456789Z"`), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := time.Date(2026, 5, 2, 6, 56, 55, 0, time.UTC)
	if !got.Time().Equal(want) {
		t.Errorf("Unmarshal: got %v want %v", got.Time(), want)
	}
}

func TestISO8601_UnmarshalSecond(t *testing.T) {
	t.Parallel()
	var got timefmt.ISO8601
	if err := json.Unmarshal([]byte(`"2026-05-02T06:56:55Z"`), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Time().Second() != 55 {
		t.Errorf("Unmarshal second-precision: got %v", got.Time())
	}
}

func TestISO8601_UnmarshalNull(t *testing.T) {
	t.Parallel()
	var got timefmt.ISO8601
	if err := json.Unmarshal([]byte(`null`), &got); err != nil {
		t.Fatalf("Unmarshal null: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("Unmarshal null should yield zero; got %v", got.Time())
	}
}

func TestISO8601_UnmarshalEmpty(t *testing.T) {
	t.Parallel()
	var got timefmt.ISO8601
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("Unmarshal empty should yield zero; got %v", got.Time())
	}
}

func TestISO8601_UnmarshalInvalid(t *testing.T) {
	t.Parallel()
	var got timefmt.ISO8601
	if err := json.Unmarshal([]byte(`"not a time"`), &got); err == nil {
		t.Errorf("Unmarshal garbage should error")
	}
}

func TestISO8601_NowSecond(t *testing.T) {
	t.Parallel()
	got := timefmt.Now()
	if got.Time().Nanosecond() != 0 {
		t.Errorf("Now() should be second-truncated; got nano=%d", got.Time().Nanosecond())
	}
	if got.Time().Location() != time.UTC {
		t.Errorf("Now() should be UTC; got %v", got.Time().Location())
	}
}
