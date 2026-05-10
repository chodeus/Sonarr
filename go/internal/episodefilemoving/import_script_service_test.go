package episodefilemoving_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

type stubScriptCfg struct {
	use      bool
	path     string
	appURL   string
	instance string
}

func (s stubScriptCfg) UseScriptImport() bool    { return s.use }
func (s stubScriptCfg) ScriptImportPath() string { return s.path }
func (s stubScriptCfg) ApplicationUrl() string   { return s.appURL }
func (s stubScriptCfg) InstanceName() string     { return s.instance }

type stubTags struct{ labels map[int]string }

func (s stubTags) GetByID(_ context.Context, id int) (string, bool) {
	if s.labels == nil {
		return "", false
	}
	l, ok := s.labels[id]
	return l, ok
}

type stubExister struct{ exists map[string]bool }

func (s stubExister) FileExists(p string) bool {
	if s.exists == nil {
		return true // default: exist
	}
	return s.exists[p]
}

type stubMediaReader struct {
	model *mediainfo.Model
	err   error
}

func (s stubMediaReader) GetMediaInfo(_ context.Context, _ string) (*mediainfo.Model, error) {
	return s.model, s.err
}

type stubProc struct {
	calls   int
	stdout  string
	exit    int
	err     error
	gotEnv  map[string]string
	gotArgs []string
	gotExe  string
}

func (s *stubProc) StartAndCapture(_ context.Context, exe string, args []string, env map[string]string) (string, int, error) {
	s.calls++
	s.gotExe = exe
	s.gotArgs = append([]string(nil), args...)
	s.gotEnv = env
	return s.stdout, s.exit, s.err
}

func newScriptSvc(t *testing.T, cfg episodefilemoving.ImportScriptConfigReader, instance episodefilemoving.InstanceNameReader, tags episodefilemoving.TagLabelReader, disk episodefilemoving.FileExister, media episodefilemoving.MediaInfoReader, proc episodefilemoving.ProcessRunner) *episodefilemoving.ImportScriptService {
	t.Helper()
	svc, err := episodefilemoving.NewImportScriptService(cfg, instance, tags, disk, media, proc, nil)
	if err != nil {
		t.Fatalf("NewImportScriptService: %v", err)
	}
	return svc
}

func basicLE() *episodeimport.LocalEpisode {
	return &episodeimport.LocalEpisode{
		Series: &series.Series{
			ID:               7,
			Title:            "Show",
			TitleSlug:        "show",
			Path:             "/tv/Show",
			TvdbID:           1234,
			TvMazeID:         5678,
			TmdbID:           9999,
			ImdbID:           "tt0000001",
			SeriesType:       0,
			OriginalLanguage: 1,
			Genres:           "Drama|Sci-Fi",
			Tags:             []int{1, 2},
		},
		Episodes: []episodes.Episode{
			{ID: 11, EpisodeNumber: 1, SeasonNumber: 2, Title: "Pilot", AirDate: "2025-01-01"},
			{ID: 12, EpisodeNumber: 2, SeasonNumber: 2, Title: "Two", AirDate: "2025-01-08"},
		},
		Quality: quality.Model{
			Quality:  quality.HDTV720p,
			Revision: quality.Revision{Version: 1},
		},
		ReleaseGroup:      "GRP",
		SceneName:         "show.s02e01",
		CustomFormats:     []customformats.CustomFormat{{Name: "X264"}},
		CustomFormatScore: 50,
	}
}

func basicFile() *episodefiles.File {
	return &episodefiles.File{ID: 33, RelativePath: "Season 2/old.mkv"}
}

