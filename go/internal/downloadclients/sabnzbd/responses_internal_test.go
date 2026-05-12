package sabnzbd

import (
	"encoding/json"
	"testing"
)

// TestQueueTimeleft_UnmarshalJSON locks the C# QueueTime converter
// parity at `JsonConverters/SabnzbdQueueTimeConverter.cs:1-34`:
// accepts H:M:S and D:H:M:S; empty/null → zero; bogus → error.
// Slice 4.BS (C-cleanup sub-arc C2a).
func TestQueueTimeleft_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	type wrap struct {
		T queueTimeleft `json:"t"`
	}
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{`"0:01:30"`, "1m30s", false},
		{`"1:0:0"`, "1h0m0s", false},
		{`"2:1:30:00"`, "49h30m0s", false},
		{`""`, "0s", false},
		{`null`, "0s", false},
		{`"not-a-time"`, "", true},
		{`"1:2"`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			var w wrap
			err := json.Unmarshal([]byte(`{"t":`+tc.in+`}`), &w)
			if tc.wantErr {
				if err == nil {
					t.Errorf("in=%s want err, got nil (parsed=%v)", tc.in, w.T.Duration())
				}
				return
			}
			if err != nil {
				t.Fatalf("in=%s err=%v", tc.in, err)
			}
			if w.T.Duration().String() != tc.want {
				t.Errorf("in=%s parsed=%s, want %s", tc.in, w.T.Duration(), tc.want)
			}
		})
	}
}

// TestStringArrayOrString_AcceptsBothShapes locks the C#
// SabnzbdStringArrayConverter parity at `JsonConverters/
// SabnzbdStringArrayConverter.cs:1-50`.
func TestStringArrayOrString_AcceptsBothShapes(t *testing.T) {
	t.Parallel()
	type wrap struct {
		V stringArrayOrString `json:"v"`
	}
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{`null`, nil, false},
		{`"daily"`, []string{"daily"}, false},
		{`["daily","weekly"]`, []string{"daily", "weekly"}, false},
		{`42`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			var w wrap
			err := json.Unmarshal([]byte(`{"v":`+tc.in+`}`), &w)
			if tc.wantErr {
				if err == nil {
					t.Errorf("in=%s want err", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("in=%s err=%v", tc.in, err)
			}
			if !slicesEqual(w.V, tc.want) {
				t.Errorf("in=%s got=%v want=%v", tc.in, []string(w.V), tc.want)
			}
		})
	}
}

func slicesEqual(a stringArrayOrString, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
