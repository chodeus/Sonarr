package pneumatic_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/pneumatic"
	"sonarr.tv/sonarr/internal/indexers"
)

// fakeFS captures every write + lets the test seed a per-folder
// file list, sizes, mtimes, and lock states.
type fakeFS struct {
	writes   map[string][]byte
	dirs     map[string]bool
	files    map[string][]string
	sizes    map[string]int64
	mtimes   map[string]time.Time
	locked   map[string]bool
	statErr  error
	listErr  error
	writeErr error
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		writes: map[string][]byte{},
		dirs:   map[string]bool{},
		files:  map[string][]string{},
		sizes:  map[string]int64{},
		mtimes: map[string]time.Time{},
		locked: map[string]bool{},
	}
}

func (f *fakeFS) WriteFile(path string, body []byte, _ os.FileMode) error {
	if f.writeErr != nil {
		return f.writeErr
	}
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

func (f *fakeFS) ListFiles(path string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]string(nil), f.files[path]...), nil
}

func (f *fakeFS) FileSize(path string) (int64, error) {
	if sz, ok := f.sizes[path]; ok {
		return sz, nil
	}
	return 0, errors.New("not found")
}

func (f *fakeFS) FileModTime(path string) (time.Time, error) {
	if t, ok := f.mtimes[path]; ok {
		return t, nil
	}
	return time.Time{}, errors.New("not found")
}

func (f *fakeFS) IsFileLocked(path string) (bool, error) {
	return f.locked[path], nil
}

func newProvider(t *testing.T, settings pneumatic.Settings, fs pneumatic.FS) *pneumatic.Pneumatic {
	t.Helper()
	return pneumatic.New(&downloadclients.DownloadClient{Name: "PN"}, settings, fs, nil)
}

func TestSettings_Defaults(t *testing.T) {
	s := pneumatic.NewSettings()
	if s.NzbFolder != "" || s.StrmFolder != "" {
		t.Errorf("NewSettings = %+v, want zero-valued", s)
	}
}

func TestSettings_Validate(t *testing.T) {
	t.Run("empty NzbFolder fails", func(t *testing.T) {
		s := pneumatic.Settings{StrmFolder: "/s"}
		f := s.Validate()
		if len(f) != 1 || f[0].PropertyName != "NzbFolder" {
			t.Errorf("Validate() = %+v, want NzbFolder failure", f)
		}
	})
	t.Run("empty StrmFolder fails", func(t *testing.T) {
		s := pneumatic.Settings{NzbFolder: "/n"}
		f := s.Validate()
		if len(f) != 1 || f[0].PropertyName != "StrmFolder" {
			t.Errorf("Validate() = %+v, want StrmFolder failure", f)
		}
	})
	t.Run("both missing", func(t *testing.T) {
		if f := pneumatic.NewSettings().Validate(); len(f) != 2 {
			t.Errorf("Validate() = %+v, want two failures", f)
		}
	})
	t.Run("happy path", func(t *testing.T) {
		s := pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}
		if f := s.Validate(); len(f) != 0 {
			t.Errorf("Validate() = %+v, want empty", f)
		}
	})
}

func TestIdentity(t *testing.T) {
	p := newProvider(t, pneumatic.NewSettings(), newFakeFS())
	if p.Name() != "Pneumatic" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol = %v, want Usenet", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "PN" {
		t.Errorf("Definition not bound: %+v", p.Definition())
	}
}

func TestDownload_WritesBothFiles(t *testing.T) {
	settings := pneumatic.Settings{NzbFolder: "/nzb", StrmFolder: "/strm"}
	fs := newFakeFS()
	// Seed the mtime the downloadID resolver reads after WriteFile.
	stamp := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	strmPath := filepath.Join("/strm", "Some Show S01E01.strm")
	fs.mtimes[strmPath] = stamp

	p := newProvider(t, settings, fs)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Some Show S01E01",
		NzbBody:      []byte("<nzb-bytes>"),
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	nzbPath := filepath.Join("/nzb", "Some Show S01E01.nzb")
	if _, ok := fs.writes[nzbPath]; !ok {
		t.Errorf("no write at %q; got %v", nzbPath, fs.writes)
	}
	if string(fs.writes[nzbPath]) != "<nzb-bytes>" {
		t.Errorf("nzb body = %q", fs.writes[nzbPath])
	}
	strmBody, ok := fs.writes[strmPath]
	if !ok {
		t.Fatalf("no strm at %q", strmPath)
	}
	if !strings.Contains(string(strmBody), "plugin://plugin.program.pneumatic") {
		t.Errorf("strm body doesn't reference the Kodi plugin URL: %q", strmBody)
	}
	if !strings.Contains(string(strmBody), nzbPath) {
		t.Errorf("strm body doesn't reference the nzb path: %q", strmBody)
	}
	if !strings.HasPrefix(id, "PN_") {
		t.Errorf("DownloadID = %q, want PN_<...> prefix", id)
	}
}

func TestDownload_NilRequest(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, newFakeFS())
	_, err := p.Download(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want nil-request error")
	}
}

