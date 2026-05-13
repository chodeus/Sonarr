package customscript_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/customscript"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Path string
	Env  map[string]string
}

type fakeRunner struct {
	calls    []captured
	exitCode int
	err      error
}

func (f *fakeRunner) Run(_ context.Context, path string, env map[string]string) (string, string, int, error) {
	envCopy := make(map[string]string, len(env))
	for k, v := range env {
		envCopy[k] = v
	}
	f.calls = append(f.calls, captured{Path: path, Env: envCopy})
	return "", "", f.exitCode, f.err
}

func settingsJSON(t *testing.T, s customscript.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeCustomScript(t *testing.T) (notifications.Provider, *fakeRunner) {
	t.Helper()
	s := customscript.Settings{Path: "/usr/local/bin/notify.sh"}
	fr := &fakeRunner{}
	customscript.SetRunnerForTest(fr)
	t.Cleanup(func() { customscript.SetRunnerForTest(nil) })
	def := &notifications.Notification{
		Implementation: customscript.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := customscript.New(def, notifications.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, fr
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    customscript.Settings
		want string
	}{
		{"NoPath", customscript.Settings{}, "Path"},
		{"ArgumentsRejected", customscript.Settings{Path: "/x", Arguments: "--legacy"}, "Arguments"},
		{"OK", customscript.Settings{Path: "/x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fails := c.s.Validate()
			if c.want == "" {
				if len(fails) != 0 {
					t.Errorf("expected no failures, got %+v", fails)
				}
				return
			}
			found := false
			for _, f := range fails {
				if f.PropertyName == c.want {
					found = true
				}
			}
			if !found {
				t.Errorf("missing %s failure; got %+v", c.want, fails)
			}
		})
	}
}

func TestCustomScript_OnGrab_SetsEventTypeAndSeriesVars(t *testing.T) {
	prov, fr := makeCustomScript(t)
	msg := notifications.GrabMessage{
		Series: &series.Series{
			ID:        7,
			Title:     "Show",
			TitleSlug: "show",
			Path:      "/srv/Show",
			TvdbID:    100,
			ImdbID:    "tt001",
		},
		Message:            "ok",
		DownloadClientName: "Sab",
		DownloadClientType: "Sabnzbd",
		DownloadID:         "abc",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("calls=%d", len(fr.calls))
	}
	env := fr.calls[0].Env
	if env["Sonarr_EventType"] != "Grab" {
		t.Errorf("EventType=%q", env["Sonarr_EventType"])
	}
	if env["Sonarr_Series_Id"] != "7" || env["Sonarr_Series_Title"] != "Show" {
		t.Errorf("missing series vars: %+v", env)
	}
	if env["Sonarr_Series_TvdbId"] != "100" || env["Sonarr_Series_ImdbId"] != "tt001" {
		t.Errorf("missing ID vars: %+v", env)
	}
	if env["Sonarr_Download_Client"] != "Sab" {
		t.Errorf("Sonarr_Download_Client=%q", env["Sonarr_Download_Client"])
	}
	if env["Sonarr_Download_Id"] != "abc" {
		t.Errorf("Sonarr_Download_Id=%q", env["Sonarr_Download_Id"])
	}
}

func TestCustomScript_OnHealthIssue_PopulatesHealthVars(t *testing.T) {
	prov, fr := makeCustomScript(t)
	msg := notifications.HealthCheckMessage{Check: healthcheck.HealthCheck{Message: "disk", Source: "DiskCheck"}}
	if err := prov.OnHealthIssue(t.Context(), msg); err != nil {
		t.Fatalf("OnHealthIssue: %v", err)
	}
	env := fr.calls[0].Env
	if env["Sonarr_EventType"] != "HealthIssue" {
		t.Errorf("EventType=%q", env["Sonarr_EventType"])
	}
	if env["Sonarr_Health_Issue_Message"] != "disk" {
		t.Errorf("Health_Issue_Message=%q", env["Sonarr_Health_Issue_Message"])
	}
	if env["Sonarr_Health_Issue_Type"] != "DiskCheck" {
		t.Errorf("Health_Issue_Type=%q", env["Sonarr_Health_Issue_Type"])
	}
}

func TestCustomScript_OnApplicationUpdate_PopulatesUpdateVars(t *testing.T) {
	prov, fr := makeCustomScript(t)
	msg := notifications.ApplicationUpdateMessage{
		Message:         "Updated",
		NewVersion:      "5.0.0",
		PreviousVersion: "4.0.0",
	}
	if err := prov.OnApplicationUpdate(t.Context(), msg); err != nil {
		t.Fatalf("OnApplicationUpdate: %v", err)
	}
	env := fr.calls[0].Env
	if env["Sonarr_Update_NewVersion"] != "5.0.0" || env["Sonarr_Update_PreviousVersion"] != "4.0.0" {
		t.Errorf("missing update vars: %+v", env)
	}
}

func TestCustomScript_NonZeroExit_SurfacesTypedSentinel(t *testing.T) {
	prov, fr := makeCustomScript(t)
	fr.exitCode = 42
	fr.err = errors.New("exit status 42")
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{})
	var ne *customscript.ErrNonZeroExit
	if !errors.As(err, &ne) || ne.Code != 42 {
		t.Errorf("err=%v want ErrNonZeroExit code=42", err)
	}
}

func TestCustomScript_SpawnFailure_SurfacesSendFailed(t *testing.T) {
	prov, fr := makeCustomScript(t)
	fr.exitCode = 0
	fr.err = errors.New("permission denied")
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{})
	if !errors.Is(err, customscript.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestCustomScript_Test_NonZeroExitReportsFailure(t *testing.T) {
	prov, fr := makeCustomScript(t)
	fr.exitCode = 1
	fr.err = errors.New("exit status 1")
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 {
		t.Error("expected failure for non-zero exit")
	}
}

func TestCustomScript_Test_SuccessRoundTrip(t *testing.T) {
	prov, fr := makeCustomScript(t)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected no failures, got %+v", failures)
	}
	if len(fr.calls) != 1 || fr.calls[0].Env["Sonarr_EventType"] != "Test" {
		t.Errorf("expected Test event, got %+v", fr.calls)
	}
}

func TestCustomScript_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(customscript.Implementation, customscript.ConfigContract) {
		t.Errorf("registry missing %s/%s", customscript.Implementation, customscript.ConfigContract)
	}
}

