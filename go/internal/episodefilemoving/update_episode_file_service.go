package episodefilemoving

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/series"
)

// FileDateType ports `MediaFiles/FileDateType.cs:1-9`. The numeric
// ordinals are not persisted in the Go-port — config stores the
// enum-identifier string, so this type only carries the parser
// branches and the human-readable form.
//
// Closes queue [14]. The slice-3.BK
// [UpdateEpisodeFileService.ChangeFileDateForFile] gates on this.
type FileDateType int

const (
	// FileDateNone disables the per-file mtime adjustment. Default.
	FileDateNone FileDateType = 0
	// FileDateLocalAirDate sets the file's mtime to the episode's
	// air date converted to local time (`AirDateUtc.ToLocalTime()`).
	FileDateLocalAirDate FileDateType = 1
	// FileDateUtcAirDate sets the file's mtime to the episode's
	// AirDateUtc value, but with the kind re-tagged Local — i.e.
	// the wall-clock UTC time is interpreted as local. Mirrors the
	// `DateTime.SpecifyKind(..., DateTimeKind.Local)` at C#
	// `UpdateEpisodeFileService.cs:65` which the comment at `:61`
	// flags as "intentionally pass UTC as local per user preference".
	FileDateUtcAirDate FileDateType = 2
)

// ParseFileDateType ports the C# `GetValueEnum` posture. Recognises
// the C# enum-identifier form (`"None"` / `"LocalAirDate"` /
// `"UtcAirDate"`) case-insensitively. Unknown / empty → [FileDateNone].
func ParseFileDateType(s string) FileDateType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "localairdate":
		return FileDateLocalAirDate
	case "utcairdate":
		return FileDateUtcAirDate
	default:
		return FileDateNone
	}
}

// String returns the C# enum-identifier form. Used by the config
// setter round-trip.
func (f FileDateType) String() string {
	switch f {
	case FileDateLocalAirDate:
		return "LocalAirDate"
	case FileDateUtcAirDate:
		return "UtcAirDate"
	default:
		return "None"
	}
}

// FileTimeManager narrows the disk-level mtime get/set surface that
// `UpdateEpisodeFileService` reaches for via IDiskProvider. Defined
// locally so tests can stub disk reads/writes; production wiring
// binds an adapter over `*disk.Provider.FileGetLastWrite` + a
// thin `os.Chtimes` wrapper.
//
// Mirrors the C# `IDiskProvider.FileGetLastWrite` / `FileSetLastWriteTime`
// pair at `UpdateEpisodeFileService.cs:74,89`. FileGetLastWrite
// returns UTC; the caller [UpdateService.ChangeFileDateForFile]
// converts to local before comparing.
type FileTimeManager interface {
	FileGetLastWrite(path string) (time.Time, error)
	FileSetLastWriteTime(path string, t time.Time) error
}

// FileDateConfigReader narrows [config.Provider] to the FileDate
// getter the [UpdateService] gates on.
//
// Mirrors C# `IConfigService.FileDate` at `:56`. Returns the
// enum-identifier string; the service parses via [ParseFileDateType].
type FileDateConfigReader interface {
	FileDate() string
}

// epochTime ports `DateTimeExtensions.EpochTime = 1970-01-01 UTC`.
// Used by the non-Windows lower-bound clamp at C# `:76-80` —
// pre-epoch dates can't be set on POSIX filesystems with second
// precision, so the C# code clamps and logs.
var epochTime = time.Unix(0, 0).UTC()

// EpisodesWithFilesLister narrows [episodes.Service] to the read
// accessor [UpdateService.HandleSeriesScanned] uses to enumerate
// episodes attached to a file on disk. `*episodes.Service` satisfies
// via the method at `episodes/service.go:165`. Slice J212.
type EpisodesWithFilesLister interface {
	EpisodesWithFiles(ctx context.Context, seriesID int) ([]episodes.Episode, error)
}

