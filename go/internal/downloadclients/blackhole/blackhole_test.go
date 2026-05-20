package blackhole_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/blackhole"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// fakeFS captures every write + reports a configurable directory
// existence map. Lets tests assert the exact path + body the
// Provider wrote without touching real disk.
type fakeFS struct {
	writes  map[string][]byte
	dirs    map[string]bool
	statErr error
	removed []string
}

func newFakeFS() *fakeFS {
	return &fakeFS{writes: map[string][]byte{}, dirs: map[string]bool{}}
}

func (f *fakeFS) WriteFile(path string, body []byte, _ os.FileMode) error {
	cp := make([]byte, len(body))
	copy(cp, body)
	f.writes[path] = cp
	return nil
}

func (f *fakeFS) IsDir(path string) (bool, error) {
	if f.statErr != nil {
		return false, f.statErr
	}
	return f.dirs[path], nil
}

func (f *fakeFS) RemoveAll(path string) error {
	f.removed = append(f.removed, path)
	return nil
}

func newProviderWithFS(t *testing.T, settings blackhole.TorrentSettings, fs blackhole.FS) *blackhole.TorrentBlackhole {
	t.Helper()
	return blackhole.New(&downloadclients.DownloadClient{Name: "BH"}, settings, fs, nil)
}

func TestTorrentSettings_Defaults(t *testing.T) {
	s := blackhole.NewTorrentSettings()
	if s.MagnetFileExtension != ".magnet" {
		t.Errorf("MagnetFileExtension = %q, want .magnet", s.MagnetFileExtension)
	}
	if !s.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}

func TestTorrentSettings_Validate(t *testing.T) {
	t.Run("empty TorrentFolder fails", func(t *testing.T) {
		s := blackhole.NewTorrentSettings()
		f := s.Validate()
		if len(f) == 0 {
			t.Fatal("Validate() returned no failures, want TorrentFolder failure")
		}
		if f[0].PropertyName != "TorrentFolder" {
			t.Errorf("first failure = %q, want TorrentFolder", f[0].PropertyName)
		}
	})

	t.Run("empty MagnetFileExtension fails", func(t *testing.T) {
		s := blackhole.NewTorrentSettings()
		s.TorrentFolder = "/tmp/x"
		s.MagnetFileExtension = ""
		f := s.Validate()
		if len(f) != 1 || f[0].PropertyName != "MagnetFileExtension" {
			t.Errorf("Validate() = %+v, want MagnetFileExtension failure", f)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		s := blackhole.NewTorrentSettings()
		s.TorrentFolder = "/tmp/torrents"
		if f := s.Validate(); len(f) != 0 {
			t.Errorf("Validate() = %+v, want empty", f)
		}
	})
}

func TestProvider_Identity(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	if p.Name() != "Torrent Blackhole" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "BH" {
		t.Errorf("Definition not bound")
	}
}

func TestDownload_TorrentBody(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/blackhole/torrents"
	fs := newFakeFS()
	p := newProviderWithFS(t, settings, fs)

	req := &downloadclients.GrabRequest{
		ReleaseTitle: "Some Show S01E01 720p",
		TorrentBody:  []byte("d8:announce..."),
	}
	id, err := p.Download(context.Background(), req)
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	if id != "" {
		t.Errorf("DownloadID = %q, want empty (Blackhole returns nil)", id)
	}
	wantPath := filepath.Join("/blackhole/torrents", "Some Show S01E01 720p.torrent")
	body, ok := fs.writes[wantPath]
	if !ok {
		t.Fatalf("no write at %q; got writes=%v", wantPath, fs.writes)
	}
	if string(body) != "d8:announce..." {
		t.Errorf("body = %q", body)
	}
}

func TestDownload_TorrentBody_CleansFilename(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/bh"
	fs := newFakeFS()
	p := newProviderWithFS(t, settings, fs)

	// Title with characters CleanFileName strips (slash, colon).
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show: With / Slashes",
		TorrentBody:  []byte("x"),
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	// We don't assert the exact cleaned form (depends on
	// CleanFileNameDefault's ColonReplacement default), only that
	// the dangerous characters are gone from the path.
	for path := range fs.writes {
		if strings.ContainsAny(filepath.Base(path), `/\:`+"") && !strings.HasSuffix(path, ".torrent") {
			t.Errorf("write path %q still contains unclean chars", path)
		}
		// "/" in the directory portion is fine; only the basename
		// should be clean.
		base := filepath.Base(path)
		if strings.ContainsAny(base, `:`) {
			t.Errorf("basename %q still contains ':'", base)
		}
	}
}

func TestDownload_Magnet_SaveDisabled(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/bh"
	settings.SaveMagnetFiles = false
	fs := newFakeFS()
	p := newProviderWithFS(t, settings, fs)

	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Title",
		MagnetURL:    "magnet:?xt=urn:btih:abc",
	})
	if !errors.Is(err, blackhole.ErrMagnetNotSupported) {
		t.Errorf("err = %v, want ErrMagnetNotSupported", err)
	}
	if len(fs.writes) != 0 {
		t.Errorf("writes = %v, want empty", fs.writes)
	}
}

func TestDownload_Magnet_SaveEnabled(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/bh"
	settings.SaveMagnetFiles = true
	settings.MagnetFileExtension = ".magnet"
	fs := newFakeFS()
	p := newProviderWithFS(t, settings, fs)

	magnet := "magnet:?xt=urn:btih:deadbeef"
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "MyShow",
		MagnetURL:    magnet,
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	wantPath := filepath.Join("/bh", "MyShow.magnet")
	body, ok := fs.writes[wantPath]
	if !ok {
		t.Fatalf("no write at %q; got %v", wantPath, fs.writes)
	}
	if string(body) != magnet {
		t.Errorf("body = %q, want %q", body, magnet)
	}
}