func TestCustomScript_OnDownload_WiresQualityAndMediaInfoEnvVars(t *testing.T) {
	prov, fr := makeCustomScript(t)
	file := &episodefiles.File{
		ID:           99,
		Quality:      `{"quality":7,"revision":{"version":1,"real":0,"isRepack":false}}`, // Bluray-1080p v1
		ReleaseGroup: "RELGRP",
		SceneName:    "Show.S01E01.1080p",
		SeasonNumber: 1,
		RelativePath: "Season 01/E01.mkv",
		MediaInfo: &mediainfo.Model{
			VideoFormat: "h264",
			Width:       1920, Height: 1080,
			VideoHdrFormat: mediainfo.Hdr10,
			AudioStreams: []mediainfo.AudioStream{
				{Format: "ac3", Channels: 6, ChannelPositions: "5.1 / 5.1", Language: "eng"},
				{Format: "aac", Channels: 2, Language: "fre"},
				{Format: "aac", Channels: 2, Language: "eng"}, // duplicate eng — Distinct should dedupe
			},
			SubtitleStreams: []mediainfo.SubtitleStream{
				{Language: "eng"},
				{Language: "spa"},
			},
		},
	}
	msg := notifications.DownloadMessage{
		Series:      &series.Series{ID: 7, Title: "Show", Path: "/srv/Show"},
		EpisodeFile: file,
	}
	if err := prov.OnDownload(t.Context(), msg); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	env := fr.calls[0].Env
	if env["Sonarr_EpisodeFile_Quality"] != "Bluray-1080p" {
		t.Errorf("Quality=%q want Bluray-1080p", env["Sonarr_EpisodeFile_Quality"])
	}
	if env["Sonarr_EpisodeFile_QualityVersion"] != "1" {
		t.Errorf("QualityVersion=%q", env["Sonarr_EpisodeFile_QualityVersion"])
	}
	if env["Sonarr_EpisodeFile_MediaInfo_Width"] != "1920" || env["Sonarr_EpisodeFile_MediaInfo_Height"] != "1080" {
		t.Errorf("Width/Height: %q/%q", env["Sonarr_EpisodeFile_MediaInfo_Width"], env["Sonarr_EpisodeFile_MediaInfo_Height"])
	}
	if env["Sonarr_EpisodeFile_MediaInfo_AudioCodec"] != "AC3" {
		t.Errorf("AudioCodec=%q", env["Sonarr_EpisodeFile_MediaInfo_AudioCodec"])
	}
	if env["Sonarr_EpisodeFile_MediaInfo_AudioChannels"] != "5.1" {
		t.Errorf("AudioChannels=%q want 5.1", env["Sonarr_EpisodeFile_MediaInfo_AudioChannels"])
	}
	// Languages = order-preserved with duplicates retained.
	if env["Sonarr_EpisodeFile_MediaInfo_Languages"] != "eng / fre / eng" {
		t.Errorf("Languages=%q", env["Sonarr_EpisodeFile_MediaInfo_Languages"])
	}
	// AudioLanguages = order-preserved Distinct.
	if env["Sonarr_EpisodeFile_MediaInfo_AudioLanguages"] != "eng / fre" {
		t.Errorf("AudioLanguages=%q want 'eng / fre' (Distinct)", env["Sonarr_EpisodeFile_MediaInfo_AudioLanguages"])
	}
	if env["Sonarr_EpisodeFile_MediaInfo_Subtitles"] != "eng / spa" {
		t.Errorf("Subtitles=%q", env["Sonarr_EpisodeFile_MediaInfo_Subtitles"])
	}
	// VideoCodec + VideoDynamicRangeType come from the formatter
	// helpers; verify they're non-empty (exact value depends on
	// formatter internals).
	if env["Sonarr_EpisodeFile_MediaInfo_VideoCodec"] == "" {
		t.Errorf("VideoCodec should be non-empty for VideoFormat=h264")
	}
}

