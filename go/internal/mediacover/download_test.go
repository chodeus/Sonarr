package mediacover_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

type fakeDownloader struct {
	contentLen  map[string]int64
	headErr     error
	downloaded  []string
	downloadErr map[string]error
}

func (f *fakeDownloader) ContentLength(_ context.Context, url string) (int64, bool, error) {
	if f.headErr != nil {
		return 0, false, f.headErr
	}
	n, ok := f.contentLen[url]
	return n, ok, nil
}

func (f *fakeDownloader) DownloadFile(_ context.Context, url, path string) error {
	if f.downloadErr != nil {
		if e := f.downloadErr[url]; e != nil {
			return e
		}
	}
	f.downloaded = append(f.downloaded, path)
	return nil
}

func mkSeriesWithImages(t *testing.T, id int, images string) *series.Series {
	t.Helper()
	return &series.Series{ID: id, Images: images}
}

func TestEnsureCovers_NilDownloader_NoOp(t *testing.T) {
	t.Parallel()
	fs := &stubFS{}
	svc := newSvc(t, fs, "/covers", "")
	ser := mkSeriesWithImages(t, 1, `[{"coverType":1,"remoteUrl":"http://x/p.jpg"}]`)
	updated, err := svc.EnsureCovers(context.Background(), ser)
	if err != nil || updated {
		t.Fatalf("nil downloader: got updated=%v err=%v want false,nil", updated, err)
	}
}

func TestEnsureCovers_DownloadsMissing(t *testing.T) {
	t.Parallel()
	fs := &stubFS{} // no files exist → not already present
	svc := newSvc(t, fs, "/covers", "")
	dl := &fakeDownloader{}
	svc.SetDownloader(dl)
	// Poster (1) + Banner (2) + Unknown (0, skipped).
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"},{"coverType":2,"remoteUrl":"http://x/banner.jpg"},{"coverType":0,"remoteUrl":"http://x/none"}]`)
	updated, err := svc.EnsureCovers(context.Background(), ser)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !updated {
		t.Error("expected updated=true")
	}
	if len(dl.downloaded) != 2 {
		t.Errorf("expected 2 downloads (poster+banner, unknown skipped); got %v", dl.downloaded)
	}
}

func TestEnsureCovers_SkipsWhenSizeMatches(t *testing.T) {
	t.Parallel()
	posterPath := "/covers/7/poster.jpg"
	fs := &stubFS{
		exists:   map[string]bool{posterPath: true},
		fileSize: map[string]int64{posterPath: 1234},
	}
	svc := newSvc(t, fs, "/covers", "")
	dl := &fakeDownloader{contentLen: map[string]int64{"http://x/poster.jpg": 1234}}
	svc.SetDownloader(dl)
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	updated, err := svc.EnsureCovers(context.Background(), ser)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if updated || len(dl.downloaded) != 0 {
		t.Errorf("size-match must skip download; got updated=%v downloaded=%v", updated, dl.downloaded)
	}
}

func TestEnsureCovers_RedownloadsOnSizeMismatch(t *testing.T) {
	t.Parallel()
	posterPath := "/covers/7/poster.jpg"
	fs := &stubFS{
		exists:   map[string]bool{posterPath: true},
		fileSize: map[string]int64{posterPath: 999}, // stale size
	}
	svc := newSvc(t, fs, "/covers", "")
	dl := &fakeDownloader{contentLen: map[string]int64{"http://x/poster.jpg": 1234}}
	svc.SetDownloader(dl)
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	updated, _ := svc.EnsureCovers(context.Background(), ser)
	if !updated || len(dl.downloaded) != 1 {
		t.Errorf("size-mismatch must re-download; got updated=%v downloaded=%v", updated, dl.downloaded)
	}
}

func TestEnsureCovers_AbsorbsDownloadError(t *testing.T) {
	t.Parallel()
	fs := &stubFS{}
	svc := newSvc(t, fs, "/covers", "")
	dl := &fakeDownloader{downloadErr: map[string]error{"http://x/poster.jpg": errors.New("boom")}}
	svc.SetDownloader(dl)
	// Poster fails, banner succeeds → updated=true (banner), no error returned.
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"},{"coverType":2,"remoteUrl":"http://x/banner.jpg"}]`)
	updated, err := svc.EnsureCovers(context.Background(), ser)
	if err != nil {
		t.Fatalf("per-cover error must be absorbed; got %v", err)
	}
	if !updated || len(dl.downloaded) != 1 || dl.downloaded[0] != "/covers/7/banner.jpg" {
		t.Errorf("expected only banner downloaded; got updated=%v downloaded=%v", updated, dl.downloaded)
	}
}

