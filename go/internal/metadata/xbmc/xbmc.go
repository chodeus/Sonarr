package xbmc

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// providerDisplayName is the user-facing name the v3 UI shows for
// this writer. Mirrors C# `XbmcMetadata.Name` at
// `XbmcMetadata.cs:49`.
const providerDisplayName = "Kodi (XBMC) / Emby"

// seriesImagesRegex / seasonImagesRegex / episodeImageRegex are the
// three filename-classifying patterns from C# `XbmcMetadata.cs:45-47`.
// All three use the C# `RegexOptions.IgnoreCase` flag — the Go port
// applies the `(?i)` inline option for parity.
//
// PATTERN-PARITY notes:
//   - The C# `(?<season>...)` named-capture maps to Go's
//     `(?P<season>...)` syntax (RE2 doesn't support .NET-style names);
//   - The C# `\.(?:png|jpg)` literal extension is replaced with the
//     Go-equivalent `\.(?:png|jpg)` (same — RE2 accepts the same
//     non-capturing group syntax);
//   - All three patterns anchor only at the start (`^`); the C# pattern
//     allows trailing characters after the extension. Mirror that.
var (
	seriesImagesRegex = regexp.MustCompile(`(?i)^(?P<type>poster|banner|fanart)\.(?:png|jpg)`)
	seasonImagesRegex = regexp.MustCompile(`(?i)^season(?P<season>\d{2,}|-all|-specials)-(?P<type>poster|banner|fanart)\.(?:png|jpg)`)
	episodeImageRegex = regexp.MustCompile(`(?i)-thumb\.(?:png|jpg)`)
)

// XbmcMetadata ports the C# concrete `XbmcMetadata` class at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/XbmcMetadata.cs:24-533`.
// Implements [metadata.Provider].
//
// Sub-arc W2a (this slice 4.BB): the foundation — Name +
// GetFilenameAfterMove + FindMetadataFile + Test + RequestAction +
// stubs for the XML-emitting and image methods. Sub-arc W2b fills in
// SeriesMetadata + EpisodeMetadata + SeriesImages + SeasonImages +
// EpisodeImages and registers the constructor with [metadata.Default].
//
// Goroutine-safe: stateless after construction. The compiled regexes
// are package-globals (matching the C# `static readonly` shape) so
// concurrent calls share the same Regex instances.
type XbmcMetadata struct {
	definition *metadata.Metadata
	settings   Settings
	detector   *NfoDetector
}

// NewXbmcMetadata builds an [XbmcMetadata] from a persisted
// [metadata.Metadata] row plus the per-writer Settings. The
// [NfoDetector] is required for FindMetadataFile's NFO-body sniff;
// nil-detector returns a typed error.
//
// Mirrors the C# `XbmcMetadata` constructor at `XbmcMetadata.cs:32-43`
// — same dependency-injection shape (IDetectXbmcNfo +
// IDiskProvider + IMapCoversToLocal + ITagRepository + Logger). The
// MapCoversToLocal / TagRepository / Logger deps are not threaded
// through this sub-arc because they only matter for the deferred
// XML-emission methods; they land via the [metadata.Dependencies]
// bundle in sub-arc W2b.
func NewXbmcMetadata(definition *metadata.Metadata, settings Settings, detector *NfoDetector) (*XbmcMetadata, error) {
	if detector == nil {
		return nil, errors.New("xbmc: NewXbmcMetadata: detector is nil")
	}
	return &XbmcMetadata{
		definition: definition,
		settings:   settings,
		detector:   detector,
	}, nil
}

// Name returns the user-facing display label. Mirrors C# `Name`
// at `XbmcMetadata.cs:49` — "Kodi (XBMC) / Emby".
func (m *XbmcMetadata) Name() string {
	return providerDisplayName
}

// Definition returns the persisted Metadata row backing this provider
// instance. Mirrors C# `Definition` (inherited from MetadataBase).
func (m *XbmcMetadata) Definition() *metadata.Metadata {
	if m == nil {
		return nil
	}
	return m.definition
}

