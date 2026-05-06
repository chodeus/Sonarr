package mediafiles

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

// TestFileExtensionsCount pins the registry size — anything that adds
// or removes an entry tickles this so the C# tree drift is loud.
func TestFileExtensionsCount(t *testing.T) {
	const want = 46
	if got := len(fileExtensions); got != want {
		t.Fatalf("fileExtensions size: got %d want %d (drift vs MediaFileExtensions.cs)", got, want)
	}
}

// TestIsMediaExtension covers case-insensitivity, registered/unregistered
// extensions, edge cases. Mirrors the matching predicate in C#'s
// `MediaFileExtensions.Extensions.Contains(...)` HashSet lookup.
func TestIsMediaExtension(t *testing.T) {
	cases := []struct {
		ext  string
		want bool
	}{
		{".mkv", true},
		{".MKV", true},
		{".Mp4", true},
		{".m2ts", true},
		{".webm", true},
		{".dvr-ms", true},
		{".exe", false},
		{".txt", false},
		{".", false},
		{"", false},
		{"mkv", false}, // missing leading dot
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			if got := IsMediaExtension(tc.ext); got != tc.want {
				t.Fatalf("IsMediaExtension(%q): got %v want %v", tc.ext, got, tc.want)
			}
		})
	}
}

// TestIsDiskExtension covers the three-element disk-image subset.
func TestIsDiskExtension(t *testing.T) {
	cases := []struct {
		ext  string
		want bool
	}{
		{".img", true},
		{".ISO", true},
		{".vob", true},
		{".mkv", false},
		{".mp4", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			if got := IsDiskExtension(tc.ext); got != tc.want {
				t.Fatalf("IsDiskExtension(%q): got %v want %v", tc.ext, got, tc.want)
			}
		})
	}
}

// TestQualityForExtension pins the per-extension Quality mapping for
// representatives of each tier in the C# table. Drift caught here
// cross-references `MediaFileExtensions.cs:13-71`.
func TestQualityForExtension(t *testing.T) {
	cases := []struct {
		ext  string
		want quality.Quality
	}{
		// Unknown tier
		{".webm", quality.Unknown},

		// SDTV tier — sample
		{".avi", quality.SDTV},
		{".mp4", quality.SDTV},
		{".MP4", quality.SDTV},
		{".dvr-ms", quality.SDTV},

		// DVD tier
		{".img", quality.DVD},
		{".iso", quality.DVD},
		{".vob", quality.DVD},

		// HDTV720p tier
		{".mkv", quality.HDTV720p},
		{".ts", quality.HDTV720p},
		{".wtv", quality.HDTV720p},

		// Bluray720p tier
		{".m2ts", quality.Bluray720p},

		// Unregistered
		{".exe", quality.Unknown},
		{"", quality.Unknown},
		{".unknown_ext", quality.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			got := QualityForExtension(tc.ext)
			if got.ID != tc.want.ID {
				t.Fatalf("QualityForExtension(%q): got %s (id=%d) want %s (id=%d)",
					tc.ext, got.Name, got.ID, tc.want.Name, tc.want.ID)
			}
		})
	}
}

// TestFileExtensionsLowercase pins the invariant: every key in the
// registry is lowercase. The case-folding contract relies on this — a
// stray upper-case key would silently miss lookups for the same
// extension typed differently. This test is the regression guard.
func TestFileExtensionsLowercase(t *testing.T) {
	for k := range fileExtensions {
		for _, r := range k {
			if r >= 'A' && r <= 'Z' {
				t.Fatalf("fileExtensions key %q is not lowercase", k)
			}
		}
	}
}
