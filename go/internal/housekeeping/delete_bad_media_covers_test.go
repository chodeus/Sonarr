package housekeeping_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/metadatafiles"
)

// fakeMediaCoverConfig records the one-shot flag's lifecycle for
// behaviour assertions.
type fakeMediaCoverConfig struct {
	enabled bool
	setErr  error
	mu      sync.Mutex
	setVals []bool
}

func (f *fakeMediaCoverConfig) CleanupMetadataImages() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enabled
}

func (f *fakeMediaCoverConfig) SetCleanupMetadataImages(_ context.Context, v bool) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setVals = append(f.setVals, v)
	f.enabled = v
	return nil
}

type fakeMetadataSweepRepo struct {
	bySeries map[int][]metadatafiles.MetadataFile
	deleted  []int
	allErr   error
	delErr   error
}

func (f *fakeMetadataSweepRepo) AllBySeriesID(_ context.Context, seriesID int) ([]metadatafiles.MetadataFile, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.bySeries[seriesID], nil
}

func (f *fakeMetadataSweepRepo) Delete(_ context.Context, id int) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

type fakeImageStore struct {
	// content keyed by full path. Missing entries yield a "not
	// exist" error from FileSize / OpenReadStream.
	content map[string][]byte
	deleted []string
	openErr error
	delErr  error
}

func (f *fakeImageStore) FileSize(path string) (int64, error) {
	if b, ok := f.content[path]; ok {
		return int64(len(b)), nil
	}
	return 0, os.ErrNotExist
}

func (f *fakeImageStore) OpenReadStream(path string) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	b, ok := f.content[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (f *fakeImageStore) DeleteFile(path string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, path)
	return nil
}

// newRow constructs a MetadataFile fixture with SeriesID=7 and a
// LastUpdated timestamp set to 2025-01-01 (after the C# cutoff at
// 2014-12-27). SeriesID is hardcoded because every test in this file
// uses the same series; tests that need a different series build
// the row inline.
func newRow(id int, rel string) metadatafiles.MetadataFile {
	return metadatafiles.MetadataFile{
		ID:           id,
		SeriesID:     7,
		Consumer:     "Kodi",
		Type:         3,
		RelativePath: rel,
		LastUpdated:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestDeleteBadMediaCovers_FlagOffIsNoOp(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: false}
	repo := &fakeMetadataSweepRepo{}
	paths := &fakeSeriesPaths{}
	disk := &fakeImageStore{}
	task, err := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "DeleteBadMediaCovers" {
		t.Errorf("Name() = %q", got)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 0 || len(disk.deleted) != 0 {
		t.Errorf("flag-off sweep should be a no-op")
	}
	if len(cfg.setVals) != 0 {
		t.Errorf("flag-off should not write back the flag; got %v", cfg.setVals)
	}
}

func TestDeleteBadMediaCovers_DeletesHTMLPayload(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	row1 := newRow(1, "poster.jpg")
	row2 := newRow(2, "fanart.jpg")
	repo := &fakeMetadataSweepRepo{
		bySeries: map[int][]metadatafiles.MetadataFile{7: {row1, row2}},
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	disk := &fakeImageStore{
		content: map[string][]byte{
			// First 10 bytes are "<html>404 " — contains "html" →
			// invalid. The C# algorithm only inspects the leading
			// 10 bytes, so a payload that puts "html" later in
			// the buffer (e.g. "<!DOCTYPE html><html>...") would
			// slip through the detector — that's a C# bug, but
			// the Go port preserves the behaviour verbatim.
			"/tv/Show/poster.jpg": []byte("<html>404 not found</html>"),
			"/tv/Show/fanart.jpg": []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01"),
		},
	}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 1 {
		t.Errorf("repo.deleted = %v, want [1]", repo.deleted)
	}
	if len(disk.deleted) != 1 || disk.deleted[0] != "/tv/Show/poster.jpg" {
		t.Errorf("disk.deleted = %v, want [/tv/Show/poster.jpg]", disk.deleted)
	}
	// One-shot reset.
	if len(cfg.setVals) != 1 || cfg.setVals[0] != false {
		t.Errorf("flag should be set to false once; got %v", cfg.setVals)
	}
	if cfg.enabled {
		t.Errorf("flag should be off after sweep")
	}
}

func TestDeleteBadMediaCovers_ShortFileIsInvalid(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	row := newRow(1, "tiny.jpg")
	repo := &fakeMetadataSweepRepo{bySeries: map[int][]metadatafiles.MetadataFile{7: {row}}}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	disk := &fakeImageStore{
		content: map[string][]byte{"/tv/Show/tiny.jpg": []byte("123")}, // < 10 bytes
	}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 1 {
		t.Errorf("short file should be deleted; got %v", repo.deleted)
	}
}

func TestDeleteBadMediaCovers_LastUpdatedBeforeCutoffSkipped(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	row := metadatafiles.MetadataFile{
		ID: 1, SeriesID: 7, Consumer: "Kodi", Type: 3,
		RelativePath: "poster.jpg",
		LastUpdated:  time.Date(2014, 12, 27, 0, 0, 0, 0, time.UTC), // exactly the cutoff — NOT after
	}
	repo := &fakeMetadataSweepRepo{bySeries: map[int][]metadatafiles.MetadataFile{7: {row}}}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	disk := &fakeImageStore{
		content: map[string][]byte{"/tv/Show/poster.jpg": []byte("<html>404</html>")},
	}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("cutoff-boundary row should not be deleted; got %v", repo.deleted)
	}
}

func TestDeleteBadMediaCovers_NonJpgSkipped(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	row := newRow(1, "poster.png")
	repo := &fakeMetadataSweepRepo{bySeries: map[int][]metadatafiles.MetadataFile{7: {row}}}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	disk := &fakeImageStore{
		content: map[string][]byte{"/tv/Show/poster.png": []byte("<html>404</html>")},
	}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("non-jpg row should not be deleted; got %v", repo.deleted)
	}
}

func TestDeleteBadMediaCovers_PerImageErrorLoggedAndContinues(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	row1 := newRow(1, "missing.jpg") // disk.FileSize returns os.ErrNotExist
	row2 := newRow(2, "bad.jpg")
	repo := &fakeMetadataSweepRepo{bySeries: map[int][]metadatafiles.MetadataFile{7: {row1, row2}}}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	disk := &fakeImageStore{
		content: map[string][]byte{
			"/tv/Show/bad.jpg": []byte("<html>404 not found</html>"),
		},
	}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	// row1's FileSize errored → skipped; row2 is invalid → deleted.
	if len(repo.deleted) != 1 || repo.deleted[0] != 2 {
		t.Errorf("repo.deleted = %v, want [2] (row1 skipped after disk error)", repo.deleted)
	}
}

func TestDeleteBadMediaCovers_FlagWriteErrorPropagates(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true, setErr: errors.New("EIO")}
	repo := &fakeMetadataSweepRepo{}
	paths := &fakeSeriesPaths{paths: map[int]string{}}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, &fakeImageStore{}, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from SetCleanupMetadataImages")
	}
	if !errors.Is(err, cfg.setErr) {
		t.Errorf("error chain missing setErr: %v", err)
	}
}

