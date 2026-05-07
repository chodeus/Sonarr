package episodeimport

import (
	"errors"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/customformats"
)

// LocalEpisodeFileNameBuilder is the slice of `IBuildFileNames` the
// LocalEpisodeFormatCalculator reaches for at C#
// `LocalEpisodeCustomFormatCalculationService.cs:28,34`. Returns the
// rendered episode file name (no folder prefix) used as the
// CustomFormat input's filename component.
//
// The C# `_fileNameBuilder.BuildFileName` overload takes
// `(List<Episode>, Series, EpisodeFile)`. The Go-port
// `organizer.BuildFileName` takes `(eps, series, *episodefiles.File,
// extension, BuildOptions)` AND requires a constructed EpisodeFile
// — which is what `LocalEpisode.ToEpisodeFile()` produces, but
// `ToEpisodeFile` itself is deferred to the writer slice (see
// `episodeimport.go:23-27`). Production wiring for this slice will
// land alongside that writer slice; the narrow interface here lets
// the format calculator ship today with a stub.
//
// Errors propagate to the orchestrator's defensive arm — the C# code
// would NPE on a missing series; the Go port logs and leaves the
// LocalEpisode unchanged.
type LocalEpisodeFileNameBuilder interface {
	// BuildEpisodeFileName returns the rendered file name for the
	// supplied LocalEpisode. Production binding wraps
	// `LocalEpisode.ToEpisodeFile()` + `organizer.BuildFileName`.
	BuildEpisodeFileName(le *LocalEpisode) (string, error)
}

// LocalEpisodeFormatParser is the slice of
// `ICustomFormatCalculationService.ParseCustomFormat(LocalEpisode, fileName)`
// the calculator reaches for at C# `:29,35,39`.
//
// Returns the matched CustomFormats in PERSISTED form so they can be
// assigned to [LocalEpisode.CustomFormats] directly. The typed-form
// `[]*customformats.Format` is internal to the format engine — the
// LocalEpisode field is `[]customformats.CustomFormat`, the row form.
//
// The fileName arg is the on-disk-name component the parser feeds
// into CustomFormatInput.Filename — the renamed-form when called for
// the primary CustomFormats list, or the raw basename when called
// for the OriginalFileNameCustomFormats list (mirrors C# `:39`
// `Path.GetFileName(localEpisode.Path)`).
type LocalEpisodeFormatParser interface {
	// ParseCustomFormat returns the matching CustomFormats for the
	// supplied LocalEpisode + fileName. Empty slice on no-match.
	// Implementations swallow per-spec errors (returning empty)
	// rather than propagating; the C# `_formatCalculator.ParseCustomFormat`
	// has the same swallow-and-return-empty arm.
	ParseCustomFormat(le *LocalEpisode, fileName string) []customformats.CustomFormat
}

// LocalEpisodeFormatScorer is the slice of
// `qualityProfile.CalculateCustomFormatScore(formats)` the calculator
// reaches for at C# `:37,40`. Returns the per-series profile-driven
// score for a list of matched CustomFormats.
//
// The C# code uses
// `localEpisode.Series.QualityProfile?.Value.CalculateCustomFormatScore(...) ?? 0`
// — the lazy-loaded QualityProfile may be null and falls through to
// 0. The Go port narrows this surface to a single method that the
// production binding implements via `qualityprofiles.CalculateCustomFormatScore`
// (already used by [UpgradeSpecification] in specs.go).
//
// Implementations are expected to handle missing/unloaded profiles by
// returning 0 themselves — matches C# `?? 0`.
type LocalEpisodeFormatScorer interface {
	// CalculateCustomFormatScore returns the QualityProfile-driven
	// score for the supplied series + matched formats. Missing
	// profile / unrecognised series returns 0 per C# semantics.
	CalculateCustomFormatScore(seriesID int, formats []customformats.CustomFormat) int
}

