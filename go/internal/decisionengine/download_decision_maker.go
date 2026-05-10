package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// ReleaseMapper is the slice of [parsingservice.Service] the
// [DownloadDecisionMaker] reaches for. Mirrors the C# `IParsingService`
// methods consumed by `DownloadDecisionMaker.GetDecisions` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecisionMaker.cs:79-93`:
//
//   - Map(parsedInfo, tvdbId, tvRageId, imdbId, searchCriteria) →
//     RemoteEpisode (Subject in Go). The end-to-end series/episode
//     resolution pipeline (slice 4.S sub-arc 11a).
//
// The 5-arg `ParseSpecialEpisodeTitle(parsedInfo, releaseTitle,
// tvdbId, tvRageId, imdbId, searchCriteria)` overload at C# `:313-362`
// is DEFERRED — the Go port at slice 3.66 landed only the 3-arg form
// that takes a resolved *series.Series. The DownloadDecisionMaker
// skips the C# `:81-89` IsPossibleSpecialEpisode arm until the
// 5-arg overload ports. Practical impact: special episodes whose
// parse looks "possible special" rather than canonical will fall
// through to the standard Map path; if Map's findSeries can't
// resolve them they'll get an UnknownSeries / UnknownEpisode
// rejection rather than a synthesised special-episode parse. The
// behavioural delta is bounded — the C# fallback resolves
// season-0 special titles that the regex parser missed, and at
// HEAD the Aggregate flow doesn't materially depend on the special-
// parse path for the spec evaluation.
//
// Returned *Subject is the mutable container the rest of the
// pipeline (aggregator + spec evaluation) fills in.
type ReleaseMapper interface {
	Map(
		ctx context.Context,
		parsedInfo *parser.ParsedEpisodeInfo,
		tvdbID int,
		tvRageID int,
		imdbID string,
		searchCriteria *SearchCriteria,
	) (*Subject, error)
}

// Aggregator is the slice of [aggregation.Service] the
// [DownloadDecisionMaker] reaches for. Mirrors the C#
// `IRemoteEpisodeAggregationService.Augment(remoteEpisode)` at
// `src/NzbDrone.Core/Download/Aggregation/RemoteEpisodeAggregationService.cs:9-12`.
// Returns the same Subject pointer it was passed (per the C# return
// shape) so callers can chain.
type Aggregator interface {
	Augment(ctx context.Context, sub *Subject) *Subject
}

// SceneAliasFinder is the slice of [scenemappings.Service] the
// [DownloadDecisionMaker] consults for the `MatchesAnotherSeries`
// rejection path at C# `DownloadDecisionMaker.cs:99-104`: when
// parsingservice.Map couldn't resolve a series, check if the parsed
// series title is a scene alias for *some* series — if so, surface
// "matches another series" rather than the generic "unknown series".
//
// Returns (nil, nil) when no scene mapping matches; the C# tree
// reads `_sceneMappingService.FindTvdbId(...)` which returns `int?`.
type SceneAliasFinder interface {
	FindTvdbID(ctx context.Context, seriesTitle, releaseTitle string, seasonNumber int) (*int, error)
}

// noopReleaseMapper satisfies [ReleaseMapper] by returning a Subject
// whose Series is nil — every release short-circuits to the
// UnknownSeries rejection. Bound by [NewDownloadDecisionMaker] when
// the caller passes nil; matches the production behaviour for a
// fresh install with no series in catalog.
type noopReleaseMapper struct{}

// Map returns a Subject with nil Series — every release short-circuits
// to UnknownSeries. Bound by [NewDownloadDecisionMaker] when no mapper
// is supplied.
func (noopReleaseMapper) Map(_ context.Context, parsedInfo *parser.ParsedEpisodeInfo, _, _ int, _ string, _ *SearchCriteria) (*Subject, error) {
	sub := NewSubject()
	sub.ParsedEpisodeInfo = parsedInfo
	if parsedInfo != nil {
		sub.MappedSeasonNumber = parsedInfo.SeasonNumber
		sub.Languages = parsedInfo.Languages
	}
	return sub, nil
}

