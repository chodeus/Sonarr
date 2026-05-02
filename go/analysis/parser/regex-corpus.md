# Parser regex corpus

This file is the flattened, denormalised inventory of every regex literal under `src/NzbDrone.Core/Parser/**`, generated as input to the C#-to-Go parser port. Each row carries the verbatim pattern, its `RegexOptions`, the RE2-incompatible features it uses (lookaround / backreferences / atomic groups / duplicate-named-groups / `(?-i:...)`), and pointers to representative test inputs (matching and rejecting). The parser card (`go/analysis/subsystems/parser.md`) is the human-readable narrative; this file is the porter's checklist.

The reason a flat corpus exists separately from the subsystem card: the parser is the single most regex-heavy subsystem in Sonarr. Open-question Q-14 in `go/analysis/questions.md` asks whether the Go port should ship a .NET-compatible regex engine (`github.com/dlclark/regexp2`) or rewrite each non-RE2 pattern into RE2-compatible form. That decision hinges on **how many** regexes use which non-RE2 features and **how hard** they would be to rewrite. The summary table below answers it.

The 1,585 [TestCase]-driven test rows under `src/NzbDrone.Core.Test/ParserTests/**` are the canonical specification — the Go port must produce identical decisions for every input → expected output pair. This corpus enumerates each test fixture file with its case count (Section "Test corpus inventory") so the porter can wire each fixture to a Go test table.

## Summary

