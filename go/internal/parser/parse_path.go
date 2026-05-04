package parser

import (
	"path/filepath"
)

// ParsePath ports `Parser.ParsePath` at `Parser.cs:569-640`. The
// path-aware wrapper around ParseTitle: tries the filename first,
// then several fallback combinations involving the parent directory.
//
// Strategies attempted in order (mirrors C# control flow):
//
//  1. ParseTitle(fileName).
//  2. *(deferred to 2.13.4b)* Season-folder + episode-number
//     combination: when the filename matches SimpleEpisodeNumberRegex
//     and the parent folder matches SeasonFolderRegex, build a
//     synthetic `S<season>E<episode>(-E<last>)? <remaining>` title
//     and re-parse. Both regexes have lookbehinds + duplicate named
//     groups so they need RE2 reformulation; deferred to a separate
//     slice.
//  3. *(deferred to 2.13.4b)* Numeric-only-filename narrowing: when
//     filename-without-ext is a pure integer, parse the directory
//     name and narrow its abs/episode list to that single value.
//  4. ParseTitle(dirName + " " + fileName).
//  5. ParseTitle(dirName + ext).
//
// Returns nil when every strategy fails.
//
// Path semantics: `filepath.Base` / `filepath.Dir` / `filepath.Ext`
// are platform-aware (`/` on Unix, `\` on Windows). Test fixtures
// targeting both conventions should normalise via `filepath.Clean`
// or use the platform's native separator.
func ParsePath(path string) *ParsedEpisodeInfo {
	fileName := filepath.Base(path)
	if result := ParseTitle(fileName); result != nil {
		return result
	}

	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	dirName := filepath.Base(dir)
	if dirName == "" || dirName == "." {
		return nil
	}

	if result := ParseTitle(dirName + " " + fileName); result != nil {
		return result
	}

	ext := filepath.Ext(fileName)
	return ParseTitle(dirName + ext)
}
