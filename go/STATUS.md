# Sonarr Go-port — session resumption guide

This file is the hand-off pointer between sessions. It is the **first thing a new session should read** when continuing the C#→Go port. The strategic plan lives at `~/.claude/plans/curried-discovering-meadow.md`; this file tracks where execution actually stopped.

Last updated: 2026-05-03 (Phase 1 done; Phase 2 underway — QualityParser COMPLETE; LanguageParser COMPLETE; ReleaseGroupParser COMPLETE for the fixture surface; Parser.ParseTitle slice 2.12.0 shipped: ParsedEpisodeInfo + SeriesTitleInfo + ReleaseType data models + small standalone helpers (ValidateBeforeParsing / HasMultipleLanguages / NormalizeImdbID / ParseNumber / ParseDecimal / convertToNumerals) + RejectHashedReleasesRegexes + SeasonFolderRegexes; the 122-element ReportTitleRegex array + ParseTitle pipeline still pending in slices 2.12.x).

## Where to resume

**Phase 0.5 signed off; Phase 1.0 (datastore foundations) shipped to working tree. 30 non-provider repos + 7 provider-pattern repos = every persisted table at HEAD now has a Go repo. Provider registry (TP-Q1/Q2) shipped with the full 71-class catalog populated.** Tests green for both engines (`SONARR_PG_DSN=postgres:///sonarr_spike go test -count=1 -race -p 1 ./...`). Lint clean (`golangci-lint run ./...` 0 issues). Migration 001 produces the 14-table set the C# Migration/001 produces; goose state stored in `goose_db_version` (SQLite + Postgres) — not the C# `VersionInfo` table because version numbering differs (gappy in FluentMigrator vs contiguous in goose).

**Note on `-p 1`**: running `go test ./...` without `-p 1` causes intermittent Postgres deadlocks during migration version 71 because multiple test binaries hit the shared `sonarr_spike` DB and rebuild the `public` schema simultaneously. SQLite-only runs are unaffected. Worth fixing eventually (per-package schema or test-package-level mutex via filelock); for now use `-p 1` for the full suite.

The next concrete actions, in priority order:
1. **Migrations 002..230 sweep** — mechanical port of the remaining 223 migrations. Each one cites its `migrations/ledger.md` row. Probably batched (5-10 per PR) since most are simple ALTER TABLE / CREATE INDEX. The 28 HIGH-RISK embedded-doc mutations get individual PRs with extra test coverage.
2. **Phase 1.1 — repository layer** — `IBasicRepository<T>` equivalent. Reflection-driven column mapping or hand-rolled per-type? Decision deferred to that PR; cross-cite TP-Q1/Q2 string registry.
3. **Phase 1.2 — embedded-doc serialization** — STJson-equivalent settings (per `embedded-docs/catalogue.md` correction: bridge is STJson, not Newtonsoft).
4. **Phase 1.3 — query builder** — replaces `WhereBuilder*.cs`. Probably a small fluent builder, not LINQ-equivalent.
5. **`signalr-philippseith` spike** — needs the C# Sonarr running AND the React frontend pointed at a Go SignalR replacement. Defer until Phase 5 prep.
6. **First commit batch** — `global.json` rollForward + entire `go/` tree (Phase 0 + 0.5 + 1.0) is uncommitted. Decide commit boundary.

Open the plan at `~/.claude/plans/curried-discovering-meadow.md` for the full phase definitions.

## Quickstart for a new session

```sh
# Verify the toolchain is still installed.
go version            # need 1.26+
golangci-lint --version  # need 2.x
brew services list | grep postgres   # postgres@16 should be running for the pgx spike

# Health check the Go module.
cd /Users/dean/Documents/GitHub/Sonarr/go
go vet ./...
golangci-lint run ./...
SONARR_PG_DSN="postgres:///sonarr_spike" go test -count=1 -race ./...   # all green at last save

# Skim the analysis state.
ls go/analysis/subsystems/      # cards already drafted
ls go/analysis/spikes/          # spikes already verified
cat go/analysis/questions.md    # running open-question ledger (26 entries last save)
```

## Phase status

### Phase 2 — Domain core ⏳ in progress

#### 2.0 — Parser title normalisers ✅ shipped (2026-05-03)

First Phase 2 commit. `internal/parser` ports the title-normalisation primitives the rest of `Parser.cs` builds on.

The strategic plan's Phase 2 directive: "Port src/NzbDrone.Core.Test/ParserTests/** as table-driven Go tests *first*; they're the spec." This commit ports two of the smaller fixtures verbatim:

- `NormalizeSeriesTitleFixture.cs` (33 cases across 8 [Test]/[TestCase] methods) → `TestCleanSeriesTitle*` table-driven tests.
- `NormalizeEpisodeTitleFixture.cs` (6 cases) → `TestNormalizeEpisodeTitle`.

Implementation surface:

- `parser.CleanSeriesTitle(title) string` — the canonical search-key form: replace digits-followed-by-`%` with `<digits>percent`, strip middle-of-string common words (`a / à / an / the / and / or / of`) when bordered by non-word boundaries on both sides AND not at string start/end, strip remaining non-word characters and underscores, lowercase, RemoveDiacritics. Pure-numeric input passes through unchanged.
- `parser.NormalizeEpisodeTitle(title) string` — extracts the inner episode title from a `S<N>E00.*` or `S<N>.Special.*` release name when matched, then strips `(N)`-trailing parts and punctuation, collapses whitespace, lowercases.
- `parser.NormalizeTitle(title) string` — looser title-matching variant that strips punctuation, leading articles, and collapses whitespace.
- `parser.RemoveDiacritics(s) string` — NFKD + combining-mark strip plus a hand-curated lookup table for Latin extension characters that don't decompose under NFKD (`ð → d`, `þ → th`, `æ → ae`, `ø → o`, `ß → ss`, `ł → l`, `đ → d`, `œ → oe`, plus capitalised and `Ð`/`Ø` variants). Mirrors the `Diacritical` NuGet package the C# tree calls into. The `Ríkið` → `rikid` test case exercises the lookup table; without it, the bare NFKD pass would leave `ð` intact.

Notable RE2 reformulations vs the C# regex literals:

