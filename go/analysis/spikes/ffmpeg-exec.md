# Spike: FFprobe via direct `os/exec` (replacing `FFMpegCore`)

**Status:** ✅ green — direct `ffprobe` exec covers every FFMpegCore call site Sonarr uses.

## Question

C# Sonarr depends on `FFMpegCore`, a managed wrapper that itself shells out to `ffprobe` and parses `-print_format json` output into typed structs. The Go port wants to drop that wrapper and call `ffprobe` directly via `os/exec.CommandContext`. Does the raw JSON contain every field `MediaInfoModel` needs, and can a single Go binding cover the three FFMpegCore entry points the C# code uses?

## Surface Sonarr exercises

`FFMpegCore` is imported in exactly **one** C# file: `src/NzbDrone.Core/MediaFiles/MediaInfo/VideoFileInfoReader.cs`. Confirmed by grep across `src/`. The full footprint:

| FFMpegCore call | Where | Replacement |
| --- | --- | --- |
| `GlobalFFOptions.Configure(o => o.BinaryFolder = AppDomain.BaseDirectory)` | `:38` | Go: resolve `ffprobe` path relative to `os.Executable()`'s dir; fall back to `$PATH`. |
| `FFProbe.Analyse(filename, customArguments: "-probesize 50000000")` | `:59` | `ffprobe -v quiet -print_format json -show_format -show_streams -probesize 50000000 <file>` |
| `FFProbe.Analyse(filename, customArguments: "-probesize 150000000 -analyzeduration 150000000")` | `:64` (re-probe when audio channel layout missing) | same as above with the larger probe sizes. |
| `FFProbe.GetFrames(filename, customArguments: "-read_intervals \"%+#1\" -select_streams v:N")` | `:154` (HDR side-data probe) | `ffprobe -v quiet -print_format json -show_frames -read_intervals "%+#1" -select_streams v:N <file>` |
| `FFProbe.TryGetPixelFormat(format, out pixelFormat)` | `:230` | `ffprobe -v quiet -print_format json -show_pix_fmts` once at startup, cache by name → component bit-depths. (Pixel-format list is static per ffprobe binary.) |

`MediaInfoModel.RawStreamData = string.Concat(analysis.OutputData)` (`:85`) — note: `IMediaAnalysis.OutputData` is the raw stdout bytes from the `ffprobe` invocation, captured by FFMpegCore. The Go port can preserve this verbatim by capturing `cmd.Stdout` to a buffer and stuffing it on the model. See **FFM-Q3**.

## Approach

A single Go package `internal/mediainfo/ffprobe` with three entry points:

```
type Probe struct{ BinaryPath string; Logger *slog.Logger }

func (p *Probe) Analyse(ctx context.Context, file string, extraArgs ...string) (*Analysis, error)
func (p *Probe) Frames(ctx context.Context, file string, extraArgs ...string) (*Frames, error)
func (p *Probe) PixelFormats(ctx context.Context) (map[string]PixelFormat, error)  // cached
```

Sample exec invocation (the workhorse `Analyse` call):

```
cmd := exec.CommandContext(ctx,
    p.BinaryPath,
    "-v", "quiet",
    "-print_format", "json",
    "-show_format",
    "-show_streams",
    "-probesize", "50000000",
    file,
)
var stdout, stderr bytes.Buffer
cmd.Stdout = &stdout
cmd.Stderr = &stderr
cmd.SysProcAttr = sysProcAttrKillTreeOnCancel()  // setpgid on unix, JobObject on windows
if err := cmd.Run(); err != nil {
    return nil, fmt.Errorf("ffprobe: %w (stderr: %s)", err, stderr.String())
}
var raw struct{ Streams []rawStream `json:"streams"`; Format rawFormat `json:"format"` }
if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil { … }
```

JSON shape is canonical (ffmpeg.org docs and verifiable on any system ffprobe). The fields the Go port has to map onto `MediaInfoModel`:

| `MediaInfoModel` field | ffprobe JSON path | Notes |
| --- | --- | --- |
| `ContainerFormat` | `format.format_name` | comma-separated list from ffprobe (e.g. `"mov,mp4,m4a,3gp,3g2,mj2"`); store verbatim. |
| `VideoFormat` | `streams[primary_video].codec_name` | "primary_video" = `GetPrimaryVideoStream` logic in `VideoFileInfoReader.cs:210-221`: if >1 video stream, prefer one that isn't `mjpeg`/`png`; else `streams[0]` of `codec_type=="video"`. |
| `VideoCodecID` | `streams[primary_video].codec_tag_string` | |
| `VideoProfile` | `streams[primary_video].profile` | |
| `VideoBitrate` | `streams[primary_video].bit_rate` (string→int64) — fallback to `tags.BPS` if 0/missing | mirrors `GetBitrate` in `VideoFileInfoReader.cs:195-208`. |
| `VideoBitDepth` | derived from `streams[primary_video].pix_fmt` via `-show_pix_fmts` lookup | min of all components' `bit_depth`. Default 8. |
| `VideoColourPrimaries` | `streams[primary_video].color_primaries` | |
| `VideoTransferCharacteristics` | `streams[primary_video].color_transfer` | drives HDR detection. |
| `VideoHdrFormat` | computed — combines bit-depth, primaries, transfer, and side data | logic = `VideoFileInfoReader.GetHdrFormat` (`:233-292`). Port verbatim. |
| `Height` / `Width` | `streams[primary_video].height` / `width` | int. |
| `RunTime` | `GetBestRuntime(audio_dur, video_dur, format_dur)` | logic at `:180-193`. Each duration parsed from `streams[*].duration` or `format.duration` (string seconds). |
| `VideoFps` | `streams[primary_video].r_frame_rate` (or `avg_frame_rate`) — parse `"num/den"` as decimal | C# uses FFMpegCore's parsed `decimal FrameRate`. Match the same source field. |
| `ScanType` | `streams[primary_video].field_order` → `"tt"\|"bb"\|"tb"\|"bt"` ⇒ `"Interlaced"`, else `"Progressive"` | exact switch from `:80-84`. |
| `RawStreamData` | full ffprobe stdout, unparsed | preserve for compatibility — see FFM-Q3. |
| `AudioStreams[]` | iterate `streams` where `codec_type=="audio"`, sorted by `index` | per stream: `tags.language` (default `"und"`), `codec_name`, `codec_tag_string`, `profile`, bit_rate (with BPS fallback), `channels`, `channel_layout`, `tags.title`. Mirrors `:87-109`. |
| `SubtitleStreams[]` | iterate `streams` where `codec_type=="subtitle"` AND `tags.language` non-empty | per stream: `tags.language`, `codec_name`, `tags.title`, `disposition.forced` (0/1 → bool), `disposition.hearing_impaired`. Mirrors `:111-139`. |
| `Title` | `format.tags.title` | optional. |
| `SchemaRevision` | constant `14` | matches `MINIMUM_MEDIA_INFO_SCHEMA_REVISION` / `CURRENT_MEDIA_INFO_SCHEMA_REVISION` at `:24-25`. |