func TestDownload_EmptyTitle(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{NzbBody: []byte("x")})
	if err == nil {
		t.Fatal("err = nil, want empty-title error")
	}
}

func TestDownload_EmptyBody(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want empty-NzbBody error")
	}
}

func TestDownload_MissingStrmFolder(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n"}, newFakeFS())
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("x"),
	})
	if !errors.Is(err, pneumatic.ErrStrmFolderRequired) {
		t.Errorf("err = %v, want ErrStrmFolderRequired", err)
	}
}

func TestDownload_WriteFailureSurfacesError(t *testing.T) {
	fs := newFakeFS()
	fs.writeErr = errors.New("disk full")
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, fs)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("y"),
	})
	if err == nil {
		t.Fatal("err = nil, want write-failure surfaced")
	}
}

func TestGetItems_HappyPath(t *testing.T) {
	settings := pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}
	fs := newFakeFS()
	strm := "/s/Show.S01E01.strm"
	notStrm := "/s/Show.S01E01.nfo"
	fs.files["/s"] = []string{strm, notStrm}
	fs.sizes[strm] = 4096
	fs.mtimes[strm] = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	p := newProvider(t, settings, fs)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d (%+v), want 1 (.nfo filtered out)", len(items), items)
	}
	got := items[0]
	if got.TotalSize != 4096 {
		t.Errorf("TotalSize = %d, want 4096", got.TotalSize)
	}
	if got.Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("Status = %v, want Completed", got.Status)
	}
	if !got.CanBeRemoved || !got.CanMoveFiles {
		t.Errorf("flags: CanBeRemoved=%v CanMoveFiles=%v, want both true", got.CanBeRemoved, got.CanMoveFiles)
	}
	if !strings.HasPrefix(got.DownloadID, "PN_") {
		t.Errorf("DownloadID = %q, want PN_ prefix", got.DownloadID)
	}
	if got.DownloadClientInfo == nil || got.DownloadClientInfo.Type != pneumatic.Implementation {
		t.Errorf("DownloadClientInfo Type mismatch: %+v", got.DownloadClientInfo)
	}
}

func TestGetItems_LockedFlipsDownloading(t *testing.T) {
	settings := pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}
	fs := newFakeFS()
	strm := "/s/InFlight.strm"
	fs.files["/s"] = []string{strm}
	fs.sizes[strm] = 1
	fs.mtimes[strm] = time.Now()
	fs.locked[strm] = true

	p := newProvider(t, settings, fs)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("len = %d", len(items))
	}
	if items[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status = %v, want Downloading under lock", items[0].Status)
	}
}

func TestGetItems_EmptyStrmFolderReturnsEmpty(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n"}, newFakeFS())
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len = %d, want 0 when StrmFolder unset", len(items))
	}
}

func TestGetItems_ListError(t *testing.T) {
	fs := newFakeFS()
	fs.listErr = errors.New("io")
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, fs)
	_, err := p.GetItems(context.Background())
	if err == nil {
		t.Fatal("err = nil, want list error surfaced")
	}
}

func TestRemoveItem_AlwaysUnsupported(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, newFakeFS())
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, pneumatic.ErrRemoveNotSupported) {
		t.Errorf("deleteData=true err = %v, want ErrRemoveNotSupported", err)
	}
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, false); !errors.Is(err, pneumatic.ErrRemoveNotSupported) {
		t.Errorf("deleteData=false err = %v, want ErrRemoveNotSupported", err)
	}
}

func TestGetStatus(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, newFakeFS())
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !info.IsLocalhost {
		t.Error("IsLocalhost = false, want true")
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty", info.OutputRootFolders)
	}
}

func TestMarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := newProvider(t, pneumatic.NewSettings(), newFakeFS())
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestTest_SettingsValidationGate(t *testing.T) {
	p := newProvider(t, pneumatic.NewSettings(), newFakeFS())
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 2 {
		t.Errorf("failures = %+v, want two Settings failures", failures)
	}
}

func TestTest_MissingFolders(t *testing.T) {
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/missing/n", StrmFolder: "/missing/s"}, newFakeFS())
	failures, _ := p.Test(context.Background())
	if len(failures) != 2 {
		t.Errorf("failures = %d (%+v), want 2", len(failures), failures)
	}
}

func TestTest_BothFoldersPresent(t *testing.T) {
	fs := newFakeFS()
	fs.dirs["/n"] = true
	fs.dirs["/s"] = true
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, fs)
	failures, _ := p.Test(context.Background())
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty", failures)
	}
}

func TestTest_StatError(t *testing.T) {
	fs := newFakeFS()
	fs.statErr = errors.New("io")
	p := newProvider(t, pneumatic.Settings{NzbFolder: "/n", StrmFolder: "/s"}, fs)
	failures, _ := p.Test(context.Background())
	if len(failures) < 2 {
		t.Errorf("failures = %+v, want both folders to surface the io error", failures)
	}
}

// TestRegistry_RegistersPneumatic confirms the init() registration
// landed in the global Default registry.
func TestRegistry_RegistersPneumatic(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(pneumatic.Implementation, pneumatic.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", pneumatic.Implementation, pneumatic.ConfigContract)
	}
}