func TestHandleSeriesUpdated_DelegatesToEnsureCovers(t *testing.T) {
	t.Parallel()
	fs := &stubFS{}
	svc := newSvc(t, fs, "/covers", "")
	dl := &fakeDownloader{}
	svc.SetDownloader(dl)
	ser := mkSeriesWithImages(t, 3, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	updated, err := svc.HandleSeriesUpdated(context.Background(), ser)
	if err != nil || !updated || len(dl.downloaded) != 1 {
		t.Errorf("HandleSeriesUpdated: got updated=%v err=%v downloaded=%v", updated, err, dl.downloaded)
	}
}

type fakeResizer struct {
	calls  []resizeCall
	failOn map[string]bool // dest → return error
}

type resizeCall struct {
	source string
	dest   string
	height int
}

func (r *fakeResizer) Resize(source, dest string, height int) error {
	r.calls = append(r.calls, resizeCall{source, dest, height})
	if r.failOn[dest] {
		return errors.New("resize boom")
	}
	return nil
}

func TestEnsureCovers_ResizesFreshDownload(t *testing.T) {
	t.Parallel()
	fs := &stubFS{} // master missing → downloads; variants missing → resize
	svc := newSvc(t, fs, "/covers", "")
	svc.SetDownloader(&fakeDownloader{})
	rz := &fakeResizer{}
	svc.SetResizer(rz)
	// Poster → resize heights 500, 250.
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	if _, err := svc.EnsureCovers(context.Background(), ser); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rz.calls) != 2 {
		t.Fatalf("expected 2 resize calls (500,250); got %v", rz.calls)
	}
	got := map[int]bool{rz.calls[0].height: true, rz.calls[1].height: true}
	if !got[500] || !got[250] {
		t.Errorf("expected heights {500,250}; got %v", rz.calls)
	}
	if rz.calls[0].source != "/covers/7/poster.jpg" || rz.calls[0].dest != "/covers/7/poster-500.jpg" {
		t.Errorf("resize paths: got %+v", rz.calls[0])
	}
}

func TestEnsureCovers_SkipsResizeWhenVariantPresent(t *testing.T) {
	t.Parallel()
	posterPath := "/covers/7/poster.jpg"
	v500 := "/covers/7/poster-500.jpg"
	v250 := "/covers/7/poster-250.jpg"
	// Master present + size-match → not re-downloaded (not forced); both
	// variants present + non-zero → skipped.
	fs := &stubFS{
		exists:   map[string]bool{posterPath: true, v500: true, v250: true},
		fileSize: map[string]int64{posterPath: 100, v500: 10, v250: 5},
	}
	svc := newSvc(t, fs, "/covers", "")
	svc.SetDownloader(&fakeDownloader{contentLen: map[string]int64{"http://x/poster.jpg": 100}})
	rz := &fakeResizer{}
	svc.SetResizer(rz)
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	if _, err := svc.EnsureCovers(context.Background(), ser); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rz.calls) != 0 {
		t.Errorf("present non-forced variants must skip resize; got %v", rz.calls)
	}
}

func TestEnsureCovers_RegeneratesZeroByteVariant(t *testing.T) {
	t.Parallel()
	posterPath := "/covers/7/poster.jpg"
	v500 := "/covers/7/poster-500.jpg"
	v250 := "/covers/7/poster-250.jpg"
	// Master size-match (not forced), but the 500 variant is zero-byte →
	// regenerated; the 250 variant is healthy → skipped.
	fs := &stubFS{
		exists:   map[string]bool{posterPath: true, v500: true, v250: true},
		fileSize: map[string]int64{posterPath: 100, v500: 0, v250: 5},
	}
	svc := newSvc(t, fs, "/covers", "")
	svc.SetDownloader(&fakeDownloader{contentLen: map[string]int64{"http://x/poster.jpg": 100}})
	rz := &fakeResizer{}
	svc.SetResizer(rz)
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	if _, err := svc.EnsureCovers(context.Background(), ser); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rz.calls) != 1 || rz.calls[0].height != 500 {
		t.Errorf("expected only the zero-byte 500 variant regenerated; got %v", rz.calls)
	}
}

func TestEnsureCovers_NilResizerSkipsResize(t *testing.T) {
	t.Parallel()
	fs := &stubFS{}
	svc := newSvc(t, fs, "/covers", "")
	svc.SetDownloader(&fakeDownloader{})
	// No resizer set → download only, no panic.
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	if _, err := svc.EnsureCovers(context.Background(), ser); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestEnsureCovers_AbsorbsResizeError(t *testing.T) {
	t.Parallel()
	fs := &stubFS{}
	svc := newSvc(t, fs, "/covers", "")
	svc.SetDownloader(&fakeDownloader{})
	rz := &fakeResizer{failOn: map[string]bool{"/covers/7/poster-500.jpg": true}}
	svc.SetResizer(rz)
	ser := mkSeriesWithImages(t, 7, `[{"coverType":1,"remoteUrl":"http://x/poster.jpg"}]`)
	// 500 fails but 250 still attempted; no error propagates.
	if _, err := svc.EnsureCovers(context.Background(), ser); err != nil {
		t.Fatalf("resize error must be absorbed; got %v", err)
	}
	if len(rz.calls) != 2 {
		t.Errorf("both heights attempted despite 500 failing; got %v", rz.calls)
	}
}
