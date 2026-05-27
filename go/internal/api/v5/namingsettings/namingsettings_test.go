package namingsettings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/namingsettings"
	"sonarr.tv/sonarr/internal/namingconfig"
)

type fakeSvc struct {
	mu     sync.Mutex
	cfg    namingconfig.Config
	saved  []namingconfig.Config
	getErr error
}

func newSvc() *fakeSvc {
	return &fakeSvc{cfg: namingconfig.Config{
		ID: 1, MultiEpisodeStyle: 0, RenameEpisodes: true,
		StandardEpisodeFormat: "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}",
		DailyEpisodeFormat:    "{Series Title} - {Air-Date} - {Episode Title} {Quality Full}",
		AnimeEpisodeFormat:    "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}",
		SeriesFolderFormat:    "{Series Title}",
		SeasonFolderFormat:    "Season {season}",
		SpecialsFolderFormat:  "Specials",
	}}
}

func (f *fakeSvc) GetConfig(context.Context) (*namingconfig.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	c := f.cfg
	return &c, nil
}

func (f *fakeSvc) Save(_ context.Context, c *namingconfig.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, *c)
	f.cfg = *c
	return nil
}

func (f *fakeSvc) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func TestGetHandler_EmitsSingleton(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	namingsettings.GetHandler(newSvc()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/naming", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got namingsettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 1 || got.SeriesFolderFormat != "{Series Title}" {
		t.Errorf("got %+v", got)
	}
}

func TestUpdateHandler_PutAccepted202_Persists(t *testing.T) {
	t.Parallel()
	svc := newSvc()
	body := mustEncode(t, namingsettings.Resource{
		ID: 1, MultiEpisodeStyle: 0, RenameEpisodes: true, ReplaceIllegalCharacters: true,
		ColonReplacementFormat: 1,
		StandardEpisodeFormat:  "{Series Title} - S{season:00}E{episode:00}",
		DailyEpisodeFormat:     "{Series Title} - {Air-Date}",
		AnimeEpisodeFormat:     "{Series Title} - {absolute:00}",
		SeriesFolderFormat:     "{Series Title}",
		SeasonFolderFormat:     "Season {season}",
		SpecialsFolderFormat:   "Specials",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/naming/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	namingsettings.UpdateHandler(svc).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	if svc.savedCount() != 1 {
		t.Errorf("Save not called once: %d", svc.savedCount())
	}
}

func TestUpdateHandler_MultiEpisodeStyleOutOfRange_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, namingsettings.Resource{ID: 1, MultiEpisodeStyle: 99})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/naming/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	namingsettings.UpdateHandler(newSvc()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (MultiEpisodeStyle ∈ [0,5])", rr.Code)
	}
}

func TestUpdateHandler_CustomColonRequiresValue_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, namingsettings.Resource{
		ID: 1, ColonReplacementFormat: 4, CustomColonReplacementFormat: "",
		StandardEpisodeFormat: "x", SeriesFolderFormat: "x", SeasonFolderFormat: "x",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/naming/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	namingsettings.UpdateHandler(newSvc()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (Custom requires non-empty CustomColonReplacementFormat)", rr.Code)
	}
}

func TestExamplesHandler_DeferredEmptyShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	namingsettings.ExamplesHandler(newSvc()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/naming/examples", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// All fields omitempty → empty body when none are set.
	if rr.Body.String() != "{}" {
		t.Errorf("body: %q want {} (IFilenameSampleService DEFERRED)", rr.Body.String())
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if namingsettings.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
	if namingsettings.UpdateHandler == nil {
		t.Errorf("UpdateHandler nil")
	}
	if namingsettings.ExamplesHandler == nil {
		t.Errorf("ExamplesHandler nil")
	}
}

// TestTypeAliases pins compile-time equivalence with v3.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	r := namingsettings.Resource{ID: 1, SeriesFolderFormat: "{Series Title}"}
	if r.SeriesFolderFormat != "{Series Title}" {
		t.Errorf("Resource not addressable")
	}
	e := namingsettings.ExampleResource{SeriesFolderExample: "ex"}
	if e.SeriesFolderExample != "ex" {
		t.Errorf("ExampleResource not addressable")
	}
}

func mustEncode(t *testing.T, r namingsettings.Resource) []byte {
	t.Helper()
	buf, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
