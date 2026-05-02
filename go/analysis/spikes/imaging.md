# Spike: Imaging via `github.com/disintegration/imaging`

**Status:** ✅ green — recommended library for the Go port's `MediaCover` resize pipeline.

## Question

Sonarr's C# `MediaCover` subsystem uses **SixLabors.ImageSharp** for the one and only image operation it performs: load a JPEG/PNG poster from disk, resize it to a fixed pixel height (preserving aspect ratio), and re-save. The Go port wants a pure-Go imaging library — no cgo, consistent with the `modernc.org/sqlite` decision — that covers this surface without dragging in a libvips dependency. Does one exist?

## Surface Sonarr exercises

From `src/NzbDrone.Core/MediaCover/ImageResizer.cs` and `MediaCoverService.cs`, plus the MediaCover section of `subsystems/http-instrumentation-misc.md` § 8:

- **Decode**: `Image.Load(source)` — a single call, format auto-detected. Sonarr serves only JPEG (`.jpg`) and PNG (`.png` for `Clearlogo` only) per `MediaCoverService.GetExtension` (`MediaCoverService.cs:221-231`).
- **Resize**: `image.Mutate(x => x.Resize(0, height))` (`ImageResizer.cs:46`). Width `0` means "preserve aspect ratio". Heights driven by `EnsureResizedCovers` (`MediaCoverService.cs:176-219`): Poster/Headshot {500, 250}, Banner {70, 35}, Fanart/Screenshot {360, 180}.
- **Encode**: `image.Save(destination)` — format inferred from destination extension. Output mirrors input (jpg in / jpg out, png in / png out).
- **JPEG quality**: configured globally to 92 via `JpegEncoder { Quality = 92 }` (`ImageResizer.cs:30-33`).
- **Memory tuning**: `SimpleGcMemoryAllocator` to keep RSS down on long-running daemons (`ImageResizer.cs:27`).
- **No EXIF orientation handling**, no color-profile management, no animated GIF, no metadata preservation, no transparency tweaks. Source covers come from TheTVDB Skyhook — well-formed JPEG/PNG with no exotic surface.

In short: load, resize-to-height (lanczos-quality), save. That's the whole library footprint.

## Candidate libraries

| Library | cgo? | Maintained | JPEG/PNG decode+encode | Resize | Notes |
| --- | --- | --- | --- | --- | --- |
| `github.com/disintegration/imaging` | no | repo last touched 2021, but API is feature-complete and in heavy downstream use; bug surface is tiny | yes (uses `image/jpeg`, `image/png`) | yes — `imaging.Resize`, `imaging.Fit`, `imaging.Thumbnail`, multiple resampling kernels (`Lanczos`, `CatmullRom`, `Linear`) | Simple top-level API. Quality knob via `imaging.JPEGQuality(92)` encode option. |
| `github.com/h2non/bimg` | **yes** (libvips) | active | yes | yes, fast | Disqualified — cgo violates the no-cgo principle from the sqlite spike. |
| `golang.org/x/image` | no | active (Go team) | yes | yes (`golang.org/x/image/draw`, `BiLinear`/`CatmullRom`/`ApproxBiLinear` scalers) | Lower-level. Workable but you write the resize-preserving-aspect ratio math yourself. Good fallback if `disintegration/imaging` ever rots. |
| `github.com/anthonynsimon/bild` | no | active | yes | yes (`transform.Resize`) | Fine alternative, broader scope (filters, segmentation) we don't need. Slightly larger dependency surface. |

## Verdict

`github.com/disintegration/imaging` is the closest 1:1 mapping to ImageSharp's `Image.Load` → `Resize(0, height)` → `Save` flow. The Go port's `mediacover.Resize(src, dst, height)` becomes:

```
img, err := imaging.Open(src)             // auto-detects JPEG/PNG
if err != nil { … }
resized := imaging.Resize(img, 0, height, imaging.Lanczos)  // width=0 preserves aspect ratio
err = imaging.Save(resized, dst, imaging.JPEGQuality(92))   // ext drives format
```

That covers every method ImageSharp is asked to do today. No EXIF, no color-profile, no animated-image surface to worry about — confirmed by reading the C# call sites end to end.

The repo's last commit being 2021 is the only yellow flag. It's offset by:
- API stability — no version churn risk in Sonarr's vendoring.
- `golang.org/x/image` is a drop-in replacement at the primitive level if we ever need it; the wrapper in `internal/mediacover/resizer.go` will be 30 lines.
- The library has no external dependencies beyond stdlib.

## Notes for the porter

- **Recommended import:** `github.com/disintegration/imaging` v1.6.2.
- **Resampling kernel:** use `imaging.Lanczos` to match ImageSharp's default high-quality resize (visually indistinguishable for thumbnail-scale outputs). `imaging.CatmullRom` is a faster alternative if profiling shows resize is hot.
- **Concurrency cap:** keep the C# `_semaphore = ProcessorCount/2` pattern (`MediaCoverService.cs:42`) in the Go port — a buffered channel of size `runtime.NumCPU()/2` around the resize call. ImageSharp's "slow on ARM" rationale (no SIMD on mono) doesn't apply, but pure-Go decode+resize is still CPU-bound and a library scan can otherwise saturate cores.
- **PNG path:** Clearlogo is the only PNG. `imaging.Save` picks the encoder from the extension, so no special-casing needed.
- **Resize-failure cleanup:** the C# code deletes a half-written destination on exception (`ImageResizer.cs:51-55`). Mirror this — `imaging.Save` writes via `os.Create` so a panic mid-encode could leave a 0-byte file, and the static-file mapper has a fallback to full-size only if the resized variant matches the regex. Cleanest: write to `dst+".tmp"`, rename on success.
- **`_enabled = true` constant:** the dead feature flag at `ImageResizer.cs:22-24` (noted as MC-Q5) — drop it during the port, no Go-side equivalent needed.

## Open questions

- **IMG-Q1**: Should the Go port pre-render all sizes on `SeriesUpdatedEvent` (mirror C#) or lazy-render on first request? MC-Q4 in the MediaCover card raises this; the imaging library doesn't constrain the answer either way. Decide during 0.6 implementation, not now.
- **IMG-Q2**: JPEG quality 92 produces output bytes that are *encoder-implementation-dependent* — `image/jpeg` in stdlib is not byte-equivalent to `JpegEncoder` in ImageSharp. Existing on-disk thumbnails will look identical but won't have matching SHA. Acceptable, since `MediaCoverService` regenerates on demand and there's no integrity check, but worth flagging in the migration runbook so users don't see a one-time disk churn and panic.
