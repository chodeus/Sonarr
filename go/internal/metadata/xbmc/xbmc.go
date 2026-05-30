package xbmc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/tags"
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
	coverPath  CoverPathLookup
	episodes   EpisodeLookup
	tags       TagLookup
}

// CoverPathLookup is the narrow per-writer dep wrapping
// [mediacover.Service.GetCoverPath]. The production binding is a
// `*mediacover.Service` (the Go-port port of the C# `IMapCoversToLocal`
// shim at `MediaCover/IMapCoversToLocal.cs:6-10`).
//
// Used by [XbmcMetadata.SeriesImages] only — Season and Episode
// images pull from `image.RemoteUrl` per C# (writer-author chose
// remote-URL there). The series-level path uses the locally-cached
// cover so the consumer-facing service can copy from disk rather
// than re-download. Documented divergence: the writer emits
// `[metadata.ImageFileResult.URL] = <local-file-path>` for series
// images; the orchestrator (queue [W6]) handles the local-vs-remote
// dispatch.
type CoverPathLookup interface {
	// GetCoverPath returns the absolute local-disk path of a
	// per-series cover. height==0 yields the full-resolution master.
	// Mirrors C# `IMapCoversToLocal.GetCoverPath(seriesId, coverType)`
	// at `MediaCover/MediaCoverService.cs:66-71`.
	GetCoverPath(seriesID int, coverType mediacover.CoverType, height int) string
}

