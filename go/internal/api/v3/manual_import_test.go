package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

func TestManualImportHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ManualImportHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/manualimport", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestManualImportHandler_QueryParamsAccepted(t *testing.T) {
	t.Parallel()
	cases := []string{
		"?folder=/tmp/downloads",
		"?downloadId=abc123",
		"?seriesId=1&seasonNumber=2",
		"?seriesId=1&filterExistingFiles=true",
	}
	for _, qs := range cases {
		rr := httptest.NewRecorder()
		ManualImportHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/manualimport"+qs, nil))
		if rr.Code != http.StatusOK || rr.Body.String() != "[]" {
			t.Errorf("query %q: status=%d body=%q", qs, rr.Code, rr.Body.String())
		}
	}
}

func TestManualImportHandler_InvalidInt_400(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"seriesId", "seasonNumber"} {
		rr := httptest.NewRecorder()
		ManualImportHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/manualimport?"+key+"=abc", nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s=abc: status %d", key, rr.Code)
		}
	}
}

// ----- POST /api/v3/manualimport — reprocess endpoint (J238) -----

type fakeManualImportSeriesLookup struct {
	byID map[int]*series.Series
}

func (f *fakeManualImportSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if row, ok := f.byID[id]; ok {
		return row, nil
	}
	return nil, series.ErrNotFound
}

type fakeManualImportEpisodeLookup struct {
	byID map[int]*episodes.Episode
}

func (f *fakeManualImportEpisodeLookup) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	if row, ok := f.byID[id]; ok {
		return row, nil
	}
	return nil, episodes.ErrNotFound
}

func TestManualImportReprocessHandler_EmptyBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(`[]`))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "items must be provided") {
		t.Errorf("body should say 'items must be provided': %s", rr.Body.String())
	}
}

