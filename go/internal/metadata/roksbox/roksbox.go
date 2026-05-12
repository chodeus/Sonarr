package roksbox

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
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
)

// providerDisplayName is the user-facing name the v3 UI shows for
// this writer. Mirrors C# `RoksboxMetadata.Name` at
// `RoksboxMetadata.cs:30`.
const providerDisplayName = "Roksbox"

// seasonFolderRegex classifies a season directory name as either
// `season N` (any digits) or `specials`. Mirrors C# `:33` exactly —
// case-insensitive, two-arm match. `(?P<season>\d+)` named-capture
// uses RE2 syntax (Go's encoding for .NET-style names).
var seasonFolderRegex = regexp.MustCompile(`(?i)^(season (?P<season>\d+))|(?P<specials>specials)`)

// validCertifications is the allow-list the writer uses to validate
// `series.Certification` before emitting `<mpaa>`. Mirrors C# `:34`
// — non-matching certifications emit "UNRATED" instead. The list is
// upper-case for case-insensitive matching.
var validCertifications = map[string]struct{}{
	"G": {}, "NC-17": {}, "PG": {}, "PG-13": {}, "R": {}, "UR": {},
	"UNRATED": {}, "NR": {},
	"TV-Y": {}, "TV-Y7": {}, "TV-Y7-FV": {},
	"TV-G": {}, "TV-PG": {}, "TV-14": {}, "TV-MA": {},
}

// CoverPathLookup wraps [mediacover.Service.GetCoverPath] — the
// per-series local-cover-path resolver used by [RoksboxMetadata.SeriesImages].
// Mirrors C# `IMapCoversToLocal.GetCoverPath` semantics.
type CoverPathLookup interface {
	GetCoverPath(seriesID int, coverType mediacover.CoverType, height int) string
}

