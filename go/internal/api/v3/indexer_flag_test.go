package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndexerFlagHandler_EmitsAllNineFlags(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerFlagHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexerflag", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []IndexerFlagResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("len: got %d want 9; body=%s", len(got), rr.Body.String())
	}
	expected := []IndexerFlagResource{
		{ID: 1, Name: "Freeleech", NameLower: "freeleech"},
		{ID: 2, Name: "Halfleech", NameLower: "halfleech"},
		{ID: 4, Name: "DoubleUpload", NameLower: "doubleupload"},
		{ID: 8, Name: "Internal", NameLower: "internal"},
		{ID: 16, Name: "Scene", NameLower: "scene"},
		{ID: 32, Name: "Freeleech75", NameLower: "freeleech75"},
		{ID: 64, Name: "Freeleech25", NameLower: "freeleech25"},
		{ID: 128, Name: "Nuked", NameLower: "nuked"},
		{ID: 256, Name: "Subtitles", NameLower: "subtitles"},
	}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("idx %d: got %+v want %+v", i, got[i], want)
		}
	}
}

func TestIndexerFlagHandler_JSONFieldOrder(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerFlagHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexerflag", nil))
	body := rr.Body.String()
	// Wire shape spot check: first record's keys must appear in
	// declaration order id->name->nameLower (matches the C# resource
	// projection where Id is the [JsonProperty] override + Name +
	// computed NameLower).
	want := `[{"id":1,"name":"Freeleech","nameLower":"freeleech"}`
	if body[:len(want)] != want {
		t.Errorf("body prefix: got %q want %q", body[:len(want)], want)
	}
}