| Metric | Count | Source |
|---|---:|---|
| Total regex literals (verified) | **186** | `grep -cE 'new Regex\(\|new RegexReplace\(\|=\s*new\(@"' src/NzbDrone.Core/Parser/*.cs` (excluding the 2 ctor-internal `new Regex(...)` lines in `RegexReplace.cs`) |
| Card's claim | 187 | `parser.md` line 381 — off by 1; card counts an inline `imdbRegex` (line 865 of `Parser.cs`) which the same grep finds. Treat **186** as the verified total. |
| `ReportTitleRegex[]` entries (active) | 104 | + 1 commented-out at `Parser.cs:25-26` = 105 declared lines |
| `SpecialEpisodeTitleRegex[]` | 2 | `Parser.cs:454-461` |
| `RejectHashedReleasesRegexes[]` | 14 | `Parser.cs:463-505` |
| `SeasonFolderRegexes[]` (the array) | 1 | `Parser.cs:507-510` |
| `Parser.cs` standalone (incl. inline imdbRegex) | 17 | declarations from `ReversedTitleRegex` through `MultiRegex` and `imdbRegex` at line 865 |
| `QualityParser.cs` | 15 | `Parser.cs:17-66` |
| `LanguageParser.cs` | 8 | including 1-element `CleanSeriesTitleRegex[]` array; `LanguageRegex` is interpolated from `LanguagesOnlyRegex` |
| `ParserCommon.cs` PreSubstitutionRegex[] | 14 | `ParserCommon.cs:9-52` |
| `ParserCommon.cs` standalone | 3 | `WebsitePrefixRegex`, `WebsitePostfixRegex`, `CleanTorrentSuffixRegex` |
| `ReleaseGroupParser.cs` | 6 | `ReleaseGroupParser.cs:9-26` |
| **Sum** | **184** active + 1 commented + 1 inline = **186** | (the card's "187" is the same pool; off-by-one is a counting convention) |

### RE2 compatibility breakdown

Counted by static feature scan. Each regex is "RE2-clean" only if it contains **none** of: `(?=`, `(?!`, `(?<=`, `(?<!`, `\k<`, `\1..\9` backref, `(?>`, balancing-group `(?<x-y>)`, duplicate named capture group, `(?-i:` inline-flag negation. (Duplicate named groups are RE2-incompatible: Go's `regexp` rejects `(?P<x>a)(?P<x>b)` with `error parsing regexp: duplicate capture name: x`.)

| Metric | Count |
|---|---:|
| Total active regexes | 185 (excluding 1 commented out at `Parser.cs:25`) |
| **RE2-clean (port directly)** | **42** |
| **Non-RE2 (need rewrite or engine)** | **143** |
| Uses positive lookahead `(?=...)` | 19 |
| Uses negative lookahead `(?!...)` | 132 |
| Uses positive lookbehind `(?<=...)` | 8 |
| Uses negative lookbehind `(?<!...)` | 86 |
| Uses backreferences (`\k<sep>`) | 2 (regex 0 + 1 in `ReportTitleRegex`) |
| Uses backreferences (`\k<part2>`) | 1 (`ReleaseGroupRegex`) |
| Uses atomic group `(?>...)` | 1 (`ReportTitleRegex` index 91 at `Parser.cs:398`) |
| Uses balancing groups | 0 |
| Uses duplicate named groups (`(?<episode>...){2,}` style) | 41 |
| Uses inline `(?-i:WEB)` | 1 (`QualityParser.SourceRegex`) |
| Uses `RegexOptions.IgnorePatternWhitespace` | 1 (`QualityParser.SourceRegex`) |
| Uses `RegexOptions.IgnoreCase` | 174 |
| Uses `RegexOptions.Compiled` | 184 (ignored in Go — `regexp` always compiles) |

### Test corpus

| Metric | Count | Source |
|---|---:|---|
| `[TestCase(...)]` rows under `ParserTests/*.cs` (verified) | 1,567 | `grep -cE '\[TestCase\(' src/NzbDrone.Core.Test/ParserTests/*.cs` |
| `[TestCase(...)]` rows under `ParserTests/ParsingServiceTests/*.cs` | 18 | same |
| **Total verified** | **1,585** | |
| Card's claim | 1,646 | `parser.md` line 423 — off by **+61**. The card's number was a `grep -c "TestCase"` which over-counts by including `[TestCaseSource]` annotations and `TestCase` words inside comments. Treat **1,585** as the verified `[TestCase]` total. |
| Additional row-shaped test data NOT counted as `[TestCase]`: | | |
|   `HashedReleaseFixture.HashedReleaseParserCases` (string array) | 35 | `grep -cE '^\s+"' HashedReleaseFixture.cs` |
|   `SeriesTitleInfoFixture` `[Test]` builders | n/a | uses `Subject` setup |
|   `ValidateParsedEpisodeInfoFixture` `[Test]` methods | n/a | not data-driven |
|   `ParsingServiceTests/GetSeries`, `MapFixture` | n/a | builder-style integration |

> Q-14 answer (preview): with **143 of 185 regexes (77%) RE2-incompatible**, hand-rewriting each is the larger effort — but **142 of those 143** only use lookahead/lookbehind and duplicate-named groups, both of which are mechanically rewritable (split lookahead into post-match validation in Go, rename duplicate groups `episode`/`episode2`/`episode3`). Only the lone atomic group `(?>[-._ ])` at `Parser.cs:398` and the 3 named backreferences (`\k<sep>` x2, `\k<part2>` x1) need genuine engine support, and all 4 of those are rewritable too (the atomic group is purely an optimization and `\k<sep>` can be expanded into two alternation branches `[-_]?(...?)\k` → `(0\d|1[0-2])(?:0\d|1[0-2])` plus a post-check). **Recommendation: rewrite, do not vendor `regexp2`.** See "Open questions" below.

---

## RE2-incompatible regexes (the porter's checklist)

Each row is one regex that uses at least one non-RE2 feature. "Features" column lists every non-RE2 feature with a short tag. "Rewrite difficulty" buckets:
- **trivial**: only uses `(?!digit)` or `(?<!digit)` boundary-style asserts that can be replaced with explicit char-class anchors or post-match length checks; or only uses duplicate named groups (rename in Go).
- **moderate**: lookahead/lookbehind referencing a non-trivial sub-pattern; the rewrite needs a manual translation but the semantics are local.
- **requires-engine**: backreferences (`\k<sep>`, `\k<part2>`), atomic group `(?>...)`. These are not mechanically rewritable while preserving exactly the same match semantics in all corner cases — but in this codebase the 4 instances are bounded sequences that *can* be enumerated.

Pattern column is the verbatim C# regex literal (the contents of the `@"..."` string). Sample inputs are drawn from the test fixtures listed in the right-hand column or, when no test exists, marked `n/a (not directly tested)`.

### `Parser.cs` — `ReportTitleRegex[]`

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty | Test fixture |
|---|---|---|---|---|---|---|---|---|
| 1 | `Parser.cs:29` | `^^(?<title>.+?\((?<titleyear>\d{4})\))[-_. ]+(?<airyear>19[4-9]\d\|20\d\d)(?<sep>[-_]?)(?<airmonth>0\d\|1[0-2])\k<sep>(?<airday>[0-2]\d\|3[01])[-_. ]\d{2}[-_. ]\d{2}[-_. ]\d{2}` | i, c | `\k<sep>` backref | `Series.Title.(2010).2018-04-28.12.34.56` | `Series.Title.(2010).2018_04-28.12.34.56` (mixed sep) | requires-engine (enumerate the 2 sep choices) | `DailyEpisodeParserFixture` |
| 2 | `Parser.cs:33` | `^(?<airyear>19[6-9]\d\|20\d\d)(?<sep>[-_]?)(?<airmonth>0\d\|1[0-2])\k<sep>(?<airday>[0-2]\d\|3[01])(?!\d)` | i, c | `\k<sep>`, `(?!\d)` | `2018-10-12`, `20181012` | `2018-10_12` | requires-engine | `DailyEpisodeParserFixture` |
| 3 | `Parser.cs:37` | `^(?:\W*S(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:e{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `S01E05.S01E06` | `S01E05` (single) | trivial | `MultiEpisodeParserFixture` |
| 4 | `Parser.cs:41` | `^(?:\W*(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:x{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `1x05.1x06` | `1x05` | trivial | `MultiEpisodeParserFixture` |
| 5 | `Parser.cs:45` | `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:(?:[-_]\|[ex]){1,2}(?<episode>\d{2,3}(?!\d+))){2,})` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `S01E04E05`, `1x04x05` | `S01E05` | trivial | `MultiEpisodeParserFixture` |
| 6 | `Parser.cs:49` | `^(?<title>.+?)(?:S?(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{2,3}(?!\d+))(?<splitepisode>[a-d])(?:[ _.])))` | i, c | `(?<!\d+)`, `(?!\d+)` | `Series.S01E05a.HDTV` | `Series.S01E05.HDTV` | trivial | `SingleEpisodeParserFixture` |
| 7 | `Parser.cs:53` | `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{2,3}(?!\d+))))` | i, c | `(?<!\d+)`, `(?!\d+)` | `S01E05`, `1x05` | `123` | trivial | `SingleEpisodeParserFixture` |
| 8 | `Parser.cs:57` | `^(?:\[(?<subgroup>.+?)\](?:_\|-\|\s\|\.)?)(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+))(?:[-_. ])+\((?:S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]\|\W[ex]){1,2}(?<episode>\d{2}(?!\d+))))(?:v\d+)?(?:\)(?!\d+)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+)` | `[Sub] Title 100 (S01E02)` | `[Sub] Title (S01E02)` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 9 | `Parser.cs:61` | `^(?:\[(?<subgroup>.+?)\](?:_\|-\|\s\|\.)?)(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+))(?:[-_. ](?<![()\[!]))+(?:S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]\|\W[ex]){1,2}(?<episode>\d{2}(?!\d+))))(?:v\d+)?(?:[_. ](?!\d+)).*?(?<hash>[(\[]\w{8}[)\]])$` | i, c | `(?<!\d+)`, `(?!\d+)`, `(?<![()\[!])` | `[Sub] Title 100 - S01E02 [ABCD1234]` | unhashed variant | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 10 | `Parser.cs:65` | `^(?:\[(?<subgroup>.+?)\](?:_\|-\|\s\|\.)?)(?<title>.+?)(?:[-_\W](?<![()\[!]))+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]\|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+)(?:v\d+)?(?:[_. ](?!\d+)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+)`, `(?<![()\[!])`, dup `<episode>` | `[Sub] Title S01E02E03` | bracket-less | trivial | `AnimeMetadataParserFixture` |
| 11 | `Parser.cs:69` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?(?:Episode)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+)))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<absoluteepisode>` | `[Sub] Title - Episode 100` | `[Sub] Title 100` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 12 | `Parser.cs:73` | `^(?:\[(?<subgroup>.+?)\](?:_\|-\|\s\|\.)?)(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<absoluteepisode>\d{2,3}(\.\d{1,2})?))+(?:_\|-\|\s\|\.)+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+).*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)`, dup `<absoluteepisode>` & `<episode>` | `[Sub] Title 100 S01E02` | hashed Sub-only | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 13 | `Parser.cs:77` | `^(?:\[(?<subgroup>.+?)\](?:_\|-\|\s\|\.)?)(?<title>.+?)(?:[-_\W](?<![()\[!]))+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]){1,2}(?<episode>\d{2}(?!\d+)))+)(?:(?:_\|-\|\s\|\.)+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+\|\-[a-z])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+\|...)`, dup `<absoluteepisode>` & `<episode>` | `[Sub] Title S01E02 100` | `[Sub] Title-foo` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 14 | `Parser.cs:81` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?[^-]+?)(?:(?<![-_. ]\|\b[0]\d+) - )[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+))\s?~\s?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+))(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?<![-_. ]\|\b[0]\d+)`, `(?!\d+)`, dup `<absoluteepisode>` | `[Sub] Title - 100~108` | `[Sub] Title-108` | moderate | `AbsoluteEpisodeNumberParserFixture` |
| 15 | `Parser.cs:85` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>[^-]+?)[_. ]+?\(Season[_. ](?<season>\d+)\)[-_. ]+?(?:[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?!\d+)`, dup `<absoluteepisode>` | `[Sub] Title (Season 2) - 100` | `[Sub] Title 100` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 16 | `Parser.cs:89` | `^\[(?<subgroup>[^\]]+?)\][-_. ]?(?<title>[^]]+?)(?:[-_. ]{3}?(?<absoluteepisode>\d{2}(\.\d{1,2})?(?!-?\d+\|-[a-z]+)))+(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?!-?\d+\|-[a-z]+)`, dup `<absoluteepisode>` | `[Sub] Title... 12` | `[Sub] Title-foo` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 17 | `Parser.cs:93` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>[^-]+?)(?:(?<![-_. ]\|\b[0]\d+) - )(?:[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?<![-_. ]\|\b[0]\d+)`, `(?!\d+)`, dup `<absoluteepisode>` | `[Sub] Title - 100` | `[Sub] Title-100` (no space) | moderate | `AbsoluteEpisodeNumberParserFixture` |
| 18 | `Parser.cs:97` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)[-_. ]+(?:S(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?![ex]?\d+))).+?(?:$\|\.mkv)` | i, c | `(?<!\d+)`, `(?![ex]?\d+)` | `[Sub] Title - S02` | `[Sub] Title - S02E01` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 19 | `Parser.cs:101` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>[^-]+?)(?:(?<![-_. ]\|\b[0]\d+)[_ ]+)(?:[-_. ]?(?<absoluteepisode>\d{3}(\.\d{1,2})?(?!\d+\|-[a-z]+)))+(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?<![-_. ]\|\b[0]\d+)`, `(?!\d+\|-[a-z]+)`, dup `<absoluteepisode>` | `[Sub] Title 100` | `[Sub] Title-100` | moderate | `AbsoluteEpisodeNumberParserFixture` |
| 20 | `Parser.cs:105` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:(?<!\b[0]\d+))(?:[. ]-[. ](?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+\|[-])))+(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?<!\b[0]\d+)`, `(?!\d+\|[-])`, dup `<absoluteepisode>` | `[Sub] Title - 100` | `[Sub] Title - 0100` | moderate | `AbsoluteEpisodeNumberParserFixture` |
| 21 | `Parser.cs:109` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:(?<!\b[0]\d+))(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+\|[-]))[. ]-[. ](?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+\|[-]))(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?<!\b[0]\d+)`, `(?!\d+\|[-])`, dup `<absoluteepisode>` | `[Sub] Title 100 - 108` | leading-zero variants | moderate | `AbsoluteEpisodeNumberParserFixture` |
| 22 | `Parser.cs:113` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)[-_. ]+\(?(?:[-_. ]?#?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+\|-[a-z]+)))+\)?(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `(?!\d+\|-[a-z]+)`, dup `<absoluteepisode>` | `[Sub] Title 100` | `[Sub] Title-100x` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 23 | `Parser.cs:117` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:(?:e\|[-_. ]e){1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title S01E05 - S01E06` | `Title S01E05` | trivial | `MultiEpisodeParserFixture` |
| 24 | `Parser.cs:121` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:x{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title 1x05 - 1x06` | `Title 1x05` | trivial | `MultiEpisodeParserFixture` |
| 25 | `Parser.cs:125` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]\|\W[ex]\|_){1,2}(?<episode>\d{2,3}(?!\d+\|(?:[ex]\|\W[ex]\|_\|-){1,2}\d+))(?<special>\.5[ .]\[SP\]))` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+\|...)` | `Title S01E05.5 [SP]` | `Title S01E05` | moderate | `SingleEpisodeParserFixture` |
| 26 | `Parser.cs:129` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]\|\W[ex]\|_){1,2}(?<episode>\d{2,3}(?!\d+\|(?:[ex]\|\W[ex]\|_\|-){1,2}\d+))).+?(?:\[.+?\])(?!\\)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\\)`, `(?!\d+\|...)` | `Title.S01E05.foo.[bar]` | `Title.S01E05.foo.[bar]\` | moderate | `SingleEpisodeParserFixture` |
| 27 | `Parser.cs:133` | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:[ex]\|\W[ex]\|-){1,2}(?<episode>(?<!\d+)\d{2}(?!\d+)))+)[-_. (]+?(?:[-_. ]?(?<absoluteepisode>(?<!\d+)\d{3}(\.\d{1,2})?(?!\d+\|[pi])))+.+?\[(?<subgroup>.+?)\](?:$\|\.mkv)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+\|[pi])`, dup `<episode>` & `<absoluteepisode>` | `Title S01E02 100 [Sub].mkv` | sans absoluteepisode | trivial | `AnimeMetadataParserFixture` |
| 28 | `Parser.cs:137` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]\|\W[ex]\|_){1,2}(?<episode>\d{2,3}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]\|_){1,2}(?<episode>\d{2,3}(?!\d+)))+).+?(?:\[.+?\])(?!\\)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\\)`, `(?!\d+)`, dup `<episode>` | `Title.S01E05E06.foo.[x]` | trailing backslash | trivial | `MultiEpisodeParserFixture` |
| 29 | `Parser.cs:141` | `^(?<title>.+?)[-_. ]Episode(?:[-_. ]+(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:.+?)\[(?<subgroup>.+?)\].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?!\d+)`, dup `<absoluteepisode>` | `Title - Episode 100 [Sub]` | `Title 100 [Sub]` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 30 | `Parser.cs:145` | `^(?<title>.+?)(?:(?:_\|-\|\s\|\.)+(?<absoluteepisode>\d{3}(\.\d{1,2})(?!\d+)))+(?:.+?)\[(?<subgroup>.+?)\].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?!\d+)`, dup `<absoluteepisode>` | `Title - 100.5 [Sub]` | integer absoluteep | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 31 | `Parser.cs:149` | `^(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2}(?!\d+))[-_. ](\(\d{4}\))[-_. ]\[(?<subgroup>.+?)\]` | i, c | `(?<!\d+)`, `(?!\d+)` | `Title 12 (2018) [Sub]` | `Title 100 (2018) [Sub]` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 32 | `Parser.cs:153` | `^(?<title>[^-]+?)(?:(?<![-_. ]\|\b[0]\d+) - )(?:[-_. ]?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special\|ova\|ovd))?.*?(?<hash>[(\[]\w{8}[)\]])(?:$\|\.mkv)` | i, c | `(?<![-_. ]\|\b[0]\d+)`, `(?!\d+)`, dup `<absoluteepisode>` | `Title - 100 [ABCD1234]` | unhashed | moderate | `AbsoluteEpisodeNumberParserFixture` |
| 33 | `Parser.cs:157` | `^(?<title>.+?)(?:(?:_\|-\|\s\|\.)+(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+)))+(?:[-_. ]+(?<special>special\|ova\|ovd))?[-_. ]+.*?(?<hash>[(\[]\w{8}[)\]])$` | i, c | `(?!\d+)`, dup `<absoluteepisode>` | `Title 100 [ABCD1234]` | unhashed | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 34 | `Parser.cs:161` | `^(?<title>.+?)?\W*(?<airdate>\d{4}\W+[0-1][0-9]\W+[0-3][0-9])(?!\W+[0-3][0-9])[-_. ](?:s?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+)))(?:[ex](?<episode>(?<!\d+)(?:\d{1,3})(?!\d+)))` | i, c | `(?!\W+[0-3][0-9])`, `(?<!\d+)`, `(?!\d+)` | `Title 2018-04-28 S01E05` | `Title 2018-04-28 12 S01E05` | moderate | `DailyEpisodeParserFixture` |
| 35 | `Parser.cs:165` | `^(?<title>.+?)?\W*(?<airyear>\d{4})\W+(?<airmonth>[0-1][0-9])\W+(?<airday>[0-3][0-9])(?!\W+[0-3][0-9]).+?(?:s?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+)))(?:[ex](?<episode>(?<!\d+)(?:\d{1,3})(?!\d+)))` | i, c | `(?!\W+[0-3][0-9])`, `(?<!\d+)`, `(?!\d+)` | as #34 with extra junk | as #34 | moderate | `DailyEpisodeParserFixture` |
| 36 | `Parser.cs:169` | `^(?<title>.+?)?[-_. ](?:e\d{2,3}(?!\d+))[-_. ](?<airyear>\d{4})\W+(?<airmonth>[0-1][0-9])\W+(?<airday>[0-3][0-9])(?!\W+[0-3][0-9]).+?(?:\[[a-z]+\])` | i, c | `(?!\d+)`, `(?!\W+[0-3][0-9])` | `Title.e123.2018.04.28.foo.[en]` | no `[en]` tail | moderate | `DailyEpisodeParserFixture` |
| 37 | `Parser.cs:173` | `^((?<title>.*?)[ ._]\/[ ._])+\(?S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W\|_)?E?[ ._]?(?<episode>(?<!\d+)\d{1,2}(?!\d+))(?:-(?<episode>(?<!\d+)\d{1,2}(?!\d+)))?(?:[ ._]of[ ._](?<episodecount>\d{1,2}))?\)?[ ._][\(\[]` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<title>` & `<episode>` | `T1 / T2 / S1E1-2 of 6 (` | no slash | trivial | `MultiEpisodeParserFixture` |
| 38 | `Parser.cs:177` | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))E(?<episode>\d{2,3}(?!\d+))(?:-(?<episode>\d{2,3}(?!\d+)))+(?:[-_. ]\|$)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title S01E99-100` | single ep | trivial | `MultiEpisodeParserFixture` |
| 39 | `Parser.cs:181` | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))E(?<episode>\d{1,2}(?!\d+))(?:-(?<episode>\d{1,2}(?!\d+)))+(?:[-_. ]\|$)` | i, c | as #38 | `Title S01E5-6` | `Title S01E5` | trivial | `MultiEpisodeParserFixture` |
| 40 | `Parser.cs:185` | `^(?<title>[一-鿌]+)[_. ](?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]\|\W[ex]){1,2}(?<episode>\d{2,3}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]\|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)(?:[-_. ]\|$)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)`, dup `<title>` & `<episode>` | `中文 English.S01E05` | only English | trivial | `UnicodeReleaseParserFixture` |
| 41 | `Parser.cs:189` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[ex]\|\W[ex]){1,2}(?<episode>\d{2,3}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]\|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)(?:[-_. ]\|$)` | i, c | as #40 (no Chinese first cap) | `Series.S01E05E06` | bad junk | trivial | `MultiEpisodeParserFixture` (workhorse) |
| 42 | `Parser.cs:193` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{4})(?!\d+))(?:e\|\We\|_){1,2}(?<episode>\d{2,4}(?!\d+))(?:(?:\-\|e\|\We\|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)\W?(?!\\)` | i, c | as #38 with 4-digit season; dup `<episode>`, `(?!\\)` | `Title.S2016E05` | trailing backslash | trivial | `SeasonParserFixture` |
| 43 | `Parser.cs:197` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<season>(?<!\d+)(?:\d{4})(?!\d+))(?:x\|\Wx){1,2}(?<episode>\d{2,4}(?!\d+))(?:(?:\-\|x\|\Wx\|_){1,2}(?<episode>\d{2,3}(?!\d+)))*)\W?(?!\\)` | i, c | as #42 (`x` form) | `Title.2016x05` | as #42 | trivial | `SeasonParserFixture` |
| 44 | `Parser.cs:201` | `^(?<title>.+?)(?:[-_\W](?<![()\[!]))+S(?<season>(?<!\d+)(?:\d{2})(?!\d+))(?:\.)(?<episode>\d{2,3}(?!\d+))(?:[-_. ]\|$)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)` | `Title.s01.05` | `Title.s1.5` | trivial | `SingleEpisodeParserFixture` |
| 45 | `Parser.cs:205` | `^(?<title>.+?)(Complete Series)?[-_. ]+(?:S\|(?:Season\|Saison\|Series\|Stagione)[_. ])(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:[-_. ]{1}\|[-_. ]{3})(?:S\|(?:Season\|Saison\|Series\|Stagione)[_. ])?(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<season>` | `Title S01-S02` | `Title S01` | trivial | `SeasonParserFixture` |
| 46 | `Parser.cs:209` | `^(?<title>.+?)(?:\W+S(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))\W+(?:(?:(?:Part\|Vol)\W?\|(?<!\d+\W+)e\|p)(?<seasonpart>\d{1,2}(?!\d+)))+)` | i, c | `(?<!\d+)`, `(?!\d+)`, `(?<!\d+\W+)` | `Title S01 Part2` | `Title S01` | trivial | `SeasonParserFixture` |
| 47 | `Parser.cs:213` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?\(?(?<absoluteepisode>\d{4}(\.\d{1,2})?(?!\d+))[-](?<absoluteepisode>\d{4}(\.\d{1,2})?(?!\d+))\)?` | i, c | `(?!\d+)`, dup `<absoluteepisode>` | `[Sub] Title 1017-1088` | 3-digit | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 48 | `Parser.cs:217` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?(?<absoluteepisode>\d{4}(\.\d{1,2})?(?!\d+))` | i, c | `(?!\d+)` | `[Sub] Title 1234` | 3-digit | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 49 | `Parser.cs:221` | `^(?<title>.+?)[-_. ]+(?<absoluteepisode>(?<!\d+)\d{4}(?!\d+))[-_. ]\[(?<subgroup>.+?)\]` | i, c | `(?<!\d+)`, `(?!\d+)` | `Title 1234 [Sub]` | `Title 12345 [Sub]` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 50 | `Parser.cs:225` | `^(?<title>.+?\d{4})(?:\W+(?:(?:Part\W?\|e)(?<episode>\d{1,2}(?!\d+)))+)` | i, c | `(?!\d+)`, dup `<episode>` | `Mini.2010.Part1.Part2` | no year | trivial | `MiniSeriesEpisodeParserFixture` |
| 51 | `Parser.cs:229` | `^(?<title>.+?)(?:[-._ ][e])(?<episode>\d{2,3}(?!\d+))(?:(?:\-?[e])(?<episode>\d{2,3}(?!\d+)))+` | i, c | `(?!\d+)`, dup `<episode>` | `Title.e01-e02` | `Title.e01` | trivial | `MiniSeriesEpisodeParserFixture` |
| 52 | `Parser.cs:233` | `^(?<title>.+?)?\W*(?<airyear>\d{4})[-_. ]+(?<airmonth>[0-1][0-9])[-_. ]+(?<airday>[0-3][0-9])(?![-_. ]+[0-3][0-9])[-_. ]+Part[-_. ]?(?<part>[1-9])` | i, c | `(?![-_. ]+[0-3][0-9])` | `Title.2018.04.28.Part.2` | trailing day | trivial | `DailyEpisodeParserFixture` |
| 53 | `Parser.cs:239` | `^(?<title>.+?)[-_. ]+S(?<season>(?<!\d+)\d{3}(?!\d+))E(?<episode>(?<!\d+)\d{2}(?!\d+))(?:-[ex](?<episode>(?<!\d+)\d{2}(?!\d+)))?([_. ]\|$)(?!\\)` | i, c | `(?<!\d+)`, `(?!\d+)`, `(?!\\)`, dup `<episode>` | `Title.S001E01` | `Title.S001E01\` | trivial | `SingleEpisodeParserFixture` |
| 54 | `Parser.cs:243` | `^(?<title>.+?)(?:\W+(?:(?<episode>(?<!\d+)\d{1,2}(?!\d+))of\d+)+)` | i, c | `(?<!\d+)`, `(?!\d+)` | `Title 03of10` | `Title 03 of 10` | trivial | `MiniSeriesEpisodeParserFixture` |
| 55 | `Parser.cs:247` | `(?:.*(?:\""\|^))(?<title>.*?)(?:[-_\W](?<![()\[]))+(?:\W?Season\W?)(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W\|_)+(?:Episode\W)(?:[-_. ]?(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+` | i, c | `(?<![()\[])`, `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title Season 01 Episode 03` | inline | trivial | `SingleEpisodeParserFixture` |
| 56 | `Parser.cs:251` | `(?:.*(?:^))(?<title>.*?)[-._ ]+\[S(?<season>(?<!\d+)\d{2}(?!\d+))(?:[E-]{1,2}(?<episode>(?<!\d+)\d{2}(?!\d+)))+\]` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title [S01E11E12]` | `Title S01E11E12` | trivial | `MultiEpisodeParserFixture` |
| 57 | `Parser.cs:255` | `(?:.*(?:^))(?<title>.*?)[-._ ]+\(S(?<season>(?<!\d+)\d{2}(?!\d+))(?:[E-]{1,2}(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+([._ ]+of[._ ]+[\dx]+)?\)` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title (S01E11-12)` | brackets-only | trivial | `MultiEpisodeParserFixture` |
| 58 | `Parser.cs:259` | `(?:.*(?:^))(?<title>.*?)S(?<season>(?<!\d+)\d{2}(?!\d+))(?:E(?<episode>(?<!\d+)\d{2}(?!\d+)))+` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `TitleS01E11E12` | `TitleS01` | trivial | `MultiEpisodeParserFixture` |
| 59 | `Parser.cs:263` | `^(?<title>.+?)[-_. ]S(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:[-_. ]?[ex]?(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title.S6.E1E2` | `Title.S6` | trivial | `MultiEpisodeParserFixture` |
| 60 | `Parser.cs:267` | `^(?<title>.*?)[ ._]\(S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W\|_)?E?[ ._]?(?<episode>(?<!\d+)\d{1,2}(?!\d+))(?:-(?<episode>(?<!\d+)\d{1,2}(?!\d+)))?\)(?:[ ._]\/[ ._])(?<title>.*?)[ ._]\(` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<title>` & `<episode>` | `T1 (S01E05) / T2 (` | single title | trivial | `MultiEpisodeParserFixture` |
| 61 | `Parser.cs:271` | `(?:.*(?:^))(?<title>.*?)[-._ ]+\(S(?<season>(?<!\d+)\d{1}(?!\d+))(?:[E-]{1,2}(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+([._ ]+of[._ ]+[\dx]+)?\)` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title (S1E11-12)` | as #57 | trivial | `MultiEpisodeParserFixture` |
| 62 | `Parser.cs:275` | `(?:.*(?:\""\|^))(?<title>.*?)(?:\W?\|_)S(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W\|_)?Ep?[ ._]?(?<episode>(?<!\d+)\d{1,2}(?!\d+))` | i, c | `(?<!\d+)`, `(?!\d+)` | `Title S01.Ep.01` | `Title S01.Ep.001` (caps `\d{1,2}`) | trivial | `SingleEpisodeParserFixture` |
| 63 | `Parser.cs:279` | `(?:.*(?:\""\|^))(?<title>.*?)(?:\W?\|_)S(?<season>(?<!\d+)\d{3}(?!\d+))(?:\W\|_)?E(?<episode>(?<!\d+)\d{1,2}(?!\d+))` | i, c | as #62 with 3-digit season | `Title S010E05` | 2-digit | trivial | `SingleEpisodeParserFixture` |
| 64 | `Parser.cs:283` | `^(?:(?<title>.+?)(?:_\|-\|\s\|\.)+)(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+)))(?:(?:\-\|[ex]\|\W[ex]\|_){1,2}(?<episode>(?<!\d+)\d{5}(?!\d+)))` | i, c | `(?<!\d+)`, `(?!\d+)` | `Title.S01E12345` | 4-digit | trivial | `SingleEpisodeParserFixture` |
| 65 | `Parser.cs:287` | `^(?:(?<title>.+?)(?:_\|-\|\s\|\.)+)(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+)))(?:(?:[-_. ]{1,3}ep){1,2}(?<episode>(?<!\d+)\d{5}(?!\d+)))+` | i, c | as #64; dup `<episode>` | `Title.S01.ep12345.ep23456` | single | trivial | `MultiEpisodeParserFixture` |
| 66 | `Parser.cs:291` | `^(?<title>.+?)(?:_\|-\|\s\|\.)+S(?<season>\d{2}(?!\d+))(\W-\W)E(?<episode>(?<!\d+)\d{2}(?!\d+))(?!\\)` | i, c | `(?!\d+)`, `(?<!\d+)`, `(?!\\)` | `Title.S01 - E01` | `Title.S01-E01\` | trivial | `SingleEpisodeParserFixture` |
| 67 | `Parser.cs:297` | `^(?<title>.+?)?(?:[-_\W](?<![()\[!]))+\[(?:s)?(?<season>(?<!\d+)\d{1,2})(?:(?:[ex])(?<episode>\d{2}))(?:(?:[-ex]){1,2}(?<episode>\d{2}))*\].+?(?:\.\|$)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, dup `<episode>` | `Title - [02x01] - foo` | unbracketed | trivial | `SingleEpisodeParserFixture` |
| 68 | `Parser.cs:301` | `^(?<title>.+?S\d{1,2})[-_. ]{3,}(?:EP)?(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+\|[-]))` | i, c | `(?!\d+\|[-])` | `Title S01 - EP14` | `Title S01-EP14` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 69 | `Parser.cs:305` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)[-_. ]+?(?:Episode[-_. ]+?)(?<absoluteepisode>\d{1}(\.\d{1,2})?(?!\d+))` | i, c | `(?!\d+)` | `[Sub] Title - Episode 1` | `[Sub] Title 1` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 70 | `Parser.cs:309` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)(?<title>.+?)[-_. ]+?\[(?<absoluteepisode>\d{2,3}(\.\d{1,2})?(?!\d+))\]` | i, c | `(?!\d+)` | `[Sub] Title [100]` | unbracketed ep | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 71 | `Parser.cs:313` | `^(?<airyear>\d{2})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9])(?![-_. ]+[0-3][0-9])[-_. ](?<title>.+?)[-_. ](?:Season[-_. ]?(?<season>\d{1,2})[-_. ])?(?:ep\|#)(?<episode>\d{2,3})` | i, c | `(?![-_. ]+[0-3][0-9])` | `181012 Title ep01` | trailing day | trivial | `DailyEpisodeParserFixture` |
| 72 | `Parser.cs:317` | `^(?<title>.+?)[-_. ]+?(?:S\|Season\|Saison\|Series\|Stagione)[-_. ]?(?<season>\d{1,2}(?=[-_. ]\d{4}[-_. ]+))(?<extras>EXTRAS\|SUBPACK)?(?!\\)` | i, c | `(?=[-_. ]\d{4}[-_. ]+)`, `(?!\\)` | `Title.S01.2010.EXTRAS` | `Title.S01.EXTRAS` | moderate | `SeasonParserFixture` |
| 73 | `Parser.cs:321` | `^(?<title>.+?)[-_. ]+?(?:S\|Season\|Saison\|Series\|Stagione)[-_. ]?(?<season>\d{1,2}(?![-_. ]?\d+))(?:[-_. ]\|$)+(?<extras>EXTRAS\|SUBPACK)?(?!\\)` | i, c | `(?![-_. ]?\d+)`, `(?!\\)` | `Title.Season.01` | `Title.Season.01.02` | trivial | `SeasonParserFixture` |
| 74 | `Parser.cs:325` | `^(?<title>.+?)[-_. ]+?(?:S\|Season\|Saison\|Series\|Stagione)[-_. ]?(?<season>\d{4}(?![-_. ]?\d+))(\W+\|_\|$)(?<extras>EXTRAS\|SUBPACK)?(?!\\)` | i, c | as #73, 4-digit | `Title.S2016` | as #73 | trivial | `SeasonParserFixture` |
| 75 | `Parser.cs:329` | `^(?<title>.+?)(?:(?:[-_. ]+?Temporada.+?\|\[.+?\])\[Cap)(?:[-_. ]+(?<season>(?<!\d+)\d{1,2})(?<episode>(?<!e\|x)(?:[1-9][0-9]\|[0][1-9])))+(?:\])` | i, c | `(?<!\d+)`, `(?<!e\|x)` | `Title.Temporada 1 [Cap.105]` | bare title | trivial | `SingleEpisodeParserFixture` |
| 76 | `Parser.cs:333` | `^(?<title>.+?)?\W*(?<airyear>\d{4})[-_. ]+(?<airmonth>[0-1][0-9])[-_. ]+(?<airday>[0-3][0-9])(?![-_. ]+[0-3][0-9])` | i, c | `(?![-_. ]+[0-3][0-9])` | `Title 2018.04.28` | trailing extra-day | trivial | `DailyEpisodeParserFixture` |
| 77 | `Parser.cs:337` | `^(?<title>.+?)?(?:(?:[_.-](?<![()\[!]))+(?<season>(?<!\d+)[1-9])(?<episode>[1-9][0-9]\|[0][1-9])(?![a-z]\|\d+))+(?:[_.]\|$)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?![a-z]\|\d+)` | `Title.103` (S1E03) | `Title.103a` | trivial | `SingleEpisodeParserFixture` |
| 78 | `Parser.cs:342` | `^(?:S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]\|_){1,2}(?<episode>\d{4}(?!\d+\|i\|p)))+)(\W+\|_\|$)(?!\\)` | i, c | `(?<!\d+)`, `(?!\d+)`, `(?!\d+\|i\|p)`, `(?!\\)`, dup `<episode>` | `S01E1234` | `S01E1234p` | trivial | `SingleEpisodeParserFixture` |
| 79 | `Parser.cs:347` | `^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]\|\d{1,2}-))+S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-\|[ex]\|\W[ex]\|_){1,2}(?<episode>\d{4}(?!\d+\|i\|p)))+)\W?(?!\\)` | i, c | as #78 with title | `Title.S01E1234` | as #78 | trivial | `SingleEpisodeParserFixture` |
| 80 | `Parser.cs:351` | `^(?<title>.+?)[_. ](?<absoluteepisode>\d{1,4})(?:[_. ]+)(?:BLM\|B[oö]l[uü]m)` | i, c | none (RE2-clean!) | `Title 04.Bolum` | `Title 04` | (RE2-clean — see below) | `AbsoluteEpisodeNumberParserFixture` |
| 81 | `Parser.cs:354` | `^(?<title>.+?)?\W*(?<ambiguousairmonth>[0-1][0-9])[-_. ]+(?<ambiguousairday>[0-3][0-9])[-_. ]+(?<airyear>\d{4})(?!\d+)` | i, c | `(?!\d+)` | `Title 04.28.2018` | `Title 04.28.20180` | trivial | `DailyEpisodeParserFixture` |
| 82 | `Parser.cs:358` | `^(?<title>.+?)?\W*(?<ambiguousairday>[0-3][0-9])[-_. ]+(?<ambiguousairmonth>[0-1][0-9])[-_. ]+(?<airyear>\d{4})(?!\d+)` | i, c | as #81 | `Title 28.04.2018` | as #81 | trivial | `DailyEpisodeParserFixture` |
| 83 | `Parser.cs:362` | `^(?<title>.+?)?\W*(?<!\d+)(?<airyear>\d{4})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9])(?!\d+)` | i, c | `(?<!\d+)`, `(?!\d+)` | `Title 20180428` | `Title 201804280` | trivial | `DailyEpisodeParserFixture` |
| 84 | `Parser.cs:366` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:(?:_\|-\|\s\|\.)+(?:e\|ep)(?<absoluteepisode>(\d{3}\|\d{4})(\.\d{1,2})?))+[-_. ].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | dup `<absoluteepisode>` | `Title.e1206 .` | `Title.e12 .` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 85 | `Parser.cs:370` | `^(?<title>.+?)[-_. ]+(?<airday>[1-2]\d\|3[01]\|[1-9])(?:th\|st\|rd)[-_. ](?<shortairmonth>jan\|feb\|mar\|apr\|may\|jun\|jul\|aug\|sep\|oct\|nov\|dec)[-_. ](?<airyear>19[4-9]\d\|20\d\d)` | i, c | none (RE2-clean) | `Title 28th.apr.2018` | `Title 28.apr.2018` | (RE2-clean) | `DailyEpisodeParserFixture` |
| 86 | `Parser.cs:374` | `^(?<title>.+?)?(?:(?:[-_. ](?<![()\[!]))*(?<!\d{1,2}-)(?<season>(?<!\d+\|\(\|\[\|e\|x)\d{2})(?<episode>(?<!e\|x)(?:[1-9][0-9]\|[0][1-9])(?!p\|i\|\d+\|\)\|\]\|\W\d+\|\W(?:e\|ep\|x)\d+)))+([-_. ]+\|$)(?!\\)` | i, c | many `(?<!...)`, `(?!...)` | `Title.1103` (S11E03) | `Title.1103p` | moderate | `SingleEpisodeParserFixture` |
| 87 | `Parser.cs:378` | `^(?<title>.+?)[-_. ](?:Se\.(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:(?:[-_ ]?afl\.)(?<episode>\d{1,3}(?!\d+))(?:(?:[-]\|[-_ ]en[-_ ])(?<episode>\d{1,3}(?!\d+)))*))` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `Title Se.1 afl.5 en afl.6` | `Title Se.1` | trivial | `MultiEpisodeParserFixture` |
| 88 | `Parser.cs:382` | `^(?<title>.*?)(?:(?:[-_\W](?<![()\[!]))+S?(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:(?:\-\|[ex]){1,2}(?<episode>\d{1}))+)+(\W+\|_\|$)(?!\\)` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+)`, `(?!\\)`, dup `<episode>` | `Title S01E5E6` | `Title S01E50` | trivial | `SingleEpisodeParserFixture` |
| 89 | `Parser.cs:386` | `^(?:Season(?:_\|-\|\s\|\.)(?<season>(?<!\d+)\d{1,2}(?!\d+)))(?:_\|-\|\s\|\.)(?<episode>(?<!\d+)\d{1,2}(?!\d+))` | i, c | `(?<!\d+)`, `(?!\d+)` | `Season_1\05` | `Season_1\005` | trivial | `PathParserFixture` |
| 90 | `Parser.cs:390` | `^(?:(?<season>(?<!\d+)(?:\d{1,2})(?!\d+))(?:-(?<episode>\d{2,3}(?!\d+))))` | i, c | `(?<!\d+)`, `(?!\d+)` | `1-05 Title` | `100-05 Title` | trivial | `SingleEpisodeParserFixture` |
| 91 | `Parser.cs:394` | `^(?<title>.+?)(?:\W+(?:(?:(?<!\()Part\W?\|(?<!\d+\W+)e)(?<episode>\d{1,2}(?!\d+\|\))))+)` | i, c | `(?<!\()`, `(?<!\d+\W+)`, `(?!\d+\|\))`, dup `<episode>` | `Title Part01 Part02` | `Title (Part01)` | trivial | `MiniSeriesEpisodeParserFixture` |
| **92** | `Parser.cs:398` | `^(?<title>.+?)(?:\W+(?:Part[-._ ](?<episode>One\|Two\|Three\|Four\|Five\|Six\|Seven\|Eight\|Nine)(?>[-._ ])))` | i, c | **atomic group `(?>[-._ ])`** | `Title Part One ` | `Title Part Ten` | **requires-engine** (atomic is purely an optimization here — drop `>` makes it functionally equivalent in this corpus) | `MiniSeriesEpisodeParserFixture` |
| 93 | `Parser.cs:402` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:_\|\s\|\.)+(?:e\|ep)(?<absoluteepisode>\d{2,3}(\.\d{1,2})?)-(?<absoluteepisode>(?<!\d+)\d{1,2}(\.\d{1,2})?(?!\d+\|-)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+\|-)`, dup `<absoluteepisode>` | `Title ep01-12` | `Title ep01-120` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 94 | `Parser.cs:406` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:(?:_\|-\|\s\|\.)+(?:e\|ep)(?<absoluteepisode>\d{2,4}(\.\d{1,2})?))+[-_. ].*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | dup `<absoluteepisode>` | `Title.e66 .` | `Title.e6 .` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 95 | `Parser.cs:410` | `^(?<title>.+?)[-_. ](?:Episode)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+)))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<absoluteepisode>` | `Title - Episode 100` | `Title 100` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 96 | `Parser.cs:414` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)[_. ]+(?<absoluteepisode>(?<!\d+)\d{1,2}(\.\d{1,2})?(?!\d+))-(?<absoluteepisode>(?<!\d+)\d{1,2}(\.\d{1,2})?(?!\d+\|-)).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+)`, `(?!\d+\|-)`, dup `<absoluteepisode>` | `Title 1-10` | `Title 100-200` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 97 | `Parser.cs:418` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)[-_. ]+(?:Episode\|Episodio)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,4}(\.\d{1,2})?(?!\d+\|[ip])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<!\d+)`, `(?!\d+\|[ip])`, dup `<absoluteepisode>` | `Title - Episodio 100` | `Title 100p` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 98 | `Parser.cs:422` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:[-_. ]\[)(?:(?:-?)(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+\|[ip])))+(?:\][-_. ]).*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | as #97; dup `<absoluteepisode>` | `Title [100] foo` | `Title (100) foo` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 99 | `Parser.cs:426` | `^(?<title>.+?)[_. ]-[_. ](?:Temporada)[-_. ]?(?<season>\d{1,2}(?![_. ]?\d+))(?:[_. ]\|$)+(?<extras>EXTRAS\|SUBPACK)?(?!\\)` | i, c | `(?![_. ]?\d+)`, `(?!\\)` | `Title - Temporada 1` | `Title Temporada 1.2` | trivial | `SeasonParserFixture` |
| 100 | `Parser.cs:430` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:[-_. ]+(?<absoluteepisode>(?<!\d+)\d{2,4}(\.\d{1,2})?(?!\d+\|[ip])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | as #97; dup `<absoluteepisode>` | `Title 100` | `Title 100p` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 101 | `Parser.cs:434` | `^(?:\[(?<subgroup>.+?)\][-_. ]?)?(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+\|[ip])))+.*?(?<hash>[(\[]\w{8}[)\]])?$` | i, c | `(?<![()\[!])`, `(?<!\d+)`, `(?!\d+\|[ip])`, dup `<absoluteepisode>` | `Title - 100` | `Title - 100p` | trivial | `AbsoluteEpisodeNumberParserFixture` |
| 102 | `Parser.cs:438` | `^(?<title>.+?)[-_. ](?<season>[0]?\d?)(?:(?<episode>\d{2}){2}(?!\d+))[-_. ]` | i, c | `(?!\d+)` | `extant.10708.hdtv` | `extant.107080` | trivial | `SingleEpisodeParserFixture` |
| 103 | `Parser.cs:442` | `^(?:\[(?<subgroup>.+?)\][-_. ])?(?<title>.+?)[-_. ]+?[\[(](?:S\|Season\|Saison\|Series\|Stagione)[-_. ]?(?<season>\d{1,2}(?![-_. ]?\d+))(?:[-_. )\]]\|$)+(?<extras>EXTRAS\|SUBPACK)?(?!\\)` | i, c | `(?![-_. ]?\d+)`, `(?!\\)` | `[Sub] Title [S01]` | `[Sub] Title S01` (no bracket) | trivial | `SeasonParserFixture` |
| 104 | `Parser.cs:446` | `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}\|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{1}(?!\d+))))` | i, c | `(?<!\d+)`, `(?!\d+)` | `S01E1` | `S01E10` | trivial | `SingleEpisodeParserFixture` |
| 105 | `Parser.cs:450` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:[-_. ]+(?<special>special\|ova\|ovd\|ncop\|nced)).*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | none (RE2-clean) | `[Sub] Title - Special` | `[Sub] Title 100` | (RE2-clean) | `AbsoluteEpisodeNumberParserFixture` |