// EpisodeLookup narrows the per-writer dep for episode-by-file
// lookup. Used by [RoksboxMetadata.EpisodeMetadata] /
// [RoksboxMetadata.EpisodeImages] — mirrors the C# `episodeFile.Episodes.Value`
// lazy-load.
//
// Production binding wraps `*episodes.Repo` via Go structural typing.
type EpisodeLookup interface {
	AllByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// FolderLister narrows the disk surface [RoksboxMetadata.SeasonImages]
// needs to enumerate season directories under the series root.
// Mirrors C# `IDiskProvider.GetDirectories(seriesPath)`.
//
// Production binding wraps `*disk.Provider` via Go structural typing
// (the production method's signature `GetDirectories(string) ([]string,
// error)` matches this contract).
type FolderLister interface {
	GetDirectories(seriesPath string) ([]string, error)
}

// Deps bundles the roksbox-specific narrow deps. All fields are
// nil-tolerant — when an optional dep is unset the corresponding
// method degrades to empty output (matches MetadataBase defensive
// style).
type Deps struct {
	// CoverPath is the local-cover-path lookup used by SeriesImages.
	// nil → SeriesImages returns empty.
	CoverPath CoverPathLookup

	// Episodes is the episode-by-file lookup used by EpisodeMetadata
	// + EpisodeImages. nil → both methods return empty.
	Episodes EpisodeLookup

	// Folders is the disk directory lister used by SeasonImages to
	// resolve `season N` / `specials` directory names. nil →
	// SeasonImages returns empty.
	Folders FolderLister
}

// RoksboxMetadata ports the C# concrete `RoksboxMetadata` class at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Roksbox/RoksboxMetadata.cs:13-258`.
// Implements [metadata.Provider].
//
// Sidecar emission shape:
//   - SeriesMetadata: NEVER (C# `:118-121` always returns null).
//   - EpisodeMetadata: per-episode `<video>` XML sub-document in
//     `<stem>.xml` next to the episode file. Multi-episode files
//     emit multiple `<video>` blocks separated by newlines.
//   - SeriesImages: `<seriesFolderName>.<ext>` at series root.
//   - SeasonImages: `<seasonFolderName>.jpg` inside each season folder.
//   - EpisodeImages: `<stem>.jpg` next to the episode file (no
//     "-thumb" suffix unlike Xbmc).
type RoksboxMetadata struct {
	definition *metadata.Metadata
	settings   Settings
	coverPath  CoverPathLookup
	episodes   EpisodeLookup
	folders    FolderLister
}

// NewRoksboxMetadata builds a [RoksboxMetadata] from a persisted
// [metadata.Metadata] row + Settings + [Deps] bundle. Like Plex, no
// required deps — every optional dep degrades to empty output when
// nil.
func NewRoksboxMetadata(definition *metadata.Metadata, settings Settings, deps Deps) (*RoksboxMetadata, error) {
	return &RoksboxMetadata{
		definition: definition,
		settings:   settings,
		coverPath:  deps.CoverPath,
		episodes:   deps.Episodes,
		folders:    deps.Folders,
	}, nil
}

// Name returns the user-facing display label. Mirrors C# `Name` at
// `RoksboxMetadata.cs:30`.
func (m *RoksboxMetadata) Name() string {
	return providerDisplayName
}

// Definition returns the persisted Metadata row backing this provider
// instance.
func (m *RoksboxMetadata) Definition() *metadata.Metadata {
	if m == nil {
		return nil
	}
	return m.definition
}

// SettingsConfig returns the per-instance Settings struct.
func (m *RoksboxMetadata) SettingsConfig() Settings {
	if m == nil {
		return Settings{}
	}
	return m.settings
}

// GetFilenameAfterMove dispatches on metadataFile.Type — mirrors C#
// `RoksboxMetadata.GetFilenameAfterMove` at `:32-46`. The C# default
// fall-through path `Path.Combine(series.Path, metadataFile.RelativePath)`
// is preserved for non-episode-derivative types (series + season
// images, which the C# tree carries unchanged across renames).
func (m *RoksboxMetadata) GetFilenameAfterMove(s *series.Series, episodeFile *episodefiles.File, metadataFile *metadatafiles.MetadataFile) string {
	if m == nil || s == nil || episodeFile == nil || metadataFile == nil {
		return ""
	}
	episodeFilePath := filepath.Join(s.Path, episodeFile.RelativePath)
	switch metadata.MetadataType(metadataFile.Type) {
	case metadata.MetadataTypeEpisodeImage:
		return swapExtension(episodeFilePath, ".jpg")
	case metadata.MetadataTypeEpisodeMetadata:
		return swapExtension(episodeFilePath, ".xml")
	}
	return filepath.Join(s.Path, metadataFile.RelativePath)
}

// FindMetadataFile classifies an on-disk file at `path`. Mirrors C#
// `RoksboxMetadata.FindMetadataFile` at `:48-100`.
//
// Classification (in C# evaluation order — first match wins):
//   - filename-stem matches parent-directory name (case-insensitive):
//   - parent name matches seasonFolderRegex → SeasonImage
//     with SeasonNumber parsed from the `season N` capture or
//     0 for `specials`;
//   - else → SeriesImage.
//   - parser.ParseTitle succeeds AND !FullSeason AND ext is .xml
//     → EpisodeMetadata.
//   - parser.ParseTitle succeeds AND !FullSeason AND ext is .jpg
//     AND stem does NOT end with "-thumb" AND parent dir name is
//     NOT "metadata" → EpisodeImage.
//   - Else nil.
//
// Non-child-of-series-path returns a typed error.
func (m *RoksboxMetadata) FindMetadataFile(s *series.Series, path string) (*metadatafiles.MetadataFile, error) {
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
	parentDir := filepath.Base(filepath.Dir(path))
	stem := stripExtension(filename)
	ext := strings.ToLower(filepath.Ext(filename))

	base := &metadatafiles.MetadataFile{
		SeriesID:     s.ID,
		Consumer:     ImplementationName,
		RelativePath: relativePath,
	}

	// The C# `:81-101` arm: filename-stem matches parent-dir name
	// → SeriesImage or SeasonImage based on whether the parent name
	// matches the season-folder regex.
	if strings.EqualFold(stem, parentDir) {
		if match := seasonFolderRegex.FindStringSubmatch(parentDir); match != nil {
			base.Type = int(metadata.MetadataTypeSeasonImage)
			if specialsIdx := seasonFolderRegex.SubexpIndex("specials"); specialsIdx >= 0 && match[specialsIdx] != "" {
				zero := 0
				base.SeasonNumber = &zero
			} else if seasonIdx := seasonFolderRegex.SubexpIndex("season"); seasonIdx >= 0 && match[seasonIdx] != "" {
				n, parseErr := strconv.Atoi(match[seasonIdx])
				if parseErr != nil {
					return nil, nil
				}
				base.SeasonNumber = &n
			}
			return base, nil
		}
		base.Type = int(metadata.MetadataTypeSeriesImage)
		return base, nil
	}

	parseResult := parser.ParseTitle(filename)
	if parseResult == nil || parseResult.FullSeason {
		return nil, nil
	}

	if ext == ".xml" {
		base.Type = int(metadata.MetadataTypeEpisodeMetadata)
		return base, nil
	}

	if ext == ".jpg" {
		if !strings.HasSuffix(stem, "-thumb") && !strings.EqualFold(parentDir, "metadata") {
			base.Type = int(metadata.MetadataTypeEpisodeImage)
			return base, nil
		}
	}

	return nil, nil
}

// SeriesMetadata always returns zero-value. Roksbox does not support
// series-level metadata. Mirrors C# `:103-107`.
func (m *RoksboxMetadata) SeriesMetadata(_ context.Context, _ *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

// EpisodeMetadata builds the per-episode `<stem>.xml` body. Mirrors
// C# `RoksboxMetadata.EpisodeMetadata` at `:109-167`.
//
// Body shape per linked episode:
//
//	<video>
//	  <title>SeriesTitle - SeasonNumber x EpisodeNumber - EpisodeTitle</title>
//	  <year>AirDate</year>
//	  <genre>Genre1 / Genre2 / ...</genre>
//	  <actors>Name - Character , Name2 - Character2 , ...</actors>
//	  <description>Overview</description>
//	  <length>Runtime</length>
//	  <mpaa>Certification</mpaa>
//	</video>
//
// Multi-episode files emit multiple `<video>` blocks separated by
// "\n" matching C# `:144` `xmlResult += Environment.NewLine`.
//
// Actors are capped at 3 (C# `Math.Min(3, series.Actors.Count)` at
// `:135`); the cap matches the C# behaviour intended to keep the
// `<actors>` element short for Roksbox parsing.
//
// Certification is upper-cased and validated against
// [validCertifications]; non-matching values emit "UNRATED" per C#
// `:140-148`.
//
// nil [EpisodeLookup] → zero-value (C# requires the dep but the Go
// port degrades gracefully). Settings.EpisodeMetadata=false → zero-value.
func (m *RoksboxMetadata) EpisodeMetadata(ctx context.Context, s *series.Series, episodeFile *episodefiles.File) (metadata.MetadataFileResult, error) {
	if m == nil || s == nil || episodeFile == nil || !m.settings.EpisodeMetadata || m.episodes == nil {
		return metadata.MetadataFileResult{}, nil
	}
	linked, err := m.episodes.AllByFileID(ctx, episodeFile.ID)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("roksbox: EpisodeMetadata: lookup episodes for fileID=%d: %w", episodeFile.ID, err)
	}
	if len(linked) == 0 {
		return metadata.MetadataFileResult{}, nil
	}

	genres, err := decodeGenres(s.Genres)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("roksbox: EpisodeMetadata: decode genres: %w", err)
	}
	actors, err := decodeActors(s.Actors)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("roksbox: EpisodeMetadata: decode actors: %w", err)
	}

	type videoXML struct {
		XMLName     xml.Name `xml:"video"`
		Title       string   `xml:"title"`
		Year        string   `xml:"year"`
		Genre       string   `xml:"genre"`
		Actors      string   `xml:"actors"`
		Description string   `xml:"description"`
		Length      int      `xml:"length"`
		Mpaa        string   `xml:"mpaa"`
	}

	var body bytes.Buffer
	for i, ep := range linked {
		title := fmt.Sprintf("%s - %dx%d - %s", s.Title, ep.SeasonNumber, ep.EpisodeNumber, ep.Title)
		v := videoXML{
			Title:       title,
			Year:        ep.AirDate,
			Genre:       strings.Join(genres, " / "),
			Actors:      formatActors(actors),
			Description: ep.Overview,
			Length:      s.Runtime,
			Mpaa:        mpaaCertification(s.Certification),
		}
		enc := xml.NewEncoder(&body)
		if err := enc.Encode(&v); err != nil {
			return metadata.MetadataFileResult{}, fmt.Errorf("roksbox: EpisodeMetadata: encode episode %d: %w", ep.ID, err)
		}
		if err := enc.Flush(); err != nil {
			return metadata.MetadataFileResult{}, fmt.Errorf("roksbox: EpisodeMetadata: flush episode %d: %w", ep.ID, err)
		}
		if i < len(linked)-1 {
			body.WriteString("\n")
		}
	}

	filename := swapExtension(episodeFile.RelativePath, ".xml")
	return metadata.NewMetadataFileResult(filename, body.String()), nil
}

