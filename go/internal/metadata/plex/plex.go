package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// providerDisplayName is the user-facing name the v3 UI shows for
// this writer. Mirrors C# `PlexMetadata.Name` at
// `PlexMetadata.cs:14`.
const providerDisplayName = "Plex"

// plexMatchFilename is the single sidecar Plex looks for at the
// series root. Hard-coded both in the C# tree (PlexMetadata.cs:33)
// and on the Plex side.
const plexMatchFilename = ".plexmatch"

// MappingsLookup narrows the per-writer dep for the optional
// EpisodeMappings extension. Used by [PlexMetadata.SeriesMetadata]
// only when [Settings.EpisodeMappings] is true.
//
// Production binding wraps `*episodes.Repo` + `*episodefiles.Repo`
// via Go structural typing — both have AllBySeriesIDs methods.
// `*Adapter` could collapse both lookups behind one struct; the
// minimal interface here keeps the dep narrow for tests.
type MappingsLookup interface {
	// EpisodesBySeries returns every episode row for the given series.
	// Mirrors C# `IEpisodeService.GetEpisodeBySeries(int)` —
	// internally backed by `episodes.Repo.AllBySeriesIDs([id])`.
	EpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)

	// FilesBySeries returns every persisted episode-file row for the
	// given series. Mirrors C# `IMediaFileService.GetFilesBySeries(int)`
	// — internally backed by `episodefiles.Repo.AllBySeriesIDs([id])`.
	FilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// Deps bundles the plex-specific narrow deps the [PlexMetadata]
// constructor accepts. Both fields are nil-tolerant — when Mappings
// is nil, the EpisodeMappings extension simply emits no mapping
// lines (a graceful degradation that mirrors the C# fallback path
// for tests + the read-only-bootstrap edge case).
type Deps struct {
	// Mappings is the episode + episode-file lookup used by the
	// optional EpisodeMappings extension to `.plexmatch`. nil →
	// EpisodeMappings setting is honoured for the title/year/tvdb
	// header lines but no per-episode `Episode:` lines are appended.
	Mappings MappingsLookup
}

// PlexMetadata ports the C# concrete `PlexMetadata` class at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Plex/PlexMetadata.cs:12-100`.
// Implements [metadata.Provider].
//
// Plex's `.plexmatch` is a single text manifest at the series root.
// Per-episode NFO sidecars and per-cover images are NOT emitted by
// this writer — the C# tree returns null / empty list from
// EpisodeMetadata + SeriesImages + SeasonImages + EpisodeImages.
// The Go port preserves that behaviour.
//
// Goroutine-safe: stateless after construction.
type PlexMetadata struct {
	definition *metadata.Metadata
	settings   Settings
	mappings   MappingsLookup
}

// NewPlexMetadata builds a [PlexMetadata] from a persisted
// [metadata.Metadata] row + per-writer Settings + [Deps] bundle.
// No required deps — the constructor never errors today (kept
// signature with error return for API parity with the other writer
// constructors so a future C# rule addition doesn't break callers).
func NewPlexMetadata(definition *metadata.Metadata, settings Settings, deps Deps) (*PlexMetadata, error) {
	return &PlexMetadata{
		definition: definition,
		settings:   settings,
		mappings:   deps.Mappings,
	}, nil
}

// Name returns the user-facing display label. Mirrors C# `Name`
// at `PlexMetadata.cs:14` — "Plex".
func (m *PlexMetadata) Name() string {
	return providerDisplayName
}

// Definition returns the persisted Metadata row backing this provider
// instance. Mirrors C# `Definition` (inherited from MetadataBase).
func (m *PlexMetadata) Definition() *metadata.Metadata {
	if m == nil {
		return nil
	}
	return m.definition
}

// SettingsConfig returns the per-instance Settings struct. Exposed
// for tests + the consumer-facing service.
func (m *PlexMetadata) SettingsConfig() Settings {
	if m == nil {
		return Settings{}
	}
	return m.settings
}

