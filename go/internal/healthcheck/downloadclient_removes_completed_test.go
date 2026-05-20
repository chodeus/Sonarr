package healthcheck_test

import (
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/healthcheck"
)

func TestDownloadClientRemovesCompletedCheck_NewRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(nil, nil); err == nil {
		t.Errorf("expected error for nil enumerator")
	}
}

func TestDownloadClientRemovesCompletedCheck_OKAllFalse(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Sab", Status: &downloadclients.ClientInfo{RemovesCompletedDownloads: false}},
		{Name: "qBit", Status: &downloadclients.ClientInfo{RemovesCompletedDownloads: false}},
	}
	chk, _ := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(
		fakeDCStatusEnumerator{rows: rows}, quietLogger())
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientRemovesCompletedCheck_Warns(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Sab", Status: &downloadclients.ClientInfo{RemovesCompletedDownloads: false}},
		{Name: "Trans", Status: &downloadclients.ClientInfo{RemovesCompletedDownloads: true}},
	}
	chk, _ := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(
		fakeDCStatusEnumerator{rows: rows}, quietLogger())
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != healthcheck.ReasonDownloadClientRemovesCompletedDownloads {
		t.Errorf("reason = %v", got.Reason)
	}
	if !strings.Contains(got.Message, "Trans") {
		t.Errorf("message missing client name: %q", got.Message)
	}
}

func TestDownloadClientRemovesCompletedCheck_SkipsErrRows(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Broken", Err: errors.New("construct failed")},
		{Name: "Trans", Status: &downloadclients.ClientInfo{RemovesCompletedDownloads: true}},
	}
	chk, _ := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(
		fakeDCStatusEnumerator{rows: rows}, quietLogger())
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning (Trans still flagged)", got.Type)
	}
}

func TestDownloadClientRemovesCompletedCheck_NilStatusIgnored(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{{Name: "Mysterious", Status: nil}}
	chk, _ := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(
		fakeDCStatusEnumerator{rows: rows}, quietLogger())
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientRemovesCompletedCheck_EnumerationErrorPropagates(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(
		fakeDCStatusEnumerator{err: errors.New("boom")}, quietLogger())
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
	if !strings.Contains(got.Message, "boom") {
		t.Errorf("message: %q", got.Message)
	}
}

func TestDownloadClientRemovesCompletedCheck_FlagDefaults(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(
		fakeDCStatusEnumerator{}, quietLogger())
	if !chk.CheckOnStartup() || !chk.CheckOnSchedule() {
		t.Errorf("startup/schedule defaults should be true")
	}
	if len(chk.EventTriggers()) == 0 {
		t.Errorf("expected at least one event trigger")
	}
}