// SettingsConfig returns the per-instance Settings struct. Exposed
// for the XML-emission methods in sub-arc W2b plus tests.
func (m *XbmcMetadata) SettingsConfig() Settings {
	if m == nil {
		return Settings{}
	}
	return m.settings
}

// GetFilenameAfterMove ports `XbmcMetadata.GetFilenameAfterMove` at
// `XbmcMetadata.cs:51-67`. Computes the new metadata-file path after
// the parent episode-file moves. Dispatches on [metadataFile.Type]:
//
//   - [metadata.MetadataTypeEpisodeImage] → swap stem ext to "-thumb.jpg"
//     via [getEpisodeImageFilename];
//   - [metadata.MetadataTypeEpisodeMetadata] → swap stem ext to ".nfo"
//     via [getEpisodeMetadataFilename];
//   - anything else → fall back to `series.Path` + relative path
//     (preserves the prior C# semantics for series-level metadata
//     types where the file name is not derivative of the episode
//     file).
//
// nil-receiver / nil-args return "" rather than panicking.
func (m *XbmcMetadata) GetFilenameAfterMove(s *series.Series, episodeFile *episodefiles.File, metadataFile *metadatafiles.MetadataFile) string {
	if m == nil || s == nil || episodeFile == nil || metadataFile == nil {
		return ""
	}
	episodeFilePath := filepath.Join(s.Path, episodeFile.RelativePath)
	switch metadata.MetadataType(metadataFile.Type) {
	case metadata.MetadataTypeEpisodeImage:
		return getEpisodeImageFilename(episodeFilePath)
	case metadata.MetadataTypeEpisodeMetadata:
		return getEpisodeMetadataFilename(episodeFilePath)
	}
	return filepath.Join(s.Path, metadataFile.RelativePath)
}

// FindMetadataFile classifies an on-disk file at `path` as a known
// XbmcMetadata-emitted sidecar. Mirrors C# `XbmcMetadata.FindMetadataFile`
// at `XbmcMetadata.cs:69-139`. Returns nil-row + nil-err when the
// file is NOT one of this writer's outputs — the consumer-facing
// scan path iterates every registered writer so a nil return is the
// common case.
//
// Classification rules (in C# evaluation order — first match wins):
//
//   - Filename matches [seriesImagesRegex] → SeriesImage.
//   - Filename matches [seasonImagesRegex] → SeasonImage; SeasonNumber
//     parsed from the named-capture group ("specials" → 0, "-all" →
//     skip-via-nil, NN → int). The C# tree returns nil when the
//     season-number group parses neither; the Go port matches.
//   - Filename matches [episodeImageRegex] → EpisodeImage.
//   - Filename equals "tvshow.nfo" (case-insensitive) → SeriesMetadata.
//   - [parser.ParseTitle] succeeds AND !FullSeason AND ext is ".nfo"
//     AND [NfoDetector.IsXbmcNfoFile] → EpisodeMetadata.
//   - Else nil.
//
// The error return is reserved for "GetRelativePath failed"
// scenarios; the C# tree throws on a non-parent path. The Go port
// surfaces such cases as a non-nil error so the consumer-facing
// scanner can log + skip.
func (m *XbmcMetadata) FindMetadataFile(s *series.Series, path string) (*metadatafiles.MetadataFile, error) {
	if m == nil || s == nil || path == "" {
		return nil, nil
	}
	filename := filepath.Base(path)
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		return nil, nil
	}
	relativePath, err := getRelativePath(s.Path, path)
	if err != nil {
		return nil, err
	}
	base := &metadatafiles.MetadataFile{
		SeriesID:     s.ID,
		Consumer:     ImplementationName,
		RelativePath: relativePath,
	}

	if seriesImagesRegex.MatchString(filename) {
		base.Type = int(metadata.MetadataTypeSeriesImage)
		return base, nil
	}

	if match := seasonImagesRegex.FindStringSubmatch(filename); match != nil {
		base.Type = int(metadata.MetadataTypeSeasonImage)
		// Named-capture index lookup; C# `.Groups["season"].Value`.
		seasonIdx := seasonImagesRegex.SubexpIndex("season")
		if seasonIdx < 0 || seasonIdx >= len(match) {
			return nil, nil
		}
		seasonNumberMatch := match[seasonIdx]
		if strings.Contains(seasonNumberMatch, "specials") {
			zero := 0
			base.SeasonNumber = &zero
			return base, nil
		}
		// C# `int.TryParse` only succeeds on a pure-int body; the
		// "-all" capture-value is left out of TryParse semantics
		// (it doesn't parse as int → return nil per C# `:109`).
		n, parseErr := strconv.Atoi(seasonNumberMatch)
		if parseErr != nil {
			return nil, nil
		}
		base.SeasonNumber = &n
		return base, nil
	}

	if episodeImageRegex.MatchString(filename) {
		base.Type = int(metadata.MetadataTypeEpisodeImage)
		return base, nil
	}

	if strings.EqualFold(filename, "tvshow.nfo") {
		base.Type = int(metadata.MetadataTypeSeriesMetadata)
		return base, nil
	}

	parseResult := parser.ParseTitle(filename)
	if parseResult != nil &&
		!parseResult.FullSeason &&
		strings.EqualFold(filepath.Ext(filename), ".nfo") &&
		m.detector.IsXbmcNfoFile(path) {
		base.Type = int(metadata.MetadataTypeEpisodeMetadata)
		return base, nil
	}

	return nil, nil
}

