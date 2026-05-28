package mediacover_test

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/mediacover"
)

func writeTestImage(t *testing.T, path string, w, h int, asPNG bool) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if asPNG {
		if err := png.Encode(f, img); err != nil {
			t.Fatalf("encode png: %v", err)
		}
		return
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
}

func decodedSize(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode config %s: %v", path, err)
	}
	return cfg.Width, cfg.Height
}

func TestImageResizer_PreservesAspectAndHeight(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.jpg")
	dst := filepath.Join(dir, "poster-250.jpg")
	writeTestImage(t, src, 200, 100, false) // 2:1

	if err := mediacover.NewImageResizer().Resize(src, dst, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	w, h := decodedSize(t, dst)
	if h != 50 {
		t.Errorf("height: got %d want 50", h)
	}
	if w != 100 { // 2:1 aspect → 100x50
		t.Errorf("width: got %d want 100 (aspect preserved)", w)
	}
	// No leftover temp file.
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part temp not cleaned up")
	}
}

func TestImageResizer_PNGForClearlogo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "clearlogo.png")
	dst := filepath.Join(dir, "clearlogo-250.png")
	writeTestImage(t, src, 300, 150, true)
	if err := mediacover.NewImageResizer().Resize(src, dst, 75); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	// Decodes as PNG (the .png destination path selects the PNG encoder).
	f, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, format, err := image.Decode(f); err != nil || format != "png" {
		t.Errorf("expected png output; got format=%q err=%v", format, err)
	}
}

func TestImageResizer_MissingSourceErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := mediacover.NewImageResizer().Resize(filepath.Join(dir, "nope.jpg"), filepath.Join(dir, "out.jpg"), 50)
	if err == nil {
		t.Fatal("expected error on missing source")
	}
}