func TestManualImportReprocessHandler_MalformedBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(`not-json`))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestManualImportReprocessHandler_ParsesPathAndFillsReleaseGroup(t *testing.T) {
	t.Parallel()
	// Test: blank releaseGroup gets filled from the parsed path.
	body := `[{"path":"/tv/Show.S01E01.720p.WEB-DL.x264-RELEASEGRP.mkv","releaseGroup":"","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out []ManualImportReprocessItem
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("response length: got %d want 1", len(out))
	}
	if out[0].ReleaseGroup == "" {
		t.Errorf("ReleaseGroup should be filled from parsed path; got empty (parsed.ReleaseGroup may be empty for this title)")
	}
}

func TestManualImportReprocessHandler_PreservesNonBlankReleaseGroup(t *testing.T) {
	t.Parallel()
	// Test: non-blank releaseGroup is NOT overwritten.
	body := `[{"path":"/tv/Show.S01E01.720p.WEB-DL.x264-RELEASEGRP.mkv","releaseGroup":"USER-CHOSEN","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out[0].ReleaseGroup != "USER-CHOSEN" {
		t.Errorf("ReleaseGroup: got %q want USER-CHOSEN (must preserve non-blank user input)", out[0].ReleaseGroup)
	}
}

func TestManualImportReprocessHandler_SeasonNumberFromParser(t *testing.T) {
	t.Parallel()
	body := `[{"path":"/tv/Show.S01E01.720p.mkv","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out[0].SeasonNumber == nil || *out[0].SeasonNumber != 1 {
		t.Errorf("SeasonNumber: got %v want 1 (parsed from S01E01)", out[0].SeasonNumber)
	}
}

func TestManualImportReprocessHandler_ReleaseTypeForSingleEpisode(t *testing.T) {
	t.Parallel()
	body := `[{"path":"/tv/Show.S01E05.720p.mkv","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out[0].ReleaseType != "singleEpisode" {
		t.Errorf("ReleaseType: got %q want singleEpisode", out[0].ReleaseType)
	}
}

func TestManualImportReprocessHandler_ReleaseTypeForMultiEpisode(t *testing.T) {
	t.Parallel()
	body := `[{"path":"/tv/Show.S01E01-E03.720p.mkv","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out[0].ReleaseType != "multiEpisode" {
		t.Errorf("ReleaseType: got %q want multiEpisode (E01-E03 spans two episodes)", out[0].ReleaseType)
	}
}

func TestManualImportReprocessHandler_QualityFilledWhenUnknown(t *testing.T) {
	t.Parallel()
	// Quality with .Quality=Unknown gets overwritten from parsed.
	body := `[{
		"path":"/tv/Show.S01E01.1080p.WEB-DL.mkv",
		"quality":{"quality":0,"revision":{"version":1,"real":0,"isRepack":false}},
		"languages":[]
	}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out[0].Quality == nil {
		t.Fatalf("Quality should be populated from parsed")
	}
	if out[0].Quality.Quality == quality.Unknown {
		t.Errorf("Quality should be filled with parsed value (1080p WEB-DL ≠ Unknown), got Unknown")
	}
}

func TestManualImportReprocessHandler_QualityPreservedWhenSet(t *testing.T) {
	t.Parallel()
	// Non-Unknown user-supplied Quality is NOT overwritten.
	// Quality.Id=5 = HDTV-720p in the Sonarr quality registry.
	body := `[{
		"path":"/tv/Show.S01E01.1080p.WEB-DL.mkv",
		"quality":{"quality":5,"revision":{"version":1,"real":0,"isRepack":false}},
		"languages":[]
	}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out[0].Quality == nil || out[0].Quality.Quality.ID != 5 {
		t.Errorf("Quality.Id: got %+v want id=5 (user-supplied non-Unknown must be preserved)", out[0].Quality)
	}
}

func TestManualImportReprocessHandler_EpisodeIDsResolveToFullEpisodes(t *testing.T) {
	t.Parallel()
	// When EpisodeIds are provided, the reprocess resolves them and
	// returns them in item.Episodes; EpisodeIds field is cleared.
	epLookup := &fakeManualImportEpisodeLookup{byID: map[int]*episodes.Episode{
		42: {ID: 42, SeasonNumber: 1, EpisodeNumber: 5, Title: "Pilot"},
		43: {ID: 43, SeasonNumber: 1, EpisodeNumber: 6, Title: "Second"},
	}}
	body := `[{"path":"/tv/Show.S01E05.720p.mkv","episodeIds":[42,43],"languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, epLookup).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out[0].EpisodeIDs) != 0 {
		t.Errorf("EpisodeIds should be cleared (C# `:76`); got %v", out[0].EpisodeIDs)
	}
	if len(out[0].Episodes) != 2 {
		t.Errorf("Episodes: got %d want 2 (resolved from EpisodeIds)", len(out[0].Episodes))
	}
	if out[0].Episodes[0].ID != 42 || out[0].Episodes[1].ID != 43 {
		t.Errorf("Episodes ids: got %+v", out[0].Episodes)
	}
}

func TestManualImportReprocessHandler_RejectionsAlwaysArray(t *testing.T) {
	t.Parallel()
	// Even when no rejections are computed (deferred), the field must
	// emit `[]` not `null` so the React UI's table renderer doesn't NPE.
	body := `[{"path":"/tv/Show.S01E01.720p.mkv","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"rejections":[]`) {
		t.Errorf("body should emit rejections:[] not null: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"customFormats":[]`) {
		t.Errorf("body should emit customFormats:[] not null: %s", rr.Body.String())
	}
}

// J300 wire-shape pin: ManualImportReprocessItem.CustomFormats is
// typed `[]CustomFormatResource` (NOT `[]any`) so the wire shape
// matches the C# `List<CustomFormatResource>` projection at
// `ManualImportReprocessResource.cs`. The reprocess flow already
// defaults the field to `[]CustomFormatResource{}` on both the
// successful-parse and parse==nil branches (J300). When the
// calculator wire-through lands populated rows, the typed shape
// allows downstream code to consume the slice without reflection.
func TestManualImportReprocessHandler_CustomFormatsTyped(t *testing.T) {
	t.Parallel()
	body := `[{"path":"/tv/Show.S01E01.720p.mkv","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// Decode through the typed shape — Go's strict decode would fail
	// if the wire emitted something incompatible with []CustomFormatResource.
	var out []ManualImportReprocessItem
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(out) != 1 {
		t.Fatalf("len: %d", len(out))
	}
	if out[0].CustomFormats == nil {
		t.Errorf("CustomFormats must be non-nil (empty slice, not nil)")
	}
	if len(out[0].CustomFormats) != 0 {
		t.Errorf("CustomFormats must be empty at deferred state; got %d", len(out[0].CustomFormats))
	}
	if !strings.Contains(rr.Body.String(), `"customFormatScore":0`) {
		t.Errorf("body must contain `\"customFormatScore\":0`; got %s", rr.Body.String())
	}
}

// J300: parse-failure branch (path that won't ParsePath) must still
// default CustomFormats to `[]` so the wire is consistent across
// both parse-success and parse-failure paths.
func TestManualImportReprocessHandler_CustomFormatsDefaultsOnParseFailure(t *testing.T) {
	t.Parallel()
	// Use a path so opaque that ParsePath will fall through entirely.
	body := `[{"path":"random/gibberish/nothing/to/see.txt","languages":[]}]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"customFormats":[]`) {
		t.Errorf("body should emit customFormats:[] on parse-failure too; got %s", rr.Body.String())
	}
}

func TestManualImportReprocessHandler_BatchOfMultipleItems(t *testing.T) {
	t.Parallel()
	body := `[
		{"path":"/tv/A.S01E01.720p.mkv","languages":[]},
		{"path":"/tv/B.S02E04.1080p.mkv","languages":[]}
	]`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/manualimport", strings.NewReader(body))
	ManualImportReprocessHandler(&fakeManualImportSeriesLookup{}, &fakeManualImportEpisodeLookup{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var out []ManualImportReprocessItem
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out) != 2 {
		t.Fatalf("response length: got %d want 2", len(out))
	}
	if out[0].SeasonNumber == nil || *out[0].SeasonNumber != 1 {
		t.Errorf("item[0] SeasonNumber: %v", out[0].SeasonNumber)
	}
	if out[1].SeasonNumber == nil || *out[1].SeasonNumber != 2 {
		t.Errorf("item[1] SeasonNumber: %v", out[1].SeasonNumber)
	}
}
