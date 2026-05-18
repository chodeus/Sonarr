package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDownloadClientConfig struct {
	workingFolders string
	enabled        bool
	autoRedl       bool
	autoRedlInter  bool
}

func (f *fakeDownloadClientConfig) DownloadClientWorkingFolders() string  { return f.workingFolders }
func (f *fakeDownloadClientConfig) EnableCompletedDownloadHandling() bool { return f.enabled }
func (f *fakeDownloadClientConfig) AutoRedownloadFailed() bool            { return f.autoRedl }
func (f *fakeDownloadClientConfig) AutoRedownloadFailedFromInteractiveSearch() bool {
	return f.autoRedlInter
}

func TestDownloadClientConfigHandler_Defaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientConfigHandler(&fakeDownloadClientConfig{
		workingFolders: "_UNPACK_|_FAILED_",
		enabled:        true,
		autoRedl:       true,
		autoRedlInter:  true,
	}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/downloadclient", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got DownloadClientConfigResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := DownloadClientConfigResource{
		ID:                              1,
		DownloadClientWorkingFolders:    "_UNPACK_|_FAILED_",
		EnableCompletedDownloadHandling: true,
		AutoRedownloadFailed:            true,
		AutoRedownloadFailedFromInteractiveSearch: true,
	}
	if got != want {
		t.Errorf("body: got %+v want %+v", got, want)
	}
}

// ---------- 5.B13: PUT tests ----------

type fakeDownloadClientConfigWriter struct {
	*fakeDownloadClientConfig
	wf                                string
	enable, auto, autoFromInteractive bool
}

func (f *fakeDownloadClientConfigWriter) SetDownloadClientWorkingFolders(_ context.Context, v string) error {
	f.wf = v
	f.workingFolders = v
	return nil
}
func (f *fakeDownloadClientConfigWriter) SetEnableCompletedDownloadHandling(_ context.Context, v bool) error {
	f.enable = v
	f.enabled = v
	return nil
}
func (f *fakeDownloadClientConfigWriter) SetAutoRedownloadFailed(_ context.Context, v bool) error {
	f.auto = v
	f.autoRedl = v
	return nil
}
func (f *fakeDownloadClientConfigWriter) SetAutoRedownloadFailedFromInteractiveSearch(_ context.Context, v bool) error {
	f.autoFromInteractive = v
	f.autoRedlInter = v
	return nil
}

func TestDownloadClientConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientConfigWriter{fakeDownloadClientConfig: &fakeDownloadClientConfig{}}
	rr := httptest.NewRecorder()
	body := `{"id":1,"downloadClientWorkingFolders":"_UNPACK_|_FAILED_","enableCompletedDownloadHandling":true,"autoRedownloadFailed":true,"autoRedownloadFailedFromInteractiveSearch":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/downloadclient/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	DownloadClientConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.wf != "_UNPACK_|_FAILED_" {
		t.Errorf("wf: got %q", w.wf)
	}
	if !w.enable {
		t.Errorf("enable not set")
	}
	if !w.auto {
		t.Errorf("auto not set")
	}
	if w.autoFromInteractive {
		t.Errorf("autoFromInteractive should be false")
	}
}

func TestDownloadClientConfigUpdateHandler_MalformedBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/downloadclient/1", strings.NewReader(`not json`))
	req.SetPathValue("id", "1")
	DownloadClientConfigUpdateHandler(&fakeDownloadClientConfigWriter{fakeDownloadClientConfig: &fakeDownloadClientConfig{}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