func TestDownload_Magnet_ExtensionWithoutLeadingDot(t *testing.T) {
	// C# `MagnetFileExtension.Trim('.')` accepts both ".magnet" and
	// "magnet" — the Go port mirrors that.
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/bh"
	settings.SaveMagnetFiles = true
	settings.MagnetFileExtension = "torrent" // operator may set this
	fs := newFakeFS()
	p := newProviderWithFS(t, settings, fs)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:f",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := fs.writes[filepath.Join("/bh", "X.torrent")]; !ok {
		t.Errorf("writes = %v, want /bh/X.torrent", fs.writes)
	}
}

func TestDownload_NoBodyOrMagnet(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/bh"
	p := newProviderWithFS(t, settings, newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want error for empty grab")
	}
}

func TestDownload_NilRequest(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	_, err := p.Download(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want nil-request error")
	}
}

func TestDownload_EmptyTitle(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{TorrentBody: []byte("x")})
	if err == nil {
		t.Fatal("err = nil, want empty-title error")
	}
}

func TestGetItems_ReturnsEmptyNonNil(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if items == nil {
		t.Fatal("items = nil, want non-nil empty slice")
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0 (ScanWatchFolder not yet ported)", len(items))
	}
}

func TestRemoveItem_WithoutDelete(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	err := p.RemoveItem(context.Background(), &downloadclients.Item{OutputPath: ospath.New("/tmp/x")}, false)
	if !errors.Is(err, blackhole.ErrRemoveWithoutDelete) {
		t.Errorf("err = %v, want ErrRemoveWithoutDelete", err)
	}
}

func TestRemoveItem_DeleteDataPath(t *testing.T) {
	fs := newFakeFS()
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), fs)
	err := p.RemoveItem(context.Background(), &downloadclients.Item{OutputPath: ospath.New("/data/show.s01e01")}, true)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fs.removed) != 1 || !strings.HasSuffix(fs.removed[0], "show.s01e01") {
		t.Errorf("removed = %v, want one entry ending in show.s01e01", fs.removed)
	}
}

func TestRemoveItem_NilItem(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	err := p.RemoveItem(context.Background(), nil, true)
	if err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

func TestRemoveItem_EmptyOutputPath(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true)
	if err == nil {
		t.Fatal("err = nil, want empty-OutputPath error")
	}
}

func TestGetStatus(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.WatchFolder = "/watch"
	p := newProviderWithFS(t, settings, newFakeFS())
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !info.IsLocalhost {
		t.Error("IsLocalhost = false, want true")
	}
	if len(info.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %v, want one entry", info.OutputRootFolders)
	}
	if !strings.Contains(info.OutputRootFolders[0].FullPath(), "watch") {
		t.Errorf("OutputRootFolders[0] = %v, want path containing 'watch'", info.OutputRootFolders[0])
	}
}

func TestMarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), newFakeFS())
	err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{})
	if !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestTest_SettingsValidationGate(t *testing.T) {
	// Empty Settings → Settings.Validate fails first; the disk
	// probe MUST NOT run.
	fs := newFakeFS()
	p := newProviderWithFS(t, blackhole.NewTorrentSettings(), fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("failures = empty, want TorrentFolder failure from Settings.Validate")
	}
	if failures[0].PropertyName != "TorrentFolder" {
		t.Errorf("first failure = %q, want TorrentFolder", failures[0].PropertyName)
	}
}

func TestTest_MissingFolders(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/missing/t"
	settings.WatchFolder = "/missing/w"
	fs := newFakeFS()
	// fs.dirs left empty → both folders read as not-a-dir.
	p := newProviderWithFS(t, settings, fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %d (%+v), want 2", len(failures), failures)
	}
}

func TestTest_BothFoldersPresent(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/t"
	settings.WatchFolder = "/w"
	fs := newFakeFS()
	fs.dirs["/t"] = true
	fs.dirs["/w"] = true
	p := newProviderWithFS(t, settings, fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty", failures)
	}
}

func TestTest_EmptyWatchFolderReported(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/t"
	// WatchFolder left empty
	fs := newFakeFS()
	fs.dirs["/t"] = true
	p := newProviderWithFS(t, settings, fs)
	failures, _ := p.Test(context.Background())
	hasWatchFailure := false
	for _, f := range failures {
		if f.PropertyName == "WatchFolder" {
			hasWatchFailure = true
		}
	}
	if !hasWatchFailure {
		t.Errorf("failures = %+v, want WatchFolder entry", failures)
	}
}

func TestTest_StatError(t *testing.T) {
	settings := blackhole.NewTorrentSettings()
	settings.TorrentFolder = "/t"
	settings.WatchFolder = "/w"
	fs := newFakeFS()
	fs.statErr = errors.New("io error")
	p := newProviderWithFS(t, settings, fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) < 2 {
		t.Errorf("failures = %+v, want both folders to surface the io error", failures)
	}
}

// TestRegistry_RegistersTorrentBlackhole confirms the init()
// registration landed in the global Default registry — wiring
// guard.
func TestRegistry_RegistersTorrentBlackhole(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(blackhole.TorrentImplementation, blackhole.TorrentConfigContract) {
		t.Errorf("registry missing (%s, %s)", blackhole.TorrentImplementation, blackhole.TorrentConfigContract)
	}
}