// SeriesMetadata is the W2a STUB for the per-series `tvshow.nfo`
// emitter. Returns the zero-value [metadata.MetadataFileResult]
// (RelativePath == "" — the "writer chose to skip" sentinel per
// go/internal/metadata/results.go). Sub-arc W2b fills the body.
//
// Ports C# `XbmcMetadata.SeriesMetadata` at `XbmcMetadata.cs:141-276`
// — ~135 LOC of XElement build-up gated on Settings.SeriesMetadata
// plus the optional `episodeguide` JSON sub-document.
func (m *XbmcMetadata) SeriesMetadata(_ context.Context, _ *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	// STUB — slice 4.BB sub-arc W2a. Body lands in sub-arc W2b.
	return metadata.MetadataFileResult{}, nil
}

// EpisodeMetadata is the W2a STUB for the per-episode `<filename>.nfo`
// emitter. Returns the zero-value [metadata.MetadataFileResult].
// Sub-arc W2b fills the body.
//
// Ports C# `XbmcMetadata.EpisodeMetadata` at `XbmcMetadata.cs:278-423`
// — ~145 LOC of XElement build-up including the MediaInfo
// streamdetails sub-document.
func (m *XbmcMetadata) EpisodeMetadata(_ context.Context, _ *series.Series, _ *episodefiles.File) (metadata.MetadataFileResult, error) {
	// STUB — slice 4.BB sub-arc W2a. Body lands in sub-arc W2b.
	return metadata.MetadataFileResult{}, nil
}

// SeriesImages is the W2a STUB. Returns an empty slice — the C# code
// at `XbmcMetadata.cs:425-433` returns an empty list when
// [Settings.SeriesImages] is false, so the stub matches the
// disabled-setting C# behaviour. Sub-arc W2b adds the enabled-path
// emission (iterate series.Images decoded blob + per-cover
// destination filename).
func (m *XbmcMetadata) SeriesImages(_ context.Context, _ *series.Series) ([]metadata.ImageFileResult, error) {
	// STUB — slice 4.BB sub-arc W2a. Body lands in sub-arc W2b.
	return nil, nil
}

// SeasonImages is the W2a STUB. See [XbmcMetadata.SeriesImages] for
// the parallel rationale. Ports C# `XbmcMetadata.SeasonImages` at
// `XbmcMetadata.cs:435-443`.
func (m *XbmcMetadata) SeasonImages(_ context.Context, _ *series.Series, _ metadata.Season) ([]metadata.ImageFileResult, error) {
	// STUB — slice 4.BB sub-arc W2a. Body lands in sub-arc W2b.
	return nil, nil
}