// SeriesImages emits a single per-series poster. Mirrors C#
// `RoksboxMetadata.SeriesImages` at `:169-187`.
//
// Picks the [mediacover.CoverTypePoster] image if present, else the
// first image of any type. The filename is derived from the series
// folder name + the local cover-path extension (.jpg or .png).
//
// Disabled setting / nil deps / no images / nil cover-path → empty.
func (m *RoksboxMetadata) SeriesImages(_ context.Context, s *series.Series) ([]metadata.ImageFileResult, error) {
	if m == nil || s == nil || !m.settings.SeriesImages || m.coverPath == nil {
		return nil, nil
	}
	covers, err := decodeCovers(s.Images)
	if err != nil {
		return nil, fmt.Errorf("roksbox: SeriesImages: decode series.Images: %w", err)
	}
	cover, ok := pickPosterOrFirst(covers)
	if !ok {
		return nil, nil
	}
	source := m.coverPath.GetCoverPath(s.ID, cover.CoverType, 0)
	destination := filepath.Base(s.Path) + filepath.Ext(source)
	return []metadata.ImageFileResult{metadata.NewImageFileResult(destination, source)}, nil
}

// SeasonImages emits a single per-season poster inside the season
// folder. Mirrors C# `RoksboxMetadata.SeasonImages` at `:189-215`.
//
// Algorithm:
//  1. Enumerate season folders via [FolderLister.GetDirectories];
//  2. Look up the folder name matching the requested season number;
//  3. Pick the [mediacover.CoverTypePoster] from season.Images
//     (fallback first); RemoteUrl is used as the source URL.
//
// Disabled setting / nil Folders / unknown season folder / no images
// → empty.
func (m *RoksboxMetadata) SeasonImages(_ context.Context, s *series.Series, season metadata.Season) ([]metadata.ImageFileResult, error) {
	if m == nil || s == nil || !m.settings.SeasonImages || m.folders == nil {
		return nil, nil
	}
	seasonFolders, err := getSeasonFolders(m.folders, s.Path)
	if err != nil {
		return nil, fmt.Errorf("roksbox: SeasonImages: list season folders: %w", err)
	}
	folder, ok := seasonFolders[season.SeasonNumber]
	if !ok {
		return nil, nil
	}
	cover, ok := pickPosterOrFirst(season.Images)
	if !ok {
		return nil, nil
	}
	// destination = "<seasonFolderName>/<seasonFolderName>.jpg"
	// stored relative to series.Path. The C# tree uses GetRelativePath
	// against the full path; we precompose since the structure is
	// known.
	folderBase := filepath.Base(folder)
	destination := filepath.Join(folderBase, folderBase+".jpg")
	return []metadata.ImageFileResult{metadata.NewImageFileResult(destination, cover.RemoteUrl)}, nil
}