// EpisodeLookup narrows the per-writer dep for episode-by-file
// lookup. Used by [XbmcMetadata.EpisodeImages] to find the episodes
// linked to a given EpisodeFile, mirroring the C# `Lazy<List<Episode>>`
// accessor at `MediaFiles/EpisodeFile.cs::Episodes`.
//
// Production binding wraps an `*episodes.Repo` via Go structural
// typing — `Repo.AllByFileID` already satisfies this contract.
type EpisodeLookup interface {
	// AllByFileID returns every episode linked to the given
	// EpisodeFile row. Mirrors the lazy-load semantics of
	// `episodeFile.Episodes.Value` at C# `XbmcMetadata.cs:301`.
	AllByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// TagLookup narrows the per-writer dep for resolving the integer
// `series.Tags` slice into the typed [tags.Tag] rows. Used by
// [XbmcMetadata.SeriesMetadata] (sub-arc W2c-i) to render `<tag>`
// elements with each tag's Label.
//
// Production binding wraps `*tags.Service` via Go structural typing
// — `Service.GetByIDs` already satisfies this contract.
type TagLookup interface {
	// GetByIDs returns the [tags.Tag] rows matching the input IDs.
	// Missing rows are silently skipped (matches the C#
	// `_tagRepo.GetTags(series.Tags)` contract at
	// `XbmcMetadata.cs:200`). Empty input → empty result.
	GetByIDs(ctx context.Context, ids []int) ([]tags.Tag, error)
}

// Deps bundles the xbmc-specific narrow deps the [XbmcMetadata]
// constructor requires. Fields are nil-tolerant — the writer
// degrades gracefully when an optional dep is unset (e.g. missing
// CoverPath turns SeriesImages into a no-op rather than a panic),
// matching the C# `MetadataBase` defensive style.
//
// Detector is the only required dep — FindMetadataFile relies on it
// to classify on-disk .nfo files. Construction errors when nil.
type Deps struct {
	// Detector is the [*NfoDetector] used by FindMetadataFile to
	// sniff Kodi-format NFO bodies on disk. REQUIRED.
	Detector *NfoDetector

	// CoverPath is the local-cover-path lookup [SeriesImages]
	// consults. nil → SeriesImages returns empty.
	CoverPath CoverPathLookup

	// Episodes is the episode-by-file lookup [EpisodeImages]
	// consults. nil → EpisodeImages returns empty.
	Episodes EpisodeLookup

	// Tags is the tag-row lookup [SeriesMetadata] consults to render
	// `<tag>` elements. nil → no `<tag>` elements emitted (the C#
	// behaviour at `XbmcMetadata.cs:198-206` is to skip the tag
	// block when series.Tags is empty — same outcome).
	Tags TagLookup
}

// NewXbmcMetadata builds an [XbmcMetadata] from a persisted
// [metadata.Metadata] row, per-writer Settings, and the [Deps]
// bundle. Mirrors the C# `XbmcMetadata` constructor at
// `XbmcMetadata.cs:32-43` (IDetectXbmcNfo + IDiskProvider +
// IMapCoversToLocal + ITagRepository + Logger).
//
// nil Deps.Detector returns a typed error; the other deps are
// nil-tolerant (see [Deps]).
func NewXbmcMetadata(definition *metadata.Metadata, settings Settings, deps Deps) (*XbmcMetadata, error) {
	if deps.Detector == nil {
		return nil, errors.New("xbmc: NewXbmcMetadata: deps.Detector is nil")
	}
	return &XbmcMetadata{
		definition: definition,
		settings:   settings,
		detector:   deps.Detector,
		coverPath:  deps.CoverPath,
		episodes:   deps.Episodes,
		tags:       deps.Tags,
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

// EpisodeMetadata lives in episode_metadata.go (slice 4.BE).

// SeriesImages emits one [metadata.ImageFileResult] per cover in
// series.Images (decoded from the JSON-blob `series.Series.Images`).
//
// Mirrors C# `XbmcMetadata.SeriesImages` + `ProcessSeriesImages` at
// `XbmcMetadata.cs:425-433` + `:483-492`:
//
//   - Returns empty when [Settings.SeriesImages] is false.
//   - Iterates series.Images;
//   - source = [CoverPathLookup.GetCoverPath] (local cached file
//     path — NOT a remote URL) for the (seriesID, coverType) pair;
//   - destination = `<coverType>.<extension>` lower-cased
//     (e.g. `poster.jpg`, `clearlogo.png`). The extension is derived
//     from the source path's extension matching C# `Path.GetExtension(source)`.
//
// nil [CoverPathLookup] → empty slice (defensive — never panic on
// missing dep). Documented divergence from C# where the dep is
// REQUIRED.
func (m *XbmcMetadata) SeriesImages(_ context.Context, s *series.Series) ([]metadata.ImageFileResult, error) {
	if m == nil || s == nil || !m.settings.SeriesImages || m.coverPath == nil {
		return nil, nil
	}
	covers, err := decodeCovers(s.Images)
	if err != nil {
		return nil, fmt.Errorf("xbmc: SeriesImages: decode series.Images: %w", err)
	}
	out := make([]metadata.ImageFileResult, 0, len(covers))
	for _, image := range covers {
		source := m.coverPath.GetCoverPath(s.ID, image.CoverType, 0)
		destination := image.CoverType.String() + filepath.Ext(source)
		out = append(out, metadata.NewImageFileResult(destination, source))
	}
	return out, nil
}

// SeasonImages emits one [metadata.ImageFileResult] per cover in
// season.Images.
//
// Mirrors C# `XbmcMetadata.SeasonImages` + `ProcessSeasonImages` at
// `XbmcMetadata.cs:435-443` + `:494-507`:
//
//   - Returns empty when [Settings.SeasonImages] is false.
//   - Filename for non-zero season is
//     `seasonNN-<covertype>.jpg` (zero-padded to 2 digits);
//   - Filename for season 0 (specials) is
//     `season-specials-<covertype>.jpg`.
//   - The URL field uses `image.RemoteUrl` — NOT the local cover
//     path (unlike SeriesImages). The consumer-facing service
//     handles the download. Documented C# divergence inside the
//     writer family.
func (m *XbmcMetadata) SeasonImages(_ context.Context, _ *series.Series, season metadata.Season) ([]metadata.ImageFileResult, error) {
	if m == nil || !m.settings.SeasonImages {
		return nil, nil
	}
	out := make([]metadata.ImageFileResult, 0, len(season.Images))
	for _, image := range season.Images {
		var filename string
		if season.SeasonNumber == 0 {
			filename = "season-specials-" + image.CoverType.String() + ".jpg"
		} else {
			filename = fmt.Sprintf("season%02d-%s.jpg", season.SeasonNumber, image.CoverType.String())
		}
		out = append(out, metadata.NewImageFileResult(filename, image.RemoteUrl))
	}
	return out, nil
}

// EpisodeImages emits one [metadata.ImageFileResult] for the
// per-episode screenshot — `<filename>-thumb.jpg` next to the
// imported episode file.
//
// Mirrors C# `XbmcMetadata.EpisodeImages` at `XbmcMetadata.cs:445-481`:
//
//   - Returns empty when [Settings.EpisodeImages] is false.
//   - Looks up the linked episodes via [EpisodeLookup.AllByFileID];
//     no episodes → empty (matches C# "no associated episodes,
//     potentially a duplicate file" branch at `:457-460`).
//   - Decodes the FIRST episode's Images JSON-blob, finds the
//     mediacover.CoverType.Screenshot entry; missing → empty.
//   - Filename = stem-of-episodeFile.RelativePath + "-thumb.jpg",
//     via [getEpisodeImageFilename].
//   - URL = `screenshot.RemoteUrl` (NOT a local path; the
//     consumer-facing service downloads).
//
// Any lookup-error short-circuits to empty + nil to match C# `:475-481`
// (the C# code catches any exception and returns empty). Documented
// divergence: the Go port returns the lookup error verbatim so the
// orchestrator can log; a partial-failure mode is preferred over the
// C# "swallow everything" behaviour. Test verifies the behaviour.
func (m *XbmcMetadata) EpisodeImages(ctx context.Context, _ *series.Series, episodeFile *episodefiles.File) ([]metadata.ImageFileResult, error) {
	if m == nil || episodeFile == nil || !m.settings.EpisodeImages || m.episodes == nil {
		return nil, nil
	}
	linked, err := m.episodes.AllByFileID(ctx, episodeFile.ID)
	if err != nil {
		return nil, fmt.Errorf("xbmc: EpisodeImages: lookup episodes for fileID=%d: %w", episodeFile.ID, err)
	}
	if len(linked) == 0 {
		return nil, nil
	}
	first := linked[0]
	covers, decodeErr := decodeCovers(first.Images)
	if decodeErr != nil {
		return nil, fmt.Errorf("xbmc: EpisodeImages: decode episode.Images: %w", decodeErr)
	}
	for _, cover := range covers {
		if cover.CoverType != mediacover.CoverTypeScreenshot {
			continue
		}
		filename := getEpisodeImageFilename(episodeFile.RelativePath)
		return []metadata.ImageFileResult{metadata.NewImageFileResult(filename, cover.RemoteUrl)}, nil
	}
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

// decodeCovers parses a JSON-blob string (series.Images / episode.Images
// shape) into a typed [mediacover.Cover] slice. Empty / blank input
// yields nil + nil-error — matches the C# `null`-Images branch where
// the writer iterates over an empty collection.
//
// Malformed JSON yields a wrapped error so the caller can surface
// the per-row parse failure (different rows may have valid JSON;
// the orchestrator decides whether to abort the writer or skip).
func decodeCovers(jsonBlob string) ([]mediacover.Cover, error) {
	trimmed := strings.TrimSpace(jsonBlob)
	if trimmed == "" {
		return nil, nil
	}
	var covers []mediacover.Cover
	if err := json.Unmarshal([]byte(trimmed), &covers); err != nil {
		return nil, fmt.Errorf("xbmc: decodeCovers: %w", err)
	}
	return covers, nil
}

// init registers the XbmcMetadata constructor with the package-global
// [metadata.Default] registry. Concurrent-safe — [metadata.Registry]
// guards the entries map with a sync.RWMutex.
//
// The constructor wraps [NewXbmcMetadata] — it:
//  1. Deserialises the Settings JSON from the persisted
//     Metadata.Settings column;
//  2. Extracts the CoverPath / Episodes / Detector deps from the
//     [metadata.Dependencies] bundle's `any`-typed fields (CoverPath
//     from MapCoversToLocal; Episodes from a new top-level field
//     that future writers can also consume; Detector built ad-hoc
//     from DiskProvider since the C# tree carries a dedicated
//     IDetectXbmcNfo singleton, which the Go port reconstructs at
//     construct time).
//  3. Invokes [NewXbmcMetadata] with the assembled [Deps] bundle.
//
// Dep extraction is permissive — missing optional deps (CoverPath /
// Episodes) yield a writer instance whose corresponding methods
// degrade to empty, matching the [Deps] nil-tolerant contract.
// The Detector dep is mandatory; missing Detector means we can't
// run FindMetadataFile — the constructor returns a typed error.
//
// Production wiring lives in main.go (queue [W6]+ slice — the
// consumer-facing service hooks up disk.Provider + mediacover.Service
// + episodes.Repo and threads them through metadata.Dependencies).
func init() {
	metadata.Default().MustRegister(ImplementationName, ConfigContractName, constructFromDependencies)
}

// constructFromDependencies is the exported [metadata.Constructor]
// wrapper. Factored out of init() so tests can call it directly.
func constructFromDependencies(definition *metadata.Metadata, deps metadata.Dependencies) (metadata.Provider, error) {
	settings, err := decodeSettings(definition)
	if err != nil {
		return nil, err
	}

	xbmcDeps := Deps{}

	// DiskProvider → NfoDetector. Required (FindMetadataFile path).
	if fs, ok := deps.DiskProvider.(NfoFilesystem); ok {
		detector, dErr := NewNfoDetector(fs)
		if dErr != nil {
			return nil, fmt.Errorf("xbmc: construct: %w", dErr)
		}
		xbmcDeps.Detector = detector
	} else {
		return nil, errors.New("xbmc: construct: deps.DiskProvider does not satisfy NfoFilesystem (need GetFileSize + ReadAllText)")
	}

	// MapCoversToLocal → CoverPathLookup. Optional.
	if cp, ok := deps.MapCoversToLocal.(CoverPathLookup); ok {
		xbmcDeps.CoverPath = cp
	}

	// EpisodeLookup → EpisodeLookup (episode-by-file). Optional; nil →
	// EpisodeImages degrades to empty. J473 wired the bundle field.
	if el, ok := deps.EpisodeLookup.(EpisodeLookup); ok {
		xbmcDeps.Episodes = el
	}

	return NewXbmcMetadata(definition, settings, xbmcDeps)
}

// decodeSettings deserialises the persisted Settings JSON from
// `Metadata.Settings`. Empty / blank string → [DefaultSettings]
// (matches the C# `ProviderFactory.GetInstance` "create with default
// settings when the persisted blob is missing" fallback at the
// ThingiProvider base class).
func decodeSettings(definition *metadata.Metadata) (Settings, error) {
	if definition == nil {
		return DefaultSettings(), nil
	}
	blob := strings.TrimSpace(definition.Settings)
	if blob == "" || blob == "{}" {
		return DefaultSettings(), nil
	}
	var s Settings
	if err := json.Unmarshal([]byte(blob), &s); err != nil {
		return Settings{}, fmt.Errorf("xbmc: decodeSettings: %w", err)
	}
	return s, nil
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
