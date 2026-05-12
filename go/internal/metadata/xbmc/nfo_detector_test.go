package xbmc_test

import (
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/metadata/xbmc"
)

type fakeNfoFS struct {
	files        map[string]string
	sizeErr      map[string]error
	readErr      map[string]error
	overrideSize map[string]int64
}

func (f *fakeNfoFS) GetFileSize(path string) (int64, error) {
	if err, ok := f.sizeErr[path]; ok {
		return 0, err
	}
	if size, ok := f.overrideSize[path]; ok {
		return size, nil
	}
	body, ok := f.files[path]
	if !ok {
		return 0, errors.New("not found")
	}
	return int64(len(body)), nil
}

func (f *fakeNfoFS) ReadAllText(path string) (string, error) {
	if err, ok := f.readErr[path]; ok {
		return "", err
	}
	body, ok := f.files[path]
	if !ok {
		return "", errors.New("not found")
	}
	return body, nil
}

func TestNewNfoDetector_RejectsNilFS(t *testing.T) {
	t.Parallel()
	if _, err := xbmc.NewNfoDetector(nil); err == nil {
		t.Errorf("expected error on nil fs")
	}
}

func TestNfoDetector_IsXbmcNfoFile_RecognisesAllRoots(t *testing.T) {
	t.Parallel()
	fs := &fakeNfoFS{files: map[string]string{
		"/show/tvshow.nfo":   "<tvshow><title>Foo</title></tvshow>",
		"/show/ep.nfo":       "<episodedetails><title>Pilot</title></episodedetails>",
		"/show/movie.nfo":    "<movie><title>Bar</title></movie>",
		"/show/artist.nfo":   "<artist></artist>",
		"/show/album.nfo":    "<album></album>",
		"/show/musicvid.nfo": "<musicvideo></musicvideo>",
	}}
	d, err := xbmc.NewNfoDetector(fs)
	if err != nil {
		t.Fatalf("NewNfoDetector: %v", err)
	}
	for path := range fs.files {
		if !d.IsXbmcNfoFile(path) {
			t.Errorf("IsXbmcNfoFile(%q) = false, want true", path)
		}
	}
}

func TestNfoDetector_IsXbmcNfoFile_RejectsNonNfoBodies(t *testing.T) {
	t.Parallel()
	// The C# regex is a plain substring match — it does NOT account
	// for XML comments. A file containing `<!-- <tvshow> -->` would
	// be classified as a Kodi NFO by both Sonarr.NET and this port.
	// That's faithful behaviour, not tested for here.
	fs := &fakeNfoFS{files: map[string]string{
		"/random.txt": "hello world",
		"/empty.nfo":  "",
		"/wrong.nfo":  "<other><title>Foo</title></other>",
	}}
	d, _ := xbmc.NewNfoDetector(fs)
	for path := range fs.files {
		if d.IsXbmcNfoFile(path) {
			t.Errorf("IsXbmcNfoFile(%q) = true, want false", path)
		}
	}
}

func TestNfoDetector_IsXbmcNfoFile_RejectsOversizedFiles(t *testing.T) {
	t.Parallel()
	// Use override size to fake an 11MB file without allocating one.
	fs := &fakeNfoFS{
		files:        map[string]string{"/big.nfo": "<tvshow></tvshow>"},
		overrideSize: map[string]int64{"/big.nfo": 11 * 1024 * 1024},
	}
	d, _ := xbmc.NewNfoDetector(fs)
	if d.IsXbmcNfoFile("/big.nfo") {
		t.Errorf("oversized file: IsXbmcNfoFile() = true, want false (bail at >10MB)")
	}

	// Cap is inclusive of 10MB exactly — verify boundary.
	exact := strings.Repeat("a", 10*1024*1024-len("<tvshow></tvshow>")) + "<tvshow></tvshow>"
	fs2 := &fakeNfoFS{files: map[string]string{"/edge.nfo": exact}}
	d2, _ := xbmc.NewNfoDetector(fs2)
	if !d2.IsXbmcNfoFile("/edge.nfo") {
		t.Errorf("file exactly 10MB: IsXbmcNfoFile() = false, want true")
	}
}

func TestNfoDetector_IsXbmcNfoFile_HandlesIOErrors(t *testing.T) {
	t.Parallel()
	fs := &fakeNfoFS{
		files: map[string]string{
			"/size-err.nfo": "<tvshow></tvshow>",
			"/read-err.nfo": "<tvshow></tvshow>",
		},
		sizeErr: map[string]error{"/size-err.nfo": errors.New("permission denied")},
		readErr: map[string]error{"/read-err.nfo": errors.New("permission denied")},
	}
	d, _ := xbmc.NewNfoDetector(fs)
	if d.IsXbmcNfoFile("/size-err.nfo") {
		t.Errorf("GetFileSize error: IsXbmcNfoFile() = true, want false")
	}
	if d.IsXbmcNfoFile("/read-err.nfo") {
		t.Errorf("ReadAllText error: IsXbmcNfoFile() = true, want false")
	}
}

func TestNfoDetector_NilReceiver(t *testing.T) {
	t.Parallel()
	var d *xbmc.NfoDetector
	if d.IsXbmcNfoFile("anything") {
		t.Errorf("nil receiver: IsXbmcNfoFile() = true, want false")
	}
}
