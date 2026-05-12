package wdtv

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

// providerDisplayName mirrors C# `WdtvMetadata.Name` at
// `WdtvMetadata.cs:30`.
const providerDisplayName = "WDTV"

// seasonFolderRegex classifies a season directory name as either
// `season N` (any digits) or `specials`. Identical pattern to the
// Roksbox writer (intentional — both writers scan the same folder
// layouts). Mirrors C# `:33`.
var seasonFolderRegex = regexp.MustCompile(`(?i)^(season (?P<season>\d+))|(?P<specials>specials)`)

// CoverPathLookup wraps [mediacover.Service.GetCoverPath] — used by
// [WdtvMetadata.SeriesImages]. Mirrors C# `IMapCoversToLocal.GetCoverPath`.
type CoverPathLookup interface {
	GetCoverPath(seriesID int, coverType mediacover.CoverType, height int) string
}

// EpisodeLookup narrows the per-writer dep for episode-by-file
// lookup. Used by [WdtvMetadata.EpisodeMetadata] /
// [WdtvMetadata.EpisodeImages].
type EpisodeLookup interface {
	AllByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// FolderLister narrows the disk surface [WdtvMetadata.SeasonImages]
// needs to enumerate season directories under the series root.
// Mirrors C# `IDiskProvider.GetDirectories(seriesPath)`.
type FolderLister interface {
	GetDirectories(seriesPath string) ([]string, error)
}

// Deps bundles the wdtv-specific narrow deps. All fields are
// nil-tolerant — when an optional dep is unset the corresponding
// method degrades to empty output.
type Deps struct {
	// CoverPath is the local-cover-path lookup used by SeriesImages.
	CoverPath CoverPathLookup

	// Episodes is the episode-by-file lookup used by EpisodeMetadata
	// + EpisodeImages.
	Episodes EpisodeLookup

	// Folders is the disk directory lister used by SeasonImages.
	Folders FolderLister
}

// WdtvMetadata ports the C# concrete `WdtvMetadata` class at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Wdtv/WdtvMetadata.cs:14-290`.
// Implements [metadata.Provider].
//
// Sidecar emission shape:
//   - SeriesMetadata: NEVER (C# `:122-126` always returns null).
//   - EpisodeMetadata: per-episode `<details>` XML sub-document in
//     `<stem>.xml` next to the episode file. Multi-episode files
//     emit multiple `<details>` blocks separated by newlines.
//   - SeriesImages: `folder.<ext>` at series root.
//   - SeasonImages: `<seasonFolder>/folder.jpg` inside each season folder.
//   - EpisodeImages: `<stem>.metathumb` next to the episode file
//     (NOT .jpg — distinctive WDTV extension).
type WdtvMetadata struct {
	definition *metadata.Metadata
	settings   Settings
	coverPath  CoverPathLookup
	episodes   EpisodeLookup
	folders    FolderLister
}

// NewWdtvMetadata builds a [WdtvMetadata] from a persisted
// [metadata.Metadata] row + Settings + [Deps] bundle. No required
// deps — every optional dep degrades to empty output when nil.
func NewWdtvMetadata(definition *metadata.Metadata, settings Settings, deps Deps) (*WdtvMetadata, error) {
	return &WdtvMetadata{
		definition: definition,
		settings:   settings,
		coverPath:  deps.CoverPath,
		episodes:   deps.Episodes,
		folders:    deps.Folders,
	}, nil
}

// Name returns the user-facing display label. Mirrors C# `Name` at
// `WdtvMetadata.cs:30`.
func (m *WdtvMetadata) Name() string {
	return providerDisplayName
}

// Definition returns the persisted Metadata row.
func (m *WdtvMetadata) Definition() *metadata.Metadata {
	if m == nil {
		return nil
	}
	return m.definition
}

// SettingsConfig returns the per-instance Settings struct.
func (m *WdtvMetadata) SettingsConfig() Settings {
	if m == nil {
		return Settings{}
	}
	return m.settings
}

// GetFilenameAfterMove dispatches on metadataFile.Type — mirrors C#
// `WdtvMetadata.GetFilenameAfterMove` at `:32-46`.
//
//   - EpisodeImage → swap ext to ".metathumb" (NOT .jpg).
//   - EpisodeMetadata → swap ext to ".xml".
//   - else → filepath.Join(series.Path, RelativePath) fallback.
func (m *WdtvMetadata) GetFilenameAfterMove(s *series.Series, episodeFile *episodefiles.File, metadataFile *metadatafiles.MetadataFile) string {
	if m == nil || s == nil || episodeFile == nil || metadataFile == nil {
		return ""
	}
	episodeFilePath := filepath.Join(s.Path, episodeFile.RelativePath)
	switch metadata.MetadataType(metadataFile.Type) {
	case metadata.MetadataTypeEpisodeImage:
		return swapExtension(episodeFilePath, ".metathumb")
	case metadata.MetadataTypeEpisodeMetadata:
		return swapExtension(episodeFilePath, ".xml")
	}
	return filepath.Join(s.Path, metadataFile.RelativePath)
}

// FindMetadataFile classifies an on-disk file at `path`. Mirrors C#
// `WdtvMetadata.FindMetadataFile` at `:48-100`.
//
// Classification (in C# evaluation order — first match wins):
//   - filename equals "folder.jpg" (case-insensitive):
//   - parent dir matches seasonFolderRegex → SeasonImage with
//     SeasonNumber from `season N` capture or 0 for `specials`;
//   - else → SeriesImage.
//   - parser.ParseTitle succeeds AND !FullSeason:
//   - ext .xml → EpisodeMetadata;
//   - ext .metathumb → EpisodeImage.
//   - Else nil.
//
// Non-child-of-series-path returns a typed error.
func (m *WdtvMetadata) FindMetadataFile(s *series.Series, path string) (*metadatafiles.MetadataFile, error) {
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

	// "folder.jpg" arm — parent dir determines Series vs Season.
	if strings.EqualFold(filename, "folder.jpg") {
		parentDir := filepath.Base(filepath.Dir(path))
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

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xml":
		base.Type = int(metadata.MetadataTypeEpisodeMetadata)
		return base, nil
	case ".metathumb":
		base.Type = int(metadata.MetadataTypeEpisodeImage)
		return base, nil
	}

	return nil, nil
}

// SeriesMetadata always returns zero-value. WDTV does not support
// series-level metadata. Mirrors C# `:107-110`.
func (m *WdtvMetadata) SeriesMetadata(_ context.Context, _ *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	return metadata.MetadataFileResult{}, nil
}

// EpisodeMetadata builds the per-episode `<stem>.xml` body. Mirrors
// C# `WdtvMetadata.EpisodeMetadata` at `:112-167`.
//
// Body shape per linked episode — `<details>` root with:
//
//	<id>SeriesID</id>
//	<title>SeriesTitle - SeasonNumber x EpisodeNumber:00 - EpisodeTitle</title>
//	<series_name>SeriesTitle</series_name>
//	<episode_name>EpisodeTitle</episode_name>
//	<season_number>NN</season_number>     (zero-padded)
//	<episode_number>NN</episode_number>   (zero-padded)
//	<firstaired>AirDate</firstaired>
//	<genre>Genre1 / Genre2 / ...</genre>
//	<actor>Name - Character / Name2 - Character2 / ...</actor>
//	<overview>Overview</overview>
//
// Notable WDTV differences from Roksbox:
//   - No actor cap — all actors emitted (C# `:152`).
//   - Actors separated by " / " not " , ".
//   - No mpaa / certification handling.
//
// Multi-episode files emit multiple `<details>` blocks separated by
// "\n" matching C# `:158` Environment.NewLine.
//
// nil [EpisodeLookup] / Settings.EpisodeMetadata=false → zero-value.
func (m *WdtvMetadata) EpisodeMetadata(ctx context.Context, s *series.Series, episodeFile *episodefiles.File) (metadata.MetadataFileResult, error) {
	if m == nil || s == nil || episodeFile == nil || !m.settings.EpisodeMetadata || m.episodes == nil {
		return metadata.MetadataFileResult{}, nil
	}
	linked, err := m.episodes.AllByFileID(ctx, episodeFile.ID)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("wdtv: EpisodeMetadata: lookup episodes for fileID=%d: %w", episodeFile.ID, err)
	}
	if len(linked) == 0 {
		return metadata.MetadataFileResult{}, nil
	}

	genres, err := decodeGenres(s.Genres)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("wdtv: EpisodeMetadata: decode genres: %w", err)
	}
	actors, err := decodeActors(s.Actors)
	if err != nil {
		return metadata.MetadataFileResult{}, fmt.Errorf("wdtv: EpisodeMetadata: decode actors: %w", err)
	}

	type detailsXML struct {
		XMLName       xml.Name `xml:"details"`
		ID            int      `xml:"id"`
		Title         string   `xml:"title"`
		SeriesName    string   `xml:"series_name"`
		EpisodeName   string   `xml:"episode_name"`
		SeasonNumber  string   `xml:"season_number"`
		EpisodeNumber string   `xml:"episode_number"`
		FirstAired    string   `xml:"firstaired"`
		Genre         string   `xml:"genre"`
		Actor         string   `xml:"actor"`
		Overview      string   `xml:"overview"`
	}

	var body bytes.Buffer
	for i, ep := range linked {
		// Title format mirrors C# `:144`:
		// "{SeriesTitle} - {SeasonNumber}x{EpisodeNumber:00} - {EpisodeTitle}".
		// Episode number zero-padded; season number NOT padded in title.
		title := fmt.Sprintf("%s - %dx%02d - %s", s.Title, ep.SeasonNumber, ep.EpisodeNumber, ep.Title)
		d := detailsXML{
			ID:            s.ID,
			Title:         title,
			SeriesName:    s.Title,
			EpisodeName:   ep.Title,
			SeasonNumber:  fmt.Sprintf("%02d", ep.SeasonNumber),
			EpisodeNumber: fmt.Sprintf("%02d", ep.EpisodeNumber),
			FirstAired:    ep.AirDate,
			Genre:         strings.Join(genres, " / "),
			Actor:         formatActors(actors),
			Overview:      ep.Overview,
		}
		enc := xml.NewEncoder(&body)
		if err := enc.Encode(&d); err != nil {
			return metadata.MetadataFileResult{}, fmt.Errorf("wdtv: EpisodeMetadata: encode episode %d: %w", ep.ID, err)
		}
		if err := enc.Flush(); err != nil {
			return metadata.MetadataFileResult{}, fmt.Errorf("wdtv: EpisodeMetadata: flush episode %d: %w", ep.ID, err)
		}
		if i < len(linked)-1 {
			body.WriteString("\n")
		}
	}

	filename := swapExtension(episodeFile.RelativePath, ".xml")
	return metadata.NewMetadataFileResult(filename, body.String()), nil
}

