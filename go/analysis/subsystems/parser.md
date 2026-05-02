# Parser

**Source:** `src/NzbDrone.Core/Parser/**`
**Target Go package:** `go/internal/parser/`
**Status:** drafted

## Purpose

Parser is the single most behaviour-critical subsystem in Sonarr: it translates free-form release names ("Series.Name.S01E02.720p.WEB-DL.x264-RLSGRP", anime brackets, daily-show airdates, Chinese release prefixes, six-digit hashes, …) into structured `ParsedEpisodeInfo` records (series title + season + episode numbers + airdate + languages + quality + release group). Every download decision, file import, and episode mapping downstream depends on this parser being byte-stable with the C# implementation. The parser is implemented as a long, ordered list of compiled regexes evaluated first-match-wins, plus a separate `QualityParser`, `LanguageParser`, and `ReleaseGroupParser` that run on the matched release tokens. Rejection regexes (hashed releases, season folders) short-circuit before the main loop.

## Pipeline shape

Input: a single release title string (NZB title, torrent title, or filename). Output: a `ParsedEpisodeInfo` (or `null` when nothing matches).

`Parser.ParseTitle` orchestrates the work; the order is load-bearing.

```
ParseTitle(title)
  └── ValidateBeforeParsing                       (Parser.cs:1180)
        ├── reject "password" + "yenc"
        ├── reject !title.Any(IsLetterOrDigit)
        ├── reject if any RejectHashedReleasesRegexes matches
        └── reject if any SeasonFolderRegexes matches
  └── ReversedTitleRegex check → reverse string  (Parser.cs:651, 681)
  └── strip file extension                        (FileExtensions.RemoveFileExtension)
  └── replace 【...】 with [...]                   (Parser.cs:693)
  └── ParserCommon.PreSubstitutionRegex[]         (each is RegexReplace; first-match-wins via TryReplace)
  └── SimpleTitleRegex.Replace                    (strips resolution/codec tokens)
  └── ParserCommon.WebsitePrefixRegex.Replace
  └── ParserCommon.WebsitePostfixRegex.Replace
  └── ParserCommon.CleanTorrentSuffixRegex.Replace
  └── CleanQualityBracketsRegex.Replace           (only strips the trailing [ ... ] if it parses as a known quality)
  └── SixDigitAirDateRegex normalise to 4-digit year
  └── for each regex in ReportTitleRegex (52 patterns, ordered):
        if Matches.Count != 0:
            ParseMatchCollection(matches, releaseTitle)
            if airYear < 1900 → standard/anime branch (season/episode + absolute episode)
            else → daily branch (airdate, possibly ambiguous m/d swap)
            on success: attach LanguageParser.ParseLanguages, QualityParser.ParseQuality, ReleaseGroupParser.ParseReleaseGroup, GetSubGroup, GetReleaseHash
            return ParsedEpisodeInfo
  └── (no match) → return null
```

`ParsePath(path)` (Parser.cs:569) wraps `ParseTitle` with three fallbacks: (a) parse file name, (b) combine `SeasonFolder` + `SimpleEpisodeNumber` from filename if title was empty/anime/mini-series, (c) parse just the directory name, (d) parse `directoryName + " " + fileName`, (e) parse `directoryName + extension`.

`ParsingService` (the only `IParsingService`) consumes `ParsedEpisodeInfo` and resolves it against the database:

```
indexer/import flow → Parser.ParseTitle → ParsedEpisodeInfo
                   → ParsingService.Map → RemoteEpisode { Series, Episodes[], Quality, Languages }
                   → ParsingService.GetEpisodes (handles scene mapping, anime absolute lookup, daily, special)
                   → ParseSpecialEpisodeTitle (last resort: episode title match)
```

Callers of `Parser.ParseTitle` / `ParsingService` include indexer parsers, `DownloadDecisionMaker`, `MediaFileService`, import-list mappers, history grabbing, manual-import, and SignalR push events.

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `ParsedEpisodeInfo` | `Model/ParsedEpisodeInfo.cs` | model | core output of `ParseTitle`; carries `EpisodeNumbers`, `AbsoluteEpisodeNumbers`, `SpecialAbsoluteEpisodeNumbers`, `AirDate`, `Languages`, `QualityModel`, `SeriesTitle`, `SeriesTitleInfo`, `ReleaseTokens`, flags `FullSeason`/`IsPartialSeason`/`IsMultiSeason`/`IsSeasonExtra`/`IsSplitEpisode`/`IsMiniSeries`/`Special`, `SeasonPart`, `DailyPart`, computed `IsDaily`/`IsAbsoluteNumbering`/`IsPossibleSpecialEpisode`/`IsPossibleSceneSeasonSpecial`/`ReleaseType` |
| `SeriesTitleInfo` | `Model/SeriesTitleInfo.cs` | model | `Title`, `TitleWithoutYear`, `Year`, `AllTitles[]` (for "AKA"/"\|"/parenthesised alt titles) |
| `ReleaseInfo` / `TorrentInfo` / `GrabbedReleaseInfo` / `ImportListItemInfo` | `Model/*.cs` | DTOs | indexer/import inputs that carry `Title` to be parsed |
| `RemoteEpisode` | `Model/RemoteEpisode.cs` | model | `ParsedEpisodeInfo + Series + Episodes[] + Languages + SceneMapping + MappedSeasonNumber + SeriesMatchType + EpisodeRequested` |
| `LocalEpisode` | `Model/LocalEpisode.cs` | model | post-import equivalent for files on disk |
| `FindSeriesResult` | `Model/FindSeriesResult.cs` | enum/struct | series lookup outcome with `MatchType` |
| `IndexerFlags` | `Model/IndexerFlags.cs` | enum (flags) | scene/freeleech/internal/etc. bit flags |
| `ReleaseType` | `Model/ReleaseType.cs` | enum | Unknown / SingleEpisode / MultiEpisode / SeasonPack |
| `ReleaseComparerModel` | `Model/ReleaseComparerModel.cs` | comparison model | used by `ReleaseComparer.cs` |
| `SubtitleTitleInfo` | `Model/SubtitleTitleInfo.cs` | model | output of `LanguageParser.ParseSubtitleLanguageInformation` |
| `Resolution` | `QualityParser.cs:714` | enum | R360p/R480p/R540p/R576p/R720p/R1080p/R2160p/Unknown |
| `IsoLanguage` | `IsoLanguage.cs` | model | `TwoLetterCode`, `ThreeLetterCode`, `Language` mapping |
| `InvalidDateException` | `InvalidDateException.cs` | exception | thrown by daily-branch on impossible/future date |
| `InvalidSeasonException` | `InvalidSeasonException.cs` | exception | thrown by season validation |

## Public services

| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|
| `Parser` (static) | `ParsePath(string)`, `ParseTitle(string)`, `ParseSeriesName(string)`, `CleanSeriesTitle(this string)`, `NormalizeEpisodeTitle(string)`, `NormalizeTitle(string)`, `NormalizeImdbId(string)`, `RemoveFileExtension(string)`, `HasMultipleLanguages(string)`, `SimplifyTitle(string)` | logs only | the one-stop title parser; consumed everywhere |
| `QualityParser` (static) | `ParseQuality(string)`, `ParseQualityName(string)` | logs only | runs Source/Resolution/Codec/Remux/Proper/Repack/Real/Version regexes and resolves to `Quality` enum |
| `LanguageParser` (static) | `ParseLanguages(string)`, `ParseSubtitleLanguage(string)`, `ParseBasicSubtitle(string)`, `ParseSubtitleLanguageInformation(string)`, `UpdateTitleAndCopyFromTitle(SubtitleTitleInfo)`, `ParseLanguageTags(string)` | logs only | substring-contains pass + `LanguageRegex` + `CaseSensitiveLanguageRegex` + German DL/ML special handling |
| `ReleaseGroupParser` (static) | `ParseReleaseGroup(string)` | logs only | strips known prefixes, then exception lists, then trailing `-Group` regex |
| `SceneChecker` (static) | `IsSceneTitle(string)` | logs only | requires `.` + no spaces + non-empty parsed result |
| `IsoLanguages` (static) | `Find(string code)`, `FindByName(string)` | none | lookup table for ISO 639-1/2 codes |
| `IParsingService` / `ParsingService` | `GetSeries(string)`, `Map(...)` (3 overloads), `GetEpisodes(parsedEpisodeInfo, series, sceneSource, searchCriteria)`, `ParseSpecialEpisodeTitle(...)` (2 overloads) | reads `IEpisodeService`, `ISeriesService`, `ISceneMappingService` | DB-aware translation from parsed info to series/episode rows; handles scene mappings, special-episode title fallback |
| `ValidateParsedEpisodeInfo` (static) | `ValidateForSeriesType(parsedEpisodeInfo, series, warnIfInvalid)` | logs only | reject daily release matched to a Standard series |
| `ReleaseComparer` | `IComparer<ReleaseInfo>` | none | sort comparator for release queues |
| `RegexReplace` | ctor(pattern, replacement, RegexOptions), `Replace(string)`, `TryReplace(ref string)` | none | thin wrapper used by `ParserCommon.PreSubstitutionRegex` and `LanguageParser.CleanSeriesTitleRegex` |

> Note: the prompt mentioned `ReleaseTitleNormalizer.cs` — no such file exists in the C# tree. The closest equivalents are `Parser.SimplifyTitle`, `Parser.NormalizeTitle`, `Parser.NormalizeEpisodeTitle`, `Parser.CleanSeriesTitle`, plus the `RegexReplace` arrays in `ParserCommon` and `LanguageParser`. Logged in open-question Q-13.

## DB tables touched

`Parser` itself is purely functional. `ParsingService` reads (no writes) via:

- `Series` — `ISeriesService.FindByTitle`, `FindByTvdbId`, `FindByTvRageId`, `FindByImdbId`, `GetSeries(id)`
- `Episodes` — `IEpisodeService.FindEpisode(...)`, `FindEpisodesBySceneNumbering`, `GetEpisodesBySeason`, `GetEpisodes(ids)`
- `SceneMappings` — `ISceneMappingService.FindTvdbId`, `FindSceneMapping`, `GetSceneSeasonNumber` (in-memory cache)

## Events raised (IEventAggregator)

None — Parser is a pure library. Events are raised by callers (e.g. `DownloadEventHub`, `EpisodeImportedEvent`).

## Events consumed

None.

## Config keys

None directly. Indirectly the `MultiLanguage` indexer setting feeds `LanguageParser.HasMultipleLanguages` callers.

## Jobs / commands

None registered by Parser itself.

## External services

None. The parser is offline.

---

## Regex inventory

