package extras

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

type stubHardlinkCfg struct{ hl bool }

func (s stubHardlinkCfg) CopyUsingHardlinks() bool { return s.hl }

type stubExtraStore struct {
	rows     []*extrafiles.ExtraFile
	err      error
	bySeries []extrafiles.ExtraFile
	updated  []*extrafiles.ExtraFile
}

func (s *stubExtraStore) Upsert(_ context.Context, f *extrafiles.ExtraFile) error {
	if s.err != nil {
		return s.err
	}
	s.rows = append(s.rows, f)
	return nil
}

func (s *stubExtraStore) AllBySeriesID(_ context.Context, _ int) ([]extrafiles.ExtraFile, error) {
	return s.bySeries, nil
}

func (s *stubExtraStore) Update(_ context.Context, f *extrafiles.ExtraFile) error {
	s.updated = append(s.updated, f)
	return nil
}

// passthroughTransferrer records dst paths and always succeeds.
type passthroughTransferrer struct{ moved []string }

func (p *passthroughTransferrer) TransferFile(_, dst string, _ TransferMode) error {
	p.moved = append(p.moved, dst)
	return nil
}

func otherInput(sourcePath string, season int, eps ...int) ImportInput {
	return ImportInput{
		Series:      &series.Series{ID: 7, Path: "/tv/Show"},
		EpisodeFile: &episodefiles.File{ID: 55, SeasonNumber: season, RelativePath: "Season 01/Show.S01E02.mkv"},
		SourcePath:  sourcePath,
		FileInfo:    &parser.ParsedEpisodeInfo{SeasonNumber: season, EpisodeNumbers: eps},
	}
}

func TestOtherExtraManager_Metadata(t *testing.T) {
	t.Parallel()
	m := NewOtherExtraManager(&stubExtraStore{}, &passthroughTransferrer{}, &recordingMover{}, stubHardlinkCfg{}, slog.New(slog.DiscardHandler))
	if m.Order() != 2 {
		t.Errorf("Order = %d, want 2", m.Order())
	}
	if !m.CanImportFile("/x/y.nfo", ".nfo") {
		t.Error("Other must claim any file")
	}
}

func TestOtherExtraManager_FilenamePrefixMatch(t *testing.T) {
	t.Parallel()
	store := &stubExtraStore{}
	tr := &passthroughTransferrer{}
	m := NewOtherExtraManager(store, tr, &recordingMover{}, stubHardlinkCfg{}, slog.New(slog.DiscardHandler))
	// source stem "Show.S01E02.WEBDL"; companion shares the prefix.
	in := otherInput("/dl/Show.S01E02.WEBDL.mkv", 1, 2)
	n, err := m.ImportFiles(context.Background(), in, []string{"/dl/Show.S01E02.WEBDL.txt"})
	if err != nil || n != 1 {
		t.Fatalf("ImportFiles = (%d,%v), want (1,nil)", n, err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.RelativePath != "Season 01/Show.S01E02.txt" || row.SeriesID != 7 || row.EpisodeFileID != 55 || row.Extension != ".txt" {
		t.Errorf("row = %+v", row)
	}
}

func TestOtherExtraManager_EpisodeParseMatch(t *testing.T) {
	t.Parallel()
	store := &stubExtraStore{}
	m := NewOtherExtraManager(store, &passthroughTransferrer{}, &recordingMover{}, stubHardlinkCfg{}, slog.New(slog.DiscardHandler))
	// Different prefix, but parses to S01E02 → matches via the parse arm.
	in := otherInput("/dl/Show.S01E02.WEBDL.mkv", 1, 2)
	n, _ := m.ImportFiles(context.Background(), in, []string{"/dl/Show.S01E02.1080p.nfo"})
	if n != 1 || len(store.rows) != 1 {
		t.Fatalf("expected 1 imported (parse match), got %d rows=%d", n, len(store.rows))
	}
}

func TestOtherExtraManager_NoMatch_Skipped(t *testing.T) {
	t.Parallel()
	store := &stubExtraStore{}
	m := NewOtherExtraManager(store, &passthroughTransferrer{}, &recordingMover{}, stubHardlinkCfg{}, slog.New(slog.DiscardHandler))
	in := otherInput("/dl/Show.S01E02.mkv", 1, 2)
	// A file for a different episode + no shared prefix → no match.
	n, _ := m.ImportFiles(context.Background(), in, []string{"/dl/Other.S05E09.txt"})
	if n != 0 || len(store.rows) != 0 {
		t.Errorf("expected 0 imported, got %d rows=%d", n, len(store.rows))
	}
}

func TestOtherExtraManager_NfoDedup(t *testing.T) {
	t.Parallel()
	store := &stubExtraStore{}
	m := NewOtherExtraManager(store, &passthroughTransferrer{}, &recordingMover{}, stubHardlinkCfg{}, slog.New(slog.DiscardHandler))
	in := otherInput("/dl/Show.S01E02", 1, 2)
	// Two matching .nfo files — only the FIRST is considered.
	n, _ := m.ImportFiles(context.Background(), in, []string{
		"/dl/Show.S01E02.nfo",
		"/dl/Show.S01E02.extra.nfo",
	})
	if n != 1 || len(store.rows) != 1 {
		t.Errorf("nfo de-dup: expected 1 imported, got %d rows=%d", n, len(store.rows))
	}
}

func TestOtherExtraManager_MoveFilesAfterRename(t *testing.T) {
	t.Parallel()
	store := &stubExtraStore{bySeries: []extrafiles.ExtraFile{
		{ID: 1, SeriesID: 7, EpisodeFileID: 55, RelativePath: "Season 01/Old.Name.nfo", Extension: ".nfo"},
		{ID: 2, SeriesID: 7, EpisodeFileID: 99, RelativePath: "Season 01/Unrelated.nfo", Extension: ".nfo"},
	}}
	mover := &recordingMover{}
	m := NewOtherExtraManager(store, &passthroughTransferrer{}, mover, stubHardlinkCfg{}, slog.New(slog.DiscardHandler))
	renamed := &episodefiles.File{ID: 55, RelativePath: "Season 01/New.Name.mkv"}
	n, err := m.MoveFilesAfterRename(context.Background(), MoveInput{
		Series:       &series.Series{ID: 7, Path: "/tv/Show"},
		EpisodeFiles: []*episodefiles.File{renamed},
	})
	if err != nil || n != 1 {
		t.Fatalf("MoveFilesAfterRename = (%d,%v), want (1,nil)", n, err)
	}
	if len(mover.moves) != 1 {
		t.Fatalf("mover calls = %d, want 1 (only the matching episode file)", len(mover.moves))
	}
	wantDst := "/tv/Show/Season 01/New.Name.nfo"
	if mover.moves[0][1] != wantDst {
		t.Errorf("move dst = %q, want %q", mover.moves[0][1], wantDst)
	}
	if len(store.updated) != 1 || store.updated[0].RelativePath != "Season 01/New.Name.nfo" {
		t.Errorf("updated rows = %+v", store.updated)
	}
}
