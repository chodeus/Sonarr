package extras

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/series"
)

type stubExtraCfg struct {
	on   bool
	exts string
}

func (s stubExtraCfg) ImportExtraFiles() bool      { return s.on }
func (s stubExtraCfg) ExtraFileExtensions() string { return s.exts }

// claimManager records which files it was asked to import + claims a
// fixed extension (or all when ext == "").
type claimManager struct {
	order     int
	claimExt  string // "" → claim everything
	gotFiles  []string
	importErr error
	movedFor  int
}

func (m *claimManager) Order() int { return m.order }
func (m *claimManager) CanImportFile(_ string, extension string) bool {
	return m.claimExt == "" || extension == m.claimExt
}
func (m *claimManager) ImportFiles(_ context.Context, _ ImportInput, files []string) (int, error) {
	m.gotFiles = append(m.gotFiles, files...)
	return len(files), m.importErr
}

func (m *claimManager) MoveFilesAfterRename(_ context.Context, in MoveInput) (int, error) {
	m.movedFor += len(in.EpisodeFiles)
	return len(in.EpisodeFiles), nil
}

func svcInput() ImportInput {
	return ImportInput{
		Series:      &series.Series{ID: 7, Path: "/tv/Show"},
		EpisodeFile: &episodefiles.File{ID: 55, RelativePath: "Season 01/Show.S01E02.mkv"},
		SourcePath:  "/dl/Show.S01E02.mkv",
	}
}

func mustService(t *testing.T, managers []Manager, files []string, cfg stubExtraCfg) *ExtraService {
	t.Helper()
	s, err := NewExtraService(managers, stubFolders{files: files}, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewExtraService: %v", err)
	}
	return s
}

func TestExtraService_ConfigOff_NoOp(t *testing.T) {
	t.Parallel()
	sub := &claimManager{order: 1, claimExt: ".srt"}
	s := mustService(t, []Manager{sub}, []string{"/dl/Show.S01E02.srt"}, stubExtraCfg{on: false, exts: "srt"})
	n, _ := s.ImportEpisode(context.Background(), svcInput())
	if n != 0 || len(sub.gotFiles) != 0 {
		t.Errorf("config off must be a no-op, got n=%d files=%v", n, sub.gotFiles)
	}
}

func TestExtraService_ExtensionFilter(t *testing.T) {
	t.Parallel()
	other := &claimManager{order: 2, claimExt: ""}
	// Only .srt is wanted; the .jpg + .mkv must be filtered out.
	s := mustService(t, []Manager{other},
		[]string{"/dl/Show.S01E02.srt", "/dl/Show.S01E02.jpg", "/dl/Show.S01E02.mkv"},
		stubExtraCfg{on: true, exts: "srt"})
	if _, err := s.ImportEpisode(context.Background(), svcInput()); err != nil {
		t.Fatalf("ImportEpisode: %v", err)
	}
	if len(other.gotFiles) != 1 || other.gotFiles[0] != "/dl/Show.S01E02.srt" {
		t.Errorf("extension filter: got %v, want only the .srt", other.gotFiles)
	}
}

func TestExtraService_FirstClaimWins_ByOrder(t *testing.T) {
	t.Parallel()
	// Subtitle (order 1) claims .srt; Other (order 2) claims everything.
	// A .srt must go to the subtitle manager (lower order claims first);
	// a .nfo falls through to Other.
	sub := &claimManager{order: 1, claimExt: ".srt"}
	other := &claimManager{order: 2, claimExt: ""}
	// Pass managers out of order to also exercise the Order sort.
	s := mustService(t, []Manager{other, sub},
		[]string{"/dl/Show.S01E02.srt", "/dl/Show.S01E02.nfo"},
		stubExtraCfg{on: true, exts: "srt,nfo"})
	if _, err := s.ImportEpisode(context.Background(), svcInput()); err != nil {
		t.Fatalf("ImportEpisode: %v", err)
	}
	if len(sub.gotFiles) != 1 || sub.gotFiles[0] != "/dl/Show.S01E02.srt" {
		t.Errorf("subtitle manager files = %v, want [.srt]", sub.gotFiles)
	}
	if len(other.gotFiles) != 1 || other.gotFiles[0] != "/dl/Show.S01E02.nfo" {
		t.Errorf("other manager files = %v, want [.nfo]", other.gotFiles)
	}
}

func TestExtraService_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := NewExtraService(nil, nil, stubExtraCfg{}, nil); err == nil {
		t.Error("expected error on nil FolderEnumerator")
	}
	if _, err := NewExtraService(nil, stubFolders{}, nil, nil); err == nil {
		t.Error("expected error on nil ExtraConfig")
	}
}
