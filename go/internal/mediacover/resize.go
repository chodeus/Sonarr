package mediacover

import (
	"fmt"
	"image"
	"image/jpeg" // also registers the JPEG decoder for image.Decode
	"image/png"  // also registers the PNG decoder for image.Decode
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

// jpegQuality mirrors the C# `JpegEncoder { Quality = 92 }` set in
// `ImageResizer`'s ctor (`MediaCover/ImageResizer.cs:33-36`) — thumbnails
// don't need full quality.
const jpegQuality = 92

// ImageResizer ports `MediaCover/ImageResizer.cs:15-59` — the
// SixLabors.ImageSharp `image.Resize(0, height)` wrapper. The Go port
// uses golang.org/x/image/draw with the CatmullRom kernel (a
// high-quality cubic filter comparable to ImageSharp's default bicubic
// resampler). Implements [Resizer].
//
// Stateless; safe to share. Construct via [NewImageResizer].
type ImageResizer struct{}

// NewImageResizer returns the production [Resizer].
func NewImageResizer() ImageResizer { return ImageResizer{} }

// Resize reads source, scales it to the target height (width derived to
// preserve aspect ratio — the C# `Resize(0, height)` shape), and writes
// it to destination. Encoding follows the destination extension: `.png`
// → PNG (alpha-preserving, for Clearlogo); everything else → JPEG at
// quality 92.
//
// The write goes through a temp `.part` file + rename so a crash mid-
// encode can't leave a truncated cover. On any failure the temp file +
// any pre-existing destination are removed (mirrors the C# catch arm at
// `:209-218` which deletes the destination on failure) and the error is
// returned for the caller to log.
func (ImageResizer) Resize(source, destination string, height int) (err error) {
	if height <= 0 {
		return fmt.Errorf("mediacover: resize: non-positive height %d", height)
	}
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("mediacover: resize: open source: %w", err)
	}
	img, _, err := image.Decode(src)
	_ = src.Close()
	if err != nil {
		return fmt.Errorf("mediacover: resize: decode source: %w", err)
	}

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return fmt.Errorf("mediacover: resize: empty source bounds")
	}
	// Width auto from the target height, preserving aspect ratio. Round
	// to nearest, clamp to >= 1.
	width := (b.Dx()*height + b.Dy()/2) / b.Dy()
	if width < 1 {
		width = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)

	tmp := destination + ".part"
	// Ensure the parent dir exists (the master download already created
	// it, but be defensive — the resize variant shares the dir).
	if mkErr := os.MkdirAll(filepath.Dir(destination), 0o755); mkErr != nil {
		return fmt.Errorf("mediacover: resize: mkdir: %w", mkErr)
	}
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("mediacover: resize: create temp: %w", err)
	}
	// Cleanup helper for every error path past this point.
	cleanup := func(cause error) error {
		_ = out.Close()
		_ = os.Remove(tmp)
		_ = os.Remove(destination)
		return cause
	}

	if strings.EqualFold(filepath.Ext(destination), ".png") {
		if encErr := png.Encode(out, dst); encErr != nil {
			return cleanup(fmt.Errorf("mediacover: resize: encode png: %w", encErr))
		}
	} else {
		if encErr := jpeg.Encode(out, dst, &jpeg.Options{Quality: jpegQuality}); encErr != nil {
			return cleanup(fmt.Errorf("mediacover: resize: encode jpeg: %w", encErr))
		}
	}
	if closeErr := out.Close(); closeErr != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(destination)
		return fmt.Errorf("mediacover: resize: close temp: %w", closeErr)
	}
	if renErr := os.Rename(tmp, destination); renErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediacover: resize: rename: %w", renErr)
	}
	return nil
}

// Compile-time guard.
var _ Resizer = ImageResizer{}