func TestNewImportScriptService_NilGuards(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{}
	tags := stubTags{}
	disk := stubExister{}
	media := stubMediaReader{}
	proc := &stubProc{}
	if _, err := episodefilemoving.NewImportScriptService(nil, cfg, tags, disk, media, proc, nil); err == nil {
		t.Error("nil cfg")
	}
	if _, err := episodefilemoving.NewImportScriptService(cfg, nil, tags, disk, media, proc, nil); err == nil {
		t.Error("nil instance")
	}
	if _, err := episodefilemoving.NewImportScriptService(cfg, cfg, nil, disk, media, proc, nil); err == nil {
		t.Error("nil tags")
	}
	if _, err := episodefilemoving.NewImportScriptService(cfg, cfg, tags, nil, media, proc, nil); err == nil {
		t.Error("nil disk")
	}
	if _, err := episodefilemoving.NewImportScriptService(cfg, cfg, tags, disk, nil, proc, nil); err == nil {
		t.Error("nil media")
	}
	if _, err := episodefilemoving.NewImportScriptService(cfg, cfg, tags, disk, media, nil, nil); err == nil {
		t.Error("nil proc")
	}
}

func TestTryImport_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *episodefilemoving.ImportScriptService
	got := s.TryImport("a", "b", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("nil receiver: got %v want DeferMove", got)
	}
}

func TestTryImport_NilLE(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/x"}
	proc := &stubProc{}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("a", "b", nil, basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("nil le: got %v", got)
	}
	if proc.calls != 0 {
		t.Errorf("nil le: proc invoked")
	}
}

func TestTryImport_UseScriptImportFalse(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: false, path: "/x"}
	proc := &stubProc{}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/out", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("disabled: got %v", got)
	}
	if proc.calls != 0 {
		t.Errorf("disabled: proc invoked")
	}
}

func TestTryImport_EmptyScriptPath(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "  "}
	proc := &stubProc{}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/out", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("empty path: got %v", got)
	}
	if proc.calls != 0 {
		t.Errorf("empty path: proc invoked")
	}
}

func TestTryImport_NilSeries(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/x"}
	proc := &stubProc{}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	le := basicLE()
	le.Series = nil
	got := svc.TryImport("/in", "/out", le, basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("nil series: got %v", got)
	}
}

func TestTryImport_ProcInvocationError(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{err: errors.New("exec failed")}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/out", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("exec err: got %v", got)
	}
}

func TestTryImport_NonZeroExit(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{exit: 17, stdout: "[MoveStatus]MoveComplete"}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/out", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("non-zero exit: got %v", got)
	}
}

func TestTryImport_MoveCompleteDefault(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script", instance: "Sonarr-Test"}
	proc := &stubProc{stdout: ""}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	le := basicLE()
	file := basicFile()
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", le, file, episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportMoveComplete {
		t.Errorf("default: got %v", got)
	}
	if !le.ScriptImported {
		t.Errorf("ScriptImported should be true")
	}
	if !le.ShouldImportExtras {
		t.Errorf("ShouldImportExtras default true")
	}
}

func TestTryImport_DeferMoveSkipsScriptImported(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{stdout: "[MoveStatus]DeferMove"}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	le := basicLE()
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", le, basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("got %v", got)
	}
	if le.ScriptImported {
		t.Errorf("DeferMove: ScriptImported should remain false")
	}
}

func TestTryImport_RenameRequestedTriggersMediaProbe(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	mi := &mediainfo.Model{SchemaRevision: 14, Width: 1920}
	media := stubMediaReader{model: mi}
	proc := &stubProc{stdout: "[MoveStatus]RenameRequested"}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, media, proc)
	le := basicLE()
	file := basicFile()
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", le, file, episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportRenameRequested {
		t.Errorf("got %v", got)
	}
	if file.MediaInfo == nil || file.MediaInfo.Width != 1920 {
		t.Errorf("media info should be stamped on file: %+v", file.MediaInfo)
	}
}

func TestTryImport_MultipleMediaFilesParseError(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{stdout: "[MediaFile] /tmp/a.mkv\n[MediaFile] /tmp/b.mkv\n"}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("multiple media: got %v want DeferMove", got)
	}
}

func TestTryImport_InvalidMediaExtension(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{stdout: "[MediaFile] /tmp/x.txt\n"}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("bad ext: got %v want DeferMove", got)
	}
}

