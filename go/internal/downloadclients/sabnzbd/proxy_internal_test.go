package sabnzbd

import "testing"

// TestJsonError_Failed_CaseInsensitive locks the case-insensitive
// match against "false" that mirrors the C# `SabnzbdJsonError.Failed`
// getter at `SabnzbdJsonError.cs:10-11`
// (`Status.Equals("false", InvariantCultureIgnoreCase)`).
// Cleanup arc slice 4.BM corrected the original case-sensitive port.
func TestJsonError_Failed_CaseInsensitive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{"LowerFalse", "false", true},
		{"UpperFalse", "FALSE", true},
		{"MixedFalse", "False", true},
		{"True", "true", false},
		{"Empty", "", false},
		{"Whitespace", "   ", false},
		{"Other", "ok", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := jsonError{Status: tc.status}
			if got := e.Failed(); got != tc.want {
				t.Fatalf("Failed() with Status=%q = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
