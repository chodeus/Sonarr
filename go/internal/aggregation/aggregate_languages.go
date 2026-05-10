package aggregation

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// IndexerMultiLanguagesLookup is the narrow interface
// [AggregateLanguages] reaches for to consult per-indexer
// `MultiLanguages` settings. Mirrors the slice of `IIndexerFactory`
// consumed at C# `AggregateLanguages.cs:81-92`:
//
//   - `_indexerFactory.Find(IndexerId)` → IndexerDefinition?
//   - `_indexerFactory.FindByName(Indexer)` → IndexerDefinition?
//   - `indexer.Settings as IIndexerSettings → MultiLanguages` (int[])
//
// The Go port collapses those into two getters that return the
// resolved `MultiLanguages` list directly. The boolean second return
// distinguishes "indexer not found" (false) from "indexer found but
// MultiLanguages empty" (true with empty slice) so the caller can
// short-circuit without touching the slice.
//
// Production binding wires through an adapter over indexers.Repo +
// per-provider settings parser; see sub-arc 11c's wiring slice. Tests
// pass a stub.
//
// A nil IndexerMultiLanguagesLookup on [AggregateLanguages] is
// permitted: [noopIndexerLookup] is bound by [NewAggregateLanguages]
// and returns (nil, false) from both methods. That matches the C#
// "no IndexerFactory at this Sonarr build" scenario which never
// occurs in production but is convenient for unit tests of the other
// branches.
type IndexerMultiLanguagesLookup interface {
	// FindByID returns the MultiLanguages list for the indexer row
	// whose ID matches. Mirrors C# `Find(IndexerId)` at `:85`.
	// (nil, false) means "indexer row not found"; (nil, true) means
	// "indexer found, MultiLanguages empty"; (lang-list, true) means
	// "indexer found with explicit MultiLanguages".
	FindByID(ctx context.Context, indexerID int) ([]language.Language, bool, error)

	// FindByName returns the MultiLanguages list for the indexer row
	// whose Name (case-sensitive — matches the C# `FindByName` exact
	// match) equals indexerName. Same return semantics as FindByID.
	FindByName(ctx context.Context, indexerName string) ([]language.Language, bool, error)
}

// noopIndexerLookup returns (nil, false, nil) from both methods. Used
// when [NewAggregateLanguages] is called with a nil lookup.
type noopIndexerLookup struct{}

// FindByID always returns (nil, false, nil) — the noop binding.
func (noopIndexerLookup) FindByID(_ context.Context, _ int) ([]language.Language, bool, error) {
	return nil, false, nil
}

// FindByName always returns (nil, false, nil) — the noop binding.
func (noopIndexerLookup) FindByName(_ context.Context, _ string) ([]language.Language, bool, error) {
	return nil, false, nil
}

// SeriesOriginalLanguageLookup is the slice of [series.Service] used
// to resolve the series's OriginalLanguage int ID to a
// [language.Language]. The C# tree carries the resolved Language
// directly on `Series.OriginalLanguage` (typed `Language`); the Go
// port stores the int ID (`series.Series.OriginalLanguage int`) per
// the v5 schema field. AggregateLanguages reads this ID + delegates
// to [language.FindByID] for the typed value — no narrow interface
// is needed.
//
// This struct exists only as a doc-comment anchor; the lookup is the
// free function [language.FindByID] (`internal/language/language.go:131`).
// Declared here to mirror the "every dep gets a narrow interface"
// convention used by sibling augmenters when concrete deps are added
// in follow-up slices.
type SeriesOriginalLanguageLookup struct{}

