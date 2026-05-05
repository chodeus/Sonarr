package episodefiles_test

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// cleanupHarness wires episodefiles.Service + episodes.Service +
// CleanupService against a single sqlite test DB. The series rows
// are NOT actually persisted — Cleanup takes the *series.Series
// as a parameter, so a stub struct is enough.
type cleanupHarness struct {
	db       *datastore.DB
	files    *episodefiles.Service
	filesR   *episodefiles.Repo
	episodes *episodes.Service
	episodeR *episodes.Repo
	cleanup  *episodefiles.CleanupService
	parent   *series.Series
}

func newCleanupHarness(t *testing.T) *cleanupHarness {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	filesRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo files: %v", err)
	}
	filesSvc, err := episodefiles.NewService(filesRepo, bus)
	if err != nil {
		t.Fatalf("NewService files: %v", err)
	}

	epsRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo episodes: %v", err)
	}
	epsSvc, err := episodes.NewService(epsRepo, bus)
	if err != nil {
		t.Fatalf("NewService episodes: %v", err)
	}

	cleanup, err := episodefiles.NewCleanupService(filesSvc, epsSvc, slog.Default())
	if err != nil {
		t.Fatalf("NewCleanupService: %v", err)
	}

	parent := &series.Series{
		ID:   42,
		Path: filepath.Join(t.TempDir(), "Foo"),
	}
	return &cleanupHarness{
		db:       db,
		files:    filesSvc,
		filesR:   filesRepo,
		episodes: epsSvc,
		episodeR: epsRepo,
		cleanup:  cleanup,
		parent:   parent,
	}
}

func (h *cleanupHarness) seedFile(t *testing.T, rel string) *episodefiles.File {
	t.Helper()
	f := &episodefiles.File{
		SeriesID:     h.parent.ID,
		SeasonNumber: 1,
		RelativePath: rel,
		Quality:      "{}",
		Size:         1024,
		DateAdded:    time.Now().UTC(),
		Languages:    []int{1},
	}
	if _, err := h.filesR.Insert(t.Context(), f); err != nil {
		t.Fatalf("seed file %q: %v", rel, err)
	}
	return f
}

func (h *cleanupHarness) seedEpisode(t *testing.T, num, fileID int) {
	t.Helper()
	e := &episodes.Episode{
		SeriesID:      h.parent.ID,
		SeasonNumber:  1,
		EpisodeNumber: num,
		Title:         "ep",
		Monitored:     true,
	}
	if fileID > 0 {
		id := fileID
		e.EpisodeFileID = &id
	}
	if _, err := h.episodeR.Insert(t.Context(), e); err != nil {
		t.Fatalf("seed episode %d: %v", num, err)
	}
}

func (h *cleanupHarness) abs(rel string) string {
	return filepath.Clean(filepath.Join(h.parent.Path, rel))
}

func TestNewCleanupService_NilGuards(t *testing.T) {
	t.Parallel()
	if _, err := episodefiles.NewCleanupService(nil, nil, nil); err == nil {
		t.Errorf("NilGuards: want error")
	}
}

func TestCleanupService_DeletesMissingFromDisk(t *testing.T) {
	t.Parallel()
	h := newCleanupHarness(t)
	keep := h.seedFile(t, "Season 1/keep.mkv")
	gone := h.seedFile(t, "Season 1/gone.mkv")
	h.seedEpisode(t, 1, keep.ID)
	h.seedEpisode(t, 2, gone.ID)

	deleted, cleared, err := h.cleanup.Clean(t.Context(), h.parent, []string{h.abs("Season 1/keep.mkv")})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if deleted != 1 || cleared != 1 {
		t.Errorf("Clean: deleted=%d cleared=%d, want 1/1", deleted, cleared)
	}

	// gone is removed from DB; keep stays.
	if _, err := h.files.GetFile(t.Context(), gone.ID); err == nil {
		t.Errorf("gone: still present after cleanup")
	}
	if _, err := h.files.GetFile(t.Context(), keep.ID); err != nil {
		t.Errorf("keep: dropped erroneously: %v", err)
	}

	// Episode for `gone` had its EpisodeFileID cleared.
	allEps, err := h.episodes.GetEpisodesBySeries(t.Context(), h.parent.ID)
	if err != nil {
		t.Fatalf("GetEpisodesBySeries: %v", err)
	}
	for _, e := range allEps {
		if e.EpisodeNumber == 2 && e.EpisodeFileID != nil {
			t.Errorf("episode 2: EpisodeFileID=%v after cleanup, want nil", *e.EpisodeFileID)
		}
		if e.EpisodeNumber == 1 && (e.EpisodeFileID == nil || *e.EpisodeFileID != keep.ID) {
			t.Errorf("episode 1: EpisodeFileID=%v, want %d", e.EpisodeFileID, keep.ID)
		}
	}
}

