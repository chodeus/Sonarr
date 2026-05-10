package videofileinforeader_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/videofileinforeader"
)

type stubFS struct{ exists map[string]bool }

func (s stubFS) FileExists(p string) bool {
	if s.exists == nil {
		return true
	}
	return s.exists[p]
}

type stubRunner struct {
	calls int
	err   error
	// payload[i] is the JSON to return on the (i+1)-th call. Falls
	// back to last entry if exhausted.
	payload [][]byte
	args    [][]string
}

func (s *stubRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	s.calls++
	s.args = append(s.args, append([]string(nil), args...))
	if s.err != nil {
		return nil, s.err
	}
	if len(s.payload) == 0 {
		return []byte(`{}`), nil
	}
	if s.calls-1 < len(s.payload) {
		return s.payload[s.calls-1], nil
	}
	return s.payload[len(s.payload)-1], nil
}

func newReader(t *testing.T, fs videofileinforeader.FileSystem, runner videofileinforeader.Runner) *videofileinforeader.Reader {
	t.Helper()
	r, err := videofileinforeader.NewReader(fs, runner, nil)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}

func TestNewReader_NilFS(t *testing.T) {
	t.Parallel()
	if _, err := videofileinforeader.NewReader(nil, &stubRunner{}, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestNewReader_NilRunner(t *testing.T) {
	t.Parallel()
	if _, err := videofileinforeader.NewReader(stubFS{}, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestGetMediaInfo_NilReceiver(t *testing.T) {
	t.Parallel()
	var r *videofileinforeader.Reader
	m, err := r.GetMediaInfo(t.Context(), "/x")
	if err == nil {
		t.Fatal("want error")
	}
	if m != nil {
		t.Fatal("want nil model")
	}
}

func TestGetMediaInfo_FileMissing(t *testing.T) {
	t.Parallel()
	runner := &stubRunner{}
	r := newReader(t, stubFS{exists: map[string]bool{}}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/nope.mkv")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m != nil {
		t.Fatal("want nil model")
	}
	if runner.calls != 0 {
		t.Errorf("missing file: runner called %d times", runner.calls)
	}
}

func TestGetMediaInfo_DiskExtensionSkips(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".iso", ".IMG", ".vob"} {
		ext := ext
		t.Run(ext, func(t *testing.T) {
			t.Parallel()
			runner := &stubRunner{}
			r := newReader(t, stubFS{}, runner)
			m, err := r.GetMediaInfo(t.Context(), "/tv/x"+ext)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if m != nil {
				t.Errorf("disk ext %q: want nil model", ext)
			}
			if runner.calls != 0 {
				t.Errorf("disk ext %q: runner called %d times", ext, runner.calls)
			}
		})
	}
}

func TestGetMediaInfo_RunnerErrorReturnsNil(t *testing.T) {
	t.Parallel()
	runner := &stubRunner{err: errors.New("ffprobe not found")}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil {
		t.Fatalf("want nil err, got %v", err)
	}
	if m != nil {
		t.Errorf("runner error: want nil model")
	}
}

func TestGetMediaInfo_BadJSONReturnsNil(t *testing.T) {
	t.Parallel()
	runner := &stubRunner{payload: [][]byte{[]byte(`{not json`)}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m != nil {
		t.Errorf("bad json: want nil model")
	}
}

func TestGetMediaInfo_NoVideoStreamReturnsNil(t *testing.T) {
	t.Parallel()
	runner := &stubRunner{payload: [][]byte{[]byte(`{"format":{"format_name":"matroska"},"streams":[{"codec_type":"audio","codec_name":"aac"}]}`)}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m != nil {
		t.Errorf("no video: want nil model, got %+v", m)
	}
}

func TestGetMediaInfo_BasicFields(t *testing.T) {
	t.Parallel()
	json := []byte(`{
		"format": {
			"format_name": "matroska,webm",
			"duration": "120.5",
			"tags": {"title": "S01E01"}
		},
		"streams": [
			{
				"index": 0,
				"codec_type": "video",
				"codec_name": "h264",
				"codec_tag_string": "avc1",
				"profile": "High",
				"width": 1920,
				"height": 1080,
				"pix_fmt": "yuv420p",
				"r_frame_rate": "24000/1001",
				"field_order": "progressive",
				"duration": "120.0",
				"bit_rate": "5000000"
			},
			{
				"index": 1,
				"codec_type": "audio",
				"codec_name": "aac",
				"codec_tag_string": "mp4a",
				"profile": "LC",
				"channels": 2,
				"channel_layout": "stereo",
				"bit_rate": "128000",
				"tags": {"language": "eng", "title": " Stereo "}
			},
			{
				"index": 2,
				"codec_type": "subtitle",
				"codec_name": "srt",
				"tags": {"language": "eng"},
				"disposition": {"forced": 0, "hearing_impaired": 1}
			}
		]
	}`)
	runner := &stubRunner{payload: [][]byte{json}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m == nil {
		t.Fatal("want model")
	}
	if m.ContainerFormat != "matroska,webm" {
		t.Errorf("container: %q", m.ContainerFormat)
	}
	if m.VideoFormat != "h264" || m.VideoCodecID != "avc1" || m.VideoProfile != "High" {
		t.Errorf("video codec triple: %+v", m)
	}
	if m.Width != 1920 || m.Height != 1080 {
		t.Errorf("dims: %dx%d", m.Width, m.Height)
	}
	if m.VideoBitrate != 5000000 {
		t.Errorf("video bitrate: %d", m.VideoBitrate)
	}
	if m.VideoBitDepth != 8 {
		t.Errorf("bit depth (yuv420p): want 8 got %d", m.VideoBitDepth)
	}
	if got := time.Duration(m.RunTime); got != 120*time.Second {
		t.Errorf("runtime: want 120s got %v", got)
	}
	if m.VideoFps < 23.97 || m.VideoFps > 23.98 {
		t.Errorf("fps: want ~23.976 got %v", m.VideoFps)
	}
	if m.ScanType != "Progressive" {
		t.Errorf("scan: %q", m.ScanType)
	}
	if m.SchemaRevision != videofileinforeader.CurrentSchemaRevision {
		t.Errorf("schema: %d", m.SchemaRevision)
	}
	if m.Title != "S01E01" {
		t.Errorf("title: %q", m.Title)
	}
	if m.RawStreamData == "" {
		t.Errorf("raw stream data should be populated")
	}
	if len(m.AudioStreams) != 1 {
		t.Fatalf("audio streams: %d", len(m.AudioStreams))
	}
	a := m.AudioStreams[0]
	if a.Format != "aac" || a.Channels != 2 || a.ChannelPositions != "stereo" {
		t.Errorf("audio: %+v", a)
	}
	if a.Title != "Stereo" {
		t.Errorf("audio title trimmed: %q", a.Title)
	}
	if a.Language != "eng" {
		t.Errorf("audio lang: %q", a.Language)
	}
	if len(m.SubtitleStreams) != 1 {
		t.Fatalf("sub streams: %d", len(m.SubtitleStreams))
	}
	s := m.SubtitleStreams[0]
	if s.Forced == nil || *s.Forced != false {
		t.Errorf("forced: %v", s.Forced)
	}
	if s.HearingImpaired == nil || *s.HearingImpaired != true {
		t.Errorf("hearing-impaired: %v", s.HearingImpaired)
	}
	_ = datastore.STDuration(0)
}

func TestGetMediaInfo_AudioRetryWhenChannelLayoutMissing(t *testing.T) {
	t.Parallel()
	first := []byte(`{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1280,"height":720,"r_frame_rate":"24/1"},
			{"index":1,"codec_type":"audio","codec_name":"aac","channels":2,"channel_layout":""}
		]
	}`)
	retry := []byte(`{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1280,"height":720,"r_frame_rate":"24/1"},
			{"index":1,"codec_type":"audio","codec_name":"aac","channels":2,"channel_layout":"5.1","tags":{"language":"eng"}}
		]
	}`)
	runner := &stubRunner{payload: [][]byte{first, retry}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m == nil {
		t.Fatal("want model")
	}
	if runner.calls != 2 {
		t.Errorf("expected retry probe; calls=%d", runner.calls)
	}
	if len(m.AudioStreams) != 1 || m.AudioStreams[0].ChannelPositions != "5.1" {
		t.Errorf("retry analysis not used: %+v", m.AudioStreams)
	}
}

func TestGetMediaInfo_PixelBitDepthFromName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fmt string
		bd  int
	}{
		{"yuv420p", 8},
		{"yuv420p10le", 10},
		{"yuv422p12be", 12},
		{"gbrp16le", 16},
		{"p010le", 10},
		{"nv12", 8},
		{"", 8},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.fmt, func(t *testing.T) {
			t.Parallel()
			body := `{"format":{"format_name":"x"},"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","width":1,"height":1,"pix_fmt":"` + tc.fmt + `","r_frame_rate":"1/1"}]}`
			runner := &stubRunner{payload: [][]byte{[]byte(body)}}
			r := newReader(t, stubFS{}, runner)
			m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
			if err != nil || m == nil {
				t.Fatalf("err=%v m=%v", err, m)
			}
			if m.VideoBitDepth != tc.bd {
				t.Errorf("pix_fmt %q: want %d got %d", tc.fmt, tc.bd, m.VideoBitDepth)
			}
		})
	}
}

func TestGetMediaInfo_ScanType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fo   string
		want string
	}{
		{"tt", "Interlaced"},
		{"bb", "Interlaced"},
		{"tb", "Interlaced"},
		{"bt", "Interlaced"},
		{"progressive", "Progressive"},
		{"", "Progressive"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.fo, func(t *testing.T) {
			t.Parallel()
			body := `{"format":{"format_name":"x"},"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","width":1,"height":1,"r_frame_rate":"1/1","field_order":"` + tc.fo + `"}]}`
			runner := &stubRunner{payload: [][]byte{[]byte(body)}}
			r := newReader(t, stubFS{}, runner)
			m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
			if err != nil || m == nil {
				t.Fatalf("err=%v m=%v", err, m)
			}
			if m.ScanType != tc.want {
				t.Errorf("field_order %q: want %q got %q", tc.fo, tc.want, m.ScanType)
			}
		})
	}
}

func TestGetMediaInfo_PrimaryVideoSkipsCoverArt(t *testing.T) {
	t.Parallel()
	body := `{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"mjpeg","width":300,"height":300,"r_frame_rate":"1/1"},
			{"index":1,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"r_frame_rate":"24/1"}
		]
	}`
	runner := &stubRunner{payload: [][]byte{[]byte(body)}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil || m == nil {
		t.Fatalf("err=%v m=%v", err, m)
	}
	if m.VideoFormat != "hevc" {
		t.Errorf("primary video: want hevc got %q", m.VideoFormat)
	}
}

func TestGetMediaInfo_HdrDolbyVisionSdr(t *testing.T) {
	t.Parallel()
	body := `{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"pix_fmt":"yuv420p10le","color_primaries":"bt2020","color_transfer":"smpte2084","r_frame_rate":"24/1",
				"side_data_list":[
					{"side_data_type":"DOVI configuration record","dv_bl_signal_compatibility_id":2}
				]
			}
		]
	}`
	runner := &stubRunner{payload: [][]byte{[]byte(body)}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil || m == nil {
		t.Fatalf("err=%v m=%v", err, m)
	}
	if m.VideoHdrFormat != mediainfo.HdrDolbyVisionSdr {
		t.Errorf("hdr: want DolbyVisionSdr got %v", m.VideoHdrFormat)
	}
}

func TestGetMediaInfo_HdrPq10WithFramesProbe(t *testing.T) {
	t.Parallel()
	streams := []byte(`{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"pix_fmt":"yuv420p10le","color_primaries":"bt2020","color_transfer":"smpte2084","r_frame_rate":"24/1"},
			{"index":1,"codec_type":"audio","codec_name":"aac","channels":2,"channel_layout":"stereo"}
		]
	}`)
	frames := []byte(`{"frames":[{"side_data_list":[{"side_data_type":"Mastering display metadata"}]}]}`)
	runner := &stubRunner{payload: [][]byte{streams, frames}}
	r := newReader(t, stubFS{}, runner)
	m, err := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if err != nil || m == nil {
		t.Fatalf("err=%v m=%v", err, m)
	}
	if m.VideoHdrFormat != mediainfo.Hdr10 {
		t.Errorf("hdr: want Hdr10 (mastering display) got %v", m.VideoHdrFormat)
	}
	if runner.calls < 2 {
		t.Errorf("frames probe should fire for PQ; calls=%d", runner.calls)
	}
}

func TestGetMediaInfo_HdrHlg10(t *testing.T) {
	t.Parallel()
	body := `{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1,"height":1,"pix_fmt":"yuv420p10le","color_primaries":"bt2020","color_transfer":"arib-std-b67","r_frame_rate":"24/1"}
		]
	}`
	runner := &stubRunner{payload: [][]byte{[]byte(body)}}
	r := newReader(t, stubFS{}, runner)
	m, _ := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if m == nil || m.VideoHdrFormat != mediainfo.HdrHlg10 {
		t.Errorf("want Hlg10, got %v", m)
	}
}

func TestGetMediaInfo_StreamBitrateBpsFallback(t *testing.T) {
	t.Parallel()
	body := `{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1,"height":1,"r_frame_rate":"1/1","tags":{"BPS":"1234567"}}
		]
	}`
	runner := &stubRunner{payload: [][]byte{[]byte(body)}}
	r := newReader(t, stubFS{}, runner)
	m, _ := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if m == nil || m.VideoBitrate != 1234567 {
		t.Errorf("BPS fallback: %+v", m)
	}
}

func TestGetMediaInfo_AudioLanguageDefaultUnd(t *testing.T) {
	t.Parallel()
	body := `{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1,"height":1,"r_frame_rate":"1/1"},
			{"index":1,"codec_type":"audio","codec_name":"aac","channels":2,"channel_layout":"stereo"}
		]
	}`
	runner := &stubRunner{payload: [][]byte{[]byte(body)}}
	r := newReader(t, stubFS{}, runner)
	m, _ := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if m == nil || len(m.AudioStreams) != 1 || m.AudioStreams[0].Language != "und" {
		t.Errorf("missing audio lang should default to 'und': %+v", m)
	}
}

func TestGetMediaInfo_SubtitleNoLanguageFiltered(t *testing.T) {
	t.Parallel()
	body := `{
		"format":{"format_name":"matroska"},
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","width":1,"height":1,"r_frame_rate":"1/1"},
			{"index":1,"codec_type":"subtitle","codec_name":"srt"}
		]
	}`
	runner := &stubRunner{payload: [][]byte{[]byte(body)}}
	r := newReader(t, stubFS{}, runner)
	m, _ := r.GetMediaInfo(t.Context(), "/tv/x.mkv")
	if m == nil {
		t.Fatal("want model")
	}
	if len(m.SubtitleStreams) != 0 {
		t.Errorf("subtitle without language should be filtered: %+v", m.SubtitleStreams)
	}
}

func TestExecRunner_NilReceiver(t *testing.T) {
	t.Parallel()
	var r *videofileinforeader.ExecRunner
	if _, err := r.Run(t.Context()); err == nil {
		t.Fatal("want error")
	}
}