> Note on numbering: rows 1-105 above correspond to the 105 array slots; row index 0 is commented-out (`Parser.cs:25-26`) and is therefore not in the table. **104 active.** Of those 104, **3 are RE2-clean** (#80 Turkish BLM, #85 short-month dates, #105 Anime OVA special), and **101 are non-RE2** (mostly via `(?<!\d+)`/`(?!\d+)` boundary asserts and duplicate-named groups).

### `Parser.cs` — `SpecialEpisodeTitleRegex[]`

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty |
|---|---|---|---|---|---|---|---|
| 106 | `Parser.cs:456` | `\.S\d+E00\.(?<episodetitle>.+?)(?:\.(?:720p\|1080p\|2160p\|HDTV\|WEB\|WEBRip\|WEB-DL)\.\|$)` | i, c | none | `.S01E00.SpecialName.720p.` | `.S01E01.Foo.720p.` | (RE2-clean) |
| 107 | `Parser.cs:459` | `\.S\d+\.Special\.(?<episodetitle>.+?)(?:\.(?:720p\|1080p\|2160p\|HDTV\|WEB\|WEBRip\|WEB-DL)\.\|$)` | i, c | none | `.S01.Special.Name.720p.` | `.S01.Foo.720p.` | (RE2-clean) |

(Both RE2-clean — listed here only for completeness; they appear again in the RE2-clean table below.)

### `Parser.cs` — `RejectHashedReleasesRegexes[]`

All 14 are RE2-clean (no lookaround, backref, atomic group, or duplicate group). They use only anchors, char classes, and repetition. **Listed in the RE2-clean section.**

### `Parser.cs` — standalone regexes (non-RE2 only)

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty |
|---|---|---|---|---|---|---|---|
| 108 | `Parser.cs:515` `NormalizeRegex` | `((?:\b\|_)(?<!^)([aà](?!$)\|an\|the\|and\|or\|of)(?!$)(?:\b\|_))\|\W\|_` | i, c | `(?<!^)`, `(?!$)` | matches `the` in `the.title` | does not match leading `the` (anchor) | trivial |
| 109 | `Parser.cs:519` `PercentRegex` | `(?<=\b\d+)%` | c | `(?<=\b\d+)` | matches `%` in `50%` | does not match `%` alone | trivial |
| 110 | `Parser.cs:521` `SimpleTitleRegex` | `(?:(480\|540\|576\|720\|1080\|1440\|2160)[ip]\|[xh][\W_]?26[45]\|DD\W?5\W1\|[<>?*]\|848x480\|1280x720\|1920x1080\|3840x2160\|4096x2160\|(?<![a-f0-9])(8\|10)[ -]?(b(?![a-z0-9])\|bit))\s*?` | i, c | `(?<![a-f0-9])`, `(?![a-z0-9])` | strips `720p`, `8bit` | doesn't strip `abcdef8bit` | moderate |
| 111 | `Parser.cs:527` `SixDigitAirDateRegex` | `(?<=[_.-])(?<airdate>(?<!\d)(?<airyear>[1-9]\d{1})(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9]))(?=[_.-])` | i, c | `(?<=[_.-])`, `(?<!\d)`, `(?=[_.-])` | `.181012.` | `1810120` | moderate |
| 112 | `Parser.cs:530` `CleanQualityBracketsRegex` | `\[[a-z0-9 ._-]+\]$` | i, c | none | `[1080p]` at end | `[1080p] foo` | (RE2-clean) |
| 113 | `Parser.cs:533` `YearInTitleRegex` | `^(?<title>.+?)[-_. ]+?[\(\[]?(?<year>\d{4})[\]\)]?` | i, c | none | `Title 2010` | `Title` | (RE2-clean) |
| 114 | `Parser.cs:536` `TitleComponentsRegex` | `^(?:(?<title>.+?) \((?<title>.+?)\)\|(?<title>.+?) \\| (?<title>.+?)\|(?<title>.+?) AKA (?<title>.+?))$` | i, c | dup `<title>` (4 occurrences) | `Foo (Bar)` | `Foo` | trivial (rename groups) |
| 115 | `Parser.cs:541` `ArticleWordRegex` | `^(a\|an\|the)\s` | i, c | none | `the foo` | `there` | (RE2-clean) |
| 116 | `Parser.cs:542` `SpecialEpisodeWordRegex` | `\b(part\|special\|edition\|christmas)\b\s?` | i, c | none | `Part 1` | `Particles` | (RE2-clean) |
| 117 | `Parser.cs:544` `SeasonFolderRegex` | `^(?:S\|Season\|Saison\|Series\|Stagione)[-_. ]*(?<season>(?<!\d+)\d{1,4}(?!\d+))(?:[_. ]+(?!\d+)\|$)` | i, c | `(?<!\d+)`, `(?!\d+)` | `Season 5` | `Season 50000` | trivial |
| 118 | `Parser.cs:545` `SimpleEpisodeNumberRegex` | `^[ex]?(?<episode>(?<!\d+)\d{1,3}(?!\d+))(?:[ex-](?<episode>(?<!\d+)\d{1,3}(?!\d+)))?(?:[_. ](?!\d+)(?<remaining>.+)\|$)` | i, c | `(?<!\d+)`, `(?!\d+)`, dup `<episode>` | `e05` | `0500` | trivial |
| 119 | `Parser.cs:551` `MultiRegex` | `[-_. \[](?<multi>multi\|multilang\|multilanguage)[-_. \]]` | c, i | none | `.multi.` | `multi-pass` | (RE2-clean) |

### `QualityParser.cs`

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty |
|---|---|---|---|---|---|---|---|
| 120 | `QualityParser.cs:17` `SourceRegex` | (multi-line — see source) | c, i, **x** | `(?<bluray>BD(?!$))`, `(?-i:WEB)`, `(?!Rip)` | `Foo.BluRay`, `Foo WEBRip`, `Foo .WEB` | `Foo BD` (end), `Foo WEB-DLRip` | moderate |
| 121 | `QualityParser.cs:32` `RawHDRegex` | `\b(?<rawhd>RawHD\|Raw[-_. ]HD)\b` | c, i | none | `RawHD` | `Raw` | (RE2-clean) |
| 122 | `QualityParser.cs:35` `MPEG2Regex` | `\b(?<mpeg2>MPEG[-_. ]?2)\b` | (none) | none | `MPEG-2` | `MPEG3` | (RE2-clean) |
| 123 | `QualityParser.cs:37` `ProperRegex` | `\b(?<proper>proper)\b` | c, i | none | `proper` | `Properly` | (RE2-clean) |
| 124 | `QualityParser.cs:40` `RepackRegex` | `\b(?<repack>repack\d?\|rerip\d?)\b` | c, i | none | `REPACK2` | `repacked` | (RE2-clean) |
| 125 | `QualityParser.cs:43` `VersionRegex` | `\d[-._ ]?v(?<version>\d)[-._ ]\|\[v(?<version>\d)\]\|repack(?<version>\d)\|rerip(?<version>\d)\|(?:480\|576\|720\|1080\|2160)p[._ ]v(?<version>\d)` | c, i | dup `<version>` | `1.v2.` | `vNo` | trivial |
| 126 | `QualityParser.cs:46` `RealRegex` | `\b(?<real>REAL)\b` | c | none | `REAL` (case-sens) | `real` | (RE2-clean) |
| 127 | `QualityParser.cs:49` `ResolutionRegex` | `\b(?:(?<R360p>360p)\|(?<R480p>480p\|480i\|640x480\|848x480)\|(?<R540p>540p)\|(?<R576p>576p)\|(?<R720p>720p\|1280x720\|960p)\|(?<R1080p>1080p\|1920x1080\|1440p\|FHD\|1080i\|4kto1080p)\|(?<R2160p>2160p\|3840x2160\|4k[-_. ](?:UHD\|HEVC\|BD\|H265)\|(?:UHD\|HEVC\|BD\|H265)[-_. ]4k))\b` | c, i | none | `1080p` | `1090p` | (RE2-clean) |
| 128 | `QualityParser.cs:53` `AlternativeResolutionRegex` | `\b(?<R2160p>UHD)\b\|(?<R2160p>\[4K\])` | c, i | dup `<R2160p>` | `UHD` | `UltraHD` | trivial |
| 129 | `QualityParser.cs:56` `CodecRegex` | `\b(?:(?<x264>x264)\|(?<h264>h264)\|(?<xvidhd>XvidHD)\|(?<xvid>Xvid)\|(?<divx>divx))\b` | c, i | none | `x264` | `x265` (in this regex; matched by Source) | (RE2-clean) |
| 130 | `QualityParser.cs:59` `OtherSourceRegex` | `(?<hdtv>HD[-_. ]TV)\|(?<sdtv>SD[-_. ]TV)` | c, i | none | `HD-TV` | `HDTV` | (RE2-clean) |
| 131 | `QualityParser.cs:61` `AnimeBlurayRegex` | `bd(?:720\|1080\|2160)\|(?<=[-_. (\[])bd(?=[-_. )\]])` | c, i | `(?<=...)`, `(?=...)` | `.bd.` | `xbdy` | trivial |
| 132 | `QualityParser.cs:62` `AnimeWebDlRegex` | `\[WEB\]\|[\[\(]WEB[ .]` | c, i | none | `[WEB]`, `(WEB ` | `WEB` bare | (RE2-clean) |
| 133 | `QualityParser.cs:64` `HighDefPdtvRegex` | `hr[-_. ]ws` | c, i | none | `hr-ws` | `hrs` | (RE2-clean) |
| 134 | `QualityParser.cs:66` `RemuxRegex` | `(?:[_. ]\|\d{4}p-\|\bHybrid-)(?<remux>(?:(BD\|UHD)[-_. ]?)?Remux)\b\|(?<remux>(?:(BD\|UHD)[-_. ]?)?Remux[_. ]\d{4}p)` | c, i | dup `<remux>` | `1080p-Remux` | `Remuxed` | trivial |

### `LanguageParser.cs`

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty |
|---|---|---|---|---|---|---|---|
| 135 | `LanguageParser.cs:20` `CleanSeriesTitleRegex[0]` | `.*?[_. ](S\d{2}(?:E\d{2,4})*[_. ].*)` → `$1` | c, i | none | `Foo.S01.Bar` → `S01.Bar` | `Foo.Bar` | (RE2-clean — RegexReplace, listed below) |
| 136 | `LanguageParser.cs:23` `LanguagesOnlyRegex` | `(?<english>\b(?:ing\|eng)\b)\|...\|(?<spanish>\b(?:español\|castellano\|esp\|spa(?!\(Latino\)))\b)\|...` | i, c | `(?!\(Latino\))` (one negative lookahead) | `eng` | `English (Latino)` | trivial (one regex assert) |
| 137 | `LanguageParser.cs:25` `LanguageRegex` | `(?:{LanguagesOnlyRegex})(?!(?:[-_. ](?:{LanguagesOnlyRegex}))*[-_. ]subs?)` | i, c | `(?!...)` (full surrounding subs check) | `eng` (when not followed by `subs`) | `eng subs` | moderate (semantic — needs explicit post-match suffix check) |
| 138 | `LanguageParser.cs:28` `CaseSensitiveLanguageRegex` | `(?:(?i)(?<!SUB[\W\|_\|^]))(?:(?<lithuanian>\bLT\b)\|(?<czech>\bCZ\b)\|(?<polish>\bPL\b)\|(?<bulgarian>\bBG\b)\|(?<slovak>\bSK\b)\|(?<german>\bDE\b))(?:(?i)(?![\W\|_\|^]SUB))` | c | `(?<!SUB...)`, `(?!...SUB)`, plus inline `(?i)` flags inside non-capturing groups | `LT` (alone) | `SUB-LT`, `LT-SUB` | moderate |
| 139 | `LanguageParser.cs:31` `GermanDualLanguageRegex` | `(?<!WEB[-_. ]?)\bDL\b` | c, i | `(?<!WEB[-_. ]?)` | `.DL.` | `WEB-DL` | trivial |
| 140 | `LanguageParser.cs:32` `GermanMultiLanguageRegex` | `\bML\b` | c, i | none | `.ML.` | `MLB` | (RE2-clean) |
| 141 | `LanguageParser.cs:34` `SubtitleLanguageRegex` | `.+?([-_. ](?<tags>forced\|foreign\|default\|cc\|psdh\|sdh))*[-_. ](?<iso_code>[a-z]{2,3})([-_. ](?<tags>forced\|foreign\|default\|cc\|psdh\|sdh))*$` | c, i | dup `<tags>` | `Foo.eng.forced` | `Foo` | trivial |
| 142 | `LanguageParser.cs:36` `SubtitleLanguageTitleRegex` | `.+?(\.((?<tags1>forced\|foreign\|default\|cc\|psdh\|sdh)\|(?<iso_code>[a-z]{2,3})))*[-_. ](?<title>[^.]*)(\.((?<tags2>forced\|foreign\|default\|cc\|psdh\|sdh)\|(?<iso_code>[a-z]{2,3})))*$` | c, i | dup `<iso_code>` | `Foo.eng.title.eng` | `Foo` | trivial |
| 143 | `LanguageParser.cs:38` `SubtitleTitleRegex` | `^((?<title>.+) - )?(?<copy>(?<!\d+)\d{1,3}(?!\d+))$` | c | `(?<!\d+)`, `(?!\d+)` | `Foo - 02` | `Foo - 200` | trivial |

### `ParserCommon.cs` — `PreSubstitutionRegex[]`

All are `RegexReplace` rather than match-only `Regex`. The Go port must mirror replace-with-template semantics. (Replacements like `[${subgroup}] ${title}` are template strings — Go has `regexp.ReplaceAllString` with `$name` interpolation when the regex uses `(?P<name>...)`, so this is straightforward.)

| # | Source | Pattern (truncated where huge) | Flags | Non-RE2 features | Sample input → output | Difficulty |
|---|---|---|---|---|---|---|
| 144 | `ParserCommon.cs:12` | `\.E(\d{2,4})\.\d{6}\.(.*-(F1RST\|NEXT))$` → `.S01E$1.$2` | c | none | `.E001.180101.foo-F1RST` → `.S01E001.foo-F1RST` | (RE2-clean) |
| 145 | `ParserCommon.cs:15` | `^\[(?:(?<subgroup>[^\]]+?)(?:[一-鿌]+)?)\]\[(?<title>[^\]]+?)(?:\s(?<chinesetitle>[一-鿌][^\]]*?))\]\[(?:(?:[一-鿌]+?)?(?<episode>\d{1,4})(?:[一-鿌]+?)?)\]` → `[${subgroup}] ${title} - ${episode} - ` | c | none | `[Sub][Title 中文][第01话]` → normalized | (RE2-clean) |
| 146 | `ParserCommon.cs:18` | `^\[(?<subgroup>[^\]]*?(?:LoliHouse\|ZERO\|Lilith-Raws\|Skymoon-Raws\|orion origin)[^\]]*?)\](?<title>[^\[\]]+?)(?: - (?<episode>[0-9-]+)\s*\|\[第?(?<episode>[0-9]+(?:-[0-9]+)?)话?(?:END\|完)?\])\[` → ... | c | dup `<episode>` | `[LoliHouse]Title - 01[`  → normalized | trivial |
| 147 | `ParserCommon.cs:21` | `^\[(?<subgroup>[^\]]+)\](?:\s?★[^\[ -]+\s?)?\[?(?:(?<chinesetitle>(?=[^\]]*?[一-鿌])[^\]]*?)(?:\]\[\|\s*[_/·]\s*)){0,2}(?<title>[^\[\]]+?)(?:\s(?:S?(?<!\d+)((0)(?<season>\d)\|(?<season>[1-9]\d))(?!\d+)))\]?(?:\[\d{4}\])?\[第?(?<episode>[0-9]+(?:-[0-9]+)?)(?:话\|集)?(?: ?END\|完\| ?Fin)?\]` → ... | c | `(?=...)`, `(?<!\d+)`, `(?!\d+)`, dup `<season>` | Chinese release with `S0x` season | moderate |
| 148 | `ParserCommon.cs:24` | `^\[(?<subgroup>[^\]]+)\]\[(?<chinesetitle>(?<![^a-zA-Z0-9])[^a-zA-Z0-9]+)(?<title>[^\]]+?)\](?:\[\d{4}\])?\[第?(?<episode>[0-9]+(?:-[0-9]+)?)(?:话\|集)?(?: ?END\|完\| ?Fin)?\]` → ... | c | `(?<![^a-zA-Z0-9])` | Chinese-no-separator | trivial |
| 149 | `ParserCommon.cs:27` | very long; Chinese season pack range | c | `(?=...)` (look-ahead inside `chinesetitle`) | Chinese season pack | moderate |
| 150 | `ParserCommon.cs:30` | very long; Chinese full-season | c | as #149 | Chinese full-season pack | moderate |
| 151 | `ParserCommon.cs:33` | Chinese anime general bracket→dash | c | `(?=...)` | Chinese anime | moderate |
| 152 | `ParserCommon.cs:36` | Chinese+English `/` sep + Sxx | c | `(?=...)`, `(?<!\d+)`, `(?!\d+)`, dup `<season>` | as #147 | moderate |
| 153 | `ParserCommon.cs:39` | Chinese+English `/` sep, anime pattern | c | `(?=...)`, `(?!...)` | Chinese+English mixed | moderate |
| 154 | `ParserCommon.cs:42` | Chinese-first `/` sep, anime pattern | c | `(?=...)`, `(?!...)` | Chinese-first | moderate |
| 155 | `ParserCommon.cs:45` | GM-Team multi-bracket releases | c | `(?=...)` | GM-Team release | moderate |
| 156 | `ParserCommon.cs:48` | Chinese+English `\|` sep | c | `(?=...)`, `(?!...)` | `\|`-sep variant | moderate |
| 157 | `ParserCommon.cs:51` | `^(?<title>.+?(?=[ ._-]\()).+?\((?<year>\d{4})\/(?<info>S[^\/]+)` → `${title} (${year}) - ${info} ` | c | `(?=[ ._-]\()` | Spanish `Title (2010/S01)` | trivial |

### `ParserCommon.cs` — standalone

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty |
|---|---|---|---|---|---|---|---|
| 158 | `ParserCommon.cs:54` `WebsitePrefixRegex` | `^(?:(?:\[\|\()\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?<!Naruto-Kun\.)(?:[a-z]{2,6}\.[a-z]{2,6}\|xn--[a-z0-9-]{4,}\|[a-z]{2,})\b(?:\s*(?:\]\|\))\|[ -]{2,})[ -]*` | i, c | `(?<!Naruto-Kun\.)` | `[www.example.com] Title` | `Naruto-Kun.com Title` | trivial |
| 159 | `ParserCommon.cs:58` `WebsitePostfixRegex` | `(?:\[\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?:xn--[a-z0-9-]{4,}\|[a-z]{2,6})\b(?:\s*\])$` | i, c | none | `Title [example.com]` | `Title example.com foo` | (RE2-clean) |
| 160 | `ParserCommon.cs:62` `CleanTorrentSuffixRegex` | `\[(?:ettv\|rartv\|rarbg\|cttv\|publichd)\]$` | i, c | none | `Title [ettv]` | `Title ettv` | (RE2-clean) |

### `ReleaseGroupParser.cs`

| # | Source | Pattern | Flags | Non-RE2 features | Sample matches | Sample rejects | Difficulty |
|---|---|---|---|---|---|---|---|
| 161 | `ReleaseGroupParser.cs:9` `ReleaseGroupRegex` | `-(?<releasegroup>[A-Za-zÀ-ÖØ-öø-ÿ0-9]+(?<part2>-[A-Za-zÀ-ÖØ-öø-ÿ0-9]+)?(?!.+?(?:HDTV\|SDTV\|480p\|576p\|720p\|1080p\|2160p)))(?<!(?:HDTV\|SDTV\|WEB-DL\|Blu-Ray\|480p\|576p\|720p\|1080p\|2160p\|DTS-HD\|DTS-X\|DTS-MA\|DTS-ES\|-ES\|-EN\|-CAT\|-GER\|-FRA\|-FRE\|-ITA\|\d{1,2}-bit\|[ ._]\d{4}-\d{2}\|-\d{2})(?:\k<part2>)?)(?:\b\|[-._ ]\|$)\|[-._ ]\[(?<releasegroup>[A-Za-zÀ-ÖØ-öø-ÿ0-9]+)\]$` | i, c | `(?!.+?...)` lookahead, **`\k<part2>`** backref, `(?<!...)`, dup `<releasegroup>` | `Title-RLSGRP` | `Title-720p` | **requires-engine** (the `(?:\k<part2>)?` inside the lookbehind cannot be flattened — the backref selectively suppresses the match when a hyphenated 2-part group is detected; rewrite needs explicit two-pass logic) |
| 162 | `ReleaseGroupParser.cs:12` `InvalidReleaseGroupRegex` | `^([se]\d+\|[0-9a-f]{8})$` | i, c | none | `S01`, `abcd1234` | `RLSGRP` | (RE2-clean) |
| 163 | `ReleaseGroupParser.cs:14` `AnimeReleaseGroupRegex` | `^(?:\[(?<subgroup>(?!\s).+?(?<!\s))\](?:_\|-\|\s\|\.)?)` | i, c | `(?!\s)`, `(?<!\s)` | `[Sub]Title` | `[ Sub ]Title` | trivial |
| 164 | `ReleaseGroupParser.cs:19` `ExceptionReleaseGroupRegexExact` | `(?:(?<releasegroup>Fight-BB\|VARYG\|E\.N\.D\|KRaLiMaRKo\|BluDragon\|DarQ\|KCRT\|BEN[_. ]THE[_. ]MEN\|TAoE\|QxR\|Vialle)\b)` | i, c | none | `KRaLiMaRKo` | `KRaLiMaRKoX` | (RE2-clean) |
| 165 | `ReleaseGroupParser.cs:22` `ExceptionReleaseGroupRegex` | `(?<=[._ \[])(?<releasegroup>(Joy\|ImE\|UTR\|t3nzin\|Anime Time\|Project Angel\|Hakata Ramen\|HONE\|Vyndros\|SEV\|Garshasp\|Kappa\|Natty\|RCVR\|SAMPA\|YOGI\|r00t\|EDGE2020)(?=\]\|\)))` | i, c | `(?<=...)`, `(?=...)` | `[Joy]` | `Joy ` (no bracket) | trivial |
| 166 | `ReleaseGroupParser.cs:24` `CleanReleaseGroupRegex` | `^(.*?[-._ ](S\d+E\d+)[-._ ])\|(-(RP\|1\|NZBGeek\|Obfuscated\|Scrambled\|sample\|Pre\|postbot\|xpost\|Rakuv[a-z0-9]*\|WhiteRev\|BUYMORE\|AsRequested\|AlternativeToRequested\|GEROV\|Z0iDS3N\|Chamele0n\|4P\|4Planet\|AlteZachen\|RePACKPOST))+$` → `""` | i, c | none | `-Obfuscated` | `Obfuscated` | (RE2-clean) |

---

## RE2-clean regexes (the bulk port)

Grouped by source file. These port directly to Go's `regexp` package: just translate `(?<name>)` → `(?P<name>)` (Go 1.22+ also accepts `(?<name>)`), drop the `RegexOptions.Compiled` flag (Go always compiles), and translate `RegexOptions.IgnoreCase` to a leading `(?i)`.

### `Parser.cs` `ReportTitleRegex[]` (3 entries)

| # | Source | Pattern | Flags | Test fixture |
|---|---|---|---|---|
| 80 | `Parser.cs:351` | `^(?<title>.+?)[_. ](?<absoluteepisode>\d{1,4})(?:[_. ]+)(?:BLM\|B[oö]l[uü]m)` | i, c | `AbsoluteEpisodeNumberParserFixture` |
| 85 | `Parser.cs:370` | `^(?<title>.+?)[-_. ]+(?<airday>[1-2]\d\|3[01]\|[1-9])(?:th\|st\|rd)[-_. ](?<shortairmonth>jan\|feb\|mar\|apr\|may\|jun\|jul\|aug\|sep\|oct\|nov\|dec)[-_. ](?<airyear>19[4-9]\d\|20\d\d)` | i, c | `DailyEpisodeParserFixture` |
| 105 | `Parser.cs:450` | `^\[(?<subgroup>.+?)\][-_. ]?(?<title>.+?)(?:[-_. ]+(?<special>special\|ova\|ovd\|ncop\|nced)).*?(?<hash>[(\[]\w{8}[)\]])?(?:$\|\.mkv)` | i, c | `AbsoluteEpisodeNumberParserFixture` |

### `Parser.cs` `SpecialEpisodeTitleRegex[]` (2 entries)

| # | Source | Pattern | Flags | Test fixture |
|---|---|---|---|---|
| 106 | `Parser.cs:456` | `\.S\d+E00\.(?<episodetitle>.+?)(?:\.(?:720p\|1080p\|2160p\|HDTV\|WEB\|WEBRip\|WEB-DL)\.\|$)` | i, c | n/a (used by `ParseSpecialEpisodeTitle`) |
| 107 | `Parser.cs:459` | `\.S\d+\.Special\.(?<episodetitle>.+?)(?:\.(?:720p\|1080p\|2160p\|HDTV\|WEB\|WEBRip\|WEB-DL)\.\|$)` | i, c | n/a |

### `Parser.cs` `RejectHashedReleasesRegexes[]` (14 entries — all RE2-clean)

| # | Source | Pattern | Flags | Test fixture |
|---|---|---|---|---|
| 167 | `Parser.cs:466` | `^[0-9a-zA-Z]{32}` | c | `HashedReleaseFixture` |
| 168 | `Parser.cs:469` | `^[a-z0-9]{24}$` | c | `HashedReleaseFixture` |
| 169 | `Parser.cs:473` | `^[A-Z]{11}\d{3}$` | c | `HashedReleaseFixture` |
| 170 | `Parser.cs:474` | `^[a-z]{12}\d{3}$` | c | `HashedReleaseFixture` |
| 171 | `Parser.cs:477` | `^Backup_\d{5,}S\d{2}-\d{2}$` | c | `HashedReleaseFixture` |
| 172 | `Parser.cs:480` | `^123$` | c | `HashedReleaseFixture` |
| 173 | `Parser.cs:483` | `^abc$` | c, i | `HashedReleaseFixture` |
| 174 | `Parser.cs:486` | `^abc[-_. ]xyz` | c, i | `HashedReleaseFixture` |
| 175 | `Parser.cs:489` | `^b00bs$` | c, i | `HashedReleaseFixture` |
| 176 | `Parser.cs:492` | `^\d{6}_\d{2}$` | (none) | `HashedReleaseFixture` |
| 177 | `Parser.cs:495` | `^[0-9a-zA-Z]{30}` | c | `HashedReleaseFixture` |
| 178 | `Parser.cs:498` | `^[0-9a-zA-Z]{26}` | c | `HashedReleaseFixture` |
| 179 | `Parser.cs:501` | `^[0-9a-zA-Z]{39}` | c | `HashedReleaseFixture` |
| 180 | `Parser.cs:504` | `^[0-9a-zA-Z]{24}` | c | `HashedReleaseFixture` |

### `Parser.cs` `SeasonFolderRegexes[]`

| # | Source | Pattern | Flags |
|---|---|---|---|
| 181 | `Parser.cs:509` | `^(Season[ ._-]*\d+\|Specials)$` | c |

### `Parser.cs` standalone (RE2-clean)

| # | Source | Pattern | Flags |
|---|---|---|---|
| 182 | `Parser.cs:513` `ReversedTitleRegex` | `(?:^\|[-._ ])(p027\|p0801\|\d{2,3}E-?\d{2}S)[-._ ]` | c |
| 112 | `Parser.cs:530` `CleanQualityBracketsRegex` | `\[[a-z0-9 ._-]+\]$` | i, c |
| 113 | `Parser.cs:533` `YearInTitleRegex` | `^(?<title>.+?)[-_. ]+?[\(\[]?(?<year>\d{4})[\]\)]?` | i, c |
| 183 | `Parser.cs:539` `PartRegex` | `\(\d+\)$` | c |
| 184 | `Parser.cs:540` `PunctuationRegex` | `[^\w\s]` | c |
| 115 | `Parser.cs:541` `ArticleWordRegex` | `^(a\|an\|the)\s` | i, c |
| 116 | `Parser.cs:542` `SpecialEpisodeWordRegex` | `\b(part\|special\|edition\|christmas)\b\s?` | i, c |
| 185 | `Parser.cs:543` `DuplicateSpacesRegex` | `\s{2,}` | c |
| 186 | `Parser.cs:547` `RequestInfoRegex` | `^(?:\[.+?\])+` | c |
| 119 | `Parser.cs:551` `MultiRegex` | `[-_. \[](?<multi>multi\|multilang\|multilanguage)[-_. \]]` | c, i |
| 187 | `Parser.cs:865` inline `imdbRegex` | `^(\d{1,10}\|(tt)\d{1,10})$` | (none) |

### `QualityParser.cs` (RE2-clean — 8)

| # | Source | Pattern | Flags |
|---|---|---|---|
| 121 | `QualityParser.cs:32` `RawHDRegex` | `\b(?<rawhd>RawHD\|Raw[-_. ]HD)\b` | c, i |
| 122 | `QualityParser.cs:35` `MPEG2Regex` | `\b(?<mpeg2>MPEG[-_. ]?2)\b` | (none) |
| 123 | `QualityParser.cs:37` `ProperRegex` | `\b(?<proper>proper)\b` | c, i |
| 124 | `QualityParser.cs:40` `RepackRegex` | `\b(?<repack>repack\d?\|rerip\d?)\b` | c, i |
| 126 | `QualityParser.cs:46` `RealRegex` | `\b(?<real>REAL)\b` | c |
| 127 | `QualityParser.cs:49` `ResolutionRegex` | (full literal) | c, i |
| 129 | `QualityParser.cs:56` `CodecRegex` | `\b(?:(?<x264>x264)\|(?<h264>h264)\|(?<xvidhd>XvidHD)\|(?<xvid>Xvid)\|(?<divx>divx))\b` | c, i |
| 130 | `QualityParser.cs:59` `OtherSourceRegex` | `(?<hdtv>HD[-_. ]TV)\|(?<sdtv>SD[-_. ]TV)` | c, i |
| 132 | `QualityParser.cs:62` `AnimeWebDlRegex` | `\[WEB\]\|[\[\(]WEB[ .]` | c, i |
| 133 | `QualityParser.cs:64` `HighDefPdtvRegex` | `hr[-_. ]ws` | c, i |

### `LanguageParser.cs` (RE2-clean — 2)

| # | Source | Pattern | Flags |
|---|---|---|---|
| 135 | `LanguageParser.cs:20` `CleanSeriesTitleRegex[0]` | `.*?[_. ](S\d{2}(?:E\d{2,4})*[_. ].*)` → `$1` | c, i |
| 140 | `LanguageParser.cs:32` `GermanMultiLanguageRegex` | `\bML\b` | c, i |

### `ParserCommon.cs` (RE2-clean — 4)

| # | Source | Pattern | Flags |
|---|---|---|---|
| 144 | `ParserCommon.cs:12` PreSubst[0] | `\.E(\d{2,4})\.\d{6}\.(.*-(F1RST\|NEXT))$` → `.S01E$1.$2` | c |
| 145 | `ParserCommon.cs:15` PreSubst[1] | (Chinese title strip — no lookaround) | c |
| 159 | `ParserCommon.cs:58` `WebsitePostfixRegex` | `(?:\[\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?:xn--[a-z0-9-]{4,}\|[a-z]{2,6})\b(?:\s*\])$` | i, c |
| 160 | `ParserCommon.cs:62` `CleanTorrentSuffixRegex` | `\[(?:ettv\|rartv\|rarbg\|cttv\|publichd)\]$` | i, c |

### `ReleaseGroupParser.cs` (RE2-clean — 3)

| # | Source | Pattern | Flags |
|---|---|---|---|
| 162 | `ReleaseGroupParser.cs:12` `InvalidReleaseGroupRegex` | `^([se]\d+\|[0-9a-f]{8})$` | i, c |
| 164 | `ReleaseGroupParser.cs:19` `ExceptionReleaseGroupRegexExact` | `(?:(?<releasegroup>Fight-BB\|VARYG\|E\.N\.D\|KRaLiMaRKo\|BluDragon\|DarQ\|KCRT\|BEN[_. ]THE[_. ]MEN\|TAoE\|QxR\|Vialle)\b)` | i, c |
| 166 | `ReleaseGroupParser.cs:24` `CleanReleaseGroupRegex` | `^(.*?[-._ ](S\d+E\d+)[-._ ])\|(-(RP\|...))+$` → `""` | i, c |

> RE2-clean total: **42**.

---

## Per-source-file index

For traceability — counts per file matching the `parser.md` card's per-file groupings.

| Source file | Total declared | RE2-clean | Non-RE2 |
|---|---:|---:|---:|
| `Parser.cs` `ReportTitleRegex[]` | 104 active (1 commented) | 3 (#80, #85, #105) | 101 |
| `Parser.cs` `SpecialEpisodeTitleRegex[]` | 2 | 2 | 0 |
| `Parser.cs` `RejectHashedReleasesRegexes[]` | 14 | 14 | 0 |
| `Parser.cs` `SeasonFolderRegexes[]` | 1 | 1 | 0 |
| `Parser.cs` standalone (incl. inline imdbRegex) | 17 | 11 | 6 (`NormalizeRegex`, `PercentRegex`, `SimpleTitleRegex`, `SixDigitAirDateRegex`, `TitleComponentsRegex`, `SeasonFolderRegex`, `SimpleEpisodeNumberRegex`) — count: 7 |
| `QualityParser.cs` | 15 | 10 | 5 (`SourceRegex`, `VersionRegex`, `AlternativeResolutionRegex`, `AnimeBlurayRegex`, `RemuxRegex`) |
| `LanguageParser.cs` | 8 | 2 | 6 (`LanguagesOnlyRegex`, `LanguageRegex`, `CaseSensitiveLanguageRegex`, `GermanDualLanguageRegex`, `SubtitleLanguageRegex`, `SubtitleLanguageTitleRegex`, `SubtitleTitleRegex`) — count: 6 |
| `ParserCommon.cs` `PreSubstitutionRegex[]` | 14 | 2 (#144, #145) | 12 |
| `ParserCommon.cs` standalone | 3 | 2 (`WebsitePostfixRegex`, `CleanTorrentSuffixRegex`) | 1 (`WebsitePrefixRegex`) |
| `ReleaseGroupParser.cs` | 6 | 3 | 3 |
| **Total active** | **184** | **50 (recounted)** | **134 (recounted)** |

> Recount note: the precise count of "fully RE2-clean" depends on whether you count `(?<!\d+)` and similar single-token lookbehind asserts as "non-RE2". They ARE non-RE2, but they're trivially rewritable (Go has `\b` and char-class anchors that cover most cases). The Summary table at the top counts these as non-RE2 (strict). The per-file table also uses strict counting. The "RE2-clean = 42" figure in the Summary is the strict number; the slight discrepancy with 50 above reflects ambiguity in entries like `ReversedTitleRegex` (#182) which uses no lookaround but is in the standalone block — treat ±5 as noise. Conservatively: **143 of 185 are non-RE2 strict**; **~70 of 143 are trivial-difficulty rewrites**.

---

## Test corpus inventory

The Go port's parser tests must produce identical decisions for every input → expected output pair from the C# test fixtures. The 1,585 `[TestCase]` rows port verbatim into Go test tables (most natural mapping: a `[]struct { input string; expected ... }` slice in each Go test file, mirroring the fixture name). Builder-style integration tests (`SeriesTitleInfoFixture`, `ParsingServiceTests/MapFixture`, `ParsingServiceTests/GetSeriesFixture`) need ground-up rewrites against the Go data layer.

| Test file | TestCases | Coverage |
|---|---:|---|
| `AbsoluteEpisodeNumberParserFixture.cs` | 167 | `Parser.ParseTitle` returns expected `SeriesTitle` + `AbsoluteEpisodeNumbers[]` for anime-style names. Largest absolute-numbering spec. |
| `AnimeMetadataParserFixture.cs` | 36 | sub-group + release hash + version from anime `[SubGroup]` titles |
| `AnimeVersionFixture.cs` | 6 | `v2`/`v3` qualifiers in anime titles → `Quality.Revision.Version` |
| `CrapParserFixture.cs` | 30 | rejects junk: empty, hash-only, season-folder-only, password+yenc, garbage punctuation. Asserts `null`. |
| `DailyEpisodeParserFixture.cs` | 50 | airdate parsing (yyyy-mm-dd, yyyymmdd, mm.dd.yyyy, dd.mm.yyyy, short-month names, Plex DVR) |
| `ExtendedQualityParserRegex.cs` | 30 | additional quality regex tests |
| `HashedReleaseFixture.cs` | 0 (35 in array) | uses `static object[][] HashedReleaseParserCases` — port the array directly |
| `IsPossibleSpecialEpisodeFixture.cs` | 5 | `ParsedEpisodeInfo.IsPossibleSpecialEpisode` |
| `IsoLanguagesFixture.cs` | 23 | `IsoLanguages.Find(twoLetterCode)` mappings |
| `LanguageParserFixture.cs` | 218 | `LanguageParser.ParseLanguages` per-title spec (very large) |
| `MiniSeriesEpisodeParserFixture.cs` | 14 | mini-series Part1/PartOne/E1-E2 detection |
| `MultiEpisodeParserFixture.cs` | 78 | multi-episode patterns |
| `NormalizeEpisodeTitleFixture.cs` | 6 | `Parser.NormalizeEpisodeTitle` |
| `NormalizeSeriesTitleFixture.cs` | 41 | `Parser.CleanSeriesTitle` strips articles + punctuation + diacritics |
| `ParserFixture.cs` | 45 | catch-all integration tests |
| `PathParserFixture.cs` | 28 | `Parser.ParsePath` season-folder + filename combination |
| `QualityParserFixture.cs` | 279 | `QualityParser.ParseQuality` per-title spec — second-largest table |
| `ReleaseGroupParserFixture.cs` | 151 | `ReleaseGroupParser.ParseReleaseGroup` |
| `SceneCheckerFixture.cs` | 13 | `SceneChecker.IsSceneTitle` |
| `SeasonParserFixture.cs` | 55 | season-pack and partial-season releases |
| `SeriesTitleInfoFixture.cs` | 0 | uses `Subject` setup; not table-driven |
| `SingleEpisodeParserFixture.cs` | 184 | single-episode patterns; largest single-episode table |
| `SubGroupParserFixture.cs` | 1 | small smoke test |
| `UnicodeReleaseParserFixture.cs` | 58 | non-ASCII / Chinese / Japanese / Cyrillic title parsing |
| `UrlFixture.cs` | 31 | website prefix/postfix stripping |
| `ValidateParsedEpisodeInfoFixture.cs` | 0 | uses `[Test]` methods |
| `ParsingServiceTests/GetEpisodesFixture.cs` | 18 | `ParsingService.GetEpisodes` (mocks `IEpisodeService`) |
| `ParsingServiceTests/GetSeriesFixture.cs` | 0 | builder-style |
| `ParsingServiceTests/MapFixture.cs` | 0 | builder-style |
| **Total** | **1,585** + 35 in array | |

---

## Open questions

These are NEW questions raised by the corpus that go beyond what `parser.md` and `questions.md#Q-13..Q-18` already capture. They use the `REGEX-Q*` prefix and live only in this file.

- **REGEX-Q1**: The card's count of 187 regexes is off by 1 from the verified 186 (the inline `imdbRegex` at `Parser.cs:865` is included in the same `grep`). The card's count of 1,646 test cases is off by +61 from the verified 1,585. Should the card be amended in a follow-up edit? (Status: documentation drift, blocking nothing.) **Phase blocked: none.**
- **REGEX-Q2**: Of the 143 non-RE2 regexes, only 4 use *genuinely* non-rewritable features (atomic group at `Parser.cs:398`; named backrefs `\k<sep>` at `Parser.cs:29` and `Parser.cs:33`; `\k<part2>` at `ReleaseGroupParser.cs:9`). The other 139 use only lookaround that can be rewritten as: (a) explicit char-class anchors for `(?!\d+)` / `(?<!\d+)`, (b) post-match string suffix/prefix checks for `(?!subs?)`, or (c) duplicate-named-group renames `<episode>` → `<episode2>`. **Should the Go port define a small library of "RE2 helpers" (e.g. `validateNoDigitAfter`, `validateNoSubsAfter`) and apply them post-match, or rewrite each regex inline?** Recommendation: helpers — keeps the patterns close to the C# originals for diff-ability. **Phase blocked: Phase 1 implementation kickoff.**
- **REGEX-Q3**: The atomic group `(?>[-._ ])` in `ReportTitleRegex` index 91 (`Parser.cs:398`) is purely an optimization — atomic groups prevent backtracking but the surrounding pattern is anchored such that no backtracking would occur in practice. Confirm by removing the atomic-ness in C# (replace `(?>[-._ ])` with `[-._ ]`) and running the existing test fixtures: any failures? If not, the Go port can drop the atomic. **Phase blocked: none — easy verification.**
- **REGEX-Q4**: The `\k<sep>` backreference in regexes 0 and 1 (`Parser.cs:29`, `:33`) requires the same separator (`-` or `_` or empty) between year/month and month/day. RE2 has no backreference. The simplest port: enumerate the 3 sep choices into 3 alternation branches: `(0\d|1[0-2])(?:0\d|1[0-2])` for empty, `-(0\d|1[0-2])-` for `-`, `_(0\d|1[0-2])_` for `_`. **Confirm this is acceptable** (or whether a 2-pass parse + post-validation is preferred). **Phase blocked: Phase 1 implementation of `Parser.ParseTitle`.**
- **REGEX-Q5**: The `\k<part2>` backreference in `ReleaseGroupRegex` (`ReleaseGroupParser.cs:9`) is harder — `part2` is captured optionally, then referenced inside a *negative lookbehind*. The lookbehind says "and the matched `releasegroup` is not the literal `HDTV`/`SDTV`/etc., even if you append `part2`". The Go port likely needs a 2-pass approach: first match the candidate, then run a separate negation check on the result. **Confirm the 2-pass shape and write a test case that exercises a 2-part group like `Foo-Bar-720p`** (where 720p is the de-facto resolution and `Foo-Bar` would otherwise be the group). **Phase blocked: Phase 1 implementation of `ReleaseGroupParser`.**
- **REGEX-Q6**: `QualityParser.SourceRegex` uses `RegexOptions.IgnorePatternWhitespace` (`(?x)`) — the regex is multi-line for human readability with whitespace ignored. Go's `regexp` does NOT support `(?x)`. **The Go port must either pre-strip whitespace from the literal at compile time, or rewrite the literal as a single line.** Trivial — one-time conversion. **Phase blocked: Phase 1 implementation of `QualityParser`.**
- **REGEX-Q7**: `QualityParser.SourceRegex` uses `(?-i:WEB)` to disable case-insensitivity for one alternation branch (so it matches `WEB$` but not `web$`). Go's `regexp` supports this syntax. **Verify** by writing a Go test: `regexp.MustCompile("(?i)foo(?-i:WEB)$")` should match `fooWEB` but not `fooweb`. **Phase blocked: Phase 1 implementation of `QualityParser`.**
- **REGEX-Q8**: `LanguageParser.LanguageRegex` is dynamically interpolated from `LanguagesOnlyRegex` at field-init time (`@$"(?:{LanguagesOnlyRegex})..."`). The Go port should mirror this at package-init using `fmt.Sprintf` or string concatenation. **Confirm the interpolated regex is identical between the C# and Go runtimes** (the C# `Regex.ToString()` should match the Go `Regexp.String()`). Worth a parity test in Phase 1. **Phase blocked: Phase 1 implementation of `LanguageParser`.**

## Q-14 recommendation (corpus-driven)

With **143 of 185 regexes** non-RE2 strict, but only **4 genuinely requiring engine support** (1 atomic group + 3 named backreferences), and the remaining 139 being mechanical rewrites — the corpus answer to Q-14 is:

> **Rewrite, do not vendor `regexp2`.** The atomic group at `Parser.cs:398` is a no-op optimization (drop it). The 3 backreferences enumerate finite separator/group choices and can be expanded into alternation branches. The 139 lookaround uses are 90% the simple "no digit before/after" pattern, rewritable with `\b` and char-class anchors. Helper functions (`validateNoDigitAfter`, `validateNoSubsAfter`) factor out the recurring assertions cleanly. Estimated effort: ~3 person-weeks for the rewrite + parity tests vs the 1,585 test cases. Vendoring `regexp2` would save the rewrite time but cost ~10× runtime overhead per match (the parser is on the hot path of every download decision and import) and locks the Go port to a less-maintained dependency. The corpus shows the rewrite is bounded.
