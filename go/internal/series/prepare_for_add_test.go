package series_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/series"
)

var prepareForAddFixed = time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

// J189 — AddOptions.Monitor=None flips Monitored to false.
func TestPrepareForAdd_AddOptionsMonitorNone_FlipsMonitored(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:          "Show",
		TvdbID:         42,
		RootFolderPath: "/tv",
		Monitored:      true, // upstream said monitor; AddOptions overrides
		AddOptions:     `{"monitor":"none"}`,
	}
	series.PrepareForAdd(s, prepareForAddFixed, func(*series.Series) string { return "Show" })
	if s.Monitored {
		t.Errorf("Monitored: want false (AddOptions.Monitor=None), got true")
	}
}

// J189 — integer-form ordinal also triggers the override (C# allows both).
func TestPrepareForAdd_AddOptionsMonitorNoneInt_FlipsMonitored(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:      "Show",
		Monitored:  true,
		AddOptions: `{"monitor":12}`,
	}
	series.PrepareForAdd(s, prepareForAddFixed, nil)
	if s.Monitored {
		t.Errorf("Monitored (int 12): want false, got true")
	}
}

// J189 — AddOptions.Monitor=All leaves Monitored untouched.
func TestPrepareForAdd_AddOptionsMonitorAll_PreservesMonitored(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:      "Show",
		Monitored:  true,
		AddOptions: `{"monitor":"all"}`,
	}
	series.PrepareForAdd(s, prepareForAddFixed, nil)
	if !s.Monitored {
		t.Errorf("Monitored: want preserved-true (Monitor=All), got false")
	}
}

// J189 — empty AddOptions blob = no override (typical import-list row).
func TestPrepareForAdd_EmptyAddOptions_NoOverride(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:      "Show",
		Monitored:  true,
		AddOptions: "",
	}
	series.PrepareForAdd(s, prepareForAddFixed, nil)
	if !s.Monitored {
		t.Errorf("Monitored: want preserved-true (empty AddOptions), got false")
	}
}

// J189 — malformed AddOptions blob degrades silently to no-override.
func TestPrepareForAdd_MalformedAddOptions_NoOverride(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:      "Show",
		Monitored:  true,
		AddOptions: `{not json`,
	}
	series.PrepareForAdd(s, prepareForAddFixed, nil)
	if !s.Monitored {
		t.Errorf("Monitored: malformed AddOptions should be silent no-op, got false")
	}
}

// J186 happy path: every property set when input has Path empty +
// RootFolderPath set + folderName returns non-empty.
func TestPrepareForAdd_HappyPath(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:          "Some Show: Special!",
		TvdbID:         123456,
		RootFolderPath: "/tv",
	}
	series.PrepareForAdd(s, prepareForAddFixed, func(_ *series.Series) string { return "Some Show (2026)" })

	if s.Path != "/tv/Some Show (2026)" {
		t.Errorf("Path = %q, want /tv/Some Show (2026)", s.Path)
	}
	if s.CleanTitle == "" || s.CleanTitle == s.Title {
		t.Errorf("CleanTitle = %q, want a cleaned form != Title", s.CleanTitle)
	}
	if s.SortTitle == "" {
		t.Errorf("SortTitle empty, want a normalized form")
	}
	if s.Added == nil || !s.Added.Equal(prepareForAddFixed) {
		t.Errorf("Added = %v, want %v", s.Added, prepareForAddFixed)
	}
}

// J186: Path already set → not overwritten.
func TestPrepareForAdd_PathSet_NoOverwrite(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		Title:          "Show",
		RootFolderPath: "/tv",
		Path:           "/custom/path/Show",
	}
	series.PrepareForAdd(s, prepareForAddFixed, func(_ *series.Series) string { return "Show (Other)" })

	if s.Path != "/custom/path/Show" {
		t.Errorf("Path = %q, want unchanged /custom/path/Show", s.Path)
	}
}

// J186: nil folderName callable → Path stays empty (folder-creation
// subscriber will skip via the EmptyPath_NoOp gate).
func TestPrepareForAdd_NilFolderName_PathEmpty(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Show", RootFolderPath: "/tv"}
	series.PrepareForAdd(s, prepareForAddFixed, nil)

	if s.Path != "" {
		t.Errorf("Path = %q, want empty when folderName is nil", s.Path)
	}
	if s.CleanTitle == "" {
		t.Error("CleanTitle should still be set even when folderName is nil")
	}
}

// J186: empty RootFolderPath → Path stays empty (no rooted join
// possible).
func TestPrepareForAdd_NoRootFolder_PathEmpty(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Show"}
	series.PrepareForAdd(s, prepareForAddFixed, func(_ *series.Series) string { return "Show" })

	if s.Path != "" {
		t.Errorf("Path = %q, want empty when RootFolderPath is empty", s.Path)
	}
}

// J186: folderName returning "" → Path stays empty (defensive
// against a folder-format that resolves to an empty literal).
func TestPrepareForAdd_FolderNameEmpty_PathEmpty(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Show", RootFolderPath: "/tv"}
	series.PrepareForAdd(s, prepareForAddFixed, func(_ *series.Series) string { return "" })

	if s.Path != "" {
		t.Errorf("Path = %q, want empty when folderName returns empty", s.Path)
	}
}

// J186: Added already set → not overwritten.
func TestPrepareForAdd_AddedAlreadySet_Preserved(t *testing.T) {
	t.Parallel()
	preset := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &series.Series{Title: "Show", Added: &preset}
	series.PrepareForAdd(s, prepareForAddFixed, nil)

	if s.Added == nil || !s.Added.Equal(preset) {
		t.Errorf("Added = %v, want preserved %v", s.Added, preset)
	}
}

// J186: nil Series → defensive no-op (caller-error tolerance).
func TestPrepareForAdd_NilSeries_NoPanic(t *testing.T) {
	t.Parallel()
	series.PrepareForAdd(nil, prepareForAddFixed, nil)
	// No assertion — the test passes if no panic.
}

// J186: folderName callable receives the Series so it can inspect
// Title/Year/TvdbID/etc. while computing the folder name.
func TestPrepareForAdd_FolderNameReceivesSeries(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Some Show", Year: 2024, RootFolderPath: "/tv"}
	captured := ""
	series.PrepareForAdd(s, prepareForAddFixed, func(in *series.Series) string {
		captured = in.Title + " inspected"
		return "out"
	})
	if captured != "Some Show inspected" {
		t.Errorf("folderName callable received unexpected series: captured=%q", captured)
	}
	if s.Path != "/tv/out" {
		t.Errorf("Path = %q, want /tv/out", s.Path)
	}
}
