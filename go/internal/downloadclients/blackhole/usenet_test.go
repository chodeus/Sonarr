package blackhole_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/blackhole"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

func newUsenetWithFS(t *testing.T, settings blackhole.UsenetSettings, fs blackhole.FS) *blackhole.UsenetBlackhole {
	t.Helper()
	return blackhole.NewUsenet(&downloadclients.DownloadClient{Name: "UBH"}, settings, fs, nil, nil)
}

func TestUsenetSettings_Defaults(t *testing.T) {
	s := blackhole.NewUsenetSettings()
	// C# default constructor leaves both paths blank.
	if s.NzbFolder != "" || s.WatchFolder != "" {
		t.Errorf("NewUsenetSettings = %+v, want zero-valued", s)
	}
}

func TestUsenetSettings_Validate(t *testing.T) {
	t.Run("empty NzbFolder fails", func(t *testing.T) {
		s := blackhole.NewUsenetSettings()
		s.WatchFolder = "/w"
		f := s.Validate()
		if len(f) != 1 || f[0].PropertyName != "NzbFolder" {
			t.Errorf("Validate() = %+v, want single NzbFolder failure", f)
		}
	})

	t.Run("empty WatchFolder fails", func(t *testing.T) {
		s := blackhole.NewUsenetSettings()
		s.NzbFolder = "/n"
		f := s.Validate()
		if len(f) != 1 || f[0].PropertyName != "WatchFolder" {
			t.Errorf("Validate() = %+v, want single WatchFolder failure", f)
		}
	})

	t.Run("both missing surfaces both", func(t *testing.T) {
		f := blackhole.NewUsenetSettings().Validate()
		if len(f) != 2 {
			t.Errorf("Validate() = %+v, want two failures", f)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		s := blackhole.UsenetSettings{NzbFolder: "/n", WatchFolder: "/w"}
		if f := s.Validate(); len(f) != 0 {
			t.Errorf("Validate() = %+v, want empty", f)
		}
	})
}

func TestUsenet_Identity(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	if p.Name() != "Usenet Blackhole" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol = %v, want Usenet", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "UBH" {
		t.Errorf("Definition not bound")
	}
}

func TestUsenet_Download_NzbBody(t *testing.T) {
	settings := blackhole.UsenetSettings{NzbFolder: "/blackhole/nzbs", WatchFolder: "/blackhole/watch"}
	fs := newFakeFS()
	p := newUsenetWithFS(t, settings, fs)

	req := &downloadclients.GrabRequest{
		ReleaseTitle: "Some Show S01E02 1080p",
		NzbBody:      []byte("<nzb>...</nzb>"),
	}
	id, err := p.Download(context.Background(), req)
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	if id != "" {
		t.Errorf("DownloadID = %q, want empty (Blackhole returns nil)", id)
	}
	wantPath := filepath.Join("/blackhole/nzbs", "Some Show S01E02 1080p.nzb")
	body, ok := fs.writes[wantPath]
	if !ok {
		t.Fatalf("no write at %q; got writes=%v", wantPath, fs.writes)
	}
	if string(body) != "<nzb>...</nzb>" {
		t.Errorf("body = %q", body)
	}
}

func TestUsenet_Download_CleansFilename(t *testing.T) {
	settings := blackhole.UsenetSettings{NzbFolder: "/bh", WatchFolder: "/w"}
	fs := newFakeFS()
	p := newUsenetWithFS(t, settings, fs)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show: With / Slashes",
		NzbBody:      []byte("x"),
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	for path := range fs.writes {
		base := filepath.Base(path)
		if strings.ContainsAny(base, `:`) {
			t.Errorf("basename %q still contains ':'", base)
		}
	}
}

func TestUsenet_Download_EmptyBody(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.UsenetSettings{NzbFolder: "/n"}, newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want empty-NzbBody error")
	}
}

func TestUsenet_Download_NilRequest(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	_, err := p.Download(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want nil-request error")
	}
}

func TestUsenet_Download_EmptyTitle(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{NzbBody: []byte("x")})
	if err == nil {
		t.Fatal("err = nil, want empty-title error")
	}
}

func TestUsenet_GetItems_NilScannerReturnsEmptyNonNil(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if items == nil {
		t.Fatal("items = nil, want non-nil empty slice")
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(items))
	}
}

