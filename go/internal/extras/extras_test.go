package extras

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestIsSubtitleExtension(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".srt", "srt", ".SRT", ".ASS", ".vtt", ".sub", ".utf-8"} {
		if !IsSubtitleExtension(ext) {
			t.Errorf("IsSubtitleExtension(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{".mkv", ".nfo", ".jpg", "", "."} {
		if IsSubtitleExtension(ext) {
			t.Errorf("IsSubtitleExtension(%q) = true, want false", ext)
		}
	}
}

func TestParseExtraFileExtensions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"srt", []string{".srt"}},
		{"srt, ass,.nfo", []string{".srt", ".ass", ".nfo"}},
		{" SRT , .ASS ", []string{".srt", ".ass"}},
		{"srt,srt,.srt", []string{".srt"}}, // dedup
		{"", []string{}},
		{" , ,", []string{}},
	}
	for _, c := range cases {
		got := ParseExtraFileExtensions(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseExtraFileExtensions(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseExtraFileExtensions(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// recordingTransferrer captures the last TransferFile call.
type recordingTransferrer struct {
	src, dst string
	mode     TransferMode
	err      error
	calls    int
}

func (r *recordingTransferrer) TransferFile(src, dst string, mode TransferMode) error {
	r.calls++
	r.src, r.dst, r.mode = src, dst, mode
	return r.err
}

// recordingMover records MoveFile calls (shared across the extras tests).
type recordingMover struct {
	moves [][2]string
	err   error
}

func (m *recordingMover) MoveFile(src, dst string) error {
	if m.err != nil {
		return m.err
	}
	m.moves = append(m.moves, [2]string{src, dst})
	return nil
}

func TestImportFile_NamingAndMode(t *testing.T) {
	t.Parallel()
	seriesPath := "/tv/Show"
	epRel := filepath.Join("Season 01", "Show.S01E02.mkv")

	cases := []struct {
		name        string
		suffix, ext string
		readOnly    bool
		hardlinks   bool
		wantRel     string
		wantMode    TransferMode
	}{
		{"other no-suffix move", "", ".nfo", false, false, filepath.Join("Season 01", "Show.S01E02.nfo"), TransferModeMove},
		{"subtitle suffix move", ".en", ".srt", false, false, filepath.Join("Season 01", "Show.S01E02.en.srt"), TransferModeMove},
		{"readonly copy", ".en", ".srt", true, false, filepath.Join("Season 01", "Show.S01E02.en.srt"), TransferModeCopy},
		{"readonly hardlink", "", ".srt", true, true, filepath.Join("Season 01", "Show.S01E02.srt"), TransferModeHardLinkOrCopy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingTransferrer{}
			rel, err := importFile(rec, c.hardlinks, seriesPath, epRel, "/dl/sub.srt", c.suffix, c.ext, c.readOnly)
			if err != nil {
				t.Fatalf("importFile: %v", err)
			}
			if rel != c.wantRel {
				t.Errorf("rel = %q, want %q", rel, c.wantRel)
			}
			if rec.mode != c.wantMode {
				t.Errorf("mode = %v, want %v", rec.mode, c.wantMode)
			}
			if rec.dst != filepath.Join(seriesPath, c.wantRel) {
				t.Errorf("dst = %q, want %q", rec.dst, filepath.Join(seriesPath, c.wantRel))
			}
			if rec.src != "/dl/sub.srt" {
				t.Errorf("src = %q", rec.src)
			}
		})
	}
}

func TestImportFile_NilTransferrer(t *testing.T) {
	t.Parallel()
	if _, err := importFile(nil, false, "/tv/Show", "a.mkv", "/dl/x.srt", "", ".srt", false); err == nil {
		t.Error("expected error on nil transferrer")
	}
}

func TestImportFile_TransferError(t *testing.T) {
	t.Parallel()
	rec := &recordingTransferrer{err: errors.New("disk full")}
	if _, err := importFile(rec, false, "/tv/Show", "a.mkv", "/dl/x.srt", "", ".srt", false); err == nil {
		t.Error("expected transfer error to propagate")
	}
}