// EpisodeFileLookup narrows [episodefiles.Service] to the by-id read
// accessor [UpdateService.HandleSeriesScanned] uses to load each
// EpisodeFile after grouping. `*episodefiles.Service` satisfies via
// `episodefiles/service.go:209 GetFile`. Slice J212.
type EpisodeFileLookup interface {
	GetFile(ctx context.Context, id int) (*episodefiles.File, error)
}

// UpdateService ports `UpdateEpisodeFileService` at
// `MediaFiles/UpdateEpisodeFileService.cs:22-136`. Slice 3.BK landed
// [ChangeFileDateForFile] (the public IUpdateEpisodeFileService
// surface at C# `:41-44`); slice J212 lands the
// IHandle<SeriesScannedEvent> subscriber at C# `:103-136` via
// [UpdateService.HandleSeriesScanned] + [UpdateService.SetSeriesScannedDeps].
//
// `episodesLister` and `fileLookup` are optional — when either is nil
// HandleSeriesScanned debug-logs + returns nil. This keeps the
// existing slice-3.BK [NewUpdateService] signature stable; callers
// that need the scan-subscriber behaviour wire the two extras post-
// construction via SetSeriesScannedDeps.
//
// Goroutine-safe: stateless after construction (post-construction
// setter must run before the bus subscription that drives
// HandleSeriesScanned).
type UpdateService struct {
	disk           FileTimeManager
	cfg            FileDateConfigReader
	log            *slog.Logger
	episodesLister EpisodesWithFilesLister
	fileLookup     EpisodeFileLookup
}