// SeriesImages emits a single per-series `folder.<ext>` at the series
// root. Mirrors C# `WdtvMetadata.SeriesImages` at `:169-187`.
//
// Picks poster preferred, first cover fallback. The destination is
// "folder" + the local cover-path's extension (.jpg or .png).
//
// Disabled setting / nil deps / no images → empty.
func (m *WdtvMetadata) SeriesImages(_ context.Context, s *series.Series) ([]metadata.ImageFileResult, error) {
	if m == nil || s == nil || !m.settings.SeriesImages || m.coverPath == nil {
		return nil, nil
	}
	covers, err := decodeCovers(s.Images)
	if err != nil {
		return nil, fmt.Errorf("wdtv: SeriesImages: decode series.Images: %w", err)
	}
	cover, ok := pickPosterOrFirst(covers)
	if !ok {
		return nil, nil
	}
	source := m.coverPath.GetCoverPath(s.ID, cover.CoverType, 0)
	destination := "folder" + filepath.Ext(source)
	return []metadata.ImageFileResult{metadata.NewImageFileResult(destination, source)}, nil
}

// SeasonImages emits a single per-season `<seasonFolder>/folder.jpg`.
// Mirrors C# `WdtvMetadata.SeasonImages` at `:189-215`.
//
// Algorithm:
//  1. Enumerate season folders via [FolderLister];
//  2. Look up the folder matching the requested season number;
//  3. Pick poster or first; URL = RemoteUrl.
func (m *WdtvMetadata) SeasonImages(_ context.Context, s *series.Series, season metadata.Season) ([]metadata.ImageFileResult, error) {
	if m == nil || s == nil || !m.settings.SeasonImages || m.folders == nil {
		return nil, nil
	}
	seasonFolders, err := getSeasonFolders(m.folders, s.Path)
	if err != nil {
		return nil, fmt.Errorf("wdtv: SeasonImages: list season folders: %w", err)
	}
	folder, ok := seasonFolders[season.SeasonNumber]
	if !ok {
		return nil, nil
	}
	cover, ok := pickPosterOrFirst(season.Images)
	if !ok {
		return nil, nil
	}
	// destination = "<seasonFolderBase>/folder.jpg".
	folderBase := filepath.Base(folder)
	destination := filepath.Join(folderBase, "folder.jpg")
	return []metadata.ImageFileResult{metadata.NewImageFileResult(destination, cover.RemoteUrl)}, nil
}

