package videofileinforeader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/mediainfoupdater"
)

// CurrentSchemaRevision mirrors C#
// `VideoFileInfoReader.CURRENT_MEDIA_INFO_SCHEMA_REVISION = 14` at
// `VideoFileInfoReader.cs:25`. Stamped onto every freshly-extracted
// [mediainfo.Model.SchemaRevision]. Slice 3.BH already pinned the
// MIN constant on [mediainfoupdater.MinimumSchemaRevision]; both
// values track equal in current C# but the Go-port keeps them as
// separate consts so an upcoming schema bump can move CURRENT
// without immediately invalidating every persisted row.
const CurrentSchemaRevision = 14

// FileSystem narrows the disk-existence check to the one method the
// reader needs. Mirrors C# `IDiskProvider.FileExists(string)` at
// `:43`. Defined locally so tests can stub disk reads.
type FileSystem interface {
	FileExists(path string) bool
}

// Reader ports `VideoFileInfoReader` at `VideoFileInfoReader.cs:19-293`.
// Satisfies [mediainfoupdater.MediaInfoReader] — the boot site swaps
// in a *Reader for the Phase-3-stub `noopMediaInfoReader`.
//
// Goroutine-safe: stateless after construction. Each Run shells out
// to ffprobe; concurrent GetMediaInfo calls spawn concurrent
// ffprobe processes (matches C# FFMpegCore behaviour).
type Reader struct {
	fs     FileSystem
	runner Runner
	log    *slog.Logger
}

