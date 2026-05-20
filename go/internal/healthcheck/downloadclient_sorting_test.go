package healthcheck_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/healthcheck"
)

type fakeDCStatusEnumerator struct {
	rows []healthcheck.EnumeratedDownloadClient
	err  error
}

func (f fakeDCStatusEnumerator) EnabledDownloadClients(_ context.Context) ([]healthcheck.EnumeratedDownloadClient, error) {
	return f.rows, f.err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDownloadClientSortingCheck_NewRejectsNilEnumerator(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewDownloadClientSortingCheck(nil, nil); err == nil {
		t.Errorf("expected error for nil enumerator")
	}
}

func TestDownloadClientSortingCheck_OKEmpty(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientSortingCheck(fakeDCStatusEnumerator{}, quietLogger())
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientSortingCheck_OKAllClean(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Sab", Status: &downloadclients.ClientInfo{SortingMode: ""}},
		{Name: "Trans", Status: &downloadclients.ClientInfo{SortingMode: "   "}},
	}
	chk, _ := healthcheck.NewDownloadClientSortingCheck(fakeDCStatusEnumerator{rows: rows}, quietLogger())
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientSortingCheck_WarnsOnSortingMode(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Sab", Status: &downloadclients.ClientInfo{SortingMode: ""}},
		{Name: "qBit", Status: &downloadclients.ClientInfo{SortingMode: "category"}},
	}
	chk, _ := healthcheck.NewDownloadClientSortingCheck(fakeDCStatusEnumerator{rows: rows}, quietLogger())
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != healthcheck.ReasonDownloadClientSorting {
		t.Errorf("reason = %v", got.Reason)
	}
	if !strings.Contains(got.Message, "qBit") {
		t.Errorf("message missing client name: %q", got.Message)
	}
	if !strings.Contains(got.Message, "category") {
		t.Errorf("message missing sorting mode: %q", got.Message)
	}
}

func TestDownloadClientSortingCheck_SkipsClientsWithErr(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Broken", Err: errors.New("construct failed")},
		{Name: "qBit", Status: &downloadclients.ClientInfo{SortingMode: "subcategory"}},
	}
	chk, _ := healthcheck.NewDownloadClientSortingCheck(fakeDCStatusEnumerator{rows: rows}, quietLogger())
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v, want Warning (broken client skipped, qBit still flagged)", got.Type)
	}
	if !strings.Contains(got.Message, "qBit") {
		t.Errorf("expected qBit; got %q", got.Message)
	}
}

func TestDownloadClientSortingCheck_NilStatusIgnored(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Mysterious", Status: nil}, // no err, no status (defensive)
	}
	chk, _ := healthcheck.NewDownloadClientSortingCheck(fakeDCStatusEnumerator{rows: rows}, quietLogger())
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK (nil status defensive)", got.Type)
	}
}

func TestDownloadClientSortingCheck_TotalEnumerationErrorPropagates(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientSortingCheck(
		fakeDCStatusEnumerator{err: errors.New("total boom")},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
	if !strings.Contains(got.Message, "total boom") {
		t.Errorf("message: %q", got.Message)
	}
}

func TestDownloadClientSortingCheck_FlagDefaults(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientSortingCheck(fakeDCStatusEnumerator{}, quietLogger())
	if !chk.CheckOnStartup() || !chk.CheckOnSchedule() {
		t.Errorf("CheckOnStartup/Schedule should default true")
	}
	if len(chk.EventTriggers()) == 0 {
		t.Errorf("expected at least one event trigger")
	}
}
