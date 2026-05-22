package series_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// TestApplyChanges_CopiesOnlyTheElevenFields verifies that
// [series.Series.ApplyChanges] copies exactly the 11 fields C#
// `Series.ApplyChanges` copies — and nothing else. Receiver starts
// with SkyHook-authoritative values; `other` carries user-supplied
// values on the 11 transferable fields PLUS divergent values on
// SkyHook-owned fields (which must NOT be copied).
func TestApplyChanges_CopiesOnlyTheElevenFields(t *testing.T) {
	t.Parallel()

	dst := &series.Series{
		TvdbID:           100,
		Title:            "skyhook-title",
		CleanTitle:       "skyhook-clean",
		SortTitle:        "skyhook-sort",
		Overview:         "skyhook-overview",
		Network:          "skyhook-network",
		Images:           `[{"coverType":"poster"}]`,
		ImdbID:           "tt-skyhook",
		Seasons:          "[]",
		Path:             "",
		QualityProfileID: 0,
		SeasonFolder:     false,
		Monitored:        false,
		MonitorNewItems:  0,
		SeriesType:       0,
		RootFolderPath:   "",
		Tags:             nil,
		AddOptions:       "",
	}

	src := &series.Series{
		TvdbID:           200, // copied
		Title:            "user-title-IGNORED",
		CleanTitle:       "user-clean-IGNORED",
		SortTitle:        "user-sort-IGNORED",
		Overview:         "user-overview-IGNORED",
		Network:          "user-network-IGNORED",
		Images:           `[{"coverType":"banner"}]`, // IGNORED
		ImdbID:           "tt-user-IGNORED",
		Seasons:          `[{"seasonNumber":1,"monitored":true}]`, // copied
		Path:             "/tv/Show",                              // copied
		QualityProfileID: 7,                                       // copied
		SeasonFolder:     true,                                    // copied
		Monitored:        true,                                    // copied
		MonitorNewItems:  2,                                       // copied
		SeriesType:       1,                                       // copied
		RootFolderPath:   "/tv",                                   // copied
		Tags:             []int{3, 5},                             // copied
		AddOptions:       `{"monitor":"all"}`,                     // copied
	}

	dst.ApplyChanges(src)

	// 11 fields C# copies — every one must match src.
	if dst.TvdbID != 200 {
		t.Errorf("TvdbID: got %d, want 200", dst.TvdbID)
	}
	if dst.Seasons != src.Seasons {
		t.Errorf("Seasons: got %q, want %q", dst.Seasons, src.Seasons)
	}
	if dst.Path != "/tv/Show" {
		t.Errorf("Path: got %q", dst.Path)
	}
	if dst.QualityProfileID != 7 {
		t.Errorf("QualityProfileID: got %d", dst.QualityProfileID)
	}
	if !dst.SeasonFolder {
		t.Errorf("SeasonFolder: want true")
	}
	if !dst.Monitored {
		t.Errorf("Monitored: want true")
	}
	if dst.MonitorNewItems != 2 {
		t.Errorf("MonitorNewItems: got %d", dst.MonitorNewItems)
	}
	if dst.SeriesType != 1 {
		t.Errorf("SeriesType: got %d", dst.SeriesType)
	}
	if dst.RootFolderPath != "/tv" {
		t.Errorf("RootFolderPath: got %q", dst.RootFolderPath)
	}
	if len(dst.Tags) != 2 || dst.Tags[0] != 3 || dst.Tags[1] != 5 {
		t.Errorf("Tags: got %v", dst.Tags)
	}
	if dst.AddOptions != `{"monitor":"all"}` {
		t.Errorf("AddOptions: got %q", dst.AddOptions)
	}

	// SkyHook-owned fields must stay untouched.
	if dst.Title != "skyhook-title" {
		t.Errorf("Title: should not be copied, got %q", dst.Title)
	}
	if dst.CleanTitle != "skyhook-clean" {
		t.Errorf("CleanTitle: should not be copied, got %q", dst.CleanTitle)
	}
	if dst.SortTitle != "skyhook-sort" {
		t.Errorf("SortTitle: should not be copied, got %q", dst.SortTitle)
	}
	if dst.Overview != "skyhook-overview" {
		t.Errorf("Overview: should not be copied")
	}
	if dst.Network != "skyhook-network" {
		t.Errorf("Network: should not be copied")
	}
	if dst.Images != `[{"coverType":"poster"}]` {
		t.Errorf("Images: should not be copied, got %q", dst.Images)
	}
	if dst.ImdbID != "tt-skyhook" {
		t.Errorf("ImdbID: should not be copied")
	}
}

func TestApplyChanges_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *series.Series
	// Should not panic.
	s.ApplyChanges(&series.Series{Path: "/tv"})
}

func TestApplyChanges_NilOther(t *testing.T) {
	t.Parallel()
	dst := &series.Series{Path: "/orig"}
	dst.ApplyChanges(nil)
	if dst.Path != "/orig" {
		t.Errorf("Path mutated by nil other: %q", dst.Path)
	}
}
