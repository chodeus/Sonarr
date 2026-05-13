package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
)

type fakeHealthLister struct {
	results []healthcheck.HealthCheck
}

func (f *fakeHealthLister) Results() []healthcheck.HealthCheck { return f.results }

func TestHealthHandler_EmptyList(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HealthHandler(&fakeHealthLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body should be []: %q", rr.Body.String())
	}
}

func TestHealthHandler_SortsErrorBeforeWarning(t *testing.T) {
	t.Parallel()
	// Three entries deliberately handed back in non-canonical order
	// (Warning before Error, mid-letter Source second). The handler
	// must sort to (Type DESC, Source ASC) so Error sorts first,
	// then warnings alphabetically.
	rr := httptest.NewRecorder()
	HealthHandler(&fakeHealthLister{results: []healthcheck.HealthCheck{
		healthcheck.New("ZetaCheck", healthcheck.ResultWarning, healthcheck.ReasonAppDataLocation, "msg-z", "#z"),
		healthcheck.New("AlphaCheck", healthcheck.ResultError, healthcheck.ReasonAppDataLocation, "msg-a", "#a"),
		healthcheck.New("BetaCheck", healthcheck.ResultWarning, healthcheck.ReasonAppDataLocation, "msg-b", "#b"),
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/health", nil))

	var got []HealthResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("count: %d", len(got))
	}
	if got[0].Source != "AlphaCheck" || got[0].Type != "error" {
		t.Errorf("first slot should be Error AlphaCheck: %+v", got[0])
	}
	if got[1].Source != "BetaCheck" || got[1].Type != "warning" {
		t.Errorf("second slot should be Warning BetaCheck: %+v", got[1])
	}
	if got[2].Source != "ZetaCheck" {
		t.Errorf("third slot should be ZetaCheck: %+v", got[2])
	}
}

func TestHealthHandler_TypeStringMapping(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HealthHandler(&fakeHealthLister{results: []healthcheck.HealthCheck{
		healthcheck.New("X", healthcheck.ResultNotice, healthcheck.ReasonAppDataLocation, "m", "#x"),
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/health", nil))
	if !strings.Contains(rr.Body.String(), `"type":"notice"`) {
		t.Errorf("type mapping for Notice: %s", rr.Body.String())
	}
}
