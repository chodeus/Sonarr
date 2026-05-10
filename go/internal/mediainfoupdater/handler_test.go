package mediainfoupdater_test

import (
	"errors"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/mediainfoupdater"
	"sonarr.tv/sonarr/internal/series"
)

func TestHandleSeriesScanned_NilReceiverNoOp(t *testing.T) {
	t.Parallel()
	var s *mediainfoupdater.UpdateService
	if err := s.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{}); err != nil {
		t.Errorf("nil receiver: %v", err)
	}
}

func TestHandleSeriesScanned_NilSeriesNoOp(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	fileReader := &stubFileReader{}
	svc := newSvcWithFileReader(t, stubFS{}, files, fileReader, reader, stubCfg{enabled: true})
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: nil}); err != nil {
		t.Errorf("nil Series: %v", err)
	}
	if fileReader.calls != 0 {
		t.Errorf("nil Series: GetFilesBySeries should not be called")
	}
}

func TestHandleSeriesScanned_DisabledByConfig(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	fileReader := &stubFileReader{}
	svc := newSvcWithFileReader(t, stubFS{}, files, fileReader, reader, stubCfg{enabled: false})
	ev := mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 7, Path: "/tv/Show"}}
	if err := svc.HandleSeriesScanned(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if fileReader.calls != 0 {
		t.Errorf("disabled: GetFilesBySeries should not be called")
	}
	if reader.calls != 0 {
		t.Errorf("disabled: GetMediaInfo should not be called")
	}
}

func TestHandleSeriesScanned_FileReaderErrorPropagates(t *testing.T) {
	t.Parallel()
	rdErr := errors.New("db down")
	files := &stubFiles{}
	reader := &stubReader{}
	fileReader := &stubFileReader{err: rdErr}
	svc := newSvcWithFileReader(t, stubFS{}, files, fileReader, reader, stubCfg{enabled: true})
	ev := mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 7, Path: "/tv/Show"}}
	err := svc.HandleSeriesScanned(t.Context(), ev)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, rdErr) {
		t.Errorf("error chain missing reader err: %v", err)
	}
}

func TestHandleSeriesScanned_FiltersStaleAndMissingMediaInfo(t *testing.T) {
	t.Parallel()
	// Three files: nil MediaInfo / stale SchemaRevision / current.
	// The filter should pick rows 0 and 1, skip row 2.
	rows := []episodefiles.File{
		{ID: 1, RelativePath: "ep1.mkv", MediaInfo: nil},
		{ID: 2, RelativePath: "ep2.mkv", MediaInfo: &mediainfo.Model{SchemaRevision: mediainfoupdater.MinimumSchemaRevision - 1}},
		{ID: 3, RelativePath: "ep3.mkv", MediaInfo: &mediainfo.Model{SchemaRevision: mediainfoupdater.MinimumSchemaRevision}},
	}
	exists := map[string]bool{
		filepath.Join("/tv/Show", "ep1.mkv"): true,
		filepath.Join("/tv/Show", "ep2.mkv"): true,
		filepath.Join("/tv/Show", "ep3.mkv"): true,
	}
	files := &stubFiles{}
	reader := &stubReader{model: &mediainfo.Model{SchemaRevision: mediainfoupdater.MinimumSchemaRevision}}
	fileReader := &stubFileReader{rows: rows}
	svc := newSvcWithFileReader(t, stubFS{exists: exists}, files, fileReader, reader, stubCfg{enabled: true})

	ev := mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 7, Path: "/tv/Show"}}
	if err := svc.HandleSeriesScanned(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if fileReader.got != 7 {
		t.Errorf("GetFilesBySeries called with %d, want 7", fileReader.got)
	}
	// Reader called for filtered rows only (rows 0 and 1).
	if reader.calls != 2 {
		t.Errorf("reader.GetMediaInfo calls = %d, want 2", reader.calls)
	}
	if files.calls != 2 {
		t.Errorf("files.Update calls = %d, want 2", files.calls)
	}
}

func TestHandleSeriesScanned_AbortsOnFirstUpdateError(t *testing.T) {
	t.Parallel()
	persistErr := errors.New("persist failure")
	rows := []episodefiles.File{
		{ID: 1, RelativePath: "ep1.mkv"},
		{ID: 2, RelativePath: "ep2.mkv"},
	}
	exists := map[string]bool{
		filepath.Join("/tv/Show", "ep1.mkv"): true,
		filepath.Join("/tv/Show", "ep2.mkv"): true,
	}
	files := &stubFiles{err: persistErr}
	reader := &stubReader{model: &mediainfo.Model{SchemaRevision: 14}}
	fileReader := &stubFileReader{rows: rows}
	svc := newSvcWithFileReader(t, stubFS{exists: exists}, files, fileReader, reader, stubCfg{enabled: true})

	ev := mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 7, Path: "/tv/Show"}}
	err := svc.HandleSeriesScanned(t.Context(), ev)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, persistErr) {
		t.Errorf("error chain missing persist err: %v", err)
	}
	// Should have aborted after first failure.
	if reader.calls != 1 {
		t.Errorf("reader called %d times after abort, want 1", reader.calls)
	}
}

func TestHandleSeriesScanned_EmptyFileListNoOp(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	fileReader := &stubFileReader{rows: nil}
	svc := newSvcWithFileReader(t, stubFS{}, files, fileReader, reader, stubCfg{enabled: true})

	ev := mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 7, Path: "/tv/Show"}}
	if err := svc.HandleSeriesScanned(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if fileReader.calls != 1 {
		t.Errorf("GetFilesBySeries should be called once even for empty result; calls=%d", fileReader.calls)
	}
	if reader.calls != 0 {
		t.Errorf("empty list: reader should not be called")
	}
}

func TestHandleSeriesScanned_AllCurrentSchemaSkipsAllRows(t *testing.T) {
	t.Parallel()
	rows := []episodefiles.File{
		{ID: 1, RelativePath: "ep1.mkv", MediaInfo: &mediainfo.Model{SchemaRevision: mediainfoupdater.MinimumSchemaRevision}},
		{ID: 2, RelativePath: "ep2.mkv", MediaInfo: &mediainfo.Model{SchemaRevision: mediainfoupdater.MinimumSchemaRevision + 5}},
	}
	files := &stubFiles{}
	reader := &stubReader{}
	fileReader := &stubFileReader{rows: rows}
	svc := newSvcWithFileReader(t, stubFS{}, files, fileReader, reader, stubCfg{enabled: true})

	ev := mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 7, Path: "/tv/Show"}}
	if err := svc.HandleSeriesScanned(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if reader.calls != 0 {
		t.Errorf("all current: reader should not be called; calls=%d", reader.calls)
	}
}
