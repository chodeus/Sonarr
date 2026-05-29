package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeUpdateLister struct {
	rows []UpdateResource
	err  error
}

func (f *fakeUpdateLister) RecentUpdates(context.Context) ([]UpdateResource, error) {
	return f.rows, f.err
}

func TestUpdateHandler_EmptyList(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UpdateHandler(&fakeUpdateLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/update", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body should be []: %q", rr.Body.String())
	}
}

func TestUpdateHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UpdateHandler(&fakeUpdateLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/update", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// J403: compareVersions mirrors System.Version component-wise ordering.
func TestCompareVersions_J403(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"4.0.1.234", "4.0.1.234", 0},
		{"4.0.2.0", "4.0.1.999", 1},
		{"4.0.1.5", "4.0.1.10", -1},
		{"4.0.1", "4.0.1.0", 0}, // missing trailing component == 0
		{"4.0.1.1", "4.0.1", 1}, // present > absent
		{"4.1", "4.0.9.9", 1},   // major.minor dominates
		{"dev", "4.0.1.0", -1},  // non-numeric → 0 → below any release
		{"dev", "0.0.0.0", 0},   // non-numeric == all-zero
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// J403: empty list is returned unmarked (no panic, no flags).
func TestMarkRecentUpdates_Empty_J403(t *testing.T) {
	t.Parallel()
	if got := markRecentUpdates(nil, "4.0.1.0"); len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

// J403: a single package newer than the build → Latest + Installable,
// not Installed.
func TestMarkRecentUpdates_NewerThanBuild_J403(t *testing.T) {
	t.Parallel()
	rows := markRecentUpdates([]UpdateResource{{Version: "4.0.2.0"}}, "4.0.1.0")
	if !rows[0].Latest || !rows[0].Installable || rows[0].Installed {
		t.Errorf("got %+v, want Latest+Installable, not Installed", rows[0])
	}
}

// J403: unsorted multi-row input — highest version becomes row[0] and is
// Latest; the row equal to the build is Installed; Installable set on the
// newer top row.
func TestMarkRecentUpdates_SortsAndMarks_J403(t *testing.T) {
	t.Parallel()
	rows := markRecentUpdates([]UpdateResource{
		{Version: "4.0.1.0"}, // == build → Installed
		{Version: "4.0.3.0"}, // newest → Latest + Installable
		{Version: "4.0.2.0"},
	}, "4.0.1.0")
	if rows[0].Version != "4.0.3.0" || !rows[0].Latest || !rows[0].Installable {
		t.Errorf("row0 = %+v, want 4.0.3.0 Latest+Installable", rows[0])
	}
	if rows[0].Installed {
		t.Error("newest row must not be Installed")
	}
	// The build-matching row is last after desc sort.
	last := rows[len(rows)-1]
	if last.Version != "4.0.1.0" || !last.Installed || last.Installable {
		t.Errorf("build row = %+v, want 4.0.1.0 Installed, not Installable", last)
	}
}

// J403: when the highest row equals the build, it is Latest + Installed
// but NOT Installable (nothing newer to install).
func TestMarkRecentUpdates_HighestIsBuild_J403(t *testing.T) {
	t.Parallel()
	rows := markRecentUpdates([]UpdateResource{
		{Version: "4.0.1.0"},
		{Version: "3.9.0.0"},
	}, "4.0.1.0")
	if !rows[0].Latest || rows[0].Installable || !rows[0].Installed {
		t.Errorf("got %+v, want Latest+Installed, not Installable", rows[0])
	}
}
