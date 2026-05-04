package organizer

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
)

// multiPartCleanupRegex strips trailing `(1)` / `(2)` / ` Part 1` /
// `Pt. 2` style markers from episode titles. Mirrors C#
// `MultiPartCleanupRegex` at `FileNameBuilder.cs:78`.
//
// TODO from C#: doesn't handle "One/Two/Three" or Roman numerals.
// Carry the same gap forward.
var multiPartCleanupRegex = regexp.MustCompile(`(?i)(?::?\s?(?:\(\d+\)|(?:Part|Pt\.?)\s?\d+))$`)

// episodeTitleTrimChars is the set the C# version strips from
// the trailing edge of episode titles before joining: space, dot,
// question mark. Mirrors `:80`.
const episodeTitleTrimChars = " .?"

// CleanupEpisodeTitle ports `:1085-1089`. Strips `(1)` / `Part 2`
// suffixes, returns the trimmed result.
func CleanupEpisodeTitle(title string) string {
	return strings.TrimSpace(multiPartCleanupRegex.ReplaceAllString(title, ""))
}

// GetEpisodeTitles ports `FileNameBuilder.GetEpisodeTitles` at
// `:1016-1039`. Builds the deduplicated list of episode titles after
// the multi-part-cleanup pass. When the cleanup leaves every title
// blank, falls back to the raw titles (matches `:1031-1036`).
func GetEpisodeTitles(eps []episodes.Episode) []string {
	if len(eps) == 0 {
		return nil
	}
	if len(eps) == 1 {
		return []string{strings.TrimRight(eps[0].Title, episodeTitleTrimChars)}
	}
	cleaned := make([]string, 0, len(eps))
	seenCleaned := map[string]bool{}
	allBlank := true
	for _, e := range eps {
		t := strings.TrimRight(e.Title, episodeTitleTrimChars)
		c := CleanupEpisodeTitle(t)
		if !seenCleaned[c] {
			seenCleaned[c] = true
			cleaned = append(cleaned, c)
			if strings.TrimSpace(c) != "" {
				allBlank = false
			}
		}
	}
	if !allBlank {
		return cleaned
	}
	// Cleanup blanked everything — fall back to raw titles
	// (deduplicated).
	out := make([]string, 0, len(eps))
	seen := map[string]bool{}
	for _, e := range eps {
		t := strings.TrimRight(e.Title, episodeTitleTrimChars)
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// GetEpisodeTitle ports `:1041-1083`. Joins `titles` with `separator`,
// truncating to fit `maxLength` (and the optional caller-supplied
// `formatter` max). Falls through three formatting strategies:
//
//  1. If the joined string fits, return it.
//  2. If `first + last + 3` fits (multi-ep), emit
//     `first{ellipsis}last`.
//  3. If `first + 3` fits (multi-ep), emit `first{ellipsis}`.
//  4. Single-title-fits → return it.
//  5. Otherwise truncate first title to `maxLength-3` chars + ellipsis.
//
// The `{ellipsis}` placeholder gets substituted to `...` by the
// downstream BuildFileName final pass.
//
// Length is measured in runes (multi-byte aware), matching the C#
// `GetByteCount()` semantic for ASCII-clean inputs and matching it
// closely enough for typical UTF-8 use.
func GetEpisodeTitle(titles []string, separator string, maxLength int, formatter string) string {
	if len(titles) == 0 {
		return ""
	}
	if maxFormatter := getMaxLengthFromFormatter(formatter); maxFormatter > 0 && maxFormatter < maxLength {
		maxLength = maxFormatter
	}
	separator = " " + strings.TrimSpace(separator) + " "

	joined := strings.Join(titles, separator)
	if len([]rune(joined)) <= maxLength {
		return joined
	}

	first := titles[0]
	firstRunes := len([]rune(first))

	if len(titles) >= 2 {
		last := titles[len(titles)-1]
		lastRunes := len([]rune(last))
		if firstRunes+lastRunes+3 <= maxLength {
			return strings.TrimRight(first, " .") + "{ellipsis}" + last
		}
	}
	if len(titles) > 1 && firstRunes+3 <= maxLength {
		return strings.TrimRight(first, " .") + "{ellipsis}"
	}
	if len(titles) == 1 && firstRunes <= maxLength {
		return first
	}
	cap := maxLength - 3
	if cap < 0 {
		cap = 0
	}
	runes := []rune(first)
	if cap > len(runes) {
		cap = len(runes)
	}
	return strings.TrimRight(string(runes[:cap]), " .") + "{ellipsis}"
}

func getMaxLengthFromFormatter(formatter string) int {
	if formatter == "" {
		return 0
	}
	n, err := strconv.Atoi(formatter)
	if err != nil {
		return 0
	}
	return n
}

// AddEpisodeTitleTokens ports `:618-622`. Registers `{Episode Title}`
// + `{Episode CleanTitle}` handlers using GetEpisodeTitles + the
// title-cleaning pipeline.
//
// `maxLength` is the max characters to allocate to the episode title
// portion of the filename — the caller computes this from the
// remaining budget after the other tokens render.
func AddEpisodeTitleTokens(handlers map[string]TokenHandler, eps []episodes.Episode, maxLength int) {
	titles := GetEpisodeTitles(eps)
	handlers[NormalizeTokenKey("Episode Title")] = func(tm TokenMatch) string {
		return GetEpisodeTitle(titles, "+", maxLength, tm.CustomFormat)
	}
	handlers[NormalizeTokenKey("Episode CleanTitle")] = func(tm TokenMatch) string {
		cleaned := make([]string, len(titles))
		for i, t := range titles {
			cleaned[i] = CleanTitle(t)
		}
		return GetEpisodeTitle(cleaned, "and", maxLength, tm.CustomFormat)
	}
}

// AddEpisodeTitlePlaceholderTokens ports `:612-616`. Registers
// `{Episode Title}` / `{Episode CleanTitle}` to return the empty
// string — used by the length-pre-flight pass that measures the
// pattern length without the episode title.
func AddEpisodeTitlePlaceholderTokens(handlers map[string]TokenHandler) {
	handlers[NormalizeTokenKey("Episode Title")] = func(TokenMatch) string { return "" }
	handlers[NormalizeTokenKey("Episode CleanTitle")] = func(TokenMatch) string { return "" }
}

// RegisterEpisodeFileTokens ports `AddEpisodeFileTokens` at
// `:624-630`. Registers the 4 episode-file tokens. `useCurrentFilenameAsFallback`
// controls whether `{Original Filename}` falls back to the current
// file path when no scene name is set.
func RegisterEpisodeFileTokens(handlers map[string]TokenHandler, file *episodefiles.File, useCurrentFilenameAsFallback bool) {
	if file == nil {
		// Register no-op handlers so the tokens render to empty
		// rather than passing through unresolved.
		put := func(name string) {
			handlers[NormalizeTokenKey(name)] = func(TokenMatch) string { return "" }
		}
		put("Original Title")
		put("Original Filename")
		put("Release Group")
		put("Release Hash")
		return
	}

	handlers[NormalizeTokenKey("Original Title")] = func(TokenMatch) string {
		return getOriginalTitle(file, useCurrentFilenameAsFallback)
	}
	handlers[NormalizeTokenKey("Original Filename")] = func(TokenMatch) string {
		return getOriginalFileName(file, useCurrentFilenameAsFallback)
	}
	handlers[NormalizeTokenKey("Release Group")] = func(tm TokenMatch) string {
		if strings.TrimSpace(file.ReleaseGroup) == "" {
			return tm.DefaultValue("Sonarr")
		}
		return Truncate(file.ReleaseGroup, tm.CustomFormat)
	}
	handlers[NormalizeTokenKey("Release Hash")] = func(TokenMatch) string {
		return file.ReleaseHash
	}
}

// getOriginalTitle ports `:1116-1124`. SceneName takes priority; falls
// back to the original filename (sans extension) when SceneName is
// empty.
func getOriginalTitle(file *episodefiles.File, useCurrentFilenameAsFallback bool) string {
	if strings.TrimSpace(file.SceneName) == "" {
		return CleanFileNameDefault(getOriginalFileName(file, useCurrentFilenameAsFallback))
	}
	return CleanFileNameDefault(file.SceneName)
}

// getOriginalFileName ports `:1126-1139`. When `useCurrentFilenameAsFallback`
// is false, returns empty. Otherwise uses RelativePath if set, else
// derives from the absolute Path. Strips the file extension via
// `filepath.Base` + extension trim.
func getOriginalFileName(file *episodefiles.File, useCurrentFilenameAsFallback bool) string {
	if !useCurrentFilenameAsFallback {
		return ""
	}
	source := file.RelativePath
	if strings.TrimSpace(source) == "" {
		source = file.OriginalFilePath
	}
	if strings.TrimSpace(source) == "" {
		return ""
	}
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	if ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}