func TestTryImport_NonexistentMediaFile(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{stdout: "[MediaFile] /tmp/x.mkv\n"}
	disk := stubExister{exists: map[string]bool{"/tmp/x.mkv": false}}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, disk, stubMediaReader{}, proc)
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportDeferMove {
		t.Errorf("missing media: got %v", got)
	}
}

func TestTryImport_PreventExtraImport(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{stdout: "[PreventExtraImport]\n[MoveStatus]MoveComplete\n"}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	le := basicLE()
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", le, basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportMoveComplete {
		t.Errorf("got %v", got)
	}
	if le.ShouldImportExtras {
		t.Errorf("PreventExtraImport: ShouldImportExtras should be false")
	}
}

func TestTryImport_MediaFileSetsRelativePath(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	mediaPath := "/tv/Show/Season 2/script.mkv"
	proc := &stubProc{stdout: "[MediaFile]" + mediaPath + "\n[MoveStatus]MoveComplete\n"}
	disk := stubExister{exists: map[string]bool{mediaPath: true}}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, disk, stubMediaReader{}, proc)
	le := basicLE()
	file := basicFile()
	got := svc.TryImport("/in", "/tv/Show/Season 2/old.mkv", le, file, episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportMoveComplete {
		t.Errorf("got %v", got)
	}
	if file.RelativePath != "Season 2/script.mkv" {
		t.Errorf("relative path: %q", file.RelativePath)
	}
}

func TestTryImport_ExtraFilesCollected(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script"}
	proc := &stubProc{stdout: "[ExtraFile] /tmp/extra.srt\n[MoveStatus]MoveComplete\n"}
	disk := stubExister{exists: map[string]bool{"/tmp/extra.srt": true}}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, disk, stubMediaReader{}, proc)
	le := basicLE()
	got := svc.TryImport("/in", "/tv/Show/Season 2/new.mkv", le, basicFile(), episodefilemoving.TransferModeMove)
	if got != episodefilemoving.ScriptImportMoveComplete {
		t.Errorf("got %v", got)
	}
	if len(le.PossibleExtraFiles) != 1 || le.PossibleExtraFiles[0] != "/tmp/extra.srt" {
		t.Errorf("extras: %+v", le.PossibleExtraFiles)
	}
}