func TestDeleteBadMediaCovers_AllSeriesPathsErrorPropagates(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	paths := &fakeSeriesPaths{err: errors.New("EIO")}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, &fakeMetadataSweepRepo{}, paths, &fakeImageStore{}, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from AllSeriesPaths")
	}
}

func TestDeleteBadMediaCovers_RepoDeleteErrorPropagates(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{enabled: true}
	row := newRow(1, "poster.jpg")
	repo := &fakeMetadataSweepRepo{
		bySeries: map[int][]metadatafiles.MetadataFile{7: {row}},
		delErr:   errors.New("repo down"),
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	disk := &fakeImageStore{
		content: map[string][]byte{"/tv/Show/poster.jpg": []byte("<html>404 not found</html>")},
	}
	task, _ := housekeeping.NewDeleteBadMediaCovers(cfg, repo, paths, disk, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from repo.Delete")
	}
	if !errors.Is(err, repo.delErr) {
		t.Errorf("error chain missing repo err: %v", err)
	}
	// Disk delete must NOT have happened (DB row delete failed first).
	if len(disk.deleted) != 0 {
		t.Errorf("disk delete should not fire when DB delete fails; got %v", disk.deleted)
	}
}

func TestDeleteBadMediaCovers_ConstructorGuards(t *testing.T) {
	t.Parallel()
	cfg := &fakeMediaCoverConfig{}
	repo := &fakeMetadataSweepRepo{}
	paths := &fakeSeriesPaths{}
	disk := &fakeImageStore{}
	cases := []struct {
		name        string
		cfg         housekeeping.MediaCoverConfig
		repo        housekeeping.MetadataFileSweepRepo
		paths       housekeeping.SeriesPaths
		disk        housekeeping.MediaCoverImageStore
		expectError bool
	}{
		{"nil cfg", nil, repo, paths, disk, true},
		{"nil repo", cfg, nil, paths, disk, true},
		{"nil paths", cfg, repo, nil, disk, true},
		{"nil disk OK (default)", cfg, repo, paths, nil, false},
		{"all set", cfg, repo, paths, disk, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := housekeeping.NewDeleteBadMediaCovers(c.cfg, c.repo, c.paths, c.disk, slog.Default())
			if c.expectError && err == nil {
				t.Errorf("%s: expected error", c.name)
			}
			if !c.expectError && err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
		})
	}
}

func TestOSMediaCoverImageStore_RealDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jpg")
	if err := os.WriteFile(path, []byte("hello-world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	disk := housekeeping.OSMediaCoverImageStore{}
	size, err := disk.FileSize(path)
	if err != nil || size != 11 {
		t.Errorf("FileSize: got (%d, %v); want (11, nil)", size, err)
	}
	rc, err := disk.OpenReadStream(path)
	if err != nil {
		t.Fatalf("OpenReadStream: %v", err)
	}
	b, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil {
		t.Fatalf("rc.Close: %v", err)
	}
	if string(b) != "hello-world" {
		t.Errorf("OpenReadStream returned %q", string(b))
	}
	if err := disk.DeleteFile(path); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("DeleteFile did not remove file: %v", err)
	}
}