// EpisodeImages emits the per-episode screenshot. Mirrors C#
// `RoksboxMetadata.EpisodeImages` at `:217-234`. Filename = stem +
// ".jpg" (no -thumb suffix).
//
// Disabled / nil Episodes / no linked episodes / no screenshot →
// empty.
func (m *RoksboxMetadata) EpisodeImages(ctx context.Context, _ *series.Series, episodeFile *episodefiles.File) ([]metadata.ImageFileResult, error) {
	if m == nil || episodeFile == nil || !m.settings.EpisodeImages || m.episodes == nil {
		return nil, nil
	}
	linked, err := m.episodes.AllByFileID(ctx, episodeFile.ID)
	if err != nil {
		return nil, fmt.Errorf("roksbox: EpisodeImages: lookup episodes for fileID=%d: %w", episodeFile.ID, err)
	}
	if len(linked) == 0 {
		return nil, nil
	}
	covers, err := decodeCovers(linked[0].Images)
	if err != nil {
		return nil, fmt.Errorf("roksbox: EpisodeImages: decode episode.Images: %w", err)
	}
	for _, c := range covers {
		if c.CoverType != mediacover.CoverTypeScreenshot {
			continue
		}
		filename := swapExtension(episodeFile.RelativePath, ".jpg")
		return []metadata.ImageFileResult{metadata.NewImageFileResult(filename, c.RemoteUrl)}, nil
	}
	return nil, nil
}