Conventions:
- `Parser.cs:N` cites the line where each regex is declared.
- The `ReportTitleRegex` array order is the matching priority. Rows are listed top-to-bottom in declaration order (= match priority).
- "verbatim" patterns are reproduced from the C# source. C# regex flags `IgnoreCase`/`Compiled`/`IgnorePatternWhitespace` are noted in a separate column.

### `Parser.cs` — `ReportTitleRegex[]` (52 entries, ordered, first-match-wins)

| Line | Index | Intent | Pattern | Flags |
|---|---|---|---|---|
| 25 | (commented out) | Anime - Absolute episode + Title + Season+Episode (disabled because it breaks numeric titles) | `^(?:(?<absoluteepisode>\d{2,3})(?:_|-|\s|\.)+)+(?<title>.+?)(?:\W|_)+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-|[ex]|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+)` | i, c |
| 29 | 0 | Daily episode, year in title + airtime suffix (Plex DVR) | `^^(?<title>.+?\((?<titleyear>\d{4})\))[-_. ]+(?<airyear>19[4-9]\d|20\d\d)(?<sep>[-_]?)(?<airmonth>0\d|1[0-2])\k<sep>(?<airday>[0-2]\d|3[01])[-_. ]\d{2}[-_. ]\d{2}[-_. ]\d{2}` | i, c |
| 33 | 1 | Daily episodes without title (2018-10-12 / 20181012) | `^(?<airyear>19[6-9]\d|20\d\d)(?<sep>[-_]?)(?<airmonth>0\d|1[0-2])\k<sep>(?<airday>[0-2]\d|3[01])(?!\d)` | i, c |
| 37 | 2 | Multi-Part episodes without title (S01E05.S01E06) | `^(?:\W*S(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:e{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c |
| 41 | 3 | Multi-Part episodes without title (1x05.1x06) | `^(?:\W*(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:x{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c |
| 45 | 4 | Episodes without title, multi (S01E04E05, 1x04x05) | `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_]|[ex]){1,2}(?<episode>\d{2,3}(?!\d+))){2,})` | i, c |
| 49 | 5 | Split episodes (S01E05a, S01E05b) | `^(?<title>.+?)(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{2,3}(?!\d+))(?<splitepisode>[a-d])(?:[ _.])))` | i, c |
| 53 | 6 | Episodes without title, single (S01E05, 1x05) | `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{2,3}(?!\d+))))` | i, c |
| 57 | 7 | Anime - `[SubGroup] Title Absolute (Season+Episode)` | `^(?:\[(?<subgroup>.+?)\](?:_|-|\s|\.)?)(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+))(?:[-_. ])+\((?:S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]|\W[ex]){1,2}(?<episode>\d{2}(?!\d+))))(?:v\d+)?(?:\)(?!\d+)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 61 | 8 | Anime - `[SubGroup] Title Absolute - Season+Episode` | `^(?:\[(?<subgroup>.+?)\](?:_|-|\s|\.)?)(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+))(?:[-_. ](?<![()\[!]))+(?:S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]|\W[ex]){1,2}(?<episode>\d{2}(?!\d+))))(?:v\d+)?(?:[_. ](?!\d+)).*?(?<hash>[(\[]\w{8}[)\]])$` | i, c |
| 65 | 9 | Anime - `[SubGroup] Title Season+Episode` | `^(?:\[(?<subgroup>.+?)\](?:_|-|\s|\.)?)(?<title>.+?)(?:[-_\W](?<![()\[!]))+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+)(?:v\d+)?(?:[_. ](?!\d+)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 69 | 10 | Anime - `[SubGroup] Title Episode AbsoluteEpisodeNumber` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?(?:Episode)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+)))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 73 | 11 | Anime - `[SubGroup] Title Absolute + Season+Episode` | `^(?:\[(?<subgroup>.+?)\](?:_|-|\s|\.)?)(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<absoluteepisode>\d{2,3}(\.\d{1,2})?))+(?:_|-|\s|\.)+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-|[ex]|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+).*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.)` | i, c |
| 77 | 12 | Anime - `[SubGroup] Title Season+Episode + Absolute` | `^(?:\[(?<subgroup>.+?)\](?:_|-|\s|\.)?)(?<title>.+?)(?:[-_\W](?<![()\[!]))+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-|[ex]|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+)(?:(?:_|-|\s|\.)+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+|\-[a-z])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 81 | 13 | Anime - `[SubGroup] Title Absolute~Absolute` (tilde batch) | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?[^-]+?)(?:(?<![-_. ]|\b[0]\d+) - )[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+))\s?~\s?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+))(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 85 | 14 | Anime - `[SubGroup] Title (Season N) Absolute` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>[^-]+?)[_. ]+?\(Season[_. ](?<season>\d+)\)[-_. ]+?(?:[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 89 | 15 | Anime - `[SubGroup] Title with trailing 3-digit + sub title - Absolute` | `^\[(?<subgroup>[^\]]+?)\][-_. ]?(?<title>[^]]+?)(?:[-_. ]{3}?(?<absoluteepisode>\d{2}(\.\d{1,2})?(?!-?\d+|-[a-z]+)))+(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 93 | 16 | Anime - `[SubGroup] Title with trailing - Absolute` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>[^-]+?)(?:(?<![-_. ]|\b[0]\d+) - )(?:[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 97 | 17 | Anime - `[SubGroup] Title Season SXX` (full season) | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)[-_. ]+(?:S(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?![ex]?\d+))).+?(?:$|\.mkv)` | i, c |
| 101 | 18 | Anime - `[SubGroup] Title with trailing-3digit Absolute` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>[^-]+?)(?:(?<![-_. ]|\b[0]\d+)[_ ]+)(?:[-_. ]?(?<absoluteepisode>\d{3}(\.\d{1,2})?(?!\d+|-[a-z]+)))+(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 105 | 19 | Anime - `[SubGroup] Title - Absolute` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:(?<!\b[0]\d+))(?:[. ]-[. ](?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+|[-])))+(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 109 | 20 | Anime - `[SubGroup] Title Absolute - Absolute` (batch) | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:(?<!\b[0]\d+))(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+|[-]))[. ]-[. ](?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+|[-]))(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 113 | 21 | Anime - `[SubGroup] Title Absolute` (basic) | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)[-_. ]+\(?(?:[-_. ]?#?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+|-[a-z]+)))+\)?(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |
| 117 | 22 | Multi-episode repeated `(S01E05 - S01E06)` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:e|[-_. ]e){1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c |
| 121 | 23 | Multi-episode repeated `(1x05 - 1x06)` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:x{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c |
| 125 | 24 | Single ep with title + ".5 [SP]" | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]|\W[ex]|_){1,2}(?<episode>\d{2,3}(?!\d+|(?:[ex]|\W[ex]|_|-){1,2}\d+))(?<special>\.5[ .]\[SP\]))` | i, c |
| 129 | 25 | Single ep + trailing info in `[...]` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]|\W[ex]|_){1,2}(?<episode>\d{2,3}(?!\d+|(?:[ex]|\W[ex]|_|-){1,2}\d+))).+?(?:\[.+?\])(?!\\)` | i, c |
| 133 | 26 | Anime - `Title Season Episode + Absolute [SubGroup]` | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]|\W[ex]|-){1,2}(?<episode>(?<!\d+)\d{2}(?!\d+)))+)[-_. (]+?(?:[-_. ]?(?<absoluteepisode>(?<!\d+)\d{3}(\.\d{1,2})?(?!\d+|[pi])))+.+?\[(?<subgroup>.+?)\](?:$|\.mkv)` | i, c |
| 137 | 27 | Multi-ep with title (S01E05E06, S01E05-06) + trailing `[..]` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]|\W[ex]|_){1,2}(?<episode>\d{2,3}(?!\d+))(?:(?:\-|[ex]|\W[ex]|_){1,2}(?<episode>\d{2,3}(?!\d+)))+).+?(?:\[.+?\])(?!\\)` | i, c |
| 141 | 28 | Anime - Title Absolute `[SubGroup] [Hash]?` (Episode 99-100 batch) | `^(?<title>.+?)[-_. ]Episode(?:[-_. ]+(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:.+?)\[(?<subgroup>.+?)\].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 145 | 29 | Anime - Title Absolute(.x) `[SubGroup] [Hash]` | `^(?<title>.+?)(?:(?:_|-|\s|\.)+(?<absoluteepisode>\d{3}(\.\d{1,2})(?!\d+)))+(?:.+?)\[(?<subgroup>.+?)\].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 149 | 30 | Anime - Title Absolute (Year) `[SubGroup]` | `^(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2}(?!\d+))[-_. ](\(\d{4}\))[-_. ]\[(?<subgroup>.+?)\]` | i, c |
| 153 | 31 | Anime - Title with trailing num, Absolute, hash | `^(?<title>[^-]+?)(?:(?<![-_. ]|\b[0]\d+) - )(?:[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special|ova|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])(?:$|\.mkv)` | i, c |
| 157 | 32 | Anime - Title Absolute `[Hash]` | `^(?<title>.+?)(?:(?:_|-|\s|\.)+(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special|ova|ovd))?[-_. ]+.*?(?<hash>[(\[]\w{8}[)\]])$` | i, c |
| 161 | 33 | Episodes with airdate AND season/ep, capture S/E only | `^(?<title>.+?)?\W*(?<airdate>\d{4}\W+[0-1][0-9]\W+[0-3][0-9])(?!\W+[0-3][0-9])[-_. ](?:s?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+)))(?:[ex](?<episode>(?<!\d+)(?:\d{1,3})(?!\d+)))` | i, c |
| 165 | 34 | Episodes with airdate AND season/ep | `^(?<title>.+?)?\W*(?<airyear>\d{4})\W+(?<airmonth>[0-1][0-9])\W+(?<airday>[0-3][0-9])(?!\W+[0-3][0-9]).+?(?:s?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+)))(?:[ex](?<episode>(?<!\d+)(?:\d{1,3})(?!\d+)))` | i, c |
| 169 | 35 | Episodes with absolute ep AND airdate (TJET Wrestling) | `^(?<title>.+?)?[-_. ](?:e\d{2,3}(?!\d+))[-_. ](?<airyear>\d{4})\W+(?<airmonth>[0-1][0-9])\W+(?<airday>[0-3][0-9])(?!\W+[0-3][0-9]).+?(?:\[[a-z]+\])` | i, c |
| 173 | 36 | Multi-title with `S1E1-2 of 6` after the last title | `^((?<title>.*?)[ ._]\/[ ._])+\(?S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W|_)?E?[ ._]?(?<episode>(?<!\d+)\d{1,2}(?!\d+))(?:-(?<episode>(?<!\d+)\d{1,2}(?!\d+)))?(?:[ ._]of[ ._](?<episodecount>\d{1,2}))?\)?[ ._][\(\[]` | i, c |
| 177 | 37 | Multi-ep with title (S01E99-100, S01E05-06) | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))E(?<episode>\d{2,3}(?!\d+))(?:-(?<episode>\d{2,3}(?!\d+)))+(?:[-_. ]|$)` | i, c |
| 181 | 38 | Multi-ep with title (S01E05-06, S01E05-6) | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))E(?<episode>\d{1,2}(?!\d+))(?:-(?<episode>\d{1,2}(?!\d+)))+(?:[-_. ]|$)` | i, c |
| 185 | 39 | Chinese + English title + Single/Multi ep | `^(?<title>[一-鿌]+)[_. ](?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]|\W[ex]){1,2}(?<episode>\d{2,3}(?!\d+))(?:(?:\-|[ex]|\W[ex]|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)(?:[-_. ]|$)` | i, c |
| 189 | 40 | Generic single + multi episode with title (workhorse) | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]|\W[ex]){1,2}(?<episode>\d{2,3}(?!\d+))(?:(?:\-|[ex]|\W[ex]|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)(?:[-_. ]|$)` | i, c |
| 193 | 41 | Title + 4-digit season Sxxxx + ExNN | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{4})(?!\d+))(?:e|\We|_){1,2}(?<episode>\d{2,4}(?!\d+))(?:(?:\-|e|\We|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)\W?(?!\\)` | i, c |
| 197 | 42 | Title + 4-digit season YYYYx05 | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<season>(?<!\d+)(?:\d{4})(?!\d+))(?:x|\Wx){1,2}(?<episode>\d{2,4}(?!\d+))(?:(?:\-|x|\Wx|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)\W?(?!\\)` | i, c |
| 201 | 43 | Title + s01.05 form | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{2})(?!\d+))(?:\.)(?<episode>\d{2,3}(?!\d+))(?:[-_. ]|$)` | i, c |
| 205 | 44 | Multi-season pack (Sxx-Syy / SeasonNN-Mm) | `^(?<title>.+?)(Complete Series)?[-_. ]+(?:S|(?:Season|Saison|Series|Stagione)[_. ])(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[-_. ]{1}|[-_. ]{3})(?:S|(?:Season|Saison|Series|Stagione)[_. ])?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))` | i, c |
| 209 | 45 | Partial season pack | `^(?<title>.+?)(?:\W+S(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))\W+(?:(?:(?:Part|Vol)\W?|(?<!\d+\W+)e|p)(?<seasonpart>\d{1,2}(?!\d+)))+)` | i, c |
| 213 | 46 | Anime - 4-digit absolute episode batch (1017-1088) | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?\(?(?<absoluteepisode>\d{4}(\.\d{1,2})?(?!\d+))[-](?<absoluteepisode>\d{4}(\.\d{1,2})?(?!\d+))\)?` | i, c |
| 217 | 47 | Anime - 4-digit absolute single | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?(?<absoluteepisode>\d{4}(\.\d{1,2})?(?!\d+))` | i, c |
| 221 | 48 | Anime - Title 4-digit Absolute `[SubGroup]` | `^(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{4}(?!\d+))[-_. ]\[(?<subgroup>.+?)\]` | i, c |
| 225 | 49 | Mini-Series with year, Part01/Part 01/Part.1 | `^(?<title>.+?\d{4})(?:\W+(?:(?:Part\W?|e)(?<episode>\d{1,2}(?!\d+)))+)` | i, c |
| 229 | 50 | Mini-Series multi-ep `E1-E2` | `^(?<title>.+?)(?:[-._ ][e])(?<episode>\d{2,3}(?!\d+))(?:(?:\-?[e])(?<episode>\d{2,3}(?!\d+)))+` | i, c |
| 233 | 51 | Episodes with airdate + Part (2018.04.28.Part.2) | `^(?<title>.+?)?\W*(?<airyear>\d{4})[-_. ]+(?<airmonth>[0-1][0-9])[-_. ]+(?<airday>[0-3][0-9])(?![-_. ]+[0-3][0-9])[-_. ]+Part[-_. ]?(?<part>[1-9])` | i, c |
| 239 | 52 | 3-digit season `S001E01[-E02]` | `^(?<title>.+?)[-_. ]+S(?<season>(?<!\d+)\d{3}(?!\d+))E(?<episode>(?<!\d+)\d{2}(?!\d+))(?:-[ex](?<episode>(?<!\d+)\d{2}(?!\d+)))?([_. ]|$)(?!\\)` | i, c |
| 243 | 53 | Mini-Series episodes labelled `XofY` | `^(?<title>.+?)(?:\W+(?:(?<episode>(?<!\d+)\d{1,2}(?!\d+))of\d+)+)` | i, c |
| 247 | 54 | "Season 01 Episode 03" | `(?:.*(?:\""|^))(?<title>.*?)(?:[-_\W](?<![()\[]))+(?:\W?Season\W?)(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W|_)+(?:Episode\W)(?:[-_. ]?(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+` | i, c |
| 251 | 55 | Multi-ep in `[S01E11E12]` / `[S01E11-12]` | `(?:.*(?:^))(?<title>.*?)[-._ ]+\[S(?<season>(?<!\d+)\d{2}(?!\d+))(?:[E-]{1,2}(?<episode>(?<!\d+)\d{2}(?!\d+)))+\]` | i, c |
| 255 | 56 | Multi-ep in `(S01E11E12)` / `(S01E11-12)` / `(S01E1-3 of 12)` | `(?:.*(?:^))(?<title>.*?)[-._ ]+\(S(?<season>(?<!\d+)\d{2}(?!\d+))(?:[E-]{1,2}(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+([._ ]+of[._ ]+[\dx]+)?\)` | i, c |
| 259 | 57 | Multi-ep release no space (S01E11E12) | `(?:.*(?:^))(?<title>.*?)S(?<season>(?<!\d+)\d{2}(?!\d+))(?:E(?<episode>(?<!\d+)\d{2}(?!\d+)))+` | i, c |
| 263 | 58 | Multi-ep S6.E1-E2 / S6.E1E2 | `^(?<title>.+?)[-_. ]S(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:[-_. ]?[ex]?(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+` | i, c |
| 267 | 59 | Multi-titles in brackets, each with S/E | `^(?<title>.*?)[ ._]\(S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W|_)?E?[ ._]?(?<episode>(?<!\d+)\d{1,2}(?!\d+))(?:-(?<episode>(?<!\d+)\d{1,2}(?!\d+)))?\)(?:[ ._]\/[ ._])(?<title>.*?)[ ._]\(` | i, c |
| 271 | 60 | Multi-ep `(S1E11-12)` 1-digit season | `(?:.*(?:^))(?<title>.*?)[-._ ]+\(S(?<season>(?<!\d+)\d{1}(?!\d+))(?:[E-]{1,2}(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+([._ ]+of[._ ]+[\dx]+)?\)` | i, c |
| 275 | 61 | Single ep S1E1 / S1.Ep1 / S01.Ep.01 | `(?:.*(?:\""|^))(?<title>.*?)(?:\W?|_)S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W|_)?Ep?[ ._]?(?<episode>(?<!\d+)\d{1,2}(?!\d+))` | i, c |
| 279 | 62 | 3 digit season S010E05 | `(?:.*(?:\""|^))(?<title>.*?)(?:\W?|_)S(?<season>(?<!\d+)\d{3}(?!\d+))(?:\W|_)?E(?<episode>(?<!\d+)\d{1,2}(?!\d+))` | i, c |
| 283 | 63 | 5-digit episode number with title | `^(?:(?<title>.+?)(?:_|-|\s|\.)+)(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+)))(?:(?:\-|[ex]|\W[ex]|_){1,2}(?<episode>(?<!\d+)\d{5}(?!\d+)))` | i, c |
| 287 | 64 | 5-digit multi-ep with title | `^(?:(?<title>.+?)(?:_|-|\s|\.)+)(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+)))(?:(?:[-_. ]{1,3}ep){1,2}(?<episode>(?<!\d+)\d{5}(?!\d+)))+` | i, c |
| 291 | 65 | Separated season+ep `S01 - E01` | `^(?<title>.+?)(?:_|-|\s|\.)+S(?<season>\d{2}(?!\d+))(\W-\W)E(?<episode>(?<!\d+)\d{2}(?!\d+))(?!\\)` | i, c |
| 297 | 66 | Bracketed S/E `Title - [02x01] - …` | `^(?<title>.+?)?(?:[-_\W](?<![()\[!]))+\[(?:s)?(?<season>(?<!\d+)\d{1,2})(?:(?:[ex])(?<episode>\d{2}))(?:(?:[-ex]){1,2}(?<episode>\d{2}))*\].+?(?:\.|$)` | i, c |
| 301 | 67 | Anime - `Title S01 - EP14` | `^(?<title>.+?S\d{1,2})[-_. ]{3,}(?:EP)?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+|[-]))` | i, c |
| 305 | 68 | Anime - French `[Rls] Title - Episode 1` (single digit) | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)[-_. ]+?(?:Episode[-_. ]+?)(?<absoluteepisode>\d{1}(\.\d{1,2})?(?!\d+))` | i, c |
| 309 | 69 | Anime - Absolute episode in `[NN]` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?\[(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+))\]` | i, c |
| 313 | 70 | Japanese variety show with leading date `YYMMDD Title…` | `^(?<airyear>\d{2})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9])(?![-_. ]+[0-3][0-9])[-_. ](?<title>.+?)[-_. ](?:Season[-_. ]?(?<season>\d{1,2})[-_. ])?(?:ep|#)(?<episode>\d{2,3})` | i, c |
| 317 | 71 | Season-only releases followed by year | `^(?<title>.+?)[-_. ]+?(?:S|Season|Saison|Series|Stagione)[-_. ]?(?<season>\d{1,2}(?=[-_. ]\d{4}[-_. ]+))(?<extras>EXTRAS|SUBPACK)?(?!\\)` | i, c |
| 321 | 72 | Season-only releases | `^(?<title>.+?)[-_. ]+?(?:S|Season|Saison|Series|Stagione)[-_. ]?(?<season>\d{1,2}(?![-_. ]?\d+))(?:[-_. ]|$)+(?<extras>EXTRAS|SUBPACK)?(?!\\)` | i, c |
| 325 | 73 | 4-digit season-only releases | `^(?<title>.+?)[-_. ]+?(?:S|Season|Saison|Series|Stagione)[-_. ]?(?<season>\d{4}(?![-_. ]?\d+))(\W+|_|$)(?<extras>EXTRAS|SUBPACK)?(?!\\)` | i, c |
| 329 | 74 | Spanish tracker `Temporada [Cap NNNN]` | `^(?<title>.+?)(?:(?:[-_. ]+?Temporada.+?|\[.+?\])\[Cap)(?:[-_. ]+(?<season>(?<!\d+)\d{1,2})(?<episode>(?<!e|x)(?:[1-9][0-9]|[0][1-9])))+(?:\])` | i, c |
| 333 | 75 | Episodes with airdate (2018.04.28) | `^(?<title>.+?)?\W*(?<airyear>\d{4})[-_. ]+(?<airmonth>[0-1][0-9])[-_. ]+(?<airday>[0-3][0-9])(?![-_. ]+[0-3][0-9])` | i, c |
| 337 | 76 | 103/113 implicit S+E packing | `^(?<title>.+?)?(?:(?:[_.-](?<![()\[!]))+(?<season>(?<!\d+)[1-9])(?<episode>[1-9][0-9]|[0][1-9])(?![a-z]|\d+))+(?:[_.]|$)` | i, c |
| 342 | 77 | 4-digit episode number, no title | `^(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-|[ex]|\W[ex]|_){1,2}(?<episode>\d{4}(?!\d+|i|p)))+)(\W+|_|$)(?!\\)` | i, c |
| 347 | 78 | 4-digit episode number with title | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]|\d{1,2}-))+S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-|[ex]|\W[ex]|_){1,2}(?<episode>\d{4}(?!\d+|i|p)))+)\W?(?!\\)` | i, c |
| 351 | 79 | Turkish `01 BLM`, `04.Bolum` | `^(?<title>.+?)[_. ](?<absoluteepisode>\d{1,4})(?:[_. ]+)(?:BLM|B[oö]l[uü]m)` | i, c |
| 354 | 80 | Episodes with airdate (04.28.2018) | `^(?<title>.+?)?\W*(?<ambiguousairmonth>[0-1][0-9])[-_. ]+(?<ambiguousairday>[0-3][0-9])[-_. ]+(?<airyear>\d{4})(?!\d+)` | i, c |
| 358 | 81 | Episodes with airdate (28.04.2018) | `^(?<title>.+?)?\W*(?<ambiguousairday>[0-3][0-9])[-_. ]+(?<ambiguousairmonth>[0-1][0-9])[-_. ]+(?<airyear>\d{4})(?!\d+)` | i, c |
| 362 | 82 | Episodes with airdate (20180428) | `^(?<title>.+?)?\W*(?<!\d+)(?<airyear>\d{4})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9])(?!\d+)` | i, c |
| 366 | 83 | Anime - Title E195 / E1206 | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:(?:_|-|\s|\.)+(?:e|ep)(?<absoluteepisode>(\d{3}|\d{4})(\.\d{1,2})?))+[-_. ].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 370 | 84 | Daily episodes with short month (Jan/Feb/…) | `^(?<title>.+?)[-_. ]+(?<airday>[1-2]\d|3[01]|[1-9])(?:th|st|rd)[-_. ](?<shortairmonth>jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[-_. ](?<airyear>19[4-9]\d|20\d\d)` | i, c |
| 374 | 85 | 1103/1113 implicit S+E packing | `^(?<title>.+?)?(?:(?:[-_. ](?<![()\[!]))*(?<!\d{1,2}-)(?<season>(?<!\d+|\(|\[|e|x)\d{2})(?<episode>(?<!e|x)(?:[1-9][0-9]|[0][1-9])(?!p|i|\d+|\)|\]|\W\d+|\W(?:e|ep|x)\d+)))+([-_. ]+|$)(?!\\)` | i, c |
| 378 | 86 | Dutch/Flemish `Se.X afl.Y` | `^(?<title>.+?)[-_. ](?:Se\.(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_ ]?afl\.)(?<episode>\d{1,3}(?!\d+))(?:(?:[-]|[-_ ]en[-_ ])(?<episode>\d{1,3}(?!\d+)))*))` | i, c |
| 382 | 87 | Single digit episode (S01E1, S01E5E6) | `^(?<title>.*?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-|[ex]){1,2}(?<episode>\d{1}))+)+(\W+|_|$)(?!\\)` | i, c |
| 386 | 88 | iTunes `Season 1\05 Title` | `^(?:Season(?:_|-|\s|\.)(?<season>(?<!\d+)\d{1,2}(?!\d+)))(?:_|-|\s|\.)(?<episode>(?<!\d+)\d{1,2}(?!\d+))` | i, c |
| 390 | 89 | iTunes `1-05 Title` | `^(?:(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:-(?<episode>\d{2,3}(?!\d+))))` | i, c |
| 394 | 90 | Mini-Series `Part01 / Part 01 / Part.1` | `^(?<title>.+?)(?:\W+(?:(?:(?<!\()Part\W?|(?<!\d+\W+)e)(?<episode>\d{1,2}(?!\d+|\))))+)` | i, c |
| 398 | 91 | Mini-Series Part One/Two/Three…Nine | `^(?<title>.+?)(?:\W+(?:Part[-._ ](?<episode>One|Two|Three|Four|Five|Six|Seven|Eight|Nine)(?>[-._ ])))` | i, c |
| 402 | 92 | Anime range `epAA-BB` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:_|\s|\.)+(?:e|ep)(?<absoluteepisode>\d{2,3}(\.\d{1,2})?)-(?<absoluteepisode>(?<!\d+)\d{1,2}(\.\d{1,2})?(?!\d+|-)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 406 | 93 | Anime - Title `e66` form | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:(?:_|-|\s|\.)+(?:e|ep)(?<absoluteepisode>\d{2,4}(\.\d{1,2})?))+[-_. ].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 410 | 94 | Anime - Title Episode N | `^(?<title>.+?)[-_. ](?:Episode)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+)))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 414 | 95 | Anime range 1- or 2-digit (1-10) | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)[_. ]+(?<absoluteepisode>(?<!\d+)\d{1,2}(\.\d{1,2})?(?!\d+))-(?<absoluteepisode>(?<!\d+)\d{1,2}(\.\d{1,2})?(?!\d+|-)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 418 | 96 | Anime - Title Episode/Episodio Absolute | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)[-_. ]+(?:Episode|Episodio)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,4}(\.\d{1,2})?(?!\d+|[ip])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 422 | 97 | Anime - AniLibriaTV `[N]` form | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:[-_. ]\[)(?:(?:-?)(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+|[ip])))+(?:\][-_. ]).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 426 | 98 | Series (year) - Temporada N | `^(?<title>.+?)[_. ]-[_. ](?:Temporada)[-_. ]?(?<season>\d{1,2}(?![_. ]?\d+))(?:[_. ]|$)+(?<extras>EXTRAS|SUBPACK)?(?!\\)` | i, c |
| 430 | 99 | Anime - Title Absolute (general) | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,4}(\.\d{1,2})?(?!\d+|[ip])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 434 | 100 | Anime - Title `{Absolute}` (general) | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+|[ip])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c |
| 438 | 101 | "extant.10708.hdtv-lol.mp4" terrible-multi | `^(?<title>.+?)[-_. ](?<season>[0]?\d?)(?:(?<episode>\d{2}){2}(?!\d+))[-_. ]` | i, c |
| 442 | 102 | Season-only for poorly-named anime | `^(?:\[(?<subgroup>.+?)\][-_. ])?(?<title>.+?)[-_. ]+?[\[(](?:S|Season|Saison|Series|Stagione)[-_. ]?(?<season>\d{1,2}(?![-_. ]?\d+))(?:[-_. )\]]|$)+(?<extras>EXTRAS|SUBPACK)?(?!\\)` | i, c |
| 446 | 103 | Episodes without title, single (S1E1, 1x1) | `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{1}(?!\d+))))` | i, c |
| 450 | 104 | Anime OVA special | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:[-_. ]+(?<special>special|ova|ovd|ncop|nced)).*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)` | i, c |

Total `ReportTitleRegex` entries: 105 indices but only 105 are evaluated; one (line 25) is commented out. **104 active entries.** (Note: index numbering above is 0-based and some entries split into multiple lines; for porting, count by `new Regex(` occurrences in the array.)

### `Parser.cs` — `SpecialEpisodeTitleRegex[]` (2 entries, ordered)

| Line | Variable | Intent | Pattern | Flags |
|---|---|---|---|---|
| 456 | `[0]` | Special episode title from `.S\d+E00.<title>.<quality>` | `\.S\d+E00\.(?<episodetitle>.+?)(?:\.(?:720p|1080p|2160p|HDTV|WEB|WEBRip|WEB-DL)\.|$)` | i, c |
| 459 | `[1]` | Special episode title from `.S\d+.Special.<title>.<quality>` | `\.S\d+\.Special\.(?<episodetitle>.+?)(?:\.(?:720p|1080p|2160p|HDTV|WEB|WEBRip|WEB-DL)\.|$)` | i, c |

### `Parser.cs` — `RejectHashedReleasesRegexes[]` (12 entries)

| Line | Intent | Pattern | Flags |
|---|---|---|---|
| 466 | md5 / mixed-case hash 32 chars | `^[0-9a-zA-Z]{32}` | c |
| 469 | shorter lower-case hash 24 chars | `^[a-z0-9]{24}$` | c |
| 473 | NZBGeek `^[A-Z]{11}\d{3}$` | `^[A-Z]{11}\d{3}$` | c |
| 474 | NZBGeek `^[a-z]{12}\d{3}$` | `^[a-z]{12}\d{3}$` | c |
| 477 | Backup_NNNNNSnn-mm | `^Backup_\d{5,}S\d{2}-\d{2}$` | c |
| 480 | literal "123" | `^123$` | c |
| 483 | literal "abc" | `^abc$` | c, i |
| 486 | "abc-_. xyz" prefix | `^abc[-_. ]xyz` | c, i |
| 489 | literal "b00bs" | `^b00bs$` | c, i |
| 492 | NNNNNN_NN | `^\d{6}_\d{2}$` | (none) |
| 495 | hash 30 chars | `^[0-9a-zA-Z]{30}` | c |
| 498 | hash 26 chars | `^[0-9a-zA-Z]{26}` | c |
| 501 | hash 39 chars | `^[0-9a-zA-Z]{39}` | c |
| 504 | hash 24 chars | `^[0-9a-zA-Z]{24}` | c |

### `Parser.cs` — single-instance regexes

| Line | Variable | Intent | Pattern | Flags |
|---|---|---|---|---|
| 509 | `SeasonFolderRegexes[0]` | reject pure season-folder names | `^(Season[ ._-]*\d+|Specials)$` | c |
| 513 | `ReversedTitleRegex` | detect reversed names ("p027", "p0801", "NNESnnS") | `(?:^|[-._ ])(p027|p0801|\d{2,3}E-?\d{2}S)[-._ ]` | c |
| 515 | `NormalizeRegex` (RegexReplace) | strip articles+punct for comparison | `((?:\b|_)(?<!^)([aà](?!$)|an|the|and|or|of)(?!$)(?:\b|_))|\W|_` | i, c |
| 519 | `PercentRegex` | replace `%` with "percent" | `(?<=\b\d+)%` | c |
| 521 | `SimpleTitleRegex` (RegexReplace) | strip resolution/codec/8-bit tokens | `(?:(480|540|576|720|1080|1440|2160)[ip]|[xh][\W_]?26[45]|DD\W?5\W1|[<>?*]|848x480|1280x720|1920x1080|3840x2160|4096x2160|(?<![a-f0-9])(8|10)[ -]?(b(?![a-z0-9])|bit))\s*?` | i, c |
| 527 | `SixDigitAirDateRegex` | normalise YYMMDD → 20YYMMDD | `(?<=[_.-])(?<airdate>(?<!\d)(?<airyear>[1-9]\d{1})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9]))(?=[_.-])` | i, c |
| 530 | `CleanQualityBracketsRegex` | strip trailing `[xxx]` if it parses as quality | `\[[a-z0-9 ._-]+\]$` | i, c |
| 533 | `YearInTitleRegex` | extract optional year-in-title | `^(?<title>.+?)[-_. ]+?[\(\[]?(?<year>\d{4})[\]\)]?` | i, c |
| 536 | `TitleComponentsRegex` | split alt titles "X (Y)", "X \| Y", "X AKA Y" | `^(?:(?<title>.+?) \((?<title>.+?)\)|(?<title>.+?) \| (?<title>.+?)|(?<title>.+?) AKA (?<title>.+?))$` | i, c |
| 539 | `PartRegex` | trailing "(N)" remover | `\(\d+\)$` | c |
| 540 | `PunctuationRegex` | non-word/non-space stripper | `[^\w\s]` | c |
| 541 | `ArticleWordRegex` | leading "a "/"an "/"the " stripper | `^(a|an|the)\s` | i, c |
| 542 | `SpecialEpisodeWordRegex` | (currently disabled) | `\b(part|special|edition|christmas)\b\s?` | i, c |
| 543 | `DuplicateSpacesRegex` | collapse 2+ spaces | `\s{2,}` | c |
| 544 | `SeasonFolderRegex` | extract season number from a folder name | `^(?:S|Season|Saison|Series|Stagione)[-_. ]*(?<season>(?<!\d+)\d{1,4}(?!\d+))(?:[_. ]+(?!\d+)|$)` | i, c |
| 545 | `SimpleEpisodeNumberRegex` | extract episode (and tail) from a filename like `e05`/`5`/`05-06` | `^[ex]?(?<episode>(?<!\d+)\d{1,3}(?!\d+))(?:[ex-](?<episode>(?<!\d+)\d{1,3}(?!\d+)))?(?:[_. ](?!\d+)(?<remaining>.+)|$)` | i, c |
| 547 | `RequestInfoRegex` | strip leading bracketed request info | `^(?:\[.+?\])+` | c |
| 551 | `MultiRegex` | multilanguage tag detector | `[-_. \[](?<multi>multi|multilang|multilanguage)[-_. \]]` | c, i |
| 865 | `imdbRegex` (inline, in `NormalizeImdbId`) | IMDB id form `123` or `tt123` | `^(\d{1,10}|(tt)\d{1,10})$` | (none) |

### `QualityParser.cs`

| Line | Variable | Intent | Pattern | Flags |
|---|---|---|---|---|
| 17 | `SourceRegex` | source family (BluRay/WEB-DL/WEBRip/HDTV/BDRip/BRRip/DVD/DSR/PDTV/SDTV/TVRip) | `\b(?:(?<bluray>BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))|(?<webdl>WEB[-_. ]DL(?:mux)?|WEBDL|AmazonHD|AmazonSD|iTunesHD|MaxdomeHD|NetflixU?HD|WebHD|HBOMaxHD|DisneyHD|[. ]WEB[. ](?:[xh][ .]?26[45]|AVC|HEVC|DDP?[ .]?5[. ]1)|[. ](?-i:WEB)$|(?:720|1080|2160)p[-. ]WEB[-. ]|[-. ]WEB[-. ](?:720|1080|2160)p|\b\s\/\sWEB\s\/\s\b|(?:AMZN|NF|DP)[. -]WEB[. -](?!Rip))|(?<webrip>WebRip|Web-Rip|WEBMux)|(?<hdtv>HDTV)|(?<bdrip>BDRip|BDLight)|(?<brrip>BRRip)|(?<dvd>DVD|DVDRip|NTSC|PAL|xvidvd)|(?<dsr>WS[-_. ]DSR|DSR)|(?<pdtv>PDTV)|(?<sdtv>SDTV)|(?<tvrip>TVRip))(?:\b|$|[ .])` | c, i, x (IgnorePatternWhitespace; multi-line for readability) |
| 32 | `RawHDRegex` | RawHD detection | `\b(?<rawhd>RawHD|Raw[-_. ]HD)\b` | c, i |
| 35 | `MPEG2Regex` | MPEG-2 indicator | `\b(?<mpeg2>MPEG[-_. ]?2)\b` | (none) |
| 37 | `ProperRegex` | "proper" tag | `\b(?<proper>proper)\b` | c, i |
| 40 | `RepackRegex` | "repack" / "rerip" with optional digit | `\b(?<repack>repack\d?|rerip\d?)\b` | c, i |
| 43 | `VersionRegex` | version "v2" forms | `\d[-._ ]?v(?<version>\d)[-._ ]|\[v(?<version>\d)\]|repack(?<version>\d)|rerip(?<version>\d)|(?:480|576|720|1080|2160)p[._ ]v(?<version>\d)` | c, i |
| 46 | `RealRegex` | uppercase REAL | `\b(?<real>REAL)\b` | c |
| 49 | `ResolutionRegex` | named resolution buckets | `\b(?:(?<R360p>360p)|(?<R480p>480p|480i|640x480|848x480)|(?<R540p>540p)|(?<R576p>576p)|(?<R720p>720p|1280x720|960p)|(?<R1080p>1080p|1920x1080|1440p|FHD|1080i|4kto1080p)|(?<R2160p>2160p|3840x2160|4k[-_. ](?:UHD|HEVC|BD|H265)|(?:UHD|HEVC|BD|H265)[-_. ]4k))\b` | c, i |
| 53 | `AlternativeResolutionRegex` | UHD or `[4K]` → 2160p | `\b(?<R2160p>UHD)\b|(?<R2160p>\[4K\])` | c, i |
| 56 | `CodecRegex` | x264/h264/Xvid/divx | `\b(?:(?<x264>x264)|(?<h264>h264)|(?<xvidhd>XvidHD)|(?<xvid>Xvid)|(?<divx>divx))\b` | c, i |
| 59 | `OtherSourceRegex` | "HD-TV" / "SD-TV" forms | `(?<hdtv>HD[-_. ]TV)|(?<sdtv>SD[-_. ]TV)` | c, i |
| 61 | `AnimeBlurayRegex` | anime BluRay (`bd720` etc, or bare `bd`) | `bd(?:720|1080|2160)|(?<=[-_. (\[])bd(?=[-_. )\]])` | c, i |
| 62 | `AnimeWebDlRegex` | anime WEB tag | `\[WEB\]|[\[\(]WEB[ .]` | c, i |
| 64 | `HighDefPdtvRegex` | HR-WS HD PDTV | `hr[-_. ]ws` | c, i |
| 66 | `RemuxRegex` | (BD/UHD) Remux | `(?:[_. ]|\d{4}p-|\bHybrid-)(?<remux>(?:(BD|UHD)[-_. ]?)?Remux)\b|(?<remux>(?:(BD|UHD)[-_. ]?)?Remux[_. ]\d{4}p)` | c, i |

### `LanguageParser.cs`

| Line | Variable | Intent | Pattern (truncated where huge) | Flags |
|---|---|---|---|---|
| 20 | `CleanSeriesTitleRegex[0]` (RegexReplace) | strip everything before `Sxx[Eyy…]` | `.*?[_. ](S\d{2}(?:E\d{2,4})*[_. ].*)` → `$1` | c, i |
| 23 | `LanguagesOnlyRegex` | named-group alternation per language (English/Italian/German/Flemish/Greek/French/Russian/Hungarian/Hebrew/Polish/Chinese/Bulgarian/Spanish/Ukrainian/Thai/Romanian/Catalan/Latvian/Turkish/Urdu/Romansh/Georgian/Japanese/Portuguese/original) | `(?<english>\b(?:ing\|eng)\b)\|(?<italian>\b(?:ita\|italian)\b)\|(?<german>(?:swiss)?german\b\|videomann\|ger[. ]dub\|\bger\b)\|(?<flemish>flemish)\|(?<greek>greek)\|(?<french>(?:_\|\b)(?:FR\|VF\|VF2\|VFF\|VFI\|VFQ\|TRUEFRENCH\|FRENCH\|FRE\|FRA)(?:_\|\b))\|(?<russian>\b(?:rus\|ru)\b)\|(?<hungarian>\b(?:HUNDUB\|HUN)\b)\|(?<hebrew>\bHebDub\b)\|(?<polish>\b(?:PL\W?DUB\|DUB\W?PL\|LEK\W?PL\|PL\W?LEK)\b)\|(?<chinese>\[(?:CH[ST]\|BIG5\|GB)\]\|简\|繁\|字幕\|国语音轨[.+])\|(?<bulgarian>\bbgaudio\b)\|(?<spanish>\b(?:español\|castellano\|esp\|spa(?!\(Latino\)))\b)\|(?<ukrainian>\b(?:\dx?)?(?:ukr))\|(?<thai>\b(?:THAI)\b)\|(?<romanian>\b(?:RoDubbed\|ROMANIAN)\b)\|(?<catalan>[-,. ]cat[. ](?:DD\|subs)\|\b(?:catalan\|catalán)\b)\|(?<latvian>\b(?:lat\|lav\|lv)\b)\|(?<turkish>\b(?:tur)\b)\|(?<urdu>\burdu\b)\|(?<romansh>\b(?:romansh\|rumantsch\|romansch)\b)\|(?<georgian>\b(?:geo\|ka\|kat\|georgian)\b)\|(?<japanese>\(JA\)\|JAP\|JPN)\|(?<portuguese>[_. ]por[_. ])\|(?<original>\b(?:orig\|original)\b)` | i, c |
| 25 | `LanguageRegex` | `LanguagesOnlyRegex` with negative-lookahead to exclude trailing "subs" | composed via interpolation: `(?:{LanguagesOnlyRegex})(?!(?:[-_. ](?:{LanguagesOnlyRegex}))*[-_. ]subs?)` | i, c |
| 28 | `CaseSensitiveLanguageRegex` | uppercase 2-letter codes (LT/CZ/PL/BG/SK/DE) without `SUB` adjacency | `(?:(?i)(?<!SUB[\W\|_\|^]))(?:(?<lithuanian>\bLT\b)\|(?<czech>\bCZ\b)\|(?<polish>\bPL\b)\|(?<bulgarian>\bBG\b)\|(?<slovak>\bSK\b)\|(?<german>\bDE\b))(?:(?i)(?![\W\|_\|^]SUB))` | c |
| 31 | `GermanDualLanguageRegex` | `\bDL\b` not preceded by `WEB` | `(?<!WEB[-_. ]?)\bDL\b` | c, i |
| 32 | `GermanMultiLanguageRegex` | `\bML\b` | `\bML\b` | c, i |
| 34 | `SubtitleLanguageRegex` | trailing iso code + tags in subtitle filenames | `.+?([-_. ](?<tags>forced\|foreign\|default\|cc\|psdh\|sdh))*[-_. ](?<iso_code>[a-z]{2,3})([-_. ](?<tags>forced\|foreign\|default\|cc\|psdh\|sdh))*$` | c, i |
| 36 | `SubtitleLanguageTitleRegex` | iso code + tags + title in subtitle filenames | `.+?(\.((?<tags1>forced\|foreign\|default\|cc\|psdh\|sdh)\|(?<iso_code>[a-z]{2,3})))*[-_. ](?<title>[^.]*)(\.((?<tags2>forced\|foreign\|default\|cc\|psdh\|sdh)\|(?<iso_code>[a-z]{2,3})))*$` | c, i |
| 38 | `SubtitleTitleRegex` | optional `<title> - <copy>` form | `^((?<title>.+) - )?(?<copy>(?<!\d+)\d{1,3}(?!\d+))$` | c |

### `ParserCommon.cs` — `PreSubstitutionRegex[]` (RegexReplace, 14 entries, ordered)

These run before the main `ReportTitleRegex` loop (`Parser.cs:695`). Each is a `RegexReplace.TryReplace(ref title)`; the first one that matches mutates `title` and the loop **continues** to the next regex (it does *not* break — see Parser.cs:696-702). All `Compiled`.

| Line | Intent | Pattern → Replacement |
|---|---|---|
| 12 | Korean F1RST/NEXT releases (no season number) → S01Exxx | `\.E(\d{2,4})\.\d{6}\.(.*-(F1RST\|NEXT))$` → `.S01E$1.$2` |
| 15 | Chinese with English+Chinese title → strip Chinese title | `^\[(?:(?<subgroup>[^\]]+?)(?:[一-鿌]+)?)\]\[(?<title>[^\]]+?)(?:\s(?<chinesetitle>[一-鿌][^\]]*?))\]\[(?:(?:[一-鿌]+?)?(?<episode>\d{1,4})(?:[一-鿌]+?)?)\]` → `[${subgroup}] ${title} - ${episode} - ` |
| 18 | LoliHouse/ZERO/Lilith-Raws/Skymoon-Raws/orion origin bracket normalisation | `^\[(?<subgroup>[^\]]*?(?:LoliHouse\|ZERO\|Lilith-Raws\|Skymoon-Raws\|orion origin)[^\]]*?)\](?<title>[^\[\]]+?)(?: - (?<episode>[0-9-]+)\s*\|\[第?(?<episode>[0-9]+(?:-[0-9]+)?)话?(?:END\|完)?\])\[` → `[${subgroup}][${title}][${episode}][` |
| 21 | Chinese with Sxx season number → Sx | `^\[(?<subgroup>[^\]]+)\](?:\s?★[^\[ -]+\s?)?\[?(?:(?<chinesetitle>(?=[^\]]*?[一-鿌])[^\]]*?)(?:\]\[\|\s*[_/·]\s*)){0,2}(?<title>[^\[\]]+?)(?:\s(?:S?(?<!\d+)((0)(?<season>\d)\|(?<season>[1-9]\d))(?!\d+)))\]?(?:\[\d{4}\])?\[第?(?<episode>[0-9]+(?:-[0-9]+)?)(?:话\|集)?(?: ?END\|完\| ?Fin)?\]` → `[${subgroup}] ${title} S${season} - ${episode} ` |
| 24 | Chinese with no separation between titles | `^\[(?<subgroup>[^\]]+)\]\[(?<chinesetitle>(?<![^a-zA-Z0-9])[^a-zA-Z0-9]+)(?<title>[^\]]+?)\](?:\[\d{4}\])?\[第?(?<episode>[0-9]+(?:-[0-9]+)?)(?:话\|集)?(?: ?END\|完\| ?Fin)?\]` → `[${subgroup}] ${title} - ${episode} ` |
| 27 | Chinese season pack (range, multi-episode) | (very long; see source) → `${title}S${season}E${episode}.${language}${rest}` |
| 30 | Chinese season pack (full season) | (very long) → `${title}S${season}.${language}${rest}` |
| 33 | Chinese anime general bracket→dash form | `^\[(?<subgroup>[^\]]+)\](?:\s?★[^\[ -]+\s?)?\[?(?:(?<chinesetitle>(?=[^\]]*?[一-鿌])[^\]]*?)(?:\]\[\|\s*[_/·]\s*)){0,2}(?<title>[^\]]+?)\]?(?:\[\d{4}\])?\[第?(?<episode>[0-9]{1,4}(?:-[0-9]{1,4})?)(?:话\|集)?(?: ?END\|完\| ?Fin)?\]` → `[${subgroup}] ${title} - ${episode} ` |
| 36 | Chinese+English sep "/" with Sxx → Sx | (long) → `[${subgroup}] ${title} S${season} - ${episode} ` |
| 39 | English+Chinese sep "/" → strip Chinese | (long) → `[${subgroup}] ${title} - ${episode} ` |
| 42 | Chinese+English sep "/" → strip Chinese | (long) → `[${subgroup}] ${title} - ${episode} ` |
| 45 | GM-Team multi-bracket releases | `^\[(?<subgroup>[^\]]+)\](?:(?<chinesubgroup>\[(?=[^\]]*?[一-鿌])[^\]]*\])+)\[(?<title>[^\]]+?)\](?<junk>\[[^\]]+\])*\[(?<episode>[0-9]+(?:-[0-9]+)?)( END\| Fin)?\]` → `[${subgroup}] ${title} - ${episode} ` |
| 48 | Chinese+English sep "\|" → strip Chinese | (long) → `[${subgroup}] ${title} - ${episode} ` |
| 51 | Spanish releases `(year/info)` | `^(?<title>.+?(?=[ ._-]\()).+?\((?<year>\d{4})\/(?<info>S[^\/]+)` → `${title} (${year}) - ${info} ` |

### `ParserCommon.cs` — single regexes

| Line | Variable | Intent | Pattern | Flags |
|---|---|---|---|---|
| 54 | `WebsitePrefixRegex` (RegexReplace) | strip `[www.host.tld]` / `(www.host.tld)` prefix tokens | `^(?:(?:\[\|\()\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?<!Naruto-Kun\.)(?:[a-z]{2,6}\.[a-z]{2,6}\|xn--[a-z0-9-]{4,}\|[a-z]{2,})\b(?:\s*(?:\]\|\))\|[ -]{2,})[ -]*` | i, c |
| 58 | `WebsitePostfixRegex` (RegexReplace) | strip trailing `[host.tld]` postfix | `(?:\[\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?:xn--[a-z0-9-]{4,}\|[a-z]{2,6})\b(?:\s*\])$` | i, c |
| 62 | `CleanTorrentSuffixRegex` (RegexReplace) | strip trailing `[ettv]/[rartv]/[rarbg]/[cttv]/[publichd]` | `\[(?:ettv\|rartv\|rarbg\|cttv\|publichd)\]$` | i, c |

### `ReleaseGroupParser.cs`

| Line | Variable | Intent | Pattern | Flags |
|---|---|---|---|---|
| 9 | `ReleaseGroupRegex` | trailing `-Group` (or `-Group-Subgroup`) at end of title, with negative-lookahead/lookbehind for resolution/codec/2-letter language tags / dates / ranges | `-(?<releasegroup>[A-Za-zÀ-ÖØ-öø-ÿ0-9]+(?<part2>-[A-Za-zÀ-ÖØ-öø-ÿ0-9]+)?(?!.+?(?:HDTV\|SDTV\|480p\|576p\|720p\|1080p\|2160p)))(?<!(?:HDTV\|SDTV\|WEB-DL\|Blu-Ray\|480p\|576p\|720p\|1080p\|2160p\|DTS-HD\|DTS-X\|DTS-MA\|DTS-ES\|-ES\|-EN\|-CAT\|-GER\|-FRA\|-FRE\|-ITA\|\d{1,2}-bit\|[ ._]\d{4}-\d{2}\|-\d{2})(?:\k<part2>)?)(?:\b\|[-._ ]\|$)\|[-._ ]\[(?<releasegroup>[A-Za-zÀ-ÖØ-öø-ÿ0-9]+)\]$` | i, c |
| 12 | `InvalidReleaseGroupRegex` | reject release groups that look like SxxEyy / 8-hex hash | `^([se]\d+\|[0-9a-f]{8})$` | i, c |
| 14 | `AnimeReleaseGroupRegex` | leading `[SubGroup]` capture | `^(?:\[(?<subgroup>(?!\s).+?(?<!\s))\](?:_\|-\|\s\|\.)?)` | i, c |
| 19 | `ExceptionReleaseGroupRegexExact` | manual list (Fight-BB / VARYG / E.N.D / KRaLiMaRKo / BluDragon / DarQ / KCRT / BEN.THE.MEN / TAoE / QxR / Vialle) | `(?:(?<releasegroup>Fight-BB\|VARYG\|E\.N\.D\|KRaLiMaRKo\|BluDragon\|DarQ\|KCRT\|BEN[_. ]THE[_. ]MEN\|TAoE\|QxR\|Vialle)\b)` | i, c |
| 22 | `ExceptionReleaseGroupRegex` | groups whose releases end with `RlsGroup)` / `RlsGroup]` (Joy / ImE / UTR / t3nzin / Anime Time / Project Angel / Hakata Ramen / HONE / Vyndros / SEV / Garshasp / Kappa / Natty / RCVR / SAMPA / YOGI / r00t / EDGE2020) | `(?<=[._ \[])(?<releasegroup>(Joy\|ImE\|UTR\|t3nzin\|Anime Time\|Project Angel\|Hakata Ramen\|HONE\|Vyndros\|SEV\|Garshasp\|Kappa\|Natty\|RCVR\|SAMPA\|YOGI\|r00t\|EDGE2020)(?=\]\|\)))` | i, c |
| 24 | `CleanReleaseGroupRegex` (RegexReplace) | strip leading `…SxxEyy.` and trailing junk like `-RP / -1 / -NZBGeek / -Obfuscated / -postbot / -Rakuv… / -GEROV / -Z0iDS3N / -Chamele0n / -4P / -4Planet / -AlteZachen / -RePACKPOST` | `^(.*?[-._ ](S\d+E\d+)[-._ ])\|(-(RP\|1\|NZBGeek\|Obfuscated\|Scrambled\|sample\|Pre\|postbot\|xpost\|Rakuv[a-z0-9]*\|WhiteRev\|BUYMORE\|AsRequested\|AlternativeToRequested\|GEROV\|Z0iDS3N\|Chamele0n\|4P\|4Planet\|AlteZachen\|RePACKPOST))+$` | i, c |

### Regex totals

| Source file | Regex count |
|---|---|
| `Parser.cs` `ReportTitleRegex[]` | 104 active (1 commented out) |
| `Parser.cs` `SpecialEpisodeTitleRegex[]` | 2 |
| `Parser.cs` `RejectHashedReleasesRegexes[]` | 14 |
| `Parser.cs` `SeasonFolderRegexes[]` + standalone `SeasonFolderRegex` | 1 + 1 |
| `Parser.cs` standalone (`Reversed`, `Normalize`, `Percent`, `SimpleTitle`, `SixDigitAirDate`, `CleanQualityBrackets`, `YearInTitle`, `TitleComponents`, `Part`, `Punctuation`, `ArticleWord`, `SpecialEpisodeWord`, `DuplicateSpaces`, `SimpleEpisodeNumber`, `RequestInfo`, `Multi`, inline IMDB) | 17 |
| `QualityParser.cs` | 16 |
| `LanguageParser.cs` | 9 (including the `LanguageRegex` interpolation that re-uses `LanguagesOnlyRegex`) |
| `ParserCommon.cs` `PreSubstitutionRegex[]` | 14 |
| `ParserCommon.cs` standalone | 3 |
| `ReleaseGroupParser.cs` | 6 |
| **Total** | **187** |

> The "highest-value" cluster is the 104 ordered title regexes. Porting to Go RE2 will require splitting some patterns: `(?<...>)` named groups exist in RE2 syntax (`(?P<name>...)`), but **lookahead/lookbehind `(?=...)`, `(?!...)`, `(?<=...)`, `(?<!...)`, `\k<name>` backreferences, and the atomic group `(?>...)` (used in regex 91 line 398) are not supported by RE2**. Open-question Q-14.

---

## Test fixture inventory

Each `*Fixture.cs` under `src/NzbDrone.Core.Test/ParserTests/`. `[TestCase("input", expected)]` rows are the de-facto specification — they must port verbatim into Go test tables.

| Fixture | Lines | TestCases | Tests |
|---|---:|---:|---|
| `AbsoluteEpisodeNumberParserFixture.cs` | 278 | 167 | `Parser.ParseTitle` returns expected `SeriesTitle` + `AbsoluteEpisodeNumbers[]` for anime-style names. The single largest table-driven spec for absolute numbering. |
| `AnimeMetadataParserFixture.cs` | 63 | 36 | parses sub-group + release hash + version from anime `[SubGroup]` titles |
| `AnimeVersionFixture.cs` | 23 | 6 | parses `v2`/`v3` qualifiers in anime titles into `Quality.Revision.Version` |
| `CrapParserFixture.cs` | 119 | 30 | rejects junk: empty, hash-only, season-folder-only, password+yenc, garbage punctuation. Calls `Parser.ParseTitle` and asserts `null` |
| `DailyEpisodeParserFixture.cs` | 129 | 50 | daily-show airdate parsing (yyyy-mm-dd, yyyymmdd, mm.dd.yyyy, dd.mm.yyyy, short-month names, Plex DVR) |
| `ExtendedQualityParserRegex.cs` | 51 | 30 | additional quality regex tests (probably extends `QualityParserFixture`); name has no `Fixture` suffix but it is a TestFixture |
| `HashedReleaseFixture.cs` | 166 | 1 | uses an array `[]` of titles + an `[Explicit]` test (count=1 because the cases live in `static object[][] HashedReleaseParserCases` rather than `[TestCase]`) |
| `IsPossibleSpecialEpisodeFixture.cs` | 55 | 5 | `ParsedEpisodeInfo.IsPossibleSpecialEpisode` |
| `IsoLanguagesFixture.cs` | 83 | 23 | `IsoLanguages.Find(twoLetterCode)` mappings |
| `LanguageParserFixture.cs` | 612 | 218 | `LanguageParser.ParseLanguages` per-title spec **(very large table)** |
| `MiniSeriesEpisodeParserFixture.cs` | 54 | 14 | mini-series Part1/PartOne/E1-E2 detection — sets `IsMiniSeries=true`, `SeasonNumber=1` |
| `MultiEpisodeParserFixture.cs` | 100 | 78 | multi-episode patterns (S01E05E06, S01E05-06, repeated S01E05.S01E06, batch with `of N`) |
| `NormalizeEpisodeTitleFixture.cs` | 23 | 6 | `Parser.NormalizeEpisodeTitle` strips trailing parenthetical, punctuation, dup-spaces |
| `NormalizeSeriesTitleFixture.cs` | 165 | 41 | `Parser.CleanSeriesTitle` strips articles + punctuation + diacritics |
| `ParserFixture.cs` | 116 | 45 | catch-all integration tests for `Parser.ParseTitle` |
| `PathParserFixture.cs` | 67 | 28 | `Parser.ParsePath` season-folder + filename combination logic |
| `QualityParserFixture.cs` | 522 | 281 | `QualityParser.ParseQuality` per-title spec **(largest table after `LanguageParser`)** |
| `ReleaseGroupParserFixture.cs` | 206 | 151 | `ReleaseGroupParser.ParseReleaseGroup` |
| `SceneCheckerFixture.cs` | 33 | 13 | `SceneChecker.IsSceneTitle` |
| `SeasonParserFixture.cs` | 128 | 55 | `Parser.ParseTitle` for season-pack and partial-season releases |
| `SeriesTitleInfoFixture.cs` | 60 | 0 | tests `SeriesTitleInfo` filling — uses `Subject` setup not `[TestCase]` |
| `SingleEpisodeParserFixture.cs` | 249 | 184 | single-episode patterns `S01E05`, `1x05`, `S01.E05`, `[S01E05]`, etc. **largest single-episode table** |
| `SubGroupParserFixture.cs` | 19 | 1 | `Parser.ParseTitle` returns sub-group on a single sample (small smoke test) |
| `UnicodeReleaseParserFixture.cs` | 179 | 58 | non-ASCII / Chinese / Japanese / Cyrillic title parsing |
| `UrlFixture.cs` | 55 | 31 | website prefix/postfix stripping (`www.foo.com - Title`, `[example.org] Title`) |
| `ValidateParsedEpisodeInfoFixture.cs` | 73 | 0 | `ValidateParsedEpisodeInfo.ValidateForSeriesType` — uses `[Test]` methods only |
| `ParsingServiceTests/GetEpisodesFixture.cs` | 564 | n/a | `ParsingService.GetEpisodes` integration with mocked `IEpisodeService`, `ISceneMappingService`. Uses `[Test]`/builders, not `[TestCase]` rows |
| `ParsingServiceTests/GetSeriesFixture.cs` | 63 | n/a | `ParsingService.GetSeries` |
| `ParsingServiceTests/MapFixture.cs` | 391 | n/a | `ParsingService.Map` overloads |

**Table-driven tests that must port verbatim as Go test tables (sum of `TestCase` rows): ~1,646.** This is the canonical Sonarr parser spec. Any divergence in the Go port should be flagged here.

> **Caveat:** the `TestCase` count is computed by `grep -c "TestCase"`, which over-counts when a single test method has many `TestCase` lines and slightly under-counts when cases live in `static object[][]` arrays (see `HashedReleaseFixture` and `SeriesTitleInfoFixture`). Both shapes need porting, but for those fixtures the numeric count is misleading; treat the row counts as a reading order, not an exact assertion budget. A second pass during Phase 1 should re-count by tokenising the C# AST.

---

## Notable quirks / TODOs / HACKs found in source

- `Parser.cs:25-26` — first regex of `ReportTitleRegex` is **commented out** with a "Todo: This currently breaks series that start with numbers". Anime absolute-episode-number prefix is therefore not parsed; downstream patterns must catch these. **Port note:** keep it commented in Go too.
- `Parser.cs:625` — `// TODO: Skip for anime specials` — fall-through path in `ParsePath` when filename is purely numeric. Anime specials are not handled.
- `Parser.cs:706` — `// TODO: Quick fix stripping [url] - prefixes and postfixes.` — `WebsitePrefixRegex` / `WebsitePostfixRegex` were a quick patch.
- `QualityParser.cs:700-701` — `// TODO: re-enable this when we have a reliable way to determine real / Only treat it as a real if it comes AFTER the season/episode number`. The `RealRegex` currently counts every `REAL` occurrence; the comment notes this is over-broad.
- `Parser.cs:472` — comment "Be very strict with these coz they are very close to the valid 101 ep numbering." — the 11-letter+3-digit hash regex must not collide with `S01E101` style.
- `Parser.cs:711-719` — `CleanQualityBracketsRegex` only strips trailing `[xxx]` if `QualityParser.ParseQualityName(m.Value).Quality != Unknown`. This is a callback-style `MatchEvaluator` — the Go port must mimic that semantics.
- `Parser.cs:721-734` — six-digit airdate `YYMMDD` is **rewritten in-place** to `20YY.MM.DD` before the main loop (only if month/day aren't both `00`). Order matters: this happens before `ReportTitleRegex` runs.
- `Parser.cs:1117-1120` — daily branch swaps day/month if month > 12 (defensive against scene typos).
- `Parser.cs:1133-1143` — daily branch rejects future airdates (>1 day past now) and dates before 1970 (unless `titleyear` was matched). This makes `ParseTitle` **time-dependent**, which is a regression risk — Go port should accept an injectable clock.
- `Parser.cs:651-689` — `ReversedTitleRegex` literally reverses the string when the title looks reversed (e.g. `…p027.…`). Subtle.
- `Parser.cs:693` — `replace 【 ] 】 with [ ]` to normalise full-width Chinese brackets.
- `ParserCommon.cs:9-52` — 14 PreSubstitutionRegex entries cover Chinese/Korean releases. The loop at `Parser.cs:695-702` runs `TryReplace` for **every** entry (not first-match-wins) — multiple substitutions can stack.
- `ReleaseGroupParser.cs:32-38` — also runs `PreSubstitutionRegex` in **first-match-wins** mode (uses `break`). Inconsistent with `Parser.cs` — this is asymmetric and must be preserved.
- `LanguageParser.cs:42-47` — `CleanSeriesTitleRegex` is "first-match-wins" (uses `break`).
- `LanguageParser.cs:54-204` — language detection has **two layers**: substring `lowerTitle.Contains(...)` checks for full English language names, plus the regex layer afterwards. The two are additive.
- `LanguageParser.cs:211-224` — German-only result with `\bDL\b` (not `WEB-DL`) adds `Original`; with `\bML\b` adds `Original` + `English`.
- `Parser.cs:551` — `MultiRegex` flags multi-language but does not by itself populate `Languages`; consumers (e.g. `MultiLanguageBlocklistSpecification`) call `HasMultipleLanguages` separately.
- `Parser.cs:865-878` — `NormalizeImdbId` zero-pads to 7 digits and re-prefixes `tt`. Edge case: it returns `null` if length ≤ 2 even after a successful regex match. Suspicious.
- `Parser.cs:484` — `^abc$` is matched **case-insensitively** (different from `^123$` next to it which is case-sensitive). Must port flag-by-flag.
- `Parser.cs:492` — `^\d{6}_\d{2}$` has **no** `Compiled` flag. The other rejection regexes do. Likely a copy-paste oversight — the C# behaviour is identical, but the Go port should not treat that as semantically meaningful.
- `Parser.cs:398` — regex 91 uses **atomic group** `(?>[-._ ])` — RE2-incompatible. Open-question Q-14.
- `Parser.cs:29` — first daily regex starts with `^^` (double anchor). C# tolerates it; Go RE2 does too. Cosmetic but flagged.

## Subtle behaviours

### Absolute episode numbering (anime)
- Indices 7-21, 28-32, 46-48, 67-69, 92-100, 104 in `ReportTitleRegex` cover anime layouts. Captures: `subgroup`, `title`, `absoluteepisode` (often repeated for batches), optional `season`+`episode` for hybrid releases, `special`, `hash`.
- Decimal absolute episodes (e.g. `12.5`) are interpreted as specials: `Parser.cs:984-995` puts them into `SpecialAbsoluteEpisodeNumbers` and sets `Special=true`. **Multiple decimal captures are rejected** (returns `null`).
- 4-digit absolute numbers (`\d{4}`) live in indices 46-48 and are tried late in the array; ordering matters because earlier `\d{2,3}` patterns would otherwise swallow them.

### Special seasons / specials
- Indices 13-21, 31, 104 capture `special|ova|ovd|ncop|nced` token.
- `Parser.cs:749-753` upgrades a `FullSeason` match whose `ReleaseTokens` contains "Special" to `Special=true`.
- `IsPossibleSpecialEpisode` (`ParsedEpisodeInfo.cs:67-80`) treats single-episode `E0` as a probable special.
- Absolute-episode decimal → `SpecialAbsoluteEpisodeNumbers`.

### Daily episodes (yyyy-mm-dd)
- `airYear < 1900` (or absent) → standard branch; otherwise daily branch (`Parser.cs:933 / 1075-1161`).
- Three daily formats with **ambiguous** ordering: `mm.dd.yyyy` (index 80) and `dd.mm.yyyy` (index 81). When both `ambiguousairmonth` and `ambiguousairday` ≤ 12, the parser **throws** `InvalidDateException` (Parser.cs:1090) — the title is unparseable, not silently mis-parsed.
- Short-month form (`<day>th <Mon> <year>`, index 84) maps via the `ShortMonths` dict (`Parser.cs:553-567`).
- `airDate > now+1day` or `airDate < 1970-01-01` (without `titleyear`) → `InvalidDateException` (`Parser.cs:1133-1143`). Time-dependent.

### Multi-episode releases (S01E02E03, S01E05-06, batches)
- All `episode` named groups can match repeatedly (multiple captures). `Parser.cs:944-960` uses `Captures.First()` and `Captures.Last()` to compute `(first, last)` and inflates `EpisodeNumbers = Range(first, last-first+1)`. **If first > last → return null.**
- `S01E04E05` (index 4) and `S01E04-E05` (index 37) and `1x04x05` (index 23) all map to the same `EpisodeNumbers = [4, 5]`.
- `episodecount` group (e.g. `S1E1-2 of 6`) is treated as a season-pack when both end-episodes equal it (`Parser.cs:1038-1042`).
- `seasonpart` group (`S01.Part2`, `S01.Vol3`) sets `IsPartialSeason=true` + `SeasonPart=N` (`Parser.cs:1019-1027`).

### Repacks / propers
- `QualityParser.cs:687-698` — `proper` increments `Revision.Version` by 1 (or sets to 2 if no explicit `vN`). `repack` does the same and sets `Revision.IsRepack=true`. `vN` alone sets `Revision.Version=N`. `REAL` count is captured separately into `Revision.Real`. **Order matters:** `proper` is checked before `repack`; both can fire; subsequent assignments overwrite.

### Scene vs P2P naming
- `SceneChecker.IsSceneTitle` (`SceneChecker.cs:7`) demands all-dot/no-space + a non-empty `ReleaseGroup` + a non-Unknown quality + a non-empty `SeriesTitle`. P2P releases that have spaces/brackets fall back to `false`.
- `ReleaseGroupParser` has **two exception lists** that override the trailing `-Group` regex (`ReleaseGroupParser.cs:19, 22`) — used for groups whose names violate the standard scene tag convention. Both lists are matched **last-occurrence wins**.
- Anime sub-group (leading `[Group]`) takes precedence over standard release group: `Parser.cs:763-767` overwrites `ReleaseGroup` with the sub-group when present.

### Reversed titles
- `ReversedTitleRegex` (line 513) detects markers `p027`, `p0801`, `\d{2,3}E-?\d{2}S` after a separator. When found, the title (minus extension) is character-reversed before parsing. This is rare and applies to certain Polish/Russian release patterns.

### Six-digit airdate normalisation
- `SixDigitAirDateRegex` (line 527) matches `YYMMDD` only when the 2-digit year starts with 1-9 (excludes `0YMMDD`). Year `00`-`09` are not auto-rewritten. The replacement always prefixes `20`, so 1990s are mis-mapped. Likely intentional but worth flagging.

### Series with year in title vs daily
- `YearInTitleRegex` (line 533) handles `Series Title 2015` → `SeriesTitleInfo.Year=2015`. The Plex DVR daily regex (index 0) handles `Title (2015) 2018-04-28 12:34:56` separately — **first-match-wins ordering puts daily ahead of season/episode**, so any release with a year-in-title and a date is parsed as daily.

### Language parsing order
1. Substring checks for English language names (Spanish/Danish/Dutch/…/Latvian/Azerbaijani/Uzbek)
2. `LanguageRegex` matches (with the negative-lookahead for "subs")
3. `CaseSensitiveLanguageRegex` matches uppercase 2-letter codes that are not adjacent to "SUB"
4. Substring "english" (added last)
5. If only `German` matched, `\bDL\b`/`\bML\b` adds `Original` (and `English` for ML)
6. `Distinct` by enum int value, return list. If empty → `[Unknown]`.

## Open questions

- Q-13: `ReleaseTitleNormalizer.cs` was named in the analysis prompt but does not exist in the C# tree. Confirm whether the Go port should expose a single `ReleaseTitleNormalizer` package as a façade over `Parser.SimplifyTitle`, `Parser.NormalizeTitle`, `Parser.NormalizeEpisodeTitle`, `Parser.CleanSeriesTitle`, the `RegexReplace` arrays in `ParserCommon`, and the `LanguageParser.CleanSeriesTitleRegex`. → see `questions.md#Q-13`
- Q-14: The C# regexes use lookaround (`(?=...)`, `(?!...)`, `(?<=...)`, `(?<!...)`), backreferences (`\k<sep>` in regex 0/1), and one atomic group (`(?>[-._ ])` in regex 91). Go's stdlib `regexp` (RE2) supports none of these. The Go port must either (a) ship a different regex engine (e.g. `github.com/dlclark/regexp2`, which is .NET-compatible but ~10× slower and not RE2), or (b) rewrite each pattern by hand into RE2-compatible form, accepting potential semantic drift. → see `questions.md#Q-14`
- Q-15: Daily-branch rejects airdates `> DateTime.Now.AddDays(1)`. This makes parsing wall-clock-dependent and breaks reproducibility (a release seeded today may parse, but a fixture file replayed in a year may not). Should the Go port accept an injectable clock and how should test fixtures pin "now"? → see `questions.md#Q-15`
- Q-16: `RealRegex` counts every `REAL` occurrence anywhere in the title (TODO comment at `QualityParser.cs:700`). Should the Go port preserve the buggy-but-stable behaviour, or fix it (which would make some releases parse to a different revision)? → see `questions.md#Q-16`
- Q-17: `ParserCommon.PreSubstitutionRegex[]` runs in **all-apply** mode from `Parser.ParseTitle` but **first-match-wins** mode from `ReleaseGroupParser.ParseReleaseGroup`. Is the asymmetry intentional? It means a Chinese release run through `ReleaseGroupParser` standalone may get a different group than when run through the full pipeline. → see `questions.md#Q-17`
- Q-18: Test fixtures store many table inputs in `static object[][]` arrays rather than `[TestCase]` attributes (e.g. `HashedReleaseFixture`, `SeriesTitleInfoFixture`, the `ParsingServiceTests/*`). The Phase-0.5 README says "TestCase rows must port verbatim as Go test tables" — confirm whether the data-array form should also port verbatim. → see `questions.md#Q-18`

## Test files (the port must keep passing equivalents)

All under `src/NzbDrone.Core.Test/ParserTests/`:

- `AbsoluteEpisodeNumberParserFixture.cs`, `AnimeMetadataParserFixture.cs`, `AnimeVersionFixture.cs`
- `CrapParserFixture.cs`, `DailyEpisodeParserFixture.cs`
- `ExtendedQualityParserRegex.cs`
- `HashedReleaseFixture.cs`, `IsPossibleSpecialEpisodeFixture.cs`, `IsoLanguagesFixture.cs`
- `LanguageParserFixture.cs`, `MiniSeriesEpisodeParserFixture.cs`, `MultiEpisodeParserFixture.cs`
- `NormalizeEpisodeTitleFixture.cs`, `NormalizeSeriesTitleFixture.cs`
- `ParserFixture.cs`, `PathParserFixture.cs`
- `QualityParserFixture.cs`
- `ReleaseGroupParserFixture.cs`
- `SceneCheckerFixture.cs`, `SeasonParserFixture.cs`, `SeriesTitleInfoFixture.cs`, `SingleEpisodeParserFixture.cs`, `SubGroupParserFixture.cs`
- `UnicodeReleaseParserFixture.cs`, `UrlFixture.cs`
- `ValidateParsedEpisodeInfoFixture.cs`
- `ParsingServiceTests/GetEpisodesFixture.cs`, `ParsingServiceTests/GetSeriesFixture.cs`, `ParsingServiceTests/MapFixture.cs`

Plus the `RegexFixture.cs` / standalone test-helper files that may exist outside `ParserTests/` and exercise these regexes indirectly via integration tests (search: `Parser.ParseTitle\(`).
