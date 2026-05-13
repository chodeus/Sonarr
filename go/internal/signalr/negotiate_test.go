package signalr_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/signalr"
)

func TestNegotiateHandler_PostReturnsResponse(t *testing.T) {
	t.Parallel()
	gen := func() (string, error) { return "fixed-conn-id", nil }
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signalr/messages/negotiate?negotiateVersion=1", nil)
	signalr.NegotiateHandler(gen).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var resp signalr.NegotiateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.NegotiateVersion != 1 {
		t.Errorf("negotiateVersion: %d", resp.NegotiateVersion)
	}
	if resp.ConnectionID != "fixed-conn-id" {
		t.Errorf("connectionId: %q", resp.ConnectionID)
	}
	if resp.ConnectionToken != "fixed-conn-id" {
		t.Errorf("connectionToken should mirror id at negotiateVersion=1: %q", resp.ConnectionToken)
	}
	if len(resp.AvailableTransports) != 1 || resp.AvailableTransports[0].Transport != "WebSockets" {
		t.Errorf("availableTransports: %+v", resp.AvailableTransports)
	}
}

func TestNegotiateHandler_OptionsReturns200(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/signalr/messages/negotiate", nil)
	signalr.NegotiateHandler(nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("OPTIONS preflight: got %d want 200", rr.Code)
	}
}

func TestNegotiateHandler_GetReturnsMethodNotAllowed(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signalr/messages/negotiate", nil)
	signalr.NegotiateHandler(nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: got %d want 405", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Allow"), "POST") {
		t.Errorf("Allow header missing POST: %q", rr.Header().Get("Allow"))
	}
}

func TestNegotiateHandler_GeneratorError(t *testing.T) {
	t.Parallel()
	gen := func() (string, error) { return "", errors.New("rng-down") }
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signalr/messages/negotiate", nil)
	signalr.NegotiateHandler(gen).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestDefaultConnectionIDGenerator_Unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 64)
	for range 64 {
		id, err := signalr.DefaultConnectionIDGenerator()
		if err != nil {
			t.Fatalf("generator: %v", err)
		}
		if id == "" {
			t.Errorf("generator returned empty id")
		}
		if seen[id] {
			t.Errorf("generator returned duplicate id: %q", id)
		}
		seen[id] = true
	}
}