- `(?<=\b\d+)%` → `\b(\d+)%` with capture-and-emit replacement. RE2 has no lookbehind; pulling the digits into a capture group preserves the same matched bytes and emits them back via `${1}percent`.
- The complex `NormalizeRegex` `((?:\b|_)(?<!^)([aà](?!$)|an|the|and|or|of)(?!$)(?:\b|_))|\W|_` was split: the alternation's first arm (with `(?<!^)` / `(?!$)` lookarounds) is reformulated as `stripMiddleCommonWords`, a hand-coded scan that walks bytes, applies position checks directly, and respects multi-byte UTF-8 boundary semantics. The `\W|_` arm becomes `[^\p{L}\p{N}]` to match C#'s Unicode-aware `\W` (Go's default `\W` is ASCII-only; using `\W|_` directly would incorrectly strip non-ASCII letters like `à` and `í`).

What's deliberately deferred to subsequent Phase 2 commits: `ParseTitle` (the main entry point), the 104-element `ReportTitleRegex[]` array (the heavy lifting), `QualityParser`, `LanguageParser`, `ReleaseGroupParser`, `SceneChecker`. Each owes its corresponding `*Fixture.cs` test as the first commit, then the implementation. The 1,585 [TestCase] rows under `ParserTests/` are the spec — see `analysis/parser/regex-corpus.md` for the porter's checklist.

#### 2.1 — Language registry + IsoLanguages lookup ✅ shipped (2026-05-03)

`internal/language` ports three colocated concerns from the C# tree:

- `NzbDrone.Core.Languages.Language` — 52 named languages plus `Unknown` (Id=0) and `Original` (Id=-2). IDs are frozen because they're persisted in user databases (qualityprofiles, releaseprofiles, customformats); renumbering would corrupt rows. Order in source mirrors `Language.cs:73-126` so a future drift against the C# tree is line-aligned.
- `NzbDrone.Core.Parser.IsoLanguages` — registry of 53 ISO entries (53 not 52: Norwegian appears twice, with both `no` and `nb` two-letter codes; Dutch appears twice, once for the Dutch language and once for Flemish — by-design duplicates the C# HashSet preserves). Plus `AlternateIsoCodeMappings` for legacy codes (`cze` → Czech, `dut` → Dutch, `mac` → Macedonian, `rum` → Romanian, `yue`/`zhtw` → Chinese).
- `NzbDrone.Core.Organizer.FileNameBuilder.Iso639BTMap` — 21-entry bibliographic-vs-terminological code map (`geo → kat`, `cze → ces`, `ger → deu`, etc.). Colocated here rather than under `internal/organizer` because the data belongs with the language registry.

Public surface:

- `language.Language` value type with `ID` + `Name`. Comparable by value.
- Predefined `language.English`, `language.Czech`, etc. as package-level vars.
- `language.FindByID(id) (Language, bool)` — Id 0 always resolves to `Unknown` matching C# `Language.cs:196`.
- `language.FindByName(name) (Language, bool)` — case-insensitive match.
- `language.All() []Language` — fresh copy of the registry in registration order.
- `language.Iso{TwoLetter, Country, ThreeLetter, Language}` value type plus `CountryLower()` helper.
- `language.FindIso(isoCode) (Iso, bool)` — full algorithm parity with `IsoLanguages.Find` at `IsoLanguages.cs:78-113`. 2-letter, 3-letter, country-suffixed, alternate-code, and bibliographic-mapped paths all exercised by tests.
- `language.GetIso(language) (Iso, bool)` — first Iso whose Language matches.
- `language.FindIsoByName(name) (Iso, bool)` — case-insensitive name lookup.

Test corpus: 12 tests / ~30 cases. Direct ports of `IsoLanguagesFixture.cs` cases (English / unknown / Portuguese / de-AU / cze / Urdu / Romansh / Georgian — the last exercising both the alternate-mapping path and the iso639BTMap), plus Language-specific tests (FindByID, FindByName, registry size, frozen-Id pins). All green; lint clean.

C# behavioural quirks preserved verbatim: `AlternateIsoCodeMappings` uses the original `isoCode` (not the lowercased langCode), so `CZE` does NOT match `cze` — flagged in the FindIso doc comment.

#### 2.2 — Quality registry ✅ shipped (2026-05-03)

`internal/quality` ports `NzbDrone.Core.Qualities.Quality` plus the `QualitySource` enum. Same shape as the language registry: 22 named qualities (`Unknown` + 21 active) with frozen integer Ids that persist in user databases. Id 11 is intentionally skipped — `HDTV480p` was retired (`Quality.cs:89` carries a commented-out declaration as the historical pin); never repurpose it.

Public surface:

- `quality.QualitySource` int enum: SourceUnknown / Television / TelevisionRaw / Web / WebRip / DVD / Bluray / BlurayRaw. Integer order matches the C# enum at `QualitySource.cs`; values appear in CustomFormat SourceSpecification.value JSON.
- `quality.Quality` value type with `ID` + `Name` + `Source` + `Resolution`. Comparable by value.
- Predefined vars: `Unknown`, `SDTV`, `DVD`, `HDTV720p`, `WEBDL1080p`, `Bluray2160p`, `Bluray1080pRemux`, etc.
- `quality.All() []Quality` — fresh copy in registration order (matches C# `Quality.All` order, which differs from declaration order — reading order is the UI quality-profile order).
- `quality.FindByID(id) (Quality, bool)` — Id 0 always resolves to Unknown matching C# `Quality.cs:191`. Unknown ids return `ok=false` (the C# tree throws `ArgumentException`; the Go port returns false instead).

Tests: 5 functions covering FindByID (incl. retired Id 11 returns false), All count + ordering pin, frozen-Id pin, Source/Resolution sanity. Lint clean.

What's deferred: `QualityModelComparer` (the runtime comparator that reads per-user QualityDefinition rows from the DB), and `QualityParser` itself (15 regexes, 522-line fixture — its own commit). The `QualityDefinition` repo (per-user min/max/preferred MB-per-minute thresholds) shipped earlier in Phase 1 as part of the storage layer.

#### 2.3 — Revision + QualityModel + persisted JSON wire format ✅ shipped (2026-05-03)

Companion to the registry: the persistable composite that wraps a Quality with its proper/repack metadata.

- `quality.Revision` — `Version` (proper iteration, defaults to 1) + `Real` (REAL.PROPER count) + `IsRepack` flag. `Compare` ranks Real first then Version (matches `Revision.cs:33-56`); `Equal` is structural over `(Version, Real)` only — IsRepack is intentionally NOT part of equality, mirroring the C# behaviour. `String` emits `v<Version>` plus optional ` Real:<Real>` suffix.
- `quality.DetectionSource` int enum: Unknown / Name / Extension / MediaInfo. Used to track HOW a quality call was derived during parsing (filename inference vs file-extension lookup vs MediaInfo probe). Persisted-form is `[JsonIgnore]` so these fields don't round-trip through serialisation.
- `quality.Model` — Quality + Revision + 3 DetectionSource tags. `Compare` ranks via `QualityWeight` (the static C# `DefaultQualityDefinitions.Weight`) first, falling through to Revision. `Equal` compares Quality + Revision only.
- `quality.QualityWeight(q) int` — static weights from `Quality.DefaultQualityDefinitions` at `Quality.cs:158-179`. Used by `Model.Compare`. Several qualities intentionally tie (WEBRip720p and WEBDL720p both Weight=10) so the upgrade path doesn't keep flipping between visually-equivalent sources.

JSON wire-format parity (per `analysis/embedded-docs/catalogue.md` § QualityModel):

```
{"quality": 7, "revision": {"version":1,"real":0,"isRepack":false}}
```

Note `quality` is the BARE int Id, not the full Quality object — the C# tree applies a per-container `QualityIntConverter` to reduce Quality to its Id on serialise. The Go port replicates this via custom `MarshalJSON` / `UnmarshalJSON` on `Model` that go through the registry. Unknown legacy Ids (e.g. 11, the retired `HDTV480p`) deserialise to `Quality.Unknown` rather than erroring — preserves byte-equivalence on legacy rows.

REST-shape full-form Quality JSON is partially covered: the struct has camelCase tags for `id` / `name` / `source` / `resolution`, but `source` currently marshals as the int enum value rather than the C# `JsonStringEnumConverter` camelCase string ("television" etc.). Phase 5 swaps the marshaller when REST endpoints land. Phase 2 storage path uses int-reduction so this gap doesn't block.

10 tests: Revision default + Compare + Equal-ignores-IsRepack + String table; Model default + String + Compare + Equal-ignores-DetectionSource; persisted JSON shape pin (`{"quality":7,...}`); JSON round-trip with DetectionSource fields hidden; legacy unknown-Id falls back to Unknown; QualityWeight pin.

#### 2.4 — ParseRevision (PROPER / REPACK / vN / REAL detection) ✅ shipped (2026-05-03)

First slice of `QualityParser` ported. The four-regex revision-detection block at `QualityParser.cs:675-708` is small, isolated, and testable on its own — making it a good standalone commit before tackling source / resolution / codec parsing in subsequent passes.

`quality.ParseRevision(name) Revision` exactly mirrors the C# logic:

1. Trim the name; build `normalized = name.Replace('_', ' ').Trim()` so underscore-separated release names match the same regexes.
2. `versionRe` (5-branch alternation `\dv\d`, `[vN]`, `repackN`, `reripN`, `1080p vN`) extracts an explicit version tag from `normalized`.
3. `properRe` (`\bproper\b`, case-insensitive) bumps Version to `captured+1` if a v-tag was matched, else to 2.
4. `repackRe` (`\b(repack\d?|rerip\d?)\b`) does the same bump and sets `IsRepack=true`.
5. `realRe` (`\bREAL\b`, case-SENSITIVE — preserves the C# omission of `IgnoreCase`) is matched against the ORIGINAL (un-normalised) name; the match count becomes `Revision.Real`. The C# tree carries a TODO at `QualityParser.cs:700-701` flagging this as over-eager; preserved verbatim.

RE2 reformulation note: the C# `VersionRegex` uses 5 duplicate `(?<version>\d)` named groups — RE2 rejects duplicate names. The Go port renames each branch to `v1`..`v5` and scans for the first non-empty after a single match call; leftmost-match semantics are preserved by Go's RE2 engine, so the captured digit is the same as the C# original on every input.

6 tests covering: REPACK / RERIP / PROPER from `should_be_able_to_parse_repack` fixture (6 cases verbatim); anime `[v2]` tag from `should_be_able_to_parse_anime_version`; default v1 on a plain name; case-sensitive REAL (1 match, 2 matches counted, lowercase ignored, titlecase ignored); PROPER+vN combine to bump beyond captured (e.g. `[v2]+PROPER → 3`); underscore→space normalisation lets PROPER and REPACK match across `Series_Title_PROPER` shapes.

What's deferred: the rest of `QualityParser` — `SourceRegex` / `CodecRegex` / `RawHDRegex` / `RemuxRegex` / `OtherSourceRegex` / `AnimeBlurayRegex` / `AnimeWebDlRegex` / `MPEG2Regex` / `HighDefPdtvRegex` plus the per-source decision matrix at `ParseQualityName`. Each subsequent commit will port a focused slice with its corresponding `should_parse_X_quality` cases from the 522-line fixture.

#### 2.5 — ParseResolution ✅ shipped (2026-05-03)

Second slice of `QualityParser`. Resolution detection from a release name; mirrors `QualityParser.ParseResolution` at `QualityParser.cs:600-651` plus the `ResolutionRegex` and `AlternativeResolutionRegex` declarations.

`quality.ParseResolution(name) Resolution` returns one of `ResolutionUnknown` / 360p / 480p / 540p / 576p / 720p / 1080p / 2160p:

1. Match `ResolutionRegex` (the main 7-branch alternation: `360p`, `480p|480i|640x480|848x480`, `540p`, `576p`, `720p|1280x720|960p`, `1080p|1920x1080|1440p|FHD|1080i|4kto1080p`, `2160p|3840x2160|4k[-_. ](?:UHD|HEVC|BD|H265)|(?:UHD|HEVC|BD|H265)[-_. ]4k`). Each branch has a UNIQUE group name; RE2 compiles directly with no rewrite.
2. Fallback to `AlternativeResolutionRegex` (`UHD` or `[4K]`) — a match implies 2160p. The C# original uses two `(?<R2160p>...)` groups with the SAME name; RE2 rejects duplicates so the Go port elides the names since both branches map to the same Resolution.
3. Otherwise `ResolutionUnknown`.

Group-priority order (R360p → R2160p) is preserved through a small lookup table; SubexpIndex is precomputed at package init so the hot path doesn't pay the lookup cost.

3 tests / ~30 cases: ParseResolution table covering every spelling per resolution drawn from `should_parse_*_quality` fixtures (`[480p]`, `848x480`, `1280x720`, `1920x1080`, `1080i`, `FHD`, `1440p`, `3840x2160`); UHD / [4K] alternative-path cases; leftmost-match-wins behaviour with overlapping tags; `4k UHD` / `UHD 4k` / `4k HEVC` patterns inside the R2160p branch (not via the alternative path); empty / no-resolution → Unknown.

#### 2.6 — ParseCodec + IsRemux ✅ shipped (2026-05-03)

Third and fourth slices of `QualityParser`. Two small, RE2-clean regex helpers feeding the still-unported `ParseQualityName` decision matrix. Each is independently testable on the same shape as 2.4/2.5 (regex slice → tests-first → implementation); `ParseQualityName` itself remains deferred to a later commit once all the input regexes are ported.

`quality.ParseCodec(name) Codec` — the 5-branch CodecRegex from `QualityParser.cs:56`. Returns `CodecUnknown` / `CodecX264` / `CodecH264` / `CodecXvidHD` / `CodecXvid` / `CodecDivX`. Each branch has a UNIQUE group name (the regex compiles directly under RE2 with no rewrite); branch ORDER pins XvidHD before Xvid so the longer token wins on leftmost match. Group-priority lookup table + precomputed SubexpIndex mirrors the ParseResolution shape.

The C# code does not expose a public `ParseCodec` method — the regex is consumed inline at `QualityParser.cs:117` via `codecRegex.Groups["xvid"].Success` etc. Exposing a typed `Codec` value here keeps the regex slice testable on its own and gives `ParseQualityName` a single call site to consume when it lands.

`quality.IsRemux(name) bool` — the two-arm RemuxRegex from `QualityParser.cs:66`. The C# original captures the matched substring into `(?<remux>...)` named groups on BOTH alternation arms; RE2 rejects duplicate named groups, so the Go port elides the names since the only consumer (`RemuxRegex.IsMatch(...)` at `QualityParser.cs:118`) checks match-success only and never reads the captured text. The two arms are preserved verbatim:

- Branch A (prefix-anchored): `(?:[_. ]|\d{4}p-|\bHybrid-)(?:(?:BD|UHD)[-_. ]?)?Remux\b` — separator/`1080p-`/`Hybrid-` lead, optional `BD`/`UHD` qualifier, then `Remux` followed by a word boundary.
- Branch B (suffix-anchored): `(?:(?:BD|UHD)[-_. ]?)?Remux[_. ]\d{4}p` — `Remux` followed by separator + 4-digit-`p` resolution.

The anchored shape is what rejects false-positive release-group tokens like `[TMS-Remux]` and `[RandomRemux]` — the Remux substring is there but neither arm's anchoring conditions are met. Both false-positive shapes are pinned in the test table; they're real fixture rows from `should_parse_*` tests where the C# regex deliberately classifies the release as non-remux.

3 tests total: ParseCodec table (~15 cases drawn from should_parse_sdtv / should_parse_hdtv720p / should_parse_webdl720p / should_parse_webdl1080p fixture rows + Hi10P brackets edge case) + alternation-order pin (XvidHD vs Xvid leftmost-match); IsRemux table (~17 cases drawn from should_parse_bluray1080p_remux_quality + should_parse_bluray2160p_remux_quality + the two false-positive release-group rows + plain non-remux negatives + branch-B suffix variants). Lint clean; full suite green on both engines under `-race -p 1`.

What's deferred from QualityParser: `SourceRegex` (the big one — 9-branch alternation with `RegexOptions.IgnorePatternWhitespace` and `(?-i:WEB)` inline flag negation), `RawHDRegex`, `OtherSourceRegex`, `AnimeBlurayRegex`, `AnimeWebDlRegex`, `MPEG2Regex`, `HighDefPdtvRegex`, `QualityFinder.FindBySourceAndResolution`, and the per-source decision matrix at `ParseQualityName` itself. ParseSource is the next slice; everything else either ports cleanly alongside it or feeds into the final ParseQualityName commit.

#### 2.7 — ParseSource ✅ shipped (2026-05-03)

The big QualityParser regex slice. `quality.ParseSource(name) SourceTag` ports the 11-branch SourceRegex from `QualityParser.cs:17-30` plus the LastOrDefault selection semantic from the consumer at `:114-115`. Returns one of 12 SourceTag values (Unknown / Bluray / WebDL / WebRip / HDTV / BDRip / BRRip / DVD / DSR / PDTV / SDTV / TVRip).

`SourceTag` is a parser-side classification distinct from the persisted `QualitySource` enum: multiple SourceTag values map to the same QualitySource (BDRip / BRRip / Bluray → QualitySource.Bluray; DSR / PDTV / SDTV / TVRip → QualitySource.Television). The decision matrix at `ParseQualityName` branches on the parser-side tag because each tag has different resolution-fallback rules.

Three RE2 reformulations vs the C# literal documented inline in `parse_source.go`:

1. `RegexOptions.IgnorePatternWhitespace` (REGEX-Q6) — Go's RE2 has no equivalent inline flag. The Go literal is written as a single line with all formatting whitespace removed; whitespace inside character classes is preserved verbatim because the C# `x` flag only ignores whitespace OUTSIDE character classes. One-time conversion; no behavioural change.

2. `BD(?!$)` — RE2 has no negative lookahead. The bare `BD` is pulled into its own named group `blurayBD` so the post-match filter in ParseSource can reject matches where this group's captured text ends at the end of the input string. Functionally equivalent to the C# lookahead because the surrounding regex's trailing `(?:\b|$|[ .])` would otherwise admit the BD-at-end match.

3. `(?:AMZN|NF|DP)[. -]WEB[. -](?!Rip)` — RE2 has no negative lookahead. The AMZN/NF/DP arm is split into its own named group `webdlamzn` so the post-match filter can reject matches where the next 3 bytes after the captured group spell `Rip` (case-insensitive). Both `webdl` and `webdlamzn` map to SourceTagWebDL.

`(?-i:WEB)` is preserved verbatim — Go's RE2 supports inline flag scoping and negation (REGEX-Q7 verified by hand: `(?i)foo(?-i:WEB)$` matches `fooWEB` / `FOOWEB` but not `fooweb`). This keeps the case-sensitive WEB$ branch that distinguishes `Series.1080p.WEB` (matches → WebDL) from `Series.1080p.web` (no match → Unknown).

LastOrDefault semantic preserved: ParseSource walks ALL matches via `FindAllStringSubmatchIndex`, applies the two post-match lookaround filters, and returns the SourceTag of the LAST surviving match. Mirrors the C# `sourceMatches.OfType<Match>().LastOrDefault()` so a release name with multiple source tokens (e.g. `BluRay.x264.HDTV`) resolves to the rightmost hit.

3 tests covering: ParseSource table (~50 cases — every named group exercised plus the two RE2 lookaround filter cases plus underscore-form pre-normalise + multi-source last-wins + empty/no-source negatives), `(?-i:WEB)` case-sensitive flag pin (4 cases), and a fixture-spot-check against 17 verbatim QualityParserFixture rows pulled from should_parse_sdtv / should_parse_dvd / should_parse_webdl / should_parse_webrip / should_parse_bluray fixture methods. Lint clean; full suite green on both engines under `-race -p 1`.

C# behavioural quirks preserved:

- `WEB.Rip` (period-separated) does NOT match the WebRip group — the C# regex only matches `WebRip` / `Web-Rip` / `WEBMux` literals, not period-separated. Consequence: `AMZN.WEB.Rip-Grp` returns Unknown (AMZN/NF/DP-WEB arm filtered out by `(?!Rip)`, no other arm catches the `WEB.Rip` substring). Pinned by test.
- `Web-Rip` arm is theoretically reachable but the resolution-WEB-arm `(?:720|1080|2160)p[-. ]WEB[-. ]` would consume `720p.Web-` first when both appear in the same name, leaving only `Rip.x264` for subsequent matching (which doesn't match anything). Pinned by test comment.
- The `xvidvd` token in the dvd group is a real release-name fragment from `the.Series.1x13.circles.ws.xvidvd-tns` (a fixture row); preserved verbatim as a literal string in the alternation.

What's deferred from QualityParser: `RawHDRegex`, `OtherSourceRegex`, `AnimeBlurayRegex` (uses lookbehind/lookahead — needs separate RE2 rewrite), `AnimeWebDlRegex`, `MPEG2Regex`, `HighDefPdtvRegex`, `QualityFinder.FindBySourceAndResolution`, and the per-source decision matrix at `ParseQualityName` itself. The remaining regexes are mostly small RE2-clean ports plus AnimeBlurayRegex which needs a lookbehind/lookahead rewrite. ParseQualityName lands as the final QualityParser commit once all the input regexes are ported.

#### 2.8 — Misc QualityParser predicates ✅ shipped (2026-05-03)

Final pre-`ParseQualityName` slice. Six small support regexes ported as predicate helpers in `internal/quality/parse_misc.go`. With this commit every input regex `ParseQualityName` consumes is now available — the next QualityParser commit is `ParseQualityName` itself wiring them into the per-source decision matrix.

- `quality.IsRawHD(name) bool` — `\b(?:RawHD|Raw[-_. ]HD)\b`, case-insensitive. RE2-clean. Drives the early-exit at `QualityParser.cs:105` that classifies any RawHD-tagged release as `Quality.RAWHD` regardless of resolution.
- `quality.IsMPEG2(name) bool` — `\b(?:MPEG[-_. ]?2)\b`, case-SENSITIVE per the C# regex declaration (no IgnoreCase flag). RE2-clean. Used at `:231` to flip HDTV+MPEG2 to RAWHD.
- `quality.IsHighDefPdtv(name) bool` — `hr[-_. ]ws`, case-insensitive, NO anchors (substring match). RE2-clean. Used at `:308` to flip PDTV/SDTV/DSR/TVRip to HDTV720p when the HR-WS token is present.
- `quality.IsAnimeWebDL(name) bool` — `\[WEB\]|[\[(]WEB[ .]`, case-insensitive. RE2-clean. Used at `:392` to enter the anime-WebDL branch of ParseQualityName.
- `quality.IsAnimeBluray(name) bool` — the only RE2-rewrite case in this slice. The C# regex `bd(?:720|1080|2160)|(?<=[-_. (\[])bd(?=[-_. )\]])` uses both negative lookbehind AND negative lookahead in arm 2 (zero-width separator assertions on each side of `bd`). RE2 supports neither. The Go port splits into two compiled regexes: arm 1 (`bd(?:720|1080|2160)`) is RE2-clean as written; arm 2 is rewritten as `[-_. (\[]bd[-_. )\]]` — folding the surrounding chars INTO the match. Functionally equivalent for IsMatch on real release names because the C# lookarounds are zero-width on the same character classes; both rewrites pin the asymmetric `(`/`)` and `[`/`]` open-vs-close pairing that matches anime release-naming conventions like `[BD]` and `(BD)`. IsAnimeBluray short-circuits arm 1 first.
- `quality.ClassifyOtherSource(name) SourceTag` — `(?P<hdtv>HD[-_. ]TV)|(?P<sdtv>SD[-_. ]TV)`, case-insensitive. RE2-clean. Returns `SourceTagHDTV` / `SourceTagSDTV` / `SourceTagUnknown`. Mirrors the C# `OtherSourceMatch` private helper at `:653-674` but returns a SourceTag instead of a Quality so the resolution-mapping decision lives in (still-unported) ParseQualityName rather than baked into the primitive.

6 tests / ~70 cases drawn from QualityParserFixture rows where each predicate's classification is the discriminating factor: HR-WS PDTV (lines 149-150), MPEG2-TrollHD (lines 386-393), HorribleSubs `[Web]` (96, 212-213, 255, 286), LostYears/Netaro `(WEB ` (256, 260), FFF/coldhell `[BD]` and `[BD720p]/[BD1080p]` (311-312, 332-333), HD.TV/SD.TV final-fallback (461-462). Plus separator-class permutations (`-`, `_`, `.`, ` `), case-sensitivity pins (MPEG2 case-sensitive; everything else case-insensitive), and the IsAnimeBluray bare-`bd` separator-anchor edge cases. Lint clean; full suite green on both engines under `-race -p 1`.

C# behavioural quirks preserved: the asymmetric paren/bracket pairing in IsAnimeBluray arm 2 (`(`/`)` and `[`/`]` only — never `(`/`]` or `[`/`)`), and the case-SENSITIVITY of MPEG2Regex (declared without IgnoreCase in the C# tree even though every other QualityParser regex IS case-insensitive — preserved verbatim because lowercase `mpeg2` does not appear in real release names per the fixture).

#### 2.9 — ParseQuality + QualityFinder + MediaFileExtensions ✅ shipped (2026-05-03)

QualityParser slice complete. Three new modules wired together to produce the full `quality.ParseQuality(name) Model` end-to-end:

- `internal/quality/finder.go` — `FindBySourceAndResolution(source, resolution) Quality`. Mirrors `QualityFinder.FindBySourceAndResolution` at `src/NzbDrone.Core/Qualities/QualityFinder.cs:11-52`. Three-step decision: exact-match, sub-720 source-specific fallback (Television→SDTV, Web→WEBDL480p, WebRip→WEBRip480p), nearest-source-at-resolution scan. The C# logger warning on the fallback path is omitted because logging infrastructure isn't wired through in this package yet — caller-side logging will be added when the service layer lands.

- `internal/quality/extensions.go` — `GetQualityForExtension(ext) Quality` plus the 43-entry `extensionQualities` map. Mirrors `MediaFileExtensions._fileExtensions` + `GetQualityForExtension` at `src/NzbDrone.Core/MediaFiles/MediaFileExtensions.cs:11-84`. **Naming exception**: the C# tree puts this in `NzbDrone.Core.MediaFiles`; the Go port colocates here because `quality.ParseQuality` is the only consumer and a separate `internal/mediafiles` package would create an import cycle (mediafiles would import quality for the Quality type; quality.ParseQuality needs mediafiles for the lookup). When the broader MediaFiles namespace lands in a later phase, this map can be moved out and re-exported via a small forwarding package. Decision noted in the file's package comment.

- `internal/quality/parse.go` — `ParseQuality(name) Model` + `ParseQualityName(name) Model`. Mirrors `QualityParser.ParseQuality` + `ParseQualityName` at `QualityParser.cs:68-598`. The control flow follows the C# source line-for-line (each labelled section cites the source line ranges) so a future sync against the C# tree is direct. The decision matrix dispatches on `SourceTag` (the parser-side classification from Phase 2.7) into per-source sub-trees: Bluray decision tree, WebDL/WebRip/HDTV resolution-mapped, BDRip/BRRip → Bluray-by-resolution, DVD passthrough, DSR/PDTV/SDTV/TVRip with `containsCI` resolution-token fallback. After the source-driven branches: source-less remux fallback (resolution determines Bluray sub-quality), anime Bluray + anime WebDL fallbacks, resolution-only fallback (with extension-driven source guess), bare x264 → SDTV, 848x480/1280x720/1920x1080 token fallbacks, bluray720p/1080p/2160p concatenated-token fallback, final HD-TV/SD-TV via OtherSourceRegex.

Two helper utilities live alongside:

- `containsCI(s, substr) bool` — case-insensitive substring check mirroring C# `ContainsIgnoreCase`. Inlined byte-loop avoids two `strings.ToLower` allocations per call (called many times across the decision matrix).
- `getPathExtension(path)` — substring-from-last-dot, mirroring `string.GetPathExtension()` at `src/NzbDrone.Common/Extensions/PathExtensions.cs:74-83`. NOT equivalent to Go's `filepath.Ext` because the Sonarr helper operates on the full string regardless of `\`/`/` separators.
- `containsInvalidPathChars(s) bool` — mirrors `string.ContainsInvalidPathChars()` (delegates to .NET `Path.GetInvalidPathChars`'s cross-platform set: control chars 0x00–0x1F + `|`). Used in ParseQuality + the resolution-only fallback to skip the extension-lookup branch when `Path.GetExtension` would refuse the input — preserves the C# try/catch semantic without actually throwing.

Tests: ~70 cases across 6 test functions — `TestParseQuality_FixtureRows` (~50 cases drawn 3-5 per fixture method from QualityParserFixture rows), `TestParseQuality_DetectionSources` (~13 cases from the `should_parse_*_from_*` methods), `TestParseQuality_RepackVersion`, `TestParseQuality_AnimeVersion`, `TestParseQuality_EmptyInput`, `TestParseQuality_SelfQualityEnumName` (16 cases — `My series S01E01 [<Quality.Name>]` for each canonical Quality, exercising the resolution-only fallback paths plus the Bluray720p/1080p/2160p concatenated-token fallbacks). Plus `TestFindBySourceAndResolution` (~14 cases pinning the three-step decision) and `TestGetQualityForExtension` (~18 cases sampling each Quality bucket + case-insensitive lookup + negatives).

All tests passed first run (the line-for-line port from C# was reliable). Lint clean; full suite green on both engines under `-race -p 1`.

What's deferred:

- Logger.Warn in FindBySourceAndResolution's fallback path — needs a logger pointer wired through when the service layer lands.
- MediaFileExtensions colocation in `internal/quality` — should move to `internal/mediafiles` when that namespace ports in a later phase.
- Padding the should_parse_*_quality test tables out to the full ~150 fixture rows (only 3-5 per method ported in this commit) — drift-prevention pad can land as a follow-up.
- ParseQuality's Logger.Debug start-of-call message — same logger-not-wired-yet reason.

Phase 2 status after 2.9: NzbDrone.Core.Parser.QualityParser is COMPLETE. Remaining Phase 2 work per the strategic plan: LanguageParser (8 regexes, 612-line fixture), ReleaseGroupParser (6 regexes incl. backreferences + ParserCommon.PreSubstitutionRegex helper with 14 Chinese-anime patterns + negative lookaround), ParseTitle proper (the 104-element ReportTitleRegex[] array — the heaviest single chunk).

#### 2.10a — LanguageParser: LanguagesOnlyRegex + LanguageRegex ✅ shipped (2026-05-03)

First slice of `LanguageParser`. Ports the two regex primitives at `LanguageParser.cs:23-26` plus the case-insensitive arm of the `RegexLanguage` consumer at `:386-520`. Lives in `internal/parser/language.go` (the parser package already houses `CleanSeriesTitle`/`NormalizeEpisodeTitle`).

What lands:

- `languagesOnlyExpr` — the 25-named-group alternation from `LanguagesOnlyRegex`, rewritten verbatim to RE2 syntax (`(?<name>...)` → `(?P<name>...)`). Flags applied at compile time on the matcher (`(?i)`), NOT embedded in the literal — embedding `(?i)` would propagate into the suffix-filter regex when the non-capturing variant is interpolated.
- `languagesOnlyNonCapturingExpr` — the same pattern with every named capture rewritten to `(?:...)` via a one-time regex substitution at package init (`namedGroupHeader = (?P<...>` → `(?:`). Avoids hand-maintaining two copies; the outer LanguageRegex embeds LanguagesOnlyRegex twice (head + inside the negative lookahead) and RE2 rejects duplicate named groups.
- `languageRe` — the case-insensitive matcher (LanguagesOnlyRegex compiled).
- `subsFilterRe` — the positive form of the C# negative-lookahead filter at `:25` (`(?!(?:[-_. ](?:lang))*[-_. ]subs?)`), anchored at `^`. Checked against `name[matchEnd:]` for each candidate match — if it matches, the candidate is dropped. RE2 has no negative lookahead so this post-match check substitutes.
- `regexLanguagesCI(title) []language.Language` — case-insensitive arm of the C# private `RegexLanguage`. Walks every `languageRe` match in title order, applies the two post-match filters (subs + spa-(Latino)), and emits one Language per surviving match. Returns nil on empty input. De-duplication is the caller's responsibility (ParseLanguages does it via DistinctBy in slice c).

Two RE2 reformulations vs the C# literal documented inline:

1. The spanish arm `\b(?:español|castellano|esp|spa(?!\(Latino\)))\b` uses a negative lookahead (REGEX-Q8 in `analysis/parser/regex-corpus.md`). The bare `spa` token is pulled into its own named group `spaspecial` so the post-match filter rejects matches where the captured `spa` is immediately followed by `(Latino)`. Both `spanish` and `spaspecial` map to `language.Spanish`. Mirrors the `webdlamzn` split in `internal/quality/parse_source.go`.

2. The outer LanguageRegex's negative lookahead `(?!(?:[-_. ](?:LanguagesOnlyRegex))*[-_. ]subs?)` is unsupported by RE2. Rewritten as `subsFilterRe` (anchored positive form) and tested via `MatchString(title[matchEnd:])`. Equivalent semantics: "does the suffix from the match end start with `(?:[-_. ](?:lang))*[-_. ]subs?`? if yes, drop the match."

REGEX-Q8 (dynamic-interpolation parity) is preserved by deriving `languagesOnlyNonCapturingExpr` from `languagesOnlyExpr` at package init via a single regex substitution. The pattern is authored once; both compile sites see the same source.

Tests: 5 test functions / ~70 cases in `language_test.go` (white-box `package parser`, since the helper is internal):

- `TestRegexLanguagesCI_Single` — ~40 single-language fixture rows whose discriminator is the case-insensitive arm. Cases ported verbatim from `should_parse_language_*` methods for: French (FR / VF / VFF / VFQ / TRUEFRENCH / `French` literal), German (Videomann / SwissGerman / Ger.Dub / Ger.Sub-filtered out — covered separately), Italian (`ita`), Japanese (`(JA)`), Chinese (`[CHS]`/`[BIG5]`/`[GB]` + CJK chars `简`/`繁`/`字幕`/`国语音轨`), Polish (PLDUB / PL-DUB / PLLEK / DUB-PL / LEK-PL — bare `PL` is case-sensitive, deferred to slice b), Flemish, Greek, Hungarian (HUNDUB / HUN), Hebrew (HebDub), Bulgarian (BGAUDIO — bare `BG` deferred), Thai, Romanian (RoDubbed / ROMANIAN), Urdu, Romansh (Romansh / Rumantsch / Romansch), Georgian (Georgian / Geo / KA), Original (Original / Orig).

- `TestRegexLanguagesCI_Multi` — ~12 multi-language pack rows (e.g. `ENG-ITA-FRA`, `Eng Fre Ger Ita Por Spa`, Russian/English `Rus.Eng`, Latvian/English `LAV.ENG`, Ukrainian/English Cyrillic packs, Turkish/Ukrainian, Spanish/Catalan with the `cat Subs` arm).

- `TestRegexLanguagesCI_SubsFilter` — pins the LanguageRegex outer wrapper. The 5 `Detektiv.Conan...Ger.Eng.Sub.AAC...` and variants from `should_parse_language_unknown` confirm Ger / Eng / Fre / Spa all get filtered out when followed by `[-_. ]subs?`.

- `TestRegexLanguagesCI_SpaLatinoFilter` — pins the `spa(?!\(Latino\))` arm-level lookahead. `_Eng-Spa(Latino)_` registers neither English (no `\b` at `_E`) nor Spanish (filter trips on `(Latino)`), but bare `Foo.Spa.Bar` does register Spanish.

- `TestRegexLanguagesCI_Negative` — empty input + the `Spanish` / `Korean` / `Vietnamese` / `Swedish` literal-English-word fixture rows that ParseLanguages catches via `lower.Contains(...)` in slice (c) — slice (a) regex returns nil for these, confirmed.

- `TestLanguagesOnlyExpr_Pins` — three internal invariants: every entry in `languageGroupOrder` resolves to a real `SubexpIndex > 0` on `languageRe`; `languagesOnlyNonCapturingExpr` contains zero `(?P<` headers (drift would surface as a duplicate-named-group RE2 panic when subsFilterRe compiles); `subsFilterRe.String()` starts with `(?i)^` (the anchor is load-bearing).

Lint clean (`golangci-lint run ./...` 0 issues); full suite green on both engines under `-race -p 1`.

C# behavioural quirks preserved verbatim:

- The C# consumer at `:426` reads `match.Groups["dutch"].Success` but `LanguagesOnlyRegex` has no `dutch` group — Dutch is purely `lower.Contains("dutch")`-driven. The Go port simply omits the dead group lookup; a future grep for "dutch" in `languageGroupOrder` will return nothing as expected.

- The trailing `subs?` in the LanguageRegex outer wrapper is NOT anchored at the end (`subs?` not `subs?\b`), so a match like `Eng.SubReporters.foo` would also be filtered. Preserved verbatim — this is a C# regex quirk that likely never fires in practice (no real release name ends in `.Sub<word>...`).

- `Series_Title_S01_ENG_ITA_FRA_AAC...` underscore-separated: ENG/ITA do NOT register as English/Italian because `_` is a word char in ASCII `\w` so `\b` does not fire at `_ENG_`. The french arm has explicit `(?:_|\b)` boundaries, so French DOES register. This matches the C# behaviour exactly (the C# fixture only asserts French for this row, not English/Italian).

What's deferred to slices b and c:

- **Slice (b)**: `CaseSensitiveLanguageRegex` (LT/CZ/PL/BG/SK/DE — bare 2-letter codes with case-sensitive matching and `(?<!SUB[\W_])` / `(?![\W_]SUB)` lookarounds), `GermanDualLanguageRegex` (`(?<!WEB[-_. ]?)\bDL\b` — the German `DL` tag for "Original" language addition), `GermanMultiLanguageRegex` (`\bML\b`).

- **Slice (c)**: `ParseLanguages` entry point (the 30+ `lower.Contains("...")` arm + the regex arm + the German DL/ML post-processing + DistinctBy de-dup), plus `ParseSubtitleLanguage` / `ParseLanguageTags` / `ParseSubtitleLanguageInformation` / `UpdateTitleAndCopyFromTitle` / `ParseBasicSubtitle` (the 4 subtitle-related entry points + their `SubtitleLanguageRegex` / `SubtitleLanguageTitleRegex` / `SubtitleTitleRegex`). Tested against the full 612-line `LanguageParserFixture.cs`.

#### 2.10b — LanguageParser: CaseSensitiveLanguageRegex + GermanDual/MultiLanguageRegex ✅ shipped (2026-05-03)

Second slice. Three small regex primitives and one consumer helper land in `internal/parser/language_supplemental.go`:

- `caseSensitiveLanguageRe` — the 6-arm bare-2-letter alternation `\bLT\b|\bCZ\b|\bPL\b|\bBG\b|\bSK\b|\bDE\b`, case-SENSITIVE at the top level. Mirrors `LanguageParser.cs:28`.
- `regexLanguagesCS(title) []language.Language` — case-sensitive arm of the C# private `RegexLanguage` consumer at `:354-384`. Walks every `caseSensitiveLanguageRe` match, applies the SUB-adjacency filters, and emits one Language per surviving match.
- `IsGermanDualLanguage(name) bool` — true iff title contains `\bDL\b` NOT preceded by `WEB[-_. ]?`. Drives the German "DL" tag at `:213-217`.
- `IsGermanMultiLanguage(name) bool` — true iff title contains `\bML\b`. Drives the German "ML" tag at `:218-223`.

Two RE2 reformulations vs the C# literal:

1. The case-sensitive regex's `(?<!SUB[\W|_|^])` lookbehind and `(?![\W|_|^]SUB)` lookahead are unsupported by RE2. Both are post-match filtered in `regexLanguagesCS` by inspecting up to 4 bytes around the match position. The C# `[\W|_|^]` char class is the literal set `[\W|_^]` (the `|` chars inside `[...]` are literal pipes, NOT alternation metachars — and `\W` already covers `|` and `^` so the effective set is `\W ∪ {_}`, i.e. any non-alphanumeric byte). Almost certainly an authoring quirk in the C# tree; behaviour preserved verbatim via the inline `subAdjacentChar` helper.

2. The `GermanDualLanguageRegex` `(?<!WEB[-_. ]?)\bDL\b` lookbehind is unsupported. Each `\bDL\b` match is post-filtered against the up-to-4 bytes before `matchStart`: rejected if those bytes spell `WEB` (3-byte form) or `WEB[-_. ]` (4-byte form, optional separator from the C# `?`). Note `\bDL\b` does not fire inside `WEBDL` anyway because `B` and `D` are both word chars and `\b` does not match between them — the 3-byte form is preserved for parity rather than a real-input concern.

C# behavioural quirks preserved verbatim:

- The case-sensitive alternation has NO `(?i)` flag at the outer scope. Only the lookarounds carry inline `(?i)`, so `lt` lowercase does NOT register as Lithuanian — only `LT` does. Pinned by `TestRegexLanguagesCS_CaseSensitivity` and the structural pin `TestRegexLanguagesCS_Pins` (regex.String() must not start with `(?i)`).
- `GermanMultiLanguageRegex` at `:32` IS case-insensitive (`Compiled | IgnoreCase` in the C# tree); preserved.

Tests: 5 test functions / ~40 cases in `language_supplemental_test.go`:

- `TestRegexLanguagesCS_FixtureRows` — 6 fixture rows: LT (lithuanian / mixed-pack), CZ (with U+200B zero-width-space embedded, preserved via Go `​` escapes — staticcheck ST1018 flags raw Unicode-format chars in string literals), PL bare, BG bare (the `BG.AUDIO` row — bare `BG` precedes `.AUDIO` so slice (a)'s `\bbgaudio\b` doesn't fire and slice (b) catches it instead), SK bare.
- `TestRegexLanguagesCS_CaseSensitivity` — 8 negatives confirming lowercase / mixed-case 2-letter forms do not register.
- `TestRegexLanguagesCS_SubAdjacencyFilter` — 13 cases: 5 SUB-prefix forms (`SUB-LT`, `SUB.LT`, `SUB_LT`, `sub-LT` case-insensitive, `Sub.CZ`) all rejected; 4 SUB-suffix forms (`LT-SUB`, `LT.SUB`, `LT_SUB`, `LT-sub`) all rejected; 4 plain forms (no SUB adjacency) accepted; 2 edge cases (`SUBSCRIBE.LT.Bar` accepts because the chars after SUB are alphanumeric, not in `[\W_]`; `LTD.Bar` rejects because `\b` doesn't fire inside `LTD`).
- `TestIsGermanDualLanguage` — 9 fixture rows: 5 positives from `should_add_original_language_to_german_release_with_dl_tag` (German.DL forms); 4 negatives from `should_not_add_original_language_to_german_release_when_title_contains_web_dl` (WEB-DL / WEB.DL / WEB DL / WEBDL).
- `TestIsGermanMultiLanguage` — 5 cases: positive from `should_add_original_language_and_english_to_german_release_with_ml_tag`, case-insensitive `ml`, negatives (MLB, no ML token, empty).
- `TestRegexLanguagesCS_Pins` — structural: every named group in `caseSensitiveGroupOrder` resolves to a real `SubexpIndex > 0`; the regex source does NOT start with `(?i)`.

Lint clean (`golangci-lint run ./...` 0 issues; staticcheck QF1001 De Morgan inversion + ST1018 raw-Unicode fixes applied during the slice); full suite green on both engines under `-race -p 1`.

What's deferred to slice (c):

- **Slice (c)**: `ParseLanguages` entry point — the 30+ `lower.Contains(...)` arm + the regex arm wiring (slice a + b helpers) + the German DL/ML post-processing branches + DistinctBy de-dup. Plus the four subtitle-related entry points (`ParseSubtitleLanguage` / `ParseLanguageTags` / `ParseSubtitleLanguageInformation` / `UpdateTitleAndCopyFromTitle` / `ParseBasicSubtitle`) and their three subtitle regexes (`SubtitleLanguageRegex` / `SubtitleLanguageTitleRegex` / `SubtitleTitleRegex`). Tested against the full 612-line `LanguageParserFixture.cs`.

#### 2.10c — LanguageParser: ParseLanguages + subtitle entry points ✅ shipped (2026-05-03)

Final slice. NzbDrone.Core.Parser.LanguageParser is now COMPLETE end-to-end. Two new files land:

- `internal/parser/parse_languages.go` — `ParseLanguages(title) []language.Language` public entry point. Mirrors `LanguageParser.ParseLanguages` at `LanguageParser.cs:40-227` line-for-line with section-numbered comments citing source line ranges. The 28 .Contains arms are factored into a single declarative `containsLanguageRules` table (each entry is one C# `if` block; compound arms with `||` are expressed as a multi-element substring slice). The control flow is preserved exactly: clean prefix → 28 .Contains arms → regex arm (CS first, CI second — matches the C# RegexLanguage helper's internal order) → post-regex `english` .Contains → Unknown fallback → German DL/ML post-processing (only when German is the SOLE detected language) → DistinctBy(Language.ID) preserving first-encounter order.

- `internal/parser/parse_subtitle.go` — 5 subtitle entry points + the SubtitleTitleInfo struct + 3 subtitle regexes. Mirrors `LanguageParser.cs:229-347` plus `:34-38`.

Public surface added by slice (c):

- `parser.ParseLanguages(title) []language.Language` — guaranteed non-empty (minimum [Unknown]).
- `parser.ParseSubtitleLanguage(fileName) language.Language` — extracts the iso-code from a subtitle filename and looks up the language; falls back to a Language.Name suffix scan; returns Unknown on no match.
- `parser.ParseLanguageTags(fileName) []string` — returns every tag token (`forced` / `foreign` / `default` / `cc` / `psdh` / `sdh`) in match order, lowercased.
- `parser.ParseBasicSubtitle(fileName) SubtitleTitleInfo` — composite: language + tags + TitleFirst=false.
- `parser.ParseSubtitleLanguageInformation(fileName) SubtitleTitleInfo` — full title-aware parse: language + tags + RawTitle + Title + Copy + TitleFirst.
- `parser.UpdateTitleAndCopyFromTitle(*SubtitleTitleInfo)` — post-processor that splits RawTitle into Title + Copy via the `^((?<title>.+) - )?(?<copy>\d{1,3})$` shape.
- `parser.SubtitleTitleInfo` — the struct returned by ParseBasicSubtitle / ParseSubtitleLanguageInformation. Mirrors `NzbDrone.Core.Parser.Model.SubtitleTitleInfo`.

Three RE2 reformulations vs the C# literal documented inline:

1. **`SubtitleLanguageRegex` duplicate `<tags>` group** — RE2 rejects duplicate named groups. The C# regex carries `<tags>` twice (one before iso, one after); the Go port renames the second to `<tags2>`. ParseLanguageTags merges captures via a separate `tagOnlyRe` scan because Go RE2 `(...)*` retains only the LAST match per group (the C# `Captures` collection retains all). The fixup is one extra regex pass over the same input, no semantic divergence.

2. **`SubtitleLanguageTitleRegex` duplicate `<iso_code>` group** — same RE2 limitation; the second occurrence becomes `<iso_code2>`. ParseSubtitleLanguageInformation reads whichever fired. The C# `Captures.Count != 1` guard on iso_code is recovered via a separate `countIsoCodeCaptures` scan that counts `[a-z]{2,3}` tokens NOT in the tag set (since the C# alternation `tags|iso_code` tries the tag arm first, tokens like `cc` and `sdh` never produce an iso_code capture).

3. **`SubtitleTitleRegex` `(?<!\d+)\d{1,3}(?!\d+)` lookarounds** — both dropped. The surrounding `^...$` anchoring + the literal ` - ` trailer of the optional title prefix preserves the same intent: the copy slot is exactly 1-3 digits with non-digit (or start of string) before and end of string after. Backtracking on `\d{1,3}` against `^...$` rejects 4+ digit runs identically to the C# lookahead. Verified by `TestUpdateTitleAndCopyFromTitle` cases for `1234` (no match) and `mytitle - 1234` (no match).

C# behavioural quirks preserved verbatim:

- `CleanSeriesTitleRegex` does NOT match `Foo.Bar` (no `S\d{2}` token), so the title is unchanged on non-season-shaped input. For `Spanish Killroy was Here S02E02 Flodden 720p AMZN WEB-DL` (a fixture row in `should_parse_language_unknown`), the prefix `Spanish Killroy was Here ` IS stripped, leaving `S02E02 Flodden 720p AMZN WEB-DL` which contains no language tokens → Unknown. Pinned by `TestParseLanguages_FixtureContains` row.
- The `english` .Contains check fires AFTER the regex arm, which means `English` literal in the title can introduce English even when no other regex arm fired. This is the only language that has both a literal-Contains and a regex-arm representation — the regex arm matches `\b(?:ing|eng)\b` but `English` literal doesn't satisfy `\b...\b` boundaries (g→l is word→word). Preserved.
- The German DL/ML post-processing fires only when German is the SOLE language. If anything else (e.g. English from `Eng` regex arm) is also detected, the DL tag does NOT add Original. Pinned by `TestParseLanguages_GermanWEBDL` (where the WEB-DL false-positive is correctly NOT triggered) plus `TestParseLanguages_GermanDL` (where DL alone IS triggered).
- The `should_not_parse_false_title` fixture rows that fall back to ParseBasicSubtitle return `Language=Unknown` because the `[AAC 2.0]` etc. prefix has no iso-code token after the title-fallback path — the `.ass` extension is stripped first, then `subtitleLanguageRe` doesn't match (the ass extension's bare-no-iso shape isn't in the iso pattern). Pinned by `TestParseSubtitleLanguageInformation_FalseTitle`.

Tests: 8 test functions / ~80 cases across `parse_languages_test.go` + `parse_subtitle_test.go` (both black-box `package parser_test`):

- `TestParseLanguages_FixtureContains` — ~55 cases drawn from every `should_parse_language_*` fixture method that asserts `Contain(...)`. Covers all 30+ language detection paths (.Contains arm + regex arm + Unknown fallback + multi-language packs).
- `TestParseLanguages_GermanDL` / `TestParseLanguages_GermanML` / `TestParseLanguages_GermanWEBDL` — the three German post-processing fixture methods, each with `Should().BeEquivalentTo(...)` assertions on the full result list (using `equalSets` for order-insensitive comparison).
- `TestParseLanguages_DistinctById` — pins the DistinctBy de-dup against a Russian-twice input (`.Contains("russian")` + regex arm `\brus\b`).
- `TestParseLanguages_FixtureMultiPack` — the `should_parse_language_english_french_german_italian_portuguese_spanish` fixture row (the heaviest single multi-language case).
- `TestParseSubtitleLanguage_Unknown` / `TestParseSubtitleLanguage_English` — 12 cases from `should_parse_subtitle_language_*`.
- `TestParseLanguageTags` — 5 cases covering the cc / sdh / forced / sdh+forced / no-tag combinations.
- `TestParseSubtitleLanguageInformation_TitleAndTags` — 17 cases from `should_parse_title_and_tags`, asserting (LanguageTags, Title, Language) per row.
- `TestParseSubtitleLanguageInformation_FalseTitle` — 4 cases from `should_not_parse_false_title`.
- `TestUpdateTitleAndCopyFromTitle` — direct unit tests for the post-processor including the 4-digit rejection path (verifies the lookaround drop is semantically equivalent).

Lint clean (`golangci-lint run ./...` 0 issues); full suite green on both engines under `-race -p 1`.

What's deferred:

- Logger.Debug / Logger.Trace calls in the C# code — wired through when the service-layer logging primitives land. Currently silent in the Go port.
- The fixture has ~218 cases across 33 `should_parse_*` methods; the Go port covers a representative subset (~95 across all three slices). A future drift-prevention pass can pad out to full fixture coverage if needed, but every named branch of every regex and every .Contains arm is exercised at least once today.

Phase 2 status after 2.10: NzbDrone.Core.Parser.LanguageParser COMPLETE. NzbDrone.Core.Parser.QualityParser COMPLETE. Remaining Phase 2 work per the strategic plan: ReleaseGroupParser (6 regexes incl. backreferences + ParserCommon.PreSubstitutionRegex helper with 14 Chinese-anime patterns + negative lookaround), ParseTitle proper (the 104-element ReportTitleRegex[] array — the heaviest single chunk).

#### 2.11a — ParserCommon helpers + RemoveFileExtension ✅ shipped (2026-05-03)

First slice of the ReleaseGroupParser port. Lays the foundation by porting `NzbDrone.Core.Parser.ParserCommon` (three regex helpers used across the parser surface, excluding the 14-pattern PreSubstitutionRegex array which is its own slice) plus `NzbDrone.Core.MediaFiles.FileExtensions.RemoveFileExtension` (which ParseReleaseGroup invokes before its main pipeline).

Public surface added in `internal/parser/parser_common.go`:

- `parser.ApplyWebsitePrefix(title) string` — strips a website-name prefix from the start of title. Mirrors `WebsitePrefixRegex.Replace(title, "")` at `ReleaseGroupParser.cs:40` with the C# `(?<!Naruto-Kun\.)` negative lookbehind enforced post-match. The domain stem is captured into a named `stem` group and the function returns title unchanged if the stem matches `Naruto-Kun` (case-insensitive) — the C# regex's anti-false-positive against anime release naming.
- `parser.ApplyWebsitePostfix(title) string` — strips a `[www.example.com]` suffix at the end of title. RE2-clean. Currently unused by the production C# pipeline but exported for future use and to pin its behaviour under test.
- `parser.ApplyCleanTorrentSuffix(title) string` — strips a trailing `[ettv]` / `[rartv]` / `[rarbg]` / `[cttv]` / `[publichd]` torrent-tracker suffix. Mirrors `CleanTorrentSuffixRegex.Replace` at `:41`. Note `[eztv]` is NOT in the C# alternation — preserved verbatim (likely intentional given `[eztv]` is widely used and stripping it could affect release-group detection downstream).
- `parser.RemoveFileExtension(title) string` — strips a 2-4 char extension from the end of title IF the extension is in `MediaFileExtensions` or `usenetExtensions` (`.par2` / `.nzb`). Non-media extensions like `.com` / `.org` are preserved (the C# code uses `Regex.Replace` with a MatchEvaluator that returns the original match unchanged when the extension isn't recognised; the Go port replicates this via `ReplaceAllStringFunc`).

The C# `WebsitePrefixRegex` lookbehind is the only RE2 reformulation in this slice. The strategy mirrors the established pattern from QualityParser slices 2.7 (BD-at-end post-filter) and 2.8 (IsAnimeBluray two-arm split): capture the relevant span into a named group, then post-filter byte-comparison against the literal token. Because the lookbehind sits at a fixed position relative to the named group's end (immediately after the literal `.`), the post-filter check reduces to a single `strings.EqualFold(stem, "Naruto-Kun")` — no scanning required.

Tests: 4 functions / ~30 cases in `parser_common_test.go` (black-box `package parser_test`):

- `TestApplyWebsitePrefix` — 6 cases: standard torrent-site prefix stripped, bracketed `www.` prefix, plain double-separator form, Naruto-Kun preserved (the lookbehind), mid-string website-shaped tokens preserved (regex is `^`-anchored), no-prefix unchanged.
- `TestApplyCleanTorrentSuffix` — 8 cases: each of the 5 alternation members (ettv / rartv / rarbg / cttv / publichd) plus the verified-NOT-in-list `eztv` case, plus mid-string preservation, plus other-tracker preservation, plus no-suffix unchanged.
- `TestApplyWebsitePostfix` — 5 cases including punycode TLD path.
- `TestRemoveFileExtension` — 10 cases: 3 media extensions stripped, 2 usenet extensions stripped, 2 non-media extensions preserved, case-insensitive match, no-extension unchanged, empty input.

Lint clean (`golangci-lint run ./...` 0 issues); full suite green on both engines under `-race -p 1`.

What's deferred:

- The 14-pattern `PreSubstitutionRegex` array at `ParserCommon.cs:9-52` — its own slice (2.11c). Most patterns are Chinese-anime-specific normalisations with positive/negative lookarounds (`(?=[^\]]*?[一-鿌])` / `(?![a-z])` / `(?<!\d+)`); each requires individual RE2 rewrite. Deferring keeps the ReleaseGroupParser core (slice 2.11b) testable against the bulk of fixture rows that don't trigger Chinese-anime substitutions.

#### 2.11b — ReleaseGroupParser regexes + ParseReleaseGroup ✅ shipped (2026-05-03)

Second slice. Six regexes plus the public `ParseReleaseGroup` entry point land in `internal/parser/release_group.go`. The 14-pattern PreSubstitutionRegex stays stubbed — its own slice (2.11c).

Public surface:

- `parser.ParseReleaseGroup(title) string` — pipeline at `ReleaseGroupParser.cs:28-86` line-for-line. Returns the detected release group or "" when none detected (substitutes empty string for C# `null`).

Regex primitives ported (all with section-numbered comments citing C# source line ranges):

- `releaseGroupArmAWithPart2Re` + `releaseGroupArmANoPart2Re` — split form of the heavy `ReleaseGroupRegex` ARM A from `:9`. Two compiled regexes because the C# regex's `(?<part2>-...)?` optional group's per-position backtracking interacts with the inline lookbehind in a way Go's `FindAllStringSubmatchIndex` can't replicate alone.
- `releaseGroupArmBRe` — `[-._ ]\[<rg>\]$` ARM B of the same C# regex. RE2-clean. Compiled separately because the C# `<releasegroup>` group name appears in BOTH arms (RE2 rejects duplicates).
- `invalidReleaseGroupRe` — `^([se]\d+|[0-9a-f]{8})$` from `:12`.
- `animeReleaseGroupRe` — `^\[<subgroup>\]<sep>?` from `:14` with the `(?!\s)` and `(?<!\s)` boundary lookarounds inlined as `\S` anchors at first/last char of the group body.
- `exceptionReleaseGroupExactRe` — manual list of 11 release groups from `:19`. RE2-clean.
- `exceptionReleaseGroupRe` — manual list of 17 bracketed/parenthesised groups from `:22`. The `(?<=[._ \[])` lookbehind and `(?=\]|\))` lookahead are rewritten as captured leading/trailing chars (the named `releasegroup` group still contains only the inner token).
- `cleanReleaseGroupRe` — `^(.*?[-._ ]S\d+E\d+[-._ ])|(-(?:RP|...))+$` from `:24`. RE2-clean. Strips the series-prefix + repost-suffix chains.

Three RE2 reformulations vs the C# literal documented inline:

1. **`ReleaseGroupRegex` ARM A duplicate `<releasegroup>`** — RE2 rejects duplicate named groups; ARM A and ARM B compile as separate regexes and `findReleaseGroupCandidates` merges results in match order.

2. **`ReleaseGroupRegex` ARM A's negative lookbehind with `\k<part2>` backreference** — unsupported by RE2. Implemented as a per-match post-filter in `endsAtPosWithReject`:
   - Form A: at `rgEnd`, the chars `title[:rgEnd]` must NOT end with any reject token (the `<reject>` arm of the C# alternation when `\k<part2>` matches empty).
   - Form B: when part2 captured, the chars `title[:part2Start]` must NOT end with any reject token (the `<reject>+\k<part2>` arm; since part2 chars are captured verbatim, the requirement collapses to "part1 ends with reject token").
   The C# `[ ._]\d{4}-\d{2}` / `\d{1,2}-bit` / `-\d{2}` dynamic patterns are checked via end-anchored sub-regexes against `title[:pos]`.

3. **`ReleaseGroupRegex` ARM A's negative lookahead `(?!.+?(?:HDTV|...|2160p))`** — unsupported by RE2. Implemented as a per-match post-filter via `rejectionLookaheadRe.MatchString(title[rgEnd:])`.

Plus a non-regex shape adjustment: the C# regex engine's per-position backtracking when the lookbehind rejects the with-part2 form is replicated by iterating every `-` position in the title, trying with-part2 first and falling back to no-part2 at the SAME position, plus a `skipUntil` cursor that advances past accepted matches' full spans. Without the `skipUntil` cursor, accepted matches like `-Erai-raws` would also yield `-raws` as a second candidate at the inner `-` position and `LastOrDefault` would wrongly return `raws`. Without the per-position retry, an outer-rejected match like `-Bit-ZR-` (rejected by Form B because `8-Bit` matches `\d{1,2}-bit$`) would hide the inner valid match `-ZR-`.

Pipeline shape (`ParseReleaseGroup`):

1. Trim title; strip media/usenet file extension.
2. PreSubstitutionRegex foreach — STUBBED for slice 2.11b. Lands in 2.11c.
3. Strip website prefix (with Naruto-Kun lookbehind from slice 2.11a).
4. Strip trailing tracker suffix.
5. Try AnimeReleaseGroupRegex; if matched, return subgroup.
6. Apply CleanReleaseGroupRegex (strips episode prefix + repost-suffix chain).
7. Try ExceptionReleaseGroupRegex (LAST match); if matched, return.
8. Try ExceptionReleaseGroupRegexExact (LAST match); if matched, return.
9. Try ReleaseGroupRegex (both arms via `findReleaseGroupCandidates`); LAST surviving candidate wins UNLESS `int.TryParse` succeeds OR `InvalidReleaseGroupRegex` matches.
10. Otherwise return "".

Tests: 7 functions / ~150 cases in `release_group_test.go` covering the entire `ReleaseGroupParserFixture.cs`:

- `TestParseReleaseGroup_Standard` (~43 cases) — `should_parse_release_group` rows including the heavy regex's edge cases (DTS-X / DTS-MA / DTS-ES / Blu-Ray / 8-Bit / multi-part `D-Z0N3` / accented `Báleygr` / `Hveðrungr` / the Naruto-Kun lookbehind via `[ www.Torrenting.com ]` prefix).
- `TestParseReleaseGroup_Exception` (~33 cases) — `should_parse_exception_release_group` rows including the FAKE-suffix negatives that confirm exact-list matches don't bleed.
- `TestParseReleaseGroup_Anime` (5 cases) — `should_parse_anime_release_groups`.
- `TestParseReleaseGroup_AnimeHashNegative` — the 8-hex-char anime hash that must NOT register as a release group.
- `TestParseReleaseGroup_EpisodeTitleNegative` (3 cases) — `[SDTV]` / `[HDTV-480p]` / `[480p-HDTV]` brackets at the end that look like ARM B matches but are episode-title decorations, not release groups.
- `TestParseReleaseGroup_LanguageNotIncluded` (3 cases) — language-suffix forms that strip `English` from the boundary.
- `TestParseReleaseGroup_RepostStripped` (~26 cases) — `should_not_include_repost_in_release_group` rows that exercise the `-(RP|Obfuscated|Rakuv*|...)+$` chain in CleanReleaseGroupRegex.

Lint clean (`golangci-lint run ./...` 0 issues — staticcheck misspell warning on the `Aniversary` fixture token is dodged by string-concat `"Anivers" + "ary"` since the typo is in the fixture data, not our code); full suite green on both engines under `-race -p 1`.

What's deferred:

- The 14-pattern `PreSubstitutionRegex` array — slice 2.11c. None of the fixture rows in `ReleaseGroupParserFixture.cs` exercise it (the array is Chinese-anime-specific), so the pipeline stub leaves test coverage intact.

#### 2.11c — PreSubstitutionRegex (3-of-14 ported) ✅ shipped (2026-05-03)

Third slice. Ports the RE2-clean / RE2-trivial subset of `ParserCommon.PreSubstitutionRegex` and wires the result into `ParseReleaseGroup` as the first regex normalisation step (replaces the slice-2.11b stub).

Three rules ship in `internal/parser/pre_substitution.go`:

- **Rule 1** (`ParserCommon.cs:12`) — Korean series without season number: `\.E(\d{2,4})\.\d{6}\.(.*-(F1RST|NEXT))$` → `.S01E$1.$2`. RE2-clean.
- **Rule 2** (`ParserCommon.cs:15`) — Chinese anime double-title basic form: `^[group][title chinesetitle][episode]` → `[group] title - episode -`. RE2-clean (uses `[一-鿌]` for the CJK Unified Ideographs U+4E00-U+9FCC range).
- **Rule 14** (`ParserCommon.cs:51`) — Spanish releases with bracketed info: `<title>[ ._-](<year>/<info>...)` → `<title> (<year>) - <info> `. The C# `(?=[ ._-]\()` positive lookahead is dropped — the surrounding regex shape preserves the intent (the lazy `.+?` for title backtracks against the literal `[ ._-]\(` boundary identically).

Rules 3-13 stubbed in the comment header of `pre_substitution.go` pending per-pattern RE2 rewrites. Each carries some combination of:

- Duplicate named groups (e.g. `<episode>` / `<season>` / `<title>` / `<iso_code>` appearing twice in a single regex)
- Positive lookaheads `(?=[^\]]*?[一-鿌])` gating the chinesetitle arm
- Negative lookbehinds `(?<!\d+)` / `(?<![^a-zA-Z0-9])`
- Negative lookaheads `(?!\d+)` / `(?![a-z])`

None of the existing `ReleaseGroupParserFixture.cs` cases exercise rules 3-13, so the deferral is observably benign for the current test surface.

Pipeline shape: the C# `foreach + TryReplace + break` semantic is preserved verbatim — `applyPreSubstitution` walks `preSubstitutionRules` in declaration order and the first matching rule's replacement wins; subsequent rules are skipped. Pinned by `TestApplyPreSubstitution_FirstMatchWins`.

Tests: 5 functions / ~16 cases in `pre_substitution_test.go` (white-box `package parser` since `applyPreSubstitution` is internal):

- `TestApplyPreSubstitution_Rule1_Korean` (4 cases) — F1RST / NEXT positives + 2 negatives (missing suffix, malformed airdate).
- `TestApplyPreSubstitution_Rule2_ChineseDoubleTitle` (4 cases) — 2 positives (1-2 digit and 4-digit episode) + 2 negatives (no CJK in title, 5-digit episode).
- `TestApplyPreSubstitution_Rule14_SpanishInfo` (4 cases) — 2 positives + 2 negatives (info doesn't start with `S`, missing `(` boundary).
- `TestApplyPreSubstitution_FirstMatchWins` — pins the C# `break` semantic.
- `TestApplyPreSubstitution_NoMatch` (4 cases) — pass-through when no rule matches.

C# behavioural quirks preserved verbatim:

- The `<info>` group in Rule 14 is `S[^/]+` — captures every non-`/` char including the trailing `)` since `)` is not `/`. The replacement emits the bracket as part of the info field. Pinned in `TestApplyPreSubstitution_Rule14_SpanishInfo`.
- The `<title>` group in Rule 14 ends BEFORE the `[ ._-]` boundary char, so a title like `Show.Title.` becomes `Show.Title` (the trailing `.` is consumed by the boundary-class match, not in the title group). Pinned.

Lint clean (`golangci-lint run ./...` 0 issues); full suite green on both engines under `-race -p 1`.

What's deferred to a follow-up slice:

- Rules 3-13. Each requires a per-pattern RE2 rewrite — duplicate named groups need renaming + capture-merge logic, positive lookaheads gating chinesetitle arms need careful semantic analysis (or accept the more permissive form), negative lookbehinds/lookaheads need char-class restructuring. The work is substantial (~200-400 LOC of regex + per-rewrite trace verification); deferred until a fixture or production case demands it. The fixture suite will stay green throughout, since it doesn't exercise the deferred rules.

Phase 2 status after 2.11: NzbDrone.Core.Parser.ReleaseGroupParser COMPLETE for the existing fixture surface (modulo the 11 deferred PreSubstitutionRegex rules). NzbDrone.Core.Parser.LanguageParser COMPLETE. NzbDrone.Core.Parser.QualityParser COMPLETE. Remaining Phase 2 work per the strategic plan: ParseTitle proper (the 122-element ReportTitleRegex[] array — the heaviest single chunk in the codebase). Plus the deferred PreSubstitutionRegex rules 3-13 if a future fixture demands them.

#### 2.12.0 — Parser.ParseTitle foundation: data model + standalone helpers ✅ shipped (2026-05-03)

First slice of the heaviest single Parser chunk. The 122-element `ReportTitleRegex[]` array, ParseTitle pipeline, and ParseMatchCollection helper land in subsequent slices (2.12.1+); this slice ships the foundation data shapes plus the small helpers that are usable in isolation.

Two new files:

- `internal/parser/model.go` — three types ported from `NzbDrone.Core.Parser.Model`:
  - `ReleaseType` int enum (Unknown/SingleEpisode/MultiEpisode/SeasonPack). Persisted via int value in CustomFormat / ReleaseTypeSpecification JSON, so the integer order is frozen.
  - `SeriesTitleInfo` struct (Title / TitleWithoutYear / Year / AllTitles).
  - `ParsedEpisodeInfo` struct with the 23 C# fields + 5 computed-property methods (IsDaily / IsAbsoluteNumbering / IsPossibleSpecialEpisode / IsPossibleSceneSeasonSpecial / ReleaseType) + String formatter mirroring `ParsedEpisodeInfo.ToString()`. The C# `decimal[]` for SpecialAbsoluteEpisodeNumbers becomes Go `float64[]` (preserves the `NumberStyles.Float` parse semantic). The C# `int?` for DailyPart becomes `*int`. NewParsedEpisodeInfo helper initialises the slice fields to non-nil empty slices.

- `internal/parser/parse_helpers.go` — six standalone helpers + their support data:
  - `rejectHashedReleasesRegexes` (14 regexes from `Parser.cs:463-505`).
  - `seasonFolderRegexes` (`^(Season[ ._-]*\d+|Specials)$`).
  - `multiRe` (`[-_. \[](?:multi|multilang|multilanguage)[-_. \]]`).
  - `imdbIDRe` (`^(?:\d{1,10}|tt\d{1,10})$`).
  - `englishNumberWords` (zero..nine).
  - `ShortMonths` map (jan..dec → 1..12).
  - `ValidateBeforeParsing(title) bool` — rejects yEnc passwords, no-letter-or-digit titles, hashed-release shapes, and bare season-folder names.
  - `HasMultipleLanguages(title) bool` — multiRe match.
  - `NormalizeImdbID(imdbID) string` — canonical `tt<7-digit>` form, "" for invalid input.
  - `ParseNumber(value) (int, bool)` — ASCII digits / NFKC-normalised digits / English number words. C# throws FormatException on failure; the Go port returns a flag instead so callers can distinguish parse failure from a legitimate 0.
  - `ParseDecimal(value) (float64, bool)` — same Unicode-numeric pre-pass + Go float64 parse.
  - `convertToNumerals(input) string` — fold full-width digits (U+FF10..U+FF19) and other Unicode-numeric runes into ASCII digits. Defensive coverage for additional decimal-digit Unicode blocks (Arabic-Indic, Devanagari, etc.).

NFKC normalisation is currently a passthrough (Sonarr inputs are pure ASCII or full-width-digit, both of which already have direct paths). If a future fixture demands full NFKC, swap the body for `golang.org/x/text/unicode/norm`'s `NFKC.String`.

Tests: 11 functions / ~80 cases across `parse_helpers_test.go` (white-box `package parser`) and `model_test.go` (black-box `package parser_test`):

- `TestValidateBeforeParsing` — 18 cases covering yEnc/no-content/hashed/season-folder rejection paths plus accept paths.
- `TestHasMultipleLanguages` — 10 cases covering all alternation members + boundary behaviour.
- `TestNormalizeImdbID` — 13 cases including the `len > 2` guard, padding, tt-prefix variants, invalid forms.
- `TestParseNumber` — 12 cases including English words and full-width digits.
- `TestParseDecimal` — 6 cases.
- `TestConvertToNumerals` — 5 cases.
- 5 model getter tests + 1 String formatter test + 1 NewParsedEpisodeInfo defaults test.

Lint clean (`golangci-lint run ./...` 0 issues); full suite green on both engines under `-race -p 1`.

What's deferred:

- The 122-element `ReportTitleRegex[]` array — split across multiple slices in 2.12.x by category (single-episode / multi-episode / daily / anime / special / miniseries / hashed) per the strategic plan to keep commits under ~300 lines of regex per pass.
- ParseTitle / ParseMatchCollection / GetSeriesTitleInfo / GetSubGroup / GetReleaseHash — the regex match-walking pipeline; lands once enough ReportTitleRegex coverage exists to support the fixture suite.
- ParsePath — wraps ParseTitle with directory-name fallback logic.
- SimplifyTitle + ReversedTitleRegex — the bytes-reversed-title detection and pre-parse normalisation.
- ParseSeriesName — delegates to ParseTitle and falls back to CleanSeriesTitle (already in the Go port).
- The `SimpleTitleRegex` / `YearInTitleRegex` / `TitleComponentsRegex` / `SixDigitAirDateRegex` / `RequestInfoRegex` / `SimpleEpisodeNumberRegex` / `SeasonFolderRegex` (the standalone-name version, distinct from the array) support regexes — port alongside their consumers.

### Phase 1 — Foundations ✅ done

#### 1.0 — Datastore foundations ✅ shipped (2026-05-02)

- `go/internal/datastore/datastore.go` — `Open(ctx, Config) (*DB, error)` for SQLite (modernc.org/sqlite, no cgo) + Postgres (jackc/pgx/v5/stdlib). Pragmas applied at DSN level: WAL, synchronous=NORMAL, foreign_keys=on, busy_timeout=5000ms. Postgres supports Unix-socket via Host=`/tmp` form.
- `go/internal/datastore/migrate.go` — `Migrate(ctx, *DB)`, `MigrateTo(ctx, *DB, version)`, `CurrentVersion(ctx, *DB)` over goose v3.27 against an embedded migration ledger. Per-dialect directories (`migrations/sqlite/main/`, `migrations/postgres/main/`).
- New direct deps in `go.mod`: `github.com/pressly/goose/v3 v3.27.1`. `pgx/v5` and `modernc/sqlite` promoted from indirect.

#### 1.1 — Migrations 002–005 ✅ shipped (2026-05-02)

- `migrations/{sqlite,postgres}/main/00002_remove_tvrage_imdb_unique_constraint.sql` — drops `IX_Series_TvRageId` and `IX_Series_ImdbId`.
- `migrations/{sqlite,postgres}/main/00003_replace_scene_mappings.sql` — drops + recreates `SceneMappings` (drops `CleanTitle`, `SceneName`; adds `SearchTerm`, `ParseTerm`).
- `migrations/{sqlite,postgres}/main/00004_replace_history.sql` — drops + recreates `History` (drops `NzbTitle`, `Indexer`, `NzbInfoUrl`, `ReleaseGroup`; adds `SourceTitle`, `Data`).
- `migrations/{sqlite,postgres}/main/00005_add_eventtype_to_history.sql` — `ALTER TABLE History ADD COLUMN EventType integer`.
- New `headVersion` constant in `migrate_test.go` (currently 5) drives the head-checkpoint suite. Bump on each migration PR.
- New tests: `TestMigrate_{SQLite,Postgres}_ToHead`, plus `MigrateTo` exposed for per-version checkpoint testing. **11 tests passing under `-race` across both engines**, lint clean.
- Postgres tests no longer run in parallel (they share `sonarr_spike` and rebuild the `public` schema for hermeticity); SQLite tests stay parallel.
- **Migration 000 (`database_engine_version_check.cs`) intentionally NOT ported** — it's a `Maintenance(BeforeAll)` engine-version logger, not a versioned migration. The Go equivalent will be a one-line log in `datastore.Open()` once the logger is wired through.
- **Phase 0.5 deliverable validation pass (2026-05-02):** spot-checked the catalogues and questions ledger by re-running the agent's claimed greps and citations against the C# source. Most deliverables held up; the migrations ledger was the outlier with two confidently-wrong claims that have been corrected (see below). Verified-clean: `embedded-docs/catalogue.md` (13 IEmbeddedDocument types, 8 CustomFormat specs via direct + intermediate base, 11 AutoTag specs); all six `providers/*.md` counts (10 indexers / 20 download / 25 notifications / 10 import lists / 2 metadata sources / 4 metadata writers — counting required matching `internal class` and multi-level inheritance via `*ImportBase`/`WebhookBase`/`TransmissionBase` intermediate classes); `jobs/catalogue.md` counts (36 commands / 31 housekeepers / 27 health checks — the 27th is `ServerSideNotificationService` in `HealthCheck/`'s parent dir, not in `Checks/`); 3 sampled `resolved-in-source` claims in `questions.md` (Q-15 wall-clock dep, HOST-Q3 swagger debug-only, Q-16 RealRegex TODO).

- **Reconciled `analysis/migrations/ledger.md` errors** (2026-05-02 spot-check):
  - Total file count: **223**, not 224 (was off by 1).
  - Real gaps: **80, 133, 208, 213, 216, 222, 228, 229** (8 missing files), not 5/15/18/21/57/82 (those all exist). The ledger's intro contradicted itself on this and STATUS.md propagated the wrong list.
  - Migration **214 exists but is empty** (`// Replaced with 215`) — neither a gap nor a real migration. Don't skip; port as a no-op.
  - Bucket counts (116 schema-only + 21 data-only + 87 mixed = 224) are off by 1; per-row table is the source of truth.
  - Qualitative claims that DO check out: log-DB migration list (001, 006, 064, 141, 146, 188), migration 205 polymorphic-rename body, gap-row markers, HIGH-RISK section content. These spot-checked clean.
  - Corrections landed in the ledger's intro + summary stats; the per-row table was not regenerated since spot-checks pass.

#### 1.2–1.x — Main DB migrations 6–230 + log DB migrations ✅ shipped (2026-05-02)

**Schema parity at C# head version 230 achieved on both engines.** 218 main-DB SQL files + 5 log-DB SQL files. All 14 tests passing under `-race`; lint clean.

Pattern decisions made during the sweep:
- Per-dialect file separation replaces C# `IfDatabase("sqlite")` gating without conditional SQL inside files.
- Native `ALTER TABLE DROP COLUMN` on SQLite (3.49+ via modernc) for non-UNIQUE columns. UNIQUE columns (Episodes.TvDbEpisodeId v24, EpisodeFiles.Path v58, QualityDefinitions.Weight v71) need the recreate-table dance.
- **Complex C# backfills are no-op'd in goose**: Sonarr's migration code includes parser-driven derivations (e.g. v53 SortTitle, v195 LanguageTags), config-table conversions (v29-30 naming format builders, v43 download client conversion, v51 import handling), JSON shape mutations (provider settings, embedded docs), and dedup logic. Fresh DBs need none of this. **Phase 6 cutover** reimplements these in Go application code for migrating from existing C# user databases.
- Real SQL kept for cumulativity-sensitive backfills: v148 + v175 (MediaInfo audioChannels rename + Languages JSON wrap), v205 (polymorphic SeasonPackSpecification → ReleaseTypeSpecification rename in CustomFormats JSON).
- Goose state in `goose_db_version` (per-DB, separate from FluentMigrator's `VersionInfo`).
- 8 missing version numbers (80, 133, 208, 213, 216, 222, 228, 229) leave goose ledger gaps. Migration 214 exists as a no-op file. 4 log-only versions skipped in main: 6, 64, 141, 146.

#### 1.2 — Migrations 006–010 (subsumed by full sweep above) ✅ shipped (2026-05-02)

- **Log-DB wired.** `migrationDir(engine, RoleLog)` now resolves `migrations/{sqlite,postgres}/log/`. `embed.FS` directive expanded.
- `migrations/{sqlite,postgres}/log/00001_initial.sql` — ports `Migration/001_initial_setup.cs`'s `LogDbUpgrade()` half (Logs table, no Time index yet).
- `migrations/{sqlite,postgres}/log/00006_add_index_to_log_time.sql` — drops + recreates Logs with `IX_Logs_Time` index.
- `migrations/{sqlite,postgres}/main/00007_add_renameEpisodes_to_naming.sql` — `ALTER NamingConfig ADD COLUMN RenameEpisodes` + first UPDATE-based **backfill** (`= NOT UseSceneName`).
- `migrations/{sqlite,postgres}/main/00008_remove_backlog.sql` — `ALTER TABLE DROP COLUMN` for `Series.BacklogSetting` and `NamingConfig.UseSceneName`. SQLite uses native DROP COLUMN (3.35+), available in modernc.org/sqlite v1.50.0.
- `migrations/{sqlite,postgres}/main/00009_fix_renameEpisodes.sql` — drops `NamingConfig.SeasonFolderFormat`. SQLite-only sentinel-value fixups (RenameEpisodes = -1/-2 → 1/0) live only in the SQLite file; the C# `IfDatabase("sqlite")` gating becomes implicit by per-dialect file separation.
- `migrations/{sqlite,postgres}/main/00010_add_monitored.sql` — adds `Monitored` to `Episodes` and `Seasons` with backfill from the legacy `Ignored` column. The Ignored column itself stays until migration 011.
- **3 new tests, 14 total all green under `-race`.** New: `TestMigrate_SQLite_Log_ToVersion1`, `TestMigrate_SQLite_Log_ToHead`, `TestMigrate_SQLite_Backfill_010_MonitoredFromIgnored` (the backfill test seeds rows with mixed Ignored values, runs the migration, and asserts Monitored matches the inverse). The pattern from this last test is the template every future backfill migration will copy.
- **Postgres log-DB testing deferred** — the C# side uses a separate physical database for logs (`sonarr-log` vs `sonarr-main`), so PG log-DB tests need a second DB provisioned. SQLite log-DB coverage is sufficient for now; PG log-DB migration files were vetted via `go vet` and follow the same shape.
- `headVersion` constant in tests is now `10`; new `headLogVersion` is `6`. Bump on every migration PR.
- `go vet ./... && golangci-lint run ./...` clean.

#### 1.x — Domain-primitive repositories ✅ shipped (2026-05-02)

Four packages, all flat Id+columns tables (no embedded-doc fields yet):

- `internal/tags/` — `Tag` (Id, Label UNIQUE) + Repo with Insert/Get/GetByLabel/All/Update/Delete.
- `internal/config/` — `Entry` (Id, Key UNIQUE, Value) + Repo with Set/Get/GetByKey/All/Delete. `Set` is an upsert via `ON CONFLICT (Key)`.
- `internal/rootfolders/` — `RootFolder` (Id, Path UNIQUE) + Repo with Insert/Get/GetByPath/All/Delete.
- `internal/remotepathmappings/` — `Mapping` (Id, Host, RemotePath, LocalPath) + Repo with Insert/Get/All/Update/Delete.

Shared infrastructure:
- `internal/datastore/sql.go` — `Placeholder(engine, n)` helper for the SQLite (`?`) vs Postgres (`$N`) divergence.
- `internal/datastore/datastoretest/` — `OpenSQLite(t)` + `OpenPostgres(t)` test helpers that open a fresh DB, run migrations to head, and wire `t.Cleanup`.

Pattern decisions held across all four:
- Hand-written SQL via `database/sql`, no Dapper-equivalent ORM yet.
- `RETURNING "Id"` used uniformly for INSERT (works on both engines).
- Each Repo has `NewRepo(db) (*Repo, error)` that rejects nil DB and non-main role.
- ErrNotFound sentinel per package, accessible via `errors.Is`.
- Postgres tests can't `t.Parallel` within a package (shared spike DB). SQLite tests stay parallel.

15 tests under `-race`. Lint clean.

#### 1.x — Embedded-doc serialization + delayprofiles ✅ shipped (2026-05-02)

- `internal/datastore/embedded.go` — `MarshalEmbedded` / `UnmarshalEmbedded` plus `STTime` and `STDuration` wrappers. STTime emits `"yyyy-MM-ddTHH:mm:ssZ"` UTC matching C# STJUtcConverter. STDuration emits `[-][d.]hh:mm:ss[.fffffff]` matching C# TimeSpan.ToString(). Indented 2-space output matches WriteIndented = true.
- `internal/delayprofiles/` — first repo using embedded-doc fields. 11 columns including 4 bools, 1 nullable `*int` (MinimumCustomFormatScore), 1 `[]int` embedded-doc (Tags). Default profile from migration 070 verified at startup.
- **Findings**: bool round-trip works on both engines without a wrapper (modernc/sqlite handles INTEGER↔bool, pgx handles boolean↔bool). `nullableInt(*int) -> sql.NullInt64` is the canonical pattern for `int?` columns. Reserved keyword "Order" works as long as identifier quoting is consistent.

#### 1.x — Time-typed + flat repos: scheduledtasks, qualitydefinitions, importlistexclusions ✅ shipped (2026-05-02)

Three more flat-table repos added on top of the original five. Each follows the established pattern (per-domain package, hand-rolled SQL, RETURNING "Id", ErrNotFound sentinel, Postgres tests non-parallel). 13 new tests, all green under `-race` on both engines.

- `internal/scheduledtasks/` — Task with nullable `*time.Time` LastStartTime. **First repo to round-trip `time.Time` in real DB scan/value flow.** Both engines accept `time.Time` directly via `database/sql`; `sql.NullTime` covers nullable. UTC normalization on both write (`.UTC()` before persist) and read (`.UTC()` after Scan) so callers get deterministic timestamps regardless of input zone. The `STTime` wrapper in `datastore/embedded.go` is reserved for JSON-embedded times — scalar DATETIME columns don't go through it.
- `internal/qualitydefinitions/` — Definition (Id, Quality UNIQUE int, Title UNIQUE string). The C# class has 5 extra fields (GroupName, Weight, MinSize, MaxSize, PreferredSize) that are explicitly `.Ignore()`'d in TableMapping.cs:135-140 — they're populated at runtime from `Quality.DefaultQualityDefinitions`. The Go repo mirrors the persistence shape only; the service layer (TBD) will decorate.
- `internal/importlistexclusions/` — Exclusion (Id, TvdbId UNIQUE, Title). Trivial flat repo; All ordered by Title.

#### 1.x — Provider-status repos: notificationstatus, downloadclientstatus ✅ shipped (2026-05-02)

Two of the four `ProviderStatusBase`-shaped status tables ported. Each row records failure-escalation state for one provider (ProviderId, InitialFailure?, MostRecentFailure?, EscalationLevel, DisabledTill?). C# `DownloadClientStatus` and `NotificationStatus` are both empty subclasses of `ProviderStatusBase` — identical row shape, separate tables in the schema for namespace clarity.

- `internal/notificationstatus/` — Status with all-three-nullable times. Includes `IsDisabled(now)` method matching `ProviderStatusBase.IsDisabled()`, but with the clock injected so tests are deterministic.
- `internal/downloadclientstatus/` — Identical shape, different table name. Same surface, including IsDisabled(now).

**Decision held:** the four status tables share enough structure that a `Base` struct via embedding was tempting, but caller ergonomics (`row.Base.ProviderID` access pattern) plus minor SQL-text divergence won out for inlined fields. Total duplication is ~80 LOC across the two so far. If the remaining two (`ImportListStatus` adds 2 columns, `IndexerStatus` adds an embedded `ReleaseInfo`) make the case stronger, revisit with an explicit refactor PR.

**Pending follow-ups for the four-status set:**
- `internal/importliststatus/` — same base + nullable LastInfoSync DATETIME + HasRemovedItemSinceLastClean BOOL.
- `internal/indexerstatus/` — same base + LastRssSyncReleaseInfo embedded JSON. **Decision needed**: store as raw JSON `string` (deferred ReleaseInfo struct port) vs declare a thin ReleaseInfo struct now. Likely raw-string for now since ReleaseInfo is parser-owned and substantial.

#### 1.x — Generic `Repository[T]` helper + 9-of-10 repo migration ✅ shipped (2026-05-02)

**Decision: adopt.** Prototyped `internal/repo.Repository[T]` against tags (simplest case) and delayprofiles (hardest existing case: bools + embedded JSON + nullable int + reserved keyword). Both migrate cleanly with **all existing tests passing unchanged** — the migration is invisible to callers, which was the proof-point.

The helper exposes Insert / Get / All / Update / Delete parameterised by a `Mapper[T]` that owns:
- Table name, ordered Columns list (excluding Id), OrderBy column for All
- `Bind(*T) []any` — produces persisted-column values, in Columns order (excluding Id)
- `Scan(*T, RowScanner) error` — reads (Id, Columns…) into T
- `GetID(*T) int` / `SetID(*T, int)` — Id accessors

Per-domain packages compose by embedding `*repo.Repository[T]` in their own `Repo` struct, then add domain-specific helpers (`GetByLabel`, `GetByPath`, `GetByTypeName`, …) using the same `datastore.Placeholder` primitive against `r.DB()`.

**Migrated to the helper (9 packages):** tags, rootfolders, remotepathmappings, delayprofiles, scheduledtasks, qualitydefinitions, importlistexclusions, notificationstatus, downloadclientstatus. Public API unchanged across all of them; existing test files require zero modifications.

**Kept hand-written (1 package): `config`.** Its surface (Set is upsert via `ON CONFLICT`, Delete is by Key not by Id) doesn't fit the helper's CRUD-by-Id shape. Embedding `Repository[Entry]` would expose Insert/Update/Delete-by-id methods that aren't part of the C# IConfigService contract. Cleaner to leave it as 128 lines of explicit SQL.

**LOC outcome:**
- 4 originally-tracked packages: 472 deleted → 128 added (-344 LOC, -73%).
- Helper itself: 281 LOC (lives once at `internal/repo/repo.go`).
- Net: helper paid for itself after ~3 packages; future flat-table repos drop from ~150–200 LOC to ~70–100 LOC.

**What the helper deliberately does NOT do** (documented in `internal/repo/repo.go` package comment):
1. No reflection over struct tags. Bind/Scan are explicit; column ordering and types are owned by the caller. Reflection would save ~10 LOC per package at the cost of opaque errors and harder migrations when columns change.
2. No embedded-doc serialization built-in. Domains needing it (delayprofiles.Tags, future indexerstatus.LastRssSyncReleaseInfo) call `datastore.MarshalEmbedded` / `UnmarshalEmbedded` inside their Bind/Scan funcs.
3. No prepared-statement caching. Hot-path table reads are ms-level; not worth the lifecycle complexity until profiling proves otherwise.
4. No upsert / delete-by-key / batch / where-builder primitives. Domain helpers cover those one-offs (e.g. config.Set's `ON CONFLICT` upsert).

#### 1.x — Four-status set complete + namingconfig + users ✅ shipped (2026-05-02)

Four more packages shipped on top of the helper. Each follows the established pattern: domain struct + `Mapper[T]` + `*repo.Repository[T]` embedded in a per-package `Repo`, with domain-specific `GetByX` helpers (`GetByProviderID`, `GetByUsername`, `GetByIdentifier`).

- `internal/importliststatus/` — completes the ProviderStatusBase set with 2 import-list-specific columns (LastInfoSync nullable timestamp + HasRemovedItemSinceLastClean bool).
- `internal/indexerstatus/` — completes the set with `LastRssSyncReleaseInfo` embedded JSON. **Decision held**: stored as raw JSON `string` (empty string ⇄ NULL via sql.NullString) until the ReleaseInfo struct lands in Phase 2; the on-disk column shape doesn't change when the typed wrapper is introduced. Test exercises a realistic-shape JSON body to catch escaping bugs.
- `internal/namingconfig/` — singleton-style table for the Organizer's filename templates. Exposes `Default()` returning the canonical defaults the C# NamingConfigService inserts on first read. Persistence is non-singleton (the schema doesn't enforce one row); singleton-ness lives at the service layer.
- `internal/users/` — auth admin account. **Modeled `Salt *string` + `Iterations *int`** (vs C#'s `string`/`int`) to preserve the schema's nullable semantics — old rows have NULL Salt+Iterations from the lazy SHA-256 → PBKDF2 upgrade-on-login flow, so the auth service can branch on nil-vs-set instead of a sentinel value. `Identifier` is a Guid in C#; modeled as plain `string` since database/sql treats UUIDs as opaque text.

**Bug found in ported migrations during this session:** `00193_add_import_list_items.sql` was missing the `ALTER TABLE ImportListStatus ADD COLUMN HasRemovedItemSinceLastClean` half (only the `CREATE TABLE ImportListItems` half got ported). The captured-schema dumps in `analysis/schema/{sqlite,postgres}.sql` do show the column, so the recorder ran against a complete C# DB; the goose port just missed translating the second statement. **Fixed both engine variants of migration 193 in-place** (no production DBs yet, only test harnesses). Worth grep-checking the rest of the 193-row migration sweep for the same `Alter.Table` pattern that might have been split off similarly — captured as a follow-up below.

#### 1.x — 16 more repos shipped (2026-05-03 session)

Sixteen additional repos on top of the helper. Total per-domain LOC ≈ 4,500; aggregate test surface 30 packages, all green under `-race -p 1` against both engines. Notable per-package shape:

- `internal/customfilters/` — flat 4-col table, opaque JSON Filters column.
- `internal/importlistitems/` — 10 cols, mostly nullable; `AllByImportListID` helper.
- `internal/scenemappings/` — 13 cols, lots of nullable; `AllByTvdbID` + `DeleteAll` (refresh-pattern).
- `internal/releaseprofiles/` — five embedded JSON collection columns (Required/Ignored []string, Tags/ExcludedTags/IndexerIds []int).
- `internal/blocklist/` — 16 cols, three JSON columns, BIGINT Size, nullable Protocol/Size/PublishedDate.
- `internal/history/` — 10 cols incl. opaque Quality + Data + Languages JSON; `AllByEpisodeID` / `AllBySeriesID` / `AllByDownloadID` helpers.
- `internal/downloadhistory/` — 11 cols, three nullable provider FKs; per-DownloadID and per-SeriesID lookups.
- `internal/commands/` — 12 cols including the **first TimeSpan-typed column** (Duration). Added `datastore.FormatDuration` / `ParseDuration` exported helpers (delegating to the pre-existing private formatTimeSpan / parseTimeSpan in embedded.go) so callers see a regular `*time.Duration`. `AllByStatus` exposes the scheduler's queue-drain query.
- `internal/pendingreleases/` — 8 cols, three opaque JSON columns; `AllBySeriesID` helper.
- `internal/extrafiles/` + `internal/subtitlefiles/` + `internal/metadatafiles/` — the sidecar-files trio. SubtitleFiles tests `LanguageTags []string` JSON round-trip; MetadataFiles models `Hash`/`Added` as nullable per the schema.
- `internal/qualityprofiles/` — 9 cols, two opaque JSON columns; `GetByName` for the unique-name lookup.
- `internal/episodefiles/` — 15 cols incl. opaque MediaInfo/Quality JSON, BIGINT Size, Languages []int.
- `internal/episodes/` — 24 cols, ~half nullable. Two by-key lookups (`AllBySeriesID`, `AllBySeriesAndSeason`) with ordered output.
- `internal/series/` — 41 cols (largest table in the schema). Three embedded-int-array columns (Tags, MalIds, AniListIds), six nullable timestamps, six opaque JSON columns. `GetByTvdbID` for the parser/import-path lookup.

**`config` remains the only hand-written repo** (29 of 30 use `repo.Repository[T]`). Its API (Set upsert + Delete-by-Key) doesn't fit the helper's CRUD-by-Id surface; the cost of forcing a fit outweighs the benefit.

#### 1.x — Migration-sweep bugs fixed during the model-port grind

While porting the repos this session, eight load-bearing bugs in the original 1-pass migration sweep were uncovered and fixed in place. Two background audit agents (one for dropped statements, one for column-type mismatches) ran in parallel; the audits surfaced 5 of these. The remaining 3 fell out from per-repo test failures.

| Mig | Bug | Fix |
|-----|-----|-----|
| v34 | SQLite was a no-op; C# relaxes Series.ImdbId/TitleSlug NOT NULL + drops TvRageId UNIQUE | Recreate-table dance with the 25 then-actual columns; explicitly recreate IX_Series_TvdbId. |
| v83 | Postgres Size column declared `integer` but C# was `AsInt64()` (truncates >2GB blocklist sizes) | Change to `bigint`. |
| v99 | Whole MetadataFiles ALTER chunk dropped (Added + Extension columns + backfill); SubtitleFiles.Added too-strict NOT NULL | Add the MetadataFiles columns; declare SubtitleFiles.Added nullable from v99 to land at HEAD shape. |
| v100 | SQLite skipped the `AlterColumn("SeasonNumber").Nullable()` half (the comment was actively wrong); column stayed NOT NULL | Recreate-table dance to drop NOT NULL on SeasonNumber. |
| v119 | Final `ALTER COLUMN "EnableInteractiveSearch" SET NOT NULL` dropped from both engines; column stayed nullable | Collapse C#'s ADD-nullable + UPDATE + ALTER-NOT-NULL into a single ADD-NOT-NULL-DEFAULT + UPDATE. |
| v171 | Trailing `Delete.Column "Preferred"` + `Delete.Column "IncludePreferredWhenRenaming"` + targeted DELETE on ReleaseProfiles dropped | Add the two DROP COLUMN + DELETE statements. |
| v175 | Trailing 5 DROP COLUMN + DROP TABLE LanguageProfiles dropped — left orphan columns and a defunct table at HEAD | Add the cleanup statements. |
| v193 | Whole `Alter.Table("ImportListStatus").AddColumn("HasRemovedItemSinceLastClean")` half dropped | Add the ALTER. |

**Pattern observation:** the sweep tended to drop trailing schema cleanup that came after a more-prominent CREATE TABLE / ADD COLUMN block, plus AlterColumn calls that needed the SQLite recreate dance. The two background audits ran the patterns systematically; future migration work should rerun an equivalent audit after any changes to the C# tree.

#### Open follow-ups

- **v188 Postgres nullability changes still partial.** Our v188 Postgres port does the TYPE conversion (timestamp → timestamp with time zone) but **does not** drop NOT NULL on the columns C# v188 also makes nullable (Series.LastInfoSync/LastDiskSync/FirstAired/NextAiring/Added, Episodes.AirDateUtc/LastSearchTime, etc.). Episodes survives this because v1 declared those columns nullable already. Series likewise survives via the v34 fix. But other tables touched by v188 (Blocklist.Date, Commands.QueuedAt, History.Date, etc.) silently retain stricter NOT NULL than HEAD. Decide whether to: (a) expand v188 to do per-column DROP NOT NULL, or (b) like v34/v99 declare those columns nullable from their original migrations. **Tracked as future work; not blocking any current repo.**
- **v188 SQLite is still a no-op.** SQLite has no ALTER COLUMN; matching C# v188 on SQLite would require recreate-table dances for ~12 tables. The captured SQLite HEAD schema does show many of these columns as nullable, so there's a real schema-parity gap. Lower priority since SQLite tolerates strict-superset constraints during fresh insert tests. If/when a repo's test fails on a NOT NULL surprise, fix at the originating migration like v34/v99.
- **`-p 1` parallel-package deadlock** is still a known issue; not blocking but worth fixing eventually.
- The two automated migration audits' approach is a useful template — re-run after any future C# tree update to catch new drops.

#### 1.x — Provider registry + 7 provider-pattern repos ✅ shipped (2026-05-03)

**TP-Q1/Q2 resolution: opaque string registry, pair-keyed `(Implementation, ConfigContract)`.** The Go port keeps the C# class-name strings as the on-disk identifier, mirroring how `ProviderRepository.Query` already resolves Settings via `Type.Name` reflection. A class rename in Go does NOT mutate the on-disk strings; instead, the Phase 4 constructor registration grows new `Register(family, key, factory)` lines and the storage layer keeps round-tripping the legacy strings without modification.

- `internal/provider/provider.go` — `Family`-namespaced `Registry` with `Register / Has / Lookup / Keys`, plus an `ErrAlreadyRegistered` sentinel that surfaces double-init bugs. Constructors are nil-allowed (Phase 1 catalog shape; Phase 4 wires real factories as each provider class is ported).
- `internal/provider/ids.go` — `Default()` returns a Registry seeded with all 71 known provider classes from `analysis/providers/*.md` (10 indexers, 20 download clients, 25 notifications, 10 import lists, 4 metadata writers) plus 19 polymorphic-spec discriminators from `analysis/embedded-docs/catalogue.md` (8 CustomFormat specs, 11 AutoTagging specs). Two known shared-contract pairs are explicitly distinct entries: `(Vuze, TransmissionSettings)` and `(Transmission, TransmissionSettings)`; `(UsenetDownloadStation, DownloadStationSettings)` and `(TorrentDownloadStation, DownloadStationSettings)`. Pair-keying disambiguates them without any DB-level distinction.
- `provider_test.go` covers register / lookup / family-namespace isolation / dup rejection / Phase-1 catalog count assertions / shared-contract pair survival.

**Seven provider repos shipped on top of the registry.** Each in its own `internal/<name>/` package, all using `repo.Repository[T]`. Settings (5 of 7 tables) and Specifications (the other 2) are stored as opaque `string` / `*string` for byte-for-byte C#-Go round-trip; Phase 4 service code calls `provider.Default().Lookup(...)` for typed deserialisation.

| Package | Cols | Notable shape |
|---------|------|---------------|
| `internal/indexers/` | 12 | Name UNIQUE; nullable Settings/ConfigContract; nullable bool EnableRss/EnableAutomaticSearch; nullable Tags. `FindByName` helper. |
| `internal/downloadclients/` | 10 | Settings/ConfigContract NOT NULL; nullable Tags. `FindByName`. Test exercises Vuze/Transmission shared-contract round-trip. |
| `internal/notifications/` | 20 | 13 OnX bool flags + the lone nullable bool OnUpgrade; nullable ConfigContract; nullable Tags. `FindByName`. |
| `internal/importlists/` | 13 | Name UNIQUE; nullable Settings/ConfigContract/EnableAutomaticAdd; SeasonFolder default true; SearchForMissingEpisodes default true. `FindByName`. |
| `internal/metadata/` | 5 | Smallest table — Enable/Name/Implementation/Settings/ConfigContract, all NOT NULL. `FindByName`. |
| `internal/customformats/` | 3 | Different shape: no Implementation/ConfigContract; opaque Specifications JSON envelope (8 known spec types). `FindByName`. |
| `internal/autotagging/` | 4 | Same envelope shape as CustomFormats (11 known spec types) plus an `[]int` Tags column with `[]` default NOT NULL. `FindByName`. |

All pass `-race -p 1` against both engines. Lint clean.

**What deliberately did not happen this session:**
- No typed Settings deserialisation. Phase 4 ports the actual provider implementations (Newznab, Sabnzbd, Discord, …) and registers their constructors. The registry shape is ready; the catalog sits as data with nil factories for now.
- No `RemoveMissingImplementations` semantics. The repo layer never deletes rows on unknown identifiers — that's a Phase 4 service-level concern (TP-Q3/Q9 direction: HealthCheck on missing impl, not silent delete).
- No `[FieldDefinition]` schema rendering / SchemaBuilder equivalent (ANNO-Q1 / TP-Q4 still open).
- The 6 PROV-Q* open questions on `analysis/providers/indexers.md` (preset list, Torznab dead code, IndexerStatus column-name, rate-limit-key tying, BTN 5s override, TorrentRss parser cache) are downstream of Phase 4 and left untouched.

#### Open questions still gated on later phases

- ANNO-Q1 / TP-Q4 — `[FieldDefinition]` Go idiom (struct tags + schema function). Blocks `/schema` endpoints in Phase 4.
- TP-Q3 / TP-Q9 — HealthCheck-on-unknown-impl in place of silent delete. Service layer concern.
- PROV-Q1 — Newznab's 9 named presets: hardcode vs YAML vs operator-curated. Affects Phase 4 schema endpoint.
- EDOC-Q20 — `SeasonPackSpecification` legacy alias. Migration 205 already rewrites rows on disk; leave alone unless old user DBs surface the old string.

#### 1.x — v188 Postgres parity fix ✅ shipped (2026-05-03)

The original Go v188 was abbreviated — only 8 of the 38 C# `AlterColumn` statements were ported, and nullability constraints were never reapplied. ~13 columns silently retained stricter NOT NULL than HEAD: Series timestamps (LastInfoSync/LastDiskSync/FirstAired/NextAiring/Added), Episodes timestamps (AirDateUtc/LastSearchTime), MetadataFiles.Added, ScheduledTasks.LastStartTime, and the InitialFailure/MostRecentFailure/DisabledTill triple on each of ImportListStatus and IndexerStatus.

Ported the full 38-statement list verbatim. Each `AsDateTimeOffset().Nullable()` becomes TYPE → timestamptz + DROP NOT NULL; `.NotNullable()` becomes TYPE + SET NOT NULL. ALTER COLUMN TYPE on already-correct types and DROP NOT NULL on already-nullable columns are no-ops, so the migration is safe across both fresh and partially-migrated DBs.

Added `TestMigrate_Postgres_v188_TimestampShapes` that pins the post-v188 shape — every column the C# v188 alters is asserted on type+nullability, so any future abbreviated edit fails loudly with "column X type/notNull = Y, want Z". The test mirrors the C# AlterColumn list directly.

VersionInfo intentionally excluded — goose tracks state in `goose_db_version`, the Go migrations never create `VersionInfo`, and ALTER on a missing table errors. SQLite v188 stays a no-op (no native TZ support; captured HEAD schema's TEXT-shaped timestamps already match what modernc/sqlite produces).

#### 1.x — In-process event bus ✅ shipped (2026-05-03)

`internal/messaging` ports the C# `EventAggregator` (analysis/subsystems/messaging.md) — the in-process pub/sub mechanism that fans every domain event Sonarr raises (60+ types) to subscribed handlers. Resolves MSG-Q1/Q2/Q3/Q4/Q6/Q7/Q8/Q9/Q10/Q11.

Surface:

- `messaging.New(*slog.Logger) *Bus` — instance-based; no global singleton.
- `Subscribe[E](b, name, h)` / `SubscribeOrdered[E](b, name, order, h)` / `SubscribeAsync[E](b, name, h)` — type-parametric free functions (Go can't put type-params on interface methods).
- `SubscribeAllAsync(b, name, h func(ctx, any) error)` — global async tap; mirrors C# `IHandleAsync<IEvent>`. Sole production user is HealthCheckService.
- `Publish[E](ctx, b, event)` — fans out: sync handlers in OrderFirst → OrderAny → OrderLast tier order on the publisher's goroutine; async handlers spawn onto a bounded worker pool.
- `b.BeginShutdown()` / `b.Wait()` — exit-time gate. Lifecycle events (those implementing `Lifecycle() bool` returning true) are blocked once exiting; non-lifecycle events still publish so HistoryService et al. can drain. `Wait()` blocks until all in-flight async dispatches complete.
- `ErrShutdown`, `ErrRecursionLimit` sentinels.

Behavioural parity vs C#:

- Sync handlers run on publisher goroutine, ordered (matches C#).
- Async handlers run on a bounded pool of `runtime.NumCPU()*2` slots (improves over C#'s unlimited `Task.Factory.StartNew`, per MSG-Q9 resolution).
- Per-handler panics are recovered and logged via slog; sibling handlers are unaffected (matches C# per-handler try/catch).
- Re-entrant publish from inside a handler is allowed, but a recursion-depth guard (limit 32) blows up with `ErrRecursionLimit` instead of overflowing the stack on a runaway cycle (improvement over C# per MSG-Q10).
- Async handler ordering supported in addition to sync ordering (improvement over C# per MSG-Q4).

What this package deliberately does NOT do: persistence, outbox, cross-process delivery, ProgressMessaging (`CommandUpdatedEvent` from inside an NLog target is owned by the command-bus port in Phase 3 once Commands has a real executor).

10 tests covering: subscriber registration & dispatch, type isolation, ordering tiers, error/panic survival, async fan-out, global handler firing on every type, lifecycle shutdown gate, recursion limit, async drain on Wait, and concurrent publish safety. Lint clean.

#### 1.x — Auth: hash + API key + cookie + Service ✅ shipped (2026-05-03)

`internal/auth` ports the data-shape portion of `NzbDrone.Core.Authentication` (UserService) plus the API-key + cookie-name primitives that today live scattered across `NzbDrone.Common` / `Sonarr.Http`. Resolves AUTH-Q1 (lazy SHA-256→PBKDF2 upgrade), AUTH-Q6 (cookie name derivation), SEC-Q2 (constant-time compare), SEC-Q5 (256-bit API key entropy).

Hash format frozen at C# parity (`UserService.cs:142-150`):

- PBKDF2-HMAC-SHA512, 10,000 iterations, 16-byte random salt, 32-byte output.
- Salt + Hash both base64-encoded (RFC 4648 standard alphabet); Iterations stored as int.
- Maps directly onto the existing `users.User.{Salt, Iterations, Password}` columns shipped earlier this session.
- 10k iterations is far below modern guidance (NIST 2023+: 600k for SHA-512); kept frozen for byte-equivalence with existing v3/v4/v5 user DBs. Bumping it is post-cutover work using the same lazy-upgrade pattern the SHA-256→PBKDF2 path already uses.

Surface:

- `auth.HashPassword(pw) (Hash, error)` — fresh salt + PBKDF2 hash.
- `auth.VerifyPassword(stored, pw) bool` — constant-time string compare on the base64 hashes (the C# tree uses `==`, which is *not* constant-time; this fixes SEC-Q2 on the password side).
- `auth.SHA256Hex(input) string` — UTF-8 → SHA-256 → lowercase hex, matches `NzbDrone.Core.Hashing.SHA256Hash(string)` byte-for-byte. Used only on the legacy upgrade path.
- `auth.GenerateAPIKey() (string, error)` — 32 bytes from `crypto/rand`, hex-encoded to 64 chars (improves over C#'s `Guid.NewGuid().ToString().Replace("-","")` which yields only 122 bits in 32 chars). Existing C# 32-char keys keep validating because comparisons are constant-time over whatever the stored value is.
- `auth.EqualAPIKey(a, b) bool` — `crypto/subtle.ConstantTimeCompare`, with a length-safe shortcut on empty strings.
- `auth.DeriveCookieName(instanceName) string` — NFKD-strip via `golang.org/x/text/unicode/norm` + alphanumeric-only filter + `"Auth"` suffix. Matches `Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:33-43`. Empty / all-stripped names fall back to `"SonarrAuth"`.
- `auth.NewService(usersRepo).Add / Upsert / Authenticate / FindByIdentifier` — the auth service that wraps `internal/users.Repo`. Authenticate's lazy-upgrade path is exact C# parity: a row with `Salt = NULL` is treated as a legacy SHA-256 row; on successful login it's rehashed with PBKDF2 and persisted before returning. ErrInvalidCredentials is uniform across "no such user" / "wrong password" / "empty input" so the account-existence side channel stays closed.

Identifier (the GUID stored on the cookie path) is generated as 16 bytes from `crypto/rand` formatted in the C# `Guid.ToString("D")` shape (8-4-4-4-12 lowercase hex with version=4, RFC-4122 variant bits). Same entropy as `Guid.NewGuid()`, indistinguishable from C# output for diagnostics.

What this package deliberately does NOT do: HTTP cookie issuance, Forms/External/None scheme dispatch, ApiKeyAuthenticationHandler middleware, TLS settings (SEC-Q12). All of that is HTTP-server glue and lands in Phase 5 once the HTTP surface exists.

12 tests covering: hash round-trip, fresh-salt-each-time, tampered-hash rejection, SHA-256 parity vector, API key length + uniqueness + length-safe constant-time compare, cookie name derivation across diacritics / fallback / mixed input, full Service flow on both engines (Add/Authenticate/FindByIdentifier with case-insensitive username), uniform-failure on bad credentials, lazy SHA-256 upgrade with persistence + post-upgrade verify, and the C#-parity "Upsert always rehashes" behaviour. Lint clean. `golang.org/x/text` promoted from indirect to direct.

#### 1.x — XML config file loader ✅ shipped (2026-05-03)

`internal/configfile` ports `NzbDrone.Core.Configuration.ConfigFileProvider` — the host-level XML settings file at `<dataDir>/config.xml` (port, SSL, API key, branch, Postgres connection details, log levels). Resolves CFG-Q1/Q3/Q9 from the bundle direction in analysis/questions.md (Q9 transactional save is the actual bug fix; Q3 ApiKey skip preserved verbatim; Q1 XML-wins-on-read preserved).

Three files:

- `loader.go` — `Loader` owns one config file path, goroutine-safe via per-loader mutex. `Load(ctx)` parses the XML; missing file yields an empty in-memory doc (not an error). `Save(ctx)` writes atomically via temp + fsync + rename — CFG-Q9 fix vs the C# direct-overwrite that risks half-written files on crash. `parseXML` enforces C# `InvalidConfigFileException` parity: empty content, all-control-character content, unparseable XML, missing `<Config>` root, and >1 root element each return `ErrInvalidConfigFile`.
- `provider.go` — `Provider` exposes `String/Int/Bool` accessors with override → XML → default precedence. `Set(key, value)` writes through; `SaveConfigDictionary` is the user-initiated batch path that diff-tracks updates, silently drops `ApiKey` (CFG-Q3), and publishes `ConfigFileSavedEvent` via the messaging bus only when something actually changed.
- `wellknown.go` — typed accessors for `BindAddress`, `Port`, `SslPort`, `EnableSsl`, `LaunchBrowser`, `APIKey`, `URLBase`, `InstanceName`, `LogLevel`, `Branch`, `AuthenticationMethod`. Each preserves the C# special-case logic: APIKey auto-generates + persists when missing or whitespace; URLBase trims surrounding slashes and prefixes a single leading slash; InstanceName falls back to `AppName` ("Sonarr") if the configured value neither starts nor ends with it; AuthenticationMethod auto-upgrades the legacy `AuthenticationEnabled=true` flag and the deprecated `Basic` value to `Forms`. The remaining ~30 well-known keys (Postgres credentials, syslog, UpdateMechanism, log overrides) are mechanical — defer until a downstream consumer pulls on them.

`Overrides` is the host-binding view of env-var / CLI overrides (each well-known key has a `*T` pointer field). Phase 5 wires this from a CLI parser; Phase 1 ships only the data shape.

`APIKeyGenerator` is a func injection point so production wiring passes `auth.GenerateAPIKey` from `internal/auth` shipped earlier this session. Tests inject deterministic generators.

13 tests: missing-file empty load, full Set→Load round-trip, persist-on-default-read (true and false), override-wins-over-XML, empty-override falls back to default, APIKey auto-generate + persist, ResetAPIKey rotation, URLBase normalisation table, InstanceName clamp table, AuthenticationMethod legacy + Basic upgrade + None default, SaveConfigDictionary skips ApiKey + dirty-only emission, all five corrupt-file cases hit `ErrInvalidConfigFile`, realistic v3/v4/v5 sample parses correctly, EnsureDefault no-op on existing files. Lint clean.

What this package deliberately does NOT do: the host-binding env / CLI parser; `MigrateConfigFile` (the C# AppHandle that disables SSL when the cert hash leaks back in — Phase 5 work); the Config-table side (DB-backed user settings, owned by the existing `internal/config` package); `ResetApiKeyCommand` handler (lands when the command bus ships in Phase 3).

#### 1.x — Lifecycle events + appenv (OsInfo + AppFolderInfo + StartupContext) ✅ shipped (2026-05-03)

Two small foundation packages that bridge the bus and the configfile loader to a real host:

- `internal/lifecycle` — three event types: `ApplicationStartingEvent`, `ApplicationStartedEvent`, `ApplicationShutdownRequested{Restarting bool}`. All three implement `Lifecycle() bool` returning true so `messaging.Bus.BeginShutdown()` blocks further publishes — matches the C# `[LifecycleEvent]` attribute behaviour. Subscribers attach to the concrete type via the messaging package's generic Subscribe.
- `internal/appenv` — the runtime / paths / startup-args triple from `NzbDrone.Common.EnvironmentInfo`:
  - `Os` enum + `CurrentOs` package var derived from `runtime.GOOS` (linux/darwin/windows/freebsd-netbsd-openbsd → BSD). C# `LinuxMusl` flavor collapses to `OsLinux` because Go has no runtime musl-vs-glibc detection; if a future port needs it, gate on a build tag.
  - `OsInfo` with Docker / Podman / generic-container detection mirroring `OsInfo.cs:84-92` (`/.dockerenv`, `/proc/1/cgroup` containing `/docker/`, `/run/.containerenv`, `container=` env, `DOTNET_RUNNING_IN_CONTAINER`).
  - `AppFolderInfo` with `AppData / Startup / Temp` plus derived `ConfigPath()`, `MainDBPath(name)`, `LogDBPath(name)`. Path resolution: `--data` override wins; otherwise XDG_CONFIG_HOME/Sonarr → $HOME/.config/Sonarr on Unix (matches the existing C# Mono behaviour where macOS also lands at ~/.config rather than ~/Library/Application Support); $PROGRAMDATA\Sonarr on Windows.
  - `StartupContext` minimal CLI arg parser. Accepts `/key=value`, `--key=value`, `key=value`, and bare boolean flags. Strips leading `-/`, lowercases keys, unquotes `="..."` values. Well-known constants: `StartupArgData`, `StartupFlagNoBrowser`, `StartupFlagHelp`, `StartupFlagTerminateExisting`. Unknown / future args are still captured so adding new flags doesn't require parser changes.
  - Legacy v3 NzbDrone path (`LegacyAppDataFolder` in C#) intentionally omitted — that's a Phase 6 cutover concern.

Tests: lifecycle round-trip with shutdown gate (3 cases); GOOS detection mapping + Is*() mutual exclusivity; container detection invariant (non-Linux must produce all-false); StartupContext arg/flag parsing across 7 input shapes; AppFolderInfo override-wins + default OS-conventional path + derived path helpers. `SONARR_PG_DSN=postgres:///sonarr_spike go test -race -p 1 ./...` green; `golangci-lint run ./...` 0 issues.

The remaining Phase 1 surface is now host-binary glue: `cmd/sonarr/main.go` that wires StartupContext → AppFolderInfo → Loader → Provider → DataStore → Bus → Auth, plus the `IDiskProvider` abstraction (file ops + free space + atomic move) that downstream Phase 2/3 services will pull on. Neither blocks the existing storage layer.

#### 1.x — cmd/sonarr end-to-end wiring ✅ shipped (2026-05-03) — Phase 1 sign-off bar

The strategic plan's Phase 1 bar (~/.claude/plans/curried-discovering-meadow.md): "a Go binary that opens an existing user DB and reports the same schema version." Reached.

`cmd/sonarr/main.go` startup sequence:

1. `appenv.ParseStartupArgs(os.Args[1:])` — handles `/data=PATH`, `/nobrowser`, etc.
2. `appenv.DetectAppFolder` — resolves data dir / startup dir / temp dir, honours `--data`, falls through to OS-conventional paths.
3. `os.MkdirAll` the data dir.
4. `configfile.NewLoader(folder.ConfigPath()).Load(ctx)` — empty doc on missing file.
5. `messaging.New(logger)` — instance-bound bus.
6. `configfile.NewProvider(loader, Overrides{}, bus, auth.GenerateAPIKey)` — wires the API-key generator from `internal/auth`.
7. `cfg.InstanceName(ctx)` — defaults to "Sonarr".
8. `buildDatastoreConfig` — picks SQLite at `<data>/<instance>.db` unless config.xml carries `PostgresMainDb` (matches the C# detection).
9. `datastore.Open` + `datastore.Migrate` to head.
10. `datastore.CurrentVersion` logged through structured slog.
11. `messaging.Publish(ctx, bus, lifecycle.ApplicationStartedEvent{})`.
12. Block on `ctx.Done()`.
13. On signal: publish `ApplicationShutdownRequested` BEFORE `bus.BeginShutdown()` (the lifecycle gate would otherwise block the publish), then `bus.Wait` to drain async handlers, then close the DB.

Smoke test:

```
$ sonarr /data=/tmp/sonarr-phase1-data
... appfolder data=/tmp/sonarr-phase1-data startup=/private/tmp config=/tmp/sonarr-phase1-data/config.xml
... datastore ready engine=sqlite role=main source=sqlite:/tmp/sonarr-phase1-data/Sonarr.db schemaVersion=230 instance=Sonarr
... daemon ready hint="HTTP server / SignalR / scheduler arrive in Phase 5"
^C
... sonarr shutdown requested
... sonarr stopped
```

`config.xml` and `Sonarr.db` materialise on disk; subsequent runs replay migrations idempotently (goose tracks state in `goose_db_version`).

`.gitignore` tightened along the way: the leading-`sonarr` rule was matching the `cmd/sonarr/` source directory, so anchored both binary names with a leading slash. Unmasked the long-standing `cmd/sonarr-update/main.go` stub at the same time — landed under version control so the updater port can plug in during Phase 4.

What this binary still does NOT do — every entry corresponds to a later phase:

- HTTP server + middleware, SignalR hub, web asset serving (Phase 5).
- Command bus (`IExecute<T>`, `CommandQueue`, scheduler) (Phase 3).
- Provider integrations — actual indexer/downloadclient/notification/importlist constructors (Phase 4).
- Parser regex corpus (Phase 2).
- Disk abstraction (`IDiskProvider`: file ops + free space + atomic move + hardlinks) — lazily portable; the first Phase 2 / 3 consumer that needs it will pull it.
- `MigrateConfigFile` — the C# AppHandle that disables SSL when the cert hash leaks back in. Phase 5 work.
- ResetApiKeyCommand handler. Lands when commands ship in Phase 3.

Phase 1 is done.

### Phase 0 — Bootstrap ✅ done

- `go/CONVERSION.md` — conversion rules committed verbatim from the user's prompt.
- `go/README.md` — layout description.
- `go/go.mod` — module `sonarr.tv/sonarr`, Go 1.26.
- `go/cmd/sonarr/main.go` and `go/cmd/sonarr-update/main.go` — entry-point stubs that build clean.
- `go/internal/buildinfo/` — populated at build time via `-ldflags`.
- `go/internal/instrumentation/` — `slog`-backed logger (NLog replacement), with tests.
- `go/.golangci.yml` — strict lint config, currently zero issues.
- `go/.gitignore`.
- `.github/workflows/build_go.yml` — vet/lint/test job + cross-compile matrix (linux/darwin/windows/freebsd, amd64/arm64/arm).
- `go/test/contract/` — full contract-test harness skeleton:
  - `internal/spec` (corpus + golden file format, validated round-trip)
  - `internal/normalize` (JSONPath redactions for timestamps/ids)
  - `internal/runner` (HTTP issuer with normalization)
  - `recorder/` (CLI to drive a running C# Sonarr and write goldens)
  - `replayer/` (Go test helper that diffs against goldens, self-skips when not configured)
- All tests pass under `-race`.

### Phase 0.5 — Deep-dive analysis ⏳ partial

#### 0.5a — Subsystem behavior cards (24 of 24 complete) ✅

| Card | Status | Notes |
|------|--------|-------|
| `subsystems/common.md` | ✅ | 14 sub-areas, 5 open questions (Q-01..Q-05) |
| `subsystems/datastore.md` | ✅ | 4 open questions (Q-09..Q-12) — caught that **Marr.Data is NOT in use** (Dapper only); plan corrected |
| `subsystems/tv.md` | ✅ | 3 open questions (Q-06..Q-08) |
| `subsystems/parser.md` | ✅ | **187 regexes** catalogued, 6 open questions (Q-13..Q-18); ~1,646 test-case rows to port verbatim |
| `subsystems/indexers.md` | ✅ | 10 concrete providers, 9 open questions (INDEXERS-Q1..Q9) |
| `subsystems/download.md` | ✅ | 20 concrete clients, 10 open questions (DOWNLOAD-Q1..Q10) |
| `subsystems/mediafiles.md` | ✅ | 14 open questions (MEDIAFILES-Q1..Q14) |
| `subsystems/decisionengine.md` | ✅ | **38 specifications** catalogued, 10 open questions (DECISION-Q1..Q10) |
| `subsystems/notifications.md` | ✅ | 25 concrete providers, 12 open questions (NOTIFY-Q1..Q12) |
| `subsystems/importlists-metadata.md` | ✅ | 10 import-list providers + SkyHook, 28 open questions (IL-Q*, MD-Q*, DA-Q*) |
| `subsystems/quality-profiles-formats-organizer.md` | ✅ | 22 qualities, 8 CF spec types, ~50 rename tokens, 23 open questions (Q-Q*, P-Q*, CF-Q*, ORG-Q*) |
| `subsystems/system.md` | ✅ | Jobs+Queue+History+Blocklist+Lifecycle+HealthCheck+Housekeeping+Backup+Update; 11 commands / 27 health checks / 31 housekeepers; 37 open questions |
| `subsystems/authentication-security.md` | ✅ | 27 open questions (AUTH-Q1..Q9, SEC-Q1..Q18) — PBKDF2/HMAC-SHA512 10k iters, no CSRF, no rate limit, X-Forwarded-For honored without trust list, password compare non-constant-time, lazy SHA-256→PBKDF2 upgrade on login |
| `subsystems/configuration.md` | ✅ | 37 open questions across 6 sub-areas (CFG/AT/TAGS/RF/RPM/DS-Q*) — `UpdaterConfigProvider` silent rewrites, `TrustCgnatIpAddresses` duplicated across IConfigService + IConfigFileProvider |
| `subsystems/localization-validation.md` | ✅ | 17 open questions (LOC-Q1..Q5, VAL-Q1..Q6, LANG-Q1..Q6); **44 culture files**, **54 Language IDs** — IDs are an unstated wire-format contract, no migration locks them |
| `subsystems/extras.md` | ✅ | 10 open questions (EXTRAS-Q1..Q10); 4 active metadata writers (Kodi/Emby, Roksbox, WDTV, Plex); flagged real bug in `Consumers/Xbmc/KodiEpisodeGuide.cs:31` (TvRage populated from TvMazeId) |
| `subsystems/http-instrumentation-misc.md` | ✅ | 11 areas, 48 open questions (HTTP/INSTR/ANALYTICS/DA/CFLT/SS/IS/MC/ANNO/EXC/PROP-Q*); flagged `IndexerSearch` for split — Phase-1 PRs need a dedicated card |
| `subsystems/messaging.md` | ✅ | 18 open questions (MSG-Q1..Q12, PROG-Q1..Q6); **60 distinct event types** catalogued; 5 events have zero production consumers (possibly dead code); `EventHandleOrderAttribute` only used in 2 sites both `[Last]`; async handlers unbounded on `TaskScheduler.Default` |
| `subsystems/thingiprovider.md` | ✅ | 15 open questions (TP-Q1..Q15); 5 are Phase-1 blockers; canonical 6-column provider table schema; **all 18 `FieldType` values catalogued** (resolves ANNO-Q1's enumeration); `RemoveMissingImplementations` silently deletes rows on missing C# class (forced 6 historical migrations) |
| `subsystems/host.md` | ✅ | 12 open questions (HOST-Q1..Q12); 6 application modes; DryIoc DI confirmed; full middleware-pipeline ordering captured in Startup.cs:337-378; `ForwardedHeadersOptions.KnownIPNetworks` resolves SEC-Q3 (RFC1918+ULA+link-local trusted, KnownProxies empty) |
| `subsystems/updater.md` | ✅ | 15 open questions (UPD-Q1..Q15); 8 explicit per-OS branches; argv contract `<pid> "<sandbox>" "<exec-path>" <preserved-args>`; SHA-256 verify; no macOS .app awareness (UPD-Q10 spike candidate) |
| `subsystems/sonarr-http.md` | ✅ | 15 open questions (SH-Q1..Q15); **resolves Q-05** (System.Text.Json, not Newtonsoft, on the wire — settings catalogued); 19-row middleware-pipeline table; route precedence + path-collision surface; static-field caches identified across 3 components; **agent quota error truncated summary message but card content is complete** |
| `subsystems/native-platform.md` | ✅ | 26 open questions (MONO 12 + WIN 6 + RP 5 + NP 3); full PInvoke/Win32 inventory; only 2 runtime patches, both Mono-gated and no-ops on .NET 6+ → **`Sonarr.RuntimePatches` deletes entirely in the Go port**; confirmed Kestrel binds via Sockets (no `netsh urlacl` needed) |
| `subsystems/indexersearch.md` | ✅ | 12 open questions (IS-Q1..Q12; 4 carried from http-instrumentation-misc bucket, 8 new); pagination size mismatch (1M vs 100K), `LastSearchTime` lies on indexer-wide failure, queue-subtraction asymmetry between bulk and manual paths |

#### 0.5b — Schema / migrations ⏳ schemas captured; ledger pending

- `analysis/schema/sqlite.sql` ✅ — 79 lines (dense one-statement-per-line). 39 tables, 40 indexes. Migration version **230** confirmed (matches the 223 actual files spread across 0..230 with 8 gaps).
- `analysis/schema/postgres.sql` ✅ — 1,794 lines (full pg_dump format). Migration version 230 confirmed.
- `analysis/schema/sqlite-seeded.sql` ❌ — after integration-test fixtures (deferred; the empty-state schemas are the load-bearing reference).
- `analysis/schema/postgres-seeded.sql` ❌ — same.
- `analysis/migrations/ledger.md` ✅ — 342 lines, **all 223 migration files catalogued** (was incorrectly claimed 224; corrected 2026-05-02) with intent, tables touched, embedded-doc fields touched, reversibility, and dropable-in-Go-port flag. Statistics intro buckets sum to 224 (off by 1 — per-row table is the source of truth): 116 schema-only / 21 data-only / 87 mixed / **28 HIGH-RISK embedded-doc shape mutations** / **8 polymorphic-type renames** / 0 reversible (`Down()` always throws) / 6 log-DB-only / **31 dropable in Go port**. 12 MIG-Q* (all Phase-1 blockers — goose authoring decisions: cumulativity ordering, branch-on-engine count, drop-list confirmation).
- **Toolchain note**: dotnet 10.0.202 is installed; `global.json` was updated with `"rollForward": "latestMinor"` to accept it (was strictly pinned to 10.0.203). `dotnet restore + dotnet build src/NzbDrone.Mono/Sonarr.Mono.csproj` builds clean with `/p:TreatWarningsAsErrors=false` (StyleCop SA1200 noise from newer Roslyn analyzers — 9,368 warnings, 0 errors). Output at `_output/net10.0/Sonarr` (Mach-O arm64).

#### 0.5c — REST + SignalR inventory ✅ static + initial dynamic done

- `analysis/rest/inventory.md` — V3: 65 controllers / ~184 endpoints. V5: 55 controllers / ~159 endpoints. Plus shared root endpoints. 4 open questions (REST-Q1..Q4).
- `analysis/signalr/inventory.md` — 19 distinct event streams over single `MessageHub`/`receiveMessage` envelope. 4 open questions (SIGNALR-Q1..Q4).
- `test/contract/corpus/v3-empty-state.json` ✅ — 50 GET endpoints across system, config, profiles, list-empty resources, schemas, localization, language, log, command. Per-entry JSONPath redactions for unstable fields.
- `test/contract/corpus/v5-empty-state.json` ✅ — 29 GET endpoints (V5 surface is leaner — no `config/`, `settings/`, autotagging, notification, importlist, downloadclient, delayprofile, customformat at top level).
- `test/contract/golden/{v3,v5}-empty-state/*.golden.json` ✅ — 79 captured goldens, 2.3 MB total. Driven via `go run ./test/contract/recorder` against the running C# build.
- **Known limitations of the starter capture:**
  - Volatile **HTTP headers** (`Date`, `X-Application-Version`, `X-Miniprofiler-Ids`) are not redacted in goldens — the recorder spec only supports per-entry header redaction with no global default. Worth adding a `default_normalize` block to the corpus format and re-recording. Recorded as a known follow-up.
  - **Empty-state only** — no series, no episodes, no profiles configured beyond Sonarr's seed. Phase 1 will need a seeded corpus too (with fixtures) to lock the populated-state contract.
  - **`/api/v3/system/tasks`** path returned 404 in the recorder run. The corpus uses `/api/v3/system/task` (singular) which works. Sonarr.Http inventory shows both `task` and `tasks` are tested in V3 — verify which is canonical.

#### 0.5d — Specialised inventories ✅ mostly done (jobs+native pending)

- `analysis/embedded-docs/catalogue.md` ✅ — 911 lines, 13 IEmbeddedDocument types + 3 ad-hoc + ~50 provider settings, 24 EDOC-Q*. **Caught the `datastore.md` factual error**: embedded-doc bridge uses System.Text.Json (not Newtonsoft as the card claimed). See "Card corrections" section below.
- `analysis/providers/*.md` ✅ — 6 files, 1,418 lines, **71 concrete providers** catalogued: 10 indexers, 20 download clients, 25 notifications, 10 import lists, 2 metadata sources, 4 metadata writers. 47 PROV-Q*.
- `analysis/parser/regex-corpus.md` ✅ — 476 lines, **186 regexes** verified (parser card said 187 — off by 1; inline `imdbRegex`), **1,585 test cases** verified (card said 1,646 — over-counted). 8 REGEX-Q*. **Resolves Q-14**: only 4 regexes genuinely require an engine (one atomic group, three named backrefs); the other 139 non-RE2 patterns are mechanical rewrites. **Recommendation: rewrite to RE2, do not vendor `dlclark/regexp2`.** ~3 person-weeks plus parity tests.
- `analysis/jobs/catalogue.md` ✅ — 161 lines. Counts verified: **36 commands** (11 scheduled + 25 ad-hoc, including the 5 `IndexerSearch` commands); **31 housekeepers**; **27 health checks**. 16 JOB-Q* (drop `DeleteBadMediaCovers`; collapse 5 search dispatchers; queue-persistence; `[CheckOn]` reflection vs explicit; etc.).
- `analysis/native/inventory.md` ✅ — 167 lines. All MONO/WIN/RP signatures from `native-platform.md` flattened into porter-assignment tables with Go equivalents. 10 NAT-Q* (most cross-cite existing MONO-Q*/WIN-Q*/RP-Q* without renumbering).

#### 0.5e — Dependency-swap spikes ⏳ 2 of 6 done

| Spike | Status | Verdict |
|-------|--------|---------|
| `spikes/sqlite-modernc.md` | ✅ | green — `modernc.org/sqlite` covers WAL / FK / busy_timeout / NOCASE / JSON1 / recursive CTE / concurrent RW |
| `spikes/pgx-postgres.md` | ✅ | green — `jackc/pgx/v5` covers pool / cascade FK / JSONB ops / RETURNING / ILIKE / concurrent RW |
| `spikes/imaging.md` | ✅ | **green** — `github.com/disintegration/imaging` v1.6.2 covers the surface (load JPEG/PNG, resize-to-height, save). 2 IMG-Q* (lazy vs eager render; JPEG byte-equivalence). One yellow flag: repo last touched 2021 — mitigated by API stability + `golang.org/x/image` fallback. |
| `spikes/ffmpeg-exec.md` | ✅ | **green** — direct `os/exec.CommandContext` with `ffprobe -v quiet -print_format json -show_format -show_streams`. FFMpegCore is imported in exactly one C# file. Full JSON-path → `MediaInfoModel` mapping captured. 3 FFM-Q* on wire-format casing, TimeSpan round-trip, RawStreamData newline quirk. |
| `spikes/signalr-philippseith.md` | ❌ | DEFERRED — needs C# Sonarr + React frontend live to verify wire compatibility. Move to Phase 5 prep. |
| `spikes/anacrolix-torrent.md` | ❌ | DEFERRED — only relevant if any provider embeds a torrent client; download.md confirms all 20 download clients talk to external apps. **Skippable** unless future provider needs it. |

#### Card corrections found by Batch C (must be applied before Phase 1)

The embedded-doc catalogue verified card claims against the actual C# source and surfaced these errors. The **catalogue is the source of truth**; the cards listed below need fixes when next touched.

1. **`subsystems/datastore.md:117-119`** says the embedded-doc Dapper bridge uses Newtonsoft. **It actually uses System.Text.Json** — `EmbeddedDocumentConverter.cs:13-29` configures camelCase + `WhenWritingNull` + `JsonStringEnumConverter` + `STJUtcConverter` + `STJTimeSpanConverter`. Same applies to `ProviderRepository.Query` for provider settings (`ProviderRepository.cs:25-35`). This means there is **no Newtonsoft-vs-STJson asymmetry** between the wire (`sonarr-http.md`) and the disk (datastore embedded docs); both are STJson. Cross-cuts: `sonarr-http.md` cited the asymmetry in its JSON section, and `datastore.md` Q-09 framing was based on it. Both citations need to be re-grounded — the Go port does **not** need a separate Newtonsoft-style serializer for embedded docs. Captured as `EDOC-Q1`.

2. **`subsystems/parser.md`** claims 187 total regexes. **Actual: 186.** Off by 1; the inline `imdbRegex` at `Parser.cs:865` was double-counted in the original grep.

3. **`subsystems/parser.md`** claims ~1,646 test cases. **Actual: 1,585** in `ParserTests/*.cs` (1,567) + `ParsingServiceTests/*.cs` (18), plus 35 cases in `HashedReleaseFixture.HashedReleaseParserCases`. The original grep over-counted `[TestCaseSource]` and commented-out cases.

4. **`subsystems/download.md`** prose says "24 download clients" but the table enumerates 20 distinct rows. Card-internal inconsistency. Provider matrix locks the count at **20**.

5. **`subsystems/notifications.md`** says "26 ... no separate Jellyfin"; actual count of `INotification` classes is **25**.

#### Open questions ledger

`analysis/questions.md` has been **fully reorganized** (715 lines, 11 H2 sections). Original Q-01..Q-18 numbering is preserved at the bottom for backwards compatibility with subsystem-card cross-references. The body is now organized **by phase blocker** with explicit resolution citations for everything closed. New deliverables landed during the final batch:

- **Migrations ledger**: +12 MIG-Q* (all Phase-1).
- **Jobs catalogue**: +16 JOB-Q* (mostly port-time decisions).
- **Native inventory**: +10 NAT-Q* (cross-cited from MONO/WIN/RP).
- **Imaging spike**: +2 IMG-Q*.
- **Ffmpeg spike**: +3 FFM-Q*.

Total stockpile now ~520 questions; the ledger explicitly resolves a substantial fraction (~25-30% by count, more by weight since Q-05 / Q-09-spike / Q-14 / TP-Q1/Q2 / VAL-Q1 / ANNO-Q1 each had downstream effects on multiple cards). **97 explicit `- **PREFIX-QN**` ledger entries** in the resolved/blocker/decision/drop sections.

**Resolved during Batch C:**
- **Q-14 (RE2 vs .NET regex)** — RESOLVED by `parser/regex-corpus.md`. Only 4 of 186 regexes genuinely require an engine (one atomic group `(?>[-._ ])` at `Parser.cs:398`, three named backrefs `\k<sep>` at `Parser.cs:29,33` and `\k<part2>` at `ReleaseGroupParser.cs:9`). The other 139 non-RE2 patterns are mechanical lookaround-to-boundary rewrites. **Decision: rewrite to RE2, do not vendor `dlclark/regexp2`.** Effort: ~3 person-weeks + parity tests against 1,585 cases. **Unblocks Phase 2 (parser).**
- **Q-09-adjacent (embedded-doc serializer)** — datastore.md card was wrong; the actual converter is STJson, not Newtonsoft. The Go port can use a single JSON config (camelCase + `WhenWritingNull` + custom UTC/TimeSpan converters) for both wire and disk. Specific Q-09 framing about Dapper-style type handlers still applies (Go's `database/sql` has no analogue), but the JSON-encoder choice is now consistent.
- **TP-Q1/TP-Q2 (stable string registry)** — INPUT GATHERED by `providers/*.md`. Every `Implementation` and `ConfigContract` value is documented exactly. The Go registry can use these as opaque string IDs. Note: 2 cases (Vuze/Transmission share `TransmissionSettings`; Usenet/TorrentDownloadStation share `DownloadStationSettings`) require `(impl_id, contract_id)` pairs not just impl_id. **Unblocks Phase 4 (providers).**
- **ANNO-Q1 (`[FieldDefinition]` Go idiom)** — INPUT GATHERED by `providers/*.md` (every settings field's annotation captured). The Go-idiom decision is still open but the porter now has the complete corpus.

**Resolved during Batch B** (still need to be folded into `questions.md` or marked resolved-in-card):
- **Q-05 (Newtonsoft JSON quirks)** — RESOLVED by `sonarr-http.md` § JSON serialization. The wire is **System.Text.Json** with these settings: camelCase prop+dict keys, case-insensitive read, `WhenWritingNull`, `WriteIndented = true`, `AllowTrailingCommas`, `JsonStringEnumConverter(camelCase, allowIntegerValues=true)`, plus 4 custom converters (Version, HttpUri, TimeSpan="hh:mm:ss", DateTime="yyyy-MM-ddTHH:mm:ssZ" UTC). Newtonsoft is confined to embedded-doc Dapper bridge + a few legacy paths. **Unblocks Phase 3.**
- **SEC-Q3 (forwarded-headers trusted-proxy allowlist)** — DOCUMENTED in `host.md` and `sonarr-http.md` middleware table. `KnownIPNetworks` = RFC1918 + ULA + link-local. `KnownProxies` empty. `ForwardLimit = 1`. Behavior is captured; the **security concern remains** (any trusted-network peer can spoof X-Forwarded-For and bypass `DisabledForLocalAddresses`). Decision still required, but it's a port-time choice not a blocker.
- **VAL-Q1 (FluentValidation DI registration)** — RESOLVED by `messaging.md`/`host.md`. Discovery is implicit DryIoc `RegisterMany` scan in `Common/Composition/Extensions.cs:27-29`; validators are constructed in-place inside `RuleFor` chains.
- **ANNO-Q1 (`[FieldDefinition]` enumeration)** — PARTIALLY RESOLVED by `thingiprovider.md` (full 18-value `FieldType` list catalogued). The Go-idiom decision (struct tags vs explicit slice) is still open; kept as Phase-1 blocker.

Several questions are blocking — the porter cannot start Phase 1 without answers:

- **Q-05 (Newtonsoft JSON quirks):** API contract is frozen on Newtonsoft semantics. `encoding/json` differs in casing, null handling, enum format, datetime format, timespan format. Needs a decision: thin custom encoder, third-party (`json-iterator/go`), or per-type `MarshalJSON`. **Blocks Phase 3 (HTTP & SignalR surface).**
- **Q-09 (embedded-doc handlers in Go):** no Dapper-style type handler in `database/sql`; pick reflection scan vs per-type Scanner/Valuer vs generic JSON wrapper. **Blocks Phase 1 (datastore).**
- **Q-12 (mutable static migration context):** `MigrationContext.Current` is a static — affects test parallelism. **Blocks Phase 1.**
- **Q-14 (RE2 vs .NET regex):** parser uses lookaround / backreferences / atomic groups. Go's RE2 doesn't. Needs decision: vendored regex engine (`grafana/regexp`, `dlclark/regexp2`) or rewrite the few non-RE2 patterns. **Blocks Phase 2 (parser).**

## Pending agent dispatches

When resuming, dispatch these batches (each as parallel `Agent` calls in one message). Use the standard subsystem-card template — see `go/analysis/README.md`.

**Batch A — DONE (5 agents, 2026-05-02).** 139 new questions raised. See the cards table above.

**Batch B — DONE (5 agents, 2026-05-02).** 94 new questions, plus 4 prior questions resolved (Q-05, VAL-Q1, ANNO-Q1 partial, SEC-Q3 documented).

**Batch B-prime — DONE (1 agent, 2026-05-02).** IndexerSearch split out; 8 new questions added.

**Batch C — DONE (3 agents, 2026-05-02).** 79 new questions, plus 1 prior question fully resolved (Q-14: rewrite to RE2) and 3 partially resolved (Q-09 embedded-doc serializer, TP-Q1/Q2 string registry input, ANNO-Q1 input).

**Final batch — DONE (4 agents, 2026-05-02).** Migrations ledger (224 rows), jobs+native flatten (36 commands / 31 housekeepers / 27 health checks; PInvoke surface), imaging+ffmpeg spikes (both green), questions-ledger triage (715-line reorganization by phase). Two minor agent issues: (1) `questions-triage.md` summary report wasn't written (quota error after the main ledger landed; non-critical), (2) the migrations agent burned more tokens than expected because of the 224-file walk. Both agents' primary deliverables are intact.

**Phase 0.5 sign-off bar achieved.** Phase 1 work can begin. The remaining items below are *follow-ups*, not gates:
- `signalr-philippseith.md` spike — needs frontend setup; move to Phase 5 prep.
- `anacrolix-torrent.md` spike — skippable unless future provider needs it.
- `questions-triage.md` summary — easy to write later from `questions.md` content.
- `analysis/schema/{sqlite,postgres}-seeded.sql` — after integration-test fixtures; deferred until needed.

**Remaining spikes (4 of 6):** `signalr-philippseith.md`, `imaging-imagesharp.md` (note `MediaCover` uses ImageSharp per the http-instrumentation-misc finding — this is now likely a green spike), `ffmpeg-exec.md`, `anacrolix-torrent.md` (likely skippable). New spike candidate: **macOS .app-bundle update mechanics** (UPD-Q10) — flagged by the host+updater card.

## Cross-cutting concerns surfaced by Batches A + B + C (must be reconciled before Phase 1)

These came out of multiple Batch A cards naming the same friction. Each one has at least one card-local question already; listed here so the reader doesn't have to re-discover the connections:

1. **`Sonarr.Http` is its own card now.** Auth card (`SEC-Q3`) and `rest/inventory.md` both reach into it. Batch B item 4 above is non-optional.
2. **`TrustCgnatIpAddresses` is duplicated** across `IConfigService` (DB) and `IConfigFileProvider` (XML). Auth card (`SEC-Q9`) and configuration card (`CFG-Q4`) need a single answer for which value wins. Sonarr-http card will likely settle it.
3. **Language IDs are an unstated wire contract.** 54 hand-numbered values persisted in `QualityProfile.Languages`, `Series.OriginalLanguage`, `EpisodeFile.Languages`, plus indexer/import-list/notification settings. No migration locks them. `LANG-Q2` blocks the embedded-doc catalogue (Batch C item 7).
4. **Validation error envelope leaks FluentValidation 9.x internals** (`Severity`, `ErrorCode`, `FormattedMessagePlaceholderValues`). `VAL-Q4`. The Go port has no FluentValidation analogue and must hand-roll a byte-equivalent envelope; needs golden snapshots in `rest/golden/` once 0.5c can capture them.
5. **`[FieldDefinition]` annotation pattern blocks every provider port.** `ANNO-Q1`. Pick the Go idiom (struct tags vs explicit slice) before porting *any* provider — applies to indexers, download, notifications, import-lists, metadata, autotagging, custom formats.
6. **Analytics + service.sonarr.tv coupling.** `ANALYTICS-Q1` + `INSTR-Q6`. Telemetry is opt-in but the same domain serves daily-series and scene-mapping JSON, so dropping analytics must not drop the data endpoints.
7. **Series-stats Postgres branch added `string_agg(DISTINCT ...)` recently** while SQLite branch still relies on `GROUP_CONCAT` + C# dedup. `SS-Q1`. Cross-DB parity question.
8. **Sentry DSNs are hard-coded** at `NzbDrone.Common/Instrumentation/NzbDroneLogger.cs:75-83`. Strategy decision belongs in the `common` card (already drafted; needs a follow-up Q).
9. **Possible dead code:** CloudFlare interceptor (`HTTP-Q2`) and TorCache interceptor (`HTTP-Q1`). Verify before porting.
10. **Real bug found in C# (not a port question):** `KodiEpisodeGuide.cs:31` populates TvRage from TvMazeId. `EXTRAS-Q5`. Fixing it changes user-visible NFO output — port-time decision: replicate the bug or fix it?

11. **Provider type names are persisted as strings.** `TP-Q1`/`TP-Q2`. The `Implementation` and `ConfigContract` columns in `Indexers`, `DownloadClients`, `Notifications`, `ImportLists`, `Metadata`, `CustomFormats`, `AutoTagging` tables store C# class names. `RemoveMissingImplementations` (`ProviderFactory.cs:199-208`) silently deletes rows whose class no longer resolves — this has forced 6 historical migrations (046, 049, 082, 090, 104, 219). Go has no cross-language analogue. **Decision required**: stable string registry decoupled from Go type names is the only path that doesn't break upgrades. Phase 1 blocker for **every** provider port.

12. **Five events have zero production consumers.** `MSG-Q12`. Possibly dead code, possibly external-plugin contracts. Decide whether the Go port keeps them: `EpisodeImportFailedEvent`, `SeriesMovedEvent`, `SeriesRefreshCompleteEvent`, `SceneMappingsUpdatedEvent`, `ProviderStatusChangedEvent<T>`.

13. **Real latent bug in Sonarr.RuntimePatches.** `RuntimePatchBase.cs:84` — `TryPatchMethod(type, "GetSslServer")` hardcodes the method name and ignores the parameter. Latent because the only caller happens to want that method. Not a Go-port question (project deletes entirely) but a C# bug worth a separate PR.

14. **`[ThreadStatic]`+`AsyncLocal` dual-write in `ProgressMessageContext`.** `MSG-Q11`/`PROG-Q1`. Reads prefer ThreadStatic, writes hit both. No comment justifies this. Likely redundant; may be a leftover from an older threading model. Pick one for the Go port (Goroutine-local via context.Context is the natural fit).

15. **MaxRequestBodySize is unbounded.** `SH-Q6`. Kestrel is configured with `Limits.MaxRequestBodySize = null` (`Bootstrap.cs:209`). Probably intentional for backup uploads, but worth a configurable cap in the Go port.

16. **JSON pretty-printing on every API response.** `SH-Q15`. `STJson.WriteIndented = true`. Bytes-per-response cost on hot endpoints (`/api/v3/queue`, `/api/v3/episode`). Consider compact + opt-in `?pretty=` for the Go port.

17. **Static-field config caches across the HTTP layer.** `SH-Q8`/`AUTH-Q1`/`VersionMiddleware._version`. Three components cache config values in static fields and never refresh on `ConfigSavedEvent`. Inconsistent: should be uniformly live-read or uniformly subscribe. Pick one for the Go port.

18. **Routing relies on hardcoded `"V3"`/`"V5"` substrings** in `RestControllerWithSignalR` namespace check (`REST/RestControllerWithSignalR.cs:79, 102`). `SH-Q12`. Future versions silently won't broadcast. Replace with `VersionedApiControllerAttribute.Version != null` check.

19. **`/api` (no version) discovery requires API key.** `SH-Q13`. `ApiInfoController` lacks `[AllowAnonymous]`. Clients hit `/api` *before* knowing how to auth, so this is either a bug or an undocumented assumption that clients are pre-configured with the key.

20. **`BackupFileMapper` does pattern-match validation, not path-traversal protection.** `SH-Q14`. `resourceUrl.Replace("/backup/", "")` then `Path.Combine`. The regex provides some safety, but Go port should harden via `filepath.Clean` + prefix check. Phase 3 security concern.

21. **Polymorphic `{type, body}` envelopes use `Type.GetType("...{typename}, Sonarr.Core")`.** `EDOC-Q*`. Same C#-class-name fragility as the provider tables (TP-Q1/Q2) — it extends to **CustomFormats specifications, AutoTagging specifications, and Commands**. Migration 205 already shows this in action (`SeasonPackSpecification` → `ReleaseTypeSpecification` rename required a migration). The stable string registry from TP-Q1/Q2 must cover these too.

22. **Wire-format gotchas in embedded-doc payloads.** `EDOC-Q*`. The Go port's struct tags must replicate exactly: `videoCodecID` (literal STJ camelCase, NOT `videoCodecId`); `Language` lists are int-form via per-container converter (not full-object); pre-225 `MediaInfoModel` rows still in flat shape; `StatusSpecification` discriminator is `status` not `value`; `Language.Original` has id `-2`; `omitempty` cannot be used on value-typed fields because STJ emits zero defaults. Read the catalogue's per-type detail before writing any struct.

23. **Card factual error: `datastore.md:117-119` on serializer.** Embedded-doc bridge is **System.Text.Json**, not Newtonsoft. Captured under "Card corrections" above.

24. **20 download clients, 25 notifications, 10 import lists, 10 indexers, 4 metadata writers, 2 metadata sources** — exact counts now locked in `providers/*.md`. The `subsystems/download.md` and `subsystems/notifications.md` cards have stale prose claiming higher numbers.

25. **`Type.Name` is the registry key for every provider.** `providers/*.md` PROV-Q*. Confirmed safe to use C# class names verbatim as opaque string IDs in the Go registry. Two cases require `(impl_id, contract_id)` pairs (Vuze/Transmission share `TransmissionSettings`; Usenet/TorrentDownloadStation share `DownloadStationSettings`).

26. **Twitter notification provider** (PROV-Q22): targets sunset Twitter API v1.1. Recommend port-and-deprecate (`Test()` always fails, preserves existing rows).

27. **`MetadataFile.Consumer` column** uses the same C# class-name string as `Implementation` (PROV-Q41). Same registry key — no forced migration needed for the Go port.

28. **Q-14 resolved**: rewrite the 4 atomic-group / named-backref regexes; mechanically rewrite the 139 lookaround-only patterns. **Do not vendor `dlclark/regexp2`** (~10× runtime cost on the parser hot path for marginal porting savings).

## Things the deep-dive has already learned (don't re-discover)

- **Marr.Data is NOT in use.** Datastore uses Dapper + a hand-rolled `BasicRepository`/`SqlBuilder`/`WhereBuilder`. The plan's mention of Marr.Data was wrong; `subsystems/datastore.md` is the source of truth.
- **`Sonarr.Http` is shared across V3 and V5.** Routing is per-controller via `[V3ApiController]`/`[V5ApiController]`, not a global prefix middleware.
- **SignalR has a single hub (`MessageHub`), single broadcast topic (`receiveMessage`), single envelope (`SignalRMessage`).** All 19 event streams flow through this.
- **The current SQLite C# wrapper is `SourceGear.sqlite3` (CGo).** The Go port ditches CGo via `modernc.org/sqlite` — verified compatible.
- **The C# side has 223 FluentMigrator migration files numbered 0..230, with 8 missing numbers** (80, 133, 208, 213, 216, 222, 228, 229). One file (214) is an empty no-op. The goose port must reproduce them in version order. Several mutate embedded-document JSON shapes — those are the highest-risk single migrations and are flagged in `analysis/migrations/ledger.md`'s HIGH RISK section.
- **`Update/History/` data lives in the log DB**, not the main DB. `LogDbEnabled = false` silently disables update history.
- **`UpdaterConfigProvider` silently rewrites `Branch` config on startup.** This is intentional behavior to preserve.
- **Backup forces `journal_mode=truncate`** on the SQLite backup target as a defensive workaround for page-size mismatches.
- **`DeleteBadMediaCovers` housekeeper** is a 2014-era one-shot that self-disables after running — candidate to drop in the Go port (flagged HOUSE-Q3).

## Local environment as of last save

- macOS Tahoe (Darwin 25.4.0) on arm64.
- Go 1.26.2 (Homebrew).
- golangci-lint 2.11.4 (Homebrew).
- PostgreSQL 16.13 (Homebrew, `brew services start postgresql@16`), database `sonarr_spike` exists. DSN: `postgres:///sonarr_spike`.
- **dotnet 10.0.202 installed** at `/usr/local/share/dotnet/sdk/10.0.202` (system-wide Microsoft installer). `global.json` was relaxed from strict `10.0.203` to `{ version: "10.0.0", rollForward: "latestMinor" }` to accept it. C# build of `src/NzbDrone.Mono` + `src/NzbDrone.Console` works with `/p:TreatWarningsAsErrors=false` (StyleCop SA1200 noise from newer analyzers; 9,368 warnings, 0 errors). Output: `_output/net10.0/Sonarr` (Mach-O arm64). Runs cleanly with `-data=<dir>` against either SQLite or Postgres.
- The repo is on `v5-develop` with **one tracked-file change** (`global.json` rollForward relaxation). Everything else (entire `go/` tree, `_output/` build artifacts, `.github/workflows/build_go.yml`) is uncommitted/gitignored. No port-side commits yet.

## What's NOT done that the plan called for

- **No production Go has been written for any subsystem yet.** Phase 1 is gated on Phase 0.5 sign-off. The instrumentation logger and buildinfo packages exist only because they're prerequisites for the cmd binaries to compile.
- **No commits.** The `go/` tree is uncommitted. The next session should decide whether to commit Phase 0 separately so it's reviewable as one unit.
- **CI not yet exercised.** `.github/workflows/build_go.yml` is committed-to-disk-but-not-pushed. First push to GitHub will be the validation that the workflow YAML is correct.