// noopAggregator satisfies [Aggregator] as a pass-through. Bound by
// [NewDownloadDecisionMaker] when aggregator==nil.
type noopAggregator struct{}

// Augment returns sub unchanged — the noop binding.
func (noopAggregator) Augment(_ context.Context, sub *Subject) *Subject { return sub }

// noopSceneAliasFinder satisfies [SceneAliasFinder] with a (nil, nil)
// return — no scene-alias resolution. Bound by
// [NewDownloadDecisionMaker] when sceneAlias==nil.
type noopSceneAliasFinder struct{}

// FindTvdbID always returns (nil, nil) — the noop binding skips the
// scene-alias rejection path.
func (noopSceneAliasFinder) FindTvdbID(_ context.Context, _, _ string, _ int) (*int, error) {
	return nil, nil
}

// DownloadDecisionMaker is the end-to-end orchestrator that turns a
// slice of raw release reports into a slice of [Decision] values.
// Ports the C# `DownloadDecisionMaker` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecisionMaker.cs:1-242`
// (the public IMakeDownloadDecision surface — GetRssDecision /
// GetSearchDecision).
//
// Slice 4.U (sub-arc 11c of queue [11], CLOSES queue [11]). Sits ON
// TOP OF the existing [DecisionMaker] which evaluates the per-spec
// gate; this type wraps it with the parsing+mapping+aggregation
// pre-pass.
//
// Per-report decision tree (line-aligned with C# `:71-178`):
//
//  1. parser.ParseTitle(report.Title). nil result → "Unable to parse"
//     short-circuit (only when SearchCriteria != nil; otherwise the
//     report is dropped silently — matches C# `:128-150`).
//
//  2. If parsedInfo.SeriesTitle is non-empty: call ReleaseMapper.Map
//     to resolve the Series + Episodes + ParsedEpisodeInfo into a
//     Subject. Set Subject.Release = report, ReleaseSource via
//     [ReleaseSourceFor].
//
//  3. Series-nil branch: consult SceneAliasFinder.FindTvdbID. If a
//     scene alias matches, emit a `MatchesAnotherSeries` rejection;
//     otherwise emit `UnknownSeries`. Mirrors C# `:97-108`.
//
//  4. Episodes-empty branch: emit `UnknownEpisode` rejection.
//     Mirrors C# `:110-113`.
//
//  5. Otherwise: Aggregator.Augment(subject); set DownloadAllowed =
//     (len(Episodes) > 0); call DecisionMaker.Decide for the spec-
//     gate evaluation. Custom-format calculation at C# `:118-121`
//     is DEFERRED — the customformats subsystem isn't ported at
//     HEAD; flagged as a queue [11] follow-up.
//
//  6. SearchCriteria fallback: when criteria != nil AND no
//     parsedInfo (or parsedInfo.SeriesTitle is empty), build a
//     minimal "couldn't parse" Subject + emit `UnableToParse`
//     rejection. Mirrors C# `:128-150`.
//
//  7. Panic recovery (Go) / catch-all (C# `:153-160`): a panic
//     anywhere in the per-report processing path produces a
//     Subject{Release: report} + `Error` rejection.
//
// Returns the assembled []Decision in input order. Reports that
// parsed to nil AND don't have a SearchCriteria attached are dropped
// (no Decision emitted) — matches C# `:71-178` skip semantics.
//
// Goroutine-safe post-construction: stateless beyond the wired deps,
// which themselves must be goroutine-safe (production deps are).
type DownloadDecisionMaker struct {
	mapper     ReleaseMapper
	aggregator Aggregator
	sceneAlias SceneAliasFinder
	decisions  *DecisionMaker
	log        *slog.Logger
}

