package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/quality"
)

type fakeEpisodeFileWriter struct {
	get         map[int]*episodefiles.File
	getMany     []episodefiles.File
	getErr      error
	deletedIDs  []int
	deleteErr   error
	updated     *episodefiles.File
	updatedMany []*episodefiles.File
	updateErr   error
}

func (f *fakeEpisodeFileWriter) Update(_ context.Context, file *episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	cp := *file
	f.updated = &cp
	return nil
}

func (f *fakeEpisodeFileWriter) UpdateMany(_ context.Context, files []*episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for _, fi := range files {
		cp := *fi
		f.updatedMany = append(f.updatedMany, &cp)
	}
	return nil
}

func (f *fakeEpisodeFileWriter) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if v, ok := f.get[id]; ok {
		return v, nil
	}
	return nil, episodefiles.ErrNotFound
}

func (f *fakeEpisodeFileWriter) GetFiles(_ context.Context, _ []int) ([]episodefiles.File, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getMany, nil
}

func (f *fakeEpisodeFileWriter) Delete(_ context.Context, file *episodefiles.File, _ episodefiles.DeleteMediaFileReason) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, file.ID)
	return nil
}

func TestEpisodeFileDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SeriesID: 1, RelativePath: "Show.S01E01.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/7", nil)
	req.SetPathValue("id", "7")
	EpisodeFileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.deletedIDs) != 1 || w.deletedIDs[0] != 7 {
		t.Errorf("deleted: %+v", w.deletedIDs)
	}
}

func TestEpisodeFileDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/999", nil)
	req.SetPathValue("id", "999")
	EpisodeFileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/abc", nil)
	req.SetPathValue("id", "abc")
	EpisodeFileDeleteHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileDeleteHandler_ServiceError_500(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{
		get:       map[int]*episodefiles.File{1: {ID: 1}},
		deleteErr: errors.New("boom"),
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/1", nil)
	req.SetPathValue("id", "1")
	EpisodeFileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SeriesID: 1, RelativePath: "a.mkv"},
		{ID: 2, SeriesID: 1, RelativePath: "b.mkv"},
		{ID: 3, SeriesID: 1, RelativePath: "c.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`{"episodeFileIds":[1,2,3]}`))
	EpisodeFileBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.deletedIDs) != 3 {
		t.Errorf("deleted count: %d; ids=%+v", len(w.deletedIDs), w.deletedIDs)
	}
}

func TestEpisodeFileBulkDeleteHandler_EmptyList(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`{"episodeFileIds":[]}`))
	EpisodeFileBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.deletedIDs) != 0 {
		t.Errorf("should not have deleted anything: %+v", w.deletedIDs)
	}
}

func TestEpisodeFileBulkDeleteHandler_NoMatchingFiles_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: nil} // GetFiles returns empty slice
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`{"episodeFileIds":[999]}`))
	EpisodeFileBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkDeleteHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`not json`))
	EpisodeFileBulkDeleteHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.I2: GET /episodefile tests ----------

type fakeEpisodeFileReader struct {
	bySeries    map[int][]episodefiles.File
	byIDs       []episodefiles.File
	byID        map[int]*episodefiles.File
	bySeriesErr error
}

func (f *fakeEpisodeFileReader) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if v, ok := f.byID[id]; ok {
		return v, nil
	}
	return nil, episodefiles.ErrNotFound
}
func (f *fakeEpisodeFileReader) GetFiles(_ context.Context, _ []int) ([]episodefiles.File, error) {
	return f.byIDs, nil
}
func (f *fakeEpisodeFileReader) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	if f.bySeriesErr != nil {
		return nil, f.bySeriesErr
	}
	return f.bySeries[seriesID], nil
}

// stubSeriesExistsChecker implements SeriesExistsChecker with a
// fixed set of "known" series ids. Used by the EpisodeFileListHandler
// tests below — the seriesId path requires this checker (5.J180) so
// the handler can short-circuit to 404 when the series is missing.
type stubSeriesExistsChecker struct {
	known map[int]bool
	err   error
}

func (s *stubSeriesExistsChecker) SeriesExists(_ context.Context, id int) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.known[id], nil
}