// GetFilenameAfterMove returns the C# `MetadataBase.GetFilenameAfterMove`
// default — `series.Path` + `metadataFile.RelativePath`. PlexMetadata
// does NOT override this; the .plexmatch file lives at the series
// root and doesn't track per-episode renames.
//
// nil-receiver / nil-args return "" rather than panicking.
func (m *PlexMetadata) GetFilenameAfterMove(s *series.Series, _ *episodefiles.File, metadataFile *metadatafiles.MetadataFile) string {
	if m == nil || s == nil || metadataFile == nil {
		return ""
	}
	return filepath.Join(s.Path, metadataFile.RelativePath)
}

// FindMetadataFile classifies an on-disk file at `path` as the
// Plex `.plexmatch` sidecar. Mirrors C# `PlexMetadata.FindMetadataFile`
// at `PlexMetadata.cs:16-39`.
//
// Returns nil-row + nil-err when the file is NOT `.plexmatch` at the
// series root (the only Plex-recognised filename). Returns an error
// when the path is not a child of series.Path (mirrors the C#
// `GetRelativePath` NotParentException via a typed error return).
func (m *PlexMetadata) FindMetadataFile(s *series.Series, path string) (*metadatafiles.MetadataFile, error) {
	if m == nil || s == nil || path == "" {
		return nil, nil
	}
	relativePath, err := getRelativePath(s.Path, path)
	if err != nil {
		return nil, err
	}
	if relativePath != plexMatchFilename {
		return nil, nil
	}
	return &metadatafiles.MetadataFile{
		SeriesID:     s.ID,
		Consumer:     ImplementationName,
		RelativePath: relativePath,
		Type:         int(metadata.MetadataTypeSeriesMetadata),
	}, nil
}