// NewDownloadDecisionMaker binds the dependencies. decisions
// (the per-spec gate orchestrator) is REQUIRED. mapper, aggregator,
// sceneAlias, log are OPTIONAL — nil values bind defensible no-op
// stubs (see noopReleaseMapper, noopAggregator, noopSceneAliasFinder)
// so the constructor never returns a nil-DownloadDecisionMaker.
func NewDownloadDecisionMaker(
	decisions *DecisionMaker,
	mapper ReleaseMapper,
	aggregator Aggregator,
	sceneAlias SceneAliasFinder,
	log *slog.Logger,
) (*DownloadDecisionMaker, error) {
	if decisions == nil {
		return nil, errors.New("decisionengine: NewDownloadDecisionMaker: nil decisions")
	}
	if mapper == nil {
		mapper = noopReleaseMapper{}
	}
	if aggregator == nil {
		aggregator = noopAggregator{}
	}
	if sceneAlias == nil {
		sceneAlias = noopSceneAliasFinder{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &DownloadDecisionMaker{
		mapper:     mapper,
		aggregator: aggregator,
		sceneAlias: sceneAlias,
		decisions:  decisions,
		log:        log,
	}, nil
}

// GetRssDecision ports the public `IMakeDownloadDecision.GetRssDecision`
// entry at C# `DownloadDecisionMaker.cs:48-51`. Used by the RssSync
// handler (slice 4.U wiring) after FetchRSS returns the raw release
// fleet.
//
// pushedRelease = false in the production RSS path; the orchestrator
// passes true only when invoked from the manual "push release" entry
// point (currently a Phase-5 surface, deferred).
func (d *DownloadDecisionMaker) GetRssDecision(ctx context.Context, reports []any, pushedRelease bool) []Decision {
	return d.getDecisions(ctx, reports, pushedRelease, nil)
}

// GetSearchDecision ports the public `IMakeDownloadDecision.GetSearchDecision`
// entry at C# `DownloadDecisionMaker.cs:53-56`. Used by the indexer
// search entry points (EpisodeSearch / SeasonSearch) once they switch
// to the typed Decision return — flagged as a queue [11] follow-up
// since the consumer surfaces depend on Phase-5 API/v3 endpoints.
//
// pushedRelease is always false for search paths; ReleaseSource is
// derived from criteria.InteractiveSearch / UserInvokedSearch by
// [ReleaseSourceFor].
func (d *DownloadDecisionMaker) GetSearchDecision(ctx context.Context, reports []any, criteria *SearchCriteria) []Decision {
	return d.getDecisions(ctx, reports, false, criteria)
}

// getDecisions is the shared per-report processing loop. Ports C#
// `GetDecisions` at `DownloadDecisionMaker.cs:58-178`.
//
// The C# version returns `IEnumerable<DownloadDecision>` and `yield`s
// per report so callers can stream. The Go port materialises to a
// slice — every caller at HEAD consumes the whole list anyway, and a
// channel/iterator adds little value at the slice sizes (typically
// 10-200 reports per RSS sweep).
func (d *DownloadDecisionMaker) getDecisions(ctx context.Context, reports []any, pushedRelease bool, criteria *SearchCriteria) []Decision {
	if len(reports) > 0 {
		d.log.Info("decisionMaker: processing releases", "count", len(reports))
	} else {
		d.log.Info("decisionMaker: no results found")
	}

	out := make([]Decision, 0, len(reports))
	for reportNumber, report := range reports {
		releaseInfo, ok := asReleaseInfo(report)
		if !ok || releaseInfo == nil {
			d.log.Debug("decisionMaker: skipping non-release entry",
				"report", reportNumber+1, "of", len(reports))
			continue
		}

		decision, kept := d.decideOneReport(ctx, report, releaseInfo, pushedRelease, criteria, reportNumber+1, len(reports))
		if kept {
			out = append(out, decision)
		}
	}
	return out
}

// decideOneReport runs the per-report processing block with panic
// recovery — when anything panics inside, emit an Error rejection
// rather than failing the entire batch. Mirrors the C# `:77-159`
// try/catch.
//
// Returns (decision, true) when a decision was produced for the
// report; (zero, false) when the report should be silently dropped
// (parsedInfo nil + no search criteria — matches the C# yield-skip).
func (d *DownloadDecisionMaker) decideOneReport(
	ctx context.Context,
	report any,
	releaseInfo *parser.ReleaseInfo,
	pushedRelease bool,
	criteria *SearchCriteria,
	reportNumber, totalReports int,
) (dec Decision, kept bool) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("decisionMaker: panic processing release",
				"title", releaseInfo.Title, "indexer", releaseInfo.Indexer,
				"panic", r)
			sub := NewSubject()
			sub.Release = report
			sub.ReleaseSource = ReleaseSourceFor(pushedRelease, criteria)
			dec = Decision{
				Subject: sub,
				Rejections: []Rejection{{
					Reason:  ReasonError,
					Message: fmt.Sprintf("Unexpected error processing release: %v", r),
					Kind:    RejectionPermanent,
				}},
			}
			kept = true
		}
	}()

	d.log.Debug("decisionMaker: processing release",
		"report", reportNumber, "of", totalReports,
		"title", releaseInfo.Title, "indexer", releaseInfo.Indexer)

	parsedInfo := parser.ParseTitle(releaseInfo.Title)

	// C# `:81-89` ParseSpecialEpisodeTitle 5-arg overload: deferred,
	// see ReleaseMapper doc. Skip and fall through to the standard
	// Map path.

	if parsedInfo != nil && parsedInfo.SeriesTitle != "" {
		dec = d.decideMappedReport(ctx, report, releaseInfo, parsedInfo, pushedRelease, criteria)
		return dec, true
	}

	if criteria != nil {
		// C# `:128-150`: when a search is in flight, every report
		// produces a Decision — even ones that didn't parse. Build
		// a synthetic parsedInfo from regex-only language/quality
		// detection so the UnableToParse rejection carries something
		// usable for the UI.
		if parsedInfo == nil {
			parsedInfo = &parser.ParsedEpisodeInfo{
				Languages: parser.ParseLanguages(releaseInfo.Title),
				Quality:   quality.ParseQuality(releaseInfo.Title),
			}
		}
		if parsedInfo.SeriesTitle == "" {
			sub := NewSubject()
			sub.Release = report
			sub.ReleaseSource = ReleaseSourceFor(pushedRelease, criteria)
			sub.ParsedEpisodeInfo = parsedInfo
			sub.Languages = parsedInfo.Languages
			dec = Decision{
				Subject: sub,
				Rejections: []Rejection{{
					Reason:  ReasonUnableToParse,
					Message: "Unable to parse release",
					Kind:    RejectionPermanent,
				}},
			}
			return dec, true
		}
	}

	return Decision{}, false
}