func TestEpisodeFileListHandler_MissingParams_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(&fakeEpisodeFileReader{}, &stubSeriesExistsChecker{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileListHandler_SeriesIDBranch(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{
		7: {
			{ID: 1, SeriesID: 7, RelativePath: "a.mkv"},
			{ID: 2, SeriesID: 7, RelativePath: "b.mkv"},
		},
	}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":1`) || !strings.Contains(rr.Body.String(), `"id":2`) {
		t.Errorf("body should contain both ids: %s", rr.Body.String())
	}
}

// J339 wire-shape pin for the EpisodeFilePopulator optional-
// extension integration. When the handler's reader/writer parameter
// implements PopulateEpisodeFile (via type-assertion), the response
// CustomFormats + CustomFormatScore are populated; otherwise the
// empty-stub fallback fires (preserves pre-J339 behavior).
//
// Test covers BOTH branches: a populator-implementing fake →
// populated typed entries; the existing fakeEpisodeFileReader
// (stub) → empty fallback per existing positive-pin tests.
func TestEpisodeFileListHandler_PopulatorOptionalExtension_J339(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReaderWithPopulator{
		bySeries: map[int][]episodefiles.File{7: {{ID: 1, SeriesID: 7, RelativePath: "a.mkv"}}},
		populator: func(_ context.Context, f *episodefiles.File) ([]CustomFormatResource, int) {
			return []CustomFormatResource{{ID: 11, Name: "DTS-X"}}, 17
		},
	}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"customFormats":[{"id":11,"name":"DTS-X"}]`) {
		t.Errorf("populator should produce typed CustomFormats; got %s", body)
	}
	if !strings.Contains(body, `"customFormatScore":17`) {
		t.Errorf("populator should set customFormatScore:17; got %s", body)
	}
}

// fakeEpisodeFileReaderWithPopulator satisfies EpisodeFileReader
// AND EpisodeFilePopulator — exercises the J339 optional-extension
// type-assertion branch.
type fakeEpisodeFileReaderWithPopulator struct {
	bySeries  map[int][]episodefiles.File
	populator func(context.Context, *episodefiles.File) ([]CustomFormatResource, int)
}

func (f *fakeEpisodeFileReaderWithPopulator) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	for _, files := range f.bySeries {
		for i := range files {
			if files[i].ID == id {
				return &files[i], nil
			}
		}
	}
	return nil, episodefiles.ErrNotFound
}
func (f *fakeEpisodeFileReaderWithPopulator) GetFiles(_ context.Context, ids []int) ([]episodefiles.File, error) {
	var out []episodefiles.File
	for _, id := range ids {
		if got, err := f.GetFile(context.Background(), id); err == nil {
			out = append(out, *got)
		}
	}
	return out, nil
}
func (f *fakeEpisodeFileReaderWithPopulator) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	return f.bySeries[seriesID], nil
}
func (f *fakeEpisodeFileReaderWithPopulator) PopulateEpisodeFile(ctx context.Context, file *episodefiles.File) ([]CustomFormatResource, int) {
	return f.populator(ctx, file)
}

// J328 wire-shape pin for EpisodeFileResource scalar widening
// (Languages + Quality + IndexerFlags + ReleaseType +
// QualityCutoffNotMet). Tests via the production EpisodeFileListHandler
// path (covers ToEpisodeFileResource adapter behavior).
//
// 2 rows: row 1 populated with Languages=[1,2], IndexerFlags=42,
// ReleaseType=3 (SeasonPack); row 2 defaults (no IndexerFlags, no
// ReleaseType set → both omitempty drop, Languages emits `[]`).
func TestEpisodeFileListHandler_EmitsScalarWidening_J328(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{
		7: {
			{ID: 1, SeriesID: 7, RelativePath: "a.mkv", Languages: []int{1, 2}, IndexerFlags: 42, ReleaseType: 3},
			{ID: 2, SeriesID: 7, RelativePath: "b.mkv"},
		},
	}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	// Row 1 positive substring assertions.
	mustContain := []string{
		`"languages":[{"id":1,"name":"English"},{"id":2,"name":"French"}]`,
		`"indexerFlags":42`,
		`"releaseType":"seasonPack"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("row 1 missing %q in %s", want, body)
		}
	}
	// Row 2: defaults — Languages emits empty `[]`, IndexerFlags omits, ReleaseType omits.
	if !strings.Contains(body, `"languages":[]`) {
		t.Errorf("row 2 default Languages must emit `\"languages\":[]`; got %s", body)
	}
	// indexerFlags + releaseType each appear exactly once (row 1 only).
	for _, key := range []string{`"indexerFlags":`, `"releaseType":`} {
		if c := strings.Count(body, key); c != 1 {
			t.Errorf("expected exactly 1 %q occurrence (row 1 only); got %d", key, c)
		}
	}
	// Both fixture rows have an empty Quality column, so the quality
	// key is absent (the J373 projection only emits when the persisted
	// string is non-empty — see TestEpisodeFile_QualityProjection_J373).
	if strings.Contains(body, `"quality":`) {
		t.Errorf("Quality must be absent for empty-Quality fixtures; got %s", body)
	}
	// J375: qualityCutoffNotMet is now ALWAYS emitted (C# non-nullable
	// bool). The fake lister implements no populator extension, so it
	// stays the false default — but the key must be present.
	if !strings.Contains(body, `"qualityCutoffNotMet":false`) {
		t.Errorf("QualityCutoffNotMet must always emit (false here); got %s", body)
	}
}