// AggregateLanguages ports
// `src/NzbDrone.Core/Download/Aggregation/Aggregators/AggregateLanguages.cs:1-135`.
// Refines Subject.Languages by combining:
//
//   - the parser's per-release language detection (parsedEpisodeInfo.Languages),
//   - the indexer's MultiLanguages setting (when the release title
//     contains "MULTi" markers per [parser.HasMultipleLanguages]),
//   - the matched series's OriginalLanguage (for the
//     [language.Original] substitution + the "no languages parsed"
//     fallback).
//
// Construction: [NewAggregateLanguages] binds an
// [IndexerMultiLanguagesLookup] + logger. Both may be nil — nil
// lookup → [noopIndexerLookup] (skips the MultiLanguages indexer
// path entirely); nil logger → [slog.Default].
//
// Algorithm — line-aligned with C# `AggregateLanguages.cs:25-133`:
//
//  1. Pull (parsedEpisodeInfo, releaseInfo, series) off the Subject.
//     Compute releaseTokens = parsedEpisodeInfo.ReleaseTokens or
//     ReleaseTitle (C# `:31`); normalize via
//     [parser.NormalizeEpisodeTitle] (C# `:32`).
//
//  2. Series-nil short-circuit (C# `:35-42`): when the parsing
//     service couldn't resolve a Series, fall back to
//     releaseInfo.Languages (when non-empty) or the parsed
//     Languages — no indexer / series-original-language step
//     applies.
//
//  3. Indexer-supplied languages take priority (C# `:44-50`):
//     non-empty releaseInfo.Languages wins over the parser's
//     guesses. Skip the episode-title language-exclusion step.
//
//  4. Else (C# `:51-77`): per-episode, parse the episode title for
//     languages. If the title parses to anything non-Unknown AND
//     the episode title appears in the normalized release tokens,
//     mark those languages for removal AND strip the episode title
//     from the working release-tokens copy. Then reconcile:
//     re-parse the trimmed releaseTokens, and only remove languages
//     that ARE NOT re-parsed from the trimmed tokens (the
//     "Greek.Greek" case — Greek appears twice, so removing one
//     occurrence still leaves Greek detected).
//
//  5. Indexer MultiLanguages enrichment (C# `:79-105`): when
//     releaseInfo.Title contains MULTi markers AND the indexer's
//     settings have a non-empty MultiLanguages list, apply the
//     list. If the working languages are empty or [Unknown], the
//     MultiLanguages list REPLACES them. Otherwise it UNIONS with
//     them (no duplicates).
//
//  6. OriginalLanguage fallback (C# `:108-112`): when the working
//     languages are still empty or [Unknown], fall back to the
//     series's OriginalLanguage (resolved via
//     [language.FindByID]). If the ID doesn't resolve, leave as
//     [Unknown].
//
//  7. Original substitution (C# `:114-126`): when the working
//     languages contain [language.Original], replace it with the
//     series's OriginalLanguage. If OriginalLanguage was already
//     present, replace with [language.Unknown] instead so the
//     length is preserved.
//
//  8. Assign the resolved list to Subject.Languages.
//
// Returns no error in normal operation. ctx is plumbed through the
// IndexerMultiLanguagesLookup so the production binding's DB call
// can honour cancellation. A nil Subject returns
// [ErrNilSubject] — defensive guard so the orchestrator can log
// rather than panic.
type AggregateLanguages struct {
	indexers IndexerMultiLanguagesLookup
	log      *slog.Logger
}

// ErrNilSubject is returned by [AggregateLanguages.Aggregate] when
// Subject is nil. The orchestrator's per-augmenter error log surfaces
// the misuse without crashing the decision pipeline.
var ErrNilSubject = errors.New("aggregation: AggregateLanguages: nil subject")