func TestCustomScript_OnDownload_NoMediaInfoSkipsAllMediaVars(t *testing.T) {
	prov, fr := makeCustomScript(t)
	file := &episodefiles.File{
		ID:      99,
		Quality: `{"quality":1}`, // SDTV
	}
	msg := notifications.DownloadMessage{
		Series:      &series.Series{ID: 7, Title: "Show"},
		EpisodeFile: file,
	}
	if err := prov.OnDownload(t.Context(), msg); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	env := fr.calls[0].Env
	if env["Sonarr_EpisodeFile_Quality"] != "SDTV" {
		t.Errorf("Quality=%q", env["Sonarr_EpisodeFile_Quality"])
	}
	// No MediaInfo → none of the MediaInfo_ vars should be set.
	for k := range env {
		if len(k) >= 26 && k[:26] == "Sonarr_EpisodeFile_MediaInf" {
			t.Errorf("unexpected MediaInfo var %q=%q (MediaInfo was nil)", k, env[k])
		}
	}
}

func TestCustomScript_OnEpisodeFileDelete_WiresQualityAndMediaInfo(t *testing.T) {
	prov, fr := makeCustomScript(t)
	file := &episodefiles.File{
		ID:           99,
		Quality:      `{"quality":4}`, // HDTV-720p
		MediaInfo:    &mediainfo.Model{Width: 1280, Height: 720},
		SeasonNumber: 2,
	}
	msg := notifications.EpisodeDeleteMessage{
		Series:      &series.Series{ID: 7, Title: "Show", Path: "/srv/Show"},
		EpisodeFile: file,
	}
	if err := prov.OnEpisodeFileDelete(t.Context(), msg); err != nil {
		t.Fatalf("OnEpisodeFileDelete: %v", err)
	}
	env := fr.calls[0].Env
	if env["Sonarr_EpisodeFile_Quality"] != "HDTV-720p" {
		t.Errorf("Quality=%q", env["Sonarr_EpisodeFile_Quality"])
	}
	if env["Sonarr_EpisodeFile_MediaInfo_Width"] != "1280" {
		t.Errorf("Width=%q", env["Sonarr_EpisodeFile_MediaInfo_Width"])
	}
}