// TestEpisodeFile_QualityProjection_J373 pins the Quality field: a file
// whose persisted Quality column holds the reduced
// `{"quality":<id>,"revision":{...}}` JSON decodes + projects to the
// expanded REST wire shape; an empty Quality column omits the key.
func TestEpisodeFile_QualityProjection_J373(t *testing.T) {
	t.Parallel()
	// Populated: WEBDL-1080p v2 Real:1 persisted in the reduced shape.
	qm := quality.NewModelWithRevision(quality.WEBDL1080p, quality.Revision{Version: 2, Real: 1})
	raw, err := json.Marshal(qm)
	if err != nil {
		t.Fatalf("marshal persisted quality: %v", err)
	}
	res := ToEpisodeFileResource(episodefiles.File{ID: 1, SeriesID: 7, RelativePath: "a.mkv", Quality: string(raw)}, "")
	if res.Quality == nil {
		t.Fatalf("quality must be populated for a non-empty Quality column")
	}
	if res.Quality.Quality.ID != quality.WEBDL1080p.ID || res.Quality.Quality.Name != quality.WEBDL1080p.Name {
		t.Errorf("quality value: got id=%d name=%q want id=%d name=%q",
			res.Quality.Quality.ID, res.Quality.Quality.Name, quality.WEBDL1080p.ID, quality.WEBDL1080p.Name)
	}
	if res.Quality.Revision.Version != 2 || res.Quality.Revision.Real != 1 {
		t.Errorf("revision: got %+v want version=2 real=1", res.Quality.Revision)
	}
	// Expanded wire shape: nested quality object + revision (NOT the
	// reduced bare-int persisted form).
	buf, _ := json.Marshal(res)
	body := string(buf)
	for _, want := range []string{`"quality":{"quality":{"id":`, `"name":`, `"source":`, `"resolution":`, `"revision":{"version":2,"real":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("expanded quality wire shape missing %q in %s", want, body)
		}
	}

	// Empty Quality column → key absent (omitempty drops nil pointer).
	res2 := ToEpisodeFileResource(episodefiles.File{ID: 2, SeriesID: 7, RelativePath: "b.mkv"}, "")
	if res2.Quality != nil {
		t.Errorf("empty Quality column must leave the pointer nil; got %+v", res2.Quality)
	}
}

// TestEpisodeFile_MediaInfoProjection_J374 pins the mediaInfo projection:
// the UNION shape (v3-flat audio/subtitle summary + shared video/runtime
// + v5-typed stream lists) from the typed File.MediaInfo model; a nil
// model omits the key.
func TestEpisodeFile_MediaInfoProjection_J374(t *testing.T) {
	t.Parallel()
	model := &mediainfo.Model{
		VideoBitDepth: 8,
		VideoBitrate:  5_000_000,
		VideoFps:      23.976023, // rounds to 23.976
		Width:         1920,
		Height:        1080,
		ScanType:      "Progressive",
		RunTime:       datastore.STDuration(22 * time.Minute),
		AudioStreams: []mediainfo.AudioStream{
			{Language: "eng", Format: "ac3", Bitrate: 384_000, Channels: 6, CodecID: "a3"},
			{Language: "fre", Format: "aac", Bitrate: 128_000, Channels: 2},
		},
		SubtitleStreams: []mediainfo.SubtitleStream{
			{Language: "eng", Format: "SRT"},
		},
	}
	res := ToMediaInfoResource(model, "Some.Series.S01E01-Sonarr")
	if res == nil {
		t.Fatalf("mediaInfo must be populated for a non-nil model")
	}
	// v3-flat audio summary (primary stream = first audio stream).
	if res.AudioBitrate != 384_000 {
		t.Errorf("audioBitrate: got %d want 384000", res.AudioBitrate)
	}
	if res.AudioChannels != 6 {
		t.Errorf("audioChannels: got %v want 6", res.AudioChannels)
	}
	if res.AudioCodec != "AC3" {
		t.Errorf("audioCodec: got %q want AC3", res.AudioCodec)
	}
	if res.AudioLanguages != "eng/fre" {
		t.Errorf("audioLanguages: got %q want eng/fre", res.AudioLanguages)
	}
	if res.AudioStreamCount != 2 {
		t.Errorf("audioStreamCount: got %d want 2", res.AudioStreamCount)
	}
	// shared video / runtime.
	if res.VideoFps != 23.976 {
		t.Errorf("videoFps: got %v want 23.976", res.VideoFps)
	}
	if res.Resolution != "1920x1080" {
		t.Errorf("resolution: got %q want 1920x1080", res.Resolution)
	}
	if res.RunTime != "22:00" {
		t.Errorf("runTime: got %q want 22:00", res.RunTime)
	}
	if res.ScanType != "Progressive" {
		t.Errorf("scanType: got %q want Progressive", res.ScanType)
	}
	// v3-flat subtitle summary.
	if res.Subtitles != "eng" {
		t.Errorf("subtitles: got %q want eng", res.Subtitles)
	}
	// v5-typed stream lists.
	if res.AudioStreams == nil || len(*res.AudioStreams) != 2 {
		t.Fatalf("audioStreams must carry 2 entries; got %+v", res.AudioStreams)
	}
	if a0 := (*res.AudioStreams)[0]; a0.Language != "eng" || a0.Codec != "AC3" || a0.Bitrate != 384_000 || a0.Channels != 6 {
		t.Errorf("audioStreams[0]: %+v", a0)
	}
	if res.SubtitleStreams == nil || len(*res.SubtitleStreams) != 1 || (*res.SubtitleStreams)[0].Language != "eng" {
		t.Errorf("subtitleStreams: %+v", res.SubtitleStreams)
	}

	// Runtime with an hours component → H:MM:SS.
	hrs := ToMediaInfoResource(&mediainfo.Model{RunTime: datastore.STDuration(time.Hour + 5*time.Minute + 9*time.Second)}, "")
	if hrs.RunTime != "1:05:09" {
		t.Errorf("runTime hours form: got %q want 1:05:09", hrs.RunTime)
	}

	// nil model → nil (via ToEpisodeFileResource the key is dropped).
	if ToMediaInfoResource(nil, "") != nil {
		t.Errorf("nil model must project to nil")
	}
	res2 := ToEpisodeFileResource(episodefiles.File{ID: 2, SeriesID: 7, RelativePath: "b.mkv"}, "")
	if res2.MediaInfo != nil {
		t.Errorf("nil File.MediaInfo must leave the resource pointer nil; got %+v", res2.MediaInfo)
	}
}

// fakeQualityCutoffPopulator implements EpisodeFileQualityCutoffPopulator
// but NOT EpisodeFilePopulator — exercises the J375 independent
// type-assertion in ApplyEpisodeFilePopulator.
type fakeQualityCutoffPopulator struct{ ret bool }

func (f fakeQualityCutoffPopulator) PopulateQualityCutoffNotMet(_ context.Context, _ *episodefiles.File) bool {
	return f.ret
}

// TestApplyEpisodeFilePopulator_QualityCutoff_J375 pins the
// qualityCutoffNotMet optional-extension wiring: an implementing svc
// sets the flag; a non-implementing svc leaves it false; the field is
// always emitted (no omitempty).
func TestApplyEpisodeFilePopulator_QualityCutoff_J375(t *testing.T) {
	t.Parallel()
	f := &episodefiles.File{ID: 1, SeriesID: 7}

	// Implementing svc → flag set true (independent of EpisodeFilePopulator).
	res := &EpisodeFileResource{ID: 1}
	ApplyEpisodeFilePopulator(context.Background(), res, f, fakeQualityCutoffPopulator{ret: true})
	if !res.QualityCutoffNotMet {
		t.Errorf("expected QualityCutoffNotMet=true from the populator")
	}

	// Non-implementing svc → stays false.
	res2 := &EpisodeFileResource{ID: 2}
	ApplyEpisodeFilePopulator(context.Background(), res2, f, struct{}{})
	if res2.QualityCutoffNotMet {
		t.Errorf("non-implementing svc must leave QualityCutoffNotMet false")
	}

	// Always emitted (no omitempty): false still produces the key.
	buf, _ := json.Marshal(res2)
	if !strings.Contains(string(buf), `"qualityCutoffNotMet":false`) {
		t.Errorf("qualityCutoffNotMet must always emit; got %s", buf)
	}
}

// J298 wire-shape pin: a populated row must emit `customFormats:[]`
// (non-nil empty) + `customFormatScore:0` — matches the C# v3
// EpisodeFileResource mapper at `EpisodeFileResource.cs:46-66`
// which initialises CustomFormats via
// `formatCalculator?.ParseCustomFormat(model, model.Series).ToResource(false)`
// and CustomFormatScore via the QualityProfile lookup. Both fields
// land on the wire today as empty/zero pending the populated-row
// calculator wire-through; their presence on the struct is what
// the future mapper widens into. Same widening simultaneously
// affects Wanted, Calendar, and Episode families because their
// EpisodeFile subresource shares this type.
func TestEpisodeFileListHandler_EmitsCustomFormatsAndScore(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{
		7: {
			{ID: 1, SeriesID: 7, RelativePath: "a.mkv"},
		},
	}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"customFormats":[]`) {
		t.Errorf("body must contain `\"customFormats\":[]`; got %s", body)
	}
	if !strings.Contains(body, `"customFormatScore":0`) {
		t.Errorf("body must contain `\"customFormatScore\":0`; got %s", body)
	}
	var got []EpisodeFileResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].CustomFormats == nil {
		t.Errorf("CustomFormats must be non-nil (empty slice, not nil)")
	}
	if len(got[0].CustomFormats) != 0 {
		t.Errorf("CustomFormats must be empty at deferred state; got %d", len(got[0].CustomFormats))
	}
	if got[0].CustomFormatScore != 0 {
		t.Errorf("CustomFormatScore must be 0 at deferred state; got %d", got[0].CustomFormatScore)
	}
}

