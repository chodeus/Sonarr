package organizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// MaxFilePathLength bounds the absolute path length used for component
// truncation. Mirrors the C# `LongPathSupport.MaxFilePathLength`. The
// numeric value (4096) is the typical Linux PATH_MAX; Windows hosts
// run with the long-path-aware cousin which exceeds this. Setting a
// per-platform value isn't worth the complexity for the Go port — the
// Sonarr file-builder rarely produces paths anywhere near this bound.
const MaxFilePathLength = 4096

// MaxFileNameLength bounds the per-component (single filename) length.
// 255 bytes matches NTFS / ext4 / APFS / HFS+. Used to clamp the
// per-segment budget in [BuildFileName].
const MaxFileNameLength = 255

// trimSeparatorsRegex strips trailing separator chars (`-`, ` `, `.`,
// `_`) from the end of a built component. Mirrors the C# regex at
// `:72`.
var trimSeparatorsRegex = regexp.MustCompile(`[- ._]+$`)

// fileNameRunCollapseRegex collapses adjacent identical separator
// chars to a single instance — same job as `runCollapseRegexes` but
// applied as a final pass on the assembled component.
//
// Separator pairs handled: `-`, ` `, `.`, `_`. The C# combines these
// into one regex via backreference `\1`; RE2 doesn't support
// backreferences so we run four passes.
var fileNameRunCollapse = []struct {
	re      *regexp.Regexp
	replace string
}{
	{regexp.MustCompile(`-{2,}`), "-"},
	{regexp.MustCompile(` {2,}`), " "},
	{regexp.MustCompile(`\.{2,}`), "."},
	{regexp.MustCompile(`_{2,}`), "_"},
}

// ErrNamingFormatRequired mirrors the C# `NamingFormatException`
// thrown when a series-type-specific format is empty (e.g. anime
// episode tries to build with `AnimeEpisodeFormat=""`). Callers
// classify via `errors.Is(err, ErrNamingFormatRequired)`.
var ErrNamingFormatRequired = errors.New("organizer: naming format is required for series type")

// BuildOptions encapsulates all the inputs `BuildFileName` /
// `BuildFilePath` need beyond the per-call series + episodes + file.
//
// The C# version receives most of these from constructor-injected
// services (`INamingConfigService`, `IQualityDefinitionService`,
// `ICustomFormatCalculationService`); the Go port ships the resolved
// data via this options bag so [BuildFileName] is a pure function
// (no I/O, deterministic given inputs).
//
// Callers populate these fields from the relevant Service.Get / All
// calls before invoking BuildFileName.
type BuildOptions struct {
	// NamingConfig fields used during the build:
	RenameEpisodes               bool
	StandardEpisodeFormat        string
	DailyEpisodeFormat           string
	AnimeEpisodeFormat           string
	SeriesFolderFormat           string
	SeasonFolderFormat           string
	SpecialsFolderFormat         string
	MultiEpisodeStyle            MultiEpisodeStyle
	ReplaceIllegalCharacters     bool
	ColonReplacementFormat       ColonReplacementFormat
	CustomColonReplacementFormat string

	// Resolver/dependency injections (all optional — nil ⇒ stub):
	QualityResolver QualityTitleResolver
	CustomFormats   []*customformats.Format

	// MaxPath bounds the overall path; defaults to MaxFilePathLength
	// when zero.
	MaxPath int

	// UseCurrentFilenameAsFallback maps to the C# argument that's
	// passed to AddEpisodeFileTokens at `:198`. The C# version derives
	// this as `!patternHasEpisodeIdentifier || episodeFile.Id == 0`;
	// the Go port lets the caller set it explicitly so unit tests
	// can pin behaviour without depending on the C# heuristic.
	UseCurrentFilenameAsFallback bool
}

// CleanOptions returns the [CleanNameOptions] subset of `o`.
func (o BuildOptions) CleanOptions() CleanNameOptions {
	return CleanNameOptions{
		ReplaceIllegalCharacters:     o.ReplaceIllegalCharacters,
		ColonReplacementFormat:       o.ColonReplacementFormat,
		CustomColonReplacementFormat: o.CustomColonReplacementFormat,
	}
}