// Test always succeeds — the C# base impl returns empty
// ValidationResult and Roksbox doesn't override.
func (m *RoksboxMetadata) Test(_ context.Context) ([]metadata.TestFailure, error) {
	if m == nil {
		return nil, nil
	}
	return m.settings.Validate(), nil
}

// RequestAction always returns nil/nil — Roksbox doesn't expose any
// v3-UI action handshakes.
func (m *RoksboxMetadata) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard.
var _ metadata.Provider = (*RoksboxMetadata)(nil)

// init registers the RoksboxMetadata constructor with the package-
// global [metadata.Default] registry.
func init() {
	metadata.Default().MustRegister(ImplementationName, ConfigContractName, constructFromDependencies)
}

// constructFromDependencies wires the metadata.Dependencies fields
// into [Deps]. CoverPath comes from MapCoversToLocal; Episodes +
// Folders are left nil pending queue [W6] orchestrator wiring.
func constructFromDependencies(definition *metadata.Metadata, deps metadata.Dependencies) (metadata.Provider, error) {
	settings, err := decodeSettings(definition)
	if err != nil {
		return nil, err
	}
	roksboxDeps := Deps{}
	if cp, ok := deps.MapCoversToLocal.(CoverPathLookup); ok {
		roksboxDeps.CoverPath = cp
	}
	// TODO (queue [W6]): thread EpisodeLookup + FolderLister.
	return NewRoksboxMetadata(definition, settings, roksboxDeps)
}

// decodeSettings deserialises the persisted Settings JSON from
// `Metadata.Settings`. Empty / "{}" / blank → [DefaultSettings].
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
		return Settings{}, fmt.Errorf("roksbox: decodeSettings: %w", err)
	}
	return s, nil
}

// formatActors builds the `<actors>` element string. Mirrors C# `:135`:
// `string.Join(" , ", actors.Take(3).Select(a => $"{Name} - {Character}"))`.
//
// Cap at 3 actors, ` , ` separator (note: spaces on both sides), and
// the per-actor format is "Name - Character" with hyphen-space-hyphen.
func formatActors(actors []actorBlob) string {
	max := len(actors)
	if max > 3 {
		max = 3
	}
	parts := make([]string, 0, max)
	for i := 0; i < max; i++ {
		parts = append(parts, actors[i].Name+" - "+actors[i].Character)
	}
	return strings.Join(parts, " , ")
}

// mpaaCertification validates and normalises series.Certification.
// Mirrors C# `:140-148`. Non-empty + in the allow-list → upper-cased;
// otherwise → "UNRATED".
func mpaaCertification(certification string) string {
	if strings.TrimSpace(certification) == "" {
		return "UNRATED"
	}
	upper := strings.ToUpper(certification)
	if _, ok := validCertifications[upper]; ok {
		return upper
	}
	return "UNRATED"
}