// EpisodeImages emits the per-episode screenshot as `<stem>.metathumb`.
// Mirrors C# `WdtvMetadata.EpisodeImages` at `:217-234`. The
// `.metathumb` extension is WDTV-distinctive (Roksbox uses bare .jpg;
// Xbmc uses -thumb.jpg).
func (m *WdtvMetadata) EpisodeImages(ctx context.Context, _ *series.Series, episodeFile *episodefiles.File) ([]metadata.ImageFileResult, error) {
	if m == nil || episodeFile == nil || !m.settings.EpisodeImages || m.episodes == nil {
		return nil, nil
	}
	linked, err := m.episodes.AllByFileID(ctx, episodeFile.ID)
	if err != nil {
		return nil, fmt.Errorf("wdtv: EpisodeImages: lookup episodes for fileID=%d: %w", episodeFile.ID, err)
	}
	if len(linked) == 0 {
		return nil, nil
	}
	covers, err := decodeCovers(linked[0].Images)
	if err != nil {
		return nil, fmt.Errorf("wdtv: EpisodeImages: decode episode.Images: %w", err)
	}
	for _, c := range covers {
		if c.CoverType != mediacover.CoverTypeScreenshot {
			continue
		}
		filename := swapExtension(episodeFile.RelativePath, ".metathumb")
		return []metadata.ImageFileResult{metadata.NewImageFileResult(filename, c.RemoteUrl)}, nil
	}
	return nil, nil
}

