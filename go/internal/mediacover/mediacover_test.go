package mediacover_test

import (
	"errors"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/mediacover"
)

type stubFS struct {
	exists       map[string]bool
	folderExists map[string]bool
	deletes      []string
	deleteErr    error
	lastWrite    map[string]int64
	lastWriteErr error
}

func (s *stubFS) FileExists(p string) bool { return s.exists[p] }
func (s *stubFS) FolderExists(p string) bool {
	if s.folderExists != nil {
		return s.folderExists[p]
	}
	return false
}
func (s *stubFS) DeleteFolder(p string, _ bool) error {
	s.deletes = append(s.deletes, p)
	return s.deleteErr
}
func (s *stubFS) FileGetLastWrite(p string) (int64, error) {
	if s.lastWriteErr != nil {
		return 0, s.lastWriteErr
	}
	return s.lastWrite[p], nil
}

type stubFolders struct{ root string }

func (s stubFolders) MediaCoverPath() string { return s.root }

type stubURLBase struct{ base string }

func (s stubURLBase) URLBase() string { return s.base }

func newSvc(t *testing.T, fs *stubFS, root, urlBase string) *mediacover.Service {
	t.Helper()
	svc, err := mediacover.NewService(fs, stubFolders{root: root}, stubURLBase{base: urlBase}, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestCoverType_String(t *testing.T) {
	t.Parallel()
	cases := map[mediacover.CoverType]string{
		mediacover.CoverTypeUnknown:    "unknown",
		mediacover.CoverTypePoster:     "poster",
		mediacover.CoverTypeBanner:     "banner",
		mediacover.CoverTypeFanart:     "fanart",
		mediacover.CoverTypeScreenshot: "screenshot",
		mediacover.CoverTypeHeadshot:   "headshot",
		mediacover.CoverTypeClearlogo:  "clearlogo",
	}
	for tp, want := range cases {
		if got := tp.String(); got != want {
			t.Errorf("%v: got %q want %q", tp, got, want)
		}
	}
}

func TestParseCoverType_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, v := range []mediacover.CoverType{
		mediacover.CoverTypeUnknown,
		mediacover.CoverTypePoster,
		mediacover.CoverTypeBanner,
		mediacover.CoverTypeFanart,
		mediacover.CoverTypeScreenshot,
		mediacover.CoverTypeHeadshot,
		mediacover.CoverTypeClearlogo,
	} {
		got, ok := mediacover.ParseCoverType(v.String())
		if !ok || got != v {
			t.Errorf("round-trip %v: got %v ok=%v", v, got, ok)
		}
	}
	// Unknown name returns false.
	if _, ok := mediacover.ParseCoverType("garbage"); ok {
		t.Error("garbage should be rejected")
	}
}

func TestPathExtensionFor(t *testing.T) {
	t.Parallel()
	if mediacover.PathExtensionFor(mediacover.CoverTypeClearlogo) != ".png" {
		t.Error("clearlogo should be .png")
	}
	for _, t1 := range []mediacover.CoverType{
		mediacover.CoverTypePoster, mediacover.CoverTypeBanner,
		mediacover.CoverTypeFanart, mediacover.CoverTypeScreenshot,
		mediacover.CoverTypeHeadshot, mediacover.CoverTypeUnknown,
	} {
		if mediacover.PathExtensionFor(t1) != ".jpg" {
			t.Errorf("%v should be .jpg", t1)
		}
	}
}

func TestResizeHeights(t *testing.T) {
	t.Parallel()
	cases := map[mediacover.CoverType][]int{
		mediacover.CoverTypePoster:     {500, 250},
		mediacover.CoverTypeHeadshot:   {500, 250},
		mediacover.CoverTypeBanner:     {70, 35},
		mediacover.CoverTypeFanart:     {360, 180},
		mediacover.CoverTypeScreenshot: {360, 180},
		mediacover.CoverTypeUnknown:    nil,
		mediacover.CoverTypeClearlogo:  nil,
	}
	for tp, want := range cases {
		got := mediacover.ResizeHeights(tp)
		if len(got) != len(want) {
			t.Errorf("%v: got %v want %v", tp, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%v[%d]: got %d want %d", tp, i, got[i], want[i])
			}
		}
	}
}

func TestNewService_NilGuards(t *testing.T) {
	t.Parallel()
	if _, err := mediacover.NewService(nil, stubFolders{}, stubURLBase{}, nil); err == nil {
		t.Error("nil fs")
	}
	if _, err := mediacover.NewService(&stubFS{}, nil, stubURLBase{}, nil); err == nil {
		t.Error("nil folders")
	}
	if _, err := mediacover.NewService(&stubFS{}, stubFolders{}, nil, nil); err == nil {
		t.Error("nil urlBase")
	}
}

func TestGetCoverPath_NoHeight(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubFS{}, "/var/sonarr/MediaCover", "")
	got := svc.GetCoverPath(7, mediacover.CoverTypePoster, 0)
	want := filepath.Join("/var/sonarr/MediaCover", "7", "poster.jpg")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestGetCoverPath_WithHeight(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubFS{}, "/var/sonarr/MediaCover", "")
	got := svc.GetCoverPath(7, mediacover.CoverTypeFanart, 360)
	want := filepath.Join("/var/sonarr/MediaCover", "7", "fanart-360.jpg")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestGetCoverPath_ClearlogoPNG(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubFS{}, "/x", "")
	got := svc.GetCoverPath(1, mediacover.CoverTypeClearlogo, 0)
	if filepath.Ext(got) != ".png" {
		t.Errorf("clearlogo ext: %q", got)
	}
}