// FolderOptions returns the [FolderOptions] subset of `o`.
func (o BuildOptions) FolderOptions() FolderOptions {
	return FolderOptions{
		SeriesFolderFormat:   o.SeriesFolderFormat,
		SeasonFolderFormat:   o.SeasonFolderFormat,
		SpecialsFolderFormat: o.SpecialsFolderFormat,
		Clean:                o.CleanOptions(),
	}
}

// BuildFileName ports `FileNameBuilder.BuildFileName` at
// `FileNameBuilder.cs:136-224` (the private overload taking
// `maxPath`).
//
// Returns the rendered episode filename WITHOUT the season/series
// folder prefix. Concatenate with `BuildSeasonPath(...)` (or call
// [BuildFilePath]) to get the full path.
//
// `extension` is appended to the last segment. Callers typically pass
// `.mkv`, `.mp4`, etc. (with the leading dot).
//
// When `opts.RenameEpisodes == false`, returns the original filename
// (via [getOriginalTitle]) + extension verbatim — the rename path is
// fully bypassed.
func BuildFileName(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string, opts BuildOptions) (string, error) {
	if s == nil {
		return "", errors.New("organizer: BuildFileName: nil series")
	}
	if !opts.RenameEpisodes {
		// Caller wants the original filename preserved; just return
		// it (with extension) without running the token engine.
		if file == nil {
			return extension, nil
		}
		return getOriginalTitle(file, true) + extension, nil
	}

	seriesType := SeriesType(s.SeriesType)
	pattern, err := pickPattern(seriesType, eps, opts)
	if err != nil {
		return "", err
	}

	maxPath := opts.MaxPath
	if maxPath == 0 {
		maxPath = MaxFilePathLength
	}

	// Sort episodes by (SeasonNumber, EpisodeNumber). Mirrors C# `:165`.
	sortedEpisodes := append([]episodes.Episode(nil), eps...)
	sort.SliceStable(sortedEpisodes, func(i, j int) bool {
		if sortedEpisodes[i].SeasonNumber != sortedEpisodes[j].SeasonNumber {
			return sortedEpisodes[i].SeasonNumber < sortedEpisodes[j].SeasonNumber
		}
		return sortedEpisodes[i].EpisodeNumber < sortedEpisodes[j].EpisodeNumber
	})

	splitPatterns := splitOnPathSep(pattern)
	components := make([]string, 0, len(splitPatterns))
	for i, splitPattern := range splitPatterns {
		component := buildOneComponent(splitPattern, sortedEpisodes, s, file, opts, seriesType, i == len(splitPatterns)-1, extension, maxPath)
		components = append(components, component)
	}

	return strings.Join(components, string(filepath.Separator)) + extension, nil
}

// BuildFilePath ports `FileNameBuilder.BuildFilePath` at `:231-240`.
// Joins `BuildSeasonPath(series, seasonNumber)` with the rendered
// filename. `extension` is required.
func BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string, opts BuildOptions) (string, error) {
	if strings.TrimSpace(extension) == "" {
		return "", errors.New("organizer: BuildFilePath: extension is required")
	}
	if s == nil || len(eps) == 0 {
		return "", errors.New("organizer: BuildFilePath: nil series or empty episodes")
	}
	seasonPath := BuildSeasonPath(s, eps[0].SeasonNumber, opts.FolderOptions())

	maxPath := opts.MaxPath
	if maxPath == 0 {
		maxPath = MaxFilePathLength
	}
	remaining := maxPath - len(seasonPath) - 1
	if remaining < 0 {
		remaining = 0
	}

	innerOpts := opts
	innerOpts.MaxPath = remaining
	fileName, err := BuildFileName(eps, s, file, extension, innerOpts)
	if err != nil {
		return "", err
	}
	return filepath.Join(seasonPath, fileName), nil
}