// LocalEpisodeFormatCalculator ports
// `MediaFiles/EpisodeImport/LocalEpisodeCustomFormatCalculationService.cs:15-42`.
// Wraps the per-LocalEpisode CustomFormat resolution flow:
//
//  1. Build the renamed file-name string
//     (`_fileNameBuilder.BuildFileName(...)`).
//  2. Run the format parser against (LocalEpisode + renamed name) —
//     these are the "current" CustomFormats the orchestrator stores
//     on `LocalEpisode.CustomFormats` for spec evaluation.
//  3. Score the current CustomFormats against the series'
//     QualityProfile.
//  4. Run the format parser AGAIN against (LocalEpisode + raw basename)
//     — these are the "original" CustomFormats stored on
//     `LocalEpisode.OriginalFileNameCustomFormats` and used by the
//     NotCustomFormatUpgradeAfterRename spec to detect post-rename
//     score regressions.
//  5. Score the original CustomFormats against the same QualityProfile.
//
// Step 4 is the C# `:39-40` quirk where the same parser runs twice
// with different `fileName` arguments — once for the renamed form
// and once for the on-wire basename. The two outputs may differ
// when CustomFormat specs include filename-pattern matchers (most
// of them do).
//
// Goroutine-safe post-construction (deps must themselves be
// goroutine-safe; the production deps are).
//
// Satisfies the [EpisodeCustomFormatCalculator] interface from
// import_decision_maker.go via [UpdateEpisodeCustomFormats]. Compile-
// time guard at the bottom of the file.
type LocalEpisodeFormatCalculator struct {
	builder LocalEpisodeFileNameBuilder
	parser  LocalEpisodeFormatParser
	scorer  LocalEpisodeFormatScorer
	log     *slog.Logger
}