func TestGetSeriesCoverPath(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubFS{}, "/var/sonarr/MediaCover", "")
	got := svc.GetSeriesCoverPath(42)
	want := filepath.Join("/var/sonarr/MediaCover", "42")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestConvertToLocalUrls_HappyPath(t *testing.T) {
	t.Parallel()
	root := "/var/sonarr/MediaCover"
	posterPath := filepath.Join(root, "7", "poster.jpg")
	bannerPath := filepath.Join(root, "7", "banner.jpg")
	fs := &stubFS{
		exists:    map[string]bool{posterPath: true, bannerPath: false},
		lastWrite: map[string]int64{posterPath: 99999},
	}
	svc := newSvc(t, fs, root, "/sonarr")
	covers := []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://up/poster.jpg"},
		{CoverType: mediacover.CoverTypeBanner, RemoteUrl: "https://up/banner.jpg"},
		{CoverType: mediacover.CoverTypeUnknown, RemoteUrl: "https://up/?.jpg"},
	}
	svc.ConvertToLocalUrls(7, covers)

	if covers[0].Url != "/sonarr/MediaCover/7/poster.jpg?lastWrite=99999" {
		t.Errorf("poster url: %q", covers[0].Url)
	}
	if covers[1].Url != "/sonarr/MediaCover/7/banner.jpg" {
		t.Errorf("banner url (no file): %q", covers[1].Url)
	}
	// Unknown cover-type — Url unchanged (empty).
	if covers[2].Url != "" {
		t.Errorf("unknown cover should be skipped: %q", covers[2].Url)
	}
}

func TestConvertToLocalUrls_SeriesIDZeroNoOp(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubFS{}, "/x", "/sonarr")
	covers := []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://up/p.jpg"},
	}
	svc.ConvertToLocalUrls(0, covers)
	if covers[0].Url != "" {
		t.Errorf("seriesID=0 should leave Url unchanged (proxy arm Phase-4): %q", covers[0].Url)
	}
}

func TestConvertToLocalUrls_LastWriteErrorIsTolerated(t *testing.T) {
	t.Parallel()
	root := "/var"
	posterPath := filepath.Join(root, "7", "poster.jpg")
	fs := &stubFS{
		exists:       map[string]bool{posterPath: true},
		lastWriteErr: errors.New("boom"),
	}
	svc := newSvc(t, fs, root, "/s")
	covers := []mediacover.Cover{{CoverType: mediacover.CoverTypePoster}}
	svc.ConvertToLocalUrls(7, covers)
	// URL should NOT include lastWrite suffix when read fails.
	if covers[0].Url != "/s/MediaCover/7/poster.jpg" {
		t.Errorf("err-tolerated url: %q", covers[0].Url)
	}
}

func TestHandleSeriesDeleted_DeletesExistingFolders(t *testing.T) {
	t.Parallel()
	root := "/var/sonarr/MediaCover"
	folder7 := filepath.Join(root, "7")
	folder8 := filepath.Join(root, "8")
	fs := &stubFS{
		folderExists: map[string]bool{folder7: true, folder8: false},
	}
	svc := newSvc(t, fs, root, "")
	if err := svc.HandleSeriesDeleted(t.Context(), []int{7, 8}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(fs.deletes) != 1 || fs.deletes[0] != folder7 {
		t.Errorf("deletes: %+v", fs.deletes)
	}
}

func TestHandleSeriesDeleted_DeleteErrorSwallowed(t *testing.T) {
	t.Parallel()
	root := "/x"
	folder := filepath.Join(root, "7")
	fs := &stubFS{
		folderExists: map[string]bool{folder: true},
		deleteErr:    errors.New("permission denied"),
	}
	svc := newSvc(t, fs, root, "")
	if err := svc.HandleSeriesDeleted(t.Context(), []int{7}); err != nil {
		t.Errorf("delete error should be swallowed-with-warn, got %v", err)
	}
}

func TestHandleSeriesDeleted_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *mediacover.Service
	if err := s.HandleSeriesDeleted(t.Context(), []int{7}); err != nil {
		t.Errorf("nil receiver should no-op, got %v", err)
	}
}

func TestService_NilReceiver_PathOps(t *testing.T) {
	t.Parallel()
	var s *mediacover.Service
	if got := s.GetCoverPath(1, mediacover.CoverTypePoster, 0); got != "" {
		t.Errorf("nil GetCoverPath: %q", got)
	}
	if got := s.GetSeriesCoverPath(1); got != "" {
		t.Errorf("nil GetSeriesCoverPath: %q", got)
	}
}