// Test always succeeds — empty validator + no override.
func (m *WdtvMetadata) Test(_ context.Context) ([]metadata.TestFailure, error) {
	if m == nil {
		return nil, nil
	}
	return m.settings.Validate(), nil
}

// RequestAction default nil/nil.
func (m *WdtvMetadata) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard.
var _ metadata.Provider = (*WdtvMetadata)(nil)

// init registers the WdtvMetadata constructor with the package-global
// [metadata.Default] registry.
func init() {
	metadata.Default().MustRegister(ImplementationName, ConfigContractName, constructFromDependencies)
}

// constructFromDependencies wires the metadata.Dependencies fields
// into [Deps]. Episodes + Folders are left nil pending queue [W6]
// orchestrator wiring.
func constructFromDependencies(definition *metadata.Metadata, deps metadata.Dependencies) (metadata.Provider, error) {
	settings, err := decodeSettings(definition)
	if err != nil {
		return nil, err
	}
	wdtvDeps := Deps{}
	if cp, ok := deps.MapCoversToLocal.(CoverPathLookup); ok {
		wdtvDeps.CoverPath = cp
	}
	// TODO (queue [W6]): thread EpisodeLookup + FolderLister.
	return NewWdtvMetadata(definition, settings, wdtvDeps)
}

// decodeSettings deserialises the persisted Settings JSON. Empty /
// "{}" / blank → [DefaultSettings].
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
		return Settings{}, fmt.Errorf("wdtv: decodeSettings: %w", err)
	}
	return s, nil
}

// formatActors builds the `<actor>` element string. Mirrors C# `:152`:
// `string.Join(" / ", actors.Select(a => $"{Name} - {Character}"))`.
//
// Unlike Roksbox, WDTV does NOT cap the actor count.
func formatActors(actors []actorBlob) string {
	parts := make([]string, 0, len(actors))
	for _, a := range actors {
		parts = append(parts, a.Name+" - "+a.Character)
	}
	return strings.Join(parts, " / ")
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

// getSeasonFolders walks the series root + returns season-number →
// folder-path map. Mirrors C# `:236-272` GetSeasonFolders.
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

// actorBlob mirrors C# `Tv.Actor`.
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

// decodeCovers parses a JSON-blob into []mediacover.Cover.
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
// (caller passes leading "."). Mirrors C# `Path.ChangeExtension`.
func swapExtension(path, newExt string) string {
	ext := filepath.Ext(path)
	stem := path[:len(path)-len(ext)]
	return stem + newExt
}

// getRelativePath mirrors C# `PathExtensions.GetRelativePath`. Local
// copy — see xbmc / plex / roksbox for rationale.
func getRelativePath(parentPath, childPath string) (string, error) {
	parent := filepath.Clean(parentPath)
	child := filepath.Clean(childPath)
	if parent == "" || child == "" {
		return "", errors.New("wdtv: getRelativePath: empty parent or child")
	}
	if !strings.HasPrefix(child, parent) {
		return "", errors.New("wdtv: getRelativePath: child is not under parent")
	}
	suffix := child[len(parent):]
	return strings.Trim(suffix, `/\`), nil
}