For HDR side-data: the four constants in `MediaInfo/FFMpegCoreSideDataTypes.cs` are the literal `side_data_type` strings ffprobe emits inside `streams[*].side_data_list[*]` and `frames[*].side_data_list[*]`. No translation needed — match strings verbatim and keep `GetHdrFormat`'s switch logic.

## Verdict

Direct exec is straightforward. FFMpegCore is a thin parser on top of the same `ffprobe -print_format json` invocation, so we lose nothing by going direct, and we gain explicit control over context cancellation and stderr capture. The wire-shape that lands in `EpisodeFiles.MediaInfo` JSONB/text columns is governed by Go-side serialization of `MediaInfoModel`, which the embedded-doc catalogue already pins — **the FFprobe binding is not the shape contract**, the C# struct serialization is. As long as the Go port emits a JSON document with the same property names (PascalCase, per Sonarr's serializer settings), byte-equivalence on the wire is achievable.

## Notes for the porter

- **ffprobe binary**: ship the same binary the C# build ships, or a pinned version. The exact build matters less than the JSON-schema baseline (FFM-Q1 in MEDIAFILES). ffprobe JSON output has been stable across the 4.x→6.x major versions for the fields we use — this is verifiable by running `ffprobe -version` and `ffprobe -print_format json -show_streams` on the bundled binary at port start and comparing against a captured baseline.
- **Process lifecycle**:
  - `exec.CommandContext(ctx, …)` — caller cancels ctx, child gets killed.
  - On Unix, set `SysProcAttr.Setpgid = true` so a `Process.Kill` reaches the whole process group (ffmpeg can spawn helpers in some configurations).
  - On Windows, attach a Job Object so child processes die with the parent on cancel.
  - Capture stderr separately for diagnostics — useful when ffprobe rejects a malformed file (the C# `Logger.Error(ex, …)` path at `:166-168` becomes `slog.Error("ffprobe failed", "stderr", stderr.String(), "file", file)`).
- **Probe-size escalation**: keep the same two-pass behavior. First pass with `-probesize 50000000`. If the parsed model has a `PrimaryAudioStream` with empty `ChannelLayout`, re-probe with `-probesize 150000000 -analyzeduration 150000000`. Mirrors `VideoFileInfoReader.cs:62-65`.
- **Pixel-format table**: cache the result of `ffprobe -show_pix_fmts -print_format json` on first use. The output is static per binary; no need to re-run per file. The C# wrapper does this internally via FFMpegCore's static `FFProbePixelFormat` cache.
- **Disk extension skip**: replicate the `MediaFileExtensions.DiskExtensions.Contains(ext)` short-circuit at `:48-51` — `.iso`, `.img`, `.vob` files return `null` without spawning ffprobe.
- **No CGo**: stdlib `os/exec` only. Cross-compilation works.

## Open questions

- **FFM-Q1**: The C# `MediaInfoModel` is serialized via Newtonsoft (or System.Text.Json under v5; verify) with default property-name casing — confirm whether existing on-disk JSON in `EpisodeFiles.MediaInfo` is camelCase, PascalCase, or mixed. The Go port's struct tags must match exactly. (Cross-cutting: this affects the embedded-docs catalogue's `MediaInfoModel` shape contract, not the spike itself.)
- **FFM-Q2**: `MediaInfoModel.RunTime` is a `TimeSpan`. C# Newtonsoft serializes as `"00:42:18.5000000"`. Go's `time.Duration` doesn't round-trip that format natively — needs a custom marshaler. Verify the Go port's serialization layer handles this; otherwise old rows won't deserialize.
- **FFM-Q3**: `RawStreamData = string.Concat(analysis.OutputData)` (`:85`) — `OutputData` is a `string[]` of stdout lines from the ffprobe call, joined without separators. That means `RawStreamData` is the raw ffprobe stdout *without newlines between original lines*. The Go port should match: capture `cmd.Stdout` to a `bytes.Buffer`, then strip `\n` and `\r` before assigning, OR change the schema (bumping `SchemaRevision`) and store the raw buffer with newlines. Cleanest: match C# byte-for-byte to keep `SchemaRevision == 14` valid for existing rows. Cross-reference MEDIAFILES-Q3.
