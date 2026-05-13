package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

func TestListAll_SortOrder(t *testing.T) {
	t.Parallel()
	all := ListAll(language.All())
	if len(all) == 0 {
		t.Fatal("ListAll returned empty slice")
	}
	// Non-positive IDs (Original=-2, Unknown=0) sort first in name
	// order; the rest follow alphabetically. Verify the leading two
	// entries and that the third begins the positive-Id alphabetical
	// run.
	if all[0].Name != "Original" || all[0].ID != -2 {
		t.Errorf("first entry: got %+v want Original/-2", all[0])
	}
	if all[1].Name != "Unknown" || all[1].ID != 0 {
		t.Errorf("second entry: got %+v want Unknown/0", all[1])
	}
	if all[2].ID <= 0 {
		t.Errorf("third entry should be positive-Id: got %+v", all[2])
	}
	// Confirm full alphabetical ordering within each bucket.
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		prevBucket, curBucket := bucket(prev.ID), bucket(cur.ID)
		if prevBucket > curBucket {
			t.Errorf("buckets out of order at %d: %+v then %+v", i, prev, cur)
		}
		if prevBucket == curBucket && prev.Name > cur.Name {
			t.Errorf("names out of order at %d: %q then %q", i, prev.Name, cur.Name)
		}
	}
}

func TestListAll_NameLower(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   language.Language
		want string
	}{
		{language.English, "english"},
		{language.PortugueseBrazil, "portuguese (brazil)"},
		{language.Original, "original"},
	}
	for _, tc := range cases {
		got := ListAll([]language.Language{tc.in})
		if got[0].NameLower != tc.want {
			t.Errorf("%s: got %q want %q", tc.in.Name, got[0].NameLower, tc.want)
		}
	}
}

func TestLanguageHandler_BodyShape(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/language", nil)
	rr := httptest.NewRecorder()
	LanguageHandler([]language.Language{language.English}).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}

	var got []LanguageResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0] != (LanguageResource{ID: 1, Name: "English", NameLower: "english"}) {
		t.Fatalf("body: %+v", got)
	}
}
