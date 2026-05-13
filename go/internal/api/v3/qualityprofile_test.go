package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// fakeProfileLister returns canned QualityProfile rows.
type fakeProfileLister struct {
	rows []qualityprofiles.Profile
	err  error
}

func (f *fakeProfileLister) All(context.Context) ([]qualityprofiles.Profile, error) {
	return f.rows, f.err
}

// fakeFormatLister stands in for customformats.Service.All.
type fakeFormatLister struct {
	rows []*customformats.Format
	err  error
}

func (f *fakeFormatLister) All(context.Context) ([]*customformats.Format, error) {
	return f.rows, f.err
}

func TestQualityProfileListHandler_QualityEntry(t *testing.T) {
	t.Parallel()
	// One profile with two top-level quality entries (no groups, no
	// format items). Exercises the quality-entry branch: full Quality
	// object on the wire, size pointers present, Id/Name omitted.
	itemsJSON := `[
		{"id":0,"name":"","quality":1,"items":[],"allowed":true,"minSize":2,"maxSize":100,"preferredSize":95},
		{"id":0,"name":"","quality":2,"items":[],"allowed":false,"minSize":null,"maxSize":null,"preferredSize":null}
	]`
	lister := &fakeProfileLister{rows: []qualityprofiles.Profile{
		{ID: 1, Name: "Test", Cutoff: 1, Items: itemsJSON, FormatItems: "[]", UpgradeAllowed: true},
	}}
	rr := httptest.NewRecorder()
	QualityProfileListHandler(lister, &fakeFormatLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/qualityprofile", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}

	var got []QualityProfileResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || len(got[0].Items) != 2 {
		t.Fatalf("shape: %+v", got)
	}
	if got[0].Items[0].Quality == nil || got[0].Items[0].Quality.ID != 1 || got[0].Items[0].Quality.Source != "television" {
		t.Errorf("first item Quality: %+v", got[0].Items[0].Quality)
	}
	if got[0].Items[0].MinSize == nil || *got[0].Items[0].MinSize != 2 {
		t.Errorf("first item MinSize: %+v", got[0].Items[0].MinSize)
	}

	// Wire-level assertions — Id/Name must be omitted on the
	// quality-entry shape (omitempty default).
	body := rr.Body.String()
	if strings.Contains(body, `"id":0`) {
		t.Errorf("body should omit zero Id on quality entries:\n%s", body)
	}
}

func TestQualityProfileListHandler_GroupEntry(t *testing.T) {
	t.Parallel()
	// One profile carrying a group (id=1000) wrapping two qualities.
	// Exercises the group branch: Id/Name present, Quality omitted,
	// size pointers omitted, child Items each carry a Quality.
	itemsJSON := `[
		{"id":1000,"name":"Web 480p","quality":0,"items":[
			{"id":0,"name":"","quality":12,"items":[],"allowed":true,"minSize":2,"maxSize":100,"preferredSize":95},
			{"id":0,"name":"","quality":8,"items":[],"allowed":true,"minSize":2,"maxSize":100,"preferredSize":95}
		],"allowed":true,"minSize":null,"maxSize":null,"preferredSize":null}
	]`
	lister := &fakeProfileLister{rows: []qualityprofiles.Profile{
		{ID: 7, Name: "WithGroup", Cutoff: 1000, Items: itemsJSON, FormatItems: "[]"},
	}}
	rr := httptest.NewRecorder()
	QualityProfileListHandler(lister, &fakeFormatLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/qualityprofile", nil))

	body := rr.Body.String()
	// Group must carry id + name on the wire; must NOT carry a
	// quality object or size limits.
	if !strings.Contains(body, `"id":1000`) {
		t.Errorf("group should emit id=1000:\n%s", body)
	}
	if !strings.Contains(body, `"name":"Web 480p"`) {
		t.Errorf("group should emit name:\n%s", body)
	}
	if strings.Contains(body, `"quality":{"id":0,"name":""`) {
		// "id":0 inside a "quality" object would betray a present-but-zero
		// Quality on the group — Quality should be omitted entirely.
		t.Errorf("group quality should be omitted:\n%s", body)
	}
}

func TestQualityProfileListHandler_FormatNameJoin(t *testing.T) {
	t.Parallel()
	// FormatItems carry { format, score }; the handler must join Name
	// from the CustomFormat lookup. Missing Ids fall back to "" (the
	// map zero-value), matching what an orphaned formatItem produces.
	formatsJSON := `[{"format":42,"score":10},{"format":99,"score":-5}]`
	lister := &fakeProfileLister{rows: []qualityprofiles.Profile{
		{ID: 1, Name: "X", Items: "[]", FormatItems: formatsJSON},
	}}
	formatsLister := &fakeFormatLister{rows: []*customformats.Format{
		{ID: 42, Name: "FortyTwo"},
	}}
	rr := httptest.NewRecorder()
	QualityProfileListHandler(lister, formatsLister).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/qualityprofile", nil))

	var got []QualityProfileResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 1 || len(got[0].FormatItems) != 2 {
		t.Fatalf("shape: %+v", got)
	}
	if got[0].FormatItems[0].Name != "FortyTwo" || got[0].FormatItems[0].Score != 10 {
		t.Errorf("formatItem[0] join: %+v", got[0].FormatItems[0])
	}
	if got[0].FormatItems[1].Name != "" || got[0].FormatItems[1].Format != 99 {
		t.Errorf("formatItem[1] orphan: %+v", got[0].FormatItems[1])
	}
}

func TestQualityProfileListHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QualityProfileListHandler(
		&fakeProfileLister{err: errors.New("boom")},
		&fakeFormatLister{},
	).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualityprofile", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
