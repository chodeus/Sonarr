package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeFreeSpaceLister struct {
	records []DiskSpaceResource
	err     error
}

func (f *fakeFreeSpaceLister) GetFreeSpace(_ context.Context) ([]DiskSpaceResource, error) {
	return f.records, f.err
}

func TestDiskSpaceHandler_EmptyReturnsArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DiskSpaceHandler(&fakeFreeSpaceLister{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/diskspace", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `[]` {
		t.Errorf("empty body: got %q want []", rr.Body.String())
	}
}

func TestDiskSpaceHandler_Records(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DiskSpaceHandler(&fakeFreeSpaceLister{records: []DiskSpaceResource{
		{Path: "/mnt/data", FreeSpace: 500, TotalSpace: 1000},
		{Path: "/mnt/archive", FreeSpace: 200, TotalSpace: 2000},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/diskspace", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []DiskSpaceResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	// Ordering: case-insensitive Path ascending → /mnt/archive before /mnt/data.
	if got[0].Path != "/mnt/archive" || got[1].Path != "/mnt/data" {
		t.Errorf("ordering: got %q,%q", got[0].Path, got[1].Path)
	}
	if got[0].FreeSpace != 200 || got[0].TotalSpace != 2000 {
		t.Errorf("/mnt/archive figures: %+v", got[0])
	}
}

func TestDiskSpaceHandler_Error_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DiskSpaceHandler(&fakeFreeSpaceLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/diskspace", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}