func TestCleanupService_DeletesNoLinkedEpisodes(t *testing.T) {
	t.Parallel()
	h := newCleanupHarness(t)
	linked := h.seedFile(t, "Season 1/linked.mkv")
	orphan := h.seedFile(t, "Season 1/orphan.mkv")
	h.seedEpisode(t, 1, linked.ID)
	// Note: no Episode references `orphan` — both files exist on
	// disk so the missing-from-disk branch doesn't fire.

	disk := []string{h.abs("Season 1/linked.mkv"), h.abs("Season 1/orphan.mkv")}
	deleted, cleared, err := h.cleanup.Clean(t.Context(), h.parent, disk)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if deleted != 1 || cleared != 0 {
		t.Errorf("Clean: deleted=%d cleared=%d, want 1/0", deleted, cleared)
	}
	if _, err := h.files.GetFile(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan: still present after cleanup")
	}
	if _, err := h.files.GetFile(t.Context(), linked.ID); err != nil {
		t.Errorf("linked: dropped erroneously: %v", err)
	}
}

func TestCleanupService_ClearsEpisodesPointingAtMissingFiles(t *testing.T) {
	t.Parallel()
	h := newCleanupHarness(t)

	// Episode points at file ID 999, which never exists. Cleanup
	// should clear the link.
	keep := h.seedFile(t, "Season 1/keep.mkv")
	h.seedEpisode(t, 1, keep.ID)
	h.seedEpisode(t, 2, 999) // dangling reference

	disk := []string{h.abs("Season 1/keep.mkv")}
	deleted, cleared, err := h.cleanup.Clean(t.Context(), h.parent, disk)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if deleted != 0 || cleared != 1 {
		t.Errorf("Clean: deleted=%d cleared=%d, want 0/1", deleted, cleared)
	}

	allEps, err := h.episodes.GetEpisodesBySeries(t.Context(), h.parent.ID)
	if err != nil {
		t.Fatalf("GetEpisodesBySeries: %v", err)
	}
	for _, e := range allEps {
		if e.EpisodeNumber == 2 && e.EpisodeFileID != nil {
			t.Errorf("episode 2: EpisodeFileID=%v, want nil (dangling cleared)", *e.EpisodeFileID)
		}
	}
}

func TestCleanupService_NoOpWhenAllAligned(t *testing.T) {
	t.Parallel()
	h := newCleanupHarness(t)
	a := h.seedFile(t, "Season 1/a.mkv")
	b := h.seedFile(t, "Season 1/b.mkv")
	h.seedEpisode(t, 1, a.ID)
	h.seedEpisode(t, 2, b.ID)

	disk := []string{h.abs("Season 1/a.mkv"), h.abs("Season 1/b.mkv")}
	deleted, cleared, err := h.cleanup.Clean(t.Context(), h.parent, disk)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if deleted != 0 || cleared != 0 {
		t.Errorf("Clean: deleted=%d cleared=%d, want 0/0", deleted, cleared)
	}
}

func TestCleanupService_PathNormalization(t *testing.T) {
	t.Parallel()
	h := newCleanupHarness(t)
	a := h.seedFile(t, "Season 1/a.mkv")
	h.seedEpisode(t, 1, a.ID)

	// Pass un-normalised paths (./ prefix, double-slash) — Cleanup
	// runs filepath.Clean on both sides.
	abs := h.abs("Season 1/a.mkv")
	disk := []string{"./" + abs, abs + "//", abs}
	deleted, _, err := h.cleanup.Clean(t.Context(), h.parent, disk)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Clean(normalised): deleted=%d, want 0 (path should match after Clean)", deleted)
	}
}