// J180: when the supplied seriesId doesn't exist, the handler must
// short-circuit to 404 with a descriptive body matching C#
// EpisodeFileController.cs:72's ModelNotFoundException → 404
// pipeline. Mirrors the v3.episodefile.list contract golden's 404
// status — closing the 404-vs-200 drift.
func TestEpisodeFileListHandler_MissingSeriesReturns404(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{}
	checker := &stubSeriesExistsChecker{known: map[int]bool{} /* nothing known */}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=1", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Series with ID 1 does not exist") {
		t.Errorf("body should explain the missing series: %s", rr.Body.String())
	}
}

// J180: existing series → 200 + []. The series-exists-but-no-files
// case stays a 200, matching C# `:75-78` `if (files == null) return
// new List<EpisodeFileResource>()` fallback. This guards against an
// over-eager 404 path that would lump this case with the missing-
// series 404.
func TestEpisodeFileListHandler_ExistingSeriesEmptyFiles_200(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("body: got %q, want []", rr.Body.String())
	}
}

func TestEpisodeFileListHandler_EpisodeFileIDsBranch(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{byIDs: []episodefiles.File{
		{ID: 5, SeriesID: 7, RelativePath: "x.mkv"},
	}}
	rr := httptest.NewRecorder()
	// episodeFileIds path doesn't precondition on series existence
	// (matches C# :85-90 per-row join after-the-fact) — checker can
	// be empty.
	EpisodeFileListHandler(svc, &stubSeriesExistsChecker{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?episodeFileIds=5", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestEpisodeFileListHandler_InvalidEpisodeFileID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(&fakeEpisodeFileReader{}, &stubSeriesExistsChecker{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?episodeFileIds=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileByIDHandler_Found(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{byID: map[int]*episodefiles.File{
		3: {ID: 3, SeriesID: 1, RelativePath: "y.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episodefile/3", nil)
	req.SetPathValue("id", "3")
	EpisodeFileByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":3`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestEpisodeFileByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episodefile/999", nil)
	req.SetPathValue("id", "999")
	EpisodeFileByIDHandler(&fakeEpisodeFileReader{byID: map[int]*episodefiles.File{}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.D5b: PUT /episodefile/{id} tests ----------

func TestEpisodeFileUpdateHandler_UpdatesSceneNameAndReleaseGroup(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SeriesID: 1, SceneName: "old.scene", ReleaseGroup: "OLD"},
	}}
	rr := httptest.NewRecorder()
	body := `{"id":7,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","sceneName":"New.Scene-GROUP","releaseGroup":"NEW"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/7", strings.NewReader(body))
	req.SetPathValue("id", "7")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updated == nil || w.updated.SceneName != "New.Scene-GROUP" || w.updated.ReleaseGroup != "NEW" {
		t.Errorf("updated: %+v", w.updated)
	}
}

func TestEpisodeFileUpdateHandler_EmptyStringFieldsPreserveExisting(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SceneName: "keep.scene", ReleaseGroup: "KEEP"},
	}}
	rr := httptest.NewRecorder()
	body := `{"id":7,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/7", strings.NewReader(body))
	req.SetPathValue("id", "7")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.updated == nil || w.updated.SceneName != "keep.scene" || w.updated.ReleaseGroup != "KEEP" {
		t.Errorf("existing values should be preserved: %+v", w.updated)
	}
}

func TestEpisodeFileUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{}}
	rr := httptest.NewRecorder()
	body := `{"id":999,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/999", strings.NewReader(body))
	req.SetPathValue("id", "999")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileUpdateHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/abc", strings.NewReader(`{}`))
	req.SetPathValue("id", "abc")
	EpisodeFileUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileUpdateHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/1", strings.NewReader(`not json`))
	req.SetPathValue("id", "1")
	EpisodeFileUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileUpdateHandler_UpdateError_500(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{
		get:       map[int]*episodefiles.File{1: {ID: 1}},
		updateErr: errors.New("boom"),
	}
	rr := httptest.NewRecorder()
	body := `{"id":1,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","sceneName":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.D5c: PUT /episodefile/editor + /bulk tests ----------

func TestEpisodeFileEditorHandler_AppliesSameOverridesToAll(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SceneName: "a", ReleaseGroup: "OLD"},
		{ID: 2, SceneName: "b", ReleaseGroup: "OLD"},
		{ID: 3, SceneName: "c", ReleaseGroup: "OLD"},
	}}
	rr := httptest.NewRecorder()
	body := `{"episodeFileIds":[1,2,3],"releaseGroup":"NEW"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(body))
	EpisodeFileEditorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updatedMany) != 3 {
		t.Fatalf("expected 3 updated; got %d", len(w.updatedMany))
	}
	for _, f := range w.updatedMany {
		if f.ReleaseGroup != "NEW" {
			t.Errorf("ReleaseGroup not propagated: %+v", f)
		}
		// SceneName not set in body -> preserved.
		if f.SceneName == "" {
			t.Errorf("SceneName should be preserved: %+v", f)
		}
	}
}

func TestEpisodeFileEditorHandler_NoMatchingFiles_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(`{"episodeFileIds":[999]}`))
	EpisodeFileEditorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileEditorHandler_MissingIDs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(`{"episodeFileIds":[]}`))
	EpisodeFileEditorHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkUpdateHandler_PerIDOverrides(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SceneName: "old.1", ReleaseGroup: "OLD"},
		{ID: 2, SceneName: "old.2", ReleaseGroup: "OLD"},
	}}
	rr := httptest.NewRecorder()
	body := `[{"id":1,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","sceneName":"new.1"},{"id":2,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","releaseGroup":"NEW2"}]`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(body))
	EpisodeFileBulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updatedMany) != 2 {
		t.Fatalf("expected 2 updated; got %d", len(w.updatedMany))
	}
	// Per-id overrides: id=1 gets sceneName='new.1' + preserves OLD release group;
	// id=2 gets releaseGroup='NEW2' + preserves old.2 scene name.
	for _, f := range w.updatedMany {
		switch f.ID {
		case 1:
			if f.SceneName != "new.1" || f.ReleaseGroup != "OLD" {
				t.Errorf("id=1: %+v", f)
			}
		case 2:
			if f.SceneName != "old.2" || f.ReleaseGroup != "NEW2" {
				t.Errorf("id=2: %+v", f)
			}
		}
	}
}

func TestEpisodeFileBulkUpdateHandler_EmptyArray_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(`[]`))
	EpisodeFileBulkUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkUpdateHandler_MissingID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `[{"id":0,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z"}]`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(body))
	EpisodeFileBulkUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// J389: single PUT /{id} persists Quality (decoded to the bare-int model
// column) but leaves Languages untouched (C# single SetQuality scope).
func TestEpisodeFileUpdateHandler_PersistsQuality_J389(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SeriesID: 1, Quality: `{"quality":1,"revision":{"version":1}}`, Languages: []int{1}},
	}}
	q, _ := quality.FindByID(7)
	body, _ := json.Marshal(EpisodeFileResource{ID: 7, SeriesID: 1, Quality: ToQualityModelResource(quality.NewModel(q))})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/7", strings.NewReader(string(body)))
	req.SetPathValue("id", "7")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	var got quality.Model
	if err := json.Unmarshal([]byte(w.updated.Quality), &got); err != nil {
		t.Fatalf("persisted quality not decodable: %q: %v", w.updated.Quality, err)
	}
	if got.Quality.ID != 7 {
		t.Errorf("persisted quality id = %d, want 7", got.Quality.ID)
	}
	if len(w.updated.Languages) != 1 || w.updated.Languages[0] != 1 {
		t.Errorf("single update must not change Languages; got %v", w.updated.Languages)
	}
}