// decideMappedReport handles the C# `:91-126` branch: parsedInfo has a
// SeriesTitle, so call ReleaseMapper.Map then route on Series/Episodes
// state. Always returns a Decision (mirrors C# which always assigns
// `decision` inside the `parsedEpisodeInfo != null && !SeriesTitle...`
// arm); the caller treats every return as kept=true.
func (d *DownloadDecisionMaker) decideMappedReport(
	ctx context.Context,
	report any,
	releaseInfo *parser.ReleaseInfo,
	parsedInfo *parser.ParsedEpisodeInfo,
	pushedRelease bool,
	criteria *SearchCriteria,
) Decision {
	sub, err := d.mapper.Map(ctx, parsedInfo, releaseInfo.TvdbID, releaseInfo.TvRageID, releaseInfo.ImdbID, criteria)
	if err != nil {
		d.log.Error("decisionMaker: Map failed",
			"title", releaseInfo.Title, "err", err)
		errSub := NewSubject()
		errSub.Release = report
		errSub.ReleaseSource = ReleaseSourceFor(pushedRelease, criteria)
		errSub.ParsedEpisodeInfo = parsedInfo
		return Decision{
			Subject: errSub,
			Rejections: []Rejection{{
				Reason:  ReasonError,
				Message: fmt.Sprintf("Unable to map release: %v", err),
				Kind:    RejectionPermanent,
			}},
		}
	}
	if sub == nil {
		// Defensive: should not happen — Map always returns a Subject.
		sub = NewSubject()
		sub.ParsedEpisodeInfo = parsedInfo
	}
	sub.Release = report
	sub.ReleaseSource = ReleaseSourceFor(pushedRelease, criteria)

	// C# `:97-108`: series-nil branch.
	if sub.Series == nil {
		matchingTvdbID, sceneErr := d.sceneAlias.FindTvdbID(ctx, parsedInfo.SeriesTitle, parsedInfo.ReleaseTitle, parsedInfo.SeasonNumber)
		if sceneErr != nil {
			d.log.Debug("decisionMaker: scene alias lookup failed",
				"title", releaseInfo.Title, "err", sceneErr)
		}
		if matchingTvdbID != nil {
			return Decision{
				Subject: sub,
				Rejections: []Rejection{{
					Reason:  ReasonMatchesAnotherSeries,
					Message: fmt.Sprintf("%s matches an alias for series with TVDB ID: %d", parsedInfo.SeriesTitle, *matchingTvdbID),
					Kind:    RejectionPermanent,
				}},
			}
		}
		return Decision{
			Subject: sub,
			Rejections: []Rejection{{
				Reason:  ReasonUnknownSeries,
				Message: "Unknown Series",
				Kind:    RejectionPermanent,
			}},
		}
	}

	// C# `:110-113`: episodes-empty branch.
	if len(sub.Episodes) == 0 {
		return Decision{
			Subject: sub,
			Rejections: []Rejection{{
				Reason:  ReasonUnknownEpisode,
				Message: "Unable to identify correct episode(s) using release name and scene mappings",
				Kind:    RejectionPermanent,
			}},
		}
	}

	// C# `:114-125`: full spec evaluation branch.
	d.aggregator.Augment(ctx, sub)

	// Custom-format calculation (C# `:118-121`) DEFERRED — see
	// DownloadDecisionMaker doc. Subject.CustomFormats stays empty;
	// Subject.CustomFormatScore stays 0; specs that read these
	// (CustomFormatAllowedByProfile etc.) treat zero-state as "no
	// custom formats matched" which is the same result as the C#
	// code would produce for an empty match.

	sub.DownloadAllowed = len(sub.Episodes) > 0

	info := ReleaseDecisionInformation{
		PushedRelease:  pushedRelease,
		SearchCriteria: searchCriteriaAsAny(criteria),
	}
	return d.decisions.Decide(ctx, sub, info)
}

