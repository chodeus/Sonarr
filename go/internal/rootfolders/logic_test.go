package rootfolders

import (
	"path/filepath"
	"testing"
)

func TestIsSpecialFolder(t *testing.T) {
	cases := map[string]bool{
		"$RECYCLE.BIN":              true,
		"$recycle.bin":              true,
		"System Volume Information": true,
		"@eaDir":                    true,
		".appledouble":              true,
		"lost+found":                true,
		"normal-folder":             false,
		"":                          false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IsSpecialFolder(name); got != want {
				t.Errorf("IsSpecialFolder(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestIsParentPath(t *testing.T) {
	tv := "/tv"
	tvSlash := "/tv/"
	tvAnimeShow := filepath.Join("/tv", "anime", "Show")
	cases := []struct {
		parent, child string
		want          bool
	}{
		{tv, tvAnimeShow, true},
		{tvSlash, tvAnimeShow, true},
		{tv, tv, false}, // self is not parent
		{tv, "/tvshows", false},
		{tv, filepath.Join("/movies", "anime"), false},
		{"", "/tv/Show", false},
		{tv, "", false},
		// Case-insensitive.
		{"/TV", tvAnimeShow, true},
	}
	for _, tc := range cases {
		t.Run(tc.parent+" / "+tc.child, func(t *testing.T) {
			if got := IsParentPath(tc.parent, tc.child); got != tc.want {
				t.Errorf("IsParentPath(%q, %q) = %v, want %v", tc.parent, tc.child, got, tc.want)
			}
		})
	}
}

func TestCleanPath(t *testing.T) {
	if got := CleanPath("/tv/"); got != filepath.Clean("/tv") {
		t.Errorf("trailing slash should be stripped, got %q", got)
	}
	if got := CleanPath(""); got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
	// `filepath.Clean` collapses `//` and `..` etc.
	if got := CleanPath(filepath.Join("/tv", "anime", "..")); got != filepath.Clean("/tv") {
		t.Errorf("filepath.Clean should collapse ..; got %q", got)
	}
}

func TestBestPath(t *testing.T) {
	folders := []Folder{
		{ID: 1, Path: "/tv"},
		{ID: 2, Path: filepath.Join("/tv", "anime")},
		{ID: 3, Path: "/movies"},
	}

	t.Run("longest matching prefix wins", func(t *testing.T) {
		got := BestPath(filepath.Join("/tv", "anime", "Series.Title"), folders)
		want := filepath.Clean(filepath.Join("/tv", "anime"))
		if got != want {
			t.Errorf("BestPath = %q, want %q", got, want)
		}
	})

	t.Run("fallback to filepath.Dir when no match", func(t *testing.T) {
		got := BestPath(filepath.Join("/elsewhere", "Show"), folders)
		want := filepath.Clean("/elsewhere")
		if got != want {
			t.Errorf("BestPath fallback = %q, want %q", got, want)
		}
	})

	t.Run("only the deeper folder is parent", func(t *testing.T) {
		got := BestPath(filepath.Join("/movies", "Indie", "X"), folders)
		want := filepath.Clean("/movies")
		if got != want {
			t.Errorf("BestPath = %q, want %q", got, want)
		}
	})

	t.Run("empty folders list falls back to filepath.Dir", func(t *testing.T) {
		got := BestPath(filepath.Join("/anywhere", "X"), nil)
		want := filepath.Clean("/anywhere")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("longest-path tie-break: deeper wins even when defined first", func(t *testing.T) {
		// Defined order shouldn't matter — only path length.
		swapped := []Folder{
			{ID: 2, Path: filepath.Join("/tv", "anime")},
			{ID: 1, Path: "/tv"},
		}
		got := BestPath(filepath.Join("/tv", "anime", "Show"), swapped)
		want := filepath.Clean(filepath.Join("/tv", "anime"))
		if got != want {
			t.Errorf("BestPath = %q, want %q", got, want)
		}
	})
}

func TestFilterSpecialFolders(t *testing.T) {
	in := []UnmappedFolder{
		{Name: "Series A", Path: filepath.Join("/tv", "Series A")},
		{Name: "$RECYCLE.BIN", Path: filepath.Join("/tv", "$RECYCLE.BIN")},
		{Name: "Series B", Path: filepath.Join("/tv", "Series B")},
		{Name: "@eaDir", Path: filepath.Join("/tv", "@eaDir")},
		{Name: ".appledouble", Path: filepath.Join("/tv", ".appledouble")},
	}
	got := FilterSpecialFolders(in)
	if len(got) != 2 {
		t.Errorf("expected 2 folders after filter, got %d: %+v", len(got), got)
	}
	if got[0].Name != "Series A" || got[1].Name != "Series B" {
		t.Errorf("unexpected filter result: %+v", got)
	}
}
