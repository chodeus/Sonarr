package mediafiles

import (
	"errors"
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/ospath"
)

// TestExcludedSubFoldersRegex pins each unconditionally-skipped
// subfolder name. The relative-path input shape is what FilterPaths
// will pass through (no leading separator on the first segment).
func TestExcludedSubFoldersRegex(t *testing.T) {
	matchCases := []string{
		"@eadir/file.mkv",
		"@EADIR/file.mkv",
		".@__thumb/file.mkv",
		"plex versions/file.mkv",
		"Plex Versions/file.mkv",
		".secret/file.mkv",
		".hidden/file.mkv",
		".unwanted/file.mkv",
		"Season 1/.hidden/file.mkv",
		"Season 1/@eadir/file.mkv",
	}
	for _, in := range matchCases {
		t.Run("match/"+in, func(t *testing.T) {
			if !excludedSubFoldersRegex.MatchString(in) {
				t.Fatalf("excludedSubFoldersRegex: expected match for %q", in)
			}
		})
	}

	noMatchCases := []string{
		"Season 1/file.mkv",
		"file.mkv",
		"normal/path/file.mkv",
		".hidden", // no trailing separator
	}
	for _, in := range noMatchCases {
		t.Run("nomatch/"+in, func(t *testing.T) {
			if excludedSubFoldersRegex.MatchString(in) {
				t.Fatalf("excludedSubFoldersRegex: did not expect match for %q", in)
			}
		})
	}
}

// TestExcludedExtrasSubFolderRegex pins each extras-name. These get
// filtered only when filterExtras=true.
func TestExcludedExtrasSubFolderRegex(t *testing.T) {
	matchCases := []string{
		"extras/file.mkv",
		"Extras/file.mkv",
		"EXTRAS/file.mkv",
		"extrafanart/img.jpg",
		"behind the scenes/clip.mkv",
		"deleted scenes/clip.mkv",
		"featurettes/clip.mkv",
		"interviews/clip.mkv",
		"other/clip.mkv",
		"scenes/clip.mkv",
		"samples/clip.mkv",
		"shorts/clip.mkv",
		"trailers/clip.mkv",
		"theme-music/track.mp3",
		"theme_music/track.mp3",
		"theme.music/track.mp3",
		"theme music/track.mp3",
		"backdrops/img.jpg",
		"Season 1/extras/clip.mkv",
	}
	for _, in := range matchCases {
		t.Run("match/"+in, func(t *testing.T) {
			if !excludedExtrasSubFolderRegex.MatchString(in) {
				t.Fatalf("excludedExtrasSubFolderRegex: expected match for %q", in)
			}
		})
	}

	noMatchCases := []string{
		"Season 1/file.mkv",
		"main/file.mkv",
		"file.mkv",
	}
	for _, in := range noMatchCases {
		t.Run("nomatch/"+in, func(t *testing.T) {
			if excludedExtrasSubFolderRegex.MatchString(in) {
				t.Fatalf("excludedExtrasSubFolderRegex: did not expect match for %q", in)
			}
		})
	}
}

// TestExcludedExtraFilesRegex pins the per-suffix extra-file name
// pattern. Anchored at end of string.
func TestExcludedExtraFilesRegex(t *testing.T) {
	matchCases := []string{
		"movie-trailer.mkv",
		"movie-TRAILER.mkv",
		"movie-other.mp4",
		"movie-behindthescenes.mkv",
		"movie-deleted.mkv",
		"movie-featurette.mkv",
		"movie-interview.mkv",
		"movie-scene.mkv",
		"movie-short.mkv",
	}
	for _, in := range matchCases {
		t.Run("match/"+in, func(t *testing.T) {
			if !excludedExtraFilesRegex.MatchString(in) {
				t.Fatalf("excludedExtraFilesRegex: expected match for %q", in)
			}
		})
	}

	noMatchCases := []string{
		"movie.mkv",
		"movie-trailer.unknown.mkv", // dot in extension portion blocks the [^.]+$ tail
		"trailer.mkv",               // missing leading dash
		"movie-thing.mkv",           // unknown role keyword
	}
	for _, in := range noMatchCases {
		t.Run("nomatch/"+in, func(t *testing.T) {
			if excludedExtraFilesRegex.MatchString(in) {
				t.Fatalf("excludedExtraFilesRegex: did not expect match for %q", in)
			}
		})
	}
}

// TestExcludedFilesRegex pins OS-noise filenames.
func TestExcludedFilesRegex(t *testing.T) {
	matchCases := []string{
		"._foo.mkv",
		"._.mkv",
		".unmanic",
		".DS_Store",
		"Thumbs.db",
		"thumbs.db", // case-insensitive
	}
	for _, in := range matchCases {
		t.Run("match/"+in, func(t *testing.T) {
			if !excludedFilesRegex.MatchString(in) {
				t.Fatalf("excludedFilesRegex: expected match for %q", in)
			}
		})
	}

	noMatchCases := []string{
		"foo.mkv",
		"DS_Store",   // missing leading dot
		"Thumbs.dbx", // Thumbs\.db is $-anchored so trailing chars block the match
	}
	for _, in := range noMatchCases {
		t.Run("nomatch/"+in, func(t *testing.T) {
			if excludedFilesRegex.MatchString(in) {
				t.Fatalf("excludedFilesRegex: did not expect match for %q", in)
			}
		})
	}
}

