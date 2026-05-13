package main

import (
	"runtime"
	"testing"
	"time"
)

func TestParseBuildTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"unknown", "unknown", time.Time{}},
		{"empty", "", time.Time{}},
		{"valid_rfc3339", "2026-05-01T12:34:56Z", time.Date(2026, 5, 1, 12, 34, 56, 0, time.UTC)},
		{"invalid", "garbage", time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBuildTime(tc.in); !got.Equal(tc.want) {
				t.Errorf("parseBuildTime(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestOsDisplayName(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"darwin", "Darwin"},
		{"linux", "Linux"},
		{"windows", "Windows"},
		{"freebsd", "FreeBSD"},
		{"plan9", "Plan9"},
	}
	for _, tc := range cases {
		if got := osDisplayName(tc.in); got != tc.want {
			t.Errorf("osDisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOsDisplayName_MatchesHostRuntime(t *testing.T) {
	t.Parallel()
	got := osDisplayName(runtime.GOOS)
	if got == "" {
		t.Fatalf("osDisplayName(%q) returned empty", runtime.GOOS)
	}
}

func TestFileExists(t *testing.T) {
	t.Parallel()
	if !fileExists("/") {
		t.Error("root / should exist")
	}
	if fileExists("/this/path/does/not/exist/at/all") {
		t.Error("nonexistent path should not exist")
	}
}
