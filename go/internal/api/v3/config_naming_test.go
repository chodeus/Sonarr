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