// NewUpdateService binds the two narrow deps. nil-required-dep
// returns a typed error (matches the sibling spec-constructor
// posture across Phase 3). nil-log defaults to slog.Default().
func NewUpdateService(disk FileTimeManager, cfg FileDateConfigReader, log *slog.Logger) (*UpdateService, error) {
	if disk == nil {
		return nil, errors.New("episodefilemoving: NewUpdateService: disk is nil")
	}
	if cfg == nil {
		return nil, errors.New("episodefilemoving: NewUpdateService: cfg is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpdateService{disk: disk, cfg: cfg, log: log}, nil
}

// Compile-time guard — *UpdateService satisfies [FileDateUpdater].
var _ FileDateUpdater = (*UpdateService)(nil)

// ChangeFileDateForFile ports `:41-44` (the public IUpdateEpisodeFileService
// entry point) which delegates to the private `ChangeFileDate` at
// `:46-69`.
//
//  1. Resolves the on-disk path from `series.Path + file.RelativePath`
//     (mirrors C# `:48`).
//  2. Reads the first-episode `AirDateUtc`. nil → no-op return nil
//     (matches C# `:51-54` early-return false; the Go-port collapses
//     the bool to error since callers don't read the result).
//  3. Branches on [FileDateConfigReader.FileDate]:
//     - LocalAirDate → set mtime to AirDateUtc.ToLocalTime()
//     - UtcAirDate   → set mtime to AirDateUtc tagged as local
//     (the "user preference" pass-through at `:61`)
//     - default      → no-op
//  4. Delegates to [changeFileDateToLocalDate] which implements the
//     pre-epoch clamp + sub-second preservation + actual disk write.
//
// nil-receiver / nil-file / nil-series / empty-eps → typed error so
// caller bugs surface loudly.
func (s *UpdateService) ChangeFileDateForFile(file *episodefiles.File, ser *series.Series, eps []episodes.Episode) error {
	if s == nil {
		return errors.New("episodefilemoving: ChangeFileDateForFile on nil receiver")
	}
	if file == nil {
		return errors.New("episodefilemoving: ChangeFileDateForFile: nil file")
	}
	if ser == nil {
		return errors.New("episodefilemoving: ChangeFileDateForFile: nil series")
	}
	if len(eps) == 0 {
		return errors.New("episodefilemoving: ChangeFileDateForFile: empty episodes")
	}
	air := eps[0].AirDateUtc
	if air == nil {
		return nil
	}
	path := filepath.Join(ser.Path, file.RelativePath)
	mode := ParseFileDateType(s.cfg.FileDate())
	switch mode {
	case FileDateLocalAirDate:
		return s.changeFileDateToLocalDate(path, air.Local())
	case FileDateUtcAirDate:
		// Mirror C# `DateTime.SpecifyKind(airDateUtc.Value, DateTimeKind.Local)`
		// at `:65` — the wall-clock UTC components are reinterpreted
		// as local time without conversion. Achieved here by stripping
		// the timezone via time.Date with the Local location.
		t := *air
		local := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
		return s.changeFileDateToLocalDate(path, local)
	default:
		return nil
	}
}

// changeFileDateToLocalDate ports the private helper at `:71-101`.
// Compares the rounded-to-second target time against the rounded
// last-write time; if they differ, preserves the existing mtime's
// sub-seconds and writes (per Sonarr#7228 which fixed a bug where
// the rewrite kept appending zero sub-seconds and confused backup
// tools). Returns nil on success or when no change is needed; the
// disk-write error surfaces wrapped (the C# tree logs+swallows at
// `:94-97`, but the Go-port leaves the choice to the caller —
// the existing [Service] writer already wraps in its own log+continue).
func (s *UpdateService) changeFileDateToLocalDate(path string, localDate time.Time) error {
	oldUTC, err := s.disk.FileGetLastWrite(path)
	if err != nil {
		return fmt.Errorf("episodefilemoving: FileGetLastWrite(%q): %w", path, err)
	}
	oldLocal := oldUTC.Local()

	// POSIX filesystems can't represent times before 1970-01-01;
	// the Sonarr C# code clamps to epoch on non-Windows. Mirror.
	if runtime.GOOS != "windows" && localDate.UTC().Before(epochTime) {
		s.log.Debug("episodefilemoving: clamping pre-epoch air date to 1970-01-01", "path", path)
		localDate = epochTime.Local()
	}

	// Compare with sub-second precision stripped.
	if withoutTicks(localDate).Equal(withoutTicks(oldLocal)) {
		return nil
	}

	// Preserve prior sub-second precision per Sonarr#7228.
	mtime := withTicksFrom(localDate, oldLocal)
	if err := s.disk.FileSetLastWriteTime(path, mtime); err != nil {
		s.log.Warn("episodefilemoving: FileSetLastWriteTime failed", "path", path, "err", err)
		return nil
	}
	s.log.Debug("episodefilemoving: file mtime changed", "path", path, "from", oldLocal, "to", mtime)
	return nil
}

// withoutTicks ports the C# extension `DateTime.WithoutTicks()` —
// truncates sub-second precision so the equality check at `:82`
// doesn't trip on round-trip nanosecond drift.
func withoutTicks(t time.Time) time.Time {
	return t.Truncate(time.Second)
}

// withTicksFrom ports `DateTime.WithTicksFrom(other)` — keeps the
// year/month/day/hour/minute/second from t, but inherits the
// sub-second remainder (`Nanoseconds % 1e9`) from src. The Sonarr#7228
// fix avoids backup-tool churn by not rewriting sub-seconds to zero.
func withTicksFrom(t, src time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), src.Nanosecond(), t.Location())
}

// SetSeriesScannedDeps wires the two optional dependencies that
// [UpdateService.HandleSeriesScanned] reaches for. Must be called
// before the bus subscription that drives HandleSeriesScanned —
// either dep nil keeps the handler in debug-log-and-skip mode. Slice
// J212.
func (s *UpdateService) SetSeriesScannedDeps(episodesLister EpisodesWithFilesLister, fileLookup EpisodeFileLookup) {
	if s == nil {
		return
	}
	s.episodesLister = episodesLister
	s.fileLookup = fileLookup
}