// RequiresEpisodeTitle ports `:399-436`. Walks the active pattern
// (Standard / Daily / Anime per series type + episodes) and returns
// true when `{Episode Title}` or `{Episode CleanTitle}` is present.
//
// `opts.RenameEpisodes == false` short-circuits to false — when
// renaming is off, the user's pattern doesn't drive the result.
func RequiresEpisodeTitle(s *series.Series, eps []episodes.Episode, opts BuildOptions) bool {
	if !opts.RenameEpisodes {
		return false
	}
	seriesType := SeriesType(s.SeriesType)
	pattern := opts.StandardEpisodeFormat
	if seriesType == SeriesTypeDaily {
		pattern = opts.DailyEpisodeFormat
	}
	if seriesType == SeriesTypeAnime {
		allHaveAbsolute := true
		for _, e := range eps {
			if e.AbsoluteEpisodeNumber == nil {
				allHaveAbsolute = false
				break
			}
		}
		if allHaveAbsolute {
			pattern = opts.AnimeEpisodeFormat
		}
	}
	matches := titleRegex.FindAllStringSubmatch(pattern, -1)
	names := titleRegex.SubexpNames()
	for _, m := range matches {
		for i, n := range names {
			if n == "token" && m[i] != "" {
				switch NormalizeTokenKey(m[i]) {
				case NormalizeTokenKey("Episode Title"), NormalizeTokenKey("Episode CleanTitle"):
					return true
				}
			}
		}
	}
	return false
}

// RequiresAbsoluteEpisodeNumber ports `:438-449`. Returns true when
// the AnimeEpisodeFormat pattern contains an `{absolute}` token.
func RequiresAbsoluteEpisodeNumber(opts BuildOptions) bool {
	return absoluteEpisodeRegex.MatchString(opts.AnimeEpisodeFormat)
}

// pickPattern selects the right per-series-type format string,
// short-circuits when the required format is missing.
func pickPattern(t SeriesType, eps []episodes.Episode, opts BuildOptions) (string, error) {
	switch t {
	case SeriesTypeDaily:
		if strings.TrimSpace(opts.DailyEpisodeFormat) == "" {
			return "", fmt.Errorf("%w: daily", ErrNamingFormatRequired)
		}
		// C# `:167-170` only switches to Daily when SeasonNumber > 0;
		// ports verbatim. Specials (S00) fall back to Standard.
		if len(eps) > 0 && eps[0].SeasonNumber > 0 {
			return opts.DailyEpisodeFormat, nil
		}
	case SeriesTypeAnime:
		if strings.TrimSpace(opts.AnimeEpisodeFormat) == "" {
			return "", fmt.Errorf("%w: anime", ErrNamingFormatRequired)
		}
		// C# `:172-177` switches to Anime when all eps have absolute
		// number OR when the pattern doesn't require absolute. The
		// "doesn't require absolute" check uses RequiresAbsoluteEpisodeNumber
		// over the user's AnimeEpisodeFormat.
		allHaveAbsolute := true
		for _, e := range eps {
			if e.AbsoluteEpisodeNumber == nil {
				allHaveAbsolute = false
				break
			}
		}
		if allHaveAbsolute || !RequiresAbsoluteEpisodeNumber(opts) {
			return opts.AnimeEpisodeFormat, nil
		}
	}
	if strings.TrimSpace(opts.StandardEpisodeFormat) == "" {
		return "", fmt.Errorf("%w: standard", ErrNamingFormatRequired)
	}
	return opts.StandardEpisodeFormat, nil
}

// splitOnPathSep splits the pattern on `/` and `\`, keeping only
// non-empty components. Mirrors the C# `pattern.Split(['\\', '/'],
// StringSplitOptions.RemoveEmptyEntries)` at `:179`.
func splitOnPathSep(pattern string) []string {
	parts := strings.FieldsFunc(pattern, func(r rune) bool {
		return r == '\\' || r == '/'
	})
	return parts
}