// SeriesMetadata builds the `.plexmatch` text manifest. Mirrors C#
// `PlexMetadata.SeriesMetadata` at `PlexMetadata.cs:41-79`.
//
// Body shape (line-by-line):
//
//	Title: <title>
//	Year: <year>
//	TvdbId: <tvdb id>
//	ImdbId: <imdb id>
//	[Episode: <fmt>: <relativePath>]  (zero or more, only when
//	                                    EpisodeMappings && deps.Mappings non-nil)
//
// Episode-mapping format:
//
//   - Season > 0: `S{seasonNumber:00}E{n1:00}[-E{n2:00}…]`
//     joined by "-" for multi-episode files. C# `:69`.
//   - Season 0 (specials): `SP{first.EpisodeNumber:00}` — only the
//     first episode's number, no multi-episode join. C# `:71-74`.
//
// SeriesMetadataReason has no effect — the .plexmatch body is the
// same regardless of trigger reason (unlike the XbmcMetadata which
// skips on EpisodesImported). Documented divergence from C# (which
// also has no reason-gate at this method); same outcome here.
//
// Returns zero-value when [Settings.SeriesPlexMatchFile] is false
// (the C# null-return contract).
func (m *PlexMetadata) SeriesMetadata(ctx context.Context, s *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	if m == nil || s == nil || !m.settings.SeriesPlexMatchFile {
		return metadata.MetadataFileResult{}, nil
	}

	var body strings.Builder
	body.WriteString("Title: ")
	body.WriteString(s.Title)
	body.WriteString("\n")

	body.WriteString("Year: ")
	body.WriteString(strconv.Itoa(s.Year))
	body.WriteString("\n")

	body.WriteString("TvdbId: ")
	body.WriteString(strconv.Itoa(s.TvdbID))
	body.WriteString("\n")

	body.WriteString("ImdbId: ")
	body.WriteString(s.ImdbID)
	body.WriteString("\n")

	if m.settings.EpisodeMappings && m.mappings != nil {
		mappingLines, err := buildEpisodeMappingLines(ctx, s.ID, m.mappings)
		if err != nil {
			return metadata.MetadataFileResult{}, err
		}
		for _, line := range mappingLines {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}

	return metadata.NewMetadataFileResult(plexMatchFilename, body.String()), nil
}

// EpisodeMetadata always returns zero-value. Plex doesn't consume
// per-episode NFO sidecars. C# `PlexMetadata.EpisodeMetadata` at
// `PlexMetadata.cs:81-84` returns null unconditionally.
func (m *PlexMetadata) EpisodeMetadata(_ context.Context, _ *series.Series, _ *episodefiles.File) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

// SeriesImages always returns an empty slice. C# `PlexMetadata.SeriesImages`
// at `PlexMetadata.cs:86-89` returns an empty list.
func (m *PlexMetadata) SeriesImages(_ context.Context, _ *series.Series) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

// SeasonImages always returns an empty slice. C# `:91-94`.
func (m *PlexMetadata) SeasonImages(_ context.Context, _ *series.Series, _ metadata.Season) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

// EpisodeImages always returns an empty slice. C# `:96-99`.
func (m *PlexMetadata) EpisodeImages(_ context.Context, _ *series.Series, _ *episodefiles.File) ([]metadata.ImageFileResult, error) {
	return nil, nil
}

// Test is the per-provider connection self-check. Mirrors C#
// `MetadataBase.Test()` at `MetadataBase.cs:25-28` — the base impl
// returns an empty ValidationResult. The Go port returns
// settings.Validate() (always nil for PlexMetadata today).
func (m *PlexMetadata) Test(_ context.Context) ([]metadata.TestFailure, error) {
	if m == nil {
		return nil, nil
	}
	return m.settings.Validate(), nil
}

// RequestAction handles the OAuth-style v3-UI handshake. Mirrors C#
// `MetadataBase.RequestAction` — base impl returns null, PlexMetadata
// does not override.
func (m *PlexMetadata) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard that PlexMetadata satisfies the
// [metadata.Provider] interface contract. Catches signature drift if
// the foundation interface changes.
var _ metadata.Provider = (*PlexMetadata)(nil)

// buildEpisodeMappingLines builds the per-episode `Episode: <fmt>:
// <relativePath>` lines for the `.plexmatch` body. Mirrors C#
// `PlexMetadata.SeriesMetadata` lines `:60-77`.
//
// Algorithm:
//  1. Fetch every episode-file row for the series via
//     [MappingsLookup.FilesBySeries];
//  2. Fetch every episode row for the series via
//     [MappingsLookup.EpisodesBySeries];
//  3. For each episode-file: filter the episodes by
//     `EpisodeFileID == file.ID` and emit a single `Episode:` line.
//
// Episode ordering within a file is by EpisodeNumber ascending so
// multi-episode files render as "E01-E02" not "E02-E01". The C# tree
// relies on the natural order of the episodes collection which is
// usually sorted; the Go port pins this explicitly for stability.
//
// nil receiver / fetch errors propagate up so the caller (and
// ultimately the orchestrator) can surface them.
func buildEpisodeMappingLines(ctx context.Context, seriesID int, lookup MappingsLookup) ([]string, error) {
	if lookup == nil {
		return nil, nil
	}
	files, err := lookup.FilesBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("plex: FilesBySeries(%d): %w", seriesID, err)
	}
	if len(files) == 0 {
		return nil, nil
	}
	eps, err := lookup.EpisodesBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("plex: EpisodesBySeries(%d): %w", seriesID, err)
	}

	// Group episodes by EpisodeFileID for an O(1) per-file lookup.
	byFile := make(map[int][]episodes.Episode, len(files))
	for _, e := range eps {
		if e.EpisodeFileID == nil {
			continue
		}
		byFile[*e.EpisodeFileID] = append(byFile[*e.EpisodeFileID], e)
	}

	out := make([]string, 0, len(files))
	for _, file := range files {
		episodesInFile := byFile[file.ID]
		if len(episodesInFile) == 0 {
			// C# `episodesInFile` would be an empty collection here;
			// the formatter at `:67` produces "S01" with no episode
			// numbers. The Go port skips the file entirely — empty
			// `S01: <path>` is meaningless to Plex's scanner.
			continue
		}
		// Sort ascending by EpisodeNumber for stable multi-episode
		// rendering. C# relies on natural ordering; pin explicitly.
		sort.Slice(episodesInFile, func(i, j int) bool {
			return episodesInFile[i].EpisodeNumber < episodesInFile[j].EpisodeNumber
		})
		out = append(out, "Episode: "+formatEpisodeMapping(file, episodesInFile)+": "+file.RelativePath)
	}
	return out, nil
}