// HandleSeriesScanned ports C# `UpdateEpisodeFileService.cs:103-136`
// — the IHandle<SeriesScannedEvent> subscriber that re-stamps every
// episode file's mtime to its episode air date after a disk scan
// completes.
//
// Decision tree (line-aligned with C#):
//
//   - `:105-108` if FileDate == None → no-op (the default config —
//     most users never touch this).
//   - `:110` fetch `EpisodesWithFiles(seriesId)` — only episodes with
//     a non-nil EpisodeFileID participate.
//   - `:115` group episodes by EpisodeFileId so multi-episode files
//     (two-part stories sharing a single physical file) get a single
//     ChangeFileDate call rather than one-per-episode.
//   - `:120-125` for each group: load the EpisodeFile by ID and call
//     [ChangeFileDateForFile]. The C# version reaches the file via the
//     joined `episodesInFile.First().EpisodeFile` populated by the
//     `QueryJoined<Episode, EpisodeFile>` shape; the Go port walks
//     the lookup explicitly because [episodes.Service.EpisodesWithFiles]
//     leaves the join unpopulated (see queries.go:189-194 doc-comment).
//   - `:128-135` log a one-line summary of how many files were
//     processed.
//
// Divergence note: C# tracks an "updated" count separate from the
// total based on whether ChangeFileDate returned true (the file mtime
// actually differed from the target). The Go-port's [ChangeFileDateForFile]
// collapses the bool return to error (slice 3.BK established this
// convention since the writer-side caller didn't read it). The
// summary log here counts "processed" files — same semantic intent,
// loses the no-op-vs-actual-change distinction. Restoring the bool
// would touch every existing call site for marginal gain.
//
// Per-episode-file failures (lookup or write) are warn-logged and
// swallowed so one bad file doesn't abort the whole series. C#
// behaviour: ChangeFileDate's catch at `:94-97` already absorbs
// disk-write errors; the Go-port adds the same posture for lookup
// failures.
//
// Returns nil on the happy path. Returns a wrapped error only when
// the initial EpisodesWithFiles query fails (the series-level
// load — distinct from per-file errors).
//
// Slice J212.
func (s *UpdateService) HandleSeriesScanned(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
	if s == nil || ev.Series == nil {
		return nil
	}
	if s.episodesLister == nil || s.fileLookup == nil {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episodefilemoving: SeriesScanned deps not wired — skipping",
			slog.Int("seriesId", ev.Series.ID),
			slog.String("title", ev.Series.Title))
		return nil
	}
	if ParseFileDateType(s.cfg.FileDate()) == FileDateNone {
		return nil
	}
	eps, err := s.episodesLister.EpisodesWithFiles(ctx, ev.Series.ID)
	if err != nil {
		return fmt.Errorf("episodefilemoving: EpisodesWithFiles(%d): %w", ev.Series.ID, err)
	}
	groups := make(map[int][]episodes.Episode)
	for _, ep := range eps {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		groups[*ep.EpisodeFileID] = append(groups[*ep.EpisodeFileID], ep)
	}
	if len(groups) == 0 {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episodefilemoving: no episode files to update",
			slog.Int("seriesId", ev.Series.ID),
			slog.String("title", ev.Series.Title))
		return nil
	}
	processed := 0
	for fileID, group := range groups {
		file, err := s.fileLookup.GetFile(ctx, fileID)
		if err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"episodefilemoving: GetFile failed — skipping",
				slog.Int("fileId", fileID),
				slog.Any("err", err))
			continue
		}
		if file == nil {
			continue
		}
		if err := s.ChangeFileDateForFile(file, ev.Series, group); err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"episodefilemoving: ChangeFileDateForFile failed",
				slog.Int("fileId", fileID),
				slog.Any("err", err))
			continue
		}
		processed++
	}
	if processed > 0 {
		s.log.LogAttrs(ctx, slog.LevelInfo,
			"episodefilemoving: processed file dates after scan",
			slog.Int("files", processed),
			slog.Int("of", len(groups)),
			slog.String("series", ev.Series.Title))
	} else {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episodefilemoving: no file dates processed after scan",
			slog.String("series", ev.Series.Title))
	}
	return nil
}