// pickPosterOrFirst returns the [mediacover.CoverTypePoster] entry if
// present, else the first entry, else false. Mirrors C# `:179` +
// `:209` `SingleOrDefault(c => c.CoverType == Poster) ?? FirstOrDefault()`.
func pickPosterOrFirst(covers []mediacover.Cover) (mediacover.Cover, bool) {
	var poster *mediacover.Cover
	for i := range covers {
		if covers[i].CoverType == mediacover.CoverTypePoster {
			poster = &covers[i]
			break
		}
	}
	if poster != nil {
		return *poster, true
	}
	if len(covers) > 0 {
		return covers[0], true
	}
	return mediacover.Cover{}, false
}

// getSeasonFolders walks the series root and returns a season-number
// → folder-path map. Mirrors C# `:236-258` `GetSeasonFolders`.
//
// Matches each child folder against [seasonFolderRegex]. `specials`
// folders map to season 0; `season N` folders map to N (atoi). Folders
// that don't match are skipped silently (C# logs at Debug; the Go
// port mirrors the silent-skip behaviour since this method is on the
// hot path).
func getSeasonFolders(lister FolderLister, seriesPath string) (map[int]string, error) {
	folders, err := lister.GetDirectories(seriesPath)
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(folders))
	for _, folder := range folders {
		base := filepath.Base(folder)
		match := seasonFolderRegex.FindStringSubmatch(base)
		if match == nil {
			continue
		}
		if specialsIdx := seasonFolderRegex.SubexpIndex("specials"); specialsIdx >= 0 && match[specialsIdx] != "" {
			out[0] = folder
			continue
		}
		if seasonIdx := seasonFolderRegex.SubexpIndex("season"); seasonIdx >= 0 && match[seasonIdx] != "" {
			n, parseErr := strconv.Atoi(match[seasonIdx])
			if parseErr == nil {
				out[n] = folder
			}
		}
	}
	return out, nil
}

// actorBlob mirrors C# `Tv.Actor` — {name, character, images}.
type actorBlob struct {
	Name      string             `json:"name"`
	Character string             `json:"character"`
	Images    []mediacover.Cover `json:"images"`
}

// decodeGenres parses the [series.Series.Genres] JSON-blob.
func decodeGenres(blob string) ([]string, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeActors parses the [series.Series.Actors] JSON-blob.
func decodeActors(blob string) ([]actorBlob, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var out []actorBlob
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeCovers parses a [series.Series.Images] or
// [episode.Episode.Images] JSON-blob into []mediacover.Cover.
func decodeCovers(blob string) ([]mediacover.Cover, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" {
		return nil, nil
	}
	var covers []mediacover.Cover
	if err := json.Unmarshal([]byte(trimmed), &covers); err != nil {
		return nil, err
	}
	return covers, nil
}

// swapExtension replaces the extension of `path` with `newExt`
// (newExt should include the leading "."). Mirrors C#
// `Path.ChangeExtension(path, "xml")` semantics — the C# version
// adds a leading "." automatically; the Go port requires the caller
// to pass it.
func swapExtension(path, newExt string) string {
	ext := filepath.Ext(path)
	stem := path[:len(path)-len(ext)]
	return stem + newExt
}

// stripExtension returns the basename with its extension removed.
func stripExtension(filename string) string {
	ext := filepath.Ext(filename)
	return filename[:len(filename)-len(ext)]
}

// getRelativePath mirrors C# `PathExtensions.GetRelativePath`. Local
// copy — see same helper in xbmc / plex packages for rationale.
func getRelativePath(parentPath, childPath string) (string, error) {
	parent := filepath.Clean(parentPath)
	child := filepath.Clean(childPath)
	if parent == "" || child == "" {
		return "", errors.New("roksbox: getRelativePath: empty parent or child")
	}
	if !strings.HasPrefix(child, parent) {
		return "", errors.New("roksbox: getRelativePath: child is not under parent")
	}
	suffix := child[len(parent):]
	return strings.Trim(suffix, `/\`), nil
}