// NewLocalEpisodeFormatCalculator binds the three narrow deps. All
// three are required — there's no graceful degradation when any is
// nil because the calculator's whole purpose is to orchestrate them.
//
// nil log defaults to [slog.Default]. Mirrors the constructor-
// validation pattern in [NewAggregationService] (slice 3.60) and
// [NewImportDecisionMaker] (slice 3.62).
func NewLocalEpisodeFormatCalculator(
	builder LocalEpisodeFileNameBuilder,
	parser LocalEpisodeFormatParser,
	scorer LocalEpisodeFormatScorer,
	log *slog.Logger,
) (*LocalEpisodeFormatCalculator, error) {
	if builder == nil {
		return nil, errors.New("episodeimport: NewLocalEpisodeFormatCalculator: nil LocalEpisodeFileNameBuilder")
	}
	if parser == nil {
		return nil, errors.New("episodeimport: NewLocalEpisodeFormatCalculator: nil LocalEpisodeFormatParser")
	}
	if scorer == nil {
		return nil, errors.New("episodeimport: NewLocalEpisodeFormatCalculator: nil LocalEpisodeFormatScorer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &LocalEpisodeFormatCalculator{
		builder: builder,
		parser:  parser,
		scorer:  scorer,
		log:     log,
	}, nil
}

// ParseEpisodeCustomFormats ports
// `LocalEpisodeCustomFormatCalculationService.cs:26-30` — the public
// "build the renamed filename, parse" path. Returns the matched
// CustomFormats without mutating the LocalEpisode.
//
// A FileNameBuilder error is logged + an empty slice returned (the
// C# version would NPE; the Go port chooses to swallow because the
// caller's contract is "best-effort enrichment, no blocking").
//
// nil le returns nil — defensive against the orchestrator's
// upstream errors.
func (c *LocalEpisodeFormatCalculator) ParseEpisodeCustomFormats(le *LocalEpisode) []customformats.CustomFormat {
	if c == nil || le == nil {
		return nil
	}
	name, err := c.builder.BuildEpisodeFileName(le)
	if err != nil {
		c.log.Warn("episodeimport: BuildEpisodeFileName failed; returning empty CustomFormats",
			"path", le.Path, "err", err)
		return nil
	}
	return c.parser.ParseCustomFormat(le, name)
}

// UpdateEpisodeCustomFormats ports
// `LocalEpisodeCustomFormatCalculationService.cs:32-41` — the in-
// place mutation path the orchestrator calls after a successful
// aggregation. Sets four LocalEpisode fields:
//
//   - CustomFormats — matched formats against the rendered filename.
//   - FileNameUsedForCustomFormatCalculation — the rendered filename
//     used as the CustomFormatInput.Filename for `CustomFormats`.
//   - OriginalFileNameCustomFormats — matched formats against the
//     raw on-wire basename (`filepath.Base(le.Path)`).
//   - CustomFormatScore + OriginalFileNameCustomFormatScore — the
//     QualityProfile-driven scores for each format list.
//
// nil le is a no-op (defensive against upstream nil-LocalEpisode).
//
// FileNameBuilder errors short-circuit the rename-side update —
// CustomFormats stays nil and FileNameUsedForCustomFormatCalculation
// stays "" — but the OriginalFileName side STILL runs because
// filepath.Base never errors. This matches C# `:34` raising directly
// out of BuildFileName, but the Go port chooses to keep the
// best-effort half on the original-basename pass since that doesn't
// depend on the rename engine.
func (c *LocalEpisodeFormatCalculator) UpdateEpisodeCustomFormats(le *LocalEpisode) {
	if c == nil || le == nil {
		return
	}
	if le.Series == nil {
		// Both halves of the C# code at `:37,40` reach for
		// `localEpisode.Series.QualityProfile.Value.CalculateCustomFormatScore`.
		// With nil Series we can't even compute the original-name
		// score, so skip the whole call rather than half-mutate the
		// LocalEpisode.
		c.log.Warn("episodeimport: UpdateEpisodeCustomFormats called with nil Series",
			"path", le.Path)
		return
	}
	if c.tryUpdateRenamedHalf(le) {
		// C# `:35` assigns le.CustomFormats; `:37` then computes the
		// score from le.CustomFormats. We pass the slice directly to
		// avoid re-reading the field.
		le.CustomFormatScore = c.scorer.CalculateCustomFormatScore(le.Series.ID, le.CustomFormats)
	} else {
		// Failure path: ensure we don't carry stale formats from a
		// previous run. C# implicitly does this via `localEpisode.CustomFormats =`.
		le.CustomFormats = nil
		le.CustomFormatScore = 0
		le.FileNameUsedForCustomFormatCalculation = ""
	}

	// C# `:39` — the original-name pass ALWAYS runs (no nil check).
	// `Path.GetFileName(le.Path)` ports as `filepath.Base(le.Path)`.
	originalName := filepath.Base(le.Path)
	le.OriginalFileNameCustomFormats = c.parser.ParseCustomFormat(le, originalName)
	le.OriginalFileNameCustomFormatScore = c.scorer.CalculateCustomFormatScore(le.Series.ID, le.OriginalFileNameCustomFormats)
}

// tryUpdateRenamedHalf runs the BuildFileName + parser pipeline and
// writes le.CustomFormats + FileNameUsedForCustomFormatCalculation.
// Returns true on success. Pulled out so UpdateEpisodeCustomFormats'
// happy-path arm reads in declaration order.
func (c *LocalEpisodeFormatCalculator) tryUpdateRenamedHalf(le *LocalEpisode) bool {
	// le.Series is nil-checked by the caller (UpdateEpisodeCustomFormats);
	// if you call this directly you're on your own.
	name, err := c.builder.BuildEpisodeFileName(le)
	if err != nil {
		c.log.Warn("episodeimport: BuildEpisodeFileName failed; OriginalFileNameCustomFormats will still run",
			"path", le.Path, "err", err)
		return false
	}
	le.CustomFormats = c.parser.ParseCustomFormat(le, name)
	le.FileNameUsedForCustomFormatCalculation = name
	return true
}

// Compile-time interface-satisfaction guard. A refactor that breaks
// the contract (LocalEpisodeFormatCalculator drops UpdateEpisodeCustomFormats,
// EpisodeCustomFormatCalculator changes its signature) fails at build
// time rather than at the wiring callsite.
var _ EpisodeCustomFormatCalculator = (*LocalEpisodeFormatCalculator)(nil)