// formatEpisodeMapping builds the `<season>E<n1>[-E<n2>...]` or
// `SP<n>` portion of the Episode line. Mirrors C# `:65-74`.
func formatEpisodeMapping(file episodefiles.File, episodesInFile []episodes.Episode) string {
	if file.SeasonNumber == 0 {
		// SP<n> — only the first episode's number. C# `:71-74`.
		return fmt.Sprintf("SP%02d", episodesInFile[0].EpisodeNumber)
	}
	// S<seasonNumber>E<n1>[-E<n2>...]
	var b strings.Builder
	fmt.Fprintf(&b, "S%02d", file.SeasonNumber)
	for i, e := range episodesInFile {
		if i > 0 {
			b.WriteString("-")
		}
		fmt.Fprintf(&b, "E%02d", e.EpisodeNumber)
	}
	return b.String()
}

// getRelativePath mirrors C# `PathExtensions.GetRelativePath` at
// `src/NzbDrone.Common/Extensions/PathExtensions.cs:85-93`. Returns
// the suffix of childPath after parentPath, with leading slashes
// stripped. Errors when childPath is not a child of parentPath.
//
// Local copy of the xbmc-package helper at xbmc.go (deliberate small
// duplication to keep this package's import surface narrow — the
// helper is one-line trivial).
func getRelativePath(parentPath, childPath string) (string, error) {
	parent := filepath.Clean(parentPath)
	child := filepath.Clean(childPath)
	if parent == "" || child == "" {
		return "", errors.New("plex: getRelativePath: empty parent or child")
	}
	if !strings.HasPrefix(child, parent) {
		return "", errors.New("plex: getRelativePath: child is not under parent")
	}
	suffix := child[len(parent):]
	return strings.Trim(suffix, `/\`), nil
}

// init registers the PlexMetadata constructor with the package-global
// [metadata.Default] registry. The consumer-facing service (queue
// [W6]) calls Registry.Construct at dispatch time to instantiate a
// PlexMetadata from a persisted Metadata row.
func init() {
	metadata.Default().MustRegister(ImplementationName, ConfigContractName, constructFromDependencies)
}

// constructFromDependencies is the exported [metadata.Constructor]
// wrapper. Factored out of init() so tests can call it directly.
//
// The PlexMetadata Mappings dep doesn't have a dedicated field on
// [metadata.Dependencies] today — the orchestrator (queue [W6]) will
// add one. For now the constructor leaves Mappings nil; the
// EpisodeMappings extension is a no-op until W6 threads the lookup
// through.
func constructFromDependencies(definition *metadata.Metadata, _ metadata.Dependencies) (metadata.Provider, error) {
	settings, err := decodeSettings(definition)
	if err != nil {
		return nil, err
	}
	return NewPlexMetadata(definition, settings, Deps{})
}

// decodeSettings deserialises the persisted Settings JSON from
// `Metadata.Settings`. Empty / blank / "{}" → [DefaultSettings]
// matching the C# `ProviderFactory.GetInstance` fallback shape.
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
		return Settings{}, fmt.Errorf("plex: decodeSettings: %w", err)
	}
	return s, nil
}
