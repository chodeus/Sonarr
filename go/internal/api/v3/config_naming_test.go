package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/namingconfig"
)

type fakeNamingGetter struct {
	cfg *namingconfig.Config
	err error
}

func (f *fakeNamingGetter) GetConfig(context.Context) (*namingconfig.Config, error) {
	return f.cfg, f.err
}

func TestNamingConfigHandler_Defaults(t *testing.T) {
	t.Parallel()
	cfg := namingconfig.Default()
	cfg.ID = 1
	rr := httptest.NewRecorder()
	NamingConfigHandler(&fakeNamingGetter{cfg: cfg}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/naming", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got NamingConfigResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := NamingConfigResource{
		ID:                           1,
		MultiEpisodeStyle:            5,
		RenameEpisodes:               false,
		StandardEpisodeFormat:        `{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}`,
		DailyEpisodeFormat:           `{Series Title} - {Air-Date} - {Episode Title} {Quality Full}`,
		AnimeEpisodeFormat:           `{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}`,
		SeasonFolderFormat:           `Season {season}`,
		SeriesFolderFormat:           `{Series Title}`,
		SpecialsFolderFormat:         `Specials`,
		ReplaceIllegalCharacters:     true,
		ColonReplacementFormat:       4,
		CustomColonReplacementFormat: "",
	}
	if got != want {
		t.Errorf("body:\n got %+v\nwant %+v", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
}

func TestNamingConfigHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NamingConfigHandler(&fakeNamingGetter{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/naming", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B12: PUT + /examples tests ----------

type fakeNamingWriter struct {
	fakeNamingGetter
	saveErr        error
	saveCalledWith *namingconfig.Config
}

func (f *fakeNamingWriter) Save(_ context.Context, cfg *namingconfig.Config) error {
	f.saveCalledWith = cfg
	return f.saveErr
}

// ----- PUT /config/naming/{id} -----

func TestNamingConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeNamingWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":1,"multiEpisodeStyle":2,"renameEpisodes":true,"standardEpisodeFormat":"{Series Title} - S{season:00}E{episode:00}","dailyEpisodeFormat":"","seasonFolderFormat":"Season {season}","seriesFolderFormat":"{Series Title}","animeEpisodeFormat":"","replaceIllegalCharacters":true,"specialsFolderFormat":"Specials","colonReplacementFormat":0,"customColonReplacementFormat":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/naming/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	NamingConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.saveCalledWith == nil || w.saveCalledWith.MultiEpisodeStyle != 2 {
		t.Errorf("Save called with %+v", w.saveCalledWith)
	}
}

func TestNamingConfigUpdateHandler_MultiEpisodeStyleOutOfRange400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":1,"multiEpisodeStyle":99,"renameEpisodes":false,"standardEpisodeFormat":"","dailyEpisodeFormat":"","seasonFolderFormat":"","seriesFolderFormat":"","animeEpisodeFormat":"","replaceIllegalCharacters":false,"specialsFolderFormat":"","colonReplacementFormat":0,"customColonReplacementFormat":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/naming/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	NamingConfigUpdateHandler(&fakeNamingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"MultiEpisodeStyle"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestNamingConfigUpdateHandler_CustomColonWithoutValue400(t *testing.T) {
	t.Parallel()
	// ColonReplacementFormat = 4 (Custom) but CustomColonReplacementFormat is empty.
	rr := httptest.NewRecorder()
	body := `{"id":1,"multiEpisodeStyle":0,"renameEpisodes":false,"standardEpisodeFormat":"","dailyEpisodeFormat":"","seasonFolderFormat":"","seriesFolderFormat":"","animeEpisodeFormat":"","replaceIllegalCharacters":false,"specialsFolderFormat":"","colonReplacementFormat":4,"customColonReplacementFormat":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/naming/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	NamingConfigUpdateHandler(&fakeNamingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Custom Colon Replacement Format must be provided") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestNamingConfigUpdateHandler_CustomColonWithValueSucceeds(t *testing.T) {
	t.Parallel()
	w := &fakeNamingWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":1,"multiEpisodeStyle":0,"renameEpisodes":false,"standardEpisodeFormat":"","dailyEpisodeFormat":"","seasonFolderFormat":"","seriesFolderFormat":"","animeEpisodeFormat":"","replaceIllegalCharacters":false,"specialsFolderFormat":"","colonReplacementFormat":4,"customColonReplacementFormat":" - "}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/naming/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	NamingConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestNamingConfigUpdateHandler_SaveErrorIs500(t *testing.T) {
	t.Parallel()
	w := &fakeNamingWriter{saveErr: errors.New("db down")}
	rr := httptest.NewRecorder()
	body := `{"id":1,"multiEpisodeStyle":0,"renameEpisodes":false,"standardEpisodeFormat":"","dailyEpisodeFormat":"","seasonFolderFormat":"","seriesFolderFormat":"","animeEpisodeFormat":"","replaceIllegalCharacters":false,"specialsFolderFormat":"","colonReplacementFormat":0,"customColonReplacementFormat":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/naming/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	NamingConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- GET /config/naming/examples -----

func TestNamingConfigExamplesHandler_ReturnsEmptyObject(t *testing.T) {
	t.Parallel()
	// DEFERRED state: handler returns NamingExampleResource{} with
	// all fields omitted → body is "{}". Pins the wire shape while
	// the FilenameSampleService port is pending.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/config/naming/examples", nil)
	NamingConfigExamplesHandler(&fakeNamingGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "{}" {
		t.Errorf("body: got %q want %q (DEFERRED placeholder)", rr.Body.String(), "{}")
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
}