func TestTryImport_EnvironmentSnapshot(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/script", appURL: "http://localhost:8989", instance: "Sonarr-X"}
	tags := stubTags{labels: map[int]string{1: "fav", 2: "kids"}}
	disk := stubExister{}
	proc := &stubProc{stdout: ""}
	svc := newScriptSvc(t, cfg, cfg, tags, disk, stubMediaReader{}, proc)
	le := basicLE()
	mi := &mediainfo.Model{
		Width: 1920, Height: 1080,
		AudioStreams: []mediainfo.AudioStream{
			{Format: "aac", Language: "eng", Channels: 2, ChannelPositions: "stereo"},
			{Format: "ac3", Language: "spa", Channels: 6},
		},
		SubtitleStreams: []mediainfo.SubtitleStream{{Format: "srt", Language: "eng"}},
	}
	le.MediaInfo = mi
	le.DownloadItem = &downloadclients.Item{
		DownloadID:         "ABCD",
		DownloadClientInfo: &downloadclients.ItemClientInfo{Name: "qbit", Type: "torrent"},
	}
	air := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	le.Episodes[0].AirDateUtc = &air

	svc.TryImport("/in/dl.mkv", "/tv/Show/Season 2/new.mkv", le, basicFile(), episodefilemoving.TransferModeHardLinkOrCopy)

	if proc.calls != 1 {
		t.Fatalf("calls=%d", proc.calls)
	}
	env := proc.gotEnv
	checks := []struct{ k, want string }{
		{"Sonarr_SourcePath", "/in/dl.mkv"},
		{"Sonarr_DestinationPath", "/tv/Show/Season 2/new.mkv"},
		{"Sonarr_InstanceName", "Sonarr-X"},
		{"Sonarr_ApplicationUrl", "http://localhost:8989"},
		{"Sonarr_Series_Id", "7"},
		{"Sonarr_Series_Title", "Show"},
		{"Sonarr_Series_TitleSlug", "show"},
		{"Sonarr_Series_Path", "/tv/Show"},
		{"Sonarr_Series_TvdbId", "1234"},
		{"Sonarr_Series_TvMazeId", "5678"},
		{"Sonarr_Series_TmdbId", "9999"},
		{"Sonarr_Series_ImdbId", "tt0000001"},
		{"Sonarr_Series_Type", "Standard"},
		{"Sonarr_Series_Genres", "Drama|Sci-Fi"},
		{"Sonarr_EpisodeFile_EpisodeCount", "2"},
		{"Sonarr_EpisodeFile_EpisodeIds", "11,12"},
		{"Sonarr_EpisodeFile_SeasonNumber", "2"},
		{"Sonarr_EpisodeFile_EpisodeNumbers", "1,2"},
		{"Sonarr_EpisodeFile_EpisodeTitles", "Pilot|Two"},
		{"Sonarr_EpisodeFile_ReleaseGroup", "GRP"},
		{"Sonarr_EpisodeFile_SceneName", "show.s02e01"},
		{"Sonarr_EpisodeFile_CustomFormat", "X264"},
		{"Sonarr_EpisodeFile_CustomFormatScore", "50"},
		{"Sonarr_Download_Client", "qbit"},
		{"Sonarr_Download_Client_Type", "torrent"},
		{"Sonarr_Download_Id", "ABCD"},
		{"Sonarr_EpisodeFile_MediaInfo_Height", "1080"},
		{"Sonarr_EpisodeFile_MediaInfo_Width", "1920"},
	}
	for _, c := range checks {
		if got := env[c.k]; got != c.want {
			t.Errorf("env[%q] = %q want %q", c.k, got, c.want)
		}
	}
	// Tags by tag-ID lookup.
	if env["Sonarr_Series_Tags"] != "fav|kids" {
		t.Errorf("tags env: %q", env["Sonarr_Series_Tags"])
	}
	// Distinct + non-distinct languages.
	if env["Sonarr_EpisodeFile_MediaInfo_AudioLanguages"] != "eng / spa" {
		t.Errorf("audio langs distinct: %q", env["Sonarr_EpisodeFile_MediaInfo_AudioLanguages"])
	}
	if env["Sonarr_EpisodeFile_MediaInfo_Subtitles"] != "eng" {
		t.Errorf("subs: %q", env["Sonarr_EpisodeFile_MediaInfo_Subtitles"])
	}
	// AirDateUtc — UTC-Z formatted.
	if !strings.Contains(env["Sonarr_EpisodeFile_EpisodeAirDatesUtc"], "2025-01-01T12:00:00Z") {
		t.Errorf("airdate utc env: %q", env["Sonarr_EpisodeFile_EpisodeAirDatesUtc"])
	}
}

func TestTryImport_ScriptArgsAndExe(t *testing.T) {
	t.Parallel()
	cfg := stubScriptCfg{use: true, path: "/usr/local/bin/import.sh"}
	proc := &stubProc{stdout: ""}
	svc := newScriptSvc(t, cfg, cfg, stubTags{}, stubExister{}, stubMediaReader{}, proc)
	svc.TryImport("/in/file.mkv", "/tv/Show/Season 2/dest.mkv", basicLE(), basicFile(), episodefilemoving.TransferModeMove)
	if proc.gotExe != "/usr/local/bin/import.sh" {
		t.Errorf("exe: %q", proc.gotExe)
	}
	if len(proc.gotArgs) != 2 || proc.gotArgs[0] != "/in/file.mkv" || proc.gotArgs[1] != "/tv/Show/Season 2/dest.mkv" {
		t.Errorf("args: %+v", proc.gotArgs)
	}
}

func TestImportScriptService_SatisfiesImportScriptRunner(t *testing.T) {
	t.Parallel()
	var _ episodefilemoving.ImportScriptRunner = (*episodefilemoving.ImportScriptService)(nil)
}
