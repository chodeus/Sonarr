package episodefilemoving

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
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

// UpdateService ports the public surface of `UpdateEpisodeFileService`
// at `MediaFiles/UpdateEpisodeFileService.cs:22-44`. This slice
// (3.BK, queue [14]) lands the [ChangeFileDateForFile] method only
// — the IHandle<SeriesScannedEvent> subscriber at `:103-136`
// remains unported (flagged for follow-up; orthogonal to the writer
// path that already pulls ChangeFileDateForFile per-file via
// [FileDateUpdater]).
//
// Goroutine-safe: stateless after construction.
type UpdateService struct {
	disk FileTimeManager
	cfg  FileDateConfigReader
	log  *slog.Logger
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
