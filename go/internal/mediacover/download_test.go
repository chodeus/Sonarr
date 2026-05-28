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