// TestFilterPathsRemovesUnconditionalSubfolders mirrors the C#
// `should_filter_certain_sub_folders` fixture at
// `Test/ProviderTests/DiskScanProviderTests/GetVideoFilesFixture.cs:86-104`.
// Each subfolder name is exercised individually; FilterPaths must
// drop every file under that subfolder and keep the surface-level
// files.
func TestFilterPathsRemovesUnconditionalSubfolders(t *testing.T) {
	subFolders := []string{
		"Extras",
		"@eadir",
		"extrafanart",
		"Plex Versions",
		".secret",
		".hidden",
		".unwanted",
		"theme-music",
	}

	const base = "/tv/series"
	surface := []string{
		base + "/30 Rock1.mkv",
		base + "/30 Rock2.avi",
		base + "/30 Rock3.MP4",
		base + "/30 Rock4.wMv",
	}

	for _, sub := range subFolders {
		t.Run(sub, func(t *testing.T) {
			specials := []string{
				base + "/" + sub + "/30 Rock1.mkv",
				base + "/" + sub + "/30 Rock2.avi",
			}
			all := append(append([]string{}, surface...), specials...)

			got, err := FilterPaths(base, all, true)
			if err != nil {
				t.Fatalf("FilterPaths: unexpected error %v", err)
			}
			if len(got) == 0 {
				t.Fatalf("FilterPaths: expected at least the surface files to survive")
			}
			for _, sp := range specials {
				for _, kept := range got {
					if kept == sp {
						t.Fatalf("FilterPaths: special %q under %q was not filtered out", sp, sub)
					}
				}
			}
			for _, surfFile := range surface {
				found := false
				for _, kept := range got {
					if kept == surfFile {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("FilterPaths: surface file %q was incorrectly dropped", surfFile)
				}
			}
		})
	}
}

// TestFilterPathsExtrasGate pins that filterExtras=false skips the two
// extras-only regex passes. Uses an extras-named subfolder and an
// extras-suffixed file; both must survive when filterExtras=false and
// be dropped when filterExtras=true.
func TestFilterPathsExtrasGate(t *testing.T) {
	const base = "/tv/series"
	paths := []string{
		base + "/Season 1/episode.mkv",
		base + "/extras/clip.mkv",
		base + "/Season 1/episode-trailer.mkv",
	}

	t.Run("filterExtras=false keeps extras", func(t *testing.T) {
		got, err := FilterPaths(base, paths, false)
		if err != nil {
			t.Fatalf("FilterPaths: %v", err)
		}
		if !reflect.DeepEqual(got, paths) {
			t.Fatalf("FilterPaths(false): got %v want %v", got, paths)
		}
	})

	t.Run("filterExtras=true drops extras", func(t *testing.T) {
		got, err := FilterPaths(base, paths, true)
		if err != nil {
			t.Fatalf("FilterPaths: %v", err)
		}
		want := []string{base + "/Season 1/episode.mkv"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("FilterPaths(true): got %v want %v", got, want)
		}
	})
}

// TestFilterPathsDropsOSNoise pins that OS-noise files are dropped
// regardless of filterExtras (they live in the unconditional pass).
func TestFilterPathsDropsOSNoise(t *testing.T) {
	const base = "/tv/series"
	paths := []string{
		base + "/Season 1/episode.mkv",
		base + "/Season 1/.DS_Store",
		base + "/Season 1/Thumbs.db",
		base + "/Season 1/._episode.mkv",
		base + "/Season 1/.unmanic",
	}
	for _, fe := range []bool{false, true} {
		got, err := FilterPaths(base, paths, fe)
		if err != nil {
			t.Fatalf("FilterPaths(fe=%v): %v", fe, err)
		}
		want := []string{base + "/Season 1/episode.mkv"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("FilterPaths(fe=%v): got %v want %v", fe, got, want)
		}
	}
}

// TestFilterPathsPreservesOrder ensures FilterPaths returns survivors
// in input order — Scan() calls upstream cleanup logic with the
// resulting slice expecting input-position parity.
func TestFilterPathsPreservesOrder(t *testing.T) {
	const base = "/tv/series"
	paths := []string{
		base + "/c.mkv",
		base + "/a.mkv",
		base + "/extras/junk.mkv", // dropped
		base + "/b.mkv",
	}
	got, err := FilterPaths(base, paths, true)
	if err != nil {
		t.Fatalf("FilterPaths: %v", err)
	}
	want := []string{base + "/c.mkv", base + "/a.mkv", base + "/b.mkv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterPaths: got %v want %v", got, want)
	}
}

// TestFilterPathsEmptyInput pins the empty-input contract: a non-nil
// empty slice (so callers can range without nil-check noise).
func TestFilterPathsEmptyInput(t *testing.T) {
	got, err := FilterPaths("/tv/series", nil, true)
	if err != nil {
		t.Fatalf("FilterPaths: %v", err)
	}
	if got == nil {
		t.Fatalf("FilterPaths: empty input must yield non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("FilterPaths: empty input expected 0-length, got %d", len(got))
	}
}

// TestFilterPathsErrorOnNonChild pins the misuse contract: any path
// not under basePath returns the wrapped [ospath.ErrNotParent]
// sentinel, mirroring the C# `NotParentException` throw.
func TestFilterPathsErrorOnNonChild(t *testing.T) {
	const base = "/tv/series"
	paths := []string{
		base + "/Season 1/episode.mkv",
		"/elsewhere/file.mkv",
	}
	got, err := FilterPaths(base, paths, true)
	if err == nil {
		t.Fatalf("FilterPaths: expected error on non-child path; got %v", got)
	}
	if !errors.Is(err, ospath.ErrNotParent) {
		t.Fatalf("FilterPaths: error %v does not wrap ospath.ErrNotParent", err)
	}
	if got != nil {
		t.Fatalf("FilterPaths: error path must yield nil slice, got %v", got)
	}
}