// NewAggregateLanguages binds the dependencies. Both deps may be nil:
//
//   - indexers == nil → MultiLanguages indexer path is a no-op.
//     Algorithm step 5 still runs but always sees "indexer not
//     found", so the union/replace branch never fires.
//   - log == nil → slog.Default.
func NewAggregateLanguages(indexers IndexerMultiLanguagesLookup, log *slog.Logger) *AggregateLanguages {
	if indexers == nil {
		indexers = noopIndexerLookup{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &AggregateLanguages{indexers: indexers, log: log}
}

// String returns the augmenter's identifier for log lines. Mirrors
// the C# type-name surfaced via `_logger.Warn(ex, ex.Message)` in the
// orchestrator's per-augmenter catch block at
// `RemoteEpisodeAggregationService.cs:41`.
func (a *AggregateLanguages) String() string { return "AggregateLanguages" }

// Aggregate implements [Augmenter]. See the type's doc-comment for
// the full algorithm; this method body is a straight-line port of C#
// `AggregateLanguages.Aggregate` at `:25-133`.
func (a *AggregateLanguages) Aggregate(ctx context.Context, sub *decisionengine.Subject) error {
	if sub == nil {
		return ErrNilSubject
	}

	parsedInfo := sub.ParsedEpisodeInfo
	if parsedInfo == nil {
		// C# `:27` would NPE here; the Go port treats it as a no-op
		// because Subject construction without parsedInfo is wiring
		// misuse, not a runtime case. Log and continue.
		a.log.Debug("aggregateLanguages: nil ParsedEpisodeInfo, skipping")
		return nil
	}

	releaseInfo, _ := releaseInfoFromSubject(sub)
	languages := copyLanguages(parsedInfo.Languages)
	ser := sub.Series

	releaseTokens := parsedInfo.ReleaseTokens
	if releaseTokens == "" {
		releaseTokens = parsedInfo.ReleaseTitle
	}
	normalizedReleaseTokens := parser.NormalizeEpisodeTitle(releaseTokens)

	// Step 2: series-nil short-circuit. C# `:35-42`.
	if ser == nil {
		if releaseInfo != nil && len(releaseInfo.Languages) > 0 {
			sub.Languages = copyLanguages(releaseInfo.Languages)
		} else {
			sub.Languages = languages
		}
		a.log.Debug("aggregateLanguages: no series resolved, using parsed/release languages",
			"languages", langNames(sub.Languages))
		return nil
	}

	// Step 3: release-supplied languages take priority. C# `:44-50`.
	if releaseInfo != nil && len(releaseInfo.Languages) > 0 {
		languages = copyLanguages(releaseInfo.Languages)
	} else {
		// Step 4: episode-title language exclusion. C# `:51-77`.
		var languagesToRemove []language.Language
		for _, ep := range sub.Episodes {
			episodeTitleLanguages := parser.ParseLanguages(ep.Title)
			if !containsLanguage(episodeTitleLanguages, language.Unknown) {
				normalizedEpisodeTitle := parser.NormalizeEpisodeTitle(ep.Title)
				episodeTitleIndex := strings.Index(
					strings.ToLower(normalizedReleaseTokens),
					strings.ToLower(normalizedEpisodeTitle),
				)
				if episodeTitleIndex >= 0 {
					// Cut the episode title out of the working
					// release-tokens. C# `:66`. We splice on the
					// NORMALIZED tokens since that's what we used for
					// the index lookup — keeps the offsets aligned.
					normalizedReleaseTokens = normalizedReleaseTokens[:episodeTitleIndex] +
						normalizedReleaseTokens[episodeTitleIndex+len(normalizedEpisodeTitle):]
					languagesToRemove = append(languagesToRemove, episodeTitleLanguages...)
				}
			}
		}

		// Reconcile: only remove languages NOT detected in the
		// trimmed tokens. C# `:73`.
		trimmedReleaseLanguages := parser.ParseLanguages(normalizedReleaseTokens)
		languagesToRemove = exceptLanguages(languagesToRemove, trimmedReleaseLanguages)

		// Strip removed languages from the working list. C# `:76`.
		languages = exceptLanguages(languages, languagesToRemove)
	}

	// Step 5: indexer MultiLanguages enrichment. C# `:79-105`.
	if releaseInfo != nil && strings.TrimSpace(releaseInfo.Title) != "" {
		multi, err := a.lookupMultiLanguages(ctx, releaseInfo)
		if err != nil {
			a.log.Debug("aggregateLanguages: indexer MultiLanguages lookup failed",
				"err", err, "indexerId", releaseInfo.IndexerID, "indexerName", releaseInfo.Indexer)
		}
		if len(multi) > 0 && parser.HasMultipleLanguages(releaseInfo.Title) {
			if len(languages) == 0 || (len(languages) == 1 && languages[0] == language.Unknown) {
				languages = copyLanguages(multi)
			} else {
				languages = unionLanguages(languages, multi)
			}
		}
	}

	// Step 6: OriginalLanguage fallback. C# `:108-112`.
	originalLanguage, _ := originalLanguageFor(ser)
	if len(languages) == 0 || (len(languages) == 1 && languages[0] == language.Unknown) {
		languages = []language.Language{originalLanguage}
		a.log.Debug("aggregateLanguages: fallback to series original language",
			"originalLanguage", originalLanguage.Name)
	}

	// Step 7: language.Original substitution. C# `:114-126`.
	if containsLanguage(languages, language.Original) {
		languages = removeLanguage(languages, language.Original)
		if !containsLanguage(languages, originalLanguage) {
			languages = append(languages, originalLanguage)
		} else {
			languages = append(languages, language.Unknown)
		}
	}

	sub.Languages = languages
	a.log.Debug("aggregateLanguages: resolved", "languages", langNames(languages))
	return nil
}

// lookupMultiLanguages encapsulates the C# `:81-91` indexer-lookup
// fan-out: try Find(IndexerId) first; if that misses AND
// releaseInfo.Indexer is set, try FindByName(Indexer). Returns nil
// when neither hits.
func (a *AggregateLanguages) lookupMultiLanguages(ctx context.Context, releaseInfo *parser.ReleaseInfo) ([]language.Language, error) {
	if releaseInfo.IndexerID > 0 {
		multi, found, err := a.indexers.FindByID(ctx, releaseInfo.IndexerID)
		if err != nil {
			return nil, err
		}
		if found {
			return multi, nil
		}
	}
	if strings.TrimSpace(releaseInfo.Indexer) != "" {
		multi, found, err := a.indexers.FindByName(ctx, releaseInfo.Indexer)
		if err != nil {
			return nil, err
		}
		if found {
			return multi, nil
		}
	}
	return nil, nil
}

// releaseInfoFromSubject pulls the embedded *parser.ReleaseInfo from
// Subject.Release. Subject.Release is `any` and concretely is either
// *parser.ReleaseInfo or *parser.TorrentInfo; the latter embeds the
// former by value so &t.ReleaseInfo recovers the pointer.
//
// Returns (nil, false) when Subject.Release is nil or an unknown
// concrete type — matches C# `:28` where releaseInfo defaults to null.
func releaseInfoFromSubject(sub *decisionengine.Subject) (*parser.ReleaseInfo, bool) {
	if sub.Release == nil {
		return nil, false
	}
	if ri, ok := sub.Release.(*parser.ReleaseInfo); ok {
		return ri, true
	}
	if ti := parser.AsTorrentInfo(sub.Release); ti != nil {
		return &ti.ReleaseInfo, true
	}
	return nil, false
}

// originalLanguageFor resolves series.OriginalLanguage (int ID) to
// the typed [language.Language]. Unknown IDs resolve to Unknown
// (matches the C# `FindById(0) → Unknown` invariant at
// `Language.cs:196`).
func originalLanguageFor(ser *series.Series) (language.Language, bool) {
	if ser == nil {
		return language.Unknown, false
	}
	return language.FindByID(ser.OriginalLanguage)
}

// copyLanguages returns a fresh slice with the same elements. Used
// to avoid mutating caller-owned slices when we'll later append or
// remove elements.
func copyLanguages(in []language.Language) []language.Language {
	if len(in) == 0 {
		return []language.Language{}
	}
	out := make([]language.Language, len(in))
	copy(out, in)
	return out
}

// containsLanguage returns true iff l appears in xs (by struct
// equality — Language is `{ID int, Name string}` so this matches the
// C# `List<Language>.Contains` semantics).
func containsLanguage(xs []language.Language, l language.Language) bool {
	for _, x := range xs {
		if x == l {
			return true
		}
	}
	return false
}

// exceptLanguages returns xs with every member of ys removed. Order
// preserved on xs; duplicates in xs are all removed. Mirrors the C#
// `Enumerable.Except` extension at `:73,76`.
func exceptLanguages(xs, ys []language.Language) []language.Language {
	if len(ys) == 0 {
		return copyLanguages(xs)
	}
	remove := make(map[language.Language]struct{}, len(ys))
	for _, y := range ys {
		remove[y] = struct{}{}
	}
	out := make([]language.Language, 0, len(xs))
	for _, x := range xs {
		if _, drop := remove[x]; !drop {
			out = append(out, x)
		}
	}
	return out
}

// removeLanguage returns xs with all occurrences of l removed. Order
// preserved. Mirrors C# `List<T>.Remove(item)` (which removes the
// first occurrence) — but at C# `:116` only one Language.Original is
// ever in the list, so the all-occurrences variant here is
// equivalent.
func removeLanguage(xs []language.Language, l language.Language) []language.Language {
	out := make([]language.Language, 0, len(xs))
	for _, x := range xs {
		if x != l {
			out = append(out, x)
		}
	}
	return out
}

// unionLanguages returns xs + every element of ys that isn't already
// in xs. Order: xs first (preserved), then new elements from ys in
// input order. Mirrors C# `:102` `languages.AddRange(...Except(languages))`.
func unionLanguages(xs, ys []language.Language) []language.Language {
	present := make(map[language.Language]struct{}, len(xs))
	for _, x := range xs {
		present[x] = struct{}{}
	}
	out := copyLanguages(xs)
	for _, y := range ys {
		if _, ok := present[y]; !ok {
			present[y] = struct{}{}
			out = append(out, y)
		}
	}
	return out
}

// langNames is a slog-friendly formatter for Language lists. Pulled
// out so the test file doesn't need to import strings just to assert
// log output.
func langNames(xs []language.Language) string {
	if len(xs) == 0 {
		return ""
	}
	names := make([]string, len(xs))
	for i, l := range xs {
		names[i] = l.Name
	}
	return strings.Join(names, ", ")
}