// NewReader binds the disk + ffprobe-runner deps. Nil-required-dep
// returns a typed error (mirrors the constructor posture across the
// other Phase-3 services).
func NewReader(fs FileSystem, runner Runner, log *slog.Logger) (*Reader, error) {
	if fs == nil {
		return nil, errors.New("videofileinforeader: NewReader: fs is nil")
	}
	if runner == nil {
		return nil, errors.New("videofileinforeader: NewReader: runner is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Reader{fs: fs, runner: runner, log: log}, nil
}

// Compile-time guard — Reader satisfies MediaInfoReader.
var _ mediainfoupdater.MediaInfoReader = (*Reader)(nil)

// diskExtensions mirrors C# `MediaFileExtensions.DiskExtensions` at
// `MediaFileExtensions.cs:74` — extensions that ffprobe is unable to
// usefully analyse (disk image containers); the C# code returns null
// early at `:48`. Match is case-insensitive.
var diskExtensions = map[string]struct{}{
	".img": {},
	".iso": {},
	".vob": {},
}

// pixelBitDepthRe matches the trailing bit-depth suffix in ffmpeg
// pixel-format names (e.g. "yuv420p10le", "gbrp16be", "p010le"). The
// C# tree consults FFMpegCore's pixel-format DB for component bit
// depths; the Go-port lifts the bit depth out of the canonical
// pixfmt name pattern (\d+)(le|be)$. Names without the suffix
// (yuv420p, nv12, gray etc.) default to 8 — matches the C# fallback
// at `:73` (`?? 8`).
var pixelBitDepthRe = regexp.MustCompile(`(\d+)(le|be)$`)

// GetMediaInfo ports `VideoFileInfoReader.GetMediaInfo` at
// `:42-171`. Returns the parsed Model on success; (nil, nil) when:
//
//  1. the file does not exist on disk (the C# version throws
//     FileNotFoundException — Go-port softens to (nil, nil) so the
//     [mediainfoupdater.UpdateService] caller's nil-result arm
//     fires, matching the IO-error fallthrough used elsewhere in
//     the C# tree),
//  2. the file extension is in [diskExtensions],
//  3. the ffprobe runner errors (catch-all-Exception parity at
//     `:165`),
//  4. the JSON output fails to unmarshal,
//  5. there is no primary video stream (a non-video file passed in).
//
// Errors are logged at debug — never propagate (the
// [mediainfoupdater.MediaInfoReader] doc-comment promises this).
func (r *Reader) GetMediaInfo(ctx context.Context, filename string) (*mediainfo.Model, error) {
	if r == nil {
		return nil, errors.New("videofileinforeader: GetMediaInfo on nil receiver")
	}
	if !r.fs.FileExists(filename) {
		r.log.Debug("videofileinforeader: file does not exist", "path", filename)
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if _, isDisk := diskExtensions[ext]; isDisk {
		r.log.Debug("videofileinforeader: disk-image extension — skipping", "path", filename, "ext", ext)
		return nil, nil
	}

	r.log.Debug("videofileinforeader: probing", "path", filename)

	// First pass — large-but-not-huge probesize. Mirror `:59`.
	rawJSON, err := r.runProbe(ctx, filename, "50000000", "")
	if err != nil {
		r.log.Debug("videofileinforeader: ffprobe error", "path", filename, "err", err)
		return nil, nil
	}
	analysis := ffprobeAnalysis{}
	if err := json.Unmarshal(rawJSON, &analysis); err != nil {
		r.log.Debug("videofileinforeader: parse error", "path", filename, "err", err)
		return nil, nil
	}

	// Audio-channel-layout retry — `:62-65`. If primary audio's
	// channel layout is empty, re-probe with the bigger window.
	if primaryAudioChannelLayoutEmpty(analysis) {
		retryJSON, err := r.runProbe(ctx, filename, "150000000", "150000000")
		if err == nil {
			retried := ffprobeAnalysis{}
			if err := json.Unmarshal(retryJSON, &retried); err == nil {
				analysis = retried
				rawJSON = retryJSON
			}
		}
	}

	primary := primaryVideoStream(analysis)
	if primary == nil {
		r.log.Debug("videofileinforeader: no video stream", "path", filename)
		return nil, nil
	}

	model := &mediainfo.Model{
		ContainerFormat:              analysis.Format.FormatName,
		VideoFormat:                  primary.CodecName,
		VideoCodecID:                 primary.CodecTagString,
		VideoProfile:                 primary.Profile,
		VideoBitrate:                 streamBitrate(primary),
		VideoBitDepth:                pixelBitDepth(primary.PixelFormat),
		VideoColourPrimaries:         primary.ColorPrimaries,
		VideoTransferCharacteristics: primary.ColorTransfer,
		Height:                       primary.Height,
		Width:                        primary.Width,
		RunTime:                      datastore.STDuration(bestRuntime(primaryAudioDuration(analysis), durationOf(primary), durationOf(&analysis.Format))),
		VideoFps:                     parseFraction(primary.RFrameRate),
		ScanType:                     scanTypeOf(primary.FieldOrder),
		AudioStreams:                 mapAudioStreams(analysis),
		SubtitleStreams:              mapSubtitleStreams(analysis),
		SchemaRevision:               CurrentSchemaRevision,
		RawStreamData:                string(rawJSON),
	}
	if title, ok := analysis.Format.Tags["title"]; ok && title != "" {
		model.Title = title
	}

	// PQ HDR variant detection — `:151-155`. Pull a single frame
	// sample to surface dynamic-metadata side data (mastering /
	// CLL / HDR10+).
	var frameSideData []map[string]json.RawMessage
	if _, isPQ := pqTransferFunctions[primary.ColorTransfer]; isPQ {
		idx := videoStreamIndex(analysis, primary)
		if idx < 0 {
			idx = 0
		}
		framesJSON, err := r.runFrames(ctx, filename, idx)
		if err == nil {
			frames := ffprobeFrames{}
			if err := json.Unmarshal(framesJSON, &frames); err == nil && len(frames.Frames) > 0 {
				frameSideData = frames.Frames[0].SideDataList
			}
		}
	}

	combinedSideData := append([]map[string]json.RawMessage{}, primary.SideDataList...)
	combinedSideData = append(combinedSideData, frameSideData...)
	model.VideoHdrFormat = detectHdrFormat(model.VideoBitDepth, model.VideoColourPrimaries, model.VideoTransferCharacteristics, combinedSideData)

	return model, nil
}

// runProbe executes the standard `-show_format -show_streams`
// invocation. analyzeDuration empty omits the flag. Mirrors `:59`
// and `:64`.
func (r *Reader) runProbe(ctx context.Context, filename, probesize, analyzeDuration string) ([]byte, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-probesize", probesize,
	}
	if analyzeDuration != "" {
		args = append(args, "-analyzeduration", analyzeDuration)
	}
	args = append(args, filename)
	return r.runner.Run(ctx, args...)
}

// runFrames pulls a single frame's side-data via `-show_frames
// -read_intervals "%+#1" -select_streams v:N`. Mirrors `:154`.
func (r *Reader) runFrames(ctx context.Context, filename string, videoStreamIdx int) ([]byte, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_frames",
		"-read_intervals", "%+#1",
		"-select_streams", fmt.Sprintf("v:%d", videoStreamIdx),
		filename,
	}
	return r.runner.Run(ctx, args...)
}

// primaryVideoStream ports `:210-221`. When more than one video
// stream exists, mjpeg/png motion-image streams (cover art /
// thumbnails) are filtered out — preserves the "real" primary.
func primaryVideoStream(a ffprobeAnalysis) *ffprobeStream {
	videos := videoStreams(a)
	if len(videos) == 0 {
		return nil
	}
	if len(videos) == 1 {
		return videos[0]
	}
	for _, s := range videos {
		if s.CodecName != "mjpeg" && s.CodecName != "png" {
			return s
		}
	}
	return videos[0]
}

func videoStreams(a ffprobeAnalysis) []*ffprobeStream {
	out := make([]*ffprobeStream, 0, len(a.Streams))
	for i := range a.Streams {
		if a.Streams[i].CodecType == "video" {
			out = append(out, &a.Streams[i])
		}
	}
	return out
}

// videoStreamIndex returns the position of primary in the
// video-stream-only slice (matches C#
// `analysis.VideoStreams.FindIndex(...)` at `:153`). Returns -1 if
// not found.
func videoStreamIndex(a ffprobeAnalysis, primary *ffprobeStream) int {
	idx := 0
	for i := range a.Streams {
		if a.Streams[i].CodecType != "video" {
			continue
		}
		if a.Streams[i].Index == primary.Index {
			return idx
		}
		idx++
	}
	return -1
}

func primaryAudioStream(a ffprobeAnalysis) *ffprobeStream {
	for i := range a.Streams {
		if a.Streams[i].CodecType == "audio" {
			return &a.Streams[i]
		}
	}
	return nil
}

func primaryAudioChannelLayoutEmpty(a ffprobeAnalysis) bool {
	s := primaryAudioStream(a)
	if s == nil {
		return true
	}
	return strings.TrimSpace(s.ChannelLayout) == ""
}

func primaryAudioDuration(a ffprobeAnalysis) time.Duration {
	s := primaryAudioStream(a)
	if s == nil {
		return 0
	}
	return durationOf(s)
}

// streamBitrate ports `GetBitrate(MediaStream)` at `:195-208`.
// Falls back to the BPS tag when the format-level bit_rate is missing.
func streamBitrate(s *ffprobeStream) int64 {
	if s == nil {
		return 0
	}
	if br := parseInt64(s.BitRate); br > 0 {
		return br
	}
	if bps, ok := s.Tags["BPS"]; ok && strings.TrimSpace(bps) != "" {
		return parseInt64(bps)
	}
	return 0
}

// pixelBitDepth ports `GetPixelFormat(...)?.Components.Min(...)?? 8`
// at `:73`. See [pixelBitDepthRe].
func pixelBitDepth(format string) int {
	if strings.TrimSpace(format) == "" {
		return 8
	}
	m := pixelBitDepthRe.FindStringSubmatch(format)
	if m == nil {
		return 8
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v <= 0 {
		return 8
	}
	return v
}

// scanTypeOf ports `:80-84`.
func scanTypeOf(fieldOrder string) string {
	switch fieldOrder {
	case "tt", "bb", "tb", "bt":
		return "Interlaced"
	default:
		return "Progressive"
	}
}

// durationOf parses a "duration" string from a stream / format into
// a time.Duration. Empty / invalid → 0.
func durationOf(d interface{ getDuration() string }) time.Duration {
	if d == nil {
		return 0
	}
	v := parseFloat(d.getDuration())
	if v <= 0 {
		return 0
	}
	return time.Duration(v * float64(time.Second))
}

func (s *ffprobeStream) getDuration() string { return s.Duration }
func (f *ffprobeFormat) getDuration() string { return f.Duration }

// bestRuntime ports `GetBestRuntime(audio, video, general)` at
// `:180-193`. Prefers video, then audio, falling back to format.
func bestRuntime(audio, video, format time.Duration) time.Duration {
	if video > 0 {
		return video
	}
	if audio > 0 {
		return audio
	}
	return format
}

// mapAudioStreams ports `:87-109`. Result is sorted by Index — the
// C# version uses `OrderBy(stream.Index)`.
func mapAudioStreams(a ffprobeAnalysis) []mediainfo.AudioStream {
	out := []mediainfo.AudioStream{}
	for i := range a.Streams {
		s := &a.Streams[i]
		if s.CodecType != "audio" {
			continue
		}
		lang := streamLanguage(s)
		if lang == "" {
			lang = "und"
		}
		stream := mediainfo.AudioStream{
			Language:         lang,
			Format:           s.CodecName,
			CodecID:          s.CodecTagString,
			Profile:          s.Profile,
			Bitrate:          streamBitrate(s),
			Channels:         s.Channels,
			ChannelPositions: s.ChannelLayout,
		}
		if title, ok := s.Tags["title"]; ok {
			t := strings.TrimSpace(title)
			if t != "" {
				stream.Title = t
			}
		}
		out = append(out, stream)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return audioIndexFor(a, out[i]) < audioIndexFor(a, out[j])
	})
	return out
}

// audioIndexFor finds the original stream index for a mapped audio
// stream — used purely for the OrderBy(Index) re-sort. Linear scan;
// audio-stream counts are typically <10.
func audioIndexFor(a ffprobeAnalysis, m mediainfo.AudioStream) int {
	for i := range a.Streams {
		s := &a.Streams[i]
		if s.CodecType != "audio" {
			continue
		}
		if s.CodecName == m.Format && s.CodecTagString == m.CodecID && s.Channels == m.Channels {
			return s.Index
		}
	}
	return 0
}

// mapSubtitleStreams ports `:111-139`. Filters out streams without
// a language tag (matches C# `Where(stream.Language.IsNotNullOrWhiteSpace())`).
// Disposition int → bool conversion uses != 0 (ffprobe's 0/1 encoding).
func mapSubtitleStreams(a ffprobeAnalysis) []mediainfo.SubtitleStream {
	out := []mediainfo.SubtitleStream{}
	for i := range a.Streams {
		s := &a.Streams[i]
		if s.CodecType != "subtitle" {
			continue
		}
		lang := streamLanguage(s)
		if lang == "" {
			continue
		}
		stream := mediainfo.SubtitleStream{
			Language: lang,
			Format:   s.CodecName,
		}
		if title, ok := s.Tags["title"]; ok {
			t := strings.TrimSpace(title)
			if t != "" {
				stream.Title = t
			}
		}
		if forced, ok := s.Disposition["forced"]; ok {
			b := forced != 0
			stream.Forced = &b
		}
		if hi, ok := s.Disposition["hearing_impaired"]; ok {
			b := hi != 0
			stream.HearingImpaired = &b
		}
		out = append(out, stream)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return subtitleIndexFor(a, out[i]) < subtitleIndexFor(a, out[j])
	})
	return out
}

func subtitleIndexFor(a ffprobeAnalysis, m mediainfo.SubtitleStream) int {
	for i := range a.Streams {
		s := &a.Streams[i]
		if s.CodecType != "subtitle" {
			continue
		}
		if s.CodecName == m.Format && streamLanguage(s) == m.Language {
			return s.Index
		}
	}
	return 0
}

// streamLanguage extracts the stream "language" — ffprobe stores
// it as a stream-level tag, not a top-level field. The C# wrapper
// surfaces it as `stream.Language`; map it back from the tags dict.
func streamLanguage(s *ffprobeStream) string {
	if s == nil {
		return ""
	}
	if l, ok := s.Tags["language"]; ok {
		return strings.TrimSpace(l)
	}
	return ""
}