// EpisodeImages is the W2a STUB. See [XbmcMetadata.SeriesImages] for
// the parallel rationale. Ports C# `XbmcMetadata.EpisodeImages` at
// `XbmcMetadata.cs:445-481`.
func (m *XbmcMetadata) EpisodeImages(_ context.Context, _ *series.Series, _ *episodefiles.File) ([]metadata.ImageFileResult, error) {
	// STUB — slice 4.BB sub-arc W2a. Body lands in sub-arc W2b.
	return nil, nil
}

// Test is the per-provider connection self-check. Mirrors C#
// `MetadataBase.Test()` at `MetadataBase.cs:25-28` — the base impl
// returns an empty ValidationResult, and XbmcMetadata does not
// override. The Go port returns the result of Settings.Validate()
// for consistency with the per-Settings rule path; today the
// validator is empty (see [Settings.Validate]) so this is always
// nil / nil.
func (m *XbmcMetadata) Test(_ context.Context) ([]metadata.TestFailure, error) {
	if m == nil {
		return nil, nil
	}
	return m.settings.Validate(), nil
}

// RequestAction handles the OAuth-style v3-UI handshake. Mirrors C#
// `MetadataBase.RequestAction` at `MetadataBase.cs:47-50` — base
// impl returns null, and XbmcMetadata does not override.
func (m *XbmcMetadata) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard that XbmcMetadata satisfies the
// [metadata.Provider] interface contract. Catches signature drift if
// the foundation interface changes.
var _ metadata.Provider = (*XbmcMetadata)(nil)

// getEpisodeMetadataFilename mirrors C# `XbmcMetadata.GetEpisodeMetadataFilename`
// at `XbmcMetadata.cs:509-512` — swap extension to ".nfo".
//
// Path.ChangeExtension in .NET preserves the dot, so the result is
// `<stem>.nfo`. filepath in Go has no direct equivalent; we strip
// the existing extension via filepath.Ext + slice.
func getEpisodeMetadataFilename(episodeFilePath string) string {
	ext := filepath.Ext(episodeFilePath)
	stem := episodeFilePath[:len(episodeFilePath)-len(ext)]
	return stem + ".nfo"
}

// getEpisodeImageFilename mirrors C# `XbmcMetadata.GetEpisodeImageFilename`
// at `XbmcMetadata.cs:514-517` — strip the extension AND any trailing
// dots, then append "-thumb.jpg".
//
// The C# `.ChangeExtension(path, "").Trim('.')` produces "S01E01"
// from "S01E01.mkv"; the Go-port uses filepath.Ext + slice + a
// trailing-dot trim.
func getEpisodeImageFilename(episodeFilePath string) string {
	ext := filepath.Ext(episodeFilePath)
	stem := episodeFilePath[:len(episodeFilePath)-len(ext)]
	stem = strings.TrimRight(stem, ".")
	return stem + "-thumb.jpg"
}

// getRelativePath mirrors C# `PathExtensions.GetRelativePath` at
// `src/NzbDrone.Common/Extensions/PathExtensions.cs:85-93`. Returns
// the suffix of childPath after parentPath, with leading slashes
// stripped. Errors when childPath is not a child of parentPath.
//
// The C# tree throws a `NotParentException`; the Go port returns
// a typed error so the consumer-facing scanner can log + skip.
func getRelativePath(parentPath, childPath string) (string, error) {
	parent := filepath.Clean(parentPath)
	child := filepath.Clean(childPath)
	if parent == "" || child == "" {
		return "", errors.New("xbmc: getRelativePath: empty parent or child")
	}
	if !strings.HasPrefix(child, parent) {
		return "", errors.New("xbmc: getRelativePath: child is not under parent")
	}
	suffix := child[len(parent):]
	return strings.Trim(suffix, `/\`), nil
}
