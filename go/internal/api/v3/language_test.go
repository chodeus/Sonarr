package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

// J314 helper pin: ToLanguageRef + ToLanguageRefs project the
// persisted int form into the 2-field carrier wire shape used by
// History/Blocklist resources. Unrecognised ids preserve the
// persisted int while flagging name as "Unknown" (matches C#
// `Language.FindById(id) ?? Language.Unknown` posture). Empty/nil
// input yields a non-nil empty slice so the wire emits
// `"languages":[]` rather than `null` — matches C# `required
// List<Language>` non-null default.
func TestToLanguageRefs_HandlesNilEmptyAndUnknown_J314(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []int
		want []LanguageRef
	}{
		{"nil → non-nil empty slice", nil, []LanguageRef{}},
		{"empty → non-nil empty slice", []int{}, []LanguageRef{}},
		{"single English", []int{1}, []LanguageRef{{ID: 1, Name: "English"}}},
		{"mixed English + French", []int{1, 2}, []LanguageRef{{ID: 1, Name: "English"}, {ID: 2, Name: "French"}}},
		{"unknown id preserves int, name=Unknown", []int{99999}, []LanguageRef{{ID: 99999, Name: "Unknown"}}},
		{"id 0 → Unknown (registry resolves)", []int{0}, []LanguageRef{{ID: 0, Name: "Unknown"}}},
		{"Original id -2 resolves via registry", []int{-2}, []LanguageRef{{ID: -2, Name: "Original"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToLanguageRefs(tc.in)
			if got == nil {
				t.Errorf("must return non-nil slice (got nil)")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d want %d (got=%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %+v want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// J314 wire-shape pin: the 2-field LanguageRef marshals to
// `{"id":N,"name":"X"}` — both fields always emit (no omitempty;
// matches C# Language model under STJson, where Id=0/Unknown still
// emits explicitly).
func TestLanguageRef_WireShape_J314(t *testing.T) {
	t.Parallel()
	buf, err := json.Marshal(LanguageRef{ID: 1, Name: "English"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(buf) != `{"id":1,"name":"English"}` {
		t.Errorf("wire shape: got %s want `{\"id\":1,\"name\":\"English\"}`", buf)
	}
	// Id=0 / empty-name must still emit (no omitempty on either field).
	buf2, err := json.Marshal(LanguageRef{})
	if err != nil {
		t.Fatalf("marshal-zero: %v", err)
	}
	if string(buf2) != `{"id":0,"name":""}` {
		t.Errorf("zero-value wire shape: got %s want `{\"id\":0,\"name\":\"\"}`", buf2)
	}
}

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

func TestLanguageByIDHandler(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		id       string
		wantCode int
		wantBody *LanguageResource
	}{
		{"found English", "1", http.StatusOK, &LanguageResource{ID: 1, Name: "English", NameLower: "english"}},
		{"unknown id 0 resolves via registry", "0", http.StatusOK, &LanguageResource{ID: 0, Name: "Unknown", NameLower: "unknown"}},
		{"missing id → 404", "99999", http.StatusNotFound, nil},
		{"non-numeric → 400", "abc", http.StatusBadRequest, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v3/language/"+tc.id, nil)
			req.SetPathValue("id", tc.id)
			rr := httptest.NewRecorder()
			// Pass the full registry so 0/Unknown resolves; the handler
			// also has a global fallback for partial slices.
			LanguageByIDHandler(language.All()).ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("status: got %d want %d (body=%s)", rr.Code, tc.wantCode, rr.Body)
			}
			if tc.wantBody != nil {
				var got LanguageResource
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if got != *tc.wantBody {
					t.Errorf("body: got %+v want %+v", got, *tc.wantBody)
				}
			}
		})
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
