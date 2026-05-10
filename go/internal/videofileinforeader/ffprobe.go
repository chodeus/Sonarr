package videofileinforeader

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ffprobeAnalysis is the top-level shape of `ffprobe -print_format
// json -show_format -show_streams` output. The C# code consumes
// FFMpegCore's strongly-typed [IMediaAnalysis]; this Go port maps
// the raw JSON directly into local types — only the fields the
// reader actually reads are bound.
type ffprobeAnalysis struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

// ffprobeFormat mirrors `analysis.Format`. Tags carry container-
// level metadata; the reader pulls "title" from here for
// [mediainfo.Model.Title].
type ffprobeFormat struct {
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Tags       map[string]string `json:"tags"`
}

// ffprobeStream mirrors `analysis.Streams[i]` for both video and
// audio/subtitle paths. Disposition fields are typed as ints
// because ffprobe encodes booleans as 0/1 ints.
type ffprobeStream struct {
	Index          int                          `json:"index"`
	CodecType      string                       `json:"codec_type"`
	CodecName      string                       `json:"codec_name"`
	CodecTagString string                       `json:"codec_tag_string"`
	Profile        string                       `json:"profile"`
	Width          int                          `json:"width"`
	Height         int                          `json:"height"`
	PixelFormat    string                       `json:"pix_fmt"`
	ColorPrimaries string                       `json:"color_primaries"`
	ColorTransfer  string                       `json:"color_transfer"`
	FieldOrder     string                       `json:"field_order"`
	RFrameRate     string                       `json:"r_frame_rate"`
	Duration       string                       `json:"duration"`
	BitRate        string                       `json:"bit_rate"`
	Channels       int                          `json:"channels"`
	ChannelLayout  string                       `json:"channel_layout"`
	Tags           map[string]string            `json:"tags"`
	Disposition    map[string]int               `json:"disposition"`
	SideDataList   []map[string]json.RawMessage `json:"side_data_list"`
}

// ffprobeFrames is the shape of `ffprobe -show_frames -read_intervals
// "%+#1" -select_streams v:N` output — used for the PQ-HDR variant
// detection at `VideoFileInfoReader.cs:154`.
type ffprobeFrames struct {
	Frames []ffprobeFrame `json:"frames"`
}

type ffprobeFrame struct {
	SideDataList []map[string]json.RawMessage `json:"side_data_list"`
}

// parseFloat is a tolerant strconv.ParseFloat — empty / "N/A" → 0.
func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseInt64 is a tolerant strconv.ParseInt — empty / "N/A" → 0.
func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseFraction parses ffprobe's "num/den" fraction (e.g. r_frame_rate
// "24000/1001"). Empty / "0/0" / unparseable → 0.
func parseFraction(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return parseFloat(s)
	}
	num := parseFloat(parts[0])
	den := parseFloat(parts[1])
	if den == 0 {
		return 0
	}
	return num / den
}
