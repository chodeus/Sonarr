package healthcheck_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/series"
)

// stubRemovedSeriesLister returns a fixed slice / error.
type stubRemovedSeriesLister struct {
	all []series.Series
	err error
}

func (s stubRemovedSeriesLister) GetAllSeries(_ context.Context) ([]series.Series, error) {
	return s.all, s.err
}

func makeRemovedSeriesCheck(t *testing.T, lister healthcheck.RemovedSeriesLister) *healthcheck.RemovedSeriesCheck {
	t.Helper()
	c, err := healthcheck.NewRemovedSeriesCheck(lister)
	if err != nil {
		t.Fatalf("NewRemovedSeriesCheck: %v", err)
	}
	return c
}

func TestRemovedSeriesCheck_NewRequiresLister(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewRemovedSeriesCheck(nil); err == nil {
		t.Errorf("expected error on nil lister")
	}
}

func TestRemovedSeriesCheck_AllActiveSeriesPasses(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{all: []series.Series{
		{ID: 1, Title: "Show A", TvdbID: 100, Status: series.SeriesStatusContinuing},
		{ID: 2, Title: "Show B", TvdbID: 200, Status: series.SeriesStatusEnded},
	}})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK, got %v: %s", got.Type, got.Message)
	}
}

func TestRemovedSeriesCheck_SingleDeletedReturnsSingleReason(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{all: []series.Series{
		{ID: 1, Title: "Show A", TvdbID: 100, Status: series.SeriesStatusContinuing},
		{ID: 2, Title: "Removed Show", TvdbID: 999, Status: series.SeriesStatusDeleted},
	}})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRemovedSeriesSingle {
		t.Errorf("Reason = %v, want ReasonRemovedSeriesSingle", got.Reason)
	}
	if !strings.Contains(got.Message, "Removed Show") {
		t.Errorf("Message %q should mention deleted series", got.Message)
	}
	if !strings.Contains(got.Message, "999") {
		t.Errorf("Message %q should mention TvdbID", got.Message)
	}
}

func TestRemovedSeriesCheck_MultipleDeletedReturnsMultipleReason(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{all: []series.Series{
		{ID: 1, Title: "Zebra Show", TvdbID: 100, Status: series.SeriesStatusDeleted},
		{ID: 2, Title: "Alpha Show", TvdbID: 200, Status: series.SeriesStatusDeleted},
		{ID: 3, Title: "Active Show", TvdbID: 300, Status: series.SeriesStatusContinuing},
	}})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRemovedSeriesMultiple {
		t.Errorf("Reason = %v, want ReasonRemovedSeriesMultiple", got.Reason)
	}
	// Title sort is case-insensitive; "Alpha Show" comes before "Zebra Show".
	if !strings.Contains(got.Message, "Alpha Show") || !strings.Contains(got.Message, "Zebra Show") {
		t.Errorf("Message %q should list both deleted titles", got.Message)
	}
	alphaIdx := strings.Index(got.Message, "Alpha Show")
	zebraIdx := strings.Index(got.Message, "Zebra Show")
	if alphaIdx >= zebraIdx {
		t.Errorf("Alpha Show should appear before Zebra Show in %q", got.Message)
	}
}

func TestRemovedSeriesCheck_EmptyListPasses(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{all: []series.Series{}})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on empty list, got %v", got.Type)
	}
}

func TestRemovedSeriesCheck_ListerErrorReturnsWarning(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{err: errors.New("db down")})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning, got %v", got.Type)
	}
	if !strings.Contains(got.Message, "db down") {
		t.Errorf("Message %q should mention error", got.Message)
	}
}

func TestRemovedSeriesCheck_CheckOnStartupAndScheduleDefaults(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{})
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
}

func TestRemovedSeriesCheck_EventTriggersIncludeSeriesDeleted(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{})
	want := reflect.TypeOf(series.SeriesDeletedEvent{})
	var trigger *healthcheck.EventTrigger
	for i, tr := range c.EventTriggers() {
		if tr.EventType == want {
			t1 := c.EventTriggers()[i]
			trigger = &t1
		}
	}
	if trigger == nil {
		t.Fatalf("expected SeriesDeletedEvent trigger")
	}
	if trigger.Filter == nil {
		t.Fatalf("expected Filter on SeriesDeletedEvent trigger")
	}
	// Filter should fire only when at least one removed series had
	// Status == Deleted.
	deletedEv := series.SeriesDeletedEvent{Series: []series.Series{
		{ID: 1, Status: series.SeriesStatusDeleted},
		{ID: 2, Status: series.SeriesStatusContinuing},
	}}
	if !trigger.Filter(deletedEv) {
		t.Errorf("expected filter to pass when at least one deleted-status series present")
	}
	noneDeletedEv := series.SeriesDeletedEvent{Series: []series.Series{
		{ID: 1, Status: series.SeriesStatusContinuing},
	}}
	if trigger.Filter(noneDeletedEv) {
		t.Errorf("expected filter to skip when no deleted-status series")
	}
	// Wrong-typed event → filter returns false (defensive).
	if trigger.Filter("not an event") {
		t.Errorf("filter should reject wrong-typed events")
	}
}

func TestRemovedSeriesCheck_NameAndInterfaceShape(t *testing.T) {
	t.Parallel()
	c := makeRemovedSeriesCheck(t, stubRemovedSeriesLister{})
	if c.Name() != healthcheck.RemovedSeriesCheckName {
		t.Errorf("Name = %q", c.Name())
	}
	var iface healthcheck.Check = c
	_ = iface
	var iface2 healthcheck.EventDrivenCheck = c
	_ = iface2
}