// J389: editor applies Quality + Languages to ALL files; the Original
// sentinel (id -2) is dropped from the persisted language list.
func TestEpisodeFileEditorHandler_PersistsQualityLanguages_J389(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SeriesID: 1}, {ID: 2, SeriesID: 1},
	}}
	q, _ := quality.FindByID(7)
	body, _ := json.Marshal(episodeFileEditorBody{
		EpisodeFileIDs: []int{1, 2},
		Quality:        ToQualityModelResource(quality.NewModel(q)),
		Languages:      []LanguageRef{ToLanguageRef(1), ToLanguageRef(language.Original.ID), ToLanguageRef(2)},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(string(body)))
	EpisodeFileEditorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updatedMany) != 2 {
		t.Fatalf("updatedMany = %d, want 2", len(w.updatedMany))
	}
	for _, f := range w.updatedMany {
		var got quality.Model
		if err := json.Unmarshal([]byte(f.Quality), &got); err != nil || got.Quality.ID != 7 {
			t.Errorf("file %d quality = %q (id err %v)", f.ID, f.Quality, err)
		}
		if len(f.Languages) != 2 || f.Languages[0] != 1 || f.Languages[1] != 2 {
			t.Errorf("file %d languages = %v, want [1 2] (Original dropped)", f.ID, f.Languages)
		}
	}
}

// J389: bulk applies per-entry Quality + Languages matched by id.
func TestEpisodeFileBulkUpdateHandler_PersistsQualityLanguages_J389(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{{ID: 5, SeriesID: 1}}}
	q, _ := quality.FindByID(3)
	body, _ := json.Marshal([]EpisodeFileResource{{
		ID: 5, SeriesID: 1,
		Quality:   ToQualityModelResource(quality.NewModel(q)),
		Languages: []LanguageRef{ToLanguageRef(2)},
	}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(string(body)))
	EpisodeFileBulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updatedMany) != 1 {
		t.Fatalf("updatedMany = %d, want 1", len(w.updatedMany))
	}
	f := w.updatedMany[0]
	var got quality.Model
	if err := json.Unmarshal([]byte(f.Quality), &got); err != nil || got.Quality.ID != 3 {
		t.Errorf("quality = %q (id err %v), want id 3", f.Quality, err)
	}
	if len(f.Languages) != 1 || f.Languages[0] != 2 {
		t.Errorf("languages = %v, want [2]", f.Languages)
	}
}
