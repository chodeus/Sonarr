package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