func TestUsenet_GetItems_WiredScanner_Conversion(t *testing.T) {
	fd := newFakeDisk()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	folder := "/watch/Movie.2026"
	file := "/watch/Movie.2026/payload.mkv"
	fd.dirs["/watch"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now.Add(-2 * time.Minute)}
	fd.entries[file] = fakeDiskEntry{mtime: now.Add(-2 * time.Minute), size: 1500}
	scanner := blackhole.NewScanWatchFolder(fd, newFakeScanner(), nil, func() time.Time { return now })

	settings := blackhole.UsenetSettings{NzbFolder: "/n", WatchFolder: "/watch"}
	def := &downloadclients.DownloadClient{Name: "BH-Usenet", ID: 11, RemoveCompletedDownloads: false}
	p := blackhole.NewUsenet(def, settings, newFakeFS(), scanner, nil)

	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	got := items[0]
	if !strings.HasPrefix(got.DownloadID, "BH-Usenet_") {
		t.Errorf("DownloadID = %q, want BH-Usenet_<...> prefix", got.DownloadID)
	}
	if got.Category != "sonarr" {
		t.Errorf("Category = %q", got.Category)
	}
	if !got.CanMoveFiles {
		t.Errorf("CanMoveFiles = false, want true (Usenet half is always movable)")
	}
	if got.CanBeRemoved {
		t.Errorf("CanBeRemoved = true, want false (RemoveCompletedDownloads=false)")
	}
	if got.DownloadClientInfo == nil || got.DownloadClientInfo.Type != blackhole.UsenetImplementation {
		t.Errorf("DownloadClientInfo Type mismatch: %+v", got.DownloadClientInfo)
	}
	if got.TotalSize != 1500 {
		t.Errorf("TotalSize = %d, want 1500", got.TotalSize)
	}
}

func TestUsenet_RemoveItem_WithoutDelete(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	err := p.RemoveItem(context.Background(), &downloadclients.Item{OutputPath: ospath.New("/tmp/x")}, false)
	if !errors.Is(err, blackhole.ErrRemoveWithoutDelete) {
		t.Errorf("err = %v, want ErrRemoveWithoutDelete", err)
	}
}

func TestUsenet_RemoveItem_DeleteDataPath(t *testing.T) {
	fs := newFakeFS()
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), fs)
	err := p.RemoveItem(context.Background(), &downloadclients.Item{OutputPath: ospath.New("/data/show.s01e02")}, true)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fs.removed) != 1 || !strings.HasSuffix(fs.removed[0], "show.s01e02") {
		t.Errorf("removed = %v, want one entry ending in show.s01e02", fs.removed)
	}
}

func TestUsenet_RemoveItem_NilItem(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	if err := p.RemoveItem(context.Background(), nil, true); err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

func TestUsenet_RemoveItem_EmptyOutputPath(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); err == nil {
		t.Fatal("err = nil, want empty-OutputPath error")
	}
}

func TestUsenet_GetStatus(t *testing.T) {
	settings := blackhole.UsenetSettings{NzbFolder: "/n", WatchFolder: "/watch"}
	p := newUsenetWithFS(t, settings, newFakeFS())
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

func TestUsenet_MarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), newFakeFS())
	err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{})
	if !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestUsenet_Test_SettingsValidationGate(t *testing.T) {
	// Both folders blank → Settings.Validate fails; disk probe MUST
	// NOT run.
	fs := newFakeFS()
	p := newUsenetWithFS(t, blackhole.NewUsenetSettings(), fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %+v, want two Settings failures", failures)
	}
}

func TestUsenet_Test_MissingFolders(t *testing.T) {
	settings := blackhole.UsenetSettings{NzbFolder: "/missing/n", WatchFolder: "/missing/w"}
	p := newUsenetWithFS(t, settings, newFakeFS())
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %d (%+v), want 2", len(failures), failures)
	}
}

func TestUsenet_Test_BothFoldersPresent(t *testing.T) {
	settings := blackhole.UsenetSettings{NzbFolder: "/n", WatchFolder: "/w"}
	fs := newFakeFS()
	fs.dirs["/n"] = true
	fs.dirs["/w"] = true
	p := newUsenetWithFS(t, settings, fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty", failures)
	}
}

func TestUsenet_Test_StatError(t *testing.T) {
	settings := blackhole.UsenetSettings{NzbFolder: "/n", WatchFolder: "/w"}
	fs := newFakeFS()
	fs.statErr = errors.New("io error")
	p := newUsenetWithFS(t, settings, fs)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) < 2 {
		t.Errorf("failures = %+v, want both folders to surface the io error", failures)
	}
}

// TestRegistry_RegistersUsenetBlackhole confirms the init()
// registration landed in the global Default registry.
func TestRegistry_RegistersUsenetBlackhole(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(blackhole.UsenetImplementation, blackhole.UsenetConfigContract) {
		t.Errorf("registry missing (%s, %s)", blackhole.UsenetImplementation, blackhole.UsenetConfigContract)
	}
}