// buildOneComponent renders one path-segment of the user's pattern.
// Mirrors the inner loop body at `:182-221`.
func buildOneComponent(splitPattern string, eps []episodes.Episode, s *series.Series, file *episodefiles.File, opts BuildOptions, seriesType SeriesType, isLast bool, extension string, maxPath int) string {
	handlers := map[string]TokenHandler{}

	splitPattern = AddSeasonEpisodeNumberingTokens(splitPattern, handlers, eps, opts.MultiEpisodeStyle)
	splitPattern = AddAbsoluteNumberingTokens(splitPattern, handlers, s, eps, opts.MultiEpisodeStyle, seriesType)
	// Escape any literal `...` so a downstream {ellipsis}-replace pass
	// doesn't mistakenly emit doubled dots.
	splitPattern = strings.ReplaceAll(splitPattern, "...", "{{ellipsis}}")

	RegisterSeriesTokens(handlers, s)
	RegisterIDTokens(handlers, s)
	AddEpisodeTokens(handlers, eps)
	AddEpisodeTitlePlaceholderTokens(handlers)
	RegisterEpisodeFileTokens(handlers, file, opts.UseCurrentFilenameAsFallback)
	registerQualityTokensFromOpts(handlers, s, file, opts)
	RegisterMediaInfoStubs(handlers)
	RegisterCustomFormatTokens(handlers, opts.CustomFormats)

	// First pass: replace tokens with escape=true so the pre-flight
	// length measurement runs on a structure that survives a second
	// ReplaceTokens pass.
	component := strings.TrimSpace(ReplaceTokens(splitPattern, handlers, opts.CleanOptions(), true))

	// Determine the per-segment length budget for the episode-title
	// portion. Mirrors `:204-210`.
	maxPathSegment := MaxFileNameLength
	if maxPath < maxPathSegment {
		maxPathSegment = maxPath
	}
	if isLast {
		maxPathSegment -= len(extension)
	}
	maxEpisodeTitleLength := maxPathSegment - getLengthWithoutEpisodeTitle(component, opts.CleanOptions())
	if maxEpisodeTitleLength < 0 {
		maxEpisodeTitleLength = 0
	}

	AddEpisodeTitleTokens(handlers, eps, maxEpisodeTitleLength)
	component = strings.TrimSpace(ReplaceTokens(component, handlers, opts.CleanOptions(), false))

	// Final cleanup pass: collapse adjacent separator runs, trim
	// trailing separators, substitute {ellipsis}, replace reserved
	// device names. Mirrors `:215-218`.
	for _, rule := range fileNameRunCollapse {
		component = rule.re.ReplaceAllString(component, rule.replace)
	}
	component = trimSeparatorsRegex.ReplaceAllString(component, "")
	component = strings.ReplaceAll(component, "{ellipsis}", "...")
	component = ReplaceReservedDeviceNames(component)
	return component
}

// registerQualityTokensFromOpts wires the file's QualityModel into the
// quality token handlers. Falls back to the bare quality.Quality
// shipped on the file when MediaInfo / decision-engine resolution
// hasn't run yet.
//
// The Go port stores `EpisodeFile.Quality` as the embedded JSON
// string of a quality.Model — the parsed view lives in the typed
// helpers shipped in the quality package. For now (until the
// quality.Model parser lands) we resolve from the file's name and
// pass the empty Model through; resolver fallback handles the rest.
func registerQualityTokensFromOpts(handlers map[string]TokenHandler, s *series.Series, file *episodefiles.File, opts BuildOptions) {
	model := quality.Model{}
	if file != nil && strings.TrimSpace(file.Quality) != "" {
		// File.Quality is JSON encoded as the typed Model wire shape.
		// Fail-soft on parse error → empty model with Quality=Unknown
		// so the {Quality ...} tokens render to empty rather than
		// leaking the JSON literal.
		_ = json.Unmarshal([]byte(file.Quality), &model)
	}
	RegisterQualityTokens(handlers, s, model, opts.QualityResolver)
}

// getLengthWithoutEpisodeTitle ports `:1141-1151`. Re-runs ReplaceTokens
// against `component` with the episode-title tokens registered to
// empty (and `{ellipsis}` resolved to `...`), returns the byte length
// of the result. Used to compute the remaining budget for the actual
// episode title rendering.
func getLengthWithoutEpisodeTitle(component string, clean CleanNameOptions) int {
	handlers := map[string]TokenHandler{
		NormalizeTokenKey("Episode Title"):      func(TokenMatch) string { return "" },
		NormalizeTokenKey("Episode CleanTitle"): func(TokenMatch) string { return "" },
		NormalizeTokenKey("ellipsis"):           func(TokenMatch) string { return "..." },
	}
	out := ReplaceTokens(component, handlers, clean, false)
	return len(out)
}