// asReleaseInfo extracts a *parser.ReleaseInfo from the `any`
// element shape used by the indexersearch fan-out + RSS sweep. Both
// *parser.ReleaseInfo and *parser.TorrentInfo are acceptable — the
// latter embeds the former.
func asReleaseInfo(release any) (*parser.ReleaseInfo, bool) {
	if release == nil {
		return nil, false
	}
	if ri, ok := release.(*parser.ReleaseInfo); ok {
		return ri, true
	}
	if ti := parser.AsTorrentInfo(release); ti != nil {
		return &ti.ReleaseInfo, true
	}
	return nil, false
}

// searchCriteriaAsAny narrows a typed *SearchCriteria to the
// ReleaseDecisionInformation.SearchCriteria field's `any` shape.
// Returning nil-untyped-any (vs nil-typed-any) lets specs use a
// straightforward nil check; the typed-nil-into-interface pitfall is
// avoided.
func searchCriteriaAsAny(c *SearchCriteria) any {
	if c == nil {
		return nil
	}
	return c
}

// compile-time guards — fail the build at port time if the deps
// drift away from the contract.
var (
	_ ReleaseMapper    = noopReleaseMapper{}
	_ Aggregator       = noopAggregator{}
	_ SceneAliasFinder = noopSceneAliasFinder{}
)

// dummy reference so the language import (used by tests via the
// production binding's transitive ParseLanguages path) isn't flagged.
var _ = language.Unknown
