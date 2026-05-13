package synology_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/synology"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Name string
	Args []string
}

type fakeRunner struct {
	calls  []captured
	stdout string
	stderr string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, string, error) {
	f.calls = append(f.calls, captured{Name: name, Args: append([]string(nil), args...)})
	return f.stdout, f.stderr, f.err
}

func settingsJSON(t *testing.T, s synology.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeSynology(t *testing.T, extras func(*synology.Settings)) (notifications.Provider, *fakeRunner) {
	t.Helper()
	s := synology.Settings{UpdateLibrary: true}
	if extras != nil {
		extras(&s)
	}
	fr := &fakeRunner{}
	synology.SetRunnerForTest(fr)
	t.Cleanup(func() { synology.SetRunnerForTest(nil) })
	def := &notifications.Notification{
		Implementation: synology.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := synology.New(def, notifications.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, fr
}

func TestSettings_AlwaysValid(t *testing.T) {
	t.Parallel()
	fails := (&synology.Settings{}).Validate()
	if len(fails) != 0 {
		t.Errorf("expected no failures, got %+v", fails)
	}
}

func TestSynology_Disabled_UpdateLibrary_SkipsAllHooks(t *testing.T) {
	prov, fr := makeSynology(t, func(s *synology.Settings) { s.UpdateLibrary = false })
	if err := prov.OnDownload(t.Context(), notifications.DownloadMessage{
		Series:      &series.Series{Path: "/srv/series/Show"},
		EpisodeFile: &episodefiles.File{RelativePath: "S01/E01.mkv"},
	}); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("expected no calls, got %+v", fr.calls)
	}
}

func TestSynology_OnDownload_DeletesOldFilesThenAddsNew(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	msg := notifications.DownloadMessage{
		Series: &series.Series{Path: "/srv/series/Show"},
		OldFiles: []*episodefiles.DeletedFile{
			{File: &episodefiles.File{RelativePath: "S01/old1.mkv"}},
			{File: &episodefiles.File{RelativePath: "S01/old2.mkv"}},
		},
		EpisodeFile: &episodefiles.File{RelativePath: "S01/new.mkv"},
	}
	if err := prov.OnDownload(t.Context(), msg); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	if len(fr.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(fr.calls))
	}
	if fr.calls[0].Args[0] != "-d" || fr.calls[0].Args[1] != "/srv/series/Show/S01/old1.mkv" {
		t.Errorf("first call = %+v", fr.calls[0])
	}
	if fr.calls[1].Args[0] != "-d" || fr.calls[1].Args[1] != "/srv/series/Show/S01/old2.mkv" {
		t.Errorf("second call = %+v", fr.calls[1])
	}
	if fr.calls[2].Args[0] != "-a" || fr.calls[2].Args[1] != "/srv/series/Show/S01/new.mkv" {
		t.Errorf("third call = %+v", fr.calls[2])
	}
}

func TestSynology_OnImportComplete_UpdateFolder(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	msg := notifications.ImportCompleteMessage{Series: &series.Series{Path: "/srv/series/Show"}}
	if err := prov.OnImportComplete(t.Context(), msg); err != nil {
		t.Fatalf("OnImportComplete: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].Args[0] != "-R" || fr.calls[0].Args[1] != "/srv/series/Show" {
		t.Errorf("calls=%+v", fr.calls)
	}
}

func TestSynology_OnSeriesDelete_OnlyWhenDeletedFiles(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	msg := notifications.SeriesDeleteMessage{Series: &series.Series{Path: "/srv/Show"}, DeletedFiles: false}
	if err := prov.OnSeriesDelete(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesDelete: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("DeletedFiles=false should be no-op, got %+v", fr.calls)
	}

	msg2 := notifications.SeriesDeleteMessage{Series: &series.Series{Path: "/srv/Show"}, DeletedFiles: true}
	if err := prov.OnSeriesDelete(t.Context(), msg2); err != nil {
		t.Fatalf("OnSeriesDelete: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].Args[0] != "-D" || fr.calls[0].Args[1] != "/srv/Show" {
		t.Errorf("calls=%+v", fr.calls)
	}
}

func TestSynology_OnRename_UpdatesFolderFromAnySeries(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	msg := notifications.RenameMessage{Series: &series.Series{Path: "/srv/Show"}}
	if err := prov.OnRename(t.Context(), msg); err != nil {
		t.Fatalf("OnRename: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].Args[0] != "-R" {
		t.Errorf("calls=%+v", fr.calls)
	}
}

func TestSynology_OnEpisodeFileDelete_DeletesPath(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	msg := notifications.EpisodeDeleteMessage{
		Series:      &series.Series{Path: "/srv/Show"},
		EpisodeFile: &episodefiles.File{RelativePath: "S01/E01.mkv"},
	}
	if err := prov.OnEpisodeFileDelete(t.Context(), msg); err != nil {
		t.Fatalf("OnEpisodeFileDelete: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].Args[0] != "-d" || fr.calls[0].Args[1] != "/srv/Show/S01/E01.mkv" {
		t.Errorf("calls=%+v", fr.calls)
	}
}

func TestSynology_OnGrab_IsNoop(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{}); err != nil {
		t.Errorf("OnGrab: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("OnGrab should be no-op, got %+v", fr.calls)
	}
	if prov.SupportsOnGrab() {
		t.Error("SupportsOnGrab should be false")
	}
}

func TestSynology_StdoutTreatedAsError(t *testing.T) {
	prov, fr := makeSynology(t, nil)
	fr.stdout = "permission denied"
	msg := notifications.ImportCompleteMessage{Series: &series.Series{Path: "/srv/Show"}}
	err := prov.OnImportComplete(t.Context(), msg)
	if !errors.Is(err, synology.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestSynology_Test_NonLinuxReports(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("non-Linux-only test")
	}
	def := &notifications.Notification{
		Implementation: synology.Implementation,
		Settings:       `{}`,
	}
	p, err := synology.New(def, notifications.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 {
		t.Error("expected non-Linux failure")
	}
}

func TestSynology_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(synology.Implementation, synology.ConfigContract) {
		t.Errorf("registry missing %s/%s", synology.Implementation, synology.ConfigContract)
	}
}
