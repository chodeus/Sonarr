package downloadclients_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// TestIsLocalhostHost covers the J162 canonical detector consolidated
// from 7 duplicated `isLocalHost` package-private helpers. Pins the
// case-insensitive + trim + IPv6-loopback semantics so any future
// edit that narrows / widens the check trips a regression.
func TestIsLocalhostHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// True branch — the three canonical shapes plus the case +
		// whitespace tolerances the helper deliberately adds.
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"name", "localhost", true},
		{"name uppercase", "LOCALHOST", true},
		{"name mixed case", "LocalHost", true},
		{"name padded whitespace", "  localhost  ", true},

		// False branch — common non-loopback inputs the audit-flag
		// scenario worries about (a remote DC misclassified as local).
		{"empty", "", false},
		{"whitespace-only", "   ", false},
		{"non-loopback ipv4", "192.168.1.50", false},
		{"non-loopback ipv6", "fe80::1", false},
		{"public DNS", "downloads.example.com", false},
		{"host:port shape (not split)", "127.0.0.1:8080", false}, // intentionally NOT loopback — aria2 has its own Contains-based helper
		{"loopback substring inside hostname", "127.0.0.1.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := downloadclients.IsLocalhostHost(tc.in); got != tc.want {
				t.Errorf("IsLocalhostHost(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
