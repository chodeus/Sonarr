package xbmc

import (
	"errors"
	"regexp"
)

// NfoFilesystem narrows the disk surface [NfoDetector] needs. The
// production binding wraps an internal/disk.Provider via Go
// structural typing; for tests, an in-memory map satisfies the
// interface trivially.
//
// Method semantics intentionally match the C# `IDiskProvider`
// methods at:
//   - `NzbDrone.Common/Disk/IDiskProvider.cs::GetFileSize` →
//     [NfoFilesystem.GetFileSize]
//   - `NzbDrone.Common/Disk/IDiskProvider.cs::ReadAllText` →
//     [NfoFilesystem.ReadAllText]
//
// Returning an error from either method routes through the C#
// "treat as non-Kodi" branch (the C# tree propagates IOException up;
// the Go port catches at this seam and returns false).
type NfoFilesystem interface {
	// GetFileSize returns the file size in bytes. Mirrors C#
	// `IDiskProvider.GetFileSize(path)` at `:38`.
	GetFileSize(path string) (int64, error)

	// ReadAllText returns the entire file contents as a string.
	// Mirrors C# `IDiskProvider.ReadAllText(path)` at `:78`.
	ReadAllText(path string) (string, error)
}

// nfoBodyRegex matches the opening tag of every Kodi/XBMC NFO root
// element variant. Mirrors C# `XbmcNfoDetector.cs:16` exactly —
// {movie, tvshow, episodedetails, artist, album, musicvideo}. The
// last three are Lidarr-ish; XbmcMetadata only emits `tvshow` and
// `episodedetails`, but the C# pattern accepts the broader set so a
// .nfo from a sibling-arr is recognised as "already managed" during
// ExistingMetadataImporter scans.
//
// `(?:...)` non-capturing group; case-sensitive — the C# tree relies
// on the default Regex case-sensitivity.
var nfoBodyRegex = regexp.MustCompile(`<(?:movie|tvshow|episodedetails|artist|album|musicvideo)>`)

// nfoMaxBytes is the soft size cap before the detector refuses to
// load the file into memory. Mirrors C# `10.Megabytes()` at
// `XbmcNfoDetector.cs:26` — `NzbDrone.Common.Extensions.Megabytes`
// returns `value * 1024 * 1024` so 10MB == 10_485_760 bytes.
const nfoMaxBytes int64 = 10 * 1024 * 1024

// NfoDetector ports the C# `XbmcNfoDetector` at
// `XbmcNfoDetector.cs:12-36`. The single public method
// [NfoDetector.IsXbmcNfoFile] reports whether `path` is a recognised
// Kodi-format NFO sidecar (i.e. its body opens with one of the
// nfoBodyRegex roots).
//
// Goroutine-safe: stateless after construction. The compiled regex
// is package-global (matches the C# `static readonly` shape) so
// concurrent calls share the same Regex instance.
type NfoDetector struct {
	fs NfoFilesystem
}

// NewNfoDetector binds the narrow filesystem dep. nil-fs returns a
// typed error rather than a runtime panic — mirrors the constructor
// nil-guard convention used in metadatasource.NewProxy and similar
// concrete providers.
func NewNfoDetector(fs NfoFilesystem) (*NfoDetector, error) {
	if fs == nil {
		return nil, errors.New("xbmc: NewNfoDetector: fs is nil")
	}
	return &NfoDetector{fs: fs}, nil
}

// IsXbmcNfoFile reports whether the file at `path` is a recognised
// Kodi-format NFO. Mirrors C# `XbmcNfoDetector.IsXbmcNfoFile` at
// `XbmcNfoDetector.cs:23-35`:
//
//  1. Reject files larger than [nfoMaxBytes] — bail without reading.
//  2. Read the file contents.
//  3. Return whether the body matches [nfoBodyRegex].
//
// Either a GetFileSize or ReadAllText error short-circuits to false
// — the C# tree propagates IOException up the call stack; the Go
// port catches at this seam because the only call site
// (XbmcMetadata.FindMetadataFile) treats every "not one of mine"
// outcome identically.
func (d *NfoDetector) IsXbmcNfoFile(path string) bool {
	if d == nil {
		return false
	}
	size, err := d.fs.GetFileSize(path)
	if err != nil {
		return false
	}
	if size > nfoMaxBytes {
		return false
	}
	body, err := d.fs.ReadAllText(path)
	if err != nil {
		return false
	}
	return nfoBodyRegex.MatchString(body)
}
